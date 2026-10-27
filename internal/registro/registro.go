// Package registro keeps the image registry from filling up. Every build
// pushes a new image and replaces its build cache, and the registry keeps
// them all: when its disk filled, every push failed. Each night the
// Cleaner removes what nothing needs any more (old build tags, and the
// manifests no tag points to, mostly old caches), then runs the registry's
// garbage collector to free their layers. It keeps every image a workload
// in the cluster or a recent release uses, the newest tags of each
// repository, and any tag that is not a build tag.
//
// It works on the registry's own storage, through its pod (the registry
// has no API to list manifests or collect garbage), so it needs to exec
// into that pod; it only runs when no build is pushing.
package registro

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

// root is where the registry (distribution, filesystem driver) keeps its
// repositories inside its pod.
const root = "/var/lib/registry/docker/registry/v2"

// FullAt is the share of the registry's disk that, still used after a
// cleanup, is reported as a problem.
const FullAt = 0.8

// Releases gives the images of each app's last releases.
type Releases interface {
	RecentReleaseImages(ctx context.Context, perApp int) ([]string, error)
}

// Runs tells whether a build may be pushing right now.
type Runs interface {
	BusyRuns(ctx context.Context) (int, error)
}

// Cleaner cleans the registry once a night.
type Cleaner struct {
	Kube      kubernetes.Interface
	Rest      *rest.Config
	Host      string // the registry as images name it, e.g. registry.example.lan:5000
	Namespace string // the registry's pod: its namespace and label selector
	Selector  string
	Config    string // the registry's config file inside its pod
	At        string // "03:30": when, in TZ
	TZ        *time.Location
	Keep      int // newest build tags kept per repository
	Releases  Releases
	Runs      Runs
	Log       *slog.Logger
}

// Result is what one cleanup did.
type Result struct {
	Tags, Revisions, Repaired int
	Before, After             float64 // share of the disk used
}

// Run cleans every night at c.At until ctx ends. A night with builds
// running waits for them, for up to four hours.
func (c *Cleaner) Run(ctx context.Context) {
	for {
		wait := time.Until(c.next(time.Now()))
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		deadline := time.Now().Add(4 * time.Hour)
		for {
			busy, err := c.Runs.BusyRuns(ctx)
			if err == nil && busy == 0 {
				break
			}
			if time.Now().After(deadline) {
				c.Log.Warn("image registry cleanup skipped: builds kept running all night")
				break
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Minute):
			}
		}
		r, err := c.Clean(ctx)
		switch {
		case err != nil:
			c.Log.Warn("image registry cleanup failed", "err", err)
		case r.After >= FullAt:
			c.Log.Warn("image registry nearly full after its nightly cleanup", "used", fmt.Sprintf("%.0f%%", r.After*100))
		default:
			c.Log.Info("image registry cleaned", "tags", r.Tags, "manifests", r.Revisions, "repaired", r.Repaired,
				"before", fmt.Sprintf("%.0f%%", r.Before*100), "after", fmt.Sprintf("%.0f%%", r.After*100))
		}
	}
}

// next is the next time of day c.At, in c.TZ, after now (03:30 when unset
// or wrong).
func (c *Cleaner) next(now time.Time) time.Time {
	if c.TZ != nil {
		now = now.In(c.TZ)
	}
	at, err := time.Parse("15:04", c.At)
	if err != nil {
		at, _ = time.Parse("15:04", "03:30")
	}
	t := time.Date(now.Year(), now.Month(), now.Day(), at.Hour(), at.Minute(), 0, 0, now.Location())
	if !t.After(now) {
		t = t.AddDate(0, 0, 1)
	}
	return t
}

// Clean cleans the registry once: reads it, plans, repairs and removes,
// and collects the garbage.
func (c *Cleaner) Clean(ctx context.Context) (Result, error) {
	var r Result
	use, err := c.inUse(ctx)
	if err != nil {
		return r, fmt.Errorf("the images in use: %w", err)
	}
	pod, err := c.pod(ctx)
	if err != nil {
		return r, err
	}
	out, err := c.exec(ctx, pod, listing, "")
	if err != nil {
		return r, fmt.Errorf("reading the registry: %w", err)
	}
	l := Parse(out)
	if len(l.Tags) == 0 {
		return r, fmt.Errorf("the registry lists no tags; nothing done")
	}
	r.Before = share(l)
	p := Decide(l, use, c.Keep)
	sh, err := Script(root+"/repositories", p, l.Broken)
	if err != nil {
		return r, err
	}
	for _, ds := range l.Broken {
		r.Repaired += len(ds)
	}
	r.Tags, r.Revisions = len(p.Tags), len(p.Revisions)
	if _, err := c.exec(ctx, pod, "sh", sh); err != nil {
		return r, fmt.Errorf("removing old images: %w", err)
	}
	// Collect only while no build is pushing: a layer uploaded now, before
	// its manifest, would look like garbage.
	if busy, err := c.Runs.BusyRuns(ctx); err != nil || busy > 0 {
		return r, fmt.Errorf("a build started; the space is freed tomorrow night")
	}
	gc := fmt.Sprintf("registry garbage-collect %s > /tmp/garbage-collect.log 2>&1 || { tail -3 /tmp/garbage-collect.log; exit 1; }; df -P /var/lib/registry | tail -1 | awk '{print \"D\", $3*1024, $4*1024}'", c.Config)
	out, err = c.exec(ctx, pod, gc, "")
	if err != nil {
		return r, fmt.Errorf("collecting garbage: %w: %s", err, strings.TrimSpace(out))
	}
	r.After = share(Parse(out))
	return r, nil
}

// share is the part of the disk in use.
func share(l Listing) float64 {
	if l.Used+l.Free == 0 {
		return 0
	}
	return float64(l.Used) / float64(l.Used+l.Free)
}

// inUse gathers every image the cluster's workloads name (running or not:
// a deployment scaled to zero, a cron job, an old replica set kept for a
// rollback) and those of each app's last releases.
func (c *Cleaner) inUse(ctx context.Context) (InUse, error) {
	use := InUse{Tags: map[string]map[string]bool{}, Digests: map[string]bool{}}
	add := func(image string) {
		repo, tag, digest, ok := Ref(image, c.Host)
		if !ok {
			return
		}
		if tag != "" {
			if use.Tags[repo] == nil {
				use.Tags[repo] = map[string]bool{}
			}
			use.Tags[repo][tag] = true
		}
		if digest != "" {
			use.Digests[digest] = true
		}
	}
	spec := func(s corev1.PodSpec) {
		for _, ct := range append(s.InitContainers, s.Containers...) {
			add(ct.Image)
		}
	}
	all := metav1.ListOptions{}
	pods, err := c.Kube.CoreV1().Pods("").List(ctx, all)
	if err != nil {
		return use, err
	}
	for _, p := range pods.Items {
		spec(p.Spec)
		for _, st := range append(p.Status.InitContainerStatuses, p.Status.ContainerStatuses...) {
			add(st.Image)
		}
	}
	apps := c.Kube.AppsV1()
	if l, err := apps.Deployments("").List(ctx, all); err != nil {
		return use, err
	} else {
		for _, x := range l.Items {
			spec(x.Spec.Template.Spec)
		}
	}
	if l, err := apps.StatefulSets("").List(ctx, all); err != nil {
		return use, err
	} else {
		for _, x := range l.Items {
			spec(x.Spec.Template.Spec)
		}
	}
	if l, err := apps.DaemonSets("").List(ctx, all); err != nil {
		return use, err
	} else {
		for _, x := range l.Items {
			spec(x.Spec.Template.Spec)
		}
	}
	if l, err := apps.ReplicaSets("").List(ctx, all); err != nil {
		return use, err
	} else {
		for _, x := range l.Items {
			spec(x.Spec.Template.Spec)
		}
	}
	if l, err := c.Kube.BatchV1().CronJobs("").List(ctx, all); err != nil {
		return use, err
	} else {
		for _, x := range l.Items {
			spec(x.Spec.JobTemplate.Spec.Template.Spec)
		}
	}
	if l, err := c.Kube.BatchV1().Jobs("").List(ctx, all); err != nil {
		return use, err
	} else {
		for _, x := range l.Items {
			spec(x.Spec.Template.Spec)
		}
	}
	imgs, err := c.Releases.RecentReleaseImages(ctx, 10)
	if err != nil {
		return use, err
	}
	for _, i := range imgs {
		add(i)
	}
	return use, nil
}

// pod finds the registry's running pod.
func (c *Cleaner) pod(ctx context.Context) (string, error) {
	l, err := c.Kube.CoreV1().Pods(c.Namespace).List(ctx, metav1.ListOptions{LabelSelector: c.Selector})
	if err != nil {
		return "", fmt.Errorf("finding the registry's pod: %w", err)
	}
	for _, p := range l.Items {
		if p.Status.Phase == corev1.PodRunning && p.DeletionTimestamp == nil {
			return p.Name, nil
		}
	}
	return "", fmt.Errorf("no running pod matches %s in %s", c.Selector, c.Namespace)
}

// exec runs a shell command in the registry's pod (with stdin, when given)
// and returns what it printed.
func (c *Cleaner) exec(ctx context.Context, pod, cmd, stdin string) (string, error) {
	opts := &corev1.PodExecOptions{Command: []string{"sh", "-c", cmd}, Stdout: true, Stderr: true, Stdin: stdin != ""}
	req := c.Kube.CoreV1().RESTClient().Post().Resource("pods").Namespace(c.Namespace).Name(pod).
		SubResource("exec").VersionedParams(opts, scheme.ParameterCodec)
	ex, err := remotecommand.NewSPDYExecutor(c.Rest, "POST", req.URL())
	if err != nil {
		return "", err
	}
	var out, errOut bytes.Buffer
	so := remotecommand.StreamOptions{Stdout: &out, Stderr: &errOut}
	if stdin != "" {
		so.Stdin = strings.NewReader(stdin)
	}
	if err := ex.StreamWithContext(ctx, so); err != nil {
		return out.String(), fmt.Errorf("%w: %s", err, strings.TrimSpace(errOut.String()))
	}
	return out.String(), nil
}

// listing prints, from the registry's storage, every tag ("T repo tag
// digest time"), every manifest revision ("R repo hex", or "E repo hex"
// when its link is empty), what each small index lists ("C hex
// digests…"), and the disk's use ("D used free", in bytes; df -P, so a
// long device name doesn't wrap the line).
const listing = `cd ` + root + `/repositories || exit 1
find . -path '*/_manifests/tags/*/current/link' | while read -r f; do
  r=${f#./}; r=${r%%/_manifests/*}; t=${f#*/_manifests/tags/}; t=${t%%/*}
  echo "T $r $t $(cat "$f") $(stat -c %Y "$f")"
done
find . -path '*/_manifests/revisions/sha256/*/link' | while read -r f; do
  r=${f#./}; r=${r%%/_manifests/*}; h=${f%/link}; h=${h##*/}
  if [ -s "$f" ]; then echo "R $r $h"; else echo "E $r $h"; echo "R $r $h"; fi
done
find . -path '*/_manifests/revisions/sha256/*/link' | sed 's#/link$##; s#.*/##' | sort -u | while read -r h; do
  b=` + root + `/blobs/sha256/$(echo "$h" | cut -c1-2)/$h/data
  [ -f "$b" ] && [ "$(stat -c %s "$b")" -lt 500000 ] && grep -q 'image.index\|manifest.list' "$b" && echo "C $h $(grep -o 'sha256:[0-9a-f]\{64\}' "$b" | tr '\n' ' ')"
done
df -P /var/lib/registry | tail -1 | awk '{print "D", $3*1024, $4*1024}'
`

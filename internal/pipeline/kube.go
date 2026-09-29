package pipeline

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"io"
	"net"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
)

// KubeExecutor runs each step as a pod in a dedicated namespace.
type KubeExecutor struct {
	Client    kubernetes.Interface
	Namespace string // e.g. rendimiento-builds
	// BuildkitAddr is the shared BuildKit daemon, e.g. tcp://buildkitd.devops-tools.svc.cluster.local:1234.
	// It is used when there is no pool, or the pool has no ready daemon.
	BuildkitAddr string
	// BuildkitPool is a headless Service whose SRV records
	// (_buildkit._tcp.<pool>) list the ready daemons of a BuildKit pool,
	// e.g. buildkitd-pool.devops-tools.svc.cluster.local. Each build goes to
	// one daemon chosen by its image name: the same image always lands on
	// the same daemon (a warm cache) while different ones spread out.
	BuildkitPool string
	Resolver     interface {
		LookupSRV(ctx context.Context, service, proto, name string) (string, []*net.SRV, error)
	}
	BuildImage string // buildctl client image; match the daemon version
	GitImage   string
	// InsecureRegistry pushes over plain HTTP / self-signed TLS (registry.example.lan:5000 today).
	InsecureRegistry bool
	// Token returns a short-lived clone token for a repo; nil or "" means a public clone.
	Token   func(ctx context.Context, repo string) (string, error)
	Timeout time.Duration
	// ExcludeNodes keeps build pods off these nodes, e.g. ones that cannot
	// enforce the build namespace's NetworkPolicy (the GPU node's kernel lacks
	// an ipset type kube-router needs) or that are reserved for other work.
	ExcludeNodes []string
	// RailpackImage has the railpack CLI; it writes the build plan for
	// services without a Dockerfile. Empty disables Railpack builds.
	RailpackImage string
	// RailpackFrontend is the BuildKit frontend that builds a Railpack plan;
	// keep its version in step with the CLI's.
	RailpackFrontend string
}

func (k *KubeExecutor) defaults() {
	if k.BuildImage == "" {
		k.BuildImage = "moby/buildkit:v0.18.2"
	}
	if k.GitImage == "" {
		k.GitImage = "alpine/git:2.47.2"
	}
	if k.Timeout == 0 {
		k.Timeout = 30 * time.Minute
	}
	if k.RailpackFrontend == "" {
		k.RailpackFrontend = "ghcr.io/railwayapp/railpack-frontend:v0.40.0"
	}
}

var nonName = regexp.MustCompile(`[^a-z0-9-]+`)

func podName(runID, stepID string) string {
	n := nonName.ReplaceAllString(strings.ToLower("run-"+runID+"-"+stepID), "-")
	if len(n) > 57 {
		n = n[:57]
	}
	return strings.TrimRight(n, "-")
}

func randSuffix() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)[:5]
}

// Cleanup removes pods and clone secrets left by a previous process. Only
// the process that owns the run queue may call it.
func (k *KubeExecutor) Cleanup(ctx context.Context) (int, error) {
	sel := metav1.ListOptions{LabelSelector: "app.kubernetes.io/managed-by=rendimiento,rendimiento.ai/run"}
	pods, err := k.Client.CoreV1().Pods(k.Namespace).List(ctx, sel)
	if err != nil {
		return 0, err
	}
	for _, p := range pods.Items {
		_ = k.Client.CoreV1().Pods(k.Namespace).Delete(ctx, p.Name, metav1.DeleteOptions{})
	}
	secrets, err := k.Client.CoreV1().Secrets(k.Namespace).List(ctx, sel)
	if err != nil {
		return 0, err
	}
	for _, s := range secrets.Items {
		_ = k.Client.CoreV1().Secrets(k.Namespace).Delete(ctx, s.Name, metav1.DeleteOptions{})
	}
	return len(pods.Items), nil
}

// Execute runs one step as a pod: a clone secret with a short-lived token, the pod (clone, plan,
// then the step), its logs streamed to w, and the digest of a build read from the termination
// message. The pod and secret are removed afterwards.
func (k *KubeExecutor) Execute(ctx context.Context, runID string, src Source, step Step, w io.Writer) StepResult {
	k.defaults()
	res := StepResult{Started: time.Now()}
	fail := func(format string, a ...any) StepResult {
		res.Status, res.Message, res.Finished = StatusFailed, fmt.Sprintf(format, a...), time.Now()
		fmt.Fprintln(w, "rendimiento: "+res.Message)
		return res
	}
	ctx, cancel := context.WithTimeout(ctx, k.Timeout)
	defer cancel()

	// A random suffix keeps a retried run from colliding with pods its
	// interrupted predecessor left behind.
	name := podName(runID, step.ID) + "-" + randSuffix()
	labels := map[string]string{"app.kubernetes.io/managed-by": "rendimiento", "rendimiento.ai/run": nonName.ReplaceAllString(runID, "-")}

	token := ""
	if k.Token != nil {
		t, err := k.Token(ctx, src.Repo)
		if err != nil {
			return fail("could not get a clone token: %v", err)
		}
		token = t
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: k.Namespace, Labels: labels},
		StringData: map[string]string{"token": token},
	}
	if err := transientRetry(ctx, func() error {
		_, err := k.Client.CoreV1().Secrets(k.Namespace).Create(ctx, secret, metav1.CreateOptions{})
		if apierrors.IsAlreadyExists(err) {
			return nil
		}
		return err
	}); err != nil {
		return fail("create clone secret: %v", err)
	}
	buildkit := k.BuildkitAddr
	if step.Kind == KindBuild {
		var daemon string
		buildkit, daemon = k.buildkitFor(ctx, step.Target)
		if daemon != "" {
			fmt.Fprintf(w, "rendimiento: building on %s\n", daemon)
		}
	}
	pod, err := k.pod(name, labels, src, step, buildkit)
	if err != nil {
		return fail("%v", err)
	}
	cleanup := func() {
		bg := context.Background()
		_ = k.Client.CoreV1().Pods(k.Namespace).Delete(bg, name, metav1.DeleteOptions{})
		_ = k.Client.CoreV1().Secrets(k.Namespace).Delete(bg, name, metav1.DeleteOptions{})
	}
	defer cleanup()
	if err := transientRetry(ctx, func() error {
		_, err := k.Client.CoreV1().Pods(k.Namespace).Create(ctx, pod, metav1.CreateOptions{})
		if apierrors.IsAlreadyExists(err) {
			return nil // an earlier attempt got through after all
		}
		return err
	}); err != nil {
		return fail("create pod: %v", err)
	}

	var containers []string
	for _, c := range append(pod.Spec.InitContainers, pod.Spec.Containers...) {
		containers = append(containers, c.Name)
	}
	for _, container := range containers {
		if err := k.waitStarted(ctx, name, container); err != nil {
			return fail("%s: %v", container, err)
		}
		fmt.Fprintf(w, "::group::%s\n", container)
		if err := k.streamLogs(ctx, name, container, w); err != nil {
			fmt.Fprintf(w, "rendimiento: log stream interrupted: %v\n", err)
		}
		fmt.Fprintln(w, "::endgroup::")
	}

	final, err := k.waitDone(ctx, name)
	if err != nil {
		return fail("%v", err)
	}
	res.Finished = time.Now()
	if final.Status.Phase != corev1.PodSucceeded {
		return fail("step failed: %s", terminationSummary(final))
	}
	res.Status = StatusSucceeded
	if step.Kind == KindBuild {
		for _, cs := range final.Status.ContainerStatuses {
			if cs.Name == "step" && cs.State.Terminated != nil {
				res.Digest = strings.TrimSpace(cs.State.Terminated.Message)
			}
		}
		if !strings.HasPrefix(res.Digest, "sha256:") {
			return fail("build finished but reported no image digest")
		}
		fmt.Fprintf(w, "rendimiento: pushed %s@%s\n", step.Target, res.Digest)
	}
	return res
}

// --8<-- [start:scripts]

// planScript picks the builder: the Dockerfile when the folder has one (or
// it is required), Railpack otherwise, which then writes its build plan.
// Extra arguments ("--env K=V" pairs) go to railpack prepare.
const planScript = `set -eu
mkdir -p /workspace/plan && cd "$CONTEXT"
if [ "$BUILDER" = dockerfile ] || { [ -z "$BUILDER" ] && [ -f "$DOCKERFILE" ]; }; then
  echo "builder: Dockerfile ($DOCKERFILE)"
  echo dockerfile > /workspace/plan/builder
  exit 0
fi
if [ -z "$BUILDER" ]; then
  echo "builder: Railpack (no $DOCKERFILE in this folder; add one or set build.builder to take control)"
else
  echo "builder: Railpack"
fi
set -- prepare "$CONTEXT" --plan-out /workspace/plan/railpack-plan.json --error-missing-start "$@"
if [ -n "$START" ]; then set -- "$@" --start-cmd "$START"; fi
railpack "$@"
echo railpack > /workspace/plan/builder
`

const cloneScript = `set -eu
mkdir -p /workspace/src && cd /workspace/src
git init -q
git remote add origin "$CLONE_URL"
if [ -n "${GIT_TOKEN:-}" ]; then
  git config http.extraHeader "Authorization: basic $(printf 'x-access-token:%s' "$GIT_TOKEN" | base64 | tr -d '\n')"
fi
git fetch -q --depth 1 origin "$REF"
git checkout -q FETCH_HEAD
echo "checked out $(git rev-parse HEAD)"
`

// The digest is written to the termination log so the executor can read it
// from the pod status without parsing logs.
const buildScript = `set -eu
if [ "$(cat /workspace/plan/builder 2>/dev/null || echo dockerfile)" = railpack ]; then
  set -- --frontend gateway.v0 --opt source="$RAILPACK_FRONTEND" \
    --local context="$CONTEXT" --local dockerfile=/workspace/plan
else
  set -- --frontend dockerfile.v0 --local context="$CONTEXT" --local dockerfile="$CONTEXT" \
    --opt filename="$DOCKERFILE" --opt build-arg:GIT_SHA="$GIT_SHA" "$@"
fi
buildctl --addr "$BUILDKIT_ADDR" build "$@" \
  --output "type=image,name=$IMAGE:$TAG,push=true$INSECURE" \
  --import-cache "type=registry,ref=$IMAGE:buildcache$INSECURE" \
  --export-cache "type=registry,ref=$IMAGE:buildcache,mode=max$INSECURE" \
  --metadata-file /tmp/metadata.json
grep -o '"containerimage.digest": *"sha256:[a-f0-9]*"' /tmp/metadata.json | grep -o 'sha256:[a-f0-9]*' > /dev/termination-log
`

// --8<-- [end:scripts]

// --8<-- [start:buildkitFor]

// buildkitFor picks the pool daemon for key by rendezvous hashing: each key
// has a stable favourite among the ready daemons, and only the keys of a
// daemon that goes away move elsewhere. It returns the address and the
// daemon's name, or the single BuildkitAddr (and "") without a pool.
func (k *KubeExecutor) buildkitFor(ctx context.Context, key string) (string, string) {
	if k.BuildkitPool == "" {
		return k.BuildkitAddr, ""
	}
	res := k.Resolver
	if res == nil {
		res = net.DefaultResolver
	}
	lctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, srvs, err := res.LookupSRV(lctx, "buildkit", "tcp", k.BuildkitPool)
	if err != nil || len(srvs) == 0 {
		return k.BuildkitAddr, ""
	}
	var best *net.SRV
	var bestScore uint64
	for _, s := range srvs {
		h := fnv.New64a()
		h.Write([]byte(strings.TrimSuffix(s.Target, ".") + "|" + key))
		if score := h.Sum64(); best == nil || score > bestScore {
			best, bestScore = s, score
		}
	}
	host := strings.TrimSuffix(best.Target, ".")
	daemon, _, _ := strings.Cut(host, ".")
	return fmt.Sprintf("tcp://%s:%d", host, best.Port), daemon
}

// --8<-- [end:buildkitFor]

func (k *KubeExecutor) pod(name string, labels map[string]string, src Source, step Step, buildkit string) (*corev1.Pod, error) {
	cloneURL := src.Repo
	if !strings.Contains(cloneURL, "://") {
		cloneURL = "https://github.com/" + src.Repo + ".git"
	}
	ref := src.SHA
	if ref == "" {
		ref = src.Branch
	}
	workdir := path.Join("/workspace/src", path.Clean(step.Path))
	if !strings.HasPrefix(workdir, "/workspace/src") {
		return nil, fmt.Errorf("step path %q escapes the workspace", step.Path)
	}
	ws := corev1.VolumeMount{Name: "workspace", MountPath: "/workspace"}
	deadline := int64(k.Timeout.Seconds())
	clone := corev1.Container{
		Name:    "clone",
		Image:   k.GitImage,
		Command: []string{"sh", "-c", cloneScript},
		Env: []corev1.EnvVar{
			{Name: "CLONE_URL", Value: cloneURL},
			{Name: "REF", Value: ref},
			{Name: "GIT_TOKEN", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: name}, Key: "token"}}},
		},
		VolumeMounts: []corev1.VolumeMount{ws},
	}
	initContainers := []corev1.Container{clone}
	var main corev1.Container
	switch step.Kind {
	case KindTest:
		main = corev1.Container{
			Name:       "step",
			Image:      step.Image,
			Command:    []string{"sh", "-c", step.Command},
			WorkingDir: workdir,
			Env:        []corev1.EnvVar{{Name: "CI", Value: "true"}, {Name: "GIT_SHA", Value: src.SHA}},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("250m"), corev1.ResourceMemory: resource.MustParse("256Mi")},
				Limits:   corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("2Gi")},
			},
		}
	case KindBuild:
		insecure := ""
		if k.InsecureRegistry {
			insecure = ",registry.insecure=true"
		}
		tag := src.SHA
		if len(tag) > 12 {
			tag = tag[:12]
		}
		if tag == "" {
			tag = "latest"
		}
		var dockerArgs, railpackArgs []string
		keys := make([]string, 0, len(step.BuildArgs))
		for key := range step.BuildArgs {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			dockerArgs = append(dockerArgs, "--opt", "build-arg:"+key+"="+step.BuildArgs[key])
			railpackArgs = append(railpackArgs, "--env", key+"="+step.BuildArgs[key])
		}
		switch {
		case k.RailpackImage != "":
			initContainers = append(initContainers, corev1.Container{
				Name:    "plan",
				Image:   k.RailpackImage,
				Command: append([]string{"sh", "-c", planScript, "plan"}, railpackArgs...),
				Env: []corev1.EnvVar{
					{Name: "CONTEXT", Value: workdir},
					{Name: "DOCKERFILE", Value: step.Dockerfile},
					{Name: "BUILDER", Value: step.Builder},
					{Name: "START", Value: step.Start},
				},
				VolumeMounts: []corev1.VolumeMount{ws},
			})
		case step.Builder == "railpack":
			return nil, fmt.Errorf("build.builder is railpack but Railpack is not configured on this platform (RAILPACK_IMAGE)")
		}
		main = corev1.Container{
			Name:    "step",
			Image:   k.BuildImage,
			Command: append([]string{"sh", "-c", buildScript, "build"}, dockerArgs...),
			Env: []corev1.EnvVar{
				{Name: "RAILPACK_FRONTEND", Value: k.RailpackFrontend},
				{Name: "BUILDKIT_ADDR", Value: buildkit},
				{Name: "CONTEXT", Value: workdir},
				{Name: "DOCKERFILE", Value: step.Dockerfile},
				{Name: "IMAGE", Value: step.Target},
				{Name: "TAG", Value: tag},
				{Name: "GIT_SHA", Value: src.SHA},
				{Name: "INSECURE", Value: insecure},
			},
		}
	default:
		return nil, fmt.Errorf("unknown step kind %q", step.Kind)
	}
	main.VolumeMounts = []corev1.VolumeMount{ws}
	main.TerminationMessagePolicy = corev1.TerminationMessageReadFile
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: k.Namespace, Labels: labels},
		Spec: corev1.PodSpec{
			RestartPolicy:                corev1.RestartPolicyNever,
			Affinity:                     k.affinity(),
			ActiveDeadlineSeconds:        &deadline,
			AutomountServiceAccountToken: ptr(false),
			InitContainers:               initContainers,
			Containers:                   []corev1.Container{main},
			Volumes:                      []corev1.Volume{{Name: "workspace", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}},
		},
	}, nil
}

// waitStarted blocks until the container is running or has terminated,
// failing fast on image pull errors.
func (k *KubeExecutor) waitStarted(ctx context.Context, pod, container string) error {
	for {
		p, err := k.Client.CoreV1().Pods(k.Namespace).Get(ctx, pod, metav1.GetOptions{})
		if err != nil {
			return err
		}
		statuses := append(append([]corev1.ContainerStatus{}, p.Status.InitContainerStatuses...), p.Status.ContainerStatuses...)
		for _, cs := range statuses {
			if cs.Name != container {
				continue
			}
			if cs.State.Running != nil || cs.State.Terminated != nil {
				return nil
			}
			if w := cs.State.Waiting; w != nil && (w.Reason == "ErrImagePull" || w.Reason == "ImagePullBackOff" || w.Reason == "InvalidImageName" || w.Reason == "CreateContainerConfigError") {
				return fmt.Errorf("%s: %s", w.Reason, w.Message)
			}
		}
		if p.Status.Phase == corev1.PodFailed || p.Status.Phase == corev1.PodSucceeded {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for container to start")
		case <-time.After(time.Second):
		}
	}
}

func (k *KubeExecutor) streamLogs(ctx context.Context, pod, container string, w io.Writer) error {
	rc, err := k.Client.CoreV1().Pods(k.Namespace).GetLogs(pod, &corev1.PodLogOptions{Container: container, Follow: true}).Stream(ctx)
	if err != nil {
		return err
	}
	defer rc.Close()
	_, err = io.Copy(w, rc)
	return err
}

func (k *KubeExecutor) waitDone(ctx context.Context, name string) (*corev1.Pod, error) {
	for {
		p, err := k.Client.CoreV1().Pods(k.Namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		if p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
			return p, nil
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("step timed out after %s", k.Timeout)
		case <-time.After(time.Second):
		}
	}
}

func terminationSummary(p *corev1.Pod) string {
	if p.Status.Reason != "" {
		return p.Status.Reason + ": " + p.Status.Message
	}
	statuses := append(append([]corev1.ContainerStatus{}, p.Status.InitContainerStatuses...), p.Status.ContainerStatuses...)
	for _, cs := range statuses {
		if t := cs.State.Terminated; t != nil && t.ExitCode != 0 {
			return fmt.Sprintf("%s exited with code %d", cs.Name, t.ExitCode)
		}
	}
	return string(p.Status.Phase)
}

func (k *KubeExecutor) affinity() *corev1.Affinity {
	if len(k.ExcludeNodes) == 0 {
		return nil
	}
	return &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{
		RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{NodeSelectorTerms: []corev1.NodeSelectorTerm{{
			MatchExpressions: []corev1.NodeSelectorRequirement{{
				Key: "kubernetes.io/hostname", Operator: corev1.NodeSelectorOpNotIn, Values: k.ExcludeNodes,
			}},
		}}},
	}}
}

// transientRetry retries API calls that failed for reasons that pass on
// their own: an overloaded API server (k3s's SQLite datastore answers
// "database is locked" under heavy I/O), timeouts, throttling.
func transientRetry(ctx context.Context, fn func() error) error {
	backoff := wait.Backoff{Steps: 6, Duration: 500 * time.Millisecond, Factor: 2, Jitter: 0.2}
	var last error
	err := wait.ExponentialBackoffWithContext(ctx, backoff, func(context.Context) (bool, error) {
		last = fn()
		switch {
		case last == nil:
			return true, nil
		case isTransient(last):
			return false, nil // try again
		}
		return false, last
	})
	if wait.Interrupted(err) {
		return last
	}
	return err
}

func isTransient(err error) bool {
	return apierrors.IsInternalError(err) || apierrors.IsServerTimeout(err) || apierrors.IsTimeout(err) ||
		apierrors.IsTooManyRequests(err) || apierrors.IsServiceUnavailable(err) ||
		strings.Contains(err.Error(), "database is locked")
}

func ptr[T any](v T) *T { return &v }

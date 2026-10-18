// Package selfupdate moves rendimiento to an image it built and tested
// itself: its own repository's builds: entry (SELF_APP), released like any
// app's. The Environment page shows when the latest release holds an image
// other than the one running, and one button switches the Deployment to it.
package selfupdate

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/p0dxD/rendimiento.ai/internal/i18n"
	"github.com/p0dxD/rendimiento.ai/internal/store"
)

var M = i18n.M

// Releases is what the updater reads from the store.
type Releases interface {
	GetApp(ctx context.Context, name string) (*store.App, error)
	ListReleases(ctx context.Context, appID int64, limit int) ([]*store.Release, error)
}

type Updater struct {
	Kube     client.Client
	Releases Releases
	// App is the app whose releases hold the platform's image, under
	// "build:"+Build (rendimiento's own repository: builds: platform).
	App, Build string
	// Namespace, Deployment and Container are where rendimiento runs; Pod is
	// this process's pod (HOSTNAME), to read the image it really runs.
	Namespace, Deployment, Container, Pod string
	// Also are the platform's other Deployments (one per role), updated to
	// the same image; a missing one is skipped.
	Also []string
	// Changes lists the files that differ between two commits of the app's
	// repository (GitHub's compare). With it, Status says when the
	// manifests changed since the last `make deploy`; nil skips that check.
	Changes Changes

	mu      sync.Mutex
	changed map[[2]string][]string // two commits never change: compare once
}

// Changes is the part of the GitHub client the manifest check uses.
type Changes interface {
	ChangedFiles(ctx context.Context, installation int64, repo, base, head string) ([]string, bool, error)
}

// ManifestsAnnotation, on the Deployment, is the commit `make deploy`
// last applied. The Update button only changes the image; the manifests
// (CRDs, permissions, settings) are applied by `make deploy`.
const ManifestsAnnotation = "rendimiento.ai/manifests"

// Manifests says whether `make deploy` is needed: the files of the
// platform's manifests (deploy/) that changed between the commit it last
// applied and the newest release.
type Manifests struct {
	// Applied is the commit `make deploy` last applied ("" when it has
	// not recorded one yet).
	Applied string `json:"applied,omitempty"`
	// Changed are the manifest files the newest release changed since
	// then; when not empty, run `make deploy`.
	Changed []string `json:"changed,omitempty"`
}

// Version is one release of the platform.
type Version struct {
	Release int64     `json:"release"`
	SHA     string    `json:"sha"`
	Image   string    `json:"image"`
	At      time.Time `json:"at"`
}

type Status struct {
	App string `json:"app"`
	// Running is the image digest this pod runs; Current the release it came
	// from (none when it was built by hand, `make image`).
	Running string   `json:"running,omitempty"`
	Current *Version `json:"current,omitempty"`
	// Latest is the newest release's image; Available says it is not the one
	// running.
	Latest    *Version `json:"latest,omitempty"`
	Available bool     `json:"available"`
	// Manifests is nil when they could not be checked (no Changes, or
	// GitHub did not answer).
	Manifests *Manifests `json:"manifests,omitempty"`
}

// releases looked at for the running version: a few weeks of pushes.
const history = 50

// Status says which release runs (by the pod's image digest) and whether
// the newest release holds another image.
func (u *Updater) Status(ctx context.Context) (*Status, error) {
	st := &Status{App: u.App}
	app, versions, err := u.versions(ctx)
	if err != nil {
		return nil, err
	}
	st.Running = u.running(ctx)
	for i := range versions {
		if st.Running != "" && digest(versions[i].Image) == st.Running {
			st.Current = &versions[i]
			break
		}
	}
	if len(versions) > 0 {
		st.Latest = &versions[0]
		st.Available = digest(st.Latest.Image) != st.Running
		st.Manifests = u.manifests(ctx, app, st.Latest.SHA)
	}
	return st, nil
}

// manifests compares the commit `make deploy` last applied with head.
func (u *Updater) manifests(ctx context.Context, app *store.App, head string) *Manifests {
	if u.Changes == nil {
		return nil
	}
	var d appsv1.Deployment
	if err := u.Kube.Get(ctx, client.ObjectKey{Namespace: u.Namespace, Name: u.Deployment}, &d); err != nil {
		return nil
	}
	m := &Manifests{Applied: d.Annotations[ManifestsAnnotation]}
	if m.Applied == "" || m.Applied == head {
		return m
	}
	key := [2]string{m.Applied, head}
	u.mu.Lock()
	files, ok := u.changed[key]
	u.mu.Unlock()
	if !ok {
		all, _, err := u.Changes.ChangedFiles(ctx, app.InstallationID, app.Repo, m.Applied, head)
		if err != nil {
			return nil
		}
		for _, f := range all {
			if isManifest(f) && !slices.Contains(files, f) {
				files = append(files, f)
			}
		}
		u.mu.Lock()
		if u.changed == nil {
			u.changed = map[[2]string][]string{}
		}
		u.changed[key] = files
		u.mu.Unlock()
	}
	m.Changed = files
	return m
}

// isManifest says whether `make deploy` applies the file: the YAML under
// deploy/ (not, say, the Railpack image's Dockerfile).
func isManifest(path string) bool {
	return strings.HasPrefix(path, "deploy/") && (strings.HasSuffix(path, ".yaml") || strings.HasSuffix(path, ".yml"))
}

// Update points the platform's Deployments at release's image. Each rolls
// over (the new pods start, migrate the database and take over, then the old
// ones stop) within a minute or two; the request answers before that.
func (u *Updater) Update(ctx context.Context, release int64, login string) (*Version, error) {
	_, versions, err := u.versions(ctx)
	if err != nil {
		return nil, err
	}
	var v *Version
	for i := range versions {
		if versions[i].Release == release {
			v = &versions[i]
			break
		}
	}
	if v == nil {
		return nil, fmt.Errorf(M("release %d of %s has no image of rendimiento"), release, u.App)
	}
	// The other services first: the one answering this request goes last.
	for _, name := range u.Also {
		if err := u.patch(ctx, name, v, login); err != nil && !apierrors.IsNotFound(err) {
			return nil, err
		}
	}
	if err := u.patch(ctx, u.Deployment, v, login); err != nil {
		return nil, err
	}
	return v, nil
}

// patch points one Deployment's container at v's image.
func (u *Updater) patch(ctx context.Context, name string, v *Version, login string) error {
	var d appsv1.Deployment
	if err := u.Kube.Get(ctx, client.ObjectKey{Namespace: u.Namespace, Name: name}, &d); err != nil {
		return err
	}
	before := d.DeepCopy()
	found := false
	for i := range d.Spec.Template.Spec.Containers {
		if c := &d.Spec.Template.Spec.Containers[i]; c.Name == u.Container {
			c.Image, found = v.Image, true
		}
	}
	if !found {
		return fmt.Errorf(M("the %s deployment has no container named %s"), name, u.Container)
	}
	if d.Spec.Template.Annotations == nil {
		d.Spec.Template.Annotations = map[string]string{}
	}
	d.Spec.Template.Annotations["rendimiento.ai/release"] = fmt.Sprint(v.Release)
	d.Spec.Template.Annotations["rendimiento.ai/updated-by"] = login
	return u.Kube.Patch(ctx, &d, client.StrategicMergeFrom(before))
}

// versions are the app's releases that hold a platform image, newest first.
func (u *Updater) versions(ctx context.Context) (*store.App, []Version, error) {
	app, err := u.Releases.GetApp(ctx, u.App)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil, fmt.Errorf(M("%s is not an app here: rendimiento updates itself from its own repository's releases"), u.App)
	}
	if err != nil {
		return nil, nil, err
	}
	rels, err := u.Releases.ListReleases(ctx, app.ID, history)
	if err != nil {
		return nil, nil, err
	}
	var out []Version
	for _, r := range rels {
		if img := r.Images["build:"+u.Build]; img != "" && digest(img) != "" {
			out = append(out, Version{Release: r.Number, SHA: r.SHA, Image: img, At: r.CreatedAt})
		}
	}
	return app, out, nil
}

// running is the digest of the image this pod runs ("" when unknown: not in
// a pod, or the image is still being pulled).
func (u *Updater) running(ctx context.Context) string {
	if u.Pod == "" {
		return ""
	}
	var pod corev1.Pod
	if err := u.Kube.Get(ctx, client.ObjectKey{Namespace: u.Namespace, Name: u.Pod}, &pod); err != nil {
		return ""
	}
	for _, c := range pod.Status.ContainerStatuses {
		if c.Name == u.Container {
			return digest(c.ImageID)
		}
	}
	return ""
}

// digest is the sha256:… of an image reference or a container's image ID.
func digest(ref string) string {
	if i := strings.LastIndex(ref, "@"); i >= 0 && strings.HasPrefix(ref[i+1:], "sha256:") {
		return ref[i+1:]
	}
	return ""
}

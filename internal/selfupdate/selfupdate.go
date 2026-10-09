// Package selfupdate moves rendimiento to an image it built and tested
// itself: its own repository's builds: entry (SELF_APP), released like any
// app's. The Environment page shows when the latest release holds an image
// other than the one running, and one button switches the Deployment to it.
package selfupdate

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
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
}

// releases looked at for the running version: a few weeks of pushes.
const history = 50

func (u *Updater) Status(ctx context.Context) (*Status, error) {
	st := &Status{App: u.App}
	versions, err := u.versions(ctx)
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
	}
	return st, nil
}

// Update points the Deployment at release's image. With one replica and the
// Recreate strategy, this pod stops and the new one starts (and migrates the
// database) within a minute; the request answers before that.
func (u *Updater) Update(ctx context.Context, release int64, login string) (*Version, error) {
	versions, err := u.versions(ctx)
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
	var d appsv1.Deployment
	if err := u.Kube.Get(ctx, client.ObjectKey{Namespace: u.Namespace, Name: u.Deployment}, &d); err != nil {
		return nil, err
	}
	before := d.DeepCopy()
	found := false
	for i := range d.Spec.Template.Spec.Containers {
		if c := &d.Spec.Template.Spec.Containers[i]; c.Name == u.Container {
			c.Image, found = v.Image, true
		}
	}
	if !found {
		return nil, fmt.Errorf(M("the %s deployment has no container named %s"), u.Deployment, u.Container)
	}
	if d.Spec.Template.Annotations == nil {
		d.Spec.Template.Annotations = map[string]string{}
	}
	d.Spec.Template.Annotations["rendimiento.ai/release"] = fmt.Sprint(v.Release)
	d.Spec.Template.Annotations["rendimiento.ai/updated-by"] = login
	if err := u.Kube.Patch(ctx, &d, client.StrategicMergeFrom(before)); err != nil {
		return nil, err
	}
	return v, nil
}

// versions are the app's releases that hold a platform image, newest first.
func (u *Updater) versions(ctx context.Context) ([]Version, error) {
	app, err := u.Releases.GetApp(ctx, u.App)
	if errors.Is(err, store.ErrNotFound) {
		return nil, fmt.Errorf(M("%s is not an app here: rendimiento updates itself from its own repository's releases"), u.App)
	}
	if err != nil {
		return nil, err
	}
	rels, err := u.Releases.ListReleases(ctx, app.ID, history)
	if err != nil {
		return nil, err
	}
	var out []Version
	for _, r := range rels {
		if img := r.Images["build:"+u.Build]; img != "" && digest(img) != "" {
			out = append(out, Version{Release: r.Number, SHA: r.SHA, Image: img, At: r.CreatedAt})
		}
	}
	return out, nil
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

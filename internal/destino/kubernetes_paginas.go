package destino

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/rendimiento-ai/rendimiento/api/v1alpha1"
	"github.com/rendimiento-ai/rendimiento/internal/controller"
	"github.com/rendimiento-ai/rendimiento/internal/render"
	"github.com/rendimiento-ai/rendimiento/internal/store"
)

// Situacion reads the App object's status, and whether it is suspended.
func (k Kubernetes) Situacion(ctx context.Context, app string) (Situacion, error) {
	var cr v1alpha1.App
	if err := k.Kube.Get(ctx, client.ObjectKey{Name: app}, &cr); err != nil {
		return Situacion{}, err
	}
	return Situacion{Status: cr.Status, Suspendida: cr.Spec.Suspend}, nil
}

// Recursos is the app's resource tree: per service, its Deployment and
// pods, Service, Ingress and volume, each with a health (Healthy,
// Progressing, Degraded or Missing), rolled up to the app.
func (k Kubernetes) Recursos(ctx context.Context, a *store.App) (*Recurso, error) {
	ns := client.InNamespace(a.Name)
	sel := client.MatchingLabels{render.LabelApp: a.Name}
	var deps appsv1.DeploymentList
	var pods corev1.PodList
	var svcs corev1.ServiceList
	var ings networkingv1.IngressList
	var pvcs corev1.PersistentVolumeClaimList
	for _, l := range []client.ObjectList{&deps, &pods, &svcs, &ings, &pvcs} {
		if err := k.Kube.List(ctx, l, ns, sel); err != nil {
			return nil, err
		}
	}
	root := Recurso{Kind: "App", Name: a.Name, Health: "Healthy"}
	for _, svc := range a.Spec.Services {
		node := Recurso{Kind: "Service", Name: svc.Name, Health: "Missing"}
		for _, d := range deps.Items {
			if d.Name != svc.Name {
				continue
			}
			dn := Recurso{Kind: "Deployment", Name: d.Name, Health: deploymentHealth(&d),
				Info: []string{fmt.Sprintf("%d/%d ready", d.Status.ReadyReplicas, ptrOr(d.Spec.Replicas, 1))}}
			for _, p := range pods.Items {
				if p.Labels[render.LabelService] == svc.Name {
					dn.Children = append(dn.Children, podNode(&p))
				}
			}
			node.Health = dn.Health
			node.Children = append(node.Children, dn)
		}
		for _, sv := range svcs.Items {
			if sv.Name == svc.Name {
				node.Children = append(node.Children, Recurso{Kind: "Service", Name: sv.Name, Health: "Healthy", Info: []string{sv.Spec.ClusterIP}})
			}
		}
		for _, ing := range ings.Items {
			if ing.Name == svc.Name {
				info := []string{}
				for _, rule := range ing.Spec.Rules {
					info = append(info, "https://"+rule.Host)
				}
				h := "Progressing"
				if len(ing.Status.LoadBalancer.Ingress) > 0 {
					h = "Healthy"
				}
				node.Children = append(node.Children, Recurso{Kind: "Ingress", Name: ing.Name, Health: h, Info: info})
			}
		}
		for _, pvc := range pvcs.Items {
			if pvc.Name == svc.Name+"-data" {
				h := "Progressing"
				if pvc.Status.Phase == corev1.ClaimBound {
					h = "Healthy"
				}
				node.Children = append(node.Children, Recurso{Kind: "Volume", Name: pvc.Name, Health: h, Info: []string{string(pvc.Status.Phase)}})
			}
		}
		if node.Health != "Healthy" && root.Health != "Degraded" {
			root.Health = node.Health
			if node.Health == "Missing" {
				root.Health = "Progressing"
			}
		}
		root.Children = append(root.Children, node)
	}
	return &root, nil
}

// deploymentHealth: Degraded past its progress deadline, Healthy once every
// replica is updated and ready.
func deploymentHealth(d *appsv1.Deployment) string {
	for _, c := range d.Status.Conditions {
		if c.Type == appsv1.DeploymentProgressing && c.Reason == "ProgressDeadlineExceeded" {
			return "Degraded"
		}
	}
	want := ptrOr(d.Spec.Replicas, 1)
	if d.Status.ObservedGeneration >= d.Generation && d.Status.UpdatedReplicas == want && d.Status.ReadyReplicas == want {
		return "Healthy"
	}
	return "Progressing"
}

// podNode is a pod in the resource tree: Healthy when ready, Degraded when
// crash-looping or unable to pull its image, with its restarts and node.
func podNode(p *corev1.Pod) Recurso {
	n := Recurso{Kind: "Pod", Name: p.Name, Health: "Progressing", Info: []string{string(p.Status.Phase)}}
	ready := false
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
			ready = true
		}
	}
	for _, cs := range p.Status.ContainerStatuses {
		if w := cs.State.Waiting; w != nil && (w.Reason == "CrashLoopBackOff" || w.Reason == "ImagePullBackOff" || w.Reason == "ErrImagePull") {
			n.Health = "Degraded"
			n.Info = append(n.Info, w.Reason)
		}
		if cs.RestartCount > 0 {
			n.Info = append(n.Info, fmt.Sprintf("%d restarts", cs.RestartCount))
		}
	}
	if ready {
		n.Health = "Healthy"
	}
	if p.Spec.NodeName != "" {
		n.Info = append(n.Info, "node "+p.Spec.NodeName)
	}
	return n
}

func ptrOr(p *int32, d int32) int32 {
	if p == nil {
		return d
	}
	return *p
}

// Reiniciar restarts the app's Deployments (a new annotation on their pod
// template), so new secret values are picked up.
func (k Kubernetes) Reiniciar(ctx context.Context, app string) error {
	var deps appsv1.DeploymentList
	if err := k.Kube.List(ctx, &deps, client.InNamespace(app), client.MatchingLabels{render.LabelApp: app}); err != nil {
		return err
	}
	for i := range deps.Items {
		d := &deps.Items[i]
		patch := client.MergeFrom(d.DeepCopy())
		if d.Spec.Template.Annotations == nil {
			d.Spec.Template.Annotations = map[string]string{}
		}
		d.Spec.Template.Annotations["rendimiento.ai/restartedAt"] = time.Now().Format(time.RFC3339)
		if err := k.Kube.Patch(ctx, d, patch); err != nil {
			return err
		}
	}
	return nil
}

// Impacto lists what deleting the app would remove: its namespace, and the
// volumes and secrets in it (only those rendimiento owns, in a shared
// namespace).
func (k Kubernetes) Impacto(ctx context.Context, app string) (*Impacto, error) {
	im := &Impacto{Namespace: app, Volumes: []string{}, Secrets: []string{}}
	var cr v1alpha1.App
	if err := k.Kube.Get(ctx, client.ObjectKey{Name: app}, &cr); err == nil {
		im.Adopted = cr.Spec.Adopt
		im.SharedNamespace = cr.Spec.SharedNamespace
	}
	own := func(o metav1.Object) bool {
		// With a shared namespace only objects rendimiento owns are deleted.
		if !im.SharedNamespace {
			return true
		}
		for _, r := range o.GetOwnerReferences() {
			if r.UID == cr.UID {
				return true
			}
		}
		return false
	}
	var pvcs corev1.PersistentVolumeClaimList
	if err := k.Kube.List(ctx, &pvcs, client.InNamespace(app)); err != nil {
		return nil, err
	}
	for _, v := range pvcs.Items {
		if !own(&v) {
			continue
		}
		size := v.Spec.Resources.Requests[corev1.ResourceStorage]
		im.Volumes = append(im.Volumes, fmt.Sprintf("%s (%s)", v.Name, size.String()))
	}
	var secs corev1.SecretList
	if err := k.Kube.List(ctx, &secs, client.InNamespace(app)); err != nil {
		return nil, err
	}
	for _, sec := range secs.Items {
		if sec.Type == corev1.SecretTypeOpaque && own(&sec) {
			im.Secrets = append(im.Secrets, sec.Name)
		}
	}
	return im, nil
}

// Desconectar stops managing the app but leaves everything running: the
// namespace and its objects lose rendimiento's ownership (so deleting the
// App garbage-collects nothing) and labels, DNS records stay, and the App
// object is deleted. It is the reverse of a migration.
func (k Kubernetes) Desconectar(ctx context.Context, app string) error {
	var cr v1alpha1.App
	err := k.Kube.Get(ctx, client.ObjectKey{Name: app}, &cr)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	// 1. Stop reconciling, and mark the App so its deletion keeps DNS.
	patch := client.MergeFrom(cr.DeepCopy())
	cr.Spec.Suspend = true
	if cr.Annotations == nil {
		cr.Annotations = map[string]string{}
	}
	cr.Annotations[controller.AnnotationDisconnect] = "true"
	if err := k.Kube.Patch(ctx, &cr, patch); err != nil {
		return fmt.Errorf("suspend: %w", err)
	}
	// 2. Release every object: drop the owner reference and rendimiento's
	// labels (metadata only, so no pod restarts).
	var ns corev1.Namespace
	objs := []client.Object{}
	if err := k.Kube.Get(ctx, client.ObjectKey{Name: app}, &ns); err == nil {
		objs = append(objs, &ns)
	}
	lists := []client.ObjectList{&appsv1.DeploymentList{}, &corev1.ServiceList{}, &networkingv1.IngressList{},
		&corev1.PersistentVolumeClaimList{}, &batchv1.CronJobList{}, &corev1.SecretList{}}
	for _, l := range lists {
		if err := k.Kube.List(ctx, l, client.InNamespace(app)); err != nil {
			return err
		}
		items, err := meta.ExtractList(l)
		if err != nil {
			return err
		}
		for _, it := range items {
			objs = append(objs, it.(client.Object))
		}
	}
	for _, o := range objs {
		if err := release(ctx, k.Kube, o, cr.UID); err != nil {
			return fmt.Errorf("release %T %s: %w", o, o.GetName(), err)
		}
	}
	// 3. Delete the App; nothing is owned by it any more.
	return client.IgnoreNotFound(k.Kube.Delete(ctx, &cr))
}

// release removes owner references to the App and rendimiento's top-level
// labels from one object.
func release(ctx context.Context, c client.Client, o client.Object, owner types.UID) error {
	// Snapshot first: GetLabels returns the object's own map, so editing it
	// before taking the patch base would hide the change from the patch.
	patch := client.MergeFrom(o.DeepCopyObject().(client.Object))
	var refs []metav1.OwnerReference
	changed := false
	for _, r := range o.GetOwnerReferences() {
		if r.UID == owner {
			changed = true
			continue
		}
		refs = append(refs, r)
	}
	labels := o.GetLabels()
	if labels[render.LabelManagedBy] == render.ManagedBy {
		for _, k := range []string{render.LabelManagedBy, render.LabelApp, render.LabelService} {
			delete(labels, k)
		}
		changed = true
	}
	if !changed {
		return nil
	}
	o.SetOwnerReferences(refs)
	o.SetLabels(labels)
	return c.Patch(ctx, o, patch)
}

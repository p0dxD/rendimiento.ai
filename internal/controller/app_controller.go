// Package controller reconciles App objects into running workloads: the
// GitOps half of rendimiento. Desired objects are server-side applied with
// an owner reference, so drift is corrected on every watch event and
// deleting an App garbage-collects its namespace.
package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/p0dxD/rendimiento.ai/api/v1alpha1"
	"github.com/p0dxD/rendimiento.ai/internal/dns"
	"github.com/p0dxD/rendimiento.ai/internal/render"
	"github.com/p0dxD/rendimiento.ai/internal/spec"
)

const (
	finalizer  = "rendimiento.ai/cleanup"
	fieldOwner = client.FieldOwner("rendimiento")
)

var certificateGVK = schema.GroupVersionKind{Group: "cert-manager.io", Version: "v1", Kind: "Certificate"}

type AppReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Render render.Options
	DNS    dns.Provider
}

// +kubebuilder:rbac:groups=rendimiento.ai,resources=apps;apps/status;apps/finalizers,verbs=*
// +kubebuilder:rbac:groups="",resources=namespaces;services;persistentvolumeclaims,verbs=*
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=*
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=*
// +kubebuilder:rbac:groups=batch,resources=cronjobs,verbs=*
// +kubebuilder:rbac:groups=cert-manager.io,resources=certificates,verbs=get;list;watch

func (r *AppReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var app v1alpha1.App
	if err := r.Get(ctx, req.NamespacedName, &app); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !app.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, r.finalize(ctx, &app)
	}
	if controllerutil.AddFinalizer(&app, finalizer) {
		if err := r.Update(ctx, &app); err != nil {
			return ctrl.Result{}, err
		}
	}

	status := v1alpha1.AppStatus{ObservedGeneration: app.Generation, Release: app.Status.Release}
	result, err := r.sync(ctx, &app, &status)
	if err != nil {
		status.Phase, status.Message = v1alpha1.PhaseError, err.Error()
		log.FromContext(ctx).Error(err, "sync failed")
	}
	app.Status = status
	if uerr := r.Status().Update(ctx, &app); uerr != nil {
		return ctrl.Result{}, uerr
	}
	if err != nil && !errors.Is(err, errBlocked) {
		return ctrl.Result{}, err // retried with backoff
	}
	return result, nil
}

// errBlocked marks problems that need a human (name clashes); retried slowly.
var errBlocked = errors.New("blocked")

func (r *AppReconciler) sync(ctx context.Context, app *v1alpha1.App, st *v1alpha1.AppStatus) (ctrl.Result, error) {
	switch {
	case app.Spec.Suspend:
		st.Phase, st.Message = v1alpha1.PhaseSuspended, "reconciliation suspended"
		return ctrl.Result{}, nil
	case len(app.Spec.Images) == 0:
		st.Phase, st.Message = v1alpha1.PhaseWaiting, "waiting for the first successful build"
		return ctrl.Result{}, nil
	}

	legacy, err := r.checkOwnership(ctx, app)
	if err != nil {
		return ctrl.Result{RequeueAfter: 5 * time.Minute}, err
	}

	in := render.Input{App: app.Name, Selectors: map[string]map[string]string{}}
	in.Spec.Services = app.Spec.Services
	in.Spec.Jobs = app.Spec.Jobs
	in.Spec.Postgres, in.Spec.Redis = app.Spec.Postgres, app.Spec.Redis
	if in.ServiceURLs, err = r.serviceURLs(ctx, app, in.Spec); err != nil {
		st.Phase, st.Message = v1alpha1.PhaseError, err.Error()
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}
	in.Images = app.Spec.Images
	for _, svc := range app.Spec.Services {
		// Selectors are immutable: keep whatever an existing Deployment has.
		var live appsv1.Deployment
		if err := r.Get(ctx, client.ObjectKey{Namespace: app.Name, Name: svc.Name}, &live); err == nil && live.Spec.Selector != nil {
			in.Selectors[svc.Name] = live.Spec.Selector.MatchLabels
		}
	}
	objs, err := render.Render(in, r.Render)
	if err != nil {
		return ctrl.Result{}, err
	}
	var ingresses []metav1.Object
	for _, obj := range objs.List() {
		if _, isNS := obj.(*corev1.Namespace); isNS {
			if !app.Spec.SharedNamespace { // otherwise someone else owns it: never label, own or delete it
				if err := r.apply(ctx, app, obj); err != nil {
					return ctrl.Result{}, err
				}
			}
			// The namespace exists now: create the credentials the database,
			// cache and their users read, before any of them start.
			if err := r.ensureNeedSecrets(ctx, app, in.Spec); err != nil {
				return ctrl.Result{}, err
			}
			continue
		}
		if _, isIngress := obj.(*networkingv1.Ingress); isIngress {
			if len(legacy) == 0 { // otherwise traffic stays on the legacy ingress until migrate switches it
				ingresses = append(ingresses, obj)
			}
			continue
		}
		if err := r.apply(ctx, app, obj); err != nil {
			return ctrl.Result{}, err
		}
	}
	if err := r.applyIngresses(ctx, app, ingresses); err != nil {
		return ctrl.Result{}, err
	}
	if len(legacy) > 0 {
		done, err := r.migrate(ctx, app, objs, legacy)
		if err != nil {
			return ctrl.Result{}, err
		}
		if !done {
			st.Phase = v1alpha1.PhaseProgressing
			st.Message = "migrating: new pods are starting; traffic still goes to the existing deployment"
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}
	}
	if err := r.prune(ctx, app, objs); err != nil {
		return ctrl.Result{}, err
	}
	st.Release = app.Spec.Release

	healthy := true
	// The app's services, then the database and cache its needs run.
	for _, svc := range append(append([]spec.Service{}, app.Spec.Services...), render.NeedServices(in.Spec)...) {
		image := app.Spec.Images[svc.Name]
		if image == "" {
			image = svc.Image
		}
		ss := v1alpha1.ServiceStatus{Name: svc.Name, Image: image, CertReady: true, DNSReady: true}
		var dep appsv1.Deployment
		if err := r.Get(ctx, client.ObjectKey{Namespace: app.Name, Name: svc.Name}, &dep); err == nil {
			ss.Replicas, ss.ReadyReplicas = *dep.Spec.Replicas, dep.Status.ReadyReplicas
			if c := deploymentCondition(&dep, appsv1.DeploymentProgressing); c != nil && c.Reason == "ProgressDeadlineExceeded" {
				ss.Message = c.Message
				st.Phase = v1alpha1.PhaseDegraded
			}
			// Healthy only once the rollout completed: every pod runs the new
			// version and is available, and no old pod is left serving.
			if dep.Status.ObservedGeneration < dep.Generation || dep.Status.UpdatedReplicas < ss.Replicas ||
				dep.Status.Replicas > dep.Status.UpdatedReplicas || dep.Status.AvailableReplicas < ss.Replicas {
				healthy = false
				if dep.Status.Replicas > dep.Status.UpdatedReplicas && ss.Message == "" {
					ss.Message = fmt.Sprintf("rolling out: %d old pod(s) still serving", dep.Status.Replicas-dep.Status.UpdatedReplicas)
				}
			}
			if msg := r.crashLooping(ctx, &dep); msg != "" {
				ss.Message = msg
				st.Phase = v1alpha1.PhaseDegraded
			}
		}
		if ss.ReadyReplicas < ss.Replicas {
			healthy = false
		}
		if svc.Domain != "" {
			ss.URL = "https://" + svc.Domain
			for _, h := range svc.Hosts() {
				if err := r.DNS.Ensure(ctx, h, app.Name); err != nil {
					ss.DNSReady, ss.Message = false, err.Error()
					healthy = false
				}
			}
			ss.CertReady = true
			for _, g := range svc.TLSGroups() {
				ss.CertReady = ss.CertReady && r.certReady(ctx, app.Name, g[0].(string))
			}
			healthy = healthy && ss.CertReady
		}
		st.Services = append(st.Services, ss)
	}
	switch {
	case st.Phase == v1alpha1.PhaseDegraded:
		st.Message = "a rollout failed; see service messages"
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	case healthy:
		st.Phase, st.Message = v1alpha1.PhaseHealthy, ""
		return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
	default:
		st.Phase, st.Message = v1alpha1.PhaseProgressing, "rolling out"
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}
}

// checkOwnership refuses to take over a namespace or hostname that belongs
// to something else, e.g. an app still deployed by Jenkins + ArgoCD. With
// Spec.Adopt, the app's own namespace may be taken over, and ingresses in
// it that serve the app's domains are returned as legacy (to be replaced).
func (r *AppReconciler) checkOwnership(ctx context.Context, app *v1alpha1.App) ([]networkingv1.Ingress, error) {
	var ns corev1.Namespace
	err := r.Get(ctx, client.ObjectKey{Name: app.Name}, &ns)
	if app.Spec.SharedNamespace && apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("%w: namespace %q must exist: with sharedNamespace rendimiento does not create it", errBlocked, app.Name)
	}
	if err == nil && ns.Labels[render.LabelApp] != app.Name && !app.Spec.Adopt && !app.Spec.SharedNamespace {
		return nil, fmt.Errorf("%w: namespace %q already exists and is not managed by rendimiento; pick another app name, or migrate it (adopt)", errBlocked, app.Name)
	}
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, err
	}
	domains := map[string]bool{}
	for _, s := range app.Spec.Services {
		for _, h := range s.Hosts() {
			domains[h] = true
		}
	}
	if len(domains) == 0 {
		return nil, nil
	}
	var ings networkingv1.IngressList
	if err := r.List(ctx, &ings); err != nil {
		return nil, err
	}
	ours := map[string]bool{} // ingress names rendimiento will render (one per service with a domain)
	for _, s := range app.Spec.Services {
		if s.HasIngress() {
			ours[s.IngressName()] = true
		}
	}
	var legacy []networkingv1.Ingress
	for _, ing := range ings.Items {
		if ing.Labels[render.LabelApp] == app.Name && ing.Namespace == app.Name {
			continue
		}
		if app.Spec.Adopt && ing.Namespace == app.Name && ours[ing.Name] {
			continue // same name: replaced in place by takeover
		}
		for _, rule := range ing.Spec.Rules {
			if !domains[rule.Host] {
				continue
			}
			if app.Spec.Adopt && ing.Namespace == app.Name {
				legacy = append(legacy, ing)
				break
			}
			return nil, fmt.Errorf("%w: host %s is already served by ingress %s/%s", errBlocked, rule.Host, ing.Namespace, ing.Name)
		}
	}
	return legacy, nil
}

// migrate runs one step of an adoption: new workloads are already applied;
// once they are ready, traffic moves from the legacy ingresses to ours and
// the old workloads behind them are removed. It returns done=false while
// waiting for the new pods.
func (r *AppReconciler) migrate(ctx context.Context, app *v1alpha1.App, objs *render.Objects, legacy []networkingv1.Ingress) (bool, error) {
	for _, d := range objs.Deployments {
		var live appsv1.Deployment
		if err := r.Get(ctx, client.ObjectKeyFromObject(d), &live); err != nil {
			return false, client.IgnoreNotFound(err)
		}
		want := *d.Spec.Replicas
		if live.Status.ObservedGeneration < live.Generation || live.Status.UpdatedReplicas < want || live.Status.AvailableReplicas < want {
			return false, nil
		}
	}
	ours := map[string]bool{}
	for _, s := range objs.Services {
		ours[s.Name] = true
	}
	log := log.FromContext(ctx)
	oldServices := map[string]bool{}
	for i := range legacy {
		ing := &legacy[i]
		for _, rule := range ing.Spec.Rules {
			if rule.HTTP == nil {
				continue
			}
			for _, p := range rule.HTTP.Paths {
				if b := p.Backend.Service; b != nil && !ours[b.Name] {
					oldServices[b.Name] = true
				}
			}
		}
		if err := r.Delete(ctx, ing); client.IgnoreNotFound(err) != nil {
			return false, err
		}
		log.Info("adopt: removed legacy ingress", "ingress", ing.Name)
	}
	// Our ingresses go in right away. The ingress-nginx admission webhook may
	// briefly still see the deleted ingress, so retry for a few seconds.
	for _, ing := range objs.Ingresses {
		var err error
		for attempt := 0; attempt < 30; attempt++ {
			if err = r.apply(ctx, app, ing); err == nil {
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
		if err != nil {
			return false, err
		}
	}
	// Remove the old services and the unmanaged deployments they fronted.
	for name := range oldServices {
		var svc corev1.Service
		if err := r.Get(ctx, client.ObjectKey{Namespace: app.Name, Name: name}, &svc); err != nil {
			continue
		}
		if svc.Labels[render.LabelManagedBy] == render.ManagedBy || len(svc.Spec.Selector) == 0 {
			continue
		}
		var deps appsv1.DeploymentList
		if err := r.List(ctx, &deps, client.InNamespace(app.Name)); err != nil {
			return false, err
		}
		for i := range deps.Items {
			d := &deps.Items[i]
			if d.Labels[render.LabelManagedBy] == render.ManagedBy || !selects(svc.Spec.Selector, d.Spec.Template.Labels) {
				continue
			}
			if err := r.Delete(ctx, d); client.IgnoreNotFound(err) != nil {
				return false, err
			}
			log.Info("adopt: removed legacy deployment", "deployment", d.Name)
		}
		if err := r.Delete(ctx, &svc); client.IgnoreNotFound(err) != nil {
			return false, err
		}
		log.Info("adopt: removed legacy service", "service", name)
	}
	return true, nil
}

func selects(selector, labels map[string]string) bool {
	for k, v := range selector {
		if labels[k] != v {
			return false
		}
	}
	return true
}

func (r *AppReconciler) apply(ctx context.Context, app *v1alpha1.App, obj metav1.Object) error {
	robj := obj.(client.Object)
	if err := controllerutil.SetControllerReference(app, robj, r.Scheme); err != nil {
		return err
	}
	if app.Spec.Adopt {
		if err := r.takeover(ctx, robj); err != nil {
			return fmt.Errorf("take over %T %s: %w", robj, robj.GetName(), err)
		}
	}
	m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(robj)
	if err != nil {
		return err
	}
	u := &unstructured.Unstructured{Object: m}
	delete(u.Object, "status")
	unstructured.RemoveNestedField(u.Object, "metadata", "creationTimestamp")
	unstructured.RemoveNestedField(u.Object, "spec", "template", "metadata", "creationTimestamp")
	if err := r.Apply(ctx, client.ApplyConfigurationFromUnstructured(u), fieldOwner, client.ForceOwnership); err != nil {
		return fmt.Errorf("apply %s %s: %w", u.GetKind(), u.GetName(), err)
	}
	return nil
}

// applyIngresses applies ingresses in several passes. Moving a path from one
// ingress to another (e.g. /api from the website's ingress to the API's) is
// rejected by the ingress-nginx webhook while both claim it, so an ingress
// that fails is retried after the others have been applied.
func (r *AppReconciler) applyIngresses(ctx context.Context, app *v1alpha1.App, pending []metav1.Object) error {
	var lastErr error
	for pass := 0; pass < 3 && len(pending) > 0; pass++ {
		var failed []metav1.Object
		for _, ing := range pending {
			if err := r.apply(ctx, app, ing); err != nil {
				failed, lastErr = append(failed, ing), err
			}
		}
		if len(failed) == len(pending) {
			break // no progress; retrying now will not help
		}
		pending = failed
	}
	if len(pending) > 0 {
		return lastErr
	}
	return nil
}

// takeover replaces, once, a same-named object that rendimiento does not
// manage yet (e.g. deployed by ArgoCD) with the desired one. A full replace
// rather than an apply drops everything the previous owner set (old env,
// containers, annotations), and clearing managedFields removes its field
// ownership so later applies fully own the object. Immutable or
// allocated fields are carried over from the live object.
func (r *AppReconciler) takeover(ctx context.Context, desired client.Object) error {
	var live client.Object
	switch desired.(type) {
	case *appsv1.Deployment:
		live = &appsv1.Deployment{}
	case *corev1.Service:
		live = &corev1.Service{}
	case *networkingv1.Ingress:
		live = &networkingv1.Ingress{}
	case *batchv1.CronJob:
		live = &batchv1.CronJob{}
	default:
		return nil // namespaces and volumes are applied normally
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(desired), live); err != nil {
		return client.IgnoreNotFound(err)
	}
	if live.GetLabels()[render.LabelManagedBy] == render.ManagedBy {
		return nil
	}
	obj := desired.DeepCopyObject().(client.Object)
	switch o := obj.(type) {
	case *appsv1.Deployment:
		l := live.(*appsv1.Deployment)
		o.Spec.Selector = l.Spec.Selector
		if rev := l.Annotations["deployment.kubernetes.io/revision"]; rev != "" {
			o.SetAnnotations(map[string]string{"deployment.kubernetes.io/revision": rev})
		}
	case *corev1.Service:
		l := live.(*corev1.Service)
		o.Spec.ClusterIP, o.Spec.ClusterIPs = l.Spec.ClusterIP, l.Spec.ClusterIPs
		o.Spec.IPFamilies, o.Spec.IPFamilyPolicy = l.Spec.IPFamilies, l.Spec.IPFamilyPolicy
	}
	// 1. Replace: the object now holds exactly the desired content, and
	// rendimiento's Update entry records every field it set.
	obj.SetResourceVersion(live.GetResourceVersion())
	if err := r.Update(ctx, obj, fieldOwner); err != nil {
		return err
	}
	// 2. Hand ownership over to server-side apply: keep only rendimiento's
	// entry, converted from Update to Apply (the documented client-side to
	// server-side apply upgrade). Previous owners (ArgoCD, kubectl) and the
	// "before-first-apply" placeholder would otherwise co-own fields and
	// block their removal when they are later dropped from rendimiento.yaml.
	var mine *metav1.ManagedFieldsEntry
	for _, mf := range obj.GetManagedFields() {
		if mf.Manager == string(fieldOwner) && mf.Operation == metav1.ManagedFieldsOperationUpdate {
			m := mf
			mine = &m
		}
	}
	if mine == nil {
		return fmt.Errorf("no field ownership recorded for %s after replace", fieldOwner)
	}
	mine.Operation = metav1.ManagedFieldsOperationApply
	obj.SetManagedFields([]metav1.ManagedFieldsEntry{*mine})
	if err := r.Update(ctx, obj, fieldOwner); err != nil {
		return err
	}
	log.FromContext(ctx).Info("adopt: took over existing object in place", "kind", fmt.Sprintf("%T", obj), "name", obj.GetName())
	return nil
}

// prune deletes workloads, services and ingresses of this app that are no
// longer in the spec. Volumes are never pruned automatically: data outlives config.
func (r *AppReconciler) prune(ctx context.Context, app *v1alpha1.App, objs *render.Objects) error {
	want := map[string]bool{}
	for _, o := range objs.List() {
		want[fmt.Sprintf("%T/%s", o, o.GetName())] = true
	}
	sel := client.MatchingLabels{render.LabelApp: app.Name, render.LabelManagedBy: render.ManagedBy}
	in := client.InNamespace(app.Name)
	var deps appsv1.DeploymentList
	var svcs corev1.ServiceList
	var ings networkingv1.IngressList
	var crons batchv1.CronJobList
	for _, l := range []client.ObjectList{&deps, &svcs, &ings, &crons} {
		if err := r.List(ctx, l, in, sel); err != nil {
			return err
		}
	}
	var stale []client.Object
	for i := range deps.Items {
		stale = appendIfStale(stale, want, &deps.Items[i])
	}
	for i := range svcs.Items {
		stale = appendIfStale(stale, want, &svcs.Items[i])
	}
	for i := range ings.Items {
		stale = appendIfStale(stale, want, &ings.Items[i])
	}
	for i := range crons.Items {
		stale = appendIfStale(stale, want, &crons.Items[i])
	}
	for _, o := range stale {
		if err := r.Delete(ctx, o); client.IgnoreNotFound(err) != nil {
			return err
		}
		log.FromContext(ctx).Info("pruned", "kind", fmt.Sprintf("%T", o), "name", o.GetName())
	}
	return nil
}

func appendIfStale(stale []client.Object, want map[string]bool, o client.Object) []client.Object {
	if !want[fmt.Sprintf("%T/%s", o, o.GetName())] {
		return append(stale, o)
	}
	return stale
}

func (r *AppReconciler) certReady(ctx context.Context, ns, name string) bool {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(certificateGVK)
	if err := r.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, u); err != nil {
		return false
	}
	conds, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	for _, c := range conds {
		m, _ := c.(map[string]any)
		if m["type"] == "Ready" && m["status"] == "True" {
			return true
		}
	}
	return false
}

// AnnotationDisconnect marks an App being disconnected: its workloads keep
// running unmanaged, so deleting it must not remove their DNS records.
const AnnotationDisconnect = "rendimiento.ai/disconnect"

func (r *AppReconciler) finalize(ctx context.Context, app *v1alpha1.App) error {
	if !controllerutil.ContainsFinalizer(app, finalizer) {
		return nil
	}
	if app.Annotations[AnnotationDisconnect] == "true" {
		controllerutil.RemoveFinalizer(app, finalizer)
		return r.Update(ctx, app)
	}
	for _, svc := range app.Spec.Services {
		for _, h := range svc.Hosts() {
			if err := r.DNS.Remove(ctx, h, app.Name); err != nil {
				return err
			}
		}
	}
	controllerutil.RemoveFinalizer(app, finalizer)
	return r.Update(ctx, app)
}

// crashLooping reports a pod of the deployment stuck restarting, e.g. a new
// version that crashes while the old pods keep serving.
func (r *AppReconciler) crashLooping(ctx context.Context, dep *appsv1.Deployment) string {
	var pods corev1.PodList
	if err := r.List(ctx, &pods, client.InNamespace(dep.Namespace), client.MatchingLabels(dep.Spec.Selector.MatchLabels)); err != nil {
		return ""
	}
	for _, p := range pods.Items {
		for _, cs := range p.Status.ContainerStatuses {
			if w := cs.State.Waiting; w != nil && (w.Reason == "CrashLoopBackOff" || w.Reason == "ImagePullBackOff" || w.Reason == "ErrImagePull" || w.Reason == "CreateContainerConfigError") {
				return fmt.Sprintf("pod %s: %s (%d restarts)", p.Name, w.Reason, cs.RestartCount)
			}
		}
	}
	return ""
}

func deploymentCondition(d *appsv1.Deployment, t appsv1.DeploymentConditionType) *appsv1.DeploymentCondition {
	for i := range d.Status.Conditions {
		if d.Status.Conditions[i].Type == t {
			return &d.Status.Conditions[i]
		}
	}
	return nil
}

func (r *AppReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.App{}).
		Owns(&corev1.Namespace{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&networkingv1.Ingress{}).
		Owns(&batchv1.CronJob{}).
		Complete(r)
}

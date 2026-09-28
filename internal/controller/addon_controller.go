package controller

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/pmezard/go-difflib/difflib"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/yaml"

	"github.com/p0dxD/rendimiento.ai/api/v1alpha1"
	"github.com/p0dxD/rendimiento.ai/internal/addon"
)

const (
	addonFinalizer  = "rendimiento.ai/addon"
	addonFieldOwner = client.FieldOwner("rendimiento-addons")
	// LabelAddon marks every object an add-on applies.
	LabelAddon = "rendimiento.ai/addon"
	// AnnotationUninstall on an Addon makes deleting it also delete what it
	// installed. Without it, deleting an Addon leaves everything running.
	AnnotationUninstall = "rendimiento.ai/uninstall"
	addonResync         = 5 * time.Minute
)

// neverDelete are kinds an add-on never deletes, whether pruning or
// uninstalling: losing them loses data or breaks other workloads.
var neverDelete = map[string]bool{
	"CustomResourceDefinition": true, "Namespace": true, "PersistentVolumeClaim": true,
	"PersistentVolume": true, "StorageClass": true,
}

// AddonReconciler installs add-ons and keeps them in sync. Client reads and
// updates Addon objects; Target reads and changes what add-ons install and
// acts as a separate, more privileged identity (the rendimiento-addons
// service account), so those changes are attributed to it.
type AddonReconciler struct {
	client.Client
	Target   client.Client
	Renderer *addon.Renderer
}

// +kubebuilder:rbac:groups=rendimiento.ai,resources=addons;addons/status;addons/finalizers,verbs=*

func (r *AddonReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var a v1alpha1.Addon
	if err := r.Get(ctx, req.NamespacedName, &a); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !a.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, r.finalize(ctx, &a)
	}
	if controllerutil.AddFinalizer(&a, addonFinalizer) {
		if err := r.Update(ctx, &a); err != nil {
			return ctrl.Result{}, err
		}
	}
	st := a.Status.DeepCopy()
	st.ObservedGeneration = a.Generation
	if a.Spec.Suspend {
		st.Phase, st.Message = v1alpha1.AddonSuspended, "suspended: objects are left as they are"
		return ctrl.Result{}, r.saveStatus(ctx, &a, st)
	}
	res, err := r.Renderer.Render(ctx, &a)
	if err != nil {
		st.Phase, st.Message = v1alpha1.AddonError, err.Error()
		_ = r.saveStatus(ctx, &a, st)
		return ctrl.Result{RequeueAfter: addonResync}, nil
	}
	objs, err := r.prepare(&a, res.Objects)
	if err != nil {
		st.Phase, st.Message = v1alpha1.AddonError, err.Error()
		_ = r.saveStatus(ctx, &a, st)
		return ctrl.Result{RequeueAfter: addonResync}, nil
	}
	st.Revision, st.SkippedHooks = res.Revision, res.SkippedHooks

	preview, err := r.preview(ctx, &a, objs)
	if err != nil {
		st.Phase, st.Message = v1alpha1.AddonError, "preview: "+err.Error()
		_ = r.saveStatus(ctx, &a, st)
		return ctrl.Result{RequeueAfter: addonResync}, nil
	}
	st.Preview = preview
	pending := preview.Create + preview.Update + preview.Prune

	switch {
	case a.Spec.Adopt && !st.Adopted && preview.Update > 0 && !a.Spec.AllowAdoptChanges:
		st.Phase = v1alpha1.AddonBlocked
		st.Message = fmt.Sprintf("taking over would change %d existing object(s); review the preview, then allow it or fix the values", preview.Update)
		return ctrl.Result{RequeueAfter: addonResync}, r.saveStatus(ctx, &a, st)
	case a.Spec.ManualSync && pending > 0 && a.Spec.SyncRequest <= st.AppliedSyncRequest:
		st.Phase = v1alpha1.AddonOutOfSync
		st.Message = fmt.Sprintf("%d to create, %d to update, %d to prune: waiting for a manual sync", preview.Create, preview.Update, preview.Prune)
		return ctrl.Result{RequeueAfter: addonResync}, r.saveStatus(ctx, &a, st)
	}

	if err := r.applyAll(ctx, objs); err != nil {
		st.Phase, st.Message = v1alpha1.AddonError, err.Error()
		_ = r.saveStatus(ctx, &a, st)
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}
	inventory := refs(objs)
	if a.Spec.Prune {
		if err := r.prune(ctx, st.Objects, inventory); err != nil {
			st.Phase, st.Message = v1alpha1.AddonError, "prune: "+err.Error()
			_ = r.saveStatus(ctx, &a, st)
			return ctrl.Result{RequeueAfter: time.Minute}, nil
		}
	}
	now := metav1.Now()
	st.Objects, st.LastSynced, st.Adopted = inventory, &now, true
	st.AppliedSyncRequest = a.Spec.SyncRequest
	st.Phase = v1alpha1.AddonSynced
	st.Message = fmt.Sprintf("%d objects in sync", len(inventory))
	if pending > 0 {
		st.Message = fmt.Sprintf("applied: %d created, %d updated, %d pruned; %d objects in sync", preview.Create, preview.Update, preview.Prune, len(inventory))
	}
	// What is live now matches; keep only the counts of what was done.
	st.Preview = &v1alpha1.Preview{Unchanged: len(inventory)}
	return ctrl.Result{RequeueAfter: addonResync}, r.saveStatus(ctx, &a, st)
}

// prepare sets the namespace of namespaced objects that have none and
// labels every object with the add-on's name.
func (r *AddonReconciler) prepare(a *v1alpha1.Addon, in []*unstructured.Unstructured) ([]*unstructured.Unstructured, error) {
	bundled := map[schema.GroupKind]bool{} // kinds defined by CRDs in this add-on → namespaced?
	for _, o := range in {
		if o.GetKind() != "CustomResourceDefinition" {
			continue
		}
		var crd apiextensionsv1.CustomResourceDefinition
		if err := fromUnstructured(o, &crd); err == nil {
			bundled[schema.GroupKind{Group: crd.Spec.Group, Kind: crd.Spec.Names.Kind}] = crd.Spec.Scope == apiextensionsv1.NamespaceScoped
		}
	}
	var out []*unstructured.Unstructured
	seen := map[string]bool{}
	hasNS := false
	for _, o := range in {
		o = o.DeepCopy()
		gvk := o.GroupVersionKind()
		namespaced, known := bundled[gvk.GroupKind()]
		if !known {
			m, err := r.Target.RESTMapper().RESTMapping(gvk.GroupKind(), gvk.Version)
			if err != nil {
				return nil, fmt.Errorf("%s %s: unknown kind (is its CRD installed?): %w", gvk.Kind, o.GetName(), err)
			}
			namespaced = m.Scope.Name() == meta.RESTScopeNameNamespace
		}
		if namespaced && o.GetNamespace() == "" {
			o.SetNamespace(a.Spec.Namespace)
		}
		if !namespaced {
			o.SetNamespace("")
		}
		if o.GetKind() == "Namespace" && o.GetName() == a.Spec.Namespace {
			hasNS = true
		}
		labels := o.GetLabels()
		if labels == nil {
			labels = map[string]string{}
		}
		labels[LabelAddon] = a.Name
		o.SetLabels(labels)
		key := refKey(ref(o))
		if seen[key] {
			return nil, fmt.Errorf("%s is rendered twice", key)
		}
		seen[key] = true
		out = append(out, o)
	}
	if a.Spec.CreateNamespace && !hasNS && a.Spec.Namespace != "" {
		ns := &unstructured.Unstructured{}
		ns.SetAPIVersion("v1")
		ns.SetKind("Namespace")
		ns.SetName(a.Spec.Namespace)
		ns.SetLabels(map[string]string{LabelAddon: a.Name})
		out = append([]*unstructured.Unstructured{ns}, out...)
	}
	sort.SliceStable(out, func(i, j int) bool { return applyRank(out[i]) < applyRank(out[j]) })
	return out, nil
}

// applyRank orders CRDs and namespaces before the objects that need them.
func applyRank(o *unstructured.Unstructured) int {
	switch o.GetKind() {
	case "CustomResourceDefinition":
		return 0
	case "Namespace":
		return 1
	}
	return 2
}

// preview dry-runs every object and records what a sync would change.
func (r *AddonReconciler) preview(ctx context.Context, a *v1alpha1.Addon, objs []*unstructured.Unstructured) (*v1alpha1.Preview, error) {
	p := &v1alpha1.Preview{}
	add := func(it v1alpha1.PreviewItem) {
		if len(p.Items) < 100 {
			p.Items = append(p.Items, it)
		}
	}
	for _, o := range objs {
		live := &unstructured.Unstructured{}
		live.SetGroupVersionKind(o.GroupVersionKind())
		err := r.Target.Get(ctx, client.ObjectKeyFromObject(o), live)
		switch {
		case apierrors.IsNotFound(err) || meta.IsNoMatchError(err):
			p.Create++
			add(v1alpha1.PreviewItem{ObjectRef: ref(o), Action: "create"})
			continue
		case err != nil:
			return nil, fmt.Errorf("get %s: %w", refKey(ref(o)), err)
		}
		dry := o.DeepCopy()
		if err := r.Target.Patch(ctx, dry, client.Apply, addonFieldOwner, client.ForceOwnership, client.DryRunAll); err != nil {
			return nil, fmt.Errorf("dry run %s: %w", refKey(ref(o)), err)
		}
		before, after := normalize(live), normalize(dry)
		if before == after {
			p.Unchanged++
			continue
		}
		p.Update++
		add(v1alpha1.PreviewItem{ObjectRef: ref(o), Action: "update", Diff: unifiedDiff(before, after)})
	}
	if a.Spec.Prune {
		current := map[string]bool{}
		for _, o := range objs {
			current[refKey(ref(o))] = true
		}
		for _, old := range a.Status.Objects {
			if !current[refKey(old)] && !neverDelete[old.Kind] {
				p.Prune++
				add(v1alpha1.PreviewItem{ObjectRef: old, Action: "prune"})
			}
		}
	}
	return p, nil
}

// normalize renders an object without the fields that always differ
// between a live object and a dry run, or that rendimiento adds itself.
func normalize(o *unstructured.Unstructured) string {
	c := o.DeepCopy()
	delete(c.Object, "status")
	for _, f := range []string{"managedFields", "resourceVersion", "generation", "uid", "creationTimestamp", "selfLink"} {
		unstructured.RemoveNestedField(c.Object, "metadata", f)
	}
	if labels := c.GetLabels(); labels != nil {
		delete(labels, LabelAddon)
		if len(labels) == 0 {
			labels = nil // no labels left: same as none at all
		}
		c.SetLabels(labels)
	}
	b, _ := yaml.Marshal(c.Object)
	return string(b)
}

func unifiedDiff(a, b string) string {
	d, _ := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A: difflib.SplitLines(a), B: difflib.SplitLines(b), FromFile: "live", ToFile: "desired", Context: 2,
	})
	if len(d) > 3000 {
		d = d[:3000] + "\n… (diff truncated)"
	}
	return d
}

// applyAll server-side applies the objects in order, waiting for CRDs to
// be established before the objects that use them.
func (r *AddonReconciler) applyAll(ctx context.Context, objs []*unstructured.Unstructured) error {
	for i, o := range objs {
		if err := r.Target.Patch(ctx, o.DeepCopy(), client.Apply, addonFieldOwner, client.ForceOwnership); err != nil {
			return fmt.Errorf("apply %s: %w", refKey(ref(o)), err)
		}
		last := i == len(objs)-1
		if o.GetKind() == "CustomResourceDefinition" && (last || objs[i+1].GetKind() != "CustomResourceDefinition") {
			if err := r.waitCRDs(ctx, objs[:i+1]); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *AddonReconciler) waitCRDs(ctx context.Context, objs []*unstructured.Unstructured) error {
	deadline := time.Now().Add(30 * time.Second)
	for _, o := range objs {
		if o.GetKind() != "CustomResourceDefinition" {
			continue
		}
		for {
			live := &unstructured.Unstructured{}
			live.SetGroupVersionKind(o.GroupVersionKind())
			if err := r.Target.Get(ctx, client.ObjectKey{Name: o.GetName()}, live); err == nil && established(live) {
				break
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("CRD %s not established after 30s", o.GetName())
			}
			time.Sleep(time.Second)
		}
	}
	return nil
}

func established(crd *unstructured.Unstructured) bool {
	conds, _, _ := unstructured.NestedSlice(crd.Object, "status", "conditions")
	for _, c := range conds {
		m, _ := c.(map[string]any)
		if m["type"] == "Established" && m["status"] == "True" {
			return true
		}
	}
	return false
}

// prune deletes objects that were in the previous inventory but are no
// longer rendered, except kinds that must never be deleted.
func (r *AddonReconciler) prune(ctx context.Context, previous, current []v1alpha1.ObjectRef) error {
	if len(current) == 0 && len(previous) > 0 {
		return errors.New("the source rendered nothing; refusing to prune everything")
	}
	keep := map[string]bool{}
	for _, c := range current {
		keep[refKey(c)] = true
	}
	for _, old := range previous {
		if keep[refKey(old)] || neverDelete[old.Kind] {
			continue
		}
		if err := r.deleteRef(ctx, old); err != nil {
			return err
		}
		log.FromContext(ctx).Info("addon: pruned", "object", refKey(old))
	}
	return nil
}

func (r *AddonReconciler) deleteRef(ctx context.Context, x v1alpha1.ObjectRef) error {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(schema.GroupVersionKind{Group: x.Group, Version: x.Version, Kind: x.Kind})
	u.SetNamespace(x.Namespace)
	u.SetName(x.Name)
	err := r.Target.Delete(ctx, u, client.PropagationPolicy(metav1.DeletePropagationBackground))
	if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
		return nil
	}
	return err
}

// finalize runs when an Addon is deleted: by default everything it
// installed keeps running; with the uninstall annotation it is removed
// (except kinds that must never be deleted).
func (r *AddonReconciler) finalize(ctx context.Context, a *v1alpha1.Addon) error {
	if !controllerutil.ContainsFinalizer(a, addonFinalizer) {
		return nil
	}
	if a.Annotations[AnnotationUninstall] == "true" {
		for _, x := range a.Status.Objects {
			if neverDelete[x.Kind] {
				continue
			}
			if err := r.deleteRef(ctx, x); err != nil {
				return err
			}
		}
		log.FromContext(ctx).Info("addon: uninstalled", "addon", a.Name, "objects", len(a.Status.Objects))
	}
	controllerutil.RemoveFinalizer(a, addonFinalizer)
	return r.Update(ctx, a)
}

func (r *AddonReconciler) saveStatus(ctx context.Context, a *v1alpha1.Addon, st *v1alpha1.AddonStatus) error {
	a.Status = *st
	return r.Status().Update(ctx, a)
}

func ref(o *unstructured.Unstructured) v1alpha1.ObjectRef {
	gvk := o.GroupVersionKind()
	return v1alpha1.ObjectRef{Group: gvk.Group, Version: gvk.Version, Kind: gvk.Kind, Namespace: o.GetNamespace(), Name: o.GetName()}
}

// refKey identifies an object regardless of API version.
func refKey(x v1alpha1.ObjectRef) string {
	return x.Group + "/" + x.Kind + "/" + x.Namespace + "/" + x.Name
}

func refs(objs []*unstructured.Unstructured) []v1alpha1.ObjectRef {
	out := make([]v1alpha1.ObjectRef, 0, len(objs))
	for _, o := range objs {
		out = append(out, ref(o))
	}
	sort.Slice(out, func(i, j int) bool { return refKey(out[i]) < refKey(out[j]) })
	return out
}

func fromUnstructured(u *unstructured.Unstructured, into any) error {
	b, err := u.MarshalJSON()
	if err != nil {
		return err
	}
	return yaml.Unmarshal(b, into)
}

func (r *AddonReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.Addon{}, builder.WithPredicates(predicate.Or(predicate.GenerationChangedPredicate{}, predicate.AnnotationChangedPredicate{}))).
		Named("addon").
		Complete(r)
}


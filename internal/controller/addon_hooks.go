package controller

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/p0dxD/rendimiento.ai/api/v1alpha1"
	"github.com/p0dxD/rendimiento.ai/internal/addon"
)

// lifecycle returns what this sync is in Helm's terms: "install" for the
// first sync of a new add-on, "upgrade" when the chart, version, values or
// release name changed since the last apply, and "" otherwise (an adoption
// of something already installed, or a resync with nothing new).
func lifecycle(a *v1alpha1.Addon, res *addon.Result) string {
	if a.Spec.SkipHooks || len(res.Hooks) == 0 {
		return ""
	}
	switch {
	case !a.Status.Adopted && !a.Spec.Adopt:
		return "install"
	case a.Status.Adopted && a.Status.AppliedHash != "" && a.Status.AppliedHash != res.Hash:
		return "upgrade"
	}
	return ""
}

// runHooks runs, in weight order, every hook for event (e.g. "post-upgrade"),
// the way Helm does: an existing hook object is replaced
// (before-hook-creation), Jobs and Pods are waited for, and the delete
// policies apply afterwards. It stops at the first failure.
func (r *AddonReconciler) runHooks(ctx context.Context, a *v1alpha1.Addon, st *v1alpha1.AddonStatus, hooks []addon.Hook, event string) error {
	timeout := time.Duration(a.Spec.HookTimeout) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	for _, h := range hooks {
		if !h.Has(event) {
			continue
		}
		obj, err := r.prepareOne(a, h.Object, map[schema.GroupKind]bool{})
		if err != nil {
			return err
		}
		if h.HookPolicy("before-hook-creation") {
			if err := r.deleteAndWait(ctx, obj); err != nil {
				return fmt.Errorf("%s hook %s: remove the previous run: %w", event, h.Name, err)
			}
		}
		if err := r.Target.Patch(ctx, obj.DeepCopy(), client.Apply, addonFieldOwner, client.ForceOwnership); err != nil {
			return r.hookDone(ctx, st, h, obj, event, fmt.Errorf("create: %w", err))
		}
		log.FromContext(ctx).Info("addon: running hook", "addon", a.Name, "event", event, "hook", h.Name)
		if err := r.hookDone(ctx, st, h, obj, event, r.waitHook(ctx, obj, timeout)); err != nil {
			return err
		}
	}
	return nil
}

// hookDone records a hook's outcome and applies its delete policy.
func (r *AddonReconciler) hookDone(ctx context.Context, st *v1alpha1.AddonStatus, h addon.Hook, obj *unstructured.Unstructured, event string, runErr error) error {
	run := v1alpha1.HookRun{Event: event, Name: h.Name, Kind: h.Kind, Status: "succeeded", Finished: metav1.Now()}
	if runErr != nil {
		run.Status, run.Message = "failed", runErr.Error()
	}
	st.HookRuns = append(st.HookRuns, run)
	if n := len(st.HookRuns); n > 20 {
		st.HookRuns = st.HookRuns[n-20:]
	}
	if (runErr == nil && h.HookPolicy("hook-succeeded")) || (runErr != nil && h.HookPolicy("hook-failed")) {
		_ = r.Target.Delete(ctx, obj, client.PropagationPolicy(metav1.DeletePropagationBackground))
	}
	if runErr != nil {
		return fmt.Errorf("%s hook %s failed: %w", event, h.Name, runErr)
	}
	return nil
}

// waitHook waits for a Job to complete or a Pod to succeed; other kinds
// (a ConfigMap or ServiceAccount a hook Job uses) are done once created.
func (r *AddonReconciler) waitHook(ctx context.Context, obj *unstructured.Unstructured, timeout time.Duration) error {
	kind := obj.GetKind()
	if kind != "Job" && kind != "Pod" {
		return nil
	}
	deadline := time.Now().Add(timeout)
	for {
		live := &unstructured.Unstructured{}
		live.SetGroupVersionKind(obj.GroupVersionKind())
		if err := r.Target.Get(ctx, client.ObjectKeyFromObject(obj), live); err != nil {
			return err
		}
		if kind == "Pod" {
			switch phase, _, _ := unstructured.NestedString(live.Object, "status", "phase"); phase {
			case "Succeeded":
				return nil
			case "Failed":
				return fmt.Errorf("pod failed")
			}
		} else {
			conds, _, _ := unstructured.NestedSlice(live.Object, "status", "conditions")
			for _, c := range conds {
				m, _ := c.(map[string]any)
				if m["status"] != "True" {
					continue
				}
				switch m["type"] {
				case "Complete":
					return nil
				case "Failed":
					msg, _ := m["message"].(string)
					return fmt.Errorf("job failed: %s", msg)
				}
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("did not finish within %s", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// deleteAndWait removes an object (and a Job's pods) and waits until it is gone.
func (r *AddonReconciler) deleteAndWait(ctx context.Context, obj *unstructured.Unstructured) error {
	// Background: the object goes at once (its pods are cleaned up after),
	// so a new hook of the same name can be created without waiting on the
	// garbage collector.
	err := r.Target.Delete(ctx, obj.DeepCopy(), client.PropagationPolicy(metav1.DeletePropagationBackground))
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		live := &unstructured.Unstructured{}
		live.SetGroupVersionKind(obj.GroupVersionKind())
		if err := r.Target.Get(ctx, client.ObjectKeyFromObject(obj), live); apierrors.IsNotFound(err) {
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("%s %s still exists after 2m", obj.GetKind(), obj.GetName())
}

// hookInfo is what the add-on's status shows of its Helm hooks.
func hookInfo(hooks []addon.Hook) []v1alpha1.HookInfo {
	var out []v1alpha1.HookInfo
	for _, h := range hooks {
		out = append(out, v1alpha1.HookInfo{Name: h.Name, Kind: h.Kind, Events: h.Events})
	}
	return out
}

// countHooks is how many hooks run on event (install, upgrade, delete).
func countHooks(hooks []addon.Hook, event string) int {
	n := 0
	for _, h := range hooks {
		if h.Has(event) {
			n++
		}
	}
	return n
}

package controller

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/rendimiento-ai/rendimiento/api/v1alpha1"
)

// The controller wakes for a person's annotations, not for its own record
// of the pre hooks it ran.
func TestPeopleAnnotationsChanged(t *testing.T) {
	addon := func(ann map[string]string) *v1alpha1.Addon {
		return &v1alpha1.Addon{ObjectMeta: metav1.ObjectMeta{Name: "a", Annotations: ann}}
	}
	changed := func(old, new map[string]string) bool {
		return peopleAnnotationsChanged.Update(event.UpdateEvent{ObjectOld: addon(old), ObjectNew: addon(new)})
	}
	if changed(nil, map[string]string{annotationPreHooksDone: "upgrade:abc"}) {
		t.Error("woke for its own pre-hooks record")
	}
	if changed(map[string]string{annotationPreHooksDone: "install:1"}, map[string]string{annotationPreHooksDone: "upgrade:2"}) {
		t.Error("woke for a new pre-hooks record")
	}
	if !changed(nil, map[string]string{AnnotationPaused: "true"}) {
		t.Error("did not wake for a person pausing it")
	}
	if !changed(map[string]string{annotationPreHooksDone: "x", "poke": "1"}, map[string]string{annotationPreHooksDone: "x", "poke": "2"}) {
		t.Error("did not wake for a person's annotation beside the record")
	}
}

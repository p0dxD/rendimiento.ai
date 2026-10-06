package environment

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func lh(kind, name string, labels map[string]string, spec, status map[string]any) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{"spec": spec, "status": status}}
	u.SetGroupVersionKind(longhorn.WithKind(kind))
	u.SetNamespace("longhorn-system")
	u.SetName(name)
	u.SetLabels(labels)
	return u
}

func lhVolume(name, ns, pvc, state, lastBackup string, labels map[string]string) *unstructured.Unstructured {
	return lh("Volume", name, labels, map[string]any{}, map[string]any{
		"state": state, "lastBackupAt": lastBackup,
		"kubernetesStatus": map[string]any{"namespace": ns, "pvcName": pvc},
	})
}

func TestCheckBackups(t *testing.T) {
	recent := time.Now().Add(-6 * time.Hour).UTC().Format(time.RFC3339)
	old := time.Now().Add(-72 * time.Hour).UTC().Format(time.RFC3339)
	group := func(g string) map[string]string {
		return map[string]string{"recurring-job-group.longhorn.io/" + g: "enabled"}
	}
	objs := []client.Object{
		lh("RecurringJob", "backup-nightly", nil, map[string]any{"task": "backup", "groups": []any{"backup-nightly"}}, nil),
		lh("RecurringJob", "trim", nil, map[string]any{"task": "filesystem-trim", "groups": []any{"default"}}, nil),
		lh("BackupTarget", "default", nil, map[string]any{"backupTargetURL": "s3://backups@us-east-1/"}, map[string]any{"available": true}),
		lhVolume("pvc-1", "shop", "postgres-data", "attached", recent, group("backup-nightly")),
		lhVolume("pvc-2", "shop", "uploads", "attached", old, group("backup-nightly")),
		lhVolume("pvc-3", "grafana", "grafana", "attached", "", group("default")),
		lhVolume("pvc-4", "old", "unused", "detached", "", nil),
		lhVolume("pvc-5", "registry", "registry", "attached", "", nil),
		&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Namespace: "registry", Name: "registry", Labels: map[string]string{LabelBackupSkip: "skip"}}},
	}
	c := &Checker{Kube: fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(objs...).Build()}
	ch := c.checkBackups(context.Background())
	if ch.Status != Warning || !strings.Contains(ch.Summary, "2 volumes backed up") || !strings.Contains(ch.Summary, "1 in use without") {
		t.Fatalf("status %s: %s", ch.Status, ch.Summary)
	}
	d := strings.Join(ch.Details, "\n")
	for _, want := range []string{"in use, not backed up: grafana/grafana", "shop/uploads (last backup:", "not in use and not backed up: old/unused", "no backup on purpose: registry/registry"} {
		if !strings.Contains(d, want) {
			t.Errorf("details missing %q:\n%s", want, d)
		}
	}
	if strings.Contains(d, "shop/postgres-data") || ch.Fix == "" {
		t.Errorf("a recent backup is fine, and an uncovered volume needs a fix:\n%s\nfix: %s", d, ch.Fix)
	}
}

func TestCheckBackupsWithoutLonghorn(t *testing.T) {
	c := &Checker{Kube: fake.NewClientBuilder().WithScheme(newScheme()).Build()}
	if ch := c.checkBackups(context.Background()); ch.Status != Missing && ch.Status != OK {
		t.Errorf("status %s: %s", ch.Status, ch.Summary)
	}
}

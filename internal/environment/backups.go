package environment

import (
	"context"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	. "github.com/p0dxD/rendimiento.ai/internal/i18n" //nolint:revive // M marks messages for people
)

// LabelBackupSkip on a volume claim says it needs no backup on purpose (a
// cache, an image registry, the backup store itself), so the check stays quiet.
const LabelBackupSkip = "rendimiento.ai/backup"

// backupStale is how old a volume's last backup may be before it is reported.
const backupStale = 48 * time.Hour

var longhorn = schema.GroupVersion{Group: "longhorn.io", Version: "v1beta2"}

func longhornList(ctx context.Context, c *Checker, kind string) ([]unstructured.Unstructured, error) {
	u := &unstructured.UnstructuredList{}
	u.SetGroupVersionKind(longhorn.WithKind(kind + "List"))
	if err := c.Kube.List(ctx, u); err != nil {
		return nil, err
	}
	return u.Items, nil
}

// checkBackups reports which Longhorn volumes a backup job covers, which
// volumes in use are not covered, and whose last backup is too old.
func (c *Checker) checkBackups(ctx context.Context) Check {
	ch := Check{ID: "backups", Name: M("Volume backups"), Category: CatCluster}
	jobs, err := longhornList(ctx, c, "RecurringJob")
	if apimeta.IsNoMatchError(err) {
		ch.Status, ch.Summary = Missing, M("no Longhorn: volume backups are not checked")
		return ch
	}
	if err != nil {
		ch.Status, ch.Summary = Error, err.Error()
		return ch
	}
	backupJobs, backupGroups := map[string]bool{}, map[string]bool{}
	for _, j := range jobs {
		task, _, _ := unstructured.NestedString(j.Object, "spec", "task")
		if !strings.HasPrefix(task, "backup") {
			continue
		}
		backupJobs[j.GetName()] = true
		groups, _, _ := unstructured.NestedStringSlice(j.Object, "spec", "groups")
		for _, g := range groups {
			backupGroups[g] = true
		}
	}
	if targets, err := longhornList(ctx, c, "BackupTarget"); err == nil {
		for _, t := range targets {
			url, _, _ := unstructured.NestedString(t.Object, "spec", "backupTargetURL")
			ok, _, _ := unstructured.NestedBool(t.Object, "status", "available")
			if url != "" && !ok {
				ch.Details = append(ch.Details, M("backup target %s is not available", url))
			}
		}
	}

	volumes, err := longhornList(ctx, c, "Volume")
	if err != nil {
		ch.Status, ch.Summary = Error, err.Error()
		return ch
	}
	var claims corev1.PersistentVolumeClaimList
	if err := c.Kube.List(ctx, &claims); err != nil {
		ch.Status, ch.Summary = Error, err.Error()
		return ch
	}
	skip := map[string]bool{}
	for _, pvc := range claims.Items {
		if pvc.Labels[LabelBackupSkip] == "skip" {
			skip[pvc.Namespace+"/"+pvc.Name] = true
		}
	}

	var uncovered, idle, stale, skipped []string
	covered, inUse := 0, 0
	for _, v := range volumes {
		ns, _, _ := unstructured.NestedString(v.Object, "status", "kubernetesStatus", "namespace")
		pvc, _, _ := unstructured.NestedString(v.Object, "status", "kubernetesStatus", "pvcName")
		name := v.GetName()
		if pvc != "" {
			name = ns + "/" + pvc
		}
		state, _, _ := unstructured.NestedString(v.Object, "status", "state")
		attached := state == "attached"
		if attached {
			inUse++
		}
		if skip[name] {
			skipped = append(skipped, name)
			continue
		}
		backedUp := false
		for k, val := range v.GetLabels() {
			if val != "enabled" {
				continue
			}
			if g, ok := strings.CutPrefix(k, "recurring-job-group.longhorn.io/"); ok && backupGroups[g] {
				backedUp = true
			}
			if j, ok := strings.CutPrefix(k, "recurring-job.longhorn.io/"); ok && backupJobs[j] {
				backedUp = true
			}
		}
		switch {
		case backedUp:
			covered++
			last, _, _ := unstructured.NestedString(v.Object, "status", "lastBackupAt")
			if at, err := time.Parse(time.RFC3339, last); attached && (err != nil || time.Since(at) > backupStale) {
				when := M("never")
				if err == nil {
					when = at.Format("2006-01-02 15:04")
				}
				stale = append(stale, M("%s (last backup: %s)", name, when))
			}
		case attached:
			uncovered = append(uncovered, name)
		default:
			idle = append(idle, name)
		}
	}
	sort.Strings(uncovered)
	sort.Strings(idle)
	sort.Strings(stale)
	sort.Strings(skipped)

	ch.Summary = M("%d volumes backed up by a nightly job, %d in use without a backup", covered, len(uncovered))
	switch {
	case len(uncovered) > 0 || len(stale) > 0 || len(ch.Details) > 0:
		ch.Status = Warning
	default:
		ch.Status = OK
	}
	if len(uncovered) > 0 {
		ch.Details = append(ch.Details, M("in use, not backed up: %s", strings.Join(uncovered, ", ")))
		ch.Fix = M("Label the volume claim with %s and %s to back it up nightly, or with %s if it doesn't need a backup.",
			"recurring-job.longhorn.io/source=enabled", "recurring-job-group.longhorn.io/<group>=enabled", LabelBackupSkip+"=skip")
	}
	if len(stale) > 0 {
		ch.Details = append(ch.Details, M("backups older than two days: %s", strings.Join(stale, ", ")))
	}
	if len(idle) > 0 {
		ch.Details = append(ch.Details, M("not in use and not backed up: %s", strings.Join(idle, ", ")))
	}
	if len(skipped) > 0 {
		ch.Details = append(ch.Details, M("no backup on purpose: %s", strings.Join(skipped, ", ")))
	}
	return ch
}

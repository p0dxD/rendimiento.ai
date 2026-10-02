package platform

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	corev1 "k8s.io/api/core/v1"

	"github.com/p0dxD/rendimiento.ai/internal/spec"
	"github.com/p0dxD/rendimiento.ai/internal/store"
	"github.com/p0dxD/rendimiento.ai/internal/uptime"
)

func TestTaskJob(t *testing.T) {
	rel := &store.Release{Number: 7, SHA: "abc123"}
	base := &corev1.Container{
		Image:        "reg/shop-api@sha256:1",
		Env:          []corev1.EnvVar{{Name: "DATABASE_URL", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "postgres-credentials"}, Key: "uri"}}}},
		EnvFrom:      []corev1.EnvFromSource{{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "shop-env"}}}},
		VolumeMounts: []corev1.VolumeMount{{Name: "data", MountPath: "/data"}, {Name: "creds", MountPath: "/etc/creds"}},
	}
	volumes := []corev1.Volume{
		{Name: "data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "api-data"}}},
		{Name: "creds", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: "creds"}}},
	}
	task := spec.Task{Name: "migrate", Stage: spec.StagePostDeploy, Service: "api", Command: "./migrate up", Size: spec.SizeMedium,
		Env: map[string]string{"MODE": "deploy"}, SecretEnv: map[string]string{"TOKEN": "tok/value"}, Timeout: 120}
	job := taskJob("shop", rel, task, base, volumes, "x1y2z")
	c := job.Spec.Template.Spec.Containers[0]
	env := map[string]corev1.EnvVar{}
	for _, e := range c.Env {
		env[e.Name] = e
	}
	if job.Namespace != "shop" || job.Name != "post-migrate-r7-x1y2z" || c.Image != base.Image || strings.Join(c.Command, " ") != "sh -c ./migrate up" {
		t.Fatalf("job %s/%s image %s command %v", job.Namespace, job.Name, c.Image, c.Command)
	}
	if env["DATABASE_URL"].ValueFrom == nil || env["RENDIMIENTO_RELEASE"].Value != "7" || env["MODE"].Value != "deploy" ||
		env["TOKEN"].ValueFrom.SecretKeyRef.Name != "tok" || len(c.EnvFrom) != 1 {
		t.Fatalf("env = %+v envFrom %+v", c.Env, c.EnvFrom)
	}
	if v := job.Spec.Template.Spec.Volumes; len(v) != 1 || v[0].Name != "creds" || len(c.VolumeMounts) != 1 {
		t.Fatalf("volumes %+v mounts %+v: secret files come along, the data volume must not", v, c.VolumeMounts)
	}
	if *job.Spec.BackoffLimit != 0 || *job.Spec.ActiveDeadlineSeconds != 120 || job.Labels["rendimiento.ai/release"] != "7" || *job.Spec.Template.Spec.AutomountServiceAccountToken {
		t.Fatalf("job spec = %+v", job.Spec)
	}
	if c.Resources.Limits.Memory().String() != "512Mi" {
		t.Fatalf("limits = %v", c.Resources.Limits)
	}
}

// A required post-deploy task that fails fails verification and rolls the
// release back; an optional one is only reported; one waiting on a failed
// task is skipped.
func TestPostDeployVerification(t *testing.T) {
	p, fake, _, _ := setup(t)
	ctx := context.Background()
	failing := map[string]bool{}
	p.Verify = VerifySettings{
		Window: 100 * time.Millisecond, Every: 20 * time.Millisecond,
		Rollout: func(context.Context, string, int64) (RolloutState, string, error) { return RolloutHealthy, "", nil },
		Check: func(_ context.Context, tg uptime.Target) store.Probe {
			return store.Probe{AppID: tg.AppID, Service: tg.Service, Kind: tg.Kind, OK: true, At: time.Now()}
		},
		RunTask: func(_ context.Context, _ *store.App, _ *store.Release, task spec.Task) store.ReleaseTask {
			if failing[task.Name] {
				return store.ReleaseTask{Status: store.TaskFailed, Message: "exit 1 · relation \"orders\" does not exist", Log: "running " + task.Name + "\n"}
			}
			return store.ReleaseTask{Status: store.TaskSucceeded, Log: "ok\n"}
		},
	}
	yaml := `services:
  - name: web
    health: { path: /health }
tasks:
  - { name: migrate, stage: post-deploy, service: web, command: ./migrate }
  - { name: smoke, stage: post-deploy, image: curl, command: curl -f http://web, after: [migrate] }
  - { name: report, stage: post-deploy, image: curl, command: ./report, optional: true }
`
	sha := func(c byte) string { return strings.Repeat(string(c), 40) }
	tree := fstest.MapFS{"rendimiento.yaml": {Data: []byte(yaml)}}
	for _, c := range "ab" {
		fake.files[sha(byte(c))] = tree
	}
	fake.files["main"] = tree
	sp, err := spec.Parse([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Onboard(ctx, OnboardRequest{Installation: 7, Repo: "p0dxD/shop", DefaultBranch: "main", Name: "shop", Spec: *sp}, false); err != nil {
		t.Fatal(err)
	}
	app, _ := p.Store.GetApp(ctx, "shop")
	push := func(c byte) {
		t.Helper()
		runs, err := p.HandlePush(ctx, PushEvent{Installation: 7, Repo: "p0dxD/shop", Branch: "main", SHA: sha(c)})
		if err != nil || len(runs) != 1 {
			t.Fatalf("push: %v %v", runs, err)
		}
		waitRun(t, p, runs[0].ID)
	}
	verified := func(number int64) *store.Release {
		t.Helper()
		for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
			if r, err := p.Store.GetRelease(ctx, app.ID, number); err == nil && r.VerifyStatus != "" && r.VerifyStatus != store.VerifyRunning {
				return r
			}
		}
		t.Fatalf("release %d was not verified", number)
		return nil
	}
	tasksOf := func(r *store.Release) map[string]store.ReleaseTask {
		m, _ := p.Store.ReleaseTasks(ctx, []int64{r.ID})
		out := map[string]store.ReleaseTask{}
		for _, t := range m[r.ID] {
			out[t.Name] = t
		}
		return out
	}

	// 1. All pass, except the optional report: verified, with a warning.
	failing["report"] = true
	push('a')
	r1 := verified(1)
	if r1.VerifyStatus != store.VerifyPassed || !strings.Contains(r1.VerifyMessage, "post-deploy task report failed") {
		t.Fatalf("release 1: %s %q", r1.VerifyStatus, r1.VerifyMessage)
	}
	if ts := tasksOf(r1); ts["migrate"].Status != store.TaskSucceeded || ts["smoke"].Status != store.TaskSucceeded || ts["report"].Status != store.TaskFailed {
		t.Fatalf("release 1 tasks = %+v", ts)
	}
	// 2. The migration fails: smoke is skipped, and the release is rolled back.
	failing["migrate"] = true
	push('b')
	r2 := verified(2)
	if r2.VerifyStatus != store.VerifyFailed || !strings.Contains(r2.VerifyMessage, "post-deploy task migrate failed") || !strings.Contains(r2.VerifyMessage, "rolled back to release #1") {
		t.Fatalf("release 2: %s %q", r2.VerifyStatus, r2.VerifyMessage)
	}
	ts := tasksOf(r2)
	if ts["smoke"].Status != store.TaskSkipped || !strings.Contains(ts["smoke"].Message, "migrate did not succeed") {
		t.Fatalf("release 2 tasks = %+v", ts)
	}
	if log, err := p.Store.ReleaseTaskLog(ctx, app.ID, 2, "migrate"); err != nil || log != "running migrate\n" {
		t.Fatalf("task log = %q, %v", log, err)
	}
}

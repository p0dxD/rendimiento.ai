package destino

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/p0dxD/rendimiento.ai/internal/spec"
	"github.com/p0dxD/rendimiento.ai/internal/store"
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

func TestPreDeployJobUsesTheNewImage(t *testing.T) {
	rel := &store.Release{Number: 8, Images: map[string]string{"api": "reg/shop-api@sha256:NEW"}}
	base := &corev1.Container{Image: "reg/shop-api@sha256:OLD", Env: []corev1.EnvVar{{Name: "DATABASE_URL", Value: "x"}}}
	job := taskJob("shop", rel, spec.Task{Name: "migrate", Stage: spec.StagePreDeploy, Service: "api", Command: "./migrate"}, base, nil, "abcde")
	c := job.Spec.Template.Spec.Containers[0]
	if c.Image != "reg/shop-api@sha256:NEW" || c.Env[0].Name != "DATABASE_URL" || job.Name != "pre-migrate-r8-abcde" || job.Labels["rendimiento.ai/stage"] != "pre-deploy" {
		t.Fatalf("pre-deploy job: image %s env %v name %s labels %v", c.Image, c.Env, job.Name, job.Labels)
	}
	post := taskJob("shop", rel, spec.Task{Name: "smoke", Stage: spec.StagePostDeploy, Service: "api", Command: "./smoke"}, base, nil, "abcde")
	if post.Spec.Template.Spec.Containers[0].Image != "reg/shop-api@sha256:OLD" {
		t.Fatal("a post-deploy job runs the live (already new) Deployment's image as is")
	}
}

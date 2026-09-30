package pipeline

import (
	"bytes"
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/p0dxD/rendimiento.ai/internal/spec"
)

func TestPlanTasks(t *testing.T) {
	s, err := spec.Parse([]byte(`services: [{name: api, path: api}]
tasks:
  - {name: mobile, image: node:20, command: npx eas-cli build, path: mobile, secretEnv: {EXPO_TOKEN: expo/token}, timeout: 600}
  - {name: smoke, image: curl, command: curl -f x, after: [api, mobile], optional: true}
`))
	if err != nil {
		t.Fatal(err)
	}
	steps := Plan("shop", "reg", *s)
	byID := map[string]Step{}
	for _, st := range steps {
		byID[st.ID] = st
	}
	m, smoke := byID["mobile:task"], byID["smoke:task"]
	if m.Kind != KindTask || m.Service != "task:mobile" || m.App != "shop" || m.Path != "mobile" ||
		m.SecretEnv["EXPO_TOKEN"] != "expo/token" || m.Timeout.Seconds() != 600 || m.Resources.MemLimit != "512Mi" {
		t.Fatalf("mobile = %+v", m)
	}
	if strings.Join(smoke.DependsOn, ",") != "api:build,mobile:task" || !smoke.Optional {
		t.Fatalf("smoke = %+v", smoke)
	}
	if err := validate(steps); err != nil {
		t.Fatal(err)
	}
}

func TestTaskPod(t *testing.T) {
	k := &KubeExecutor{Timeout: 45 * 60e9}
	k.defaults()
	step := Step{ID: "mobile:task", Kind: KindTask, Path: "mobile", Image: "node:20", Command: "npx eas-cli build",
		App: "shop", Env: map[string]string{"NODE_ENV": "production"}, Timeout: 600e9,
		Resources: spec.Size("medium").Resources()}
	secretEnv := corev1.EnvVar{Name: "EXPO_TOKEN", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
		LocalObjectReference: corev1.LocalObjectReference{Name: "p"}, Key: "env-EXPO_TOKEN"}}}
	pod, err := k.pod("p", nil, Source{Repo: "o/r", SHA: "abc", Branch: "main"}, step, "", secretEnv)
	if err != nil {
		t.Fatal(err)
	}
	c := pod.Spec.Containers[0]
	env := map[string]corev1.EnvVar{}
	for _, e := range c.Env {
		env[e.Name] = e
	}
	if c.Image != "node:20" || c.WorkingDir != "/workspace/src/mobile" || strings.Join(c.Command, " ") != "sh -c npx eas-cli build" {
		t.Fatalf("container = %+v", c)
	}
	if env["NODE_ENV"].Value != "production" || env["RENDIMIENTO_APP"].Value != "shop" || env["GIT_BRANCH"].Value != "main" ||
		env["EXPO_TOKEN"].ValueFrom == nil {
		t.Fatalf("env = %+v", c.Env)
	}
	if *pod.Spec.ActiveDeadlineSeconds != 600 || c.Resources.Limits.Memory().String() != "512Mi" {
		t.Fatalf("deadline %d, limits %v", *pod.Spec.ActiveDeadlineSeconds, c.Resources.Limits)
	}
	if len(pod.Spec.InitContainers) != 1 || pod.Spec.InitContainers[0].Name != "clone" {
		t.Fatalf("a task pod clones and nothing else: %+v", pod.Spec.InitContainers)
	}
}

func TestTaskSecrets(t *testing.T) {
	client := fake.NewSimpleClientset(
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "expo"}, Data: map[string][]byte{"token": []byte("expo-secret-123")}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "env"}, Data: map[string][]byte{
			"API_KEY": []byte("k-abcdef"), "EXPO_TOKEN": []byte("overridden"), "not-a-var": []byte("x")}},
	)
	k := &KubeExecutor{Client: client}
	step := Step{App: "shop", Secrets: []string{"env"}, SecretEnv: map[string]string{"EXPO_TOKEN": "expo/token"}}
	values, env, err := k.taskSecrets(context.Background(), "run-1-mobile", step)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range env {
		names = append(names, e.Name)
		if e.ValueFrom.SecretKeyRef.Name != "run-1-mobile" || e.ValueFrom.SecretKeyRef.Key != "env-"+e.Name {
			t.Fatalf("%s reads %+v", e.Name, e.ValueFrom.SecretKeyRef)
		}
	}
	if strings.Join(names, ",") != "API_KEY,EXPO_TOKEN" || values["env-EXPO_TOKEN"] != "expo-secret-123" || values["env-API_KEY"] != "k-abcdef" {
		t.Fatalf("env %v, values %v (secretEnv must win over a whole secret; non-variable keys skipped)", names, values)
	}

	for _, tc := range []struct {
		step Step
		want string
	}{
		{Step{App: "shop", Secrets: []string{"missing"}}, `secret "missing" does not exist in namespace shop`},
		{Step{App: "shop", SecretEnv: map[string]string{"X": "expo/nokey"}}, `has no key "nokey"`},
	} {
		if _, _, err := k.taskSecrets(context.Background(), "p", tc.step); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("got %v, want %q", err, tc.want)
		}
	}
}

func TestMaskSecrets(t *testing.T) {
	var buf bytes.Buffer
	w := maskSecrets(&buf, map[string]string{"env-TOKEN": "expo-secret-123", "env-SHORT": "abc", "env-PEM": "line-one-xyz\nline-two-xyz"})
	w.Write([]byte("token is expo-secret-123; short abc; pem line-two-xyz\n"))
	if got := buf.String(); got != "token is ***; short abc; pem ***\n" {
		t.Fatalf("masked log = %q", got)
	}
	var plain bytes.Buffer
	if maskSecrets(&plain, nil) != &plain {
		t.Fatal("no secrets: the writer is used as is")
	}
}

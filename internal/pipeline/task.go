package pipeline

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"sync"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/p0dxD/rendimiento.ai/internal/spec"
)

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// taskSecrets reads the secrets a task step uses from its app's namespace.
// It returns the values to put in the step's own secret (under keys
// env-<VAR>) and the variables that read them. Whole secrets come first and
// secretEnv entries after, so a single key set explicitly wins.
func (k *KubeExecutor) taskSecrets(ctx context.Context, stepSecret string, step Step) (map[string]string, []corev1.EnvVar, error) {
	values := map[string]string{}
	var order []string
	set := func(name, value string) {
		if _, ok := values["env-"+name]; !ok {
			order = append(order, name)
		}
		values["env-"+name] = value
	}
	cache := map[string]*corev1.Secret{}
	get := func(name string) (*corev1.Secret, error) {
		if s, ok := cache[name]; ok {
			return s, nil
		}
		s, err := k.Client.CoreV1().Secrets(step.App).Get(ctx, name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("secret %q does not exist in namespace %s: set it on the app's Settings page (Secrets)", name, step.App)
		}
		if err != nil {
			return nil, fmt.Errorf("read secret %s: %v", name, err)
		}
		cache[name] = s
		return s, nil
	}
	for _, name := range step.Secrets {
		s, err := get(name)
		if err != nil {
			return nil, nil, err
		}
		keys := make([]string, 0, len(s.Data))
		for key := range s.Data {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if envName.MatchString(key) { // like envFrom: other keys are not variables
				set(key, string(s.Data[key]))
			}
		}
	}
	vars := make([]string, 0, len(step.SecretEnv))
	for v := range step.SecretEnv {
		vars = append(vars, v)
	}
	sort.Strings(vars)
	for _, v := range vars {
		name, key, _ := strings.Cut(step.SecretEnv[v], "/")
		s, err := get(name)
		if err != nil {
			return nil, nil, err
		}
		value, ok := s.Data[key]
		if !ok {
			return nil, nil, fmt.Errorf("secret %s has no key %q (for %s)", name, key, v)
		}
		set(v, string(value))
	}
	env := make([]corev1.EnvVar, 0, len(order))
	for _, name := range order {
		env = append(env, corev1.EnvVar{Name: name, ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: stepSecret}, Key: "env-" + name}}})
	}
	return values, env, nil
}

// maskSecrets replaces secret values in a task's log with ***. It is a
// safety net for a command that prints a secret, not a guarantee: a value
// split across two writes, or transformed (base64, quoted), is not caught.
// Values shorter than 6 characters are left alone, since masking them
// would hide ordinary words and numbers.
func maskSecrets(w io.Writer, values map[string]string) io.Writer {
	var secrets [][]byte
	for _, v := range values {
		for _, line := range strings.Split(v, "\n") {
			if line = strings.TrimSpace(line); len(line) >= 6 {
				secrets = append(secrets, []byte(line))
			}
		}
	}
	if len(secrets) == 0 {
		return w
	}
	// Longest first, so a value containing another is masked whole.
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	return &maskingWriter{w: w, secrets: secrets}
}

type maskingWriter struct {
	mu      sync.Mutex
	w       io.Writer
	secrets [][]byte
}

// Write masks p and passes it on, reporting all of p as written.
func (m *maskingWriter) Write(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := p
	for _, s := range m.secrets {
		if bytes.Contains(out, s) {
			out = bytes.ReplaceAll(out, s, []byte("***"))
		}
	}
	if _, err := m.w.Write(out); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Close closes the underlying writer when it is a closer (the step's log).
func (m *maskingWriter) Close() error {
	if c, ok := m.w.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

// taskResources turns a size preset (with its overrides) into requests and limits.
func taskResources(r spec.Resources) (corev1.ResourceRequirements, error) {
	out := corev1.ResourceRequirements{Requests: corev1.ResourceList{}, Limits: corev1.ResourceList{}}
	for _, q := range []struct {
		list  corev1.ResourceList
		name  corev1.ResourceName
		value string
	}{
		{out.Requests, corev1.ResourceCPU, r.CPURequest}, {out.Requests, corev1.ResourceMemory, r.MemRequest},
		{out.Limits, corev1.ResourceCPU, r.CPULimit}, {out.Limits, corev1.ResourceMemory, r.MemLimit},
	} {
		if q.value == "" {
			continue
		}
		v, err := resource.ParseQuantity(q.value)
		if err != nil {
			return out, fmt.Errorf("task resources: %v", err)
		}
		q.list[q.name] = v
	}
	return out, nil
}

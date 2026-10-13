package render

import (
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/p0dxD/rendimiento.ai/internal/spec"
)

func TestRenderObserve(t *testing.T) {
	s, err := spec.Parse([]byte(`
observe: true
services:
  - name: ui
    language: node
    env: {NODE_OPTIONS: --max-old-space-size=256}
    needs: [postgres]
  - name: api
    language: python
  - name: whois
    language: java
  - name: hello
    language: go
  - name: quiet
    language: node
    observe: false
  - name: ollama
    image: ollama/ollama:0.12
    port: 11434
`))
	if err != nil {
		t.Fatal(err)
	}
	in := Input{App: "shop", Spec: *s, Images: map[string]string{
		"ui": "reg/ui@sha256:1", "api": "reg/api@sha256:2", "whois": "reg/w@sha256:3", "hello": "reg/h@sha256:4", "quiet": "reg/q@sha256:5",
	}}

	// Without a collector, observe changes nothing.
	plain, err := Render(in, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range plain.Deployments {
		if v := envValue(d.Spec.Template.Spec.Containers[0], "OTEL_SERVICE_NAME"); v != "" || len(d.Spec.Template.Spec.InitContainers) != 0 {
			t.Fatalf("%s is observed without a collector", d.Name)
		}
	}

	opt := DefaultOptions()
	opt.Telemetry = Telemetry{Endpoint: "http://collector.observa:4318", Agents: DefaultAgents()}
	objs, err := Render(in, opt)
	if err != nil {
		t.Fatal(err)
	}
	pods := map[string]corev1.PodSpec{}
	for _, d := range objs.Deployments {
		pods[d.Name] = d.Spec.Template.Spec
	}
	for _, name := range []string{"ui", "api", "whois", "hello", "ollama"} {
		c := pods[name].Containers[0]
		if envValue(c, "OTEL_SERVICE_NAME") != "shop/"+name || envValue(c, "OTEL_EXPORTER_OTLP_ENDPOINT") != "http://collector.observa:4318" || envValue(c, "OTEL_TRACES_SAMPLER_ARG") != "0.25" {
			t.Fatalf("%s env = %+v", name, c.Env)
		}
	}
	// Agents for Java, Node and Python; the app's own NODE_OPTIONS are kept.
	for name, want := range map[string]struct{ env, value, language string }{
		"ui":    {"NODE_OPTIONS", "--max-old-space-size=256 --require /otel-auto-instrumentation/autoinstrumentation.js", "node"},
		"api":   {"PYTHONPATH", "/otel-auto-instrumentation/opentelemetry/instrumentation/auto_instrumentation:/otel-auto-instrumentation", "python"},
		"whois": {"JAVA_TOOL_OPTIONS", "-javaagent:/otel-auto-instrumentation/javaagent.jar", "java"},
	} {
		p := pods[name]
		if got := envValue(p.Containers[0], want.env); got != want.value {
			t.Fatalf("%s %s = %q", name, want.env, got)
		}
		if len(p.InitContainers) != 1 || p.InitContainers[0].Image != DefaultAgents()[want.language] {
			t.Fatalf("%s init = %+v", name, p.InitContainers)
		}
		if m := p.Containers[0].VolumeMounts; len(m) == 0 || m[len(m)-1].MountPath != agentDir || !m[len(m)-1].ReadOnly {
			t.Fatalf("%s mounts = %+v", name, m)
		}
	}
	// Go and ready-made images get only the variables; observe: false and
	// the database rendimiento runs get nothing.
	for _, name := range []string{"hello", "ollama"} {
		if len(pods[name].InitContainers) != 0 {
			t.Fatalf("%s got an agent", name)
		}
	}
	for _, name := range []string{"quiet", "postgres"} {
		if envValue(pods[name].Containers[0], "OTEL_SERVICE_NAME") != "" || len(pods[name].InitContainers) != 0 {
			t.Fatalf("%s is observed", name)
		}
	}
}

// envValue is a container variable's plain value ("" when unset).
func envValue(c corev1.Container, name string) string {
	for _, e := range c.Env {
		if e.Name == name {
			return e.Value
		}
	}
	return ""
}

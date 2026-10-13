package render

import (
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/p0dxD/rendimiento.ai/internal/spec"
)

// Telemetry is where observed services send their traces (an OpenTelemetry
// collector) and the agents that instrument them without code changes.
type Telemetry struct {
	// Endpoint is the collector's OTLP/HTTP address
	// (http://collector.observa.svc.cluster.local:4318); empty turns
	// tracing off for every app.
	Endpoint string
	// SampleRatio is the share of traces kept, "0.25" by default.
	SampleRatio string
	// Agents are the auto-instrumentation images by language (java, node,
	// python); a language without one only gets the OTEL_* variables.
	Agents map[string]string
}

// DefaultAgents are the OpenTelemetry auto-instrumentation images, built
// for arm64 and amd64.
func DefaultAgents() map[string]string {
	const repo = "ghcr.io/open-telemetry/opentelemetry-operator/autoinstrumentation-"
	return map[string]string{
		"java":   repo + "java:2.32.0",
		"node":   repo + "nodejs:0.78.0",
		"python": repo + "python:0.66b1",
	}
}

// agent is how one language's agent is copied into the pod and loaded.
type agent struct {
	copy string // shell command run in the agent image
	env  string // variable that loads it
	load string // its value; appended to the app's own value
	sep  string // between the app's value and ours
}

const agentDir = "/otel-auto-instrumentation"

var agents = map[string]agent{
	"java":   {copy: "cp /javaagent.jar " + agentDir + "/javaagent.jar", env: "JAVA_TOOL_OPTIONS", load: "-javaagent:" + agentDir + "/javaagent.jar", sep: " "},
	"node":   {copy: "cp -r /autoinstrumentation/. " + agentDir, env: "NODE_OPTIONS", load: "--require " + agentDir + "/autoinstrumentation.js", sep: " "},
	"python": {copy: "cp -r /autoinstrumentation/. " + agentDir, env: "PYTHONPATH", load: agentDir + "/opentelemetry/instrumentation/auto_instrumentation:" + agentDir, sep: ":"},
}

// observe makes a Deployment send traces: the OTEL_* variables every SDK
// reads (the service is named app/service, the node ID on the Universe
// map) and, for Java, Node and Python, an init container that copies the
// agent into a shared folder plus the variable that loads it.
func observe(d *appsv1.Deployment, app string, svc spec.Service, t Telemetry) {
	pod := &d.Spec.Template.Spec
	c := &pod.Containers[0]
	ratio := t.SampleRatio
	if ratio == "" {
		ratio = "0.25"
	}
	vars := []corev1.EnvVar{
		{Name: "OTEL_SERVICE_NAME", Value: app + "/" + svc.Name},
		{Name: "OTEL_RESOURCE_ATTRIBUTES", Value: "service.namespace=" + app + ",rendimiento.app=" + app + ",rendimiento.service=" + svc.Name},
		{Name: "OTEL_EXPORTER_OTLP_ENDPOINT", Value: t.Endpoint},
		{Name: "OTEL_EXPORTER_OTLP_PROTOCOL", Value: "http/protobuf"},
		{Name: "OTEL_TRACES_EXPORTER", Value: "otlp"},
		// The collector turns traces into the map's numbers; the agents'
		// own metrics and logs would only cost memory.
		{Name: "OTEL_METRICS_EXPORTER", Value: "none"},
		{Name: "OTEL_LOGS_EXPORTER", Value: "none"},
		{Name: "OTEL_TRACES_SAMPLER", Value: "parentbased_traceidratio"},
		{Name: "OTEL_TRACES_SAMPLER_ARG", Value: ratio},
	}
	for _, v := range vars {
		setEnv(c, v.Name, v.Value, false, "")
	}
	a, ok := agents[svc.Language]
	image := t.Agents[svc.Language]
	if !ok || image == "" || svc.Image != "" {
		return // a ready-made image or a language without an agent: variables only
	}
	pod.Volumes = append(pod.Volumes, corev1.Volume{Name: "otel-agent", VolumeSource: corev1.VolumeSource{
		EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: ptr(resource.MustParse("200Mi"))},
	}})
	mount := corev1.VolumeMount{Name: "otel-agent", MountPath: agentDir}
	pod.InitContainers = append(pod.InitContainers, corev1.Container{
		Name:            "otel-agent",
		Image:           image,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Command:         []string{"sh", "-c", a.copy},
		VolumeMounts:    []corev1.VolumeMount{mount},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("10m"), corev1.ResourceMemory: resource.MustParse("32Mi")},
			Limits:   corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("128Mi")},
		},
		SecurityContext: c.SecurityContext,
	})
	mount.ReadOnly = true
	c.VolumeMounts = append(c.VolumeMounts, mount)
	setEnv(c, a.env, a.load, true, a.sep)
}

// setEnv sets a container variable. When the app already sets it, the
// app's value wins, unless join is true: then ours is appended to it with
// sep (NODE_OPTIONS, JAVA_TOOL_OPTIONS and PYTHONPATH can hold several).
func setEnv(c *corev1.Container, name, value string, join bool, sep string) {
	for i, e := range c.Env {
		if e.Name != name {
			continue
		}
		switch {
		case !join || e.ValueFrom != nil || strings.Contains(e.Value, value):
		case e.Value == "":
			c.Env[i].Value = value
		default:
			c.Env[i].Value = e.Value + sep + value
		}
		return
	}
	c.Env = append(c.Env, corev1.EnvVar{Name: name, Value: value})
}

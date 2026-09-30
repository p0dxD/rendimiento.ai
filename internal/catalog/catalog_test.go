package catalog

import (
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/p0dxD/rendimiento.ai/api/v1alpha1"
	"github.com/p0dxD/rendimiento.ai/internal/spec"
)

func svc(ns, name string, labels map[string]string, ports ...int32) corev1.Service {
	s := corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, Labels: labels},
		Spec: corev1.ServiceSpec{Selector: map[string]string{"app": name}}}
	for _, p := range ports {
		s.Spec.Ports = append(s.Spec.Ports, corev1.ServicePort{Port: p, TargetPort: intstr.FromInt32(p), Protocol: corev1.ProtocolTCP})
	}
	return s
}

func dep(ns, name string, env map[string]string) appsv1.Deployment {
	d := appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name}}
	c := corev1.Container{Name: "c"}
	for k, v := range env {
		c.Env = append(c.Env, corev1.EnvVar{Name: k, Value: v})
	}
	d.Spec.Template.Spec.Containers = []corev1.Container{c}
	return d
}

func TestAssemble(t *testing.T) {
	managed := map[string]string{"app.kubernetes.io/managed-by": "rendimiento"}
	lb := svc("jobsentry", "sentry-linguistic", nil, 8989)
	lb.Spec.Type = corev1.ServiceTypeLoadBalancer
	lb.Annotations = map[string]string{"argocd.argoproj.io/tracking-id": "jobsentry-linguistic-application:/Service:jobsentry/sentry-linguistic"}
	lb.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{{IP: "192.168.1.82"}}
	s := snapshot{
		apps: []v1alpha1.App{{ObjectMeta: metav1.ObjectMeta{Name: "jobsentry"}, Spec: v1alpha1.AppSpec{
			Repo: "p0dxD/jobsentry.net", Release: 7,
			Images: map[string]string{"ollama-internal": "dustynv/ollama:r36.4.0", "gate": "reg/gate@sha256:1"},
			Services: []spec.Service{
				{Name: "ollama-internal", Image: "dustynv/ollama:r36.4.0", Port: 11434,
					Catalog: &spec.Catalog{Title: "Ollama", Env: "OLLAMA_HOST", Endpoints: []string{"POST /api/generate"}}},
				{Name: "gate", Path: "gate", Port: 8383, Health: &spec.Health{Path: "/health"}},
			}}}},
		services: []corev1.Service{
			svc("jobsentry", "ollama-internal", managed, 11434),
			svc("jobsentry", "gate", managed, 80, 8383),
			lb,
			svc("stockpulse", "stockpulse-postgres", nil, 5432),
			svc("kube-system", "kube-dns", nil, 53),
		},
		ingresses: []networkingv1.Ingress{{ObjectMeta: metav1.ObjectMeta{Namespace: "jobsentry", Name: "api"},
			Spec: networkingv1.IngressSpec{Rules: []networkingv1.IngressRule{{Host: "api.jobsentry.net",
				IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{Paths: []networkingv1.HTTPIngressPath{
					{Path: "/", Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{Name: "gate"}}}}}}}}}}},
		deployments: []appsv1.Deployment{
			dep("secplus", "web", map[string]string{"OLLAMA_HOST": "http://ollama-internal.jobsentry.svc.cluster.local:11434", "MODE": "gate"}),
			dep("jobsentry", "gate", map[string]string{"AI_SERVICE_URL": "http://sentry-linguistic:8989/analyze", "OLLAMA": "ollama-internal"}),
			dep("home", "script", map[string]string{"TARGET": "http://192.168.1.82:8989"}),
			dep("other", "x", map[string]string{"URL": "http://ollama-internal:11434"}), // other namespace: not a match
		},
	}
	c := assemble(s, DefaultInfra)
	byID := map[string]Entry{}
	for _, e := range c.Entries {
		byID[e.ID] = e
	}
	if c.Entries[0].Group != GroupApps || c.Entries[len(c.Entries)-1].Group != GroupInfra {
		t.Errorf("groups out of order: %v … %v", c.Entries[0].Group, c.Entries[len(c.Entries)-1].Group)
	}

	o := byID["jobsentry/ollama-internal"]
	if o.Title != "Ollama" || o.Env != "OLLAMA_HOST" || o.URL != "http://ollama-internal.jobsentry.svc.cluster.local:11434" ||
		o.Origin.Kind != "rendimiento" || o.Origin.Images[0].Link != "https://hub.docker.com/r/dustynv/ollama" {
		t.Errorf("ollama = %+v", o)
	}
	if !strings.Contains(o.Snippet, "OLLAMA_HOST: http://ollama-internal.jobsentry.svc.cluster.local:11434") {
		t.Errorf("snippet = %s", o.Snippet)
	}
	// Bare "ollama-internal" under a key that isn't an address is not a consumer.
	if len(o.UsedBy) != 1 || o.UsedBy[0].Workload != "web" || o.UsedBy[0].Env != "OLLAMA_HOST" {
		t.Errorf("ollama consumers = %+v", o.UsedBy)
	}

	g := byID["jobsentry/gate"]
	if g.URL != "http://gate.jobsentry.svc.cluster.local:8383" || g.Health != "/health" || len(g.Public) != 1 || g.Public[0] != "https://api.jobsentry.net" ||
		g.Origin.RepoURL != "https://github.com/p0dxD/jobsentry.net/tree/HEAD/gate" || !g.Origin.Images[0].Built {
		t.Errorf("gate = %+v", g)
	}
	if len(g.UsedBy) != 0 {
		t.Errorf("MODE=gate is not a reference: %+v", g.UsedBy)
	}

	l := byID["jobsentry/sentry-linguistic"]
	if l.Group != GroupOther || l.Origin.Kind != "argocd" || l.Origin.ArgoApp != "jobsentry-linguistic-application" || l.LAN != "192.168.1.82:8989" || l.LANURL != "http://192.168.1.82:8989" {
		t.Errorf("linguistic = %+v", l)
	}
	if len(l.UsedBy) != 2 {
		t.Errorf("linguistic consumers (short name + LAN IP) = %+v", l.UsedBy)
	}

	p := byID["stockpulse/stockpulse-postgres"]
	if p.Protocol != "postgres" || p.Env != "DATABASE_URL" || !strings.Contains(p.Snippet, "secretEnv:") ||
		!strings.Contains(p.Snippet, "postgres://<user>:<password>@stockpulse-postgres.stockpulse.svc.cluster.local:5432/<database>") {
		t.Errorf("postgres = %+v\n%s", p, p.Snippet)
	}
	if byID["kube-system/kube-dns"].Group != GroupInfra {
		t.Error("kube-system is infrastructure")
	}
}

func TestImageLink(t *testing.T) {
	for ref, want := range map[string]string{
		"postgres:15-alpine":                      "https://hub.docker.com/_/postgres",
		"dustynv/ollama:r36.4.0":                  "https://hub.docker.com/r/dustynv/ollama",
		"docker.io/library/redis@sha256:abc":      "https://hub.docker.com/r/library/redis",
		"ghcr.io/railwayapp/railpack-frontend:v1": "https://github.com/railwayapp/railpack-frontend",
		"quay.io/prometheus/node-exporter:v1":     "https://quay.io/repository/prometheus/node-exporter",
		"registry.cube.local:5000/app@sha256:abc": "",
	} {
		if got := imageLink(ref); got != want {
			t.Errorf("imageLink(%s) = %q, want %q", ref, got, want)
		}
	}
}

func TestCategorize(t *testing.T) {
	img := func(refs ...string) Origin {
		o := Origin{}
		for _, r := range refs {
			o.Images = append(o.Images, Image{Ref: r})
		}
		return o
	}
	for _, tc := range []struct {
		e    Entry
		want string
	}{
		{Entry{Name: "blog-postgres", Namespace: "podoi", Protocol: "postgres"}, "database"},
		{Entry{Name: "rendimiento-db", Namespace: "rendimiento-system", Protocol: "postgres"}, "database"},
		{Entry{Name: "argocd-redis", Namespace: "argocd", Protocol: "redis"}, "messaging"},
		{Entry{Name: "ollama-internal", Namespace: "jobsentry", Protocol: "http", Origin: img("dustynv/ollama:r36.4.0")}, "ai"},
		{Entry{Name: "sentry-linguistic", Namespace: "jobsentry", Protocol: "http"}, "ai"},
		{Entry{Name: "minio-api", Namespace: "minio", Protocol: "http"}, "storage"},
		{Entry{Name: "umami", Namespace: "umami", Protocol: "http"}, "monitoring"},
		{Entry{Name: "jenkinsci", Namespace: "devops-tools", Protocol: "http"}, "devtools"},
		{Entry{Name: "ingress-nginx-controller", Namespace: "ingress-nginx", Protocol: "http"}, "platform"},
		// Built images live in the cluster registry: that must not make them "storage".
		{Entry{Name: "web", Namespace: "secplus", Protocol: "http", Origin: img("registry.cube.local:5000/secplus-web@sha256:abc")}, "web"},
	} {
		if got := categorize(tc.e); got != tc.want {
			t.Errorf("%s/%s = %s, want %s", tc.e.Namespace, tc.e.Name, got, tc.want)
		}
	}
}

func TestLANAddress(t *testing.T) {
	lb := func(ip string) corev1.Service {
		var s corev1.Service
		s.Spec.Type = corev1.ServiceTypeLoadBalancer
		if ip != "" {
			s.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{{IP: ip}}
		}
		return s
	}
	for _, tc := range []struct {
		ip             string
		port           int32
		protocol       string
		addr, wantLink string
	}{
		{"192.168.1.87", 80, "http", "192.168.1.87:80", "http://192.168.1.87"},          // Grafana
		{"192.168.1.73", 8082, "http", "192.168.1.73:8082", "http://192.168.1.73:8082"}, // Longhorn UI
		{"192.168.1.9", 443, "https", "192.168.1.9:443", "https://192.168.1.9"},
		{"192.168.1.5", 5432, "postgres", "192.168.1.5:5432", ""}, // an address, not a page to open
		{"", 80, "http", "", ""}, // no IP assigned yet
	} {
		addr, link := lanAddress(lb(tc.ip), tc.port, tc.protocol)
		if addr != tc.addr || link != tc.wantLink {
			t.Errorf("%s:%d %s = %q, %q; want %q, %q", tc.ip, tc.port, tc.protocol, addr, link, tc.addr, tc.wantLink)
		}
	}
}

package environment

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	storagev1 "k8s.io/api/storage/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/version"
	fakediscovery "k8s.io/client-go/discovery/fake"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	kubetesting "k8s.io/client-go/testing"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/p0dxD/rendimiento.ai/internal/dns"
	gh "github.com/p0dxD/rendimiento.ai/internal/github"
)

type pinger struct{ err error }

func (p pinger) Ping(context.Context) error { return p.err }

func node(name string, ready bool, labels map[string]string) *corev1.Node {
	st := corev1.ConditionFalse
	if ready {
		st = corev1.ConditionTrue
	}
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: st}},
			Capacity:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4"), corev1.ResourceMemory: resource.MustParse("8Gi")},
			NodeInfo:   corev1.NodeSystemInfo{Architecture: "arm64", KubeletVersion: "v1.34.1+k3s1", OSImage: "Debian"},
		},
	}
}

func deploy(ns, name, image string, ready int32) *appsv1.Deployment {
	one := int32(1)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec: appsv1.DeploymentSpec{Replicas: &one, Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "c", Image: image}}}}},
		Status: appsv1.DeploymentStatus{ReadyReplicas: ready},
	}
}

func certificate(ns, name string, ready bool, notAfter time.Time) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetAPIVersion("cert-manager.io/v1")
	u.SetKind("Certificate")
	u.SetNamespace(ns)
	u.SetName(name)
	st := "False"
	if ready {
		st = "True"
	}
	_ = unstructured.SetNestedSlice(u.Object, []any{map[string]any{"type": "Ready", "status": st, "message": "Issuing certificate as Secret does not exist"}}, "status", "conditions")
	_ = unstructured.SetNestedField(u.Object, notAfter.Format(time.RFC3339), "status", "notAfter")
	return u
}

func issuer(name string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetAPIVersion("cert-manager.io/v1")
	u.SetKind("ClusterIssuer")
	u.SetName(name)
	_ = unstructured.SetNestedField(u.Object, "https://acme-v02.api.letsencrypt.org/directory", "spec", "acme", "server")
	_ = unstructured.SetNestedSlice(u.Object, []any{map[string]any{"dns01": map[string]any{"cloudflare": map[string]any{}}}}, "spec", "acme", "solvers")
	_ = unstructured.SetNestedSlice(u.Object, []any{map[string]any{"type": "Ready", "status": "True"}}, "status", "conditions")
	return u
}

func fakeDiscovery(gitVersion string) *fakediscovery.FakeDiscovery {
	return &fakediscovery.FakeDiscovery{Fake: &kubetesting.Fake{}, FakedServerVersion: &version.Info{GitVersion: gitVersion}}
}

func newScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(s)
	_ = apiextensionsv1.AddToScheme(s)
	return s
}

func find(t *testing.T, r *Report, id string) Check {
	t.Helper()
	for _, c := range r.Checks {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no check %q", id)
	return Check{}
}

func TestFullyInstalledCluster(t *testing.T) {
	// A registry answering like registry:2, and a TCP listener standing in for buildkitd.
	reg := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/" {
			w.WriteHeader(200)
		}
	}))
	defer reg.Close()
	bk, _ := net.Listen("tcp", "127.0.0.1:0")
	defer bk.Close()

	yes := true
	objs := []client.Object{
		node("main", true, map[string]string{"node-role.kubernetes.io/control-plane": "true"}),
		node("worker1", true, nil),
		&networkingv1.IngressClass{ObjectMeta: metav1.ObjectMeta{Name: "nginx"}, Spec: networkingv1.IngressClassSpec{Controller: "k8s.io/ingress-nginx"}},
		deploy("ingress-nginx", "ingress-nginx-controller", "registry.k8s.io/ingress-nginx/controller:v1.15.1@sha256:abc", 1),
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: "ingress-nginx", Name: "ingress-nginx-controller"},
			Spec:   corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer},
			Status: corev1.ServiceStatus{LoadBalancer: corev1.LoadBalancerStatus{Ingress: []corev1.LoadBalancerIngress{{IP: "192.168.1.75"}}}}},
		&apiextensionsv1.CustomResourceDefinition{ObjectMeta: metav1.ObjectMeta{Name: "certificates.cert-manager.io"}},
		deploy("cert-manager", "cert-manager", "quay.io/jetstack/cert-manager-controller:v1.20.2", 1),
		deploy("devops-tools", "buildkitd", "moby/buildkit:v0.18.2", 1),
		&storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: "longhorn"}, Provisioner: "driver.longhorn.io", AllowVolumeExpansion: &yes},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "rendimiento-builds"}},
		issuer("letsencrypt-prod"),
		certificate("web", "web-tls", true, time.Now().Add(60*24*time.Hour)),
		certificate("old", "old-tls", false, time.Time{}),
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "devops-tools", Name: "renovate-1", CreationTimestamp: metav1.NewTime(time.Now().Add(-time.Hour)),
			OwnerReferences: []metav1.OwnerReference{{Kind: "Job", Name: "renovate", APIVersion: "batch/v1", UID: "u"}}},
			Status: corev1.PodStatus{Phase: corev1.PodFailed, ContainerStatuses: []corev1.ContainerStatus{{Name: "renovate",
				State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1}}}}}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "shop", Name: "web-1", CreationTimestamp: metav1.NewTime(time.Now())},
			Spec: corev1.PodSpec{NodeName: "worker1"},
			Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{{Name: "web",
				State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}}}}}},
	}
	kube := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(objs...).Build()
	c := &Checker{
		Kube: kube, Discovery: fakeDiscovery("v1.34.1+k3s1"), DNS: dns.Noop{}, GitHub: &gh.Holder{}, DB: pinger{},
		Config: Config{IngressClass: "nginx", ClusterIssuer: "letsencrypt-prod", StorageClass: "longhorn",
			BuildkitAddr: "tcp://" + bk.Addr().String(), Registry: strings.TrimPrefix(reg.URL, "https://"), RegistryInsecure: true,
			BuildNamespace: "rendimiento-builds", PlatformURL: "https://rendimiento.example"},
	}
	r := c.Report(context.Background(), true)

	if r.Cluster.Distribution != "k3s" || r.Cluster.Nodes != 2 || r.Cluster.NodesReady != 2 {
		t.Fatalf("cluster = %+v", r.Cluster)
	}
	want := map[string]Status{
		"kubernetes": OK, "nodes": OK, "ingress": OK, "cert-manager": OK, "cluster-issuer": OK, "storage": OK,
		"buildkit": OK, "registry": OK, "build-namespace": OK, "database": OK,
		"certificates": Warning, "dns": Warning, "github": Missing, "metrics": Missing, "build-isolation": Warning,
	}
	for id, st := range want {
		if got := find(t, r, id); got.Status != st {
			t.Errorf("%s = %s (%s), want %s", id, got.Status, got.Summary, st)
		}
	}
	if v := find(t, r, "ingress").Version; v != "v1.15.1" {
		t.Errorf("ingress version = %q", v)
	}
	if d := strings.Join(find(t, r, "ingress").Details, ";"); !strings.Contains(d, "192.168.1.75") {
		t.Errorf("ingress details = %s", d)
	}
	if d := strings.Join(find(t, r, "cluster-issuer").Details, ";"); !strings.Contains(d, "dns01 via cloudflare") {
		t.Errorf("issuer details = %s", d)
	}
	if r.Nodes[0].Name != "main" || r.Nodes[0].Roles[0] != "control-plane" || r.Nodes[1].Pods != 1 {
		t.Errorf("nodes = %+v", r.Nodes)
	}
	// Problems: the failed cron job pod, the crash-looping pod and the stuck certificate.
	var probs []string
	for _, p := range r.Problems {
		probs = append(probs, p.Kind+" "+p.Namespace+"/"+p.Name+": "+p.Reason)
	}
	joined := strings.Join(probs, "\n")
	for _, w := range []string{"Job pod devops-tools/renovate-1: Failed: renovate exited with code 1", "Pod shop/web-1: CrashLoopBackOff (web)", "Certificate old/old-tls"} {
		if !strings.Contains(joined, w) {
			t.Errorf("missing problem %q in:\n%s", w, joined)
		}
	}
	// GitHub is required and missing, so the environment is not fully ready.
	if r.Overall != Missing || !strings.Contains(r.Summary, "1 required component needs attention") {
		t.Errorf("overall = %s: %s", r.Overall, r.Summary)
	}
	if len(r.Providers) != 4 || r.Providers[0].Active != "k3s" || r.Providers[1].Active != "manual" {
		t.Errorf("providers = %+v", r.Providers)
	}
}

func TestBareCluster(t *testing.T) {
	kube := fake.NewClientBuilder().WithScheme(newScheme()).WithObjects(node("n1", false, nil)).Build()
	c := &Checker{
		Kube: kube, Discovery: fakeDiscovery("v1.30.2-eks-1234"), DNS: dns.Noop{}, GitHub: &gh.Holder{}, DB: pinger{errors.New("refused")},
		Config: Config{IngressClass: "nginx", ClusterIssuer: "letsencrypt-prod", StorageClass: "longhorn",
			BuildkitAddr: "tcp://127.0.0.1:1", Registry: "127.0.0.1:1", BuildNamespace: "rendimiento-builds"},
	}
	r := c.Report(context.Background(), true)
	if r.Cluster.Distribution != "eks" {
		t.Errorf("distribution = %s", r.Cluster.Distribution)
	}
	for id, st := range map[string]Status{"nodes": Error, "ingress": Missing, "cert-manager": Missing, "cluster-issuer": Missing,
		"storage": Missing, "buildkit": Error, "registry": Error, "database": Error} {
		ch := find(t, r, id)
		if ch.Status != st {
			t.Errorf("%s = %s (%s), want %s", id, ch.Status, ch.Summary, st)
		}
		if ch.Status != OK && ch.Fix == "" && id != "nodes" && id != "database" {
			t.Errorf("%s has no fix hint", id)
		}
	}
	if r.Overall != Error {
		t.Errorf("overall = %s", r.Overall)
	}
}

func TestReportIsCached(t *testing.T) {
	kube := fake.NewClientBuilder().WithScheme(newScheme()).Build()
	c := &Checker{Kube: kube, DNS: dns.Noop{}, GitHub: &gh.Holder{}, DB: pinger{}, TTL: time.Minute,
		Config: Config{BuildkitAddr: "tcp://127.0.0.1:1", Registry: "127.0.0.1:1"}}
	a := c.Report(context.Background(), false)
	if b := c.Report(context.Background(), false); a != b {
		t.Fatal("expected cached report")
	}
	if b := c.Report(context.Background(), true); a == b {
		t.Fatal("refresh should rebuild")
	}
}

func TestDistribution(t *testing.T) {
	cases := map[string]string{"v1.34.1+k3s1": "k3s", "v1.29.3+rke2r1": "rke2", "v1.30.2-eks-1234": "eks", "v1.30.1-gke.1": "gke", "v1.31.0": "kubernetes"}
	for v, want := range cases {
		if id, _ := Distribution(v, nil); id != want {
			t.Errorf("Distribution(%q) = %s, want %s", v, id, want)
		}
	}
	aks := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"kubernetes.azure.com/cluster": "x"}}}
	if id, _ := Distribution("v1.31.0", aks); id != "aks" {
		t.Errorf("aks = %s", id)
	}
}

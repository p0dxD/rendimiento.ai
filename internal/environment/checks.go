package environment

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	storagev1 "k8s.io/api/storage/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type checkFunc func(context.Context) Check

func (c *Checker) checks(s *snapshot) []checkFunc {
	return []checkFunc{
		func(context.Context) Check { return c.checkAPI(s) },
		func(context.Context) Check { return checkNodes(s) },
		func(context.Context) Check { return checkMetrics(s) },
		func(ctx context.Context) Check { return c.checkIngress(ctx, s) },
		func(ctx context.Context) Check { return c.checkCertManager(ctx, s) },
		c.checkIssuer,
		func(context.Context) Check { return checkCertificates(s) },
		c.checkStorage,
		func(ctx context.Context) Check { return c.checkBuildkit(ctx, s) },
		c.checkRegistry,
		c.checkBuildNamespace,
		c.checkBuildIsolation,
		c.checkDNS,
		c.checkNotifications,
		c.checkLogArchive,
		c.checkGitHub,
		c.checkDatabase,
	}
}

func (c *Checker) checkAPI(s *snapshot) Check {
	ch := Check{ID: "kubernetes", Name: "Kubernetes API", Category: CatCluster, Required: true, Version: s.cluster.Version}
	if s.err != nil && s.cluster.Version == "" {
		ch.Status, ch.Summary = Error, "cannot reach the Kubernetes API: "+s.err.Error()
		ch.Fix = "Check the platform's service account and that the API server is up."
		return ch
	}
	ch.Status = OK
	ch.Summary = fmt.Sprintf("%s %s, %d namespaces, %d pods", s.cluster.Name, s.cluster.Version, s.cluster.Namespaces, s.cluster.Pods)
	return ch
}

func checkNodes(s *snapshot) Check {
	ch := Check{ID: "nodes", Name: "Nodes", Category: CatCluster, Required: true}
	archs := map[string]int{}
	for _, n := range s.nodes {
		archs[n.Arch]++
		if !n.Ready {
			ch.Details = append(ch.Details, n.Name+" is NotReady")
		}
		for _, p := range n.Pressure {
			ch.Details = append(ch.Details, n.Name+" reports "+p)
		}
		if n.Unschedulable {
			ch.Details = append(ch.Details, n.Name+" is cordoned")
		}
	}
	var arch []string
	for a, n := range archs {
		arch = append(arch, fmt.Sprintf("%d× %s", n, a))
	}
	ch.Summary = fmt.Sprintf("%d/%d ready (%s)", s.cluster.NodesReady, s.cluster.Nodes, strings.Join(arch, ", "))
	switch {
	case s.cluster.Nodes == 0:
		ch.Status, ch.Summary = Error, "no nodes visible"
	case s.cluster.NodesReady == 0:
		ch.Status = Error
	case len(ch.Details) > 0:
		ch.Status = Warning
		ch.Fix = "Inspect with: kubectl describe node <name>"
	default:
		ch.Status = OK
	}
	if len(archs) > 1 {
		ch.Details = append(ch.Details, "mixed CPU architectures: images must be built for each (rendimiento builds for the build node's arch)")
	}
	return ch
}

func checkMetrics(s *snapshot) Check {
	ch := Check{ID: "metrics", Name: "Metrics server", Category: CatCluster}
	if s.metrics {
		ch.Status, ch.Summary = OK, "live CPU and memory usage available"
		return ch
	}
	ch.Status, ch.Summary = Missing, "no live resource usage (metrics.k8s.io unavailable)"
	ch.Fix = "kubectl apply -f https://github.com/kubernetes-sigs/metrics-server/releases/latest/download/components.yaml (k3s ships it by default)"
	return ch
}

// imageVersion returns the tag of an image reference, without any digest.
func imageVersion(image string) string {
	image, _, _ = strings.Cut(image, "@")
	if i := strings.LastIndex(image, ":"); i > strings.LastIndex(image, "/") {
		return image[i+1:]
	}
	return "latest"
}

// findDeployment returns the first deployment running an image containing substr.
func findDeployment(s *snapshot, substr string) *appsv1.Deployment {
	for i := range s.deployments {
		for _, ct := range s.deployments[i].Spec.Template.Spec.Containers {
			if strings.Contains(ct.Image, substr) {
				return &s.deployments[i]
			}
		}
	}
	return nil
}

func deploymentReady(d *appsv1.Deployment) (bool, string) {
	want := int32(1)
	if d.Spec.Replicas != nil {
		want = *d.Spec.Replicas
	}
	return d.Status.ReadyReplicas >= want && want > 0, fmt.Sprintf("%d/%d replicas ready", d.Status.ReadyReplicas, want)
}

func firstImage(d *appsv1.Deployment, substr string) string {
	for _, ct := range d.Spec.Template.Spec.Containers {
		if strings.Contains(ct.Image, substr) {
			return ct.Image
		}
	}
	return ""
}

// ingressControllers maps IngressClass controller names to the image that implements them.
var ingressControllers = map[string]string{
	"k8s.io/ingress-nginx":          "ingress-nginx/controller",
	"traefik.io/ingress-controller": "traefik",
}

func (c *Checker) checkIngress(ctx context.Context, s *snapshot) Check {
	ch := Check{ID: "ingress", Name: "Ingress controller", Category: CatNetworking, Required: true}
	ch.Fix = "helm upgrade --install ingress-nginx ingress-nginx --repo https://kubernetes.github.io/ingress-nginx -n ingress-nginx --create-namespace"
	var ic networkingv1.IngressClass
	if err := c.Kube.Get(ctx, client.ObjectKey{Name: c.Config.IngressClass}, &ic); err != nil {
		ch.Status, ch.Summary = Missing, fmt.Sprintf("IngressClass %q not found", c.Config.IngressClass)
		var list networkingv1.IngressClassList
		if c.Kube.List(ctx, &list) == nil && len(list.Items) > 0 {
			var names []string
			for _, i := range list.Items {
				names = append(names, i.Name)
			}
			ch.Details = append(ch.Details, "available classes: "+strings.Join(names, ", ")+" (set INGRESS_CLASS to use one)")
		}
		return ch
	}
	ch.Details = append(ch.Details, "IngressClass "+ic.Name+" → "+ic.Spec.Controller)
	img, known := ingressControllers[ic.Spec.Controller]
	if !known {
		ch.Status, ch.Summary = OK, "IngressClass present (controller "+ic.Spec.Controller+" not inspected)"
		return ch
	}
	d := findDeployment(s, img)
	if d == nil {
		ch.Status, ch.Summary = Error, "IngressClass exists but no controller pods were found"
		return ch
	}
	ch.Version = imageVersion(firstImage(d, img))
	ready, detail := deploymentReady(d)
	ch.Details = append(ch.Details, d.Namespace+"/"+d.Name+": "+detail)
	for _, svc := range s.services {
		if svc.Namespace == d.Namespace && svc.Spec.Type == corev1.ServiceTypeLoadBalancer {
			var ips []string
			for _, in := range svc.Status.LoadBalancer.Ingress {
				ips = append(ips, in.IP+in.Hostname)
			}
			if len(ips) == 0 {
				ch.Details = append(ch.Details, "LoadBalancer "+svc.Name+" has no external address yet")
			} else {
				ch.Details = append(ch.Details, "external address "+strings.Join(ips, ", "))
			}
		}
	}
	if !ready {
		ch.Status, ch.Summary = Error, "controller not ready ("+detail+")"
		ch.Fix = "kubectl -n " + d.Namespace + " describe deploy " + d.Name
		return ch
	}
	ch.Status, ch.Summary, ch.Fix = OK, "routing traffic via class "+ic.Name, ""
	return ch
}

func (c *Checker) checkCertManager(ctx context.Context, s *snapshot) Check {
	ch := Check{ID: "cert-manager", Name: "cert-manager", Category: CatTLS, Required: true,
		Fix: "helm upgrade --install cert-manager cert-manager --repo https://charts.jetstack.io -n cert-manager --create-namespace --set crds.enabled=true"}
	var crd apiextensionsv1.CustomResourceDefinition
	if err := c.Kube.Get(ctx, client.ObjectKey{Name: "certificates.cert-manager.io"}, &crd); err != nil {
		ch.Status, ch.Summary = Missing, "not installed (no Certificate CRD); apps would have no HTTPS certificates"
		return ch
	}
	d := findDeployment(s, "cert-manager-controller")
	if d == nil {
		ch.Status, ch.Summary = Error, "CRDs present but the controller is not running"
		return ch
	}
	ch.Version = imageVersion(firstImage(d, "cert-manager-controller"))
	ready, detail := deploymentReady(d)
	if !ready {
		ch.Status, ch.Summary = Error, "controller not ready ("+detail+")"
		return ch
	}
	ch.Status, ch.Summary, ch.Fix = OK, "issuing certificates ("+detail+")", ""
	return ch
}

var clusterIssuerGVK = schema.GroupVersionKind{Group: "cert-manager.io", Version: "v1", Kind: "ClusterIssuer"}

func (c *Checker) checkIssuer(ctx context.Context) Check {
	ch := Check{ID: "cluster-issuer", Name: "Certificate issuer", Category: CatTLS, Required: true}
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(clusterIssuerGVK)
	if err := c.Kube.Get(ctx, client.ObjectKey{Name: c.Config.ClusterIssuer}, u); err != nil {
		ch.Status, ch.Summary = Missing, fmt.Sprintf("ClusterIssuer %q not found", c.Config.ClusterIssuer)
		ch.Fix = "Create a Let's Encrypt ClusterIssuer named " + c.Config.ClusterIssuer + " (DNS-01 with your DNS provider, or HTTP-01 through the ingress), or set CLUSTER_ISSUER."
		return ch
	}
	server, _, _ := unstructured.NestedString(u.Object, "spec", "acme", "server")
	if strings.Contains(server, "staging") {
		ch.Details = append(ch.Details, "uses the Let's Encrypt staging server: browsers will not trust its certificates")
	}
	solvers, _, _ := unstructured.NestedSlice(u.Object, "spec", "acme", "solvers")
	for _, sv := range solvers {
		m, _ := sv.(map[string]any)
		for kind, cfg := range m {
			if kind == "selector" {
				continue
			}
			if inner, ok := cfg.(map[string]any); ok {
				for provider := range inner {
					ch.Details = append(ch.Details, "solver: "+kind+" via "+provider)
				}
			} else {
				ch.Details = append(ch.Details, "solver: "+kind)
			}
		}
	}
	conds, _, _ := unstructured.NestedSlice(u.Object, "status", "conditions")
	for _, cnd := range conds {
		m, _ := cnd.(map[string]any)
		if m["type"] == "Ready" && m["status"] != "True" {
			msg, _ := m["message"].(string)
			ch.Status, ch.Summary = Error, "issuer not ready: "+msg
			return ch
		}
	}
	ch.Status, ch.Summary = OK, c.Config.ClusterIssuer+" is ready"
	return ch
}

func checkCertificates(s *snapshot) Check {
	ch := Check{ID: "certificates", Name: "Certificates in the cluster", Category: CatTLS}
	if s.certTotal == 0 && len(s.certProblems) == 0 {
		ch.Status, ch.Summary = OK, "no certificates yet"
		return ch
	}
	if len(s.certProblems) == 0 {
		ch.Status, ch.Summary = OK, fmt.Sprintf("all %d certificates valid", s.certTotal)
		return ch
	}
	ch.Status = Warning
	ch.Summary = fmt.Sprintf("%d of %d certificates need attention", len(s.certProblems), s.certTotal)
	for _, p := range s.certProblems {
		ch.Details = append(ch.Details, p.Namespace+"/"+p.Name+": "+p.Reason)
	}
	ch.Fix = "kubectl describe certificate -n <namespace> <name>; with DNS-01, check the DNS provider API token used by the issuer."
	return ch
}

func (c *Checker) checkStorage(ctx context.Context) Check {
	ch := Check{ID: "storage", Name: "Persistent storage", Category: CatCluster, Required: true}
	var list storagev1.StorageClassList
	if err := c.Kube.List(ctx, &list); err != nil {
		ch.Status, ch.Summary = Error, err.Error()
		return ch
	}
	var names []string
	for _, sc := range list.Items {
		n := sc.Name
		if sc.Annotations["storageclass.kubernetes.io/is-default-class"] == "true" {
			n += " (default)"
		}
		names = append(names, n)
		if sc.Name == c.Config.StorageClass {
			ch.Status = OK
			ch.Summary = fmt.Sprintf("apps with volumes use %s (%s)", sc.Name, sc.Provisioner)
			if sc.AllowVolumeExpansion != nil && *sc.AllowVolumeExpansion {
				ch.Details = append(ch.Details, "volumes can be expanded")
			}
		}
	}
	ch.Details = append(ch.Details, "classes: "+strings.Join(names, ", "))
	if ch.Status == "" {
		ch.Status, ch.Summary = Missing, fmt.Sprintf("StorageClass %q not found; apps with volumes cannot start", c.Config.StorageClass)
		ch.Fix = "Install a storage provider (e.g. Longhorn: helm upgrade --install longhorn longhorn --repo https://charts.longhorn.io -n longhorn-system --create-namespace) or set STORAGE_CLASS to an existing class."
	}
	return ch
}

func (c *Checker) checkBuildkit(ctx context.Context, s *snapshot) Check {
	ch := Check{ID: "buildkit", Name: "BuildKit (image builder)", Category: CatBuild, Required: true,
		Fix: "Deploy moby/buildkit as a Deployment + Service listening on tcp://0.0.0.0:1234 and set BUILDKIT_ADDR."}
	if d := findDeployment(s, "moby/buildkit"); d != nil {
		ch.Version = imageVersion(firstImage(d, "moby/buildkit"))
		_, detail := deploymentReady(d)
		ch.Details = append(ch.Details, d.Namespace+"/"+d.Name+": "+detail)
	}
	u, err := url.Parse(c.Config.BuildkitAddr)
	if err != nil || u.Host == "" {
		ch.Status, ch.Summary = Error, "BUILDKIT_ADDR is not a tcp:// address: "+c.Config.BuildkitAddr
		return ch
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", u.Host)
	if err != nil {
		ch.Status, ch.Summary = Error, "cannot connect to "+u.Host+": "+err.Error()
		return ch
	}
	conn.Close()
	ch.Status, ch.Summary, ch.Fix = OK, "accepting connections at "+u.Host, ""
	if c.Config.BuildkitPool != "" {
		_, srvs, err := net.DefaultResolver.LookupSRV(ctx, "buildkit", "tcp", c.Config.BuildkitPool)
		var ready []string
		for _, s := range srvs {
			name, _, _ := strings.Cut(s.Target, ".")
			ready = append(ready, name)
		}
		sort.Strings(ready)
		switch {
		case err != nil || len(ready) == 0:
			ch.Status = Warning
			ch.Summary = "no daemon of the pool " + c.Config.BuildkitPool + " is ready; builds use " + u.Host
		default:
			ch.Summary = fmt.Sprintf("%d daemons ready in the pool; each image builds on its own daemon", len(ready))
			ch.Details = append(ch.Details, "ready: "+strings.Join(ready, ", "))
		}
	}
	return ch
}

func (c *Checker) checkRegistry(ctx context.Context) Check {
	ch := Check{ID: "registry", Name: "Container registry", Category: CatBuild, Required: true}
	scheme := "https"
	client := &http.Client{Timeout: 5 * time.Second}
	if c.Config.RegistryInsecure {
		client.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} //nolint:gosec // matches REGISTRY_INSECURE for self-signed registries
		ch.Details = append(ch.Details, "TLS verification is off (REGISTRY_INSECURE=true)")
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, scheme+"://"+c.Config.Registry+"/v2/", nil)
	resp, err := client.Do(req)
	if err != nil {
		ch.Status, ch.Summary = Error, "cannot reach "+c.Config.Registry+": "+err.Error()
		ch.Fix = "Check REGISTRY and that nodes and the platform can resolve it."
		return ch
	}
	resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		ch.Status, ch.Summary = OK, c.Config.Registry+" is reachable"
	case http.StatusUnauthorized:
		ch.Status, ch.Summary = OK, c.Config.Registry+" is reachable (requires auth)"
	default:
		ch.Status, ch.Summary = Error, fmt.Sprintf("%s answered %s to the registry API", c.Config.Registry, resp.Status)
	}
	return ch
}

func (c *Checker) checkBuildNamespace(ctx context.Context) Check {
	ch := Check{ID: "build-namespace", Name: "Build namespace", Category: CatBuild, Required: true}
	var ns corev1.Namespace
	if err := c.Kube.Get(ctx, client.ObjectKey{Name: c.Config.BuildNamespace}, &ns); err != nil {
		ch.Status, ch.Summary = Missing, "namespace "+c.Config.BuildNamespace+" not found; CI steps cannot run"
		ch.Fix = "kubectl apply -f deploy/namespace.yaml"
		return ch
	}
	ch.Status, ch.Summary = OK, "CI pods run in "+c.Config.BuildNamespace
	return ch
}

// checkBuildIsolation reports whether CI pods, which run repo code, are
// fenced off from the rest of the cluster by a NetworkPolicy.
func (c *Checker) checkBuildIsolation(ctx context.Context) Check {
	ch := Check{ID: "build-isolation", Name: "Build isolation", Category: CatBuild}
	var list networkingv1.NetworkPolicyList
	if err := c.Kube.List(ctx, &list, client.InNamespace(c.Config.BuildNamespace)); err != nil {
		ch.Status, ch.Summary = Error, "cannot read network policies: "+err.Error()
		return ch
	}
	if len(list.Items) == 0 {
		ch.Status, ch.Summary = Warning, "build pods can reach every service in the cluster and the home network"
		ch.Fix = "kubectl apply -f deploy/networkpolicy.yaml (allows DNS, BuildKit and the internet only)"
		return ch
	}
	var names []string
	for _, np := range list.Items {
		names = append(names, np.Name)
	}
	ch.Status, ch.Summary = OK, "build pods limited to DNS, BuildKit and the internet ("+strings.Join(names, ", ")+")"
	if len(c.Config.ExcludeNodes) > 0 {
		ch.Details = append(ch.Details, "builds never run on: "+strings.Join(c.Config.ExcludeNodes, ", ")+" (BUILD_EXCLUDE_NODES)")
	}
	ch.Details = append(ch.Details, "policies only work on nodes whose network plugin enforces them; test with a probe pod after adding nodes")
	return ch
}

func (c *Checker) checkDNS(ctx context.Context) Check {
	ch := Check{ID: "dns", Name: "DNS provider", Category: CatIntegrations}
	d := c.DNS.Describe(ctx)
	ch.Summary = d.Name + ": " + d.Detail
	switch {
	case d.Healthy:
		ch.Status = OK
	case d.ID == "manual":
		ch.Status = Warning
		ch.Fix = "Create the rendimiento-dns secret with CLOUDFLARE_API_TOKEN and DNS_TARGET, then restart the platform."
	default:
		ch.Status = Error
		ch.Fix = "Replace the token in the rendimiento-dns secret (Zone:Read + DNS:Edit) and restart the platform."
	}
	return ch
}

func (c *Checker) checkLogArchive(ctx context.Context) Check {
	ch := Check{ID: "log-archive", Name: "Log archive", Category: CatIntegrations}
	if c.Config.LogArchive == nil {
		ch.Status, ch.Summary = Warning, "off: build logs stay in Postgres forever"
		ch.Fix = "Set LOG_ARCHIVE_ENDPOINT (an S3-compatible host:port) in the rendimiento ConfigMap and LOG_ARCHIVE_ACCESS_KEY/LOG_ARCHIVE_SECRET_KEY in the rendimiento-logs secret, then restart the platform."
		return ch
	}
	ok, summary := c.Config.LogArchive(ctx)
	ch.Summary = summary
	if ok {
		ch.Status = OK
	} else {
		ch.Status = Error
		ch.Fix = "Check that the bucket exists and the rendimiento-logs credentials can read and write it."
	}
	return ch
}

func (c *Checker) checkNotifications(context.Context) Check {
	ch := Check{ID: "notifications", Name: "Email notifications", Category: CatIntegrations}
	switch {
	case c.Config.NotifyReady:
		ch.Status, ch.Summary = OK, "failed runs, rollbacks, outages and recoveries are emailed to "+strings.Join(c.Config.NotifyTo, ", ")
	case len(c.Config.NotifyTo) > 0:
		ch.Status, ch.Summary = Warning, "NOTIFY_EMAIL_TO is set but there is no RESEND_API_KEY, so nothing is sent"
		ch.Fix = "Put RESEND_API_KEY (a Resend API key allowed to send from your domain) in the rendimiento-notify secret and restart the platform."
	default:
		ch.Status, ch.Summary = Warning, "off: nobody is told about failed runs, rollbacks or outages"
		ch.Fix = "Set NOTIFY_EMAIL_TO in the rendimiento ConfigMap and RESEND_API_KEY in the rendimiento-notify secret, then restart the platform."
	}
	return ch
}

func (c *Checker) checkGitHub(ctx context.Context) Check {
	ch := Check{ID: "github", Name: "GitHub App", Category: CatIntegrations, Required: true}
	app, err := c.GitHub.Get()
	if err != nil {
		ch.Status, ch.Summary = Missing, "not connected; no repos can be deployed"
		ch.Fix = "Open " + c.Config.PlatformURL + "/setup and create the GitHub App."
		return ch
	}
	inst, err := app.Installations(ctx)
	if err != nil {
		ch.Status, ch.Summary = Error, "credentials stored but GitHub rejected them: "+err.Error()
		return ch
	}
	var accounts []string
	for _, i := range inst {
		accounts = append(accounts, i.Account.Login)
	}
	creds := app.Credentials()
	if len(inst) == 0 {
		ch.Status, ch.Summary = Warning, creds.Slug+" is not installed on any account"
		ch.Fix = "Install it: " + creds.HTMLURL + "/installations/new"
		return ch
	}
	ch.Status, ch.Summary = OK, creds.Slug+" installed on "+strings.Join(accounts, ", ")
	return ch
}

func (c *Checker) checkDatabase(ctx context.Context) Check {
	ch := Check{ID: "database", Name: "Platform database", Category: CatIntegrations, Required: true}
	if err := c.DB.Ping(ctx); err != nil {
		ch.Status, ch.Summary = Error, "Postgres unreachable: "+err.Error()
		return ch
	}
	ch.Status, ch.Summary = OK, "Postgres reachable"
	return ch
}

// providers describes each swappable slot, its active option and the roadmap.
func (c *Checker) providers(ctx context.Context, s *snapshot, checks []Check) []Provider {
	status := map[string]Status{}
	summary := map[string]string{}
	for _, ch := range checks {
		status[ch.ID], summary[ch.ID] = ch.Status, ch.Summary
	}
	dnsDesc := c.DNS.Describe(ctx)
	ghName, ghDetail := "Not connected", summary["github"]
	if app, err := c.GitHub.Get(); err == nil {
		ghName = "GitHub App " + app.Credentials().Slug
	}
	clusterStatus := status["kubernetes"]
	if status["nodes"] != OK {
		clusterStatus = worst(clusterStatus, status["nodes"])
	}
	return []Provider{
		{
			Slot: "cluster", Title: "Runs on", Active: s.cluster.Distribution, Name: s.cluster.Name + " (in-cluster)",
			Status: clusterStatus, Detail: fmt.Sprintf("%s · %d/%d nodes ready", s.cluster.Version, s.cluster.NodesReady, s.cluster.Nodes),
			Options: []Option{
				{ID: s.cluster.Distribution, Name: s.cluster.Name + " (this cluster)", Available: true},
				{ID: "eks", Name: "Amazon EKS"}, {ID: "gke", Name: "Google GKE"}, {ID: "aks", Name: "Azure AKS"},
			},
		},
		{
			Slot: "dns", Title: "DNS", Active: dnsDesc.ID, Name: dnsDesc.Name, Status: status["dns"], Detail: dnsDesc.Detail,
			Options: []Option{
				{ID: "cloudflare", Name: "Cloudflare", Available: true},
				{ID: "manual", Name: "Manual", Available: true},
				{ID: "route53", Name: "AWS Route 53"}, {ID: "clouddns", Name: "Google Cloud DNS"},
			},
		},
		{
			Slot: "source", Title: "Source", Active: "github", Name: ghName, Status: status["github"], Detail: ghDetail,
			Options: []Option{{ID: "github", Name: "GitHub", Available: true}, {ID: "gitlab", Name: "GitLab"}, {ID: "bitbucket", Name: "Bitbucket"}},
		},
		{
			Slot: "registry", Title: "Registry", Active: "registry", Name: c.Config.Registry, Status: status["registry"], Detail: summary["registry"],
			Options: []Option{{ID: "registry", Name: "Self-hosted registry", Available: true}, {ID: "ecr", Name: "Amazon ECR"}, {ID: "gar", Name: "Google Artifact Registry"}, {ID: "ghcr", Name: "GitHub Container Registry"}},
		},
	}
}

package environment

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// snapshot is read once per report and shared by the checks.
type snapshot struct {
	cluster      ClusterInfo
	nodes        []Node
	problems     []Problem
	certProblems []Problem
	certTotal    int
	deployments  []appsv1.Deployment
	services     []corev1.Service
	metrics      bool
	err          error
}

// Distribution identifies the Kubernetes flavor from the server version and
// node metadata. It decides which install hints and provider card to show.
func Distribution(gitVersion string, node *corev1.Node) (id, name string) {
	v := strings.ToLower(gitVersion)
	switch {
	case strings.Contains(v, "+k3s"):
		return "k3s", "k3s"
	case strings.Contains(v, "+rke2"):
		return "rke2", "RKE2"
	case strings.Contains(v, "-eks-"):
		return "eks", "Amazon EKS"
	case strings.Contains(v, "-gke."):
		return "gke", "Google GKE"
	}
	if node != nil {
		l := node.Labels
		switch {
		case l["kubernetes.azure.com/cluster"] != "":
			return "aks", "Azure AKS"
		case strings.HasPrefix(node.Spec.ProviderID, "kind://"):
			return "kind", "kind"
		case l["minikube.k8s.io/name"] != "":
			return "minikube", "minikube"
		case l["microk8s.io/cluster"] != "":
			return "microk8s", "MicroK8s"
		}
	}
	return "kubernetes", "Kubernetes"
}

var nodeMetricsGVK = schema.GroupVersionKind{Group: "metrics.k8s.io", Version: "v1beta1", Kind: "NodeMetricsList"}

func (c *Checker) snapshot(ctx context.Context) *snapshot {
	s := &snapshot{}
	if c.Discovery != nil {
		if v, err := c.Discovery.ServerVersion(); err == nil {
			s.cluster.Version = v.GitVersion
		} else {
			s.err = err
		}
	}

	var nodes corev1.NodeList
	if err := c.Kube.List(ctx, &nodes); err != nil {
		s.err = err
	}
	var first *corev1.Node
	if len(nodes.Items) > 0 {
		first = &nodes.Items[0]
	}
	s.cluster.Distribution, s.cluster.Name = Distribution(s.cluster.Version, first)

	var pods corev1.PodList
	_ = c.Kube.List(ctx, &pods)
	var nss corev1.NamespaceList
	_ = c.Kube.List(ctx, &nss)
	s.cluster.Pods, s.cluster.Namespaces = len(pods.Items), len(nss.Items)

	var deps appsv1.DeploymentList
	_ = c.Kube.List(ctx, &deps)
	s.deployments = deps.Items
	var svcs corev1.ServiceList
	_ = c.Kube.List(ctx, &svcs)
	s.services = svcs.Items

	// Live usage, when metrics-server is installed.
	usage := map[string][2]int64{} // node → {milliCPU, bytes}
	nm := &unstructured.UnstructuredList{}
	nm.SetGroupVersionKind(nodeMetricsGVK)
	if err := c.Kube.List(ctx, nm); err == nil && len(nm.Items) > 0 {
		s.metrics = true
		for _, item := range nm.Items {
			cpu, _, _ := unstructured.NestedString(item.Object, "usage", "cpu")
			mem, _, _ := unstructured.NestedString(item.Object, "usage", "memory")
			var u [2]int64
			if q, err := resource.ParseQuantity(cpu); err == nil {
				u[0] = q.MilliValue()
			}
			if q, err := resource.ParseQuantity(mem); err == nil {
				u[1] = q.Value()
			}
			usage[item.GetName()] = u
		}
	}

	podsPerNode := map[string]int{}
	for _, p := range pods.Items {
		if p.Spec.NodeName != "" && p.Status.Phase != corev1.PodSucceeded && p.Status.Phase != corev1.PodFailed {
			podsPerNode[p.Spec.NodeName]++
		}
	}
	for _, n := range nodes.Items {
		node := Node{
			Name:          n.Name,
			Arch:          n.Status.NodeInfo.Architecture,
			OS:            n.Status.NodeInfo.OSImage,
			Kubelet:       n.Status.NodeInfo.KubeletVersion,
			Pods:          podsPerNode[n.Name],
			CPUCores:      float64(n.Status.Capacity.Cpu().MilliValue()) / 1000,
			MemBytes:      n.Status.Capacity.Memory().Value(),
			Unschedulable: n.Spec.Unschedulable,
		}
		for label := range n.Labels {
			if role, ok := strings.CutPrefix(label, "node-role.kubernetes.io/"); ok && role != "" {
				node.Roles = append(node.Roles, role)
			}
		}
		sort.Strings(node.Roles)
		for _, cond := range n.Status.Conditions {
			switch {
			case cond.Type == corev1.NodeReady:
				node.Ready = cond.Status == corev1.ConditionTrue
			case cond.Status == corev1.ConditionTrue && strings.HasSuffix(string(cond.Type), "Pressure"):
				node.Pressure = append(node.Pressure, string(cond.Type))
			}
		}
		if u, ok := usage[n.Name]; ok {
			cpu := float64(u[0]) / 1000
			node.CPUUsed, node.MemUsed = &cpu, &u[1]
		}
		s.cluster.Nodes++
		if node.Ready {
			s.cluster.NodesReady++
		}
		s.nodes = append(s.nodes, node)
	}
	sort.Slice(s.nodes, func(i, j int) bool { return s.nodes[i].Name < s.nodes[j].Name })

	s.problems = podProblems(pods.Items, time.Now())
	s.certTotal, s.certProblems = c.certificateProblems(ctx)
	return s
}

// podProblems lists pods that are failing or stuck. Pods that are merely
// starting (under five minutes) are not problems yet.
func podProblems(pods []corev1.Pod, now time.Time) []Problem {
	var out []Problem
	for _, p := range pods {
		age := now.Sub(p.CreationTimestamp.Time)
		reason := ""
		for _, cs := range append(append([]corev1.ContainerStatus{}, p.Status.InitContainerStatuses...), p.Status.ContainerStatuses...) {
			if w := cs.State.Waiting; w != nil {
				switch w.Reason {
				case "CrashLoopBackOff", "ImagePullBackOff", "ErrImagePull", "CreateContainerConfigError", "InvalidImageName":
					reason = fmt.Sprintf("%s (%s)", w.Reason, cs.Name)
				}
			}
		}
		switch {
		case reason != "":
		case p.Status.Phase == corev1.PodFailed:
			reason = "Failed"
			if p.Status.Reason != "" {
				reason += ": " + p.Status.Reason
			}
			for _, cs := range p.Status.ContainerStatuses {
				if t := cs.State.Terminated; t != nil && t.ExitCode != 0 {
					reason = fmt.Sprintf("Failed: %s exited with code %d", cs.Name, t.ExitCode)
				}
			}
		case p.Status.Phase == corev1.PodPending && age > 5*time.Minute:
			reason = "Pending for " + age.Round(time.Minute).String()
			for _, cond := range p.Status.Conditions {
				if cond.Type == corev1.PodScheduled && cond.Status == corev1.ConditionFalse && cond.Message != "" {
					reason = "Unschedulable: " + cond.Message
				}
			}
		case p.Status.Phase == corev1.PodUnknown:
			reason = "Unknown (node unreachable?)"
		}
		if reason == "" {
			continue
		}
		kind := "Pod"
		for _, o := range p.OwnerReferences {
			if o.Kind == "Job" {
				kind = "Job pod"
			}
		}
		out = append(out, Problem{Kind: kind, Namespace: p.Namespace, Name: p.Name, Reason: reason, Since: p.CreationTimestamp.Time})
	}
	return out
}

var certificateListGVK = schema.GroupVersionKind{Group: "cert-manager.io", Version: "v1", Kind: "CertificateList"}

func (c *Checker) certificateProblems(ctx context.Context) (int, []Problem) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(certificateListGVK)
	if err := c.Kube.List(ctx, list); err != nil {
		return 0, nil
	}
	var out []Problem
	for _, item := range list.Items {
		ready, msg := false, ""
		conds, _, _ := unstructured.NestedSlice(item.Object, "status", "conditions")
		for _, cnd := range conds {
			m, _ := cnd.(map[string]any)
			if m["type"] == "Ready" {
				ready = m["status"] == "True"
				msg, _ = m["message"].(string)
			}
		}
		notAfter, _, _ := unstructured.NestedString(item.Object, "status", "notAfter")
		expiry, _ := time.Parse(time.RFC3339, notAfter)
		switch {
		case !ready:
			if msg == "" {
				msg = "not ready"
			}
			out = append(out, Problem{Kind: "Certificate", Namespace: item.GetNamespace(), Name: item.GetName(), Reason: msg})
		case !expiry.IsZero() && time.Until(expiry) < 14*24*time.Hour:
			out = append(out, Problem{Kind: "Certificate", Namespace: item.GetNamespace(), Name: item.GetName(),
				Reason: fmt.Sprintf("expires in %s and has not renewed", time.Until(expiry).Round(time.Hour))})
		}
	}
	return len(list.Items), out
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

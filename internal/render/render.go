// Package render turns an app's spec plus its released images into the
// Kubernetes objects that run it. Output mirrors the hand-written manifests
// the cluster already uses (see ~/secplus/argocd, ~/simplerfc/argocd).
package render

import (
	"fmt"
	"sort"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"sigs.k8s.io/yaml"

	"github.com/p0dxD/rendimiento.ai/internal/spec"
)

const (
	LabelManagedBy = "app.kubernetes.io/managed-by"
	ManagedBy      = "rendimiento"
	LabelApp       = "rendimiento.ai/app"
	LabelService   = "rendimiento.ai/service"
	// LabelNeed marks the database or cache rendimiento runs for `needs:`.
	LabelNeed = "rendimiento.ai/need"
	// LabelLAN marks the LoadBalancer Service that exposes a service on the LAN.
	LabelLAN = "rendimiento.ai/lan"
)

// Options carries cluster-level settings, so nothing is hardcoded per app.
type Options struct {
	IngressClass  string // "nginx"
	ClusterIssuer string // "letsencrypt-prod"
	StorageClass  string // "longhorn"
	// VolumeLabels are added to every volume claim, e.g. the labels that put
	// a Longhorn volume in a backup job's group.
	VolumeLabels map[string]string
	GPU          GPUProfile
}

// GPUProfile is how this cluster attaches a GPU to a pod. Apps only say how
// many GPUs they need; everything node-specific lives here.
type GPUProfile struct {
	Resource     string            // extended resource, e.g. nvidia.com/gpu; empty = no GPUs
	RuntimeClass string            // e.g. nvidia
	HostPaths    []string          // driver libraries mounted read-only at the same path
	Env          map[string]string // e.g. NVIDIA_VISIBLE_DEVICES=all; the app's env wins
	SharedMemory string            // size of a memory-backed /dev/shm, e.g. 1Gi
}

// DefaultOptions are this cluster's defaults: ingress-nginx, letsencrypt-prod, Longhorn.
func DefaultOptions() Options {
	return Options{IngressClass: "nginx", ClusterIssuer: "letsencrypt-prod", StorageClass: "longhorn"}
}

// Input is one app release: its spec and the image reference for each service.
type Input struct {
	App       string // app name; also its namespace
	Spec      spec.Spec
	Images    map[string]string // service name → image ref (prefer @sha256 digests)
	Namespace string            // defaults to App
	// Selectors are the live selectors of Deployments that already exist.
	// A Deployment's selector is immutable, so an adopted Deployment keeps
	// its original one; the Service uses the same selector so old and new
	// pods both receive traffic during the takeover rollout.
	Selectors map[string]map[string]string
	// ServiceURLs are the addresses of services the app's services need
	// ({service: namespace/name}), looked up by the controller.
	ServiceURLs map[string]string
}

// Objects returns the namespace first, then each service's objects in a stable order.
type Objects struct {
	Namespace   *corev1.Namespace
	Deployments []*appsv1.Deployment
	Services    []*corev1.Service
	Ingresses   []*networkingv1.Ingress
	Volumes     []*corev1.PersistentVolumeClaim
	CronJobs    []*batchv1.CronJob
}

// Render returns every object an app release needs: namespace, deployments, services, ingresses,
// volumes and cron jobs, including the database and cache its needs ask for.
func Render(in Input, opt Options) (*Objects, error) {
	ns := in.Namespace
	if ns == "" {
		ns = in.App
	}
	out := &Objects{Namespace: &corev1.Namespace{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Namespace"},
		ObjectMeta: metav1.ObjectMeta{Name: ns, Labels: appLabels(in.App)},
	}}
	services, err := withNeeds(in)
	if err != nil {
		return nil, err
	}
	for _, svc := range services {
		image, ok := in.Images[svc.Name]
		if (!ok || image == "") && svc.Image != "" {
			image, ok = svc.Image, true
		}
		if !ok || image == "" {
			return nil, fmt.Errorf("service %s has no released image", svc.Name)
		}
		meta := func(name string) metav1.ObjectMeta {
			return metav1.ObjectMeta{Name: name, Namespace: ns, Labels: svcLabels(in.App, svc.Name)}
		}
		sel := in.Selectors[svc.Name]
		if len(sel) == 0 {
			sel = selector(in.App, svc.Name)
		}
		if svc.GPU > 0 && opt.GPU.Resource == "" {
			return nil, fmt.Errorf("service %s needs a GPU but this cluster has no GPU profile configured", svc.Name)
		}
		out.Deployments = append(out.Deployments, deployment(meta(svc.Name), sel, svc, image, opt.GPU))
		s := service(meta(svc.Name), sel, svc)
		if kind := needKind(svc.Name, in.Spec); kind != "" {
			// A database or cache is reached on its own port only.
			s.Spec.Ports = s.Spec.Ports[len(s.Spec.Ports)-1:]
			s.Spec.Ports[0].Name = kind
			for _, o := range []metav1.Object{out.Deployments[len(out.Deployments)-1], s} {
				o.GetLabels()[LabelNeed] = kind
			}
		}
		out.Services = append(out.Services, s)
		if svc.LAN != nil {
			out.Services = append(out.Services, lanService(meta(svc.Name+"-lan"), sel, svc))
		}
		if svc.HasIngress() {
			out.Ingresses = append(out.Ingresses, ingress(meta(svc.IngressName()), svc, opt))
		}
		if svc.Volume != nil && svc.Volume.ExistingClaim == "" {
			pvc, err := volume(meta(svc.ClaimName()), svc, opt)
			if err != nil {
				return nil, err
			}
			out.Volumes = append(out.Volumes, pvc)
		}
	}
	for _, j := range in.Spec.Jobs {
		image := jobImage(j, in)
		if image == "" {
			return nil, fmt.Errorf("job %s has no released image", j.Name)
		}
		out.CronJobs = append(out.CronJobs, cronJob(ns, in.App, j, image))
	}
	return out, nil
}

// jobImage resolves a job's image: its service's, its own build, or as given.
func jobImage(j spec.Job, in Input) string {
	switch {
	case j.Image != "":
		return j.Image
	case j.Path != "":
		return in.Images[j.ImageKey()]
	}
	if img := in.Images[j.Service]; img != "" {
		return img
	}
	for _, svc := range in.Spec.Services {
		if svc.Name == j.Service {
			return svc.Image
		}
	}
	return ""
}

// cronJob is a job of the app: a CronJob in its namespace running the job's
// image (its own build or the app's) on its schedule.
func cronJob(ns, app string, j spec.Job, image string) *batchv1.CronJob {
	labels := appLabels(app)
	labels["rendimiento.ai/job"] = j.Name
	res := spec.ResourcesFor(j.Size, j.Resources)
	c := corev1.Container{
		Name:      j.Name,
		Image:     image,
		Command:   j.Command,
		Args:      j.Args,
		Env:       env(spec.Service{Env: j.Env, SecretEnv: j.SecretEnv}),
		Resources: requirements(res),
		SecurityContext: &corev1.SecurityContext{
			AllowPrivilegeEscalation: ptr(false),
			SeccompProfile:           &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
		},
	}
	c.Env = c.Env[1:] // no PORT for jobs
	for _, s := range j.Secrets {
		c.EnvFrom = append(c.EnvFrom, corev1.EnvFromSource{SecretRef: &corev1.SecretEnvSource{
			LocalObjectReference: corev1.LocalObjectReference{Name: s}, Optional: ptr(true)}})
	}
	job := batchv1.JobSpec{
		BackoffLimit: ptr(int32(2)),
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: labels},
			Spec:       corev1.PodSpec{RestartPolicy: corev1.RestartPolicyNever, Containers: []corev1.Container{c}},
		},
	}
	if j.Timeout > 0 {
		job.ActiveDeadlineSeconds = ptr(int64(j.Timeout))
	}
	cj := &batchv1.CronJob{
		TypeMeta:   metav1.TypeMeta{APIVersion: "batch/v1", Kind: "CronJob"},
		ObjectMeta: metav1.ObjectMeta{Name: j.Name, Namespace: ns, Labels: labels},
		Spec: batchv1.CronJobSpec{
			Schedule:                   j.Schedule,
			ConcurrencyPolicy:          batchv1.ForbidConcurrent,
			SuccessfulJobsHistoryLimit: ptr(int32(3)),
			FailedJobsHistoryLimit:     ptr(int32(3)),
			JobTemplate:                batchv1.JobTemplateSpec{Spec: job},
		},
	}
	if j.TimeZone != "" {
		cj.Spec.TimeZone = ptr(j.TimeZone)
	}
	return cj
}

// appLabels mark everything rendimiento makes for an app.
func appLabels(app string) map[string]string {
	return map[string]string{LabelManagedBy: ManagedBy, LabelApp: app}
}

// svcLabels are an app's labels plus its service's, including the standard
// app.kubernetes.io ones other tools read.
func svcLabels(app, svc string) map[string]string {
	l := appLabels(app)
	l[LabelService] = svc
	l["app.kubernetes.io/name"] = svc
	l["app.kubernetes.io/part-of"] = app
	return l
}

// selector picks a service's pods; it never changes once a Deployment
// exists, since Kubernetes does not allow it.
func selector(app, svc string) map[string]string {
	return map[string]string{LabelApp: app, LabelService: svc}
}

// deployment runs a service: its image, port, command, environment and
// secrets, size (requests and limits), replicas, health checks, volume and
// GPU, keeping the last 5 versions for rollbacks. A service with a volume
// or a GPU uses Recreate (the old pod stops before the new one starts):
// either can be held by only one pod at a time.
func deployment(meta metav1.ObjectMeta, sel map[string]string, svc spec.Service, image string, gpu GPUProfile) *appsv1.Deployment {
	res := spec.ResourcesFor(svc.Size, svc.Resources)
	replicas := int32(svc.Replicas)
	history := int32(5)
	c := corev1.Container{
		Name:            svc.Name,
		Image:           image,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Ports:           []corev1.ContainerPort{{Name: "http", ContainerPort: int32(svc.Port), Protocol: corev1.ProtocolTCP}},
		Command:         svc.Command,
		Args:            svc.Args,
		Env:             env(svc),
		Resources:       requirements(res),
		SecurityContext: &corev1.SecurityContext{
			AllowPrivilegeEscalation: ptr(false),
			SeccompProfile:           &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
		},
	}
	for _, s := range svc.Secrets {
		c.EnvFrom = append(c.EnvFrom, corev1.EnvFromSource{SecretRef: &corev1.SecretEnvSource{
			LocalObjectReference: corev1.LocalObjectReference{Name: s},
			Optional:             ptr(true),
		}})
	}
	if svc.Health != nil {
		handler := corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: svc.Health.Path, Port: intstr.FromString("http")}}
		if svc.Health.TCP {
			handler = corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromString("http")}}
		}
		probe := func(initial, failures int32) *corev1.Probe {
			return &corev1.Probe{
				ProbeHandler:        handler,
				InitialDelaySeconds: initial,
				PeriodSeconds:       10,
				FailureThreshold:    failures,
				TimeoutSeconds:      int32(svc.Health.Timeout),
			}
		}
		c.ReadinessProbe = probe(5, 3)
		c.LivenessProbe = probe(30, 6)
	} else {
		// Without a declared health check, at least wait for the port to open
		// so a rollout never swaps in a pod that isn't listening yet.
		c.ReadinessProbe = &corev1.Probe{
			ProbeHandler:  corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromString("http")}},
			PeriodSeconds: 2, FailureThreshold: 3, TimeoutSeconds: 1,
		}
	}
	if svc.Volume == nil {
		// Give ingress-nginx time to drop the endpoint before the pod stops.
		c.Lifecycle = &corev1.Lifecycle{PreStop: &corev1.LifecycleHandler{Sleep: &corev1.SleepAction{Seconds: 5}}}
	}
	for i, f := range svc.SecretFiles {
		vol := fmt.Sprintf("secret-%d", i)
		c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{Name: vol, MountPath: f.Mount, ReadOnly: true})
	}
	for i, f := range svc.ConfigFiles {
		c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{Name: fmt.Sprintf("config-%d", i), MountPath: f.Mount, ReadOnly: true})
	}
	pod := corev1.PodSpec{Containers: []corev1.Container{c}}
	for i, f := range svc.ConfigFiles {
		pod.Volumes = append(pod.Volumes, corev1.Volume{Name: fmt.Sprintf("config-%d", i), VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: f.ConfigMap}, Optional: ptr(true)},
		}})
	}
	for i, f := range svc.SecretFiles {
		pod.Volumes = append(pod.Volumes, corev1.Volume{Name: fmt.Sprintf("secret-%d", i), VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{SecretName: f.Secret, Optional: ptr(true)},
		}})
	}
	strategy := appsv1.DeploymentStrategy{Type: appsv1.RollingUpdateDeploymentStrategyType}
	if svc.GPU > 0 {
		attachGPU(&pod, svc, gpu)
		// A GPU is held by one pod at a time: a surge pod would wait forever.
		strategy = appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType}
	}
	if svc.Volume != nil {
		// ReadWriteOnce Longhorn volumes cannot be attached to two pods at once.
		strategy = appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType}
		if g := svc.VolumeFSGroup(); g != nil {
			pod.SecurityContext = &corev1.PodSecurityContext{FSGroup: g}
		}
		pod.Volumes = append(pod.Volumes, corev1.Volume{Name: "data", VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: svc.ClaimName()},
		}})
		pod.Containers[0].VolumeMounts = append(pod.Containers[0].VolumeMounts, corev1.VolumeMount{Name: "data", MountPath: svc.Volume.Mount})
	}
	return &appsv1.Deployment{
		TypeMeta:   metav1.TypeMeta{APIVersion: "apps/v1", Kind: "Deployment"},
		ObjectMeta: meta,
		Spec: appsv1.DeploymentSpec{
			Replicas:             &replicas,
			RevisionHistoryLimit: &history,
			Selector:             &metav1.LabelSelector{MatchLabels: sel},
			Strategy:             strategy,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: merge(meta.Labels, sel)},
				Spec:       pod,
			},
		},
	}
}

// attachGPU gives the service's container its GPUs and what the cluster's
// GPU profile needs (runtime class, host paths, shared memory,
// environment).
func attachGPU(pod *corev1.PodSpec, svc spec.Service, gpu GPUProfile) {
	c := &pod.Containers[0]
	n := resource.MustParse(fmt.Sprint(svc.GPU))
	if c.Resources.Limits == nil {
		c.Resources.Limits = corev1.ResourceList{}
	}
	if c.Resources.Requests == nil {
		c.Resources.Requests = corev1.ResourceList{}
	}
	c.Resources.Limits[corev1.ResourceName(gpu.Resource)] = n
	c.Resources.Requests[corev1.ResourceName(gpu.Resource)] = n
	if gpu.RuntimeClass != "" {
		pod.RuntimeClassName = ptr(gpu.RuntimeClass)
	}
	keys := make([]string, 0, len(gpu.Env))
	for k := range gpu.Env {
		if _, own := svc.Env[k]; !own {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		c.Env = append(c.Env, corev1.EnvVar{Name: k, Value: gpu.Env[k]})
	}
	for i, p := range gpu.HostPaths {
		name := fmt.Sprintf("gpu-host-%d", i)
		pod.Volumes = append(pod.Volumes, corev1.Volume{Name: name, VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: p}}})
		c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{Name: name, MountPath: p, ReadOnly: true})
	}
	if gpu.SharedMemory != "" {
		size := resource.MustParse(gpu.SharedMemory)
		pod.Volumes = append(pod.Volumes, corev1.Volume{Name: "dshm", VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{Medium: corev1.StorageMediumMemory, SizeLimit: &size}}})
		c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{Name: "dshm", MountPath: "/dev/shm"})
	}
}

// lanService exposes a service on the local network: a LoadBalancer Service
// (MetalLB gives it an address from its pool, the requested one if set).
func lanService(meta metav1.ObjectMeta, sel map[string]string, svc spec.Service) *corev1.Service {
	port := svc.LAN.Port
	if port == 0 {
		port = 80
	}
	meta.Labels[LabelLAN] = "true"
	if svc.LAN.IP != "" {
		meta.Annotations = map[string]string{"metallb.io/loadBalancerIPs": svc.LAN.IP}
	}
	return &corev1.Service{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Service"},
		ObjectMeta: meta,
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeLoadBalancer,
			Selector: sel,
			Ports: []corev1.ServicePort{{Name: "lan", Port: int32(port), Protocol: corev1.ProtocolTCP,
				TargetPort: intstr.FromInt32(int32(svc.Port))}},
		},
	}
}

// env is a service's environment: PORT first, then its env in name order,
// then its secretEnv as references to Kubernetes secrets (values never
// appear in the object).
func env(svc spec.Service) []corev1.EnvVar {
	out := []corev1.EnvVar{{Name: "PORT", Value: fmt.Sprint(svc.Port)}}
	keys := make([]string, 0, len(svc.Env))
	for k := range svc.Env {
		if k != "PORT" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, corev1.EnvVar{Name: k, Value: svc.Env[k]})
	}
	keys = keys[:0]
	for k := range svc.SecretEnv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		name, key, _ := strings.Cut(svc.SecretEnv[k], "/")
		out = append(out, corev1.EnvVar{Name: k, ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: name}, Key: key, Optional: ptr(true),
		}}})
	}
	return out
}

// service exposes port 80 and, when different, the app's own port, so
// other services can keep calling it as before (e.g. stockpulse-api:8000).
func service(meta metav1.ObjectMeta, sel map[string]string, svc spec.Service) *corev1.Service {
	// Target the port number, not the "http" port name: pods deployed before
	// a takeover (e.g. by ArgoCD) may not name their port, and during the
	// rolling update both old and new pods must keep receiving traffic.
	target := intstr.FromInt32(int32(svc.Port))
	ports := []corev1.ServicePort{{Name: "http", Port: 80, TargetPort: target, Protocol: corev1.ProtocolTCP}}
	if svc.Port != 80 {
		ports = append(ports, corev1.ServicePort{Name: "app", Port: int32(svc.Port), TargetPort: target, Protocol: corev1.ProtocolTCP})
	}
	return &corev1.Service{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Service"},
		ObjectMeta: meta,
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: sel,
			Ports:    ports,
		},
	}
}

// merge is a copy of a with b's keys added over it.
func merge(a, b map[string]string) map[string]string {
	out := make(map[string]string, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

// ingress publishes a service on its domain, aliases and routes over HTTPS:
// cert-manager issues the certificate, HTTP redirects to HTTPS, and the
// service's own ingress annotations are added.
func ingress(meta metav1.ObjectMeta, svc spec.Service, opt Options) *networkingv1.Ingress {
	meta.Annotations = map[string]string{
		"cert-manager.io/cluster-issuer":                 opt.ClusterIssuer,
		"nginx.ingress.kubernetes.io/ssl-redirect":       "true",
		"nginx.ingress.kubernetes.io/force-ssl-redirect": "true",
	}
	if svc.Ingress != nil {
		for k, v := range svc.Ingress.Annotations {
			meta.Annotations[k] = v
		}
	}
	if svc.Streaming {
		meta.Annotations["nginx.ingress.kubernetes.io/proxy-buffering"] = "off"
		meta.Annotations["nginx.ingress.kubernetes.io/proxy-read-timeout"] = "3600"
		meta.Annotations["nginx.ingress.kubernetes.io/proxy-send-timeout"] = "3600"
	}
	pathType := networkingv1.PathTypePrefix
	return &networkingv1.Ingress{
		TypeMeta:   metav1.TypeMeta{APIVersion: "networking.k8s.io/v1", Kind: "Ingress"},
		ObjectMeta: meta,
		Spec: networkingv1.IngressSpec{
			IngressClassName: &opt.IngressClass,
			TLS:              tls(svc),
			Rules:            rules(svc, &pathType),
		},
	}
}

// tls is the ingress's certificates: one secret per group of hosts.
func tls(svc spec.Service) []networkingv1.IngressTLS {
	var out []networkingv1.IngressTLS
	for _, g := range svc.TLSGroups() {
		out = append(out, networkingv1.IngressTLS{SecretName: g[0].(string), Hosts: g[1].([]string)})
	}
	return out
}

// requirements builds requests and limits, leaving out unset ("") values.
func requirements(r spec.Resources) corev1.ResourceRequirements {
	list := func(cpu, mem string) corev1.ResourceList {
		l := corev1.ResourceList{}
		if cpu != "" {
			l[corev1.ResourceCPU] = resource.MustParse(cpu)
		}
		if mem != "" {
			l[corev1.ResourceMemory] = resource.MustParse(mem)
		}
		return l
	}
	return corev1.ResourceRequirements{Requests: list(r.CPURequest, r.MemRequest), Limits: list(r.CPULimit, r.MemLimit)}
}

// rules is the ingress's hosts, each with the paths that go to the
// service (/ for its domain and aliases, and the paths of its routes).
func rules(svc spec.Service, pathType *networkingv1.PathType) []networkingv1.IngressRule {
	var out []networkingv1.IngressRule
	index := map[string]int{}
	for _, r := range svc.AllRoutes() {
		path := networkingv1.HTTPIngressPath{
			Path:     r.Path,
			PathType: pathType,
			Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{
				Name: svc.Name, Port: networkingv1.ServiceBackendPort{Name: "http"},
			}},
		}
		if i, ok := index[r.Host]; ok {
			out[i].HTTP.Paths = append(out[i].HTTP.Paths, path)
			continue
		}
		index[r.Host] = len(out)
		out = append(out, networkingv1.IngressRule{
			Host:             r.Host,
			IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{Paths: []networkingv1.HTTPIngressPath{path}}},
		})
	}
	return out
}

// volume is a service's persistent disk: a claim of its size in the
// storage class rendimiento uses, mounted by one pod at a time.
func volume(meta metav1.ObjectMeta, svc spec.Service, opt Options) (*corev1.PersistentVolumeClaim, error) {
	size, err := resource.ParseQuantity(svc.Volume.Size)
	if err != nil {
		return nil, fmt.Errorf("service %s volume size: %w", svc.Name, err)
	}
	meta.Labels = merge(opt.VolumeLabels, meta.Labels)
	return &corev1.PersistentVolumeClaim{
		TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "PersistentVolumeClaim"},
		ObjectMeta: meta,
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			StorageClassName: &opt.StorageClass,
			Resources:        corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: size}},
		},
	}, nil
}

// ptr returns a pointer to a copy of v.
func ptr[T any](v T) *T { return &v }

// List returns every object in apply order: namespace, volumes, workloads, services, ingresses.
func (o *Objects) List() []metav1.Object {
	out := []metav1.Object{o.Namespace}
	for _, v := range o.Volumes {
		out = append(out, v)
	}
	for _, d := range o.Deployments {
		out = append(out, d)
	}
	for _, s := range o.Services {
		out = append(out, s)
	}
	for _, i := range o.Ingresses {
		out = append(out, i)
	}
	for _, c := range o.CronJobs {
		out = append(out, c)
	}
	return out
}

// YAML renders all objects as a multi-document manifest (for previews and tests).
func (o *Objects) YAML() ([]byte, error) {
	var buf []byte
	for i, obj := range o.List() {
		b, err := yaml.Marshal(obj)
		if err != nil {
			return nil, err
		}
		if i > 0 {
			buf = append(buf, "---\n"...)
		}
		buf = append(buf, b...)
	}
	return buf, nil
}

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/p0dxD/rendimiento.ai/internal/spec"
)

// AppSpec is the desired state: the repo's rendimiento.yaml plus the
// release pointer (one image per service) chosen by the pipeline or a rollback.
type AppSpec struct {
	// Repo is owner/name on GitHub, informational.
	Repo string `json:"repo,omitempty"`
	// Services mirrors rendimiento.yaml.
	// +kubebuilder:validation:MinItems=1
	Services []spec.Service `json:"services"`
	// Jobs mirrors the scheduled jobs in rendimiento.yaml.
	Jobs []spec.Job `json:"jobs,omitempty"`
	// SharedNamespace mirrors rendimiento.yaml: leave the namespace itself alone.
	SharedNamespace bool `json:"sharedNamespace,omitempty"`
	// Postgres and Redis mirror rendimiento.yaml's settings for `needs:`.
	Postgres *spec.PostgresOptions `json:"postgres,omitempty"`
	Redis    *spec.RedisOptions    `json:"redis,omitempty"`
	// Images maps service name to an image reference, ideally pinned by digest.
	// Empty until the first successful build.
	Images map[string]string `json:"images,omitempty"`
	// Release is the platform's release number the images belong to.
	Release int64 `json:"release,omitempty"`
	// Suspend stops reconciliation (manual changes are left alone).
	Suspend bool `json:"suspend,omitempty"`
	// Adopt takes over an existing namespace of the same name (an app
	// deployed another way, e.g. by ArgoCD). New workloads start next to the
	// old ones; traffic switches only when they are ready, then the old
	// workloads serving the app's domains are removed.
	Adopt bool `json:"adopt,omitempty"`
}

type Phase string

const (
	PhaseWaiting     Phase = "WaitingForBuild"
	PhaseProgressing Phase = "Progressing"
	PhaseHealthy     Phase = "Healthy"
	PhaseDegraded    Phase = "Degraded"
	PhaseSuspended   Phase = "Suspended"
	PhaseError       Phase = "Error"
)

type ServiceStatus struct {
	Name          string `json:"name"`
	Replicas      int32  `json:"replicas"`
	ReadyReplicas int32  `json:"readyReplicas"`
	Image         string `json:"image,omitempty"`
	URL           string `json:"url,omitempty"`
	// CertReady reports the cert-manager Certificate for the service's domain.
	CertReady bool   `json:"certReady"`
	DNSReady  bool   `json:"dnsReady"`
	Message   string `json:"message,omitempty"`
}

type AppStatus struct {
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	Phase              Phase              `json:"phase,omitempty"`
	Message            string             `json:"message,omitempty"`
	Release            int64              `json:"release,omitempty"`
	Services           []ServiceStatus    `json:"services,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
}

// App is one application managed by rendimiento. It is cluster-scoped
// because it owns the application's namespace.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Release",type=integer,JSONPath=`.status.release`
// +kubebuilder:printcolumn:name="Repo",type=string,JSONPath=`.spec.repo`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type App struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AppSpec   `json:"spec,omitempty"`
	Status AppStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type AppList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []App `json:"items"`
}

func init() { SchemeBuilder.Register(&App{}, &AppList{}) }

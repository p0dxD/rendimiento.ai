package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AddonSpec is software rendimiento installs and keeps in sync, rendered
// from a Helm chart or from Kubernetes manifests (a kustomize folder) in git.
type AddonSpec struct {
	// Namespace receives the add-on's namespaced objects that do not set one.
	Namespace string      `json:"namespace"`
	Source    AddonSource `json:"source"`
	// Values are Helm values (YAML). Ignored for git sources.
	Values string `json:"values,omitempty"`
	// ReleaseName is the Helm release name (default: the add-on's name).
	// Keep a migrated release's name so rendered labels stay identical.
	ReleaseName string `json:"releaseName,omitempty"`
	// Adopt takes over objects that already exist (e.g. installed by
	// ArgoCD or Helm). The first sync of an adopted add-on only proceeds if
	// it would change nothing, unless AllowAdoptChanges is set.
	Adopt             bool `json:"adopt,omitempty"`
	AllowAdoptChanges bool `json:"allowAdoptChanges,omitempty"`
	// Prune deletes objects that leave the source. CRDs, namespaces,
	// volumes and storage classes are never deleted.
	Prune bool `json:"prune,omitempty"`
	// ManualSync applies changes only when requested (SyncRequest raised),
	// after reviewing the preview. Otherwise changes apply automatically.
	ManualSync  bool  `json:"manualSync,omitempty"`
	SyncRequest int64 `json:"syncRequest,omitempty"`
	// Suspend stops syncing; the objects are left as they are.
	Suspend bool `json:"suspend,omitempty"`
	// CreateNamespace creates Namespace if it does not exist.
	CreateNamespace bool `json:"createNamespace,omitempty"`
	// SkipHooks never runs the chart's Helm hooks.
	SkipHooks bool `json:"skipHooks,omitempty"`
	// HookTimeout is how long a hook Job may run, in seconds (default 600).
	HookTimeout int `json:"hookTimeout,omitempty"`
	// Title, Category and Description describe the add-on in the UI.
	Title       string `json:"title,omitempty"`
	Category    string `json:"category,omitempty"`
	Description string `json:"description,omitempty"`
}

// AddonSource is exactly one of Helm or Git.
type AddonSource struct {
	Helm *HelmSource `json:"helm,omitempty"`
	Git  *GitSource  `json:"git,omitempty"`
}

// HelmSource is a chart in a classic (index.yaml) Helm repository.
type HelmSource struct {
	// Repo is the chart repository URL (https://charts.longhorn.io).
	Repo    string `json:"repo"`
	Chart   string `json:"chart"`
	Version string `json:"version"`
}

// GitSource is a folder of Kubernetes manifests (or a kustomization) in a GitHub repository.
type GitSource struct {
	// Repo is owner/name on GitHub.
	Repo string `json:"repo"`
	// Path is the folder holding kustomization.yaml (or plain manifests).
	Path string `json:"path"`
	// Revision is the commit to render; rendimiento pins it on each sync.
	Revision string `json:"revision,omitempty"`
}

// AddonPhase summarizes an add-on's state for people: Synced, OutOfSync, Blocked, Error, Suspended.
type AddonPhase string

const (
	AddonPending   AddonPhase = "Pending"
	AddonSynced    AddonPhase = "Synced"
	AddonOutOfSync AddonPhase = "OutOfSync" // manual sync: changes wait for approval
	AddonBlocked   AddonPhase = "Blocked"   // adoption would change live objects
	AddonError     AddonPhase = "Error"
	AddonSuspended AddonPhase = "Suspended"
)

// ObjectRef identifies one object the add-on manages.
type ObjectRef struct {
	Group     string `json:"group,omitempty"`
	Version   string `json:"version"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
}

// PreviewItem is what syncing would do to one object.
type PreviewItem struct {
	ObjectRef `json:",inline"`
	// Action is create, update or unchanged (or prune).
	Action string `json:"action"`
	// Diff is a unified diff of the changed fields (truncated).
	Diff string `json:"diff,omitempty"`
}

// Preview is what the next sync would do, from a server-side dry run of every object.
type Preview struct {
	Create    int `json:"create"`
	Update    int `json:"update"`
	Unchanged int `json:"unchanged"`
	Prune     int `json:"prune"`
	// Items lists the objects that would change (unchanged ones are only counted).
	Items []PreviewItem `json:"items,omitempty"`
}

// HookInfo describes one Helm hook of the chart and the events it runs at.
type HookInfo struct {
	Name   string   `json:"name"`
	Kind   string   `json:"kind"`
	Events []string `json:"events"`
}

// HookRun records one execution of a Helm hook.
type HookRun struct {
	Event    string      `json:"event"`
	Name     string      `json:"name"`
	Kind     string      `json:"kind"`
	Status   string      `json:"status"` // succeeded, failed
	Message  string      `json:"message,omitempty"`
	Finished metav1.Time `json:"finished"`
}

// AddonStatus is what the controller last observed and did.
type AddonStatus struct {
	ObservedGeneration int64      `json:"observedGeneration,omitempty"`
	Phase              AddonPhase `json:"phase,omitempty"`
	Message            string     `json:"message,omitempty"`
	// Revision is what was rendered: the chart version or the git commit.
	Revision string `json:"revision,omitempty"`
	// Objects is the inventory of what the add-on applied (used for pruning).
	Objects []ObjectRef `json:"objects,omitempty"`
	// Preview is the dry run from the last reconcile.
	Preview *Preview `json:"preview,omitempty"`
	// Hooks are the chart's Helm hooks and the events they run at.
	Hooks []HookInfo `json:"hooks,omitempty"`
	// HookRuns are the most recent hook executions, newest last.
	HookRuns []HookRun `json:"hookRuns,omitempty"`
	// AppliedHash identifies the chart, version, values and release name
	// last applied; a different one makes the next sync an upgrade.
	AppliedHash string       `json:"appliedHash,omitempty"`
	LastSynced  *metav1.Time `json:"lastSynced,omitempty"`
	// AppliedSyncRequest is the SyncRequest last honoured (manual sync).
	AppliedSyncRequest int64 `json:"appliedSyncRequest,omitempty"`
	// Adopted is set once an adopting add-on has synced for the first time.
	Adopted bool `json:"adopted,omitempty"`
}

// Addon is software installed from a Helm chart or git manifests.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=radd
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Namespace",type=string,JSONPath=`.spec.namespace`
// +kubebuilder:printcolumn:name="Revision",type=string,JSONPath=`.status.revision`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type Addon struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AddonSpec   `json:"spec,omitempty"`
	Status AddonStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type AddonList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Addon `json:"items"`
}

func init() { SchemeBuilder.Register(&Addon{}, &AddonList{}) }

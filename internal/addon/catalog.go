package addon

// Field is one setting a catalog entry offers in its install form. Key is
// the Helm values path it sets ("persistence.defaultClassReplicaCount").
type Field struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Type        string `json:"type"` // string, number or boolean
	Default     any    `json:"default,omitempty"`
	Description string `json:"description,omitempty"`
}

// CatalogEntry is something that can be installed with a few settings.
type CatalogEntry struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Category    string `json:"category"`
	Description string `json:"description"`
	Homepage    string `json:"homepage,omitempty"`
	// Kind is helm, or custom-helm / custom-git for the bring-your-own entries.
	Kind      string              `json:"kind"`
	Helm      *HelmSourceTemplate `json:"helm,omitempty"`
	Namespace string              `json:"namespace,omitempty"`
	Fields    []Field             `json:"fields,omitempty"`
	// ManualSync is recommended for software where an unreviewed change is
	// risky (storage).
	ManualSync bool `json:"manualSync,omitempty"`
	// Notes are shown before installing.
	Notes string `json:"notes,omitempty"`
}

type HelmSourceTemplate struct {
	Repo    string `json:"repo"`
	Chart   string `json:"chart"`
	Version string `json:"version"`
}

// Catalog lists what can be installed. Versions are ones known to work on
// this cluster (arm64); any other can be typed in when installing.
var Catalog = []CatalogEntry{
	{
		ID: "longhorn", Title: "Longhorn", Category: "storage", Kind: "helm", Namespace: "longhorn-system",
		Description: "Replicated block storage for the cluster: the volumes behind every database and app that keeps data.",
		Homepage:    "https://longhorn.io",
		Helm:        &HelmSourceTemplate{Repo: "https://charts.longhorn.io/", Chart: "longhorn", Version: "v1.10.2"},
		ManualSync:  true,
		Fields: []Field{
			{Key: "persistence.defaultClassReplicaCount", Label: "Replicas per volume", Type: "number", Default: 3, Description: "Copies of each volume, on different nodes."},
			{Key: "defaultSettings.defaultDataPath", Label: "Data path on each node", Type: "string", Default: "/var/lib/longhorn/"},
			{Key: "preUpgradeChecker.jobEnabled", Label: "Run the pre-upgrade checker job", Type: "boolean", Default: false, Description: "A pre-upgrade hook that checks an upgrade is safe before applying it."},
		},
		Notes: "Storage is critical: changes wait for a manual sync after you review the preview. Longhorn's Helm hooks run as with Helm: its post-upgrade job after an upgrade, and on uninstall its uninstaller, which refuses unless Longhorn's deleting-confirmation-flag setting is on.",
	},
	{
		ID: "hajimari", Title: "Hajimari", Category: "web", Kind: "helm", Namespace: "hajimari",
		Description: "A start page listing the cluster's apps with icons and links.",
		Homepage:    "https://github.com/toboshii/hajimari",
		Helm:        &HelmSourceTemplate{Repo: "https://hajimari.io", Chart: "hajimari", Version: "2.0.2"},
		Fields: []Field{
			{Key: "hajimari.title", Label: "Page title", Type: "string", Default: "Home Lab"},
			{Key: "hajimari.defaultEnable", Label: "List every ingress automatically", Type: "boolean", Default: true},
		},
	},
	{
		ID: "uptime-kuma", Title: "Uptime Kuma", Category: "monitoring", Kind: "helm", Namespace: "uptime-kuma",
		Description: "Uptime monitoring with a status page and notifications (e-mail, Telegram, Discord, …).",
		Homepage:    "https://github.com/louislam/uptime-kuma",
		Helm:        &HelmSourceTemplate{Repo: "https://dirsigler.github.io/uptime-kuma-helm", Chart: "uptime-kuma", Version: "4.2.0"},
		Fields: []Field{
			{Key: "volume.size", Label: "Data volume size", Type: "string", Default: "2Gi"},
			{Key: "volume.storageClassName", Label: "Storage class", Type: "string", Default: "longhorn"},
		},
	},
	{
		ID: "cloudnative-pg", Title: "CloudNativePG", Category: "database", Kind: "helm", Namespace: "cnpg-system",
		Description: "A PostgreSQL operator: create highly available Postgres clusters with backups by declaring a Cluster object.",
		Homepage:    "https://cloudnative-pg.io",
		Helm:        &HelmSourceTemplate{Repo: "https://cloudnative-pg.github.io/charts", Chart: "cloudnative-pg", Version: "0.29.1"},
	},
	{
		ID: "custom-helm", Title: "Any Helm chart", Category: "", Kind: "custom-helm",
		Description: "Install a chart from any Helm repository by its URL, name and version, with your own values.",
	},
	{
		ID: "custom-git", Title: "Manifests from a git folder", Category: "", Kind: "custom-git",
		Description: "Apply a folder of Kubernetes manifests (or a kustomization) from the gitops repository or another repo.",
	},
}

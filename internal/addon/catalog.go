package addon

import "github.com/p0dxD/rendimiento.ai/internal/i18n"

var M = i18n.M

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
	// Values are Helm values (YAML) the install form starts with, for
	// settings too big for fields; the fields are merged under them.
	Values string `json:"values,omitempty"`
}

// HelmSourceTemplate is a catalog entry's chart and default version.
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
		Description: M("Replicated block storage for the cluster: the volumes behind every database and app that keeps data."),
		Homepage:    "https://longhorn.io",
		Helm:        &HelmSourceTemplate{Repo: "https://charts.longhorn.io/", Chart: "longhorn", Version: "v1.10.2"},
		ManualSync:  true,
		Fields: []Field{
			{Key: "persistence.defaultClassReplicaCount", Label: M("Replicas per volume"), Type: "number", Default: 3, Description: M("Copies of each volume, on different nodes.")},
			{Key: "defaultSettings.defaultDataPath", Label: M("Data path on each node"), Type: "string", Default: "/var/lib/longhorn/"},
			{Key: "preUpgradeChecker.jobEnabled", Label: M("Run the pre-upgrade checker job"), Type: "boolean", Default: false, Description: M("A pre-upgrade hook that checks an upgrade is safe before applying it.")},
		},
		Notes: M("Storage is critical: changes wait for a manual sync after you review the preview. Longhorn's Helm hooks run as with Helm: its post-upgrade job after an upgrade, and on uninstall its uninstaller, which refuses unless Longhorn's deleting-confirmation-flag setting is on."),
	},
	{
		ID: "hajimari", Title: "Hajimari", Category: "web", Kind: "helm", Namespace: "hajimari",
		Description: M("A start page listing the cluster's apps with icons and links."),
		Homepage:    "https://github.com/toboshii/hajimari",
		Helm:        &HelmSourceTemplate{Repo: "https://hajimari.io", Chart: "hajimari", Version: "2.0.2"},
		Fields: []Field{
			{Key: "hajimari.title", Label: M("Page title"), Type: "string", Default: "Home Lab"},
			{Key: "hajimari.defaultEnable", Label: M("List every ingress automatically"), Type: "boolean", Default: true},
		},
	},
	{
		ID: "uptime-kuma", Title: "Uptime Kuma", Category: "monitoring", Kind: "helm", Namespace: "uptime-kuma",
		Description: M("Uptime monitoring with a status page and notifications (e-mail, Telegram, Discord, …)."),
		Homepage:    "https://github.com/louislam/uptime-kuma",
		Helm:        &HelmSourceTemplate{Repo: "https://dirsigler.github.io/uptime-kuma-helm", Chart: "uptime-kuma", Version: "4.2.0"},
		Fields: []Field{
			{Key: "volume.size", Label: M("Data volume size"), Type: "string", Default: "2Gi"},
			{Key: "volume.storageClassName", Label: M("Storage class"), Type: "string", Default: "longhorn"},
		},
	},
	{
		ID: "cloudnative-pg", Title: "CloudNativePG", Category: "database", Kind: "helm", Namespace: "cnpg-system",
		Description: M("A PostgreSQL operator: create highly available Postgres clusters with backups by declaring a Cluster object."),
		Homepage:    "https://cloudnative-pg.io",
		Helm:        &HelmSourceTemplate{Repo: "https://cloudnative-pg.github.io/charts", Chart: "cloudnative-pg", Version: "0.29.1"},
	},
	{
		ID: "opentelemetry-collector", Title: "OpenTelemetry Collector", Category: "monitoring", Kind: "helm", Namespace: "observa",
		Description: M("Receives traces from apps with `observe` and turns them into the service graph: calls per minute, errors and latency on every line of the Universe map."),
		Homepage:    "https://opentelemetry.io/docs/collector/",
		Helm:        &HelmSourceTemplate{Repo: "https://open-telemetry.github.io/opentelemetry-helm-charts", Chart: "opentelemetry-collector", Version: "0.175.1"},
		Fields: []Field{
			{Key: "config.exporters.prometheusremotewrite.endpoint", Label: M("Metrics store remote-write URL"), Type: "string",
				Default: "http://vmsingle.monitoring.svc.cluster.local:8428/api/v1/write", Description: M("Where the service graph is written: VictoriaMetrics or Prometheus with remote write on. The platform reads it back from METRICS_URL.")},
		},
		Notes:  M("After installing, set TELEMETRY_ENDPOINT on the platform to this collector (http://opentelemetry-collector.observa.svc.cluster.local:4318) and METRICS_URL to the metrics store, then add `observe: true` to an app's rendimiento.yaml. Traces themselves are not kept: only the numbers."),
		Values: otelCollectorValues,
	},
	{
		ID: "custom-helm", Title: M("Any Helm chart"), Category: "", Kind: "custom-helm",
		Description: M("Install a chart from any Helm repository by its URL, name and version, with your own values."),
	},
	{
		ID: "custom-git", Title: M("Manifests from a git folder"), Category: "", Kind: "custom-git",
		Description: M("Apply a folder of Kubernetes manifests (or a kustomization) from the gitops repository or another repo."),
	},
}

// otelCollectorValues run the collector as one small Deployment that takes
// traces over OTLP and Zipkin and keeps only the service graph and span
// metrics, written to the metrics store; the chart's Jaeger, Prometheus
// and logs parts are switched off.
const otelCollectorValues = `mode: deployment
image:
  repository: otel/opentelemetry-collector-contrib
resources:
  requests: { cpu: 50m, memory: 128Mi }
  limits: { memory: 400Mi }
ports:
  jaeger-compact: { enabled: false }
  jaeger-thrift: { enabled: false }
  jaeger-grpc: { enabled: false }
  zipkin: { enabled: true }
config:
  receivers:
    jaeger: null
    prometheus: null
  connectors:
    servicegraph:
      latency_histogram_buckets: [1ms, 2ms, 5ms, 10ms, 25ms, 50ms, 100ms, 250ms, 500ms, 1s, 2500ms, 5s, 10s]
      store: { ttl: 10s, max_items: 5000 }
      # Calls to something without traces (a database, a Go service, an
      # API outside the cluster) still become a line, named by these.
      virtual_node_peer_attributes: [peer.service, db.system, db.name, server.address, net.peer.name]
    spanmetrics:
      histogram:
        explicit:
          buckets: [1ms, 2ms, 5ms, 10ms, 25ms, 50ms, 100ms, 250ms, 500ms, 1s, 2500ms, 5s, 10s]
  processors:
    # Health checks (Kubernetes' probes, rendimiento's uptime checks) are
    # not traffic: without this every service gets a line from Internet.
    filter/health:
      error_mode: ignore
      traces:
        span:
          - IsMatch(attributes["user_agent.original"], "^(kube-probe|rendimiento-uptime)/")
          - IsMatch(attributes["http.user_agent"], "^(kube-probe|rendimiento-uptime)/")
  service:
    pipelines:
      logs: null
      traces:
        receivers: [otlp, zipkin]
        processors: [memory_limiter, filter/health, batch]
        exporters: [servicegraph, spanmetrics]
      metrics:
        receivers: [servicegraph, spanmetrics]
        processors: [memory_limiter, batch]
        exporters: [prometheusremotewrite]
`

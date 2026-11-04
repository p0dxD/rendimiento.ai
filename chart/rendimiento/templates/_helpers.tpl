{{/* The platform's image. */}}
{{- define "rendimiento.image" -}}
{{ .Values.image.repository }}:{{ .Values.image.tag | default .Chart.AppVersion }}
{{- end }}

{{/* The host of baseURL, for the Ingress. */}}
{{- define "rendimiento.host" -}}
{{ .Values.baseURL | trimPrefix "https://" | trimPrefix "http://" | trimSuffix "/" | splitList "/" | first }}
{{- end }}

{{/* Where builds reach BuildKit. */}}
{{- define "rendimiento.buildkit" -}}
{{- if .Values.buildkit.bundled -}}
tcp://rendimiento-buildkitd.{{ .Release.Namespace }}.svc.cluster.local:1234
{{- else -}}
{{ required "buildkit.address is required when buildkit.bundled is false" .Values.buildkit.address }}
{{- end }}
{{- end }}

{{- define "rendimiento.labels" -}}
app.kubernetes.io/managed-by: rendimiento
app.kubernetes.io/instance: {{ .Release.Name }}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version }}
{{- end }}

{{/* A secret value kept across upgrades: the one already in the cluster, or a new random one. */}}
{{- define "rendimiento.keep" -}}
{{- $s := lookup "v1" "Secret" .ns .name -}}
{{- if and $s (index ($s.data | default dict) .key) -}}
{{ index $s.data .key }}
{{- else -}}
{{ randAlphaNum 32 | b64enc }}
{{- end }}
{{- end }}

{{/*
Expand the name of the chart.
*/}}
{{- define "amp-evaluation-extension.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "amp-evaluation-extension.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "amp-evaluation-extension.labels" -}}
helm.sh/chart: {{ include "amp-evaluation-extension.chart" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: openchoreo
{{- end }}

{{/*
Get the registry endpoint for image references
Returns external endpoint if global.baseDomain is set, otherwise uses configured endpoint
*/}}
{{- define "amp-evaluation-extension.registryEndpoint" -}}
{{- if .Values.global.baseDomain -}}
  {{- printf "registry.%s" .Values.global.baseDomain -}}
{{- else -}}
  {{- .Values.global.registry.endpoint -}}
{{- end -}}
{{- end -}}

{{/*
Get the full amp-evaluation image reference
Returns the complete image path with registry endpoint if useLocalRegistry is true
Otherwise returns the repository:tag as-is for external registries
*/}}
{{- define "amp-evaluation-extension.image" -}}
{{- $repository := .Values.ampEvaluation.image.repository -}}
{{- $tag := .Values.ampEvaluation.image.tag | default .Chart.AppVersion -}}
{{- if .Values.ampEvaluation.useLocalRegistry -}}
  {{- $registry := include "amp-evaluation-extension.registryEndpoint" . -}}
  {{- printf "%s/%s:%s" $registry $repository $tag -}}
{{- else -}}
  {{- with .Values.global.ampImageRegistry -}}
    {{- $repository = printf "%s/%s" (trimSuffix "/" .) (base $repository) -}}
  {{- end -}}
  {{- printf "%s:%s" $repository $tag -}}
{{- end -}}
{{- end -}}

{{/*
Image pull secrets for the evaluation job pod.

The pod runs in the workflow namespace ("workflows-<environment>"), not this
chart's release namespace, so the named secrets have to exist there. The chart
does not create them: that keeps registry credentials out of Helm values and
the release secret, and matches how the installer provisions them per namespace.
*/}}
{{- define "amp-evaluation-extension.imagePullSecrets" -}}
imagePullSecrets:
{{- range .Values.global.imagePullSecrets }}
  - name: {{ . }}
{{- end }}
{{- end }}

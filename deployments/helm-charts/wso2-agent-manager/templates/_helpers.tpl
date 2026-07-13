{{/*
Expand the name of the chart.
*/}}
{{- define "agent-management-platform.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
Uses simple naming: "amp" instead of release-name-chart-name format
*/}}
{{- define "agent-management-platform.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- print "amp" | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "agent-management-platform.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "agent-management-platform.labels" -}}
helm.sh/chart: {{ include "agent-management-platform.chart" . }}
{{ include "agent-management-platform.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- with .Values.commonLabels }}
{{ toYaml . }}
{{- end }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "agent-management-platform.selectorLabels" -}}
app.kubernetes.io/name: {{ include "agent-management-platform.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
Uses simple naming: "amp" instead of release-name-chart-name
*/}}
{{- define "agent-management-platform.serviceAccountName" -}}
{{- if or .Values.serviceAccount.create .Values.rbac.create }}
{{- default "amp" .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
==============================================
Agent Manager Service Helpers
==============================================
*/}}

{{/*
Agent Manager Service fullname
Uses simple naming: "amp-api" instead of release-name-chart-name-agent-manager-service
*/}}
{{- define "agent-management-platform.agentManagerService.fullname" -}}
{{- print "amp-api" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Agent Manager Service labels
*/}}
{{- define "agent-management-platform.agentManagerService.labels" -}}
{{ include "agent-management-platform.labels" . }}
app.kubernetes.io/component: agent-manager-service
{{- end }}

{{/*
Agent Manager Service selector labels
*/}}
{{- define "agent-management-platform.agentManagerService.selectorLabels" -}}
{{ include "agent-management-platform.selectorLabels" . }}
app.kubernetes.io/component: agent-manager-service
{{- end }}

{{/*
==============================================
Console Helpers
==============================================
*/}}

{{/*
Console fullname
Uses simple naming: "amp-console" instead of release-name-chart-name-console
*/}}
{{- define "agent-management-platform.console.fullname" -}}
{{- print "amp-console" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Console labels
*/}}
{{- define "agent-management-platform.console.labels" -}}
{{ include "agent-management-platform.labels" . }}
app.kubernetes.io/component: console
{{- end }}

{{/*
Console selector labels
*/}}
{{- define "agent-management-platform.console.selectorLabels" -}}
{{ include "agent-management-platform.selectorLabels" . }}
app.kubernetes.io/component: console
{{- end }}

{{/*
==============================================
Database Helpers
==============================================
*/}}

{{/*
PostgreSQL host
Uses simple naming: "amp-postgresql" instead of release-name-postgresql
*/}}
{{- define "agent-management-platform.postgresql.host" -}}
{{- if .Values.postgresql.enabled }}
{{- print "amp-postgresql" }}
{{- else }}
{{- .Values.postgresql.external.host }}
{{- end }}
{{- end }}

{{/*
PostgreSQL port
*/}}
{{- define "agent-management-platform.postgresql.port" -}}
{{- if .Values.postgresql.enabled }}
{{- print "5432" }}
{{- else }}
{{- .Values.postgresql.external.port }}
{{- end }}
{{- end }}

{{/*
PostgreSQL database name
*/}}
{{- define "agent-management-platform.postgresql.database" -}}
{{- if .Values.postgresql.enabled }}
{{- .Values.postgresql.auth.database }}
{{- else }}
{{- .Values.postgresql.external.database }}
{{- end }}
{{- end }}

{{/*
PostgreSQL username
*/}}
{{- define "agent-management-platform.postgresql.username" -}}
{{- if .Values.postgresql.enabled }}
{{- .Values.postgresql.auth.username }}
{{- else }}
{{- .Values.postgresql.external.username }}
{{- end }}
{{- end }}

{{/*
PostgreSQL password secret name
Uses simple naming: "amp-postgresql" instead of release-name-postgresql
*/}}
{{- define "agent-management-platform.postgresql.secretName" -}}
{{- if .Values.postgresql.enabled }}
{{- if .Values.postgresql.auth.existingSecret }}
{{- .Values.postgresql.auth.existingSecret }}
{{- else }}
{{- print "amp-postgresql" }}
{{- end }}
{{- else }}
{{- if .Values.postgresql.external.existingSecret }}
{{- .Values.postgresql.external.existingSecret }}
{{- else }}
{{- print "amp-postgresql-external" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
PostgreSQL password secret key
*/}}
{{- define "agent-management-platform.postgresql.secretPasswordKey" -}}
{{- if .Values.postgresql.enabled }}
{{- print "password" }}
{{- else }}
{{- .Values.postgresql.external.existingSecretPasswordKey | default "password" }}
{{- end }}
{{- end }}

{{/*
==============================================
JWT Keys Secret Helpers
==============================================
*/}}

{{/*
JWT Keys Secret name
*/}}
{{- define "agent-management-platform.jwtKeysSecretName" -}}
{{- if .Values.jwtSigning.existingSecret }}
{{- .Values.jwtSigning.existingSecret }}
{{- else }}
{{- printf "%s-jwt-keys" (include "agent-management-platform.fullname" .) }}
{{- end }}
{{- end }}

{{/*
TLS Certificates Secret name
*/}}
{{- define "agent-management-platform.tlsCertsSecretName" -}}
{{- if .Values.agentManagerService.certificates.certificatesSecret }}
{{- .Values.agentManagerService.certificates.certificatesSecret }}
{{- else }}
{{- printf "%s-tls-certs" (include "agent-management-platform.fullname" .) }}
{{- end }}
{{- end }}

{{/*
==============================================
API Platform Gateway Helpers (issue #1131)
==============================================
*/}}

{{/*
Name of the standalone APIGateway CR (and its restapi-target label value).
*/}}
{{- define "agent-management-platform.apiGateway.name" -}}
{{- default "amp-platform-gateway" .Values.apiGateway.name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Name of the ConfigMap holding the gateway stack Helm values (configRef).
*/}}
{{- define "agent-management-platform.apiGateway.configMapName" -}}
{{- printf "%s-config" (include "agent-management-platform.apiGateway.name" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Name of the gateway runtime Service the operator creates for this instance.
The operator names the Helm release "<cr-name>-gateway"; the gateway chart's
fullname collapses to the release name (chart name "gateway" is a substring),
and the runtime Service is "<fullname>-gateway-runtime" — hence the doubled
"-gateway-gateway-runtime". Verified against the live per-env instance.
*/}}
{{- define "agent-management-platform.apiGateway.runtimeServiceName" -}}
{{- printf "%s-gateway-gateway-runtime" (include "agent-management-platform.apiGateway.name" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
API Platform Gateway labels
*/}}
{{- define "agent-management-platform.apiGateway.labels" -}}
{{ include "agent-management-platform.labels" . }}
app.kubernetes.io/component: api-platform-gateway
{{- end }}

{{/*
Resolve whether the gateway should enforce scopes. apiGateway.enforceScopes
wins when set ("true"/"false"); otherwise follow the service's RBAC setting so
the gateway never enforces scopes amp-api itself is configured to skip.
*/}}
{{- define "agent-management-platform.apiGateway.enforceScopes" -}}
{{- if kindIs "invalid" .Values.apiGateway.enforceScopes -}}
{{- .Values.agentManagerService.config.rbacEnabled -}}
{{- else if eq (toString .Values.apiGateway.enforceScopes) "" -}}
{{- .Values.agentManagerService.config.rbacEnabled -}}
{{- else -}}
{{- toString .Values.apiGateway.enforceScopes -}}
{{- end -}}
{{- end }}

{{/*
==============================================
Image Pull Secrets
==============================================
*/}}

{{/*
Image pull secrets
*/}}
{{- define "agent-management-platform.imagePullSecrets" -}}
{{- if .Values.global.imagePullSecrets }}
imagePullSecrets:
{{- range .Values.global.imagePullSecrets }}
  - name: {{ . }}
{{- end }}
{{- end }}
{{- end }}

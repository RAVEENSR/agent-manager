{{/*
==============================================
Production secret validation
==============================================
When agentManagerService.requireExternalSecrets is true, every credential
must be supplied via an existingSecret rather than a committed literal
default. Renders nothing on success; fails the render with a single
consolidated message listing every credential still missing an
existingSecret reference. No-op when the flag is false (the dev default).
*/}}
{{- define "agent-management-platform.validateProdSecrets" -}}
{{- if .Values.agentManagerService.requireExternalSecrets -}}
{{- $errors := list -}}
{{- $am := .Values.agentManagerService.config -}}
{{- if .Values.postgresql.enabled -}}
{{- if not .Values.postgresql.auth.existingSecret -}}
{{- $errors = append $errors "postgresql.auth.existingSecret (in-cluster Postgres still uses the committed literal password)" -}}
{{- end -}}
{{- else -}}
{{- if not .Values.postgresql.external.existingSecret -}}
{{- $errors = append $errors "postgresql.external.existingSecret" -}}
{{- end -}}
{{- end -}}
{{- if not $am.apiKey.existingSecret -}}
{{- $errors = append $errors "agentManagerService.config.apiKey.existingSecret" -}}
{{- end -}}
{{- if not $am.encryptionKey.existingSecret -}}
{{- $errors = append $errors "agentManagerService.config.encryptionKey.existingSecret (without it, secrets become unrecoverable across reinstalls)" -}}
{{- end -}}
{{- if not $am.openbao.existingSecret -}}
{{- $errors = append $errors "agentManagerService.config.openbao.existingSecret (still uses the dev root token)" -}}
{{- end -}}
{{- if not $am.workflowPlaneOpenbao.existingSecret -}}
{{- $errors = append $errors "agentManagerService.config.workflowPlaneOpenbao.existingSecret (still uses the dev root token)" -}}
{{- end -}}
{{- if not $am.thunder.existingSecret -}}
{{- $errors = append $errors "agentManagerService.config.thunder.existingSecret (still uses the committed client secret)" -}}
{{- end -}}
{{- if $errors -}}
{{- fail (printf "agentManagerService.requireExternalSecrets is enabled, but these credentials still lack an existingSecret reference:\n  - %s\nCreate each Kubernetes Secret and reference it (see values-prod.yaml), or unset requireExternalSecrets for a dev install." (join "\n  - " $errors)) -}}
{{- end -}}
{{- end -}}
{{- end -}}

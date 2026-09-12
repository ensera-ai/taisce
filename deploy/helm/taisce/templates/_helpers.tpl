{{/* Copyright 2026 The Taisce Authors. SPDX-License-Identifier: Apache-2.0 */}}
{{- define "taisce.name" -}}{{ .Release.Name }}-taisce{{- end -}}
{{- define "taisce.db" -}}{{ include "taisce.name" . }}-db{{- end -}}
{{- define "taisce.labels" -}}
app.kubernetes.io/name: taisce
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}
{{- define "taisce.image" -}}{{ .Values.image.repository }}:{{ .Values.image.tag | default .Chart.AppVersion }}{{- end -}}
{{- define "taisce.serviceAccount" -}}{{ .Values.serviceAccount.name | default (include "taisce.name" .) }}{{- end -}}
{{- define "taisce.credentialsSecret" -}}{{ .Values.credentials.existingSecret | default (printf "%s-credentials" (include "taisce.name" .)) }}{{- end -}}
{{/* Where the runtime roles connect: the operator's read-write service, or the external secret. */}}
{{- define "taisce.dbHost" -}}{{ include "taisce.db" . }}-rw{{- end -}}
{{/* The registry connection, for the roles that authenticate. A worker resolves no credentials, so it
     is never given the login that can read them. */}}
{{- define "taisce.registryEnv" -}}
{{- if eq .Values.postgresql.mode "external" }}
- name: TAISCE_REGISTRY_DSN
  valueFrom: {secretKeyRef: {name: {{ .Values.postgresql.external.existingSecret | quote }}, key: registryDSN}}
{{- else }}
- name: TAISCE_CONTROL_PASSWORD
  valueFrom: {secretKeyRef: {name: {{ include "taisce.credentialsSecret" . | quote }}, key: controlPassword}}
- name: TAISCE_REGISTRY_DSN
  value: "postgres://taisce_control:$(TAISCE_CONTROL_PASSWORD)@{{ include "taisce.dbHost" . }}:5432/taisce?sslmode=require"
{{- end }}
{{- end -}}
{{/* The environment every serving role shares (compose.yaml's x-service-env, in cluster form). */}}
{{- define "taisce.serviceEnv" -}}
- name: TAISCE_SCHEMA
  value: {{ .Values.schema | quote }}
{{- if eq .Values.postgresql.mode "external" }}
- name: TAISCE_MEMORY_DSN
  valueFrom: {secretKeyRef: {name: {{ .Values.postgresql.external.existingSecret | quote }}, key: memoryDSN}}
{{- else }}
- name: TAISCE_DATA_PASSWORD
  valueFrom: {secretKeyRef: {name: {{ include "taisce.credentialsSecret" . | quote }}, key: dataPassword}}
- name: TAISCE_MEMORY_DSN
  value: "postgres://taisce_data:$(TAISCE_DATA_PASSWORD)@{{ include "taisce.dbHost" . }}:5432/taisce?sslmode=require"
{{- end }}
- name: TAISCE_INFERENCE_ENDPOINT
  value: {{ .Values.inference.endpoint | quote }}
- name: TAISCE_INFERENCE_EXTRACTOR_MODEL
  value: {{ .Values.inference.extractorModel | quote }}
- name: TAISCE_INFERENCE_ALLOWLIST
  value: {{ .Values.inference.allowlist | quote }}
{{- if .Values.inference.apiKeySecret }}
- name: TAISCE_INFERENCE_API_KEY
  valueFrom: {secretKeyRef: {name: {{ .Values.inference.apiKeySecret | quote }}, key: apiKey}}
{{- end }}
- name: TAISCE_INFERENCE_EMBEDDING_ENDPOINT
  value: {{ .Values.inference.embedding.endpoint | quote }}
- name: TAISCE_INFERENCE_EMBEDDING_MODEL
  value: {{ .Values.inference.embedding.model | quote }}
- name: TAISCE_INFERENCE_EMBEDDING_REVISION
  value: {{ .Values.inference.embedding.revision | quote }}
{{- if .Values.inference.embedding.apiKeySecret }}
- name: TAISCE_INFERENCE_EMBEDDING_API_KEY
  valueFrom: {secretKeyRef: {name: {{ .Values.inference.embedding.apiKeySecret | quote }}, key: apiKey}}
{{- end }}
{{- end -}}
{{/* The administrative connection, the database owner with CREATEROLE: only bootstrap and the
manage role hold it. */}}
{{- define "taisce.adminEnv" -}}
{{- if eq .Values.postgresql.mode "external" }}
- name: TAISCE_ADMIN_DSN
  valueFrom: {secretKeyRef: {name: {{ .Values.postgresql.external.existingSecret | quote }}, key: adminDSN}}
{{- else }}
- name: PGPASSWORD_ADMIN
  valueFrom: {secretKeyRef: {name: {{ include "taisce.db" . }}-app, key: password}}
- name: TAISCE_ADMIN_DSN
  value: "postgres://taisce_admin:$(PGPASSWORD_ADMIN)@{{ include "taisce.dbHost" . }}:5432/taisce?sslmode=require"
{{- end }}
{{- end -}}
{{- define "taisce.securityContext" -}}
runAsNonRoot: true
runAsUser: 65532
runAsGroup: 65532
seccompProfile: {type: RuntimeDefault}
{{- end -}}
{{- define "taisce.containerSecurityContext" -}}
allowPrivilegeEscalation: false
readOnlyRootFilesystem: true
capabilities: {drop: [ALL]}
{{- end -}}

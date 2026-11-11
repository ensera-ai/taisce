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
{{/* How many connections a pool may open, appended to a DSN.

     pgx sizes a pool it is not told about at the number of CPUs the process can see. In a cluster
     that is a property of whichever node the scheduler picked, so the same release declares one
     ceiling on a 4-CPU node and eight times that on a 32-CPU one — and for the API the ceiling is
     not only a cache setting, because admission bounds concurrent work at one less than the memory
     pool. A bound nobody chose is not a bound. So every DSN this chart builds says what it may open.

     The external mode takes its DSNs from a secret the operator wrote, and this chart cannot append
     to a value it never sees: there the ceilings are the operator's to set, and the README says so. */}}
{{- define "taisce.pool" -}}{{ printf "&pool_max_conns=%d" (int .) }}{{- end -}}
{{/* The registry connection, for the roles that authenticate. A worker resolves no credentials, so it
     is never given the login that can read them.

     Takes a dict: root is the chart context, pool is this role's registry ceiling. A parameter
     rather than one shared number because the roles do different work against this database, and a
     single default wide enough for the busiest would be carried by every replica of the others. */}}
{{- define "taisce.registryEnv" -}}
{{- $root := .root -}}
{{- if eq $root.Values.postgresql.mode "external" }}
- name: TAISCE_REGISTRY_DSN
  valueFrom: {secretKeyRef: {name: {{ $root.Values.postgresql.external.existingSecret | quote }}, key: registryDSN}}
{{- else }}
- name: TAISCE_CONTROL_PASSWORD
  valueFrom: {secretKeyRef: {name: {{ include "taisce.credentialsSecret" $root | quote }}, key: controlPassword}}
- name: TAISCE_REGISTRY_DSN
  value: "postgres://taisce_control:$(TAISCE_CONTROL_PASSWORD)@{{ include "taisce.dbHost" $root }}:5432/taisce?sslmode=require{{ include "taisce.pool" .pool }}"
{{- end }}
{{- end -}}
{{/* The environment every serving role shares, as one definition rather than a copy per role: a
     connection string that drifted between them would be a defect nobody sees until one half is
     talking to the wrong database.

     Takes a dict: root is the chart context, pool is this role's memory ceiling. */}}
{{- define "taisce.serviceEnv" -}}
{{- $root := .root -}}
- name: TAISCE_SCHEMA
  value: {{ $root.Values.schema | quote }}
{{- if eq $root.Values.postgresql.mode "external" }}
- name: TAISCE_MEMORY_DSN
  valueFrom: {secretKeyRef: {name: {{ $root.Values.postgresql.external.existingSecret | quote }}, key: memoryDSN}}
{{- else }}
- name: TAISCE_DATA_PASSWORD
  valueFrom: {secretKeyRef: {name: {{ include "taisce.credentialsSecret" $root | quote }}, key: dataPassword}}
- name: TAISCE_MEMORY_DSN
  value: "postgres://taisce_data:$(TAISCE_DATA_PASSWORD)@{{ include "taisce.dbHost" $root }}:5432/taisce?sslmode=require{{ include "taisce.pool" .pool }}"
{{- end }}
- name: TAISCE_INFERENCE_ENDPOINT
  value: {{ $root.Values.inference.endpoint | quote }}
- name: TAISCE_INFERENCE_EXTRACTOR_MODEL
  value: {{ $root.Values.inference.extractorModel | quote }}
- name: TAISCE_INFERENCE_ALLOWLIST
  value: {{ $root.Values.inference.allowlist | quote }}
{{- /* Stated only when the operator states one. The binary refuses a ceiling it cannot read, so an
       empty variable here would be a setting somebody has to explain rather than an absent one. */}}
{{- with $root.Values.inference.maxOutputTokens }}
- name: TAISCE_INFERENCE_MAX_OUTPUT_TOKENS
  value: {{ . | toString | quote }}
{{- end }}
{{- if $root.Values.inference.apiKeySecret }}
- name: TAISCE_INFERENCE_API_KEY
  valueFrom: {secretKeyRef: {name: {{ $root.Values.inference.apiKeySecret | quote }}, key: apiKey}}
{{- end }}
- name: TAISCE_INFERENCE_EMBEDDING_ENDPOINT
  value: {{ $root.Values.inference.embedding.endpoint | quote }}
- name: TAISCE_INFERENCE_EMBEDDING_MODEL
  value: {{ $root.Values.inference.embedding.model | quote }}
- name: TAISCE_INFERENCE_EMBEDDING_REVISION
  value: {{ $root.Values.inference.embedding.revision | quote }}
{{- if $root.Values.inference.embedding.apiKeySecret }}
- name: TAISCE_INFERENCE_EMBEDDING_API_KEY
  valueFrom: {secretKeyRef: {name: {{ $root.Values.inference.embedding.apiKeySecret | quote }}, key: apiKey}}
{{- end }}
{{- end -}}
{{/* The administrative connection, the database owner with CREATEROLE: only bootstrap and the
manage role hold it.

Takes a dict: root is the chart context, pool is this role's admin ceiling. The ops centre serves
from this connection for the life of the pod, so it needs a stated ceiling as much as the API does;
bootstrap runs its statements in order and exits, and is given the floor the admission gate accepts
rather than a knob nobody would turn. */}}
{{- define "taisce.adminEnv" -}}
{{- $root := .root -}}
{{- if eq $root.Values.postgresql.mode "external" }}
- name: TAISCE_ADMIN_DSN
  valueFrom: {secretKeyRef: {name: {{ $root.Values.postgresql.external.existingSecret | quote }}, key: adminDSN}}
{{- else }}
- name: PGPASSWORD_ADMIN
  valueFrom: {secretKeyRef: {name: {{ include "taisce.db" $root }}-app, key: password}}
- name: TAISCE_ADMIN_DSN
  value: "postgres://taisce_admin:$(PGPASSWORD_ADMIN)@{{ include "taisce.dbHost" $root }}:5432/taisce?sslmode=require{{ include "taisce.pool" .pool }}"
{{- end }}
{{- end -}}
{{/* The database's memory limit, in MiB.

     Refuses a unit it cannot read rather than guessing. Every parameter below is a fraction of this
     number, so a quantity misread by a factor of 1024 would configure a database to reserve more
     shared memory than its container allows and it would not start — a failure whose cause is this
     line and whose symptom is somewhere else entirely. Gi and Mi are what a database limit is
     written in; anything else is told so. */}}
{{- define "taisce.dbMemoryMi" -}}
{{- $v := .Values.postgresql.cnpg.resources.limits.memory | toString -}}
{{- if hasSuffix "Gi" $v -}}
{{- mul (trimSuffix "Gi" $v | int) 1024 -}}
{{- else if hasSuffix "Mi" $v -}}
{{- trimSuffix "Mi" $v | int -}}
{{- else -}}
{{- fail (printf "postgresql.cnpg.resources.limits.memory is %q: write it in Gi or Mi, because the database's shared_buffers, effective_cache_size, maintenance_work_mem, max_wal_size and shared-memory volume are all derived from it and a unit this chart cannot read would derive them wrongly." $v) -}}
{{- end -}}
{{- end -}}
{{/* The PostgreSQL parameters that are fractions of the memory limit, and nothing else.

     These four were written for the 32Gi limit they sat beside in values.yaml. Lowering the limit on
     its own left a database configured to reserve more than its container allowed, which does not
     start — and the README's own instructions for shrinking the deployment changed the replica counts
     and nothing else, so a reader following them had no reason to expect it. A parameter an operator
     must remember to change in step with another is the kind of rule that gets broken under pressure,
     so the arithmetic is here and the two cannot disagree.

     The fractions reproduce the previous defaults exactly at 32Gi: a quarter for shared_buffers,
     eleven sixteenths for the cache estimate, a sixteenth for maintenance_work_mem, a half for
     max_wal_size. An operator who states one of these in `parameters` still wins: these are the
     defaults underneath, not a ceiling over the top.

     work_mem is not here. It is memory per sort, so it scales with how many queries run at once
     rather than with how much memory the server has, and deriving it from the limit would tie it to
     the wrong number. */}}
{{- define "taisce.derivedParameters" -}}
{{- $mi := include "taisce.dbMemoryMi" . | int -}}
shared_buffers: {{ printf "%dMB" (div (mul $mi 25) 100) | quote }}
effective_cache_size: {{ printf "%dMB" (div (mul $mi 11) 16) | quote }}
maintenance_work_mem: {{ printf "%dMB" (div $mi 16) | quote }}
max_wal_size: {{ printf "%dMB" (div $mi 2) | quote }}
{{- end -}}
{{/* A whole number of MiB, written the way the API server stores it.

     A quantity has many spellings and Kubernetes keeps one: the largest binary suffix that leaves a
     whole number, so 1024Mi is stored as 1Gi. Anything that compares a declared quantity with a
     stored one by spelling sees two values where there is one amount, and the database operator
     compares pod volumes exactly that way — a spelling that differs is a pod it replaces, and the
     replacement is stored under the same spelling, so it never converges.

     Mi, Gi and Ti are handled. A volume of 1024Ti or more would be respelled in Pi and is not; no
     memory limit this chart can be given reaches it. */}}
{{- define "taisce.storedQuantity" -}}
{{- $mi := . | int -}}
{{- if eq (mod $mi 1048576) 0 -}}
{{- printf "%dTi" (div $mi 1048576) -}}
{{- else if eq (mod $mi 1024) 0 -}}
{{- printf "%dGi" (div $mi 1024) -}}
{{- else -}}
{{- printf "%dMi" $mi -}}
{{- end -}}
{{- end -}}
{{/* The shared-memory volume, derived unless an operator states it.

     An eighth of the limit, which is twice maintenance_work_mem at every limit: parallel index
     builds put their work there, so a volume smaller than that bound is one an index build fails in.
     Keeping it a fraction of the same number keeps that relation true without anyone maintaining it.

     A stated size is read and respelled rather than passed through, and one that cannot be read is
     refused. Passing it through was the alternative, and it leaves the operator one keystroke from
     the loop described above: 1024Mi, 1G and 1.5Gi are all valid quantities and none is stored as
     written. Mi and Gi in whole numbers is what the memory limit beside it is written in, so the
     rule is one an operator has already met. */}}
{{- define "taisce.sharedMemory" -}}
{{- $stated := .Values.postgresql.cnpg.sharedMemory | toString -}}
{{- if not .Values.postgresql.cnpg.sharedMemory -}}
{{- include "taisce.storedQuantity" (div (include "taisce.dbMemoryMi" . | int) 8) -}}
{{- else if regexMatch "^[1-9][0-9]*Gi$" $stated -}}
{{- include "taisce.storedQuantity" (mul (trimSuffix "Gi" $stated | int) 1024) -}}
{{- else if regexMatch "^[1-9][0-9]*Mi$" $stated -}}
{{- include "taisce.storedQuantity" (trimSuffix "Mi" $stated | int) -}}
{{- else -}}
{{- fail (printf "postgresql.cnpg.sharedMemory is %q: write it as a whole number of Mi or Gi. The database operator compares this size with the one the API server stored, by spelling, and a quantity stored under a different spelling than it was written in makes it replace a replica without end." $stated) -}}
{{- end -}}
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

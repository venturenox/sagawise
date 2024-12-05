{{/*
Expand the name of the chart.
*/}}
{{- define "sagawise.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Generate release name for the sagawise chart.
*/}}
{{- define "relname" -}}
{{- printf .Release.Name | trunc 24 | trimSuffix "-" -}}
{{- end -}}


{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "sagawise.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "sagawise.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "sagawise.labels" -}}
helm.sh/chart: {{ include "sagawise.chart" . }}
{{ include "sagawise.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "sagawise.selectorLabels" -}}
app.kubernetes.io/name: {{ include "sagawise.name" . }}
app.kubernetes.io/instance: {{ include "relname" . }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "sagawise.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "sagawise.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/* Generate the fullname of postgresql subchart */}}
{{- define "sagawise.postgresql.fullname" -}}
{{- printf "%s-%s" .Release.Name "postgresql" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/* Generate the fullname of redis subchart */}}
{{- define "sagawise.redis.fullname" -}}
{{- printf "%s-%s" .Release.Name "redis" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/* Defining default application port */}}
{{- define "sagawise.application.port" -}}
{{ default 5000 }}
{{- end }}
{{- define "snippet.redis.env" -}}
- name: REDIS_CONNECTION_STRING
  value: {{ include "snippet.redis.connection.string" . }}
- name: REDIS_HOST
  value: {{ include "snippet.redis.host" . }}
- name: REDIS_PORT
  value: {{ include "snippet.redis.port" . | quote }}
- name: REDIS_PASSWORD
  value: {{ include "snippet.redis.password" . | quote }}
{{- end }}

{{- define "snippet.redis.connection.string" -}}
{{- if .Values.redis.enabled -}}
  redis://{{ include "snippet.redis.host" . }}:{{ include "snippet.redis.port" . }}
{{- else -}}
  redis://default:{{ include "snippet.redis.password" . }}@{{ include "snippet.redis.host" . }}:{{ include "snippet.redis.port" . }}
{{- end }}
{{- end }}


{{- define "snippet.redis.host" -}}
{{ if not .Values.redis.enabled -}}
    {{- .Values.externalRedis.host -}}
{{- else -}}
    {{- include "sagawise.redis.fullname" . }}-master.{{ include "relname" . }}
{{- end -}}
{{- end -}}


{{- define "snippet.redis.port" -}}
{{ default 6379 }}
{{- end -}}


{{- define "snippet.redis.password" -}}
  {{- .Values.externalRedis.password -}}
{{- end -}}


{{- define "snippet.postgresql.env" -}}
- name: POSTGRES_HOST
  value: {{ include "snippet.postgresql.host" . }}
- name: POSTGRES_PORT
  value: {{ include "snippet.postgresql.port" . | quote }}
- name: POSTGRES_USERNAME
  value: {{ include "snippet.postgresql.username" . }}  
- name: POSTGRES_PASSWORD
  value: {{ include "snippet.postgresql.password" . }}
- name: POSTGRES_DATABASE
  value: {{ include "snippet.postgresql.database" . }}
{{- end }}


{{- define "snippet.postgresql.host" -}}
{{ if not .Values.postgresql.enabled -}}
  {{ required "externalPostgresql.host is required if database.type=postgres and not postgresql.enabled" .Values.externalPostgresql.host }}
{{- else -}}
  {{ include "sagawise.postgresql.fullname" . }}.{{ include "relname" . }}
{{- end }}
{{- end }}


{{- define "snippet.postgresql.port" -}}
{{ default 5432 }}
{{- end }}


{{- define "snippet.postgresql.username" -}}
{{ if not .Values.postgresql.enabled -}}
  {{ .Values.externalPostgresql.username | default "postgres" }}
{{- else -}}
  {{ .Values.postgresql.auth.username | default "postgres" }}
{{- end }}
{{- end }}


{{- define "snippet.postgresql.password" -}}
{{ if not .Values.postgresql.enabled -}}
  {{ .Values.externalPostgresql.password }}
{{- else -}}
- name: POSTGRES_PASSWORD
  valueFrom:
    secretKeyRef:
      name: psql-postgresql
      key: postgres-password
{{- end }}
{{- end }}


{{- define "snippet.postgresql.database" -}}
{{ if not .Values.postgresql.enabled -}}
  {{ .Values.externalPostgresql.database | default "sagawise" }}
{{- else -}}
  {{ .Values.postgresql.auth.database | default "sagawise" }}
{{- end }}
{{- end }}

{{- define "snippet.server.env" -}}
- name: SERVER_ENV
  value: development
{{- end }}


{{- define "snippet.sagawise.server.env" -}}
{{ include "snippet.redis.env" . }}
{{ include "snippet.postgresql.env" . }}
{{ include "snippet.server.env" . }}
{{- end }}
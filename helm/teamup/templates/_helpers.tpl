{{/*
Expand the name of the chart.
*/}}
{{- define "teamup.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Fully qualified app name. Truncated at 63 chars because some Kubernetes name
fields are limited to that (by the DNS naming spec).
*/}}
{{- define "teamup.fullname" -}}
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
Chart name and version, as used by the helm.sh/chart label.
*/}}
{{- define "teamup.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels.
*/}}
{{- define "teamup.labels" -}}
helm.sh/chart: {{ include "teamup.chart" . }}
{{ include "teamup.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: teamup
{{- end }}

{{/*
Selector labels. These end up in an immutable Deployment field, so nothing
version-dependent may appear here.
*/}}
{{- define "teamup.selectorLabels" -}}
app.kubernetes.io/name: {{ include "teamup.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Name of the service account to use.
*/}}
{{- define "teamup.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "teamup.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Name of the ConfigMap holding the non-secret TEAMUP_* variables.
*/}}
{{- define "teamup.configMapName" -}}
{{- printf "%s-config" (include "teamup.fullname" .) }}
{{- end }}

{{/*
Name of the Secret holding the credential-bearing TEAMUP_* variables. Either
one the operator brought, or one this chart creates.
*/}}
{{- define "teamup.secretName" -}}
{{- if .Values.secrets.existingSecret }}
{{- .Values.secrets.existingSecret }}
{{- else }}
{{- printf "%s-secrets" (include "teamup.fullname" .) }}
{{- end }}
{{- end }}

{{/*
The container image reference. An empty image.tag follows the chart's
appVersion, which is what a chart released alongside the binary should do.
*/}}
{{- define "teamup.image" -}}
{{- printf "%s:%s" .Values.image.repository (default .Chart.AppVersion .Values.image.tag) }}
{{- end }}

{{/*
The credential variables this chart is able to supply, as name/value pairs,
with the empty ones dropped.

Deliberately omitting empty values rather than writing them as "": the server
parses its configuration with caarlos0/env, whose `required` tag is satisfied
by a variable that is set but empty. Writing TEAMUP_DATABASE_URL="" would
therefore silence the startup check and turn a clear boot-time error into an
obscure failure on the first query. Left unset, the server refuses to start
and names every variable it is missing.
*/}}
{{- define "teamup.secretData" -}}
{{- $s := .Values.secrets -}}
{{- $pairs := dict
  "TEAMUP_DATABASE_URL" $s.databaseURL
  "TEAMUP_REDIS_URL" $s.redisURL
  "TEAMUP_MINIO_ACCESS_KEY" $s.minioAccessKey
  "TEAMUP_MINIO_SECRET_KEY" $s.minioSecretKey
  "TEAMUP_LIVEKIT_API_KEY" $s.livekitAPIKey
  "TEAMUP_LIVEKIT_API_SECRET" $s.livekitAPISecret
  "TEAMUP_AUTH_SECRET" $s.authSecret
  "TEAMUP_VAPID_PUBLIC_KEY" $s.vapidPublicKey
  "TEAMUP_VAPID_PRIVATE_KEY" $s.vapidPrivateKey
  "TEAMUP_SMTP_PASSWORD" $s.smtpPassword
  "TEAMUP_GOOGLE_CLIENT_SECRET" $s.googleClientSecret
  "TEAMUP_ENTRA_CLIENT_SECRET" $s.entraClientSecret
  "TEAMUP_CUSTOM_CLIENT_SECRET" $s.customClientSecret
-}}
{{- range $k, $v := $pairs }}
{{- if $v }}
{{ $k }}: {{ $v | b64enc | quote }}
{{- end }}
{{- end }}
{{- end }}

{{/*
The required variables that neither the chart values nor an existing Secret
are known to provide. Used by NOTES.txt to warn before the pod crash-loops.
Renders a space-separated list; empty when nothing is missing.
*/}}
{{- define "teamup.missingRequired" -}}
{{- $missing := list -}}
{{- if not .Values.config.minio.endpoint }}{{ $missing = append $missing "TEAMUP_MINIO_ENDPOINT" }}{{ end -}}
{{- if not .Values.config.minio.bucket }}{{ $missing = append $missing "TEAMUP_MINIO_BUCKET" }}{{ end -}}
{{- if not .Values.config.livekit.url }}{{ $missing = append $missing "TEAMUP_LIVEKIT_URL" }}{{ end -}}
{{- if not .Values.secrets.existingSecret -}}
{{- if not .Values.secrets.databaseURL }}{{ $missing = append $missing "TEAMUP_DATABASE_URL" }}{{ end -}}
{{- if not .Values.secrets.redisURL }}{{ $missing = append $missing "TEAMUP_REDIS_URL" }}{{ end -}}
{{- if not .Values.secrets.minioAccessKey }}{{ $missing = append $missing "TEAMUP_MINIO_ACCESS_KEY" }}{{ end -}}
{{- if not .Values.secrets.minioSecretKey }}{{ $missing = append $missing "TEAMUP_MINIO_SECRET_KEY" }}{{ end -}}
{{- if not .Values.secrets.livekitAPIKey }}{{ $missing = append $missing "TEAMUP_LIVEKIT_API_KEY" }}{{ end -}}
{{- if not .Values.secrets.livekitAPISecret }}{{ $missing = append $missing "TEAMUP_LIVEKIT_API_SECRET" }}{{ end -}}
{{- end -}}
{{- join " " $missing -}}
{{- end }}

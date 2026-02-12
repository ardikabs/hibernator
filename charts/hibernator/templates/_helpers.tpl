{{- include "hibernator.apiVersion" }}
{{- include "hibernator.apiKind" }}

{{/*
Expand the name of the chart.
*/}}
{{- define "hibernator.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "hibernator.fullname" -}}
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
{{- define "hibernator.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "hibernator.labels" -}}
helm.sh/chart: {{ include "hibernator.chart" . }}
{{ include "hibernator.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- with .Values.labels }}
{{ toYaml . }}
{{- end }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "hibernator.selectorLabels" -}}
app.kubernetes.io/name: {{ include "hibernator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "hibernator.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "hibernator.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Create the name of the runner service account to use
*/}}
{{- define "hibernator.runnerServiceAccountName" -}}
{{- if .Values.runnerServiceAccount.create }}
{{- default "hibernator-runner" .Values.runnerServiceAccount.name }}
{{- else }}
{{- default "hibernator-runner" .Values.runnerServiceAccount.name }}
{{- end }}
{{- end }}

{{/*
Get webhook CA bundle from Secret or use provided value
*/}}
{{- define "hibernator.webhook.caBundle" -}}
{{- if .Values.webhook.certManager.enabled -}}
{{- /* cert-manager will inject the CA bundle automatically */ -}}
{{- else -}}
{{- $secret := lookup "v1" "Secret" .Release.Namespace .Values.webhook.certs.secretName -}}
{{- if $secret -}}
{{- /* Use ca from the generated Secret */ -}}
{{- index $secret.data "ca" | default "" -}}
{{- else -}}
{{- /* Fallback to manually provided CA bundle (if any) */ -}}
{{- .Values.webhook.certs.caBundle | default "" | b64enc -}}
{{- end -}}
{{- end -}}
{{- end -}}

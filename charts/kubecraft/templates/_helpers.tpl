{{- define "kubecraft.fullname" -}}
{{- if contains .Chart.Name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "kubecraft.baseLabels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
app.kubernetes.io/part-of: kubecraft
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "kubecraft.labels" -}}
{{ include "kubecraft.baseLabels" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "kubecraft.managerSelector" -}}
app.kubernetes.io/name: kubecraft-manager
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "kubecraft.routerSelector" -}}
app.kubernetes.io/name: mc-router
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "kubecraft.routerName" -}}
{{- printf "%s-mc-router" (include "kubecraft.fullname" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "kubecraft.secretName" -}}
{{- default (printf "%s-secrets" (include "kubecraft.fullname" .)) .Values.secrets.existingSecret -}}
{{- end -}}

{{- define "kubecraft.image" -}}
{{- printf "%s:%s" .Values.image.repository (default .Chart.AppVersion .Values.image.tag) -}}
{{- end -}}

{{- define "kubecraft.containerSecurity" -}}
allowPrivilegeEscalation: false
readOnlyRootFilesystem: true
runAsNonRoot: true
capabilities:
  drop: [ALL]
{{- end -}}

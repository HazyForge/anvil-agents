{{- define "anvil-agents.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "anvil-agents.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name (include "anvil-agents.name" .) | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}

{{- define "anvil-agents.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "anvil-agents.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- required "serviceAccount.name is required when serviceAccount.create=false" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

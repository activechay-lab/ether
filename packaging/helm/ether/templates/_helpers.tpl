{{- define "ether.name" -}}
{{- printf "%s-ether" .Release.Name | trunc 45 | trimSuffix "-" -}}
{{- end -}}
{{- define "ether.labels" -}}
app.kubernetes.io/name: ether
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | quote }}
{{- end -}}
{{- define "ether.selector" -}}
app.kubernetes.io/name: ether
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}
{{- define "ether.image" -}}
{{- if .Values.image.digest -}}
{{ .Values.image.repository }}@{{ .Values.image.digest }}
{{- else -}}
{{ .Values.image.repository }}:{{ .Values.image.tag }}
{{- end -}}
{{- end -}}

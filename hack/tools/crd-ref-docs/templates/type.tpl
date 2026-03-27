{{ define "type" -}}

### {{ .Name }}

{{ .Doc }}

{{ if .Members -}}
| Field | Type | Description | Validation |
| ----- | ---- | ----------- | ---------- |
{{ range .Members -}}
{{ template "type_members" . }}
{{ end -}}
{{- end -}}

{{ end -}}

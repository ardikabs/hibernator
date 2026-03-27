{{ define "gvDetails" -}}

## {{ .GroupVersionString }}

{{ .Doc }}

{{- if .Kinds }}

### Resource Types

{{ range .Kinds -}}
- {{ . }}
{{ end }}
{{- end }}

{{ range .SortedTypes -}}
{{ template "type" . }}
{{ end -}}

{{ end -}}

{{ define "gvList" -}}
# API Reference

## Packages

{{ range . -}}
- {{ markdownRenderGVLink . }}
{{ end -}}

{{ range . -}}
{{ template "gvDetails" . }}
{{ end -}}
{{ end -}}

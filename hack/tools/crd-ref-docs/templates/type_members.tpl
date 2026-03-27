{{ define "type_members" -}}
{{- if eq .Name "metadata" -}}
| `metadata` | _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.31/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. | |
{{- else -}}
| `{{ .Name }}` | _{{ markdownRenderType .Type }}_ | {{ markdownRenderFieldDoc .Doc }} | {{ .Validation }} |
{{- end -}}
{{- end -}}

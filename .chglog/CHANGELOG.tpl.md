{{- range .Versions -}}
<a name="{{ .Tag.Name }}"></a>

## {{ if .Tag.Previous }}[{{ .Tag.Name }}]({{ $.Info.RepositoryURL }}/compare/{{ .Tag.Previous.Name }}...{{ .Tag.Name }}){{ else }}{{ .Tag.Name }}{{ end }} ({{ datetime "2006-01-02" .Tag.Date }})

{{ range .CommitGroups -}}
{{- $group := . -}}
{{- $printedAny := false -}}

{{- /* First pass: Check if there is anything to print */ -}}
{{- range .Commits -}}
  {{- if not (or (eq .Scope "charts") (eq .Scope "release")) -}}
    {{- $printedAny = true -}}
  {{- end -}}
{{- end -}}

{{- /* Only render the group if we found valid commits */ -}}
{{- if $printedAny -}}

### {{ $group.Title }}
{{ range $group.Commits -}}
  {{- if not (or (eq .Scope "charts") (eq .Scope "release")) }}
* {{ if .Scope }}**{{ .Scope }}:** {{ end }}{{ .Subject }}
  {{- end }}
{{- end }}
{{ end }}
{{ end -}}

{{- if .RevertCommits -}}
### Reverts

{{ range .RevertCommits -}}
* {{ .Revert.Header }}
{{ end }}
{{- end -}}

{{- if .NoteGroups -}}
{{ range .NoteGroups -}}
### {{ .Title }}

{{ range .Notes -}}
{{ .Body }}
{{- end }}
{{ end -}}
{{- end -}}
{{- end -}}

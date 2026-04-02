/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package telegram

// DefaultTemplate is the built-in Go template for Telegram notifications.
// Dynamic values are piped through escapeHTML to prevent HTML injection when
// parse_mode is set to HTML. Headlines are operation-aware.
var DefaultTemplate = `{{ if eq .Event "Start" -}}
{{ if eq .Operation "shutdown" -}}
▶️ <b>Hibernation Starting</b> ({{ len .Targets }} targets)
{{ else -}}
▶️ <b>Wake-Up Starting</b> ({{ len .Targets }} targets)
{{ end -}}
{{ else if eq .Event "Success" -}}
{{ if eq .Operation "shutdown" -}}
✅ <b>Hibernation Completed</b>
{{ else -}}
✅ <b>Wake-Up Completed</b>
{{ end -}}
{{ else if eq .Event "Failure" -}}
{{ if eq .Operation "shutdown" -}}
🔴 <b>Hibernation Failed</b>
{{ else -}}
🔴 <b>Wake-Up Failed</b>
{{ end -}}
{{ else if eq .Event "Recovery" -}}
{{ if eq .Operation "shutdown" -}}
♻️ <b>Hibernation Retrying</b> (attempt {{ .RetryCount }})
{{ else -}}
♻️ <b>Wake-Up Retrying</b> (attempt {{ .RetryCount }})
{{ end -}}
{{ else -}}
ℹ️ <b>Phase Change</b>
{{ end -}}
<b>Plan:</b> {{ .Plan.Name | escapeHTML }}
<b>Namespace:</b> {{ .Plan.Namespace | escapeHTML }}
<b>Phase:</b> {{ .Phase | escapeHTML }}
<b>Operation:</b> {{ .Operation | default "N/A" | escapeHTML }}
{{ if .PreviousPhase -}}
<b>Previous Phase:</b> {{ .PreviousPhase | escapeHTML }}
{{ end -}}
{{ if .ErrorMessage -}}
<b>Error:</b> {{ .ErrorMessage | escapeHTML }}
{{ end -}}
<b>Timestamp:</b> {{ .Timestamp | date "2006-01-02 15:04:05 MST" | escapeHTML }}
{{ if .Targets -}}
<b>Targets:</b>
{{ range .Targets -}}
• {{ .Name | escapeHTML }} ({{ .Executor | escapeHTML }}): {{ .State | escapeHTML }}{{ if .Connector.AccountID }} | Account: {{ .Connector.AccountID | escapeHTML }}{{ end }}{{ if .Connector.ClusterName }} | Cluster: {{ .Connector.ClusterName | escapeHTML }}{{ end }}{{ if .Connector.Region }} | Region: {{ .Connector.Region | escapeHTML }}{{ end }}
{{ end -}}
{{ end }}`

// config is the expected JSON schema for the Secret's "config" key.
type config struct {
	// Token is the Telegram Bot API token.
	Token string `json:"token"`
	// ChatID is the target chat ID (numeric ID or channel username like "@mychannel").
	ChatID string `json:"chat_id"`
	// ParseMode is the message parse mode (MarkdownV2 or HTML), defaults to HTML if not specified.
	ParseMode *string `json:"parse_mode,omitempty"`
}

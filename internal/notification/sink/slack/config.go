/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package slack

// DefaultTemplate is the built-in Go template for Slack notifications.
var DefaultTemplate = `{{ if eq .Event "Failure" -}}
:red_circle: *Hibernation Failed*
{{ else if eq .Event "Success" -}}
:white_check_mark: *Hibernation Succeeded*
{{ else if eq .Event "Start" -}}
:arrow_forward: *Execution Starting*
{{ else if eq .Event "Recovery" -}}
:recycle: *Recovery Triggered*
{{ else -}}
:information_source: *Phase Change*
{{ end -}}
*Plan:* {{ .Plan.Name }}
*Namespace:* {{ .Plan.Namespace }}
*Phase:* {{ .Phase }}
*Operation:* {{ .Operation | default "N/A" }}
{{ if .PreviousPhase -}}
*Previous Phase:* {{ .PreviousPhase }}
{{ end -}}
{{ if .ErrorMessage -}}
*Error:* {{ .ErrorMessage }}
{{ end -}}
*Timestamp:* {{ .Timestamp | date "2006-01-02 15:04:05 MST" }}
{{ if .Targets -}}
*Targets:*
{{ range .Targets -}}
• {{ .Name }} ({{ .Executor }}): {{ .State }}
{{ end -}}
{{ end }}`

// config is the expected JSON schema for the Secret's "config" key.
type config struct {
	// WebhookURL is the Slack Incoming Webhook URL.
	WebhookURL string `json:"webhook_url"`
}

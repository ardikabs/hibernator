/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package slack

// DefaultTemplate is the built-in Go template for Slack notifications.
// Headlines are operation-aware: they distinguish Hibernate from Wake-Up.
var DefaultTemplate = `{{ if eq .Event "Start" -}}
{{ if eq .Operation "shutdown" -}}
:arrow_forward: *Hibernation Starting* ({{ len .Targets }} targets)
{{ else -}}
:arrow_forward: *Wake-Up Starting* ({{ len .Targets }} targets)
{{ end -}}
{{ else if eq .Event "Success" -}}
{{ if eq .Operation "shutdown" -}}
:white_check_mark: *Hibernation Completed*
{{ else -}}
:white_check_mark: *Wake-Up Completed*
{{ end -}}
{{ else if eq .Event "Failure" -}}
{{ if eq .Operation "shutdown" -}}
:red_circle: *Hibernation Failed*
{{ else -}}
:red_circle: *Wake-Up Failed*
{{ end -}}
{{ else if eq .Event "Recovery" -}}
{{ if eq .Operation "shutdown" -}}
:recycle: *Hibernation Retrying* (attempt {{ .RetryCount }})
{{ else -}}
:recycle: *Wake-Up Retrying* (attempt {{ .RetryCount }})
{{ end -}}
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

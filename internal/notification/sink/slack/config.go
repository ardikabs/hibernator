/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package slack

// config is the expected JSON schema for the Secret's "config" key.
type config struct {
	// WebhookURL is the Slack Incoming Webhook URL.
	WebhookURL string `json:"webhook_url"`
}

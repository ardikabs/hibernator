/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package telegram

// config is the expected JSON schema for the Secret's "config" key.
type config struct {
	// Token is the Telegram Bot API token.
	Token string `json:"token"`
	// ChatID is the target chat ID (numeric ID or channel username like "@mychannel").
	ChatID string `json:"chat_id"`
	// ParseMode is the message parse mode (MarkdownV2 or HTML), defaults to HTML if not specified.
	ParseMode *string `json:"parse_mode,omitempty"`
}

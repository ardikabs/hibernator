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

	// RateLimit controls the rate limiting for this specific sink instance.
	// Used to prevent burst traffic from overwhelming Telegram's API limits.
	// If not specified, uses default rate of 5 req/sec with burst of 10.
	RateLimit *RateLimitConfig `json:"rate_limit,omitempty"`
}

// RateLimitConfig holds rate limiting settings for the sink.
type RateLimitConfig struct {
	// Rate is the sustained rate limit (e.g. 5.0 for 5 req/unit).
	// Default: 5.0
	Rate float64 `json:"rate,omitempty"`

	// Unit is the time unit for Rate: "second" or "minute".
	// Default: "second"
	Unit string `json:"unit,omitempty"`

	// Burst is the maximum number of requests allowed in a burst.
	// Default: 10
	Burst int `json:"burst,omitempty"`
}

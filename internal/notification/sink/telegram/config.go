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
	// RequestsPerSecond is the sustained rate limit.
	// Default: 5.0 (5 requests per second)
	RequestsPerSecond float64 `json:"requests_per_second,omitempty"`

	// Burst is the maximum number of requests allowed in a burst.
	// Default: 10
	Burst int `json:"burst,omitempty"`
}

func (c *config) useDefaults() {
	if c.RateLimit == nil {
		c.RateLimit = &RateLimitConfig{
			RequestsPerSecond: 5.0,
			Burst:             10,
		}
	}
}

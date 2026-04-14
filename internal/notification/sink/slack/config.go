/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package slack

import "strings"

const (
	// formatText sends only plain text content.
	formatText = "text"
	// formatJSON sends Slack JSON payloads with blocks.
	formatJSON = "json"

	// blockLayoutDefault is the balanced summary+detail layout.
	blockLayoutDefault = "default"
	// blockLayoutCompact is the short high-signal layout.
	blockLayoutCompact = "compact"
	// blockLayoutAuto uses progress layout for ExecutionProgress events and
	// falls back to default layout for all other events.
	blockLayoutAuto = "auto"

	// Scope keys for JSON preset footer metadata.
	scopeAccount     = "account"
	scopeCluster     = "cluster"
	scopeEnvironment = "environment"
	scopeRegion      = "region"
	scopeProject     = "project"
	scopeProvider    = "provider"
	scopeConnector   = "connector"

	defaultFormat      = formatText
	defaultBlockLayout = blockLayoutDefault
	defaultMaxTargets  = 8
)

// config is the expected JSON schema for the Secret's "config" key.
type config struct {
	// WebhookURL is the Slack Incoming Webhook URL.
	WebhookURL string `json:"webhook_url"`

	// Format controls Slack payload mode.
	// Supported values: `text` (message text only), and
	// `json` (Slack blocks payload, using preset layouts or custom templates).
	Format string `json:"format,omitempty"`

	// BlockLayout selects the preset JSON layout used when format=json and
	// no custom JSON template is provided (or parsing fails).
	// Supported values: `default`, `compact`, `auto`.
	BlockLayout string `json:"block_layout,omitempty"`

	// MaxTargets limits target lines in preset JSON layouts.
	// It defaults to 8, which is enough to show all targets in most cases while keeping the message concise.
	MaxTargets int `json:"max_targets,omitempty"`

	// AdditionalScopes appends additional scope fields to the scope context.
	// Account and Cluster are always included by default.
	// Supported: `environment` (alias: env), `region`, `project`, `provider`, `connector`,
	// `account`, `cluster`.
	AdditionalScopes []string `json:"additional_scopes,omitempty"`
}

func (c *config) useDefaults() {
	c.Format = strings.ToLower(strings.TrimSpace(c.Format))
	if c.Format == "" {
		c.Format = defaultFormat
	}

	c.BlockLayout = strings.ToLower(strings.TrimSpace(c.BlockLayout))
	if c.BlockLayout == "" {
		c.BlockLayout = defaultBlockLayout
	}

	if c.MaxTargets <= 0 {
		c.MaxTargets = defaultMaxTargets
	}

	c.AdditionalScopes = normalizeScopeList(c.AdditionalScopes)
}

func normalizeScopeList(scopes []string) []string {
	seen := make(map[string]struct{}, len(scopes))
	out := make([]string, 0, len(scopes))
	for _, raw := range scopes {
		s := normalizeScope(raw)
		if s == "" {
			continue
		}
		if _, exists := seen[s]; exists {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func normalizeScope(scope string) string {
	s := strings.ToLower(strings.TrimSpace(scope))
	switch s {
	case "env":
		return scopeEnvironment
	case "account_id", "accountid":
		return scopeAccount
	case "cluster_id", "clusterid":
		return scopeCluster
	default:
		return s
	}
}

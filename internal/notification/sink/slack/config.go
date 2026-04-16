/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package slack

import (
	"fmt"
	"strings"
	"time"
)

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

	// timeDisplaySlackDynamic renders time via Slack date token in viewer-local timezone.
	timeDisplaySlackDynamic = "slack_dynamic"
	// timeDisplayFixed renders time in configured timezone and layout.
	timeDisplayFixed = "fixed"
	// timeDisplayUTC renders time in UTC.
	timeDisplayUTC = "utc"

	// deliveryModeChannel posts each notification as a channel message.
	deliveryModeChannel = "channel"
	// deliveryModeThread posts notifications as replies in a cycle thread.
	deliveryModeThread = "thread"

	// Scope keys for JSON preset footer metadata.
	scopeAccount     = "account"
	scopeCluster     = "cluster"
	scopeEnvironment = "environment"
	scopeRegion      = "region"
	scopeProject     = "project"
	scopeProvider    = "provider"
	scopeConnector   = "connector"

	defaultFormat       = formatText
	defaultBlockLayout  = blockLayoutDefault
	defaultMaxTargets   = 8
	defaultTimeDisplay  = timeDisplaySlackDynamic
	defaultTimeLayout   = "Mon, 02 Jan 2006 15:04:05 MST"
	defaultDeliveryMode = deliveryModeChannel
)

// config is the expected JSON schema for the Secret's "config" key.
type config struct {
	// WebhookURL is the Slack Incoming Webhook URL.
	// Mutually exclusive with `bot_token` and `channel_id`, required when `delivery_mode=channel`.
	WebhookURL string `json:"webhook_url"`

	// BotToken is the Slack Bot token used for Web API delivery mode.
	// Mutually exclusive with `webhook_url`, required when `delivery_mode=thread`.
	BotToken string `json:"bot_token"`

	// ChannelID is the Slack channel ID used for Web API delivery mode.
	// Mutually exclusive with `webhook_url`, required when `delivery_mode=thread`.
	ChannelID string `json:"channel_id"`

	// Format controls Slack payload mode.
	// Supported values: `text` (message text only), and
	// `json` (Slack blocks payload, using preset layouts or custom templates).
	Format string `json:"format,omitempty"`

	// BlockLayout selects the preset JSON layout used when format=json and
	// no custom JSON template is provided (or parsing fails).
	// Supported values: `default`, `compact`, `auto`.
	// For `ExecutionProgress`, `default` and `compact` suppress non-terminal updates (`Pending`, `Running`)
	// and only send terminal updates (`Completed`, `Failed`, `Aborted`).
	// Use `auto` for full progress streaming
	BlockLayout string `json:"block_layout,omitempty"`

	// MaxTargets limits target lines in preset JSON layouts.
	// It defaults to 8, which is enough to show all targets in most cases while keeping the message concise.
	MaxTargets int `json:"max_targets,omitempty"`

	// AdditionalScopes appends additional scope fields to the scope context.
	// Account and Cluster are always included by default.
	// Supported: `environment` (alias: env), `region`, `project`, `provider`, `connector`,
	// `account`, `cluster`.
	AdditionalScopes []string `json:"additional_scopes,omitempty"`

	// TimeDisplay controls how preset JSON layouts render context time.
	// Supported values:
	// - `slack_dynamic` (default): Slack date token rendered in each viewer's locale/timezone.
	// - `fixed`: rendered with Timezone + TimeLayout.
	// - `utc`: rendered in UTC with TimeLayout.
	TimeDisplay string `json:"time_display,omitempty"`

	// Timezone is an IANA timezone name (for example, `Asia/Jakarta`) used only
	// when TimeDisplay is `fixed`. Defaults to `UTC` in fixed mode.
	Timezone string `json:"timezone,omitempty"`

	// TimeLayout is Go time layout used by fixed/utc displays.
	// Defaults to `Mon, 02 Jan 2006 15:04:05 MST`.
	TimeLayout string `json:"time_layout,omitempty"`

	// DeliveryMode controls message grouping behavior.
	// Supported values:
	// - `channel` (default): each event posts as standalone channel message.
	// - `thread`: root message is treated as a live status card and updated on every
	//   delivered event for the same plan/cycle, while event entries are posted as
	//   thread replies (including Start).
	//   Root status is monotonic per sink+plan+cycle+operation: once terminal (`Success`/`Failure`),
	//   late non-terminal events (`ExecutionProgress`, `Recovery`, `PhaseChange`, `Start`)
	//   do not downgrade root status back to in-progress, though they are still posted as thread replies.
	//   In `thread` mode, `templateRef`/custom templates are intentionally ignored:
	//   the sink always uses built-in, opinionated thread layouts so context and
	//   status progression remain consistent across root updates and replies.
	//   Recommendation: include `ExecutionProgress` in onEvents so root status moves
	//   continuously across execution; otherwise root updates only on subscribed events.
	DeliveryMode string `json:"delivery_mode,omitempty"`
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

	c.TimeDisplay = strings.ToLower(strings.TrimSpace(c.TimeDisplay))
	if c.TimeDisplay == "" {
		c.TimeDisplay = defaultTimeDisplay
	}

	c.Timezone = strings.TrimSpace(c.Timezone)
	if c.TimeDisplay == timeDisplayFixed && c.Timezone == "" {
		c.Timezone = "UTC"
	}

	c.TimeLayout = strings.TrimSpace(c.TimeLayout)
	if c.TimeLayout == "" {
		c.TimeLayout = defaultTimeLayout
	}

	c.DeliveryMode = strings.ToLower(strings.TrimSpace(c.DeliveryMode))
	if c.DeliveryMode == "" {
		c.DeliveryMode = defaultDeliveryMode
	}

	c.BotToken = strings.TrimSpace(c.BotToken)
	c.ChannelID = strings.TrimSpace(c.ChannelID)

	c.AdditionalScopes = normalizeScopeList(c.AdditionalScopes)
}

func (c config) validate() error {
	prefixErr := "slack sink config:"

	switch c.Format {
	case formatText, formatJSON:
		// ok
	default:
		return fmt.Errorf("%s format must be %q or %q", prefixErr, formatText, formatJSON)
	}

	switch c.DeliveryMode {
	case deliveryModeChannel, deliveryModeThread:
		// ok
	default:
		return fmt.Errorf("%s delivery_mode must be one of %q, %q", prefixErr, deliveryModeChannel, deliveryModeThread)
	}

	if c.DeliveryMode == deliveryModeChannel {
		if c.WebhookURL == "" {
			return fmt.Errorf("%s webhook_url is required when delivery_mode=%q", prefixErr, deliveryModeChannel)
		}
	}

	if c.DeliveryMode == deliveryModeThread {
		if c.BotToken == "" {
			return fmt.Errorf("%s bot_token is required when delivery_mode=%q", prefixErr, deliveryModeThread)
		}
		if c.ChannelID == "" {
			return fmt.Errorf("%s channel_id is required when delivery_mode=%q", prefixErr, deliveryModeThread)
		}
	}

	if c.Format != formatJSON {
		return nil
	}

	switch c.BlockLayout {
	case blockLayoutDefault, blockLayoutCompact, blockLayoutAuto:
		// ok
	default:
		return fmt.Errorf("%s block_layout must be one of %q, %q, %q", prefixErr, blockLayoutDefault, blockLayoutCompact, blockLayoutAuto)
	}

	switch c.TimeDisplay {
	case timeDisplaySlackDynamic, timeDisplayFixed, timeDisplayUTC:
		// ok
	default:
		return fmt.Errorf("%s time_display must be one of %q, %q, %q", prefixErr, timeDisplaySlackDynamic, timeDisplayFixed, timeDisplayUTC)
	}

	if c.TimeDisplay == timeDisplayFixed {
		if _, err := time.LoadLocation(c.Timezone); err != nil {
			return fmt.Errorf("%s invalid timezone %q: %w", prefixErr, c.Timezone, err)
		}
	}

	for _, scope := range c.AdditionalScopes {
		switch scope {
		case scopeAccount, scopeCluster, scopeEnvironment, scopeRegion, scopeProject, scopeProvider, scopeConnector:
			// ok
		default:
			return fmt.Errorf("%s unsupported additional scope %q", prefixErr, scope)
		}
	}

	return nil
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

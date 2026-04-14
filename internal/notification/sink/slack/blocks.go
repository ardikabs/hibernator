/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package slack

import (
	"fmt"
	"sort"
	"strings"
	"time"

	slackapi "github.com/slack-go/slack"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/notification/sink"
)

func presetJSONMessage(payload sink.Payload, layout string, maxTargets int, additionalScopes []string) *slackapi.WebhookMessage {
	factory := newLayoutFactory()
	composer := newLayoutComposer(payload, maxTargets, additionalScopes)
	return &slackapi.WebhookMessage{
		Text:   fallbackText(payload),
		Blocks: &slackapi.Blocks{BlockSet: factory.build(layout, composer)},
	}
}

type layoutFactory struct {
	builders map[string]func(*layoutComposer) []slackapi.Block
}

func newLayoutFactory() *layoutFactory {
	return &layoutFactory{
		builders: map[string]func(*layoutComposer) []slackapi.Block{
			blockLayoutDefault:  (*layoutComposer).buildDefault,
			blockLayoutCompact:  (*layoutComposer).buildCompact,
			blockLayoutProgress: (*layoutComposer).buildProgress,
		},
	}
}

func (f *layoutFactory) build(layout string, composer *layoutComposer) []slackapi.Block {
	resolved := layout
	if resolved == blockLayoutProgress && composer.payload.Event != "ExecutionProgress" {
		resolved = blockLayoutDefault
	}

	builder, ok := f.builders[resolved]
	if !ok {
		builder = f.builders[blockLayoutDefault]
	}
	return builder(composer)
}

type layoutComposer struct {
	payload          sink.Payload
	maxTargets       int
	additionalScopes []string
}

func newLayoutComposer(payload sink.Payload, maxTargets int, additionalScopes []string) *layoutComposer {
	return &layoutComposer{
		payload:          payload,
		maxTargets:       maxTargets,
		additionalScopes: additionalScopes,
	}
}

func (c *layoutComposer) buildDefault() []slackapi.Block {
	scope := c.scopeLine()
	hasScope := scope != ""
	errorMessage := c.payload.ErrorMessage
	hasError := errorMessage != ""

	return newBlockSetBuilder(8).
		Add(
			slackapi.NewHeaderBlock(plainText().WithEmoji().WithText(c.headerTitle()).Build()),
			slackapi.NewDividerBlock(),
			slackapi.NewSectionBlock(mdText().WithText(c.summaryLine()).Build(), nil, nil),
		).
		AddWhen(hasError, slackapi.NewSectionBlock(mdText().WithText(fmt.Sprintf("*Error:* %s", errorMessage)).Build(), nil, nil)).
		AddWhenTextBlocks(targetLines(c.payload.Targets, c.maxTargets), func(targets string) []slackapi.Block {
			return []slackapi.Block{
				slackapi.NewDividerBlock(),
				slackapi.NewSectionBlock(mdText().WithText(targets).Build(), nil, nil),
			}
		}).
		Add(c.metaContextBlock()).
		AddWhen(hasScope, c.scopeContextBlock(scope)).
		Build()
}

func (c *layoutComposer) buildCompact() []slackapi.Block {
	targets := ""
	if c.payload.Event != "ExecutionProgress" {
		targets = targetLines(c.payload.Targets, c.maxTargets)
	}
	scope := c.scopeLine()
	hasScope := scope != ""
	errorMessage := c.payload.ErrorMessage
	hasError := errorMessage != ""

	return newBlockSetBuilder(6).
		Add(slackapi.NewSectionBlock(mdText().WithText(c.compactSummary()).Build(), nil, nil)).
		AddWhen(hasError, slackapi.NewSectionBlock(mdText().WithText(fmt.Sprintf("*Error:* %s", errorMessage)).Build(), nil, nil)).
		AddWhenText(targets, func(v string) slackapi.Block {
			return slackapi.NewSectionBlock(mdText().WithText(v).Build(), nil, nil)
		}).
		Add(c.metaContextBlock()).
		AddWhen(hasScope, c.scopeContextBlock(scope)).
		Build()
}

func (c *layoutComposer) buildProgress() []slackapi.Block {
	target := c.payload.TargetExecution
	if target == nil {
		return []slackapi.Block{}
	}

	sectionText := (*slackapi.TextBlockObject)(nil)
	if msg := strings.TrimSpace(target.Message); msg != "" {
		sectionText = mdText().WithText(msg).Build()
	}

	section := slackapi.NewSectionBlock(
		sectionText,
		[]*slackapi.TextBlockObject{
			mdText().WithText(fmt.Sprintf("*Target:* %s", target.Name)).Build(),
			mdText().WithText(fmt.Sprintf("*Type:* %s", target.Executor)).Build(),
			mdText().WithText(fmt.Sprintf("*Operation:* %s", c.payload.Operation)).Build(),
			mdText().WithText(fmt.Sprintf("*State:* %s", target.State)).Build(),
		},
		nil,
	)

	title := ":loading: Execution Progress"
	if strings.EqualFold(target.State, "Completed") {
		title = ":white_check_mark: Execution Completed"
	}
	scope := c.scopeLine()
	hasScope := scope != ""
	errorMessage := c.payload.ErrorMessage
	hasError := errorMessage != ""

	return newBlockSetBuilder(7).
		Add(slackapi.NewHeaderBlock(plainText().WithEmoji().WithText(title).Build())).
		AddWhen(hasScope, c.scopeContextBlock(scope)).
		Add(slackapi.NewDividerBlock(), section).
		AddWhen(hasError, slackapi.NewSectionBlock(mdText().WithText(fmt.Sprintf("*Error:* %s", errorMessage)).Build(), nil, nil)).
		Add(c.metaContextBlock()).
		Build()
}

func (c *layoutComposer) headerTitle() string {
	switch hibernatorv1alpha1.NotificationEvent(c.payload.Event) {
	case hibernatorv1alpha1.EventStart:
		if c.payload.Operation == "shutdown" {
			return ":arrow_forward: Hibernation Starting"
		}
		return ":arrow_forward: Wake-Up Starting"
	case hibernatorv1alpha1.EventSuccess:
		if c.payload.Operation == "shutdown" {
			return ":white_check_mark: Hibernation Completed"
		}
		return ":white_check_mark: Wake-Up Completed"
	case hibernatorv1alpha1.EventFailure:
		if c.payload.Operation == "shutdown" {
			return ":alert: Hibernation Failed"
		}
		return ":alert: Wake-Up Failed"
	case hibernatorv1alpha1.EventRecovery:
		if c.payload.Operation == "shutdown" {
			return ":repeat: Hibernation Retrying"
		}
		return ":repeat: Wake-Up Retrying"
	default:
		return ":repeat: Phase Change"
	}
}

func (c *layoutComposer) summaryLine() string {
	plan := fmt.Sprintf("%s/%s", c.payload.Plan.Namespace, c.payload.Plan.Name)
	parts := []string{
		fmt.Sprintf("*Plan:* `%s`", plan),
		fmt.Sprintf("*Phase:* `%s`", c.payload.Phase),
	}
	if c.payload.Operation != "" {
		parts = append(parts, fmt.Sprintf("*Operation:* `%s`", c.payload.Operation))
	}
	if c.payload.RetryCount > 0 {
		parts = append(parts, fmt.Sprintf("*Retry:* `%d`", c.payload.RetryCount))
	}
	return strings.Join(parts, "\n")
}

func (c *layoutComposer) compactSummary() string {
	return fmt.Sprintf("*%s* | `%s/%s` | `%s`", c.payload.Event, c.payload.Plan.Namespace, c.payload.Plan.Name, c.payload.Phase)
}

func (c *layoutComposer) contextLine() string {
	ts := c.payload.Timestamp
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	parts := []string{fmt.Sprintf("Event: *%s*", c.payload.Event)}
	if c.payload.CycleID != "" {
		parts = append(parts, fmt.Sprintf("Cycle: `%s`", c.payload.CycleID))
	}
	parts = append(parts, fmt.Sprintf("Time: %s", ts.UTC().Format(time.RFC3339)))
	return strings.Join(parts, " • ")
}

func (c *layoutComposer) metaContextBlock() *slackapi.ContextBlock {
	return slackapi.NewContextBlock("notification-meta", mdText().WithText(c.contextLine()).Build())
}

func (c *layoutComposer) scopeContextBlock(scope string) *slackapi.ContextBlock {
	return slackapi.NewContextBlock("notification-scope", mdText().WithText(scope).Build())
}

func (c *layoutComposer) scopeLine() string {
	connector := c.discoverScopeConnector()
	orderedScopes := make([]string, 0, 2+len(c.additionalScopes))
	orderedScopes = append(orderedScopes, scopeAccount, scopeCluster)
	orderedScopes = append(orderedScopes, c.additionalScopes...)

	seen := make(map[string]struct{}, len(orderedScopes))
	parts := make([]string, 0, len(orderedScopes))
	for _, rawScope := range orderedScopes {
		scope := normalizeScope(rawScope)
		if scope == "" {
			continue
		}
		if _, exists := seen[scope]; exists {
			continue
		}
		seen[scope] = struct{}{}

		part, ok := c.scopePart(scope, connector)
		if ok {
			parts = append(parts, part)
		}
	}

	return strings.Join(parts, " • ")
}

func (c *layoutComposer) scopePart(scope string, connector sink.ConnectorInfo) (string, bool) {
	switch scope {
	case scopeAccount:
		if connector.AccountID == "" {
			return "", false
		}
		return fmt.Sprintf("*Account:* `%s`", connector.AccountID), true
	case scopeCluster:
		if connector.ClusterName == "" {
			return "", false
		}
		return fmt.Sprintf("*Cluster:* `%s`", connector.ClusterName), true
	case scopeEnvironment:
		env := c.discoverEnvironment()
		if env == "" {
			return "", false
		}
		return fmt.Sprintf("*Environment:* `%s`", env), true
	case scopeRegion:
		if connector.Region == "" {
			return "", false
		}
		return fmt.Sprintf("*Region:* `%s`", connector.Region), true
	case scopeProject:
		if connector.ProjectID == "" {
			return "", false
		}
		return fmt.Sprintf("*Project:* `%s`", connector.ProjectID), true
	case scopeProvider:
		if connector.Provider == "" {
			return "", false
		}
		return fmt.Sprintf("*Provider:* `%s`", connector.Provider), true
	case scopeConnector:
		if connector.Name == "" {
			return "", false
		}
		return fmt.Sprintf("*Connector:* `%s`", connector.Name), true
	default:
		return "", false
	}
}

func (c *layoutComposer) discoverScopeConnector() sink.ConnectorInfo {
	if c.payload.TargetExecution != nil && hasConnectorScopeData(c.payload.TargetExecution.Connector) {
		return c.payload.TargetExecution.Connector
	}

	for _, target := range c.payload.Targets {
		if strings.EqualFold(target.State, "Failed") && hasConnectorScopeData(target.Connector) {
			return target.Connector
		}
	}

	for _, target := range c.payload.Targets {
		if hasConnectorScopeData(target.Connector) {
			return target.Connector
		}
	}

	return sink.ConnectorInfo{}
}

func (c *layoutComposer) discoverEnvironment() string {
	for _, key := range []string{"env", "environment"} {
		if v := c.payload.Plan.Labels[key]; strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	for _, key := range []string{"env", "environment"} {
		if v := c.payload.Plan.Annotations[key]; strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func hasConnectorScopeData(connector sink.ConnectorInfo) bool {
	return connector.AccountID != "" ||
		connector.ClusterName != "" ||
		connector.Region != "" ||
		connector.ProjectID != "" ||
		connector.Provider != "" ||
		connector.Name != ""
}
func fallbackText(payload sink.Payload) string {
	parts := []string{
		fmt.Sprintf("[%s] %s/%s", payload.Event, payload.Plan.Namespace, payload.Plan.Name),
		fmt.Sprintf("phase=%s", payload.Phase),
	}
	if payload.Operation != "" {
		parts = append(parts, fmt.Sprintf("operation=%s", payload.Operation))
	}
	if payload.ErrorMessage != "" {
		parts = append(parts, fmt.Sprintf("error=%s", payload.ErrorMessage))
	}
	return strings.Join(parts, " | ")
}

func targetLines(targets []sink.TargetInfo, maxTargets int) string {
	if len(targets) == 0 {
		return ""
	}
	if maxTargets <= 0 {
		maxTargets = defaultMaxTargets
	}

	ordered := make([]sink.TargetInfo, len(targets))
	copy(ordered, targets)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].State == ordered[j].State {
			return ordered[i].Name < ordered[j].Name
		}
		if ordered[i].State == "Failed" {
			return true
		}
		if ordered[j].State == "Failed" {
			return false
		}
		return ordered[i].Name < ordered[j].Name
	})

	count := len(ordered)
	if count > maxTargets {
		count = maxTargets
	}

	lines := make([]string, 0, count+1)
	for i := 0; i < count; i++ {
		t := ordered[i]
		line := fmt.Sprintf("- %s (%s) -> `%s`", t.Name, t.Executor, t.State)
		if t.Message != "" {
			line += " — " + t.Message
		}
		lines = append(lines, line)
	}

	if len(ordered) > count {
		lines = append(lines, fmt.Sprintf("... and %d more target(s)", len(ordered)-count))
	}

	return strings.Join(lines, "\n")
}

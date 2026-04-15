/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package slack

import (
	"context"
	"fmt"
	"strings"

	slackapi "github.com/slack-go/slack"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/notification/sink"
)

type States = map[string]string

type deliveryHandler interface {
	deliver(ctx context.Context, payload sink.Payload) (States, error)
}

func newDeliveryHandler(s *Sink, cfg config, rt deliveryRuntime) (deliveryHandler, error) {
	switch cfg.DeliveryMode {
	case deliveryModeChannel:
		return &channelDelivery{s: s, cfg: cfg, rt: rt}, nil
	case deliveryModeThread:
		return &threadDelivery{s: s, cfg: cfg, rt: rt}, nil
	default:
		return nil, fmt.Errorf("slack sink config: delivery_mode must be one of %q, %q", deliveryModeChannel, deliveryModeThread)
	}
}

type channelDelivery struct {
	s   *Sink
	cfg config
	rt  deliveryRuntime
}

func (cd *channelDelivery) deliver(ctx context.Context, payload sink.Payload) (States, error) {
	if shouldSuppressExecutionProgress(payload, cd.cfg) {
		return nil, nil
	}

	msg := cd.s.buildMessage(ctx, payload, cd.cfg, cd.rt.customTemplate)
	if err := slackapi.PostWebhookCustomHTTPContext(ctx, cd.cfg.WebhookURL, cd.s.client, msg); err != nil {
		return States{}, fmt.Errorf("send slack notification: %w", err)
	}
	return nil, nil
}

type threadDelivery struct {
	s   *Sink
	cfg config
	rt  deliveryRuntime
}

func (td *threadDelivery) deliver(ctx context.Context, payload sink.Payload) (States, error) {
	if shouldSuppressExecutionProgress(payload, td.cfg) {
		return nil, nil
	}

	msg := td.s.buildMessage(ctx, payload, td.cfg, td.rt.customTemplate)
	applyThreading(payload, td.rt.sinkState, msg)

	ts, err := td.sendViaChatAPI(ctx, td.cfg, msg)
	if err != nil {
		return nil, err
	}

	event := hibernatorv1alpha1.NotificationEvent(payload.Event)
	reaction := reactionForEvent(event)
	bumpTS := msg.ThreadTimestamp
	if event == hibernatorv1alpha1.EventStart {
		bumpTS = ts
	}

	if shouldBumpReaction(td.rt.sinkState, event, bumpTS, reaction) {
		prevReaction := strings.TrimSpace(td.rt.sinkState["slack.thread.last_reaction"])
		if event == hibernatorv1alpha1.EventStart {
			prevReaction = ""
		}
		if err := td.overrideRootThreadReaction(ctx, td.cfg, bumpTS, prevReaction, reaction); err != nil {
			td.rt.log.Error(err, "failed to override root thread reaction", "prev_reaction", prevReaction, "reaction", reaction, "root_ts", bumpTS, "channel_id", td.cfg.ChannelID)
		}
	}

	metadata := map[string]string{
		"slack.thread.ref": threadReference(payload),
	}
	if event == hibernatorv1alpha1.EventStart {
		metadata["slack.thread.state"] = "root_sent"
		metadata["slack.thread.root_ts"] = ts
	} else {
		metadata["slack.thread.state"] = "reply_sent"
	}
	if reaction != "" {
		metadata["slack.thread.last_reaction"] = reaction
	}

	return metadata, nil
}

func (td *threadDelivery) newSlackAPI(cfg config) *slackapi.Client {
	apiOpts := []slackapi.Option{slackapi.OptionHTTPClient(td.s.client)}
	if td.s.serverURL != "" {
		apiOpts = append(apiOpts, slackapi.OptionAPIURL(td.s.serverURL))
	}
	return slackapi.New(cfg.BotToken, apiOpts...)
}

func (td *threadDelivery) sendViaChatAPI(ctx context.Context, cfg config, msg *slackapi.WebhookMessage) (string, error) {
	api := td.newSlackAPI(cfg)
	opts := []slackapi.MsgOption{slackapi.MsgOptionText(msg.Text, false)}
	if msg.ThreadTimestamp != "" {
		opts = append(opts, slackapi.MsgOptionPostMessageParameters(slackapi.PostMessageParameters{ThreadTimestamp: msg.ThreadTimestamp}))
	}
	if msg.Blocks != nil {
		opts = append(opts, slackapi.MsgOptionBlocks(msg.Blocks.BlockSet...))
	}

	channel, ts, err := api.PostMessageContext(ctx, cfg.ChannelID, opts...)
	if err != nil {
		return "", fmt.Errorf("send slack notification: %w", err)
	}
	if channel == "" || ts == "" {
		return "", fmt.Errorf("send slack notification: missing channel/ts in Slack API response")
	}
	return ts, nil
}

func (td *threadDelivery) bumpRootThreadEmoji(ctx context.Context, cfg config, rootTS, reaction string) error {
	item := slackapi.ItemRef{Channel: cfg.ChannelID, Timestamp: rootTS}
	err := td.newSlackAPI(cfg).AddReactionContext(ctx, reaction, item)
	if err != nil && strings.Contains(err.Error(), "already_reacted") {
		return nil
	}
	return err
}

func (td *threadDelivery) removeRootThreadEmoji(ctx context.Context, cfg config, rootTS, reaction string) error {
	item := slackapi.ItemRef{Channel: cfg.ChannelID, Timestamp: rootTS}
	err := td.newSlackAPI(cfg).RemoveReactionContext(ctx, reaction, item)
	if err != nil && strings.Contains(err.Error(), "no_reaction") {
		return nil
	}
	return err
}

func (td *threadDelivery) overrideRootThreadReaction(ctx context.Context, cfg config, rootTS, prevReaction, nextReaction string) error {
	if nextReaction == "" || rootTS == "" {
		return nil
	}
	if prevReaction == nextReaction {
		return nil
	}
	if prevReaction != "" {
		if err := td.removeRootThreadEmoji(ctx, cfg, rootTS, prevReaction); err != nil {
			return err
		}
	}
	if err := td.bumpRootThreadEmoji(ctx, cfg, rootTS, nextReaction); err != nil {
		return err
	}
	return nil
}

func shouldSuppressExecutionProgress(payload sink.Payload, cfg config) bool {
	if cfg.Format != formatJSON {
		return false
	}
	if payload.Event != "ExecutionProgress" {
		return false
	}
	if cfg.BlockLayout == blockLayoutAuto {
		return false
	}
	if cfg.BlockLayout != blockLayoutDefault && cfg.BlockLayout != blockLayoutCompact {
		return false
	}
	if payload.TargetExecution == nil {
		return false
	}

	switch hibernatorv1alpha1.ExecutionState(payload.TargetExecution.State) {
	case hibernatorv1alpha1.StateCompleted,
		hibernatorv1alpha1.StateFailed,
		hibernatorv1alpha1.StateAborted:
		return false
	default:
		return true
	}
}

func shouldBumpReaction(sinkState map[string]string, event hibernatorv1alpha1.NotificationEvent, bumpTS, reaction string) bool {
	if bumpTS == "" || reaction == "" {
		return false
	}
	if event == hibernatorv1alpha1.EventStart {
		return true
	}
	return strings.TrimSpace(sinkState["slack.thread.last_reaction"]) != reaction
}

func applyThreading(payload sink.Payload, sinkState map[string]string, msg *slackapi.WebhookMessage) {
	event := hibernatorv1alpha1.NotificationEvent(payload.Event)
	if event == hibernatorv1alpha1.EventStart {
		return
	}
	if sinkState == nil {
		return
	}
	if prevRef := strings.TrimSpace(sinkState["slack.thread.ref"]); prevRef != "" && prevRef != threadReference(payload) {
		return
	}

	if rootTS := strings.TrimSpace(sinkState["slack.thread.root_ts"]); rootTS != "" {
		msg.ThreadTimestamp = rootTS
	}
}

func reactionForEvent(event hibernatorv1alpha1.NotificationEvent) string {
	switch event {
	case hibernatorv1alpha1.EventFailure:
		return "x"
	case hibernatorv1alpha1.EventSuccess:
		return "white_check_mark"
	case hibernatorv1alpha1.EventStart,
		hibernatorv1alpha1.EventExecutionProgress,
		hibernatorv1alpha1.EventRecovery:
		return "loading"
	default:
		return ""
	}
}

func threadReference(payload sink.Payload) string {
	ref := payload.Plan.String()
	if payload.CycleID == "" {
		return ref
	}
	return fmt.Sprintf("%s/%s/%s", ref, payload.CycleID, payload.Operation)
}

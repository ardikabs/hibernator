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
		apiOpts := []slackapi.Option{slackapi.OptionHTTPClient(s.client)}
		if s.serverURL != "" {
			apiOpts = append(apiOpts, slackapi.OptionAPIURL(s.serverURL))
		}
		return &threadDelivery{
			s:   s,
			api: slackapi.New(cfg.BotToken, apiOpts...),
			cfg: cfg,
			rt:  rt,
		}, nil
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
	log := cd.rt.log.WithValues(
		"format", cd.cfg.Format,
		"block_layout", cd.cfg.BlockLayout,
	)

	if shouldSuppressExecutionProgress(payload, cd.cfg) {
		log.V(1).Info("suppressed Slack channel delivery for non-terminal execution progress")
		return nil, nil
	}

	log.V(1).Info("sending Slack channel message")
	msg := cd.s.buildMessage(ctx, payload, cd.cfg, cd.rt.customTemplate)
	if err := slackapi.PostWebhookCustomHTTPContext(ctx, cd.cfg.WebhookURL, cd.s.client, msg); err != nil {
		return States{}, fmt.Errorf("send slack notification: %w", err)
	}
	log.V(1).Info("Slack channel message sent")
	return nil, nil
}

type threadDelivery struct {
	s   *Sink
	api *slackapi.Client
	cfg config
	rt  deliveryRuntime
}

type threadDeliveryFlow struct {
	event                hibernatorv1alpha1.NotificationEvent
	rootTS               string
	rootCreated          bool
	prevReaction         string
	preserveTerminalRoot bool
	nextReaction         string
	effectiveReaction    string
}

func (td *threadDelivery) deliver(ctx context.Context, payload sink.Payload) (States, error) {
	log := td.rt.log.WithValues(
		"format", td.cfg.Format,
		"block_layout", td.cfg.BlockLayout,
	)

	if shouldSuppressExecutionProgress(payload, td.cfg) {
		log.V(1).Info("suppressed Slack thread delivery for non-terminal execution progress")
		return nil, nil
	}

	flow := td.newThreadDeliveryFlow(payload)
	log.V(1).Info(
		"starting Slack thread delivery",
		"has_existing_root", flow.rootTS != "",
	)

	if err := td.syncThreadRoot(ctx, payload, &flow); err != nil {
		return nil, err
	}
	if err := td.sendThreadReply(ctx, payload, flow.rootTS); err != nil {
		return nil, err
	}
	td.syncThreadRootReaction(ctx, &flow)

	states := td.buildThreadStates(payload, flow)
	log.V(1).Info("completed Slack thread delivery",
		"root_ts", flow.rootTS,
		"root_created", flow.rootCreated)

	return states, nil
}

func (td *threadDelivery) newThreadDeliveryFlow(payload sink.Payload) threadDeliveryFlow {
	event := hibernatorv1alpha1.NotificationEvent(payload.Event)
	rootTS := resolveRootThreadTimestamp(payload, td.rt.sinkState)
	prevReaction := strings.TrimSpace(td.rt.sinkState["slack.thread.last_reaction"])

	flow := threadDeliveryFlow{
		event:             event,
		rootTS:            rootTS,
		prevReaction:      prevReaction,
		nextReaction:      reactionForEvent(event),
		effectiveReaction: prevReaction,
	}
	if rootTS != "" {
		flow.preserveTerminalRoot = shouldPreserveTerminalRoot(prevReaction, event)
	}

	return flow
}

func (td *threadDelivery) syncThreadRoot(ctx context.Context, payload sink.Payload, flow *threadDeliveryFlow) error {
	log := td.rt.log.WithValues("channel_id", td.cfg.ChannelID)

	if flow.rootTS == "" {
		log.Info("creating Slack thread root message")
		rootMsg := td.s.buildRootMessage(ctx, payload, td.cfg, td.rt.customTemplate)
		createdTS, err := td.sendViaChatAPI(ctx, td.cfg, rootMsg)
		if err != nil {
			return err
		}
		flow.rootTS = createdTS
		flow.rootCreated = true
		log.Info("created Slack thread root message",
			"root_ts", flow.rootTS)
		return nil
	}

	log = log.WithValues("root_ts", flow.rootTS)

	if flow.preserveTerminalRoot {
		log.V(1).Info("skipping Slack thread root update to preserve terminal state",
			"prev_reaction", flow.prevReaction)
		return nil
	}

	log.V(1).Info("updating Slack thread root message")
	rootMsg := td.s.buildRootMessage(ctx, payload, td.cfg, td.rt.customTemplate)
	if err := td.updateViaChatAPI(ctx, td.cfg, flow.rootTS, rootMsg); err != nil {
		log.Error(err, "failed to update root thread message")
		return nil
	}

	log.V(1).Info("updated Slack thread root message")
	return nil
}

func (td *threadDelivery) sendThreadReply(ctx context.Context, payload sink.Payload, rootTS string) error {
	replyMsg := td.s.buildMessage(ctx, payload, td.cfg, td.rt.customTemplate)
	if rootTS != "" {
		replyMsg.ThreadTimestamp = rootTS
	}

	log := td.rt.log.WithValues("root_ts", rootTS)

	log.V(1).Info("sending Slack thread reply")
	_, err := td.sendViaChatAPI(ctx, td.cfg, replyMsg)
	if err != nil {
		return err
	}
	log.V(1).Info("sent Slack thread reply")
	return nil
}

func (td *threadDelivery) syncThreadRootReaction(ctx context.Context, flow *threadDeliveryFlow) {
	log := td.rt.log.WithValues(
		"root_ts", flow.rootTS,
		"channel_id", td.cfg.ChannelID,
	)

	if flow.preserveTerminalRoot {
		log.V(1).Info("skipping Slack root reaction downgrade to preserve terminal state",
			"prev_reaction", flow.prevReaction,
			"next_reaction", flow.nextReaction)
		return
	}

	shouldBump := shouldBumpReaction(flow.prevReaction, flow.event, flow.rootTS, flow.nextReaction)
	if flow.rootCreated && flow.rootTS != "" && flow.nextReaction != "" {
		shouldBump = true
	}
	log.V(1).Info("evaluated Slack root reaction update",
		"reaction", flow.nextReaction,
		"should_bump", shouldBump)

	if !shouldBump {
		return
	}

	prevReactionForBump := flow.prevReaction
	if flow.event == hibernatorv1alpha1.EventStart || flow.rootCreated {
		prevReactionForBump = ""
	}

	log.V(1).Info("updating Slack root reaction",
		"prev_reaction", prevReactionForBump,
		"next_reaction", flow.nextReaction)
	if err := td.overrideRootThreadReaction(ctx, td.cfg, flow.rootTS, prevReactionForBump, flow.nextReaction); err != nil {
		log.Error(err, "failed to override root thread reaction",
			"prev_reaction", prevReactionForBump,
			"reaction", flow.nextReaction,
			"root_ts", flow.rootTS,
			"channel_id", td.cfg.ChannelID)
		return
	}

	log.V(1).Info("updated Slack root reaction", "reaction", flow.nextReaction)
	flow.effectiveReaction = flow.nextReaction
}

func (td *threadDelivery) buildThreadStates(payload sink.Payload, flow threadDeliveryFlow) States {
	states := map[string]string{
		"slack.thread.ref":     threadReference(payload),
		"slack.thread.root_ts": flow.rootTS,
	}
	if flow.rootCreated {
		states["slack.thread.state"] = "root_sent"
	} else {
		states["slack.thread.state"] = "reply_sent"
	}
	if flow.effectiveReaction != "" {
		states["slack.thread.last_reaction"] = flow.effectiveReaction
	}
	return states
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

func (td *threadDelivery) sendViaChatAPI(ctx context.Context, cfg config, msg *slackapi.WebhookMessage) (string, error) {
	ctx = td.s.withMethodRateLimit(ctx, cfg, "chat.postMessage")
	opts := []slackapi.MsgOption{slackapi.MsgOptionText(msg.Text, false)}
	if msg.ThreadTimestamp != "" {
		opts = append(opts, slackapi.MsgOptionPostMessageParameters(slackapi.PostMessageParameters{ThreadTimestamp: msg.ThreadTimestamp}))
	}
	if msg.Blocks != nil {
		opts = append(opts, slackapi.MsgOptionBlocks(msg.Blocks.BlockSet...))
	}

	channel, ts, err := td.api.PostMessageContext(ctx, cfg.ChannelID, opts...)
	if err != nil {
		return "", fmt.Errorf("send slack notification: %w", err)
	}
	if channel == "" || ts == "" {
		return "", fmt.Errorf("send slack notification: missing channel/ts in Slack API response")
	}
	return ts, nil
}

func (td *threadDelivery) updateViaChatAPI(ctx context.Context, cfg config, ts string, msg *slackapi.WebhookMessage) error {
	if strings.TrimSpace(ts) == "" {
		return nil
	}

	ctx = td.s.withMethodRateLimit(ctx, cfg, "chat.update")
	opts := []slackapi.MsgOption{slackapi.MsgOptionText(msg.Text, false)}
	if msg.Blocks != nil {
		opts = append(opts, slackapi.MsgOptionBlocks(msg.Blocks.BlockSet...))
	}

	channel, updatedTS, _, err := td.api.UpdateMessageContext(ctx, cfg.ChannelID, ts, opts...)
	if err != nil {
		return fmt.Errorf("update slack root notification: %w", err)
	}
	if channel == "" || updatedTS == "" {
		return fmt.Errorf("update slack root notification: missing channel/ts in Slack API response")
	}
	return nil
}

func (td *threadDelivery) bumpRootThreadEmoji(ctx context.Context, cfg config, rootTS, reaction string) error {
	ctx = td.s.withMethodRateLimit(ctx, cfg, "reactions.add")
	item := slackapi.ItemRef{Channel: cfg.ChannelID, Timestamp: rootTS}
	err := td.api.AddReactionContext(ctx, reaction, item)
	if err != nil && strings.Contains(err.Error(), "already_reacted") {
		return nil
	}
	return err
}

func (td *threadDelivery) removeRootThreadEmoji(ctx context.Context, cfg config, rootTS, reaction string) error {
	ctx = td.s.withMethodRateLimit(ctx, cfg, "reactions.remove")
	item := slackapi.ItemRef{Channel: cfg.ChannelID, Timestamp: rootTS}
	err := td.api.RemoveReactionContext(ctx, reaction, item)
	if err != nil && strings.Contains(err.Error(), "no_reaction") {
		return nil
	}
	return err
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

func shouldBumpReaction(prevReaction string, event hibernatorv1alpha1.NotificationEvent, bumpTS, reaction string) bool {
	if bumpTS == "" || reaction == "" {
		return false
	}
	if event == hibernatorv1alpha1.EventStart {
		return true
	}
	return strings.TrimSpace(prevReaction) != reaction
}

func shouldPreserveTerminalRoot(prevReaction string, event hibernatorv1alpha1.NotificationEvent) bool {
	if !isTerminalReaction(prevReaction) {
		return false
	}
	return isNonTerminalEvent(event)
}

func isTerminalReaction(reaction string) bool {
	switch strings.TrimSpace(reaction) {
	case "white_check_mark", "x":
		return true
	default:
		return false
	}
}

func isNonTerminalEvent(event hibernatorv1alpha1.NotificationEvent) bool {
	switch event {
	case hibernatorv1alpha1.EventStart,
		hibernatorv1alpha1.EventExecutionProgress,
		hibernatorv1alpha1.EventRecovery,
		hibernatorv1alpha1.EventPhaseChange:
		return true
	default:
		return false
	}
}

func resolveRootThreadTimestamp(payload sink.Payload, sinkState map[string]string) string {
	event := hibernatorv1alpha1.NotificationEvent(payload.Event)
	if event == hibernatorv1alpha1.EventStart {
		return ""
	}
	if sinkState == nil {
		return ""
	}
	if prevRef := strings.TrimSpace(sinkState["slack.thread.ref"]); prevRef != "" && prevRef != threadReference(payload) {
		return ""
	}

	return strings.TrimSpace(sinkState["slack.thread.root_ts"])
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

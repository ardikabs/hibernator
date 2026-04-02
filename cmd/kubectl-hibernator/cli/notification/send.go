/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package notification

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/go-logr/logr"
	"github.com/samber/lo"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/common"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/output"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/printers"
	"github.com/ardikabs/hibernator/internal/notification"
	"github.com/ardikabs/hibernator/internal/notification/sink"
	slacksink "github.com/ardikabs/hibernator/internal/notification/sink/slack"
	telegramsink "github.com/ardikabs/hibernator/internal/notification/sink/telegram"
	webhooksink "github.com/ardikabs/hibernator/internal/notification/sink/webhook"
)

type sendOptions struct {
	root         *common.RootOptions
	event        string
	sinkName     string
	sinkType     string
	planName     string
	planFile     string
	configFile   string
	templateFile string
	dryRun       bool
	phase        string
}

// isLocalMode returns true when enough local flags are provided to skip cluster access entirely.
func (o *sendOptions) isLocalMode() bool {
	return o.sinkType != "" && o.configFile != ""
}

func newSendCommand(opts *common.RootOptions) *cobra.Command {
	sendOpts := &sendOptions{root: opts}

	cmd := &cobra.Command{
		Use:     "send [notification-name]",
		Aliases: []string{"trigger"},
		Short:   "Send a test notification through a configured sink",
		Long: `Send a test notification by resolving the notification's sink configuration,
rendering the template, and delivering the message. This command operates
locally — it reads config directly and calls the sink endpoint without
involving the controller.

The --event flag is required and specifies which event type to simulate.
If the notification has a single sink, --sink is optional and auto-selected.
If multiple sinks exist, --sink is required to specify which one.

Use --plan to populate the payload from a real plan's current status.
Use --plan-file to load a HibernatePlan from a local YAML file instead.
Without either, a synthetic demo payload is generated.

Use --dry-run to render the message without sending it.

LOCAL MODE (no cluster access):
  When --sink-type and --config-file are provided, the command runs fully locally
  without any Kubernetes cluster access. The notification-name argument is optional
  in this mode. Combine with --plan-file to use a real plan definition from disk.

Examples:
  # Send a test notification (auto-selects sink if only one)
  kubectl hibernator notification send my-notification --event Success

  # Send to a specific sink
  kubectl hibernator notification send my-notification --event Start --sink slack-alerts

  # Dry-run: render message without sending
  kubectl hibernator notification send my-notification --event Failure --dry-run

  # Use payload from a real plan
  kubectl hibernator notification send my-notification --event Success --plan my-plan

  # Use local config file instead of cluster Secret
  kubectl hibernator notification send my-notification --event Start --config-file ./slack-config.json

  # Use local template file
  kubectl hibernator notification send my-notification --event Success --template-file ./custom.gotpl

  # Fully local (no cluster needed)
  kubectl hibernator notification send --event Success --sink-type slack --config-file ./slack-config.json

  # Fully local with plan from file
  kubectl hibernator notification send --event Failure --sink-type telegram --config-file ./telegram.json --plan-file ./my-plan.yaml

  # Control phase in synthetic payload
  kubectl hibernator notification send my-notification --event Start --phase Hibernating`,
		Args: cobra.MaximumNArgs(1),
		RunE: output.WrapRunE(func(ctx context.Context, args []string) error {
			var notifName string
			if len(args) > 0 {
				notifName = args[0]
			}
			return runSend(ctx, sendOpts, notifName)
		}),
	}

	cmd.Flags().StringVarP(&sendOpts.event, "event", "e", "", "Event type to simulate (Start, Success, Failure, Recovery, PhaseChange)")
	cmd.Flags().StringVarP(&sendOpts.planName, "plan", "p", "", "Populate payload from this HibernatePlan's status (cluster)")
	cmd.Flags().StringVarP(&sendOpts.planFile, "plan-file", "f", "", "Local YAML file of a HibernatePlan to populate payload from")
	cmd.Flags().StringVarP(&sendOpts.configFile, "config-file", "c", "", "Local JSON file for sink config (bypasses cluster Secret)")
	cmd.Flags().StringVarP(&sendOpts.templateFile, "template-file", "t", "", "Local Go template file (overrides cluster ConfigMap)")
	cmd.Flags().StringVar(&sendOpts.sinkName, "sink", "", "Sink name to send to (auto-selected if only one sink)")
	cmd.Flags().StringVar(&sendOpts.sinkType, "sink-type", "", "Sink type for local mode (slack, telegram, webhook); requires --config-file")
	cmd.Flags().BoolVar(&sendOpts.dryRun, "dry-run", false, "Render the message but do not send it")
	cmd.Flags().StringVar(&sendOpts.phase, "phase", "", "Phase to use in synthetic payload (default: auto from event)")

	cmd.MarkFlagsMutuallyExclusive("plan", "plan-file")
	lo.Must0(cmd.MarkFlagRequired("event"))

	return cmd
}

func runSend(ctx context.Context, opts *sendOptions, notifName string) error {
	// Validate event
	if !isValidEvent(opts.event) {
		return fmt.Errorf("invalid event %q: must be one of Start, Success, Failure, Recovery, PhaseChange", opts.event)
	}

	if opts.isLocalMode() {
		return runSendLocal(ctx, opts)
	}

	// Cluster mode: notification name is required
	if notifName == "" {
		return fmt.Errorf("notification-name argument is required in cluster mode; use --sink-type and --config-file for local mode")
	}

	// Validate sink-type is not used without config-file (partial local doesn't make sense)
	if opts.sinkType != "" && opts.configFile == "" {
		return fmt.Errorf("--sink-type requires --config-file for local mode")
	}

	c, err := common.NewK8sClient(opts.root)
	if err != nil {
		return err
	}

	ns := common.ResolveNamespace(opts.root)

	// Fetch the notification
	var notif hibernatorv1alpha1.HibernateNotification
	if err := c.Get(ctx, types.NamespacedName{Name: notifName, Namespace: ns}, &notif); err != nil {
		return fmt.Errorf("failed to get HibernateNotification %q in namespace %q: %w", notifName, ns, err)
	}

	// Resolve sink
	targetSink, err := resolveSink(notif.Spec.Sinks, opts.sinkName)
	if err != nil {
		return err
	}

	// Resolve sink config
	configBytes, err := resolveSinkConfig(ctx, c, ns, targetSink, opts.configFile)
	if err != nil {
		return err
	}

	// Resolve template
	customTemplate, err := resolveTemplate(ctx, c, ns, targetSink, opts.templateFile)
	if err != nil {
		return err
	}

	// Build payload
	payload, err := buildPayload(ctx, c, ns, opts, targetSink)
	if err != nil {
		return err
	}

	return executeSend(ctx, opts, string(targetSink.Type), targetSink.Name, configBytes, customTemplate, payload)
}

// runSendLocal handles fully local mode (no cluster access).
func runSendLocal(ctx context.Context, opts *sendOptions) error {
	if !isValidSinkType(opts.sinkType) {
		return fmt.Errorf("invalid sink type %q: must be one of slack, telegram, webhook", opts.sinkType)
	}

	if opts.planName != "" {
		return fmt.Errorf("--plan requires cluster access; use --plan-file for local mode")
	}

	// Read config file
	configBytes, err := os.ReadFile(opts.configFile)
	if err != nil {
		return fmt.Errorf("failed to read config file %q: %w", opts.configFile, err)
	}

	// Read optional template file
	var customTemplate *string
	if opts.templateFile != "" {
		data, err := os.ReadFile(opts.templateFile)
		if err != nil {
			return fmt.Errorf("failed to read template file %q: %w", opts.templateFile, err)
		}
		s := string(data)
		customTemplate = &s
	}

	// Build a synthetic NotificationSink for payload construction
	localSink := hibernatorv1alpha1.NotificationSink{
		Name: opts.sinkName,
		Type: hibernatorv1alpha1.NotificationSinkType(opts.sinkType),
	}
	if localSink.Name == "" {
		localSink.Name = opts.sinkType
	}

	// Build payload: from local plan file or synthetic
	var payload sink.Payload
	if opts.planFile != "" {
		payload, err = buildPayloadFromPlanFile(opts, localSink)
		if err != nil {
			return err
		}
	} else {
		ns := common.ResolveNamespace(opts.root)
		payload = buildSyntheticPayload(opts, ns, localSink)
	}

	return executeSend(ctx, opts, opts.sinkType, localSink.Name, configBytes, customTemplate, payload)
}

// executeSend is the shared send/dry-run logic for both cluster and local modes.
func executeSend(ctx context.Context, opts *sendOptions, sinkType, sinkName string, configBytes []byte, customTemplate *string, payload sink.Payload) error {
	logger := logr.FromSlogHandler(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	tmplEngine := notification.NewTemplateEngine(logger)
	sinkInstance, err := createSinkInstance(sinkType, tmplEngine)
	if err != nil {
		return err
	}

	sendOpts := sink.SendOptions{
		Config:         configBytes,
		CustomTemplate: customTemplate,
	}

	out := output.FromContext(ctx)

	if opts.dryRun {
		templateStr := resolveTemplateString(sendOpts.CustomTemplate, sinkType)
		rendered := tmplEngine.Render(ctx, templateStr, payload)

		dryRunOutput := &printers.NotifSendDryRunOutput{
			SinkName: sinkName,
			SinkType: sinkType,
			Event:    opts.event,
			Rendered: rendered,
		}

		d := &printers.Dispatcher{JSON: opts.root.JsonOutput}
		return d.PrintObj(dryRunOutput, os.Stdout)
	}

	if err := sinkInstance.Send(ctx, payload, sendOpts); err != nil {
		return fmt.Errorf("failed to send notification via %s sink %q: %w", sinkType, sinkName, err)
	}

	out.Success("Notification sent via %s sink %q for event %q", sinkType, sinkName, opts.event)
	return nil
}

func isValidEvent(event string) bool {
	switch hibernatorv1alpha1.NotificationEvent(event) {
	case hibernatorv1alpha1.EventStart,
		hibernatorv1alpha1.EventSuccess,
		hibernatorv1alpha1.EventFailure,
		hibernatorv1alpha1.EventRecovery,
		hibernatorv1alpha1.EventPhaseChange:
		return true
	}
	return false
}

func isValidSinkType(sinkType string) bool {
	switch hibernatorv1alpha1.NotificationSinkType(sinkType) {
	case hibernatorv1alpha1.SinkSlack,
		hibernatorv1alpha1.SinkTelegram,
		hibernatorv1alpha1.SinkWebhook:
		return true
	}
	return false
}

// buildPayloadFromPlanFile loads a HibernatePlan from a local YAML/JSON file
// and constructs a sink.Payload from it.
func buildPayloadFromPlanFile(opts *sendOptions, targetSink hibernatorv1alpha1.NotificationSink) (sink.Payload, error) {
	data, err := os.ReadFile(opts.planFile)
	if err != nil {
		return sink.Payload{}, fmt.Errorf("failed to read plan file %q: %w", opts.planFile, err)
	}

	var plan hibernatorv1alpha1.HibernatePlan
	// Support both YAML and JSON
	if err := yaml.UnmarshalStrict(data, &plan); err != nil {
		// Fall back to JSON if YAML strict fails
		if jsonErr := json.Unmarshal(data, &plan); jsonErr != nil {
			return sink.Payload{}, fmt.Errorf("failed to parse plan file %q (YAML: %v, JSON: %w)", opts.planFile, err, jsonErr)
		}
	}

	phase := string(plan.Status.Phase)
	if opts.phase != "" {
		phase = opts.phase
	}
	if phase == "" {
		phase = defaultPhaseForEvent(opts.event)
	}

	targets := make([]sink.TargetInfo, len(plan.Status.Executions))
	for i, exec := range plan.Status.Executions {
		targets[i] = sink.TargetInfo{
			Name:     exec.Target,
			Executor: exec.Executor,
			State:    string(exec.State),
			Message:  exec.Message,
		}
	}
	// If no executions in status, build from spec targets
	if len(targets) == 0 {
		targets = make([]sink.TargetInfo, len(plan.Spec.Targets))
		for i, t := range plan.Spec.Targets {
			targets[i] = sink.TargetInfo{
				Name:     t.Name,
				Executor: string(t.Type),
				State:    string(hibernatorv1alpha1.StatePending),
				Message:  "Target pending execution",
			}
		}
	}

	ns := plan.Namespace
	if ns == "" {
		ns = common.ResolveNamespace(opts.root)
	}

	operation := string(plan.Status.CurrentOperation)
	if operation == "" {
		operation = string(hibernatorv1alpha1.OperationHibernate)
	}

	cycleID := plan.Status.CurrentCycleID
	if cycleID == "" {
		cycleID = fmt.Sprintf("local-%d", time.Now().Unix())
	}

	return sink.Payload{
		Plan: sink.PlanInfo{
			Name:        plan.Name,
			Namespace:   ns,
			Labels:      plan.Labels,
			Annotations: plan.Annotations,
		},
		Event:         opts.event,
		Timestamp:     time.Now(),
		Phase:         phase,
		PreviousPhase: previousPhaseForEvent(opts.event, phase),
		Operation:     operation,
		CycleID:       cycleID,
		Targets:       targets,
		ErrorMessage:  plan.Status.ErrorMessage,
		RetryCount:    plan.Status.RetryCount,
		SinkName:      targetSink.Name,
		SinkType:      string(targetSink.Type),
	}, nil
}

// resolveSink selects the sink from spec.sinks. Auto-selects if only one exists.
func resolveSink(sinks []hibernatorv1alpha1.NotificationSink, name string) (hibernatorv1alpha1.NotificationSink, error) {
	if name == "" {
		if len(sinks) == 1 {
			return sinks[0], nil
		}
		names := make([]string, len(sinks))
		for i, s := range sinks {
			names[i] = s.Name
		}
		return hibernatorv1alpha1.NotificationSink{}, fmt.Errorf("multiple sinks configured, use --sink to specify one of: %v", names)
	}

	for _, s := range sinks {
		if s.Name == name {
			return s, nil
		}
	}

	names := make([]string, len(sinks))
	for i, s := range sinks {
		names[i] = s.Name
	}
	return hibernatorv1alpha1.NotificationSink{}, fmt.Errorf("sink %q not found in notification spec.sinks, available: %v", name, names)
}

// resolveSinkConfig reads sink config from --config-file or cluster Secret.
func resolveSinkConfig(ctx context.Context, c client.Client, ns string, targetSink hibernatorv1alpha1.NotificationSink, configFile string) ([]byte, error) {
	if configFile != "" {
		data, err := os.ReadFile(configFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read config file %q: %w", configFile, err)
		}
		return data, nil
	}

	var secret corev1.Secret
	if err := c.Get(ctx, types.NamespacedName{Name: targetSink.SecretRef.Name, Namespace: ns}, &secret); err != nil {
		return nil, fmt.Errorf("failed to get Secret %q in namespace %q: %w", targetSink.SecretRef.Name, ns, err)
	}

	key := "config"
	if targetSink.SecretRef.Key != nil {
		key = *targetSink.SecretRef.Key
	}

	data, ok := secret.Data[key]
	if !ok {
		return nil, fmt.Errorf("secret %q does not contain key %q", targetSink.SecretRef.Name, key)
	}
	return data, nil
}

// resolveTemplate reads template from --template-file or cluster ConfigMap.
func resolveTemplate(ctx context.Context, c client.Client, ns string, targetSink hibernatorv1alpha1.NotificationSink, templateFile string) (*string, error) {
	if templateFile != "" {
		data, err := os.ReadFile(templateFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read template file %q: %w", templateFile, err)
		}
		s := string(data)
		return &s, nil
	}

	if targetSink.TemplateRef == nil {
		return nil, nil
	}

	var cm corev1.ConfigMap
	if err := c.Get(ctx, types.NamespacedName{Name: targetSink.TemplateRef.Name, Namespace: ns}, &cm); err != nil {
		return nil, fmt.Errorf("failed to get ConfigMap %q in namespace %q: %w", targetSink.TemplateRef.Name, ns, err)
	}

	key := "template.gotpl"
	if targetSink.TemplateRef.Key != nil {
		key = *targetSink.TemplateRef.Key
	}

	data, ok := cm.Data[key]
	if !ok {
		return nil, fmt.Errorf("ConfigMap %q does not contain key %q", targetSink.TemplateRef.Name, key)
	}
	return &data, nil
}

// buildPayload constructs the sink.Payload from a real plan or synthetic data.
func buildPayload(ctx context.Context, c client.Client, ns string, opts *sendOptions, targetSink hibernatorv1alpha1.NotificationSink) (sink.Payload, error) {
	if opts.planName != "" {
		return buildPayloadFromPlan(ctx, c, ns, opts, targetSink)
	}
	return buildSyntheticPayload(opts, ns, targetSink), nil
}

// buildPayloadFromPlan loads the plan from the cluster and constructs a sink.Payload from its status.
func buildPayloadFromPlan(ctx context.Context, c client.Client, ns string, opts *sendOptions, targetSink hibernatorv1alpha1.NotificationSink) (sink.Payload, error) {
	var plan hibernatorv1alpha1.HibernatePlan
	if err := c.Get(ctx, types.NamespacedName{Name: opts.planName, Namespace: ns}, &plan); err != nil {
		return sink.Payload{}, fmt.Errorf("failed to get HibernatePlan %q in namespace %q: %w", opts.planName, ns, err)
	}

	phase := string(plan.Status.Phase)
	if opts.phase != "" {
		phase = opts.phase
	}

	targets := make([]sink.TargetInfo, len(plan.Status.Executions))
	for i, exec := range plan.Status.Executions {
		targets[i] = sink.TargetInfo{
			Name:     exec.Target,
			Executor: exec.Executor,
			State:    string(exec.State),
			Message:  exec.Message,
		}
	}

	return sink.Payload{
		Plan: sink.PlanInfo{
			Name:        plan.Name,
			Namespace:   plan.Namespace,
			Labels:      plan.Labels,
			Annotations: plan.Annotations,
		},
		Event:         opts.event,
		Timestamp:     time.Now(),
		Phase:         phase,
		PreviousPhase: previousPhaseForEvent(opts.event, phase),
		Operation:     string(plan.Status.CurrentOperation),
		CycleID:       plan.Status.CurrentCycleID,
		Targets:       targets,
		ErrorMessage:  plan.Status.ErrorMessage,
		RetryCount:    plan.Status.RetryCount,
		SinkName:      targetSink.Name,
		SinkType:      string(targetSink.Type),
	}, nil
}

// buildSyntheticPayload constructs a demo payload with realistic fields for testing and dry-run purposes.
func buildSyntheticPayload(opts *sendOptions, ns string, targetSink hibernatorv1alpha1.NotificationSink) sink.Payload {
	phase := opts.phase
	if phase == "" {
		phase = defaultPhaseForEvent(opts.event)
	}

	operation := string(hibernatorv1alpha1.OperationHibernate)
	if phase == string(hibernatorv1alpha1.PhaseActive) || phase == string(hibernatorv1alpha1.PhaseWakingUp) {
		operation = string(hibernatorv1alpha1.OperationWakeUp)
	}

	p := sink.Payload{
		Plan: sink.PlanInfo{
			Name:      "demo-plan",
			Namespace: ns,
			Labels:    map[string]string{"env": "demo", "team": "platform"},
		},
		Event:         opts.event,
		Timestamp:     time.Now(),
		Phase:         phase,
		PreviousPhase: previousPhaseForEvent(opts.event, phase),
		Operation:     operation,
		CycleID:       fmt.Sprintf("demo-%d", time.Now().Unix()),
		Targets: []sink.TargetInfo{
			{
				Name:     "demo-eks-cluster",
				Executor: "eks",
				State:    string(hibernatorv1alpha1.StateCompleted),
				Message:  "All node groups scaled"},
			{
				Name:     "demo-rds-instance",
				Executor: "rds",
				State:    string(hibernatorv1alpha1.StateCompleted),
				Message:  "Instance stopped"},
		},
		SinkName: targetSink.Name,
		SinkType: string(targetSink.Type),
	}

	if opts.event == "Failure" || opts.event == "Recovery" {
		p.Targets = append(p.Targets, sink.TargetInfo{
			Name:     "demo-ec2-instance",
			Executor: "ec2",
			State:    string(hibernatorv1alpha1.StateFailed),
			Message:  "Access Denied",
		})
		p.ErrorMessage = "simulated error: deadline exceeded during shutdown"
		p.RetryCount = 2
	}

	return p
}

func defaultPhaseForEvent(event string) string {
	switch hibernatorv1alpha1.NotificationEvent(event) {
	case hibernatorv1alpha1.EventStart:
		return string(hibernatorv1alpha1.PhaseHibernating)
	case hibernatorv1alpha1.EventSuccess:
		return string(hibernatorv1alpha1.PhaseHibernated)
	case hibernatorv1alpha1.EventFailure:
		return string(hibernatorv1alpha1.PhaseError)
	case hibernatorv1alpha1.EventRecovery:
		return string(hibernatorv1alpha1.PhaseHibernating)
	case hibernatorv1alpha1.EventPhaseChange:
		return string(hibernatorv1alpha1.PhaseHibernating)
	default:
		return string(hibernatorv1alpha1.PhaseActive)
	}
}

func previousPhaseForEvent(event, currentPhase string) string {
	switch hibernatorv1alpha1.NotificationEvent(event) {
	case hibernatorv1alpha1.EventStart:
		if currentPhase == string(hibernatorv1alpha1.PhaseWakingUp) {
			return string(hibernatorv1alpha1.PhaseHibernated)
		}
		return string(hibernatorv1alpha1.PhaseActive)
	case hibernatorv1alpha1.EventSuccess:
		if currentPhase == string(hibernatorv1alpha1.PhaseActive) {
			return string(hibernatorv1alpha1.PhaseWakingUp)
		}
		return string(hibernatorv1alpha1.PhaseHibernating)
	case hibernatorv1alpha1.EventFailure:
		if currentPhase == string(hibernatorv1alpha1.PhaseError) {
			return string(hibernatorv1alpha1.PhaseHibernating)
		}
		return string(hibernatorv1alpha1.PhaseHibernating)
	case hibernatorv1alpha1.EventRecovery:
		return string(hibernatorv1alpha1.PhaseError)
	case hibernatorv1alpha1.EventPhaseChange:
		switch hibernatorv1alpha1.PlanPhase(currentPhase) {
		case hibernatorv1alpha1.PhaseHibernating:
			return string(hibernatorv1alpha1.PhaseActive)
		case hibernatorv1alpha1.PhaseHibernated:
			return string(hibernatorv1alpha1.PhaseHibernating)
		case hibernatorv1alpha1.PhaseWakingUp:
			return string(hibernatorv1alpha1.PhaseHibernated)
		case hibernatorv1alpha1.PhaseActive:
			return string(hibernatorv1alpha1.PhaseWakingUp)
		case hibernatorv1alpha1.PhaseError:
			return string(hibernatorv1alpha1.PhaseHibernating)
		default:
			return string(hibernatorv1alpha1.PhaseActive)
		}
	default:
		return ""
	}
}

func createSinkInstance(sinkType string, renderer sink.Renderer) (sink.Sink, error) {
	switch hibernatorv1alpha1.NotificationSinkType(sinkType) {
	case hibernatorv1alpha1.SinkSlack:
		return slacksink.New(renderer), nil
	case hibernatorv1alpha1.SinkTelegram:
		return telegramsink.New(renderer), nil
	case hibernatorv1alpha1.SinkWebhook:
		return webhooksink.New(renderer), nil
	default:
		return nil, fmt.Errorf("unsupported sink type %q", sinkType)
	}
}

// resolveTemplateString returns the custom template or the sink's default template marker.
// When custom is nil, we pass an empty-ish value so the sink uses its built-in default.
func resolveTemplateString(custom *string, sinkType string) string {
	if custom != nil {
		return *custom
	}
	// Use the sink's built-in default template via the actual constant
	switch hibernatorv1alpha1.NotificationSinkType(sinkType) {
	case hibernatorv1alpha1.SinkSlack:
		return slacksink.DefaultTemplate
	case hibernatorv1alpha1.SinkTelegram:
		return telegramsink.DefaultTemplate
	default:
		return "[{{ .Event }}] {{ .Operation }} — {{ .Plan.Namespace }}/{{ .Plan.Name }} | Phase: {{ .Phase }}"
	}
}

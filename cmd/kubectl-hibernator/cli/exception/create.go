/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package exception

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/samber/lo"
	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"
	sigsyaml "sigs.k8s.io/yaml"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/common"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/output"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/printers"
	"github.com/ardikabs/hibernator/cmd/runner/timeparse"
	"github.com/ardikabs/hibernator/internal/wellknown"
)

const maxCreateAttempts = 3

type createOptions struct {
	root *common.RootOptions

	plan, selector, exceptionType, from, until string
	windowStart, windowEnd, days               string
	leadTime                                   string
	purpose                                    string
	managedBy                                  string
	format                                     string
	dryRun                                     bool

	now            func() time.Time
	generateSuffix suffixGenerator
}

func newCreateCommand(root *common.RootOptions) *cobra.Command {
	opts := &createOptions{
		root:           root,
		now:            time.Now,
		generateSuffix: randomAlphanumeric,
	}

	cmd := &cobra.Command{
		Use:   "create <prefix>",
		Short: "Create ScheduleExceptions for selected HibernatePlans",
		Args:  cobra.ExactArgs(1),
		RunE: output.WrapRunE(func(ctx context.Context, args []string) error {
			return runCreate(ctx, opts, args[0])
		}),
	}

	cmd.Flags().StringVar(&opts.plan, "plan", "", "Create an exception for this exact HibernatePlan")
	cmd.Flags().StringVarP(&opts.selector, "selector", "l", "", "Select HibernatePlans by Kubernetes label selector")
	cmd.Flags().StringVar(&opts.exceptionType, "type", "", "Exception type: suspend, extend, or replace")
	cmd.Flags().StringVar(&opts.from, "from", "", "Start of the exception validity period")
	cmd.Flags().StringVar(&opts.until, "until", "", "End of the exception validity period")
	cmd.Flags().StringVar(&opts.windowStart, "window-start", "", "Exception window start in HH:MM format")
	cmd.Flags().StringVar(&opts.windowEnd, "window-end", "", "Exception window end in HH:MM format")
	cmd.Flags().StringVar(&opts.days, "days", "", "Comma-separated weekdays and ranges (e.g. MON,WED-FRI); defaults to all days")
	cmd.Flags().StringVar(&opts.purpose, "purpose", "", "Human reason for the exception, stored as an annotation (e.g. \"Ramadan support\")")
	cmd.Flags().StringVar(&opts.managedBy, "managed-by", defaultManagedBy, "Tool recorded as managing the exception (standard managed-by label); empty omits it")
	cmd.Flags().StringVarP(&opts.format, "output", "o", "", "Output format for --dry-run previews (supported: yaml)")
	cmd.Flags().StringVar(&opts.leadTime, "lead-time", "", "Buffer before suspension windows that prevents new hibernation starts (e.g., 30m, 1h); only valid with --type suspend")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "Preview the ScheduleExceptions that would be created without making changes")

	for _, flag := range []string{"type", "from", "until", "window-start", "window-end"} {
		lo.Must0(cmd.MarkFlagRequired(flag))
	}

	return cmd
}

func runCreate(ctx context.Context, opts *createOptions, prefix string) error {
	if (opts.plan == "") == (opts.selector == "") {
		return fmt.Errorf("exactly one of --plan or --selector must be specified")
	}

	exceptionType, err := parseExceptionType(opts.exceptionType)
	if err != nil {
		return err
	}
	if opts.leadTime != "" {
		if exceptionType != hibernatorv1alpha1.ExceptionSuspend {
			return fmt.Errorf("--lead-time is only valid with --type suspend")
		}
		if _, err := time.ParseDuration(opts.leadTime); err != nil {
			return fmt.Errorf("invalid --lead-time value: %w", err)
		}
	}
	days, err := parseDays(opts.days)
	if err != nil {
		return fmt.Errorf("invalid --days: %w", err)
	}
	if err := validateWindow(opts.windowStart, opts.windowEnd); err != nil {
		return fmt.Errorf("invalid window: %w", err)
	}
	if _, err := compactPrefix(prefix, maxExceptionPrefixLen); err != nil {
		return err
	}
	if opts.managedBy != "" {
		if problems := validation.IsValidLabelValue(opts.managedBy); len(problems) > 0 {
			return fmt.Errorf("invalid --managed-by value %q: %s", opts.managedBy, strings.Join(problems, "; "))
		}
	}
	if opts.format != "" && !opts.dryRun {
		// --output only affects dry-run previews; warn and continue so the
		// flag never blocks a real creation.
		output.FromContext(ctx).Warning("--output is only used with --dry-run; ignoring --output %q", opts.format)
		opts.format = ""
	}
	if opts.format != "" && opts.format != "yaml" {
		return fmt.Errorf("unsupported --output %q (supported: yaml)", opts.format)
	}

	now := opts.now()
	validFrom, err := timeparse.ParseDeadline(opts.from, now)
	if err != nil {
		return fmt.Errorf("invalid --from value: %w", err)
	}

	validUntil, err := timeparse.ParseDeadline(opts.until, now)
	if err != nil {
		return fmt.Errorf("invalid --until value: %w", err)
	}
	if !validFrom.Before(validUntil) {
		return fmt.Errorf("--from must be before --until")
	}
	validFrom = validFrom.UTC()
	validUntil = validUntil.UTC()
	if opts.selector != "" {
		if _, err := labels.Parse(opts.selector); err != nil {
			return fmt.Errorf("parse plan selector %q: %w", opts.selector, err)
		}
	}

	c, err := common.ClientFactory(opts.root)
	if err != nil {
		return err
	}
	plans, selector, err := selectPlans(ctx, c, common.ResolveNamespace(opts.root), opts.plan, opts.selector)
	if err != nil {
		return err
	}

	if opts.dryRun {
		preview := make([]hibernatorv1alpha1.ScheduleException, 0, len(plans))
		for i := range plans {
			plan := &plans[i]
			name, err := buildExceptionName(prefix, opts.generateSuffix)
			if err != nil {
				return err
			}
			exceptionLabels := selectorLabels(plan.Labels, selector)
			exceptionLabels[wellknown.LabelPlan] = plan.Name
			preview = append(preview, *buildExceptionObject(
				plan, exceptionLabels, buildProvenance(opts.purpose, opts.managedBy, exceptionLabels), name, exceptionType,
				validFrom, validUntil, opts.windowStart, opts.windowEnd, days, opts.leadTime,
			))
		}
		if opts.format == "yaml" {
			return printExceptionPreviewYAML(ctx, preview)
		}
		if !opts.root.JsonOutput {
			output.FromContext(ctx).Info("[DRY-RUN] Would create %d ScheduleExceptions", len(preview))
		}
		dispatcher := &printers.Dispatcher{JSON: opts.root.JsonOutput}
		return dispatcher.PrintObj(&printers.ExceptionListOutput{Items: preview}, output.WriterFromContext(ctx))
	}

	results := make([]printers.ExceptionOperationResult, 0, len(plans))
	failedPlans := make([]string, 0)
	for i := range plans {
		plan := &plans[i]
		result, createErr := createExceptionForPlan(
			ctx, c, plan, selector, prefix, exceptionType,
			validFrom, validUntil, opts.windowStart, opts.windowEnd, days, opts.leadTime,
			opts.purpose, opts.managedBy, opts.generateSuffix,
		)
		if createErr != nil {
			failedPlans = append(failedPlans, plan.Name)
			result.Message = fmt.Sprintf("%s; retry with: %s", result.Message, recreateCreateCommand(prefix, opts, plan.Name))
		}
		results = append(results, result)
	}

	dispatcher := &printers.Dispatcher{JSON: opts.root.JsonOutput}
	if err := dispatcher.PrintObj(&printers.ExceptionOperationOutput{
		Action: "create",
		Items:  results,
	}, output.WriterFromContext(ctx)); err != nil {
		return err
	}
	if len(failedPlans) > 0 {
		commands := make([]string, len(failedPlans))
		for i, name := range failedPlans {
			commands[i] = "  " + recreateCreateCommand(prefix, opts, name)
		}
		return fmt.Errorf(
			"failed to create ScheduleException for plans %s; retry failed plans individually with:\n%s",
			strings.Join(failedPlans, ", "), strings.Join(commands, "\n"),
		)
	}

	return nil
}

func createExceptionForPlan(
	ctx context.Context,
	c client.Client,
	plan *hibernatorv1alpha1.HibernatePlan,
	selector labels.Selector,
	prefix string,
	exceptionType hibernatorv1alpha1.ExceptionType,
	validFrom, validUntil time.Time,
	windowStart, windowEnd string,
	days []string,
	leadTime string,
	purpose string,
	managedBy string,
	generateSuffix suffixGenerator,
) (printers.ExceptionOperationResult, error) {
	result := printers.ExceptionOperationResult{Plan: plan.Name}
	exceptionLabels := selectorLabels(plan.Labels, selector)
	exceptionLabels[wellknown.LabelPlan] = plan.Name
	annotations := buildProvenance(purpose, managedBy, exceptionLabels)

	for attempt := 0; attempt < maxCreateAttempts; attempt++ {
		name, err := buildExceptionName(prefix, generateSuffix)
		if err != nil {
			result.Result = "FAILED"
			result.Message = err.Error()
			return result, err
		}
		result.Name = name

		exception := buildExceptionObject(plan, exceptionLabels, annotations, name, exceptionType, validFrom, validUntil, windowStart, windowEnd, days, leadTime)
		err = c.Create(ctx, exception)
		if err == nil {
			result.Result = "SUCCESS"
			result.Message = "created"
			return result, nil
		}
		if !apierrors.IsAlreadyExists(err) || attempt == maxCreateAttempts-1 {
			result.Result = "FAILED"
			result.Message = err.Error()
			return result, err
		}
	}

	result.Result = "FAILED"
	result.Message = fmt.Sprintf("failed to generate a unique exception name after %d attempts", maxCreateAttempts)
	return result, fmt.Errorf("%s", result.Message)
}

// buildProvenance stamps ownership metadata onto exception labels and
// returns the exception annotations. The managed-by label overwrites any
// plan-copied value: the exception's manager is whoever runs this command,
// not the plan's manager. A nil map is returned when there is no purpose.
func buildProvenance(purpose, managedBy string, exceptionLabels map[string]string) map[string]string {
	if managedBy != "" {
		exceptionLabels[managedByLabelKey] = managedBy
	}
	if purpose == "" {
		return nil
	}
	return map[string]string{wellknown.AnnotationPurpose: purpose}
}

// buildExceptionObject constructs the exact ScheduleException that would be
// persisted for a plan, shared by the create and dry-run paths so previews
// never drift from reality.
func buildExceptionObject(
	plan *hibernatorv1alpha1.HibernatePlan,
	exceptionLabels map[string]string,
	annotations map[string]string,
	name string,
	exceptionType hibernatorv1alpha1.ExceptionType,
	validFrom, validUntil time.Time,
	windowStart, windowEnd string,
	days []string,
	leadTime string,
) *hibernatorv1alpha1.ScheduleException {
	return &hibernatorv1alpha1.ScheduleException{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   plan.Namespace,
			Labels:      exceptionLabels,
			Annotations: annotations,
		},
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			PlanRef:    hibernatorv1alpha1.PlanReference{Name: plan.Name},
			ValidFrom:  metav1.NewTime(validFrom),
			ValidUntil: metav1.NewTime(validUntil),
			Type:       exceptionType,
			LeadTime:   leadTime,
			Windows: []hibernatorv1alpha1.OffHourWindow{{
				Start:      windowStart,
				End:        windowEnd,
				DaysOfWeek: days,
			}},
		},
	}
}

// printExceptionPreviewYAML renders dry-run preview objects as an applyable
// multi-document YAML stream with one ScheduleException per document.
func printExceptionPreviewYAML(ctx context.Context, preview []hibernatorv1alpha1.ScheduleException) error {
	var b strings.Builder
	for i := range preview {
		exception := preview[i]
		exception.TypeMeta = metav1.TypeMeta{
			Kind:       "ScheduleException",
			APIVersion: hibernatorv1alpha1.GroupVersion.String(),
		}
		raw, err := sigsyaml.Marshal(exception)
		if err != nil {
			return fmt.Errorf("marshal exception preview to YAML: %w", err)
		}
		if i > 0 {
			b.WriteString("---\n")
		}
		b.Write(raw)
	}
	if _, err := io.WriteString(output.WriterFromContext(ctx), b.String()); err != nil {
		return fmt.Errorf("write exception preview: %w", err)
	}
	return nil
}

// recreateCreateCommand rebuilds the exact CLI invocation that reproduces the
// create call for a single plan, so users can copy-paste it to retry a plan
// that failed during bulk creation.
func recreateCreateCommand(prefix string, opts *createOptions, planName string) string {
	var b strings.Builder
	b.WriteString("kubectl hibernator exception create ")
	b.WriteString(shellQuote(prefix))
	if opts.root.Namespace != "" {
		b.WriteString(" -n ")
		b.WriteString(shellQuote(opts.root.Namespace))
	}
	b.WriteString(" --plan ")
	b.WriteString(shellQuote(planName))
	b.WriteString(" --type ")
	b.WriteString(shellQuote(opts.exceptionType))
	b.WriteString(" --from ")
	b.WriteString(shellQuote(opts.from))
	b.WriteString(" --until ")
	b.WriteString(shellQuote(opts.until))
	b.WriteString(" --window-start ")
	b.WriteString(shellQuote(opts.windowStart))
	b.WriteString(" --window-end ")
	b.WriteString(shellQuote(opts.windowEnd))
	if opts.days != "" {
		b.WriteString(" --days ")
		b.WriteString(shellQuote(opts.days))
	}
	if opts.leadTime != "" {
		b.WriteString(" --lead-time ")
		b.WriteString(shellQuote(opts.leadTime))
	}
	if opts.purpose != "" {
		b.WriteString(" --purpose ")
		b.WriteString(shellQuote(opts.purpose))
	}
	if opts.managedBy != "" {
		b.WriteString(" --managed-by ")
		b.WriteString(shellQuote(opts.managedBy))
	}
	return b.String()
}

// shellQuote returns value unquoted when it contains only shell-safe
// characters, otherwise double-quoted so values with spaces (for example
// natural-language dates) survive copy-paste. Note the result uses Go
// double-quote escaping: values containing shell-active characters inside
// double quotes ($, backticks, !) or non-ASCII text are quoted but not
// made fully inert — inspect before pasting untrusted input.
func shellQuote(value string) string {
	if value == "" {
		return `""`
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			r == '-' || r == '_' || r == '.' || r == '/' || r == ',' || r == ':') {
			return fmt.Sprintf("%q", value)
		}
	}
	return value
}

func parseExceptionType(value string) (hibernatorv1alpha1.ExceptionType, error) {
	switch hibernatorv1alpha1.ExceptionType(value) {
	case hibernatorv1alpha1.ExceptionSuspend, hibernatorv1alpha1.ExceptionExtend, hibernatorv1alpha1.ExceptionReplace:
		return hibernatorv1alpha1.ExceptionType(value), nil
	default:
		return "", fmt.Errorf("--type must be one of suspend, extend, or replace")
	}
}

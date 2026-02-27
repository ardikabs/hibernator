/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package preview

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/utils/clock"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/common"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/printers"
	"github.com/ardikabs/hibernator/internal/scheduler"
)

type previewOptions struct {
	root   *common.RootOptions
	file   string
	events int
}

// NewCommand creates the "preview" command.
func NewCommand(opts *common.RootOptions) *cobra.Command {
	previewOpts := &previewOptions{root: opts, events: 5}

	cmd := &cobra.Command{
		Use:     "preview <plan-name>",
		Aliases: []string{"schedule"},
		Short:   "Preview schedule details and upcoming events for a HibernatePlan",
		Long: `Show the hibernation schedule including timezone, off-hour windows,
upcoming hibernate/wakeup events, and any active schedule exceptions.

Works with both cluster resources and local YAML files:
  kubectl hibernator preview my-plan
  kubectl hibernator preview --file plan.yaml`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPreview(cmd.Context(), previewOpts, args)
		},
	}

	cmd.Flags().StringVarP(&previewOpts.file, "file", "f", "", "Path to a local HibernatePlan YAML file")
	cmd.Flags().IntVar(&previewOpts.events, "events", 5, "Number of upcoming events to display")

	return cmd
}

func runPreview(ctx context.Context, opts *previewOptions, args []string) error {
	var plan hibernatorv1alpha1.HibernatePlan

	if opts.file != "" {
		// Load from local YAML file
		if err := loadPlanFromFile(opts.file, &plan); err != nil {
			return err
		}
	} else {
		// Load from cluster
		if len(args) == 0 {
			return fmt.Errorf("plan name is required (or use --file for local YAML)")
		}

		c, err := common.NewK8sClient(opts.root)
		if err != nil {
			return err
		}

		ns := common.ResolveNamespace(opts.root)
		if err := c.Get(ctx, types.NamespacedName{Name: args[0], Namespace: ns}, &plan); err != nil {
			return fmt.Errorf("failed to get HibernatePlan %q in namespace %q: %w", args[0], ns, err)
		}
	}

	// Evaluate schedule
	evaluator := scheduler.NewScheduleEvaluator(clock.RealClock{})
	windows := common.ConvertAPIWindows(plan.Spec.Schedule.OffHours)

	result, err := evaluator.Evaluate(windows, plan.Spec.Schedule.Timezone, nil)
	if err != nil {
		return fmt.Errorf("failed to evaluate schedule: %w", err)
	}

	// Fetch active exceptions if from cluster
	var exceptions []hibernatorv1alpha1.ExceptionReference
	if opts.file == "" {
		exceptions = plan.Status.ActiveExceptions
	}

	events, err := common.ComputeUpcomingEvents(plan.Spec.Schedule, opts.events)
	if err != nil {
		events = []common.ScheduleEvent{}
	}

	output := &printers.ScheduleOutput{
		Plan:       plan,
		Result:     result,
		Exceptions: exceptions,
		Events:     events,
	}

	d := &printers.Dispatcher{JSON: opts.root.JsonOutput}
	return d.PrintObj(output, os.Stdout)
}

func loadPlanFromFile(path string, plan *hibernatorv1alpha1.HibernatePlan) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read file %q: %w", path, err)
	}

	// Handle multi-document YAML: find the HibernatePlan document
	decoder := yaml.NewYAMLOrJSONDecoder(strings.NewReader(string(data)), 4096)
	for {
		var raw hibernatorv1alpha1.HibernatePlan
		if err := decoder.Decode(&raw); err != nil {
			if err.Error() == "EOF" {
				break
			}
			return fmt.Errorf("failed to parse YAML from %q: %w", path, err)
		}
		if raw.Kind == "HibernatePlan" || (raw.Kind == "" && raw.Spec.Schedule.Timezone != "") {
			*plan = raw
			return nil
		}
	}

	// Fallback: try as single-document
	if err := yaml.UnmarshalStrict(data, plan); err != nil {
		return fmt.Errorf("no HibernatePlan found in %q: %w", path, err)
	}

	return nil
}


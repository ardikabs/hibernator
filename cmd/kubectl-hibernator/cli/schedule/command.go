/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package schedule

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/utils/clock"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/common"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/printers"
	"github.com/ardikabs/hibernator/internal/scheduler"
)

type scheduleOptions struct {
	root   *common.RootOptions
	file   string
	events int
}

// NewCommand creates the "schedule" command.
func NewCommand(opts *common.RootOptions) *cobra.Command {
	schedOpts := &scheduleOptions{root: opts, events: 5}

	cmd := &cobra.Command{
		Use:   "schedule <plan-name>",
		Short: "Display schedule details and upcoming events for a HibernatePlan",
		Long: `Show the hibernation schedule including timezone, off-hour windows,
upcoming hibernate/wakeup events, and any active schedule exceptions.

Works with both cluster resources and local YAML files:
  kubectl hibernator schedule my-plan
  kubectl hibernator schedule --file plan.yaml`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSchedule(cmd.Context(), schedOpts, args)
		},
	}

	cmd.Flags().StringVarP(&schedOpts.file, "file", "f", "", "Path to a local HibernatePlan YAML file")
	cmd.Flags().IntVar(&schedOpts.events, "events", 5, "Number of upcoming events to display")

	return cmd
}

func runSchedule(ctx context.Context, opts *scheduleOptions, args []string) error {
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
	windows, err := convertPlanWindows(plan.Spec.Schedule)
	if err != nil {
		return fmt.Errorf("failed to convert schedule windows: %w", err)
	}

	result, err := evaluator.Evaluate(windows, plan.Spec.Schedule.Timezone, nil)
	if err != nil {
		return fmt.Errorf("failed to evaluate schedule: %w", err)
	}

	// Fetch active exceptions if from cluster
	var exceptions []hibernatorv1alpha1.ExceptionReference
	if opts.file == "" {
		exceptions = plan.Status.ActiveExceptions
	}

	events, err := computeUpcomingEvents(plan.Spec.Schedule, opts.events)
	if err != nil {
		// Degrade gracefully, empty events
		events = []printers.ScheduleEvent{}
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

func convertPlanWindows(schedule hibernatorv1alpha1.Schedule) ([]scheduler.OffHourWindow, error) {
	windows := make([]scheduler.OffHourWindow, len(schedule.OffHours))
	for i, w := range schedule.OffHours {
		windows[i] = scheduler.OffHourWindow{
			Start:      w.Start,
			End:        w.End,
			DaysOfWeek: w.DaysOfWeek,
		}
	}

	return windows, nil
}

// computeUpcomingEvents computes the next N hibernate/wakeup events.
func computeUpcomingEvents(schedule hibernatorv1alpha1.Schedule, count int) ([]printers.ScheduleEvent, error) {
	hibernateCron, wakeUpCron, err := scheduler.ConvertOffHoursToCron(convertAPIWindows(schedule.OffHours))
	if err != nil {
		return nil, fmt.Errorf("failed to convert to cron: %w", err)
	}

	loc, err := time.LoadLocation(schedule.Timezone)
	if err != nil {
		return nil, fmt.Errorf("invalid timezone %q: %w", schedule.Timezone, err)
	}

	now := time.Now().In(loc)

	parser := scheduler.NewCronParser()
	hSched, err := parser.Parse(hibernateCron)
	if err != nil {
		return nil, fmt.Errorf("failed to parse hibernate cron: %w", err)
	}

	wSched, err := parser.Parse(wakeUpCron)
	if err != nil {
		return nil, fmt.Errorf("failed to parse wakeup cron: %w", err)
	}

	// Generate next N events from both schedules, interleaved by time
	var events []printers.ScheduleEvent
	hNext := hSched.Next(now)
	wNext := wSched.Next(now)

	for len(events) < count {
		if hNext.Before(wNext) {
			events = append(events, printers.ScheduleEvent{Time: hNext, Operation: "Hibernate"})
			hNext = hSched.Next(hNext)
		} else {
			events = append(events, printers.ScheduleEvent{Time: wNext, Operation: "WakeUp"})
			wNext = wSched.Next(wNext)
		}
	}

	return events, nil
}

// convertAPIWindows converts API OffHourWindows to scheduler OffHourWindows.
func convertAPIWindows(apiWindows []hibernatorv1alpha1.OffHourWindow) []scheduler.OffHourWindow {
	out := make([]scheduler.OffHourWindow, len(apiWindows))
	for i, w := range apiWindows {
		out[i] = scheduler.OffHourWindow{
			Start:      w.Start,
			End:        w.End,
			DaysOfWeek: w.DaysOfWeek,
		}
	}
	return out
}

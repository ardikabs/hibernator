/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/utils/clock"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/scheduler"
)

type showScheduleOptions struct {
	root   *rootOptions
	file   string
	events int
}

// newShowScheduleCommand creates the "show schedule" command.
func newShowScheduleCommand(opts *rootOptions) *cobra.Command {
	schedOpts := &showScheduleOptions{root: opts, events: 5}

	cmd := &cobra.Command{
		Use:   "schedule <plan-name>",
		Short: "Display schedule details and upcoming events for a HibernatePlan",
		Long: `Show the hibernation schedule including timezone, off-hour windows,
upcoming hibernate/wakeup events, and any active schedule exceptions.

Works with both cluster resources and local YAML files:
  kubectl hibernator show schedule my-plan
  kubectl hibernator show schedule --file plan.yaml`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runShowSchedule(cmd.Context(), schedOpts, args)
		},
	}

	cmd.Flags().StringVarP(&schedOpts.file, "file", "f", "", "Path to a local HibernatePlan YAML file")
	cmd.Flags().IntVar(&schedOpts.events, "events", 5, "Number of upcoming events to display")

	return cmd
}

func runShowSchedule(ctx context.Context, opts *showScheduleOptions, args []string) error {
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

		c, err := newK8sClient(opts.root)
		if err != nil {
			return err
		}

		ns := resolveNamespace(opts.root)
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

	if opts.root.jsonOutput {
		return printScheduleJSON(plan, result, exceptions, opts.events)
	}

	return printScheduleTable(plan, result, exceptions, opts.events)
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

// scheduleEvent represents a single upcoming event.
type scheduleEvent struct {
	Time      time.Time `json:"time"`
	Operation string    `json:"operation"`
}

// computeUpcomingEvents computes the next N hibernate/wakeup events.
func computeUpcomingEvents(schedule hibernatorv1alpha1.Schedule, count int) ([]scheduleEvent, error) {
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
	var events []scheduleEvent
	hNext := hSched.Next(now)
	wNext := wSched.Next(now)

	for len(events) < count {
		if hNext.Before(wNext) {
			events = append(events, scheduleEvent{Time: hNext, Operation: "Hibernate"})
			hNext = hSched.Next(hNext)
		} else {
			events = append(events, scheduleEvent{Time: wNext, Operation: "WakeUp"})
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

type scheduleJSONOutput struct {
	Plan       string                                  `json:"plan"`
	Namespace  string                                  `json:"namespace,omitempty"`
	Timezone   string                                  `json:"timezone"`
	OffHours   []hibernatorv1alpha1.OffHourWindow      `json:"offHours"`
	Current    scheduleCurrentJSON                     `json:"current"`
	Events     []scheduleEvent                         `json:"upcomingEvents"`
	Exceptions []hibernatorv1alpha1.ExceptionReference `json:"activeExceptions,omitempty"`
}

type scheduleCurrentJSON struct {
	State             string    `json:"state"`
	NextHibernateTime time.Time `json:"nextHibernateTime"`
	NextWakeUpTime    time.Time `json:"nextWakeUpTime"`
}

func printScheduleJSON(plan hibernatorv1alpha1.HibernatePlan, result *scheduler.EvaluationResult, exceptions []hibernatorv1alpha1.ExceptionReference, eventCount int) error {
	events, err := computeUpcomingEvents(plan.Spec.Schedule, eventCount)
	if err != nil {
		events = []scheduleEvent{} // degrade gracefully
	}

	output := scheduleJSONOutput{
		Plan:      plan.Name,
		Namespace: plan.Namespace,
		Timezone:  plan.Spec.Schedule.Timezone,
		OffHours:  plan.Spec.Schedule.OffHours,
		Current: scheduleCurrentJSON{
			State:             result.CurrentState,
			NextHibernateTime: result.NextHibernateTime,
			NextWakeUpTime:    result.NextWakeUpTime,
		},
		Events:     events,
		Exceptions: exceptions,
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(output)
}

func printScheduleTable(plan hibernatorv1alpha1.HibernatePlan, result *scheduler.EvaluationResult, exceptions []hibernatorv1alpha1.ExceptionReference, eventCount int) error {
	fmt.Printf("Plan:      %s\n", plan.Name)
	if plan.Namespace != "" {
		fmt.Printf("Namespace: %s\n", plan.Namespace)
	}
	fmt.Printf("Timezone:  %s\n", plan.Spec.Schedule.Timezone)
	fmt.Println()

	// Off-hour windows
	fmt.Println("Off-Hour Windows:")
	for _, w := range plan.Spec.Schedule.OffHours {
		fmt.Printf("  %s - %s  [%s]\n", w.Start, w.End, strings.Join(w.DaysOfWeek, ", "))
	}
	fmt.Println()

	// Current state
	fmt.Println("Current State:")
	fmt.Printf("  State:              %s\n", result.CurrentState)
	fmt.Printf("  Next Hibernate:     %s (%s)\n", result.NextHibernateTime.Format(time.RFC3339), humanDuration(time.Until(result.NextHibernateTime)))
	fmt.Printf("  Next WakeUp:        %s (%s)\n", result.NextWakeUpTime.Format(time.RFC3339), humanDuration(time.Until(result.NextWakeUpTime)))
	fmt.Println()

	// Upcoming events
	events, err := computeUpcomingEvents(plan.Spec.Schedule, eventCount)
	if err == nil && len(events) > 0 {
		fmt.Printf("Upcoming Events (next %d):\n", eventCount)
		for i, ev := range events {
			fmt.Printf("  %d. %-10s  %s (%s)\n", i+1, ev.Operation, ev.Time.Format(time.RFC3339), humanDuration(time.Until(ev.Time)))
		}
		fmt.Println()
	}

	// Active exceptions
	if len(exceptions) > 0 {
		fmt.Println("Active Exceptions:")
		for _, ex := range exceptions {
			fmt.Printf("  - %s (type=%s, until=%s, state=%s)\n",
				ex.Name, ex.Type, ex.ValidUntil.Format(time.RFC3339), ex.State)
		}
		fmt.Println()
	}

	return nil
}

// humanDuration formats a duration into a human-readable string.
func humanDuration(d time.Duration) string {
	if d < 0 {
		return "past"
	}

	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}

	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}

	hours := int(d.Hours())
	if hours < 24 {
		mins := int(d.Minutes()) % 60
		if mins > 0 {
			return fmt.Sprintf("%dh%dm", hours, mins)
		}
		return fmt.Sprintf("%dh", hours)
	}

	days := hours / 24
	remainHours := hours % 24
	if remainHours > 0 {
		return fmt.Sprintf("%dd%dh", days, remainHours)
	}
	return fmt.Sprintf("%dd", days)
}

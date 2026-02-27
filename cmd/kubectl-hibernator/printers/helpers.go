/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package printers

import (
	"fmt"
	"time"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/common"
)

func FormatNextEvent(event *common.ScheduleEvent) string {
	if event == nil {
		return "-"
	}

	duration := time.Until(event.Time)
	return fmt.Sprintf("%s (%s)", event.Operation, HumanDuration(duration))
}

// FormatAge formats a duration into a Kubernetes-like age string.
func FormatAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	default:
		return fmt.Sprintf("%dmo", int(d.Hours()/24/30))
	}
}

// HumanDuration formats a duration into a human-readable string.
func HumanDuration(d time.Duration) string {
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

// StateIcon returns a visual icon for execution state.
func StateIcon(state hibernatorv1alpha1.ExecutionState) string {
	switch state {
	case hibernatorv1alpha1.StateCompleted:
		return "[OK]"
	case hibernatorv1alpha1.StateFailed:
		return "[FAIL]"
	case hibernatorv1alpha1.StateRunning:
		return "[..]"
	case hibernatorv1alpha1.StatePending:
		return "[--]"
	default:
		return "[??]"
	}
}

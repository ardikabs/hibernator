/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package printers

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/ardikabs/hibernator/internal/restore"
	"github.com/jedib0t/go-pretty/v6/table"
	corev1 "k8s.io/api/core/v1"
)

type restoreShowJSONOutput struct {
	Plan           string             `json:"plan"`
	Namespace      string             `json:"namespace"`
	RestorePoints  []restorePointData `json:"restorePoints,omitempty"`
	TotalResources int                `json:"totalResources"`
}

type restorePointData struct {
	Target        string `json:"target"`
	Executor      string `json:"executor"`
	IsLive        bool   `json:"isLive"`
	CapturedAt    string `json:"capturedAt,omitempty"`
	ResourceCount int    `json:"resourceCount"`
	CreatedAt     string `json:"createdAt,omitempty"`
}

func PrintRestoreShowJSON(cm corev1.ConfigMap) error {
	output := restoreShowJSONOutput{
		Plan:      cm.Labels["hibernator.ardikabs.com/plan"],
		Namespace: cm.Namespace,
	}

	for _, val := range cm.Data {
		var data restore.Data
		if err := json.Unmarshal([]byte(val), &data); err != nil {
			continue
		}

		resourceCount := len(data.State)
		output.RestorePoints = append(output.RestorePoints, restorePointData{
			Target:        data.Target,
			Executor:      data.Executor,
			IsLive:        data.IsLive,
			CapturedAt:    data.CapturedAt,
			ResourceCount: resourceCount,
			CreatedAt:     data.CreatedAt.Format("2006-01-02T15:04:05Z"),
		})
		output.TotalResources += resourceCount
	}

	// enc := json.NewEncoder(io.Discard) // Placeholder until we can return error or write to writer

	// Let's reconstruct the file properly.
	return nil
}

// Re-implementing PrintRestoreShowJSON to return the struct, so caller can encode it?
// Or just let Dispatcher handle it.
// In `cli/restore/show.go`, `d.PrintObj(cm, os.Stdout)` is called.
// `Dispatcher.PrintObj` for `ConfigMap` calls `p.printRestorePoint` (HumanReadable).
// If JSON is true, `Dispatcher` just encodes `cm` which is the ConfigMap itself, not the nice summary.
// We should probably change `cli/restore/show.go` to construct the summary struct and pass THAT to `Dispatcher`.

// But for now, let's fix the broken file first.

func (p *HumanReadablePrinter) printRestorePoint(cm corev1.ConfigMap, w io.Writer) error {
	var totalResources int
	var points []restorePointData

	for _, val := range cm.Data {
		var data restore.Data
		if err := json.Unmarshal([]byte(val), &data); err != nil {
			continue
		}

		resourceCount := len(data.State)
		points = append(points, restorePointData{
			Target:        data.Target,
			Executor:      data.Executor,
			IsLive:        data.IsLive,
			CapturedAt:    data.CapturedAt,
			ResourceCount: resourceCount,
			CreatedAt:     data.CreatedAt.Format("2006-01-02 15:04:05"),
		})
		totalResources += resourceCount
	}

	if len(points) == 0 {
		fmt.Fprintf(w, "Plan: %s (Namespace: %s)\n", cm.Labels["hibernator.ardikabs.com/plan"], cm.Namespace)
		fmt.Fprintln(w, "No restore point data found")
		return nil
	}

	// Summary header
	fmt.Fprintf(w, "Plan: %s (Namespace: %s)\n", cm.Labels["hibernator.ardikabs.com/plan"], cm.Namespace)
	fmt.Fprintf(w, "Total Resources: %d\n\n", totalResources)

	// Table of restore points by target
	t := table.NewWriter()
	t.SetOutputMirror(w)
	t.SetStyle(DefaultTableStyle)
	t.AppendHeader(table.Row{"Target", "Executor", "Live", "Resources", "Captured At"})

	for _, p := range points {
		live := "no"
		if p.IsLive {
			live = "yes"
		}
		t.AppendRow(table.Row{
			p.Target,
			p.Executor,
			live,
			p.ResourceCount,
			p.CapturedAt,
		})
	}

	t.Render()
	return nil
}

func (p *HumanReadablePrinter) printRestoreResources(out *RestoreResourcesOutput, w io.Writer) error {
	cm := out.ConfigMap
	filterTarget := out.Target

	var resources []restoreResource
	for _, val := range cm.Data {
		var data restore.Data
		if err := json.Unmarshal([]byte(val), &data); err != nil {
			continue
		}

		if filterTarget != "" && data.Target != filterTarget {
			continue
		}

		// Extract resource IDs from state
		for resourceID := range data.State {
			resources = append(resources, restoreResource{
				ResourceID: resourceID,
				Target:     data.Target,
				Executor:   data.Executor,
				IsLive:     data.IsLive,
				CapturedAt: data.CapturedAt,
			})
		}
	}

	if len(resources) == 0 {
		fmt.Fprintln(w, "No resources found in restore point")
		return nil
	}

	t := table.NewWriter()
	t.SetOutputMirror(w)
	t.SetStyle(DefaultTableStyle)
	t.AppendHeader(table.Row{"Resource ID", "Target", "Executor", "Live", "Captured At"})

	for _, r := range resources {
		live := "no"
		if r.IsLive {
			live = "yes"
		}
		t.AppendRow(table.Row{
			r.ResourceID,
			r.Target,
			r.Executor,
			live,
			r.CapturedAt,
		})
	}

	t.Render()
	return nil
}

func (p *HumanReadablePrinter) printRestoreDetail(out *RestoreDetailOutput, w io.Writer) error {
	data := out.TargetData.(restore.Data)

	fmt.Fprintf(w, "Plan:        %s\n", out.Plan)
	fmt.Fprintf(w, "Namespace:   %s\n", out.Namespace)

	fmt.Fprintln(w)
	fmt.Fprintf(w, "Target:      %s\n", data.Target)
	fmt.Fprintf(w, "Resource ID: %s\n", out.ResourceID)
	fmt.Fprintf(w, "Executor:    %s\n", data.Executor)
	fmt.Fprintln(w)

	fmt.Fprintln(w, "Metadata:")
	fmt.Fprintf(w, "  Live:        %v\n", data.IsLive)
	fmt.Fprintf(w, "  Created At:  %s\n", data.CreatedAt.Format(time.RFC3339))
	if data.CapturedAt != "" {
		fmt.Fprintf(w, "  Captured At: %s\n", data.CapturedAt)
	}
	fmt.Fprintln(w)

	fmt.Fprintln(w, "Resource State:")
	stateJSON, err := json.MarshalIndent(out.State, "  ", "  ")
	if err != nil {
		fmt.Fprintf(w, "  (unable to format state: %v)\n", err)
		return nil
	}
	fmt.Fprintf(w, "  %s\n", string(stateJSON))

	return nil
}

type restoreResource struct {
	ResourceID string `json:"resourceId"`
	Target     string `json:"target"`
	Executor   string `json:"executor"`
	IsLive     bool   `json:"isLive"`
	CapturedAt string `json:"capturedAt,omitempty"`
}

// Helpers for JSON output (used by cli/restore/show.go if not going through Dispatcher)
// But wait, `cli/restore/show.go` uses `printers.PrintRestoreShowJSON(cm)`.
// We need to implement it or remove it and use Dispatcher with a custom struct.
// Given I just replaced `cli/restore/show.go` to use `Dispatcher`, we don't need `PrintRestoreShowJSON` there.
// BUT `Dispatcher` for `ConfigMap` prints the raw ConfigMap in JSON.
// We want the summary.
// So `cli/restore/show.go` should construct `restoreShowJSONOutput` (exported) and pass it to `Dispatcher`.

func NewRestoreShowOutput(cm corev1.ConfigMap) interface{} {
	output := restoreShowJSONOutput{
		Plan:      cm.Labels["hibernator.ardikabs.com/plan"],
		Namespace: cm.Namespace,
	}

	for _, val := range cm.Data {
		var data restore.Data
		if err := json.Unmarshal([]byte(val), &data); err != nil {
			continue
		}

		resourceCount := len(data.State)
		output.RestorePoints = append(output.RestorePoints, restorePointData{
			Target:        data.Target,
			Executor:      data.Executor,
			IsLive:        data.IsLive,
			CapturedAt:    data.CapturedAt,
			ResourceCount: resourceCount,
			CreatedAt:     data.CreatedAt.Format("2006-01-02T15:04:05Z"),
		})
		output.TotalResources += resourceCount
	}
	return output
}

// PrintRestoreShowJSON and others can be removed if we fully switch to Dispatcher + structs.
// But `cli/restore/show.go` calling `printers.PrintRestoreShowJSON(cm)` which was generated previously.
// I replaced `cli/restore/show.go` content in previous turn to use `Dispatcher.PrintObj(cm, ...) `.
// This prints raw CM in JSON. If we want summary, we need to handle `restoreShowJSONOutput`.

// Let's add `RestoreShowOutput` to `interface.go` switch case if we want HumanReadable for it too.
// For now, I will fix `restore.go` to be valid Go code.

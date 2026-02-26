/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package printers

import (
	"encoding/json"
	"fmt"
	"io"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

// ResourcePrinter is an interface that knows how to print objects.
type ResourcePrinter interface {
	PrintObj(obj interface{}, w io.Writer) error
}

// Dispatcher is a helper to choose between JSON and Table printing
type Dispatcher struct {
	JSON bool
	YAML bool // Optional, for future
}

func (d *Dispatcher) PrintObj(obj interface{}, w io.Writer) error {
	if d.JSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(obj)
	}

	// Default to HumanReadable (Table)
	p := &HumanReadablePrinter{}
	return p.PrintObj(obj, w)
}

// HumanReadablePrinter handles table-like output for various resources
type HumanReadablePrinter struct{}

func (p *HumanReadablePrinter) PrintObj(obj interface{}, w io.Writer) error {
	switch v := obj.(type) {
	case hibernatorv1alpha1.HibernatePlanList:
		return p.printHibernatePlanList(v, w)
	case *hibernatorv1alpha1.HibernatePlanList:
		return p.printHibernatePlanList(*v, w)
	case hibernatorv1alpha1.HibernatePlan:
		return p.printHibernatePlan(v, w)
	case *hibernatorv1alpha1.HibernatePlan:
		return p.printHibernatePlan(*v, w)
	case corev1.ConfigMap:
		// Used for restore points
		return p.printRestorePoint(v, w)
	case *corev1.ConfigMap:
		return p.printRestorePoint(*v, w)
	case *ScheduleOutput:
		return p.printSchedule(v, w)
	case *RestoreDetailOutput:
		return p.printRestoreDetail(v, w)
	case *RestoreResourcesOutput:
		return p.printRestoreResources(v, w)
	default:
		return fmt.Errorf("no human-readable printer registered for %T", obj)
	}
}

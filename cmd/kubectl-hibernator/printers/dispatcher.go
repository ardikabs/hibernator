/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package printers

import "io"

// Dispatcher selects between JSON and human-readable (table) output formats.
type Dispatcher struct {
	JSON bool
	YAML bool // Optional, for future
}

func (d *Dispatcher) PrintObj(obj interface{}, w io.Writer) error {
	if d.JSON {
		p := &JSONPrinter{}
		return p.PrintObj(obj, w)
	}

	// Default to HumanReadable (Table)
	p := &ConsolePrinter{}
	return p.PrintObj(obj, w)
}

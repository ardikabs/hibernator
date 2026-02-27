/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package printers

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/samber/lo"
)

type textWriter struct {
	w *tabwriter.Writer
}

// newTextWriter creates a new textWriter that wraps the given io.Writer with tabwriter.
func newTextWriter(w io.Writer) *textWriter {
	return &textWriter{w: tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)}
}

// line writes a formatted line with a newline at the end.
func (t *textWriter) line(format string, args ...interface{}) {
	lo.Must1(fmt.Fprintf(t.w, format+"\n", args...))
}

// text writes formatted text without a trailing newline.
func (t *textWriter) text(format string, args ...interface{}) {
	lo.Must1(fmt.Fprintf(t.w, format, args...))
}

// newline writes an empty line.
func (t *textWriter) newline() {
	lo.Must1(fmt.Fprintln(t.w))
}

// flush flushes the underlying tabwriter.
func (t *textWriter) flush() error {
	return t.w.Flush()
}

// header writes a tab-separated header row with uppercase column names.
func (t *textWriter) header(columns ...string) {
	for i, col := range columns {
		if i > 0 {
			lo.Must1(fmt.Fprint(t.w, "\t"))
		}
		lo.Must1(fmt.Fprint(t.w, strings.ToUpper(col)))
	}
	lo.Must1(fmt.Fprintln(t.w))
}

// row writes a tab-separated row of values.
func (t *textWriter) row(values ...interface{}) {
	for i, val := range values {
		if i > 0 {
			lo.Must1(fmt.Fprint(t.w, "\t"))
		}
		lo.Must1(fmt.Fprint(t.w, val))
	}
	lo.Must1(fmt.Fprintln(t.w))
}

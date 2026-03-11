/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package output

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/samber/lo"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// Formatter defines output formatting for styled console messages.
type Formatter interface {
	Success(msg string, args ...interface{})
	Warning(msg string, args ...interface{})
	Error(msg string, args ...interface{})
	Hint(msg string, args ...interface{})
	Info(msg string, args ...interface{})
}

// SimpleFormatter outputs plain text without styling.
type SimpleFormatter struct {
	stdout io.Writer
	stderr io.Writer
}

// ColorFormatter outputs styled text with ANSI codes (only when TTY is detected).
type ColorFormatter struct {
	stdout io.Writer
	stderr io.Writer
	colors bool
}

// ANSI color codes
const (
	colorGreen  = "\033[32m"
	colorYellow = "\033[33;1m"
	colorRed    = "\033[31m"
	colorCyan   = "\033[36m"
	colorReset  = "\033[0m"
	checkmark   = "✓"
	warningMark = "⚠"
	errorMark   = "❌"
	hintMark    = "💡"
)

// NewFormatter creates an appropriate formatter based on terminal capabilities.
func NewFormatter(stdout, stderr io.Writer) Formatter {
	if shouldDisableColor() {
		return &SimpleFormatter{stdout: stdout, stderr: stderr}
	}

	if isTerminal(stderr) {
		return &ColorFormatter{
			stdout: stdout,
			stderr: stderr,
			colors: true,
		}
	}

	return &SimpleFormatter{stdout: stdout, stderr: stderr}
}

// shouldDisableColor checks environment variables and system settings to determine if color output should be disabled.
func shouldDisableColor() bool {
	if _, noColor := os.LookupEnv("NO_COLOR"); noColor {
		return true
	}

	if os.Getenv("TERM") == "dumb" {
		return true
	}

	return false
}

// isTerminal checks if the given writer is connected to a terminal.
func isTerminal(w io.Writer) bool {
	if f, ok := w.(*os.File); ok {
		return term.IsTerminal(int(f.Fd()))
	}
	return false
}

// SimpleFormatter implementations
func (f *SimpleFormatter) Success(msg string, args ...interface{}) {
	lo.Must1(fmt.Fprintf(f.stdout, msg+"\n", args...))
}

func (f *SimpleFormatter) Warning(msg string, args ...interface{}) {
	lo.Must1(fmt.Fprintf(f.stderr, warningMark+" Warning: "+msg+"\n", args...))
}

func (f *SimpleFormatter) Error(msg string, args ...interface{}) {
	lo.Must1(fmt.Fprintf(f.stderr, "Error: "+msg+"\n", args...))
}

func (f *SimpleFormatter) Hint(msg string, args ...interface{}) {
	lo.Must1(fmt.Fprintf(f.stderr, "Hint: "+msg+"\n", args...))
}

func (f *SimpleFormatter) Info(msg string, args ...interface{}) {
	lo.Must1(fmt.Fprintf(f.stdout, msg+"\n", args...))
}

// ColorFormatter implementations
func (f *ColorFormatter) Success(msg string, args ...interface{}) {
	prefix := fmt.Sprintf("%s%s%s ", colorGreen, checkmark, colorReset)
	lo.Must1(fmt.Fprintf(f.stdout, prefix+msg+"\n", args...))
}

func (f *ColorFormatter) Warning(msg string, args ...interface{}) {
	prefix := fmt.Sprintf("%s%s Warning:%s ", colorYellow, warningMark, colorReset)
	lo.Must1(fmt.Fprintf(f.stderr, prefix+msg+"\n", args...))
}

func (f *ColorFormatter) Error(msg string, args ...interface{}) {
	prefix := fmt.Sprintf("%s%s Error:%s ", colorRed, errorMark, colorReset)
	lo.Must1(fmt.Fprintf(f.stderr, prefix+msg+"\n", args...))
}

func (f *ColorFormatter) Hint(msg string, args ...interface{}) {
	prefix := fmt.Sprintf("%s%s Hint:%s ", colorCyan, hintMark, colorReset)
	lo.Must1(fmt.Fprintf(f.stderr, prefix+msg+"\n", args...))
}

func (f *ColorFormatter) Info(msg string, args ...interface{}) {
	lo.Must1(fmt.Fprintf(f.stdout, msg+"\n", args...))
}

// Context integration
type loggerKeyType struct{}

var loggerKey = loggerKeyType{}

// WithFormatter returns a new context with the given formatter injected.
func WithFormatter(ctx context.Context, f Formatter) context.Context {
	return context.WithValue(ctx, loggerKey, f)
}

// FromContext retrieves the formatter from the context.
func FromContext(ctx context.Context) Formatter {
	f, ok := ctx.Value(loggerKey).(Formatter)
	if !ok {
		return &SimpleFormatter{stdout: os.Stdout, stderr: os.Stderr}
	}
	return f
}

// WrapRunE wraps a RunE function to handle errors with the output formatter.
// It ensures that when SilenceErrors is enabled, errors are formatted and printed
// via the output formatter instead of Cobra's default handler.
func WrapRunE(fn func(ctx context.Context, args []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		err := fn(cmd.Context(), args)
		if err != nil {
			out := FromContext(cmd.Context())
			out.Error("%v", err)
		}
		return err
	}
}

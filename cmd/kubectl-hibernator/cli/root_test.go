/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package cli

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRootCommandRegistersExceptionGroup(t *testing.T) {
	cmd := NewRootCommand()

	var exception *cobra.Command
	for _, child := range cmd.Commands() {
		if child.Name() == "exception" {
			exception = child
		}
	}
	require.NotNil(t, exception, "root must register the exception command group")

	names := map[string]bool{}
	for _, child := range exception.Commands() {
		names[child.Name()] = true
		for _, alias := range child.Aliases {
			names[alias] = true
		}
	}
	for _, want := range []string{"create", "list", "ls", "describe", "delete"} {
		assert.True(t, names[want], "exception group must include %q", want)
	}

	assert.Contains(t, cmd.Long, "ScheduleException")
}

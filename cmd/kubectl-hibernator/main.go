/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package main

import (
	"context"
	"os"

	_ "time/tzdata"

	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/cli"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/output"
)

func main() {

	out := output.NewFormatter(os.Stdout, os.Stderr)
	ctx := output.WithFormatter(context.Background(), out)
	if cmd, err := cli.NewRootCommand().ExecuteContextC(ctx); err != nil {
		out.Error("%v", err)
		out.Hint("See '%s --help' for usage.", cmd.CommandPath())
		os.Exit(1)
	}
}

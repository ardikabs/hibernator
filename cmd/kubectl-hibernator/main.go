/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package main

import (
	"os"

	_ "time/tzdata"

	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/cmd"
)

func main() {
	if err := cmd.NewRootCommand().Execute(); err != nil {
		os.Exit(1)
	}
}
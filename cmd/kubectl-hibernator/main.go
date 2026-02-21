/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package main

import (
	"os"

	_ "time/tzdata"

	cliapp "github.com/ardikabs/hibernator/internal/app/cli"
)

func main() {
	if err := cliapp.NewRootCommand().Execute(); err != nil {
		os.Exit(1)
	}
}

/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package main

import (
	"os"

	runnerapp "github.com/ardikabs/hibernator/internal/app/runner"
)

func main() {
	cfg := runnerapp.ParseFlags()

	if err := runnerapp.Run(cfg); err != nil {
		os.Exit(1)
	}
}

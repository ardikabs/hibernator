/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package main

import (
	"os"

	"github.com/ardikabs/hibernator/cmd/runner/app"
)

func main() {
	cfg := app.ParseFlags()

	if err := app.Run(cfg); err != nil {
		os.Exit(1)
	}
}

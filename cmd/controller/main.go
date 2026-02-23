/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package main

import (
	"os"

	"github.com/ardikabs/hibernator/cmd/controller/app"
)

func main() {
	opts := app.ParseFlags()

	if err := app.Run(opts); err != nil {
		os.Exit(1)
	}
}

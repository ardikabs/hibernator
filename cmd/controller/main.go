/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package main

import (
	"os"

	controlerapp "github.com/ardikabs/hibernator/internal/app/controller"
)

func main() {
	opts := controlerapp.ParseFlags()

	if err := controlerapp.Run(opts); err != nil {
		os.Exit(1)
	}
}

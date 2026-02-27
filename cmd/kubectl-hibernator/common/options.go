/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package common

// RootOptions holds global options shared across subcommands.
type RootOptions struct {
	Kubeconfig string
	Namespace  string
	JsonOutput bool
}

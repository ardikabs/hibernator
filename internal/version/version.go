/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package version

import (
	"fmt"
	"strings"
)

// Version is the version of hibernator, injected at build time via ldflags.
var Version = "dev"

// CommitHash is the git commit hash, injected at build time via ldflags.
var CommitHash = "unknown"

// GetVersion returns the full version string.
func GetVersion() string {
	if !strings.HasPrefix(Version, "v") {
		return fmt.Sprintf("%s (%s)", Version, CommitHash)
	}

	return fmt.Sprintf("v%s (%s)", Version, CommitHash)
}

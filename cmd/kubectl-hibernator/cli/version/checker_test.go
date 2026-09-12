/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package version

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseVersionOrdersReleases(t *testing.T) {
	// Malformed tags collapse to 0 and never outrank a real release.
	for _, v := range []string{"garbage", "", "v1", "v1.2", "v1.2.x", "v-1.2.3"} {
		require.Equal(t, 0, parseVersion(v), "parseVersion(%q)", v)
	}

	ordered := []string{
		"v1.2.2",
		"v1.2.3-rc.1",
		"v1.2.3-rc.2",
		"v1.2.3",
		"v1.10.0",
		"v2.0.0",
	}
	for i := 1; i < len(ordered); i++ {
		require.Greater(t, parseVersion(ordered[i]), parseVersion(ordered[i-1]),
			"%s must outrank %s", ordered[i], ordered[i-1])
	}
}

func TestParseVersionNeverPanics(t *testing.T) {
	for _, v := range []string{"", "v", "latest", "v1.2.x", "v-1.2.3", "1.2", "v1.2.3.4.5"} {
		require.NotPanics(t, func() { parseVersion(v) }, "parseVersion(%q)", v)
	}
}

func TestIsRCVersion(t *testing.T) {
	require.True(t, isRCVersion("v1.6.0-rc.1"))
	require.False(t, isRCVersion("v1.6.0"))
	require.False(t, isRCVersion("v1.6.0-beta.1"))
}

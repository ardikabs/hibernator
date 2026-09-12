/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package common

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMarkAndIsMarkedTrue(t *testing.T) {
	m := map[string]string{}
	require.False(t, IsMarkedTrue(m, "k"))
	MarkTrue(m, "k")
	require.True(t, IsMarkedTrue(m, "k"))
	require.False(t, IsMarkedTrue(m, "other"))
	require.False(t, IsMarkedTrue(nil, "k"))
}

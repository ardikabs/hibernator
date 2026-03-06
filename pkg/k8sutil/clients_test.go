/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package k8sutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestObjectKeyFromString_ValidInput(t *testing.T) {
	nn, err := ObjectKeyFromString("default/my-plan")
	require.NoError(t, err)
	assert.Equal(t, "default", nn.Namespace)
	assert.Equal(t, "my-plan", nn.Name)
}

func TestObjectKeyFromString_NameWithSlash(t *testing.T) {
	// SplitN with n=2 — only the first slash is a separator; rest becomes the name.
	nn, err := ObjectKeyFromString("ns/name/extra")
	require.NoError(t, err)
	assert.Equal(t, "ns", nn.Namespace)
	assert.Equal(t, "name/extra", nn.Name)
}

func TestObjectKeyFromString_MissingSlash_ReturnsError(t *testing.T) {
	_, err := ObjectKeyFromString("just-a-name")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid format")
}

func TestObjectKeyFromString_EmptyString_ReturnsError(t *testing.T) {
	_, err := ObjectKeyFromString("")
	assert.Error(t, err)
}

func TestObjectKeyFromString_EmptyNamespace(t *testing.T) {
	nn, err := ObjectKeyFromString("/my-plan")
	require.NoError(t, err)
	assert.Equal(t, "", nn.Namespace)
	assert.Equal(t, "my-plan", nn.Name)
}

func TestObjectKeyFromString_EmptyName(t *testing.T) {
	nn, err := ObjectKeyFromString("default/")
	require.NoError(t, err)
	assert.Equal(t, "default", nn.Namespace)
	assert.Equal(t, "", nn.Name)
}

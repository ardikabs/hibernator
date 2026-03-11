/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package common

const valueTrue = "true"

// MarkTrue sets key in m to the conventional "true" marker used for Kubernetes annotations and labels.
func MarkTrue(m map[string]string, key string) {
	m[key] = valueTrue
}

// IsMarkedTrue reports whether key in m is set to the conventional "true" marker.
func IsMarkedTrue(m map[string]string, key string) bool {
	return m[key] == valueTrue
}

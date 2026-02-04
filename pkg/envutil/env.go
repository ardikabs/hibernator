/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package envutil

import (
	"os"
	"strconv"
	"time"
)

// GetString returns the environment variable value if set and non-empty, otherwise returns the default value.
func GetString(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// GetBool returns the environment variable value as bool if set, otherwise returns the default value.
// Accepts "true", "1" as true values, everything else is false.
func GetBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		return value == "true" || value == "1"
	}
	return defaultValue
}

// GetInt returns the environment variable value as int if set and valid, otherwise returns the default value.
func GetInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultValue
}

// GetDuration returns the environment variable value as time.Duration if set and valid, otherwise returns the default value.
func GetDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if duration, err := time.ParseDuration(value); err == nil {
			return duration
		}
	}
	return defaultValue
}

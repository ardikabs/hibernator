/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package envutil

import (
	"os"
	"testing"
	"time"
)

func TestGetString(t *testing.T) {
	tests := []struct {
		name         string
		envKey       string
		envValue     string
		defaultValue string
		expected     string
	}{
		{
			name:         "returns env value when set",
			envKey:       "TEST_STRING",
			envValue:     "custom-value",
			defaultValue: "default-value",
			expected:     "custom-value",
		},
		{
			name:         "returns default when env is empty",
			envKey:       "TEST_STRING_EMPTY",
			envValue:     "",
			defaultValue: "default-value",
			expected:     "default-value",
		},
		{
			name:         "returns default when env is not set",
			envKey:       "TEST_STRING_UNSET",
			envValue:     "",
			defaultValue: "default-value",
			expected:     "default-value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			if tt.envValue != "" {
				os.Setenv(tt.envKey, tt.envValue)
				defer os.Unsetenv(tt.envKey)
			}

			// Execute
			result := GetString(tt.envKey, tt.defaultValue)

			// Assert
			if result != tt.expected {
				t.Errorf("GetString() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestGetBool(t *testing.T) {
	tests := []struct {
		name         string
		envKey       string
		envValue     string
		defaultValue bool
		expected     bool
	}{
		{
			name:         "returns true when env is 'true'",
			envKey:       "TEST_BOOL_TRUE",
			envValue:     "true",
			defaultValue: false,
			expected:     true,
		},
		{
			name:         "returns true when env is '1'",
			envKey:       "TEST_BOOL_ONE",
			envValue:     "1",
			defaultValue: false,
			expected:     true,
		},
		{
			name:         "returns false when env is 'false'",
			envKey:       "TEST_BOOL_FALSE",
			envValue:     "false",
			defaultValue: true,
			expected:     false,
		},
		{
			name:         "returns false when env is '0'",
			envKey:       "TEST_BOOL_ZERO",
			envValue:     "0",
			defaultValue: true,
			expected:     false,
		},
		{
			name:         "returns false when env is random string",
			envKey:       "TEST_BOOL_RANDOM",
			envValue:     "random",
			defaultValue: true,
			expected:     false,
		},
		{
			name:         "returns default when env is empty",
			envKey:       "TEST_BOOL_EMPTY",
			envValue:     "",
			defaultValue: true,
			expected:     true,
		},
		{
			name:         "returns default when env is not set",
			envKey:       "TEST_BOOL_UNSET",
			envValue:     "",
			defaultValue: false,
			expected:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			if tt.envValue != "" {
				os.Setenv(tt.envKey, tt.envValue)
				defer os.Unsetenv(tt.envKey)
			}

			// Execute
			result := GetBool(tt.envKey, tt.defaultValue)

			// Assert
			if result != tt.expected {
				t.Errorf("GetBool() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestGetInt(t *testing.T) {
	tests := []struct {
		name         string
		envKey       string
		envValue     string
		defaultValue int
		expected     int
	}{
		{
			name:         "returns env value when valid int",
			envKey:       "TEST_INT_VALID",
			envValue:     "42",
			defaultValue: 10,
			expected:     42,
		},
		{
			name:         "returns env value when negative int",
			envKey:       "TEST_INT_NEGATIVE",
			envValue:     "-5",
			defaultValue: 10,
			expected:     -5,
		},
		{
			name:         "returns default when env is invalid int",
			envKey:       "TEST_INT_INVALID",
			envValue:     "not-a-number",
			defaultValue: 10,
			expected:     10,
		},
		{
			name:         "returns default when env is float",
			envKey:       "TEST_INT_FLOAT",
			envValue:     "3.14",
			defaultValue: 10,
			expected:     10,
		},
		{
			name:         "returns default when env is empty",
			envKey:       "TEST_INT_EMPTY",
			envValue:     "",
			defaultValue: 10,
			expected:     10,
		},
		{
			name:         "returns default when env is not set",
			envKey:       "TEST_INT_UNSET",
			envValue:     "",
			defaultValue: 99,
			expected:     99,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			if tt.envValue != "" {
				os.Setenv(tt.envKey, tt.envValue)
				defer os.Unsetenv(tt.envKey)
			}

			// Execute
			result := GetInt(tt.envKey, tt.defaultValue)

			// Assert
			if result != tt.expected {
				t.Errorf("GetInt() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestGetDuration(t *testing.T) {
	tests := []struct {
		name         string
		envKey       string
		envValue     string
		defaultValue time.Duration
		expected     time.Duration
	}{
		{
			name:         "returns env value when valid duration",
			envKey:       "TEST_DURATION_VALID",
			envValue:     "5m",
			defaultValue: 10 * time.Minute,
			expected:     5 * time.Minute,
		},
		{
			name:         "returns env value for hours",
			envKey:       "TEST_DURATION_HOURS",
			envValue:     "2h",
			defaultValue: 1 * time.Hour,
			expected:     2 * time.Hour,
		},
		{
			name:         "returns env value for seconds",
			envKey:       "TEST_DURATION_SECONDS",
			envValue:     "30s",
			defaultValue: 1 * time.Minute,
			expected:     30 * time.Second,
		},
		{
			name:         "returns env value for complex duration",
			envKey:       "TEST_DURATION_COMPLEX",
			envValue:     "1h30m45s",
			defaultValue: 1 * time.Hour,
			expected:     1*time.Hour + 30*time.Minute + 45*time.Second,
		},
		{
			name:         "returns default when env is invalid duration",
			envKey:       "TEST_DURATION_INVALID",
			envValue:     "not-a-duration",
			defaultValue: 10 * time.Minute,
			expected:     10 * time.Minute,
		},
		{
			name:         "returns default when env is number without unit",
			envKey:       "TEST_DURATION_NO_UNIT",
			envValue:     "100",
			defaultValue: 10 * time.Minute,
			expected:     10 * time.Minute,
		},
		{
			name:         "returns default when env is empty",
			envKey:       "TEST_DURATION_EMPTY",
			envValue:     "",
			defaultValue: 15 * time.Minute,
			expected:     15 * time.Minute,
		},
		{
			name:         "returns default when env is not set",
			envKey:       "TEST_DURATION_UNSET",
			envValue:     "",
			defaultValue: 20 * time.Minute,
			expected:     20 * time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			if tt.envValue != "" {
				os.Setenv(tt.envKey, tt.envValue)
				defer os.Unsetenv(tt.envKey)
			}

			// Execute
			result := GetDuration(tt.envKey, tt.defaultValue)

			// Assert
			if result != tt.expected {
				t.Errorf("GetDuration() = %v, want %v", result, tt.expected)
			}
		})
	}
}

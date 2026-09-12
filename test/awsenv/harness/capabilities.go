//go:build e2e || awsenv || kind

package harness

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// Capability names the exact operation set a lifecycle test requires.
// Service presence is not sufficient: the environment must implement these.
type Capability string

const (
	CapabilityEC2Lifecycle       Capability = "ec2.lifecycle"
	CapabilityRDSLifecycle       Capability = "rds.instance-lifecycle"
	CapabilityRDSSnapshot        Capability = "rds.instance-snapshot"
	CapabilityEKSNodeGroupUpdate Capability = "eks.nodegroup-update"
)

// unsupportedCodeMarkers are backend "operation does not exist" codes.
// OperationNotSupported is included alongside the plan's original four
// because emulators surface it for unimplemented management APIs; it matches
// only explicit unsupported-operation codes, never state or auth failures.
var unsupportedCodeMarkers = []string{
	"UnknownOperationException",
	"InvalidAction",
	"NotImplemented",
	"UnsupportedOperation",
	"OperationNotSupported",
}

var unsupportedMessageMarkers = []string{
	"not implemented",
	"not supported",
	"unknown operation",
	"unknown action",
}

// IsUnsupported reports whether err is an explicit unknown-operation signal.
// Conservative by design: access, state, validation, timeout, connection, and
// server errors are never classified as unsupported.
func IsUnsupported(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, marker := range unsupportedCodeMarkers {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	lowered := strings.ToLower(msg)
	for _, marker := range unsupportedMessageMarkers {
		if strings.Contains(lowered, marker) {
			return true
		}
	}
	return false
}

// RequireNoErrorOrSkip continues on nil, skips with exact capability evidence
// when the environment reports the operation unsupported, and fails otherwise.
func RequireNoErrorOrSkip(t *testing.T, capability Capability, operation string, err error) {
	t.Helper()
	if err == nil {
		return
	}
	if IsUnsupported(err) || isEmulatorFallthrough(operation, err) {
		provider, _ := resolveProvider()
		t.Skipf("provider %q lacks %s (%s): %v", provider, capability, operation, err)
	}
	t.Fatalf("operation %s failed: %v", operation, err)
}

// isEmulatorFallthrough reports whether err looks like a request that reached
// a non-JSON handler instead of the intended operation. Observed case: the
// emulator has no UpdateNodegroupConfig route, so the request falls through
// to the S3 handler returning XML and the Go SDK fails JSON deserialization.
// Scoped to that single operation on purpose: the same byte shape from any
// other operation is a genuine failure and must not skip. Callers can rely on
// STS readiness plus green provisioning having preceded lifecycle calls, and
// skips print the full error text.
func isEmulatorFallthrough(operation string, err error) bool {
	if operation != "UpdateNodegroupConfig" || err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "deserialization failed") &&
		strings.Contains(strings.ToLower(msg), "invalid character '<'")
}

// resolveProvider selects the provisioning backend. Only "floci" exists;
// anything else is a hard error so a misconfigured real-AWS run can never
// silently execute against the wrong environment.
func resolveProvider() (string, error) {
	provider := os.Getenv("AWSENV_PROVIDER")
	if provider == "" {
		endpoint := os.Getenv("AWSENV_ENDPOINT_URL")
		if endpoint != "" && endpoint != defaultEndpoint {
			return "", fmt.Errorf("AWSENV_ENDPOINT_URL %q is non-default but AWSENV_PROVIDER is unset: set AWSENV_PROVIDER explicitly", endpoint)
		}
		return "floci", nil
	}
	if provider != "floci" {
		return "", fmt.Errorf("unsupported AWSENV_PROVIDER %q: only \"floci\" is implemented", provider)
	}
	return provider, nil
}

/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package v1alpha1

// ExecutorVerification is the method used to verify executor access.
type ExecutorVerification string

const (
	// VerificationPolicySimulation verifies access through IAM policy simulation.
	VerificationPolicySimulation ExecutorVerification = "PolicySimulation"
	// VerificationAccessReview verifies access through Kubernetes SelfSubjectAccessReview.
	VerificationAccessReview ExecutorVerification = "AccessReview"
)

// Connector condition types.
const (
	ConditionReady                  = "Ready"
	ConditionAuthValid              = "AuthValid"
	ConditionExecutorAccessVerified = "ExecutorAccessVerified"
)

// Connector condition reasons.
const (
	ReasonProbeFailed           = "ProbeFailed"
	ReasonSecretNotFound        = "SecretNotFound"
	ReasonInvalidCredentials    = "InvalidCredentials"
	ReasonAccountMismatch       = "AccountMismatch"
	ReasonClusterUnreachable    = "ClusterUnreachable"
	ReasonProviderNotReady      = "ProviderNotReady"
	ReasonAccessDenied          = "AccessDenied"
	ReasonSimulationUnavailable = "SimulationUnavailable"
	ReasonProbeError            = "ProbeError"
	ReasonAuthFailed            = "AuthFailed"
	ReasonReady                 = "Ready"
	ReasonNoExecutorsToVerify   = "NoExecutorsToVerify"
	ReasonUnverified            = "Unverified"
	ReasonProbeUnsupported      = "ProbeUnsupported"
)

// ExecutorAccess reports whether the connector can run an executor type.
type ExecutorAccess struct {
	// Type is the executor type.
	Type string `json:"type"`

	// Allowed indicates whether the connector has the permissions the executor needs.
	Allowed bool `json:"allowed"`

	// Reason is a machine-readable reason when access is not allowed.
	// +optional
	Reason string `json:"reason,omitempty"`

	// Message provides human-readable details.
	// +optional
	Message string `json:"message,omitempty"`

	// Verification is the method used to verify access.
	// +optional
	Verification ExecutorVerification `json:"verification,omitempty"`
}

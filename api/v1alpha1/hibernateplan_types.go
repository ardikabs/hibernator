/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ExecutionStrategyType defines the execution strategy.
// +kubebuilder:validation:Enum=Sequential;Parallel;DAG;Staged
type ExecutionStrategyType string

const (
	StrategySequential ExecutionStrategyType = "Sequential"
	StrategyParallel   ExecutionStrategyType = "Parallel"
	StrategyDAG        ExecutionStrategyType = "DAG"
	StrategyStaged     ExecutionStrategyType = "Staged"
)

// BehaviorMode defines execution behavior.
// +kubebuilder:validation:Enum=Strict;BestEffort
type BehaviorMode string

const (
	BehaviorStrict     BehaviorMode = "Strict"
	BehaviorBestEffort BehaviorMode = "BestEffort"
)

// PlanPhase represents the overall phase of the HibernatePlan.
// +kubebuilder:validation:Enum=Pending;Active;Hibernating;Hibernated;WakingUp;Error
type PlanPhase string

const (
	PhasePending     PlanPhase = "Pending"
	PhaseActive      PlanPhase = "Active"
	PhaseHibernating PlanPhase = "Hibernating"
	PhaseHibernated  PlanPhase = "Hibernated"
	PhaseWakingUp    PlanPhase = "WakingUp"
	PhaseError       PlanPhase = "Error"
)

// ExecutionState represents per-target execution state.
// +kubebuilder:validation:Enum=Pending;Running;Completed;Failed
type ExecutionState string

const (
	StatePending   ExecutionState = "Pending"
	StateRunning   ExecutionState = "Running"
	StateCompleted ExecutionState = "Completed"
	StateFailed    ExecutionState = "Failed"
)

// OffHourWindow defines a time window for hibernation.
type OffHourWindow struct {
	// Start time in HH:MM format (e.g., "20:00").
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^([0-1]?[0-9]|2[0-3]):[0-5][0-9]$`
	Start string `json:"start"`

	// End time in HH:MM format (e.g., "06:00").
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^([0-1]?[0-9]|2[0-3]):[0-5][0-9]$`
	End string `json:"end"`

	// DaysOfWeek specifies which days this window applies to.
	// Valid values: MON, TUE, WED, THU, FRI, SAT, SUN
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:items:Enum=MON;TUE;WED;THU;FRI;SAT;SUN
	DaysOfWeek []string `json:"daysOfWeek"`
}

// Schedule defines the hibernation schedule.
type Schedule struct {
	// Timezone for schedule evaluation (e.g., "Asia/Jakarta").
	// +kubebuilder:validation:Required
	Timezone string `json:"timezone"`

	// OffHours defines when hibernation should occur.
	// +kubebuilder:validation:MinItems=1
	OffHours []OffHourWindow `json:"offHours"`
}

// Dependency represents a DAG edge (from -> to).
type Dependency struct {
	// From is the source target name.
	From string `json:"from"`
	// To is the destination target name that depends on From.
	To string `json:"to"`
}

// Stage defines a group of targets to execute together.
type Stage struct {
	// Name of the stage.
	Name string `json:"name"`

	// Parallel indicates if targets in this stage run in parallel.
	// +kubebuilder:default=false
	Parallel bool `json:"parallel,omitempty"`

	// MaxConcurrency limits parallelism within this stage.
	// +kubebuilder:validation:Minimum=1
	// +optional
	MaxConcurrency *int32 `json:"maxConcurrency,omitempty"`

	// Targets are the names of targets in this stage.
	Targets []string `json:"targets"`
}

// ExecutionStrategy defines how targets are executed.
type ExecutionStrategy struct {
	// Type of execution strategy.
	// +kubebuilder:validation:Required
	Type ExecutionStrategyType `json:"type"`

	// MaxConcurrency limits concurrent executions (for Parallel/DAG/Staged).
	// +kubebuilder:validation:Minimum=1
	// +optional
	MaxConcurrency *int32 `json:"maxConcurrency,omitempty"`

	// Dependencies define DAG edges (only valid when Type=DAG).
	// +optional
	Dependencies []Dependency `json:"dependencies,omitempty"`

	// Stages define execution groups (only valid when Type=Staged).
	// +optional
	Stages []Stage `json:"stages,omitempty"`
}

// Execution holds strategy configuration.
type Execution struct {
	Strategy ExecutionStrategy `json:"strategy"`
}

// Behavior defines execution behavior.
type Behavior struct {
	// Mode determines how failures are handled.
	// +kubebuilder:default=Strict
	Mode BehaviorMode `json:"mode,omitempty"`

	// FailFast stops execution on first failure.
	// +kubebuilder:default=true
	FailFast bool `json:"failFast,omitempty"`

	// Retries is the maximum number of retry attempts for failed operations.
	// +kubebuilder:default=3
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=10
	// +optional
	Retries int32 `json:"retries,omitempty"`
}

// ConnectorRef references a connector resource.
type ConnectorRef struct {
	// Kind of the connector (CloudProvider or K8SCluster).
	// +kubebuilder:validation:Enum=CloudProvider;K8SCluster
	Kind string `json:"kind"`

	// Name of the connector resource.
	Name string `json:"name"`

	// Namespace of the connector resource (defaults to plan namespace).
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// Target defines a hibernation target.
type Target struct {
	// Name is the unique identifier for this target within the plan.
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Type of the target (e.g., eks, rds, ec2).
	// +kubebuilder:validation:Required
	Type string `json:"type"`

	// ConnectorRef references the connector for this target.
	// +kubebuilder:validation:Required
	ConnectorRef ConnectorRef `json:"connectorRef"`

	// Parameters are executor-specific configuration.
	// +kubebuilder:pruning:PreserveUnknownFields
	// +optional
	Parameters *Parameters `json:"parameters,omitempty"`
}

// Parameters is an opaque container for executor-specific config.
// +kubebuilder:pruning:PreserveUnknownFields
type Parameters struct {
	// Raw holds the JSON-encoded parameters.
	Raw []byte `json:"-"`
}

// HibernatePlanSpec defines the desired state of HibernatePlan.
type HibernatePlanSpec struct {
	// Schedule defines when hibernation occurs.
	// +kubebuilder:validation:Required
	Schedule Schedule `json:"schedule"`

	// Execution defines the execution strategy.
	// +kubebuilder:validation:Required
	Execution Execution `json:"execution"`

	// Behavior defines how failures are handled.
	// +optional
	Behavior Behavior `json:"behavior,omitempty"`

	// Targets are the resources to hibernate.
	// +kubebuilder:validation:MinItems=1
	Targets []Target `json:"targets"`
}

// ExecutionStatus represents per-target execution status.
type ExecutionStatus struct {
	// Target identifier (type/name).
	Target string `json:"target"`

	// Executor used for this target.
	Executor string `json:"executor,omitempty"`

	// State of execution.
	State ExecutionState `json:"state"`

	// StartedAt is when execution started.
	// +optional
	StartedAt *metav1.Time `json:"startedAt,omitempty"`

	// FinishedAt is when execution finished.
	// +optional
	FinishedAt *metav1.Time `json:"finishedAt,omitempty"`

	// Attempts is the number of execution attempts.
	Attempts int32 `json:"attempts,omitempty"`

	// Message provides human-readable status.
	// +optional
	Message string `json:"message,omitempty"`

	// JobRef is the namespace/name of the runner Job.
	// +optional
	JobRef string `json:"jobRef,omitempty"`

	// LogsRef is the reference to logs (stream id or object path).
	// +optional
	LogsRef string `json:"logsRef,omitempty"`

	// RestoreRef is the reference to restore metadata artifact.
	// +optional
	RestoreRef string `json:"restoreRef,omitempty"`

	// ServiceAccountRef is the namespace/name of ephemeral SA.
	// +optional
	ServiceAccountRef string `json:"serviceAccountRef,omitempty"`

	// ConnectorSecretRef is the namespace/name of connector secret.
	// +optional
	ConnectorSecretRef string `json:"connectorSecretRef,omitempty"`

	// RestoreConfigMapRef is the namespace/name of restore hints ConfigMap.
	// +optional
	RestoreConfigMapRef string `json:"restoreConfigMapRef,omitempty"`
}

// HibernatePlanStatus defines the observed state of HibernatePlan.
type HibernatePlanStatus struct {
	// Phase is the overall plan phase.
	Phase PlanPhase `json:"phase,omitempty"`

	// LastTransitionTime is when the phase last changed.
	// +optional
	LastTransitionTime *metav1.Time `json:"lastTransitionTime,omitempty"`

	// Executions is the per-target execution ledger.
	// +optional
	Executions []ExecutionStatus `json:"executions,omitempty"`

	// ObservedGeneration is the last observed generation.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// RetryCount tracks the number of retry attempts for error recovery.
	// +optional
	RetryCount int32 `json:"retryCount,omitempty"`

	// LastRetryTime is when the last retry attempt was made.
	// +optional
	LastRetryTime *metav1.Time `json:"lastRetryTime,omitempty"`

	// ErrorMessage provides details about the error that caused PhaseError.
	// +optional
	ErrorMessage string `json:"errorMessage,omitempty"`

	// ActiveExceptions is the history of schedule exceptions for this plan.
	// Maximum 10 entries, with expired exceptions pruned first.
	// +optional
	ActiveExceptions []ExceptionReference `json:"activeExceptions,omitempty"`
}

// ExceptionReference tracks an exception in the plan's history.
type ExceptionReference struct {
	// Name of the ScheduleException.
	Name string `json:"name"`

	// Type of the exception (extend, suspend, replace).
	Type ExceptionType `json:"type"`

	// ValidFrom is when the exception period starts.
	ValidFrom metav1.Time `json:"validFrom"`

	// ValidUntil is when the exception period ends.
	ValidUntil metav1.Time `json:"validUntil"`

	// State is the current state of the exception.
	State ExceptionState `json:"state"`

	// AppliedAt is when the exception was first applied.
	// +optional
	AppliedAt *metav1.Time `json:"appliedAt,omitempty"`

	// ExpiredAt is when the exception transitioned to Expired.
	// +optional
	ExpiredAt *metav1.Time `json:"expiredAt,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// HibernatePlan is the Schema for the hibernateplans API.
type HibernatePlan struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HibernatePlanSpec   `json:"spec,omitempty"`
	Status HibernatePlanStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HibernatePlanList contains a list of HibernatePlan.
type HibernatePlanList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HibernatePlan `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HibernatePlan{}, &HibernatePlanList{})
}

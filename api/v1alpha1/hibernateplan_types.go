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
// +kubebuilder:validation:Enum=Pending;Active;Hibernating;Hibernated;WakingUp;Suspended;Error
type PlanPhase string

const (
	PhasePending     PlanPhase = "Pending"
	PhaseActive      PlanPhase = "Active"
	PhaseHibernating PlanPhase = "Hibernating"
	PhaseHibernated  PlanPhase = "Hibernated"
	PhaseWakingUp    PlanPhase = "WakingUp"
	PhaseSuspended   PlanPhase = "Suspended"
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

// MarshalJSON implements json.Marshaler for Parameters.
// Note: This method is only called when p is non-nil. Nil pointers with omitempty
// are omitted entirely, and nil pointers without omitempty output "null" directly.
func (p *Parameters) MarshalJSON() ([]byte, error) {
	if len(p.Raw) == 0 {
		return []byte("{}"), nil
	}
	return p.Raw, nil
}

// UnmarshalJSON implements json.Unmarshaler for Parameters.
func (p *Parameters) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		p.Raw = nil
		return nil
	}
	p.Raw = make([]byte, len(data))
	copy(p.Raw, data)
	return nil
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

	// Suspend temporarily disables hibernation operations without deleting the plan.
	// When set to true, the plan transitions to Suspended phase and stops all execution.
	// When set to false, the plan transitions back to Active phase and resumes schedule evaluation.
	// Running jobs complete naturally but no new jobs are created while suspended.
	// +optional
	Suspend bool `json:"suspend,omitempty"`

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

// ExecutionOperationSummary summarizes the results of a shutdown or wakeup operation.
type ExecutionOperationSummary struct {
	// Operation is the operation type (shutdown or wakeup).
	Operation string `json:"operation"`

	// StartTime is when the operation started.
	StartTime metav1.Time `json:"startTime"`

	// EndTime is when the operation completed.
	// +optional
	EndTime *metav1.Time `json:"endTime,omitempty"`

	// TargetResults summarizes the result for each target.
	// +optional
	TargetResults []TargetExecutionResult `json:"targetResults,omitempty"`

	// Success indicates if all targets completed successfully.
	Success bool `json:"success"`

	// ErrorMessage contains error details if the operation failed.
	// +optional
	ErrorMessage string `json:"errorMessage,omitempty"`
}

// TargetExecutionResult is the result of a single target execution.
type TargetExecutionResult struct {
	// Target is the target identifier (type/name).
	Target string `json:"target"`
	// State is the final execution state (Completed or Failed).
	State ExecutionState `json:"state"`
	// Attempts is the number of attempts made.
	Attempts int32 `json:"attempts"`
	// ExecutionID is the unique identifier for this target execution.
	// +optional
	ExecutionID string `json:"executionId,omitempty"`
}

// ExecutionCycle groups a shutdown and corresponding wakeup operation.
type ExecutionCycle struct {
	// CycleID is a unique identifier for this cycle.
	CycleID string `json:"cycleId"`

	// ShutdownExecution summarizes the shutdown operation.
	// +optional
	ShutdownExecution *ExecutionOperationSummary `json:"shutdownExecution,omitempty"`

	// WakeupExecution summarizes the wakeup operation.
	// +optional
	WakeupExecution *ExecutionOperationSummary `json:"wakeupExecution,omitempty"`
}

// HibernatePlanStatus defines the observed state of HibernatePlan.
type HibernatePlanStatus struct {
	// CurrentCycleID is the current hibernation cycle identifier.
	CurrentCycleID string `json:"currentCycleID,omitempty"`

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

	// CurrentStageIndex tracks which stage is currently executing (0-based).
	// Reset to 0 when starting new hibernation/wakeup cycle.
	// +optional
	CurrentStageIndex int `json:"currentStageIndex,omitempty"`

	// CurrentOperation tracks the current operation type (shutdown or wakeup).
	// Used to determine which phase to transition to when stages complete.
	// +optional
	CurrentOperation string `json:"currentOperation,omitempty"`

	// ExecutionHistory records historical execution cycles (max 5).
	// Each cycle contains shutdown and wakeup operation summaries.
	// Oldest cycles are pruned when limit is exceeded.
	// +optional
	ExecutionHistory []ExecutionCycle `json:"executionHistory,omitempty"`
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

// ExceptionReferencesEqual checks if two exception reference slices are equal.
func ExceptionReferencesEqual(a, b []ExceptionReference) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		// Fast check: compare Name first
		if a[i].Name != b[i].Name {
			return false
		}
		// Only compare other fields if name matches
		if a[i].Type != b[i].Type ||
			a[i].State != b[i].State ||
			!a[i].ValidFrom.Equal(&b[i].ValidFrom) ||
			!a[i].ValidUntil.Equal(&b[i].ValidUntil) {
			return false
		}
		// Compare AppliedAt
		if (a[i].AppliedAt == nil) != (b[i].AppliedAt == nil) {
			return false
		}
		if a[i].AppliedAt != nil && !a[i].AppliedAt.Equal(b[i].AppliedAt) {
			return false
		}
		// Compare ExpiredAt
		if (a[i].ExpiredAt == nil) != (b[i].ExpiredAt == nil) {
			return false
		}
		if a[i].ExpiredAt != nil && !a[i].ExpiredAt.Equal(b[i].ExpiredAt) {
			return false
		}
	}
	return true
}

func init() {
	SchemeBuilder.Register(&HibernatePlan{}, &HibernatePlanList{})
}

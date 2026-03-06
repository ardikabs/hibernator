package wellknown

import "time"

const (
	// RequeueIntervalDuringStage is the requeue interval during stage execution.
	RequeueIntervalDuringStage = 5 * time.Second

	// RequeueIntervalOnExecution is the requeue interval during execution reconciliation.
	RequeueIntervalOnExecution = 10 * time.Second

	// RequeueIntervalOnScheduleError is the requeue interval when schedule evaluation fails.
	RequeueIntervalOnScheduleError = 3 * time.Minute

	// RequeueIntervalForScheduleException is the default requeue interval for exception reconciliation.
	RequeueIntervalForScheduleException = 1 * time.Minute

	// RequeueIntervalOnRecoveryError is the requeue interval when an error occurs during recovery.
	RequeueIntervalOnRecoveryError = 1 * time.Minute

	// DeadlineTransitionToSuspended is the deadline duration for transitioning to suspended state when in-flight executions are present.
	DeadlineTransitionToSuspended = 30 * time.Minute
)

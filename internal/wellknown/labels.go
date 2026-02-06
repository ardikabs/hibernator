package wellknown

const (
	// LabelPlan is the label key for the plan name.
	LabelPlan = "hibernator.ardikabs.com/plan"

	// LabelTarget is the label key for the target name.
	LabelTarget = "hibernator.ardikabs.com/target"

	// LabelExecutionID is the label key for the execution ID.
	LabelExecutionID = "hibernator.ardikabs.com/execution-id"

	// LabelOperation is the label key for the operation type (shutdown or wakeup).
	LabelOperation = "hibernator.ardikabs.com/operation"

	// LabelCycleID is the label key for the cycle ID (isolates jobs by cycle).
	LabelCycleID = "hibernator.ardikabs.com/cycle-id"

	// LabelStaleRunnerJob is the label key to mark stale runner jobs.
	LabelStaleRunnerJob = "hibernator.ardikabs.com/stale"

	// LabelStaleReasonRunnerJob is the label key to mark the reason why a runner job is stale.
	LabelStaleReasonRunnerJob = "hibernator.ardikabs.com/stale-reason"

	// LabelException is the label key for the exception name.
	LabelException = "hibernator.ardikabs.com/exception"
)

package wellknown

const (
	// LabelPlan is the label key for the plan name.
	// NOTE: Same key as AnnotationPlan — intentional. Both carry the plan name on
	// the same object (labels for selectors, annotations for metadata).
	LabelPlan = "hibernator.ardikabs.com/plan"

	// LabelTarget is the label key for the target name.
	LabelTarget = "hibernator.ardikabs.com/target"

	// LabelExecutor is the label key for the executor type.
	LabelExecutor = "hibernator.ardikabs.com/executor"

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

	// LabelConnectorKind is the label key for the connector kind of a probe Job.
	LabelConnectorKind = "hibernator.ardikabs.com/connector-kind"

	// LabelConnectorName is the label key for the (label-safe) connector name of a probe Job.
	LabelConnectorName = "hibernator.ardikabs.com/connector-name"

	// LabelConnectorNamespace is the label key for the (label-safe) connector namespace of a probe Job.
	LabelConnectorNamespace = "hibernator.ardikabs.com/connector-namespace"

	// LabelConnectorProbe marks a Job as a connector probe Job.
	LabelConnectorProbe = "hibernator.ardikabs.com/connector-probe"
)

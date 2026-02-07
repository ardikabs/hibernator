package wellknown

const (
	// AnnotationPlan is the annotation for plan name.
	AnnotationPlan = "hibernator/plan"

	// AnnotationTarget is the annotation for target name.
	AnnotationTarget = "hibernator/target"

	// AnnotationSuspendedAtPhase is the annotation for the plan phase at suspension time.
	AnnotationSuspendedAtPhase = "hibernator.ardikabs.com/suspended-at-phase"

	// AnnotationExceptionTrigger is the annotation key used to trigger plan reconciliation
	// when an exception changes. The value is the timestamp of the last exception change.
	AnnotationExceptionTrigger = "hibernator.ardikabs.com/exception-trigger"

	// AnnotationPreviousRestoreState is the annotation key for previous restore state snapshot.
	AnnotationPreviousRestoreState = "hibernator.ardikabs.com/restore-previous-state"

	// AnnotationRestoredPrefix is the prefix for per-target restoration tracking annotations.
	AnnotationRestoredPrefix = "hibernator.ardikabs.com/restored-"
)

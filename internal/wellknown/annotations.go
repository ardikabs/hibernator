package wellknown

const (
	// AnnotationPlan is the annotation for plan name.
	AnnotationPlan = "hibernator/plan"

	// AnnotationTarget is the annotation for target name.
	AnnotationTarget = "hibernator/target"

	// AnnotationSuspendedAtPhase is the annotation for the plan phase at suspension time.
	AnnotationSuspendedAtPhase = "hibernator.ardikabs.com/suspended-at-phase"

	// AnnotationPreviousRestoreState is the annotation key for previous restore state snapshot.
	AnnotationPreviousRestoreState = "hibernator.ardikabs.com/restore-previous-state"

	// AnnotationRestoredPrefix is the prefix for per-target restoration tracking annotations.
	AnnotationRestoredPrefix = "hibernator.ardikabs.com/restored-"

	// AnnotationRetryNow is the annotation key used to trigger a manual retry of a failed plan.
	AnnotationRetryNow = "hibernator.ardikabs.com/retry-now"

	// AnnotationSuspendUntil is the annotation key for the deadline when auto-resume should occur.
	// Value format: RFC3339 timestamp (e.g., "2026-01-15T06:00:00Z").
	AnnotationSuspendUntil = "hibernator.ardikabs.com/suspend-until"

	// AnnotationSuspendReason is the annotation key for recording the reason for suspension.
	AnnotationSuspendReason = "hibernator.ardikabs.com/suspend-reason"
)

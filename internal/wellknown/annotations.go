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

	// AnnotationRetryAt is an internal annotation set by the error recovery processor to schedule
	// a precise retry requeue. Value is an RFC3339 timestamp. Removed after the retry executes.
	AnnotationRetryAt = "hibernator.ardikabs.com/retry-at"

	// AnnotationSuspendUntil is the annotation key for the deadline when auto-resume should occur.
	// Value format: RFC3339 timestamp (e.g., "2026-01-15T06:00:00Z").
	AnnotationSuspendUntil = "hibernator.ardikabs.com/suspend-until"

	// AnnotationSuspendReason is the annotation key for recording the reason for suspension.
	AnnotationSuspendReason = "hibernator.ardikabs.com/suspend-reason"

	// AnnotationForceAction is the annotation key for manually overriding schedule-driven phase
	// transitions. While present, the schedule evaluator result is ignored and the annotated
	// direction is applied instead. The annotation is persistent — the controller never removes
	// it; the user is responsible for deleting it to restore normal schedule control.
	//
	// Valid values: ForceActionHibernate, ForceActionWakeup.
	//
	// Priority: Spec.Suspend=true always takes precedence over this annotation.
	AnnotationForceAction = "hibernator.ardikabs.com/force-action"
)

// Force-action annotation values for AnnotationForceAction.
const (
	// ForceActionHibernate forces the plan to hibernate regardless of schedule.
	ForceActionHibernate = "hibernate"

	// ForceActionWakeup forces the plan to wake up regardless of schedule.
	ForceActionWakeup = "wakeup"
)

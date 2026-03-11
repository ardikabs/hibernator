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

	// AnnotationOverrideAction is the annotation key that enables manual phase override mode.
	// While set to "true", schedule-driven phase transitions are suppressed and the direction
	// specified by AnnotationOverridePhaseTarget is applied instead.
	//
	// The annotation is persistent — the controller never removes it; the user is responsible
	// for deleting it (along with AnnotationOverridePhaseTarget) to restore normal schedule
	// control.
	//
	// Must be accompanied by AnnotationOverridePhaseTarget to specify the direction.
	//
	// Priority: Spec.Suspend=true always takes precedence over this annotation.
	AnnotationOverrideAction = "hibernator.ardikabs.com/override-action"

	// AnnotationOverridePhaseTarget is the companion to AnnotationOverrideAction.
	// It specifies which phase the override should target.
	//
	// Valid values: OverridePhaseTargetHibernate, OverridePhaseTargetWakeup.
	AnnotationOverridePhaseTarget = "hibernator.ardikabs.com/override-phase-target"

	// AnnotationRestart is a one-shot annotation that re-triggers the last executor operation
	// as recorded in .Status.CurrentOperation, even when the plan is already at a stable
	// resting phase (which would normally be a no-op).
	//
	// The controller consumes (deletes) this annotation in a single atomic patch before
	// re-executing the operation, so it is safe to use without causing loops.
	//
	// Works standalone (without AnnotationOverrideAction) or alongside it — when both are
	// present, AnnotationOverrideAction captures the tick and handles restart internally.
	//
	// Value: must be "true" — any other value is treated as absent.
	//
	//   # Re-run wakeup executor while plan is already Active
	//   kubectl annotate hibernateplan <name> hibernator.ardikabs.com/restart=true
	//
	//   # Re-run hibernation executor while plan is already Hibernated
	//   kubectl annotate hibernateplan <name> hibernator.ardikabs.com/restart=true
	AnnotationRestart = "hibernator.ardikabs.com/restart"
)

// Override phase target values for AnnotationOverridePhaseTarget.
const (
	// OverridePhaseTargetHibernate targets the Hibernated phase (forces plan to hibernate).
	OverridePhaseTargetHibernate = "hibernate"

	// OverridePhaseTargetWakeup targets the Active phase (forces plan to wake up).
	OverridePhaseTargetWakeup = "wakeup"
)

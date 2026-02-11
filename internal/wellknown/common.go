package wellknown

const (
	// PlanFinalizerName is the finalizer for HibernatePlan resources.
	PlanFinalizerName = "hibernator.ardikabs.com/finalizer"

	// ExceptionFinalizerName is the finalizer for ScheduleException resources.
	ExceptionFinalizerName = "hibernator.ardikabs.com/exception-finalizer"

	// RunnerImage is the default runner image.
	RunnerImage = "ghcr.io/ardikabs/hibernator-runner:latest"

	// StreamTokenAudience is the audience for projected SA tokens.
	StreamTokenAudience = "hibernator-control-plane"

	// StreamTokenExpirationSeconds is the token expiration time.
	StreamTokenExpirationSeconds = 600

	// DefaultJobTTLSeconds is the TTL for completed runner jobs (1 hour).
	DefaultJobTTLSeconds = 3600

	// DefaultJobBackoffLimit is the maximum retries for runner jobs.
	DefaultJobBackoffLimit = 3

	// DefaultRecoveryMaxRetryAttempts is the default max retry attempts for recovery.
	DefaultRecoveryMaxRetryAttempts = int32(3)

	// TerminationLogPath is the path where the runner writes its termination message.
	// Ref: https://kubernetes.io/docs/tasks/debug/debug-application/determine-reason-pod-failure/#customizing-the-termination-message
	TerminationLogPath = "/dev/termination-log"
)

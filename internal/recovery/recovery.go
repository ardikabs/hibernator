/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package recovery

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/smithy-go"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/wellknown"
)

// ErrorClassification categorizes errors for recovery decisions.
type ErrorClassification string

const (
	ErrorTransient ErrorClassification = "Transient"
	ErrorPermanent ErrorClassification = "Permanent"
	ErrorUnknown   ErrorClassification = "Unknown"
)

// ErrorRecoveryStrategy determines how to handle errors.
type ErrorRecoveryStrategy struct {
	ShouldRetry    bool
	RetryAfter     time.Duration
	Classification ErrorClassification
	Reason         string
}

// transientAWSErrorCodes contains AWS error codes that indicate transient failures.
var transientAWSErrorCodes = map[string]bool{
	"Throttling":                             true,
	"RequestLimitExceeded":                   true,
	"ServiceUnavailable":                     true,
	"InternalError":                          true,
	"RequestTimeout":                         true,
	"ProvisionedThroughputExceededException": true,
}

// permanentAWSErrorCodes contains AWS error codes that indicate permanent failures.
var permanentAWSErrorCodes = map[string]bool{
	"ResourceNotFoundException":      true,
	"ValidationException":            true,
	"InvalidParameterException":      true,
	"AccessDeniedException":          true,
	"UnauthorizedException":          true,
	"ResourceAlreadyExistsException": true,
}

// ClassifyError determines if an error is transient or permanent.
// It first checks for AWS SDK typed errors, then falls back to string matching.
func ClassifyError(err error) ErrorClassification {
	if err == nil {
		return ErrorUnknown
	}

	// Check for AWS API errors using smithy-go interface
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		code := apiErr.ErrorCode()
		if transientAWSErrorCodes[code] {
			return ErrorTransient
		}
		if permanentAWSErrorCodes[code] {
			return ErrorPermanent
		}
	}

	// Fallback to string matching for non-AWS errors
	errMsg := strings.ToLower(err.Error())

	transientPatterns := []string{
		"timeout",
		"connection refused",
		"temporary failure",
		"rate limit",
		"throttling",
		"service unavailable",
		"too many requests",
		"deadline exceeded",
	}

	for _, pattern := range transientPatterns {
		if strings.Contains(errMsg, pattern) {
			return ErrorTransient
		}
	}

	permanentPatterns := []string{
		"not found", "already exists", "invalid",
		"forbidden", "unauthorized", "permission denied",
	}

	for _, pattern := range permanentPatterns {
		if strings.Contains(errMsg, pattern) {
			return ErrorPermanent
		}
	}

	return ErrorUnknown
}

// DetermineRecoveryStrategy decides if and when to retry based on plan state.
func DetermineRecoveryStrategy(plan *hibernatorv1alpha1.HibernatePlan, err error) ErrorRecoveryStrategy {
	classification := ClassifyError(err)

	maxRetries := wellknown.DefaultRecoveryMaxRetryAttempts
	if plan.Spec.Behavior.Retries > 0 {
		maxRetries = plan.Spec.Behavior.Retries
	}

	if plan.Status.RetryCount >= maxRetries {
		return ErrorRecoveryStrategy{
			ShouldRetry:    false,
			Classification: classification,
			Reason:         fmt.Sprintf("max retries (%d) exceeded", maxRetries),
		}
	}

	if classification == ErrorPermanent {
		return ErrorRecoveryStrategy{
			ShouldRetry:    false,
			Classification: classification,
			Reason:         "error classified as permanent",
		}
	}

	backoff := CalculateBackoff(plan.Status.RetryCount)

	if plan.Status.LastRetryTime != nil {
		elapsed := time.Since(plan.Status.LastRetryTime.Time)
		if elapsed < backoff {
			return ErrorRecoveryStrategy{
				ShouldRetry:    true,
				RetryAfter:     backoff - elapsed,
				Classification: classification,
				Reason:         fmt.Sprintf("waiting for backoff (attempt %d/%d)", plan.Status.RetryCount+1, maxRetries),
			}
		}
	}

	return ErrorRecoveryStrategy{
		ShouldRetry:    true,
		RetryAfter:     0,
		Classification: classification,
		Reason:         fmt.Sprintf("retrying (attempt %d/%d)", plan.Status.RetryCount+1, maxRetries),
	}
}

// CalculateBackoff returns exponential backoff: min(60s * 2^attempt, 30m)
func CalculateBackoff(attempt int32) time.Duration {
	base := 60 * time.Second
	maxBackoff := 30 * time.Minute

	if attempt < 0 {
		attempt = 0
	}

	multiplier := int64(1)
	for i := int32(0); i < attempt; i++ {
		multiplier *= 2
		if time.Duration(multiplier)*base >= maxBackoff {
			return maxBackoff
		}
	}

	backoff := time.Duration(multiplier) * base
	if backoff > maxBackoff {
		return maxBackoff
	}
	return backoff
}

// RecordRetryAttempt updates the plan status for a retry attempt.
func RecordRetryAttempt(plan *hibernatorv1alpha1.HibernatePlan, err error) {
	plan.Status.RetryCount++
	plan.Status.LastRetryTime = ptr.To(metav1.Now())

	if err != nil {
		plan.Status.ErrorMessage = err.Error()
	} else {
		plan.Status.ErrorMessage = "unknown error"
	}
}

// ResetRetryState clears retry tracking when transitioning out of error state.
func ResetRetryState(plan *hibernatorv1alpha1.HibernatePlan) {
	plan.Status.RetryCount = 0
	plan.Status.LastRetryTime = nil
	plan.Status.ErrorMessage = ""
}

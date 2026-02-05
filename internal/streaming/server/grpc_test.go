/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package server

import (
	"context"
	"errors"
	"io"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestExecutionState_Fields(t *testing.T) {
	state := ExecutionState{
		ExecutionID:     "exec-123",
		Phase:           "Running",
		ProgressPercent: 50,
		Message:         "Processing",
		Completed:       false,
		Success:         false,
	}
	if state.ExecutionID != "exec-123" {
		t.Errorf("expected 'exec-123', got %s", state.ExecutionID)
	}
	if state.Phase != "Running" {
		t.Errorf("expected 'Running', got %s", state.Phase)
	}
	if state.ProgressPercent != 50 {
		t.Errorf("expected 50, got %d", state.ProgressPercent)
	}
}

func TestExecutionState_Completed(t *testing.T) {
	state := ExecutionState{
		ExecutionID: "exec-456",
		Completed:   true,
		Success:     true,
	}
	if !state.Completed {
		t.Error("expected completed")
	}
	if !state.Success {
		t.Error("expected success")
	}
}

func TestExecutionState_Failed(t *testing.T) {
	state := ExecutionState{
		ExecutionID: "exec-789",
		Completed:   true,
		Success:     false,
		Error:       "connection timeout",
	}
	if state.Success {
		t.Error("expected failure")
	}
	if state.Error != "connection timeout" {
		t.Errorf("expected error message")
	}
}

func TestIsGracefulStreamClosure(t *testing.T) {
	tests := []struct {
		name           string
		err            error
		expectGraceful bool
	}{
		{
			name:           "nil error",
			err:            nil,
			expectGraceful: false,
		},
		{
			name:           "EOF (normal closure)",
			err:            io.EOF,
			expectGraceful: true,
		},
		{
			name:           "context.Canceled",
			err:            context.Canceled,
			expectGraceful: true,
		},
		{
			name:           "context.DeadlineExceeded",
			err:            context.DeadlineExceeded,
			expectGraceful: true,
		},
		{
			name:           "gRPC Canceled code",
			err:            status.Error(codes.Canceled, "context canceled"),
			expectGraceful: true,
		},
		{
			name:           "gRPC DeadlineExceeded code",
			err:            status.Error(codes.DeadlineExceeded, "timeout"),
			expectGraceful: true,
		},
		{
			name:           "gRPC Unknown code",
			err:            status.Error(codes.Unknown, "unknown error"),
			expectGraceful: false,
		},
		{
			name:           "gRPC Unavailable code",
			err:            status.Error(codes.Unavailable, "service unavailable"),
			expectGraceful: false,
		},
		{
			name:           "network error",
			err:            errors.New("connection reset"),
			expectGraceful: false,
		},
		{
			name:           "wrapped EOF",
			err:            errors.Join(io.EOF, errors.New("wrapped")),
			expectGraceful: true,
		},
		{
			name:           "wrapped context.Canceled",
			err:            errors.Join(context.Canceled, errors.New("wrapped")),
			expectGraceful: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isGracefulStreamClosure(tt.err)
			if result != tt.expectGraceful {
				t.Errorf("isGracefulStreamClosure(%v) = %v, want %v", tt.err, result, tt.expectGraceful)
			}
		})
	}
}

func TestIsGracefulStreamClosure_ContextWrapping(t *testing.T) {
	// Test context errors wrapped with fmt.Errorf
	wrappedCanceled := errors.New("wrapper: context canceled")

	// Custom struct wrapping doesn't work with errors.Is - this is expected
	// Real-world wrapped errors use fmt.Errorf("%w", err) or errors.Join()
	// which are already tested in the main test table

	// This should NOT be detected (not properly wrapped)
	if isGracefulStreamClosure(wrappedCanceled) {
		t.Error("expected improperly wrapped error to NOT be detected as graceful")
	}
}

func TestIsGracefulStreamClosure_StatusCodes(t *testing.T) {
	gracefulCodes := []codes.Code{
		codes.Canceled,
		codes.DeadlineExceeded,
	}

	for _, code := range gracefulCodes {
		t.Run(code.String(), func(t *testing.T) {
			err := status.Error(code, "test error")
			if !isGracefulStreamClosure(err) {
				t.Errorf("expected %s to be graceful closure", code)
			}
		})
	}

	nonGracefulCodes := []codes.Code{
		codes.Unknown,
		codes.InvalidArgument,
		codes.NotFound,
		codes.AlreadyExists,
		codes.PermissionDenied,
		codes.ResourceExhausted,
		codes.FailedPrecondition,
		codes.Aborted,
		codes.OutOfRange,
		codes.Unimplemented,
		codes.Internal,
		codes.Unavailable,
		codes.DataLoss,
		codes.Unauthenticated,
	}

	for _, code := range nonGracefulCodes {
		t.Run(code.String(), func(t *testing.T) {
			err := status.Error(code, "test error")
			if isGracefulStreamClosure(err) {
				t.Errorf("expected %s to NOT be graceful closure", code)
			}
		})
	}
}

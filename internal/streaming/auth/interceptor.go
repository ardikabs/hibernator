/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package auth

import (
	"context"
	"strings"

	"github.com/go-logr/logr"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	// AuthorizationHeader is the metadata key for authorization.
	AuthorizationHeader = "authorization"

	// ExecutionIDHeader is the metadata key for execution ID.
	ExecutionIDHeader = "x-execution-id"
)

// contextKey is a custom type for context keys.
type contextKey string

const (
	// ValidationResultKey is the context key for validation results.
	ValidationResultKey contextKey = "validation-result"

	// ExecutionIDKey is the context key for execution ID.
	ExecutionIDKey contextKey = "execution-id"
)

// GRPCInterceptor creates a gRPC unary interceptor for token validation.
func GRPCInterceptor(validator *TokenValidator, log logr.Logger) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		// Extract metadata
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "missing metadata")
		}

		// Get authorization header
		authHeaders := md.Get(AuthorizationHeader)
		if len(authHeaders) == 0 {
			return nil, status.Error(codes.Unauthenticated, "missing authorization header")
		}

		token := ExtractTokenFromHeader(authHeaders[0])

		// Validate token
		result := validator.ValidateToken(ctx, token)
		if !result.Valid {
			log.Info("authentication failed",
				"method", info.FullMethod,
				"error", result.Error,
			)
			return nil, status.Error(codes.Unauthenticated, "invalid token")
		}

		// Add validation result to context
		ctx = context.WithValue(ctx, ValidationResultKey, result)

		// Extract and add execution ID
		execIDs := md.Get(ExecutionIDHeader)
		if len(execIDs) > 0 {
			ctx = context.WithValue(ctx, ExecutionIDKey, execIDs[0])
		}

		log.V(1).Info("authenticated request",
			"method", info.FullMethod,
			"user", result.Username,
		)

		return handler(ctx, req)
	}
}

// GRPCStreamInterceptor creates a gRPC stream interceptor for token validation.
func GRPCStreamInterceptor(validator *TokenValidator, log logr.Logger) grpc.StreamServerInterceptor {
	return func(
		srv interface{},
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		ctx := ss.Context()

		// Extract metadata
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return status.Error(codes.Unauthenticated, "missing metadata")
		}

		// Get authorization header
		authHeaders := md.Get(AuthorizationHeader)
		if len(authHeaders) == 0 {
			return status.Error(codes.Unauthenticated, "missing authorization header")
		}

		token := ExtractTokenFromHeader(authHeaders[0])

		// Validate token
		result := validator.ValidateToken(ctx, token)
		if !result.Valid {
			log.Info("stream authentication failed",
				"method", info.FullMethod,
				"error", result.Error,
			)
			return status.Error(codes.Unauthenticated, "invalid token")
		}

		log.V(1).Info("authenticated stream",
			"method", info.FullMethod,
			"user", result.Username,
		)

		// Wrap the stream with authenticated context
		wrapped := &authenticatedServerStream{
			ServerStream: ss,
			ctx:          context.WithValue(ctx, ValidationResultKey, result),
		}

		return handler(srv, wrapped)
	}
}

// authenticatedServerStream wraps a ServerStream with an authenticated context.
type authenticatedServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *authenticatedServerStream) Context() context.Context {
	return s.ctx
}

// GetValidationResult extracts the validation result from context.
func GetValidationResult(ctx context.Context) *ValidationResult {
	if v := ctx.Value(ValidationResultKey); v != nil {
		if result, ok := v.(*ValidationResult); ok {
			return result
		}
	}
	return nil
}

// GetExecutionID extracts the execution ID from context.
func GetExecutionID(ctx context.Context) string {
	if v := ctx.Value(ExecutionIDKey); v != nil {
		if id, ok := v.(string); ok {
			return id
		}
	}
	return ""
}

// ValidateExecutionAccess checks if the authenticated user has access to an execution.
// This validates that the runner's ServiceAccount matches the expected plan.
func ValidateExecutionAccess(ctx context.Context, planName, planNamespace string) error {
	result := GetValidationResult(ctx)
	if result == nil {
		return status.Error(codes.Unauthenticated, "no authentication context")
	}

	// Check namespace matches
	if result.Namespace != planNamespace {
		return status.Error(codes.PermissionDenied, "namespace mismatch")
	}

	// Check service account matches expected pattern
	expectedSA := "hibernator-runner-" + planName
	if !strings.HasPrefix(result.ServiceAccount, expectedSA) {
		return status.Error(codes.PermissionDenied, "service account mismatch")
	}

	return nil
}

/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package auth

// auth_integration_test.go covers GRPCInterceptor, GRPCStreamInterceptor,
// NewTokenValidator, and ValidateToken using k8s fake client and grpc test helpers.

import (
	"context"
	"fmt"
	"testing"

	"github.com/go-logr/logr"
	authv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// ---- NewTokenValidator ----

func TestNewTokenValidator_NotNil(t *testing.T) {
	fakeClient := k8sfake.NewSimpleClientset()
	v := NewTokenValidator(fakeClient, logr.Discard())
	if v == nil {
		t.Fatal("NewTokenValidator returned nil")
	}
	if v.audience != ExpectedAudience {
		t.Errorf("audience = %q, want %q", v.audience, ExpectedAudience)
	}
	if v.clientset == nil {
		t.Error("clientset should not be nil")
	}
}

// ---- ValidateToken ----

func TestValidateToken_EmptyToken(t *testing.T) {
	fakeClient := k8sfake.NewSimpleClientset()
	v := NewTokenValidator(fakeClient, logr.Discard())

	result := v.ValidateToken(context.Background(), "")
	if result.Valid {
		t.Error("empty token should not be valid")
	}
	if result.Error == nil {
		t.Error("empty token should produce an error")
	}
}

func TestValidateToken_BearerOnlyToken(t *testing.T) {
	fakeClient := k8sfake.NewSimpleClientset()
	v := NewTokenValidator(fakeClient, logr.Discard())

	// "Bearer " strips to ""
	result := v.ValidateToken(context.Background(), "Bearer ")
	if result.Valid {
		t.Error("empty-after-strip token should not be valid")
	}
}

func TestValidateToken_TokenReviewError(t *testing.T) {
	fakeClient := k8sfake.NewSimpleClientset()

	// Inject a reactor that returns an error for TokenReview
	fakeClient.PrependReactor("create", "tokenreviews", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
		return true, nil, fmt.Errorf("api server unavailable")
	})

	v := NewTokenValidator(fakeClient, logr.Discard())
	result := v.ValidateToken(context.Background(), "some-token")
	if result.Valid {
		t.Error("API error should result in invalid token")
	}
	if result.Error == nil {
		t.Error("should have error when TokenReview API fails")
	}
}

func TestValidateToken_NotAuthenticated(t *testing.T) {
	fakeClient := k8sfake.NewSimpleClientset()

	fakeClient.PrependReactor("create", "tokenreviews", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
		review := &authv1.TokenReview{
			Status: authv1.TokenReviewStatus{
				Authenticated: false,
				Error:         "token not found",
			},
		}
		return true, review, nil
	})

	v := NewTokenValidator(fakeClient, logr.Discard())
	result := v.ValidateToken(context.Background(), "bad-token")
	if result.Valid {
		t.Error("unauthenticated token should not be valid")
	}
	if result.Error == nil {
		t.Error("should have error for unauthenticated token")
	}
}

func TestValidateToken_NoAudiences(t *testing.T) {
	fakeClient := k8sfake.NewSimpleClientset()

	fakeClient.PrependReactor("create", "tokenreviews", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
		review := &authv1.TokenReview{
			Status: authv1.TokenReviewStatus{
				Authenticated: true,
				Audiences:     []string{}, // empty audiences
				User: authv1.UserInfo{
					Username: "system:serviceaccount:default:runner",
				},
			},
		}
		return true, review, nil
	})

	v := NewTokenValidator(fakeClient, logr.Discard())
	result := v.ValidateToken(context.Background(), "token-no-aud")
	if result.Valid {
		t.Error("token with no audiences should not be valid")
	}
}

func TestValidateToken_WrongAudience(t *testing.T) {
	fakeClient := k8sfake.NewSimpleClientset()

	fakeClient.PrependReactor("create", "tokenreviews", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
		review := &authv1.TokenReview{
			Status: authv1.TokenReviewStatus{
				Authenticated: true,
				Audiences:     []string{"wrong-audience"},
				User: authv1.UserInfo{
					Username: "system:serviceaccount:default:runner",
				},
			},
		}
		return true, review, nil
	})

	v := NewTokenValidator(fakeClient, logr.Discard())
	result := v.ValidateToken(context.Background(), "token-wrong-aud")
	if result.Valid {
		t.Error("token with wrong audience should not be valid")
	}
	if result.Error == nil {
		t.Error("should have error for wrong audience")
	}
}

func TestValidateToken_ValidToken(t *testing.T) {
	fakeClient := k8sfake.NewSimpleClientset()

	fakeClient.PrependReactor("create", "tokenreviews", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
		review := &authv1.TokenReview{
			ObjectMeta: metav1.ObjectMeta{},
			Status: authv1.TokenReviewStatus{
				Authenticated: true,
				Audiences:     []string{ExpectedAudience},
				User: authv1.UserInfo{
					Username: "system:serviceaccount:hibernator-system:runner",
					Groups:   []string{"system:serviceaccounts", "system:serviceaccounts:hibernator-system"},
				},
			},
		}
		return true, review, nil
	})

	v := NewTokenValidator(fakeClient, logr.Discard())
	result := v.ValidateToken(context.Background(), "valid-token")
	if !result.Valid {
		t.Errorf("should be valid, got error: %v", result.Error)
	}
	if result.Username != "system:serviceaccount:hibernator-system:runner" {
		t.Errorf("Username = %q", result.Username)
	}
	if result.Namespace != "hibernator-system" {
		t.Errorf("Namespace = %q", result.Namespace)
	}
	if result.ServiceAccount != "runner" {
		t.Errorf("ServiceAccount = %q", result.ServiceAccount)
	}
}

func TestValidateToken_ValidBearerToken(t *testing.T) {
	fakeClient := k8sfake.NewSimpleClientset()

	fakeClient.PrependReactor("create", "tokenreviews", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
		review := &authv1.TokenReview{
			Status: authv1.TokenReviewStatus{
				Authenticated: true,
				Audiences:     []string{ExpectedAudience},
				User: authv1.UserInfo{
					Username: "system:serviceaccount:ns:sa",
				},
			},
		}
		return true, review, nil
	})

	v := NewTokenValidator(fakeClient, logr.Discard())
	// Pass with "Bearer " prefix — validator should strip it
	result := v.ValidateToken(context.Background(), "Bearer valid-bearer-token")
	if !result.Valid {
		t.Errorf("bearer token should be valid, got error: %v", result.Error)
	}
}

// ---- GRPCInterceptor ----

func buildValidatorWithTokenReactor(authenticated bool, audiences []string, username string) *TokenValidator {
	fakeClient := k8sfake.NewSimpleClientset()
	fakeClient.PrependReactor("create", "tokenreviews", func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
		review := &authv1.TokenReview{
			Status: authv1.TokenReviewStatus{
				Authenticated: authenticated,
				Audiences:     audiences,
				User: authv1.UserInfo{
					Username: username,
				},
			},
		}
		return true, review, nil
	})
	return NewTokenValidator(fakeClient, logr.Discard())
}

func TestGRPCInterceptor_MissingMetadata(t *testing.T) {
	validator := buildValidatorWithTokenReactor(false, nil, "")
	interceptor := GRPCInterceptor(validator, logr.Discard())

	_, err := interceptor(
		context.Background(), // no incoming metadata
		nil,
		&grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"},
		func(ctx context.Context, req interface{}) (interface{}, error) {
			return "ok", nil
		},
	)
	if err == nil {
		t.Error("expected error with missing metadata")
	}
}

func TestGRPCInterceptor_MissingAuthHeader(t *testing.T) {
	validator := buildValidatorWithTokenReactor(false, nil, "")
	interceptor := GRPCInterceptor(validator, logr.Discard())

	// metadata present but no authorization key
	md := metadata.New(map[string]string{"x-request-id": "123"})
	ctx := metadata.NewIncomingContext(context.Background(), md)

	_, err := interceptor(
		ctx,
		nil,
		&grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"},
		func(ctx context.Context, req interface{}) (interface{}, error) {
			return "ok", nil
		},
	)
	if err == nil {
		t.Error("expected error with missing authorization header")
	}
}

func TestGRPCInterceptor_InvalidToken(t *testing.T) {
	validator := buildValidatorWithTokenReactor(false, nil, "")
	interceptor := GRPCInterceptor(validator, logr.Discard())

	md := metadata.New(map[string]string{AuthorizationHeader: "Bearer bad-token"})
	ctx := metadata.NewIncomingContext(context.Background(), md)

	_, err := interceptor(
		ctx,
		nil,
		&grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"},
		func(ctx context.Context, req interface{}) (interface{}, error) {
			return "ok", nil
		},
	)
	if err == nil {
		t.Error("expected error with invalid token")
	}
}

func TestGRPCInterceptor_ValidToken(t *testing.T) {
	validator := buildValidatorWithTokenReactor(true, []string{ExpectedAudience}, "system:serviceaccount:ns:sa")
	interceptor := GRPCInterceptor(validator, logr.Discard())

	md := metadata.New(map[string]string{
		AuthorizationHeader: "Bearer valid-token",
		ExecutionIDHeader:   "exec-123",
	})
	ctx := metadata.NewIncomingContext(context.Background(), md)

	var capturedCtx context.Context
	_, err := interceptor(
		ctx,
		nil,
		&grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"},
		func(ctx context.Context, req interface{}) (interface{}, error) {
			capturedCtx = ctx
			return "ok", nil
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify validation result is in context
	result := GetValidationResult(capturedCtx)
	if result == nil || !result.Valid {
		t.Error("expected valid result in context after successful auth")
	}

	// Verify execution ID is in context
	execID := GetExecutionID(capturedCtx)
	if execID != "exec-123" {
		t.Errorf("ExecutionID = %q, want %q", execID, "exec-123")
	}
}

// ---- GRPCStreamInterceptor ----

// mockServerStream implements grpc.ServerStream for testing.
type mockServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (m *mockServerStream) Context() context.Context {
	return m.ctx
}

func TestGRPCStreamInterceptor_MissingMetadata(t *testing.T) {
	validator := buildValidatorWithTokenReactor(false, nil, "")
	interceptor := GRPCStreamInterceptor(validator, logr.Discard())

	ss := &mockServerStream{ctx: context.Background()}
	err := interceptor(
		nil,
		ss,
		&grpc.StreamServerInfo{FullMethod: "/test.Service/Stream"},
		func(srv interface{}, stream grpc.ServerStream) error {
			return nil
		},
	)
	if err == nil {
		t.Error("expected error with missing metadata")
	}
}

func TestGRPCStreamInterceptor_MissingAuthHeader(t *testing.T) {
	validator := buildValidatorWithTokenReactor(false, nil, "")
	interceptor := GRPCStreamInterceptor(validator, logr.Discard())

	md := metadata.New(map[string]string{"x-foo": "bar"})
	ctx := metadata.NewIncomingContext(context.Background(), md)
	ss := &mockServerStream{ctx: ctx}

	err := interceptor(
		nil,
		ss,
		&grpc.StreamServerInfo{FullMethod: "/test.Service/Stream"},
		func(srv interface{}, stream grpc.ServerStream) error {
			return nil
		},
	)
	if err == nil {
		t.Error("expected error with missing authorization header")
	}
}

func TestGRPCStreamInterceptor_InvalidToken(t *testing.T) {
	validator := buildValidatorWithTokenReactor(false, nil, "")
	interceptor := GRPCStreamInterceptor(validator, logr.Discard())

	md := metadata.New(map[string]string{AuthorizationHeader: "Bearer bad"})
	ctx := metadata.NewIncomingContext(context.Background(), md)
	ss := &mockServerStream{ctx: ctx}

	err := interceptor(
		nil,
		ss,
		&grpc.StreamServerInfo{},
		func(srv interface{}, stream grpc.ServerStream) error {
			return nil
		},
	)
	if err == nil {
		t.Error("expected error for invalid stream token")
	}
}

func TestGRPCStreamInterceptor_ValidToken(t *testing.T) {
	validator := buildValidatorWithTokenReactor(true, []string{ExpectedAudience}, "system:serviceaccount:ns:sa")
	interceptor := GRPCStreamInterceptor(validator, logr.Discard())

	md := metadata.New(map[string]string{AuthorizationHeader: "Bearer valid-token"})
	ctx := metadata.NewIncomingContext(context.Background(), md)
	ss := &mockServerStream{ctx: ctx}

	var capturedStream grpc.ServerStream
	err := interceptor(
		nil,
		ss,
		&grpc.StreamServerInfo{FullMethod: "/test.Service/Stream"},
		func(srv interface{}, stream grpc.ServerStream) error {
			capturedStream = stream
			return nil
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The stream should be wrapped with auth context
	result := GetValidationResult(capturedStream.Context())
	if result == nil || !result.Valid {
		t.Error("expected valid result in stream context after successful auth")
	}
}

/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/cmd/runner/metadata"
	"github.com/ardikabs/hibernator/internal/connectorprobe"
	"github.com/ardikabs/hibernator/internal/executor"
)

type stubBuilder struct {
	cfg  executor.ConnectorConfig
	err  error
	kind string
	ns   string
	name string
}

func (s *stubBuilder) BuildConnectorConfig(_ context.Context, kind, namespace, name string) (executor.ConnectorConfig, error) {
	s.kind, s.ns, s.name = kind, namespace, name
	return s.cfg, s.err
}

type stubProber struct {
	res    connectorprobe.Result
	err    error
	called bool
	kind   string
	execs  []executor.Executor
}

func (s *stubProber) Probe(_ context.Context, kind string, _ executor.ConnectorConfig, execs []executor.Executor) (connectorprobe.Result, error) {
	s.called = true
	s.kind = kind
	s.execs = execs
	return s.res, s.err
}

func probeCfg(kind, name string) *Config {
	return &Config{Operation: "probe", ConnectorKind: kind, ConnectorName: name, ConnectorNamespace: "ns1", Namespace: "pod-ns"}
}

func TestRunProbe_BuildConfigErrors(t *testing.T) {
	gr := schema.GroupResource{Resource: "x"}
	tests := []struct {
		name       string
		err        error
		wantReason string
		wantErr    bool
	}{
		{"secret not found", fmt.Errorf("get Secret a/b: %w: %w", metadata.ErrSecretNotFound, apierrors.NewNotFound(gr, "b")), hibernatorv1alpha1.ReasonSecretNotFound, false},
		{"credentials incomplete", fmt.Errorf("%w: missing keys", metadata.ErrCredentialsIncomplete), hibernatorv1alpha1.ReasonInvalidCredentials, false},
		{"connector not found", fmt.Errorf("get CloudProvider: %w", apierrors.NewNotFound(gr, "p")), "", true},
		{"api error", fmt.Errorf("get CloudProvider: %w", apierrors.NewInternalError(errors.New("boom"))), "", true},
		{"unsupported type", errors.New("unsupported cloud provider type: gcp"), "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &stubProber{}
			res, err := runProbe(context.Background(), logr.Discard(), probeCfg("CloudProvider", "cp"), &stubBuilder{err: tt.err}, p, nil)
			assert.False(t, p.called)
			if tt.wantErr {
				require.Error(t, err)
				assert.Equal(t, connectorprobe.Result{}, res)
				return
			}
			require.NoError(t, err)
			assert.False(t, res.Ready)
			assert.Equal(t, tt.wantReason, res.Reason)
			assert.NotEmpty(t, res.Message)
		})
	}
}

func TestRunProbe_KindValidation(t *testing.T) {
	tests := []struct {
		kind, name string
		wantErr    bool
	}{
		{"CloudProvider", "cp", false},
		{"K8SCluster", "kc", false},
		{"Other", "x", true},
		{"", "x", true},
		{"CloudProvider", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.kind+"/"+tt.name, func(t *testing.T) {
			b := &stubBuilder{}
			p := &stubProber{res: connectorprobe.Result{Ready: true}}
			res, err := runProbe(context.Background(), logr.Discard(), probeCfg(tt.kind, tt.name), b, p, nil)
			if tt.wantErr {
				require.Error(t, err)
				assert.False(t, p.called)
				assert.Empty(t, b.kind)
				return
			}
			require.NoError(t, err)
			assert.True(t, res.Ready)
			assert.Equal(t, tt.kind, b.kind)
			assert.Equal(t, "ns1", b.ns)
			assert.Equal(t, tt.name, b.name)
			assert.Equal(t, tt.kind, p.kind)
		})
	}
}

func TestRunProbe_PassesExecutors(t *testing.T) {
	execs := []executor.Executor{&fakeExecutor{typeVal: "eks"}, &fakeExecutor{typeVal: "rds"}}
	p := &stubProber{res: connectorprobe.Result{Ready: true}}
	_, err := runProbe(context.Background(), logr.Discard(), probeCfg("CloudProvider", "cp"), &stubBuilder{}, p, execs)
	require.NoError(t, err)
	assert.Equal(t, execs, p.execs)
}

func TestRunProbe_ProberErrorReturnsError(t *testing.T) {
	p := &stubProber{err: context.DeadlineExceeded}
	res, err := runProbe(context.Background(), logr.Discard(), probeCfg("CloudProvider", "cp"), &stubBuilder{}, p, nil)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Equal(t, connectorprobe.Result{}, res)
}

func TestProbeExecutors_MatchRegistered(t *testing.T) {
	t.Setenv("HIBERNATOR_ACTIVE_EXECUTORS", "eks,rds,noop")
	execs := probeExecutors(logr.Discard())
	var got []string
	for _, e := range execs {
		got = append(got, e.Type())
	}
	assert.ElementsMatch(t, []string{"eks", "rds", "noop"}, got)
}

func withTerminationPath(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "termination-log")
	old := terminationLogPath
	terminationLogPath = path
	t.Cleanup(func() { terminationLogPath = old })
	return path
}

func TestWriteProbeTerminationLog(t *testing.T) {
	path := withTerminationPath(t)
	res := connectorprobe.Result{Ready: false, Reason: hibernatorv1alpha1.ReasonSecretNotFound, Message: "nope"}
	writeProbeTerminationLog(logr.Discard(), res)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	got, err := connectorprobe.Unmarshal(string(data))
	require.NoError(t, err)
	assert.Equal(t, res, got)
}

func TestExecuteProbe_NoTargetPlanOrToken(t *testing.T) {
	path := withTerminationPath(t)
	cfg := probeCfg("CloudProvider", "cp") // no Target, Plan, TokenPath, endpoints
	b := &stubBuilder{err: fmt.Errorf("%w: x", metadata.ErrCredentialsIncomplete)}
	err := executeProbe(context.Background(), logr.Discard(), cfg, b, &stubProber{}, nil)
	require.NoError(t, err, "a Ready=false verdict exits 0")
	data, rerr := os.ReadFile(path)
	require.NoError(t, rerr)
	res, uerr := connectorprobe.Unmarshal(string(data))
	require.NoError(t, uerr)
	assert.False(t, res.Ready)
	assert.Equal(t, hibernatorv1alpha1.ReasonInvalidCredentials, res.Reason)
}

func TestExecuteProbe_ErrorWritesNoVerdict(t *testing.T) {
	path := withTerminationPath(t)
	err := executeProbe(context.Background(), logr.Discard(), probeCfg("CloudProvider", "cp"), &stubBuilder{}, &stubProber{err: errors.New("boom")}, nil)
	require.Error(t, err)
	data, _ := os.ReadFile(path)
	_, uerr := connectorprobe.Unmarshal(string(data))
	assert.Error(t, uerr, "no verdict JSON may be written on error")
}

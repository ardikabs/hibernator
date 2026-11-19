/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package connector

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/wellknown"
)

// fakeStrategy reuses the CloudProvider type under a made-up kind, to show the
// Reconciler runs any Strategy through the shared probe logic.
type fakeStrategy struct{ kind string }

func (s fakeStrategy) Kind() string                                             { return s.kind }
func (fakeStrategy) Name() string                                               { return "fake-connector" }
func (fakeStrategy) New() client.Object                                         { return &hibernatorv1alpha1.CloudProvider{} }
func (fakeStrategy) Watches(b *builder.Builder, _ *Reconciler) *builder.Builder { return b }
func (fakeStrategy) SecretRequests(context.Context, *Reconciler, client.Object) []reconcile.Request {
	return nil
}
func (s fakeStrategy) Prepare(_ context.Context, _ *Reconciler, obj client.Object) (probeTarget, bool, error) {
	return probeTarget{kind: s.kind, key: client.ObjectKeyFromObject(obj), generation: obj.GetGeneration()}, false, nil
}

func TestReconcile_RequiresStrategy(t *testing.T) {
	f := newFixture(t, cloudProvider("aws"))
	f.r.Strategy = nil

	_, err := f.r.Reconcile(context.Background(), req("aws"))
	assert.Error(t, err)
	assert.Error(t, f.r.SetupWithManager(nil, 1))
}

func TestReconcile_CustomStrategyUsesSharedProbeLogic(t *testing.T) {
	f := newFixture(t, cloudProvider("aws"))
	f.r.Strategy = fakeStrategy{kind: "Fake"}

	res, err := f.r.Reconcile(context.Background(), req("aws"))
	require.NoError(t, err)
	assert.Equal(t, 10*time.Second, res.RequeueAfter)

	jobs := listProbeJobs(t, f.c)
	require.Len(t, jobs, 1)
	assert.Equal(t, "Fake", jobs[0].Labels[wellknown.LabelConnectorKind])
}

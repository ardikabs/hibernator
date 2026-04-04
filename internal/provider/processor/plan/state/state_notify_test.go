/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package state

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/notification"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ---------------------------------------------------------------------------
// buildPayload
// ---------------------------------------------------------------------------

func TestBuildPayload_PopulatesPlanInfo(t *testing.T) {
	plan := &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "prod-plan",
			Namespace:   "infra",
			Labels:      map[string]string{"env": "prod"},
			Annotations: map[string]string{"team": "platform"},
		},
		Status: hibernatorv1alpha1.HibernatePlanStatus{
			Phase:            hibernatorv1alpha1.PhaseHibernating,
			CurrentOperation: hibernatorv1alpha1.OperationHibernate,
			CurrentCycleID:   "cycle-1",
		},
	}
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	p := buildPayload(plan, hibernatorv1alpha1.EventStart, func() time.Time { return now })

	assert.Equal(t, "prod-plan", p.Plan.Name)
	assert.Equal(t, "infra", p.Plan.Namespace)
	assert.Equal(t, map[string]string{"env": "prod"}, p.Plan.Labels)
	assert.Equal(t, map[string]string{"team": "platform"}, p.Plan.Annotations)
	assert.Equal(t, string(hibernatorv1alpha1.EventStart), p.Event)
	assert.Equal(t, now, p.Timestamp)
}

func TestBuildPayload_MapsConnectorRefFromSpecTargets(t *testing.T) {
	plan := &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "plan-a",
			Namespace: "default",
		},
		Spec: hibernatorv1alpha1.HibernatePlanSpec{
			Targets: []hibernatorv1alpha1.Target{
				{
					Name: "eks-target",
					Type: "eks",
					ConnectorRef: hibernatorv1alpha1.ConnectorRef{
						Kind: "K8SCluster",
						Name: "prod-cluster",
					},
				},
				{
					Name: "rds-target",
					Type: "rds",
					ConnectorRef: hibernatorv1alpha1.ConnectorRef{
						Kind: "CloudProvider",
						Name: "aws-prod",
					},
				},
			},
		},
		Status: hibernatorv1alpha1.HibernatePlanStatus{
			Phase:            hibernatorv1alpha1.PhaseHibernating,
			CurrentOperation: hibernatorv1alpha1.OperationHibernate,
			Executions: []hibernatorv1alpha1.ExecutionStatus{
				{Target: "eks-target", Executor: "eks", State: hibernatorv1alpha1.StateCompleted},
				{Target: "rds-target", Executor: "rds", State: hibernatorv1alpha1.StateCompleted},
			},
		},
	}

	clk := clocktesting.NewFakeClock(time.Now())
	p := buildPayload(plan, hibernatorv1alpha1.EventSuccess, clk.Now)

	require.Len(t, p.Targets, 2)

	assert.Equal(t, "K8SCluster", p.Targets[0].Connector.Kind)
	assert.Equal(t, "prod-cluster", p.Targets[0].Connector.Name)
	assert.Equal(t, "CloudProvider", p.Targets[1].Connector.Kind)
	assert.Equal(t, "aws-prod", p.Targets[1].Connector.Name)
}

func TestBuildPayload_NoSpecTargetMatch_EmptyConnector(t *testing.T) {
	plan := &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "plan-b",
			Namespace: "default",
		},
		Spec: hibernatorv1alpha1.HibernatePlanSpec{
			Targets: []hibernatorv1alpha1.Target{},
		},
		Status: hibernatorv1alpha1.HibernatePlanStatus{
			Executions: []hibernatorv1alpha1.ExecutionStatus{
				{Target: "orphan", Executor: "noop", State: hibernatorv1alpha1.StateCompleted},
			},
		},
	}

	clk := clocktesting.NewFakeClock(time.Now())
	p := buildPayload(plan, hibernatorv1alpha1.EventSuccess, clk.Now)

	require.Len(t, p.Targets, 1)
	assert.Empty(t, p.Targets[0].Connector.Kind)
	assert.Empty(t, p.Targets[0].Connector.Name)
}

// ---------------------------------------------------------------------------
// enrichConnectorInfo
// ---------------------------------------------------------------------------

func TestEnrichConnectorInfo_CloudProvider_AWS(t *testing.T) {
	cp := &hibernatorv1alpha1.CloudProvider{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "aws-prod",
			Namespace: "default",
		},
		Spec: hibernatorv1alpha1.CloudProviderSpec{
			Type: hibernatorv1alpha1.CloudProviderAWS,
			AWS: &hibernatorv1alpha1.AWSConfig{
				AccountId: "123456789012",
				Region:    "us-east-1",
				Auth:      hibernatorv1alpha1.AWSAuth{},
			},
		},
	}

	c := newHandlerFakeClient(cp)
	targets := []notification.TargetInfo{
		{
			Name:     "rds-main",
			Executor: "rds",
			Connector: notification.ConnectorInfo{
				Kind: "CloudProvider",
				Name: "aws-prod",
			},
		},
	}

	enrichConnectorInfo(context.Background(), c, "default", targets)

	assert.Equal(t, "aws", targets[0].Connector.Provider)
	assert.Equal(t, "123456789012", targets[0].Connector.AccountID)
	assert.Equal(t, "us-east-1", targets[0].Connector.Region)
	assert.Empty(t, targets[0].Connector.ClusterName)
}

func TestEnrichConnectorInfo_K8SCluster_EKS(t *testing.T) {
	kc := &hibernatorv1alpha1.K8SCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "prod-cluster",
			Namespace: "default",
		},
		Spec: hibernatorv1alpha1.K8SClusterSpec{
			EKS: &hibernatorv1alpha1.EKSConfig{
				Name:   "prod-eks",
				Region: "us-west-2",
			},
		},
	}

	c := newHandlerFakeClient(kc)
	targets := []notification.TargetInfo{
		{
			Name:     "eks-target",
			Executor: "eks",
			Connector: notification.ConnectorInfo{
				Kind: "K8SCluster",
				Name: "prod-cluster",
			},
		},
	}

	enrichConnectorInfo(context.Background(), c, "default", targets)

	assert.Equal(t, "prod-eks", targets[0].Connector.ClusterName)
	assert.Equal(t, "us-west-2", targets[0].Connector.Region)
}

func TestEnrichConnectorInfo_K8SCluster_GKE(t *testing.T) {
	kc := &hibernatorv1alpha1.K8SCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gke-cluster",
			Namespace: "default",
		},
		Spec: hibernatorv1alpha1.K8SClusterSpec{
			GKE: &hibernatorv1alpha1.GKEConfig{
				Name:     "gke-prod",
				Project:  "my-gcp-project",
				Location: "us-central1",
			},
		},
	}

	c := newHandlerFakeClient(kc)
	targets := []notification.TargetInfo{
		{
			Name:     "gke-target",
			Executor: "gke",
			Connector: notification.ConnectorInfo{
				Kind: "K8SCluster",
				Name: "gke-cluster",
			},
		},
	}

	enrichConnectorInfo(context.Background(), c, "default", targets)

	assert.Equal(t, "gke-prod", targets[0].Connector.ClusterName)
	assert.Equal(t, "us-central1", targets[0].Connector.Region)
	assert.Equal(t, "my-gcp-project", targets[0].Connector.ProjectID)
}

func TestEnrichConnectorInfo_K8SCluster_WithProviderRef(t *testing.T) {
	cp := &hibernatorv1alpha1.CloudProvider{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "aws-shared",
			Namespace: "default",
		},
		Spec: hibernatorv1alpha1.CloudProviderSpec{
			Type: hibernatorv1alpha1.CloudProviderAWS,
			AWS: &hibernatorv1alpha1.AWSConfig{
				AccountId: "999888777666",
				Region:    "eu-west-1",
				Auth:      hibernatorv1alpha1.AWSAuth{},
			},
		},
	}
	kc := &hibernatorv1alpha1.K8SCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "eks-with-ref",
			Namespace: "default",
		},
		Spec: hibernatorv1alpha1.K8SClusterSpec{
			ProviderRef: &hibernatorv1alpha1.ProviderRef{
				Name: "aws-shared",
			},
			EKS: &hibernatorv1alpha1.EKSConfig{
				Name:   "shared-eks",
				Region: "eu-west-1",
			},
		},
	}

	c := newHandlerFakeClient(cp, kc)
	targets := []notification.TargetInfo{
		{
			Name:     "eks-shared",
			Executor: "eks",
			Connector: notification.ConnectorInfo{
				Kind: "K8SCluster",
				Name: "eks-with-ref",
			},
		},
	}

	enrichConnectorInfo(context.Background(), c, "default", targets)

	assert.Equal(t, "shared-eks", targets[0].Connector.ClusterName)
	assert.Equal(t, "eu-west-1", targets[0].Connector.Region)
	assert.Equal(t, "aws", targets[0].Connector.Provider)
	assert.Equal(t, "999888777666", targets[0].Connector.AccountID)
}

func TestEnrichConnectorInfo_MissingResource_Skips(t *testing.T) {
	c := newHandlerFakeClient() // no resources
	targets := []notification.TargetInfo{
		{
			Name:     "target",
			Executor: "rds",
			Connector: notification.ConnectorInfo{
				Kind: "CloudProvider",
				Name: "nonexistent",
			},
		},
	}

	enrichConnectorInfo(context.Background(), c, "default", targets)

	// Fields remain empty when resource not found.
	assert.Empty(t, targets[0].Connector.Provider)
	assert.Empty(t, targets[0].Connector.AccountID)
}

func TestEnrichConnectorInfo_EmptyKindOrName_Skips(t *testing.T) {
	c := newHandlerFakeClient()
	targets := []notification.TargetInfo{
		{
			Name:     "no-connector",
			Executor: "noop",
			Connector: notification.ConnectorInfo{
				Kind: "",
				Name: "",
			},
		},
	}

	enrichConnectorInfo(context.Background(), c, "default", targets)

	assert.Empty(t, targets[0].Connector.Provider)
}

// ---------------------------------------------------------------------------
// subscribesToEvent
// ---------------------------------------------------------------------------

func TestSubscribesToEvent_Match(t *testing.T) {
	notif := &hibernatorv1alpha1.HibernateNotification{
		Spec: hibernatorv1alpha1.HibernateNotificationSpec{
			OnEvents: []hibernatorv1alpha1.NotificationEvent{
				hibernatorv1alpha1.EventStart,
				hibernatorv1alpha1.EventFailure,
			},
		},
	}

	assert.True(t, subscribesToEvent(notif, hibernatorv1alpha1.EventStart))
	assert.True(t, subscribesToEvent(notif, hibernatorv1alpha1.EventFailure))
	assert.False(t, subscribesToEvent(notif, hibernatorv1alpha1.EventSuccess))
}

// ---------------------------------------------------------------------------
// chainHooks
// ---------------------------------------------------------------------------

func TestChainHooks_AllNil_ReturnsNil(t *testing.T) {
	result := chainHooks[client.Object](nil, nil)
	assert.Nil(t, result)
}

func TestChainHooks_SingleNonNil_ReturnsThatHook(t *testing.T) {
	called := false
	h := func(_ context.Context, _ client.Object) error {
		called = true
		return nil
	}

	result := chainHooks[client.Object](nil, h, nil)
	require.NotNil(t, result)
	require.NoError(t, result(context.Background(), nil))
	assert.True(t, called)
}

func TestChainHooks_MultipleHooks_RunsInOrder(t *testing.T) {
	var order []int
	h1 := func(_ context.Context, _ client.Object) error { order = append(order, 1); return nil }
	h2 := func(_ context.Context, _ client.Object) error { order = append(order, 2); return nil }

	result := chainHooks[client.Object](h1, h2)
	require.NotNil(t, result)
	require.NoError(t, result(context.Background(), nil))
	assert.Equal(t, []int{1, 2}, order)
}

// ---------------------------------------------------------------------------
// spyNotifier
// ---------------------------------------------------------------------------

type spyNotifier struct {
	requests []notification.Request
}

func (s *spyNotifier) Submit(req notification.Request) {
	s.requests = append(s.requests, req)
}

// ---------------------------------------------------------------------------
// executionProgressPostHook
// ---------------------------------------------------------------------------

func TestExecutionProgressPostHook_DispatchesOnStateChange(t *testing.T) {
	plan := &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "prod-plan",
			Namespace: "infra",
			Labels:    map[string]string{"env": "prod"},
		},
		Spec: hibernatorv1alpha1.HibernatePlanSpec{
			Targets: []hibernatorv1alpha1.Target{
				{
					Name: "rds-main",
					Type: "rds",
					ConnectorRef: hibernatorv1alpha1.ConnectorRef{
						Kind: "CloudProvider",
						Name: "aws-prod",
					},
				},
			},
		},
		Status: hibernatorv1alpha1.HibernatePlanStatus{
			Phase:            hibernatorv1alpha1.PhaseHibernating,
			CurrentOperation: hibernatorv1alpha1.OperationHibernate,
			CurrentCycleID:   "cycle-1",
			Executions: []hibernatorv1alpha1.ExecutionStatus{
				{Target: "rds-main", Executor: "rds", State: hibernatorv1alpha1.StateRunning, Message: "running"},
			},
		},
	}

	notif := hibernatorv1alpha1.HibernateNotification{
		ObjectMeta: metav1.ObjectMeta{Name: "n1", Namespace: "infra"},
		Spec: hibernatorv1alpha1.HibernateNotificationSpec{
			OnEvents: []hibernatorv1alpha1.NotificationEvent{hibernatorv1alpha1.EventExecutionProgress},
			Sinks: []hibernatorv1alpha1.NotificationSink{
				{Name: "slack", Type: hibernatorv1alpha1.SinkSlack, SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "s1"}},
			},
		},
	}

	spy := &spyNotifier{}
	c := newHandlerFakeClient(plan)
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	s := newHandlerState(plan, c)
	s.Notifier = spy
	s.PlanCtx.Notifications = []hibernatorv1alpha1.HibernateNotification{notif}
	s.Clock = clocktesting.NewFakeClock(now)

	// Snapshot before transition: target was Pending.
	prevSnapshot := map[string]executionSnapshot{
		"rds-main": {State: hibernatorv1alpha1.StatePending},
	}

	hook := s.executionProgressPostHook(prevSnapshot)
	require.NotNil(t, hook)
	require.NoError(t, hook(context.Background(), plan))

	require.Len(t, spy.requests, 1)
	req := spy.requests[0]
	assert.Equal(t, string(hibernatorv1alpha1.EventExecutionProgress), req.Payload.Event)
	assert.Equal(t, "slack", req.SinkName)

	// TargetExecution must be populated.
	require.NotNil(t, req.Payload.TargetExecution)
	assert.Equal(t, "rds-main", req.Payload.TargetExecution.Name)
	assert.Equal(t, "rds", req.Payload.TargetExecution.Executor)
	assert.Equal(t, "Running", req.Payload.TargetExecution.State)
	assert.Equal(t, "running", req.Payload.TargetExecution.Message)

	// Connector kind/name resolved from spec.
	assert.Equal(t, "CloudProvider", req.Payload.TargetExecution.Connector.Kind)
	assert.Equal(t, "aws-prod", req.Payload.TargetExecution.Connector.Name)

	// Full Targets slice is also present.
	require.Len(t, req.Payload.Targets, 1)
	assert.Equal(t, "rds-main", req.Payload.Targets[0].Name)
}

func TestExecutionProgressPostHook_NilNotifier_ReturnsNil(t *testing.T) {
	plan := &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns"},
	}
	c := newHandlerFakeClient(plan)
	s := newHandlerState(plan, c)
	s.Notifier = nil // explicitly nil

	hook := s.executionProgressPostHook(nil)
	assert.Nil(t, hook)
}

func TestExecutionProgressPostHook_NoNotifications_ReturnsNil(t *testing.T) {
	plan := &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns"},
	}
	spy := &spyNotifier{}
	c := newHandlerFakeClient(plan)
	s := newHandlerState(plan, c)
	s.Notifier = spy
	s.PlanCtx.Notifications = nil // no notifications

	hook := s.executionProgressPostHook(nil)
	assert.Nil(t, hook)
}

func TestExecutionProgressPostHook_NoStateChange_NoDispatch(t *testing.T) {
	plan := &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns"},
		Status: hibernatorv1alpha1.HibernatePlanStatus{
			Executions: []hibernatorv1alpha1.ExecutionStatus{
				{Target: "t1", Executor: "noop", State: hibernatorv1alpha1.StateRunning},
			},
		},
	}

	notif := hibernatorv1alpha1.HibernateNotification{
		ObjectMeta: metav1.ObjectMeta{Name: "n1", Namespace: "ns"},
		Spec: hibernatorv1alpha1.HibernateNotificationSpec{
			OnEvents: []hibernatorv1alpha1.NotificationEvent{hibernatorv1alpha1.EventExecutionProgress},
			Sinks: []hibernatorv1alpha1.NotificationSink{
				{Name: "slack", Type: hibernatorv1alpha1.SinkSlack, SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "s1"}},
			},
		},
	}

	spy := &spyNotifier{}
	c := newHandlerFakeClient(plan)
	s := newHandlerState(plan, c)
	s.Notifier = spy
	s.PlanCtx.Notifications = []hibernatorv1alpha1.HibernateNotification{notif}

	// Snapshot matches current state — no transition.
	prevSnapshot := map[string]executionSnapshot{
		"t1": {State: hibernatorv1alpha1.StateRunning},
	}

	hook := s.executionProgressPostHook(prevSnapshot)
	require.NotNil(t, hook)
	require.NoError(t, hook(context.Background(), plan))
	assert.Empty(t, spy.requests, "should not dispatch when state has not changed")
}

func TestExecutionProgressPostHook_NotSubscribed_NoDispatch(t *testing.T) {
	plan := &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns"},
		Status: hibernatorv1alpha1.HibernatePlanStatus{
			Executions: []hibernatorv1alpha1.ExecutionStatus{
				{Target: "t1", Executor: "noop", State: hibernatorv1alpha1.StateCompleted},
			},
		},
	}

	notif := hibernatorv1alpha1.HibernateNotification{
		ObjectMeta: metav1.ObjectMeta{Name: "n1", Namespace: "ns"},
		Spec: hibernatorv1alpha1.HibernateNotificationSpec{
			OnEvents: []hibernatorv1alpha1.NotificationEvent{hibernatorv1alpha1.EventSuccess}, // NOT ExecutionProgress
			Sinks: []hibernatorv1alpha1.NotificationSink{
				{Name: "slack", Type: hibernatorv1alpha1.SinkSlack, SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "s1"}},
			},
		},
	}

	spy := &spyNotifier{}
	c := newHandlerFakeClient(plan)
	s := newHandlerState(plan, c)
	s.Notifier = spy
	s.PlanCtx.Notifications = []hibernatorv1alpha1.HibernateNotification{notif}

	// State changed from Pending → Completed.
	prevSnapshot := map[string]executionSnapshot{
		"t1": {State: hibernatorv1alpha1.StatePending},
	}

	hook := s.executionProgressPostHook(prevSnapshot)
	require.NotNil(t, hook)
	require.NoError(t, hook(context.Background(), plan))
	assert.Empty(t, spy.requests, "should not dispatch when not subscribed to ExecutionProgress")
}

func TestExecutionProgressPostHook_MultipleSinks_DispatchesAll(t *testing.T) {
	plan := &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns"},
		Status: hibernatorv1alpha1.HibernatePlanStatus{
			Executions: []hibernatorv1alpha1.ExecutionStatus{
				{Target: "t1", Executor: "eks", State: hibernatorv1alpha1.StateCompleted},
			},
		},
	}

	notif := hibernatorv1alpha1.HibernateNotification{
		ObjectMeta: metav1.ObjectMeta{Name: "n1", Namespace: "ns"},
		Spec: hibernatorv1alpha1.HibernateNotificationSpec{
			OnEvents: []hibernatorv1alpha1.NotificationEvent{hibernatorv1alpha1.EventExecutionProgress},
			Sinks: []hibernatorv1alpha1.NotificationSink{
				{Name: "slack", Type: hibernatorv1alpha1.SinkSlack, SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "s1"}},
				{Name: "telegram", Type: hibernatorv1alpha1.SinkTelegram, SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "s2"}},
			},
		},
	}

	spy := &spyNotifier{}
	c := newHandlerFakeClient(plan)
	s := newHandlerState(plan, c)
	s.Notifier = spy
	s.PlanCtx.Notifications = []hibernatorv1alpha1.HibernateNotification{notif}

	// State changed from Running → Completed.
	prevSnapshot := map[string]executionSnapshot{
		"t1": {State: hibernatorv1alpha1.StateRunning},
	}

	hook := s.executionProgressPostHook(prevSnapshot)
	require.NotNil(t, hook)
	require.NoError(t, hook(context.Background(), plan))
	require.Len(t, spy.requests, 2)
	assert.Equal(t, "slack", spy.requests[0].SinkName)
	assert.Equal(t, "telegram", spy.requests[1].SinkName)
}

func TestExecutionProgressPostHook_MultipleTargets_OnlyChangedDispatched(t *testing.T) {
	plan := &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns"},
		Status: hibernatorv1alpha1.HibernatePlanStatus{
			Executions: []hibernatorv1alpha1.ExecutionStatus{
				{Target: "t1", Executor: "eks", State: hibernatorv1alpha1.StateCompleted},
				{Target: "t2", Executor: "rds", State: hibernatorv1alpha1.StateRunning},
			},
		},
	}

	notif := hibernatorv1alpha1.HibernateNotification{
		ObjectMeta: metav1.ObjectMeta{Name: "n1", Namespace: "ns"},
		Spec: hibernatorv1alpha1.HibernateNotificationSpec{
			OnEvents: []hibernatorv1alpha1.NotificationEvent{hibernatorv1alpha1.EventExecutionProgress},
			Sinks: []hibernatorv1alpha1.NotificationSink{
				{Name: "slack", Type: hibernatorv1alpha1.SinkSlack, SecretRef: hibernatorv1alpha1.ObjectKeyReference{Name: "s1"}},
			},
		},
	}

	spy := &spyNotifier{}
	c := newHandlerFakeClient(plan)
	s := newHandlerState(plan, c)
	s.Notifier = spy
	s.PlanCtx.Notifications = []hibernatorv1alpha1.HibernateNotification{notif}

	// Only t1 changed (Running → Completed); t2 stayed Running.
	prevSnapshot := map[string]executionSnapshot{
		"t1": {State: hibernatorv1alpha1.StateRunning},
		"t2": {State: hibernatorv1alpha1.StateRunning},
	}

	hook := s.executionProgressPostHook(prevSnapshot)
	require.NotNil(t, hook)
	require.NoError(t, hook(context.Background(), plan))

	require.Len(t, spy.requests, 1, "only the changed target should dispatch")
	assert.Equal(t, "t1", spy.requests[0].Payload.TargetExecution.Name)
	assert.Equal(t, "Completed", spy.requests[0].Payload.TargetExecution.State)
}

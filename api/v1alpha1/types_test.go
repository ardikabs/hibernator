/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package v1alpha1

import (
	"encoding/json"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
)

func TestExecutionStrategyType_Constants(t *testing.T) {
	tests := []struct {
		name     string
		constant ExecutionStrategyType
		want     string
	}{
		{"Sequential", StrategySequential, "Sequential"},
		{"Parallel", StrategyParallel, "Parallel"},
		{"DAG", StrategyDAG, "DAG"},
		{"Staged", StrategyStaged, "Staged"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.constant) != tt.want {
				t.Errorf("got %q, want %q", tt.constant, tt.want)
			}
		})
	}
}

func TestBehaviorMode_Constants(t *testing.T) {
	tests := []struct {
		name     string
		constant BehaviorMode
		want     string
	}{
		{"Strict", BehaviorStrict, "Strict"},
		{"BestEffort", BehaviorBestEffort, "BestEffort"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.constant) != tt.want {
				t.Errorf("got %q, want %q", tt.constant, tt.want)
			}
		})
	}
}

func TestPlanPhase_Constants(t *testing.T) {
	tests := []struct {
		name     string
		constant PlanPhase
		want     string
	}{
		{"Pending", PhasePending, "Pending"},
		{"Active", PhaseActive, "Active"},
		{"Hibernating", PhaseHibernating, "Hibernating"},
		{"Hibernated", PhaseHibernated, "Hibernated"},
		{"WakingUp", PhaseWakingUp, "WakingUp"},
		{"Error", PhaseError, "Error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.constant) != tt.want {
				t.Errorf("got %q, want %q", tt.constant, tt.want)
			}
		})
	}
}

func TestExecutionState_Constants(t *testing.T) {
	tests := []struct {
		name     string
		constant ExecutionState
		want     string
	}{
		{"Pending", StatePending, "Pending"},
		{"Running", StateRunning, "Running"},
		{"Completed", StateCompleted, "Completed"},
		{"Failed", StateFailed, "Failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.constant) != tt.want {
				t.Errorf("got %q, want %q", tt.constant, tt.want)
			}
		})
	}
}

func TestOffHourWindow_Marshal(t *testing.T) {
	window := OffHourWindow{
		Start:      "20:00",
		End:        "06:00",
		DaysOfWeek: []string{"MON", "TUE", "WED"},
	}

	data, err := json.Marshal(window)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var result OffHourWindow
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if result.Start != window.Start {
		t.Errorf("Start: got %q, want %q", result.Start, window.Start)
	}
	if result.End != window.End {
		t.Errorf("End: got %q, want %q", result.End, window.End)
	}
	if len(result.DaysOfWeek) != len(window.DaysOfWeek) {
		t.Errorf("DaysOfWeek length: got %d, want %d", len(result.DaysOfWeek), len(window.DaysOfWeek))
	}
}

func TestSchedule_Marshal(t *testing.T) {
	schedule := Schedule{
		Timezone: "Asia/Jakarta",
		OffHours: []OffHourWindow{
			{Start: "20:00", End: "06:00", DaysOfWeek: []string{"MON", "FRI"}},
		},
	}

	data, err := json.Marshal(schedule)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var result Schedule
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if result.Timezone != schedule.Timezone {
		t.Errorf("Timezone: got %q, want %q", result.Timezone, schedule.Timezone)
	}
	if len(result.OffHours) != 1 {
		t.Errorf("OffHours length: got %d, want 1", len(result.OffHours))
	}
}

func TestDependency_Marshal(t *testing.T) {
	dep := Dependency{From: "frontend", To: "backend"}

	data, err := json.Marshal(dep)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var result Dependency
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if result.From != dep.From {
		t.Errorf("From: got %q, want %q", result.From, dep.From)
	}
	if result.To != dep.To {
		t.Errorf("To: got %q, want %q", result.To, dep.To)
	}
}

func TestStage_Marshal(t *testing.T) {
	maxConc := int32(3)
	stage := Stage{
		Name:           "compute",
		Parallel:       true,
		MaxConcurrency: &maxConc,
		Targets:        []string{"app1", "app2"},
	}

	data, err := json.Marshal(stage)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var result Stage
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if result.Name != stage.Name {
		t.Errorf("Name: got %q, want %q", result.Name, stage.Name)
	}
	if result.Parallel != stage.Parallel {
		t.Errorf("Parallel: got %v, want %v", result.Parallel, stage.Parallel)
	}
	if *result.MaxConcurrency != maxConc {
		t.Errorf("MaxConcurrency: got %d, want %d", *result.MaxConcurrency, maxConc)
	}
	if len(result.Targets) != 2 {
		t.Errorf("Targets length: got %d, want 2", len(result.Targets))
	}
}

func TestExecutionStrategy_Marshal(t *testing.T) {
	maxConc := int32(2)
	strategy := ExecutionStrategy{
		Type:           StrategyDAG,
		MaxConcurrency: &maxConc,
		Dependencies: []Dependency{
			{From: "a", To: "b"},
		},
	}

	data, err := json.Marshal(strategy)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var result ExecutionStrategy
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if result.Type != strategy.Type {
		t.Errorf("Type: got %q, want %q", result.Type, strategy.Type)
	}
	if *result.MaxConcurrency != maxConc {
		t.Errorf("MaxConcurrency: got %d, want %d", *result.MaxConcurrency, maxConc)
	}
	if len(result.Dependencies) != 1 {
		t.Errorf("Dependencies length: got %d, want 1", len(result.Dependencies))
	}
}

func TestBehavior_Marshal(t *testing.T) {
	behavior := Behavior{
		Mode:     BehaviorBestEffort,
		FailFast: false,
		Retries:  5,
	}

	data, err := json.Marshal(behavior)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var result Behavior
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if result.Mode != behavior.Mode {
		t.Errorf("Mode: got %q, want %q", result.Mode, behavior.Mode)
	}
	if result.FailFast != behavior.FailFast {
		t.Errorf("FailFast: got %v, want %v", result.FailFast, behavior.FailFast)
	}
	if result.Retries != behavior.Retries {
		t.Errorf("Retries: got %d, want %d", result.Retries, behavior.Retries)
	}
}

func TestConnectorRef_Marshal(t *testing.T) {
	ref := ConnectorRef{
		Kind:      "CloudProvider",
		Name:      "aws-prod",
		Namespace: "hibernator-system",
	}

	data, err := json.Marshal(ref)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var result ConnectorRef
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if result.Kind != ref.Kind {
		t.Errorf("Kind: got %q, want %q", result.Kind, ref.Kind)
	}
	if result.Name != ref.Name {
		t.Errorf("Name: got %q, want %q", result.Name, ref.Name)
	}
	if result.Namespace != ref.Namespace {
		t.Errorf("Namespace: got %q, want %q", result.Namespace, ref.Namespace)
	}
}

func TestTarget_Marshal(t *testing.T) {
	target := Target{
		Name: "my-cluster",
		Type: "eks",
		ConnectorRef: ConnectorRef{
			Kind: "K8SCluster",
			Name: "eks-prod",
		},
	}

	data, err := json.Marshal(target)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var result Target
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if result.Name != target.Name {
		t.Errorf("Name: got %q, want %q", result.Name, target.Name)
	}
	if result.Type != target.Type {
		t.Errorf("Type: got %q, want %q", result.Type, target.Type)
	}
	if result.ConnectorRef.Kind != target.ConnectorRef.Kind {
		t.Errorf("ConnectorRef.Kind: got %q, want %q", result.ConnectorRef.Kind, target.ConnectorRef.Kind)
	}
}

func TestExecutionStatus_Marshal(t *testing.T) {
	now := metav1.NewTime(time.Now())
	status := ExecutionStatus{
		Target:              "eks/my-cluster",
		Executor:            "eks",
		State:               StateRunning,
		StartedAt:           &now,
		Attempts:            2,
		Message:             "Scaling node groups",
		JobRef:              "hibernator-system/runner-abc123",
		LogsRef:             "stream-123",
		RestoreRef:          "s3://bucket/restore/abc.json",
		ServiceAccountRef:   "hibernator-system/runner-sa-abc",
		ConnectorSecretRef:  "hibernator-system/conn-abc",
		RestoreConfigMapRef: "hibernator-system/restore-abc",
	}

	data, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var result ExecutionStatus
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if result.Target != status.Target {
		t.Errorf("Target: got %q, want %q", result.Target, status.Target)
	}
	if result.State != status.State {
		t.Errorf("State: got %q, want %q", result.State, status.State)
	}
	if result.Attempts != status.Attempts {
		t.Errorf("Attempts: got %d, want %d", result.Attempts, status.Attempts)
	}
	if result.JobRef != status.JobRef {
		t.Errorf("JobRef: got %q, want %q", result.JobRef, status.JobRef)
	}
}

func TestHibernatePlanStatus_Marshal(t *testing.T) {
	now := metav1.NewTime(time.Now())
	status := HibernatePlanStatus{
		Phase:              PhaseHibernating,
		LastTransitionTime: &now,
		Executions: []ExecutionStatus{
			{Target: "rds/db1", State: StateCompleted},
		},
		ObservedGeneration: 5,
		RetryCount:         2,
		LastRetryTime:      &now,
		ErrorMessage:       "",
	}

	data, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var result HibernatePlanStatus
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if result.Phase != status.Phase {
		t.Errorf("Phase: got %q, want %q", result.Phase, status.Phase)
	}
	if result.ObservedGeneration != status.ObservedGeneration {
		t.Errorf("ObservedGeneration: got %d, want %d", result.ObservedGeneration, status.ObservedGeneration)
	}
	if len(result.Executions) != 1 {
		t.Errorf("Executions length: got %d, want 1", len(result.Executions))
	}
	if result.RetryCount != status.RetryCount {
		t.Errorf("RetryCount: got %d, want %d", result.RetryCount, status.RetryCount)
	}
}

func TestHibernatePlan_TypeMeta(t *testing.T) {
	plan := HibernatePlan{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "hibernator.ardikabs.com/v1alpha1",
			Kind:       "HibernatePlan",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-plan",
			Namespace: "default",
		},
	}

	if plan.Kind != "HibernatePlan" {
		t.Errorf("Kind: got %q, want %q", plan.Kind, "HibernatePlan")
	}
	if plan.Name != "test-plan" {
		t.Errorf("Name: got %q, want %q", plan.Name, "test-plan")
	}
}

func TestHibernatePlanList_Items(t *testing.T) {
	list := HibernatePlanList{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "hibernator.ardikabs.com/v1alpha1",
			Kind:       "HibernatePlanList",
		},
		Items: []HibernatePlan{
			{ObjectMeta: metav1.ObjectMeta{Name: "plan1"}},
			{ObjectMeta: metav1.ObjectMeta{Name: "plan2"}},
		},
	}

	if len(list.Items) != 2 {
		t.Errorf("Items length: got %d, want 2", len(list.Items))
	}
	if list.Items[0].Name != "plan1" {
		t.Errorf("Items[0].Name: got %q, want %q", list.Items[0].Name, "plan1")
	}
}

func TestExecution_Marshal(t *testing.T) {
	exec := Execution{
		Strategy: ExecutionStrategy{
			Type: StrategySequential,
		},
	}

	data, err := json.Marshal(exec)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var result Execution
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if result.Strategy.Type != exec.Strategy.Type {
		t.Errorf("Strategy.Type: got %q, want %q", result.Strategy.Type, exec.Strategy.Type)
	}
}

func TestHibernatePlanSpec_Complete(t *testing.T) {
	maxConc := int32(2)
	spec := HibernatePlanSpec{
		Schedule: Schedule{
			Timezone: "UTC",
			OffHours: []OffHourWindow{
				{Start: "22:00", End: "06:00", DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"}},
			},
		},
		Execution: Execution{
			Strategy: ExecutionStrategy{
				Type:           StrategyParallel,
				MaxConcurrency: &maxConc,
			},
		},
		Behavior: Behavior{
			Mode:     BehaviorStrict,
			FailFast: true,
			Retries:  3,
		},
		Targets: []Target{
			{
				Name: "cluster",
				Type: "eks",
				ConnectorRef: ConnectorRef{
					Kind: "K8SCluster",
					Name: "prod-cluster",
				},
			},
		},
	}

	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var result HibernatePlanSpec
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if result.Schedule.Timezone != spec.Schedule.Timezone {
		t.Errorf("Schedule.Timezone: got %q, want %q", result.Schedule.Timezone, spec.Schedule.Timezone)
	}
	if result.Execution.Strategy.Type != spec.Execution.Strategy.Type {
		t.Errorf("Execution.Strategy.Type: got %q, want %q", result.Execution.Strategy.Type, spec.Execution.Strategy.Type)
	}
	if result.Behavior.Mode != spec.Behavior.Mode {
		t.Errorf("Behavior.Mode: got %q, want %q", result.Behavior.Mode, spec.Behavior.Mode)
	}
	if len(result.Targets) != 1 {
		t.Errorf("Targets length: got %d, want 1", len(result.Targets))
	}
}

// CloudProvider type tests

func TestCloudProviderType_Constants(t *testing.T) {
	if string(CloudProviderAWS) != "aws" {
		t.Errorf("CloudProviderAWS: got %q, want %q", CloudProviderAWS, "aws")
	}
}

func TestAWSAuth_Marshal(t *testing.T) {
	auth := AWSAuth{
		ServiceAccount: &ServiceAccountAuth{},
	}

	data, err := json.Marshal(auth)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var result AWSAuth
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if result.ServiceAccount == nil {
		t.Fatal("ServiceAccount is nil")
	}
	// AssumeRoleArn is now at AWS spec level, not in ServiceAccountAuth
}

func TestAWSAuth_StaticCredentials(t *testing.T) {
	auth := AWSAuth{
		Static: &StaticAuth{
			SecretRef: SecretReference{
				Name:      "aws-creds",
				Namespace: "secrets",
			},
		},
	}

	data, err := json.Marshal(auth)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var result AWSAuth
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if result.Static == nil {
		t.Fatal("Static is nil")
	}
	if result.Static.SecretRef.Name != auth.Static.SecretRef.Name {
		t.Errorf("SecretRef.Name: got %q, want %q", result.Static.SecretRef.Name, auth.Static.SecretRef.Name)
	}
}

func TestAWSConfig_Marshal(t *testing.T) {
	cfg := AWSConfig{
		AccountId: "123456789012",
		Region:    "ap-southeast-3",
		Auth: AWSAuth{
			ServiceAccount: &ServiceAccountAuth{},
		},
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var result AWSConfig
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if result.AccountId != cfg.AccountId {
		t.Errorf("AccountId: got %q, want %q", result.AccountId, cfg.AccountId)
	}
	if result.Region != cfg.Region {
		t.Errorf("Region: got %q, want %q", result.Region, cfg.Region)
	}
}

func TestCloudProviderSpec_Marshal(t *testing.T) {
	spec := CloudProviderSpec{
		Type: CloudProviderAWS,
		AWS: &AWSConfig{
			AccountId: "123456789012",
			Region:    "us-east-1",
			Auth: AWSAuth{
				ServiceAccount: &ServiceAccountAuth{},
			},
		},
	}

	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var result CloudProviderSpec
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if result.Type != spec.Type {
		t.Errorf("Type: got %q, want %q", result.Type, spec.Type)
	}
	if result.AWS == nil {
		t.Fatal("AWS is nil")
	}
}

func TestCloudProviderStatus_Marshal(t *testing.T) {
	now := metav1.NewTime(time.Now())
	status := CloudProviderStatus{
		Ready:         true,
		Message:       "Credentials validated",
		LastValidated: &now,
	}

	data, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var result CloudProviderStatus
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if result.Ready != status.Ready {
		t.Errorf("Ready: got %v, want %v", result.Ready, status.Ready)
	}
	if result.Message != status.Message {
		t.Errorf("Message: got %q, want %q", result.Message, status.Message)
	}
}

func TestCloudProvider_TypeMeta(t *testing.T) {
	cp := CloudProvider{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "hibernator.ardikabs.com/v1alpha1",
			Kind:       "CloudProvider",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "aws-prod",
			Namespace: "hibernator-system",
		},
	}

	if cp.Kind != "CloudProvider" {
		t.Errorf("Kind: got %q, want %q", cp.Kind, "CloudProvider")
	}
	if cp.Name != "aws-prod" {
		t.Errorf("Name: got %q, want %q", cp.Name, "aws-prod")
	}
}

// K8SCluster type tests

func TestK8SClusterType_Constants(t *testing.T) {
	tests := []struct {
		name     string
		constant K8SClusterType
		want     string
	}{
		{"EKS", ClusterTypeEKS, "eks"},
		{"GKE", ClusterTypeGKE, "gke"},
		{"K8S", ClusterTypeK8S, "k8s"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.constant) != tt.want {
				t.Errorf("got %q, want %q", tt.constant, tt.want)
			}
		})
	}
}

func TestProviderRef_Marshal(t *testing.T) {
	ref := ProviderRef{
		Name:      "aws-prod",
		Namespace: "hibernator-system",
	}

	data, err := json.Marshal(ref)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var result ProviderRef
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if result.Name != ref.Name {
		t.Errorf("Name: got %q, want %q", result.Name, ref.Name)
	}
	if result.Namespace != ref.Namespace {
		t.Errorf("Namespace: got %q, want %q", result.Namespace, ref.Namespace)
	}
}

func TestEKSConfig_Marshal(t *testing.T) {
	cfg := EKSConfig{
		Name:   "prod-cluster",
		Region: "ap-southeast-3",
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var result EKSConfig
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if result.Name != cfg.Name {
		t.Errorf("Name: got %q, want %q", result.Name, cfg.Name)
	}
	if result.Region != cfg.Region {
		t.Errorf("Region: got %q, want %q", result.Region, cfg.Region)
	}
}

func TestGKEConfig_Marshal(t *testing.T) {
	cfg := GKEConfig{
		Name:     "prod-cluster",
		Project:  "my-project",
		Location: "us-central1-a",
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var result GKEConfig
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if result.Name != cfg.Name {
		t.Errorf("Name: got %q, want %q", result.Name, cfg.Name)
	}
	if result.Project != cfg.Project {
		t.Errorf("Project: got %q, want %q", result.Project, cfg.Project)
	}
	if result.Location != cfg.Location {
		t.Errorf("Location: got %q, want %q", result.Location, cfg.Location)
	}
}

func TestK8SAccessConfig_Marshal(t *testing.T) {
	cfg := K8SAccessConfig{
		KubeconfigRef: &KubeconfigRef{
			Name:      "cluster-kubeconfig",
			Namespace: "secrets",
		},
		InCluster: false,
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var result K8SAccessConfig
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if result.KubeconfigRef == nil {
		t.Fatal("KubeconfigRef is nil")
	}
	if result.KubeconfigRef.Name != cfg.KubeconfigRef.Name {
		t.Errorf("KubeconfigRef.Name: got %q, want %q", result.KubeconfigRef.Name, cfg.KubeconfigRef.Name)
	}
}

func TestK8SAccessConfig_InCluster(t *testing.T) {
	cfg := K8SAccessConfig{
		InCluster: true,
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var result K8SAccessConfig
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if !result.InCluster {
		t.Error("InCluster should be true")
	}
}

func TestK8SClusterSpec_EKS(t *testing.T) {
	spec := K8SClusterSpec{
		ProviderRef: &ProviderRef{
			Name:      "aws-prod",
			Namespace: "hibernator-system",
		},
		EKS: &EKSConfig{
			Name:   "my-cluster",
			Region: "us-west-2",
		},
	}

	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var result K8SClusterSpec
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if result.ProviderRef == nil {
		t.Fatal("ProviderRef is nil")
	}
	if result.EKS == nil {
		t.Fatal("EKS is nil")
	}
	if result.EKS.Name != spec.EKS.Name {
		t.Errorf("EKS.Name: got %q, want %q", result.EKS.Name, spec.EKS.Name)
	}
}

func TestK8SClusterSpec_GKE(t *testing.T) {
	spec := K8SClusterSpec{
		GKE: &GKEConfig{
			Name:     "gke-cluster",
			Project:  "my-gcp-project",
			Location: "us-central1",
		},
	}

	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var result K8SClusterSpec
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if result.GKE == nil {
		t.Fatal("GKE is nil")
	}
	if result.GKE.Project != spec.GKE.Project {
		t.Errorf("GKE.Project: got %q, want %q", result.GKE.Project, spec.GKE.Project)
	}
}

func TestK8SCluster_TypeMeta(t *testing.T) {
	cluster := K8SCluster{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "hibernator.ardikabs.com/v1alpha1",
			Kind:       "K8SCluster",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "prod-eks",
			Namespace: "hibernator-system",
		},
	}

	if cluster.Kind != "K8SCluster" {
		t.Errorf("Kind: got %q, want %q", cluster.Kind, "K8SCluster")
	}
	if cluster.Name != "prod-eks" {
		t.Errorf("Name: got %q, want %q", cluster.Name, "prod-eks")
	}
}

// Parameters type tests

func TestParameters_MarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		params  *Parameters
		want    string
		wantErr bool
	}{
		{
			name: "with data",
			params: &Parameters{
				Raw: []byte(`{"namespace":{"literals":["default"]},"workloadSelector":{"matchLabels":{"app":"test"}}}`),
			},
			want:    `{"namespace":{"literals":["default"]},"workloadSelector":{"matchLabels":{"app":"test"}}}`,
			wantErr: false,
		},
		{
			name:    "nil parameters",
			params:  nil,
			want:    `null`, // Direct marshal of nil pointer returns null, not {}
			wantErr: false,
		},
		{
			name:    "empty raw",
			params:  &Parameters{Raw: nil},
			want:    `{}`,
			wantErr: false,
		},
		{
			name:    "empty json object",
			params:  &Parameters{Raw: []byte(`{}`)},
			want:    `{}`,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(tt.params)
			if (err != nil) != tt.wantErr {
				t.Errorf("MarshalJSON() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if string(got) != tt.want {
				t.Errorf("MarshalJSON() = %s, want %s", string(got), tt.want)
			}
		})
	}
}

func TestParameters_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantRaw string
		wantErr bool
	}{
		{
			name:    "workloadscaler parameters",
			input:   `{"namespace":{"literals":["default"]},"workloadSelector":{"matchLabels":{"app":"echoserver"}}}`,
			wantRaw: `{"namespace":{"literals":["default"]},"workloadSelector":{"matchLabels":{"app":"echoserver"}}}`,
			wantErr: false,
		},
		{
			name:    "rds parameters",
			input:   `{"selector":{"instanceIds":["db-1","db-2"]},"snapshotBeforeStop":true}`,
			wantRaw: `{"selector":{"instanceIds":["db-1","db-2"]},"snapshotBeforeStop":true}`,
			wantErr: false,
		},
		{
			name:    "empty object",
			input:   `{}`,
			wantRaw: `{}`,
			wantErr: false,
		},
		{
			name:    "null",
			input:   `null`,
			wantRaw: ``,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var params Parameters
			err := json.Unmarshal([]byte(tt.input), &params)
			if (err != nil) != tt.wantErr {
				t.Errorf("UnmarshalJSON() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if string(params.Raw) != tt.wantRaw {
				t.Errorf("UnmarshalJSON() Raw = %s, want %s", string(params.Raw), tt.wantRaw)
			}
		})
	}
}

func TestParameters_RoundTrip(t *testing.T) {
	original := &Parameters{
		Raw: []byte(`{"namespace":{"literals":["default","kube-system"]},"includedGroups":["Deployment","StatefulSet"]}`),
	}

	// Marshal
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	// Unmarshal
	var decoded Parameters
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	// Compare
	if string(decoded.Raw) != string(original.Raw) {
		t.Errorf("Round trip failed: got %s, want %s", string(decoded.Raw), string(original.Raw))
	}
}

func TestTarget_WithParameters(t *testing.T) {
	target := Target{
		Name: "test-scaler",
		Type: "workloadscaler",
		ConnectorRef: ConnectorRef{
			Kind: "K8SCluster",
			Name: "local",
		},
		Parameters: &Parameters{
			Raw: []byte(`{"namespace":{"literals":["default"]},"workloadSelector":{"matchLabels":{"app":"test"}}}`),
		},
	}

	// Marshal target
	data, err := json.Marshal(target)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	// Unmarshal target
	var decoded Target
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	// Verify parameters preserved
	if decoded.Parameters == nil {
		t.Fatal("Parameters is nil after unmarshal")
	}
	if len(decoded.Parameters.Raw) == 0 {
		t.Fatal("Parameters.Raw is empty after unmarshal")
	}

	// Verify we can parse the nested structure
	var parsedParams map[string]interface{}
	if err := json.Unmarshal(decoded.Parameters.Raw, &parsedParams); err != nil {
		t.Fatalf("Failed to parse parameters: %v", err)
	}

	// Check namespace field exists
	if _, ok := parsedParams["namespace"]; !ok {
		t.Error("Expected 'namespace' field in parameters")
	}

	// Check workloadSelector field exists
	if _, ok := parsedParams["workloadSelector"]; !ok {
		t.Error("Expected 'workloadSelector' field in parameters")
	}
}

func TestTarget_WithNilParameters(t *testing.T) {
	target := Target{
		Name: "test-target",
		Type: "noop",
		ConnectorRef: ConnectorRef{
			Kind: "CloudProvider",
			Name: "aws-dev",
		},
		Parameters: nil,
	}

	// Marshal
	data, err := json.Marshal(target)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	// Unmarshal
	var decoded Target
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	// Verify parameters is nil (omitted)
	if decoded.Parameters != nil {
		t.Errorf("Expected nil parameters, got %v", decoded.Parameters)
	}
}

func TestTarget_YAMLParsing(t *testing.T) {
	tests := []struct {
		name           string
		yaml           string
		expectedName   string
		expectedType   string
		expectedKind   string
		expectedParams string // expected JSON in Parameters.Raw
	}{
		{
			name: "EKS target with parameters",
			yaml: `name: eks-cluster
type: eks
connectorRef:
  kind: K8SCluster
  name: prod-cluster
parameters:
  clusterName: production-cluster
  nodeGroups:
  - name: app-nodes
  - name: worker-nodes`,
			expectedName:   "eks-cluster",
			expectedType:   "eks",
			expectedKind:   "K8SCluster",
			expectedParams: `{"clusterName":"production-cluster","nodeGroups":[{"name":"app-nodes"},{"name":"worker-nodes"}]}`,
		},
		{
			name: "RDS target with parameters",
			yaml: `name: database-rds
type: rds
connectorRef:
  kind: CloudProvider
  name: aws-prod
parameters:
  snapshotBeforeStop: true
  selector:
    instanceIds:
    - production-db`,
			expectedName:   "database-rds",
			expectedType:   "rds",
			expectedKind:   "CloudProvider",
			expectedParams: `{"selector":{"instanceIds":["production-db"]},"snapshotBeforeStop":true}`,
		},
		{
			name: "EC2 target with parameters",
			yaml: `name: frontend-instances
type: ec2
connectorRef:
  kind: CloudProvider
  name: aws-prod
parameters:
  selector:
    tags:
      Environment: production
      Component: frontend`,
			expectedName:   "frontend-instances",
			expectedType:   "ec2",
			expectedKind:   "CloudProvider",
			expectedParams: `{"selector":{"tags":{"Component":"frontend","Environment":"production"}}}`,
		},
		{
			name: "Karpenter target with parameters",
			yaml: `name: karpenter-nodes
type: karpenter
connectorRef:
  kind: K8SCluster
  name: eks-prod
parameters:
  nodePools:
  - default
  - spot`,
			expectedName:   "karpenter-nodes",
			expectedType:   "karpenter",
			expectedKind:   "K8SCluster",
			expectedParams: `{"nodePools":["default","spot"]}`,
		},
		{
			name: "WorkloadScaler target with parameters",
			yaml: `name: scale-deployments
type: workloadscaler
connectorRef:
  kind: K8SCluster
  name: local
parameters:
  includedGroups:
    - Deployment
    - StatefulSet
  namespace:
    literals:
      - app-team-a
      - app-team-b
  workloadSelector:
    matchLabels:
      app.kubernetes.io/part-of: payments`,
			expectedName:   "scale-deployments",
			expectedType:   "workloadscaler",
			expectedKind:   "K8SCluster",
			expectedParams: `{"includedGroups":["Deployment","StatefulSet"],"namespace":{"literals":["app-team-a","app-team-b"]},"workloadSelector":{"matchLabels":{"app.kubernetes.io/part-of":"payments"}}}`,
		},
		{
			name: "Target without parameters",
			yaml: `name: simple-target
type: noop
connectorRef:
  kind: CloudProvider
  name: noop-provider`,
			expectedName:   "simple-target",
			expectedType:   "noop",
			expectedKind:   "CloudProvider",
			expectedParams: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var target Target
			err := yaml.Unmarshal([]byte(tt.yaml), &target)
			if err != nil {
				t.Fatalf("YAML Unmarshal failed: %v", err)
			}

			// Verify basic fields
			if target.Name != tt.expectedName {
				t.Errorf("Name: got %q, want %q", target.Name, tt.expectedName)
			}
			if target.Type != tt.expectedType {
				t.Errorf("Type: got %q, want %q", target.Type, tt.expectedType)
			}
			if target.ConnectorRef.Kind != tt.expectedKind {
				t.Errorf("ConnectorRef.Kind: got %q, want %q", target.ConnectorRef.Kind, tt.expectedKind)
			}

			// Verify parameters
			if tt.expectedParams == "" {
				if target.Parameters != nil {
					t.Errorf("Expected nil parameters, got %v", target.Parameters)
				}
			} else {
				if target.Parameters == nil {
					t.Fatal("Parameters is nil, expected data")
				}
				if len(target.Parameters.Raw) == 0 {
					t.Fatal("Parameters.Raw is empty, expected data")
				}

				// Unmarshal both to maps for comparison (to ignore field order)
				var gotMap, wantMap map[string]interface{}
				if err := json.Unmarshal(target.Parameters.Raw, &gotMap); err != nil {
					t.Fatalf("Failed to unmarshal parsed parameters: %v", err)
				}
				if err := json.Unmarshal([]byte(tt.expectedParams), &wantMap); err != nil {
					t.Fatalf("Failed to unmarshal expected parameters: %v", err)
				}

				// Compare as JSON strings (re-marshal for consistent formatting)
				gotJSON, _ := json.Marshal(gotMap)
				wantJSON, _ := json.Marshal(wantMap)

				if string(gotJSON) != string(wantJSON) {
					t.Errorf("Parameters.Raw:\ngot:  %s\nwant: %s", gotJSON, wantJSON)
				}
			}
		})
	}
}

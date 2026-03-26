/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package v1alpha1

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

// DeepCopy tests for HibernatePlan types

func TestHibernatePlan_DeepCopy(t *testing.T) {
	maxConc := int32(3)
	original := &HibernatePlan{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "hibernator.ardikabs.com/v1alpha1",
			Kind:       "HibernatePlan",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-plan",
			Namespace: "default",
		},
		Spec: HibernatePlanSpec{
			Schedule: Schedule{
				Timezone: "UTC",
				OffHours: []OffHourWindow{
					{Start: "20:00", End: "06:00", DaysOfWeek: []string{"MON", "TUE"}},
				},
			},
			Execution: Execution{
				Strategy: ExecutionStrategy{
					Type:           StrategyDAG,
					MaxConcurrency: &maxConc,
					Dependencies: []Dependency{
						{From: "a", To: "b"},
					},
				},
			},
			Targets: []Target{
				{Name: "target1", Type: "eks", ConnectorRef: ConnectorRef{Kind: "K8SCluster", Name: "cluster1"}},
			},
		},
	}

	copy := original.DeepCopy()

	if copy.Name != original.Name {
		t.Errorf("DeepCopy Name: got %q, want %q", copy.Name, original.Name)
	}
	if copy.Spec.Schedule.Timezone != original.Spec.Schedule.Timezone {
		t.Errorf("DeepCopy Schedule.Timezone: got %q, want %q", copy.Spec.Schedule.Timezone, original.Spec.Schedule.Timezone)
	}
	if len(copy.Spec.Targets) != len(original.Spec.Targets) {
		t.Errorf("DeepCopy Targets length: got %d, want %d", len(copy.Spec.Targets), len(original.Spec.Targets))
	}

	// Verify it's a deep copy by modifying the copy
	copy.Name = "modified"
	if original.Name == copy.Name {
		t.Error("DeepCopy should create independent copy, but modification affected original")
	}
}

func TestHibernatePlan_DeepCopyObject(t *testing.T) {
	original := &HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-plan",
		},
	}

	copyObj := original.DeepCopyObject()
	copy, ok := copyObj.(*HibernatePlan)
	if !ok {
		t.Fatal("DeepCopyObject should return *HibernatePlan")
	}
	if copy.Name != original.Name {
		t.Errorf("DeepCopyObject Name: got %q, want %q", copy.Name, original.Name)
	}
}

func TestHibernatePlanList_DeepCopy(t *testing.T) {
	original := &HibernatePlanList{
		Items: []HibernatePlan{
			{ObjectMeta: metav1.ObjectMeta{Name: "plan1"}},
			{ObjectMeta: metav1.ObjectMeta{Name: "plan2"}},
		},
	}

	copy := original.DeepCopy()

	if len(copy.Items) != len(original.Items) {
		t.Errorf("DeepCopy Items length: got %d, want %d", len(copy.Items), len(original.Items))
	}

	// Verify it's a deep copy
	copy.Items[0].Name = "modified"
	if original.Items[0].Name == copy.Items[0].Name {
		t.Error("DeepCopy should create independent copy")
	}
}

func TestHibernatePlanList_DeepCopyObject(t *testing.T) {
	original := &HibernatePlanList{
		Items: []HibernatePlan{
			{ObjectMeta: metav1.ObjectMeta{Name: "plan1"}},
		},
	}

	copyObj := original.DeepCopyObject()
	copy, ok := copyObj.(*HibernatePlanList)
	if !ok {
		t.Fatal("DeepCopyObject should return *HibernatePlanList")
	}
	if len(copy.Items) != 1 {
		t.Errorf("DeepCopyObject Items length: got %d, want 1", len(copy.Items))
	}
}

func TestHibernatePlanSpec_DeepCopyInto(t *testing.T) {
	maxConc := int32(5)
	original := HibernatePlanSpec{
		Schedule: Schedule{
			Timezone: "Asia/Jakarta",
			OffHours: []OffHourWindow{
				{Start: "22:00", End: "06:00", DaysOfWeek: []string{"MON"}},
			},
		},
		Execution: Execution{
			Strategy: ExecutionStrategy{
				Type:           StrategyParallel,
				MaxConcurrency: &maxConc,
			},
		},
		Targets: []Target{
			{Name: "t1", Type: "ec2", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}},
		},
	}

	var copy HibernatePlanSpec
	original.DeepCopyInto(&copy)

	if copy.Schedule.Timezone != original.Schedule.Timezone {
		t.Errorf("DeepCopyInto Timezone: got %q, want %q", copy.Schedule.Timezone, original.Schedule.Timezone)
	}
	if *copy.Execution.Strategy.MaxConcurrency != maxConc {
		t.Errorf("DeepCopyInto MaxConcurrency: got %d, want %d", *copy.Execution.Strategy.MaxConcurrency, maxConc)
	}
}

func TestSchedule_DeepCopyInto(t *testing.T) {
	original := Schedule{
		Timezone: "UTC",
		OffHours: []OffHourWindow{
			{Start: "20:00", End: "06:00", DaysOfWeek: []string{"MON", "TUE", "WED"}},
			{Start: "00:00", End: "23:59", DaysOfWeek: []string{"SAT", "SUN"}},
		},
	}

	var copy Schedule
	original.DeepCopyInto(&copy)

	if len(copy.OffHours) != len(original.OffHours) {
		t.Errorf("DeepCopyInto OffHours length: got %d, want %d", len(copy.OffHours), len(original.OffHours))
	}

	// Modify copy and verify original is unchanged
	copy.OffHours[0].Start = "19:00"
	if original.OffHours[0].Start == copy.OffHours[0].Start {
		t.Error("DeepCopyInto should create independent copy")
	}
}

func TestExecutionStrategy_DeepCopyInto(t *testing.T) {
	maxConc := int32(2)
	original := ExecutionStrategy{
		Type:           StrategyDAG,
		MaxConcurrency: &maxConc,
		Dependencies: []Dependency{
			{From: "frontend", To: "backend"},
			{From: "backend", To: "database"},
		},
		Stages: []Stage{
			{Name: "stage1", Targets: []string{"a", "b"}, Parallel: true},
		},
	}

	var copy ExecutionStrategy
	original.DeepCopyInto(&copy)

	if len(copy.Dependencies) != len(original.Dependencies) {
		t.Errorf("DeepCopyInto Dependencies length: got %d, want %d", len(copy.Dependencies), len(original.Dependencies))
	}
	if len(copy.Stages) != len(original.Stages) {
		t.Errorf("DeepCopyInto Stages length: got %d, want %d", len(copy.Stages), len(original.Stages))
	}
}

func TestExecutionStatus_DeepCopyInto(t *testing.T) {
	now := metav1.Now()
	original := ExecutionStatus{
		Target:              "eks/cluster1",
		State:               StateCompleted,
		StartedAt:           &now,
		FinishedAt:          &now,
		Attempts:            3,
		Message:             "Success",
		JobRef:              "ns/job1",
		RestoreConfigMapRef: "ns/restore-cm",
	}

	var copy ExecutionStatus
	original.DeepCopyInto(&copy)

	if copy.Target != original.Target {
		t.Errorf("DeepCopyInto Target: got %q, want %q", copy.Target, original.Target)
	}
	if copy.State != original.State {
		t.Errorf("DeepCopyInto State: got %q, want %q", copy.State, original.State)
	}
	if copy.Attempts != original.Attempts {
		t.Errorf("DeepCopyInto Attempts: got %d, want %d", copy.Attempts, original.Attempts)
	}
}

func TestHibernatePlanStatus_DeepCopyInto(t *testing.T) {
	now := metav1.Now()
	original := HibernatePlanStatus{
		Phase:              PhaseHibernated,
		LastTransitionTime: &now,
		Executions: []ExecutionStatus{
			{Target: "rds/db1", State: StateCompleted},
			{Target: "eks/cluster1", State: StateRunning},
		},
		ObservedGeneration: 5,
		RetryCount:         2,
	}

	var copy HibernatePlanStatus
	original.DeepCopyInto(&copy)

	if copy.Phase != original.Phase {
		t.Errorf("DeepCopyInto Phase: got %q, want %q", copy.Phase, original.Phase)
	}
	if len(copy.Executions) != len(original.Executions) {
		t.Errorf("DeepCopyInto Executions length: got %d, want %d", len(copy.Executions), len(original.Executions))
	}
}

// DeepCopy tests for CloudProvider types

func TestCloudProvider_DeepCopy(t *testing.T) {
	now := metav1.Now()
	original := &CloudProvider{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "aws-prod",
			Namespace: "hibernator-system",
		},
		Spec: CloudProviderSpec{
			Type: CloudProviderAWS,
			AWS: &AWSConfig{
				AccountId: "123456789012",
				Region:    "us-east-1",
				Auth: AWSAuth{
					ServiceAccount: &ServiceAccountAuth{},
				},
			},
		},
		Status: CloudProviderStatus{
			Ready:         true,
			Message:       "Ready",
			LastValidated: &now,
		},
	}

	copy := original.DeepCopy()

	if copy.Name != original.Name {
		t.Errorf("DeepCopy Name: got %q, want %q", copy.Name, original.Name)
	}
	if copy.Spec.AWS.AccountId != original.Spec.AWS.AccountId {
		t.Errorf("DeepCopy AWS.AccountId: got %q, want %q", copy.Spec.AWS.AccountId, original.Spec.AWS.AccountId)
	}

	// Verify deep copy
	copy.Spec.AWS.Region = "modified"
	if original.Spec.AWS.Region == copy.Spec.AWS.Region {
		t.Error("DeepCopy should create independent copy")
	}
}

func TestCloudProvider_DeepCopyObject(t *testing.T) {
	original := &CloudProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "aws-test"},
	}

	copyObj := original.DeepCopyObject()
	copy, ok := copyObj.(*CloudProvider)
	if !ok {
		t.Fatal("DeepCopyObject should return *CloudProvider")
	}
	if copy.Name != original.Name {
		t.Errorf("DeepCopyObject Name: got %q, want %q", copy.Name, original.Name)
	}
}

func TestCloudProviderList_DeepCopy(t *testing.T) {
	original := &CloudProviderList{
		Items: []CloudProvider{
			{ObjectMeta: metav1.ObjectMeta{Name: "aws1"}},
			{ObjectMeta: metav1.ObjectMeta{Name: "aws2"}},
		},
	}

	copy := original.DeepCopy()

	if len(copy.Items) != len(original.Items) {
		t.Errorf("DeepCopy Items length: got %d, want %d", len(copy.Items), len(original.Items))
	}
}

// DeepCopy tests for K8SCluster types

func TestK8SCluster_DeepCopy(t *testing.T) {
	original := &K8SCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "eks-prod",
			Namespace: "hibernator-system",
		},
		Spec: K8SClusterSpec{
			ProviderRef: &ProviderRef{
				Name:      "aws-prod",
				Namespace: "hibernator-system",
			},
			EKS: &EKSConfig{
				Name:   "prod-cluster",
				Region: "us-west-2",
			},
		},
	}

	copy := original.DeepCopy()

	if copy.Name != original.Name {
		t.Errorf("DeepCopy Name: got %q, want %q", copy.Name, original.Name)
	}
	if copy.Spec.EKS.Name != original.Spec.EKS.Name {
		t.Errorf("DeepCopy EKS.Name: got %q, want %q", copy.Spec.EKS.Name, original.Spec.EKS.Name)
	}

	// Verify deep copy
	copy.Spec.EKS.Region = "modified"
	if original.Spec.EKS.Region == copy.Spec.EKS.Region {
		t.Error("DeepCopy should create independent copy")
	}
}

func TestK8SCluster_DeepCopyObject(t *testing.T) {
	original := &K8SCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "gke-test"},
	}

	copyObj := original.DeepCopyObject()
	copy, ok := copyObj.(*K8SCluster)
	if !ok {
		t.Fatal("DeepCopyObject should return *K8SCluster")
	}
	if copy.Name != original.Name {
		t.Errorf("DeepCopyObject Name: got %q, want %q", copy.Name, original.Name)
	}
}

func TestK8SClusterList_DeepCopy(t *testing.T) {
	original := &K8SClusterList{
		Items: []K8SCluster{
			{ObjectMeta: metav1.ObjectMeta{Name: "eks1"}},
			{ObjectMeta: metav1.ObjectMeta{Name: "gke1"}},
		},
	}

	copy := original.DeepCopy()

	if len(copy.Items) != len(original.Items) {
		t.Errorf("DeepCopy Items length: got %d, want %d", len(copy.Items), len(original.Items))
	}
}

func TestK8SClusterSpec_GKE_DeepCopy(t *testing.T) {
	original := K8SClusterSpec{
		GKE: &GKEConfig{
			Name:     "gke-cluster",
			Project:  "my-project",
			Location: "us-central1",
		},
		K8S: &K8SAccessConfig{
			KubeconfigRef: &KubeconfigRef{
				Name:      "kubeconfig",
				Namespace: "secrets",
			},
			InCluster: false,
		},
	}

	copy := original.DeepCopy()

	if copy.GKE.Name != original.GKE.Name {
		t.Errorf("DeepCopy GKE.Name: got %q, want %q", copy.GKE.Name, original.GKE.Name)
	}
	if copy.K8S.KubeconfigRef.Name != original.K8S.KubeconfigRef.Name {
		t.Errorf("DeepCopy K8S.KubeconfigRef.Name: got %q, want %q", copy.K8S.KubeconfigRef.Name, original.K8S.KubeconfigRef.Name)
	}
}

func TestAWSConfig_DeepCopy(t *testing.T) {
	original := AWSConfig{
		AccountId: "123456789012",
		Region:    "ap-southeast-3",
		Auth: AWSAuth{
			Static: &StaticAuth{
				SecretRef: SecretReference{
					Name:      "aws-creds",
					Namespace: "secrets",
				},
			},
		},
	}

	copy := original.DeepCopy()

	if copy.AccountId != original.AccountId {
		t.Errorf("DeepCopy AccountId: got %q, want %q", copy.AccountId, original.AccountId)
	}
	if copy.Auth.Static.SecretRef.Name != original.Auth.Static.SecretRef.Name {
		t.Errorf("DeepCopy Auth.Static.SecretRef.Name: got %q, want %q", copy.Auth.Static.SecretRef.Name, original.Auth.Static.SecretRef.Name)
	}
}

func TestTarget_DeepCopy(t *testing.T) {
	original := Target{
		Name: "my-target",
		Type: "eks",
		ConnectorRef: ConnectorRef{
			Kind:      "K8SCluster",
			Name:      "cluster1",
			Namespace: "ns1",
		},
		Parameters: &Parameters{
			Raw: []byte(`{"key": "value"}`),
		},
	}

	copy := original.DeepCopy()

	if copy.Name != original.Name {
		t.Errorf("DeepCopy Name: got %q, want %q", copy.Name, original.Name)
	}
	if copy.ConnectorRef.Name != original.ConnectorRef.Name {
		t.Errorf("DeepCopy ConnectorRef.Name: got %q, want %q", copy.ConnectorRef.Name, original.ConnectorRef.Name)
	}
}

func TestBehavior_DeepCopy(t *testing.T) {
	original := Behavior{
		Mode:     BehaviorBestEffort,
		FailFast: false,
		Retries:  ptr.To[int32](5),
	}

	copy := original.DeepCopy()

	if copy.Mode != original.Mode {
		t.Errorf("DeepCopy Mode: got %q, want %q", copy.Mode, original.Mode)
	}
	if *copy.Retries != *original.Retries {
		t.Errorf("DeepCopy Retries: got %d, want %d", *copy.Retries, *original.Retries)
	}
}

// ---- AWSAuth ----

func TestAWSAuth_DeepCopy_NonNil(t *testing.T) {
	in := &AWSAuth{
		ServiceAccount: &ServiceAccountAuth{},
	}
	out := in.DeepCopy()
	if out == nil {
		t.Fatal("DeepCopy returned nil")
	}
	if out.ServiceAccount == nil {
		t.Error("DeepCopy did not copy ServiceAccount")
	}
}

func TestAWSAuth_DeepCopy_Nil(t *testing.T) {
	var in *AWSAuth
	if in.DeepCopy() != nil {
		t.Error("nil.DeepCopy() should return nil")
	}
}

// ---- AWSConfig ----

func TestAWSConfig_DeepCopy_NonNil(t *testing.T) {
	in := &AWSConfig{
		AccountId: "123456789012",
		Region:    "us-east-1",
		Auth:      AWSAuth{ServiceAccount: &ServiceAccountAuth{}},
	}
	out := in.DeepCopy()
	if out == nil {
		t.Fatal("DeepCopy returned nil")
	}
	if out.AccountId != in.AccountId {
		t.Errorf("AccountId mismatch: got %q want %q", out.AccountId, in.AccountId)
	}
}

func TestAWSConfig_DeepCopy_Nil(t *testing.T) {
	var in *AWSConfig
	if in.DeepCopy() != nil {
		t.Error("nil.DeepCopy() should return nil")
	}
}

// ---- CloudProviderList.DeepCopyObject ----

func TestCloudProviderList_DeepCopyObject(t *testing.T) {
	in := &CloudProviderList{}
	obj := in.DeepCopyObject()
	if obj == nil {
		t.Fatal("DeepCopyObject returned nil")
	}
	if _, ok := obj.(*CloudProviderList); !ok {
		t.Errorf("DeepCopyObject type mismatch: got %T", obj)
	}
}

// ---- CloudProviderSpec ----

func TestCloudProviderSpec_DeepCopy_NonNil(t *testing.T) {
	in := &CloudProviderSpec{
		Type: CloudProviderAWS,
		AWS: &AWSConfig{
			AccountId: "000000000000",
			Region:    "us-west-2",
			Auth:      AWSAuth{},
		},
	}
	out := in.DeepCopy()
	if out == nil {
		t.Fatal("DeepCopy returned nil")
	}
	if out.Type != in.Type {
		t.Errorf("Type mismatch: got %q want %q", out.Type, in.Type)
	}
}

func TestCloudProviderSpec_DeepCopy_Nil(t *testing.T) {
	var in *CloudProviderSpec
	if in.DeepCopy() != nil {
		t.Error("nil.DeepCopy() should return nil")
	}
}

// ---- CloudProviderStatus ----

func TestCloudProviderStatus_DeepCopy_NonNil(t *testing.T) {
	ts := metav1.NewTime(time.Now())
	in := &CloudProviderStatus{
		Ready:         true,
		Message:       "ok",
		LastValidated: &ts,
	}
	out := in.DeepCopy()
	if out == nil {
		t.Fatal("DeepCopy returned nil")
	}
	if out.Message != in.Message {
		t.Errorf("Message mismatch: got %q want %q", out.Message, in.Message)
	}
}

func TestCloudProviderStatus_DeepCopy_Nil(t *testing.T) {
	var in *CloudProviderStatus
	if in.DeepCopy() != nil {
		t.Error("nil.DeepCopy() should return nil")
	}
}

// ---- ConnectorRef ----

func TestConnectorRef_DeepCopy_NonNil(t *testing.T) {
	in := &ConnectorRef{Kind: "CloudProvider", Name: "aws-prod", Namespace: "infra"}
	out := in.DeepCopy()
	if out == nil {
		t.Fatal("DeepCopy returned nil")
	}
	if out.Kind != in.Kind || out.Name != in.Name {
		t.Errorf("ConnectorRef mismatch: got %+v want %+v", out, in)
	}
}

func TestConnectorRef_DeepCopy_Nil(t *testing.T) {
	var in *ConnectorRef
	if in.DeepCopy() != nil {
		t.Error("nil.DeepCopy() should return nil")
	}
}

// ---- Dependency ----

func TestDependency_DeepCopy_NonNil(t *testing.T) {
	in := &Dependency{From: "database", To: "app"}
	out := in.DeepCopy()
	if out == nil {
		t.Fatal("DeepCopy returned nil")
	}
	if out.From != in.From || out.To != in.To {
		t.Errorf("Dependency mismatch: got %+v want %+v", out, in)
	}
}

func TestDependency_DeepCopy_Nil(t *testing.T) {
	var in *Dependency
	if in.DeepCopy() != nil {
		t.Error("nil.DeepCopy() should return nil")
	}
}

// ---- EKSConfig ----

func TestEKSConfig_DeepCopy_NonNil(t *testing.T) {
	in := &EKSConfig{Name: "my-cluster", Region: "us-east-1"}
	out := in.DeepCopy()
	if out == nil {
		t.Fatal("DeepCopy returned nil")
	}
	if out.Name != in.Name {
		t.Errorf("Name mismatch: got %q want %q", out.Name, in.Name)
	}
}

func TestEKSConfig_DeepCopy_Nil(t *testing.T) {
	var in *EKSConfig
	if in.DeepCopy() != nil {
		t.Error("nil.DeepCopy() should return nil")
	}
}

// ---- ExceptionReference ----

func TestExceptionReference_DeepCopy_NonNil(t *testing.T) {
	applied := metav1.NewTime(time.Now())
	in := &ExceptionReference{
		Name:       "my-exception",
		Type:       ExceptionExtend,
		State:      ExceptionStatePending,
		ValidFrom:  metav1.NewTime(time.Now()),
		ValidUntil: metav1.NewTime(time.Now().Add(time.Hour)),
		AppliedAt:  &applied,
	}
	out := in.DeepCopy()
	if out == nil {
		t.Fatal("DeepCopy returned nil")
	}
	if out.Name != in.Name {
		t.Errorf("Name mismatch: got %q want %q", out.Name, in.Name)
	}
	if out.AppliedAt == nil {
		t.Error("AppliedAt should not be nil after DeepCopy")
	}
}

func TestExceptionReference_DeepCopy_Nil(t *testing.T) {
	var in *ExceptionReference
	if in.DeepCopy() != nil {
		t.Error("nil.DeepCopy() should return nil")
	}
}

// ---- Execution ----

func TestExecution_DeepCopy_NonNil(t *testing.T) {
	in := &Execution{
		Strategy: ExecutionStrategy{Type: StrategySequential},
	}
	out := in.DeepCopy()
	if out == nil {
		t.Fatal("DeepCopy returned nil")
	}
	if out.Strategy.Type != in.Strategy.Type {
		t.Errorf("Strategy.Type mismatch: got %q want %q", out.Strategy.Type, in.Strategy.Type)
	}
}

func TestExecution_DeepCopy_Nil(t *testing.T) {
	var in *Execution
	if in.DeepCopy() != nil {
		t.Error("nil.DeepCopy() should return nil")
	}
}

// ---- ExecutionCycle ----

func TestExecutionCycle_DeepCopy_NonNil(t *testing.T) {
	in := &ExecutionCycle{CycleID: "cycle-001"}
	out := in.DeepCopy()
	if out == nil {
		t.Fatal("DeepCopy returned nil")
	}
	if out.CycleID != in.CycleID {
		t.Errorf("CycleID mismatch: got %q want %q", out.CycleID, in.CycleID)
	}
}

func TestExecutionCycle_DeepCopy_Nil(t *testing.T) {
	var in *ExecutionCycle
	if in.DeepCopy() != nil {
		t.Error("nil.DeepCopy() should return nil")
	}
}

// ---- ExecutionOperationSummary ----

func TestExecutionOperationSummary_DeepCopy_NonNil(t *testing.T) {
	end := metav1.NewTime(time.Now())
	in := &ExecutionOperationSummary{
		Operation: "shutdown",
		StartTime: metav1.NewTime(time.Now()),
		EndTime:   &end,
		Success:   true,
	}
	out := in.DeepCopy()
	if out == nil {
		t.Fatal("DeepCopy returned nil")
	}
	if out.Operation != in.Operation {
		t.Errorf("Operation mismatch: got %q want %q", out.Operation, in.Operation)
	}
	if out.EndTime == nil {
		t.Error("EndTime should not be nil after DeepCopy")
	}
}

func TestExecutionOperationSummary_DeepCopy_Nil(t *testing.T) {
	var in *ExecutionOperationSummary
	if in.DeepCopy() != nil {
		t.Error("nil.DeepCopy() should return nil")
	}
}

// ---- GKEConfig ----

func TestGKEConfig_DeepCopy_NonNil(t *testing.T) {
	in := &GKEConfig{Name: "my-gke", Project: "my-project", Location: "us-central1"}
	out := in.DeepCopy()
	if out == nil {
		t.Fatal("DeepCopy returned nil")
	}
	if out.Name != in.Name {
		t.Errorf("Name mismatch: got %q want %q", out.Name, in.Name)
	}
}

func TestGKEConfig_DeepCopy_Nil(t *testing.T) {
	var in *GKEConfig
	if in.DeepCopy() != nil {
		t.Error("nil.DeepCopy() should return nil")
	}
}

// ---- K8SClusterList.DeepCopyObject ----

func TestK8SClusterList_DeepCopyObject(t *testing.T) {
	in := &K8SClusterList{}
	obj := in.DeepCopyObject()
	if obj == nil {
		t.Fatal("DeepCopyObject returned nil")
	}
	if _, ok := obj.(*K8SClusterList); !ok {
		t.Errorf("DeepCopyObject type mismatch: got %T", obj)
	}
}

// ---- K8SAccessConfig ----

func TestK8SAccessConfig_DeepCopy_NonNil(t *testing.T) {
	in := &K8SAccessConfig{
		KubeconfigRef: &KubeconfigRef{Name: "my-kubeconfig", Namespace: "default"},
		InCluster:     false,
	}
	out := in.DeepCopy()
	if out == nil {
		t.Fatal("DeepCopy returned nil")
	}
	if out.KubeconfigRef == nil || out.KubeconfigRef.Name != in.KubeconfigRef.Name {
		t.Error("KubeconfigRef not deep copied")
	}
}

func TestK8SAccessConfig_DeepCopy_Nil(t *testing.T) {
	var in *K8SAccessConfig
	if in.DeepCopy() != nil {
		t.Error("nil.DeepCopy() should return nil")
	}
}

// ---- PlanReference ----

func TestPlanReference_DeepCopy_NonNil(t *testing.T) {
	in := &PlanReference{Name: "my-plan", Namespace: "default"}
	out := in.DeepCopy()
	if out == nil {
		t.Fatal("DeepCopy returned nil")
	}
	if out.Name != in.Name {
		t.Errorf("Name mismatch: got %q want %q", out.Name, in.Name)
	}
}

func TestPlanReference_DeepCopy_Nil(t *testing.T) {
	var in *PlanReference
	if in.DeepCopy() != nil {
		t.Error("nil.DeepCopy() should return nil")
	}
}

// ---- ProviderRef ----

func TestProviderRef_DeepCopy_NonNil(t *testing.T) {
	in := &ProviderRef{Name: "aws-prod", Namespace: "infra"}
	out := in.DeepCopy()
	if out == nil {
		t.Fatal("DeepCopy returned nil")
	}
	if out.Name != in.Name {
		t.Errorf("Name mismatch: got %q want %q", out.Name, in.Name)
	}
}

func TestProviderRef_DeepCopy_Nil(t *testing.T) {
	var in *ProviderRef
	if in.DeepCopy() != nil {
		t.Error("nil.DeepCopy() should return nil")
	}
}

// ---- Schedule ----

func TestSchedule_DeepCopy_NonNil(t *testing.T) {
	in := &Schedule{
		Timezone: "America/New_York",
		OffHours: []OffHourWindow{{Start: "22:00", End: "06:00", DaysOfWeek: []string{"MON"}}},
	}
	out := in.DeepCopy()
	if out == nil {
		t.Fatal("DeepCopy returned nil")
	}
	if out.Timezone != in.Timezone {
		t.Errorf("Timezone mismatch: got %q want %q", out.Timezone, in.Timezone)
	}
	if len(out.OffHours) != len(in.OffHours) {
		t.Errorf("OffHours length mismatch: got %d want %d", len(out.OffHours), len(in.OffHours))
	}
}

func TestSchedule_DeepCopy_Nil(t *testing.T) {
	var in *Schedule
	if in.DeepCopy() != nil {
		t.Error("nil.DeepCopy() should return nil")
	}
}

// ---- ScheduleException ----

func TestScheduleException_DeepCopy_NonNil(t *testing.T) {
	in := &ScheduleException{
		ObjectMeta: metav1.ObjectMeta{Name: "my-exception", Namespace: "default"},
	}
	out := in.DeepCopy()
	if out == nil {
		t.Fatal("DeepCopy returned nil")
	}
	if out.Name != in.Name {
		t.Errorf("Name mismatch: got %q want %q", out.Name, in.Name)
	}
}

func TestScheduleException_DeepCopy_Nil(t *testing.T) {
	var in *ScheduleException
	if in.DeepCopy() != nil {
		t.Error("nil.DeepCopy() should return nil")
	}
}

func TestScheduleException_DeepCopyObject(t *testing.T) {
	in := &ScheduleException{
		ObjectMeta: metav1.ObjectMeta{Name: "my-exception"},
	}
	obj := in.DeepCopyObject()
	if obj == nil {
		t.Fatal("DeepCopyObject returned nil")
	}
	if _, ok := obj.(*ScheduleException); !ok {
		t.Errorf("DeepCopyObject type mismatch: got %T", obj)
	}
}

// ---- ScheduleExceptionList ----

func TestScheduleExceptionList_DeepCopyObject(t *testing.T) {
	in := &ScheduleExceptionList{}
	obj := in.DeepCopyObject()
	if obj == nil {
		t.Fatal("DeepCopyObject returned nil")
	}
	if _, ok := obj.(*ScheduleExceptionList); !ok {
		t.Errorf("DeepCopyObject type mismatch: got %T", obj)
	}
}

// ---- ScheduleExceptionSpec ----

func TestScheduleExceptionSpec_DeepCopy_NonNil(t *testing.T) {
	in := &ScheduleExceptionSpec{
		Type:       ExceptionSuspend,
		PlanRef:    PlanReference{Name: "my-plan", Namespace: "default"},
		ValidFrom:  metav1.NewTime(time.Date(2026, 1, 1, 20, 0, 0, 0, time.UTC)),
		ValidUntil: metav1.NewTime(time.Date(2026, 1, 2, 6, 0, 0, 0, time.UTC)),
		Windows:    []OffHourWindow{{Start: "22:00", End: "06:00"}},
	}
	out := in.DeepCopy()
	if out == nil {
		t.Fatal("DeepCopy returned nil")
	}
	if out.Type != in.Type {
		t.Errorf("Type mismatch: got %q want %q", out.Type, in.Type)
	}
	if out.PlanRef.Name != in.PlanRef.Name {
		t.Errorf("PlanRef.Name mismatch: got %q want %q", out.PlanRef.Name, in.PlanRef.Name)
	}
}

func TestScheduleExceptionSpec_DeepCopy_Nil(t *testing.T) {
	var in *ScheduleExceptionSpec
	if in.DeepCopy() != nil {
		t.Error("nil.DeepCopy() should return nil")
	}
}

// ---- ScheduleExceptionStatus ----

func TestScheduleExceptionStatus_DeepCopy_NonNil(t *testing.T) {
	in := &ScheduleExceptionStatus{
		State: ExceptionStateActive,
	}
	out := in.DeepCopy()
	if out == nil {
		t.Fatal("DeepCopy returned nil")
	}
	if out.State != in.State {
		t.Errorf("State mismatch: got %q want %q", out.State, in.State)
	}
}

func TestScheduleExceptionStatus_DeepCopy_Nil(t *testing.T) {
	var in *ScheduleExceptionStatus
	if in.DeepCopy() != nil {
		t.Error("nil.DeepCopy() should return nil")
	}
}

// ---- SecretReference ----

func TestSecretReference_DeepCopy_NonNil(t *testing.T) {
	in := &SecretReference{Name: "my-secret", Namespace: "default"}
	out := in.DeepCopy()
	if out == nil {
		t.Fatal("DeepCopy returned nil")
	}
	if out.Name != in.Name {
		t.Errorf("Name mismatch: got %q want %q", out.Name, in.Name)
	}
}

func TestSecretReference_DeepCopy_Nil(t *testing.T) {
	var in *SecretReference
	if in.DeepCopy() != nil {
		t.Error("nil.DeepCopy() should return nil")
	}
}

// ---- ServiceAccountAuth ----

func TestServiceAccountAuth_DeepCopy_NonNil(t *testing.T) {
	in := &ServiceAccountAuth{}
	out := in.DeepCopy()
	if out == nil {
		t.Fatal("DeepCopy returned nil")
	}
}

func TestServiceAccountAuth_DeepCopy_Nil(t *testing.T) {
	var in *ServiceAccountAuth
	if in.DeepCopy() != nil {
		t.Error("nil.DeepCopy() should return nil")
	}
}

// ---- Stage ----

func TestStage_DeepCopy_NonNil(t *testing.T) {
	in := &Stage{
		Name:     "db-tier",
		Parallel: true,
		Targets:  []string{"rds-1", "rds-2"},
	}
	out := in.DeepCopy()
	if out == nil {
		t.Fatal("DeepCopy returned nil")
	}
	if out.Name != in.Name {
		t.Errorf("Name mismatch: got %q want %q", out.Name, in.Name)
	}
	if len(out.Targets) != len(in.Targets) {
		t.Errorf("Targets length mismatch: got %d want %d", len(out.Targets), len(in.Targets))
	}
}

func TestStage_DeepCopy_Nil(t *testing.T) {
	var in *Stage
	if in.DeepCopy() != nil {
		t.Error("nil.DeepCopy() should return nil")
	}
}

// ---- StaticAuth ----

func TestStaticAuth_DeepCopy_NonNil(t *testing.T) {
	in := &StaticAuth{SecretRef: SecretReference{Name: "creds", Namespace: "default"}}
	out := in.DeepCopy()
	if out == nil {
		t.Fatal("DeepCopy returned nil")
	}
	if out.SecretRef.Name != in.SecretRef.Name {
		t.Errorf("SecretRef.Name mismatch: got %q want %q", out.SecretRef.Name, in.SecretRef.Name)
	}
}

func TestStaticAuth_DeepCopy_Nil(t *testing.T) {
	var in *StaticAuth
	if in.DeepCopy() != nil {
		t.Error("nil.DeepCopy() should return nil")
	}
}

// ---- TargetExecutionResult ----

func TestTargetExecutionResult_DeepCopy_NonNil(t *testing.T) {
	started := metav1.NewTime(time.Now())
	in := &TargetExecutionResult{
		Target:    "eks/my-cluster",
		State:     StateCompleted,
		Attempts:  2,
		StartedAt: &started,
	}
	out := in.DeepCopy()
	if out == nil {
		t.Fatal("DeepCopy returned nil")
	}
	if out.Target != in.Target {
		t.Errorf("Target mismatch: got %q want %q", out.Target, in.Target)
	}
	if out.StartedAt == nil {
		t.Error("StartedAt should not be nil after DeepCopy")
	}
}

func TestTargetExecutionResult_DeepCopy_Nil(t *testing.T) {
	var in *TargetExecutionResult
	if in.DeepCopy() != nil {
		t.Error("nil.DeepCopy() should return nil")
	}
}

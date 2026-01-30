/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package v1alpha1

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
					ServiceAccount: &ServiceAccountAuth{
						AssumeRoleArn: "arn:aws:iam::123456789012:role/test",
					},
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
		Retries:  5,
	}

	copy := original.DeepCopy()

	if copy.Mode != original.Mode {
		t.Errorf("DeepCopy Mode: got %q, want %q", copy.Mode, original.Mode)
	}
	if copy.Retries != original.Retries {
		t.Errorf("DeepCopy Retries: got %d, want %d", copy.Retries, original.Retries)
	}
}

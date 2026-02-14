/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package v1alpha1

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// validSchedule returns a valid schedule for tests.
func validSchedule() Schedule {
	return Schedule{
		Timezone: "UTC",
		OffHours: []OffHourWindow{
			{
				Start:      "20:00",
				End:        "06:00",
				DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"},
			},
		},
	}
}

// ec2Params returns valid EC2 executor parameters.
func ec2Params() *Parameters {
	return &Parameters{
		Raw: []byte(`{"selector": {"instanceIds": ["i-1234567890abcdef0"]}}`),
	}
}

// rdsParams returns valid RDS executor parameters.
func rdsParams() *Parameters {
	return &Parameters{
		Raw: []byte(`{"selector": {"instanceIds": ["my-db-instance"]}}`),
	}
}

// eksParams returns valid EKS executor parameters.
func eksParams() *Parameters {
	return &Parameters{
		Raw: []byte(`{"clusterName": "my-cluster"}`),
	}
}

func TestHibernatePlan_ValidateCreate(t *testing.T) {
	tests := []struct {
		name    string
		plan    *HibernatePlan
		wantErr bool
	}{
		{
			name: "valid sequential plan",
			plan: &HibernatePlan{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: HibernatePlanSpec{
					Schedule: validSchedule(),
					Execution: Execution{
						Strategy: ExecutionStrategy{Type: StrategySequential},
					},
					Targets: []Target{
						{Name: "target1", Type: "ec2", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: ec2Params()},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid time format",
			plan: &HibernatePlan{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: HibernatePlanSpec{
					Schedule: Schedule{
						Timezone: "UTC",
						OffHours: []OffHourWindow{
							{
								Start:      "25:00", // Invalid
								End:        "06:00",
								DaysOfWeek: []string{"MON"},
							},
						},
					},
					Execution: Execution{
						Strategy: ExecutionStrategy{Type: StrategySequential},
					},
					Targets: []Target{
						{Name: "target1", Type: "ec2", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: ec2Params()},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid day of week",
			plan: &HibernatePlan{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: HibernatePlanSpec{
					Schedule: Schedule{
						Timezone: "UTC",
						OffHours: []OffHourWindow{
							{
								Start:      "20:00",
								End:        "06:00",
								DaysOfWeek: []string{"MONDAY"}, // Should be MON
							},
						},
					},
					Execution: Execution{
						Strategy: ExecutionStrategy{Type: StrategySequential},
					},
					Targets: []Target{
						{Name: "target1", Type: "ec2", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: ec2Params()},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "same start and end time",
			plan: &HibernatePlan{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: HibernatePlanSpec{
					Schedule: Schedule{
						Timezone: "UTC",
						OffHours: []OffHourWindow{
							{
								Start:      "20:00",
								End:        "20:00", // Same as start - invalid
								DaysOfWeek: []string{"MON"},
							},
						},
					},
					Execution: Execution{
						Strategy: ExecutionStrategy{Type: StrategySequential},
					},
					Targets: []Target{
						{Name: "target1", Type: "ec2", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: ec2Params()},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "duplicate target names",
			plan: &HibernatePlan{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: HibernatePlanSpec{
					Schedule: validSchedule(),
					Execution: Execution{
						Strategy: ExecutionStrategy{Type: StrategySequential},
					},
					Targets: []Target{
						{Name: "target1", Type: "ec2", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: ec2Params()},
						{Name: "target1", Type: "rds", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: rdsParams()},
						{Name: "target2", Type: "eks", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: eksParams()},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid connector kind",
			plan: &HibernatePlan{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: HibernatePlanSpec{
					Schedule: validSchedule(),
					Execution: Execution{
						Strategy: ExecutionStrategy{Type: StrategySequential},
					},
					Targets: []Target{
						{Name: "target1", Type: "ec2", ConnectorRef: ConnectorRef{Kind: "Invalid", Name: "aws"}, Parameters: ec2Params()},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "DAG with cycle",
			plan: &HibernatePlan{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: HibernatePlanSpec{
					Schedule: validSchedule(),
					Execution: Execution{
						Strategy: ExecutionStrategy{
							Type: StrategyDAG,
							Dependencies: []Dependency{
								{From: "a", To: "b"},
								{From: "b", To: "c"},
								{From: "c", To: "a"}, // Cycle!
							},
						},
					},
					Targets: []Target{
						{Name: "a", Type: "ec2", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: ec2Params()},
						{Name: "b", Type: "ec2", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: ec2Params()},
						{Name: "c", Type: "ec2", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: ec2Params()},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "valid DAG",
			plan: &HibernatePlan{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: HibernatePlanSpec{
					Schedule: validSchedule(),
					Execution: Execution{
						Strategy: ExecutionStrategy{
							Type: StrategyDAG,
							Dependencies: []Dependency{
								{From: "frontend", To: "backend"},
								{From: "backend", To: "database"},
							},
						},
					},
					Targets: []Target{
						{Name: "frontend", Type: "ec2", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: ec2Params()},
						{Name: "backend", Type: "ec2", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: ec2Params()},
						{Name: "database", Type: "rds", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: rdsParams()},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "DAG with unknown target in dependency",
			plan: &HibernatePlan{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: HibernatePlanSpec{
					Schedule: validSchedule(),
					Execution: Execution{
						Strategy: ExecutionStrategy{
							Type: StrategyDAG,
							Dependencies: []Dependency{
								{From: "frontend", To: "unknown"},
							},
						},
					},
					Targets: []Target{
						{Name: "frontend", Type: "ec2", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: ec2Params()},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "staged with all targets assigned",
			plan: &HibernatePlan{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: HibernatePlanSpec{
					Schedule: validSchedule(),
					Execution: Execution{
						Strategy: ExecutionStrategy{
							Type: StrategyStaged,
							Stages: []Stage{
								{Name: "stage1", Targets: []string{"a", "b"}},
								{Name: "stage2", Targets: []string{"c"}},
							},
						},
					},
					Targets: []Target{
						{Name: "a", Type: "ec2", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: ec2Params()},
						{Name: "b", Type: "ec2", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: ec2Params()},
						{Name: "c", Type: "rds", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: rdsParams()},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "staged with unassigned target",
			plan: &HibernatePlan{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: HibernatePlanSpec{
					Schedule: validSchedule(),
					Execution: Execution{
						Strategy: ExecutionStrategy{
							Type: StrategyStaged,
							Stages: []Stage{
								{Name: "stage1", Targets: []string{"a"}},
							},
						},
					},
					Targets: []Target{
						{Name: "a", Type: "ec2", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}},
						{Name: "b", Type: "ec2", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "staged with duplicate stage names",
			plan: &HibernatePlan{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: HibernatePlanSpec{
					Schedule: validSchedule(),
					Execution: Execution{
						Strategy: ExecutionStrategy{
							Type: StrategyStaged,
							Stages: []Stage{
								{Name: "stage1", Targets: []string{"a"}},
								{Name: "stage1", Targets: []string{"b"}},
							},
						},
					},
					Targets: []Target{
						{Name: "a", Type: "ec2", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}},
						{Name: "b", Type: "ec2", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}},
					},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.plan.ValidateCreate(context.Background(), runtime.Object(tt.plan))
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateCreate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestHibernatePlan_ValidateUpdate(t *testing.T) {
	tests := []struct {
		name     string
		oldPhase PlanPhase
		newPlan  *HibernatePlan
		wantErr  bool
	}{
		{
			name:     "valid update in Active phase",
			oldPhase: PhaseActive,
			newPlan: &HibernatePlan{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: HibernatePlanSpec{
					Schedule: validSchedule(),
					Execution: Execution{
						Strategy: ExecutionStrategy{Type: StrategyParallel},
					},
					Targets: []Target{
						{Name: "target1", Type: "ec2", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: ec2Params()},
						{Name: "target2", Type: "rds", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: rdsParams()},
					},
				},
			},
			wantErr: false,
		},
		{
			name:     "valid update in Suspended phase",
			oldPhase: PhaseSuspended,
			newPlan: &HibernatePlan{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: HibernatePlanSpec{
					Schedule: validSchedule(),
					Execution: Execution{
						Strategy: ExecutionStrategy{Type: StrategyParallel},
					},
					Targets: []Target{
						{Name: "target1", Type: "ec2", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: ec2Params()},
						{Name: "target2", Type: "rds", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: rdsParams()},
					},
				},
			},
			wantErr: false,
		},
		{
			name:     "valid update in Error phase",
			oldPhase: PhaseError,
			newPlan: &HibernatePlan{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: HibernatePlanSpec{
					Schedule: validSchedule(),
					Execution: Execution{
						Strategy: ExecutionStrategy{Type: StrategyParallel},
					},
					Targets: []Target{
						{Name: "target1", Type: "ec2", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: ec2Params()},
						{Name: "target2", Type: "rds", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: rdsParams()},
					},
				},
			},
			wantErr: false,
		},
		{
			name:     "invalid update - targets modified during Hibernating phase",
			oldPhase: PhaseHibernating,
			newPlan: &HibernatePlan{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: HibernatePlanSpec{
					Schedule: validSchedule(),
					Execution: Execution{
						Strategy: ExecutionStrategy{Type: StrategySequential},
					},
					Targets: []Target{
						{Name: "target1", Type: "ec2", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: ec2Params()},
						{Name: "target2", Type: "rds", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: rdsParams()},
					},
				},
			},
			wantErr: true,
		},
		{
			name:     "invalid update - targets modified during Hibernated phase",
			oldPhase: PhaseHibernated,
			newPlan: &HibernatePlan{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: HibernatePlanSpec{
					Schedule: validSchedule(),
					Execution: Execution{
						Strategy: ExecutionStrategy{Type: StrategySequential},
					},
					Targets: []Target{
						{Name: "target1", Type: "ec2", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: ec2Params()},
						{Name: "target2", Type: "rds", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: rdsParams()},
					},
				},
			},
			wantErr: true,
		},
		{
			name:     "invalid update - targets modified during WakingUp phase",
			oldPhase: PhaseWakingUp,
			newPlan: &HibernatePlan{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: HibernatePlanSpec{
					Schedule: validSchedule(),
					Execution: Execution{
						Strategy: ExecutionStrategy{Type: StrategySequential},
					},
					Targets: []Target{
						{Name: "target1", Type: "ec2", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: ec2Params()},
						{Name: "target2", Type: "rds", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: rdsParams()},
					},
				},
			},
			wantErr: true,
		},
		{
			name:     "invalid update - duplicate targets",
			oldPhase: PhaseActive,
			newPlan: &HibernatePlan{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: HibernatePlanSpec{
					Schedule: validSchedule(),
					Execution: Execution{
						Strategy: ExecutionStrategy{Type: StrategySequential},
					},
					Targets: []Target{
						{Name: "target1", Type: "ec2", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: ec2Params()},
						{Name: "target1", Type: "rds", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: rdsParams()},
					},
				},
			},
			wantErr: true,
		},
		{
			name:     "invalid update - bad time format",
			oldPhase: PhaseActive,
			newPlan: &HibernatePlan{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: HibernatePlanSpec{
					Schedule: Schedule{
						Timezone: "UTC",
						OffHours: []OffHourWindow{
							{
								Start:      "invalid",
								End:        "06:00",
								DaysOfWeek: []string{"MON"},
							},
						},
					},
					Execution: Execution{
						Strategy: ExecutionStrategy{Type: StrategySequential},
					},
					Targets: []Target{
						{Name: "target1", Type: "ec2", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: ec2Params()},
					},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create oldPlan with the specified phase
			oldPlan := &HibernatePlan{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: HibernatePlanSpec{
					Schedule: validSchedule(),
					Execution: Execution{
						Strategy: ExecutionStrategy{Type: StrategySequential},
					},
					Targets: []Target{
						{Name: "target1", Type: "ec2", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: ec2Params()},
					},
				},
				Status: HibernatePlanStatus{
					Phase: tt.oldPhase,
				},
			}

			_, err := tt.newPlan.ValidateUpdate(context.Background(), runtime.Object(oldPlan), runtime.Object(tt.newPlan))
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateUpdate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestHibernatePlan_ValidateDelete(t *testing.T) {
	plan := &HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{Name: "test"},
		Spec: HibernatePlanSpec{
			Schedule: validSchedule(),
			Execution: Execution{
				Strategy: ExecutionStrategy{Type: StrategySequential},
			},
			Targets: []Target{
				{Name: "target1", Type: "ec2", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}},
			},
		},
	}

	warnings, err := plan.ValidateDelete(context.Background(), runtime.Object(plan))
	if err != nil {
		t.Errorf("ValidateDelete() unexpected error: %v", err)
	}
	if warnings != nil {
		t.Errorf("ValidateDelete() unexpected warnings: %v", warnings)
	}
}

func TestHibernatePlan_ValidateCreate_WrongType(t *testing.T) {
	plan := &HibernatePlan{}
	// Pass a wrong type to test the type assertion error
	wrongType := &CloudProvider{}
	_, err := plan.ValidateCreate(context.Background(), runtime.Object(wrongType))
	if err == nil {
		t.Error("ValidateCreate() expected error for wrong type, got nil")
	}
}

func TestHibernatePlan_ValidateUpdate_WrongType(t *testing.T) {
	plan := &HibernatePlan{}
	wrongType := &CloudProvider{}
	_, err := plan.ValidateUpdate(context.Background(), runtime.Object(plan), runtime.Object(wrongType))
	if err == nil {
		t.Error("ValidateUpdate() expected error for wrong type, got nil")
	}
}

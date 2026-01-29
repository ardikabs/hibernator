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
						{Name: "target1", Type: "ec2", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}},
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
						{Name: "target1", Type: "ec2", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}},
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
						{Name: "target1", Type: "ec2", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}},
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
						{Name: "target1", Type: "ec2", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}},
						{Name: "target1", Type: "rds", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}},
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
						{Name: "target1", Type: "ec2", ConnectorRef: ConnectorRef{Kind: "Invalid", Name: "aws"}},
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
						{Name: "a", Type: "ec2", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}},
						{Name: "b", Type: "ec2", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}},
						{Name: "c", Type: "ec2", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}},
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
						{Name: "frontend", Type: "ec2", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}},
						{Name: "backend", Type: "ec2", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}},
						{Name: "database", Type: "rds", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}},
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
						{Name: "frontend", Type: "ec2", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}},
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
						{Name: "a", Type: "ec2", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}},
						{Name: "b", Type: "ec2", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}},
						{Name: "c", Type: "rds", ConnectorRef: ConnectorRef{Kind: "CloudProvider", Name: "aws"}},
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

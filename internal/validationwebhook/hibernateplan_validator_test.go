/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package validationwebhook

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/go-logr/logr"
)

func validSchedule() hibernatorv1alpha1.Schedule {
	return hibernatorv1alpha1.Schedule{
		Timezone: "UTC",
		OffHours: []hibernatorv1alpha1.OffHourWindow{
			{
				Start:      "20:00",
				End:        "06:00",
				DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"},
			},
		},
	}
}

func ec2Params() *hibernatorv1alpha1.Parameters {
	return &hibernatorv1alpha1.Parameters{
		Raw: []byte(`{"selector": {"instanceIds": ["i-1234567890abcdef0"]}}`),
	}
}

func rdsParams() *hibernatorv1alpha1.Parameters {
	return &hibernatorv1alpha1.Parameters{
		Raw: []byte(`{"selector": {"instanceIds": ["my-db-instance"]}}`),
	}
}

func eksParams() *hibernatorv1alpha1.Parameters {
	return &hibernatorv1alpha1.Parameters{
		Raw: []byte(`{"clusterName": "my-cluster"}`),
	}
}

func TestHibernatePlanValidator_ValidateCreate(t *testing.T) {
	validator := NewHibernatePlanValidator(logr.Discard())

	tests := []struct {
		name    string
		plan    *hibernatorv1alpha1.HibernatePlan
		wantErr bool
	}{
		{
			name: "valid sequential plan",
			plan: &hibernatorv1alpha1.HibernatePlan{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: hibernatorv1alpha1.HibernatePlanSpec{
					Schedule: validSchedule(),
					Execution: hibernatorv1alpha1.Execution{
						Strategy: hibernatorv1alpha1.ExecutionStrategy{Type: hibernatorv1alpha1.StrategySequential},
					},
					Targets: []hibernatorv1alpha1.Target{
						{Name: "target1", Type: "ec2", ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: ec2Params()},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid time format",
			plan: &hibernatorv1alpha1.HibernatePlan{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: hibernatorv1alpha1.HibernatePlanSpec{
					Schedule: hibernatorv1alpha1.Schedule{
						Timezone: "UTC",
						OffHours: []hibernatorv1alpha1.OffHourWindow{
							{
								Start:      "25:00",
								End:        "06:00",
								DaysOfWeek: []string{"MON"},
							},
						},
					},
					Execution: hibernatorv1alpha1.Execution{
						Strategy: hibernatorv1alpha1.ExecutionStrategy{Type: hibernatorv1alpha1.StrategySequential},
					},
					Targets: []hibernatorv1alpha1.Target{
						{Name: "target1", Type: "ec2", ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: ec2Params()},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid day of week",
			plan: &hibernatorv1alpha1.HibernatePlan{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: hibernatorv1alpha1.HibernatePlanSpec{
					Schedule: hibernatorv1alpha1.Schedule{
						Timezone: "UTC",
						OffHours: []hibernatorv1alpha1.OffHourWindow{
							{
								Start:      "20:00",
								End:        "06:00",
								DaysOfWeek: []string{"MONDAY"},
							},
						},
					},
					Execution: hibernatorv1alpha1.Execution{
						Strategy: hibernatorv1alpha1.ExecutionStrategy{Type: hibernatorv1alpha1.StrategySequential},
					},
					Targets: []hibernatorv1alpha1.Target{
						{Name: "target1", Type: "ec2", ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: ec2Params()},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "same start and end time",
			plan: &hibernatorv1alpha1.HibernatePlan{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: hibernatorv1alpha1.HibernatePlanSpec{
					Schedule: hibernatorv1alpha1.Schedule{
						Timezone: "UTC",
						OffHours: []hibernatorv1alpha1.OffHourWindow{
							{
								Start:      "20:00",
								End:        "20:00",
								DaysOfWeek: []string{"MON"},
							},
						},
					},
					Execution: hibernatorv1alpha1.Execution{
						Strategy: hibernatorv1alpha1.ExecutionStrategy{Type: hibernatorv1alpha1.StrategySequential},
					},
					Targets: []hibernatorv1alpha1.Target{
						{Name: "target1", Type: "ec2", ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: ec2Params()},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "duplicate target names",
			plan: &hibernatorv1alpha1.HibernatePlan{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: hibernatorv1alpha1.HibernatePlanSpec{
					Schedule: validSchedule(),
					Execution: hibernatorv1alpha1.Execution{
						Strategy: hibernatorv1alpha1.ExecutionStrategy{Type: hibernatorv1alpha1.StrategySequential},
					},
					Targets: []hibernatorv1alpha1.Target{
						{Name: "target1", Type: "ec2", ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: ec2Params()},
						{Name: "target1", Type: "rds", ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: rdsParams()},
						{Name: "target2", Type: "eks", ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: eksParams()},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid connector kind",
			plan: &hibernatorv1alpha1.HibernatePlan{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: hibernatorv1alpha1.HibernatePlanSpec{
					Schedule: validSchedule(),
					Execution: hibernatorv1alpha1.Execution{
						Strategy: hibernatorv1alpha1.ExecutionStrategy{Type: hibernatorv1alpha1.StrategySequential},
					},
					Targets: []hibernatorv1alpha1.Target{
						{Name: "target1", Type: "ec2", ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "Invalid", Name: "aws"}, Parameters: ec2Params()},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "DAG with cycle",
			plan: &hibernatorv1alpha1.HibernatePlan{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: hibernatorv1alpha1.HibernatePlanSpec{
					Schedule: validSchedule(),
					Execution: hibernatorv1alpha1.Execution{
						Strategy: hibernatorv1alpha1.ExecutionStrategy{
							Type: hibernatorv1alpha1.StrategyDAG,
							Dependencies: []hibernatorv1alpha1.Dependency{
								{From: "a", To: "b"},
								{From: "b", To: "c"},
								{From: "c", To: "a"},
							},
						},
					},
					Targets: []hibernatorv1alpha1.Target{
						{Name: "a", Type: "ec2", ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: ec2Params()},
						{Name: "b", Type: "ec2", ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: ec2Params()},
						{Name: "c", Type: "ec2", ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: ec2Params()},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "valid DAG",
			plan: &hibernatorv1alpha1.HibernatePlan{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: hibernatorv1alpha1.HibernatePlanSpec{
					Schedule: validSchedule(),
					Execution: hibernatorv1alpha1.Execution{
						Strategy: hibernatorv1alpha1.ExecutionStrategy{
							Type: hibernatorv1alpha1.StrategyDAG,
							Dependencies: []hibernatorv1alpha1.Dependency{
								{From: "frontend", To: "backend"},
								{From: "backend", To: "database"},
							},
						},
					},
					Targets: []hibernatorv1alpha1.Target{
						{Name: "frontend", Type: "ec2", ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: ec2Params()},
						{Name: "backend", Type: "ec2", ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: ec2Params()},
						{Name: "database", Type: "rds", ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: rdsParams()},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "DAG with unknown target in dependency",
			plan: &hibernatorv1alpha1.HibernatePlan{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: hibernatorv1alpha1.HibernatePlanSpec{
					Schedule: validSchedule(),
					Execution: hibernatorv1alpha1.Execution{
						Strategy: hibernatorv1alpha1.ExecutionStrategy{
							Type: hibernatorv1alpha1.StrategyDAG,
							Dependencies: []hibernatorv1alpha1.Dependency{
								{From: "frontend", To: "unknown"},
							},
						},
					},
					Targets: []hibernatorv1alpha1.Target{
						{Name: "frontend", Type: "ec2", ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: ec2Params()},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "staged with all targets assigned",
			plan: &hibernatorv1alpha1.HibernatePlan{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: hibernatorv1alpha1.HibernatePlanSpec{
					Schedule: validSchedule(),
					Execution: hibernatorv1alpha1.Execution{
						Strategy: hibernatorv1alpha1.ExecutionStrategy{
							Type: hibernatorv1alpha1.StrategyStaged,
							Stages: []hibernatorv1alpha1.Stage{
								{Name: "stage1", Targets: []string{"a", "b"}},
								{Name: "stage2", Targets: []string{"c"}},
							},
						},
					},
					Targets: []hibernatorv1alpha1.Target{
						{Name: "a", Type: "ec2", ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: ec2Params()},
						{Name: "b", Type: "ec2", ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: ec2Params()},
						{Name: "c", Type: "rds", ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: rdsParams()},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "staged with unassigned target",
			plan: &hibernatorv1alpha1.HibernatePlan{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: hibernatorv1alpha1.HibernatePlanSpec{
					Schedule: validSchedule(),
					Execution: hibernatorv1alpha1.Execution{
						Strategy: hibernatorv1alpha1.ExecutionStrategy{
							Type: hibernatorv1alpha1.StrategyStaged,
							Stages: []hibernatorv1alpha1.Stage{
								{Name: "stage1", Targets: []string{"a"}},
							},
						},
					},
					Targets: []hibernatorv1alpha1.Target{
						{Name: "a", Type: "ec2", ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "aws"}},
						{Name: "b", Type: "ec2", ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "aws"}},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "staged with duplicate stage names",
			plan: &hibernatorv1alpha1.HibernatePlan{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: hibernatorv1alpha1.HibernatePlanSpec{
					Schedule: validSchedule(),
					Execution: hibernatorv1alpha1.Execution{
						Strategy: hibernatorv1alpha1.ExecutionStrategy{
							Type: hibernatorv1alpha1.StrategyStaged,
							Stages: []hibernatorv1alpha1.Stage{
								{Name: "stage1", Targets: []string{"a"}},
								{Name: "stage1", Targets: []string{"b"}},
							},
						},
					},
					Targets: []hibernatorv1alpha1.Target{
						{Name: "a", Type: "ec2", ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "aws"}},
						{Name: "b", Type: "ec2", ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "aws"}},
					},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validator.ValidateCreate(context.Background(), runtime.Object(tt.plan))
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateCreate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestHibernatePlanValidator_ValidateUpdate(t *testing.T) {
	validator := NewHibernatePlanValidator(logr.Discard())

	tests := []struct {
		name     string
		oldPhase hibernatorv1alpha1.PlanPhase
		newPlan  *hibernatorv1alpha1.HibernatePlan
		wantErr  bool
	}{
		{
			name:     "valid update in Active phase",
			oldPhase: hibernatorv1alpha1.PhaseActive,
			newPlan: &hibernatorv1alpha1.HibernatePlan{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: hibernatorv1alpha1.HibernatePlanSpec{
					Schedule: validSchedule(),
					Execution: hibernatorv1alpha1.Execution{
						Strategy: hibernatorv1alpha1.ExecutionStrategy{Type: hibernatorv1alpha1.StrategyParallel},
					},
					Targets: []hibernatorv1alpha1.Target{
						{Name: "target1", Type: "ec2", ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: ec2Params()},
						{Name: "target2", Type: "rds", ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: rdsParams()},
					},
				},
			},
			wantErr: false,
		},
		{
			name:     "valid update in Suspended phase",
			oldPhase: hibernatorv1alpha1.PhaseSuspended,
			newPlan: &hibernatorv1alpha1.HibernatePlan{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: hibernatorv1alpha1.HibernatePlanSpec{
					Schedule: validSchedule(),
					Execution: hibernatorv1alpha1.Execution{
						Strategy: hibernatorv1alpha1.ExecutionStrategy{Type: hibernatorv1alpha1.StrategyParallel},
					},
					Targets: []hibernatorv1alpha1.Target{
						{Name: "target1", Type: "ec2", ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: ec2Params()},
						{Name: "target2", Type: "rds", ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: rdsParams()},
					},
				},
			},
			wantErr: false,
		},
		{
			name:     "valid update in Error phase",
			oldPhase: hibernatorv1alpha1.PhaseError,
			newPlan: &hibernatorv1alpha1.HibernatePlan{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: hibernatorv1alpha1.HibernatePlanSpec{
					Schedule: validSchedule(),
					Execution: hibernatorv1alpha1.Execution{
						Strategy: hibernatorv1alpha1.ExecutionStrategy{Type: hibernatorv1alpha1.StrategyParallel},
					},
					Targets: []hibernatorv1alpha1.Target{
						{Name: "target1", Type: "ec2", ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: ec2Params()},
						{Name: "target2", Type: "rds", ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: rdsParams()},
					},
				},
			},
			wantErr: false,
		},
		{
			name:     "invalid update - targets modified during Hibernating phase",
			oldPhase: hibernatorv1alpha1.PhaseHibernating,
			newPlan: &hibernatorv1alpha1.HibernatePlan{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: hibernatorv1alpha1.HibernatePlanSpec{
					Schedule: validSchedule(),
					Execution: hibernatorv1alpha1.Execution{
						Strategy: hibernatorv1alpha1.ExecutionStrategy{Type: hibernatorv1alpha1.StrategySequential},
					},
					Targets: []hibernatorv1alpha1.Target{
						{Name: "target1", Type: "ec2", ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: ec2Params()},
						{Name: "target2", Type: "rds", ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: rdsParams()},
					},
				},
			},
			wantErr: true,
		},
		{
			name:     "invalid update - targets modified during Hibernated phase",
			oldPhase: hibernatorv1alpha1.PhaseHibernated,
			newPlan: &hibernatorv1alpha1.HibernatePlan{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: hibernatorv1alpha1.HibernatePlanSpec{
					Schedule: validSchedule(),
					Execution: hibernatorv1alpha1.Execution{
						Strategy: hibernatorv1alpha1.ExecutionStrategy{Type: hibernatorv1alpha1.StrategySequential},
					},
					Targets: []hibernatorv1alpha1.Target{
						{Name: "target1", Type: "ec2", ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: ec2Params()},
						{Name: "target2", Type: "rds", ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: rdsParams()},
					},
				},
			},
			wantErr: true,
		},
		{
			name:     "invalid update - targets modified during WakingUp phase",
			oldPhase: hibernatorv1alpha1.PhaseWakingUp,
			newPlan: &hibernatorv1alpha1.HibernatePlan{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: hibernatorv1alpha1.HibernatePlanSpec{
					Schedule: validSchedule(),
					Execution: hibernatorv1alpha1.Execution{
						Strategy: hibernatorv1alpha1.ExecutionStrategy{Type: hibernatorv1alpha1.StrategySequential},
					},
					Targets: []hibernatorv1alpha1.Target{
						{Name: "target1", Type: "ec2", ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: ec2Params()},
						{Name: "target2", Type: "rds", ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: rdsParams()},
					},
				},
			},
			wantErr: true,
		},
		{
			name:     "invalid update - duplicate targets",
			oldPhase: hibernatorv1alpha1.PhaseActive,
			newPlan: &hibernatorv1alpha1.HibernatePlan{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: hibernatorv1alpha1.HibernatePlanSpec{
					Schedule: validSchedule(),
					Execution: hibernatorv1alpha1.Execution{
						Strategy: hibernatorv1alpha1.ExecutionStrategy{Type: hibernatorv1alpha1.StrategySequential},
					},
					Targets: []hibernatorv1alpha1.Target{
						{Name: "target1", Type: "ec2", ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: ec2Params()},
						{Name: "target1", Type: "rds", ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: rdsParams()},
					},
				},
			},
			wantErr: true,
		},
		{
			name:     "invalid update - bad time format",
			oldPhase: hibernatorv1alpha1.PhaseActive,
			newPlan: &hibernatorv1alpha1.HibernatePlan{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: hibernatorv1alpha1.HibernatePlanSpec{
					Schedule: hibernatorv1alpha1.Schedule{
						Timezone: "UTC",
						OffHours: []hibernatorv1alpha1.OffHourWindow{
							{
								Start:      "invalid",
								End:        "06:00",
								DaysOfWeek: []string{"MON"},
							},
						},
					},
					Execution: hibernatorv1alpha1.Execution{
						Strategy: hibernatorv1alpha1.ExecutionStrategy{Type: hibernatorv1alpha1.StrategySequential},
					},
					Targets: []hibernatorv1alpha1.Target{
						{Name: "target1", Type: "ec2", ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: ec2Params()},
					},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldPlan := &hibernatorv1alpha1.HibernatePlan{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: hibernatorv1alpha1.HibernatePlanSpec{
					Schedule: validSchedule(),
					Execution: hibernatorv1alpha1.Execution{
						Strategy: hibernatorv1alpha1.ExecutionStrategy{Type: hibernatorv1alpha1.StrategySequential},
					},
					Targets: []hibernatorv1alpha1.Target{
						{Name: "target1", Type: "ec2", ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "aws"}, Parameters: ec2Params()},
					},
				},
				Status: hibernatorv1alpha1.HibernatePlanStatus{
					Phase: tt.oldPhase,
				},
			}

			_, err := validator.ValidateUpdate(context.Background(), runtime.Object(oldPlan), runtime.Object(tt.newPlan))
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateUpdate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestHibernatePlanValidator_ValidateDelete(t *testing.T) {
	validator := NewHibernatePlanValidator(logr.Discard())
	plan := &hibernatorv1alpha1.HibernatePlan{
		ObjectMeta: metav1.ObjectMeta{Name: "test"},
		Spec: hibernatorv1alpha1.HibernatePlanSpec{
			Schedule: validSchedule(),
			Execution: hibernatorv1alpha1.Execution{
				Strategy: hibernatorv1alpha1.ExecutionStrategy{Type: hibernatorv1alpha1.StrategySequential},
			},
			Targets: []hibernatorv1alpha1.Target{
				{Name: "target1", Type: "ec2", ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "aws"}},
			},
		},
	}

	warnings, err := validator.ValidateDelete(context.Background(), runtime.Object(plan))
	if err != nil {
		t.Errorf("ValidateDelete() unexpected error: %v", err)
	}
	if warnings != nil {
		t.Errorf("ValidateDelete() unexpected warnings: %v", warnings)
	}
}

func TestHibernatePlanValidator_ValidateCreate_WrongType(t *testing.T) {
	validator := NewHibernatePlanValidator(logr.Discard())
	wrongType := &hibernatorv1alpha1.CloudProvider{}
	_, err := validator.ValidateCreate(context.Background(), runtime.Object(wrongType))
	if err == nil {
		t.Error("ValidateCreate() expected error for wrong type, got nil")
	}
}

func TestHibernatePlanValidator_ValidateUpdate_WrongType(t *testing.T) {
	validator := NewHibernatePlanValidator(logr.Discard())
	plan := &hibernatorv1alpha1.HibernatePlan{}
	wrongType := &hibernatorv1alpha1.CloudProvider{}
	_, err := validator.ValidateUpdate(context.Background(), runtime.Object(plan), runtime.Object(wrongType))
	if err == nil {
		t.Error("ValidateUpdate() expected error for wrong type, got nil")
	}
}

func TestHibernatePlanValidator_SmallGapWindowWarning(t *testing.T) {
	validator := NewHibernatePlanValidator(logr.Discard())

	tests := []struct {
		name          string
		start         string
		end           string
		expectWarning bool
		wantGuidance  string
	}{
		{
			name:          "1-minute gap (23:59 to 00:00) should warn",
			start:         "23:59",
			end:           "00:00",
			expectWarning: true,
			wantGuidance:  "start=00:00, end=23:59",
		},
		{
			name:          "1-minute gap (14:59 to 15:00) should warn",
			start:         "14:59",
			end:           "15:00",
			expectWarning: true,
			wantGuidance:  "start=00:00, end=23:59",
		},
		{
			name:          "2-minute gap should not warn",
			start:         "23:58",
			end:           "00:00",
			expectWarning: false,
			wantGuidance:  "",
		},
		{
			name:          "5-hour gap should not warn",
			start:         "22:00",
			end:           "03:00",
			expectWarning: false,
			wantGuidance:  "",
		},
		{
			name:          "1-hour gap (forward) should not warn",
			start:         "14:00",
			end:           "15:00",
			expectWarning: false,
			wantGuidance:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := &hibernatorv1alpha1.HibernatePlan{
				ObjectMeta: metav1.ObjectMeta{Name: "test"},
				Spec: hibernatorv1alpha1.HibernatePlanSpec{
					Schedule: hibernatorv1alpha1.Schedule{
						Timezone: "UTC",
						OffHours: []hibernatorv1alpha1.OffHourWindow{
							{
								Start:      tt.start,
								End:        tt.end,
								DaysOfWeek: []string{"MON"},
							},
						},
					},
					Execution: hibernatorv1alpha1.Execution{
						Strategy: hibernatorv1alpha1.ExecutionStrategy{Type: hibernatorv1alpha1.StrategySequential},
					},
					Targets: []hibernatorv1alpha1.Target{
						{
							Name:         "target1",
							Type:         "ec2",
							ConnectorRef: hibernatorv1alpha1.ConnectorRef{Kind: "CloudProvider", Name: "aws"},
							Parameters:   ec2Params(),
						},
					},
				},
			}

			warnings, err := validator.ValidateCreate(context.Background(), plan)
			if err != nil {
				t.Fatalf("ValidateCreate() unexpected error: %v", err)
			}

			var foundWarning string
			for _, w := range warnings {
				if len(w) > 0 {
					foundWarning = w
					break
				}
			}

			if tt.expectWarning && foundWarning == "" {
				t.Error("expected warning for small gap, got none")
			}
			if !tt.expectWarning && foundWarning != "" {
				t.Errorf("expected no warning, got: %s", foundWarning)
			}

			if tt.expectWarning && tt.wantGuidance != "" {
				if !strings.Contains(foundWarning, tt.wantGuidance) {
					t.Errorf("warning guidance missing %q, got: %s", tt.wantGuidance, foundWarning)
				}
				if !strings.Contains(foundWarning, "ScheduleException") || !strings.Contains(foundWarning, "Suspend") {
					t.Errorf("warning should mention 'ScheduleException' with 'Suspend' type, got: %s", foundWarning)
				}
			}
		})
	}
}

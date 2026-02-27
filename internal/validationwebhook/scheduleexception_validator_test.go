/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package validationwebhook

import (
	"context"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/wellknown"
	"github.com/go-logr/logr"
)

func validException() *hibernatorv1alpha1.ScheduleException {
	return &hibernatorv1alpha1.ScheduleException{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-exception",
			Namespace: "default",
		},
		Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
			PlanRef: hibernatorv1alpha1.PlanReference{
				Name:      "test-plan",
				Namespace: "default",
			},
			ValidFrom:  metav1.Time{Time: time.Now().Add(-24 * time.Hour)},
			ValidUntil: metav1.Time{Time: time.Now().Add(7 * 24 * time.Hour)},
			Type:       "extend",
			Windows: []hibernatorv1alpha1.OffHourWindow{
				{
					Start:      "06:00",
					End:        "11:00",
					DaysOfWeek: []string{"SAT", "SUN"},
				},
			},
		},
	}
}

func setupTestClient(objs ...client.Object) client.Client {
	scheme := runtime.NewScheme()
	_ = hibernatorv1alpha1.AddToScheme(scheme)

	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		Build()
}

func TestScheduleExceptionValidator_ValidateCreate(t *testing.T) {
	tests := []struct {
		name      string
		exception *hibernatorv1alpha1.ScheduleException
		setup     func() client.Client
		wantErr   bool
		errMsg    string
	}{
		{
			name:      "valid exception",
			exception: validException(),
			setup: func() client.Client {
				plan := &hibernatorv1alpha1.HibernatePlan{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-plan",
						Namespace: "default",
					},
					Spec: hibernatorv1alpha1.HibernatePlanSpec{
						Schedule: hibernatorv1alpha1.Schedule{
							Timezone: "UTC",
							OffHours: []hibernatorv1alpha1.OffHourWindow{
								{Start: "20:00", End: "06:00", DaysOfWeek: []string{"MON"}},
							},
						},
					},
				}
				return setupTestClient(plan)
			},
			wantErr: false,
		},
		{
			name: "invalid planRef - nonexistent plan",
			exception: &hibernatorv1alpha1.ScheduleException{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-exception",
					Namespace: "default",
				},
				Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
					PlanRef: hibernatorv1alpha1.PlanReference{
						Name:      "nonexistent-plan",
						Namespace: "default",
					},
					ValidFrom:  metav1.Time{Time: time.Now()},
					ValidUntil: metav1.Time{Time: time.Now().Add(7 * 24 * time.Hour)},
					Type:       "extend",
					Windows:    []hibernatorv1alpha1.OffHourWindow{{Start: "06:00", End: "11:00", DaysOfWeek: []string{"SAT"}}},
				},
			},
			setup: func() client.Client {
				return setupTestClient()
			},
			wantErr: true,
			errMsg:  "Not found",
		},
		{
			name: "invalid planRef - wrong namespace",
			exception: &hibernatorv1alpha1.ScheduleException{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-exception",
					Namespace: "default",
				},
				Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
					PlanRef: hibernatorv1alpha1.PlanReference{
						Name:      "test-plan",
						Namespace: "other-namespace",
					},
					ValidFrom:  metav1.Time{Time: time.Now()},
					ValidUntil: metav1.Time{Time: time.Now().Add(7 * 24 * time.Hour)},
					Type:       "extend",
					Windows:    []hibernatorv1alpha1.OffHourWindow{{Start: "06:00", End: "11:00", DaysOfWeek: []string{"SAT"}}},
				},
			},
			setup: func() client.Client {
				return setupTestClient()
			},
			wantErr: true,
			errMsg:  "same namespace",
		},
		{
			name: "invalid time range - validUntil before validFrom",
			exception: &hibernatorv1alpha1.ScheduleException{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-exception",
					Namespace: "default",
				},
				Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
					PlanRef: hibernatorv1alpha1.PlanReference{
						Name:      "test-plan",
						Namespace: "default",
					},
					ValidFrom:  metav1.Time{Time: time.Now().Add(7 * 24 * time.Hour)},
					ValidUntil: metav1.Time{Time: time.Now()},
					Type:       "extend",
					Windows:    []hibernatorv1alpha1.OffHourWindow{{Start: "06:00", End: "11:00", DaysOfWeek: []string{"SAT"}}},
				},
			},
			setup: func() client.Client {
				plan := &hibernatorv1alpha1.HibernatePlan{
					ObjectMeta: metav1.ObjectMeta{Name: "test-plan", Namespace: "default"},
					Spec: hibernatorv1alpha1.HibernatePlanSpec{
						Schedule: hibernatorv1alpha1.Schedule{
							Timezone: "UTC",
							OffHours: []hibernatorv1alpha1.OffHourWindow{{Start: "20:00", End: "06:00", DaysOfWeek: []string{"MON"}}},
						},
					},
				}
				return setupTestClient(plan)
			},
			wantErr: true,
			errMsg:  "validUntil must be after validFrom",
		},
		{
			name: "invalid time range - exceeds 90 days",
			exception: &hibernatorv1alpha1.ScheduleException{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-exception",
					Namespace: "default",
				},
				Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
					PlanRef: hibernatorv1alpha1.PlanReference{
						Name:      "test-plan",
						Namespace: "default",
					},
					ValidFrom:  metav1.Time{Time: time.Now()},
					ValidUntil: metav1.Time{Time: time.Now().Add(100 * 24 * time.Hour)},
					Type:       "extend",
					Windows:    []hibernatorv1alpha1.OffHourWindow{{Start: "06:00", End: "11:00", DaysOfWeek: []string{"SAT"}}},
				},
			},
			setup: func() client.Client {
				plan := &hibernatorv1alpha1.HibernatePlan{
					ObjectMeta: metav1.ObjectMeta{Name: "test-plan", Namespace: "default"},
					Spec: hibernatorv1alpha1.HibernatePlanSpec{
						Schedule: hibernatorv1alpha1.Schedule{
							Timezone: "UTC",
							OffHours: []hibernatorv1alpha1.OffHourWindow{{Start: "20:00", End: "06:00", DaysOfWeek: []string{"MON"}}},
						},
					},
				}
				return setupTestClient(plan)
			},
			wantErr: true,
			errMsg:  "exceeds maximum of 90 days",
		},
		{
			name: "invalid type-specific fields - leadTime with extend type",
			exception: &hibernatorv1alpha1.ScheduleException{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-exception",
					Namespace: "default",
				},
				Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
					PlanRef: hibernatorv1alpha1.PlanReference{
						Name:      "test-plan",
						Namespace: "default",
					},
					ValidFrom:  metav1.Time{Time: time.Now()},
					ValidUntil: metav1.Time{Time: time.Now().Add(7 * 24 * time.Hour)},
					Type:       "extend",
					LeadTime:   "1h",
					Windows:    []hibernatorv1alpha1.OffHourWindow{{Start: "06:00", End: "11:00", DaysOfWeek: []string{"SAT"}}},
				},
			},
			setup: func() client.Client {
				plan := &hibernatorv1alpha1.HibernatePlan{
					ObjectMeta: metav1.ObjectMeta{Name: "test-plan", Namespace: "default"},
					Spec: hibernatorv1alpha1.HibernatePlanSpec{
						Schedule: hibernatorv1alpha1.Schedule{
							Timezone: "UTC",
							OffHours: []hibernatorv1alpha1.OffHourWindow{{Start: "20:00", End: "06:00", DaysOfWeek: []string{"MON"}}},
						},
					},
				}
				return setupTestClient(plan)
			},
			wantErr: true,
			errMsg:  "leadTime is only valid when type is 'suspend'",
		},
		{
			name: "invalid windows - bad time format",
			exception: &hibernatorv1alpha1.ScheduleException{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-exception",
					Namespace: "default",
				},
				Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
					PlanRef: hibernatorv1alpha1.PlanReference{
						Name:      "test-plan",
						Namespace: "default",
					},
					ValidFrom:  metav1.Time{Time: time.Now()},
					ValidUntil: metav1.Time{Time: time.Now().Add(7 * 24 * time.Hour)},
					Type:       "extend",
					Windows: []hibernatorv1alpha1.OffHourWindow{
						{Start: "25:00", End: "11:00", DaysOfWeek: []string{"SAT"}},
					},
				},
			},
			setup: func() client.Client {
				plan := &hibernatorv1alpha1.HibernatePlan{
					ObjectMeta: metav1.ObjectMeta{Name: "test-plan", Namespace: "default"},
					Spec: hibernatorv1alpha1.HibernatePlanSpec{
						Schedule: hibernatorv1alpha1.Schedule{
							Timezone: "UTC",
							OffHours: []hibernatorv1alpha1.OffHourWindow{{Start: "20:00", End: "06:00", DaysOfWeek: []string{"MON"}}},
						},
					},
				}
				return setupTestClient(plan)
			},
			wantErr: true,
			errMsg:  "must be in HH:MM format",
		},
		{
			name: "invalid windows - bad day name",
			exception: &hibernatorv1alpha1.ScheduleException{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-exception",
					Namespace: "default",
				},
				Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
					PlanRef: hibernatorv1alpha1.PlanReference{
						Name:      "test-plan",
						Namespace: "default",
					},
					ValidFrom:  metav1.Time{Time: time.Now()},
					ValidUntil: metav1.Time{Time: time.Now().Add(7 * 24 * time.Hour)},
					Type:       "extend",
					Windows: []hibernatorv1alpha1.OffHourWindow{
						{Start: "06:00", End: "11:00", DaysOfWeek: []string{"INVALID"}},
					},
				},
			},
			setup: func() client.Client {
				plan := &hibernatorv1alpha1.HibernatePlan{
					ObjectMeta: metav1.ObjectMeta{Name: "test-plan", Namespace: "default"},
					Spec: hibernatorv1alpha1.HibernatePlanSpec{
						Schedule: hibernatorv1alpha1.Schedule{
							Timezone: "UTC",
							OffHours: []hibernatorv1alpha1.OffHourWindow{{Start: "20:00", End: "06:00", DaysOfWeek: []string{"MON"}}},
						},
					},
				}
				return setupTestClient(plan)
			},
			wantErr: true,
			errMsg:  "must be one of: MON, TUE, WED, THU, FRI, SAT, SUN",
		},
		{
			name: "overlapping active exception",
			exception: &hibernatorv1alpha1.ScheduleException{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "new-exception",
					Namespace: "default",
				},
				Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
					PlanRef: hibernatorv1alpha1.PlanReference{
						Name:      "test-plan",
						Namespace: "default",
					},
					ValidFrom:  metav1.Time{Time: time.Now()},
					ValidUntil: metav1.Time{Time: time.Now().Add(7 * 24 * time.Hour)},
					Type:       "extend",
					Windows:    []hibernatorv1alpha1.OffHourWindow{{Start: "06:00", End: "11:00", DaysOfWeek: []string{"SAT"}}},
				},
			},
			setup: func() client.Client {
				plan := &hibernatorv1alpha1.HibernatePlan{
					ObjectMeta: metav1.ObjectMeta{Name: "test-plan", Namespace: "default"},
					Spec: hibernatorv1alpha1.HibernatePlanSpec{
						Schedule: hibernatorv1alpha1.Schedule{
							Timezone: "UTC",
							OffHours: []hibernatorv1alpha1.OffHourWindow{{Start: "20:00", End: "06:00", DaysOfWeek: []string{"MON"}}},
						},
					},
				}
				existingException := &hibernatorv1alpha1.ScheduleException{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "existing-exception",
						Namespace: "default",
						Labels:    map[string]string{wellknown.LabelPlan: "test-plan"},
					},
					Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
						PlanRef:    hibernatorv1alpha1.PlanReference{Name: "test-plan", Namespace: "default"},
						ValidFrom:  metav1.Time{Time: time.Now()},
						ValidUntil: metav1.Time{Time: time.Now().Add(14 * 24 * time.Hour)},
						Type:       "extend",
						Windows:    []hibernatorv1alpha1.OffHourWindow{{Start: "06:00", End: "11:00", DaysOfWeek: []string{"SUN"}}},
					},
					Status: hibernatorv1alpha1.ScheduleExceptionStatus{
						State: hibernatorv1alpha1.ExceptionStateActive,
					},
				}
				return setupTestClient(plan, existingException)
			},
			wantErr: true,
			errMsg:  "overlaps with existing",
		},
		{
			name: "overlapping pending exception",
			exception: &hibernatorv1alpha1.ScheduleException{
				ObjectMeta: metav1.ObjectMeta{Name: "new-exception", Namespace: "default"},
				Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
					PlanRef:    hibernatorv1alpha1.PlanReference{Name: "test-plan"},
					ValidFrom:  metav1.Time{Time: time.Now().Add(24 * time.Hour)},
					ValidUntil: metav1.Time{Time: time.Now().Add(48 * time.Hour)},
					Type:       "extend",
					Windows:    []hibernatorv1alpha1.OffHourWindow{{Start: "06:00", End: "11:00", DaysOfWeek: []string{"SUN"}}},
				},
			},
			setup: func() client.Client {
				plan := &hibernatorv1alpha1.HibernatePlan{ObjectMeta: metav1.ObjectMeta{Name: "test-plan", Namespace: "default"}}
				existingException := &hibernatorv1alpha1.ScheduleException{
					ObjectMeta: metav1.ObjectMeta{Name: "pending-exception", Namespace: "default", Labels: map[string]string{wellknown.LabelPlan: "test-plan"}},
					Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
						PlanRef:    hibernatorv1alpha1.PlanReference{Name: "test-plan"},
						ValidFrom:  metav1.Time{Time: time.Now().Add(30 * time.Hour)},
						ValidUntil: metav1.Time{Time: time.Now().Add(60 * time.Hour)},
						Type:       "extend",
						Windows:    []hibernatorv1alpha1.OffHourWindow{{Start: "06:00", End: "11:00", DaysOfWeek: []string{"SUN"}}},
					},
					Status: hibernatorv1alpha1.ScheduleExceptionStatus{
						State: hibernatorv1alpha1.ExceptionStatePending,
					},
				}
				return setupTestClient(plan, existingException)
			},
			wantErr: true,
			errMsg:  "overlaps with existing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			validator := NewScheduleExceptionValidator(logr.Discard(), tt.setup())

			_, err := validator.ValidateCreate(context.Background(), tt.exception)

			if tt.wantErr {
				if err == nil {
					t.Errorf("ValidateCreate() expected error containing %q, got nil", tt.errMsg)
					return
				}
				if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("ValidateCreate() error = %v, want error containing %q", err, tt.errMsg)
				}
			} else {
				if err != nil {
					t.Errorf("ValidateCreate() unexpected error = %v", err)
				}
			}
		})
	}
}

func TestScheduleExceptionValidator_ValidateUpdate(t *testing.T) {
	tests := []struct {
		name         string
		oldException *hibernatorv1alpha1.ScheduleException
		newException *hibernatorv1alpha1.ScheduleException
		setup        func() client.Client
		wantErr      bool
		errMsg       string
	}{
		{
			name:         "valid update",
			oldException: validException(),
			newException: func() *hibernatorv1alpha1.ScheduleException {
				exc := validException()
				exc.Spec.Windows = append(exc.Spec.Windows, hibernatorv1alpha1.OffHourWindow{
					Start:      "01:00",
					End:        "06:00",
					DaysOfWeek: []string{"MON"},
				})
				return exc
			}(),
			setup: func() client.Client {
				plan := &hibernatorv1alpha1.HibernatePlan{
					ObjectMeta: metav1.ObjectMeta{Name: "test-plan", Namespace: "default"},
					Spec: hibernatorv1alpha1.HibernatePlanSpec{
						Schedule: hibernatorv1alpha1.Schedule{
							Timezone: "UTC",
							OffHours: []hibernatorv1alpha1.OffHourWindow{{Start: "20:00", End: "06:00", DaysOfWeek: []string{"MON"}}},
						},
					},
				}
				return setupTestClient(plan)
			},
			wantErr: false,
		},
		{
			name:         "invalid update - change planRef",
			oldException: validException(),
			newException: func() *hibernatorv1alpha1.ScheduleException {
				exc := validException()
				exc.Spec.PlanRef.Name = "different-plan"
				return exc
			}(),
			setup: func() client.Client {
				return setupTestClient()
			},
			wantErr: true,
			errMsg:  "Not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			validator := NewScheduleExceptionValidator(logr.Discard(), tt.setup())

			_, err := validator.ValidateUpdate(context.Background(), tt.oldException, tt.newException)

			if tt.wantErr {
				if err == nil {
					t.Errorf("ValidateUpdate() expected error containing %q, got nil", tt.errMsg)
					return
				}
				if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("ValidateUpdate() error = %v, want error containing %q", err, tt.errMsg)
				}
			} else {
				if err != nil {
					t.Errorf("ValidateUpdate() unexpected error = %v", err)
				}
			}
		})
	}
}

func TestScheduleExceptionValidator_ValidateDelete(t *testing.T) {
	validator := NewScheduleExceptionValidator(logr.Discard(), setupTestClient())
	exc := validException()
	_, err := validator.ValidateDelete(context.Background(), exc)
	if err != nil {
		t.Errorf("ValidateDelete() unexpected error = %v", err)
	}
}

/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package v1alpha1

import (
	"context"
	"testing"
	"time"

	"github.com/ardikabs/hibernator/internal/wellknown"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// validException returns a valid ScheduleException for tests.
func validException() *ScheduleException {
	return &ScheduleException{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-exception",
			Namespace: "default",
		},
		Spec: ScheduleExceptionSpec{
			PlanRef: PlanReference{
				Name:      "test-plan",
				Namespace: "default",
			},
			ValidFrom:  metav1.Time{Time: time.Now().Add(-24 * time.Hour)},    // Yesterday
			ValidUntil: metav1.Time{Time: time.Now().Add(7 * 24 * time.Hour)}, // 7 days from now
			Type:       "extend",
			Windows: []OffHourWindow{
				{
					Start:      "06:00",
					End:        "11:00",
					DaysOfWeek: []string{"SAT", "SUN"},
				},
			},
		},
	}
}

// setupTestClient creates a fake client with necessary schemes for testing.
func setupTestClient(objs ...client.Object) client.Client {
	scheme := runtime.NewScheme()
	_ = AddToScheme(scheme)

	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		Build()
}

func TestScheduleException_ValidateCreate(t *testing.T) {
	tests := []struct {
		name      string
		exception *ScheduleException
		setup     func() client.Client
		wantErr   bool
		errMsg    string
	}{
		{
			name:      "valid exception",
			exception: validException(),
			setup: func() client.Client {
				plan := &HibernatePlan{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-plan",
						Namespace: "default",
					},
					Spec: HibernatePlanSpec{
						Schedule: Schedule{
							Timezone: "UTC",
							OffHours: []OffHourWindow{
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
			exception: &ScheduleException{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-exception",
					Namespace: "default",
				},
				Spec: ScheduleExceptionSpec{
					PlanRef: PlanReference{
						Name:      "nonexistent-plan",
						Namespace: "default",
					},
					ValidFrom:  metav1.Time{Time: time.Now()},
					ValidUntil: metav1.Time{Time: time.Now().Add(7 * 24 * time.Hour)},
					Type:       "extend",
					Windows:    []OffHourWindow{{Start: "06:00", End: "11:00", DaysOfWeek: []string{"SAT"}}},
				},
			},
			setup: func() client.Client {
				return setupTestClient() // No plan
			},
			wantErr: true,
			errMsg:  "Not found",
		},
		{
			name: "invalid planRef - wrong namespace",
			exception: &ScheduleException{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-exception",
					Namespace: "default",
				},
				Spec: ScheduleExceptionSpec{
					PlanRef: PlanReference{
						Name:      "test-plan",
						Namespace: "other-namespace",
					},
					ValidFrom:  metav1.Time{Time: time.Now()},
					ValidUntil: metav1.Time{Time: time.Now().Add(7 * 24 * time.Hour)},
					Type:       "extend",
					Windows:    []OffHourWindow{{Start: "06:00", End: "11:00", DaysOfWeek: []string{"SAT"}}},
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
			exception: &ScheduleException{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-exception",
					Namespace: "default",
				},
				Spec: ScheduleExceptionSpec{
					PlanRef: PlanReference{
						Name:      "test-plan",
						Namespace: "default",
					},
					ValidFrom:  metav1.Time{Time: time.Now().Add(7 * 24 * time.Hour)},
					ValidUntil: metav1.Time{Time: time.Now()},
					Type:       "extend",
					Windows:    []OffHourWindow{{Start: "06:00", End: "11:00", DaysOfWeek: []string{"SAT"}}},
				},
			},
			setup: func() client.Client {
				plan := &HibernatePlan{
					ObjectMeta: metav1.ObjectMeta{Name: "test-plan", Namespace: "default"},
					Spec: HibernatePlanSpec{
						Schedule: Schedule{
							Timezone: "UTC",
							OffHours: []OffHourWindow{{Start: "20:00", End: "06:00", DaysOfWeek: []string{"MON"}}},
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
			exception: &ScheduleException{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-exception",
					Namespace: "default",
				},
				Spec: ScheduleExceptionSpec{
					PlanRef: PlanReference{
						Name:      "test-plan",
						Namespace: "default",
					},
					ValidFrom:  metav1.Time{Time: time.Now()},
					ValidUntil: metav1.Time{Time: time.Now().Add(100 * 24 * time.Hour)}, // 100 days
					Type:       "extend",
					Windows:    []OffHourWindow{{Start: "06:00", End: "11:00", DaysOfWeek: []string{"SAT"}}},
				},
			},
			setup: func() client.Client {
				plan := &HibernatePlan{
					ObjectMeta: metav1.ObjectMeta{Name: "test-plan", Namespace: "default"},
					Spec: HibernatePlanSpec{
						Schedule: Schedule{
							Timezone: "UTC",
							OffHours: []OffHourWindow{{Start: "20:00", End: "06:00", DaysOfWeek: []string{"MON"}}},
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
			exception: &ScheduleException{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-exception",
					Namespace: "default",
				},
				Spec: ScheduleExceptionSpec{
					PlanRef: PlanReference{
						Name:      "test-plan",
						Namespace: "default",
					},
					ValidFrom:  metav1.Time{Time: time.Now()},
					ValidUntil: metav1.Time{Time: time.Now().Add(7 * 24 * time.Hour)},
					Type:       "extend",
					LeadTime:   "1h", // Should only be set for suspend type
					Windows:    []OffHourWindow{{Start: "06:00", End: "11:00", DaysOfWeek: []string{"SAT"}}},
				},
			},
			setup: func() client.Client {
				plan := &HibernatePlan{
					ObjectMeta: metav1.ObjectMeta{Name: "test-plan", Namespace: "default"},
					Spec: HibernatePlanSpec{
						Schedule: Schedule{
							Timezone: "UTC",
							OffHours: []OffHourWindow{{Start: "20:00", End: "06:00", DaysOfWeek: []string{"MON"}}},
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
			exception: &ScheduleException{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-exception",
					Namespace: "default",
				},
				Spec: ScheduleExceptionSpec{
					PlanRef: PlanReference{
						Name:      "test-plan",
						Namespace: "default",
					},
					ValidFrom:  metav1.Time{Time: time.Now()},
					ValidUntil: metav1.Time{Time: time.Now().Add(7 * 24 * time.Hour)},
					Type:       "extend",
					Windows: []OffHourWindow{
						{Start: "25:00", End: "11:00", DaysOfWeek: []string{"SAT"}}, // Invalid hour
					},
				},
			},
			setup: func() client.Client {
				plan := &HibernatePlan{
					ObjectMeta: metav1.ObjectMeta{Name: "test-plan", Namespace: "default"},
					Spec: HibernatePlanSpec{
						Schedule: Schedule{
							Timezone: "UTC",
							OffHours: []OffHourWindow{{Start: "20:00", End: "06:00", DaysOfWeek: []string{"MON"}}},
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
			exception: &ScheduleException{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-exception",
					Namespace: "default",
				},
				Spec: ScheduleExceptionSpec{
					PlanRef: PlanReference{
						Name:      "test-plan",
						Namespace: "default",
					},
					ValidFrom:  metav1.Time{Time: time.Now()},
					ValidUntil: metav1.Time{Time: time.Now().Add(7 * 24 * time.Hour)},
					Type:       "extend",
					Windows: []OffHourWindow{
						{Start: "06:00", End: "11:00", DaysOfWeek: []string{"INVALID"}},
					},
				},
			},
			setup: func() client.Client {
				plan := &HibernatePlan{
					ObjectMeta: metav1.ObjectMeta{Name: "test-plan", Namespace: "default"},
					Spec: HibernatePlanSpec{
						Schedule: Schedule{
							Timezone: "UTC",
							OffHours: []OffHourWindow{{Start: "20:00", End: "06:00", DaysOfWeek: []string{"MON"}}},
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
			exception: &ScheduleException{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "new-exception",
					Namespace: "default",
				},
				Spec: ScheduleExceptionSpec{
					PlanRef: PlanReference{
						Name:      "test-plan",
						Namespace: "default",
					},
					ValidFrom:  metav1.Time{Time: time.Now()},
					ValidUntil: metav1.Time{Time: time.Now().Add(7 * 24 * time.Hour)},
					Type:       "extend",
					Windows:    []OffHourWindow{{Start: "06:00", End: "11:00", DaysOfWeek: []string{"SAT"}}},
				},
			},
			setup: func() client.Client {
				plan := &HibernatePlan{
					ObjectMeta: metav1.ObjectMeta{Name: "test-plan", Namespace: "default"},
					Spec: HibernatePlanSpec{
						Schedule: Schedule{
							Timezone: "UTC",
							OffHours: []OffHourWindow{{Start: "20:00", End: "06:00", DaysOfWeek: []string{"MON"}}},
						},
					},
				}
				// Existing active exception
				existingException := &ScheduleException{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "existing-exception",
						Namespace: "default",
						Labels:    map[string]string{wellknown.LabelPlan: "test-plan"},
					},
					Spec: ScheduleExceptionSpec{
						PlanRef:    PlanReference{Name: "test-plan", Namespace: "default"},
						ValidFrom:  metav1.Time{Time: time.Now()},
						ValidUntil: metav1.Time{Time: time.Now().Add(14 * 24 * time.Hour)},
						Type:       "extend",
						Windows:    []OffHourWindow{{Start: "06:00", End: "11:00", DaysOfWeek: []string{"SUN"}}},
					},
					Status: ScheduleExceptionStatus{
						State: ExceptionStateActive,
					},
				}
				return setupTestClient(plan, existingException)
			},
			wantErr: true,
			errMsg:  "overlaps with existing",
		},
		{
			name: "overlapping_pending_exception",
			exception: &ScheduleException{
				ObjectMeta: metav1.ObjectMeta{Name: "new-exception", Namespace: "default"},
				Spec: ScheduleExceptionSpec{
					PlanRef:    PlanReference{Name: "test-plan"},
					ValidFrom:  metav1.Time{Time: time.Now().Add(24 * time.Hour)},
					ValidUntil: metav1.Time{Time: time.Now().Add(48 * time.Hour)},
					Type:       "extend",
					Windows:    []OffHourWindow{{Start: "06:00", End: "11:00", DaysOfWeek: []string{"SUN"}}},
				},
			},
			setup: func() client.Client {
				plan := &HibernatePlan{ObjectMeta: metav1.ObjectMeta{Name: "test-plan", Namespace: "default"}}
				existingException := &ScheduleException{
					ObjectMeta: metav1.ObjectMeta{Name: "pending-exception", Namespace: "default", Labels: map[string]string{wellknown.LabelPlan: "test-plan"}},
					Spec: ScheduleExceptionSpec{
						PlanRef:    PlanReference{Name: "test-plan"},
						ValidFrom:  metav1.Time{Time: time.Now().Add(30 * time.Hour)},
						ValidUntil: metav1.Time{Time: time.Now().Add(60 * time.Hour)},
						Type:       "extend",
						Windows:    []OffHourWindow{{Start: "06:00", End: "11:00", DaysOfWeek: []string{"SUN"}}},
					},
					Status: ScheduleExceptionStatus{
						State: ExceptionStatePending,
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
			// Setup test client
			scheduleExceptionValidator = tt.setup()

			// Validate
			_, err := tt.exception.ValidateCreate(context.Background(), tt.exception)

			if tt.wantErr {
				if err == nil {
					t.Errorf("ValidateCreate() expected error containing %q, got nil", tt.errMsg)
					return
				}
				if tt.errMsg != "" && !contains(err.Error(), tt.errMsg) {
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

func TestScheduleException_ValidateUpdate(t *testing.T) {
	tests := []struct {
		name         string
		oldException *ScheduleException
		newException *ScheduleException
		setup        func() client.Client
		wantErr      bool
		errMsg       string
	}{
		{
			name:         "valid update",
			oldException: validException(),
			newException: func() *ScheduleException {
				exc := validException()
				exc.Spec.Windows = append(exc.Spec.Windows, OffHourWindow{
					Start:      "01:00",
					End:        "06:00",
					DaysOfWeek: []string{"MON"},
				})
				return exc
			}(),
			setup: func() client.Client {
				plan := &HibernatePlan{
					ObjectMeta: metav1.ObjectMeta{Name: "test-plan", Namespace: "default"},
					Spec: HibernatePlanSpec{
						Schedule: Schedule{
							Timezone: "UTC",
							OffHours: []OffHourWindow{{Start: "20:00", End: "06:00", DaysOfWeek: []string{"MON"}}},
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
			newException: func() *ScheduleException {
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
			scheduleExceptionValidator = tt.setup()

			_, err := tt.newException.ValidateUpdate(context.Background(), tt.oldException, tt.newException)

			if tt.wantErr {
				if err == nil {
					t.Errorf("ValidateUpdate() expected error containing %q, got nil", tt.errMsg)
					return
				}
				if tt.errMsg != "" && !contains(err.Error(), tt.errMsg) {
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

func TestScheduleException_ValidateDelete(t *testing.T) {
	exc := validException()
	_, err := exc.ValidateDelete(context.Background(), exc)
	if err != nil {
		t.Errorf("ValidateDelete() unexpected error = %v", err)
	}
}

// contains checks if a string contains a substring.
func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

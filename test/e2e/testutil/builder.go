//go:build e2e

package testutil

import (
	"fmt"
	"time"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/wellknown"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// HibernatePlanBuilder simplifies the creation of HibernatePlan objects for testing.
type HibernatePlanBuilder struct {
	plan *hibernatorv1alpha1.HibernatePlan
}

// NewHibernatePlanBuilder initializes a new builder with a basic plan structure.
func NewHibernatePlanBuilder(name, namespace string) *HibernatePlanBuilder {
	return &HibernatePlanBuilder{
		plan: &hibernatorv1alpha1.HibernatePlan{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-%s", name, time.Now().Format("150405")),
				Namespace: namespace,
			},
			Spec: hibernatorv1alpha1.HibernatePlanSpec{
				Schedule: hibernatorv1alpha1.Schedule{
					Timezone: "UTC",
					OffHours: []hibernatorv1alpha1.OffHourWindow{
						{
							Start:      "20:00",
							End:        "06:00",
							DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"},
						},
					},
				},
				Execution: hibernatorv1alpha1.Execution{
					Strategy: hibernatorv1alpha1.ExecutionStrategy{
						Type: hibernatorv1alpha1.StrategySequential,
					},
				},
				Targets: []hibernatorv1alpha1.Target{
					{
						Name: "target1",
						Type: "ec2",
						ConnectorRef: hibernatorv1alpha1.ConnectorRef{
							Kind: "CloudProvider",
							Name: "fake-aws",
						},
					},
				},
			},
		},
	}
}

// WithSchedule sets the hibernation schedule for the plan.
func (b *HibernatePlanBuilder) WithSchedule(start, end string, days ...string) *HibernatePlanBuilder {
	if len(days) == 0 {
		days = []string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"}
	}
	b.plan.Spec.Schedule = hibernatorv1alpha1.Schedule{
		Timezone: "UTC",
		OffHours: []hibernatorv1alpha1.OffHourWindow{
			{
				Start:      start,
				End:        end,
				DaysOfWeek: days,
			},
		},
	}
	return b
}

// WithExecutionStrategy sets the execution strategy for the plan.
func (b *HibernatePlanBuilder) WithExecutionStrategy(strategy hibernatorv1alpha1.ExecutionStrategy) *HibernatePlanBuilder {
	b.plan.Spec.Execution.Strategy = strategy
	return b
}

// WithTarget adds a single target to the plan.
func (b *HibernatePlanBuilder) WithTarget(targets ...hibernatorv1alpha1.Target) *HibernatePlanBuilder {
	b.plan.Spec.Targets = targets
	return b
}

// WithSuspend sets the suspend flag on the plan spec.
func (b *HibernatePlanBuilder) WithSuspend(suspend bool) *HibernatePlanBuilder {
	b.plan.Spec.Suspend = suspend
	return b
}

// WithAnnotation adds or updates an annotation on the plan.
func (b *HibernatePlanBuilder) WithAnnotation(key, value string) *HibernatePlanBuilder {
	if b.plan.Annotations == nil {
		b.plan.Annotations = make(map[string]string)
	}
	b.plan.Annotations[key] = value
	return b
}

// WithBehavior sets the failure behavior for the plan.
func (b *HibernatePlanBuilder) WithBehavior(behavior hibernatorv1alpha1.Behavior) *HibernatePlanBuilder {
	b.plan.Spec.Behavior = behavior
	return b
}

// WithOffHours replaces the hibernation windows on the plan schedule.
// Call this after WithSchedule (or after setting a timezone) to override the default single-window
// with multiple windows, enabling OR-combined multi-window evaluation.
func (b *HibernatePlanBuilder) WithOffHours(windows ...hibernatorv1alpha1.OffHourWindow) *HibernatePlanBuilder {
	b.plan.Spec.Schedule.OffHours = windows
	return b
}

// WithTimezone overrides the timezone used for schedule evaluation.
// Must be called after WithSchedule, since WithSchedule resets the timezone to "UTC".
func (b *HibernatePlanBuilder) WithTimezone(tz string) *HibernatePlanBuilder {
	b.plan.Spec.Schedule.Timezone = tz
	return b
}

// Build returns the constructed HibernatePlan and an anonymous function to retrieve target names.
func (b *HibernatePlanBuilder) Build() (*hibernatorv1alpha1.HibernatePlan, func() []string) {
	targetNames := func() []string {
		names := make([]string, len(b.plan.Spec.Targets))
		for i, t := range b.plan.Spec.Targets {
			names[i] = t.Name
		}
		return names
	}
	return b.plan, targetNames
}

// ScheduleExceptionBuilder simplifies the creation of ScheduleException objects for testing.
type ScheduleExceptionBuilder struct {
	exc *hibernatorv1alpha1.ScheduleException
}

// NewScheduleExceptionBuilder initializes a builder for ScheduleException.
// planName is the name of the HibernatePlan to apply the exception to.
// The builder pre-sets the wellknown.LabelPlan label so the plan provider can find the exception.
func NewScheduleExceptionBuilder(name, namespace, planName string) *ScheduleExceptionBuilder {
	return &ScheduleExceptionBuilder{
		exc: &hibernatorv1alpha1.ScheduleException{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-%s", name, time.Now().Format("150405")),
				Namespace: namespace,
				// Pre-set the label so plan provider can list exceptions by plan name
				// before the LifecycleProcessor runs ensurePlanLabel.
				Labels: map[string]string{
					wellknown.LabelPlan: planName,
				},
			},
			Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
				PlanRef: hibernatorv1alpha1.PlanReference{
					Name: planName,
				},
				Type: hibernatorv1alpha1.ExceptionSuspend,
			},
		},
	}
}

// WithType sets the exception type (extend, suspend, replace).
func (b *ScheduleExceptionBuilder) WithType(t hibernatorv1alpha1.ExceptionType) *ScheduleExceptionBuilder {
	b.exc.Spec.Type = t
	return b
}

// WithValidity sets the validity period for the exception.
func (b *ScheduleExceptionBuilder) WithValidity(from, until time.Time) *ScheduleExceptionBuilder {
	b.exc.Spec.ValidFrom = metav1.Time{Time: from}
	b.exc.Spec.ValidUntil = metav1.Time{Time: until}
	return b
}

// WithWindows sets the time windows for the exception.
func (b *ScheduleExceptionBuilder) WithWindows(windows ...hibernatorv1alpha1.OffHourWindow) *ScheduleExceptionBuilder {
	b.exc.Spec.Windows = windows
	return b
}

// WithLeadTime sets the lead time (only valid for suspend type).
func (b *ScheduleExceptionBuilder) WithLeadTime(leadTime string) *ScheduleExceptionBuilder {
	b.exc.Spec.LeadTime = leadTime
	return b
}

// Build returns the constructed ScheduleException.
func (b *ScheduleExceptionBuilder) Build() *hibernatorv1alpha1.ScheduleException {
	return b.exc
}

package testutil

import (
	"fmt"
	"time"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
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

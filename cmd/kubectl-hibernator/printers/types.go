/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package printers

import (
	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/common"
	corev1 "k8s.io/api/core/v1"
)

// ScheduleOutput is a wrapper for printing schedule evaluation results
type ScheduleOutput struct {
	Plan       hibernatorv1alpha1.HibernatePlan
	Result     interface{} // EvaluationResult
	Exceptions []hibernatorv1alpha1.ExceptionReference
	Events     []common.ScheduleEvent
}

// PlanListItem represents a single plan with computed next event
type PlanListItem struct {
	Plan      hibernatorv1alpha1.HibernatePlan `json:"plan"`
	NextEvent *common.ScheduleEvent            `json:"nextEvent,omitempty"`
}

// PlanListOutput is a wrapper for printing enriched plan list
type PlanListOutput struct {
	Items []PlanListItem `json:"items"`
}

// StatusOutput is a wrapper for printing plan status
type StatusOutput struct {
	Plan hibernatorv1alpha1.HibernatePlan
}

// RestoreDetailOutput is a wrapper for printing restore resource details
type RestoreDetailOutput struct {
	Plan       string
	Namespace  string
	TargetData interface{} // restore.Data
	ResourceID string
	State      map[string]any
}

// RestoreResourcesOutput is a wrapper for listing resources in restore point
type RestoreResourcesOutput struct {
	ConfigMap corev1.ConfigMap
	Target    string
}

// PlanListItemJSON represents a single plan in the list output.
type PlanListItemJSON struct {
	Name      string                `json:"name"`
	Namespace string                `json:"namespace"`
	Phase     string                `json:"phase"`
	Suspended bool                  `json:"suspended"`
	NextEvent *common.ScheduleEvent `json:"nextEvent,omitempty"`
	Age       string                `json:"age"`
}

// PlanListJSON represents the JSON output for the list command.
type PlanListJSON struct {
	Items []PlanListItemJSON `json:"items"`
}

// ScheduleJSON represents the JSON output for the preview/schedule command.
type ScheduleJSON struct {
	Plan       string                   `json:"plan"`
	Namespace  string                   `json:"namespace,omitempty"`
	Timezone   string                   `json:"timezone"`
	OffHours   []OffHourWindowJSON      `json:"offHours"`
	State      ScheduleStateJSON        `json:"currentState"`
	Events     []common.ScheduleEvent   `json:"upcomingEvents"`
	Exceptions []ExceptionReferenceJSON `json:"activeExceptions,omitempty"`
}

type ScheduleStateJSON struct {
	Current       string `json:"current"`
	NextHibernate string `json:"nextHibernate"`
	NextWakeUp    string `json:"nextWakeUp"`
}

// PlanJSON represents the JSON output for a single HibernatePlan (describe command).
type PlanJSON struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Created   string `json:"created"`

	Schedule  PlanScheduleJSON  `json:"schedule"`
	Behavior  PlanBehaviorJSON  `json:"behavior"`
	Execution PlanExecutionJSON `json:"execution"`
	Targets   []PlanTargetJSON  `json:"targets"`
	Status    PlanStatusJSON    `json:"status"`
}

type PlanScheduleJSON struct {
	Timezone string              `json:"timezone"`
	OffHours []OffHourWindowJSON `json:"offHours"`
}

type OffHourWindowJSON struct {
	Start      string   `json:"start"`
	End        string   `json:"end"`
	DaysOfWeek []string `json:"daysOfWeek"`
}

type PlanBehaviorJSON struct {
	Mode    string `json:"mode"`
	Retries int32  `json:"retries"`
}

type PlanExecutionJSON struct {
	StrategyType   string               `json:"strategyType"`
	MaxConcurrency *int32               `json:"maxConcurrency,omitempty"`
	Dependencies   []PlanDependencyJSON `json:"dependencies,omitempty"`
}

type PlanDependencyJSON struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type PlanTargetJSON struct {
	Name         string                 `json:"name"`
	Type         string                 `json:"type"`
	ConnectorRef string                 `json:"connectorRef"`
	Parameters   map[string]interface{} `json:"parameters,omitempty"`
}

type PlanStatusJSON struct {
	Phase            string                   `json:"phase"`
	Suspended        bool                     `json:"suspended"`
	SuspendUntil     string                   `json:"suspendUntil,omitempty"`
	SuspendReason    string                   `json:"suspendReason,omitempty"`
	CurrentCycleID   string                   `json:"currentCycleId,omitempty"`
	CurrentOperation string                   `json:"currentOperation,omitempty"`
	ErrorMessage     string                   `json:"errorMessage,omitempty"`
	RetryCount       int32                    `json:"retryCount,omitempty"`
	LastRetryTime    string                   `json:"lastRetryTime,omitempty"`
	Executions       []ExecutionStatusJSON    `json:"executions,omitempty"`
	ActiveExceptions []ExceptionReferenceJSON `json:"activeExceptions,omitempty"`
}

type ExecutionStatusJSON struct {
	Target     string `json:"target"`
	State      string `json:"state"`
	Attempts   int32  `json:"attempts,omitempty"`
	Message    string `json:"message,omitempty"`
	StartedAt  string `json:"startedAt,omitempty"`
	FinishedAt string `json:"finishedAt,omitempty"`
}

// RestoreDetailJSON represents the JSON output for restore detail command.
type RestoreDetailJSON struct {
	Plan       string         `json:"plan"`
	Namespace  string         `json:"namespace"`
	Target     string         `json:"target"`
	ResourceID string         `json:"resourceId"`
	Executor   string         `json:"executor"`
	IsLive     bool           `json:"isLive"`
	CreatedAt  string         `json:"createdAt,omitempty"`
	CapturedAt string         `json:"capturedAt,omitempty"`
	State      map[string]any `json:"state"`
}

// RestoreResourcesJSON represents the JSON output for restore resources list.
type RestoreResourcesJSON struct {
	Resources []RestoreResourceJSON `json:"resources"`
}

type RestoreResourceJSON struct {
	ResourceID string `json:"resourceId"`
	Target     string `json:"target"`
	Executor   string `json:"executor"`
	IsLive     bool   `json:"isLive"`
	CapturedAt string `json:"capturedAt,omitempty"`
}

type ExceptionReferenceJSON struct {
	Name       string `json:"name"`
	ValidUntil string `json:"validUntil"`
}

type RestoreShowJSONOutput struct {
	Plan           string             `json:"plan"`
	Namespace      string             `json:"namespace"`
	RestorePoints  []RestorePointData `json:"restorePoints,omitempty"`
	TotalResources int                `json:"totalResources"`
}

type RestorePointData struct {
	Target        string `json:"target"`
	Executor      string `json:"executor"`
	IsLive        bool   `json:"isLive"`
	CapturedAt    string `json:"capturedAt,omitempty"`
	ResourceCount int    `json:"resourceCount"`
	CreatedAt     string `json:"createdAt,omitempty"`
}

type RestoreResource struct {
	ResourceID string `json:"resourceId"`
	Target     string `json:"target"`
	Executor   string `json:"executor"`
	IsLive     bool   `json:"isLive"`
	CapturedAt string `json:"capturedAt,omitempty"`
}

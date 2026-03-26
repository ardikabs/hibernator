/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package scheduler

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMergeByType(t *testing.T) {
	vf := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	vu := time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)

	exceptions := []*Exception{
		{Type: ExceptionExtend, ValidFrom: vf, ValidUntil: vu},
		{Type: ExceptionSuspend, ValidFrom: vf, ValidUntil: vu},
	}

	if got := mergeByType(exceptions, ExceptionExtend); got == nil || got.Type != ExceptionExtend {
		t.Errorf("mergeByType(extend) = %v, want extend exception", got)
	}
	if got := mergeByType(exceptions, ExceptionSuspend); got == nil || got.Type != ExceptionSuspend {
		t.Errorf("mergeByType(suspend) = %v, want suspend exception", got)
	}
	if got := mergeByType(exceptions, ExceptionReplace); got != nil {
		t.Errorf("mergeByType(replace) = %v, want nil", got)
	}
	if got := mergeByType(nil, ExceptionExtend); got != nil {
		t.Errorf("mergeByType(nil, extend) = %v, want nil", got)
	}
}

func TestMergeByType_SameType_MergesWindowsAndExpandsValidity(t *testing.T) {
	vf1 := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	vu1 := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)
	vf2 := time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC)
	vu2 := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)

	exceptions := []*Exception{
		{
			Type:       ExceptionSuspend,
			ValidFrom:  vf1,
			ValidUntil: vu1,
			LeadTime:   3 * time.Minute,
			Windows:    []OffHourWindow{{Start: "08:00", End: "12:00", DaysOfWeek: []string{"MON"}}},
		},
		{
			Type:       ExceptionSuspend,
			ValidFrom:  vf2,
			ValidUntil: vu2,
			LeadTime:   10 * time.Minute,
			Windows:    []OffHourWindow{{Start: "14:00", End: "18:00", DaysOfWeek: []string{"WED"}}},
		},
	}

	got := mergeByType(exceptions, ExceptionSuspend)
	require.NotNil(t, got)
	assert.Equal(t, ExceptionSuspend, got.Type)
	assert.Equal(t, vf1, got.ValidFrom, "ValidFrom should be the earliest")
	assert.Equal(t, vu2, got.ValidUntil, "ValidUntil should be the latest")
	assert.Equal(t, 10*time.Minute, got.LeadTime, "LeadTime should be the max")
	require.Len(t, got.Windows, 2)
	assert.Equal(t, "08:00", got.Windows[0].Start)
	assert.Equal(t, "14:00", got.Windows[1].Start)
}

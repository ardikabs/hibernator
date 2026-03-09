/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package v1alpha1

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// KindOf tests

func TestKindOf_HibernatePlan(t *testing.T) {
	if got := KindOf(&HibernatePlan{}); got != "HibernatePlan" {
		t.Errorf("KindOf(*HibernatePlan) = %q, want %q", got, "HibernatePlan")
	}
}

func TestKindOf_ScheduleException(t *testing.T) {
	if got := KindOf(&ScheduleException{}); got != "ScheduleException" {
		t.Errorf("KindOf(*ScheduleException) = %q, want %q", got, "ScheduleException")
	}
}

func TestKindOf_CloudProvider(t *testing.T) {
	if got := KindOf(&CloudProvider{}); got != "CloudProvider" {
		t.Errorf("KindOf(*CloudProvider) = %q, want %q", got, "CloudProvider")
	}
}

func TestKindOf_K8SCluster(t *testing.T) {
	if got := KindOf(&K8SCluster{}); got != "K8SCluster" {
		t.Errorf("KindOf(*K8SCluster) = %q, want %q", got, "K8SCluster")
	}
}

func TestKindOf_Unknown(t *testing.T) {
	if got := KindOf("string value"); got != "Unknown" {
		t.Errorf("KindOf(string) = %q, want %q", got, "Unknown")
	}
}

func TestKindOf_NilHibernatePlan(t *testing.T) {
	var p *HibernatePlan
	// nil pointer with concrete type still matches in type switch
	if got := KindOf(p); got != "HibernatePlan" {
		t.Errorf("KindOf(nil *HibernatePlan) = %q, want %q", got, "HibernatePlan")
	}
}

// kindOfGeneric mimics the generic pattern used in NewUpdateProcessor:
//
//	var zero T
//	kind := KindOf(zero)
//
// When T is a pointer type (e.g. *HibernatePlan), `var zero T` yields a typed nil
// that the type switch correctly matches. By contrast, `new(T)` would yield a
// **HibernatePlan double-pointer that falls to the "Unknown" default branch.
func kindOfGeneric[T any]() string {
	var zero T
	return KindOf(zero)
}

func TestKindOf_ViaGeneric_HibernatePlan(t *testing.T) {
	if got := kindOfGeneric[*HibernatePlan](); got != "HibernatePlan" {
		t.Errorf("kindOfGeneric[*HibernatePlan]() = %q, want %q", got, "HibernatePlan")
	}
}

func TestKindOf_ViaGeneric_ScheduleException(t *testing.T) {
	if got := kindOfGeneric[*ScheduleException](); got != "ScheduleException" {
		t.Errorf("kindOfGeneric[*ScheduleException]() = %q, want %q", got, "ScheduleException")
	}
}

func TestKindOf_ViaGeneric_CloudProvider(t *testing.T) {
	if got := kindOfGeneric[*CloudProvider](); got != "CloudProvider" {
		t.Errorf("kindOfGeneric[*CloudProvider]() = %q, want %q", got, "CloudProvider")
	}
}

func TestKindOf_ViaGeneric_K8SCluster(t *testing.T) {
	if got := kindOfGeneric[*K8SCluster](); got != "K8SCluster" {
		t.Errorf("kindOfGeneric[*K8SCluster]() = %q, want %q", got, "K8SCluster")
	}
}

// TestKindOf_DoublePointer_IsUnknown documents the broken behaviour that would
// result from calling KindOf(new(T)) inside a generic function where T is already
// a pointer type. new(T) allocates a **HibernatePlan, which the type switch cannot
// match, so it must return "Unknown".
func TestKindOf_DoublePointer_IsUnknown(t *testing.T) {
	// Simulate new(T) where T = *HibernatePlan → produces **HibernatePlan.
	p := new(*HibernatePlan)
	if got := KindOf(p); got != "Unknown" {
		t.Errorf("KindOf(**HibernatePlan) = %q, want %q (double-pointer must not match)", got, "Unknown")
	}
}

// ExceptionReferencesEqual tests

func newExceptionRef(name string) ExceptionReference {
	t0 := metav1.NewTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	t1 := metav1.NewTime(time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC))
	return ExceptionReference{
		Name:       name,
		Type:       ExceptionExtend,
		State:      ExceptionStatePending,
		ValidFrom:  t0,
		ValidUntil: t1,
	}
}

func TestExceptionReferencesEqual_BothEmpty(t *testing.T) {
	if !ExceptionReferencesEqual(nil, nil) {
		t.Error("nil slices should be equal")
	}
	if !ExceptionReferencesEqual([]ExceptionReference{}, []ExceptionReference{}) {
		t.Error("empty slices should be equal")
	}
}

func TestExceptionReferencesEqual_DifferentLengths(t *testing.T) {
	a := []ExceptionReference{newExceptionRef("a")}
	if ExceptionReferencesEqual(a, nil) {
		t.Error("slices with different lengths should not be equal")
	}
	if ExceptionReferencesEqual(nil, a) {
		t.Error("slices with different lengths should not be equal")
	}
}

func TestExceptionReferencesEqual_Equal(t *testing.T) {
	a := []ExceptionReference{newExceptionRef("a"), newExceptionRef("b")}
	b := []ExceptionReference{newExceptionRef("a"), newExceptionRef("b")}
	if !ExceptionReferencesEqual(a, b) {
		t.Error("identical slices should be equal")
	}
}

func TestExceptionReferencesEqual_DifferentName(t *testing.T) {
	a := []ExceptionReference{newExceptionRef("a")}
	b := []ExceptionReference{newExceptionRef("b")}
	if ExceptionReferencesEqual(a, b) {
		t.Error("slices with different names should not be equal")
	}
}

func TestExceptionReferencesEqual_DifferentType(t *testing.T) {
	ref1 := newExceptionRef("x")
	ref2 := newExceptionRef("x")
	ref2.Type = ExceptionSuspend
	if ExceptionReferencesEqual([]ExceptionReference{ref1}, []ExceptionReference{ref2}) {
		t.Error("slices with different Type should not be equal")
	}
}

func TestExceptionReferencesEqual_DifferentState(t *testing.T) {
	ref1 := newExceptionRef("x")
	ref2 := newExceptionRef("x")
	ref2.State = ExceptionStateActive
	if ExceptionReferencesEqual([]ExceptionReference{ref1}, []ExceptionReference{ref2}) {
		t.Error("slices with different State should not be equal")
	}
}

func TestExceptionReferencesEqual_DifferentValidFrom(t *testing.T) {
	ref1 := newExceptionRef("x")
	ref2 := newExceptionRef("x")
	ref2.ValidFrom = metav1.NewTime(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	if ExceptionReferencesEqual([]ExceptionReference{ref1}, []ExceptionReference{ref2}) {
		t.Error("slices with different ValidFrom should not be equal")
	}
}

func TestExceptionReferencesEqual_DifferentValidUntil(t *testing.T) {
	ref1 := newExceptionRef("x")
	ref2 := newExceptionRef("x")
	ref2.ValidUntil = metav1.NewTime(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC))
	if ExceptionReferencesEqual([]ExceptionReference{ref1}, []ExceptionReference{ref2}) {
		t.Error("slices with different ValidUntil should not be equal")
	}
}

func TestExceptionReferencesEqual_AppliedAt_OneNil(t *testing.T) {
	ref1 := newExceptionRef("x")
	ref2 := newExceptionRef("x")
	applied := metav1.NewTime(time.Date(2026, 1, 1, 6, 0, 0, 0, time.UTC))
	ref1.AppliedAt = &applied
	// ref2.AppliedAt is nil
	if ExceptionReferencesEqual([]ExceptionReference{ref1}, []ExceptionReference{ref2}) {
		t.Error("one AppliedAt nil and other non-nil should not be equal")
	}
}

func TestExceptionReferencesEqual_AppliedAt_BothNil(t *testing.T) {
	ref1 := newExceptionRef("x")
	ref2 := newExceptionRef("x")
	if !ExceptionReferencesEqual([]ExceptionReference{ref1}, []ExceptionReference{ref2}) {
		t.Error("both AppliedAt nil should be equal")
	}
}

func TestExceptionReferencesEqual_AppliedAt_DifferentValues(t *testing.T) {
	ref1 := newExceptionRef("x")
	ref2 := newExceptionRef("x")
	t1 := metav1.NewTime(time.Date(2026, 1, 1, 6, 0, 0, 0, time.UTC))
	t2 := metav1.NewTime(time.Date(2026, 1, 1, 7, 0, 0, 0, time.UTC))
	ref1.AppliedAt = &t1
	ref2.AppliedAt = &t2
	if ExceptionReferencesEqual([]ExceptionReference{ref1}, []ExceptionReference{ref2}) {
		t.Error("different AppliedAt values should not be equal")
	}
}

func TestExceptionReferencesEqual_ExpiredAt_OneNil(t *testing.T) {
	ref1 := newExceptionRef("x")
	ref2 := newExceptionRef("x")
	expired := metav1.NewTime(time.Date(2026, 1, 2, 6, 0, 0, 0, time.UTC))
	ref1.ExpiredAt = &expired
	if ExceptionReferencesEqual([]ExceptionReference{ref1}, []ExceptionReference{ref2}) {
		t.Error("one ExpiredAt nil and other non-nil should not be equal")
	}
}

func TestExceptionReferencesEqual_ExpiredAt_DifferentValues(t *testing.T) {
	ref1 := newExceptionRef("x")
	ref2 := newExceptionRef("x")
	t1 := metav1.NewTime(time.Date(2026, 1, 2, 6, 0, 0, 0, time.UTC))
	t2 := metav1.NewTime(time.Date(2026, 1, 2, 7, 0, 0, 0, time.UTC))
	ref1.ExpiredAt = &t1
	ref2.ExpiredAt = &t2
	if ExceptionReferencesEqual([]ExceptionReference{ref1}, []ExceptionReference{ref2}) {
		t.Error("different ExpiredAt values should not be equal")
	}
}

func TestExceptionReferencesEqual_AppliedAndExpired_Equal(t *testing.T) {
	ref1 := newExceptionRef("x")
	ref2 := newExceptionRef("x")
	applied := metav1.NewTime(time.Date(2026, 1, 1, 6, 0, 0, 0, time.UTC))
	expired := metav1.NewTime(time.Date(2026, 1, 2, 6, 0, 0, 0, time.UTC))
	ref1.AppliedAt = &applied
	ref1.ExpiredAt = &expired
	ref2.AppliedAt = &applied
	ref2.ExpiredAt = &expired
	if !ExceptionReferencesEqual([]ExceptionReference{ref1}, []ExceptionReference{ref2}) {
		t.Error("same AppliedAt and ExpiredAt should be equal")
	}
}

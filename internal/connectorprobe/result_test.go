/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package connectorprobe

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
)

func TestResultMarshal_RoundTrip(t *testing.T) {
	in := Result{
		Ready:             true,
		Reason:            "Verified",
		Message:           "ok",
		AccessVerified:    true,
		EligibleExecutors: 2,
		CheckedExecutors:  1,
		ExecutorAccess: []hibernatorv1alpha1.ExecutorAccess{
			{Type: "ec2", Allowed: true, Verification: hibernatorv1alpha1.VerificationPolicySimulation},
		},
	}
	out, err := Unmarshal(in.Marshal())
	require.NoError(t, err)
	assert.Equal(t, in, out)
}

func TestResultMarshal_TruncatesOversize(t *testing.T) {
	tests := []struct {
		name string
		msg  string
	}{
		{"ascii", strings.Repeat("x", 200)},
		{"multibyte", strings.Repeat("世", 200)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := Result{Ready: false, Reason: "AccessDenied", Message: tt.msg}
			for i := 0; i < 60; i++ {
				r.ExecutorAccess = append(r.ExecutorAccess, hibernatorv1alpha1.ExecutorAccess{
					Type: "ec2", Reason: "AccessDenied", Message: tt.msg,
				})
			}
			s := r.Marshal()
			assert.LessOrEqual(t, len(s), MaxTerminationBytes)
			assert.True(t, json.Valid([]byte(s)))
			assert.True(t, utf8.ValidString(s))

			out, err := Unmarshal(s)
			require.NoError(t, err)
			assert.False(t, out.Ready)
			assert.Equal(t, "AccessDenied", out.Reason)
			assert.Contains(t, out.AccessMessage, "truncated")
		})
	}
}

func TestResultMarshal_HugeTopLevel(t *testing.T) {
	r := Result{Reason: "ProbeError", Message: strings.Repeat("a", 10000), AccessMessage: strings.Repeat("b", 10000)}
	s := r.Marshal()
	assert.LessOrEqual(t, len(s), MaxTerminationBytes)
	out, err := Unmarshal(s)
	require.NoError(t, err)
	assert.Equal(t, "ProbeError", out.Reason)
}

func TestResultMarshal_LastResortKeepsCounts(t *testing.T) {
	r := Result{Ready: true, Reason: strings.Repeat("r", 10000), AccessVerified: true, EligibleExecutors: 3, CheckedExecutors: 2}
	s := r.Marshal()
	assert.LessOrEqual(t, len(s), MaxTerminationBytes)
	out, err := Unmarshal(s)
	require.NoError(t, err)
	assert.Equal(t, 3, out.EligibleExecutors)
	assert.Equal(t, 2, out.CheckedExecutors)
}

func TestResultMarshal_OmitsZeroCounts(t *testing.T) {
	assert.NotContains(t, Result{Ready: true}.Marshal(), "Executors")
}

func TestUnmarshal_Invalid(t *testing.T) {
	for _, in := range []string{"", "not json"} {
		_, err := Unmarshal(in)
		assert.Error(t, err, in)
	}
}

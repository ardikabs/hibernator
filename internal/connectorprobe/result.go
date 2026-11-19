/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

// Package connectorprobe defines the result a connector probe runner reports
// through the container termination message.
package connectorprobe

import (
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
)

// MaxTerminationBytes is the Kubernetes termination message size limit.
const MaxTerminationBytes = 4096

// messageCaps are the progressively smaller rune limits applied to messages.
var messageCaps = []int{256, 128, 64, 32, 0}

// Result is the outcome of a connector probe.
type Result struct {
	Ready          bool                                `json:"ready"`
	Reason         string                              `json:"reason,omitempty"`
	Message        string                              `json:"message,omitempty"`
	AccessVerified bool                                `json:"accessVerified"`
	AccessReason   string                              `json:"accessReason,omitempty"`
	AccessMessage  string                              `json:"accessMessage,omitempty"`
	ExecutorAccess []hibernatorv1alpha1.ExecutorAccess `json:"executorAccess,omitempty"`
	// EligibleExecutors is how many executors declare requirements for the connector kind.
	EligibleExecutors int `json:"eligibleExecutors,omitempty"`
	// CheckedExecutors is how many of them got an ExecutorAccess entry before truncation.
	CheckedExecutors int `json:"checkedExecutors,omitempty"`
}

// Marshal encodes the result as JSON of at most MaxTerminationBytes.
// Oversize results are reduced by shortening messages, then dropping trailing
// executor entries; the output is never cut mid-encoding.
func (r Result) Marshal() string {
	if s, ok := encode(r); ok {
		return s
	}

	total := len(r.ExecutorAccess)
	// Shorten messages first, keeping every entry.
	for _, c := range messageCaps {
		t := shorten(r, c)
		t.AccessMessage = truncationNote(t.AccessMessage, 0)
		if s, ok := encode(t); ok {
			return s
		}
	}

	// Then drop trailing entries, with messages at the smallest cap.
	base := shorten(r, messageCaps[len(messageCaps)-1])
	for n := total - 1; n >= 0; n-- {
		t := base
		t.ExecutorAccess = base.ExecutorAccess[:n]
		t.AccessMessage = truncationNote(t.AccessMessage, total-n)
		if s, ok := encode(t); ok {
			return s
		}
	}

	// Last resort: keep only the verdict.
	t := Result{Ready: r.Ready, AccessVerified: r.AccessVerified, AccessMessage: "truncated",
		EligibleExecutors: r.EligibleExecutors, CheckedExecutors: r.CheckedExecutors}
	t.Reason = shortenString(r.Reason, 256)
	t.AccessReason = shortenString(r.AccessReason, 256)
	s, _ := encode(t)
	return s
}

// Unmarshal decodes a termination message produced by Marshal.
func Unmarshal(s string) (Result, error) {
	if s == "" {
		return Result{}, errors.New("empty probe result")
	}
	var r Result
	if err := json.Unmarshal([]byte(s), &r); err != nil {
		return Result{}, fmt.Errorf("decode probe result: %w", err)
	}
	return r, nil
}

func encode(r Result) (string, bool) {
	b, err := json.Marshal(r)
	if err != nil {
		return "", false
	}
	return string(b), len(b) <= MaxTerminationBytes
}

// shorten returns a copy of r with all messages limited to max runes.
func shorten(r Result, max int) Result {
	r.Message = shortenString(r.Message, max)
	r.AccessMessage = shortenString(r.AccessMessage, max)
	entries := make([]hibernatorv1alpha1.ExecutorAccess, len(r.ExecutorAccess))
	for i, e := range r.ExecutorAccess {
		e.Message = shortenString(e.Message, max)
		entries[i] = e
	}
	r.ExecutorAccess = entries
	return r
}

func truncationNote(msg string, dropped int) string {
	note := "result truncated to fit termination message"
	if dropped > 0 {
		note = fmt.Sprintf("%s: %d executor entries omitted", note, dropped)
	}
	if msg == "" {
		return note
	}
	return msg + " (" + note + ")"
}

// shortenString limits s to max runes, never splitting a multi-byte character.
func shortenString(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	return string([]rune(s)[:max])
}

/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package executor

import (
	"fmt"
	"strings"
	"sync"
)

// ErrorList is a thread-safe accumulator for errors.
// It is useful when multiple goroutines may encounter errors and the caller
// wants to collect them for later reporting instead of failing fast.
type ErrorList struct {
	mu   sync.Mutex
	errs []error
}

// Add appends an error to the list. Nil errors are ignored.
func (e *ErrorList) Add(err error) {
	if err == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.errs = append(e.errs, err)
}

// Addf appends a formatted error to the list.
func (e *ErrorList) Addf(format string, args ...interface{}) {
	e.Add(fmt.Errorf(format, args...))
}

// Len returns the number of accumulated errors.
func (e *ErrorList) Len() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.errs)
}

// Join returns all accumulated error messages joined by the given separator.
func (e *ErrorList) Join(sep string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	msgs := make([]string, len(e.errs))
	for i, err := range e.errs {
		msgs[i] = err.Error()
	}
	return strings.Join(msgs, sep)
}

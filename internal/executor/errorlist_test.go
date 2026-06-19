/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package executor

import (
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestErrorList_AddNilIgnored(t *testing.T) {
	var list ErrorList
	list.Add(nil)
	assert.Equal(t, 0, list.Len())
	assert.Equal(t, "", list.Join(", "))
}

func TestErrorList_AddAndJoin(t *testing.T) {
	var list ErrorList
	list.Add(errors.New("first error"))
	list.Add(errors.New("second error"))

	assert.Equal(t, 2, list.Len())
	assert.Equal(t, "first error, second error", list.Join(", "))
}

func TestErrorList_Addf(t *testing.T) {
	var list ErrorList
	list.Addf("resource %s: %w", "db-1", errors.New("connection refused"))

	assert.Equal(t, 1, list.Len())
	assert.Contains(t, list.Join(", "), "resource db-1: connection refused")
}

func TestErrorList_ConcurrentAdds(t *testing.T) {
	var list ErrorList
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			list.Addf("error %d", n)
		}(i)
	}

	wg.Wait()
	assert.Equal(t, 100, list.Len())
}

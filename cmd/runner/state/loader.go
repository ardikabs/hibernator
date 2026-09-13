/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package state

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/go-logr/logr"

	"github.com/ardikabs/hibernator/internal/executor"
	"github.com/ardikabs/hibernator/internal/restore"
)

// LoadRestoreData retrieves restore data from ConfigMap.
// Returns (data, found, error) where found indicates whether restore data exists.
// Keys that have a StaleCount > 0 are excluded: if a resource was not reported
// during the most recent shutdown, its state may be inconsistent and should not
// be used for restoration.
// Keys marked Excluded by the operator (e.g. CLI prune for partial runs) stay
// in Data but are reported via Skipped: the runner removes them before the
// executor runs and reports the count.
func LoadRestoreData(ctx context.Context, restoreMgr *restore.Manager, log logr.Logger, namespace, plan, target string) (*executor.RestoreData, bool, error) {
	log = log.WithValues("plan", fmt.Sprintf("%s/%s", namespace, plan), "target", target)

	data, err := restoreMgr.Load(ctx, namespace, plan, target)
	if err != nil {
		return nil, false, fmt.Errorf("load from ConfigMap: %w", err)
	}

	if data == nil {
		return nil, false, nil
	}

	// Convert state map to unified map[string]json.RawMessage format,
	// excluding any keys that are currently stale (StaleCount > 0).
	// Operator-excluded keys are reported via Skipped but left in Data:
	// the runner enforces exclusion before executors run.
	transformedData := make(map[string]json.RawMessage)
	var skipped map[string]string
	staleSkipped := 0
	for key, value := range data.State {
		status, hasStatus := data.Status[key]

		// A key both stale and excluded resolves as stale: an inconsistent
		// snapshot must never be restored even if deliberately withheld.
		// (Order matters: stale is checked first.)
		if hasStatus && status.StaleCount > 0 {
			staleSkipped++
			log.Info("excluding stale key from restore data", "key", key, "staleCount", status.StaleCount)
			continue
		}
		if hasStatus && status.Excluded {
			if skipped == nil {
				skipped = make(map[string]string)
			}
			skipped[key] = executor.SkipReasonExcluded
			log.Info("marking operator-excluded key for runner enforcement", "key", key)
		}

		valueBytes, err := json.Marshal(value)
		if err != nil {
			return nil, false, fmt.Errorf("marshal state value for key %s: %w", key, err)
		}
		transformedData[key] = valueBytes
	}

	if staleSkipped > 0 {
		log.Info("stale keys excluded from restore data", "staleSkipped", staleSkipped, "eligibleKeys", len(transformedData))
	}
	if len(skipped) > 0 {
		log.Info("operator-excluded keys reported for runner enforcement", "excluded", len(skipped), "eligibleKeys", len(transformedData))
	}

	if len(transformedData) == 0 {
		log.Info("restore point exists but contains no eligible state; wakeup will proceed with empty restore data")
	}

	return &executor.RestoreData{
		Type:    data.Executor,
		Data:    transformedData,
		Skipped: skipped,
		IsLive:  data.IsLive,
	}, true, nil
}

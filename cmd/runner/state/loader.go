package state

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ardikabs/hibernator/internal/executor"
	"github.com/ardikabs/hibernator/internal/restore"
)

// LoadRestoreData retrieves restore data from ConfigMap.
// Keys that have a StaleCount > 0 are excluded: if a resource was not reported
// during the most recent shutdown, its state may be inconsistent and should not
// be used for restoration.
func LoadRestoreData(ctx context.Context, k8sClient client.Client, log logr.Logger, namespace, plan, target string) (*executor.RestoreData, error) {
	log = log.WithValues("plan", fmt.Sprintf("%s/%s", namespace, plan), "target", target)

	restoreMgr := restore.NewManager(k8sClient)

	data, err := restoreMgr.Load(ctx, namespace, plan, target)
	if err != nil {
		return nil, fmt.Errorf("load from ConfigMap: %w", err)
	}

	if data == nil {
		return nil, fmt.Errorf("no restore data found for plan=%s target=%s", plan, target)
	}

	// Convert state map to unified map[string]json.RawMessage format,
	// excluding any keys that are currently stale (StaleCount > 0).
	transformedData := make(map[string]json.RawMessage)
	staleSkipped := 0
	for key, value := range data.State {
		if count, isStale := data.StaleCounts[key]; isStale && count > 0 {
			staleSkipped++
			log.Info("excluding stale key from restore data", "key", key, "staleCount", count)
			continue
		}

		valueBytes, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("marshal state value for key %s: %w", key, err)
		}
		transformedData[key] = valueBytes
	}

	if staleSkipped > 0 {
		log.Info("stale keys excluded from restore data", "staleSkipped", staleSkipped, "eligibleKeys", len(transformedData))
	}

	if len(transformedData) == 0 {
		log.Info("restore point exists but contains no eligible state; wakeup will proceed with empty restore data")
	}

	return &executor.RestoreData{
		Type:   data.Executor,
		Data:   transformedData,
		IsLive: data.IsLive,
	}, nil
}

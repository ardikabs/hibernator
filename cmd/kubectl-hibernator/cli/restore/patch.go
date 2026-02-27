/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package restore

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/samber/lo"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/common"
	"github.com/ardikabs/hibernator/internal/restore"
)

type patchOptions struct {
	root       *common.RootOptions
	target     string
	resourceID string
	sets       []string
	removes    []string
	patchJSON  string
	patchFile  string
	dryRun     bool
}

// newPatchCommand creates the "restore patch" command
func newPatchCommand(opts *common.RootOptions) *cobra.Command {
	patchOpts := &patchOptions{root: opts}

	cmd := &cobra.Command{
		Use:   "patch <plan-name>",
		Short: "Update resource state in the restore point",
		Long: `Modify specific fields or apply structured patches to a resource's restore state.
Supports three mutation modes:
1. Field-level updates: --set key.path=value --remove key.path
2. JSON merge patch: --patch '{"key":"value"}'
3. File-based patch: --patch-file=patch.json

Field paths support dot notation for nested access: config.scaling.min

Modes are mutually exclusive:
- Use --set/--remove for granular field updates
- Use --patch or --patch-file for structured changes
- Cannot mix granular and patch modes in one command

Examples:
# Field updates (dot notation)
kubectl hibernator restore patch my-plan -t eks -r node-123 \
--set desiredCapacity=10 \
--set config.tags.environment=prod

# Remove fields
kubectl hibernator restore patch my-plan -t eks -r node-123 \
--remove config.deprecated \
--remove tempData

# Inline JSON patch (RFC 7386 merge patch)
kubectl hibernator restore patch my-plan -t eks -r node-123 \
--patch '{"desiredCapacity":10,"isLive":false}'

# File-based patch
kubectl hibernator restore patch my-plan -t eks -r node-123 \
--patch-file=update.json

# Preview changes without applying
kubectl hibernator restore patch my-plan -t eks -r node-123 \
--set desiredCapacity=10 \
--dry-run`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPatch(cmd.Context(), patchOpts, args[0])
		},
	}

	cmd.Flags().StringVarP(&patchOpts.target, "target", "t", "", "Target name (required)")
	cmd.Flags().StringVarP(&patchOpts.resourceID, "resource-id", "r", "", "Resource ID (required)")
	cmd.Flags().StringArrayVar(&patchOpts.sets, "set", nil, "Set field value (dot notation, repeatable). Example: --set config.min=5")
	cmd.Flags().StringArrayVar(&patchOpts.removes, "remove", nil, "Remove field (dot notation, repeatable). Example: --remove config.deprecated")
	cmd.Flags().StringVar(&patchOpts.patchJSON, "patch", "", "JSON merge patch (RFC 7386) inline")
	cmd.Flags().StringVar(&patchOpts.patchFile, "patch-file", "", "Path to JSON merge patch file")
	cmd.Flags().BoolVar(&patchOpts.dryRun, "dry-run", false, "Preview changes without applying")

	lo.Must0(cmd.MarkFlagRequired("target"))
	lo.Must0(cmd.MarkFlagRequired("resource-id"))

	return cmd
}

func runPatch(ctx context.Context, opts *patchOptions, planName string) error {
	// Validate mutual exclusivity
	hasFieldOps := len(opts.sets) > 0 || len(opts.removes) > 0
	hasPatchOps := opts.patchJSON != "" || opts.patchFile != ""

	if hasFieldOps && hasPatchOps {
		return fmt.Errorf("cannot mix field operations (--set/--remove) with patch operations (--patch/--patch-file)")
	}

	if !hasFieldOps && !hasPatchOps {
		return fmt.Errorf("must specify either field operations (--set/--remove) or a patch (--patch/--patch-file)")
	}

	// Load client and fetch restore data
	c, err := common.NewK8sClient(opts.root)
	if err != nil {
		return err
	}

	ns := common.ResolveNamespace(opts.root)

	cmName := restore.GetRestoreConfigMap(planName)
	var cm corev1.ConfigMap
	if err := c.Get(ctx, types.NamespacedName{Name: cmName, Namespace: ns}, &cm); err != nil {
		return fmt.Errorf("no restore point found for plan %q: %w", planName, err)
	}

	// Find the target's restore data
	var targetData *restore.Data
	var configKey string

	for key, val := range cm.Data {
		var data restore.Data
		if err := json.Unmarshal([]byte(val), &data); err != nil {
			continue
		}

		if data.Target == opts.target {
			targetData = &data
			configKey = key
			break
		}
	}

	if targetData == nil {
		return fmt.Errorf("target %q not found in restore point", opts.target)
	}

	// Check if resource exists
	resourceExists := false
	if _, ok := targetData.State[opts.resourceID]; ok {
		resourceExists = true
	}

	if !resourceExists {
		// Warn user that resource doesn't exist and ask for confirmation
		fmt.Printf("\n⚠️  WARNING: Resource %q not found in target %q\n\n", opts.resourceID, opts.target)
		fmt.Println("This command will CREATE a new resource entry in the restore point.")
		fmt.Println("This is useful for adding resources that were missed, but incorrect")
		fmt.Println("resource IDs can cause executor failures during restoration.")
		fmt.Println()
		fmt.Println("Affected:")
		fmt.Printf("  Plan:      %s\n", planName)
		fmt.Printf("  Target:    %s\n", opts.target)
		fmt.Printf("  Executor:  %s\n", targetData.Executor)
		fmt.Printf("  Resource:  %s (NEW)\n\n", opts.resourceID)

		if !opts.root.JsonOutput {
			fmt.Print("Proceed with creating this resource? (y/N): ")
			var response string
			lo.Must1(fmt.Scanln(&response))
			if strings.ToLower(response) != "y" {
				fmt.Println("Cancelled - no changes made")
				return nil
			}
		} else {
			return fmt.Errorf("resource %q not found in target %q (use interactive mode to create new resources)", opts.resourceID, opts.target)
		}

		if targetData.State == nil {
			targetData.State = make(map[string]any)
		}

		// Initialize new resource state as empty object
		targetData.State[opts.resourceID] = make(map[string]any)
	}

	// Get current resource state
	currentStateData := targetData.State[opts.resourceID]
	currentState, ok := currentStateData.(map[string]any)
	if !ok {
		return fmt.Errorf("resource state is not a valid JSON object")
	}

	// Make a copy for modification
	modifiedState := deepCopyMap(currentState)

	// Apply patches
	if hasFieldOps {
		// Apply --set operations
		for _, setOp := range opts.sets {
			parts := strings.SplitN(setOp, "=", 2)
			if len(parts) != 2 {
				return fmt.Errorf("invalid set operation %q, expected format key=value", setOp)
			}
			if err := setPathValue(modifiedState, parts[0], parts[1]); err != nil {
				return fmt.Errorf("failed to set %q: %w", parts[0], err)
			}
		}

		// Apply --remove operations
		for _, removePath := range opts.removes {
			if err := removePathValue(modifiedState, removePath); err != nil {
				return fmt.Errorf("failed to remove %q: %w", removePath, err)
			}
		}
	} else {
		// Apply --patch or --patch-file
		var patchData map[string]any
		var patchStr string

		if opts.patchFile != "" {
			fileBytes, err := os.ReadFile(opts.patchFile)
			if err != nil {
				return fmt.Errorf("failed to read patch file: %w", err)
			}
			patchStr = string(fileBytes)
		} else {
			patchStr = opts.patchJSON
		}

		if err := json.Unmarshal([]byte(patchStr), &patchData); err != nil {
			return fmt.Errorf("invalid JSON patch: %w", err)
		}

		// Apply RFC 7386 merge patch
		modifiedState = mergePatch(modifiedState, patchData)
	}

	// Show diff if dry-run or always show summary
	if err := showPatchDiff(currentState, modifiedState); err != nil {
		return err
	}

	if opts.dryRun {
		fmt.Println("\n[DRY RUN] No changes applied")
		return nil
	}

	// Confirm before applying (unless forced)
	if !opts.root.JsonOutput {
		fmt.Print("\nApply changes? (y/N): ")
		var response string
		lo.Must1(fmt.Scanln(&response))
		if strings.ToLower(response) != "y" {
			fmt.Println("Cancelled")
			return nil
		}
	}

	// Update the state
	targetData.State[opts.resourceID] = modifiedState

	// Marshal and update ConfigMap
	dataBytes, err := json.Marshal(targetData)
	if err != nil {
		return fmt.Errorf("failed to marshal restore data: %w", err)
	}
	cm.Data[configKey] = string(dataBytes)

	if err := c.Update(ctx, &cm); err != nil {
		return fmt.Errorf("failed to update restore point: %w", err)
	}

	fmt.Printf("✓ Successfully patched resource %q in target %q\n", opts.resourceID, opts.target)
	return nil
}

// setPathValue sets a value at a dotted path in a map
// Path format: "key.nested.path" or "key"
// Value is parsed as JSON if it looks like JSON, otherwise treated as string
func setPathValue(obj map[string]any, path, valueStr string) error {
	parts := strings.Split(path, ".")
	if len(parts) == 0 {
		return fmt.Errorf("empty path")
	}

	// Parse the value: try JSON first, fall back to string
	var value any
	valueStr = strings.TrimSpace(valueStr)

	// Try to parse as JSON
	if err := json.Unmarshal([]byte(valueStr), &value); err != nil {
		// If it fails, treat as string
		value = valueStr
	}

	// Navigate to parent and set value
	current := obj
	for i := 0; i < len(parts)-1; i++ {
		key := parts[i]
		if _, ok := current[key]; !ok {
			// Create intermediate objects if they don't exist
			current[key] = make(map[string]any)
		}

		next, ok := current[key].(map[string]any)
		if !ok {
			return fmt.Errorf("cannot traverse through non-object at %q", strings.Join(parts[:i+1], "."))
		}
		current = next
	}

	// Set the final value
	current[parts[len(parts)-1]] = value
	return nil
}

// removePathValue removes a value at a dotted path in a map
func removePathValue(obj map[string]any, path string) error {
	parts := strings.Split(path, ".")
	if len(parts) == 0 {
		return fmt.Errorf("empty path")
	}

	// Navigate to parent
	current := obj
	for i := 0; i < len(parts)-1; i++ {
		key := parts[i]
		next, ok := current[key].(map[string]any)
		if !ok {
			// Path doesn't exist, nothing to remove
			return nil
		}
		current = next
	}

	// Remove the final key
	delete(current, parts[len(parts)-1])
	return nil
}

// mergePatch implements RFC 7386 merge patch semantics
// Simple version: recursively merge patch into target, with patch values overwriting target
func mergePatch(target, patch map[string]any) map[string]any {
	result := deepCopyMap(target)

	for key, patchVal := range patch {
		if patchVal == nil {
			// null in patch means delete
			delete(result, key)
		} else if patchMap, ok := patchVal.(map[string]any); ok {
			// Recursively merge objects
			if targetMap, ok := result[key].(map[string]any); ok {
				result[key] = mergePatch(targetMap, patchMap)
			} else {
				// Target is not an object, replace it
				result[key] = deepCopyMap(patchMap)
			}
		} else {
			// Scalar value, replace directly
			result[key] = patchVal
		}
	}

	return result
}

// deepCopyMap creates a deep copy of a map
func deepCopyMap(m map[string]any) map[string]any {
	result := make(map[string]any)
	for k, v := range m {
		result[k] = deepCopyValue(v)
	}
	return result
}

// deepCopyValue creates a deep copy of any value
func deepCopyValue(v any) any {
	switch val := v.(type) {
	case map[string]any:
		return deepCopyMap(val)
	case []any:
		result := make([]any, len(val))
		for i, item := range val {
			result[i] = deepCopyValue(item)
		}
		return result
	default:
		return v
	}
}

// showPatchDiff displays a diff between current and modified state
func showPatchDiff(current, modified map[string]any) error {
	// Convert to JSON for diff display
	currentJSON, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return err
	}

	modifiedJSON, err := json.MarshalIndent(modified, "", "  ")
	if err != nil {
		return err
	}

	if string(currentJSON) == string(modifiedJSON) {
		fmt.Println("No changes detected")
		return nil
	}

	fmt.Println("Changes to be applied:")
	fmt.Println()
	fmt.Println("Before:")
	fmt.Println(string(currentJSON))
	fmt.Println()
	fmt.Println("After:")
	fmt.Println(string(modifiedJSON))

	return nil
}

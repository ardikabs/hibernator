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
	"sort"

	"github.com/samber/lo"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/common"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/output"
	"github.com/ardikabs/hibernator/internal/restore"
)

type importOptions struct {
	root       *common.RootOptions
	target     string
	resourceID string
	file       string
	dryRun     bool
	yes        bool
}

// newImportCommand restores previously exported data back into the restore point.
func newImportCommand(opts *common.RootOptions) *cobra.Command {
	importOpts := &importOptions{root: opts}

	cmd := &cobra.Command{
		Use:   "import <plan-name>",
		Short: "Import previously exported data back into the restore point",
		Long: `Write a file produced by 'restore export' back into the restore point,
replacing the target entry (or a single resource state) wholesale.

This is the restore half of the weekend partial-run workflow: after running
the weekend on a pruned subset, import the full backup taken beforehand.

Examples:
  # Restore a whole target entry
  kubectl hibernator restore import my-plan --target eks-cluster --file eks-full.json

  # Restore a single resource state
  kubectl hibernator restore import my-plan --target eks-cluster --resource-id ng-main --file ng-main.json

  # Preview without writing
  kubectl hibernator restore import my-plan --target eks-cluster --file eks-full.json --dry-run`,
		Args: cobra.ExactArgs(1),
		RunE: output.WrapRunE(func(ctx context.Context, args []string) error {
			return runImport(ctx, importOpts, args[0])
		}),
	}

	cmd.Flags().StringVarP(&importOpts.target, "target", "t", "", "Target name (required)")
	cmd.Flags().StringVarP(&importOpts.resourceID, "resource-id", "r", "", "Single resource ID to import (defaults to the whole target entry)")
	cmd.Flags().StringVar(&importOpts.file, "file", "", "Source file path, as written by restore export (required)")
	cmd.Flags().BoolVar(&importOpts.dryRun, "dry-run", false, "Preview what would happen without making changes")
	cmd.Flags().BoolVar(&importOpts.yes, "yes", false, "Skip confirmation prompts (non-interactive use)")

	lo.Must0(cmd.MarkFlagRequired("target"))
	lo.Must0(cmd.MarkFlagRequired("file"))

	return cmd
}

func runImport(ctx context.Context, opts *importOptions, planName string) error {
	out := output.FromContext(ctx)

	raw, err := os.ReadFile(opts.file)
	if err != nil {
		return fmt.Errorf("failed to read import file %q: %w", opts.file, err)
	}

	c, err := common.ClientFactory(opts.root)
	if err != nil {
		return err
	}

	ns := common.ResolveNamespace(opts.root)

	cmName := restore.GetRestoreConfigMap(planName)
	var cm corev1.ConfigMap
	if err := c.Get(ctx, types.NamespacedName{Name: cmName, Namespace: ns}, &cm); err != nil {
		return fmt.Errorf("no restore point found for plan %q: %w", planName, err)
	}

	key, _, current, err := loadTargetEntry(&cm, opts.target)
	if err != nil {
		return err
	}

	if opts.resourceID == "" {
		return importTarget(ctx, c, out, opts, planName, &cm, key, current, raw)
	}
	return importResource(ctx, c, out, opts, planName, &cm, key, current, raw)
}

// importTarget replaces the whole target entry. The backup must belong to
// the same target: importing another target's data is never what you want.
func importTarget(ctx context.Context, c client.Client, out output.Formatter, opts *importOptions, planName string, cm *corev1.ConfigMap, key string, current restore.Data, raw []byte) error {
	var incoming restore.Data
	if err := json.Unmarshal(raw, &incoming); err != nil {
		return fmt.Errorf("invalid target backup in %q: %w", opts.file, err)
	}
	if incoming.Target != opts.target {
		return fmt.Errorf("backup targets %q, not %q: refusing cross-target import", incoming.Target, opts.target)
	}

	if opts.dryRun {
		added, removed, changed := diffResourceIDs(current, incoming)
		out.Info("[DRY-RUN] Would replace target %q (%d resources: +%d ~%d -%d)",
			opts.target, len(current.State), len(added), len(changed), len(removed))
		return nil
	}

	if !opts.yes {
		out.Info("Replace target %q (%d resources) with backup from %q? (y/N): ", opts.target, len(current.State), opts.file)
		if !confirmPrompt(os.Stdin, "y") {
			out.Info("Cancelled - no changes made")
			return nil
		}
	}

	dataBytes, err := json.Marshal(&incoming)
	if err != nil {
		return fmt.Errorf("marshal restore data: %w", err)
	}
	cm.Data[key] = string(dataBytes)
	if err := c.Update(ctx, cm); err != nil {
		return fmt.Errorf("failed to update restore point: %w", err)
	}

	out.Success("Imported target %q (%d resources) from %q", opts.target, len(incoming.State), opts.file)
	return nil
}

// importResource replaces a single resource state inside the target entry.
// Any JSON value is accepted and stored as-is.
func importResource(ctx context.Context, c client.Client, out output.Formatter, opts *importOptions, planName string, cm *corev1.ConfigMap, key string, current restore.Data, raw []byte) error {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("invalid resource backup in %q: %w", opts.file, err)
	}

	old, existed := current.State[opts.resourceID]
	if opts.dryRun {
		if !existed {
			out.Info("[DRY-RUN] Would create resource %q in target %q", opts.resourceID, opts.target)
		} else if oldObj, ok := old.(map[string]any); ok {
			if newObj, ok := value.(map[string]any); ok {
				return showPatchDiff(ctx, oldObj, newObj)
			}
			out.Info("[DRY-RUN] Would replace resource %q in target %q (non-object state)", opts.resourceID, opts.target)
		} else {
			out.Info("[DRY-RUN] Would replace resource %q in target %q", opts.resourceID, opts.target)
		}
		return nil
	}

	if !opts.yes {
		action := "Replace"
		if !existed {
			action = "Create"
		}
		out.Info("%s resource %q in target %q from %q? (y/N): ", action, opts.resourceID, opts.target, opts.file)
		if !confirmPrompt(os.Stdin, "y") {
			out.Info("Cancelled - no changes made")
			return nil
		}
	}

	if current.State == nil {
		current.State = make(map[string]any)
	}
	current.State[opts.resourceID] = value
	dataBytes, err := json.Marshal(&current)
	if err != nil {
		return fmt.Errorf("marshal restore data: %w", err)
	}
	cm.Data[key] = string(dataBytes)
	if err := c.Update(ctx, cm); err != nil {
		return fmt.Errorf("failed to update restore point: %w", err)
	}

	out.Success("Imported resource %q in target %q from %q", opts.resourceID, opts.target, opts.file)
	return nil
}

// diffResourceIDs partitions the incoming resource set against the current
// one into added/removed/changed ID lists (sorted for stable output).
// Maps marshal with sorted keys, so byte comparison is deterministic.
func diffResourceIDs(current, incoming restore.Data) (added, removed, changed []string) {
	oldBytes := map[string][]byte{}
	for id, val := range current.State {
		raw, err := json.Marshal(val)
		if err != nil {
			continue
		}
		oldBytes[id] = raw
	}
	for id, val := range incoming.State {
		raw, err := json.Marshal(val)
		if err != nil {
			added = append(added, id)
			continue
		}
		old, ok := oldBytes[id]
		if !ok {
			added = append(added, id)
		} else if string(old) != string(raw) {
			changed = append(changed, id)
		}
	}
	for id := range current.State {
		if _, ok := incoming.State[id]; !ok {
			removed = append(removed, id)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	sort.Strings(changed)
	return added, removed, changed
}

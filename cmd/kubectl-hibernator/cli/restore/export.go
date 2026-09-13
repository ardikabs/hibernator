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
	"path/filepath"

	"github.com/samber/lo"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/common"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/output"
	"github.com/ardikabs/hibernator/internal/restore"
)

type exportOptions struct {
	root       *common.RootOptions
	target     string
	resourceID string
	file       string
}

// newExportCommand dumps restore data to a file for backup or handoff.
func newExportCommand(opts *common.RootOptions) *cobra.Command {
	exportOpts := &exportOptions{root: opts}

	cmd := &cobra.Command{
		Use:   "export <plan-name>",
		Short: "Export restore data to a file",
		Long: `Write a target's restore entry (or a single resource state) to a local file.

The file is the backup half of the weekend partial-run workflow: export the
full entry, prune down to weekend resources, then import the file back after.

Examples:
  # Back up a whole target entry
  kubectl hibernator restore export my-plan --target eks-cluster --file eks-full.json

  # Back up a single resource state
  kubectl hibernator restore export my-plan --target eks-cluster --resource-id ng-main --file ng-main.json`,
		Args: cobra.ExactArgs(1),
		RunE: output.WrapRunE(func(ctx context.Context, args []string) error {
			return runExport(ctx, exportOpts, args[0])
		}),
	}

	cmd.Flags().StringVarP(&exportOpts.target, "target", "t", "", "Target name (required)")
	cmd.Flags().StringVarP(&exportOpts.resourceID, "resource-id", "r", "", "Single resource ID to export (defaults to the whole target entry)")
	cmd.Flags().StringVar(&exportOpts.file, "file", "", "Destination file path (required)")

	lo.Must0(cmd.MarkFlagRequired("target"))
	lo.Must0(cmd.MarkFlagRequired("file"))

	return cmd
}

func runExport(ctx context.Context, opts *exportOptions, planName string) error {
	out := output.FromContext(ctx)

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

	_, raw, data, err := loadTargetEntry(&cm, opts.target)
	if err != nil {
		return err
	}

	var payload []byte
	if opts.resourceID != "" {
		state, ok := data.State[opts.resourceID]
		if !ok {
			return fmt.Errorf("resource %q not found in target %q", opts.resourceID, opts.target)
		}
		payload, err = json.MarshalIndent(state, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal resource state: %w", err)
		}
	} else {
		// Raw entry bytes round-trip verbatim on import.
		payload = []byte(raw)
	}

	if err := os.MkdirAll(filepath.Dir(opts.file), 0o755); err != nil {
		return fmt.Errorf("create destination directory: %w", err)
	}
	if err := os.WriteFile(opts.file, append(payload, '\n'), 0o600); err != nil {
		return fmt.Errorf("write export file: %w", err)
	}

	if opts.resourceID != "" {
		out.Success("Exported resource %q of target %q to %q", opts.resourceID, opts.target, opts.file)
	} else {
		out.Success("Exported target %q (%d resources) to %q", opts.target, len(data.State), opts.file)
	}
	return nil
}

// loadTargetEntry finds the ConfigMap data entry for target, skipping values
// that do not decode as restore data.
func loadTargetEntry(cm *corev1.ConfigMap, target string) (key, raw string, data restore.Data, err error) {
	for k, val := range cm.Data {
		var d restore.Data
		if uerr := json.Unmarshal([]byte(val), &d); uerr != nil {
			continue
		}
		if d.Target != target {
			continue
		}
		return k, val, d, nil
	}
	return "", "", restore.Data{}, fmt.Errorf("target %q not found in restore point", target)
}

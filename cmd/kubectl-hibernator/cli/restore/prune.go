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
	"strings"

	"github.com/samber/lo"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/common"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/output"
	"github.com/ardikabs/hibernator/internal/restore"
)

type pruneOptions struct {
	root   *common.RootOptions
	target string
	keep   []string
	dryRun bool
	yes    bool
	delete bool
}

// newPruneCommand trims a target's restore entry down to a keep-list.
func newPruneCommand(opts *common.RootOptions) *cobra.Command {
	pruneOpts := &pruneOptions{root: opts}

	cmd := &cobra.Command{
		Use:   "prune <plan-name>",
		Short: "Exclude all but listed resources in a target's restore entry",
		Long: `Mark every resource state in a target's restore entry excluded
except the --keep list, so wakeup skips them. This is the trim half of the
weekend partial-run workflow: export the full entry first, prune down to
weekend resources, run the weekend, then import the backup back.

Marking is reversible (import restores the backup, and the next full
hibernation clears markers automatically). Use --delete to permanently
remove the non-kept resources instead.

Examples:
  # Exclude everything except weekend runners (repeat --keep per resource)
  kubectl hibernator restore prune my-plan --target eks-cluster --keep ng-a --keep ng-b

  # Preview what would be excluded
  kubectl hibernator restore prune my-plan --target eks-cluster --keep ng-a --dry-run

  # Permanently delete instead of marking
  kubectl hibernator restore prune my-plan --target eks-cluster --keep ng-a --delete --yes`,
		Args: cobra.ExactArgs(1),
		RunE: output.WrapRunE(func(ctx context.Context, args []string) error {
			return runPrune(ctx, pruneOpts, args[0])
		}),
	}

	cmd.Flags().StringVarP(&pruneOpts.target, "target", "t", "", "Target name (required)")
	cmd.Flags().StringArrayVar(&pruneOpts.keep, "keep", nil, "Resource ID to keep (repeatable, at least one required)")
	cmd.Flags().BoolVar(&pruneOpts.dryRun, "dry-run", false, "Preview what would happen without making changes")
	cmd.Flags().BoolVar(&pruneOpts.yes, "yes", false, "Skip confirmation prompts (non-interactive use)")
	cmd.Flags().BoolVar(&pruneOpts.delete, "delete", false, "Permanently delete non-kept resources instead of marking them excluded")

	lo.Must0(cmd.MarkFlagRequired("target"))
	lo.Must0(cmd.MarkFlagRequired("keep"))

	return cmd
}

func runPrune(ctx context.Context, opts *pruneOptions, planName string) error {
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

	key, _, data, err := loadTargetEntry(&cm, opts.target)
	if err != nil {
		return err
	}

	keep := make(map[string]bool, len(opts.keep))
	for _, id := range opts.keep {
		keep[id] = true
	}
	var trimmed []string
	for id := range data.State {
		if !keep[id] {
			trimmed = append(trimmed, id)
		}
	}
	sort.Strings(trimmed)

	if len(trimmed) == 0 {
		out.Info("Nothing to prune: target %q already holds only the kept resources", opts.target)
		return nil
	}

	if opts.dryRun {
		if opts.delete {
			out.Info("[DRY-RUN] Would permanently delete %d resource(s) from target %q: %s",
				len(trimmed), opts.target, strings.Join(trimmed, ", "))
		} else {
			out.Info("[DRY-RUN] Would mark %d resource(s) excluded in target %q: %s",
				len(trimmed), opts.target, strings.Join(trimmed, ", "))
		}
		return nil
	}

	if !opts.yes {
		prompt := fmt.Sprintf("Mark %d resource(s) excluded in target %q (%s)? (y/N): ",
			len(trimmed), opts.target, strings.Join(trimmed, ", "))
		if opts.delete {
			prompt = fmt.Sprintf("Permanently delete %d resource(s) from target %q (%s)? (y/N): ",
				len(trimmed), opts.target, strings.Join(trimmed, ", "))
		}
		out.Info("%s", prompt)
		if !confirmPrompt(os.Stdin, "y") {
			out.Info("Cancelled - no changes made")
			return nil
		}
	}

	if opts.delete {
		for _, id := range trimmed {
			delete(data.State, id)
			delete(data.Status, id)
		}
	} else {
		if data.Status == nil {
			data.Status = make(map[string]restore.ResourceStatus)
		}
		for _, id := range trimmed {
			st := data.Status[id]
			st.Excluded = true
			data.Status[id] = st
		}
	}
	dataBytes, err := json.Marshal(&data)
	if err != nil {
		return fmt.Errorf("marshal restore data: %w", err)
	}
	cm.Data[key] = string(dataBytes)
	if err := c.Update(ctx, &cm); err != nil {
		return fmt.Errorf("failed to update restore point: %w", err)
	}

	if opts.delete {
		out.Success("Pruned target %q: permanently deleted %d resource(s), kept %d", opts.target, len(trimmed), len(data.State))
		return nil
	}
	out.Success("Marked %d resource(s) excluded in target %q (kept %d)", len(trimmed), opts.target, len(data.State)-len(trimmed))
	return nil
}

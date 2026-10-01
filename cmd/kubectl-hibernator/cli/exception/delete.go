/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package exception

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/common"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/output"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/printers"
)

type deleteOptions struct {
	root     *common.RootOptions
	selector string
	plan     string
	yes      bool
	dryRun   bool
	in       io.Reader
}

func newDeleteCommand(root *common.RootOptions) *cobra.Command {
	opts := &deleteOptions{root: root, in: os.Stdin}

	cmd := &cobra.Command{
		Use:   "delete [name...]",
		Short: "Delete ScheduleExceptions",
		Args:  cobra.ArbitraryArgs,
		RunE: output.WrapRunE(func(ctx context.Context, args []string) error {
			return runDelete(ctx, opts, args)
		}),
	}

	cmd.Flags().StringVarP(&opts.selector, "selector", "l", "", "Delete exceptions matching this Kubernetes label selector")
	cmd.Flags().StringVar(&opts.plan, "plan", "", "Delete exceptions referencing this HibernatePlan")
	cmd.Flags().BoolVar(&opts.yes, "yes", false, "Skip confirmation prompts (non-interactive use)")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "Preview the ScheduleExceptions that would be deleted without making changes")

	return cmd
}

func runDelete(ctx context.Context, opts *deleteOptions, args []string) error {
	if len(args) > 0 && (opts.selector != "" || opts.plan != "") {
		return fmt.Errorf("cannot mix exact exception names with --selector or --plan filters")
	}
	if len(args) == 0 && opts.selector == "" && opts.plan == "" {
		return fmt.Errorf("at least one of an exception name, --selector, or --plan is required")
	}
	if opts.selector != "" {
		if _, err := labels.Parse(opts.selector); err != nil {
			return fmt.Errorf("parse exception selector %q: %w", opts.selector, err)
		}
	}

	c, err := common.ClientFactory(opts.root)
	if err != nil {
		return err
	}

	ns := common.ResolveNamespace(opts.root)

	var targets []hibernatorv1alpha1.ScheduleException
	if len(args) > 0 {
		seen := map[string]struct{}{}
		for _, name := range args {
			if _, dup := seen[name]; dup {
				continue
			}
			seen[name] = struct{}{}
			var exception hibernatorv1alpha1.ScheduleException
			if err := c.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, &exception); err != nil {
				return fmt.Errorf("get ScheduleException %s/%s: %w", ns, name, err)
			}
			targets = append(targets, exception)
		}
	} else {
		selected, err := selectExceptions(ctx, c, ns, opts.selector, opts.plan)
		if err != nil {
			return err
		}
		if len(selected) == 0 {
			return fmt.Errorf("no ScheduleExceptions match the given filters")
		}
		targets = selected
	}

	out := output.FromContext(ctx)
	if opts.dryRun {
		if !opts.root.JsonOutput {
			out.Info("[DRY-RUN] Would delete %d ScheduleExceptions", len(targets))
		}
		dryDispatcher := &printers.Dispatcher{JSON: opts.root.JsonOutput}
		return dryDispatcher.PrintObj(&printers.ExceptionListOutput{Items: targets}, output.WriterFromContext(ctx))
	}
	if opts.root.JsonOutput {
		// Preview lines and the confirmation prompt write to stdout, which
		// would corrupt the JSON document. Scripted JSON use must be explicit.
		if !opts.yes {
			return fmt.Errorf("refusing to prompt for confirmation in --json mode; re-run with --yes")
		}
	} else {
		for _, exception := range targets {
			out.Info("- %s/%s (plan: %s)", exception.Namespace, exception.Name, exception.Spec.PlanRef.Name)
		}

		if !opts.yes {
			out.Info("Delete %d ScheduleExceptions? (y/N): ", len(targets))
			if !confirmDelete(opts.in) {
				out.Info("Cancelled - no changes made")
				return nil
			}
		}
	}

	results := make([]printers.ExceptionOperationResult, 0, len(targets))
	failed := []string{}
	for i := range targets {
		exception := targets[i]
		result := printers.ExceptionOperationResult{Plan: exception.Spec.PlanRef.Name, Name: exception.Name}
		if err := c.Delete(ctx, &exception); err != nil {
			result.Result = "FAILED"
			result.Message = err.Error()
			results = append(results, result)
			failed = append(failed, exception.Name)
			continue
		}
		result.Result = "SUCCESS"
		result.Message = "deleted"
		results = append(results, result)
	}

	dispatcher := &printers.Dispatcher{JSON: opts.root.JsonOutput}
	if err := dispatcher.PrintObj(&printers.ExceptionOperationOutput{
		Action: "delete",
		Items:  results,
	}, output.WriterFromContext(ctx)); err != nil {
		return err
	}
	if len(failed) > 0 {
		return fmt.Errorf("failed to delete ScheduleExceptions: %s", strings.Join(failed, ", "))
	}
	return nil
}

func confirmDelete(in io.Reader) bool {
	if in == nil {
		return false
	}
	scanner := bufio.NewScanner(in)
	if !scanner.Scan() {
		return false
	}
	response := strings.TrimSpace(scanner.Text())
	return strings.EqualFold(response, "y") || strings.EqualFold(response, "yes")
}

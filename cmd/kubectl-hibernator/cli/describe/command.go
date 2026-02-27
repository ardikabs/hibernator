/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package describe

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/types"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/common"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/printers"
)

type describeOptions struct {
	root *common.RootOptions
}

// NewCommand creates the "describe" command.
func NewCommand(opts *common.RootOptions) *cobra.Command {
	describeOpts := &describeOptions{root: opts}

	cmd := &cobra.Command{
		Use:   "describe <plan-name>",
		Short: "Display detailed information about a HibernatePlan",
		Long: `Show comprehensive details about a HibernatePlan including:
- Schedule configuration (timezone, off-hour windows)
- Execution strategy and behavior mode
- List of targets with executor-specific parameters
- Current status and execution history
- Active exceptions and suspend state

Examples:
  kubectl hibernator describe my-plan
  kubectl hibernator describe my-plan -n production
  kubectl hibernator describe my-plan --json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDescribe(cmd.Context(), describeOpts, args[0])
		},
	}

	return cmd
}

func runDescribe(ctx context.Context, opts *describeOptions, planName string) error {
	c, err := common.NewK8sClient(opts.root)
	if err != nil {
		return err
	}

	ns := common.ResolveNamespace(opts.root)

	var plan hibernatorv1alpha1.HibernatePlan
	if err := c.Get(ctx, types.NamespacedName{Name: planName, Namespace: ns}, &plan); err != nil {
		return fmt.Errorf("failed to get HibernatePlan %q in namespace %q: %w", planName, ns, err)
	}

	d := &printers.Dispatcher{JSON: opts.root.JsonOutput}
	return d.PrintObj(plan, os.Stdout)
}

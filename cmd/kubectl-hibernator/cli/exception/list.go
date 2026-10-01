/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package exception

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/labels"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/common"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/output"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/printers"
)

type listOptions struct {
	root          *common.RootOptions
	allNamespaces bool
	selector      string
	plan          string
	types         string
}

func newListCommand(root *common.RootOptions) *cobra.Command {
	opts := &listOptions{root: root}

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List ScheduleExceptions",
		Args:    cobra.NoArgs,
		RunE: output.WrapRunE(func(ctx context.Context, args []string) error {
			return runList(ctx, opts)
		}),
	}

	cmd.Flags().BoolVarP(&opts.allNamespaces, "all-namespaces", "A", false, "List exceptions from all namespaces")
	cmd.Flags().StringVarP(&opts.selector, "selector", "l", "", "Filter exceptions by Kubernetes label selector")
	cmd.Flags().StringVar(&opts.plan, "plan", "", "Filter exceptions referencing this HibernatePlan")
	cmd.Flags().StringVar(&opts.types, "type", "", "Filter by exception type (comma-separated: suspend,extend,replace)")

	return cmd
}

func runList(ctx context.Context, opts *listOptions) error {
	if opts.selector != "" {
		if _, err := labels.Parse(opts.selector); err != nil {
			return fmt.Errorf("parse exception selector %q: %w", opts.selector, err)
		}
	}
	var typeFilter map[hibernatorv1alpha1.ExceptionType]struct{}
	if opts.types != "" {
		parsed, err := parseExceptionTypes(opts.types)
		if err != nil {
			return err
		}
		typeFilter = parsed
	}

	c, err := common.ClientFactory(opts.root)
	if err != nil {
		return err
	}

	ns := common.ResolveNamespace(opts.root)
	if opts.allNamespaces {
		ns = ""
	}

	exceptions, err := selectExceptions(ctx, c, ns, opts.selector, opts.plan)
	if err != nil {
		return err
	}
	if typeFilter != nil {
		filtered := exceptions[:0]
		for _, exception := range exceptions {
			if _, ok := typeFilter[exception.Spec.Type]; ok {
				filtered = append(filtered, exception)
			}
		}
		exceptions = filtered
	}

	out := &printers.ExceptionListOutput{Items: exceptions, AllNamespaces: opts.allNamespaces}
	d := &printers.Dispatcher{JSON: opts.root.JsonOutput}
	return d.PrintObj(out, output.WriterFromContext(ctx))
}

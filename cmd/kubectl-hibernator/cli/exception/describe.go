/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package exception

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/common"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/output"
	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/printers"
	"github.com/ardikabs/hibernator/cmd/runner/timeparse"
)

type describeOptions struct {
	root          *common.RootOptions
	allNamespaces bool
	selector      string
	plan          string
	states        string
	now           func() time.Time
}

func newDescribeCommand(root *common.RootOptions) *cobra.Command {
	opts := &describeOptions{root: root, now: time.Now}

	cmd := &cobra.Command{
		Use:   "describe [name...]",
		Short: "Show operational details of ScheduleExceptions",
		Args:  cobra.ArbitraryArgs,
		RunE: output.WrapRunE(func(ctx context.Context, args []string) error {
			return runDescribe(ctx, opts, args)
		}),
	}

	cmd.Flags().BoolVarP(&opts.allNamespaces, "all-namespaces", "A", false, "Show exceptions from all namespaces")
	cmd.Flags().StringVarP(&opts.selector, "selector", "l", "", "Filter exceptions by Kubernetes label selector")
	cmd.Flags().StringVar(&opts.plan, "plan", "", "Filter exceptions referencing this HibernatePlan")
	cmd.Flags().StringVar(&opts.states, "state", "", "Filter by exception state (comma-separated: Active,Pending,Expired,Detached)")

	return cmd
}

func runDescribe(ctx context.Context, opts *describeOptions, args []string) error {
	if len(args) > 0 && (opts.selector != "" || opts.plan != "" || opts.states != "") {
		return fmt.Errorf("cannot mix exact exception names with --selector, --plan, or --state filters")
	}

	if opts.selector != "" {
		if _, err := labels.Parse(opts.selector); err != nil {
			return fmt.Errorf("parse exception selector %q: %w", opts.selector, err)
		}
	}
	var stateFilter map[hibernatorv1alpha1.ExceptionState]struct{}
	if opts.states != "" {
		parsed, err := parseStates(opts.states)
		if err != nil {
			return err
		}
		stateFilter = parsed
	}

	c, err := common.ClientFactory(opts.root)
	if err != nil {
		return err
	}

	ns := common.ResolveNamespace(opts.root)
	if opts.allNamespaces {
		ns = ""
	}

	var exceptions []hibernatorv1alpha1.ScheduleException
	if len(args) > 0 {
		exceptions, err = resolveStatusNames(ctx, c, ns, opts.allNamespaces, args)
		if err != nil {
			return err
		}
	} else {
		exceptions, err = selectExceptions(ctx, c, ns, opts.selector, opts.plan)
		if err != nil {
			return err
		}
		if stateFilter != nil {
			filtered := exceptions[:0]
			for _, exception := range exceptions {
				if _, ok := stateFilter[exception.Status.State]; ok {
					filtered = append(filtered, exception)
				}
			}
			exceptions = filtered
		}
	}

	now := time.Now()
	if opts.now != nil {
		now = opts.now()
	}

	items := make([]printers.ExceptionStatusItem, 0, len(exceptions))
	plans := map[types.NamespacedName]*hibernatorv1alpha1.HibernatePlan{}
	missingPlans := map[types.NamespacedName]struct{}{}
	for _, exception := range exceptions {
		planNS := exception.Spec.PlanRef.Namespace
		if planNS == "" {
			planNS = exception.Namespace
		}
		key := types.NamespacedName{Namespace: planNS, Name: exception.Spec.PlanRef.Name}

		item := printers.ExceptionStatusItem{Exception: exception, Timing: formatExceptionTiming(now, exception)}

		if _, ok := missingPlans[key]; ok {
			item.PlanExists = false
			items = append(items, item)
			continue
		}
		cached, ok := plans[key]
		if !ok {
			var plan hibernatorv1alpha1.HibernatePlan
			if err := c.Get(ctx, key, &plan); err != nil {
				if apierrors.IsNotFound(err) {
					missingPlans[key] = struct{}{}
					item.PlanExists = false
					items = append(items, item)
					continue
				}
				return fmt.Errorf("get HibernatePlan %s/%s: %w", key.Namespace, key.Name, err)
			}
			cached = &plan
			plans[key] = cached
		}
		item.PlanExists = true
		item.PlanPhase = string(cached.Status.Phase)
		items = append(items, item)
	}

	out := &printers.ExceptionStatusOutput{Items: items}
	d := &printers.Dispatcher{JSON: opts.root.JsonOutput}
	return d.PrintObj(out, output.WriterFromContext(ctx))
}

func resolveStatusNames(ctx context.Context, c client.Client, namespace string, allNamespaces bool, names []string) ([]hibernatorv1alpha1.ScheduleException, error) {
	if allNamespaces {
		all, err := selectExceptions(ctx, c, "", "", "")
		if err != nil {
			return nil, err
		}
		wanted := map[string]struct{}{}
		for _, name := range names {
			wanted[name] = struct{}{}
		}
		var matched []hibernatorv1alpha1.ScheduleException
		seen := map[types.NamespacedName]struct{}{}
		for _, exception := range all {
			if _, ok := wanted[exception.Name]; !ok {
				continue
			}
			key := types.NamespacedName{Namespace: exception.Namespace, Name: exception.Name}
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			matched = append(matched, exception)
		}
		for _, name := range names {
			found := false
			for _, exception := range matched {
				if exception.Name == name {
					found = true
					break
				}
			}
			if !found {
				return nil, fmt.Errorf("ScheduleException %q not found", name)
			}
		}
		return matched, nil
	}

	exceptions := make([]hibernatorv1alpha1.ScheduleException, 0, len(names))
	for _, name := range names {
		var exception hibernatorv1alpha1.ScheduleException
		if err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &exception); err != nil {
			return nil, fmt.Errorf("get ScheduleException %s/%s: %w", namespace, name, err)
		}
		exceptions = append(exceptions, exception)
	}
	return exceptions, nil
}

func formatExceptionTiming(now time.Time, exception hibernatorv1alpha1.ScheduleException) string {
	from := exception.Spec.ValidFrom.Time
	until := exception.Spec.ValidUntil.Time
	if from.IsZero() && until.IsZero() {
		return ""
	}
	if !from.IsZero() && now.Before(from) {
		return "starts in " + timeparse.FormatDuration(now, from)
	}
	if !until.IsZero() && !now.Before(until) {
		ago := timeparse.FormatDuration(until, now)
		if ago == "" || ago == "overdue" {
			return "expired"
		}
		return "expired " + ago + " ago"
	}
	if !until.IsZero() {
		remaining := timeparse.FormatDuration(now, until)
		if remaining == "" {
			return "active"
		}
		return remaining + " remaining"
	}
	return "active"
}

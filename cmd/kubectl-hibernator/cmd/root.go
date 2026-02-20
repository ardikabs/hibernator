/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
)

var scheme = runtime.NewScheme()

func init() {
	_ = clientgoscheme.AddToScheme(scheme)
	_ = hibernatorv1alpha1.AddToScheme(scheme)
}

// rootOptions holds global options shared across subcommands.
type rootOptions struct {
	kubeconfig string
	namespace  string
	jsonOutput bool
}

// NewRootCommand creates the root command for kubectl-hibernator.
func NewRootCommand() *cobra.Command {
	opts := &rootOptions{}

	cmd := &cobra.Command{
		Use:   "kubectl-hibernator",
		Short: "Manage Hibernator plans from the command line",
		Long: "kubectl-hibernator is a CLI plugin for managing HibernatePlan resources.\n\n" +
			"It provides commands to inspect schedules, view plan status, suspend/resume\n" +
			"hibernation, trigger retries, and tail controller logs.\n\n" +
			"Install by copying the binary to your PATH:\n" +
			"  cp bin/kubectl-hibernator /usr/local/bin/kubectl-hibernator\n\n" +
			"Then use as:\n" +
			"  kubectl hibernator show schedule my-plan\n" +
			"  kubectl hibernator show status my-plan\n" +
			"  kubectl hibernator suspend my-plan --hours 4 --reason \"deployment\"\n" +
			"  kubectl hibernator resume my-plan\n" +
			"  kubectl hibernator retry my-plan\n" +
			"  kubectl hibernator logs my-plan",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// Global flags
	cmd.PersistentFlags().StringVar(&opts.kubeconfig, "kubeconfig", "", "Path to kubeconfig file (defaults to $KUBECONFIG or ~/.kube/config)")
	cmd.PersistentFlags().StringVarP(&opts.namespace, "namespace", "n", "", "Kubernetes namespace (defaults to current context namespace)")
	cmd.PersistentFlags().BoolVar(&opts.jsonOutput, "json", false, "Output in JSON format")

	// Register subcommands
	cmd.AddCommand(newShowCommand(opts))
	cmd.AddCommand(newSuspendCommand(opts))
	cmd.AddCommand(newResumeCommand(opts))
	cmd.AddCommand(newRetryCommand(opts))
	cmd.AddCommand(newLogsCommand(opts))

	return cmd
}

// newK8sClient creates a controller-runtime client from the global options.
func newK8sClient(opts *rootOptions) (client.Client, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if opts.kubeconfig != "" {
		loadingRules.ExplicitPath = opts.kubeconfig
	}

	configOverrides := &clientcmd.ConfigOverrides{}
	if opts.namespace != "" {
		configOverrides.Context.Namespace = opts.namespace
	}

	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)

	restConfig, err := kubeConfig.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to build kubeconfig: %w", err)
	}

	c, err := client.New(restConfig, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	return c, nil
}

// resolveNamespace determines the effective namespace from flags or kubeconfig context.
func resolveNamespace(opts *rootOptions) string {
	if opts.namespace != "" {
		return opts.namespace
	}

	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if opts.kubeconfig != "" {
		loadingRules.ExplicitPath = opts.kubeconfig
	}

	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, &clientcmd.ConfigOverrides{})
	ns, _, err := kubeConfig.Namespace()
	if err != nil || ns == "" {
		return "default"
	}

	return ns
}

// exitError prints an error message to stderr.
func exitError(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "Error: "+format+"\n", args...)
}

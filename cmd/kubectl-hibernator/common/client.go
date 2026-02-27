/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package common

import (
	"fmt"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var Scheme = runtime.NewScheme()

func init() {
	_ = clientgoscheme.AddToScheme(Scheme)
	_ = hibernatorv1alpha1.AddToScheme(Scheme)
}

// NewK8sClient creates a controller-runtime client from the global options.
func NewK8sClient(opts *RootOptions) (client.Client, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if opts.Kubeconfig != "" {
		loadingRules.ExplicitPath = opts.Kubeconfig
	}

	configOverrides := &clientcmd.ConfigOverrides{}
	if opts.Namespace != "" {
		configOverrides.Context.Namespace = opts.Namespace
	}

	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)

	restConfig, err := kubeConfig.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to build kubeconfig: %w", err)
	}

	c, err := client.New(restConfig, client.Options{Scheme: Scheme})
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	return c, nil
}

// ResolveNamespace determines the effective namespace from flags or kubeconfig context.
func ResolveNamespace(opts *RootOptions) string {
	if opts.Namespace != "" {
		return opts.Namespace
	}

	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if opts.Kubeconfig != "" {
		loadingRules.ExplicitPath = opts.Kubeconfig
	}

	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, &clientcmd.ConfigOverrides{})
	ns, _, err := kubeConfig.Namespace()
	if err != nil || ns == "" {
		return "default"
	}

	return ns
}

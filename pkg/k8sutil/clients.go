/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package k8sutil

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/ardikabs/hibernator/pkg/awsutil"
)

// K8SConnectorConfig holds Kubernetes connector settings.
type K8SConnectorConfig struct {
	ClusterName     string
	Region          string
	Kubeconfig      []byte
	ClusterEndpoint string
	ClusterCAData   []byte
	UseEKSToken     bool
	AWS             *awsutil.AWSConnectorConfig
}

// BuildClients builds Kubernetes dynamic and typed clients from the connector config.
func BuildClients(ctx context.Context, cfg *K8SConnectorConfig) (dynamic.Interface, kubernetes.Interface, error) {
	if cfg == nil {
		return nil, nil, fmt.Errorf("K8S connector config is required")
	}

	restConfig, err := buildRestConfig(ctx, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("build rest config: %w", err)
	}

	k8sClient, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("create kubernetes client: %w", err)
	}

	dynamicClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("create dynamic client: %w", err)
	}

	return dynamicClient, k8sClient, nil
}

func buildRestConfig(ctx context.Context, cfg *K8SConnectorConfig) (*rest.Config, error) {
	restConfig, err := resolveRestConfig(cfg)
	if err != nil {
		return nil, err
	}

	if cfg.UseEKSToken {
		source, err := newEKSTokenSource(ctx, cfg)
		if err != nil {
			return nil, err
		}
		wrapTokenTransport(restConfig, source)
	}

	return restConfig, nil
}

func resolveRestConfig(cfg *K8SConnectorConfig) (*rest.Config, error) {
	if len(cfg.Kubeconfig) > 0 {
		restConfig, err := clientcmd.RESTConfigFromKubeConfig(cfg.Kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("build rest config from kubeconfig: %w", err)
		}
		return restConfig, nil
	}

	if cfg.ClusterEndpoint != "" || len(cfg.ClusterCAData) > 0 {
		return &rest.Config{
			Host: cfg.ClusterEndpoint,
			TLSClientConfig: rest.TLSClientConfig{
				CAData: cfg.ClusterCAData,
			},
		}, nil
	}

	restConfig, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("get in-cluster config: %w", err)
	}

	return restConfig, nil
}

func ObjectKeyFromString(s string) (types.NamespacedName, error) {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 {
		return types.NamespacedName{}, fmt.Errorf("invalid format: %s (expected namespace/name)", s)
	}
	return types.NamespacedName{
		Namespace: parts[0],
		Name:      parts[1],
	}, nil
}

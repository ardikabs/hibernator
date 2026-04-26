package metadata

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
	"github.com/ardikabs/hibernator/internal/executor"
	"github.com/ardikabs/hibernator/pkg/awsutil"
)

const (
	awsAccessKeyIDKey     = "AWS_ACCESS_KEY_ID"
	awsSecretAccessKeyKey = "AWS_SECRET_ACCESS_KEY"
	awsSessionToken       = "AWS_SESSION_TOKEN"
	kubeconfigKey         = "kubeconfig"
)

// ConfigBuilder constructs the executor.ConnectorConfig from Kubernetes resources.
type ConfigBuilder struct {
	k8sClient client.Client
	log       logr.Logger
}

// NewConfigBuilder creates a new ConfigBuilder.
func NewConfigBuilder(k8sClient client.Client, log logr.Logger) *ConfigBuilder {
	return &ConfigBuilder{
		k8sClient: k8sClient,
		log:       log,
	}
}

// BuildConnectorConfig resolves CloudProvider or K8SCluster references.
func (b *ConfigBuilder) BuildConnectorConfig(ctx context.Context, kind, namespace, name string) (executor.ConnectorConfig, error) {
	var cfg executor.ConnectorConfig
	switch kind {
	case "CloudProvider":
		awsCfg, err := b.loadCloudProviderConfig(ctx, namespace, name)
		if err != nil {
			return cfg, err
		}
		cfg.AWS = awsCfg
	case "K8SCluster":
		k8sCfg, err := b.loadK8SClusterConfig(ctx, namespace, name)
		if err != nil {
			return cfg, err
		}
		cfg.K8S = k8sCfg
	}
	return cfg, nil
}

func resolveNamespace(defaultNamespace, override string) string {
	if override != "" {
		return override
	}
	return defaultNamespace
}

func (b *ConfigBuilder) loadCloudProviderConfig(ctx context.Context, namespace, name string) (*executor.AWSConnectorConfig, error) {
	provider, err := b.getCloudProvider(ctx, namespace, name)
	if err != nil {
		return nil, err
	}

	return b.buildAWSConnectorConfig(ctx, &provider)
}

func (b *ConfigBuilder) getCloudProvider(ctx context.Context, namespace, name string) (hibernatorv1alpha1.CloudProvider, error) {
	var provider hibernatorv1alpha1.CloudProvider
	key := client.ObjectKey{
		Namespace: namespace,
		Name:      name,
	}
	if err := b.k8sClient.Get(ctx, key, &provider); err != nil {
		return provider, fmt.Errorf("get CloudProvider: %w", err)
	}
	return provider, nil
}

func (b *ConfigBuilder) buildAWSConnectorConfig(ctx context.Context, provider *hibernatorv1alpha1.CloudProvider) (*executor.AWSConnectorConfig, error) {
	if provider.Spec.Type != hibernatorv1alpha1.CloudProviderAWS {
		return nil, fmt.Errorf("unsupported cloud provider type: %s", provider.Spec.Type)
	}
	if provider.Spec.AWS == nil {
		return nil, fmt.Errorf("AWS config is required")
	}

	awsCfg := &executor.AWSConnectorConfig{
		Region:    provider.Spec.AWS.Region,
		AccountID: provider.Spec.AWS.AccountId,
	}

	// AssumeRoleArn is now at AWS spec level (cross-cutting for both auth methods)
	if provider.Spec.AWS.AssumeRoleArn != "" {
		awsCfg.AssumeRoleArn = provider.Spec.AWS.AssumeRoleArn
	}

	if provider.Spec.AWS.Auth.Static != nil {
		ref := provider.Spec.AWS.Auth.Static.SecretRef
		secretNamespace := resolveNamespace(provider.Namespace, ref.Namespace)
		secret, err := b.getSecret(ctx, secretNamespace, ref.Name)
		if err != nil {
			return nil, err
		}

		accessKeyID := string(secret.Data[awsAccessKeyIDKey])
		secretAccessKey := string(secret.Data[awsSecretAccessKeyKey])
		if accessKeyID == "" || secretAccessKey == "" {
			return nil, fmt.Errorf("AWS static credentials must include %s and %s", awsAccessKeyIDKey, awsSecretAccessKeyKey)
		}

		awsCfg.AccessKeyID = accessKeyID
		awsCfg.SecretAccessKey = secretAccessKey

		session, ok := secret.Data[awsSessionToken]
		if ok {
			awsCfg.SessionToken = string(session)
		}
	}

	return awsCfg, nil
}

func (b *ConfigBuilder) getSecret(ctx context.Context, namespace, name string) (*corev1.Secret, error) {
	var secret corev1.Secret
	key := client.ObjectKey{
		Namespace: namespace,
		Name:      name,
	}
	if err := b.k8sClient.Get(ctx, key, &secret); err != nil {
		return nil, fmt.Errorf("get Secret %s/%s: %w", namespace, name, err)
	}
	return &secret, nil
}

func (b *ConfigBuilder) loadK8SClusterConfig(ctx context.Context, namespace, name string) (*executor.K8SConnectorConfig, error) {
	var cluster hibernatorv1alpha1.K8SCluster
	key := client.ObjectKey{
		Namespace: namespace,
		Name:      name,
	}
	if err := b.k8sClient.Get(ctx, key, &cluster); err != nil {
		return nil, fmt.Errorf("get K8SCluster: %w", err)
	}

	if cluster.Spec.EKS != nil && cluster.Spec.K8S != nil {
		return nil, fmt.Errorf("spec.eks and spec.k8s are mutually exclusive")
	}

	if cluster.Spec.EKS != nil {
		if cluster.Spec.ProviderRef == nil {
			return nil, fmt.Errorf("providerRef is required for EKS clusters")
		}

		providerNamespace := resolveNamespace(cluster.Namespace, cluster.Spec.ProviderRef.Namespace)
		provider, err := b.getCloudProvider(ctx, providerNamespace, cluster.Spec.ProviderRef.Name)
		if err != nil {
			return nil, err
		}

		awsCfg, err := b.buildAWSConnectorConfig(ctx, &provider)
		if err != nil {
			return nil, err
		}

		if cluster.Spec.EKS.Region != "" {
			awsCfg.Region = cluster.Spec.EKS.Region
		}

		awsSDKConfig, err := awsutil.BuildAWSConfig(ctx, awsCfg)
		if err != nil {
			return nil, err
		}

		eksClient := eks.NewFromConfig(awsSDKConfig)
		clusterInfo, err := eksClient.DescribeCluster(ctx, &eks.DescribeClusterInput{
			Name: aws.String(cluster.Spec.EKS.Name),
		})
		if err != nil {
			return nil, fmt.Errorf("describe EKS cluster: %w", err)
		}

		if clusterInfo.Cluster == nil {
			return nil, fmt.Errorf("EKS cluster %s not found", cluster.Spec.EKS.Name)
		}

		endpoint := aws.ToString(clusterInfo.Cluster.Endpoint)
		if endpoint == "" {
			return nil, fmt.Errorf("EKS cluster endpoint not available")
		}

		caData := aws.ToString(clusterInfo.Cluster.CertificateAuthority.Data)
		if caData == "" {
			return nil, fmt.Errorf("EKS cluster certificate authority data missing")
		}

		decodedCA, err := base64.StdEncoding.DecodeString(caData)
		if err != nil {
			return nil, fmt.Errorf("decode EKS certificate authority data: %w", err)
		}

		return &executor.K8SConnectorConfig{
			ClusterName:     cluster.Spec.EKS.Name,
			Region:          cluster.Spec.EKS.Region,
			ClusterEndpoint: endpoint,
			ClusterCAData:   decodedCA,
			UseEKSToken:     true,
			AWS:             awsCfg,
		}, nil
	}

	if cluster.Spec.K8S != nil {
		if cluster.Spec.K8S.InCluster {
			return &executor.K8SConnectorConfig{}, nil
		}

		if cluster.Spec.K8S.KubeconfigRef != nil {
			ref := cluster.Spec.K8S.KubeconfigRef
			secretNamespace := resolveNamespace(cluster.Namespace, ref.Namespace)
			secret, err := b.getSecret(ctx, secretNamespace, ref.Name)
			if err != nil {
				return nil, err
			}
			kubeconfigBytes := secret.Data[kubeconfigKey]
			if len(kubeconfigBytes) == 0 {
				return nil, fmt.Errorf("kubeconfig secret %s/%s missing %s key", secretNamespace, ref.Name, kubeconfigKey)
			}

			return &executor.K8SConnectorConfig{
				Kubeconfig: kubeconfigBytes,
			}, nil
		}

		return nil, fmt.Errorf("kubeconfigRef or inCluster must be specified for K8S access")
	}

	if cluster.Spec.GKE != nil {
		return &executor.K8SConnectorConfig{
			ClusterName: cluster.Spec.GKE.Name,
			Region:      cluster.Spec.GKE.Location,
		}, nil
	}

	return nil, nil
}

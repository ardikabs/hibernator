/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package metadata

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
)

func schemeForBuilder() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = hibernatorv1alpha1.AddToScheme(scheme)
	return scheme
}

func buildAWSStaticSecret(namespace, name string, accessKey, secretKey, sessionToken string) *corev1.Secret {
	data := map[string][]byte{
		"AWS_ACCESS_KEY_ID":     []byte(accessKey),
		"AWS_SECRET_ACCESS_KEY": []byte(secretKey),
	}
	if sessionToken != "" {
		data["AWS_SESSION_TOKEN"] = []byte(sessionToken)
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Type:       corev1.SecretTypeOpaque,
		Data:       data,
	}
}

func buildKubeconfigSecret(namespace, name, kubeconfig string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Type:       corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"kubeconfig": []byte(kubeconfig),
		},
	}
}

func cloudProviderAwsObj(name, namespace string, region, accountId, assumeRoleArn string, secretRef *hibernatorv1alpha1.SecretReference) *hibernatorv1alpha1.CloudProvider {
	provider := &hibernatorv1alpha1.CloudProvider{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: hibernatorv1alpha1.CloudProviderSpec{
			Type: hibernatorv1alpha1.CloudProviderAWS,
			AWS: &hibernatorv1alpha1.AWSConfig{
				Region:    region,
				AccountId: accountId,
				Auth: hibernatorv1alpha1.AWSAuth{
					Static: &hibernatorv1alpha1.StaticAuth{
						SecretRef: *secretRef,
					},
				},
			},
		},
	}
	if assumeRoleArn != "" {
		provider.Spec.AWS.AssumeRoleArn = assumeRoleArn
	}
	return provider
}

func k8sClusterGkeObj(name, namespace, clusterName, location string) *hibernatorv1alpha1.K8SCluster {
	return &hibernatorv1alpha1.K8SCluster{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: hibernatorv1alpha1.K8SClusterSpec{
			GKE: &hibernatorv1alpha1.GKEConfig{
				Name:     clusterName,
				Location: location,
			},
		},
	}
}

func k8sClusterK8sInCluster(name, namespace string) *hibernatorv1alpha1.K8SCluster {
	return &hibernatorv1alpha1.K8SCluster{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: hibernatorv1alpha1.K8SClusterSpec{
			K8S: &hibernatorv1alpha1.K8SAccessConfig{
				InCluster: true,
			},
		},
	}
}

func k8sClusterK8sKubeconfigRef(name, namespace string, kubeconfigRef *hibernatorv1alpha1.KubeconfigRef) *hibernatorv1alpha1.K8SCluster {
	return &hibernatorv1alpha1.K8SCluster{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: hibernatorv1alpha1.K8SClusterSpec{
			K8S: &hibernatorv1alpha1.K8SAccessConfig{
				KubeconfigRef: kubeconfigRef,
			},
		},
	}
}

func TestBuildConnectorConfig_CloudProvider(t *testing.T) {
	secret := buildAWSStaticSecret("default", "aws-creds", "AKIA1234567890", "super-secret", "session-token")
	provider := cloudProviderAwsObj("my-provider", "default", "us-west-2", "123456789", "", &hibernatorv1alpha1.SecretReference{Name: "aws-creds", Namespace: "default"})

	fakeClient := fake.NewClientBuilder().
		WithScheme(schemeForBuilder()).
		WithObjects(secret, provider).
		Build()

	b := NewConfigBuilder(fakeClient, logr.Discard())

	cfg, err := b.BuildConnectorConfig(context.Background(), "CloudProvider", "default", "my-provider")
	require.NoError(t, err)

	assert.NotNil(t, cfg.AWS)
	assert.Equal(t, "us-west-2", cfg.AWS.Region)
	assert.Equal(t, "123456789", cfg.AWS.AccountID)
	assert.Equal(t, "AKIA1234567890", cfg.AWS.AccessKeyID)
	assert.Equal(t, "super-secret", cfg.AWS.SecretAccessKey)
	assert.Equal(t, "session-token", cfg.AWS.SessionToken)
}

func TestBuildConnectorConfig_K8SCluster_GKE(t *testing.T) {
	cluster := k8sClusterGkeObj("my-cluster", "default", "my-gke-cluster", "us-central1")

	fakeClient := fake.NewClientBuilder().
		WithScheme(schemeForBuilder()).
		WithObjects(cluster).
		Build()

	b := NewConfigBuilder(fakeClient, logr.Discard())

	cfg, err := b.BuildConnectorConfig(context.Background(), "K8SCluster", "default", "my-cluster")
	require.NoError(t, err)

	assert.NotNil(t, cfg.K8S)
	assert.Equal(t, "my-gke-cluster", cfg.K8S.ClusterName)
	assert.Equal(t, "us-central1", cfg.K8S.Region)
}

func TestBuildConnectorConfig_K8SCluster_InCluster(t *testing.T) {
	cluster := k8sClusterK8sInCluster("my-cluster", "default")

	fakeClient := fake.NewClientBuilder().
		WithScheme(schemeForBuilder()).
		WithObjects(cluster).
		Build()

	b := NewConfigBuilder(fakeClient, logr.Discard())

	cfg, err := b.BuildConnectorConfig(context.Background(), "K8SCluster", "default", "my-cluster")
	require.NoError(t, err)

	assert.NotNil(t, cfg.K8S)
	assert.Empty(t, cfg.K8S.ClusterName)
}

func TestBuildConnectorConfig_K8SCluster_KubeconfigRef(t *testing.T) {
	secret := buildKubeconfigSecret("default", "kubeconfig-secret", "yaml-content-here")
	cluster := k8sClusterK8sKubeconfigRef("my-cluster", "default", &hibernatorv1alpha1.KubeconfigRef{Name: "kubeconfig-secret", Namespace: "default"})

	fakeClient := fake.NewClientBuilder().
		WithScheme(schemeForBuilder()).
		WithObjects(secret, cluster).
		Build()

	b := NewConfigBuilder(fakeClient, logr.Discard())

	cfg, err := b.BuildConnectorConfig(context.Background(), "K8SCluster", "default", "my-cluster")
	require.NoError(t, err)

	assert.NotNil(t, cfg.K8S)
	assert.Equal(t, []byte("yaml-content-here"), cfg.K8S.Kubeconfig)
}

func TestBuildConnectorConfig_CloudProvider_AssumeRole(t *testing.T) {
	secret := buildAWSStaticSecret("default", "aws-creds", "AKIA1234567890", "super-secret", "")
	provider := cloudProviderAwsObj("my-provider", "default", "us-west-2", "123456789", "arn:aws:iam::123456789:role/my-role", &hibernatorv1alpha1.SecretReference{Name: "aws-creds", Namespace: "default"})

	fakeClient := fake.NewClientBuilder().
		WithScheme(schemeForBuilder()).
		WithObjects(secret, provider).
		Build()

	b := NewConfigBuilder(fakeClient, logr.Discard())

	cfg, err := b.BuildConnectorConfig(context.Background(), "CloudProvider", "default", "my-provider")
	require.NoError(t, err)

	assert.Equal(t, "arn:aws:iam::123456789:role/my-role", cfg.AWS.AssumeRoleArn)
}

func TestBuildConnectorConfig_CloudProvider_MissingSecret(t *testing.T) {
	provider := cloudProviderAwsObj("my-provider", "default", "us-west-2", "123456789", "", &hibernatorv1alpha1.SecretReference{Name: "nonexistent", Namespace: "default"})

	fakeClient := fake.NewClientBuilder().
		WithScheme(schemeForBuilder()).
		WithObjects(provider).
		Build()

	b := NewConfigBuilder(fakeClient, logr.Discard())

	_, err := b.BuildConnectorConfig(context.Background(), "CloudProvider", "default", "my-provider")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get Secret")
}

func TestBuildConnectorConfig_CloudProvider_MissingCredentials(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "incomplete-creds"},
		Type:       corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"AWS_ACCESS_KEY_ID": []byte("only-key"),
			// missing AWS_SECRET_ACCESS_KEY
		},
	}
	provider := cloudProviderAwsObj("my-provider", "default", "us-west-2", "123456789", "", &hibernatorv1alpha1.SecretReference{Name: "incomplete-creds", Namespace: "default"})

	fakeClient := fake.NewClientBuilder().
		WithScheme(schemeForBuilder()).
		WithObjects(secret, provider).
		Build()

	b := NewConfigBuilder(fakeClient, logr.Discard())

	_, err := b.BuildConnectorConfig(context.Background(), "CloudProvider", "default", "my-provider")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AWS static credentials must include")
}

func TestBuildConnectorConfig_CloudProvider_NotFound(t *testing.T) {
	fakeClient := fake.NewClientBuilder().
		WithScheme(schemeForBuilder()).
		Build()

	b := NewConfigBuilder(fakeClient, logr.Discard())

	_, err := b.BuildConnectorConfig(context.Background(), "CloudProvider", "default", "nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get CloudProvider")
}

func TestBuildConnectorConfig_K8SCluster_NotFound(t *testing.T) {
	fakeClient := fake.NewClientBuilder().
		WithScheme(schemeForBuilder()).
		Build()

	b := NewConfigBuilder(fakeClient, logr.Discard())

	_, err := b.BuildConnectorConfig(context.Background(), "K8SCluster", "default", "nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get K8SCluster")
}

func TestBuildConnectorConfig_K8SCluster_KubeconfigRef_MissingKey(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "kubeconfig-secret"},
		Type:       corev1.SecretTypeOpaque,
		Data:       map[string][]byte{}, // missing "kubeconfig" key
	}
	cluster := k8sClusterK8sKubeconfigRef("my-cluster", "default", &hibernatorv1alpha1.KubeconfigRef{Name: "kubeconfig-secret", Namespace: "default"})

	fakeClient := fake.NewClientBuilder().
		WithScheme(schemeForBuilder()).
		WithObjects(secret, cluster).
		Build()

	b := NewConfigBuilder(fakeClient, logr.Discard())

	_, err := b.BuildConnectorConfig(context.Background(), "K8SCluster", "default", "my-cluster")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "kubeconfig secret")
}

func TestBuildConnectorConfig_K8SCluster_KubeconfigRef_NamespaceOverride(t *testing.T) {
	secret := buildKubeconfigSecret("other-ns", "kubeconfig-secret", "yaml-content-here")
	cluster := k8sClusterK8sKubeconfigRef("my-cluster", "default", &hibernatorv1alpha1.KubeconfigRef{Name: "kubeconfig-secret", Namespace: "other-ns"})

	fakeClient := fake.NewClientBuilder().
		WithScheme(schemeForBuilder()).
		WithObjects(secret, cluster).
		Build()

	b := NewConfigBuilder(fakeClient, logr.Discard())

	cfg, err := b.BuildConnectorConfig(context.Background(), "K8SCluster", "default", "my-cluster")
	require.NoError(t, err)
	assert.Equal(t, []byte("yaml-content-here"), cfg.K8S.Kubeconfig)
}

func TestBuildConnectorConfig_K8SCluster_KubeconfigRef_DefaultNamespace(t *testing.T) {
	secret := buildKubeconfigSecret("default", "kubeconfig-secret", "yaml-content-here")
	cluster := k8sClusterK8sKubeconfigRef("my-cluster", "default", &hibernatorv1alpha1.KubeconfigRef{Name: "kubeconfig-secret"}) // no namespace override

	fakeClient := fake.NewClientBuilder().
		WithScheme(schemeForBuilder()).
		WithObjects(secret, cluster).
		Build()

	b := NewConfigBuilder(fakeClient, logr.Discard())

	cfg, err := b.BuildConnectorConfig(context.Background(), "K8SCluster", "default", "my-cluster")
	require.NoError(t, err)
	assert.Equal(t, []byte("yaml-content-here"), cfg.K8S.Kubeconfig)
}

func TestResolveNamespace(t *testing.T) {
	assert.Equal(t, "override", resolveNamespace("default", "override"))
	assert.Equal(t, "default", resolveNamespace("default", ""))
}

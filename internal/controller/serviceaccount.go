/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

package controller

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
)

const (
	// RunnerServiceAccountPrefix is the prefix for runner service accounts.
	RunnerServiceAccountPrefix = "hibernator-runner-"

	// RunnerRoleName is the name of the runner ClusterRole.
	RunnerRoleName = "hibernator-runner"

	// AnnotationIRSARoleARN is the annotation for IRSA role ARN.
	AnnotationIRSARoleARN = "eks.amazonaws.com/role-arn"

	// AnnotationAzureClientID is the annotation for Azure Workload Identity.
	AnnotationAzureClientID = "azure.workload.identity/client-id"

	// AnnotationGCPServiceAccount is the annotation for GCP Workload Identity.
	AnnotationGCPServiceAccount = "iam.gke.io/gcp-service-account"
)

// RunnerServiceAccountManager manages runner ServiceAccounts.
type RunnerServiceAccountManager struct {
	client client.Client
	log    logr.Logger
}

// NewRunnerServiceAccountManager creates a new manager.
func NewRunnerServiceAccountManager(c client.Client, log logr.Logger) *RunnerServiceAccountManager {
	return &RunnerServiceAccountManager{
		client: c,
		log:    log.WithName("runner-sa-manager"),
	}
}

// ServiceAccountName returns the runner ServiceAccount name for a plan.
func ServiceAccountName(planName string) string {
	return fmt.Sprintf("%s%s", RunnerServiceAccountPrefix, planName)
}

// EnsureServiceAccount creates or updates the runner ServiceAccount for a plan.
func (m *RunnerServiceAccountManager) EnsureServiceAccount(ctx context.Context, plan *hibernatorv1alpha1.HibernatePlan) (*corev1.ServiceAccount, error) {
	saName := ServiceAccountName(plan.Name)

	// Build desired ServiceAccount
	desired := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      saName,
			Namespace: plan.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":       "hibernator-runner",
				"app.kubernetes.io/managed-by": "hibernator",
				"hibernator.ardikabs.com/plan": plan.Name,
			},
			Annotations: make(map[string]string),
		},
	}

	// Add workload identity annotations based on connectors
	if err := m.addWorkloadIdentityAnnotations(ctx, plan, desired); err != nil {
		m.log.Error(err, "failed to add workload identity annotations", "plan", plan.Name)
		// Continue without annotations - the executor may use other auth methods
	}

	// Get existing ServiceAccount
	existing := &corev1.ServiceAccount{}
	err := m.client.Get(ctx, types.NamespacedName{
		Namespace: plan.Namespace,
		Name:      saName,
	}, existing)

	if errors.IsNotFound(err) {
		// Create new ServiceAccount
		if err := controllerutil.SetControllerReference(plan, desired, m.client.Scheme()); err != nil {
			return nil, fmt.Errorf("set controller reference: %w", err)
		}
		if err := m.client.Create(ctx, desired); err != nil {
			return nil, fmt.Errorf("create service account: %w", err)
		}
		m.log.Info("created runner service account", "name", saName)
		return desired, nil
	} else if err != nil {
		return nil, fmt.Errorf("get service account: %w", err)
	}

	// Update annotations if needed
	needsUpdate := false
	for k, v := range desired.Annotations {
		if existing.Annotations == nil {
			existing.Annotations = make(map[string]string)
		}
		if existing.Annotations[k] != v {
			existing.Annotations[k] = v
			needsUpdate = true
		}
	}

	if needsUpdate {
		if err := m.client.Update(ctx, existing); err != nil {
			return nil, fmt.Errorf("update service account: %w", err)
		}
		m.log.Info("updated runner service account", "name", saName)
	}

	return existing, nil
}

// addWorkloadIdentityAnnotations adds cloud workload identity annotations.
func (m *RunnerServiceAccountManager) addWorkloadIdentityAnnotations(ctx context.Context, plan *hibernatorv1alpha1.HibernatePlan, sa *corev1.ServiceAccount) error {
	// Find CloudProvider connectors and extract IAM role info
	for _, target := range plan.Spec.Targets {
		if target.ConnectorRef.Kind != "CloudProvider" {
			continue
		}

		var provider hibernatorv1alpha1.CloudProvider
		ns := target.ConnectorRef.Namespace
		if ns == "" {
			ns = plan.Namespace
		}

		err := m.client.Get(ctx, types.NamespacedName{
			Namespace: ns,
			Name:      target.ConnectorRef.Name,
		}, &provider)
		if err != nil {
			continue // Skip if connector not found
		}

		switch provider.Spec.Type {
		case hibernatorv1alpha1.CloudProviderAWS:
			if provider.Spec.AWS != nil && provider.Spec.AWS.Auth.ServiceAccount != nil {
				if provider.Spec.AWS.Auth.ServiceAccount.AssumeRoleArn != "" {
					sa.Annotations[AnnotationIRSARoleARN] = provider.Spec.AWS.Auth.ServiceAccount.AssumeRoleArn
				}
			}

			// TODO: Add support for other cloud providers (GCP, Azure)
		}
	}

	return nil
}

// EnsureRoleBinding creates the RoleBinding for the runner ServiceAccount.
func (m *RunnerServiceAccountManager) EnsureRoleBinding(ctx context.Context, plan *hibernatorv1alpha1.HibernatePlan) error {
	saName := ServiceAccountName(plan.Name)
	rbName := fmt.Sprintf("hibernator-runner-%s", plan.Name)

	desired := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rbName,
			Namespace: plan.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":       "hibernator-runner",
				"app.kubernetes.io/managed-by": "hibernator",
				"hibernator.ardikabs.com/plan": plan.Name,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "ClusterRole",
			Name:     RunnerRoleName,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      saName,
				Namespace: plan.Namespace,
			},
		},
	}

	existing := &rbacv1.RoleBinding{}
	err := m.client.Get(ctx, types.NamespacedName{
		Namespace: plan.Namespace,
		Name:      rbName,
	}, existing)

	if errors.IsNotFound(err) {
		if err := controllerutil.SetControllerReference(plan, desired, m.client.Scheme()); err != nil {
			return fmt.Errorf("set controller reference: %w", err)
		}
		if err := m.client.Create(ctx, desired); err != nil {
			return fmt.Errorf("create role binding: %w", err)
		}
		m.log.Info("created runner role binding", "name", rbName)
		return nil
	} else if err != nil {
		return fmt.Errorf("get role binding: %w", err)
	}

	// RoleBinding exists, no update needed for now
	return nil
}

// DeleteServiceAccount removes the runner ServiceAccount for a plan.
func (m *RunnerServiceAccountManager) DeleteServiceAccount(ctx context.Context, namespace, planName string) error {
	saName := ServiceAccountName(planName)

	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      saName,
			Namespace: namespace,
		},
	}

	if err := m.client.Delete(ctx, sa); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("delete service account: %w", err)
	}

	m.log.Info("deleted runner service account", "name", saName)
	return nil
}

// DeleteRoleBinding removes the runner RoleBinding for a plan.
func (m *RunnerServiceAccountManager) DeleteRoleBinding(ctx context.Context, namespace, planName string) error {
	rbName := fmt.Sprintf("hibernator-runner-%s", planName)

	rb := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rbName,
			Namespace: namespace,
		},
	}

	if err := m.client.Delete(ctx, rb); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("delete role binding: %w", err)
	}

	m.log.Info("deleted runner role binding", "name", rbName)
	return nil
}

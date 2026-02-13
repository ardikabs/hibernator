//go:build e2e

package testutil

import (
	"context"
	"time"

	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hibernatorv1alpha1 "github.com/ardikabs/hibernator/api/v1alpha1"
)

const (
	triggerAnnotationKey = "hibernator.ardikabs.com/trigger-test"
)

// TriggerReconcile forces the controller to reconcile by updating an annotation on the object.
// This is necessary because advancing a fakeClock doesn't wake up the controller
// from its RequeueAfter sleep (which uses real-world timers).
func TriggerReconcile(ctx context.Context, k8sClient client.Client, obj client.Object) {
	Eventually(func() error {
		// 1. Fetch the latest version of the object
		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
			return err
		}

		// 2. Update a "trigger" annotation with a unique value
		annotations := obj.GetAnnotations()
		if annotations == nil {
			annotations = make(map[string]string)
		}
		annotations[triggerAnnotationKey] = time.Now().String()
		obj.SetAnnotations(annotations)

		// 3. Push the update back to the server
		return k8sClient.Update(ctx, obj)
	}, DefaultTimeout, DefaultInterval).Should(Succeed())
}

// ReconcileUntilReady triggers reconciliation on a HibernatePlan and waits for it to be processed.
// timeout specifies the maximum time to wait for the plan to reach a stable state.
func ReconcileUntilReady(ctx context.Context, k8sClient client.Client, plan *hibernatorv1alpha1.HibernatePlan, timeout time.Duration) {
	triggerId := plan.Annotations[triggerAnnotationKey]

	TriggerReconcile(ctx, k8sClient, plan)
	Eventually(func() bool {
		if triggerId != plan.Annotations[triggerAnnotationKey] {
			return true
		}

		if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(plan), plan); err != nil {
			return false
		}

		return false
	}, timeout, DefaultInterval).Should(BeTrue())
}

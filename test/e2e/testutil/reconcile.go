//go:build e2e

package testutil

import (
	"context"
	"time"

	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TriggerReconcile forces the controller to reconcile by updating an annotation on the object.
// This is necessary because advancing a fakeClock doesn't wake up the controller
// from its RequeueAfter sleep (which uses real-world timers).
func TriggerReconcile(ctx context.Context, k8sClient client.Client, obj client.Object) {
	// 1. Fetch the latest version of the object
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(obj), obj)).To(Succeed())

	// 2. Update a "trigger" annotation with a unique value
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations["hibernator.ardikabs.com/trigger-test"] = time.Now().String()
	obj.SetAnnotations(annotations)

	// 3. Push the update back to the server
	Expect(k8sClient.Update(ctx, obj)).To(Succeed())
}

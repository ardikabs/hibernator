//go:build e2e

/*
Copyright 2026 Ardika Saputro.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package e2e

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"

	_ "github.com/ardikabs/hibernator/test/e2e/tests"
)

func init() {
	// If KUBEBUILDER_ASSETS is not set, try to resolve it using setup-envtest
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		// Equivalent to $(setup-envtest use -p path)
		out, err := exec.Command("setup-envtest", "use", "-p", "path").Output()
		if err == nil {
			os.Setenv("KUBEBUILDER_ASSETS", strings.TrimSpace(string(out)))
		}
	}
}

func TestE2E(t *testing.T) {
	gomega.RegisterFailHandler(ginkgo.Fail)
	ginkgo.RunSpecs(t, "E2E Test Suite")
}

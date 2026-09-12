# kind Full-Chain E2E (Control Plane → Runner Pods → Floci) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Real end-to-end test on a kind cluster: deployed controller → real runner pods → real AWS calls against host Floci, with all validation in Go.

**Architecture:** `test/kind` suite (stdlib testing + testify, `//go:build kind`) owns everything via `TestMain`: kind cluster, local image builds (`controller:kind`, `runner:kind`, `runner-test:kind` with Floci endpoint baked in), operator deploy (CRDs + RBAC + test-owned Deployment args), host-Floci routing via a manual-Endpoints Service, then the golden-path EC2 test on real wall-clock windows. Zero production code changes.

**Tech Stack:** Go 1.26, kind (bin/kind, node `kindest/node:v1.34.0` to match k8s.io/api v0.34), kubectl + docker CLIs (shell-out), controller-runtime client, aws-sdk-go-v2, testify require.

**Spec:** Chat-approved kind design 2026-09-07 (user chose kind over in-process). Prior plan: `docs/superpowers/plans/2026-09-07-floci-ec2-e2e.md` (executor-level suite stays as fast feedback). Scenario: `website/docs/scenarios/nightly-full-shutdown.md` (EC2 slice).

## Global Constraints

- No git commits without an explicit user request (repo policy — end at green tests, uncommitted).
- Do NOT run `test/e2e/...` without asking (repo policy). `test/floci/...` and `test/kind/` are authorized.
- `//go:build kind` on all new test files; default `go build/test ./...` unaffected. `UNIT_TEST_PKGS` must also exclude `/test/kind`.
- New binaries to `bin/` (`bin/kind`). Images only `kind load`ed, never pushed.
- TDD where it applies (pure helpers like window computation get unit-style tests first; cluster steps are verified live).

---

## File Structure

| File | Responsibility |
|---|---|
| `test/kind/kind_test.go` | `TestMain` (full setup/teardown) + `TestEC2FullChain` golden path |
| `test/kind/setup.go` | Cluster/images/deploy/routing helpers (shell-outs + k8s client) |
| `test/kind/webhookcert.go` | Self-signed webhook cert → Secret (server idles; no ValidatingWebhookConfiguration) |
| `test/kind/Dockerfile.runner-test` | Test runner image: `ENV AWS_ENDPOINT_URL=http://floci.hibernator-system.svc:4566` |
| `test/kind/README.md` | Run instructions + architecture + troubleshooting |
| `test/floci/harness/harness.go` (modify) | Retag `//go:build e2e \|\| floci \|\| kind` so kind suite reuses seed/poll core |
| `Makefile` (modify) | Exclude `/test/kind` from `UNIT_TEST_PKGS`; `bin/kind` installer; `test-kind` target |

Key deployment facts (verified, locked here):
- Controller needs cert files at `/tmp/k8s-webhook-server/serving-certs` or the manager won't start (webhook server hardcoded `:9443`). We mount a self-signed Secret there and run with `--enable-streaming=false` (also dodges the manifest's `:9443` grpc clash) and `--control-plane-endpoint=127.0.0.1` (runner telemetry dial-refused fast path; telemetry is nil-guarded).
- `--leader-elect=false`, `--runner-image=hibernator-runner-test:kind`, `--runner-service-account=hibernator-runner`.
- Helm (not the controller) creates the runner SA in prod → setup creates SA `hibernator-runner` + RoleBinding→ClusterRole `hibernator-runner` in the test namespace. Controller ClusterRole/Binding + SA come from `config/manager/manager.yaml` + `config/rbac/*` via `kubectl apply -f`.
- `make deploy` is broken upstream (references nonexistent `config/default`) — setup applies explicit paths instead.
- Pods reach host Floci via Service `floci.hibernator-system` + manual Endpoints at the kind-bridge gateway IP (`docker network inspect kind` → Gateway, override `FLOCI_HOST_IP`). Must be verified live with a probe pod before trusting it.

---

### Task 1: Toolchain — kind binary, cluster, pod→host route probe

**Files:** none yet (environment only).

**Interfaces:**
- Produces: `bin/kind` present; cluster `hibernator-kind` up; probe pod curl of `http://<gateway>:4566/_floci/health` returns 200 (or a documented fallback decision).

- [ ] **Step 1: Install kind to bin/**

Run: `GOBIN=$PWD/bin go install sigs.k8s.io/kind@v0.29.0 && bin/kind version`
Expected: `kind v0.29.0 ...`. If the version does not exist, retry with `v0.27.0` and note the substitution.

- [ ] **Step 2: Start Floci on the host and create the cluster**

Run: `docker compose -f test/floci/compose.yml up -d` then `bin/kind create cluster --name hibernator-kind --image kindest/node:v1.34.0 --wait 3m`
Expected: cluster up; `kubectl --context kind-hibernator-kind get nodes` shows 1 Ready node.

- [ ] **Step 3: Prove pods can reach host Floci (route decision gate)**

Run:
```bash
GATEWAY=$(docker network inspect kind --format '{{(index .IPAM.Config 0).Gateway}}') && echo "gateway=$GATEWAY"
kubectl --context kind-hibernator-kind run probe --image=curlimages/curl:8.5.0 --restart=Never -- curl -s -o /dev/null -w '%{http_code}\n' http://$GATEWAY:4566/_floci/health
kubectl --context kind-hibernator-kind delete pod probe
```
Expected: `200`. If not 200, stop and decide (fallbacks: `FLOCI_HOST_IP` override with another host IP, or CoreDNS rewrite) — do not build the suite on an unverified route. Record the working address form in `test/kind/README.md`.

---

### Task 2: Suite skeleton — harness retag, test image, TestMain scaffolding

**Files:**
- Modify: `test/floci/harness/harness.go` (build tag only)
- Create: `test/kind/Dockerfile.runner-test`, `test/kind/setup.go`, `test/kind/kind_test.go` (TestMain + skipped placeholder test), `test/kind/README.md`
- Modify: `Makefile` (`UNIT_TEST_PKGS`, `bin/kind`, `test-kind`)

**Interfaces:**
- Consumes: `bin/kind`, host Floci, `test/floci/compose.yml`.
- Produces: `make test-kind` runs `TestMain` that brings up cluster + images + deploy and passes a placeholder test; teardown deletes cluster unless `KEEP_KIND=1`.

- [ ] **Step 1: Retag the harness for reuse**

In `test/floci/harness/harness.go` change the first line to:
```go
//go:build e2e || floci || kind
```
Run: `env -u GOROOT go vet -tags kind ./test/floci/harness/ && env -u GOROOT go test ./test/floci/harness/ -tags=kind -count=1 -v 2>&1 | tail -3`
Expected: vet clean; test SKIP-passes without `FLOCI_ENABLED` (proves the tag compiles the package under `kind`).

- [ ] **Step 2: Create the test runner image definition**

`test/kind/Dockerfile.runner-test`:
```dockerfile
# Test-only runner: bakes the in-cluster Floci endpoint so runner pods
# reach host Floci without any production code change (CloudProvider has
# no endpoint field; the SDK reads AWS_ENDPOINT_URL from the environment).
ARG BASE=hibernator-runner:kind
FROM ${BASE}
ENV AWS_ENDPOINT_URL=http://floci.hibernator-system.svc:4566
ENV AWS_EC2_METADATA_DISABLED=true
```

- [ ] **Step 3: Write setup.go (shell-out + client helpers)**

`test/kind/setup.go` (`//go:build kind`, package `kind`):
```go
// Key helpers and their exact behavior:
// - run(name, args...) (string, error): runs a CLI, returns trimmed stdout; error includes stderr.
// - ensureCluster(): `bin/kind get clusters` contains `hibernator-kind`, else
//   `bin/kind create cluster --name hibernator-kind --image kindest/node:v1.34.0 --wait 3m`.
// - kubeconfigPath(t): `bin/kind get kubeconfig --name hibernator-kind` into t.TempDir()/kubeconfig; returns path.
// - newK8sClient(t, kubeconfig): controller-runtime client with scheme (clientgoscheme + hibernatorv1alpha1).
// - buildAndLoad(t): `docker build -f Dockerfile --target controller -t hibernator-controller:kind .`,
//   same for `--target runner -t hibernator-runner:kind`, then
//   `docker build -f test/kind/Dockerfile.runner-test --build-arg BASE=hibernator-runner:kind -t hibernator-runner-test:kind .`,
//   then `bin/kind load docker-image <all three> --name hibernator-kind`.
// - applyYAML(t, path): `kubectl --kubeconfig <path> apply -f <path>` for
//   config/crd/bases, config/manager/manager.yaml (namespace+SA+ClusterRole+Binding+Deployment),
//   config/rbac/runner_role.yaml. Then PATCH the deployed controller Deployment in code:
//   set image hibernator-controller:kind, imagePullPolicy Never, args
//   [--leader-elect=false, --runner-image=hibernator-runner-test:kind,
//    --runner-service-account=hibernator-runner, --control-plane-endpoint=127.0.0.1,
//    --enable-streaming=false], volume mount webhook Secret at /tmp/k8s-webhook-server/serving-certs,
//   and delete the container port 9443/8082 stanzas only if they clash (they don't after streaming=false).
// - ensureRunnerRBAC(t, client, ns): SA hibernator-runner + RoleBinding→ClusterRole hibernator-runner in ns.
// - ensureFlociRoute(t, client): gateway IP via FLOCI_HOST_IP or
//   `docker network inspect kind --format '{{(index .IPAM.Config 0).Gateway}}'`;
//   create/ensure Service floci (port 4566) + Endpoints (ip:4566) in hibernator-system.
// - waitForDeployment(t, client, ns, name, timeout): poll Available condition.
// Full function bodies are written at implementation time against these contracts;
// each must return descriptive errors naming the failed CLI and its stderr.
```

- [ ] **Step 4: Write TestMain + README + Makefile wiring**

`test/kind/kind_test.go`:
```go
//go:build kind

package kind

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	// Setup is idempotent: ensureCluster, kubeconfig, client, buildAndLoad,
	// apply manifests, webhook cert secret, patch deployment, runner RBAC,
	// floci route, wait for controller. Any fatal error -> os.Exit(1) with log.
	// Deferred teardown: `bin/kind delete cluster --name hibernator-kind`
	// unless KEEP_KIND=1.
	os.Exit(m.Run())
}

func TestPlaceholder(t *testing.T) {
	t.Log("scaffolding up; real test lands in Task 3")
}
```
`Makefile`: append `|/test/kind` to the `UNIT_TEST_PKGS` grep; add:
```make
KIND ?= $(CURDIR)/bin/kind
KIND_CLUSTER ?= hibernator-kind

.PHONY: kind-tool
kind-tool: ## Install kind to bin/.
	@test -s $(KIND) || { GOBIN=$(CURDIR)/bin go install sigs.k8s.io/kind@v0.29.0; }

.PHONY: test-kind
test-kind: kind-tool ## Full-chain kind E2E (builds images, provisions cluster, runs Go suite).
	@echo "$(CYAN)Running kind full-chain E2E tests...$(RESET)"
	@$(GOCMD) test ./test/kind/ -v -tags=kind -count=1 -timeout 60m
```
Run: `env -u GOROOT go vet -tags kind ./test/kind/`
Expected: clean. (Live run waits for Task 3's real test; placeholder only proves scaffolding compiles.)

---

### Task 3: Golden-path test — seed, window, shutdown, restore assert, wakeup

**Files:**
- Create: `test/kind/webhookcert.go`
- Modify: `test/kind/kind_test.go` (replace placeholder with `TestEC2FullChain`)

**Interfaces:**
- Consumes: setup.go helpers, `harness` seed/poll core, `restore.NewManager(...).Load`, EC2 SDK describe.
- Produces: green full chain on real wall clock; failing loudly with phase/Job/restore context.

- [ ] **Step 1: Webhook cert helper (TDD-able pure part first)**

`test/kind/webhookcert.go` (`//go:build kind`): `makeWebhookCert(t) (certPEM, keyPEM []byte)` — self-signed ECDSA P-256 CA + leaf with DNS SANs `hibernator-webhook-service`, `hibernator-webhook-service.hibernator-system`, `hibernator-webhook-service.hibernator-system.svc`; 24h validity. Setup creates Secret `hibernator-webhook-certs` in `hibernator-system` with keys `tls.crt`/`tls.key` mounted at `/tmp/k8s-webhook-server/serving-certs` via the Deployment patch. (No ValidatingWebhookConfiguration objects — the server idles.)
Write it, then a 60-second compile check via `go vet -tags kind ./test/kind/` before wiring into TestMain.

- [ ] **Step 2: Write TestEC2FullChain**

```go
func TestEC2FullChain(t *testing.T) {
	ctx := context.Background()
	// c := shared client from TestMain (package-level, set during setup).
	// hc := harness.Setup(t) // host-side Floci (AWS_ENDPOINT_URL=localhost:4566 via t.Setenv inside Setup)
	const ns = "hibernator-kind-e2e"
	// 1. ensureRunnerRBAC(ns); create Secret kind-floci-creds {AWS_ACCESS_KEY_ID: test, AWS_SECRET_ACCESS_KEY: test}.
	// 2. tags := {"floci-e2e": harness.Nonce(), "Role": "bastion"}; ids := harness.SeedEC2(t, ctx, hc, "kind-fullchain", tags, 2)
	// 3. CloudProvider kind-aws (static secretRef kind-floci-creds, region us-east-1, account 000000000000).
	// 4. Window: start = now+3m "15:04", end = start+12m, days = [today, tomorrow] (covers midnight wrap), timezone UTC.
	//    HibernatePlan kind-nightly: Parallel, Strict, one ec2 target (tags selector, awaitCompletion 3m).
	// 5. Poll plan Phase==Hibernating (timeout 8m, poll 5s).
	// 6. Poll runner Job Complete for (hibernate, target) via label match (timeout 10m).
	//    On timeout: dump Job status + runner pod logs (require.Fail with context).
	// 7. Poll Phase==Hibernated (3m). Load restore: restore.NewManager(c, log).Load(ctx, ns, planName, target);
	//    require 2 keys, each unmarshals to {instanceId, wasRunning:true}, ids match seeded set.
	// 8. Independent AWS check: harness.WaitForInstanceState(stopped) via host client.
	// 9. Poll Phase==WakingUp (starts after window end; timeout end+6m). Poll wakeup Job Complete (10m).
	// 10. Poll Phase==Active (5m). harness.WaitForInstanceState(running).
	// Cleanup (t.Cleanup / AfterTest): delete Plan, CloudProvider, Secret, namespace; harness SeedEC2 cleanup terminates instances.
}
```
Phase/Job polling helpers live in kind_test.go (Gomega-free, testify + wait.PollUntilContextTimeout from `k8s.io/apimachinery/pkg/util/wait`).

- [ ] **Step 3: Run the full chain live**

Run: `docker compose -f test/floci/compose.yml up -d` (if not up) then `env -u GOROOT go test ./test/kind/ -v -tags=kind -count=1 -timeout 60m`
Expected: PASS in ~8-15 min. Triage order on failure: (1) controller logs (`kubectl logs deploy/hibernator-controller`), (2) runner Job/pod describe+logs, (3) Floci state via host SDK, (4) restore ConfigMap content. Fix-forward in this task; record root causes in README troubleshooting.

---

### Task 4: Regression + docs + teardown decision

**Files:** none (verification + README).

- [ ] **Step 1: Prove sibling suites and default builds unaffected**

Run: `env -u GOROOT go build ./...`, `env -u GOROOT go test ./test/floci/... -count=1` (no tags → no packages), `FLOCI_ENABLED=1 env -u GOROOT go test ./test/floci/... -tags=floci -count=1` (Floci still up → PASS, proves harness retag didn't break the fast suite), unit pkgs `pkg/awsutil pkg/executorparams internal/executor/ec2`.
Expected: all green.

- [ ] **Step 2: Finish README and leave the tree uncommitted**

`test/kind/README.md` must document: prerequisites (docker, kubectl, go), `make test-kind`, `KEEP_KIND=1`/`FLOCI_HOST_IP` overrides, the route architecture (host Floci ↔ Endpoints Service), what is real vs simulated (only kubelet-free envtest is gone — here EVERYTHING is real: pods, Jobs, restore ConfigMaps), and troubleshooting entries for every failure actually hit in Task 3. Teardown: `bin/kind delete cluster --name hibernator-kind` + Floci compose down (run them; note KEEP_KIND=1 keeps the cluster for inspection).

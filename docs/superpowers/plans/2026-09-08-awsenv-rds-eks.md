# awsenv Rename + RDS/EKS Executor Integration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rename `test/floci` to provider-neutral `test/awsenv` and add provisioned RDS instance and EKS node-group lifecycle tests with probe-then-skip capability handling.

**Architecture:** Mechanical rename first (build tag `awsenv`, `AWSENV_ENABLED`, `AWSENV_ENDPOINT_URL`, `make test-awsenv`), then a harness core (`Environment`, `FlociProvisioner`, `Capability` constants, conservative unsupported-operation classifier), then RDS and EKS lifecycle tests that provision real fixtures, invoke the real executors, and skip with exact API evidence only when the environment reports an operation unsupported.

**Tech Stack:** Go 1.26, aws-sdk-go-v2 (rds v1.97.0, eks v1.56.0, ec2 v1.201.0, config v1.28.0), Floci digest-pinned compose, testify require, stdlib testing with `//go:build awsenv`.

**Spec:** `docs/superpowers/specs/2026-09-08-awsenv-executor-integration-design.md`

## Global Constraints

- No git commits without an explicit user request (repo policy overrides the skill default — tasks end at green tests, uncommitted).
- Do NOT run `test/e2e/...` without asking (repo policy). `test/awsenv/...` and `test/kind/` are authorized.
- All new test files carry `//go:build awsenv` so default `go build ./...` / `go test ./...` are unaffected.
- New behavior follows TDD: failing test first, watch it fail, minimal implementation, green.
- Floci stays primary; real AWS is out of scope (no real-AWS provisioner in this plan).
- Provisioning failures are failures; only explicitly classified unsupported-operation errors skip.
- Cleanup is scoped to created IDs, bounded, reverse-order, and failure-visible.

---

## File Structure

| File | Responsibility |
|---|---|
| `test/awsenv/` (renamed from `test/floci/`) | Provider-neutral executor integration suite |
| `test/awsenv/environments/floci/compose.yml` (moved) | Pinned Floci runtime asset |
| `test/awsenv/harness/harness.go` (modify) | `Environment` return, RDS/EKS/STS clients, `AWSENV_*` env, `awsenv-` nonce |
| `test/awsenv/harness/capabilities.go` (create) | `Capability` constants + `IsUnsupported` classifier + `RequireNoErrorOrSkip` |
| `test/awsenv/harness/provision_rds.go` (create) | `RDSFixture`, `ProvisionRDS`, `WaitForDBInstanceState`, `DeleteRDSFixture` |
| `test/awsenv/harness/provision_eks.go` (create) | `EKSFixture`, `ProvisionEKS`, `WaitForNodegroupState`, `DeleteEKSFixture` |
| `test/awsenv/ec2_lifecycle_test.go` (moved, retag) | Existing mixed-state EC2 test, unchanged logic |
| `test/awsenv/rds_lifecycle_test.go` (create) | RDS instance + snapshot lifecycles, validation test |
| `test/awsenv/eks_lifecycle_test.go` (create) | EKS node-group lifecycle, validation test |
| `test/awsenv/harness/harness_test.go` (moved, retag) | STS readiness smoke only |
| `test/awsenv/README.md` (rewrite) | Provider-neutral usage, capability policy |
| `test/kind/kind_test.go`, `test/kind/setup.go`, `test/kind/README.md`, `test/kind/Dockerfile.runner-test` (modify) | Consume `test/awsenv/harness`; no behavior change |
| `Makefile` (modify) | `test-floci` → `test-awsenv`, `UNIT_TEST_PKGS`, kind targets keep working |

Extensibility contract (locked): executor tests call `harness.Setup(t) *Environment`, use `env.Clients.<SVC>`, provision via `env.Provisioner`-style helpers (`ProvisionRDS`, `ProvisionEKS`), and gate lifecycle assertions behind `RequireNoErrorOrSkip(t, CapabilityX, opName, err)`. New executors add a fixture file + test file; kind consumes the same harness.

---

### Task 1: Rename test/floci to test/awsenv

**Files:**
- Move: `test/floci/` → `test/awsenv/` via `git mv test/floci test/awsenv`
- Move: `test/awsenv/compose.yml` → `test/awsenv/environments/floci/compose.yml` via `git mv`
- Modify: every file under `test/awsenv/` (build tags, package name, env names, comments)
- Modify: `Makefile` (UNIT_TEST_PKGS line 47, test-floci target lines 187-190)
- Modify: `test/kind/kind_test.go` (import + 2 env/msg references), `test/kind/README.md` (compose paths)
- Test: `git grep` for old names; `go vet -tags=awsenv ./test/awsenv/...`; `go vet -tags=kind ./test/kind/`

**Interfaces:**
- Consumes: nothing.
- Produces: `harness.Setup(t) *Clients` unchanged signature (Environment comes in Task 2); suite runs under `-tags=awsenv` with `AWSENV_ENABLED=1`; `AWSENV_ENDPOINT_URL` overrides endpoint.

- [ ] **Step 1: Move the directories**

Run: `git mv test/floci test/awsenv && mkdir -p test/awsenv/environments/floci && git mv test/awsenv/compose.yml test/awsenv/environments/floci/compose.yml && find test/awsenv -type f | sort`
Expected: files listed under `test/awsenv/` including `test/awsenv/environments/floci/compose.yml`; `test/floci` gone.

- [ ] **Step 2: Update build tags, package name, and env vars in the suite**

In `test/awsenv/ec2_lifecycle_test.go`: change `//go:build floci` to `//go:build awsenv`; change `package floci` to `package awsenv`; change import `"github.com/ardikabs/hibernator/test/floci/harness"` to `"github.com/ardikabs/hibernator/test/awsenv/harness"`.

In `test/awsenv/harness/harness.go`: change `//go:build e2e || floci || kind` to `//go:build e2e || awsenv || kind`. Update the package comment: replace `test/floci/<x>_lifecycle_test.go` with `test/awsenv/<x>_lifecycle_test.go`. Change `FLOCI_ENABLED` to `AWSENV_ENABLED` and its message to `"AWS environment test requires AWSENV_ENABLED=1 with an endpoint on :4566 (see test/awsenv/environments/floci/compose.yml)"`. Change `FLOCI_ENDPOINT` to `AWSENV_ENDPOINT_URL`. Change `Nonce()` return from `"floci-%d"` to `"awsenv-%d"`. Update `SeedEC2` comment mentioning Floci only where it names the provider-neutral contract (keep the Floci AMI-mapping sentence, it is a Floci backend fact).

In `test/awsenv/harness/harness_test.go`: change `//go:build floci` to `//go:build awsenv`.

In `test/awsenv/environments/floci/compose.yml`: update the three comment lines to reference `test/awsenv/...` paths and `AWSENV_ENABLED=1 go test ./test/awsenv/... -tags=awsenv -count=1 -v`. Keep the pinned image line and `FLOCI_IMAGE`/`FLOCI_STORAGE_MODE` names unchanged (Floci runtime asset).

- [ ] **Step 3: Update ec2 test tag values**

In `test/awsenv/ec2_lifecycle_test.go`: change tag `"floci-e2e"` to `"awsenv-e2e"` (2 occurrences: lifecycle test and no-match test). Change `TargetName: "floci-ec2"` to `"awsenv-ec2"`.

- [ ] **Step 4: Update Makefile**

Change line 47 exclusion from `/test/floci` to `/test/awsenv`. Replace the `test-floci` target block with:
```make
.PHONY: test-awsenv
test-awsenv: ## Run AWS-environment executor integration tests (requires Floci on :4566, see test/awsenv/environments/floci/compose.yml).
	@echo "$(CYAN)Running AWS environment E2E tests...$(RESET)"
	@AWSENV_ENABLED=1 $(GOCMD) test ./test/awsenv/... -v -tags=awsenv -count=1
```

Also update the kind targets in the same edit session (their test binary now gates on `AWSENV_ENABLED`):
```make
	@AWSENV_ENABLED=1 $(GOCMD) test ./test/kind/ -v -tags=kind -count=1 -timeout 60m
```
(change `FLOCI_ENABLED=1` to `AWSENV_ENABLED=1` on both the `test-kind` and `test-kind-schedule` recipe lines; leave `FLOCI_HOST_IP` docs and `FLOCI_IMAGE` compose override untouched — they are Floci backend details, not suite contract names).

- [ ] **Step 4b: Update kind_test.go gate and harness_test.go accesses**

In `test/kind/kind_test.go`: change the `TestMain` gate `os.Getenv("FLOCI_ENABLED")` to `os.Getenv("AWSENV_ENABLED")` (alongside the message update in Step 5 below — do the import, gate, and message edits together).

In `test/awsenv/harness/harness_test.go`: update every `c.<field>` access on the `Setup` return value to `c.Clients.<field>` (`Region`, plus `EC2`/`STS`/`Endpoint` if referenced).

- [ ] **Step 5: Update kind consumers**

In `test/kind/kind_test.go`: change import `"github.com/ardikabs/hibernator/test/floci/harness"` to `"github.com/ardikabs/hibernator/test/awsenv/harness"`. Change `FLOCI_ENABLED` to `AWSENV_ENABLED` and the log message to `"kind suite requires AWSENV_ENABLED=1 with host Floci on :4566 (see test/awsenv/environments/floci/compose.yml)"`. Update the `harness.Setup(t)` trailing comment to `// host-side AWS environment client; fails unless AWSENV_ENABLED=1`.

In `test/kind/README.md`: change `test/floci/compose.yml` to `test/awsenv/environments/floci/compose.yml` (3 occurrences: architecture bullet, run commands, teardown). Change `` `FLOCI_ENABLED=1` `` to `` `AWSENV_ENABLED=1` ``. Keep `FLOCI_HOST_IP`, `FLOCI_IMAGE`, and `floci.hibernator-system` service/DNS names unchanged (Floci routing details).

- [ ] **Step 6: Verify the rename is complete and green**

Run: `git grep -nE 'test/floci|test-floci|FLOCI_ENABLED|FLOCI_ENDPOINT|tags=floci' -- ':!docs/superpowers/**'`
Expected: no output (historical plan docs under `docs/superpowers/` are exempt).

Run: `env -u GOROOT go vet -tags=awsenv ./test/awsenv/... && env -u GOROOT go vet -tags=kind ./test/kind/ && env -u GOROOT go build ./...`
Expected: all silent success.

---

### Task 2: Harness Environment, clients, and capability core

**Files:**
- Modify: `test/awsenv/harness/harness.go` (Setup returns `*Environment`, add RDS/EKS clients)
- Create: `test/awsenv/harness/capabilities.go`
- Test: `test/awsenv/harness/capabilities_test.go`, existing `harness_test.go` (update only if signatures force it)

**Interfaces:**
- Consumes: `awsconfig.LoadDefaultConfig`, STS readiness (existing).
- Produces (exact signatures later tasks rely on):
  - `type Environment struct { Provider string; Endpoint string; Region string; Clients *Clients; Provisioner Provisioner }`
  - `func Setup(t *testing.T) *Environment` — fails unless `AWSENV_ENABLED=1`; resolves provider via `resolveProvider()` (below); sets `AWS_ENDPOINT_URL`, creds, region, `AWS_EC2_METADATA_DISABLED`; STS readiness ≤60s; sets `Provisioner: &FlociProvisioner{Clients: clients}`.
  - `func resolveProvider() (string, error)` — returns `AWSENV_PROVIDER`, default `"floci"` when empty, error on any other value.
  - `type EC2FixtureSpec struct { Name string; Tags map[string]string; Count int32 }`, `type EC2Fixture struct { InstanceIDs []string }`
  - `type RDSFixtureSpec struct { Prefix string }`, `type RDSFixture struct { InstanceID string }`
  - `type EKSFixtureSpec struct { Prefix string }`, `type EKSFixture struct { ClusterName string; NodegroupName string }`
  - `type Provisioner interface { ProvisionEC2(t *testing.T, ctx context.Context, spec EC2FixtureSpec) EC2Fixture; ProvisionRDS(t *testing.T, ctx context.Context, spec RDSFixtureSpec) RDSFixture; ProvisionEKS(t *testing.T, ctx context.Context, spec EKSFixtureSpec) EKSFixture }`
  - `type FlociProvisioner struct { Clients *Clients }` with `ProvisionEC2` delegating to the existing `SeedEC2` helper (RDS/EKS methods land in Tasks 3/4).
  - `type Clients struct { EC2 *ec2.Client; RDS *rds.Client; EKS *eks.Client; STS *sts.Client; Region string; Endpoint string }`
  - `type Capability string` + `CapabilityEC2Lifecycle Capability = "ec2.lifecycle"`, `CapabilityRDSLifecycle Capability = "rds.instance-lifecycle"`, `CapabilityRDSSnapshot Capability = "rds.instance-snapshot"`, `CapabilityEKSNodeGroupUpdate Capability = "eks.nodegroup-update"`
  - `func IsUnsupported(err error) bool` — true only for explicit unknown-operation markers (codes `UnknownOperationException`, `InvalidAction`, `NotImplemented`, `UnsupportedOperation`; message fragments `not implemented`, `not supported`, `unknown operation`, `unknown action`).
  - `func RequireNoErrorOrSkip(t *testing.T, capability Capability, operation string, err error)` — nil: return; `IsUnsupported`: `t.Skipf` with provider (read from `AWSENV_PROVIDER`, default `"floci"`), capability, operation, and err; else `t.Fatalf("operation %s failed: %v", operation, err)`.

- [ ] **Step 1: Write the failing capabilities test**

`test/awsenv/harness/capabilities_test.go`:
```go
//go:build awsenv

package harness

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsUnsupportedMatchesUnknownOperations(t *testing.T) {
	require.True(t, IsUnsupported(errors.New("UnknownOperationException: stop")))
	require.True(t, IsUnsupported(errors.New("NotImplemented: UpdateNodegroupConfig")))
	require.False(t, IsUnsupported(errors.New("AccessDenied: not authorized")))
	require.False(t, IsUnsupported(errors.New("DBInstanceNotFound")))
	require.False(t, IsUnsupported(nil))
}

func TestRequireNoErrorOrSkipPassesThroughNil(t *testing.T) {
	RequireNoErrorOrSkip(t, CapabilityEC2Lifecycle, "StopInstances", nil)
}

func TestResolveProviderDefaultsAndRejects(t *testing.T) {
	t.Setenv("AWSENV_PROVIDER", "")
	provider, err := resolveProvider()
	require.NoError(t, err)
	require.Equal(t, "floci", provider)

	t.Setenv("AWSENV_PROVIDER", "real-aws")
	_, err = resolveProvider()
	require.ErrorContains(t, err, "unsupported AWSENV_PROVIDER")
}
```

Run: `env -u GOROOT go test ./test/awsenv/harness/ -tags=awsenv -count=1 -run 'TestIsUnsupported|TestRequireNoError|TestResolveProvider' -v`
Expected: FAIL with `undefined: IsUnsupported` (proves the test runs and the API is missing).

- [ ] **Step 2: Run test to verify it fails**

Run: same command as Step 1.
Expected: build failure naming `IsUnsupported`, `CapabilityEC2Lifecycle`, `RequireNoErrorOrSkip`. Do not proceed until you see it.

- [ ] **Step 3: Write minimal capabilities.go**

`test/awsenv/harness/capabilities.go`:
```go
//go:build e2e || awsenv || kind

package harness

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// Capability names the exact operation set a lifecycle test requires.
// Service presence is not sufficient: the environment must implement these.
type Capability string

const (
	CapabilityEC2Lifecycle       Capability = "ec2.lifecycle"
	CapabilityRDSLifecycle       Capability = "rds.instance-lifecycle"
	CapabilityRDSSnapshot        Capability = "rds.instance-snapshot"
	CapabilityEKSNodeGroupUpdate Capability = "eks.nodegroup-update"
)

var unsupportedCodeMarkers = []string{
	"UnknownOperationException",
	"InvalidAction",
	"NotImplemented",
	"UnsupportedOperation",
	"OperationNotSupported",
}

var unsupportedMessageMarkers = []string{
	"not implemented",
	"not supported",
	"unknown operation",
	"unknown action",
}

// IsUnsupported reports whether err is an explicit unknown-operation signal.
// Conservative by design: access, state, validation, timeout, connection, and
// server errors are never classified as unsupported.
func IsUnsupported(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, marker := range unsupportedCodeMarkers {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	lowered := strings.ToLower(msg)
	for _, marker := range unsupportedMessageMarkers {
		if strings.Contains(lowered, marker) {
			return true
		}
	}
	return false
}

// RequireNoErrorOrSkip continues on nil, skips with exact capability evidence
// when the environment reports the operation unsupported, and fails otherwise.
func RequireNoErrorOrSkip(t *testing.T, capability Capability, operation string, err error) {
	t.Helper()
	if err == nil {
		return
	}
	if IsUnsupported(err) {
		provider, _ := resolveProvider()
		t.Skipf("provider %q lacks %s (%s): %v", provider, capability, operation, err)
	}
	t.Fatalf("operation %s failed: %v", operation, err)
}

// resolveProvider selects the provisioning backend. Only "floci" exists;
// anything else is a hard error so a misconfigured real-AWS run can never
// silently execute against the wrong environment.
func resolveProvider() (string, error) {
	provider := os.Getenv("AWSENV_PROVIDER")
	if provider == "" {
		return "floci", nil
	}
	if provider != "floci" {
		return "", fmt.Errorf("unsupported AWSENV_PROVIDER %q: only \"floci\" is implemented", provider)
	}
	return provider, nil
}
```

- [ ] **Step 3b: Write environment.go with provider, fixtures, and provisioner contract**

`test/awsenv/harness/environment.go`:
```go
//go:build e2e || awsenv || kind

package harness

import (
	"context"
	"testing"
)

// Environment is the provider-neutral test contract: SDK clients plus the
// provider identity. Executor tests must not branch on Provider; capability
// skips carry the provider evidence instead.
type Environment struct {
	Provider    string
	Endpoint    string
	Region      string
	Clients     *Clients
	Provisioner Provisioner
}

// EC2FixtureSpec describes instances to provision.
type EC2FixtureSpec struct {
	Name  string
	Tags  map[string]string
	Count int32
}

// EC2Fixture is a set of provisioned instances owned by one test.
type EC2Fixture struct {
	InstanceIDs []string
}

// RDSFixtureSpec describes a DB instance to provision.
type RDSFixtureSpec struct {
	Prefix string
}

// RDSFixture is a provisioned DB instance owned by one test.
type RDSFixture struct {
	InstanceID string
}

// EKSFixtureSpec describes a cluster plus node group to provision.
type EKSFixtureSpec struct {
	Prefix string
}

// EKSFixture is a provisioned cluster plus node group owned by one test.
type EKSFixture struct {
	ClusterName   string
	NodegroupName string
}

// Provisioner provisions AWS-shaped fixtures for executor tests.
// FlociProvisioner is the only implementation; a future real-AWS backend
// implements this same interface without touching executor tests.
type Provisioner interface {
	ProvisionEC2(t *testing.T, ctx context.Context, spec EC2FixtureSpec) EC2Fixture
	ProvisionRDS(t *testing.T, ctx context.Context, spec RDSFixtureSpec) RDSFixture
	ProvisionEKS(t *testing.T, ctx context.Context, spec EKSFixtureSpec) EKSFixture
}

// FlociProvisioner provisions fixtures through standard AWS SDK calls.
type FlociProvisioner struct {
	Clients *Clients
}

// ProvisionEC2 delegates to the proven SeedEC2 helper.
func (p *FlociProvisioner) ProvisionEC2(t *testing.T, ctx context.Context, spec EC2FixtureSpec) EC2Fixture {
	t.Helper()
	return EC2Fixture{InstanceIDs: SeedEC2(t, ctx, p.Clients, spec.Name, spec.Tags, spec.Count)}
}
```
(RDS/EKS methods on `FlociProvisioner` land in Tasks 3/4 alongside their fixture helpers; Task 4 adds `var _ Provisioner = (*FlociProvisioner)(nil)`. Note on the spec's `Capabilities` member: support is per-operation and discovered live against a valid fixture, so it is expressed as `Capability` constants plus `RequireNoErrorOrSkip`, not a static struct field — there is no safe value to precompute at `Setup` time.)

- [ ] **Step 4: Change Setup to return *Environment with RDS/EKS clients**

In `test/awsenv/harness/harness.go`: add imports `"github.com/aws/aws-sdk-go-v2/service/eks"` and `"github.com/aws/aws-sdk-go-v2/service/rds"`. Add `RDS *rds.Client` and `EKS *eks.Client` fields to `Clients`. Validate the provider first thing in `Setup` (after the `AWSENV_ENABLED` gate, before touching the environment):
```go
	provider, err := resolveProvider()
	if err != nil {
		t.Fatal(err.Error())
	}
```
and use `provider` in the returned `Environment` instead of the `"floci"` literal. Add:
```go
// Environment is the provider-neutral test contract: SDK clients plus the
// provider identity. Executor tests must not branch on Provider; capability
// skips carry the provider evidence instead.
type Environment struct {
	Provider    string
	Endpoint    string
	Region      string
	Clients     *Clients
	Provisioner Provisioner
}
```
Change `func Setup(t *testing.T) *Clients` to `func Setup(t *testing.T) *Environment`; construct `clients := &Clients{EC2: ..., RDS: rds.NewFromConfig(cfg), EKS: eks.NewFromConfig(cfg), STS: ..., Region: ..., Endpoint: ...}`; readiness probe uses `clients.STS`; return `&Environment{Provider: provider, Endpoint: endpoint, Region: defaultRegion, Clients: clients, Provisioner: &FlociProvisioner{Clients: clients}}` where `provider` comes from the `resolveProvider()` call above.

Update call sites to the new return shape (field access only, no logic change):
- `test/awsenv/ec2_lifecycle_test.go`: `c := harness.Setup(t)` stays, but `c.Region` → `c.Clients.Region`, `c.EC2` → `c.Clients.EC2` (occurrences: `ec2Spec(c.Region, ...)`, `c.EC2.StopInstances`, both `WaitForInstanceState(t, ctx, c.EC2, ...)`).
- `test/awsenv/harness/harness_test.go`: `c.Region` → `c.Clients.Region`.
- `test/kind/kind_test.go`: `hc.EC2` → `hc.Clients.EC2` (2 `WaitForInstanceState` calls). `hc.Region` if referenced — check and update identically.

- [ ] **Step 5: Run tests to verify green**

Run: `env -u GOROOT go vet -tags=awsenv ./test/awsenv/... && env -u GOROOT go vet -tags=kind ./test/kind/ && env -u GOROOT go test ./test/awsenv/harness/ -tags=awsenv -count=1 -v`
Expected: vet clean; `TestIsUnsupportedMatchesUnknownOperations` PASS; `TestRequireNoErrorOrSkipPassesThroughNil` PASS; `TestSetup_ConnectsToFloci` FAILS without Floci running (proves it still gates on live readiness) — start Floci only in Task 5 verification.

---

### Task 3: RDS fixture, lifecycle, snapshot, and validation tests

**Files:**
- Create: `test/awsenv/harness/provision_rds.go`
- Create: `test/awsenv/rds_lifecycle_test.go`
- Test: live run against Floci in Task 5

**Interfaces:**
- Consumes: `Environment.Clients.RDS`, `RequireNoErrorOrSkip`, `harness.Setup`, `rds.New()`, `executorparams.RDSParameters`, `rds.DBInstanceState`.
- Produces: `func (p *FlociProvisioner) ProvisionRDS(t, ctx, RDSFixtureSpec) RDSFixture`, `func WaitForDBInstanceState(t, ctx, client, id, want string, timeout)`.
- Scope note: RDS cluster lifecycle is deferred per the approved spec (separate fixture family, independently incomplete cluster-snapshot behavior) — no cluster task in this plan.

Verified executor contracts used here: restore key is `"instance:"+id`; state is `DBInstanceState{InstanceId, WasRunning, SnapshotId}`; `Validate` requires a selector and rejects `tags+instanceIds` mixes; `Shutdown` with `SnapshotBeforeStop` records `SnapshotId` before `StopDBInstance`; empty restore wakeup returns `"wakeup completed for RDS (no restore data)"`.

- [ ] **Step 1: Write the failing RDS lifecycle test**

`test/awsenv/rds_lifecycle_test.go`:
```go
//go:build awsenv

package awsenv

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"

	"github.com/ardikabs/hibernator/internal/executor"
	rdsexec "github.com/ardikabs/hibernator/internal/executor/rds"
	"github.com/ardikabs/hibernator/pkg/executorparams"
	"github.com/ardikabs/hibernator/test/awsenv/harness"
)

func rdsSpec(region, instanceID string, snapshotBefore bool, restoreTo map[string]json.RawMessage) executor.Spec {
	paramsJSON, err := json.Marshal(executorparams.RDSParameters{
		Selector:           executorparams.RDSSelector{InstanceIds: []string{instanceID}},
		SnapshotBeforeStop: snapshotBefore,
		AwaitCompletion:    executorparams.AwaitCompletion{Enabled: true, Timeout: "10m"},
	})
	if err != nil {
		panic(err)
	}
	return executor.Spec{
		TargetName: "awsenv-rds",
		TargetType: "rds",
		Parameters: paramsJSON,
		ConnectorConfig: executor.ConnectorConfig{
			AWS: harness.AWSConnectorConfig(region),
		},
		ReportStateCallback: func(key string, value interface{}) error {
			raw, err := json.Marshal(value)
			if err != nil {
				return err
			}
			restoreTo[key] = raw
			return nil
		},
	}
}

func TestRDSInstanceLifecycle(t *testing.T) {
	ctx := context.Background()
	env := harness.Setup(t)
	fixture := env.Provisioner.ProvisionRDS(t, ctx, harness.RDSFixtureSpec{Prefix: "awsenv-rds"})
	collected := map[string]json.RawMessage{}

	exec := rdsexec.New()
	spec := rdsSpec(env.Region, fixture.InstanceID, false, collected)
	require.NoError(t, exec.Validate(spec))

	_, err := exec.Shutdown(ctx, logr.Discard(), spec)
	harness.RequireNoErrorOrSkip(t, harness.CapabilityRDSLifecycle, "StopDBInstance", err)

	key := "instance:" + fixture.InstanceID
	require.Contains(t, collected, key, "restore data must be reported for the instance")
	var state rdsexec.DBInstanceState
	require.NoError(t, json.Unmarshal(collected[key], &state))
	require.Equal(t, fixture.InstanceID, state.InstanceId)
	require.True(t, state.WasRunning)

	harness.WaitForDBInstanceState(t, ctx, env.Clients.RDS, fixture.InstanceID, "stopped", 10*time.Minute)

	wakeupRes, err := exec.WakeUp(ctx, logr.Discard(), spec, executor.RestoreData{
		Type:   "rds",
		Data:   collected,
		IsLive: true,
	})
	require.NoError(t, err)
	require.Contains(t, wakeupRes.Message, "started")

	harness.WaitForDBInstanceState(t, ctx, env.Clients.RDS, fixture.InstanceID, "available", 10*time.Minute)
}

func TestRDSInstanceSnapshotLifecycle(t *testing.T) {
	ctx := context.Background()
	env := harness.Setup(t)
	fixture := env.Provisioner.ProvisionRDS(t, ctx, harness.RDSFixtureSpec{Prefix: "awsenv-rds-snap"})
	collected := map[string]json.RawMessage{}

	exec := rdsexec.New()
	spec := rdsSpec(env.Region, fixture.InstanceID, true, collected)
	require.NoError(t, exec.Validate(spec))

	_, err := exec.Shutdown(ctx, logr.Discard(), spec)
	harness.RequireNoErrorOrSkip(t, harness.CapabilityRDSLifecycle, "StopDBInstance", err)

	key := "instance:" + fixture.InstanceID
	var state rdsexec.DBInstanceState
	require.NoError(t, json.Unmarshal(collected[key], &state))
	require.NotEmpty(t, state.SnapshotId, "snapshotBeforeStop must record a snapshot id")

	out, err := env.Clients.RDS.DescribeDBSnapshots(ctx, &rds.DescribeDBSnapshotsInput{
		DBSnapshotIdentifier: aws.String(state.SnapshotId),
	})
	harness.RequireNoErrorOrSkip(t, harness.CapabilityRDSSnapshot, "DescribeDBSnapshots", err)
	require.Len(t, out.DBSnapshots, 1)

	wakeupRes, err := exec.WakeUp(ctx, logr.Discard(), spec, executor.RestoreData{
		Type:   "rds",
		Data:   collected,
		IsLive: true,
	})
	require.NoError(t, err)
	require.Contains(t, wakeupRes.Message, "started")
	harness.WaitForDBInstanceState(t, ctx, env.Clients.RDS, fixture.InstanceID, "available", 10*time.Minute)
}

func TestRDSValidateRejectsEmptySelector(t *testing.T) {
	exec := rdsexec.New()
	params, err := json.Marshal(executorparams.RDSParameters{})
	require.NoError(t, err)
	err = exec.Validate(executor.Spec{
		TargetType: "rds",
		Parameters: params,
		ConnectorConfig: executor.ConnectorConfig{
			AWS: harness.AWSConnectorConfig("us-east-1"),
		},
	})
	require.ErrorContains(t, err, "selector must specify at least one of")
}
```

Run: `env -u GOROOT go test ./test/awsenv/ -tags=awsenv -count=1 -run 'TestRDS' -v`
Expected: FAIL with `undefined: harness.ProvisionRDS` (proves the test runs and the fixture API is missing).

- [ ] **Step 2: Run test to verify it fails**

Run: same command as Step 1.
Expected: build failure naming `ProvisionRDS` (and then `WaitForDBInstanceState`). Do not proceed until you see it.

- [ ] **Step 3: Write minimal provision_rds.go**

`test/awsenv/harness/provision_rds.go`:
```go
//go:build e2e || awsenv || kind

package harness

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/rds"
)

// ProvisionRDS creates a uniquely named PostgreSQL instance, waits until it
// is available, and registers reverse-order deletion cleanup.
func (p *FlociProvisioner) ProvisionRDS(t *testing.T, ctx context.Context, spec RDSFixtureSpec) RDSFixture {
	t.Helper()
	id := fmt.Sprintf("%s-%d", spec.Prefix, time.Now().UnixNano())
	id = strings.ToLower(id)
	if len(id) > 63 {
		id = id[:63]
	}
	requestCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	_, err := p.Clients.RDS.CreateDBInstance(requestCtx, &rds.CreateDBInstanceInput{
		DBInstanceIdentifier: aws.String(id),
		DBInstanceClass:      aws.String("db.t3.micro"),
		Engine:               aws.String("postgres"),
		MasterUsername:       aws.String("admin"),
		MasterUserPassword:   aws.String("Secret123!"),
		AllocatedStorage:     aws.Int32(20),
	})
	RequireNoErrorOrSkip(t, CapabilityRDSLifecycle, "CreateDBInstance", err)

	WaitForDBInstanceState(t, ctx, p.Clients.RDS, id, "available", 10*time.Minute)

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cleanupCancel()
		_, err := p.Clients.RDS.DeleteDBInstance(cleanupCtx, &rds.DeleteDBInstanceInput{
			DBInstanceIdentifier: aws.String(id),
			SkipFinalSnapshot:    true,
		})
		if err != nil {
			t.Errorf("cleanup delete DB instance %s: %v", id, err)
			return
		}
		pollCtx, pollCancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer pollCancel()
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			_, err := p.Clients.RDS.DescribeDBInstances(pollCtx, &rds.DescribeDBInstancesInput{
				DBInstanceIdentifier: aws.String(id),
			})
			if err != nil {
				return
			}
			select {
			case <-pollCtx.Done():
				t.Errorf("cleanup: DB instance %s still present", id)
				return
			case <-ticker.C:
			}
		}
	})
	return RDSFixture{InstanceID: id}
}

// WaitForDBInstanceState polls DescribeDBInstances until the instance reports
// want, and fails with the last-seen status on timeout.
func WaitForDBInstanceState(t *testing.T, ctx context.Context, client *rds.Client, id, want string, timeout time.Duration) {
	t.Helper()
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	var last string
	var lastErr error
	for {
		requestCtx, requestCancel := context.WithTimeout(waitCtx, 20*time.Second)
		out, err := client.DescribeDBInstances(requestCtx, &rds.DescribeDBInstancesInput{
			DBInstanceIdentifier: aws.String(id),
		})
		requestCancel()
		if err == nil && len(out.DBInstances) > 0 {
			last = aws.ToString(out.DBInstances[0].DBInstanceStatus)
			if last == want {
				return
			}
		} else if err != nil {
			lastErr = err
		}
		select {
		case <-waitCtx.Done():
			t.Fatalf("DB instance %s did not reach %q within %s (last: %q, last error: %v)", id, want, timeout, last, lastErr)
		case <-ticker.C:
		}
	}
}
```

If Floci rejects `CreateDBInstance` minimal shape (missing VPC/subnets), adapt in this order and record the final shape in the test file comment: (a) add `Tags` with run nonce; (b) discover Floci subnet IDs via `EC2.DescribeSubnets` and pass `DBSubnetGroupName` only if Floci exposes one — do not invent subnet groups. If Floci requires fields incompatible with real AWS, stop and report instead of encoding Floci-only shapes into the neutral helper.

- [ ] **Step 4: Run RDS tests to verify they pass or skip with evidence**

Run with Floci up: `env -u GOROOT AWSENV_ENABLED=1 go test ./test/awsenv/ -tags=awsenv -count=1 -run 'TestRDS' -v`
Expected: PASS (lifecycle + snapshot + validation), or SKIP with `lacks rds.instance-lifecycle (StopDBInstance): <exact Floci error>`. A SKIP still requires provision + cleanup to have passed. Any other failure is a bug, not a skip.

---

### Task 4: EKS fixture, lifecycle, and validation tests

**Files:**
- Create: `test/awsenv/harness/provision_eks.go`
- Create: `test/awsenv/eks_lifecycle_test.go`
- Test: live run against Floci in Task 5

**Interfaces:**
- Consumes: `Environment.Clients.EKS`, `RequireNoErrorOrSkip`, `eks.New()`, `executorparams.EKSParameters`, `eks.NodeGroupState`.
- Produces: `type EKSFixture struct { ClusterName string; NodegroupName string }`, `func ProvisionEKS(t, ctx, env, prefix) EKSFixture`, `func WaitForNodegroupState(t, ctx, client, cluster, ng string, wantMin, wantDesired, wantMax int32, timeout)`, `func DeleteEKSFixture(t, ctx, client, fixture)`.

Verified executor contracts used here: `Shutdown` calls `DescribeCluster` (requires endpoint + CA), `ListNodegroups`/`DescribeNodegroup`, then `UpdateNodegroupConfig{MinSize:0, DesiredSize:0, MaxSize:keep}` per group; restore key is the node-group name; state is `NodeGroupState{DesiredSize, MinSize, MaxSize, WasScaled}`; shutdown message contains `"scaled 1 node group(s) to zero"`; empty-restore wakeup returns `"wakeup completed for EKS (no restore data)"`; `Validate` requires AWS config, region, and `clusterName`.

- [ ] **Step 1: Write the failing EKS lifecycle test**

`test/awsenv/eks_lifecycle_test.go`:
```go
//go:build awsenv

package awsenv

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"

	"github.com/ardikabs/hibernator/internal/executor"
	eksexec "github.com/ardikabs/hibernator/internal/executor/eks"
	"github.com/ardikabs/hibernator/pkg/executorparams"
	"github.com/ardikabs/hibernator/test/awsenv/harness"
)

func eksSpec(region, cluster, nodegroup string, restoreTo map[string]json.RawMessage) executor.Spec {
	paramsJSON, err := json.Marshal(executorparams.EKSParameters{
		ClusterName:       cluster,
		NodeGroups:        []executorparams.EKSNodeGroup{{Name: nodegroup}},
		AwaitCompletion:   executorparams.AwaitCompletion{Enabled: false},
	})
	if err != nil {
		panic(err)
	}
	return executor.Spec{
		TargetName: "awsenv-eks",
		TargetType: "eks",
		Parameters: paramsJSON,
		ConnectorConfig: executor.ConnectorConfig{
			AWS: harness.AWSConnectorConfig(region),
		},
		ReportStateCallback: func(key string, value interface{}) error {
			raw, err := json.Marshal(value)
			if err != nil {
				return err
			}
			restoreTo[key] = raw
			return nil
		},
	}
}

func TestEKSNodeGroupLifecycle(t *testing.T) {
	ctx := context.Background()
	env := harness.Setup(t)
	fixture := env.Provisioner.ProvisionEKS(t, ctx, harness.EKSFixtureSpec{Prefix: "awsenv-eks"})
	collected := map[string]json.RawMessage{}

	exec := eksexec.New()
	spec := eksSpec(env.Region, fixture.ClusterName, fixture.NodegroupName, collected)
	require.NoError(t, exec.Validate(spec))

	_, err := exec.Shutdown(ctx, logr.Discard(), spec)
	harness.RequireNoErrorOrSkip(t, harness.CapabilityEKSNodeGroupUpdate, "UpdateNodegroupConfig", err)

	var state eksexec.NodeGroupState
	require.NoError(t, json.Unmarshal(collected[fixture.NodegroupName], &state))
	require.Equal(t, int32(1), state.MinSize)
	require.Equal(t, int32(1), state.DesiredSize)
	require.Equal(t, int32(2), state.MaxSize)
	require.True(t, state.WasScaled)

	harness.WaitForNodegroupState(t, ctx, env.Clients.EKS, fixture.ClusterName, fixture.NodegroupName, 0, 0, 2, 5*time.Minute)

	wakeupRes, err := exec.WakeUp(ctx, logr.Discard(), spec, executor.RestoreData{
		Type:   "eks",
		Data:   collected,
		IsLive: true,
	})
	require.NoError(t, err)
	require.Contains(t, wakeupRes.Message, "restored 1 node group(s)")

	harness.WaitForNodegroupState(t, ctx, env.Clients.EKS, fixture.ClusterName, fixture.NodegroupName, 1, 1, 2, 5*time.Minute)
}

func TestEKSValidateRejectsMissingClusterName(t *testing.T) {
	exec := eksexec.New()
	params, err := json.Marshal(executorparams.EKSParameters{})
	require.NoError(t, err)
	err = exec.Validate(executor.Spec{
		TargetType: "eks",
		Parameters: params,
		ConnectorConfig: executor.ConnectorConfig{
			AWS: harness.AWSConnectorConfig("us-east-1"),
		},
	})
	require.ErrorContains(t, err, "clusterName is required")
}
```

Run: `env -u GOROOT go test ./test/awsenv/ -tags=awsenv -count=1 -run 'TestEKS' -v`
Expected: FAIL with `undefined: harness.ProvisionEKS` (proves the test runs and the fixture API is missing).

- [ ] **Step 2: Run test to verify it fails**

Run: same command as Step 1.
Expected: build failure naming `ProvisionEKS` (and then `WaitForNodegroupState`). Do not proceed until you see it.

- [ ] **Step 3: Write minimal provision_eks.go**

`test/awsenv/harness/provision_eks.go`:
```go
//go:build e2e || awsenv || kind

package harness

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
)

// ProvisionEKS creates a cluster and one node group ({min:1, desired:1, max:2}),
// waits until both are active, and registers reverse-order deletion cleanup.
func (p *FlociProvisioner) ProvisionEKS(t *testing.T, ctx context.Context, spec EKSFixtureSpec) EKSFixture {
	t.Helper()
	stamp := time.Now().UnixNano()
	cluster := fmt.Sprintf("%s-%d", spec.Prefix, stamp)
	if len(cluster) > 63 {
		cluster = cluster[:63]
	}
	ng := fmt.Sprintf("ng-%d", stamp)
	if len(ng) > 63 {
		ng = ng[:63]
	}

	requestCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	_, err := p.Clients.EKS.CreateCluster(requestCtx, &eks.CreateClusterInput{
		Name:    aws.String(cluster),
		RoleArn: aws.String("arn:aws:iam::000000000000:role/awsenv-test"),
	})
	RequireNoErrorOrSkip(t, CapabilityEKSNodeGroupUpdate, "CreateCluster", err)

	waitCtx, waitCancel := context.WithTimeout(ctx, 5*time.Minute)
	defer waitCancel()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
waitCluster:
	for {
		out, err := p.Clients.EKS.DescribeCluster(waitCtx, &eks.DescribeClusterInput{
			Name: aws.String(cluster),
		})
		if err == nil && out.Cluster != nil && out.Cluster.Status == ekstypes.ClusterStatusActive {
			break waitCluster
		}
		select {
		case <-waitCtx.Done():
			t.Fatalf("EKS cluster %s not active: %v", cluster, err)
		case <-ticker.C:
		}
	}

	// Capability probe that doubles as endpoint validation: the Hibernator EKS
	// executor always builds a Kubernetes client from DescribeCluster, so a
	// fixture without endpoint+CA cannot run the lifecycle.
	clusterOut, err := p.Clients.EKS.DescribeCluster(ctx, &eks.DescribeClusterInput{
		Name: aws.String(cluster),
	})
	require.NoError(t, err)
	if aws.ToString(clusterOut.Cluster.Endpoint) == "" || aws.ToString(clusterOut.Cluster.CertificateAuthority.Data) == "" {
		t.Skipf("provider %q lacks %s (DescribeCluster returned no usable endpoint/CA)", "floci", CapabilityEKSNodeGroupUpdate)
	}

	nodeCtx, nodeCancel := context.WithTimeout(ctx, 60*time.Second)
	defer nodeCancel()
	_, err = p.Clients.EKS.CreateNodegroup(nodeCtx, &eks.CreateNodegroupInput{
		ClusterName:   aws.String(cluster),
		NodegroupName: aws.String(ng),
		NodeRole:      aws.String("arn:aws:iam::000000000000:role/awsenv-node"),
		ScalingConfig: &ekstypes.NodegroupScalingConfig{
			MinSize:     aws.Int32(1),
			MaxSize:     aws.Int32(2),
			DesiredSize: aws.Int32(1),
		},
	})
	RequireNoErrorOrSkip(t, CapabilityEKSNodeGroupUpdate, "CreateNodegroup", err)

	WaitForNodegroupState(t, ctx, p.Clients.EKS, cluster, ng, 1, 1, 2, 5*time.Minute)

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cleanupCancel()
		_, err := p.Clients.EKS.DeleteNodegroup(cleanupCtx, &eks.DeleteNodegroupInput{
			ClusterName:   aws.String(cluster),
			NodegroupName: aws.String(ng),
		})
		if err != nil {
			t.Errorf("cleanup delete node group %s/%s: %v", cluster, ng, err)
			return
		}
		WaitForNodegroupGone(t, cleanupCtx, p.Clients.EKS, cluster, ng, 3*time.Minute)
		_, err = p.Clients.EKS.DeleteCluster(cleanupCtx, &eks.DeleteClusterInput{
			Name: aws.String(cluster),
		})
		if err != nil {
			t.Errorf("cleanup delete cluster %s: %v", cluster, err)
		}
	})
	return EKSFixture{ClusterName: cluster, NodegroupName: ng}
}

// WaitForNodegroupState polls DescribeNodegroup until scaling matches.
func WaitForNodegroupState(t *testing.T, ctx context.Context, client *eks.Client, cluster, ng string, wantMin, wantDesired, wantMax int32, timeout time.Duration) {
	t.Helper()
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	var last string
	var lastErr error
	for {
		requestCtx, requestCancel := context.WithTimeout(waitCtx, 20*time.Second)
		out, err := client.DescribeNodegroup(requestCtx, &eks.DescribeNodegroupInput{
			ClusterName:   aws.String(cluster),
			NodegroupName: aws.String(ng),
		})
		requestCancel()
		if err == nil && out.Nodegroup != nil && out.Nodegroup.ScalingConfig != nil {
			got := out.Nodegroup.ScalingConfig
			last = fmt.Sprintf("min=%d,desired=%d,max=%d,status=%s", aws.ToInt32(got.MinSize), aws.ToInt32(got.DesiredSize), aws.ToInt32(got.MaxSize), out.Nodegroup.Status)
			if aws.ToInt32(got.MinSize) == wantMin && aws.ToInt32(got.DesiredSize) == wantDesired && aws.ToInt32(got.MaxSize) == wantMax {
				return
			}
		} else if err != nil {
			lastErr = err
		}
		select {
		case <-waitCtx.Done():
			t.Fatalf("node group %s/%s did not reach min=%d,desired=%d,max=%d within %s (last: %s, last error: %v)", cluster, ng, wantMin, wantDesired, wantMax, timeout, last, lastErr)
		case <-ticker.C:
		}
	}
}

// FlociProvisioner satisfies Provisioner once all three methods exist.
var _ Provisioner = (*FlociProvisioner)(nil)

// WaitForNodegroupGone polls until DescribeNodegroup reports not-found.
func WaitForNodegroupGone(t *testing.T, ctx context.Context, client *eks.Client, cluster, ng string, timeout time.Duration) {
	t.Helper()
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		requestCtx, requestCancel := context.WithTimeout(waitCtx, 20*time.Second)
		_, err := client.DescribeNodegroup(requestCtx, &eks.DescribeNodegroupInput{
			ClusterName:   aws.String(cluster),
			NodegroupName: aws.String(ng),
		})
		requestCancel()
		if err != nil {
			return
		}
		select {
		case <-waitCtx.Done():
			t.Errorf("node group %s/%s still present after %s", cluster, ng, timeout)
			return
		case <-ticker.C:
		}
	}
}
```

If Floci rejects minimal `CreateCluster` (missing VPC config), adapt in this order and record the final shape in a file comment: (a) add `ResourcesVpcConfig` with subnet IDs from `EC2.DescribeSubnets` default VPC; (b) add `Tags` with the run nonce. Do not encode Floci-only fields that real AWS would reject. If `CreateNodegroup` requires subnets, pass the same discovered subnet IDs.

- [ ] **Step 4: Run EKS tests to verify they pass or skip with evidence**

Run with Floci up: `env -u GOROOT AWSENV_ENABLED=1 go test ./test/awsenv/ -tags=awsenv -count=1 -run 'TestEKS' -v`
Expected: PASS, or SKIP naming the exact missing capability (`CreateCluster`, `CreateNodegroup`, endpoint/CA, or `UpdateNodegroupConfig`) with the Floci error text. Provision + cleanup must have passed in either case.

---

### Task 5: kind wiring, docs, and full verification

**Files:**
- Modify: `test/kind/kind_test.go`, `test/kind/README.md`, `test/awsenv/README.md`, `Makefile` (already done in Task 1)
- Test: live `make test-awsenv`, live `make test-kind`, regression unit tests

**Interfaces:**
- Consumes: all Tasks 1-4 outputs.
- Produces: `make test-kind` green; zero old-name references outside historical docs.

- [ ] **Step 1: Rewrite the awsenv README**

`test/awsenv/README.md`:
```markdown
# AWS Environment Executor Integration Suite

Executor integration tests against an AWS-shaped environment (Floci primary,
real AWS later via the same provisioner contract). Tests provision fixtures,
invoke the real executors, and assert resource plus restore-state transitions.

## Layout

```bash
test/awsenv/
├── environments/floci/compose.yml  # Pinned Floci runtime asset
├── README.md
├── ec2_lifecycle_test.go           # EC2 mixed-state lifecycle + noop + validation
├── rds_lifecycle_test.go           # RDS instance + snapshot lifecycles + validation
├── eks_lifecycle_test.go           # EKS node-group lifecycle + validation
└── harness/
    ├── harness.go                  # Setup, Environment, clients, EC2 seeding
    ├── capabilities.go             # Capability constants + unsupported classifier
    ├── provision_rds.go            # RDS fixture + wait + delete
    └── provision_eks.go            # EKS fixture + wait + delete
```

## Capability policy

Service presence is not enough: each lifecycle test probes the exact APIs
Hibernator calls. An explicit unsupported-operation response skips that test
with provider, capability, operation, and API error. Provisioning, auth,
network, state, validation, timeout, and server errors fail instead of
skipping.

## Running

```bash
docker compose -f test/awsenv/environments/floci/compose.yml up -d
make test-awsenv   # or: AWSENV_ENABLED=1 go test ./test/awsenv/... -tags=awsenv -count=1 -v
docker compose -f test/awsenv/environments/floci/compose.yml down
```

`AWSENV_PROVIDER` selects the backend (only `floci` today; unknown values fail
setup). `AWSENV_ENDPOINT_URL` overrides the endpoint (default
`http://localhost:4566`). The `awsenv` build tag is explicit opt-in: running a
tagged test without `AWSENV_ENABLED=1` fails. Default `go test ./...` is
unaffected.
```

- [ ] **Step 2: Verify kind still compiles and its messages are accurate**

Run: `env -u GOROOT go vet -tags=kind ./test/kind/`
Expected: clean. Confirm `test/kind/README.md` references `test/awsenv/environments/floci/compose.yml` and `AWSENV_ENABLED=1` (updated in Task 1 Step 5).

- [ ] **Step 3: Run the full awsenv suite live**

Run with Floci up: `docker compose -f test/awsenv/environments/floci/compose.yml up -d` then `env -u GOROOT make test-awsenv`
Expected: EC2 PASS; RDS PASS-or-documented-SKIP; EKS PASS-or-documented-SKIP; validation tests PASS. Record the actual outcome (pass vs skip with Floci error text) in the final summary — a skip is an accepted outcome per the approved policy, but the provisioning and cleanup around it must be green.

Then prove no fixtures leaked (Floci is in-memory; every provision must have been deleted):
```bash
export AWS_ENDPOINT_URL=http://localhost:4566 AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1
aws ec2 describe-instances --query 'Reservations[].Instances[?starts_with(State.Name, `t`) == `false`].[InstanceId,State.Name]' --output text | grep -i awsenv && echo "LEAKED EC2" || echo "no leaked EC2"
aws rds describe-db-instances --query 'DBInstances[?starts_with(DBInstanceIdentifier, `awsenv`)].DBInstanceIdentifier' --output text
aws eks list-clusters --query 'clusters[?starts_with(@, `awsenv`)]' --output text
```
Expected: no output lines naming `awsenv-*` resources (empty results). Any hit means cleanup failed — fix before proceeding.

- [ ] **Step 4: Run kind full-chain and regression**

Run: `env -u GOROOT make test-kind`
Expected: PASS (EC2 smoke unaffected by the harness rename; proves kind consumes the new import path).

Run: `env -u GOROOT go build ./...` and `env -u GOROOT go test ./pkg/awsutil/... ./pkg/executorparams/... ./internal/executor/ec2/... ./internal/executor/rds/... ./internal/executor/eks/... ./internal/restore/... -count=1`
Expected: all PASS (contract packages untouched, but they are the surfaces the suite relies on).

Run: `git grep -nE 'test/floci|test-floci|FLOCI_ENABLED|FLOCI_ENDPOINT|tags=floci' -- ':!docs/superpowers/**'`
Expected: no output.

- [ ] **Step 5: Teardown**

Run: `docker compose -f test/awsenv/environments/floci/compose.yml down`
Expected: Floci container and network removed. Leave the working tree uncommitted for user review (repo policy: no auto-commit).

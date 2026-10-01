# ScheduleException CLI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add typed `kubectl hibernator exception create|list|status|delete` commands with plan selectors, safe bulk operations, natural-language validity dates, and console/JSON output.

**Architecture:** A focused `cli/exception` package owns Cobra wiring, validation, Kubernetes selection, resource construction, and derived status data. Existing typed controller-runtime clients and the shared printer dispatcher remain the I/O boundaries; helper functions isolate deterministic parsing and naming behavior for unit tests.

**Tech Stack:** Go, Cobra, controller-runtime typed client/fake client, Kubernetes labels and validation packages, existing `timeparse`, existing console/JSON printer framework, `testing` and `testify`.

---

## File Structure

- Create `cmd/kubectl-hibernator/cli/exception/command.go`: parent command registration.
- Create `cmd/kubectl-hibernator/cli/exception/helpers.go`: selectors, normalization, name generation, and shared resource queries.
- Create `cmd/kubectl-hibernator/cli/exception/helpers_test.go`: pure helper and fake-client selection tests.
- Create `cmd/kubectl-hibernator/cli/exception/create.go`: create flags, validation, resource construction, and bulk result handling.
- Create `cmd/kubectl-hibernator/cli/exception/create_test.go`: create validation and persistence tests.
- Create `cmd/kubectl-hibernator/cli/exception/list.go`: compact filtered list command.
- Create `cmd/kubectl-hibernator/cli/exception/list_test.go`: list filtering tests.
- Create `cmd/kubectl-hibernator/cli/exception/status.go`: operational status filtering and plan enrichment.
- Create `cmd/kubectl-hibernator/cli/exception/status_test.go`: status filtering and detached-plan tests.
- Create `cmd/kubectl-hibernator/cli/exception/delete.go`: safe exact/filtered deletion and confirmation.
- Create `cmd/kubectl-hibernator/cli/exception/delete_test.go`: confirmation and deletion tests.
- Modify `cmd/kubectl-hibernator/printers/types.go`: exception output DTOs.
- Modify `cmd/kubectl-hibernator/printers/console_printer.go`: exception tables and detailed status output.
- Modify `cmd/kubectl-hibernator/printers/json_printer.go`: stable JSON conversion for exception outputs.
- Create `cmd/kubectl-hibernator/printers/exception_printer_test.go`: console and JSON output tests.
- Modify `cmd/kubectl-hibernator/cli/root.go`: register and document the command group.
- Modify `cmd/kubectl-hibernator/cli/root_test.go` if present, otherwise create it: root registration test.

### Task 1: Shared Parsing, Naming, And Selection

**Files:**
- Create: `cmd/kubectl-hibernator/cli/exception/helpers.go`
- Create: `cmd/kubectl-hibernator/cli/exception/helpers_test.go`

- [ ] **Step 1: Write failing normalization and naming tests**

Cover all-days default, abbreviated/full day normalization to the API's `MON` through `SUN` values, invalid days, strict `HH:MM`, state normalization, DNS prefix rejection, vowel-first compaction, 63-character output, and eight-character lowercase alphanumeric suffixes. Inject suffix generation so tests are deterministic:

```go
func TestBuildExceptionNameCompactsLongPrefix(t *testing.T) {
	name, err := buildExceptionName(strings.Repeat("maintenance", 7), func(int) (string, error) {
		return "a1b2c3d4", nil
	})
	require.NoError(t, err)
	assert.LessOrEqual(t, len(name), 63)
	assert.True(t, strings.HasSuffix(name, "-a1b2c3d4"))
}

func TestParseDaysDefaultsToAllDays(t *testing.T) {
	days, err := parseDays("")
	require.NoError(t, err)
	assert.Equal(t, allDays, days)
}
```

- [ ] **Step 2: Run helper tests and verify they fail**

Run: `go test ./cmd/kubectl-hibernator/cli/exception -run 'Test(BuildExceptionName|ParseDays|ValidateWindow|ParseStates)' -count=1`

Expected: FAIL because the package/helpers do not exist.

- [ ] **Step 3: Implement deterministic helpers**

Define these core signatures and constants in `helpers.go`:

```go
const generatedSuffixLength = 8

var allDays = []string{
	"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN",
}

type suffixGenerator func(int) (string, error)

func parseDays(value string) ([]string, error)
func validateWindow(start, end string) error
func parseStates(value string) (map[hibernatorv1alpha1.ExceptionState]struct{}, error)
func compactPrefix(prefix string, max int) (string, error)
func buildExceptionName(prefix string, generate suffixGenerator) (string, error)
func randomAlphanumeric(length int) (string, error)
```

Use `validation.IsDNS1123Subdomain` before compaction, remove vowels from right to left while over 54 characters, then truncate and trim `-`/`.` from the right. Use `crypto/rand` with alphabet `abcdefghijklmnopqrstuvwxyz0123456789` for suffixes. Validate windows with `time.Parse("15:04", value)` and reject equal start/end values.

- [ ] **Step 4: Write failing selector tests**

Use fake plans with labels such as `env=prod`, `tier=api`, and `region=apac`. Verify `env=prod`, `tier in (api,worker)`, `region`, and `!deprecated` selectors; verify `--plan` exact selection and zero-match errors. Verify copied labels include selector requirement keys but overwrite `wellknown.LabelPlan` with the selected plan name.

- [ ] **Step 5: Implement typed selection helpers**

Add:

```go
func selectPlans(ctx context.Context, c client.Client, namespace, planName, selectorText string) ([]hibernatorv1alpha1.HibernatePlan, labels.Selector, error)
func selectExceptions(ctx context.Context, c client.Client, namespace, selectorText, planName string) ([]hibernatorv1alpha1.ScheduleException, error)
func selectorLabels(planLabels map[string]string, selector labels.Selector) map[string]string
```

Require exactly one of `planName` and `selectorText` in `selectPlans`. Parse selectors with `labels.Parse`; pass `client.MatchingLabelsSelector{Selector: selector}` to `List`. `selectExceptions` applies the Kubernetes selector server-side and the exact `spec.planRef.name` filter in memory because `planRef` is not a metadata label contract. Sort returned objects by namespace then name for stable output.

- [ ] **Step 6: Run helper package tests**

Run: `go test ./cmd/kubectl-hibernator/cli/exception -run 'Test(BuildExceptionName|ParseDays|ValidateWindow|ParseStates|Select|SelectorLabels)' -count=1`

Expected: PASS.

### Task 2: Exception Output Types And Printers

**Files:**
- Modify: `cmd/kubectl-hibernator/printers/types.go`
- Modify: `cmd/kubectl-hibernator/printers/console_printer.go`
- Modify: `cmd/kubectl-hibernator/printers/json_printer.go`
- Create: `cmd/kubectl-hibernator/printers/exception_printer_test.go`

- [ ] **Step 1: Write failing printer tests**

Test compact list headers/rows, all-namespace inclusion, status fields, operation result rows, and JSON field names. Construct fixed timestamps so assertions are stable. The expected compact header is:

```text
Name  Namespace  Plan  Type  State  Valid From  Valid Until  Age
```

- [ ] **Step 2: Run printer tests and verify they fail**

Run: `go test ./cmd/kubectl-hibernator/printers -run Exception -count=1`

Expected: FAIL because exception output DTOs and printer cases are missing.

- [ ] **Step 3: Add output DTOs**

Add focused structures to `types.go`:

```go
type ExceptionListOutput struct {
	Items         []hibernatorv1alpha1.ScheduleException `json:"items"`
	AllNamespaces bool                                   `json:"-"`
}

type ExceptionStatusItem struct {
	Exception  hibernatorv1alpha1.ScheduleException `json:"exception"`
	PlanPhase  string                                `json:"planPhase"`
	Timing     string                                `json:"timing"`
	PlanExists bool                                  `json:"planExists"`
}

type ExceptionStatusOutput struct {
	Items []ExceptionStatusItem `json:"items"`
}

type ExceptionOperationResult struct {
	Plan, Name, Result, Message string
}

type ExceptionOperationOutput struct {
	Action string
	Items  []ExceptionOperationResult
}
```

- [ ] **Step 4: Register and implement console printers**

Add dispatcher switch cases for list, status, and operation outputs. Use `newTextWriter`, `formatLocalTime`, and `FormatAge`. Compact list prints the approved columns. Status prints one readable detail block per item including plan phase, validity/timing, windows/days, lifecycle timestamps, and message. Operation output prints `PLAN`, `EXCEPTION`, `RESULT`, `MESSAGE`.

- [ ] **Step 5: Implement stable JSON conversion**

Add JSON cases and explicit DTO conversion rather than exposing printer-only fields. Preserve RFC3339 UTC values for validity/lifecycle timestamps, the typed window list, plan phase/existence, timing, and operation results.

- [ ] **Step 6: Run printer tests**

Run: `go test ./cmd/kubectl-hibernator/printers -run Exception -count=1`

Expected: PASS.

### Task 3: Create Subcommand

**Files:**
- Create: `cmd/kubectl-hibernator/cli/exception/command.go`
- Create: `cmd/kubectl-hibernator/cli/exception/create.go`
- Create: `cmd/kubectl-hibernator/cli/exception/create_test.go`

- [ ] **Step 1: Write failing Cobra validation tests**

Verify the parent exposes `create`; required type/from/until/window flags are enforced; exactly one of `--plan` and `-l` is required; invalid exception types, intervals, days, and windows fail before any create call.

- [ ] **Step 2: Write failing persistence tests**

Use `common.ClientFactory` with a fake client and fixed `now`/suffix dependencies. Assert exact-plan creation and multi-plan selector creation persist one typed object per plan with UTC validity, one window, canonical days, copied selector labels, `wellknown.LabelPlan`, and generated names. Add a client wrapper whose `Create` fails for one plan and assert other objects remain, all plans were attempted, output identifies the failed plan, and the final error recommends `--plan`.

- [ ] **Step 3: Run create tests and verify they fail**

Run: `go test ./cmd/kubectl-hibernator/cli/exception -run 'Test(Create|RunCreate)' -count=1`

Expected: FAIL because command constructors and execution are absent.

- [ ] **Step 4: Implement parent and create option wiring**

Use:

```go
type createOptions struct {
	root *common.RootOptions
	plan, selector, exceptionType, from, until string
	windowStart, windowEnd, days string
	now func() time.Time
	generateSuffix suffixGenerator
}
```

`NewCommand` creates `exception` and adds all four child commands. `newCreateCommand` uses `cobra.ExactArgs(1)`, marks the five required content flags, and defines `--plan` plus `--selector/-l` without using Cobra mutually-exclusive annotations so error text remains controlled and testable.

- [ ] **Step 5: Implement resource construction and bulk create**

Parse dates through `timeparse.ParseDeadline(value, now)` and call `.UTC()` before wrapping with `metav1.NewTime`. Construct:

```go
hibernatorv1alpha1.ScheduleException{
	ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: plan.Namespace, Labels: copiedLabels},
	Spec: hibernatorv1alpha1.ScheduleExceptionSpec{
		PlanRef: hibernatorv1alpha1.PlanReference{Name: plan.Name},
		ValidFrom: metav1.NewTime(validFrom.UTC()), ValidUntil: metav1.NewTime(validUntil.UTC()),
		Type: hibernatorv1alpha1.ExceptionType(exceptionType),
		Windows: []hibernatorv1alpha1.OffHourWindow{{Start: start, End: end, DaysOfWeek: days}},
	},
}
```

Retry only `apierrors.IsAlreadyExists` name collisions with a bounded three-attempt loop. Collect a result per plan and print once through `printers.Dispatcher`. If failures exist, return a summary error after printing successful and failed rows.

- [ ] **Step 6: Run create tests**

Run: `go test ./cmd/kubectl-hibernator/cli/exception -run 'Test(Create|RunCreate)' -count=1`

Expected: PASS.

### Task 4: List And Status Subcommands

**Files:**
- Create: `cmd/kubectl-hibernator/cli/exception/list.go` (includes `--type` filtering)
- Create: `cmd/kubectl-hibernator/cli/exception/list_test.go`
- Create: `cmd/kubectl-hibernator/cli/exception/describe.go` (renamed from `status.go`)
- Create: `cmd/kubectl-hibernator/cli/exception/describe_test.go`

- [ ] **Step 1: Write failing list tests**

Seed exceptions across namespaces/plans/states. Verify `list`/`ls`, namespace default, `-A`, match-expression `-l`, `--plan`, combined AND filtering, stable sorting, and JSON dispatch.

- [ ] **Step 2: Implement list command**

Define `listOptions{root, allNamespaces, selector, plan string}`. Resolve namespace to `""` under `-A`, call `selectExceptions`, and print `ExceptionListOutput`. Parse selectors before creating the client so malformed selectors fail quickly.

- [ ] **Step 3: Write failing status tests**

Verify zero args lists all in scope; exact names preserve requested scope and cannot mix with filters; state values are case-insensitive and comma-separated; state/plan/label filters combine correctly; timing says starts-in, remaining, or expired; missing plans produce `PlanExists=false` without aborting other items.

- [ ] **Step 4: Implement status collection and enrichment**

Define:

```go
type statusOptions struct {
	root *common.RootOptions
	allNamespaces bool
	selector, plan, states string
	now func() time.Time
}
```

For names, fetch each `ScheduleException` by `types.NamespacedName`. For collection mode, use `selectExceptions`, then filter normalized states. Cache referenced plans by namespaced name to avoid duplicate gets. Treat `apierrors.IsNotFound` as unavailable plan metadata; return other API errors. Derive timing against `now()` without changing resource state.

- [ ] **Step 5: Run list/status tests**

Run: `go test ./cmd/kubectl-hibernator/cli/exception -run 'Test(RunList|ListCommand|RunStatus|StatusCommand)' -count=1`

Expected: PASS.

### Task 5: Delete Subcommand

**Files:**
- Create: `cmd/kubectl-hibernator/cli/exception/delete.go`
- Create: `cmd/kubectl-hibernator/cli/exception/delete_test.go`

- [ ] **Step 1: Write failing safety and confirmation tests**

Verify exact names, selector mode, plan mode, combined selector/plan AND behavior, name/filter mutual exclusion, refusal of unqualified collection deletion, zero-match errors, default-no on empty/unrecognized input, case-insensitive `y`/`yes`, and `--yes` bypass.

- [ ] **Step 2: Write failing partial-delete test**

Wrap the fake client so one `Delete` returns a sentinel error. Assert all matched resources are attempted, successful deletes remain deleted, output includes every result, and the command returns non-zero.

- [ ] **Step 3: Run delete tests and verify they fail**

Run: `go test ./cmd/kubectl-hibernator/cli/exception -run 'Test(Delete|RunDelete|Confirm)' -count=1`

Expected: FAIL because delete is not implemented.

- [ ] **Step 4: Implement safe delete flow**

Define `deleteOptions{root, selector, plan string; yes bool; in io.Reader}`. Resolve exact names with typed `Get`, or require at least one filter and call `selectExceptions`. Print matched names before prompting. Read one line with `bufio.Scanner`; only `strings.EqualFold(trimmed, "y")` or `"yes"` proceeds. Attempt all deletes, collect `ExceptionOperationOutput`, and return a summary error after printing on any failure.

- [ ] **Step 5: Run delete tests**

Run: `go test ./cmd/kubectl-hibernator/cli/exception -run 'Test(Delete|RunDelete|Confirm)' -count=1`

Expected: PASS.

### Task 6: Root Registration And Focused Verification

**Files:**
- Modify: `cmd/kubectl-hibernator/cli/root.go`
- Modify or create: `cmd/kubectl-hibernator/cli/root_test.go`
- Modify as needed: files from Tasks 1-5 for defects found by integrated tests

- [ ] **Step 1: Write failing root registration test**

Construct `NewRootCommand`, find `exception`, and assert child commands `create`, `list` with alias `ls`, `status`, and `delete` exist. Verify root help mentions ScheduleException management.

- [ ] **Step 2: Run root test and verify it fails**

Run: `go test ./cmd/kubectl-hibernator/cli -run Exception -count=1`

Expected: FAIL because the root does not register `exception`.

- [ ] **Step 3: Register and document the command**

Import `cli/exception`, add `cmd.AddCommand(exception.NewCommand(opts))`, update root long help, and include representative create/list/status/delete examples without changing existing commands.

- [ ] **Step 4: Format changed Go files**

Run: `gofmt -w cmd/kubectl-hibernator/cli/exception cmd/kubectl-hibernator/cli/root.go cmd/kubectl-hibernator/cli/root_test.go cmd/kubectl-hibernator/printers/types.go cmd/kubectl-hibernator/printers/console_printer.go cmd/kubectl-hibernator/printers/json_printer.go cmd/kubectl-hibernator/printers/exception_printer_test.go`

Expected: command exits 0 and only formats the listed source files.

- [ ] **Step 5: Run focused unit tests**

Run: `go test ./cmd/kubectl-hibernator/cli/exception ./cmd/kubectl-hibernator/printers ./cmd/kubectl-hibernator/cli -count=1`

Expected: PASS.

- [ ] **Step 6: Run CLI package regression tests**

Run: `go test ./cmd/kubectl-hibernator/... -count=1`

Expected: PASS. Do not run `test/e2e/...` without explicit user approval.

- [ ] **Step 7: Build the CLI to the required output directory**

Run: `go build -o bin/kubectl-hibernator ./cmd/kubectl-hibernator`

Expected: command exits 0 and writes only the CLI binary under `bin/`. Do not inspect or read the binary.

- [ ] **Step 8: Inspect final worktree changes**

Run: `git status --short && git diff --check`

Expected: only intended source, test, design, and plan files are changed; `git diff --check` reports no whitespace errors. Do not commit without an explicit user request.

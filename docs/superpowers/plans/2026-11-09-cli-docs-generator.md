# CLI Docs Generator Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `hack/tools/gen-cli-docs` so `website/docs/user-guides/cli.md` is generated from the live cobra tree and can never drift behind new subcommands again.

**Architecture:** A single `main.go` imports `cli.NewRootCommand()`, walks commands in registration order, and renders one markdown page: frozen preamble const (title + installation, copied verbatim once) + generated global-flags table + per-command sections (`Short`, `Long` verbatim, `Example` fenced, flag tables with required markers). Wired into `make generate` via `docs-cli-gen`; `make verify` enforces freshness with no changes.

**Tech Stack:** Go 1.26 (same module `github.com/ardikabs/hibernator`), `github.com/spf13/cobra`, `github.com/spf13/pflag`, make, mkdocs-material (docs-build smoke only).

---

## File Structure

- Create: `hack/tools/gen-cli-docs/main.go` — preamble const, `findModuleRoot`, `main`, `renderGlobalFlags`, `renderCommand`, `flagSpec`, `flagDesc`. Single file, mirroring `hack/tools/gen-sink-docs/main.go` layout (one-file tool, no sub-packages — YAGNI).
- Create: `hack/tools/gen-cli-docs/main_test.go` — fixture-tree unit tests (required marker, aliases, nesting) + live-root test asserting the `exception` group renders.
- Modify: `Makefile` — add `docs-cli-gen` target; append it to `generate:` dependencies.
- Modify: `cmd/kubectl-hibernator/cli/exception/create.go` — add `Long`.
- Modify: `cmd/kubectl-hibernator/cli/exception/list.go` — add `Long`.
- Modify: `cmd/kubectl-hibernator/cli/exception/describe.go` — add `Long`.
- Modify: `cmd/kubectl-hibernator/cli/exception/delete.go` — add `Long`.
- Generated: `website/docs/user-guides/cli.md` — output of the tool, committed.

---

### Task 1: Generator skeleton — preamble, root, file write

**Files:**
- Create: `hack/tools/gen-cli-docs/main.go`
- Create: `hack/tools/gen-cli-docs/main_test.go`

Background: `cli.NewRootCommand()` (in `cmd/kubectl-hibernator/cli/root.go:39`) only builds cobra structs and registers flags — no client creation, safe to call from a generator. The preamble (title + installation, current `cli.md` lines 1–90) is copied verbatim once into a const; only the global-flags table is generated from `root.PersistentFlags()` so a future global flag follows automatically.

- [ ] **Step 1: Write the skeleton `main.go`**

```go
/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

// gen-cli-docs generates website/docs/user-guides/cli.md from the live cobra
// command tree built by cli.NewRootCommand. Handwritten prose lives in cobra
// Long/Example fields in Go code; this tool only walks and renders.
package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
)

const outputFile = "website/docs/user-guides/cli.md"

// preamble is the frozen page header: title plus installation guide, copied
// verbatim from website/docs/user-guides/cli.md lines 1-90. Do not hand-edit
// the generated file; edit this const (for the header) or cobra Long/Example
// fields (for command docs) instead.
const preamble = `# CLI User Guide

This guide covers how to use the ` + "`kubectl-hibernator`" + ` plugin for managing HibernatePlan resources. It provides practical commands for inspecting plans, controlling hibernation operations, managing restore data, and viewing executor logs.

## Installation

### One-Line Installer (Recommended)

The fastest way to install ` + "`kubectl-hibernator`" + ` is using the install script:

` + "```bash" + `
curl -sSL https://hibernator.ardikabs.com/install-cli.sh | bash
` + "```" + `

This automatically detects your operating system and architecture, downloads the latest release, verifies the checksum, and installs to ` + "`/usr/local/bin`" + `.

**Install a specific version:**

` + "```bash" + `
curl -sSL https://hibernator.ardikabs.com/install-cli.sh | bash -s -- --version v1.5.0
` + "```" + `

**Install to a custom directory:**

` + "```bash" + `
curl -sSL https://hibernator.ardikabs.com/install-cli.sh | bash -s -- --install-dir ~/bin
` + "```" + `

### Manual Installation

Alternatively, download the binary for your platform from the [GitHub Releases](https://github.com/ardikabs/hibernator/releases) page and place it on your ` + "`PATH`" + `.

Verify the installation:

` + "```bash" + `
kubectl hibernator version
` + "```" + `

`

func main() {
	root := findModuleRoot()

	var buf bytes.Buffer
	buf.WriteString(preamble)

	out := filepath.Join(root, outputFile)
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "error creating output dir: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(out, buf.Bytes(), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "error writing %s: %v\n", out, err)
		os.Exit(1)
	}
	fmt.Printf("Generated %s (%d bytes)\n", outputFile, buf.Len())
}

// findModuleRoot walks up from cwd to find go.mod.
func findModuleRoot() string {
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			fmt.Fprintln(os.Stderr, "error: cannot find go.mod in parent directories")
			os.Exit(1)
		}
		dir = parent
	}
}
```

- [ ] **Step 2: Write the first failing test**

```go
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreambleWritten(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "website/docs/user-guides"), 0o755); err != nil {
		t.Fatal(err)
	}
	main()
	out, err := os.ReadFile(filepath.Join(dir, outputFile))
	if err != nil {
		t.Fatalf("expected output file, got: %v", err)
	}
	if !strings.HasPrefix(string(out), "# CLI User Guide\n") {
		t.Fatalf("output does not start with preamble title:\n%.200s", out)
	}
	if !strings.Contains(string(out), "install-cli.sh") {
		t.Fatalf("output missing installation section")
	}
}
```

- [ ] **Step 3: Run test to verify it passes (skeleton is trivially correct)**

Run: `go test ./hack/tools/gen-cli-docs/ -v -count=1`
Expected: PASS (`TestPreambleWritten`). Note: this overwrites `website/docs/user-guides/cli.md` in the repo on the `main()` call — restore it right after with `git checkout -- website/docs/user-guides/cli.md`.

- [ ] **Step 4: Restore the overwritten cli.md and commit**

```bash
git checkout -- website/docs/user-guides/cli.md
git add hack/tools/gen-cli-docs/main.go hack/tools/gen-cli-docs/main_test.go
git commit -m "feat: add gen-cli-docs skeleton with preamble"
```

---

### Task 2: Walker and section renderer

**Files:**
- Modify: `hack/tools/gen-cli-docs/main.go`
- Modify: `hack/tools/gen-cli-docs/main_test.go`

Background: `cmd.Commands()` preserves registration order (matches `root.go` reading order). `CommandPath()` returns e.g. `kubectl-hibernator exception create`; docs use `kubectl hibernator`, so replace the root prefix. `MarkFlagRequired` records the annotation key `cobra.BashCompOneRequiredFlag` (`cobra_annotation_bash_completion_one_required_flag`) on the flag itself — read it via `f.Annotations`. Repo convention embeds examples inside `Long` (see `suspend/command.go:55-72`); `Example` is usually empty but render it when present.

- [ ] **Step 1: Write failing tests for the renderer**

Append to `hack/tools/gen-cli-docs/main_test.go`:

```go
package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func fixtureTree() *cobra.Command {
	root := &cobra.Command{Use: "kubectl-hibernator"}
	group := &cobra.Command{
		Use:   "exception",
		Short: "Manage ScheduleException resources",
		Long:  "Create, list, inspect, and delete ScheduleException resources.",
	}
	leaf := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List ScheduleExceptions",
		Long:    "Compact listing with plan, label, and type filters.",
		Example: "kubectl hibernator exception list --plan my-plan",
	}
	leaf.Flags().String("plan", "", "Filter exceptions referencing this HibernatePlan")
	leaf.Flags().String("type", "", "Filter by exception type")
	if err := leaf.MarkFlagRequired("type"); err != nil {
		panic(err)
	}
	leaf.Flags().BoolP("all-namespaces", "A", false, "List exceptions from all namespaces")
	group.AddCommand(leaf)
	root.AddCommand(group)
	return root
}

func renderFixture(t *testing.T) string {
	t.Helper()
	root := fixtureTree()
	var buf bytes.Buffer
	for _, sub := range root.Commands() {
		renderCommand(&buf, sub, 1)
	}
	return buf.String()
}

func TestRenderGroupAndLeafHeadings(t *testing.T) {
	out := renderFixture(t)
	for _, want := range []string{"### `exception`", "#### `exception list`"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing heading %q\n%s", want, out)
		}
	}
}

func TestRenderAliasesExampleLong(t *testing.T) {
	out := renderFixture(t)
	for _, want := range []string{
		"**Aliases:** `ls`",
		"Compact listing with plan, label, and type filters.",
		"```bash\nkubectl hibernator exception list --plan my-plan\n```",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q\n%s", want, out)
		}
	}
}

func TestRenderFlagTableWithRequired(t *testing.T) {
	out := renderFixture(t)
	for _, want := range []string{
		"| `--type` (required) | Filter by exception type |",
		"| `-A, --all-namespaces` | List exceptions from all namespaces |",
		"| `--plan` | Filter exceptions referencing this HibernatePlan |",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing flag row %q\n%s", want, out)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./hack/tools/gen-cli-docs/ -run 'TestRender' -v -count=1`
Expected: FAIL — `renderCommand` undefined (compile error).

- [ ] **Step 3: Implement walker, renderer, flag helpers**

Append to `hack/tools/gen-cli-docs/main.go` (add `sort`, `strings`, `github.com/spf13/cobra`, `github.com/spf13/pflag`, and `github.com/ardikabs/hibernator/cmd/kubectl-hibernator/cli` to imports):

```go
// displayPath rewrites "kubectl-hibernator exception create" to the
// user-facing "kubectl hibernator exception create".
func displayPath(cmd *cobra.Command) string {
	return strings.Replace(cmd.CommandPath(), "kubectl-hibernator", "kubectl hibernator", 1)
}

// shortPath drops the root prefix: "kubectl hibernator exception create"
// becomes "exception create" for headings.
func shortPath(cmd *cobra.Command) string {
	return strings.TrimPrefix(displayPath(cmd), "kubectl hibernator ")
}

// renderCommand writes one section per command and recurses into children
// in registration order. depth 1 renders ###, depth 2+ renders ####.
func renderCommand(buf *bytes.Buffer, cmd *cobra.Command, depth int) {
	if cmd.Hidden {
		return
	}
	if cmd.Use == "" || cmd.Short == "" {
		fmt.Fprintf(os.Stderr, "error: command %q missing Use or Short\n", cmd.CommandPath())
		os.Exit(1)
	}
	if cmd.Long == "" {
		fmt.Fprintf(os.Stderr, "warning: command %q has no Long description\n", cmd.CommandPath())
	}
	if !cmd.Runnable() && cmd.Example == "" && len(cmd.Commands()) == 0 {
		fmt.Fprintf(os.Stderr, "warning: command %q has no Example\n", cmd.CommandPath())
	}

	marks := strings.Repeat("#", min(depth+2, 6))
	fmt.Fprintf(buf, "%s `%s`\n\n", marks, shortPath(cmd))
	fmt.Fprintf(buf, "%s\n\n", cmd.Short)
	if len(cmd.Aliases) > 0 {
		quoted := make([]string, 0, len(cmd.Aliases))
		for _, a := range cmd.Aliases {
			quoted = append(quoted, "`"+a+"`")
		}
		fmt.Fprintf(buf, "**Aliases:** %s\n\n", strings.Join(quoted, ", "))
	}
	if cmd.Long != "" {
		fmt.Fprintf(buf, "%s\n\n", strings.TrimSpace(cmd.Long))
	}
	if cmd.Example != "" {
		fmt.Fprintf(buf, "```bash\n%s\n```\n\n", strings.TrimSpace(cmd.Example))
	}
	renderFlagTable(buf, cmd)

	buf.WriteString("---\n\n")

	for _, sub := range cmd.Commands() {
		renderCommand(buf, sub, depth+1)
	}
}

func renderFlagTable(buf *bytes.Buffer, cmd *cobra.Command) {
	flags := cmd.LocalFlags()
	if !flags.HasAvailableFlags() {
		return
	}
	buf.WriteString("| Flag | Description |\n")
	buf.WriteString("|------|-------------|\n")
	ordered := make([]*pflag.Flag, 0)
	flags.VisitAll(func(f *pflag.Flag) {
		if !f.Hidden {
			ordered = append(ordered, f)
		}
	})
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Name < ordered[j].Name })
	for _, f := range ordered {
		fmt.Fprintf(buf, "| %s | %s |\n", flagSpec(f), flagDesc(f))
	}
	buf.WriteString("\n")
}

func flagSpec(f *pflag.Flag) string {
	var name string
	if f.Shorthand != "" {
		name = fmt.Sprintf("`-%s, --%s`", f.Shorthand, f.Name)
	} else {
		name = fmt.Sprintf("`--%s`", f.Name)
	}
	if _, ok := f.Annotations[cobra.BashCompOneRequiredFlag]; ok {
		name += " (required)"
	}
	return name
}

func flagDesc(f *pflag.Flag) string {
	desc := f.Usage
	if f.DefValue != "" && f.DefValue != "false" {
		desc += fmt.Sprintf(" (default: `%s`)", f.DefValue)
	}
	return desc
}

// renderGlobalFlags writes the shared Global Flags section once from the
// root persistent flags.
func renderGlobalFlags(buf *bytes.Buffer, root *cobra.Command) {
	buf.WriteString("## Global Flags\n\n")
	buf.WriteString("These flags are available on every subcommand:\n\n")
	buf.WriteString("| Flag | Description |\n")
	buf.WriteString("|------|-------------|\n")
	root.PersistentFlags().VisitAll(func(f *pflag.Flag) {
		fmt.Fprintf(buf, "| %s | %s |\n", flagSpec(f), flagDesc(f))
	})
	buf.WriteString("\n---\n\n")
}
```

And wire into `main()` between preamble and file write:

```go
	rootCmd := cli.NewRootCommand()
	renderGlobalFlags(&buf, rootCmd)
	buf.WriteString("## Commands\n\n")
	for _, sub := range rootCmd.Commands() {
		renderCommand(&buf, sub, 1)
	}
```

(`min` is builtin since Go 1.21; module uses Go 1.26.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./hack/tools/gen-cli-docs/ -v -count=1`
Expected: PASS (all 4 tests). Restore overwritten cli.md: `git checkout -- website/docs/user-guides/cli.md`.

- [ ] **Step 5: Add the live-root regression test (the never-miss-again guard)**

Append to `main_test.go`:

```go
func TestLiveRootRendersExceptionGroup(t *testing.T) {
	rootCmd := cli.NewRootCommand()
	var buf bytes.Buffer
	for _, sub := range rootCmd.Commands() {
		renderCommand(&buf, sub, 1)
	}
	out := buf.String()
	for _, want := range []string{
		"### `exception`",
		"#### `exception create`",
		"#### `exception list`",
		"#### `exception describe`",
		"#### `exception delete`",
		"| `--type` (required) |",
		"**Aliases:** `ls`",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("live root output missing %q", want)
		}
	}
}
```

Add import `"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/cli"` to the test file. The `| \`--type\` (required) |` assertion holds because `exception/create.go:79-81` marks `type` required; `**Aliases:** \`ls\`` holds because `exception/list.go:34` declares alias `ls`.

Run: `go test ./hack/tools/gen-cli-docs/ -run TestLiveRoot -v -count=1`
Expected: PASS. Then `git checkout -- website/docs/user-guides/cli.md`.

- [ ] **Step 6: Commit**

```bash
git add hack/tools/gen-cli-docs/main.go hack/tools/gen-cli-docs/main_test.go
git commit -m "feat: render CLI reference from live cobra tree"
```

---

### Task 3: Makefile wiring

**Files:**
- Modify: `Makefile`

- [ ] **Step 1: Add the `docs-cli-gen` target after `docs-sink-gen`**

Old (lines 416–420):
```make
.PHONY: docs-sink-gen
docs-sink-gen: ## Generate notification sink reference from Go types.
	@echo "$(CYAN)Generating notification sink docs...$(RESET)"
	@$(GOCMD) run ./hack/tools/gen-sink-docs/
	@echo "$(GREEN)Notification sink docs generated in $(DOCS_DIR)/docs/reference/notification-sinks.md$(RESET)"
```

New — append after that block:
```make
.PHONY: docs-cli-gen
docs-cli-gen: ## Generate CLI command reference from the cobra tree.
	@echo "$(CYAN)Generating CLI reference docs...$(RESET)"
	@$(GOCMD) run ./hack/tools/gen-cli-docs/
	@echo "$(GREEN)CLI reference docs generated in $(DOCS_DIR)/docs/user-guides/cli.md$(RESET)"
```

- [ ] **Step 2: Hook into `generate`**

Old (line 76):
```make
generate: controller-gen docs-api-gen docs-executor-params-gen docs-sink-gen ## Generate code (DeepCopy, CRDs), sync to Helm chart, and generate API reference docs.
```

New:
```make
generate: controller-gen docs-api-gen docs-executor-params-gen docs-sink-gen docs-cli-gen ## Generate code (DeepCopy, CRDs), sync to Helm chart, and generate API reference docs.
```

- [ ] **Step 3: Verify the target works**

Run: `make docs-cli-gen`
Expected: `Generated website/docs/user-guides/cli.md (N bytes)` plus `git status --porcelain` shows `website/docs/user-guides/cli.md` modified. Restore with `git checkout -- website/docs/user-guides/cli.md` (real regeneration happens in Task 5 after backfill).

- [ ] **Step 4: Commit**

```bash
git add Makefile
git commit -m "feat: wire gen-cli-docs into make generate"
```

---

### Task 4: Backfill `Long` for the `exception` group

**Files:**
- Modify: `cmd/kubectl-hibernator/cli/exception/create.go`
- Modify: `cmd/kubectl-hibernator/cli/exception/list.go`
- Modify: `cmd/kubectl-hibernator/cli/exception/describe.go`
- Modify: `cmd/kubectl-hibernator/cli/exception/delete.go`

Background: these four files define only `Short` today (verified by grep — every other CLI command file already has `Long`). Without this step the generated `exception` sections would be thin. Type semantics (`suspend`/`extend`/`replace`) live in `concepts/schedule-exceptions.md` — link there rather than duplicating. Flag facts below come from the flag registrations in each file.

- [ ] **Step 1: Add `Long` to `exception create`**

In `create.go`, the command literal is at lines 56–63 (`Use: "create <prefix>"`, `Short: ...`, `Args: cobra.ExactArgs(1)`). Insert `Long` after `Short`:

```go
		Long: `Create ScheduleExceptions for one HibernatePlan (--plan) or a set of plans selected by label (-l).

Each invocation creates one ScheduleException per matched plan. The exception deviates
matched plans from their base schedules for the validity period (--from to --until)
within the daily window (--window-start/--window-end, --days, defaults to all days).

Either --plan or --selector (-l) is required. The exception type (--type) selects
suspend, extend, or replace semantics; see Schedule Exceptions concepts for details.

Examples:
  # Suspend a plan overnight over the holidays
  kubectl hibernator exception create holiday --plan my-plan --type suspend --from "in 1 hour" --until "in 2 hours" --window-start 22:00 --window-end 06:00

  # Target plans by label
  kubectl hibernator exception create freeze -l env=prod --type suspend --from "2026-12-24T18:00:00Z" --until "2026-12-27T06:00:00Z" --window-start 18:00 --window-end 06:00

  # Preview without creating
  kubectl hibernator exception create holiday --plan my-plan --type suspend --from "in 1 hour" --until "in 2 hours" --window-start 22:00 --window-end 06:00 --dry-run`,
```

- [ ] **Step 2: Add `Long` to `exception list`**

In `list.go`, the literal is at lines 32–40 (`Use: "list"`, `Aliases: []string{"ls"}`, `Short: ...`, `Args: cobra.NoArgs`). Insert after `Short`:

```go
		Long: `Compact kubectl-get style listing of ScheduleExceptions.

Filter by referenced plan (--plan), label selector (-l/--selector), or exception
type (--type, comma-separated: suspend,extend,replace). Use -A/--all-namespaces
to list across namespaces.

Examples:
  kubectl hibernator exception list
  kubectl hibernator exception list -A
  kubectl hibernator exception list --plan my-plan
  kubectl hibernator exception list -l env=prod --type suspend,replace`,
```

- [ ] **Step 3: Add `Long` to `exception describe`**

In `describe.go`, the literal is at lines 38–45 (`Use: "describe [name...]"`, `Short: ...`, `Args: cobra.ArbitraryArgs`). Positional names cannot be mixed with `--selector`/`--plan`/`--state` filters (enforced at `describe.go:56-58`). Insert after `Short`:

```go
		Long: `Show operational details of ScheduleExceptions: state, referenced plan,
validity period, window, and labels.

Pass exact exception names, or filter with --selector (-l), --plan, or --state
(comma-separated: Active,Pending,Expired,Detached). Exact names cannot be mixed
with filters.

Examples:
  kubectl hibernator exception describe holiday-abc12
  kubectl hibernator exception describe --plan my-plan
  kubectl hibernator exception describe -l env=prod --state Active,Pending
  kubectl hibernator exception describe -A`,
```

- [ ] **Step 4: Add `Long` to `exception delete`**

In `delete.go`, the literal is at lines 38–45 (`Use: "delete [name...]"`, `Short: ...`, `Args: cobra.ArbitraryArgs`). At least one of a name, `--selector`, or `--plan` is required (`delete.go:59-61`); names cannot be mixed with filters (`delete.go:56-58`). Insert after `Short`:

```go
		Long: `Delete ScheduleExceptions by name or by qualified filters.

Provide exact names, or select with --selector (-l) or --plan. At least one of
a name, --selector, or --plan is required. Exact names cannot be mixed with
filters. Deletion prompts for confirmation unless --yes is given.

Examples:
  kubectl hibernator exception delete holiday-abc12
  kubectl hibernator exception delete -l env=prod --plan my-plan --yes
  kubectl hibernator exception delete --plan my-plan --dry-run`,
```

- [ ] **Step 5: Build and check help output**

Run: `go build ./cmd/kubectl-hibernator/ && go vet ./cmd/kubectl-hibernator/cli/exception/`
Expected: success, no output. Then `go run ./cmd/kubectl-hibernator exception create --help` shows the new Long text.

- [ ] **Step 6: Commit**

```bash
git add cmd/kubectl-hibernator/cli/exception/
git commit -m "feat: add Long docs to exception CLI commands"
```

---

### Task 5: Regenerate, verify idempotency and site build

**Files:**
- Generated: `website/docs/user-guides/cli.md`

- [ ] **Step 1: Regenerate the reference**

Run: `go run ./hack/tools/gen-cli-docs/`
Expected: `Generated website/docs/user-guides/cli.md (N bytes)`.

- [ ] **Step 2: Confirm the `exception` gap is closed**

Run: `grep -n "exception create\|exception list\|exception describe\|exception delete" website/docs/user-guides/cli.md`
Expected: one `####` heading per subcommand plus the `### \`exception\`` group heading.

- [ ] **Step 3: Idempotency check**

Run: `sha256sum website/docs/user-guides/cli.md && go run ./hack/tools/gen-cli-docs/ && sha256sum website/docs/user-guides/cli.md`
Expected: identical hashes.

- [ ] **Step 4: Full test suite for touched packages**

Run: `go test ./hack/tools/gen-cli-docs/ ./cmd/kubectl-hibernator/... -count=1`
Expected: all PASS.

- [ ] **Step 5: Docs build smoke (requires Docker)**

Run: `make docs-build`
Expected: `Documentation built: website/site/`. If Docker is unavailable, skip with a note — `mkdocs` strictness is unchanged since output is plain markdown with material admonitions already used on the site.

- [ ] **Step 6: Commit the regenerated reference**

```bash
git add website/docs/user-guides/cli.md
git commit -m "docs: regenerate CLI reference including exception group"
```

- [ ] **Step 7: Final verify**

Run: `make verify` (runs generate + fmt + vet, fails on dirty tree)
Expected: clean exit — proves the checked-in `cli.md` matches generator output.

---

## Self-Review

**1. Spec coverage:**
- Cobra as single source, `hack/tools/gen-cli-docs/main.go` via `go run` — Tasks 1–2.
- Single page replacing `user-guides/cli.md`, preamble + current style — Task 1 preamble, Task 2 headings/tables.
- `Long` verbatim incl. admonitions, `Example` fenced — Task 2 renderer.
- Global flags once, aliases, required markers, deterministic order — Task 2.
- `docs-cli-gen` + `generate` hook, `verify` unchanged — Task 3.
- Backfill for thin commands (`exception` group) — Task 4 (grep proved these are the only four files missing `Long`; all others already comply).
- Golden/live-root tests guarding new subcommands, idempotency, docs-build — Tasks 2, 5.
- Success criteria (exception present, new group flows automatically, verify blocks drift) — Task 5 steps 2, 7 and `TestLiveRootRendersExceptionGroup`.

**2. Placeholder scan:** no TBD/TODO; every code step shows full code; flag facts (`--type` required, alias `ls`, no-mix rules, at-least-one rule, state values) cite exact file lines verified by reading.

**3. Type consistency:** `renderCommand(buf *bytes.Buffer, cmd *cobra.Command, depth int)`, `flagSpec/flagDesc(*pflag.Flag) string`, `renderGlobalFlags(*bytes.Buffer, *cobra.Command)` used identically in implementation and tests; `cobra.BashCompOneRequiredFlag` is the annotation key `MarkFlagRequired` sets.

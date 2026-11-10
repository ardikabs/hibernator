package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/cli"
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
	if !strings.Contains(string(out), "=== \"Linux (amd64)\"") {
		t.Fatalf("output missing per-platform install tabs")
	}
	for _, want := range []string{
		`=== "Linux (arm64)"`,
		`=== "macOS (amd64)"`,
		`=== "macOS (arm64)"`,
		"    ```bash",
	} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("output missing %q", want)
		}
	}
	if !strings.Contains(string(out), "Automatic Version Check") {
		t.Fatalf("output missing automatic version check section")
	}
	if !strings.Contains(string(out), "<!-- BEGIN GENERATED CONTENT") {
		t.Fatalf("output missing generator-start marker")
	}
}

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

func TestRenderExampleHeader(t *testing.T) {
	out := renderFixture(t)
	want := "**Examples for `kubectl hibernator exception list`:**"
	if !strings.Contains(out, want) {
		t.Fatalf("missing example header %q\n%s", want, out)
	}
	headerIdx := strings.Index(out, want)
	fenceIdx := strings.Index(out, "```bash\nkubectl hibernator exception list --plan my-plan")
	if fenceIdx < 0 {
		t.Fatalf("missing example fence\n%s", out)
	}
	if headerIdx > fenceIdx {
		t.Fatalf("example header must precede example fence\n%s", out)
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

func TestSeeAlsoRendering(t *testing.T) {
	cmd := &cobra.Command{
		Use:     "my-cmd",
		Short:   "My command",
		Long:    "Long description.",
		Example: "kubectl hibernator my-cmd --foo bar",
		Annotations: map[string]string{
			"hibernator.ardikabs.com/see-also": "user-guides/override-actions.md, user-guides/plan-suspension.md",
		},
	}
	var buf bytes.Buffer
	renderCommand(&buf, cmd, 1)
	out := buf.String()
	want := "**See also:** [Override Actions](override-actions.md), [Plan Suspension](plan-suspension.md)"
	if !strings.Contains(out, want) {
		t.Fatalf("missing see-also %q\n%s", want, out)
	}

	plain := &cobra.Command{
		Use:     "plain",
		Short:   "Plain command",
		Long:    "Long description.",
		Example: "kubectl hibernator plain",
	}
	var plainBuf bytes.Buffer
	renderCommand(&plainBuf, plain, 1)
	if strings.Contains(plainBuf.String(), "See also") {
		t.Fatalf("expected no See also for command without annotation\n%s", plainBuf.String())
	}
}

func TestSeeAlsoTitle(t *testing.T) {
	for path, want := range map[string]string{
		"user-guides/schedule-exceptions.md": "Schedule Exceptions",
		"user-guides/override-actions.md":    "Override Actions",
		"user-guides/plan-suspension.md":     "Plan Suspension",
		"user-guides/notifications.md":       "Notifications",
		"concepts/multi_word-name.md":        "Multi Word Name",
	} {
		if got := seeAlsoTitle(path); got != want {
			t.Errorf("seeAlsoTitle(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestLiveRootRendersSeeAlso(t *testing.T) {
	rootCmd := cli.NewRootCommand()
	var buf bytes.Buffer
	for _, sub := range rootCmd.Commands() {
		renderCommand(&buf, sub, 1)
	}
	out := buf.String()
	for _, want := range []string{
		"override-actions.md",
		"plan-suspension.md",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("live root output missing see-also %q", want)
		}
	}
}

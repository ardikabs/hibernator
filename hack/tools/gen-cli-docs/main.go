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
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
	"unicode"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/ardikabs/hibernator/cmd/kubectl-hibernator/cli"
)

const outputFile = "website/docs/user-guides/cli.md"

const seeAlsoAnnotation = "hibernator.ardikabs.com/see-also"

//go:embed cli.md.gotmpl
var preambleFS embed.FS

// preamble is the frozen page header (title, installation, version check)
// rendered from cli.md.gotmpl. Do not hand-edit the generated file; edit
// cli.md.gotmpl (for the header) or cobra Long/Example fields (for command
// docs) instead.
const preambleTmpl = "cli.md.gotmpl"

func main() {
	root := findModuleRoot()

	tmpl, err := template.ParseFS(preambleFS, preambleTmpl)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error parsing %s: %v\n", preambleTmpl, err)
		os.Exit(1)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, nil); err != nil {
		fmt.Fprintf(os.Stderr, "error rendering %s: %v\n", preambleTmpl, err)
		os.Exit(1)
	}

	rootCmd := cli.NewRootCommand()
	validateTree(rootCmd)
	renderGlobalFlags(&buf, rootCmd)
	buf.WriteString("## Commands\n\n")
	for _, sub := range rootCmd.Commands() {
		renderCommand(&buf, sub, 1)
	}

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

// validateTree fails fast on malformed commands (missing identity or summary)
// before rendering. Separated from renderCommand so the renderer stays a pure
// writer like the sibling gen tools.
func validateTree(cmd *cobra.Command) {
	if cmd.Hidden {
		return
	}
	if cmd.Use == "" || cmd.Short == "" {
		fmt.Fprintf(os.Stderr, "error: command %q missing Use or Short\n", cmd.CommandPath())
		os.Exit(1)
	}
	for _, sub := range cmd.Commands() {
		validateTree(sub)
	}
}

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
// in cobra order (alphabetical by default). depth 1 renders ###, deeper levels
// add one mark each up to ######.
func renderCommand(buf *bytes.Buffer, cmd *cobra.Command, depth int) {
	if cmd.Hidden {
		return
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
		fmt.Fprintf(buf, "**Examples for `%s`:**\n\n", displayPath(cmd))
		fmt.Fprintf(buf, "```bash\n%s\n```\n\n", strings.TrimSpace(cmd.Example))
	}
	if seeAlso, ok := cmd.Annotations[seeAlsoAnnotation]; ok && strings.TrimSpace(seeAlso) != "" {
		parts := strings.Split(seeAlso, ",")
		links := make([]string, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			base := p
			if idx := strings.LastIndex(p, "/"); idx >= 0 {
				base = p[idx+1:]
			}
			links = append(links, fmt.Sprintf("[%s](%s)", seeAlsoTitle(p), base))
		}
		if len(links) > 0 {
			fmt.Fprintf(buf, "**See also:** %s\n\n", strings.Join(links, ", "))
		}
	}
	renderFlagTable(buf, cmd)

	buf.WriteString("---\n\n")

	for _, sub := range cmd.Commands() {
		renderCommand(buf, sub, depth+1)
	}
}

// seeAlsoTitle turns a doc path like "user-guides/schedule-exceptions.md"
// into human link text like "Schedule Exceptions". Purely mechanical:
// last segment, no extension, separators become spaces, words capitalized.
func seeAlsoTitle(p string) string {
	base := p
	if idx := strings.LastIndex(p, "/"); idx >= 0 {
		base = p[idx+1:]
	}
	base = strings.TrimSuffix(base, ".md")
	words := strings.FieldsFunc(base, func(r rune) bool { return r == '-' || r == '_' })
	for i, w := range words {
		r := []rune(w)
		if len(r) > 0 {
			r[0] = unicode.ToUpper(r[0])
			words[i] = string(r)
		}
	}
	return strings.Join(words, " ")
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
		if f.Hidden {
			return
		}
		fmt.Fprintf(buf, "| %s | %s |\n", flagSpec(f), flagDesc(f))
	})
	buf.WriteString("\n---\n\n")
}

/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

// gen-sink-docs generates a Markdown reference page for notification sinks by
// scanning the Go source files in internal/notification/sink/{type}/config.go.
// It extracts the config struct fields (json tags, types, doc comments) from each
// sink sub-package and loads the DefaultTemplate from the centralized templates package,
// then renders a unified Notification Sink Reference page.
package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/ardikabs/hibernator/internal/notification/sink"
	"github.com/samber/lo"
)

const (
	sinkBaseDir = "internal/notification/sink"
	outputFile  = "website/docs/reference/notification-sinks.md"
)

// sinkTypes controls the ordering and display metadata for each sink.
// The key must match the sub-package directory name under sinkBaseDir.
var sinkTypes = []struct {
	Dir           string
	DisplayName   string
	Description   string
	ExtraSections []extraSection
}{
	{
		Dir:         "slack",
		DisplayName: "Slack",
		Description: "Delivers messages via [Slack Incoming Webhooks](https://api.slack.com/messaging/webhooks).\n\n" +
			"!!! tip \"Formatting Message Text (Slack)\"\n" +
			"    When `format: json` is configured, message content is rendered using Slack message formatting semantics (`mrkdwn`/`plain_text`).\n" +
			"    See Slack docs: [Formatting message text](https://docs.slack.dev/messaging/formatting-message-text/).",
	},
	{
		Dir:         "telegram",
		DisplayName: "Telegram",
		Description: "Delivers messages via the [Telegram Bot API](https://core.telegram.org/bots/api).\n\n" +
			"!!! tip \"Escaping Reserved Characters (Telegram)\"\n" +
			"    The Telegram Bot API requires certain characters to be escaped depending on the `parse_mode`.\n" +
			"    Two helper functions are available in all templates:\n\n" +
			"    - **`escapeHTML`** — use when `parse_mode` is `HTML` (the default). Escapes `<`, `>`, `&`, and `\"`.\n" +
			"    - **`escapeMarkdown`** — use when `parse_mode` is `MarkdownV2`. Escapes `_`, `*`, `[`, `]`, `(`, `)`, `~`, `` ` ``, `>`, `#`, `+`, `-`, `=`, `|`, `{`, `}`, `.`, `!`.\n\n" +
			"    Always pipe dynamic values through the appropriate escape function in custom Telegram templates, otherwise Telegram will reject the message.",
	},
	{
		Dir:         "webhook",
		DisplayName: "Webhook",
		Description: "Delivers notifications as a JSON `POST` request to any HTTP endpoint. Useful for custom alerting pipelines, incident management tools, or internal APIs.\n\n" +
			"!!! note \"Custom Templates with Webhooks\"\n" +
			"    If `enable_renderer` is `false` (the default), `templateRef` has no effect." +
			"    The receiver gets the raw structured payload and can format it however it likes.\n" +
			"    Set `enable_renderer: true` when you want the controller to pre-render a human-readable message.",
		ExtraSections: []extraSection{
			{Title: "Payload", StructName: "webhookBody"},
		},
	},
}

type configField struct {
	Name     string
	GoType   string
	JSONTag  string
	Required bool
	Doc      string
}

// extraSection maps a documentation section title to a Go struct name
// that should be parsed and rendered (e.g., JSON payload schema).
type extraSection struct {
	Title      string // section heading (e.g., "Payload")
	StructName string // Go struct to parse from the sub-package (e.g., "webhookBody")
}

// resolvedSection is an extraSection after struct resolution.
type resolvedSection struct {
	Title  string
	Fields []structField
}

// structField is a recursively-resolved struct field for payload rendering.
type structField struct {
	JSONKey  string // json tag value, or Go field name when no tag
	GoType   string // display type (e.g., "string", "object", "object[]")
	Required bool
	Doc      string
	Children []structField // non-nil when the field is a struct or slice-of-struct
	IsArray  bool          // true when the Go type is a slice
}

type sinkInfo struct {
	Dir             string
	DisplayName     string
	Description     string
	Type            string // SinkType constant value
	Fields          []configField
	DefaultTemplate string
	Sections        []resolvedSection
}

func main() {
	root := findModuleRoot()

	var sinks []sinkInfo
	for _, st := range sinkTypes {
		info := parseSink(root, st.Dir, st.DisplayName, st.Description, st.ExtraSections)
		sinks = append(sinks, info)
	}

	var buf bytes.Buffer
	render(&buf, sinks)

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

func parseSink(root, dir, displayName, description string, extras []extraSection) sinkInfo {
	info := sinkInfo{
		Dir:         dir,
		DisplayName: displayName,
		Description: description,
	}

	pkgDir := filepath.Join(root, sinkBaseDir, dir)

	fset := token.NewFileSet()
	localFiles, err := parseGoFiles(fset, pkgDir, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error parsing %s: %v\n", pkgDir, err)
		os.Exit(1)
	}

	for _, f := range localFiles {
		extractSinkType(f, &info)
		extractConfigFields(f, &info)
	}

	info.DefaultTemplate = string(lo.Must(sink.TemplateFS.ReadFile(info.Type + "/" + "default.gotmpl")))

	// Parse the parent sink package for cross-package type resolution
	// (e.g., sink.Payload referenced by webhookBody).
	parentDir := filepath.Join(root, sinkBaseDir)
	parentFiles, err := parseGoFiles(fset, parentDir, func(e os.DirEntry) bool {
		return !strings.HasSuffix(e.Name(), "_test.go")
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error parsing parent sink package: %v\n", err)
		os.Exit(1)
	}

	// Resolve extra sections (e.g., webhook Payload → webhookBody struct).
	for _, es := range extras {
		visited := make(map[string]bool)
		fields := resolveStructFields(localFiles, parentFiles, es.StructName, false, visited)
		if len(fields) > 0 {
			info.Sections = append(info.Sections, resolvedSection{
				Title:  es.Title,
				Fields: fields,
			})
		}
	}

	return info
}

// parseGoFiles parses all Go files in a directory, optionally filtering with fn.
func parseGoFiles(fset *token.FileSet, dir string, filter func(os.DirEntry) bool) ([]*ast.File, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []*ast.File
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		if filter != nil && !filter(e) {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, parser.ParseComments)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, nil
}

// findStruct searches files for a struct type with the given name.
func findStruct(files []*ast.File, name string) *ast.StructType {
	for _, f := range files {
		for _, decl := range f.Decls {
			genDecl, ok := decl.(*ast.GenDecl)
			if !ok || genDecl.Tok != token.TYPE {
				continue
			}
			for _, spec := range genDecl.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || ts.Name.Name != name {
					continue
				}
				if st, ok := ts.Type.(*ast.StructType); ok {
					return st
				}
			}
		}
	}
	return nil
}

// resolveStructFields recursively resolves a named struct into a flat field
// list with nested children. searchParent=true skips localFiles lookup.
func resolveStructFields(localFiles, parentFiles []*ast.File, structName string, searchParent bool, visited map[string]bool) []structField {
	if visited[structName] {
		return nil
	}
	visited[structName] = true

	var st *ast.StructType
	if !searchParent {
		st = findStruct(localFiles, structName)
	}
	if st == nil {
		st = findStruct(parentFiles, structName)
	}
	if st == nil {
		return nil
	}

	var fields []structField
	for _, field := range st.Fields.List {
		if len(field.Names) == 0 {
			continue
		}
		name := field.Names[0].Name

		jsonTag := extractJSONTag(field.Tag)
		if jsonTag == "-" {
			continue
		}
		if jsonTag == "" {
			jsonTag = name
		}

		required := !hasOmitempty(field.Tag)
		doc := ""
		if field.Doc != nil {
			doc = cleanDoc(field.Doc.Text())
		}

		sf := structField{
			JSONKey:  jsonTag,
			GoType:   exprToString(field.Type),
			Required: required,
			Doc:      doc,
		}

		resolveFieldChildren(&sf, field.Type, localFiles, parentFiles, visited)
		fields = append(fields, sf)
	}
	return fields
}

// resolveFieldChildren populates Children and adjusts GoType for
// struct-typed or slice-of-struct-typed fields.
func resolveFieldChildren(sf *structField, expr ast.Expr, localFiles, parentFiles []*ast.File, visited map[string]bool) {
	switch t := expr.(type) {
	case *ast.Ident:
		if !isBuiltinType(t.Name) {
			children := resolveStructFields(localFiles, parentFiles, t.Name, false, copyVisited(visited))
			if len(children) > 0 {
				sf.Children = children
				sf.GoType = "object"
			}
		}
	case *ast.SelectorExpr:
		pkgIdent, ok := t.X.(*ast.Ident)
		if !ok {
			break
		}
		fqName := pkgIdent.Name + "." + t.Sel.Name
		switch fqName {
		case "time.Time":
			sf.GoType = "string"
		case "types.NamespacedName":
			sf.GoType = "object"
			sf.Children = []structField{
				{JSONKey: "namespace", GoType: "string", Doc: "Kubernetes namespace."},
				{JSONKey: "name", GoType: "string", Doc: "Resource name."},
			}
		default:
			// Resolve from parent sink package (e.g., sink.Payload).
			children := resolveStructFields(parentFiles, parentFiles, t.Sel.Name, true, copyVisited(visited))
			if len(children) > 0 {
				sf.Children = children
				sf.GoType = "object"
			}
		}
	case *ast.ArrayType:
		sf.IsArray = true
		resolveFieldChildren(sf, t.Elt, localFiles, parentFiles, visited)
		if len(sf.Children) > 0 {
			sf.GoType = "object[]"
		} else {
			sf.GoType = exprToString(t.Elt) + "[]"
		}
	case *ast.StarExpr:
		resolveFieldChildren(sf, t.X, localFiles, parentFiles, visited)
	case *ast.MapType:
		sf.GoType = fmt.Sprintf("map[%s]%s", exprToString(t.Key), exprToString(t.Value))
	}
}

func isBuiltinType(name string) bool {
	switch name {
	case "string", "bool", "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64",
		"float32", "float64", "byte", "rune", "error":
		return true
	}
	return false
}

func copyVisited(m map[string]bool) map[string]bool {
	c := make(map[string]bool, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

// extractSinkType finds the SinkType constant value.
func extractSinkType(f *ast.File, info *sinkInfo) {
	for _, decl := range f.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.CONST {
			continue
		}
		for _, spec := range genDecl.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if name.Name == "SinkType" && i < len(vs.Values) {
					if lit, ok := vs.Values[i].(*ast.BasicLit); ok {
						info.Type = strings.Trim(lit.Value, `"`)
					}
				}
			}
		}
	}
}

// extractConfigFields parses the `config` struct from config.go.
func extractConfigFields(f *ast.File, info *sinkInfo) {
	for _, decl := range f.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.TYPE {
			continue
		}
		for _, spec := range genDecl.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name.Name != "config" {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				continue
			}
			for _, field := range st.Fields.List {
				if len(field.Names) == 0 {
					continue
				}
				name := field.Names[0].Name
				goType := exprToString(field.Type)
				jsonTag := extractJSONTag(field.Tag)
				if jsonTag == "-" {
					continue
				}

				required := !hasOmitempty(field.Tag)

				fieldDoc := ""
				if field.Doc != nil {
					fieldDoc = cleanDoc(field.Doc.Text())
				}

				info.Fields = append(info.Fields, configField{
					Name:     name,
					GoType:   goType,
					JSONTag:  jsonTag,
					Required: required,
					Doc:      fieldDoc,
				})
			}
		}
	}
}

func render(buf *bytes.Buffer, sinks []sinkInfo) {
	buf.WriteString("<!-- Code generated by gen-sink-docs. DO NOT EDIT. -->\n\n")
	buf.WriteString("# Notification Sink Reference\n\n")
	buf.WriteString("Each notification sink reads its configuration from a Kubernetes Secret and delivers notifications to an external system.\n")
	buf.WriteString("See the [Notifications Guide](../user-guides/notifications.md) for setup instructions and usage patterns.\n\n")

	// Sink type summary table
	buf.WriteString("## Sink Types\n\n")
	buf.WriteString("| Sink Type | Destination | Protocol |\n")
	buf.WriteString("|-----------|-------------|----------|\n")
	for _, s := range sinks {
		fmt.Fprintf(buf, "| [`%s`](#%s) | %s | HTTPS |\n", s.Type, s.Dir, s.DisplayName)
	}
	buf.WriteString("\n")

	// Render each sink
	for _, s := range sinks {
		renderSink(buf, s)
	}
}

func renderSink(buf *bytes.Buffer, s sinkInfo) {
	fmt.Fprintf(buf, "## %s\n\n", s.DisplayName)
	fmt.Fprintf(buf, "_Sink type: `%s`_\n\n", s.Type)
	fmt.Fprintf(buf, "%s\n\n", s.Description)

	// Config fields table
	if len(s.Fields) > 0 {
		buf.WriteString("### Configuration\n\n")
		buf.WriteString("The configuration must be in a JSON object stored under `config` key in secret reference:\n\n")

		// Render example JSON
		renderExampleJSON(buf, s.Fields)

		buf.WriteString("\n| Field | Type | Required | Description |\n")
		buf.WriteString("|-------|------|----------|-------------|\n")
		for _, f := range s.Fields {
			req := "No"
			if f.Required {
				req = "Yes"
			}
			doc := strings.ReplaceAll(f.Doc, "\n", " ")
			fmt.Fprintf(buf, "| `%s` | `%s` | %s | %s |\n", f.JSONTag, f.GoType, req, doc)
		}
		buf.WriteString("\n")
	}

	// Default template
	if s.DefaultTemplate != "" {
		buf.WriteString("### Default Template\n\n")
		buf.WriteString("```gotpl\n")
		buf.WriteString(strings.TrimSpace(s.DefaultTemplate))
		buf.WriteString("\n```\n\n")
	}

	// Extra sections (e.g., Payload schema for webhook)
	for _, sec := range s.Sections {
		renderExtraSection(buf, sec)
	}
}

func renderExampleJSON(buf *bytes.Buffer, fields []configField) {
	buf.WriteString("```json\n{\n")
	for i, f := range fields {
		placeholder := jsonPlaceholder(f)
		comma := ","
		if i == len(fields)-1 {
			comma = ""
		}
		fmt.Fprintf(buf, "  \"%s\": %s%s\n", f.JSONTag, placeholder, comma)
	}
	buf.WriteString("}\n```\n")
}

func renderExtraSection(buf *bytes.Buffer, sec resolvedSection) {
	fmt.Fprintf(buf, "### %s\n\n", sec.Title)
	buf.WriteString("```json\n")
	renderPayloadJSON(buf, sec.Fields, 0)
	buf.WriteString("\n```\n\n")

	buf.WriteString("| Field | Type | Description |\n")
	buf.WriteString("|-------|------|-------------|\n")
	renderPayloadFieldTable(buf, sec.Fields, "")
	buf.WriteString("\n")
}

// renderPayloadJSON writes a nested JSON example from resolved struct fields.
func renderPayloadJSON(buf *bytes.Buffer, fields []structField, indent int) {
	prefix := strings.Repeat("  ", indent)
	inner := strings.Repeat("  ", indent+1)

	fmt.Fprintf(buf, "%s{\n", prefix)
	for i, f := range fields {
		comma := ","
		if i == len(fields)-1 {
			comma = ""
		}

		switch {
		case len(f.Children) > 0 && f.IsArray:
			fmt.Fprintf(buf, "%s\"%s\": [\n", inner, f.JSONKey)
			renderPayloadJSON(buf, f.Children, indent+2)
			fmt.Fprintf(buf, "\n%s]%s\n", inner, comma)
		case len(f.Children) > 0:
			fmt.Fprintf(buf, "%s\"%s\": ", inner, f.JSONKey)
			// Render child object inline (strip its leading indent).
			var child bytes.Buffer
			renderPayloadJSON(&child, f.Children, indent+1)
			raw := child.String()
			raw = strings.TrimLeft(raw, " ")
			// Remove trailing newline so we can append comma.
			raw = strings.TrimRight(raw, "\n")
			buf.WriteString(raw)
			fmt.Fprintf(buf, "%s\n", comma)
		default:
			p := payloadFieldPlaceholder(f)
			fmt.Fprintf(buf, "%s\"%s\": %s%s\n", inner, f.JSONKey, p, comma)
		}
	}
	fmt.Fprintf(buf, "%s}", prefix)
}

// renderPayloadFieldTable writes a flat Markdown table with dotted paths.
func renderPayloadFieldTable(buf *bytes.Buffer, fields []structField, prefix string) {
	for _, f := range fields {
		key := prefix + f.JSONKey
		typeName := f.GoType

		doc := strings.ReplaceAll(f.Doc, "\n", " ")
		fmt.Fprintf(buf, "| `%s` | `%s` | %s |\n", key, typeName, doc)

		if len(f.Children) > 0 {
			childPrefix := key + "."
			if f.IsArray {
				childPrefix = key + "[]." // e.g., targets[].name
			}
			renderPayloadFieldTable(buf, f.Children, childPrefix)
		}
	}
}

func payloadFieldPlaceholder(f structField) string {
	switch f.GoType {
	case "string":
		return fmt.Sprintf("\"<%s>\"", f.JSONKey)
	case "bool":
		return "false"
	case "int", "int32", "int64":
		return "0"
	default:
		if strings.HasPrefix(f.GoType, "map[") {
			return "{}"
		}
		if strings.HasSuffix(f.GoType, "[]") {
			return "[]"
		}
		return fmt.Sprintf("\"<%s>\"", f.JSONKey)
	}
}

func jsonPlaceholder(f configField) string {
	switch {
	case f.GoType == "bool":
		return "false"
	case f.GoType == "string":
		return fmt.Sprintf("\"<%s>\"", f.JSONTag)
	case strings.HasPrefix(f.GoType, "*"):
		base := strings.TrimPrefix(f.GoType, "*")
		return jsonPlaceholderForType(base, f.JSONTag)
	case strings.HasPrefix(f.GoType, "map[string]string"):
		return "{}"
	default:
		return fmt.Sprintf("\"<%s>\"", f.JSONTag)
	}
}

func jsonPlaceholderForType(goType, jsonTag string) string {
	switch goType {
	case "string":
		return fmt.Sprintf("\"<%s>\"", jsonTag)
	case "bool":
		return "false"
	default:
		return fmt.Sprintf("\"<%s>\"", jsonTag)
	}
}

func exprToString(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + exprToString(t.X)
	case *ast.ArrayType:
		return "[]" + exprToString(t.Elt)
	case *ast.MapType:
		return fmt.Sprintf("map[%s]%s", exprToString(t.Key), exprToString(t.Value))
	case *ast.SelectorExpr:
		return exprToString(t.X) + "." + t.Sel.Name
	default:
		return fmt.Sprintf("%T", expr)
	}
}

func extractJSONTag(tag *ast.BasicLit) string {
	if tag == nil {
		return ""
	}
	re := regexp.MustCompile(`json:"([^"]*)"`)
	m := re.FindStringSubmatch(tag.Value)
	if len(m) < 2 {
		return ""
	}
	parts := strings.Split(m[1], ",")
	return parts[0]
}

func hasOmitempty(tag *ast.BasicLit) bool {
	if tag == nil {
		return false
	}
	re := regexp.MustCompile(`json:"([^"]*)"`)
	m := re.FindStringSubmatch(tag.Value)
	if len(m) < 2 {
		return false
	}
	return strings.Contains(m[1], "omitempty")
}

func cleanDoc(text string) string {
	text = strings.TrimSpace(text)
	lines := strings.Split(text, "\n")
	var result []string
	for _, line := range lines {
		result = append(result, strings.TrimSpace(line))
	}
	return strings.Join(result, " ")
}

// sort.Strings is used for deterministic output when iterating maps.
var _ = sort.Strings

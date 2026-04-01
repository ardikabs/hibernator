/*
Copyright 2026 Ardika Saputro.
Licensed under the Apache License, Version 2.0.
*/

// gen-executor-params-docs generates a Markdown API reference page from the
// Go struct definitions in pkg/executorparams/params.go. It uses the Go AST
// to extract type names, field names, json tags, Go types, and doc comments,
// then renders a Markdown file that matches the style produced by crd-ref-docs
// for the CRD API reference.
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
	"strings"
)

const (
	sourceFile = "pkg/executorparams/params.go"
	outputFile = "website/docs/reference/executor-parameters.md"
)

// executorTypes are the top-level parameter types that map to executor names.
// Order here controls the order in the generated document.
var executorTypes = []struct {
	TypeName     string
	ExecutorName string
}{
	{"EKSParameters", "eks"},
	{"KarpenterParameters", "karpenter"},
	{"EC2Parameters", "ec2"},
	{"RDSParameters", "rds"},
	{"GKEParameters", "gke"},
	{"CloudSQLParameters", "cloudsql"},
	{"WorkloadScalerParameters", "workloadscaler"},
	{"NoOpParameters", "noop"},
}

type structField struct {
	Name    string
	GoType  string
	JSONTag string
	Doc     string
}

type structType struct {
	Name   string
	Doc    string
	Fields []structField
}

func main() {
	root := findModuleRoot()

	fset := token.NewFileSet()
	src := filepath.Join(root, sourceFile)
	f, err := parser.ParseFile(fset, src, nil, parser.ParseComments)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error parsing %s: %v\n", src, err)
		os.Exit(1)
	}

	types := extractStructs(f)

	var buf bytes.Buffer
	render(&buf, types)

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

// extractStructs walks the AST and extracts all struct types with their fields.
func extractStructs(f *ast.File) map[string]*structType {
	types := make(map[string]*structType)

	for _, decl := range f.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.TYPE {
			continue
		}
		for _, spec := range genDecl.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				continue
			}

			doc := ""
			if genDecl.Doc != nil {
				doc = cleanDoc(genDecl.Doc.Text())
			}

			s := &structType{
				Name: ts.Name.Name,
				Doc:  doc,
			}

			for _, field := range st.Fields.List {
				if len(field.Names) == 0 {
					continue // skip embedded
				}
				name := field.Names[0].Name
				goType := exprToString(field.Type)
				jsonTag := extractJSONTag(field.Tag)
				if jsonTag == "-" {
					continue
				}

				fieldDoc := ""
				if field.Doc != nil {
					fieldDoc = cleanDoc(field.Doc.Text())
				}

				s.Fields = append(s.Fields, structField{
					Name:    name,
					GoType:  goType,
					JSONTag: jsonTag,
					Doc:     fieldDoc,
				})
			}

			types[s.Name] = s
		}
	}
	return types
}

// render produces the Markdown output.
func render(buf *bytes.Buffer, types map[string]*structType) {
	buf.WriteString("# Executor Parameters Reference\n\n")
	buf.WriteString("Each executor type accepts a specific parameter schema in the `parameters` field of a HibernatePlan target.\n")
	buf.WriteString("See [API Reference](api.md) for the core CRD types.\n\n")

	// Executor type index
	buf.WriteString("## Executor Types\n\n")
	for _, et := range executorTypes {
		anchor := strings.ToLower(et.TypeName)
		fmt.Fprintf(buf, "- [%s (`type: %s`)](#%s)\n", et.TypeName, et.ExecutorName, anchor)
	}
	buf.WriteString("\n")

	// Track which types we've rendered to avoid duplicates
	rendered := make(map[string]bool)

	// Render each executor's parameter type, followed by its referenced sub-types
	for _, et := range executorTypes {
		st, ok := types[et.TypeName]
		if !ok {
			continue
		}
		renderType(buf, st, types, et.ExecutorName)
		rendered[et.TypeName] = true

		// Collect and render referenced sub-types (breadth-first)
		queue := referencedTypes(st, types)
		for len(queue) > 0 {
			name := queue[0]
			queue = queue[1:]
			if rendered[name] {
				continue
			}
			rendered[name] = true
			sub, ok := types[name]
			if !ok {
				continue
			}
			renderType(buf, sub, types, "")
			queue = append(queue, referencedTypes(sub, types)...)
		}
	}
}

// renderType writes a single struct type section.
func renderType(buf *bytes.Buffer, st *structType, allTypes map[string]*structType, executorName string) {
	fmt.Fprintf(buf, "### %s\n\n", st.Name)

	if executorName != "" {
		fmt.Fprintf(buf, "_Executor type: `%s`_\n\n", executorName)
	}

	if st.Doc != "" {
		fmt.Fprintf(buf, "%s\n\n", st.Doc)
	}

	if len(st.Fields) == 0 {
		return
	}

	buf.WriteString("| Field | Type | Description |\n")
	buf.WriteString("| ----- | ---- | ----------- |\n")
	for _, f := range st.Fields {
		typeStr := renderFieldType(f.GoType, allTypes)
		doc := strings.ReplaceAll(f.Doc, "\n", "<br />")
		fmt.Fprintf(buf, "| `%s` | %s | %s |\n", f.JSONTag, typeStr, doc)
	}
	buf.WriteString("\n")
}

// renderFieldType formats a Go type for Markdown, linking to known struct types.
func renderFieldType(goType string, allTypes map[string]*structType) string {
	// Handle pointer types
	isPtr := strings.HasPrefix(goType, "*")
	base := strings.TrimPrefix(goType, "*")

	// Handle package-qualified types
	if idx := strings.LastIndex(base, "."); idx >= 0 {
		base = base[idx+1:]
	}

	// Handle slice types
	isSlice := strings.HasPrefix(base, "[]")
	elem := strings.TrimPrefix(base, "[]")

	// Check if the element is a known struct type
	display := goType
	if _, ok := allTypes[elem]; ok {
		anchor := strings.ToLower(elem)
		linked := fmt.Sprintf("[%s](#%s)", elem, anchor)
		if isSlice {
			linked = "[]" + linked
		}
		if isPtr {
			linked = "*" + linked
		}
		display = linked
	}

	return fmt.Sprintf("_%s_", display)
}

// referencedTypes returns type names referenced by a struct's fields that exist in allTypes.
func referencedTypes(st *structType, allTypes map[string]*structType) []string {
	var refs []string
	for _, f := range st.Fields {
		name := extractTypeName(f.GoType)
		if _, ok := allTypes[name]; ok && name != st.Name {
			refs = append(refs, name)
		}
	}
	return refs
}

// extractTypeName extracts the base type name from a Go type expression.
func extractTypeName(goType string) string {
	s := strings.TrimPrefix(goType, "*")
	s = strings.TrimPrefix(s, "[]")
	if idx := strings.LastIndex(s, "."); idx >= 0 {
		s = s[idx+1:]
	}
	return s
}

// exprToString converts an AST type expression to a readable string.
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

// extractJSONTag extracts the json tag name from a struct tag.
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

// cleanDoc cleans up Go doc comment text for Markdown output.
func cleanDoc(text string) string {
	text = strings.TrimSpace(text)
	// Collapse single newlines (within a paragraph) to spaces,
	// but preserve double newlines (paragraph breaks) as <br />.
	lines := strings.Split(text, "\n")
	var result []string
	for _, line := range lines {
		result = append(result, strings.TrimSpace(line))
	}
	return strings.Join(result, "<br />")
}

package ui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// views.go is the only thing that makes a view's init() run in the program, and
// nothing else in this package can hold it to that: every sweep here imports each
// view package by name, so a view left out of views.go registers in this test
// binary and in no build anybody ships. That is the one mistake a wiring packet
// can make and get a green suite for, so the list is discovered from the tree
// rather than written down twice.
func TestViews_EveryPackageThatRegistersIsImportedByTheProgram(t *testing.T) {
	imported := blankImports(t, "views.go")
	if len(imported) == 0 {
		t.Fatal("views.go imports no view package, so this is checking nothing")
	}
	found := 0
	for _, dir := range subPackages(t) {
		if !registers(t, dir) {
			continue
		}
		found++
		if !slices.Contains(imported, dir) {
			t.Errorf("internal/ui/%s registers itself with the kernel and views.go does not import it, "+
				"so its init() runs in this test binary and never in the program", dir)
		}
	}
	if found == 0 {
		t.Fatal("no package under internal/ui was found to register anything, so this is checking nothing")
	}
	for _, dir := range imported {
		if !registers(t, dir) {
			t.Errorf("views.go imports internal/ui/%s, which registers nothing; a blank import that "+
				"buys no registration is a line nobody can check", dir)
		}
	}
}

// blankImports is the packages under internal/ui that a file imports, by
// directory name.
func blankImports(t *testing.T, file string) []string {
	t.Helper()

	parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parsing %s: %v", file, err)
	}
	const prefix = "github.com/varijkapil13/saral/internal/ui/"
	var out []string
	for _, spec := range parsed.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			t.Fatalf("%s imports %s, which is not a quoted path: %v", file, spec.Path.Value, err)
		}
		if dir, under := strings.CutPrefix(path, prefix); under {
			out = append(out, dir)
		}
	}
	return out
}

func subPackages(t *testing.T) []string {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	return out
}

// registers reports whether a package calls anything on the kernel's registry.
// It reads the syntax rather than the bytes: a package that only mentions
// RegisterView in a comment registers nothing.
func registers(t *testing.T, dir string) bool {
	t.Helper()

	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", file, err)
		}
		found := false
		ast.Inspect(parsed, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return !found
			}
			pkg, named := sel.X.(*ast.Ident)
			if named && pkg.Name == "kernel" && strings.HasPrefix(sel.Sel.Name, "Register") {
				found = true
			}
			return !found
		})
		if found {
			return true
		}
	}
	return false
}

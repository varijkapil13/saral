package arch

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// portDir is the package the adapters under it adapt.
const portDir = "pkg/jira"

// unionRole is the composite a session is built with, and the one line an
// adapter may not be without.
const unionRole = "SessionClient"

// An adapter package has to say which of the port's interfaces it satisfies, and
// one of them has to be the composite.
//
// Whether an assertion is true is the compiler's job: one
// `var _ jira.Prober = (*Client)(nil)` fails the build the moment the type stops
// satisfying the role. What no compiler checks is whether anybody wrote one, and
// presence alone would let a package drop nine of ten claims and still pass —
// which is why the composite is required by name. Nothing that satisfies it can
// have dropped a role inside it.
//
// The scan skips _test.go files: an assertion only a test file carries fails
// `go test` and not `go build`.
func TestAdapters_StateWhichPortRolesTheySatisfy(t *testing.T) {
	t.Parallel()

	root := moduleRoot(t)
	modPath := modulePath(t, root)

	roles := portInterfaces(t, root)
	if len(roles) == 0 {
		t.Fatalf("no exported interface found in %s, so this check proves nothing", portDir)
	}
	if !slices.Contains(roles, "Client") {
		t.Errorf("the port declares no Client interface; roles found: %v", roles)
	}
	if !slices.Contains(roles, unionRole) {
		t.Fatalf("the port declares no %s interface, so there is no composite to require; roles found: %v",
			unionRole, roles)
	}

	adapters := adapterPackages(t, root)
	if len(adapters) == 0 {
		t.Fatalf("no adapter package found under %s, so this check proves nothing", portDir)
	}

	for _, dir := range adapters {
		t.Run(dir, func(t *testing.T) {
			t.Parallel()

			asserted := assertedRoles(t, filepath.Join(root, filepath.FromSlash(dir)), modPath)
			if len(asserted) == 0 {
				t.Fatalf("%s adapts %s and never says what it satisfies.\n"+
					"add one line per role in a non-test file of the package, e.g.\n"+
					"\tvar _ jira.Prober = (*Client)(nil)\n"+
					"without it the package compiles while implementing none of the port, "+
					"and the first thing to notice is whatever tries to use it",
					dir, portDir)
			}
			for _, name := range asserted {
				if !slices.Contains(roles, name) {
					t.Errorf("%s asserts it satisfies jira.%s, which %s does not declare; it declares %v",
						dir, name, portDir, roles)
				}
			}
			if !slices.Contains(asserted, unionRole) {
				t.Errorf("%s asserts %v and not jira.%s.\n"+
					"the composite is what a session is built with, so an adapter that does not claim it "+
					"cannot be wired in, and claiming it is what stops the single-role lines from being "+
					"quietly dropped one at a time",
					dir, asserted, unionRole)
			}
		})
	}
}

func TestAssertionsIn_FindsThePortRolesAPackageClaimsAndNothingElse(t *testing.T) {
	t.Parallel()

	const modPath = "example.com/mod"

	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "one assertion under the usual import name",
			src: `package cloud
import "example.com/mod/pkg/jira"
var _ jira.Prober = (*Client)(nil)`,
			want: []string{"Prober"},
		},
		{
			name: "a grouped var block",
			src: `package cloud
import "example.com/mod/pkg/jira"
var (
	_ jira.Prober = (*Client)(nil)
	_ jira.Searcher = (*Client)(nil)
)`,
			want: []string{"Prober", "Searcher"},
		},
		{
			name: "an aliased import",
			src: `package cloud
import port "example.com/mod/pkg/jira"
var _ port.Commenter = (*Client)(nil)`,
			want: []string{"Commenter"},
		},
		{
			name: "a package that never mentions the port",
			src: `package cloud
var _ error = (*Client)(nil)`,
			want: nil,
		},
		{
			name: "a named variable rather than a blank assertion",
			src: `package cloud
import "example.com/mod/pkg/jira"
var fallback jira.Prober = (*Client)(nil)`,
			want: nil,
		},
		{
			name: "a selector on something that merely shares the name",
			src: `package cloud
import "example.com/mod/pkg/jira"
import jira2 "example.com/other/jira"
var _ jira2.Prober = (*Client)(nil)`,
			want: nil,
		},
		{
			name: "an unexported interface is not a role",
			src: `package cloud
import "example.com/mod/pkg/jira"
var _ jira.prober = (*Client)(nil)`,
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "src.go", tt.src, 0)
			if err != nil {
				t.Fatalf("parsing the fixture: %v", err)
			}
			got := assertionsIn(file, modPath)
			slices.Sort(got)
			want := slices.Clone(tt.want)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Errorf("assertionsIn = %v, want %v", got, want)
			}
		})
	}
}

// portInterfaces lists the exported interfaces the port declares, which is the
// set an adapter may claim to satisfy.
func portInterfaces(t *testing.T, root string) []string {
	t.Helper()

	fset := token.NewFileSet()
	var out []string
	for _, file := range goFiles(t, filepath.Join(root, filepath.FromSlash(portDir))) {
		parsed, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", file, err)
		}
		for _, decl := range parsed.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.TYPE {
				continue
			}
			for _, spec := range gen.Specs {
				typ, ok := spec.(*ast.TypeSpec)
				if !ok || !typ.Name.IsExported() {
					continue
				}
				if _, isInterface := typ.Type.(*ast.InterfaceType); isInterface {
					out = append(out, typ.Name.Name)
				}
			}
		}
	}
	slices.Sort(out)
	return out
}

// adapterPackages lists the module-relative directories directly under the port
// that hold Go code. Those are the packages whose whole job is to be the port.
func adapterPackages(t *testing.T, root string) []string {
	t.Helper()

	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(portDir)))
	if err != nil {
		t.Fatalf("reading %s: %v", portDir, err)
	}
	var out []string
	for _, entry := range entries {
		if !entry.IsDir() || skipDir(entry.Name()) {
			continue
		}
		dir := path.Join(portDir, entry.Name())
		if len(goFiles(t, filepath.Join(root, filepath.FromSlash(dir)))) > 0 {
			out = append(out, dir)
		}
	}
	return out
}

func assertedRoles(t *testing.T, dir, modPath string) []string {
	t.Helper()

	files := goFiles(t, dir)
	fset := token.NewFileSet()
	out := make([]string, 0, len(files))
	for _, file := range files {
		parsed, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", file, err)
		}
		out = append(out, assertionsIn(parsed, modPath)...)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// assertionsIn reads the port roles a file claims through `var _ jira.X = ...`.
// A named variable is not a claim — only the blank one is unmistakably written
// to be checked and never read.
func assertionsIn(file *ast.File, modPath string) []string {
	alias, ok := portAlias(file, modPath)
	if !ok {
		return nil
	}
	var out []string
	for _, decl := range file.Decls {
		gen, isGen := decl.(*ast.GenDecl)
		if !isGen || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			value, isValue := spec.(*ast.ValueSpec)
			if !isValue || len(value.Names) != 1 || value.Names[0].Name != "_" {
				continue
			}
			selector, isSelector := value.Type.(*ast.SelectorExpr)
			if !isSelector {
				continue
			}
			pkg, isIdent := selector.X.(*ast.Ident)
			if !isIdent || pkg.Name != alias || !selector.Sel.IsExported() {
				continue
			}
			out = append(out, selector.Sel.Name)
		}
	}
	return out
}

// portAlias is the name the port is imported under in this file, if it is.
func portAlias(file *ast.File, modPath string) (string, bool) {
	want := modPath + "/" + portDir
	for _, spec := range file.Imports {
		imported, err := strconv.Unquote(spec.Path.Value)
		if err != nil || imported != want {
			continue
		}
		if spec.Name != nil {
			return spec.Name.Name, spec.Name.Name != "_" && spec.Name.Name != "."
		}
		return path.Base(imported), true
	}
	return "", false
}

func goFiles(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	var out []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		out = append(out, filepath.Join(dir, name))
	}
	return out
}

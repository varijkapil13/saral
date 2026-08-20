package arch

import (
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

type importRule struct {
	name   string
	from   string   // module-relative package dir the rule covers; "" covers every package
	forbid string   // module-relative import path the rule forbids, matched per path segment
	except []string // package dirs the rule does not apply to
	why    string
}

var importRules = []importRule{
	{
		name:   "pkg-must-not-import-internal",
		from:   "pkg",
		forbid: "internal",
		why:    "pkg/** is the reusable half of the tree and must build without the application",
	},
	{
		name:   "ui-must-not-import-the-cloud-adapter",
		from:   "internal/ui",
		forbid: "pkg/jira/cloud",
		why:    "views take the jira.Client port so they can be driven by pkg/jira/jiratest",
	},
	{
		name:   "only-cmd-and-config-construct-the-cloud-adapter",
		from:   "",
		forbid: "pkg/jira/cloud",
		// The adapter is exempt from its own rule so that its black-box tests can import it.
		except: []string{"cmd", "internal/config", "pkg/jira/cloud"},
		why:    "the concrete adapter is wired once, at the composition root",
	},
	{
		name:   "adf-must-not-import-jira",
		from:   "pkg/adf",
		forbid: "pkg/jira",
		why:    "pkg/adf is an independent document library; the dependency runs the other way",
	},
}

func underPath(p, prefix string) bool {
	if prefix == "" {
		return true
	}
	return p == prefix || strings.HasPrefix(p, prefix+"/")
}

func (r importRule) broken(pkgDir, importPath string) bool {
	if !underPath(pkgDir, r.from) || !underPath(importPath, r.forbid) {
		return false
	}
	for _, exempt := range r.except {
		if underPath(pkgDir, exempt) {
			return false
		}
	}
	return true
}

func brokenRules(pkgDir, importPath string) []importRule {
	out := make([]importRule, 0, len(importRules))
	for _, r := range importRules {
		if r.broken(pkgDir, importPath) {
			out = append(out, r)
		}
	}
	return out
}

func TestImports_ObeyTheLayeringInTheArchitectureDoc(t *testing.T) {
	t.Parallel()

	root := moduleRoot(t)
	modPath := modulePath(t, root)

	fset := token.NewFileSet()
	scanned, portFiles := 0, 0

	walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root && skipDir(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}

		rel, err := relativePath(root, path)
		if err != nil {
			return err
		}
		pkgDir := dirOf(rel)
		scanned++
		if pkgDir == "pkg/jira" {
			portFiles++
		}

		parsed, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return fmt.Errorf("parsing %s: %w", rel, err)
		}
		for _, spec := range parsed.Imports {
			imported, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return fmt.Errorf("%s: reading import %s: %w", rel, spec.Path.Value, err)
			}
			local, ok := strings.CutPrefix(imported, modPath+"/")
			if !ok {
				continue
			}
			for _, rule := range brokenRules(pkgDir, local) {
				t.Errorf("%s:%d imports %s, which breaks the rule %q: %s\n"+
					"the layering is described in docs/ARCHITECTURE.md and enforced in internal/arch/imports_test.go",
					rel, fset.Position(spec.Pos()).Line, imported, rule.name, rule.why)
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walking %s: %v", root, walkErr)
	}
	if scanned == 0 || portFiles == 0 {
		t.Fatalf("scanned %d Go files under %s, %d of them in pkg/jira: the walk found nothing, so this check proves nothing",
			scanned, root, portFiles)
	}
}

func TestBrokenRules_MatchTheOffendingPackagesAndNothingElse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		pkgDir  string
		imports string
		want    []string
	}{
		{
			name:    "the port reaching into the application",
			pkgDir:  "pkg/jira",
			imports: "internal/store",
			want:    []string{"pkg-must-not-import-internal"},
		},
		{
			name:    "an adapter reaching into the application",
			pkgDir:  "pkg/jira/cloud",
			imports: "internal/config",
			want:    []string{"pkg-must-not-import-internal"},
		},
		{
			name:    "a view constructing the cloud adapter",
			pkgDir:  "internal/ui/board",
			imports: "pkg/jira/cloud",
			want: []string{
				"only-cmd-and-config-construct-the-cloud-adapter",
				"ui-must-not-import-the-cloud-adapter",
			},
		},
		{
			name:    "a view taking the port",
			pkgDir:  "internal/ui/board",
			imports: "pkg/jira",
			want:    nil,
		},
		{
			name:    "the composition root wiring the adapter",
			pkgDir:  "cmd/saral",
			imports: "pkg/jira/cloud",
			want:    nil,
		},
		{
			name:    "config building a client",
			pkgDir:  "internal/config",
			imports: "pkg/jira/cloud",
			want:    nil,
		},
		{
			name:    "the adapter's own black-box test importing it",
			pkgDir:  "pkg/jira/cloud",
			imports: "pkg/jira/cloud",
			want:    nil,
		},
		{
			name:    "the fake depending on the real adapter",
			pkgDir:  "pkg/jira/jiratest",
			imports: "pkg/jira/cloud",
			want:    []string{"only-cmd-and-config-construct-the-cloud-adapter"},
		},
		{
			name:    "adf depending on jira",
			pkgDir:  "pkg/adf",
			imports: "pkg/jira",
			want:    []string{"adf-must-not-import-jira"},
		},
		{
			name:    "jira depending on adf",
			pkgDir:  "pkg/jira",
			imports: "pkg/adf",
			want:    nil,
		},
		{
			name:    "the store importing the ui",
			pkgDir:  "internal/store",
			imports: "internal/ui/kernel",
			want:    nil,
		},
		{
			name:    "a package whose name merely starts with an exempt one",
			pkgDir:  "internal/configuration",
			imports: "pkg/jira/cloud",
			want:    []string{"only-cmd-and-config-construct-the-cloud-adapter"},
		},
		{
			name:    "a package whose name merely starts with internal/ui",
			pkgDir:  "internal/uix",
			imports: "pkg/jira/cloud",
			want:    []string{"only-cmd-and-config-construct-the-cloud-adapter"},
		},
		{
			name:    "an import whose path merely starts with the forbidden one",
			pkgDir:  "internal/ui/board",
			imports: "pkg/jira/cloudy",
			want:    nil,
		},
		{
			name:    "a package at the module root",
			pkgDir:  "",
			imports: "pkg/jira/cloud",
			want:    []string{"only-cmd-and-config-construct-the-cloud-adapter"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := make([]string, 0, len(importRules))
			for _, rule := range brokenRules(tt.pkgDir, tt.imports) {
				got = append(got, rule.name)
			}
			slices.Sort(got)
			want := slices.Clone(tt.want)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Errorf("%s importing %s breaks %v, want %v", tt.pkgDir, tt.imports, got, want)
			}
		})
	}
}

func TestUnderPath_MatchesWholeSegmentsOnly(t *testing.T) {
	t.Parallel()

	tests := []struct {
		path, prefix string
		want         bool
	}{
		{path: "pkg/jira", prefix: "", want: true},
		{path: "", prefix: "", want: true},
		{path: "pkg", prefix: "pkg", want: true},
		{path: "pkg/jira/cloud", prefix: "pkg", want: true},
		{path: "pkgs/jira", prefix: "pkg", want: false},
		{path: "pk", prefix: "pkg", want: false},
		{path: "", prefix: "pkg", want: false},
		{path: "internal/ui", prefix: "internal/ui", want: true},
		{path: "internal/uikit", prefix: "internal/ui", want: false},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%q under %q", tt.path, tt.prefix), func(t *testing.T) {
			t.Parallel()

			if got := underPath(tt.path, tt.prefix); got != tt.want {
				t.Errorf("underPath(%q, %q) = %t, want %t", tt.path, tt.prefix, got, tt.want)
			}
		})
	}
}

func skipDir(name string) bool {
	return name == "testdata" || name == "vendor" ||
		strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

func relativePath(root, file string) (string, error) {
	rel, err := filepath.Rel(root, file)
	if err != nil {
		return "", fmt.Errorf("locating %s below %s: %w", file, root, err)
	}
	return filepath.ToSlash(rel), nil
}

func dirOf(relPath string) string {
	cut := strings.LastIndex(relPath, "/")
	if cut < 0 {
		return ""
	}
	return relPath[:cut]
}

func moduleRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	for {
		_, err := os.Stat(filepath.Join(dir, "go.mod"))
		if err == nil {
			return dir
		}
		if !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("looking for go.mod in %s: %v", dir, err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod in any parent of the working directory")
		}
		dir = parent
	}
}

func modulePath(t *testing.T, root string) string {
	t.Helper()

	gomod := filepath.Join(root, "go.mod")
	content, err := os.ReadFile(gomod)
	if err != nil {
		t.Fatalf("reading %s: %v", gomod, err)
	}
	for line := range strings.SplitSeq(string(content), "\n") {
		if path, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.TrimSpace(path)
		}
	}
	t.Fatalf("no module line in %s", gomod)
	return ""
}

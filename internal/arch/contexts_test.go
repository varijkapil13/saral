package arch

import (
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

const appDir = "internal/app"

// sharedKernel is the closed list of packages under internal/app that every
// context may import, each with why it is shared rather than owned.
var sharedKernel = map[string]string{
	"cache":    "the disk cache, its kinds, TTLs and codec, and the only package here that imports internal/store",
	"draft":    "the file-backed store under the comment and issue drafts: safe names, atomic writes, the legacy move",
	"issueref": "issue key and URL parsing",
	"match":    "the fuzzy pattern",
	"query":    "the coalescing search runner and the projections every context reads with",
	"term":     "the filter-term model and the in-memory match a board, a backlog and a timeline share",
}

type appPart uint8

const (
	appKernel appPart = iota + 1
	appContext
)

type appPkg struct {
	part appPart
	name string
}

func classifyApp(dir string) (appPkg, bool) {
	rest, ok := strings.CutPrefix(dir, appDir+"/")
	if !ok {
		return appPkg{}, false
	}
	name, _, _ := strings.Cut(rest, "/")
	if _, shared := sharedKernel[name]; shared {
		return appPkg{part: appKernel, name: name}, true
	}
	return appPkg{part: appContext, name: name}, true
}

type contextRule struct {
	name   string
	broken func(from, to appPkg) bool
	why    string
}

var contextRules = []contextRule{
	{
		name: "a-context-imports-no-other-context",
		broken: func(from, to appPkg) bool {
			return from.part == appContext && to.part == appContext && from.name != to.name
		},
		why: "contexts are drained and changed in parallel, so none may lean on another; " +
			"what two of them share belongs in the shared kernel",
	},
	{
		name:   "the-shared-kernel-imports-no-context",
		broken: func(from, to appPkg) bool { return from.part == appKernel && to.part == appContext },
		why:    "the shared kernel sits beneath every context, so reaching up into one ties every context to it",
	},
}

func brokenContextRules(pkgDir, importPath string) []contextRule {
	from, ok := classifyApp(pkgDir)
	if !ok {
		return nil
	}
	to, ok := classifyApp(importPath)
	if !ok {
		return nil
	}
	var out []contextRule
	for _, r := range contextRules {
		if r.broken(from, to) {
			out = append(out, r)
		}
	}
	return out
}

func appLayout(t *testing.T, root string) (rootFiles, contexts []string) {
	t.Helper()

	dir := filepath.Join(root, filepath.FromSlash(appDir))
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", appDir, err)
	}
	for _, entry := range entries {
		switch {
		case entry.IsDir():
			if _, shared := sharedKernel[entry.Name()]; skipDir(entry.Name()) || shared {
				continue
			}
			if hasNonTestGo(t, filepath.Join(dir, entry.Name())) {
				contexts = append(contexts, entry.Name())
			}
		case strings.HasSuffix(entry.Name(), ".go"):
			rootFiles = append(rootFiles, entry.Name())
		}
	}
	return rootFiles, contexts
}

func isNonTestGo(name string) bool {
	return strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go")
}

func hasNonTestGo(t *testing.T, dir string) bool {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	return slices.ContainsFunc(entries, func(e fs.DirEntry) bool { return !e.IsDir() && isNonTestGo(e.Name()) })
}

func TestAppContexts_TheSharedKernelIsWellFormed(t *testing.T) {
	t.Parallel()

	if len(sharedKernel) == 0 {
		t.Fatal("the shared kernel is empty, so nothing here proves the kernel rules hold for anything")
	}
	for name, why := range sharedKernel {
		switch {
		case name == "" || strings.Contains(name, "/"):
			t.Errorf("shared-kernel entry %q is not a direct subpackage name of %s", name, appDir)
		case strings.TrimSpace(why) == "":
			t.Errorf("shared-kernel entry %q gives no reason: adding to the kernel is a decision with one", name)
		}
	}
	for _, rule := range contextRules {
		if rule.name == "" || rule.why == "" || rule.broken == nil {
			t.Errorf("context rule %q is missing its name, its why or its predicate", rule.name)
		}
	}
}

func TestAppRoot_HoldsNoGo(t *testing.T) {
	t.Parallel()

	rootFiles, contexts := appLayout(t, moduleRoot(t))
	if len(contexts) == 0 {
		t.Fatalf("found no contexts under %s: the scan found nothing, so this check proves nothing", appDir)
	}
	for _, f := range rootFiles {
		t.Errorf("%s/%s exists, but %s is a directory of contexts and the shared kernel, not a package: "+
			"put it in a context or the shared kernel (docs/ARCHITECTURE.md, Bounded contexts)", appDir, f, appDir)
	}
}

func TestAppContexts_ImportOnlyTheSharedKernel(t *testing.T) {
	t.Parallel()

	root := moduleRoot(t)
	modPath := modulePath(t, root)
	_, contexts := appLayout(t, root)
	if len(contexts) == 0 {
		t.Fatalf("found no contexts under %s: at least board exists, so the discovery is broken", appDir)
	}

	fset := token.NewFileSet()
	scanned := 0
	walkErr := filepath.WalkDir(filepath.Join(root, filepath.FromSlash(appDir)), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if skipDir(entry.Name()) {
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
		scanned++
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
			for _, rule := range brokenContextRules(dirOf(rel), local) {
				t.Errorf("%s:%d imports %s, which breaks the rule %q: %s\n"+
					"the contexts are described in docs/ARCHITECTURE.md and enforced in internal/arch/contexts_test.go",
					rel, fset.Position(spec.Pos()).Line, imported, rule.name, rule.why)
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walking %s: %v", appDir, walkErr)
	}
	if scanned == 0 {
		t.Fatalf("scanned no Go files under %s, so this check proves nothing", appDir)
	}
}

func TestBrokenContextRules_MatchTheOffendingImportsAndNothingElse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		pkgDir  string
		imports string
		want    []string
	}{
		{"a context importing another", "internal/app/board", "internal/app/issue", []string{"a-context-imports-no-other-context"}},
		{"a context's subpackage importing another context", "internal/app/board/lanes", "internal/app/issue", []string{"a-context-imports-no-other-context"}},
		{"a context whose name merely starts with another's", "internal/app/board", "internal/app/boardx", []string{"a-context-imports-no-other-context"}},
		{"a context importing its own subpackage", "internal/app/board", "internal/app/board/lanes", nil},
		{"a context importing the shared kernel", "internal/app/board", "internal/app/cache", nil},
		{"a context importing the draft kernel", "internal/app/comment", "internal/app/draft", nil},
		{"the draft kernel importing a context", "internal/app/draft", "internal/app/issue", []string{"the-shared-kernel-imports-no-context"}},
		{"a context taking the port", "internal/app/board", "pkg/jira", nil},
		{"the shared kernel importing a context", "internal/app/cache", "internal/app/board", []string{"the-shared-kernel-imports-no-context"}},
		{"the shared kernel importing itself", "internal/app/cache", "internal/app/match", nil},
		{"a view driving a context", "internal/ui/board", "internal/app/board", nil},
		{"a package whose name merely starts with internal/app", "internal/apps/x", "internal/app/board", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got []string
			for _, rule := range brokenContextRules(tt.pkgDir, tt.imports) {
				got = append(got, rule.name)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("%s importing %s breaks %v, want %v", tt.pkgDir, tt.imports, got, tt.want)
			}
		})
	}
}

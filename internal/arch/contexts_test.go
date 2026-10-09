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

var sharedKernel = []string{"cache", "issueref", "match", "query", "term"}

var legacyRootFiles = []string{}

type appPart uint8

const (
	appRoot appPart = iota + 1
	appKernel
	appContext
)

type appPkg struct {
	part appPart
	name string
}

func classifyApp(dir string) (appPkg, bool) {
	if dir == appDir {
		return appPkg{part: appRoot}, true
	}
	rest, ok := strings.CutPrefix(dir, appDir+"/")
	if !ok {
		return appPkg{}, false
	}
	name, _, _ := strings.Cut(rest, "/")
	if slices.Contains(sharedKernel, name) {
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
		broken: func(from, to appPkg) bool { return from.part == appKernel && to.part != appKernel },
		why: "the shared kernel sits beneath every context, so reaching up into one, or into the legacy root, " +
			"ties every context to it",
	},
	{
		name:   "the-legacy-root-imports-no-context",
		broken: func(from, to appPkg) bool { return from.part == appRoot && to.part == appContext },
		why: "the root is being drained into the contexts, which may still import it while they migrate, " +
			"so importing one back is a cycle in waiting; new code goes into the context instead",
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
			if skipDir(entry.Name()) || slices.Contains(sharedKernel, entry.Name()) {
				continue
			}
			if hasNonTestGo(t, filepath.Join(dir, entry.Name())) {
				contexts = append(contexts, entry.Name())
			}
		case isNonTestGo(entry.Name()):
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

	seen := map[string]bool{}
	for _, name := range sharedKernel {
		switch {
		case name == "" || strings.Contains(name, "/"):
			t.Errorf("shared-kernel entry %q is not a direct subpackage name of %s", name, appDir)
		case seen[name]:
			t.Errorf("shared-kernel entry %q is listed twice", name)
		}
		seen[name] = true
	}
	for _, rule := range contextRules {
		if rule.name == "" || rule.why == "" || rule.broken == nil {
			t.Errorf("context rule %q is missing its name, its why or its predicate", rule.name)
		}
	}
}

func TestAppRoot_OnlyShrinks(t *testing.T) {
	t.Parallel()

	rootFiles, contexts := appLayout(t, moduleRoot(t))
	if len(rootFiles) == 0 && len(contexts) == 0 {
		t.Fatalf("found no Go files in the %s root and no contexts under it: the scan found nothing, "+
			"so this check proves nothing", appDir)
	}

	for _, f := range rootFiles {
		if slices.Contains(legacyRootFiles, f) {
			continue
		}
		if len(legacyRootFiles) == 0 {
			t.Errorf("%s/%s exists, but the legacy root has been emptied and must stay empty: "+
				"put it in a context or the shared kernel", appDir, f)
			continue
		}
		t.Errorf("%s/%s is not on legacyRootFiles: the root is legacy and only shrinks, so new code goes "+
			"into a context (docs/ARCHITECTURE.md, Bounded contexts)", appDir, f)
	}
	for _, f := range legacyRootFiles {
		if !slices.Contains(rootFiles, f) {
			t.Errorf("legacyRootFiles names %s/%s, which no longer exists: delete it from the list "+
				"in internal/arch/contexts_test.go so the list stays the truth", appDir, f)
		}
	}
}

func TestAppContexts_ImportOnlyTheSharedKernelAndTheLegacyRoot(t *testing.T) {
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
		{"a context importing the legacy root while it migrates", "internal/app/board", "internal/app", nil},
		{"a context taking the port", "internal/app/board", "pkg/jira", nil},
		{"the shared kernel importing a context", "internal/app/cache", "internal/app/board", []string{"the-shared-kernel-imports-no-context"}},
		{"the shared kernel importing the legacy root", "internal/app/match", "internal/app", []string{"the-shared-kernel-imports-no-context"}},
		{"the shared kernel importing itself", "internal/app/cache", "internal/app/match", nil},
		{"the legacy root importing a context", "internal/app", "internal/app/board", []string{"the-legacy-root-imports-no-context"}},
		{"the legacy root importing the shared kernel", "internal/app", "internal/app/cache", nil},
		{"the legacy root taking the port", "internal/app", "pkg/jira", nil},
		{"a view driving a context", "internal/ui/board", "internal/app/board", nil},
		{"a view driving the legacy root", "internal/ui/board", "internal/app", nil},
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

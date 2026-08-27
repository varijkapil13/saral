package arch

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/varijkapil13/saral/pkg/jira"
)

// A conformance table runs one set of assertions over both adapters. What no
// table can do is notice that a method has none: everything above the port is
// tested against the fake, so a rule the cloud adapter enforces and the fake
// does not is a rule no test meets, and the method that has no table at all is
// the method where that is certain.
//
// So the port is enumerated here and the tables are counted against it. A method
// that is neither covered nor on the list below fails, and the list is the
// inventory of the debt: an entry leaves it when a packet writes the table, one
// at a time, and an entry that outlives its table fails too.

// The names a conformance table's test function carries. Both adapters, or
// neither — a rule both refuse is as much a shared assertion as a value both
// return.
var conformanceMarkers = []string{"BothAdapters", "NeitherAdapter"}

// noCaseYet is every port method no conformance table covers, with why. It is
// debt and not a policy: each entry names what a table for that method would
// have to establish, so writing one is a packet and not an investigation.
//
// Nothing is exempt for being hard. An entry is deleted in the same change that
// adds the table, and this test fails on an entry whose method has since been
// covered, so the list cannot quietly outlive the gap it describes.
var noCaseYet = map[string]string{
	"Capabilities": "the probe folds six reads and their refusals into a reason per capability, and " +
		"jiratest states capabilities as options rather than deriving them from anything — so a table " +
		"has to compare the reasons a 403 produces, not the flags",
	"Issue": "one issue read whole is the widest decode in the adapter, and the fake builds an issue " +
		"rather than decoding one; a table has to assert the field mask and the shape of every value kind",
	"CreateIssue": "the write path: what a create sends for each field kind, and which refusals come " +
		"back as *jira.ValidationError against a field id rather than as prose",
	"UpdateIssue": "a sparse patch has to leave out what it does not name, and Clear has to send null; " +
		"a table has to prove the omitted field is untouched on both sides",
	"Transitions": "the list is per issue, per token and expires, and the fake's is derived from its own " +
		"workflow; a table has to compare what a screened transition states about its fields",
	"Transition": "a transition screen's required fields are not what the read said they were, so the " +
		"case set is about the refusal that arrives after the write",
	"Comments":      "the platform comment envelope has no isLast and the walk ends on a total; a table has to compare where the two adapters think a thread ends",
	"AddComment":    "an ADF body goes out and comes back re-serialised; a table has to compare what survives the round trip",
	"EditComment":   "the same, plus the refusal for a comment this token did not write",
	"DeleteComment": "the only comment method whose success is a 204 with no body, and the fake answers nothing at all",
	"Fields":        "the catalogue is an unpaged bare array on one side and a fixed list on the other; a table has to compare what a custom field's schema says",
	"MoveToSprint": "MoveToBacklog has a table and this does not, which is the pair that should have been " +
		"written together: the case set is the one that moves an issue out of a sprint it is already in",
}

// The two things this guard does not prove, said wherever it fails. A test that
// overstates what it holds is worse than none: the next person reads a green
// suite as an answer to a question it never asked.
const conformanceLimits = "This counts tables, not assertions. It proves that a conformance file " +
	"names the method and runs something against both adapters; it does not prove the two are asserted " +
	"to answer the same way, that the case set is complete — the normal answer, the well-formed empty " +
	"one, and each typed error the method can produce — or that the call site is more than setup for " +
	"another method's case."

func TestConformance_EveryPortMethodHasATableOrAnEntrySayingWhyNot(t *testing.T) {
	t.Parallel()

	methods := portMethods()
	if len(methods) == 0 {
		t.Fatal("reflection found no methods on jira.Client, so this check proves nothing")
	}

	files, covered := conformanceCoverage(t, moduleRoot(t), methods)
	if len(files) == 0 {
		t.Fatalf("no conformance table was found anywhere in the tree. A file counts when its name "+
			"carries %q and it holds a test function naming %s; without one this check proves nothing",
			"conformance", strings.Join(conformanceMarkers, " or "))
	}
	if len(covered) == 0 {
		t.Fatalf("the %d conformance file(s) found — %v — call no method of jira.Client, so either they "+
			"stopped being conformance tables or this scan stopped finding call sites",
			len(files), files)
	}

	for _, name := range methods {
		if slices.Contains(covered, name) {
			continue
		}
		why, exempt := noCaseYet[name]
		switch {
		case !exempt:
			t.Errorf("jira.%s has no conformance case and no entry saying why not.\n"+
				"Write the table beside the others — docs/TESTING.md says how — or add jira.%s to "+
				"noCaseYet in this file with the reason, which is how this repo says out loud that "+
				"the method is tested against the fake alone.\n\t%s", name, name, conformanceLimits)
		case strings.TrimSpace(why) == "":
			t.Errorf("noCaseYet exempts jira.%s with no reason. The list is the inventory of what is "+
				"untested across the two adapters, and an entry that says nothing makes it a list of "+
				"names instead", name)
		}
	}

	for name, why := range noCaseYet {
		switch {
		case !slices.Contains(methods, name):
			t.Errorf("noCaseYet exempts jira.%s and the port declares no such method; it declares %v",
				name, methods)
		case slices.Contains(covered, name):
			t.Errorf("noCaseYet still exempts jira.%s and %v covers it now. Delete the entry: an "+
				"exemption that outlives its gap makes the next method to lose its table invisible.\n"+
				"\tthe entry reads: %s", name, covered, why)
		}
	}
}

// The exemption list is read by name against the port, so a method renamed in
// pkg/jira leaves an entry pointing at nothing — which the test above reports.
// This one holds the other half: the list has to be the shape that test can read.
func TestConformance_TheExemptionListIsWellFormed(t *testing.T) {
	t.Parallel()

	if len(noCaseYet) == 0 {
		t.Log("every port method has a conformance table; delete noCaseYet and the branches that read it")
		return
	}
	for name, why := range noCaseYet {
		if name == "" {
			t.Error("noCaseYet holds an entry with no method name")
		}
		if len(strings.TrimSpace(why)) < 20 {
			t.Errorf("the entry for jira.%s reads %q, which is a label rather than a reason: say what a "+
				"table for it would have to establish", name, why)
		}
	}
}

func TestMethodsCalledIn_FindsThePortMethodsAConformanceFileExercises(t *testing.T) {
	t.Parallel()

	methods := []string{"Me", "Search", "Download", "BoardIssues"}

	tests := []struct {
		name  string
		src   string
		want  []string
		table bool
	}{
		{
			name: "a call on a value the table opened",
			src: `package cloud
func TestMe_BothAdaptersAnswerTheSameWay(t *testing.T) {
	got, err := open(t).Me(t.Context())
}`,
			want:  []string{"Me"},
			table: true,
		},
		{
			name: "a call inside a package-level helper the table drives",
			src: `package cloud
func TestSearch_BothAdaptersAnswerTheSameWayAboutAnAccount(t *testing.T) { one(t, open(t)) }
func one(t *testing.T, c jira.Searcher) { page, _ := c.Search(t.Context(), jira.Query{}) }`,
			want:  []string{"Search"},
			table: true,
		},
		{
			name: "a rule both adapters refuse",
			src: `package cloud
func TestDownload_NeitherAdapterReadsPastTheEnd(t *testing.T) {
	_ = a.Download(t.Context(), id, w, jira.DownloadOptions{})
}`,
			want:  []string{"Download"},
			table: true,
		},
		{
			name: "a file that exercises one adapter is not a table",
			src: `package cloud
func TestSearch_ReadsAPage(t *testing.T) { _, _ = c.Search(t.Context(), jira.Query{}) }`,
			want:  nil,
			table: false,
		},
		{
			name: "a method named in a comment or a string is not a call",
			src: `package cloud
// BoardIssues is not called here.
func TestMe_BothAdaptersAnswerTheSameWay(t *testing.T) {
	t.Log("BoardIssues")
	_, _ = open(t).Me(t.Context())
}`,
			want:  []string{"Me"},
			table: true,
		},
		{
			name: "a method of something else that shares a port method's name",
			src: `package cloud
func TestMe_BothAdaptersAnswerTheSameWay(t *testing.T) { page.Search() }`,
			want:  []string{"Search"},
			table: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "conformance_x_test.go", tt.src, parser.ParseComments)
			if err != nil {
				t.Fatalf("parsing the fixture: %v", err)
			}
			got, table := methodsCalledIn(file, methods)
			if table != tt.table {
				t.Errorf("the file reads as a conformance table = %v, want %v", table, tt.table)
			}
			slices.Sort(got)
			want := slices.Clone(tt.want)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Errorf("methodsCalledIn = %v, want %v", got, want)
			}
		})
	}
}

// portMethods is jira.Client's method set, read off the interface rather than out
// of the source: a method that arrives through an embedded interface is still a
// method a caller can reach, and only the type system knows the whole set.
func portMethods() []string {
	typ := reflect.TypeOf((*jira.Client)(nil)).Elem()
	out := make([]string, 0, typ.NumMethod())
	for i := range typ.NumMethod() {
		out = append(out, typ.Method(i).Name)
	}
	slices.Sort(out)
	return out
}

// conformanceCoverage returns the conformance files it found, module-relative,
// and the port methods they call between them.
func conformanceCoverage(t *testing.T, root string, methods []string) (files, covered []string) {
	t.Helper()

	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir() && skipDir(d.Name()):
			return fs.SkipDir
		case d.IsDir() || !isConformanceFile(d.Name()):
			return nil
		}
		parsed, perr := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if perr != nil {
			return fmt.Errorf("parsing %s: %w", path, perr)
		}
		called, table := methodsCalledIn(parsed, methods)
		if !table {
			return nil
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		files = append(files, filepath.ToSlash(rel))
		covered = append(covered, called...)
		return nil
	})
	if err != nil {
		t.Fatalf("walking the tree for conformance tables: %v", err)
	}
	slices.Sort(files)
	slices.Sort(covered)
	return files, slices.Compact(covered)
}

func isConformanceFile(name string) bool {
	return strings.HasSuffix(name, "_test.go") && strings.Contains(name, "conformance")
}

// methodsCalledIn reads the port methods a file calls, and whether the file is a
// conformance table at all — which is decided by a test function's name, because
// that is the one statement in the file that says the assertions inside it are
// run against both adapters.
//
// A call site anywhere in such a file counts, including one in a package-level
// helper: the tables drive their adapters through helpers, and a scan confined to
// the test function's own body would miss most of what they exercise.
func methodsCalledIn(file *ast.File, methods []string) (called []string, table bool) {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || !strings.HasPrefix(fn.Name.Name, "Test") {
			continue
		}
		for _, marker := range conformanceMarkers {
			if strings.Contains(fn.Name.Name, marker) {
				table = true
			}
		}
	}
	if !table {
		return nil, false
	}
	ast.Inspect(file, func(n ast.Node) bool {
		call, isCall := n.(*ast.CallExpr)
		if !isCall {
			return true
		}
		sel, isSel := call.Fun.(*ast.SelectorExpr)
		if !isSel || !slices.Contains(methods, sel.Sel.Name) {
			return true
		}
		called = append(called, sel.Sel.Name)
		return true
	})
	slices.Sort(called)
	return slices.Compact(called), true
}

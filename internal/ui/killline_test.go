package ui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// widgetPkg is the one package allowed to call bubbles' own constructors, because
// it is where the keymap correction lives.
const widgetPkg = "widget"

// call is one constructor call the sweep cares about, and where it was.
type call struct {
	file string
	line int
}

// scanFields reads every Go file under internal/ui and reports, per package
// directory, which of the three things it does: builds a field straight from
// bubbles, builds one through the widget, and names the replacement binding.
func scanFields(t *testing.T) (raw map[string][]call, builds, registers map[string]bool, files int) {
	t.Helper()

	raw = map[string][]call{}
	builds, registers = map[string]bool{}, map[string]bool{}
	fset := token.NewFileSet()

	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case entry.IsDir():
			if entry.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		case !strings.HasSuffix(entry.Name(), ".go"):
			return nil
		}

		parsed, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			t.Fatalf("parsing %s: %v", path, parseErr)
		}
		pkg := filepath.ToSlash(filepath.Dir(path))
		files++

		ast.Inspect(parsed, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkgName, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			switch pkgName.Name + "." + sel.Sel.Name {
			case "textinput.New", "textarea.New":
				if filepath.Base(pkg) != widgetPkg {
					raw[pkg] = append(raw[pkg], call{file: path, line: fset.Position(sel.Pos()).Line})
				}
			case "widget.NewInput", "widget.NewArea":
				builds[pkg] = true
			case "widget.KillLine":
				registers[pkg] = true
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walking internal/ui: %v", err)
	}
	return raw, builds, registers, files
}

// The kernel keeps ctrl+k for the command palette and never forwards it, not even
// to a view that is taking typing, so a field built straight from bubbles has no
// kill-to-end-of-line at all. This is a sweep over the source rather than a test
// per view so that the next field is covered before anybody writes one for it.
func TestKillLine_EveryTextFieldIsBuiltThroughTheWidgetThatKeepsTheMotion(t *testing.T) {
	t.Parallel()

	raw, builds, _, files := scanFields(t)
	if files < 50 {
		t.Fatalf("the sweep read %d files under internal/ui, which is too few to be reading the views", files)
	}
	if len(builds) == 0 {
		t.Fatal("no package builds a text field through internal/ui/widget, so this sweep is checking nothing")
	}
	for _, pkg := range sorted(raw) {
		for _, at := range raw[pkg] {
			t.Errorf("%s:%d builds a text field from bubbles directly; use widget.NewInput or widget.NewArea, "+
				"which are where the keymap the kernel's ctrl+k made necessary lives",
				at.file, at.line)
		}
	}
}

// A field that keeps the motion and never says so is half a fix: nobody guesses
// alt+k, so the state the field belongs to has to advertise it.
func TestKillLine_EveryPackageWithATextFieldAdvertisesTheBinding(t *testing.T) {
	t.Parallel()

	_, builds, registers, _ := scanFields(t)
	for _, pkg := range sorted(builds) {
		if !registers[pkg] {
			t.Errorf("%s builds a text field and never names widget.KillLine, so its footer and its ? overlay "+
				"do not know the stroke that replaced ctrl+k", pkg)
		}
	}
	for _, pkg := range sorted(registers) {
		if !builds[pkg] {
			t.Errorf("%s advertises widget.KillLine and builds no text field, so it names a stroke nothing there answers", pkg)
		}
	}
}

func sorted[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

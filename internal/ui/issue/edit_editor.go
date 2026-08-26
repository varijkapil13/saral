package issue

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/adf"
)

// editorLauncher hands a file to whatever the user edits text in and reports
// back when it exits. The default runs the real thing through the Bubble Tea
// program, which is what suspends the interface and puts it back afterwards; a
// test substitutes its own.
type editorLauncher func(path string, done func(error) tea.Msg) tea.Cmd

// launchEditor suspends the interface, runs the user's editor on the file and
// resumes.
func launchEditor(path string, done func(error) tea.Msg) tea.Cmd {
	name, args, err := editorCommand()
	if err != nil {
		return func() tea.Msg { return done(err) }
	}
	return tea.ExecProcess(exec.Command(name, append(args, path)...), done) //nolint:gosec // the command is the user's own $EDITOR
}

// editorCommand resolves what to run. $VISUAL wins over $EDITOR because that is
// what those two variables have always meant, and either may carry arguments —
// "code --wait" is a whole editor rather than a program called that.
func editorCommand() (name string, args []string, err error) {
	for _, env := range []string{"VISUAL", "EDITOR"} {
		fields := strings.Fields(os.Getenv(env))
		if len(fields) > 0 {
			return fields[0], fields[1:], lookEditor(fields[0], env)
		}
	}
	fallback := "vi"
	if runtime.GOOS == "windows" {
		fallback = "notepad"
	}
	return fallback, nil, lookEditor(fallback, "")
}

func lookEditor(name, env string) error {
	if _, err := exec.LookPath(name); err != nil {
		if env != "" {
			return fmt.Errorf("$%s names %s, which is not on the PATH", env, name)
		}
		return fmt.Errorf("there is no editor to open this in: set $EDITOR (%s is not on the PATH)", name)
	}
	return nil
}

// handOffToEditor writes the description out as markdown, hands it over and
// reads back whatever came of it.
//
// The markdown is rendered with the zero options on purpose. A width-bounded
// render truncates a table's cells with an ellipsis, and an edit anywhere in
// that document would write the truncation back into Jira.
func handOffToEditor(launch editorLauncher, addr kernel.Addr, gen int, key string, original adf.Doc) tea.Cmd {
	rendered := adf.Markdown(original)
	path, err := writeHandoff(key, rendered)
	if err != nil {
		return func() tea.Msg { return kernel.ReplyTo(editedMsg{gen: gen, err: err}, addr) }
	}
	return launch(path, func(runErr error) tea.Msg {
		return kernel.ReplyTo(readHandoff(gen, original, rendered, path, runErr), addr)
	})
}

func writeHandoff(key, rendered string) (string, error) {
	file, err := os.CreateTemp("", "saral-"+safeName(key)+"-*.md")
	if err != nil {
		return "", fmt.Errorf("making a file for the editor: %w", err)
	}
	name := file.Name()
	if _, err := file.WriteString(rendered); err != nil {
		_ = file.Close()
		_ = os.Remove(name)
		return "", fmt.Errorf("writing the description out: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(name)
		return "", fmt.Errorf("writing the description out: %w", err)
	}
	return name, nil
}

// readHandoff turns what the editor left behind into an outcome.
//
// The file survives every ending that is not a clean apply, and the message
// says where it is. An editor that exited badly may still have saved, and
// markdown this package will not parse is text the author typed — neither is
// something to delete on their behalf.
func readHandoff(gen int, original adf.Doc, rendered, path string, runErr error) tea.Msg {
	if runErr != nil {
		return editedMsg{gen: gen, note: "the editor stopped without saving, so nothing changed. Your text is in " + path}
	}
	body, err := os.ReadFile(path) //nolint:gosec // the path is the temporary file this package just wrote
	if err != nil {
		return editedMsg{gen: gen, err: fmt.Errorf("reading the description back: %w", err)}
	}
	edited := string(body)
	if edited == rendered {
		remove(path)
		return editedMsg{gen: gen, note: "the description is unchanged"}
	}
	if strings.TrimSpace(edited) == "" {
		remove(path)
		return editedMsg{gen: gen, cleared: true, note: "the description will be emptied"}
	}

	// Into, never bare: reconciling against the document the markdown came from
	// is the only thing that keeps a mention's account id, a lozenge's colour
	// and every node type this client has never heard of in the blocks nobody
	// touched.
	doc, err := adf.ParseMarkdownInto(original, edited, adf.Options{})
	if err != nil {
		return editedMsg{gen: gen, err: handoffError(err, path)}
	}
	remove(path)
	return editedMsg{gen: gen, doc: &doc, note: "the description will be updated"}
}

// handoffError puts the line number in front, because the parser knows exactly
// which line it stopped on and a save that just says "invalid" leaves an author
// hunting through their own text.
func handoffError(err error, path string) error {
	var parse *adf.ParseError
	if errors.As(err, &parse) {
		return fmt.Errorf("line %d: %w. Your text is still in %s", parse.Line, parse.Err, path)
	}
	return fmt.Errorf("%w. Your text is still in %s", err, path)
}

func remove(path string) { _ = os.Remove(path) }

// riskyEdits names what editing this document as markdown costs, narrowed to
// the constructs it actually contains. ParseMarkdownInto restores every block
// the author leaves alone; these are what the blocks they do touch lose.
func riskyEdits(d adf.Doc) []string {
	if d.IsZero() || d.IsEmpty() {
		return nil
	}
	present := d.NodeTypes()
	var out []string
	for _, entry := range adf.ParseMarkdownDropsOnly() {
		kind, _, ok := strings.Cut(entry, ":")
		if !ok {
			continue
		}
		if _, there := present[kind]; there && !containsEntry(out, entry) {
			out = append(out, entry)
		}
	}
	return out
}

func containsEntry(out []string, entry string) bool {
	for _, got := range out {
		if got == entry {
			return true
		}
	}
	return false
}

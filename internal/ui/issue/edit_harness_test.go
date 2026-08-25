package issue

import (
	"context"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// stroke builds the key press the kernel would have delivered. The harness's
// own keyPress covers the detail pane's keys; these are the ones the editor and
// the picker use.
func stroke(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "delete":
		return tea.KeyPressMsg{Code: tea.KeyDelete}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "ctrl+s":
		return tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}
	default:
		r, _ := utf8.DecodeRuneInString(s)
		return tea.KeyPressMsg{Code: r, Text: s}
	}
}

// panel drives one pushed pane the way the kernel does: it runs the commands a
// view returns, feeds the messages back in, and keeps what the kernel itself
// would have acted on.
type panel struct {
	t          *testing.T
	view       kernel.View
	statuses   []kernel.StatusMsg
	pushes     []kernel.PushMsg
	pops       int
	broadcasts []tea.Msg
}

func newPanel(t *testing.T, view kernel.View, w, h int) *panel {
	t.Helper()

	p := &panel{t: t, view: view}
	p.send(kernel.SizeMsg{Width: w, Height: h})
	p.run(view.Init())
	p.send(kernel.FocusMsg{Focused: true})
	return p
}

func (p *panel) send(msg tea.Msg) {
	p.t.Helper()

	view, cmd := p.view.Update(msg)
	p.view = view
	p.run(cmd)
}

func (p *panel) run(cmd tea.Cmd) {
	p.t.Helper()

	queue := []tea.Cmd{cmd}
	for steps := 0; len(queue) > 0; steps++ {
		if steps > 2000 {
			p.t.Fatal("commands never settled")
		}
		next := queue[0]
		queue = queue[1:]
		if next == nil {
			continue
		}
		msg := next()
		if msg == nil {
			continue
		}
		if cmds, ok := unwrapCmds(msg); ok {
			queue = append(queue, cmds...)
			continue
		}
		switch msg := msg.(type) {
		case kernel.StatusMsg:
			p.statuses = append(p.statuses, msg)
			continue
		case kernel.PushMsg:
			p.pushes = append(p.pushes, msg)
			continue
		case kernel.PopMsg:
			p.pops++
			continue
		case kernel.BroadcastMsg:
			p.broadcasts = append(p.broadcasts, msg.Msg)
			continue
		}
		view, follow := p.view.Update(msg)
		p.view = view
		queue = append(queue, follow)
	}
}

func (p *panel) keys(strokes ...string) {
	p.t.Helper()

	for _, s := range strokes {
		p.send(stroke(s))
	}
}

func (p *panel) typed(text string) {
	p.t.Helper()

	for _, r := range text {
		p.send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func (p *panel) frame() string { return ansi.Strip(p.view.View()) }

// zoneAt draws the pane, hands the frame to the zone manager the way the kernel
// does, and returns where the named element ended up. The id comes from the pane
// itself rather than being guessed at from the prefixes handed out so far — the
// counter is global to the test binary and runs past any number a loop here
// would try. The manager records on a goroutine of its own, so the zone is
// looked for until it appears.
func (p *panel) zoneAt(d kernel.Deps, suffix string) *zone.ZoneInfo {
	p.t.Helper()

	id := p.zoneID(suffix)
	deadline := time.Now().Add(10 * time.Second)
	for {
		d.Zones.Scan(p.view.View())
		if at := d.Zones.Get(id); !at.IsZero() {
			return at
		}
		if time.Now().After(deadline) {
			p.t.Fatalf("nothing on screen is marked %q", id)
		}
		runtime.Gosched()
	}
}

func (p *panel) zoneID(suffix string) string {
	p.t.Helper()

	switch v := p.view.(type) {
	case *editModel:
		return v.zones.ID(suffix)
	case *moveModel:
		return v.zones.ID(suffix)
	default:
		p.t.Fatalf("a %T marks no zones", p.view)
		return ""
	}
}

func (p *panel) clickAt(at *zone.ZoneInfo) {
	p.t.Helper()

	p.send(tea.MouseClickMsg{X: at.StartX + 2, Y: at.StartY, Button: tea.MouseLeft})
}

func (p *panel) editor() *editModel {
	p.t.Helper()

	m, ok := p.view.(*editModel)
	if !ok {
		p.t.Fatalf("the pane is a %T, not the editor", p.view)
	}
	return m
}

func (p *panel) mover() *moveModel {
	p.t.Helper()

	m, ok := p.view.(*moveModel)
	if !ok {
		p.t.Fatalf("the pane is a %T, not the transition picker", p.view)
	}
	return m
}

func (p *panel) lastStatus() kernel.StatusMsg {
	if len(p.statuses) == 0 {
		return kernel.StatusMsg{}
	}
	return p.statuses[len(p.statuses)-1]
}

func (p *panel) statusText() string {
	texts := make([]string, 0, len(p.statuses))
	for _, s := range p.statuses {
		texts = append(texts, s.Text)
	}
	return strings.Join(texts, "\n")
}

// recorder keeps the writes a test made, because what reached the port is the
// thing under test: a patch naming a field nobody read is the bug this packet
// exists to make impossible.
type recorder struct {
	jira.Client

	mu      sync.Mutex
	patches []jira.IssuePatch
	moves   []recordedMove
}

type recordedMove struct {
	key   string
	id    string
	patch jira.IssuePatch
}

func record(c jira.Client) *recorder { return &recorder{Client: c} }

func (r *recorder) UpdateIssue(ctx context.Context, key string, in jira.IssuePatch) error {
	if err := r.Client.UpdateIssue(ctx, key, in); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.patches = append(r.patches, in)
	return nil
}

func (r *recorder) Transition(ctx context.Context, key, transitionID string, in jira.IssuePatch) error {
	if err := r.Client.Transition(ctx, key, transitionID, in); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.moves = append(r.moves, recordedMove{key: key, id: transitionID, patch: in})
	return nil
}

func (r *recorder) lastPatch(t *testing.T) jira.IssuePatch {
	t.Helper()

	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.patches) == 0 {
		t.Fatal("nothing was written to Jira")
	}
	return r.patches[len(r.patches)-1]
}

func (r *recorder) lastMove(t *testing.T) recordedMove {
	t.Helper()

	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.moves) == 0 {
		t.Fatal("no transition was made")
	}
	return r.moves[len(r.moves)-1]
}

func (r *recorder) writes() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.patches) + len(r.moves)
}

// patchFieldNames is what a patch would send, named the way the wire names it.
// It is how a test says "only the summary" without reaching into the adapter.
func patchFieldNames(in jira.IssuePatch) []string {
	var out []string
	for _, named := range []struct {
		id  string
		set bool
	}{
		{"summary", in.Summary != nil},
		{"description", in.Description != nil},
		{"assignee", in.Assignee != nil},
		{"labels", in.Labels != nil},
		{"priority", in.PriorityID != nil},
		{"duedate", in.Due != nil},
	} {
		if named.set {
			out = append(out, named.id)
		}
	}
	out = append(out, in.Fields.IDs()...)
	for _, ref := range in.Clear {
		out = append(out, ref.ID+" (emptied)")
	}
	return out
}

// listSeed is the issue a row in the list hands over: identity and the fields a
// list projection asks for, carrying the mask that says which those were. The
// description, the labels and the due date are outside it, which is exactly the
// state the editor has to refuse to write.
func listSeed(t *testing.T, f *jiratest.Fake, key string) jira.Issue {
	t.Helper()

	full, err := f.Issue(t.Context(), key)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	return jira.Issue{
		ID: full.ID, Key: full.Key, Summary: full.Summary, Type: full.Type,
		Status: full.Status, Assignee: full.Assignee, Priority: full.Priority,
		Updated: full.Updated, Requested: projectionMask(),
	}
}

// tempDrafts is a draft store nowhere near the person running the tests.
func tempDrafts(t *testing.T) draftStore {
	t.Helper()
	return draftStore{dir: t.TempDir()}
}

// scriptedEditor stands in for the user's editor: it writes what the test says
// the author typed, and reports whatever the editor process would have.
func scriptedEditor(t *testing.T, body string, exit error) editorLauncher {
	t.Helper()

	return func(path string, done func(error) tea.Msg) tea.Cmd {
		return func() tea.Msg {
			if exit == nil {
				if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
					t.Errorf("standing in for the editor: %v", err)
				}
			}
			return done(exit)
		}
	}
}

// docWith builds a description carrying a mention, which is the node markdown
// cannot rebuild and the reason ParseMarkdownInto exists.
func docWith(paragraph string) adf.Doc {
	return adf.NewDoc(
		adf.NewNode("paragraph", adf.NewText(paragraph)),
		adf.NewNode("paragraph",
			adf.NewText("asked "),
			adf.NewNode("mention").WithAttrs(adf.Attrs{"id": "acct-ada", "text": "@Ada Lovelace"}),
			adf.NewText(" to look"),
		),
	)
}

func projectionMask() jira.FieldMask { return jira.NewFieldMask(app.ListProjection().IDs) }

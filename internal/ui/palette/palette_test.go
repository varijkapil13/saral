package palette

import (
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

// The whole point of the kernel seam: the palette names the command and stops
// there. Deps here is a value copied when the palette was built, and the search
// commands narrow their JQL with Deps.Project.
func TestPalette_AsksTheKernelToRunTheCommandAndNeverRunsItItself(t *testing.T) {
	t.Parallel()

	ran := false
	cmds := sample()
	at := slices.IndexFunc(cmds, func(c kernel.Command) bool { return c.ID == "issues.mine" })
	cmds[at].Run = func(kernel.Deps) tea.Cmd {
		ran = true
		return nil
	}

	p := fly(t, paletteDeps(), cmds, memoryTable(), 120, 24)
	p.typeText("my issues")
	p.press("enter")

	if got := p.ran(); len(got) != 1 || got[0] != "issues.mine" {
		t.Fatalf("enter produced %v, want one RunCommandMsg for issues.mine", got)
	}
	if ran {
		t.Error("the palette called Run itself, against the deps it was built with rather than the ones the session holds now")
	}
	if p.popped() {
		t.Error("the palette popped itself; the kernel does that after the command has run")
	}
}

func TestPalette_ACommandThisSiteRefusesIsNotOfferedAndItsReasonIsWhatYouGetInstead(t *testing.T) {
	t.Parallel()

	p := fly(t, paletteDeps(), sample(), memoryTable(), 120, 24)
	mustNotContain(t, p.frame(), "Move issues between projects")

	p.typeText("move")
	if got := p.titles(); len(got) != 0 {
		t.Errorf("the palette offers %v, and this token cannot move an issue", got)
	}
	mustContain(t, p.frame(), "Move issues between projects", noBulkMove)

	p.press("enter")
	if got := p.ran(); len(got) != 0 {
		t.Errorf("enter ran %v with nothing on offer", got)
	}
}

// The capability answer can land while the palette is open — a first probe, or
// the one a project switch asked for.
func TestPalette_OffersACommandTheProbeHasSinceAllowed(t *testing.T) {
	t.Parallel()

	p := fly(t, paletteDeps(), sample(), memoryTable(), 120, 24)
	p.typeText("move")
	p.send(kernel.CapabilitiesMsg{Caps: fullCaps()})

	if got := p.titles(); len(got) != 1 || got[0] != "Move issues between projects" {
		t.Errorf("after the probe allowed it the palette offers %v", got)
	}
}

// A capability nothing has answered for yet cannot be run either, and saying so
// beats offering it and having the kernel refuse it a keypress later.
func TestPalette_SaysWhatItCanAboutACapabilityNothingHasAnsweredFor(t *testing.T) {
	t.Parallel()

	d := paletteDeps()
	d.Caps = jira.Capabilities{}
	p := fly(t, d, sample(), memoryTable(), 120, 24)
	p.typeText("move")

	if got := p.titles(); len(got) != 0 {
		t.Errorf("the palette offers %v against a site nothing has been asked about", got)
	}
	mustContain(t, p.frame(), "Move issues between projects is not available on this site")
}

func TestPalette_FindsACommandByItsTitleItsGroupOrItsID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		query string
		want  string
	}{
		{name: "the title", query: "dark theme", want: "Use the dark theme"},
		{name: "an abbreviation of the title", query: "crt", want: "Create an issue"},
		{name: "the group", query: "comments", want: "Write a comment"},
		{name: "the ID, where the word a person types often is", query: "mine", want: "My issues"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p := fly(t, paletteDeps(), sample(), memoryTable(), 120, 24)
			p.typeText(tt.query)
			got := p.titles()
			if len(got) == 0 {
				t.Fatalf("%q matched nothing", tt.query)
			}
			if got[0] != tt.want {
				t.Errorf("%q offers %v first, want %q", tt.query, got, tt.want)
			}
		})
	}
}

func TestPalette_NothingMatchingSaysSoAndOffersNoKeysThatWouldDoAnything(t *testing.T) {
	t.Parallel()

	p := fly(t, paletteDeps(), sample(), memoryTable(), 120, 24)
	p.typeText("zzzz")

	mustContain(t, p.frame(), `Nothing matches "zzzz"`)
	set, gen := p.m.LiveKeys()
	if gen != int(keysNothing) {
		t.Errorf("the keys are in state %d with nothing on offer", gen)
	}
	if labels := shortOf(set); strings.Contains(labels, "run it") {
		t.Errorf("the footer offers enter with nothing to run: %s", labels)
	}
}

func TestPalette_PutsWhatThisMachineActuallyRunsFirst(t *testing.T) {
	t.Parallel()

	freq := memoryTable()
	before := fly(t, paletteDeps(), sample(), freq, 120, 24)
	if got := before.titles()[0]; got != "Use the dark theme" {
		t.Fatalf("an unused palette opens on %q, want the registry's own first entry", got)
	}

	// Two runs of the last command in registry order, then the palette as it is
	// built on the next ctrl+k.
	for range 2 {
		before.send(kernel.CommandRanMsg{ID: "issues.mine"})
	}
	after := fly(t, paletteDeps(), sample(), freq, 120, 24)
	if got := after.titles()[0]; got != "My issues" {
		t.Errorf("the palette opens on %q after two runs of My issues", got)
	}
}

func TestPalette_AHabitThatStoppedFallsBehindOneThatDidNot(t *testing.T) {
	t.Parallel()

	freq := memoryTable()
	lastMonth := clockAt.Add(-30 * 24 * time.Hour)
	for range 4 {
		freq.ran("issue.create", lastMonth)
	}
	freq.ran("issues.mine", clockAt.Add(-time.Hour))

	p := fly(t, paletteDeps(), sample(), freq, 120, 24)
	got := p.titles()
	mine := slices.Index(got, "My issues")
	create := slices.Index(got, "Create an issue")
	if mine > create {
		t.Errorf("the order is %v; four runs a month ago still outrank one an hour ago", got)
	}
}

// A first run, another copy of Saral holding the cache, an unwritable home: the
// palette ranks what it has seen this session and never refuses to draw.
func TestPalette_RanksWithNoDurableStoreAtAll(t *testing.T) {
	t.Parallel()

	d := paletteDeps()
	d.Cache = nil
	freq := memoryTable()

	p := fly(t, d, sample(), freq, 120, 24)
	if len(p.titles()) == 0 {
		t.Fatal("a session with nowhere to keep a ranking drew no commands at all")
	}
	p.send(kernel.CommandRanMsg{ID: "issues.mine"})
	p.send(kernel.CommandRanMsg{ID: "issues.mine"})

	next := fly(t, d, sample(), freq, 120, 24)
	if got := next.titles()[0]; got != "My issues" {
		t.Errorf("the next ctrl+k opens on %q, so nothing was learnt inside the session either", got)
	}
}

func TestPalette_ShowsTheKeyItsRegistrarNamedAndNeverInventsOne(t *testing.T) {
	t.Parallel()

	p := fly(t, paletteDeps(), sample(), memoryTable(), 120, 24)
	frame := p.frame()
	mustContain(t, frame, "g1")

	for _, tt := range []struct {
		title string
		key   string
	}{
		{title: "Edit this issue", key: "e"},
		{title: "Write a comment", key: "a"},
		{title: "Issues", key: "g1"},
	} {
		line := lineWith(t, frame, tt.title)
		if !strings.HasSuffix(line, tt.key) {
			t.Errorf("the row for %q ends %q, want the key %q its registrar named", tt.title, line, tt.key)
		}
	}
	for _, title := range []string{"My issues", "Create an issue", "Use the dark theme"} {
		line := lineWith(t, frame, title)
		if trimmed := strings.TrimRight(line, " "); trimmed != line {
			continue
		}
		t.Errorf("the row for %q ends in something other than blank, and no key reaches it: %q", title, line)
	}
}

func TestPalette_NotesTheKeyOnTheThirdRunAndNotOnTheSecond(t *testing.T) {
	t.Parallel()

	p := fly(t, paletteDeps(), sample(), memoryTable(), 120, 24)
	ran := kernel.CommandRanMsg{ID: "issue.edit", Keys: []string{"e"}}

	p.send(ran)
	p.send(ran)
	if got := p.statuses(); len(got) != 0 {
		t.Fatalf("the second run said %v; docs/UX.md notes the key on the third", got)
	}

	p.send(ran)
	got := p.statuses()
	if len(got) != 1 {
		t.Fatalf("the third run said %v, want one line", got)
	}
	mustContain(t, got[0], "e", "Edit this issue")

	p.send(ran)
	if got := p.statuses(); len(got) != 1 {
		t.Errorf("the fourth run said it again: %v", got)
	}
}

func TestPalette_SaysNothingAboutACommandNoKeyReaches(t *testing.T) {
	t.Parallel()

	p := fly(t, paletteDeps(), sample(), memoryTable(), 120, 24)
	for range hintAfter {
		p.send(kernel.CommandRanMsg{ID: "issues.mine"})
	}
	if got := p.statuses(); len(got) != 0 {
		t.Errorf("the palette invented a key for a command that has none: %v", got)
	}
}

func TestPalette_TakesEveryKeyIntoTheFilter(t *testing.T) {
	t.Parallel()

	p := fly(t, paletteDeps(), sample(), memoryTable(), 120, 24)
	if !p.m.WantsRawKeys() {
		t.Fatal("the palette does not claim the keyboard, so q quits and the digits run saved queries instead of typing")
	}

	p.typeText("q1r")
	if got := p.m.query; got != "q1r" {
		t.Errorf("the filter holds %q, want q1r", got)
	}
	if p.popped() || len(p.ran()) != 0 {
		t.Errorf("typing did something other than filter: %v", p.msgs)
	}
}

func TestPalette_EscapeAsksToBeClosed(t *testing.T) {
	t.Parallel()

	p := fly(t, paletteDeps(), sample(), memoryTable(), 120, 24)
	p.press("esc")
	if !p.popped() {
		t.Errorf("esc did not ask the kernel to pop the palette: %v", p.msgs)
	}
}

func TestPalette_ArrowsAndTheirControlTwinsMoveTheSelection(t *testing.T) {
	t.Parallel()

	p := fly(t, paletteDeps(), sample(), memoryTable(), 120, 24)
	first := p.m.selectedID()

	p.press("down")
	second := p.m.selectedID()
	if second == first {
		t.Fatal("down did not move the selection")
	}
	p.press("ctrl+p")
	if got := p.m.selectedID(); got != first {
		t.Errorf("ctrl+p left the selection on %q, want %q", got, first)
	}
	p.press("up")
	if got := p.m.selectedID(); got != first {
		t.Errorf("up moved off the first row to %q", got)
	}
	p.press("ctrl+n")
	if got := p.m.selectedID(); got != second {
		t.Errorf("ctrl+n left the selection on %q, want %q", got, second)
	}
}

func TestPalette_ClickingARowSelectsItAndClickingItAgainRunsIt(t *testing.T) {
	t.Parallel()

	p := fly(t, paletteDeps(), sample(), memoryTable(), 120, 24)
	target := "issues.mine"
	at := p.zoneOf(target)
	click := tea.MouseClickMsg{X: at.StartX + 2, Y: at.StartY, Button: tea.MouseLeft}

	p.send(click)
	if got := p.m.selectedID(); got != target {
		t.Fatalf("the click selected %q, want %q", got, target)
	}
	if got := p.ran(); len(got) != 0 {
		t.Fatalf("the first click ran %v; docs/UX.md gives click the selection", got)
	}

	p.send(click)
	if got := p.ran(); len(got) != 1 || got[0] != target {
		t.Errorf("a second click on the selected row ran %v", got)
	}
}

// New is what the kernel calls, and it has to be built from the registry rather
// than from anything of the palette's own: the theme commands live in the kernel
// and reach a user through here or not at all.
func TestNew_OffersWhatIsRegistered(t *testing.T) {
	t.Parallel()

	view, ok := New(paletteDeps()).(*Model)
	if !ok {
		t.Fatal("New did not return a *Model")
	}
	titles := make([]string, 0, len(view.rows))
	for i := range view.rows {
		titles = append(titles, view.rows[i].cmd.Title)
	}
	if !slices.Contains(titles, "Use the dark theme") {
		t.Errorf("the palette offers %v, and none of it is the kernel's own theme commands", titles)
	}
}

func lineWith(t *testing.T, frame, want string) string {
	t.Helper()
	for _, line := range strings.Split(frame, "\n") {
		if strings.Contains(line, want) {
			return line
		}
	}
	t.Fatalf("no line of the frame holds %q:\n%s", want, frame)
	return ""
}

func shortOf(set kernel.KeySet) string {
	out := make([]string, 0, len(set.Short))
	for _, b := range set.Short {
		out = append(out, b.Help().Key+" "+b.Help().Desc)
	}
	return strings.Join(out, " · ")
}

// zoneOf resolves the click target the palette marked a row with. Registering a
// zone is a side effect of the manager scanning a drawn frame, and it happens on
// the manager's own goroutine.
func (p *pilot) zoneOf(id string) zoneBounds {
	p.t.Helper()
	_ = p.m.deps.Zones.Scan(p.m.View())
	deadline := time.Now().Add(5 * time.Second)
	for {
		if at := p.m.deps.Zones.Get(p.m.zonePrefix + zoneRow + id); !at.IsZero() {
			return zoneBounds{StartX: at.StartX, StartY: at.StartY}
		}
		if time.Now().After(deadline) {
			p.t.Fatalf("the palette never marked a click target for %q", id)
		}
		runtime.Gosched()
	}
}

type zoneBounds struct{ StartX, StartY int }

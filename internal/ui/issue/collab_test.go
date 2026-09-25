package issue

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/uitest"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

var (
	collabMe    = jira.User{AccountID: "acc-me", DisplayName: "Ada Lovelace", Active: true}
	collabOther = jira.User{AccountID: "acc-other", DisplayName: "Grace Hopper", Active: true}
)

func collabFake(opts ...jiratest.Option) *jiratest.Fake {
	return newFake(4, append([]jiratest.Option{
		jiratest.WithMe(collabMe),
		jiratest.WithPeople([]jira.User{collabMe, collabOther}),
	}, opts...)...)
}

func failures() []struct {
	name string
	err  error
} {
	return []struct {
		name string
		err  error
	}{
		{"forbidden", &jira.CapabilityError{Reason: "no Link issues permission"}},
		{"rate limited", &jira.RateLimitError{RetryAfter: time.Second}},
		{"transport", &jira.TransportError{Op: "link", Err: errors.New("connection reset")}},
	}
}

type sheetDriver struct {
	t          *testing.T
	s          *sheet
	statuses   []kernel.StatusMsg
	pushed     []kernel.PushMsg
	broadcasts []tea.Msg
	pops       int
}

func openSheet(t *testing.T, f *jiratest.Fake, key string, kind sheetKind) *sheetDriver {
	t.Helper()
	iss, err := f.Issue(t.Context(), key)
	if err != nil {
		t.Fatal(err)
	}
	s := newSheet(testDeps(t, f), iss, kind)
	s.wait = 0
	d := &sheetDriver{t: t, s: s}
	d.send(kernel.SizeMsg{Width: 80, Height: 20})
	d.run(s.Init())
	return d
}

func (d *sheetDriver) send(msg tea.Msg) {
	d.t.Helper()
	_, cmd := d.s.Update(msg)
	d.run(cmd)
}

func (d *sheetDriver) run(cmd tea.Cmd) {
	d.t.Helper()
	queue := []tea.Cmd{cmd}
	for steps := 0; len(queue) > 0; steps++ {
		if steps > 500 {
			d.t.Fatal("commands never settled")
		}
		next := queue[0]
		queue = queue[1:]
		if next == nil {
			continue
		}
		msg := next()
		if cmds, ok := unwrapCmds(msg); ok {
			queue = append(queue, cmds...)
			continue
		}
		if reply, ok := msg.(kernel.ReplyMsg); ok {
			msg = reply.Msg
		}
		switch m := msg.(type) {
		case nil:
		case kernel.StatusMsg:
			d.statuses = append(d.statuses, m)
		case kernel.PushMsg:
			d.pushed = append(d.pushed, m)
		case kernel.PopMsg:
			d.pops++
		case kernel.BroadcastMsg:
			d.broadcasts = append(d.broadcasts, m.Msg)
		case sheetMsg:
			_, follow := d.s.Update(m)
			queue = append(queue, follow)
		}
	}
}

func (d *sheetDriver) keys(keys ...string) {
	d.t.Helper()
	for _, k := range keys {
		if k == "esc" {
			d.send(tea.KeyPressMsg{Code: tea.KeyEscape})
			continue
		}
		d.send(keyPress(k))
	}
}

func (d *sheetDriver) typed(text string) {
	d.t.Helper()
	for _, r := range text {
		d.send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func (d *sheetDriver) frame() string { return ansi.Strip(d.s.View()) }

func (d *sheetDriver) last() kernel.StatusMsg {
	if len(d.statuses) == 0 {
		return kernel.StatusMsg{}
	}
	return d.statuses[len(d.statuses)-1]
}

func linkOn(t *testing.T, f *jiratest.Fake, from, to string) {
	t.Helper()
	types, err := f.IssueLinkTypes(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.LinkIssues(t.Context(), jira.LinkInput{TypeID: types[0].ID, From: from, To: to}); err != nil {
		t.Fatal(err)
	}
}

func TestShare_CopiesAndOpensWhatItNames(t *testing.T) {
	t.Parallel()
	d := kernel.Deps{Site: "example.atlassian.net"}
	for _, tc := range []struct {
		stroke string
		act    ShareAct
		said   string
	}{
		{"y", ShareKey, "copied PROJ-7"},
		{"Y", ShareLink, "copied the link to PROJ-7"},
		{"q", ShareNone, ""},
	} {
		if got := ShareStroke(tc.stroke); got != tc.act {
			t.Errorf("%q is act %d, want %d", tc.stroke, got, tc.act)
		}
		if tc.act == ShareNone {
			if Share(d, tc.act, "PROJ-7") != nil {
				t.Error("no act still did something")
			}
			continue
		}
		if got := statusesOf(Share(d, tc.act, "PROJ-7")); !slices.Contains(got, tc.said) {
			t.Errorf("%q said %v, want %q", tc.stroke, got, tc.said)
		}
	}
	if got := statusesOf(Share(kernel.Deps{}, ShareLink, "PROJ-7")); len(got) != 1 || !strings.Contains(got[0], "site") {
		t.Errorf("a session with no site said %v", got)
	}
	if got := statusesOf(Share(d, ShareKey, "")); len(got) != 1 {
		t.Errorf("no issue said %v", got)
	}
}

func statusesOf(cmd tea.Cmd) []string {
	var out []string
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		if next == nil {
			continue
		}
		msg := next()
		if cmds, ok := unwrapCmds(msg); ok {
			queue = append(queue, cmds...)
			continue
		}
		if s, ok := msg.(kernel.StatusMsg); ok {
			out = append(out, s.Text)
		}
	}
	return out
}

func TestPane_ShareKeysAndBroadcastsAnswerOnlyOnTop(t *testing.T) {
	t.Parallel()
	f := collabFake()
	dr := newDriver(t, testDeps(t, f), seedOf(t, f, "PROJ-1"), 120, 38)
	dr.key("y")
	if got := dr.lastStatus().Text; got != "copied PROJ-1" {
		t.Errorf("y said %q", got)
	}
	dr.send(kernel.FocusMsg{Focused: false})
	dr.statuses = nil
	dr.send(ShareMsg{Act: ShareKey})
	if len(dr.statuses) != 0 {
		t.Errorf("a pane under something else answered the broadcast: %v", dr.statuses)
	}
	dr.send(kernel.FocusMsg{Focused: true})
	dr.send(ShareMsg{Act: ShareKey})
	if got := dr.lastStatus().Text; got != "copied PROJ-1" {
		t.Errorf("the pane on top said %q", got)
	}
}

func TestPane_SheetKeysPushASheetForThisIssue(t *testing.T) {
	t.Parallel()
	f := collabFake()
	m, ok := New(testDeps(t, f), seedOf(t, f, "PROJ-1")).(*Model)
	if !ok {
		t.Fatal("New no longer builds a *Model")
	}
	for _, stroke := range []string{"L", "w", "W"} {
		cmd, took := m.collabKey(stroke)
		push, isPush := cmd().(kernel.PushMsg)
		if !took || !isPush {
			t.Fatalf("%s did not push anything", stroke)
		}
		if s, isSheet := push.View.(*sheet); !isSheet || s.key != "PROJ-1" {
			t.Errorf("%s pushed %T", stroke, push.View)
		}
	}
	push, _ := m.openSheet(collabClone)().(kernel.PushMsg)
	if push.Title != "Clone PROJ-1" {
		t.Errorf("the clone sheet is titled %q", push.Title)
	}
	m.stage = sideTyping
	if m.openSheet(collabLinks) != nil {
		t.Error("a sheet opened over a row being typed into")
	}
}

func TestPane_AChangeFromASheetRereadsThatIssueOnly(t *testing.T) {
	t.Parallel()
	f := collabFake()
	m, _ := New(testDeps(t, f), seedOf(t, f, "PROJ-1")).(*Model)
	if m.collabMsg(changedMsg{key: "PROJ-2"}) != nil {
		t.Error("another issue's change reread this one")
	}
	if m.collabMsg(changedMsg{key: "PROJ-1"}) == nil {
		t.Error("this issue's change was not reread")
	}
}

func TestLinks_ListAddAndRemove(t *testing.T) {
	t.Parallel()
	f := collabFake()
	linkOn(t, f, "PROJ-1", "PROJ-2")
	d := openSheet(t, f, "PROJ-1", &linksKind{})
	mustContain(t, d.frame(), "1 link", "holds up", "PROJ-2")

	d.keys("a")
	mustContain(t, d.frame(), "How is it linked?", "holds up", "is held up by")
	d.typed("echoed")
	d.keys("enter")
	mustContain(t, d.frame(), "which issue?")
	d.typed("proj-3")
	d.keys("enter")
	iss, _ := f.Issue(t.Context(), "PROJ-3")
	if len(iss.Links) != 1 || iss.Links[0].Other.Key != "PROJ-1" || iss.Links[0].Direction != jira.LinkOutward {
		t.Fatalf("PROJ-3 holds %+v, want it to echo PROJ-1", iss.Links)
	}
	mustContain(t, d.frame(), "2 links", "is echoed by", "PROJ-3")
	if !slices.Contains(d.broadcasts, tea.Msg(changedMsg{key: "PROJ-1"})) {
		t.Error("the pane underneath was not told")
	}

	d.keys("k", "k", "k")
	if row := d.s.current(); row == nil || row.key != "PROJ-2" {
		t.Fatalf("the cursor is on %+v", d.s.current())
	}
	d.keys("d")
	mustContain(t, d.frame(), "Remove the link to PROJ-2?")
	d.keys("n")
	if iss, _ := f.Issue(t.Context(), "PROJ-1"); len(iss.Links) != 2 {
		t.Fatal("n removed the link")
	}
	d.keys("d", "y")
	if iss, _ := f.Issue(t.Context(), "PROJ-1"); len(iss.Links) != 1 {
		t.Errorf("PROJ-1 still holds %d links", len(iss.Links))
	}

	d.keys("enter")
	if len(d.pushed) != 1 || d.pushed[0].Title != "PROJ-3" {
		t.Errorf("enter pushed %v", d.pushed)
	}
}

func TestLinks_TheIssueItselfIsNeverOffered(t *testing.T) {
	t.Parallel()
	d := openSheet(t, collabFake(), "PROJ-1", &linksKind{})
	d.keys("a", "enter")
	d.typed("PROJ-1")
	if len(d.s.cands) != 0 {
		t.Errorf("offered %v", d.s.cands)
	}
	d.keys("enter")
	if d.s.problem == "" {
		t.Error("an answer that is not on the list was taken")
	}
	d.keys("esc")
	if d.s.asking {
		t.Error("esc left the prompt open")
	}
}

func TestLinks_FailuresKeepTheListAndSaySo(t *testing.T) {
	t.Parallel()
	for _, tc := range failures() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := collabFake()
			linkOn(t, f, "PROJ-1", "PROJ-2")
			d := openSheet(t, f, "PROJ-1", &linksKind{})
			f.FailNext(tc.err)
			d.keys("d", "y")
			if d.last().Level != kernel.LevelError || d.s.fail == "" {
				t.Errorf("the failure said %+v", d.last())
			}
			if d.s.busy {
				t.Error("the sheet stayed busy")
			}
			if iss, _ := f.Issue(t.Context(), "PROJ-1"); len(iss.Links) != 1 {
				t.Error("the link went anyway")
			}
			mustContain(t, d.frame(), "PROJ-2")

			f.FailNext(tc.err)
			d.keys("a")
			if d.s.asking {
				t.Error("a refused read of the link types still opened the prompt")
			}
			f.FailNext(tc.err)
			d.send(kernel.RefreshMsg{})
			if d.s.fail == "" {
				t.Error("a refused read was not shown")
			}
		})
	}
}

func TestParseSpent(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"1h 30m", 90 * time.Minute, true},
		{"90m", 90 * time.Minute, true},
		{"1.5h", 90 * time.Minute, true},
		{" 2H ", 2 * time.Hour, true},
		{"", 0, false},
		{"1d", 0, false},
		{"2w", 0, false},
		{"soon", 0, false},
		{"30s", 0, false},
		{"-1h", 0, false},
	} {
		got, err := parseSpent(tc.in)
		if (err == nil) != tc.ok || got != tc.want {
			t.Errorf("parseSpent(%q) = %v, %v", tc.in, got, err)
		}
	}
}

func TestParseStarted(t *testing.T) {
	t.Parallel()
	loc := time.FixedZone("account", 2*60*60)
	now := time.Date(2025, time.March, 5, 9, 15, 0, 0, time.UTC)
	for _, tc := range []struct {
		in   string
		want time.Time
		ok   bool
	}{
		{"", now.In(loc), true},
		{"2025-03-04", time.Date(2025, time.March, 4, 11, 15, 0, 0, loc), true},
		{"2025-03-04 08:30", time.Date(2025, time.March, 4, 8, 30, 0, 0, loc), true},
		{"2025-03-06", time.Time{}, false},
		{"yesterday", time.Time{}, false},
	} {
		got, err := parseStarted(tc.in, now, loc)
		if (err == nil) != tc.ok || !got.Equal(tc.want) {
			t.Errorf("parseStarted(%q) = %v, %v", tc.in, got, err)
		}
	}
}

func TestWorklog_LogsTimeInThreeAnswers(t *testing.T) {
	t.Parallel()
	f := collabFake()
	d := openSheet(t, f, "PROJ-1", &timeKind{})
	d.keys("a")
	d.typed("3 days")
	d.keys("enter")
	if d.s.problem == "" {
		t.Fatal("days were taken")
	}
	d.s.input.SetValue("")
	d.typed("1h 30m")
	d.keys("enter")
	d.typed("2025-03-04")
	d.keys("enter")
	d.typed("Pairing")
	d.keys("enter")

	page, err := f.Worklogs(t.Context(), "PROJ-1")
	if err != nil {
		t.Fatal(err)
	}
	logs := page.Items
	if len(logs) == 0 {
		t.Fatal("nothing was logged")
	}
	got := logs[len(logs)-1]
	if got.Spent != 90*time.Minute || got.Started.Day() != 4 {
		t.Errorf("logged %v from %v", got.Spent, got.Started)
	}
	mustContain(t, d.frame(), "1h30m", "Pairing", "logged")
	if d.last().Text != "logged 1h30m on PROJ-1" {
		t.Errorf("said %q", d.last().Text)
	}
}

func TestWorklog_FailuresLeaveNothingLogged(t *testing.T) {
	t.Parallel()
	for _, tc := range failures() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := collabFake()
			d := openSheet(t, f, "PROJ-1", &timeKind{})
			before, _ := f.Worklogs(t.Context(), "PROJ-1")
			d.keys("a")
			d.typed("1h")
			d.keys("enter", "enter")
			f.FailNext(tc.err)
			d.keys("enter")
			if d.last().Level != kernel.LevelError || d.s.fail == "" || d.s.busy {
				t.Errorf("the failure said %+v", d.last())
			}
			after, _ := f.Worklogs(t.Context(), "PROJ-1")
			if len(after.Items) != len(before.Items) {
				t.Errorf("%d entries before a refused write, %d after", len(before.Items), len(after.Items))
			}
			f.FailNext(tc.err)
			d.send(kernel.RefreshMsg{})
			if d.s.fail == "" {
				t.Error("a refused read was not shown")
			}
		})
	}
}

func TestWatchers_WatchAddAndRemove(t *testing.T) {
	t.Parallel()
	f := collabFake()
	d := openSheet(t, f, "PROJ-1", &watchKind{})
	mustContain(t, d.frame(), "you are not one of them")
	d.keys("w")
	mustContain(t, d.frame(), "you among them", "Ada Lovelace")

	d.keys("a")
	d.typed("grace")
	d.keys("enter")
	list, _ := f.Watchers(t.Context(), "PROJ-1")
	if list.Count != 2 {
		t.Fatalf("%d watch it", list.Count)
	}
	mustContain(t, d.frame(), "Grace Hopper")

	for d.s.current() == nil || d.s.current().id != "acc-other" {
		d.keys("j")
	}
	d.keys("d")
	d.keys("w")
	if list, _ := f.Watchers(t.Context(), "PROJ-1"); list.Count != 0 {
		t.Errorf("%d still watch it", list.Count)
	}
}

func TestWatchers_FailuresSayWhy(t *testing.T) {
	t.Parallel()
	for _, tc := range failures() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := collabFake()
			d := openSheet(t, f, "PROJ-1", &watchKind{})
			f.FailNext(tc.err)
			d.keys("w")
			if d.last().Level != kernel.LevelError {
				t.Errorf("the failure said %+v", d.last())
			}
			if list, _ := f.Watchers(t.Context(), "PROJ-1"); list.Watching {
				t.Error("watching anyway")
			}
			d.keys("a")
			f.FailNext(tc.err)
			d.typed("g")
			if d.s.fail == "" || len(d.s.cands) != 0 {
				t.Errorf("a refused search showed %v", d.s.cands)
			}
		})
	}
}

func TestClone_CarriesWhatTheCreateScreenTakesAndItsLinks(t *testing.T) {
	t.Parallel()
	f := collabFake()
	linkOn(t, f, "PROJ-1", "PROJ-2")
	src, _ := f.Issue(t.Context(), "PROJ-1")
	d := openSheet(t, f, "PROJ-1", &cloneKind{})
	mustContain(t, d.frame(), "Carried over", "Summary of the copy")
	if got := d.s.input.Value(); got != clonePrefix+src.Summary {
		t.Errorf("the summary starts as %q", got)
	}
	d.keys("enter")
	mustContain(t, d.frame(), "Copy its 1 link as well?")
	d.keys("y")

	if d.pops != 1 || len(d.pushed) != 1 {
		t.Fatalf("popped %d and pushed %v", d.pops, d.pushed)
	}
	made, err := f.Issue(t.Context(), d.pushed[0].Title)
	if err != nil {
		t.Fatal(err)
	}
	if made.Summary != clonePrefix+src.Summary || made.Type.ID != src.Type.ID || made.Project.Key != src.Project.Key {
		t.Errorf("the copy is %q, a %s in %s", made.Summary, made.Type.Name, made.Project.Key)
	}
	if src.Priority != nil {
		if v, ok := made.Fields.ByID("priority"); !ok || len(v.Options) != 1 || v.Options[0].ID != src.Priority.ID {
			t.Errorf("the priority was not carried: %+v", v)
		}
	}
	if !slices.Equal(made.Labels, src.Labels) {
		t.Errorf("labels %v, want %v", made.Labels, src.Labels)
	}
	if len(made.Links) != 1 || made.Links[0].Other.Key != "PROJ-2" || made.Links[0].Direction != jira.LinkOutward {
		t.Errorf("the copy holds links %+v", made.Links)
	}
	if !strings.HasPrefix(d.last().Text, "cloned PROJ-1 as ") {
		t.Errorf("said %q", d.last().Text)
	}
}

func TestClone_NeedsASummaryAndSaysWhyItFailed(t *testing.T) {
	t.Parallel()
	for _, tc := range failures() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := collabFake()
			d := openSheet(t, f, "PROJ-2", &cloneKind{})
			d.s.input.SetValue("")
			d.keys("enter")
			if d.s.problem == "" {
				t.Fatal("an empty summary was taken")
			}
			d.typed("Copy")
			f.FailNext(tc.err)
			d.keys("enter")
			if d.last().Level != kernel.LevelError || d.pops != 0 {
				t.Errorf("the failure said %+v and popped %d", d.last(), d.pops)
			}

			f.FailNext(tc.err)
			d.send(kernel.RefreshMsg{})
			if d.s.asking || d.s.fail == "" {
				t.Error("a refused read still asked for a summary")
			}
		})
	}
}

func TestCloneInput_SkipsFieldsTheScreenDoesNotTake(t *testing.T) {
	t.Parallel()
	src := jira.Issue{
		Project: jira.ProjectRef{Key: "PROJ"}, Type: jira.IssueType{ID: "1"},
		Labels: []string{"a"}, Priority: &jira.Priority{ID: "3"},
		Fields: jira.NewFieldSet(map[string]jira.FieldValue{
			"f1": {Kind: jira.KindNumber, Number: 5},
			"f2": {Kind: jira.KindUnknown, Text: `{"id":1}`},
			"f3": {Kind: jira.KindText, Text: "off screen"},
		}),
	}
	schema := jira.Schema{Fields: []jira.FieldMeta{
		{Field: jira.FieldRef{ID: "labels"}, Name: "Labels"},
		{Field: jira.FieldRef{ID: "f1"}, Name: "Points"},
		{Field: jira.FieldRef{ID: "f2"}, Name: "Sprint"},
	}}
	in, names := cloneInput(src, schema)
	if !slices.Equal(names, []string{"Labels", "Points"}) {
		t.Errorf("carried %v", names)
	}
	if _, ok := in.Fields.ByID("priority"); ok {
		t.Error("the priority went although the screen does not take it")
	}
	if _, ok := in.Fields.ByID("f3"); ok {
		t.Error("a field off the screen went")
	}
	if v, ok := in.Fields.ByID("f1"); !ok || v.Number != 5 {
		t.Error("the number was not carried")
	}
}

func TestSheet_Golden(t *testing.T) {
	t.Parallel()
	f := collabFake()
	linkOn(t, f, "PROJ-1", "PROJ-2")
	linkOn(t, f, "PROJ-3", "PROJ-1")
	d := openSheet(t, f, "PROJ-1", &linksKind{})
	golden(t, "sheet_links_80x20.golden", d.frame())
	d.keys("a")
	golden(t, "sheet_links_asking_80x20.golden", d.frame())
}

func TestSheet_ClickMovesThenOpens(t *testing.T) {
	t.Parallel()
	f := collabFake()
	linkOn(t, f, "PROJ-1", "PROJ-2")
	linkOn(t, f, "PROJ-1", "PROJ-3")
	d := openSheet(t, f, "PROJ-1", &linksKind{})
	z := uitest.Zone(t, d.s.deps.Zones, d.s.View, d.s.zones.ID(rowZone(2)))
	click := tea.MouseClickMsg{X: z.StartX, Y: z.StartY, Button: tea.MouseLeft}
	d.send(click)
	if d.s.cursor != 2 {
		t.Fatalf("the click left the cursor on %d", d.s.cursor)
	}
	d.send(click)
	if len(d.pushed) != 1 {
		t.Errorf("a double click pushed %v", d.pushed)
	}
}

func TestSheet_LiveKeysFollowItsState(t *testing.T) {
	t.Parallel()
	d := openSheet(t, collabFake(), "PROJ-1", &linksKind{})
	seen := map[int]bool{}
	for _, prepare := range []func(){
		func() {},
		func() { d.s.busy = true },
		func() { d.s.busy, d.s.asking = false, true },
		func() { d.s.asking, d.s.question = false, "sure?" },
	} {
		prepare()
		_, gen := d.s.LiveKeys()
		if seen[gen] {
			t.Errorf("generation %d repeats", gen)
		}
		seen[gen] = true
	}
	if _, other := (&sheet{kind: &watchKind{}}).LiveKeys(); seen[other] {
		t.Error("two kinds share a generation")
	}
}

func BenchmarkSheet_View(b *testing.B) {
	f := collabFake()
	for i := 2; i <= 4; i++ {
		if err := f.LinkIssues(b.Context(), jira.LinkInput{TypeID: "20001", From: "PROJ-1", To: "PROJ-" + string(rune('0'+i))}); err != nil {
			b.Fatal(err)
		}
	}
	iss, _ := f.Issue(b.Context(), "PROJ-1")
	s := newSheet(testDeps(b, f), iss, &linksKind{})
	s.Update(kernel.SizeMsg{Width: 120, Height: 38})
	s.setRows(linkRows(iss.Links))
	b.ReportAllocs()
	for b.Loop() {
		s.dirty = true
		_ = s.View()
	}
}

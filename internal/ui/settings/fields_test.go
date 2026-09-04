package settings

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func sampleFields() []jira.Field {
	return []jira.Field{
		{ID: "customfield_30001", Key: "customfield_30001", Name: "Alpha", Custom: true},
		{ID: "customfield_30002", Key: "customfield_30002", Name: "Bravo", Custom: true},
		{ID: "customfield_30003", Key: "customfield_30003", Name: "Charlie", Custom: true},
	}
}

// fieldPicker drives fieldPickerModel the way the kernel would, feeding an
// addressed reply back to the picker itself rather than to whatever the stack
// has on top — the same shape palette's own project picker test harness is.
type fieldPicker struct {
	t    *testing.T
	m    *fieldPickerModel
	msgs []tea.Msg
}

func flyField(t *testing.T, d kernel.Deps, w, h int) *fieldPicker {
	t.Helper()
	view, ok := newFieldPicker(d).(*fieldPickerModel)
	if !ok {
		t.Fatal("newFieldPicker did not return a *fieldPickerModel")
	}
	p := &fieldPicker{t: t, m: view}
	p.send(kernel.SizeMsg{Width: w, Height: h})
	p.run(p.m.Init())
	return p
}

func (p *fieldPicker) send(msg tea.Msg) {
	p.t.Helper()
	view, cmd := p.m.Update(msg)
	m, ok := view.(*fieldPickerModel)
	if !ok {
		p.t.Fatalf("Update returned a %T", view)
	}
	p.m = m
	p.run(cmd)
}

func (p *fieldPicker) run(cmd tea.Cmd) {
	p.t.Helper()
	queue := []tea.Cmd{cmd}
	for steps := 0; len(queue) > 0; steps++ {
		if steps > 500 {
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
		if reply, addressed := msg.(kernel.ReplyMsg); addressed {
			if len(reply.To) == 0 || reply.To[0] != p.m.addr {
				p.t.Fatalf("an answer came back addressed to %v, not to the picker", reply.To)
			}
			view, follow := p.m.Update(reply.Msg)
			m, ok := view.(*fieldPickerModel)
			if !ok {
				p.t.Fatalf("Update returned a %T", view)
			}
			p.m = m
			queue = append(queue, follow)
			continue
		}
		p.msgs = append(p.msgs, msg)
	}
}

func (p *fieldPicker) press(keys ...string) {
	p.t.Helper()
	for _, k := range keys {
		p.send(stroke(k))
	}
}

func (p *fieldPicker) typeText(s string) {
	p.t.Helper()
	for _, r := range s {
		p.send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func (p *fieldPicker) popped() bool {
	for _, msg := range p.msgs {
		if _, ok := msg.(kernel.PopMsg); ok {
			return true
		}
	}
	return false
}

func fieldDeps(fields []jira.Field, site string) kernel.Deps {
	f := jiratest.New(jiratest.WithFields(fields))
	return kernel.Deps{Jira: f, Theme: defaultTheme(), Zones: zone.New(), Site: site}
}

func TestFieldPicker_FetchesAndListsTheSiteSCatalogueAlphabetically(t *testing.T) {
	useConfig(t, nil)
	p := flyField(t, fieldDeps(sampleFields(), "example.atlassian.net"), 60, 12)

	got := p.m.View()
	mustContain(t, got, "Alpha", "Bravo", "Charlie", "3 fields")
}

// Enter pins the row under the cursor and leaves the picker open, so a second
// field costs another enter rather than a fresh trip through settings.
func TestFieldPicker_EnterPinsAndKeepsThePickerOpen(t *testing.T) {
	useConfig(t, nil)
	p := flyField(t, fieldDeps(sampleFields(), "example.atlassian.net"), 60, 12)

	p.press("enter")
	if p.popped() {
		t.Fatal("enter closed the picker")
	}
	if len(p.m.pinned) != 1 || p.m.pinned[0] != "customfield_30001" {
		t.Fatalf("pinned = %v, want customfield_30001 (Alpha, first alphabetically)", p.m.pinned)
	}

	// Choosing the same row again takes it back off, the same gesture the
	// filter picker's own values answer to.
	p.press("enter")
	if len(p.m.pinned) != 0 {
		t.Fatalf("a second enter on the same row did not unpin it: %v", p.m.pinned)
	}
}

// esc writes the accumulated list, in the order things were pinned, to the
// profile it opened over, and only then pops.
func TestFieldPicker_EscSavesThePinnedListInPinOrderAndPops(t *testing.T) {
	useConfig(t, &config.Config{
		Active: "work",
		Profiles: map[string]config.Profile{
			"work": {Site: "example.atlassian.net", Email: "me@example.com", Token: config.TokenSource{Env: "JIRA_TOKEN"}},
		},
	})
	d := fieldDeps(sampleFields(), "example.atlassian.net")
	p := flyField(t, d, 60, 12)

	p.typeText("Charlie")
	p.press("enter")
	for range "Charlie" {
		p.send(tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	p.typeText("Alpha")
	p.press("enter")
	p.press("esc")

	if !p.popped() {
		t.Fatal("esc did not pop the picker")
	}
	path, err := config.Path()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"customfield_30003", "customfield_30001"}
	if got := cfg.Profiles["work"].Pinned; !equalStrings(got, want) {
		t.Errorf("Pinned = %v, want %v (Charlie pinned before Alpha)", got, want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestFieldPicker_TypingFiltersByName(t *testing.T) {
	useConfig(t, nil)
	p := flyField(t, fieldDeps(sampleFields(), "example.atlassian.net"), 60, 12)

	p.typeText("bravo")
	if len(p.m.shown) != 1 || p.m.rows[p.m.shown[0]].label != "Bravo" {
		t.Fatalf("filtering for bravo left %v", p.m.shown)
	}
}

func TestFieldPicker_NoJiraConnectionSaysSoRatherThanFetching(t *testing.T) {
	useConfig(t, nil)
	d := kernel.Deps{Theme: defaultTheme(), Zones: zone.New(), Site: "example.atlassian.net"}
	p := flyField(t, d, 60, 12)

	mustContain(t, p.m.View(), "no Jira connection")
}

func TestWritePinned_RefusesAProfileOnAnotherSite(t *testing.T) {
	useConfig(t, &config.Config{
		Active: "work",
		Profiles: map[string]config.Profile{
			"work": {Site: "example.atlassian.net", Email: "me@example.com", Token: config.TokenSource{Env: "JIRA_TOKEN"}},
		},
	})
	err := writePinned("other.atlassian.net", []string{"customfield_1"})
	if err == nil || !strings.Contains(err.Error(), "other.atlassian.net") {
		t.Fatalf("writePinned across sites = %v, want a refusal naming the mismatch", err)
	}
}

func TestPinnedFieldsSetting_IsRegisteredAndOpensTheFieldPicker(t *testing.T) {
	useConfig(t, nil)
	var setting kernel.Setting
	found := false
	for _, s := range kernel.Settings() {
		if s.ID == "issue.pinned" {
			setting, found = s, true
		}
	}
	if !found {
		t.Fatal("issue.pinned is not registered")
	}
	if setting.Section != issueSection {
		t.Errorf("Section = %q, want %q", setting.Section, issueSection)
	}

	p := fly(t, settingsDeps(defaultTheme()), []kernel.Setting{setting}, []string{issueSection}, 100, 30)
	p.press("enter")
	pushed := p.pushed()
	if len(pushed) != 1 || !strings.HasPrefix(pushed[0], fieldPickerViewID+":") {
		t.Fatalf("enter on Pinned fields did not open the field picker: %v", pushed)
	}
}

func TestPinnedSummary_NamesHowManyArePinned(t *testing.T) {
	for _, tc := range []struct {
		pinned []string
		want   string
	}{
		{nil, "none pinned"},
		{[]string{"customfield_1"}, "1 field"},
		{[]string{"customfield_1", "customfield_2"}, "2 fields"},
	} {
		useConfig(t, &config.Config{
			Active: "work",
			Profiles: map[string]config.Profile{
				"work": {
					Site: "example.atlassian.net", Email: "me@example.com",
					Token: config.TokenSource{Env: "JIRA_TOKEN"}, Pinned: tc.pinned,
				},
			},
		})
		if got := pinnedSummary(); got != tc.want {
			t.Errorf("pinnedSummary() with %v = %q, want %q", tc.pinned, got, tc.want)
		}
	}
}

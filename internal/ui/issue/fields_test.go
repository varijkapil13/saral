package issue

import (
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

// wideSummary is what breaks a two-column layout measured with len(): umlauts
// that are two bytes and one cell, CJK that is one rune and two cells, an emoji
// with a variation selector, a vulgar fraction and a typographic ligature.
const wideSummary = "Größe der Spalte prüfen — 日本語の要約 🚀 ¾ ligature ﬁ"

// relatedIssue is an issue with the three things the old pane comma-joined into
// a row of bare keys, plus a custom field only the site can name.
func relatedIssue() (jira.Issue, app.FieldLabels) {
	points := jira.Field{
		ID: "customfield_20001", Key: "customfield_20001", Name: "Aufwandsschätzung",
		Custom: true, Schema: jira.FieldSchema{Type: "number", Custom: "com.atlassian.jira:float"},
	}
	unread := jira.Field{
		ID: "customfield_20002", Key: "customfield_20002", Name: "Abnahmekriterien",
		Custom: true, Schema: jira.FieldSchema{Type: "string", Custom: "com.atlassian.jira:textarea"},
	}
	labels := app.NewFieldLabels([]jira.Field{points, unread}, []string{points.ID, unread.ID})

	iss := jira.Issue{
		ID: "30001", Key: "PROJ-2", Summary: wideSummary,
		Project: jira.ProjectRef{Key: "PROJ", Name: "Spaltenbreite"},
		Type:    jira.IssueType{Name: "Story"},
		Status:  jira.Status{Name: "Building", Category: jira.CategoryInProgress},
		Parent: &jira.IssueRef{
			Key: "PROJ-1", Summary: "Die Tabelle · 日本語",
			Status: jira.Status{Name: "Triage", Category: jira.CategoryToDo},
		},
		Subtasks: []jira.IssueRef{
			{Key: "PROJ-101", Summary: "Spaltenbreite messen", Status: jira.Status{Name: "Shipped", Category: jira.CategoryDone}},
			{Key: "PROJ-7", Summary: "🚀 launch checklist", Status: jira.Status{Name: "Triage", Category: jira.CategoryToDo}},
		},
		Links: []jira.IssueLink{
			{
				Type: "Blocks", Label: "is blocked by",
				Other: jira.IssueRef{Key: "PROJ-9", Summary: "Umlaute prüfen", Status: jira.Status{Name: "Building", Category: jira.CategoryInProgress}},
			},
		},
		Fields: jira.NewFieldSet(map[string]jira.FieldValue{
			points.ID: {Kind: jira.KindNumber, Number: 5},
		}),
		Requested: jira.NewFieldMask([]string{
			"summary", "status", "issuetype", "project", "parent", "subtasks", "issuelinks",
			points.ID, unread.ID,
		}),
	}
	return iss, labels
}

// fieldsPane is the pane showing relatedIssue, read the way a detail read hands
// it over.
func fieldsPane(t *testing.T, w, h int) *driver {
	t.Helper()

	iss, labels := relatedIssue()
	dr := newDriver(t, testDeps(newFake(4)), jira.Issue{Key: iss.Key, Summary: iss.Summary}, w, h)
	dr.send(loadedMsg{gen: dr.m.gen, issue: iss, labels: labels})
	return dr
}

// A custom field's ID differs on every site and its name is translated, so the
// label has to come from the answer the value arrived with.
func TestFields_ACustomFieldIsNamedTheWayThisSiteSpellsIt(t *testing.T) {
	t.Parallel()

	dr := fieldsPane(t, 80, 26)
	dr.key("tab", "G")

	got := dr.view()
	mustContain(t, got, "Aufwandsschätzung", "5")
	mustNotContain(t, got, "customfield_20001")
}

// Where the read did not ask for a field, the pane says so. An empty row would
// claim the site had nothing to send, which is the other answer — and a field
// that was asked for and came back empty is counted rather than drawn.
func TestFields_AFieldOutsideTheReadSaysSoRatherThanDrawingBlank(t *testing.T) {
	t.Parallel()

	dr := fieldsPane(t, 80, 26)
	dr.key("tab", "G")

	got := dr.view()
	// reporter, duedate, created, labels, components, fixVersions, timetracking,
	// resolution and resolutiondate are all outside this read.
	if n := strings.Count(got, absent); n < 5 {
		t.Errorf("only %d fields say they were not asked for:\n%s", n, got)
	}
	mustContain(t, got, "Reporter", absent, "1 more, all empty")
	// The one that was asked for and came back with nothing is not drawn at all.
	mustNotContain(t, got, "Abnahmekriterien")
}

// Subtasks and links used to be a comma-joined row of bare keys, which says
// nothing about what is blocking what. An IssueRef already carries the status
// and the summary.
func TestFields_SubtasksAndLinksCarryTheStatusAndTheSummary(t *testing.T) {
	t.Parallel()

	dr := fieldsPane(t, 80, 26)
	dr.key("tab", "G")

	got := dr.view()
	mustContain(t, got,
		"Parent", "PROJ-1", "Triage",
		"Subtasks", "PROJ-101", "Shipped", "Spaltenbreite messen",
		"is blocked by", "PROJ-9", "Building", "Umlaute prüfen",
	)
	// The keys are a column rather than a sentence, so a reader can scan them.
	lines := strings.Split(got, "\n")
	var subtasks []string
	for _, line := range lines {
		if strings.Contains(line, "PROJ-101") || strings.Contains(line, "PROJ-7 ") {
			subtasks = append(subtasks, line)
		}
	}
	if len(subtasks) != 2 {
		t.Fatalf("the two subtasks are on %d lines, want one each:\n%s", len(subtasks), got)
	}
	first := strings.Index(subtasks[0], "Shipped")
	second := strings.Index(subtasks[1], "Triage")
	if first != second {
		t.Errorf("the status column starts at %d on one row and %d on the other:\n%s", first, second, got)
	}
}

// The case that breaks a layout measured with len(): every row still has to be
// exactly as many cells as its box.
func TestFields_AWideSummaryDoesNotShiftTheColumns(t *testing.T) {
	t.Parallel()

	for _, size := range []struct{ w, h int }{{80, 20}, {90, 28}, {120, 38}} {
		dr := fieldsPane(t, size.w, size.h)
		for _, focus := range []int{0, 1, 2} {
			if focus > 0 {
				dr.key("tab")
			}
			for i, line := range strings.Split(dr.view(), "\n") {
				if got := ansi.StringWidth(line); got > size.w {
					t.Errorf("%dx%d row %d is %d cells wide, want at most %d: %q",
						size.w, size.h, i, got, size.w, line)
				}
			}
		}
	}

	// And the summary itself is cut on a cluster boundary rather than mid-rune:
	// the ligature and the emoji either survive whole or are gone.
	head := strings.SplitN(fieldsPane(t, 80, 20).view(), "\n", 2)[0]
	if strings.ContainsRune(head, '�') {
		t.Errorf("the identity line carries a replacement rune, so a cluster was cut: %q", head)
	}
}

// sprintPane is the pane showing an issue whose sprint field arrived as the
// array of sprint objects Jira Cloud answers with. Its schema says `array` of
// `json`, which the adapter has no slot for, so the value reaches the pane as
// the bytes it was read as.
func sprintPane(t *testing.T, value string, w, h int) *driver {
	t.Helper()

	sprint := jira.Field{
		ID: "customfield_13402", Key: "customfield_13402", Name: "Sprint", Custom: true,
		Schema: jira.FieldSchema{Type: "array", Items: "json", Custom: "com.pyxis.greenhopper.jira:gh-sprint"},
	}
	labels := app.NewFieldLabels([]jira.Field{sprint}, []string{sprint.ID})
	iss := jira.Issue{
		ID: "30002", Key: "PROJ-3", Summary: "an issue in a sprint",
		Project: jira.ProjectRef{Key: "PROJ", Name: "Spaltenbreite"},
		Type:    jira.IssueType{Name: "Story"},
		Status:  jira.Status{Name: "Building", Category: jira.CategoryInProgress},
		Fields: jira.NewFieldSet(map[string]jira.FieldValue{
			sprint.ID: {Kind: jira.KindUnknown, Text: value},
		}),
		Requested: jira.NewFieldMask([]string{"summary", "status", "issuetype", "project", sprint.ID}),
	}
	dr := newDriver(t, testDeps(newFake(4)), jira.Issue{Key: iss.Key, Summary: iss.Summary}, w, h)
	dr.send(loadedMsg{gen: dr.m.gen, issue: iss, labels: labels})
	return dr
}

// The sidebar used to draw the sprint field's JSON. Clipped to the column it
// says nothing at all, and the name is the only part of it anybody reads.
func TestFields_ASprintReadsAsItsNameAndNotAsItsJSON(t *testing.T) {
	t.Parallel()

	dr := sprintPane(t, `[{"id":42,"name":"DA Sprint 14","state":"active","boardId":7,`+
		`"goal":"ship the thing","startDate":"2026-09-01T09:00:00.000Z"}]`, 80, 26)
	dr.key("tab", "G")

	got := dr.view()
	mustContain(t, got, "Sprint", "DA Sprint 14")
	mustNotContain(t, got, `{"id"`, `"state"`, "boardId", "startDate")
}

// An issue that has been through two sprints names both, in the order the site
// sent them, which is the order they happened in.
func TestFields_AnIssueInTwoSprintsNamesBoth(t *testing.T) {
	t.Parallel()

	dr := sprintPane(t, `[{"id":41,"name":"DA Sprint 13","state":"closed"},`+
		`{"id":42,"name":"DA Sprint 14","state":"active"}]`, 80, 26)
	dr.key("tab", "G")

	got := dr.view()
	mustContain(t, got, "DA Sprint 13, DA Sprint 14")
}

// A shape carrying no label at all is counted rather than drawn. The value is
// on the issue whether or not this client can read it, so a row that disappears
// says it is not there — and its bytes are still what an edit writes back.
func TestFields_AnUnlabellableValueIsCountedRatherThanDrawn(t *testing.T) {
	t.Parallel()

	dr := sprintPane(t, `[{"self":"https://example.atlassian.net/1","id":10},`+
		`{"self":"https://example.atlassian.net/2","id":11}]`, 80, 26)
	dr.key("tab", "G")

	got := dr.view()
	mustContain(t, got, "Sprint", "2 "+unreadableMany)
	mustNotContain(t, got, "example.atlassian.net", `"self"`)
}

// bookkeepingPane is a real fake-generated issue read the way a detail read
// hands it over: jiratest.Gen mints Rank on every issue it produces, so this
// is the fake's own bookkeeping value rather than one built by hand for the
// test.
func bookkeepingPane(t *testing.T, w, h int) *driver {
	t.Helper()

	f := newFake(1)
	iss := readIssue(t, f, "PROJ-1")
	catalogue, err := f.Fields(t.Context())
	if err != nil {
		t.Fatalf("Fields: %v", err)
	}
	ids := make([]string, len(catalogue))
	for i := range catalogue {
		ids[i] = catalogue[i].ID
	}
	labels := app.NewFieldLabels(catalogue, ids)

	dr := newDriver(t, testDeps(f), jira.Issue{Key: iss.Key, Summary: iss.Summary}, w, h)
	dr.send(loadedMsg{gen: dr.m.gen, issue: iss, labels: labels})
	return dr
}

// The plugin's own Rank is what every one of the fake's issues carries, so
// hiding it by default is the case that matters most: a value discarded
// without being counted is exactly the failure unmodelledText already avoids
// for a value this client cannot label.
func TestFields_ThePluginsOwnRankIsHiddenAndCountedByDefault(t *testing.T) {
	t.Parallel()

	dr := bookkeepingPane(t, 90, 28)
	dr.key("tab", "G")

	got := dr.view()
	mustContain(t, got, "1 more, hidden")
	mustNotContain(t, got, "Rank", "0|i0")
}

// A custom field whose plugin key is not on the table is data somebody put on
// the issue, and has to survive sitting beside a field that is: the ordinary
// field Aufwandsschätzung and the confirmed bookkeeping key are on the one
// fixture in pkg/jira/jiratest's own catalogue.
func TestFields_AFieldWithAnUnknownPluginKeyIsNeverHidden(t *testing.T) {
	t.Parallel()

	dr := fieldsPane(t, 80, 26)
	_, rest, hidden, _ := dr.m.customFields(60)
	if hidden != 0 {
		t.Fatalf("got %d hidden fields, want 0: this fixture carries no bookkeeping key", hidden)
	}
	if !slices.ContainsFunc(rest, func(v named) bool { return v.label == "Aufwandsschätzung" }) {
		t.Error("a field with an ordinary plugin key was hidden or dropped")
	}
}

// A field id is still per-site and a bare name collides with translation, so
// every entry has to read as the plugin key it is rather than either.
func TestFields_BookkeepingKeysAreAllPluginKeysNotFieldIDs(t *testing.T) {
	t.Parallel()

	if len(bookkeepingFields) == 0 {
		t.Fatal("bookkeepingFields is empty, so this guard would pass by scanning nothing")
	}
	for _, f := range bookkeepingFields {
		if !strings.Contains(f.Key, ":") {
			t.Errorf("%q has no colon, so it does not read as a plugin field type", f.Key)
		}
		if strings.HasPrefix(f.Key, "customfield_") {
			t.Errorf("%q looks like a field id rather than a plugin key", f.Key)
		}
	}
}

// The setting is what turns bookkeeping back on for somebody who does want to
// see the rank. This test is not parallel: it flips process-wide state and
// puts it back before returning, which only holds if nothing else reads that
// state at the same time — every other test in this file assumes it stays
// off.
func TestFields_ThePluginFieldsSettingBringsRankBack(t *testing.T) {
	var setting kernel.Setting
	found := false
	for _, s := range kernel.Settings() {
		if s.ID == "issue.bookkeeping" {
			setting, found = s, true
		}
	}
	if !found {
		t.Fatal("issue.bookkeeping is not registered")
	}
	t.Cleanup(func() { showBookkeeping.Store(false) })

	if got := setting.Value(kernel.Deps{}); got != "off" {
		t.Fatalf("got %q, want off by default", got)
	}
	before := bookkeepingPane(t, 90, 28)
	before.key("tab", "G")
	mustNotContain(t, before.view(), "0|i00001:")

	setting.Set(kernel.Deps{}, "on")
	if got := setting.Value(kernel.Deps{}); got != "on" {
		t.Fatalf("got %q after Set(\"on\"), want on", got)
	}

	dr := bookkeepingPane(t, 90, 28)
	dr.key("tab", "G")
	got := dr.view()
	mustContain(t, got, "Rank", "0|i00001:")
	mustNotContain(t, got, "hidden as Jira")

	setting.Set(kernel.Deps{}, "off")
	if got := setting.Value(kernel.Deps{}); got != "off" {
		t.Fatalf("got %q after Set(\"off\"), want off", got)
	}
}

// pinnableIssue carries four custom fields with values, named so that
// alphabetical order and pin order never coincide by accident.
func pinnableIssue() (jira.Issue, app.FieldLabels) {
	mk := func(id, name string) jira.Field {
		return jira.Field{
			ID: id, Key: id, Name: name, Custom: true,
			Schema: jira.FieldSchema{Type: "string", Custom: "com.atlassian.jira:textfield"},
		}
	}
	alpha, bravo, charlie, delta :=
		mk("customfield_30001", "Alpha"), mk("customfield_30002", "Bravo"),
		mk("customfield_30003", "Charlie"), mk("customfield_30004", "Delta")
	catalogue := []jira.Field{alpha, bravo, charlie, delta}
	ids := []string{alpha.ID, bravo.ID, charlie.ID, delta.ID}
	labels := app.NewFieldLabels(catalogue, ids)

	iss := jira.Issue{
		ID: "40001", Key: "PROJ-4", Summary: "pinned fields",
		Project: jira.ProjectRef{Key: "PROJ", Name: "Pinning"},
		Type:    jira.IssueType{Name: "Story"},
		Status:  jira.Status{Name: "Building", Category: jira.CategoryInProgress},
		Fields: jira.NewFieldSet(map[string]jira.FieldValue{
			alpha.ID:   {Kind: jira.KindText, Text: "one"},
			bravo.ID:   {Kind: jira.KindText, Text: "two"},
			charlie.ID: {Kind: jira.KindText, Text: "three"},
			delta.ID:   {Kind: jira.KindText, Text: "four"},
		}),
		Requested: jira.NewFieldMask(append([]string{"summary", "status", "issuetype", "project"}, ids...)),
	}
	return iss, labels
}

// writeProfile puts a profile at config.Path, on the site testDeps hands every
// pane in this file, so pinnedFieldIDs finds it the way it would a real one.
func writeProfile(t *testing.T, pinned []string) {
	t.Helper()
	t.Setenv("SARAL_CONFIG_DIR", t.TempDir())
	cfg := config.Config{
		Active: "work",
		Profiles: map[string]config.Profile{
			"work": {
				Site: "example.atlassian.net", Email: "you@example.com",
				Token: config.TokenSource{Env: "JIRA_TOKEN"}, Pinned: pinned,
			},
		},
	}
	path, err := config.Path()
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
}

// pinnedPane opens pinnableIssue with the given profile pinned, or with no
// config file at all when pinned is nil — the "nothing pinned because there is
// nowhere to read it from" case rather than "nothing pinned because the list is
// empty", which useConfig-style helpers elsewhere keep apart the same way.
func pinnedPane(t *testing.T, pinned []string, w, h int) *driver {
	t.Helper()
	if pinned != nil {
		writeProfile(t, pinned)
	} else {
		t.Setenv("SARAL_CONFIG_DIR", t.TempDir())
	}
	iss, labels := pinnableIssue()
	dr := newDriver(t, testDeps(newFake(4)), jira.Issue{Key: iss.Key, Summary: iss.Summary}, w, h)
	dr.send(loadedMsg{gen: dr.m.gen, issue: iss, labels: labels})
	return dr
}

// Pinning out of alphabetical order is the case that proves the sidebar is
// following the pin list rather than happening to agree with it.
func TestFields_PinnedFieldsDrawFirstAndInPinOrder(t *testing.T) {
	dr := pinnedPane(t, []string{"customfield_30003", "customfield_30001"}, 90, 40)
	dr.key("tab")
	got := dr.view()

	mustContain(t, got, "Pinned", "Charlie", "Alpha", "Fields", "Bravo", "Delta")
	if at, bt := strings.Index(got, "Pinned"), strings.Index(got, "Fields"); at < 0 || bt < 0 || at > bt {
		t.Fatalf("Pinned heading does not come before Fields:\n%s", got)
	}
	if ac, aa := strings.Index(got, "Charlie"), strings.Index(got, "Alpha"); ac < 0 || aa < 0 || ac > aa {
		t.Fatalf("Charlie does not come before Alpha, though it was pinned first:\n%s", got)
	}
}

// A pinned id the site's own catalogue no longer answers for — and that no
// issue carries a value for any more — never reaches the drawing, and nothing
// about drawing the rest of the sidebar rewrites the profile that still names
// it.
func TestFields_AnUnknownPinnedIDIsSkippedButSurvivesASave(t *testing.T) {
	pinned := []string{"customfield_99999", "customfield_30002"}
	writeProfile(t, pinned)
	iss, labels := pinnableIssue()
	dr := newDriver(t, testDeps(newFake(4)), jira.Issue{Key: iss.Key, Summary: iss.Summary}, 90, 40)
	dr.send(loadedMsg{gen: dr.m.gen, issue: iss, labels: labels})
	dr.key("tab")

	got := dr.view()
	mustContain(t, got, "Pinned", "Bravo")
	mustNotContain(t, got, "customfield_99999")

	path, err := config.Path()
	if err != nil {
		t.Fatal(err)
	}
	saved, err := config.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := saved.Profiles["work"].Pinned; !slices.Equal(got, pinned) {
		t.Errorf("Pinned on disk is %v after drawing the sidebar, want %v unchanged", got, pinned)
	}
}

// A profile on a different site is never the source of what draws here: a
// session on example.atlassian.net must not pick up another site's pins.
func TestFields_APinFromAnotherSiteIsNeverDrawn(t *testing.T) {
	t.Setenv("SARAL_CONFIG_DIR", t.TempDir())
	cfg := config.Config{
		Active: "other",
		Profiles: map[string]config.Profile{
			"other": {
				Site: "other.atlassian.net", Email: "you@example.com",
				Token: config.TokenSource{Env: "JIRA_TOKEN"}, Pinned: []string{"customfield_30001"},
			},
		},
	}
	path, err := config.Path()
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}

	iss, labels := pinnableIssue()
	dr := newDriver(t, testDeps(newFake(4)), jira.Issue{Key: iss.Key, Summary: iss.Summary}, 90, 40)
	dr.send(loadedMsg{gen: dr.m.gen, issue: iss, labels: labels})
	dr.key("tab")

	mustNotContain(t, dr.view(), "Pinned")
}

// The sidebar with nothing pinned, three pinned, and one pinned field the site
// no longer answers for — the three shapes docs/FIELDS.md asks for goldens of.
func TestFields_PinnedGoldens(t *testing.T) {
	t.Run("nothing_pinned", func(t *testing.T) {
		dr := pinnedPane(t, nil, 90, 40)
		dr.key("tab")
		golden(t, "pinned_none_90x40.golden", dr.view())
	})
	t.Run("three_pinned", func(t *testing.T) {
		dr := pinnedPane(t, []string{"customfield_30004", "customfield_30002", "customfield_30001"}, 90, 40)
		dr.key("tab")
		golden(t, "pinned_three_90x40.golden", dr.view())
	})
	t.Run("one_absent_from_site", func(t *testing.T) {
		dr := pinnedPane(t, []string{"customfield_30001", "customfield_99999"}, 90, 40)
		dr.key("tab")
		golden(t, "pinned_absent_90x40.golden", dr.view())
	})
}

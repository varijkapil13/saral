package move

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func meta(id, name string) jira.FieldMeta {
	return jira.FieldMeta{Field: jira.FieldRef{ID: id, Name: name}, Name: name}
}

func screen(fields ...jira.FieldMeta) jira.Schema { return jira.Schema{Fields: fields} }

func withValues(values map[string]jira.FieldValue) jira.Issue {
	return jira.Issue{Fields: jira.NewFieldSet(values)}
}

type holding struct {
	id    string
	count int
}

func heldBy(drops []Dropped) []holding {
	out := make([]holding, 0, len(drops))
	for i := range drops {
		out = append(out, holding{drops[i].Meta.Field.ID, drops[i].Count})
	}
	return out
}

func TestDropsOf_ComparesTheSourceScreensWithTheTargetsAndCountsWhoHoldAValue(t *testing.T) {
	t.Parallel()
	text := func(s string) jira.FieldValue { return jira.FieldValue{Kind: jira.KindText, Text: s} }
	for name, tc := range map[string]struct {
		sources []jira.Schema
		target  jira.Schema
		issues  []jira.Issue
		want    []holding
	}{
		"a field on both screens is kept": {
			sources: []jira.Schema{screen(meta("customfield_1", "Kostenstelle"))},
			target:  screen(meta("customfield_1", "Kostenstelle")),
			issues:  []jira.Issue{withValues(map[string]jira.FieldValue{"customfield_1": text("A")})},
		},
		"a field the target lacks is dropped from the issues holding it": {
			sources: []jira.Schema{screen(meta("customfield_1", "Kostenstelle"))},
			target:  screen(),
			issues: []jira.Issue{
				withValues(map[string]jira.FieldValue{"customfield_1": text("A")}),
				{},
				withValues(map[string]jira.FieldValue{"customfield_1": text("B")}),
			},
			want: []holding{{"customfield_1", 2}},
		},
		"a field nobody holds a value in loses nothing": {
			sources: []jira.Schema{screen(meta("customfield_1", "Kostenstelle"))},
			target:  screen(),
			issues: []jira.Issue{
				withValues(map[string]jira.FieldValue{"customfield_1": text("  ")}),
				withValues(map[string]jira.FieldValue{"customfield_1": {Kind: jira.KindEmpty}}),
				withValues(map[string]jira.FieldValue{"customfield_1": {Kind: jira.KindOptions}}),
			},
		},
		"the fields a move sets, maps or keeps are never reported": {
			sources: []jira.Schema{screen(meta("summary", "Zusammenfassung"), meta("reporter", "Berichterstatter"),
				meta("assignee", "Bearbeiter"), meta("issuetype", "Vorgangstyp"), meta("project", "Projekt"),
				meta("attachment", "Anhang"), meta("issuelinks", "Verknüpfungen"))},
			target: screen(),
			issues: []jira.Issue{{Summary: "x", Reporter: &jira.User{AccountID: "a"}, Assignee: &jira.User{AccountID: "a"}}},
		},
		"system fields are read off the issue itself": {
			sources: []jira.Schema{screen(meta("labels", "Stichwörter"), meta("fixVersions", "Lösungsversion"),
				meta("duedate", "Fällig"), meta("description", "Beschreibung"), meta("priority", "Priorität"),
				meta("components", "Komponenten"), meta("timetracking", "Zeiterfassung"))},
			target: screen(meta("priority", "Priorität")),
			issues: []jira.Issue{
				{Labels: []string{"x"}, Due: jira.Date{Year: 2026, Month: time.March, Day: 1}},
				{Labels: []string{"y"}, FixVersions: []jira.Version{{ID: "1"}}, Description: adf.Doc{Content: []adf.Node{{Type: "paragraph"}}}},
				{TimeTracking: &jira.TimeTracking{}},
			},
			want: []holding{
				{"labels", 2},
				{"fixVersions", 1},
				{"duedate", 1},
				{"description", 1},
			},
		},
		"two source screens are one list, most widely held first": {
			sources: []jira.Schema{
				screen(meta("customfield_1", "Eins"), meta("customfield_2", "Zwei")),
				screen(meta("customfield_2", "Zwei"), meta("customfield_3", "Drei")),
			},
			target: screen(),
			issues: []jira.Issue{
				withValues(map[string]jira.FieldValue{"customfield_1": text("a"), "customfield_3": text("c")}),
				withValues(map[string]jira.FieldValue{"customfield_3": {Kind: jira.KindNumber, Number: 0}}),
			},
			want: []holding{
				{"customfield_3", 2},
				{"customfield_1", 1},
			},
		},
		"a screen with no label falls back to the catalogue name and then the id": {
			sources: []jira.Schema{screen(
				jira.FieldMeta{Field: jira.FieldRef{ID: "customfield_1", Name: "Katalog"}},
				jira.FieldMeta{Field: jira.FieldRef{ID: "customfield_2"}},
			)},
			target: screen(),
			issues: []jira.Issue{withValues(map[string]jira.FieldValue{"customfield_1": text("a"), "customfield_2": text("b")})},
			want: []holding{
				{"customfield_1", 1},
				{"customfield_2", 1},
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := heldBy(dropsOf(gaps(tc.sources, tc.target), tc.issues))
			if !slices.Equal(got, tc.want) {
				t.Errorf("dropped\n got %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

// screens is the fake with the target's create screen narrowed, which the fake
// cannot be told: it answers the same screen for every project.
type screens struct {
	*jiratest.Fake
	without map[string]bool
}

func (s *screens) CreateMeta(ctx context.Context, project, typeID string) (jira.Schema, error) {
	schema, err := s.Fake.CreateMeta(ctx, project, typeID)
	if err != nil || project != "OTHER" {
		return schema, err
	}
	kept := schema.Fields[:0:0]
	for i := range schema.Fields {
		if !s.without[schema.Fields[i].Field.ID] {
			kept = append(kept, schema.Fields[i])
		}
	}
	schema.Fields = kept
	return schema, nil
}

func narrowed(t *testing.T) (*screens, []jira.Issue, jira.Schema) {
	t.Helper()
	f := newFake(6, jiratest.WithIssues(jiratest.GenFor("OTHER", 4)))
	s := &screens{Fake: f, without: map[string]bool{"labels": true, "priority": true}}
	iss := seeded(t, f, "PROJ-1", "PROJ-2", "PROJ-3")
	for i := range iss {
		iss[i].Requested = jira.NewFieldMask([]string{"summary", "status", "issuetype", "project"})
	}
	target, err := s.CreateMeta(t.Context(), "OTHER", iss[0].Type.ID)
	if err != nil {
		t.Fatal(err)
	}
	return s, iss, target
}

func TestCheckDrops_CountsWhatTheTargetHasNoPlaceForAndReadsOnlyThoseValues(t *testing.T) {
	t.Parallel()
	s, iss, target := narrowed(t)

	got, err := CheckDrops(t.Context(), s, LeavingIssues(iss, "OTHER"), target, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Leaving != 3 {
		t.Errorf("%d issues are leaving, want 3", got.Leaving)
	}
	ids := make([]string, 0, len(got.Fields))
	for _, d := range got.Fields {
		ids = append(ids, d.Meta.Field.ID)
	}
	if !slices.Contains(ids, "priority") || !slices.Contains(ids, "labels") {
		t.Errorf("the drops are %v, want priority and labels among them", ids)
	}
	if n := countCalls(s.Fake, "Search"); n != 1 {
		t.Errorf("the values were read in %d searches, want one", n)
	}
	if len(got.Sources) == 0 {
		t.Error("the source screens read were not handed back")
	}

	before := countCalls(s.Fake, "CreateMeta")
	if _, err := CheckDrops(t.Context(), s, LeavingIssues(iss, "OTHER"), target, got.Sources); err != nil {
		t.Fatal(err)
	}
	if n := countCalls(s.Fake, "CreateMeta") - before; n != 0 {
		t.Errorf("a source screen already read was asked for %d more times", n)
	}
}

func TestCheckDrops_AnIssueAlreadyInTheTargetIsNotLeaving(t *testing.T) {
	t.Parallel()
	iss := []jira.Issue{{Key: "OTHER-1", Project: jira.ProjectRef{Key: "OTHER"}}, {Key: "PROJ-1", Project: jira.ProjectRef{Key: "PROJ"}}}
	if got := LeavingIssues(iss, "OTHER"); len(got) != 1 || got[0].Key != "PROJ-1" {
		t.Errorf("leaving are %v", got)
	}
}

func TestCheckDrops_APortFailurePassesThroughUnwrapped(t *testing.T) {
	t.Parallel()
	for _, call := range []string{"CreateMeta", "Search"} {
		for name, want := range failures() {
			t.Run(call+"/"+name, func(t *testing.T) {
				t.Parallel()
				s, iss, target := narrowed(t)
				var known map[Pair]jira.Schema
				if call == "Search" {
					schema, err := s.CreateMeta(t.Context(), iss[0].Project.Key, iss[0].Type.ID)
					if err != nil {
						t.Fatal(err)
					}
					known = map[Pair]jira.Schema{{Project: iss[0].Project.Key, TypeID: iss[0].Type.ID}: schema}
				}
				s.FailNext(want)
				_, err := CheckDrops(t.Context(), s, iss[:1], target, known)
				mustBe(t, err, want)
			})
		}
	}
}

func TestCheckDrops_RefusesWhatItCannotCheck(t *testing.T) {
	t.Parallel()
	s, iss, target := narrowed(t)

	shapeless := []jira.Issue{{Key: "PROJ-1"}}
	if _, err := CheckDrops(t.Context(), s, shapeless, target, nil); !errors.Is(err, ErrNoSourceShape) {
		t.Errorf("an issue with no project or type answered %v", err)
	}

	odd := iss[0]
	odd.Key = `PROJ-1") OR key in ("X-1`
	if _, err := CheckDrops(t.Context(), s, []jira.Issue{odd}, target, nil); !errors.Is(err, ErrOddKey) {
		t.Errorf("a key JQL cannot name answered %v", err)
	}
}

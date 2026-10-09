package issue

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appquery "github.com/varijkapil13/saral/internal/app/query"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func meta(id, name string, schema jira.FieldSchema, allowed ...jira.Option) jira.FieldMeta {
	return jira.FieldMeta{
		Field:         jira.FieldRef{ID: id, Name: name, Schema: schema},
		Name:          name,
		Operations:    []string{"set"},
		AllowedValues: allowed,
	}
}

func option(id, label string, children ...jira.Option) jira.Option {
	return jira.Option{ID: id, Label: label, Children: children}
}

func newEntry(m jira.FieldMeta) Entry {
	return Entry{Meta: m, Shape: ShapeOf(m), Loc: time.UTC}
}

func testScreen() jira.Schema {
	return jira.Schema{
		Project:   jira.ProjectRef{Key: "PROJ"},
		IssueType: jira.IssueType{ID: "10001", Name: "Task"},
		Fields: []jira.FieldMeta{
			meta("customfield_1", "Scope", jira.FieldSchema{Type: "option-with-child"},
				option("1", "Tier One", option("11", "Pilot"))),
		},
	}
}

func TestSchemas_KeepsAScreenUntilItsTimeIsUp(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 5, 9, 0, 0, 0, time.UTC)
	cache := NewSchemas(SchemaTTL, func() time.Time { return now })
	key := Screen{Project: "PROJ", IssueType: "10001"}
	cache.Put(key, testScreen())

	if _, ok := cache.Get(key); !ok {
		t.Fatal("the screen was not kept at all")
	}
	now = now.Add(SchemaTTL - time.Minute)
	if _, ok := cache.Get(key); !ok {
		t.Error("the screen was dropped before its time was up")
	}
	now = now.Add(2 * time.Minute)
	if _, ok := cache.Get(key); ok {
		t.Error("the screen outlived its time")
	}
}

func TestSchemas_KeepsTwoIssueTypesApart(t *testing.T) {
	t.Parallel()

	cache := NewSchemas(SchemaTTL, time.Now)
	cache.Put(Screen{Project: "PROJ", IssueType: "10001"}, testScreen())

	if _, ok := cache.Get(Screen{Project: "PROJ", IssueType: "10002"}); ok {
		t.Error("one issue type's screen was handed out for another")
	}
	if _, ok := cache.Get(Screen{Project: "OTHER", IssueType: "10001"}); ok {
		t.Error("one project's screen was handed out for another")
	}
}

func TestSchemas_HandsOutACopyRatherThanWhatItHolds(t *testing.T) {
	t.Parallel()

	cache := NewSchemas(SchemaTTL, time.Now)
	key := Screen{Project: "PROJ", IssueType: "10001"}
	cache.Put(key, testScreen())

	first, _ := cache.Get(key)
	first.Fields[0].Name = "rewritten"
	first.Fields[0].AllowedValues[0].Children[0].Label = "rewritten"
	first.Fields[0].Operations[0] = "rewritten"

	second, _ := cache.Get(key)
	if second.Fields[0].Name == "rewritten" ||
		second.Fields[0].AllowedValues[0].Children[0].Label == "rewritten" ||
		second.Fields[0].Operations[0] == "rewritten" {
		t.Error("one form wrote through the cache into what the next one is built from")
	}
}

func TestSchemas_ForgetsEverythingOnAPurge(t *testing.T) {
	t.Parallel()

	cache := NewSchemas(SchemaTTL, time.Now)
	key := Screen{Project: "PROJ", IssueType: "10001"}
	cache.Put(key, testScreen())
	cache.Purge()

	if _, ok := cache.Get(key); ok {
		t.Error("a purge left the screen behind")
	}
}

func TestCreateDrafts_KeepsOnlyTheFieldsSomethingWasPutIn(t *testing.T) {
	t.Parallel()

	store := NewCreateDrafts(t.TempDir())
	key := CreateDraftKey{Site: "example.atlassian.net", Project: "PROJ", IssueType: "10001"}

	filled := newEntry(meta("summary", "Summary", jira.FieldSchema{Type: "string"}))
	filled.Text = "half a thought"
	empty := newEntry(meta("duedate", "Due", jira.FieldSchema{Type: "date"}))
	chosen := newEntry(meta("cascade", "Where", jira.FieldSchema{Type: "option-with-child"}, option("1", "One")))
	chosen.Picked = []jira.Option{{ID: "1", Label: "One", Children: []jira.Option{{ID: "2", Label: "Two"}}}}
	at := time.Date(2026, time.March, 5, 9, 0, 0, 0, time.UTC)

	if err := store.Save(key, NewCreateDraft(key, []Entry{filled, empty, chosen}, at)); err != nil {
		t.Fatal(err)
	}
	kept, ok, err := store.Load(key)
	if err != nil || !ok {
		t.Fatalf("load = %v, %v", ok, err)
	}
	if len(kept.Values) != 2 {
		t.Fatalf("the draft holds %d fields, want only the two filled in: %+v", len(kept.Values), kept.Values)
	}
	if kept.Values["summary"].Text != "half a thought" {
		t.Errorf("the summary reads %q", kept.Values["summary"].Text)
	}
	back := FromDraftOptions(kept.Values["cascade"].Picked)
	if len(back) != 1 || len(back[0].Children) != 1 || back[0].Children[0].ID != "2" {
		t.Errorf("the cascade came back as %+v", back)
	}
	if !kept.SavedAt.Equal(at) || kept.Site != key.Site || kept.IssueType != key.IssueType {
		t.Errorf("the draft's own record reads %+v", kept)
	}

	if err := store.Save(key, NewCreateDraft(key, []Entry{empty}, at)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(store.path(key)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a form emptied of everything left its draft file: %v", err)
	}
}

func TestCreateDrafts_KeepsNothingWithNowhereToWrite(t *testing.T) {
	t.Parallel()

	store := NewCreateDrafts("")
	key := CreateDraftKey{Site: "s", Project: "PROJ", IssueType: "1"}
	f := newEntry(meta("summary", "Summary", jira.FieldSchema{Type: "string"}))
	f.Text = "x"
	if err := store.Save(key, NewCreateDraft(key, []Entry{f}, time.Time{})); err != nil {
		t.Errorf("save = %v", err)
	}
	if _, ok, err := store.Load(key); ok || err != nil {
		t.Errorf("load = %v, %v from a store with no directory", ok, err)
	}
	if err := store.Discard(key); err != nil {
		t.Errorf("discard = %v", err)
	}
}

func TestCreateDrafts_NeverLeavesItsOwnDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := NewCreateDrafts(root)
	path := store.path(CreateDraftKey{Site: "../..", Project: "../x", IssueType: "a/b"})
	rel, err := filepath.Rel(filepath.Join(root, "create"), path)
	if err != nil || strings.HasPrefix(rel, "..") || strings.Count(rel, string(filepath.Separator)) != 1 {
		t.Errorf("a hostile key put the draft at %s", path)
	}
}

func TestCreateDrafts_WritesJSONAnotherBuildCanRead(t *testing.T) {
	t.Parallel()

	store := NewCreateDrafts(t.TempDir())
	key := CreateDraftKey{Site: "s", Project: "PROJ", IssueType: "1"}
	f := newEntry(meta("summary", "Summary", jira.FieldSchema{Type: "string"}))
	f.Text = "portable"
	if err := store.Save(key, NewCreateDraft(key, []Entry{f}, time.Time{})); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(store.path(key))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("the draft is not JSON: %v", err)
	}
	for _, name := range []string{"site", "project", "issueType", "savedAt", "values"} {
		if _, ok := raw[name]; !ok {
			t.Errorf("the draft has no %q: %s", name, body)
		}
	}
}

func TestTypes_AreTheOnesInUseOnce(t *testing.T) {
	t.Parallel()
	f := testFake(6)
	types, err := Types(t.Context(), appquery.NewSearch(f), "PROJ")
	if err != nil || len(types) == 0 {
		t.Fatalf("types %v, %v", types, err)
	}
	seen := map[string]bool{}
	for _, typ := range types {
		if seen[typ.ID] {
			t.Errorf("%s is offered twice", typ.Name)
		}
		seen[typ.ID] = true
	}
}

func TestCreateScreen_ReadsOnceThenFromTheCache(t *testing.T) {
	t.Parallel()
	f := testFake(2)
	cache := NewSchemas(SchemaTTL, time.Now)
	types, err := Types(t.Context(), appquery.NewSearch(f), "PROJ")
	if err != nil {
		t.Fatal(err)
	}
	key := Screen{Project: "PROJ", IssueType: types[0].ID}
	for range 2 {
		if _, err := CreateScreen(t.Context(), f, cache, key); err != nil {
			t.Fatal(err)
		}
	}
	if n := callsTo(f, "CreateMeta"); n != 1 {
		t.Errorf("the screen was read %d times, want once", n)
	}
}

func TestCreate_Failures(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	for name, call := range map[string]func(*jiratest.Fake) error{
		"types": func(f *jiratest.Fake) error { _, err := Types(ctx, appquery.NewSearch(f), "PROJ"); return err },
		"screen": func(f *jiratest.Fake) error {
			_, err := CreateScreen(ctx, f, NewSchemas(SchemaTTL, time.Now), Screen{Project: "PROJ", IssueType: "10001"})
			return err
		},
		"create": func(f *jiratest.Fake) error {
			_, err := Create(ctx, f, jira.IssueInput{ProjectKey: "PROJ", IssueTypeID: "10001", Summary: "x"})
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			failsWith(t, call)
		})
	}
}

func TestCreateInput_PutsSystemFieldsInTheirSlots(t *testing.T) {
	t.Parallel()
	summary := newEntry(meta("summary", "Summary", jira.FieldSchema{Type: "string", System: "summary"}))
	summary.Text = "  A title "
	labels := newEntry(meta("labels", "Labels", jira.FieldSchema{Type: "array", Items: "string", System: "labels"}))
	labels.Text = "a, b a"
	points := newEntry(meta("customfield_1", "Points", jira.FieldSchema{Type: "number"}))
	points.Text = "3"
	empty := newEntry(meta("customfield_2", "Notes", jira.FieldSchema{Type: "string"}))
	in := CreateInput("PROJ", "10001", []Entry{summary, labels, points, empty})
	if in.Summary != "A title" || strings.Join(in.Labels, ",") != "a,b" {
		t.Errorf("summary %q, labels %v", in.Summary, in.Labels)
	}
	if v, ok := in.Fields.ByID("customfield_1"); !ok || v.Number != 3 {
		t.Errorf("points went as %+v, %v", v, ok)
	}
	if _, ok := in.Fields.ByID("customfield_2"); ok {
		t.Error("an empty field went")
	}
}

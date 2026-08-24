package form

import (
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
)

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

func TestSchemaCache_KeepsAScreenUntilItsTimeIsUp(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.March, 5, 9, 0, 0, 0, time.UTC)
	cache := newSchemaCache(schemaTTL, func() time.Time { return now })
	key := screen{project: "PROJ", issueType: "10001"}
	cache.put(key, testScreen())

	if _, ok := cache.get(key); !ok {
		t.Fatal("the screen was not kept at all")
	}
	now = now.Add(schemaTTL - time.Minute)
	if _, ok := cache.get(key); !ok {
		t.Error("the screen was dropped before its time was up")
	}
	now = now.Add(2 * time.Minute)
	if _, ok := cache.get(key); ok {
		t.Error("the screen outlived its time")
	}
}

func TestSchemaCache_KeepsTwoIssueTypesApart(t *testing.T) {
	t.Parallel()

	cache := newSchemaCache(schemaTTL, time.Now)
	cache.put(screen{project: "PROJ", issueType: "10001"}, testScreen())

	if _, ok := cache.get(screen{project: "PROJ", issueType: "10002"}); ok {
		t.Error("one issue type's screen was handed out for another")
	}
	if _, ok := cache.get(screen{project: "OTHER", issueType: "10001"}); ok {
		t.Error("one project's screen was handed out for another")
	}
}

func TestSchemaCache_HandsOutACopyRatherThanWhatItHolds(t *testing.T) {
	t.Parallel()

	cache := newSchemaCache(schemaTTL, time.Now)
	key := screen{project: "PROJ", issueType: "10001"}
	cache.put(key, testScreen())

	first, _ := cache.get(key)
	first.Fields[0].Name = "rewritten"
	first.Fields[0].AllowedValues[0].Children[0].Label = "rewritten"
	first.Fields[0].Operations[0] = "rewritten"

	second, _ := cache.get(key)
	if second.Fields[0].Name == "rewritten" ||
		second.Fields[0].AllowedValues[0].Children[0].Label == "rewritten" ||
		second.Fields[0].Operations[0] == "rewritten" {
		t.Error("one form wrote through the cache into what the next one is built from")
	}
}

func TestSchemaCache_ForgetsEverythingOnAPurge(t *testing.T) {
	t.Parallel()

	cache := newSchemaCache(schemaTTL, time.Now)
	key := screen{project: "PROJ", issueType: "10001"}
	cache.put(key, testScreen())
	cache.purge()

	if _, ok := cache.get(key); ok {
		t.Error("a purge left the screen behind")
	}
}

func TestDraftStore_KeepsOnlyTheFieldsSomethingWasPutIn(t *testing.T) {
	t.Parallel()

	store := newDraftStore()
	key := screen{project: "PROJ", issueType: "10001"}

	filled := newField(meta("summary", "Summary", jira.FieldSchema{Type: "string"}), time.UTC)
	filled.text = "half a thought"
	empty := newField(meta("duedate", "Due", jira.FieldSchema{Type: "date"}), time.UTC)
	chosen := newField(meta("priority", "Priority", jira.FieldSchema{Type: "priority"}, option("1", "One")), time.UTC)
	chosen.picked = []jira.Option{option("1", "One")}

	store.put(key, []*field{filled, empty, chosen})
	kept := store.get(key)

	if len(kept) != 2 {
		t.Fatalf("the draft holds %d fields, want only the two that were filled in: %v", len(kept), kept)
	}
	if kept["summary"].text != "half a thought" {
		t.Errorf("the summary reads %q", kept["summary"].text)
	}
	if len(kept["priority"].picked) != 1 {
		t.Errorf("the priority reads %+v", kept["priority"].picked)
	}

	kept["priority"].picked[0].Label = "rewritten"
	if store.get(key)["priority"].picked[0].Label == "rewritten" {
		t.Error("a reader wrote through into the stored draft")
	}

	store.put(key, []*field{empty})
	if len(store.get(key)) != 0 {
		t.Error("a form emptied of everything left a draft behind")
	}
}

func TestDraftStore_ForgetsOneScreenWithoutTouchingAnother(t *testing.T) {
	t.Parallel()

	store := newDraftStore()
	one := screen{project: "PROJ", issueType: "10001"}
	two := screen{project: "PROJ", issueType: "10002"}

	f := newField(meta("summary", "Summary", jira.FieldSchema{Type: "string"}), time.UTC)
	f.text = "kept"
	store.put(one, []*field{f})
	store.put(two, []*field{f})
	store.clear(one)

	if len(store.get(one)) != 0 {
		t.Error("the cleared draft is still there")
	}
	if len(store.get(two)) != 1 {
		t.Error("clearing one screen's draft took another's with it")
	}
}

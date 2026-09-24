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

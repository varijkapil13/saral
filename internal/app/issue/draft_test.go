package issue

import (
	"encoding/json"
	"os"
	"testing"
)

func TestDrafts_KeepLoadAndDiscard(t *testing.T) {
	t.Parallel()
	store := NewDrafts(t.TempDir())
	d := Draft{Key: "PROJ-1", Site: "example.atlassian.net", Values: map[string]string{"summary": "mine"},
		Picks: map[string][]DraftOption{"customfield_1": ToDraftOptions(nil)}}
	if err := store.Save(d); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(store.path(d.Site, d.Key))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]map[string]any
	_ = json.Unmarshal(body, &raw)
	if picks, ok := raw["picks"]["customfield_1"].([]any); !ok || len(picks) != 0 {
		t.Errorf("an emptied pick is kept as %s, want []", body)
	}
	kept, ok, err := store.Load(d.Site, d.Key)
	if err != nil || !ok || kept.Values["summary"] != "mine" {
		t.Fatalf("load = %+v, %v, %v", kept, ok, err)
	}
	if err := store.Save(Draft{Key: d.Key, Site: d.Site}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := store.Load(d.Site, d.Key); ok {
		t.Error("saving an empty draft left the old one")
	}
	if err := NewDrafts("").Save(d); err != nil {
		t.Errorf("a store with nowhere to write said %v", err)
	}
}

func TestHeldAndWithHeld(t *testing.T) {
	t.Parallel()
	d := Draft{Key: "K", Values: map[string]string{"a": "1", "b": "2"}, Base: EditBase{Fields: map[string]string{"a": "x", "b": "y"}}}
	held := Held(d, func(id string) bool { return id == "b" })
	if len(held.Values) != 1 || held.Values["b"] != "2" || held.Base.Fields["b"] != "y" || held.Base.Fields["a"] != "" {
		t.Errorf("held %+v", held)
	}
	merged := WithHeld(Draft{Values: map[string]string{"b": "mine"}}, Draft{Values: map[string]string{"b": "held", "c": "3"}})
	if merged.Values["b"] != "mine" || merged.Values["c"] != "3" {
		t.Errorf("merged %+v", merged.Values)
	}
}

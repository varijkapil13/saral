package release

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	appcache "github.com/varijkapil13/saral/internal/app/cache"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func newFake(issues []jira.Issue) *jiratest.Fake {
	return jiratest.New(jiratest.WithProject("PROJ", jiratest.Scrum), jiratest.WithIssues(issues))
}

func refusals() map[string]error {
	return map[string]error{
		"a 403":       &jira.CapabilityError{Reason: "you may not"},
		"a 429":       &jira.RateLimitError{RetryAfter: time.Minute},
		"a transport": &jira.TransportError{Op: "GET", Err: errors.New("connection reset")},
	}
}

func TestLoad_ReadsTheProjectsVersionsUncounted(t *testing.T) {
	t.Parallel()
	got, err := Load(context.Background(), newFake(nil), "PROJ")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(jiratest.VersionsFor("PROJ")) {
		t.Fatalf("read %d versions, want the %d seeded", len(got), len(jiratest.VersionsFor("PROJ")))
	}
	for _, v := range got {
		if v.Unresolved != nil {
			t.Errorf("%s came back counted", v.Name)
		}
	}
}

func TestVersionCalls_PassARefusalThrough(t *testing.T) {
	t.Parallel()
	id := jiratest.VersionsFor("PROJ")[0].ID
	calls := map[string]func(f *jiratest.Fake) error{
		"load": func(f *jiratest.Fake) error {
			_, err := Load(context.Background(), f, "PROJ")
			return err
		},
		"save": func(f *jiratest.Fake) error {
			_, err := Save(context.Background(), f, jira.VersionInput{ProjectKey: "PROJ", Name: "9.0"})
			return err
		},
		"count": func(f *jiratest.Fake) error {
			_, err := CountOpen(context.Background(), f, id)
			return err
		},
		"release": func(f *jiratest.Fake) error {
			_, err := Release(context.Background(), f, id, jira.ReleaseAnyway, "", 0)
			return err
		},
	}
	for call, run := range calls {
		for name, want := range refusals() {
			t.Run(call+"/"+name, func(t *testing.T) {
				t.Parallel()
				f := newFake(nil)
				f.FailNext(want)
				if err := run(f); !errors.Is(err, want) {
					t.Errorf("got %v, want %v passed through", err, want)
				}
			})
		}
	}
}

func TestSaveAndCount_AnswerWhatTheSiteHolds(t *testing.T) {
	t.Parallel()
	f := newFake(nil)
	v, err := Save(context.Background(), f, jira.VersionInput{ProjectKey: "PROJ", Name: "9.0"})
	if err != nil || v.ID == "" || v.Name != "9.0" {
		t.Fatalf("saved %+v, %v", v, err)
	}
	open, err := CountOpen(context.Background(), f, v.ID)
	if err != nil || open != 0 {
		t.Errorf("a new version counts %d open, %v", open, err)
	}
}

func TestUpdateOf_CarriesEveryValueTheVersionHad(t *testing.T) {
	t.Parallel()
	v := jira.Version{
		ID: "1", Name: "2.0", Description: "d",
		StartDate: jira.Date{Year: 2026, Month: 1, Day: 1}, ReleaseDate: jira.Date{Year: 2026, Month: 2, Day: 1},
	}
	in := Archiving(v, true)
	if in.ID != v.ID || in.Name != v.Name || in.Description != v.Description ||
		in.StartDate != v.StartDate || in.ReleaseDate != v.ReleaseDate {
		t.Errorf("archiving sent %+v, want the version's own values", in)
	}
	if in.Archived == nil || !*in.Archived {
		t.Error("archiving did not ask for archived")
	}
}

func TestPut_KeepsACountAlreadyReadAndAppendsANewVersion(t *testing.T) {
	t.Parallel()
	three := 3
	versions := []jira.Version{{ID: "1", Name: "1.0", Unresolved: &three}}
	versions = Put(versions, jira.Version{ID: "1", Name: "1.0.1"})
	if versions[0].Name != "1.0.1" || versions[0].Unresolved == nil || *versions[0].Unresolved != 3 {
		t.Errorf("the write replaced %+v and lost the count", versions[0])
	}
	versions = Put(versions, jira.Version{ID: "2"})
	if len(versions) != 2 || versions[1].ID != "2" {
		t.Errorf("a new version was not appended: %+v", versions)
	}
	if _, ok := ByID(versions, "2"); !ok {
		t.Error("ByID does not find the appended version")
	}
	if _, ok := ByID(versions, "9"); ok {
		t.Error("ByID found a version that is not there")
	}
}

func TestMoveTargets_OfferOnlyUnshippedOthers(t *testing.T) {
	t.Parallel()
	from := jira.Version{ID: "1", ProjectID: "A"}
	versions := []jira.Version{
		from,
		{ID: "2", ProjectID: "A", Released: true},
		{ID: "3", ProjectID: "A", Archived: true},
		{ID: "4", ProjectID: "A"},
		{ID: "5", ProjectID: "B"},
	}
	ids := func(vs []jira.Version) []string {
		out := make([]string, 0, len(vs))
		for _, v := range vs {
			out = append(out, v.ID)
		}
		return out
	}
	if got := ids(MoveTargets(versions, from, false)); !slices.Equal(got, []string{"4", "5"}) {
		t.Errorf("targets %v, want 4 and 5", got)
	}
	if got := ids(MoveTargets(versions, from, true)); !slices.Equal(got, []string{"4"}) {
		t.Errorf("own-project targets %v, want 4", got)
	}
}

type memVersions struct {
	held map[string][]jira.Version
	err  error
}

func (m *memVersions) Versions(project string) (appcache.VersionsSnapshot, bool) {
	vs, ok := m.held[project]
	return appcache.VersionsSnapshot{Versions: vs}, ok
}

func (m *memVersions) PutVersions(project string, versions []jira.Version) error {
	if m.err != nil {
		return m.err
	}
	m.held[project] = versions
	return nil
}

func (m *memVersions) ForgetVersions(project string) error {
	if m.err != nil {
		return m.err
	}
	delete(m.held, project)
	return nil
}

func TestCache_ACacheThatKeepsNoVersionsIsAMiss(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]any{"nil": nil, "something else": struct{}{}} {
		got := NewCache(c)
		if got.Held() {
			t.Errorf("%s: held", name)
		}
		if _, ok := got.Recall("PROJ"); ok {
			t.Errorf("%s: recalled something", name)
		}
		if err := got.Keep("PROJ", nil); err != nil {
			t.Errorf("%s: keep failed: %v", name, err)
		}
		if err := got.Forget("PROJ"); err != nil {
			t.Errorf("%s: forget failed: %v", name, err)
		}
	}
}

func TestCache_KeepsRecallsAndForgets(t *testing.T) {
	t.Parallel()
	mem := &memVersions{held: map[string][]jira.Version{}}
	c := NewCache(mem)
	if err := c.Keep("PROJ", []jira.Version{{ID: "1"}}); err != nil {
		t.Fatal(err)
	}
	snap, ok := c.Recall("PROJ")
	if !ok || len(snap.Versions) != 1 {
		t.Fatalf("recalled %+v, %v", snap, ok)
	}
	if err := c.Forget("PROJ"); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Recall("PROJ"); ok {
		t.Error("a forgotten project was recalled")
	}
	mem.err = errors.New("disk full")
	if err := c.Keep("PROJ", nil); !errors.Is(err, mem.err) {
		t.Errorf("a failed write said %v", err)
	}
}

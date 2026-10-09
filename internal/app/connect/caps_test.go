package connect

import (
	"errors"
	"testing"
	"time"

	appcache "github.com/varijkapil13/saral/internal/app/cache"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

type capsStore struct {
	held    map[string]appcache.CapsSnapshot
	written map[string]jira.Capabilities
	err     error
}

var (
	_ appcache.Cache     = (*capsStore)(nil)
	_ appcache.CapsCache = (*capsStore)(nil)
)

func newCapsStore() *capsStore {
	return &capsStore{held: map[string]appcache.CapsSnapshot{}, written: map[string]jira.Capabilities{}}
}

func (c *capsStore) Caps(project string) (appcache.CapsSnapshot, bool) {
	snap, ok := c.held[project]
	return snap, ok
}

func (c *capsStore) PutCaps(project string, caps jira.Capabilities) error {
	if c.err != nil {
		return c.err
	}
	c.written[project] = caps
	return nil
}

func (c *capsStore) Rows(string) (appcache.Snapshot, bool)                   { return appcache.Snapshot{}, false }
func (c *capsStore) PutRows(string, []jira.Issue, bool) error                { return nil }
func (c *capsStore) Forget(string) error                                     { return nil }
func (c *capsStore) EachIssue(func(jira.Issue, time.Time) bool) (int, error) { return 0, nil }
func (c *capsStore) Generation() uint64                                      { return 0 }

func TestCaps_ProbeAnswersForTheProjectAsked(t *testing.T) {
	t.Parallel()

	f := jiratest.New(jiratest.WithProject("PROJ", jiratest.Scrum))
	caps, err := NewCaps(f, nil).Probe(t.Context(), "PROJ")
	if err != nil {
		t.Fatalf("probing: %v", err)
	}
	if !caps.Boards.OK {
		t.Errorf("boards are %+v for a scrum project, want allowed", caps.Boards)
	}
	if got := f.Calls(); len(got) != 1 || got[0] != "Capabilities" {
		t.Errorf("calls are %v, want one Capabilities", got)
	}
}

func TestCaps_ProbePassesTheSitesRefusalThroughAsItsOwnType(t *testing.T) {
	t.Parallel()

	for _, fail := range siteFailures() {
		f := jiratest.New(jiratest.WithProject("PROJ", jiratest.Scrum))
		f.FailNext(fail)
		if _, err := NewCaps(f, nil).Probe(t.Context(), "PROJ"); !errors.Is(err, fail) {
			t.Errorf("probing failed with %v, want the site's own %T", err, fail)
		}
	}
}

func TestCaps_WithoutAClientThereIsNothingToAsk(t *testing.T) {
	t.Parallel()

	var client jira.SessionClient
	probe := NewCaps(client, nil)
	if probe.CanProbe() {
		t.Error("a nil client reports it can probe")
	}
	if _, err := probe.Probe(t.Context(), "PROJ"); !errors.Is(err, ErrNoClient) {
		t.Errorf("probing without a client failed with %v, want ErrNoClient", err)
	}
}

func TestCaps_KeepsAndRestoresThroughACapsCache(t *testing.T) {
	t.Parallel()

	store := newCapsStore()
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	store.held["PROJ"] = appcache.CapsSnapshot{Caps: jira.Capabilities{Boards: jira.Capability{OK: true}}, StoredAt: at}
	caps := NewCaps(nil, store)

	snap, ok := caps.Stored("PROJ")
	if !ok || !snap.Caps.Boards.OK || !snap.StoredAt.Equal(at) {
		t.Errorf("stored answer is %+v, %v; want the one held", snap, ok)
	}
	if _, ok := caps.Stored("OTHER"); ok {
		t.Error("a project never probed has a stored answer")
	}
	if !caps.Keeps() {
		t.Fatal("a CapsCache reports nowhere to keep an answer")
	}
	want := jira.Capabilities{Plans: jira.Capability{Reason: "not here"}}
	if err := caps.Keep("PROJ", want); err != nil {
		t.Fatalf("keeping: %v", err)
	}
	if got := store.written["PROJ"]; got.Plans.Reason != "not here" {
		t.Errorf("kept %+v, want the answer handed in", got)
	}

	store.err = errors.New("disk full")
	if err := caps.Keep("PROJ", want); !errors.Is(err, store.err) {
		t.Errorf("keeping onto a failing store returned %v, want its error", err)
	}
}

func TestCaps_ACacheThatKeepsNoAnswerIsNowhereToKeepOne(t *testing.T) {
	t.Parallel()

	for name, cache := range map[string]appcache.Cache{"nil": nil, "rows only": rowsCache{}} {
		caps := NewCaps(nil, cache)
		if caps.Keeps() {
			t.Errorf("%s: reports somewhere to keep an answer", name)
		}
		if _, ok := caps.Stored("PROJ"); ok {
			t.Errorf("%s: has a stored answer", name)
		}
		if err := caps.Keep("PROJ", jira.Capabilities{}); err != nil {
			t.Errorf("%s: keeping with nowhere to keep failed: %v", name, err)
		}
	}
}

type rowsCache struct{}

func (rowsCache) Rows(string) (appcache.Snapshot, bool)                   { return appcache.Snapshot{}, false }
func (rowsCache) PutRows(string, []jira.Issue, bool) error                { return nil }
func (rowsCache) Forget(string) error                                     { return nil }
func (rowsCache) EachIssue(func(jira.Issue, time.Time) bool) (int, error) { return 0, nil }
func (rowsCache) Generation() uint64                                      { return 0 }

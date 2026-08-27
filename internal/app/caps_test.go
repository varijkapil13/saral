package app

import (
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
)

func probeAnswer(t *testing.T) jira.Capabilities {
	t.Helper()

	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skipf("this machine has no zoneinfo entry for Europe/Berlin: %v", err)
	}
	return jira.Capabilities{
		Plans:        jira.Capability{Reason: "This site has no Plans API, which arrives with a Jira Premium subscription"},
		BulkMove:     jira.Capability{OK: true},
		Boards:       jira.Capability{OK: true},
		Attachments:  jira.Capability{OK: true},
		DeleteIssues: jira.Capability{Reason: "You need the Delete Issues permission to delete issues"},
		People:       jira.Capability{OK: true},
		Graphics:     jira.GraphicsKitty,
		TimeZone:     berlin,
	}
}

func TestCaps_RoundTripsWhatTheProbeSaid(t *testing.T) {
	t.Parallel()

	cache, _ := newTestCache(t)
	want := probeAnswer(t)
	if err := cache.PutCaps("PROJ", want); err != nil {
		t.Fatalf("PutCaps: %v", err)
	}

	got, ok := cache.Caps("PROJ")
	if !ok {
		t.Fatal("what was just stored cannot be read back")
	}
	for _, key := range []jira.CapabilityKey{
		jira.CapPlans, jira.CapBulkMove, jira.CapBoards,
		jira.CapAttachments, jira.CapDeleteIssues, jira.CapPeople,
	} {
		if got.Caps.Capability(key) != want.Capability(key) {
			t.Errorf("%s came back %+v, want %+v", key, got.Caps.Capability(key), want.Capability(key))
		}
	}
	if got.Caps.Location().String() != "Europe/Berlin" {
		t.Errorf("dates would render in %s, want the account's own zone", got.Caps.Location())
	}
	if got.Caps.TimeZoneReason != "" {
		t.Errorf("the zone came back with %q attached, which is for a zone that is not the account's", got.Caps.TimeZoneReason)
	}
	if !got.StoredAt.Equal(testNow) {
		t.Errorf("stored at %v, want the clock's %v", got.StoredAt, testNow)
	}
}

// TestCaps_TheTerminalsGraphicsAnswerIsNotStored covers the one field that is
// about this machine rather than the site. A stored kitty answer restored into a
// terminal that cannot speak it prints escape bytes over the frame, and the
// detection is local and free, so it waits for the probe.
func TestCaps_TheTerminalsGraphicsAnswerIsNotStored(t *testing.T) {
	t.Parallel()

	cache, _ := newTestCache(t)
	if err := cache.PutCaps("PROJ", probeAnswer(t)); err != nil {
		t.Fatalf("PutCaps: %v", err)
	}
	got, ok := cache.Caps("PROJ")
	if !ok {
		t.Fatal("what was just stored cannot be read back")
	}
	if got.Caps.Graphics != jira.GraphicsNone {
		t.Errorf("the stored answer claims %s, want no image protocol until this terminal has been asked", got.Caps.Graphics)
	}
}

// TestCaps_AZoneThisMachineDoesNotKnowSaysSo holds the invariant the whole of
// date rendering leans on: TimeZoneReason is empty exactly when TimeZone is the
// account's own. A cache file written on a machine with zoneinfo and read on one
// without would otherwise render every date in UTC with nothing saying why.
func TestCaps_AZoneThisMachineDoesNotKnowSaysSo(t *testing.T) {
	t.Parallel()

	cache, _ := newTestCache(t)
	if err := cache.PutCaps("PROJ", jira.Capabilities{TimeZone: time.FixedZone("Mars/Olympus", 3600)}); err != nil {
		t.Fatalf("PutCaps: %v", err)
	}

	got, ok := cache.Caps("PROJ")
	if !ok {
		t.Fatal("what was just stored cannot be read back")
	}
	if got.Caps.TimeZone != nil {
		t.Fatalf("a zone this machine has no entry for came back as %v", got.Caps.TimeZone)
	}
	if got.Caps.TimeZoneReason == "" {
		t.Fatal("dates will render in UTC and nothing on screen can say why")
	}
	zone, why := got.Caps.Zone()
	if zone != time.UTC || why == "" {
		t.Errorf("Zone() answered %v, %q", zone, why)
	}
}

// TestCaps_APastTTLAnswerIsStillServedAndBadged is the shape every kind in this
// file has: an old answer beats no answer, and the badge is what says which it
// is.
func TestCaps_APastTTLAnswerIsStillServedAndBadged(t *testing.T) {
	t.Parallel()

	cache, at := newTestCache(t)
	if err := cache.PutCaps("PROJ", probeAnswer(t)); err != nil {
		t.Fatalf("PutCaps: %v", err)
	}

	at.at = testNow.Add(KindCaps.TTL() - time.Second)
	switch got, ok := cache.Caps("PROJ"); {
	case !ok:
		t.Fatal("an answer inside its TTL is not served")
	case got.Stale:
		t.Error("an answer inside its TTL is badged stale")
	}

	at.at = testNow.Add(KindCaps.TTL() + time.Second)
	switch got, ok := cache.Caps("PROJ"); {
	case !ok:
		t.Fatal("an answer past its TTL is dropped rather than badged")
	case !got.Stale:
		t.Error("an answer past its TTL is not badged")
	case !got.Caps.Allows(jira.CapBoards):
		t.Error("the answer itself did not survive the badge")
	}
}

// TestCaps_TwoProjectsAndTheWholeSiteAreThreeAnswers covers the key an entry is
// stored under. Boards, Move and Delete are per-project, and probing with no
// project answers all three as unavailable-because-nothing-was-named — a real
// answer about the site and a wrong one about any project in it.
func TestCaps_TwoProjectsAndTheWholeSiteAreThreeAnswers(t *testing.T) {
	t.Parallel()

	cache, _ := newTestCache(t)
	answers := map[string]jira.Capabilities{
		"ONE": {Boards: jira.Capability{OK: true}},
		"TWO": {Boards: jira.Capability{Reason: "TWO has no board"}},
		"":    {Boards: jira.Capability{Reason: "No project is selected, and a board belongs to a project"}},
	}
	for project, caps := range answers {
		if err := cache.PutCaps(project, caps); err != nil {
			t.Fatalf("PutCaps(%q): %v", project, err)
		}
	}
	for project, want := range answers {
		got, ok := cache.Caps(project)
		if !ok {
			t.Fatalf("the answer for %q is not there", project)
		}
		if got.Caps.Boards != want.Boards {
			t.Errorf("the answer for %q is %+v, want %+v", project, got.Caps.Boards, want.Boards)
		}
	}
}

func TestCaps_AProjectNeverProbedHasNoAnswer(t *testing.T) {
	t.Parallel()

	cache, _ := newTestCache(t)
	if err := cache.PutCaps("ONE", probeAnswer(t)); err != nil {
		t.Fatalf("PutCaps: %v", err)
	}
	if _, ok := cache.Caps("TWO"); ok {
		t.Error("a project that was never probed answered with another project's")
	}
}

// TestCaps_ANilCacheAnswersNothingRatherThanCrashing is the first run, an
// unwritable home, and another copy of Saral holding the file.
func TestCaps_ANilCacheAnswersNothingRatherThanCrashing(t *testing.T) {
	t.Parallel()

	var cache *DiskCache
	if _, ok := cache.Caps("PROJ"); ok {
		t.Error("a cache that is not there answered")
	}
	if err := cache.PutCaps("PROJ", probeAnswer(t)); err != nil {
		t.Errorf("PutCaps on a nil cache: %v", err)
	}
}

// TestCaps_AWriteMovesTheGeneration keeps the derived-copy contract: anything
// holding something built by walking the cache is told that the file moved.
func TestCaps_AWriteMovesTheGeneration(t *testing.T) {
	t.Parallel()

	cache, _ := newTestCache(t)
	before := cache.Generation()
	if err := cache.PutCaps("PROJ", probeAnswer(t)); err != nil {
		t.Fatalf("PutCaps: %v", err)
	}
	if cache.Generation() == before {
		t.Error("storing a probe answer did not move the generation")
	}
}

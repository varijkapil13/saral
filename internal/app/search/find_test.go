package search

import (
	"errors"
	"testing"
)

const findHere = "example.atlassian.net"

func findIndex(corpus *stubCorpus) *Index { return NewIndex(corpus) }

func TestFind_AKeyIsOfferedAsAJumpWhetherOrNotItIsCached(t *testing.T) {
	t.Parallel()

	got, err := Find(findIndex(newStubCorpus()), "PROJ-999", 20, findHere, true)
	if err != nil {
		t.Fatal(err)
	}
	if got.Jump != "PROJ-999" || got.Foreign != nil || len(got.Hits) != 0 {
		t.Fatalf("Find = %+v, want a jump to PROJ-999 alone", got)
	}
}

func TestFind_AURLForThisSiteIsOfferedAsAJump(t *testing.T) {
	t.Parallel()

	got, err := Find(findIndex(newStubCorpus()), "https://example.atlassian.net/browse/PROJ-77", 20, findHere, true)
	if err != nil {
		t.Fatal(err)
	}
	if got.Jump != "PROJ-77" || got.Foreign != nil {
		t.Fatalf("Find = %+v, want a jump to PROJ-77", got)
	}
}

func TestFind_AURLForAnotherSiteIsNamedAndNotOpened(t *testing.T) {
	t.Parallel()

	got, err := Find(findIndex(newStubCorpus()), "https://other.atlassian.net/browse/PROJ-77", 20, findHere, true)
	if err != nil {
		t.Fatal(err)
	}
	want := ForeignSite{Key: "PROJ-77", Host: "other.atlassian.net", Here: findHere}
	if got.Jump != "" || got.Foreign == nil || *got.Foreign != want {
		t.Fatalf("Find = %+v, want the mismatch %+v and no jump", got, want)
	}
}

func TestFind_AURLIsReadAgainstNoSiteWhenThisOneCouldNotBeNormalised(t *testing.T) {
	t.Parallel()

	got, err := Find(findIndex(newStubCorpus()), "https://other.atlassian.net/browse/PROJ-77", 20, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if got.Jump != "PROJ-77" || got.Foreign != nil {
		t.Fatalf("Find = %+v, want the jump with the site check skipped", got)
	}
}

func TestFind_AKeyTheCacheAlreadyRanksIsNotOfferedASecondTime(t *testing.T) {
	t.Parallel()

	ix := findIndex(newStubCorpus(titledIssue("PROJ-142", "Login page")))
	got, err := Find(ix, "proj-142", 20, findHere, true)
	if err != nil {
		t.Fatal(err)
	}
	if got.Jump != "" || len(got.Hits) != 1 || got.Hits[0].Key != "PROJ-142" {
		t.Fatalf("Find = %+v, want the cache hit alone", got)
	}
}

func TestFind_OrdinaryTextIsNotAJumpTarget(t *testing.T) {
	t.Parallel()

	got, err := Find(findIndex(newStubCorpus(titledIssue("PROJ-1", "login"))), "login", 20, findHere, true)
	if err != nil {
		t.Fatal(err)
	}
	if got.Jump != "" || got.Foreign != nil || len(got.Hits) != 1 {
		t.Fatalf("Find = %+v, want one hit and no jump", got)
	}
}

func TestFind_AnIndexErrorPassesThroughBesideTheJump(t *testing.T) {
	t.Parallel()

	boom := errors.New("walk failed")
	corpus := newStubCorpus()
	corpus.fail = boom
	got, err := Find(findIndex(corpus), "PROJ-5", 20, findHere, true)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want %v", err, boom)
	}
	if got.Jump != "PROJ-5" {
		t.Fatalf("Find = %+v, want the jump kept alongside the error", got)
	}
}

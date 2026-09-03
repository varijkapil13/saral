package palette

import (
	"strings"
	"testing"
)

// The whole reason a key needs a route besides the cache index: an issue
// nothing has been cached on this machine yet still has to open, with the
// fetch the CLI argument already does for it.
func TestPalette_TypingAnUncachedKeyOffersToOpenItWithAFetch(t *testing.T) {
	t.Parallel()

	d := paletteDeps()
	p := fly(t, d, sample(), memoryTable(), 120, 24)
	p.typeText("PROJ-999")

	if got := p.keys(); len(got) != 1 || got[0] != "PROJ-999" {
		t.Fatalf("typing a key nothing has cached offers %v, want [PROJ-999]", got)
	}
	p.press("enter")
	if got := p.pushed(); len(got) != 1 || got[0] != "issue:PROJ-999" {
		t.Fatalf("enter over an uncached key pushed %v, want the detail pane for PROJ-999", got)
	}
}

// A key already ranked by the index is not offered a second time under a row
// that would look like a different issue.
func TestPalette_TypingAKeyAlreadyCachedDoesNotDuplicateTheRow(t *testing.T) {
	t.Parallel()

	d, _ := cachedDeps()
	p := fly(t, d, sample(), memoryTable(), 120, 24)
	p.typeText("PROJ-142")

	if got := p.keys(); len(got) != 1 || got[0] != "PROJ-142" {
		t.Fatalf("a cached key typed exactly offers %v, want it once", got)
	}
}

// A pasted browse URL for this profile's own site reaches the issue it names,
// the way it reaches the CLI argument's parser.
func TestPalette_PastingABrowseURLOffersTheIssueItNames(t *testing.T) {
	t.Parallel()

	d := paletteDeps()
	p := fly(t, d, sample(), memoryTable(), 120, 24)
	p.typeText("https://example.atlassian.net/browse/PROJ-77")

	if got := p.keys(); len(got) != 1 || got[0] != "PROJ-77" {
		t.Fatalf("pasting a browse URL for this site offers %v, want [PROJ-77]", got)
	}
	p.press("enter")
	if got := p.pushed(); len(got) != 1 || got[0] != "issue:PROJ-77" {
		t.Fatalf("enter over a pasted URL pushed %v, want the detail pane for PROJ-77", got)
	}
}

// A board or backlog URL carries the key in ?selectedIssue= rather than the
// path, which is the shape /browse/ does not have.
func TestPalette_PastingABoardURLReadsSelectedIssueFromTheQuery(t *testing.T) {
	t.Parallel()

	d := paletteDeps()
	p := fly(t, d, sample(), memoryTable(), 120, 24)
	p.typeText("https://example.atlassian.net/jira/software/projects/PROJ/boards/3?selectedIssue=PROJ-77")

	if got := p.keys(); len(got) != 1 || got[0] != "PROJ-77" {
		t.Fatalf("pasting a board URL offers %v, want [PROJ-77]", got)
	}
}

// A URL for another site is named as the mistake it is, in the same words the
// CLI argument answers with, rather than read against this profile's site.
func TestPalette_PastingAURLForAnotherSiteNamesTheMismatchRatherThanOpeningIt(t *testing.T) {
	t.Parallel()

	d := paletteDeps()
	p := fly(t, d, sample(), memoryTable(), 120, 24)
	p.typeText("https://other.atlassian.net/browse/PROJ-77")

	if got := p.keys(); len(got) != 0 {
		t.Fatalf("a URL for another site offered %v, want nothing opened against this one", got)
	}
	// Every stroke of the paste re-evaluates, and a prefix like .../PROJ-7 is
	// already a valid key shape on the wrong site — so more than one warning is
	// expected here. It is the last one, over the whole pasted key, that has to
	// name both sites.
	got := p.statuses()
	if len(got) == 0 {
		t.Fatal("pasting a URL for another site said nothing")
	}
	last := got[len(got)-1]
	if !strings.Contains(last, "PROJ-77") || !strings.Contains(last, "other.atlassian.net") || !strings.Contains(last, "example.atlassian.net") {
		t.Fatalf("the last warning read %q, want it to name the key and both sites", last)
	}
}

// Text that is neither a key nor a URL is ordinary filter text: it must not be
// offered as something to jump to.
func TestPalette_OrdinaryTextIsNotOfferedAsAJumpTarget(t *testing.T) {
	t.Parallel()

	d, _ := cachedDeps()
	p := fly(t, d, sample(), memoryTable(), 120, 24)
	p.typeText("login")

	for _, key := range p.keys() {
		if key == "login" {
			t.Fatalf("plain filter text was offered as a key to jump to: %v", p.keys())
		}
	}
}

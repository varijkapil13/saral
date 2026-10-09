package list

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	appsearch "github.com/varijkapil13/saral/internal/app/search"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

// why is what a fetch was for. One reload serves a refresh, a revalidation of
// rows off disk, a poll and the page walked on from stored rows, so what lands
// is reported as the thing somebody asked for rather than as the mechanism that
// happened to carry it.
type why uint8

const (
	// whyOpen is the first read of a search, whose rows are their own report.
	whyOpen why = iota
	whyRefresh
	whyPurge
	// whyPage is the next page, which the count already accounts for.
	whyPage
	// whyBackground is a poll or a revalidation: nobody pressed anything, so
	// nothing is said unless it fails.
	whyBackground
)

// words are the two things a fetch is called in a status line: what it did when
// it worked, and what did not work when it did not. Both are empty for a fetch
// nobody asked for, which is how those stay silent.
func (w why) words() (did, failed string) {
	switch w {
	case whyRefresh:
		return "refreshed", "the refresh failed"
	case whyPurge:
		return "refetched from scratch", "the refetch failed"
	case whyOpen, whyPage, whyBackground:
		return "", ""
	}
	return "", ""
}

// changeText names what moved, leaving out whichever of the three did not.
func changeText(c appsearch.RowChange) string {
	parts := make([]string, 0, 3)
	for _, part := range [...]struct {
		n    int
		word string
	}{{c.Added, "new"}, {c.Updated, "changed"}, {c.Gone, "gone"}} {
		if part.n > 0 {
			parts = append(parts, strconv.Itoa(part.n)+" "+part.word)
		}
	}
	return strings.Join(parts, ", ")
}

// refreshed is the status line a landed refresh writes. "Nothing has changed" is
// said out loud, because that is the outcome a silent refresh cannot be told
// apart from.
func refreshed(w why, before, after []jira.Issue) tea.Cmd {
	did, _ := w.words()
	if did == "" {
		return nil
	}
	switch c := appsearch.DiffRows(before, after); {
	case len(after) == 0:
		return kernel.Status(did + ": still nothing matches this search")
	case len(before) == 0:
		return kernel.Status(did + ": " + issueCount(len(after)))
	case !c.Any():
		return kernel.Status(did + ": nothing has changed, still " + issueCount(len(after)))
	default:
		return kernel.Status(did + ": " + changeText(c) + ", now " + issueCount(len(after)))
	}
}

// failure says what did not work in the words the error itself carries. A
// refresh names itself first, so that a refusal is one of the three answers r
// can give rather than an error that might have come from anywhere.
func failure(w why, err error) tea.Cmd {
	_, failed := w.words()
	if failed == "" || err == nil {
		return kernel.Fail(err)
	}
	text, _ := jira.Reason(err)
	return func() tea.Msg {
		return kernel.StatusMsg{Text: failed + ": " + text, Level: kernel.LevelError}
	}
}

func issueCount(n int) string {
	if n == 1 {
		return "1 issue"
	}
	return strconv.Itoa(n) + " issues"
}

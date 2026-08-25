package palette

import (
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// hitLimit is how many cached issues one filter offers. It is a screenful at the
// smallest terminal Saral draws in, so the ranking is bounded whatever the cache
// holds and the answer to more matches than this is a longer filter rather than
// a longer scroll.
const hitLimit = 20

// noTitle is what a row says where a title was never asked for. PC.1's field
// mask makes that a different answer from an issue whose title is empty, and
// drawing both as a blank would lose the difference.
const noTitle = "(no title stored)"

// hit is one cached issue as the palette holds it, prepared once per keystroke:
// what the row says, and how old the copy on disk is.
type hit struct {
	key string
	// summary is the title as stored, and "" when nothing asked for it. It seeds
	// the detail pane so that opening one paints before the site answers.
	summary string
	text    string
	age     string
	stale   bool
}

// search ranks the issues already on disk against what has been typed. The empty
// filter is the palette as it opens, and answering that with the whole cache
// would bury the commands under five thousand rows nobody asked for.
//
// The index refreshes itself against the cache's generation, so a keystroke over
// an unchanged cache re-ranks what it already walked and touches no disk and no
// site.
func (m *Model) search(text string, now time.Time) tea.Cmd {
	m.hits = m.hits[:0]
	if text == "" {
		return nil
	}
	found, err := m.index.Search(text, hitLimit)
	for i := range found {
		m.hits = append(m.hits, newHit(found[i], now))
	}
	for i := range m.hits {
		m.shown = append(m.shown, entry{issue: true, at: i})
	}
	if err != nil {
		return kernel.Warn("some cached issues could not be read: " + err.Error())
	}
	return nil
}

func newHit(h app.Hit, now time.Time) hit {
	out := hit{key: h.Key, text: h.Key}
	switch {
	case !h.HasSummary:
		out.text += "  " + noTitle
	case h.Summary != "":
		out.summary, out.text = h.Summary, h.Key+"  "+h.Summary
	}
	if !h.StoredAt.IsZero() {
		age := now.Sub(h.StoredAt)
		out.age, out.stale = ageLabel(age), age > app.KindIssue.TTL()
	}
	return out
}

// ageLabel is how old the copy on disk is, in the largest unit that still says
// something. A clock that moved backwards reads as just now rather than as a
// negative age.
func ageLabel(age time.Duration) string {
	switch {
	case age < time.Minute:
		return "just now"
	case age < time.Hour:
		return strconv.Itoa(int(age.Minutes())) + "m old"
	case age < 24*time.Hour:
		return strconv.Itoa(int(age.Hours())) + "h old"
	default:
		return strconv.Itoa(int(age.Hours()/24)) + "d old"
	}
}

// noIssues is why the cache half of the palette answered nothing, and "" when it
// simply had no match. A session with nowhere to cache is normal — a first run,
// another copy of Saral holding the file, an unwritable home — and saying so
// beats a palette that looks half built.
func (m *Model) noIssues() string {
	switch {
	case m.deps.Cache == nil:
		return "This session has nowhere to cache issues, so only commands are searched."
	case m.index.Len() == 0:
		return "No issue has been cached on this machine yet."
	default:
		return ""
	}
}

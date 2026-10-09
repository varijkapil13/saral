package palette

import (
	"fmt"
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"

	appcache "github.com/varijkapil13/saral/internal/app/cache"
	appsearch "github.com/varijkapil13/saral/internal/app/search"
	"github.com/varijkapil13/saral/internal/config"
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
	here, herr := config.NormalizeSite(m.deps.Site)
	found, err := appsearch.Find(m.index, text, hitLimit, here, herr == nil)
	for i := range found.Hits {
		m.hits = append(m.hits, newHit(found.Hits[i], now))
	}
	if found.Jump != "" {
		m.hits = append([]hit{jumpHit(found.Jump)}, m.hits...)
	}
	for i := range m.hits {
		m.shown = append(m.shown, entry{issue: true, at: i})
	}
	var cmds []tea.Cmd
	if f := found.Foreign; f != nil {
		cmds = append(cmds, kernel.Warn(fmt.Sprintf("%s is on %s and this profile is on %s, so it was not opened", f.Key, f.Host, f.Here)))
	}
	if err != nil {
		cmds = append(cmds, kernel.Warn("the cache on this machine could not be walked: "+err.Error()))
	}
	if n := m.index.Dropped(); n > 0 && n != m.said {
		m.said = n
		cmds = append(cmds, kernel.Warn(dropped(n)))
	}
	return tea.Batch(cmds...)
}

// dropped says how much of the cache went unread, because a palette that offers
// fewer issues than the machine holds looks exactly like a project that holds
// fewer. The records are gone rather than skipped over, so the sentence promises
// what actually happens next.
func dropped(n int) string {
	if n == 1 {
		return "1 cached issue could not be read and has been dropped; the next fetch of it rewrites the record"
	}
	return strconv.Itoa(n) + " cached issues could not be read and have been dropped; the next fetch of them rewrites the records"
}

func newHit(h appsearch.Hit, now time.Time) hit {
	out := hit{key: h.Key, text: h.Key}
	switch {
	case !h.HasSummary:
		out.text += "  " + noTitle
	case h.Summary != "":
		out.summary, out.text = h.Summary, h.Key+"  "+h.Summary
	}
	if !h.StoredAt.IsZero() {
		age := now.Sub(h.StoredAt)
		out.age, out.stale = ageLabel(age), age > appcache.KindIssue.TTL()
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
	case m.index.Len() == 0 && m.index.Dropped() > 0:
		return "No issue cached on this machine could be read; every record has been dropped."
	case m.index.Len() == 0:
		return "No issue has been cached on this machine yet."
	default:
		return ""
	}
}

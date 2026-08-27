package app

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
)

// summaryFieldID is the platform's own ID for an issue's title, which is the
// same on every site — the IDs that differ per site are the custom fields.
const summaryFieldID = "summary"

// IssueCorpus is the part of a Cache an index is built from: the issues it
// holds, and a number that moves when they change.
//
// It is narrower than Cache because an index never reads a search's rows and
// never writes anything, so a test for one needs a dozen lines rather than a
// whole cache, and a later corpus that is not the disk cache can be indexed
// without widening anything.
type IssueCorpus interface {
	// EachIssue visits every issue held, in key order, stopping early when fn
	// returns false, and reports how many records it had to skip because they
	// could not be read.
	EachIssue(fn func(iss jira.Issue, storedAt time.Time) bool) (dropped int, err error)
	// Generation counts the changes made to what is held. It only ever
	// increases, and it moves for any write.
	Generation() uint64
}

// A Cache is a corpus: the index a session searches is built from the cache
// that session already holds, and nothing else has to be passed around for it.
var _ IssueCorpus = Cache(nil)

// Hit is one issue a pattern matched, and how well.
type Hit struct {
	// Key is the issue key, which is what a caller opens the issue with.
	Key string
	// Summary is the issue's title. HasSummary is whether that is an answer: a
	// narrow read stores an issue whose title nothing asked for, and an empty
	// string there is different from an issue with an empty title.
	Summary    string
	HasSummary bool
	// StoredAt is when the copy this answer came from was written. Every answer
	// an index gives is from disk, so a caller that badges stale data compares
	// this against its own clock rather than against time.Now.
	StoredAt time.Time
	// Score is what the pattern scored against this issue, for a caller that
	// wants to blend it with a ranking of its own. Hits already arrive ordered.
	Score int
}

// Index answers what somebody typed from the issues a cache already holds, with
// no round trip. It keeps a key and a title per issue rather than the issues
// themselves, so a corpus at the cache's bound costs a few hundred kilobytes
// and no decoded JSON is held.
//
// The zero Index and one built over a nil corpus are both a search that finds
// nothing, which is what a first run, a locked cache file and an unwritable
// home all look like.
//
// An Index belongs to whatever holds it and is not safe for concurrent use: it
// is read and rebuilt on the event loop, like every other read a first paint
// depends on.
type Index struct {
	corpus  IssueCorpus
	rows    []indexRow
	hits    []Hit
	built   uint64
	dropped int
	walked  bool
}

type indexRow struct {
	key        string
	summary    string
	hasSummary bool
	storedAt   time.Time
}

// NewIndex returns an index over a corpus, without reading it. A nil corpus is
// a session with nowhere to cache, and searching it finds nothing.
func NewIndex(corpus IssueCorpus) *Index { return &Index{corpus: corpus} }

// Len reports how many issues the index holds as of its last walk.
func (ix *Index) Len() int {
	if ix == nil {
		return 0
	}
	return len(ix.rows)
}

// Refresh walks the corpus when it has moved since the last walk and does
// nothing when it has not, reporting whether it walked. Comparing one number is
// what keeps a keystroke from re-reading ten thousand issues; the corpus counts
// every write, so this rebuilds more often than it strictly must and can never
// answer from a copy that is behind.
//
// A walk is a rebuild rather than a delta because a generation says that
// something changed, not what: the cache evicts and merges under its own bound,
// and no per-issue difference is recoverable from a counter.
//
// A record the corpus cannot read is skipped rather than ending the walk, and
// the count of them is kept for Dropped. A walk that fails part way keeps the
// rows it did read and is not walked again until the corpus moves, so a corpus
// that cannot be read costs one report rather than one per keystroke.
func (ix *Index) Refresh() (bool, error) {
	if ix == nil || ix.corpus == nil {
		return false, nil
	}
	gen := ix.corpus.Generation()
	if ix.walked && gen == ix.built {
		return false, nil
	}
	ix.rows = ix.rows[:0]
	dropped, err := ix.corpus.EachIssue(func(iss jira.Issue, storedAt time.Time) bool {
		if iss.Key == "" {
			return true
		}
		row := indexRow{key: iss.Key, storedAt: storedAt}
		if iss.Summary != "" || iss.Requested.Has(summaryFieldID) {
			row.summary, row.hasSummary = iss.Summary, true
		}
		ix.rows = append(ix.rows, row)
		return true
	})
	ix.dropped = dropped
	ix.built, ix.walked = gen, true
	return true, err
}

// Dropped is how many records the last walk could not read and skipped. It is
// what lets a caller tell a cache holding less than it looks from a project that
// genuinely holds little — the two are the same short list otherwise.
func (ix *Index) Dropped() int {
	if ix == nil {
		return 0
	}
	return ix.dropped
}

// Search ranks the issues held against what somebody typed, best first, and
// returns at most limit of them. A limit of zero or less is every issue that
// matched, which costs a sort over the whole corpus; a caller drawing a screen
// asks for the screenful it can show.
//
// An issue is ranked by whichever of its key and its title matches better. An
// issue whose title nothing asked for is matched on its key alone.
//
// It refreshes first, so a caller cannot answer from a corpus that has moved
// underneath it. The error is that refresh's, and it is the walk failing rather
// than one record being unreadable: a record that cannot be decoded is skipped
// and counted, which is what Dropped reports.
//
// The slice returned is the caller's own, so a view may keep it in its model
// across frames.
func (ix *Index) Search(text string, limit int) ([]Hit, error) {
	if ix == nil {
		return nil, nil
	}
	_, err := ix.Refresh()

	pattern := NewPattern(text)
	ix.hits = ix.hits[:0]
	for i := range ix.rows {
		row := &ix.rows[i]
		score, ok := pattern.Score(row.key)
		if row.hasSummary {
			if other, matched := pattern.Score(row.summary); matched && (!ok || other > score) {
				score, ok = other, true
			}
		}
		if !ok {
			continue
		}
		ix.keep(Hit{
			Key: row.key, Summary: row.summary, HasSummary: row.hasSummary,
			StoredAt: row.storedAt, Score: score,
		}, limit)
	}
	if limit <= 0 {
		slices.SortFunc(ix.hits, byRank)
	}
	if len(ix.hits) == 0 {
		return nil, err
	}
	return slices.Clone(ix.hits), err
}

// keep inserts a hit into the ranked run built so far. Bounded, that is a
// compare against the worst hit held for all but the few that beat it, which is
// what a screenful out of ten thousand costs instead of a whole sort.
func (ix *Index) keep(h Hit, limit int) {
	if limit <= 0 {
		ix.hits = append(ix.hits, h)
		return
	}
	if len(ix.hits) == limit {
		if byRank(h, ix.hits[limit-1]) >= 0 {
			return
		}
		ix.hits = ix.hits[:limit-1]
	}
	at := len(ix.hits)
	for at > 0 && byRank(h, ix.hits[at-1]) < 0 {
		at--
	}
	ix.hits = append(ix.hits, Hit{})
	copy(ix.hits[at+1:], ix.hits[at:])
	ix.hits[at] = h
}

// byRank orders hits best first: by score, and by key where two issues scored
// the same, so that one corpus and one pattern always come back in one order.
func byRank(a, b Hit) int {
	if a.Score != b.Score {
		return cmp.Compare(b.Score, a.Score)
	}
	return strings.Compare(a.Key, b.Key)
}

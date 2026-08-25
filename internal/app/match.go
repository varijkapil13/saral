package app

import (
	"unicode"
	"unicode/utf8"
)

// Every bonus and penalty below is a whole multiple of scoreUnit, and the only
// term smaller than one unit is the tail: how far into the candidate the match
// starts, and how long the candidate is. So the tiers decide the order and the
// tail only ever breaks a tie inside one.
const (
	scoreUnit = 256

	// scorePrefix is for the pattern spelt out from the candidate's first rune,
	// and scoreWhole for one that is the entire candidate. Together they are
	// what puts a prefix above a word start.
	scorePrefix = 8 * scoreUnit
	scoreWhole  = 8 * scoreUnit
	// scoreWordStart is for a matched rune that begins a word, which is what
	// puts a word start above a match landing in the middle of one.
	scoreWordStart = 4 * scoreUnit
	// scoreAdjacent is for a matched rune that follows the one before it, and
	// scoreGap against a run of runes skipped between two of them: contiguous
	// beats scattered. A gap costs more than a word start pays, so that a
	// pattern spelt out of the initials of three words stays below the same
	// pattern found whole inside one of them.
	scoreAdjacent = 3 * scoreUnit
	scoreGap      = 2 * scoreUnit
)

// The tail is the part of a score under one unit, so it can only order two
// candidates the tiers above have already tied. An earlier match wins first,
// and a shorter candidate breaks what is left: five bits of rune count against
// three of how far in the match began.
const (
	tailLeadStep = 32
	tailMaxLead  = 7
	tailMaxRunes = 31
)

// A pattern's first rune can begin many words in one candidate, and each one is
// a separate attempt at the best match. Eight is where a ranking difference
// stops being one anybody can see, and the alternative is a scan that grows
// with the square of the title.
const maxStarts = 8

// Pattern is what somebody typed, prepared once for a pass over many
// candidates. Preparing one holds no slice and copies no string, so a keystroke
// folds the pattern once rather than once per row.
//
// It matches a subsequence: every rune of the pattern has to appear in the
// candidate in that order, though not next to each other. Case is ignored on
// both sides, and neither side is copied to do it.
//
// The candidate is any string. An issue key, an issue title and a command's
// name in the palette are all scored by the same code, which is why there is no
// fuzzy-matching dependency in this module.
type Pattern struct {
	text  string
	first rune
	runes int
	ascii bool
}

// NewPattern prepares what somebody typed. It allocates nothing, so a caller
// ranking ten thousand rows against one keystroke prepares it outside the loop
// and pays for it once.
//
// The text is taken as typed. Trailing space is part of the pattern, because a
// space is a rune a title can be matched on like any other.
func NewPattern(text string) Pattern {
	p := Pattern{text: text, ascii: true}
	for _, r := range text {
		if p.runes == 0 {
			p.first = fold(r)
		}
		if r >= utf8.RuneSelf {
			p.ascii = false
		}
		p.runes++
	}
	return p
}

// Empty reports whether the pattern is the one that matches everything.
func (p Pattern) Empty() bool { return p.runes == 0 }

// Score reports how well the pattern matches a candidate. A higher score is a
// better match; the bool is the only answer about whether it matched at all,
// since a poor match can score below zero.
//
// The empty pattern matches every candidate with a score of nothing, so a
// caller filtering on it keeps whatever order it already had.
//
// Equal scores are for the caller to break. Comparing the two candidates is
// enough to make it a total order, which is what Index does.
func (p Pattern) Score(candidate string) (int, bool) {
	if p.runes == 0 {
		return 0, true
	}
	// A pattern of n ASCII runes is n bytes, and it needs n runes of candidate,
	// which is at least n bytes of it. Folding a non-ASCII rune can shorten it,
	// so this is only safe to skip on the ASCII side.
	if candidate == "" || (p.ascii && len(p.text) > len(candidate)) {
		return 0, false
	}

	best, found, starts := 0, false, 0
	prev, at := rune(-1), 0
	for off, r := range candidate {
		if fold(r) == p.first && (starts == 0 || wordStart(prev, r)) {
			score, gaps, ok := p.scoreFrom(candidate, off, at, prev)
			if ok && (!found || score > best) {
				best, found = score, true
			}
			// A contiguous match on the first rune cannot be beaten: no later
			// start can hold the prefix bonus, and none can skip fewer runes.
			if ok && off == 0 && gaps == 0 {
				break
			}
			starts++
			if starts >= maxStarts {
				break
			}
		}
		prev, at = r, at+1
	}
	if !found {
		return 0, false
	}
	return best - lengthTail(candidate), true
}

// scoreFrom matches the whole pattern from one starting rune of the candidate,
// taking the first place each later rune of the pattern appears. It reports the
// score, how many runs of runes it had to skip, and whether the pattern fitted
// at all.
func (p Pattern) scoreFrom(candidate string, start, startRune int, before rune) (score, gaps int, ok bool) {
	want, wantWidth := utf8.DecodeRuneInString(p.text)
	folded := fold(want)

	matched, taken := 0, wantWidth
	prev, end := before, -1
	for i := start; i < len(candidate); {
		r, w := utf8.DecodeRuneInString(candidate[i:])
		if fold(r) == folded {
			if wordStart(prev, r) {
				score += scoreWordStart
			}
			switch {
			case matched == 0:
			case i == end:
				score += scoreAdjacent
			default:
				score -= scoreGap
				gaps++
			}
			matched, end = matched+1, i+w
			if taken == len(p.text) {
				break
			}
			want, wantWidth = utf8.DecodeRuneInString(p.text[taken:])
			folded, taken = fold(want), taken+wantWidth
		}
		prev, i = r, i+w
	}
	if matched < p.runes {
		return 0, 0, false
	}
	// A prefix is the pattern spelt out from the candidate's first rune. Landing
	// on the first rune and then skipping through the rest of it is a scattered
	// match that happens to start early, and pays the tail for it instead.
	if start == 0 && gaps == 0 {
		score += scorePrefix
		if end == len(candidate) {
			score += scoreWhole
		}
	}
	return score - min(startRune, tailMaxLead)*tailLeadStep, gaps, true
}

// lengthTail is the part of the tail that prefers a shorter candidate. It is
// the same for every start in one candidate, so it is applied once rather than
// per attempt.
func lengthTail(candidate string) int {
	return min(utf8.RuneCountInString(candidate), tailMaxRunes)
}

// fold is the one-rune case mapping the whole scorer uses, so that neither the
// pattern nor the candidate is ever lowercased into a new string.
func fold(r rune) rune {
	if r < utf8.RuneSelf {
		if 'A' <= r && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}
	return unicode.ToLower(r)
}

// wordStart reports whether cur begins a word, given the rune before it. A
// negative prev is the start of the candidate, which counts: "the log" and
// "log" both start a word at the l.
func wordStart(prev, cur rune) bool {
	switch {
	case prev < 0, !wordRune(prev):
		return true
	case unicode.IsLower(prev) && unicode.IsUpper(cur):
		return true
	case unicode.IsLetter(prev) && unicode.IsDigit(cur):
		return true
	default:
		return false
	}
}

func wordRune(r rune) bool {
	if r < utf8.RuneSelf {
		return ('0' <= r && r <= '9') || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z')
	}
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

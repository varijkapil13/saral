package palette

import "strings"

// The weights the fuzzy score is built from. They are relative to each other
// and to nothing else: what matters is which of two candidates comes first.
const (
	scoreStart       = 24
	scoreBoundary    = 14
	scoreConsecutive = 8
	scoreLoose       = 1
	gapPenalty       = -2
	gapPenaltyFloor  = -12
	lateStartFloor   = -8
	lengthDivisor    = 4
)

// separators are what starts a word here: prose in a title, dots in an ID.
const separators = " \t.-_/:,()[]"

// candidate is one string a command can be found by, prepared once so that a
// keystroke costs a walk rather than a lowercase and a rune conversion per row.
type candidate struct {
	runes []rune
	// bound[i] reports that runes[i] begins a word, which is worth nearly as
	// much as beginning the string.
	bound []bool
}

func newCandidate(s string) candidate {
	lower := strings.ToLower(s)
	c := candidate{runes: []rune(lower)}
	c.bound = make([]bool, len(c.runes))
	for i := range c.runes {
		c.bound[i] = i == 0 || strings.ContainsRune(separators, c.runes[i-1])
	}
	return c
}

func (c candidate) empty() bool { return len(c.runes) == 0 }

// match scores query against the candidate as a subsequence, and reports false
// when the candidate does not contain it at all. The query is lowercased by the
// caller, once per keystroke rather than once per row.
//
// Every position the first rune appears at is tried, because the walk from the
// earliest one is not always the best alignment: "ie" against "issue edit"
// starts on the i of issue and finishes six words later, while starting on the
// i of edit is two consecutive characters.
func (c candidate) match(query []rune) (int, bool) {
	if len(query) == 0 {
		return 0, true
	}
	if len(query) > len(c.runes) {
		return 0, false
	}
	best, found := 0, false
	for start, r := range c.runes {
		if r != query[0] {
			continue
		}
		if score, ok := c.walk(query, start); ok && (!found || score > best) {
			best, found = score, true
		}
	}
	if !found {
		return 0, false
	}
	return best - len(c.runes)/lengthDivisor, true
}

// walk scores the greedy match that begins at start.
func (c candidate) walk(query []rune, start int) (int, bool) {
	score, at, prev := max(-start, lateStartFloor), start, -1
	for _, q := range query {
		i := c.next(q, at)
		if i < 0 {
			return 0, false
		}
		switch {
		case i == 0:
			score += scoreStart
		case c.bound[i]:
			score += scoreBoundary
		case i == prev+1:
			score += scoreConsecutive
		default:
			score += scoreLoose
		}
		if prev >= 0 && i > prev+1 {
			score += max(gapPenalty*(i-prev-1), gapPenaltyFloor)
		}
		prev, at = i, i+1
	}
	return score, true
}

func (c candidate) next(r rune, from int) int {
	for i := from; i < len(c.runes); i++ {
		if c.runes[i] == r {
			return i
		}
	}
	return -1
}

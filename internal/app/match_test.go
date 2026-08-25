package app

import (
	"strings"
	"testing"
)

func TestPattern_MatchesASubsequenceIgnoringCase(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		pattern   string
		candidate string
		want      bool
	}{
		{name: "the whole candidate", pattern: "log", candidate: "log", want: true},
		{name: "a prefix of it", pattern: "log", candidate: "login flow", want: true},
		{name: "a word inside it", pattern: "flow", candidate: "Fix the login flow", want: true},
		{name: "runes in order but apart", pattern: "lgn", candidate: "login flow", want: true},
		{name: "runes out of order do not match", pattern: "nigol", candidate: "login flow", want: false},
		{name: "a rune the candidate does not have", pattern: "logz", candidate: "login flow", want: false},
		{name: "a pattern typed in capitals", pattern: "LOGIN", candidate: "the login flow", want: true},
		{name: "a candidate written in capitals", pattern: "login", candidate: "LOGIN FLOW", want: true},
		{name: "a pattern longer than the candidate", pattern: "login flow", candidate: "login", want: false},
		{name: "an empty candidate", pattern: "log", candidate: "", want: false},
		{name: "an empty pattern matches anything", pattern: "", candidate: "login flow", want: true},
		{name: "an empty pattern matches an empty candidate", pattern: "", candidate: "", want: true},
		{name: "a space in the pattern spans two words", pattern: "login flow", candidate: "Fix the login flow", want: true},
		{name: "an issue key typed whole", pattern: "PROJ-142", candidate: "PROJ-142", want: true},
		{name: "the digits of an issue key", pattern: "142", candidate: "PROJ-142", want: true},
		{name: "the digits of another issue", pattern: "142", candidate: "PROJ-1420", want: true},
		{name: "a pattern of only spaces needs a space", pattern: " ", candidate: "login", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, got := NewPattern(tc.pattern).Score(tc.candidate); got != tc.want {
				t.Errorf("%q against %q matched %v, want %v", tc.pattern, tc.candidate, got, tc.want)
			}
		})
	}
}

// Each slice is written best first, and every neighbouring pair is asserted
// rather than only the ends.
func TestPattern_RanksAPrefixOverAWordStartOverAScatteredMatch(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		pattern string
		ordered []string
	}{
		{
			name:    "a title matched on a word",
			pattern: "log",
			ordered: []string{
				"log",                  // the whole candidate
				"login",                // a prefix of it
				"the log file",         // a word start further in, whole
				"a lonely green apple", // the initials of three words
				"catalogue",            // whole, but inside a word
				"blowing granite",      // scattered, and inside a word
			},
		},
		{
			name:    "an issue key",
			pattern: "proj-14",
			ordered: []string{
				"PROJ-14",
				"PROJ-142",
				"PROJ-1420",
			},
		},
		{
			name:    "the digits of a key against a title that also has them",
			pattern: "142",
			ordered: []string{
				"142",
				"PROJ-142",
				"Rework the 1.4.2 upgrade notes",
			},
		},
		{
			name:    "a shorter title wins where the match is otherwise the same",
			pattern: "the",
			ordered: []string{
				"the export",
				"the nightly export",
				"the nightly export of everything the site holds",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			pattern := NewPattern(tc.pattern)
			scores := make([]int, len(tc.ordered))
			for i, candidate := range tc.ordered {
				score, ok := pattern.Score(candidate)
				if !ok {
					t.Fatalf("%q did not match %q at all", tc.pattern, candidate)
				}
				scores[i] = score
			}
			for i := 1; i < len(scores); i++ {
				if scores[i-1] <= scores[i] {
					t.Errorf("%q scores %d on %q and %d on %q; the first is the better match",
						tc.pattern, scores[i-1], tc.ordered[i-1], scores[i], tc.ordered[i])
				}
			}
		})
	}
}

func TestPattern_RanksTranslatedTitlesTheSameWay(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		pattern string
		ordered []string
	}{
		{
			name:    "an accent in both the pattern and the title",
			pattern: "café",
			ordered: []string{"café", "caféine", "un caféier", "le café du matin"},
		},
		{
			name:    "an accented pattern typed in capitals",
			pattern: "CAFÉ",
			ordered: []string{"café", "le café du matin"},
		},
		{
			name:    "runes wider than a byte carry no byte offsets into the ranking",
			pattern: "ポー",
			ordered: []string{"ポート", "サポート", "会議のサポート体制"},
		},
		{
			name:    "a Cyrillic word start beats one inside a word",
			pattern: "вход",
			ordered: []string{"вход", "исправить вход в систему", "невходной"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			pattern := NewPattern(tc.pattern)
			prev := 0
			for i, candidate := range tc.ordered {
				score, ok := pattern.Score(candidate)
				if !ok {
					t.Fatalf("%q did not match %q at all", tc.pattern, candidate)
				}
				if i > 0 && prev <= score {
					t.Errorf("%q scores %d on %q and %d on %q; the first is the better match",
						tc.pattern, prev, tc.ordered[i-1], score, candidate)
				}
				prev = score
			}
		})
	}
}

func TestPattern_TreatsACamelCaseHumpAsAWordStart(t *testing.T) {
	t.Parallel()

	pattern := NewPattern("flow")
	camel, _ := pattern.Score("loginFlow")
	joined, _ := pattern.Score("loginflow")
	spaced, _ := pattern.Score("login flow")
	if camel <= joined {
		t.Errorf("%q scores %d on %q and %d on %q; the hump starts a word and the run of letters does not",
			"flow", camel, "loginFlow", joined, "loginflow")
	}
	if camel-spaced >= scoreUnit {
		t.Errorf("%q scores %d in camel case against %d spaced; the two should differ only by the tail",
			"flow", camel, spaced)
	}
}

func TestPattern_KnowsWhenItIsTheOneThatMatchesEverything(t *testing.T) {
	t.Parallel()

	if !NewPattern("").Empty() {
		t.Error("the empty pattern does not say it is empty")
	}
	if NewPattern(" ").Empty() {
		t.Error("a pattern of one space says it is empty; a space is a rune like any other")
	}
	if score, ok := NewPattern("").Score("anything"); !ok || score != 0 {
		t.Errorf("the empty pattern scored %d, %v on a candidate, want 0, true", score, ok)
	}
}

func TestPattern_CostsNothingToPrepareOrToScore(t *testing.T) {
	candidates := []string{
		"PROJ-142", "Fix the login flow", "Speed up the nightly export",
		"会議のサポート体制", "Rework webhook retries",
	}
	if got := testing.AllocsPerRun(500, func() {
		pattern := NewPattern("log")
		for _, candidate := range candidates {
			_, _ = pattern.Score(candidate)
		}
	}); got != 0 {
		t.Errorf("preparing a pattern and scoring five candidates allocates %.1f times, want none", got)
	}
}

// A title that starts more words with the pattern's first rune than maxStarts
// allows attempts from.
func TestPattern_StillMatchesATitleThatStartsManyWordsWithTheSameRune(t *testing.T) {
	t.Parallel()

	candidate := strings.Repeat("log ", 40) + "final logout"
	if _, ok := NewPattern("logout").Score(candidate); !ok {
		t.Error("a match past the eighth word start was not found at all")
	}
	if _, ok := NewPattern("logz").Score(candidate); ok {
		t.Error("a pattern the title cannot satisfy was reported as a match")
	}
}

func BenchmarkPatternScore(b *testing.B) {
	pattern := NewPattern("log")
	candidates := []string{
		"PROJ-142", "Fix the login flow", "Speed up the nightly export",
		"Investigate webhook retries", "Document the release checklist",
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		_, _ = pattern.Score(candidates[i%len(candidates)])
	}
}

func BenchmarkPatternScoreTranslated(b *testing.B) {
	pattern := NewPattern("ポー")
	candidates := []string{"ポート", "サポート", "会議のサポート体制", "オンボーディング"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		_, _ = pattern.Score(candidates[i%len(candidates)])
	}
}

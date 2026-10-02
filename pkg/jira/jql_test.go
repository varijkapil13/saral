package jira_test

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/varijkapil13/saral/pkg/jira"
)

func TestQuoteJQL_EscapesQuotesBackslashesAndControlCharacters(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, in, want string }{
		{"plain text", `login`, `"login"`},
		{"a quote", `say "hi"`, `"say \"hi\""`},
		{"a backslash", `a\b`, `"a\\b"`},
		{"a trailing backslash", `a\`, `"a\\"`},
		{"newline, return and tab", "a\nb\rc\td", `"a\nb\rc\td"`},
		{"other control characters are dropped", "a\x00b\x07c\x7fd\u2028e\u2029f", `"abcdef"`},
		{"everything else is untouched", "über straße 日本", `"über straße 日本"`},
		{"nothing at all", ``, `""`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := jira.QuoteJQL(tc.in); got != tc.want {
				t.Errorf("QuoteJQL(%q) = %s, want %s", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseText_TurnsWhatWasTypedIntoWordsAndPhrases(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, in, lucene, clause string }{
		{"two words", `login timeout`, `login timeout*`, `text ~ "login timeout*"`},
		{"a phrase and a word", `"null pointer" crash`, `"null pointer" crash*`, `text ~ "\"null pointer\" crash*"`},
		{"only a phrase", `"null pointer"`, `"null pointer"`, `text ~ "\"null pointer\""`},
		{"operators are only words", `foo AND bar`, `foo and bar*`, `text ~ "foo and bar*"`},
		{"a path", `C:\path\file.txt`, `c path file.txt*`, `text ~ "c path file.txt*"`},
		{"a key", `PROJ-142`, `proj 142*`, `text ~ "proj 142*"`},
		{"brackets and plus", `a+b (c)`, `a b c`, `text ~ "a b c"`},
		{"a leading wildcard", `*foo`, `foo*`, `text ~ "foo*"`},
		{"a question mark", `fo?o`, `fo o`, `text ~ "fo o"`},
		{"a question mark before a longer word", `fo?oo`, `fo oo*`, `text ~ "fo oo*"`},
		{"a wildcard inside a word", `fo*o`, `foo*`, `text ~ "foo*"`},
		{"an explicit prefix in the middle", `foo* bar`, `foo* bar*`, `text ~ "foo* bar*"`},
		{"accents and eszett", `über straße`, `über straße*`, `text ~ "über straße*"`},
		{"a single letter is not a prefix", `a`, `a`, `text ~ "a"`},
		{"a trailing dot is not a prefix", `foo.`, `foo.`, `text ~ "foo."`},
		{"an unpaired quote is a space", `"unbalanced quote`, `unbalanced quote*`, `text ~ "unbalanced quote*"`},
		{"a stray third quote is a space", `"a b" "c`, `"a b" c`, `text ~ "\"a b\" c"`},
		{"whitespace collapses", "  login \t\n  timeout  ", `login timeout*`, `text ~ "login timeout*"`},
		{"a phrase does not take a wildcard", `"null poin*"`, `"null poin"`, `text ~ "\"null poin\""`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			q := jira.ParseText(tc.in)
			if got := q.Lucene(); got != tc.lucene {
				t.Errorf("Lucene(%q) = %q, want %q", tc.in, got, tc.lucene)
			}
			if got := q.Clause(jira.TextAll); got != tc.clause {
				t.Errorf("Clause(%q) = %s, want %s", tc.in, got, tc.clause)
			}
		})
	}

	for _, in := range []string{"", "   ", "!!!", `\`, `""`, `"`, "...", "* ?"} {
		if q := jira.ParseText(in); !q.Empty() {
			t.Errorf("ParseText(%q) = %q, want nothing", in, q.Lucene())
		}
	}
}

func TestParseText_CapsRunesAndTerms(t *testing.T) {
	t.Parallel()
	long := jira.ParseText(strings.Repeat("a", jira.MaxTextRunes*3))
	if n := utf8.RuneCountInString(long.Lucene()); n > jira.MaxTextRunes+1 {
		t.Errorf("a very long word came out %d runes, want at most %d", n, jira.MaxTextRunes+1)
	}

	var words []string
	for i := range 50 {
		words = append(words, "w"+strconv.Itoa(i))
	}
	many := jira.ParseText(strings.Join(words, " "))
	if len(many.Terms) != jira.MaxTextTerms {
		t.Errorf("got %d terms, want %d", len(many.Terms), jira.MaxTextTerms)
	}

	var phrases []string
	for range 50 {
		phrases = append(phrases, `"ab cd"`)
	}
	if got := len(jira.ParseText(strings.Join(phrases, " ")).Terms); got > jira.MaxTextTerms {
		t.Errorf("got %d phrase terms, want at most %d", got, jira.MaxTextTerms)
	}
}

func TestTextQuery_ClauseIsOneJQLStringLiteral(t *testing.T) {
	t.Parallel()
	inputs := []string{`say "hi" \ there`, "tab\tand\nnewline", `"a" "b" "c`, `x\" OR project = Y`, `\`}
	for _, in := range inputs {
		for _, f := range []jira.TextField{jira.TextAll, jira.TextSummary, jira.TextDescription, jira.TextComment} {
			q := jira.ParseText(in)
			if q.Empty() {
				continue
			}
			clause := q.Clause(f)
			prefix := string(f) + " ~ "
			if !strings.HasPrefix(clause, prefix) {
				t.Fatalf("clause %q does not start with %q", clause, prefix)
			}
			lit := strings.TrimPrefix(clause, prefix)
			got, err := strconv.Unquote(lit)
			if err != nil {
				t.Fatalf("clause %q is not one string literal: %v", clause, err)
			}
			if got != q.Lucene() {
				t.Errorf("literal reads back %q, want %q", got, q.Lucene())
			}
		}
	}
}

func TestTextQuery_WordsAreDedupedAndCarryNoWildcard(t *testing.T) {
	t.Parallel()
	q := jira.ParseText(`Login "login timeout" TIMEOUT crash*`)
	want := []string{"login", "timeout", "crash"}
	if got := q.Words(); !reflect.DeepEqual(got, want) {
		t.Errorf("Words = %v, want %v", got, want)
	}
	if got := q.Longest(); got != 7 {
		t.Errorf("Longest = %d, want 7", got)
	}
	if got := jira.ParseText("").Longest(); got != 0 {
		t.Errorf("Longest of nothing = %d, want 0", got)
	}
}

func FuzzParseText(f *testing.F) {
	for _, seed := range []string{``, `login timeout`, `"null pointer" crash`, `a\"b`, "x\ny", `\`, `"""`, `C:\path`, "\u2028 a", `a*b?c`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		q := jira.ParseText(in)
		if q.Empty() {
			return
		}
		clause := q.Clause(jira.TextAll)
		lit := strings.TrimPrefix(clause, "text ~ ")
		for _, r := range lit {
			if r < 0x20 || r == 0x7f {
				t.Fatalf("raw control character %q in %q", r, clause)
			}
		}
		unescaped := 0
		for i := 0; i < len(lit); i++ {
			switch lit[i] {
			case '\\':
				i++
			case '"':
				unescaped++
			}
		}
		if unescaped != 2 {
			t.Fatalf("%d unescaped quotes in %q", unescaped, clause)
		}
		got, err := strconv.Unquote(lit)
		if err != nil || got != q.Lucene() {
			t.Fatalf("literal %s reads back %q (%v), want %q", lit, got, err, q.Lucene())
		}
	})
}

func BenchmarkParseText(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		jira.ParseText(`"null pointer" login timeout crash on C:\path\file.txt`).Clause(jira.TextAll)
	}
}

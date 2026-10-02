package jira

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxTextRunes is how much of a typed search ParseText reads.
const MaxTextRunes = 200

// MaxTextTerms is how many words and phrases ParseText keeps.
const MaxTextTerms = 20

// TextField is a JQL field the ~ operator searches.
type TextField string

// The fields ~ understands. TextAll searches summary, description, comments and
// the rest of what Jira indexes as text.
const (
	TextAll         TextField = "text"
	TextSummary     TextField = "summary"
	TextDescription TextField = "description"
	TextComment     TextField = "comment"
)

// TextTerm is one word or one quoted phrase of a text search.
type TextTerm struct {
	Words  []string
	Phrase bool
	Prefix bool
}

// TextQuery is what ParseText made of a typed search.
type TextQuery struct{ Terms []TextTerm }

// QuoteJQL returns s as one JQL string literal, quotes included. Control
// characters that cannot sit inside a literal are escaped or dropped.
func QuoteJQL(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20, r == 0x7f, r == 0x2028, r == 0x2029:
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// ParseText turns what someone typed into a query that Jira reads the same way
// every time: Lucene operators and punctuation become spaces, words are
// lowercased so AND, OR and NOT are only words, and the last word is a prefix so
// a search finds as the user types.
func ParseText(input string) TextQuery {
	runes := []rune(input)
	if len(runes) > MaxTextRunes {
		runes = runes[:MaxTextRunes]
	}

	quotes, lastQuote := 0, -1
	for i, r := range runes {
		if r == '"' {
			quotes++
			lastQuote = i
		}
	}
	if quotes%2 == 1 {
		runes[lastQuote] = ' '
	}

	var (
		q        TextQuery
		word     []rune
		phrase   []string
		inPhrase bool
		prefix   bool
	)
	flush := func() {
		defer func() { word, prefix = word[:0], false }()
		if !slices.ContainsFunc(word, isTextAlnum) {
			return
		}
		w := strings.ToLower(string(word))
		switch {
		case inPhrase:
			phrase = append(phrase, w)
		case len(q.Terms) < MaxTextTerms:
			q.Terms = append(q.Terms, TextTerm{Words: []string{w}, Prefix: prefix})
		}
	}
	for i, r := range runes {
		switch {
		case r == '"':
			flush()
			if inPhrase && len(phrase) > 0 && len(q.Terms) < MaxTextTerms {
				q.Terms = append(q.Terms, TextTerm{Words: phrase, Phrase: true})
			}
			phrase = nil
			inPhrase = !inPhrase
		case isTextAlnum(r) || strings.ContainsRune(".,;%$#@'_", r):
			word = append(word, r)
		case r == '*':
			if len(word) > 0 && (i+1 == len(runes) || !isTextWordRune(runes[i+1])) {
				prefix = true
			}
		default:
			flush()
		}
	}
	flush()

	if n := len(q.Terms); n > 0 && !q.Terms[n-1].Phrase {
		t := &q.Terms[n-1]
		w := []rune(t.Words[0])
		if len(w) >= 2 && isTextAlnum(w[len(w)-1]) {
			t.Prefix = true
		}
	}
	return q
}

func isTextAlnum(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

func isTextWordRune(r rune) bool {
	return isTextAlnum(r) || strings.ContainsRune(".,;%$#@'_*", r)
}

// Empty reports whether nothing searchable was typed.
func (q TextQuery) Empty() bool { return len(q.Terms) == 0 }

// Lucene is the query text Jira's text index is given.
func (q TextQuery) Lucene() string {
	var b strings.Builder
	for i, t := range q.Terms {
		if i > 0 {
			b.WriteByte(' ')
		}
		if t.Phrase {
			b.WriteByte('"')
			b.WriteString(strings.Join(t.Words, " "))
			b.WriteByte('"')
			continue
		}
		b.WriteString(strings.Join(t.Words, " "))
		if t.Prefix {
			b.WriteByte('*')
		}
	}
	return b.String()
}

// Clause is the JQL clause that searches f for the query.
func (q TextQuery) Clause(f TextField) string {
	return string(f) + " ~ " + QuoteJQL(q.Lucene())
}

// Words lists the distinct words in the query, without wildcards, in the order
// they were typed.
func (q TextQuery) Words() []string {
	var out []string
	for _, t := range q.Terms {
		for _, w := range t.Words {
			if !slices.Contains(out, w) {
				out = append(out, w)
			}
		}
	}
	return out
}

// Longest is the length in runes of the longest word in the query.
func (q TextQuery) Longest() int {
	n := 0
	for _, t := range q.Terms {
		for _, w := range t.Words {
			n = max(n, utf8.RuneCountInString(w))
		}
	}
	return n
}

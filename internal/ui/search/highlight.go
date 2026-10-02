package search

import (
	"unicode"
	"unicode/utf8"
)

type span struct{ from, to int }

func highlights(summary string, words []string) []span {
	if len(words) == 0 || summary == "" {
		return nil
	}
	var out []span
	prevWord := false
	for i := 0; i < len(summary); {
		r, size := utf8.DecodeRuneInString(summary[i:])
		isWord := unicode.IsLetter(r) || unicode.IsDigit(r)
		if isWord && !prevWord {
			best := 0
			for _, w := range words {
				if n := foldPrefix(summary[i:], w); n > best {
					best = n
				}
			}
			if best > 0 {
				out = append(out, span{from: i, to: i + best})
				i += best
				prevWord = true
				continue
			}
		}
		prevWord = isWord
		i += size
	}
	return out
}

func foldPrefix(s, prefix string) int {
	n := 0
	for _, want := range prefix {
		if n >= len(s) {
			return 0
		}
		got, size := utf8.DecodeRuneInString(s[n:])
		if !foldEq(got, want) {
			return 0
		}
		n += size
	}
	return n
}

func foldEq(a, b rune) bool {
	if a == b {
		return true
	}
	for f := unicode.SimpleFold(a); f != a; f = unicode.SimpleFold(f) {
		if f == b {
			return true
		}
	}
	return false
}

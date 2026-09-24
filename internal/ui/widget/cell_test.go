package widget

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestSanitize_DropsWhatATerminalWouldActOn(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		in   string
		want string
	}{
		"clean ascii is untouched": {
			in: "ship the release", want: "ship the release",
		},
		"a tab becomes a single space": {
			in: "a\tb\t\tc", want: "a b  c",
		},
		"an escape sequence is stripped": {
			in: "before\x1b[31mred\x1b[0mafter", want: "beforeredafter",
		},
		"an OSC hyperlink's URL is stripped, its visible text is not": {
			in: "before\x1b]8;;http://evil\x07hyperlink\x1b]8;;\x07after", want: "beforehyperlinkafter",
		},
		"an OSC sequence introduced by a C1 byte is stripped": {
			in: "before\x9dhyperlink\x07after", want: "beforeafter",
		},
		"a C0 control other than tab is stripped": {
			in: "a\x07b", want: "ab",
		},
		"DEL is stripped": {
			in: "a\x7fb", want: "ab",
		},
		"a right-to-left override is stripped": {
			in: "a\u202eb", want: "ab",
		},
		"every bidi embedding and override is stripped": {
			in: "\u202a\u202b\u202c\u202d\u202ex", want: "x",
		},
		"every bidi isolate is stripped": {
			in: "\u2066\u2067\u2068\u2069x", want: "x",
		},
		"a newline inside a cell is stripped": {
			in: "a\nb", want: "ab",
		},
		"unicode text outside the control ranges survives": {
			in: "日本語 émoji 🎉", want: "日本語 émoji 🎉",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := Sanitize(tc.in); got != tc.want {
				t.Errorf("Sanitize(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// AllocsPerRun disallows a parallel subtest, so this one runs alone.
func TestSanitize_CleanASCIIAllocatesNothing(t *testing.T) {
	const s = "PROJ-123 ship the release to the customer"
	allocs := testing.AllocsPerRun(100, func() {
		_ = Sanitize(s)
	})
	if allocs != 0 {
		t.Errorf("Sanitize on clean ASCII allocated %v times per run, want 0", allocs)
	}
}

func TestPadTruncate_PadsAndTruncatesToExactlyWidth(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		s, ellipsis string
		width       int
		want        string
	}{
		"shorter than width is padded": {s: "abc", width: 6, ellipsis: "…", want: "abc   "},
		"exact width is untouched":     {s: "abcdef", width: 6, ellipsis: "…", want: "abcdef"},
		"longer than width is truncated with an ellipsis": {
			s: "abcdefgh", width: 6, ellipsis: "…", want: "abcd" + "e…",
		},
		"a wide grapheme counts as two columns": {
			s: "日本語", width: 4, ellipsis: "…", want: "日… ",
		},
		"a non-positive width draws nothing": {s: "abc", width: 0, ellipsis: "…", want: ""},
		"embedded styling survives, so a card can colour a key before truncating it": {
			s: "\x1b[31mabcdefgh\x1b[0m", width: 4, ellipsis: "…", want: "\x1b[31mabc…\x1b[0m",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := PadTruncate(tc.s, tc.width, tc.ellipsis)
			if got != tc.want {
				t.Errorf("PadTruncate(%q, %d) = %q, want %q", tc.s, tc.width, got, tc.want)
			}
			if w := ansi.StringWidth(got); tc.width > 0 && w != tc.width {
				t.Errorf("PadTruncate(%q, %d) is %d columns wide, want %d", tc.s, tc.width, w, tc.width)
			}
		})
	}
}

func TestPadLeft_RightAlignsAndFallsBackToTruncate(t *testing.T) {
	t.Parallel()

	if got := PadLeft("42", 6, "…"); got != "    42" {
		t.Errorf("PadLeft right-aligned = %q, want %q", got, "    42")
	}
	if got := PadLeft("abcdefgh", 6, "…"); got != "abcd"+"e…" {
		t.Errorf("PadLeft over width = %q, want a truncated cell", got)
	}
	if got := PadLeft("ab", 0, "…"); got != "" {
		t.Errorf("PadLeft with no width = %q, want empty", got)
	}
}

func BenchmarkPadTruncate(b *testing.B) {
	b.Run("clean ascii", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = PadTruncate("PROJ-123 ship the release to the customer", 24, "…")
		}
	})
	b.Run("embedded styling", func(b *testing.B) {
		b.ReportAllocs()
		s := "\x1b[31m" + strings.Repeat("x", 30) + "\x1b[0m"
		for b.Loop() {
			_ = PadTruncate(s, 24, "…")
		}
	})
}

func BenchmarkSanitize(b *testing.B) {
	b.Run("clean ascii", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = Sanitize("PROJ-123 ship the release to the customer")
		}
	})
	b.Run("needs sanitising", func(b *testing.B) {
		b.ReportAllocs()
		s := "before\x1b[31m" + strings.Repeat("x", 20) + "\x1b[0mafter"
		for b.Loop() {
			_ = Sanitize(s)
		}
	})
}

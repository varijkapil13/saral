package list

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/jira"
)

func TestPlanLayout_DropsColumnsFromTheRightUntilTheSummaryFits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		width   int
		wantOff []string
	}{
		{name: "a wide terminal keeps every column", width: 160},
		{name: "a hundred columns still fits them all", width: 100},
		{name: "eighty columns gives up the date and the assignee", width: 80, wantOff: []string{"updated", "assignee"}},
		{name: "forty columns keeps only the key and the summary", width: 40, wantOff: []string{"updated", "assignee", "typ", "status"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			lay := planLayout(tt.width, 8)
			off := map[string]bool{}
			for name, w := range map[string]int{"typ": lay.typ, "status": lay.status, "assignee": lay.assignee, "updated": lay.updated} {
				if w == 0 {
					off[name] = true
				}
			}
			if len(off) != len(tt.wantOff) {
				t.Errorf("at %d columns the plan dropped %v, want %v", tt.width, off, tt.wantOff)
			}
			for _, name := range tt.wantOff {
				if !off[name] {
					t.Errorf("at %d columns the %s column survived", tt.width, name)
				}
			}
			if lay.summary < 1 {
				t.Errorf("at %d columns the summary has no room at all", tt.width)
			}
		})
	}
}

func TestRenderRow_IsExactlyAsWideAsTheLayoutWhateverTheContent(t *testing.T) {
	t.Parallel()

	theme := kernel.NewTheme(kernel.ThemeNoColor, true, kernel.UnicodeGlyphs())
	st := newStyles(theme)
	now := time.Date(2025, time.March, 5, 9, 0, 0, 0, time.UTC)

	tests := map[string]jira.Issue{
		"a plain row":        {Key: "PROJ-1", Summary: "Fix the thing", Status: jira.Status{Name: "Building", Category: jira.CategoryInProgress}},
		"a very long one":    {Key: "VERYLONGPROJECT-12345", Summary: strings.Repeat("a long summary ", 20), Status: jira.Status{Name: "Waiting on somebody else entirely"}},
		"nothing at all":     {Key: "PROJ-2"},
		"wide graphemes":     {Key: "PROJ-3", Summary: "修正 the 日本語 layout", Status: jira.Status{Name: "進行中"}},
		"an emoji and a ZWJ": {Key: "PROJ-4", Summary: "🚀 ship it 👩‍💻 today", Status: jira.Status{Name: "Triage"}},
	}

	for _, width := range []int{80, 100, 120, 200} {
		for name, iss := range tests {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				lay := planLayout(width, 8)
				// Marked and unmarked, because the clickable cells put private
				// escape sequences inside the row and a marker that measured as
				// a column would shift everything to its right.
				for zname, z := range map[string]widget.Zoner{"unmarked": {}, "marked": markingZoner(t)} {
					for _, sel := range []bool{false, true} {
						got := ansi.StringWidth(renderRow(&iss, lay, sel, st, theme, time.UTC, now, z))
						if got != lay.width {
							t.Errorf("a %s row is %d columns at width %d (selected=%t), want %d", zname, got, width, sel, lay.width)
						}
					}
				}
			})
		}
	}
}

func TestRenderRow_TypeAndStatusIconsAcrossGlyphTiers(t *testing.T) {
	iss := jira.Issue{
		Key:     "PROJ-42",
		Summary: "A row whose type and status names do not fit their columns",
		Type:    jira.IssueType{Name: "Documentation"},
		Status:  jira.Status{Name: "Waiting on somebody else entirely", Category: jira.CategoryInProgress},
	}
	now := time.Date(2025, time.March, 5, 9, 0, 0, 0, time.UTC)
	lay := planLayout(100, 8)

	for _, tier := range []struct {
		name   string
		glyphs kernel.Glyphs
	}{
		{"nerd", kernel.NerdGlyphs()},
		{"unicode", kernel.UnicodeGlyphs()},
		{"ascii", kernel.ASCIIGlyphs()},
	} {
		t.Run(tier.name, func(t *testing.T) {
			theme := kernel.NewTheme(kernel.ThemeNoColor, true, tier.glyphs)
			st := newStyles(theme)
			got := ansi.Strip(renderRow(&iss, lay, false, st, theme, time.UTC, now, widget.Zoner{}))
			golden(t, "row_icons_"+tier.name+".golden", got+"\n")
		})
	}
}

func TestFormatWhen_RendersInTheAccountsZoneAndDropsTheYearOnlyWhenItIsThisOne(t *testing.T) {
	t.Parallel()

	kolkata, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Skipf("no timezone database here: %v", err)
	}
	now := time.Date(2026, time.March, 2, 9, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		at   time.Time
		loc  *time.Location
		want string
	}{
		{name: "this year, in UTC", at: time.Date(2026, time.March, 1, 20, 15, 0, 0, time.UTC), loc: time.UTC, want: "01 Mar 20:15"},
		{name: "this year, in the account's zone", at: time.Date(2026, time.March, 1, 20, 15, 0, 0, time.UTC), loc: kolkata, want: "02 Mar 01:45"},
		{name: "another year", at: time.Date(2024, time.November, 3, 9, 0, 0, 0, time.UTC), loc: time.UTC, want: "03 Nov 2024"},
		{name: "no zone at all falls back to UTC", at: time.Date(2026, time.March, 1, 20, 15, 0, 0, time.UTC), loc: nil, want: "01 Mar 20:15"},
		{name: "never set", at: time.Time{}, loc: time.UTC, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := formatWhen(tt.at, now, tt.loc); got != tt.want {
				t.Errorf("formatWhen = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPadTruncate_CountsColumnsRatherThanBytes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		in    string
		width int
		want  string
	}{
		{name: "short is padded", in: "ab", width: 5, want: "ab   "},
		{name: "exact is left alone", in: "abcde", width: 5, want: "abcde"},
		{name: "long is cut with an ellipsis", in: "abcdefgh", width: 5, want: "abcd…"},
		{name: "a wide grapheme counts as two", in: "日本語", width: 4, want: "日… "},
		{name: "no room at all", in: "abc", width: 0, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := padTruncate(tt.in, tt.width, "…")
			if got != tt.want {
				t.Errorf("padTruncate(%q, %d) = %q, want %q", tt.in, tt.width, got, tt.want)
			}
			if w := ansi.StringWidth(got); tt.width > 0 && w != tt.width {
				t.Errorf("padTruncate(%q, %d) is %d columns wide", tt.in, tt.width, w)
			}
		})
	}
}

func TestRowCache_StaysBoundedAndForgetsARowWhoseIssueMoved(t *testing.T) {
	t.Parallel()

	c := newRowCache(4)
	base := rowKey{key: "PROJ-1", updated: 1}
	c.put(base, "first")

	if got, ok := c.get(base); !ok || got != "first" {
		t.Fatalf("the row was not memoized: %q %t", got, ok)
	}
	moved := base
	moved.updated = 2
	if _, ok := c.get(moved); ok {
		t.Error("an issue that has been updated since hit the memo anyway")
	}

	for i := range 20 {
		c.put(rowKey{key: "PROJ-1", updated: int64(i + 10)}, "row")
	}
	if len(c.rows) > 4 {
		t.Errorf("the memo grew to %d entries with a limit of 4", len(c.rows))
	}
}

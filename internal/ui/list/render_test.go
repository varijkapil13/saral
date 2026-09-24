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

// A summary reaches the row exactly as Jira sent it: a tab that would misalign
// the columns after it, an escape sequence that could repaint the row, and a
// bidi override that could read the key backwards.
func TestRenderRow_SanitisesUnsafeCharactersInTheSummary(t *testing.T) {
	theme := kernel.NewTheme(kernel.ThemeNoColor, true, kernel.UnicodeGlyphs())
	st := newStyles(theme)
	now := time.Date(2025, time.March, 5, 9, 0, 0, 0, time.UTC)
	lay := planLayout(100, 8)

	iss := jira.Issue{
		Key:     "PROJ-42",
		Summary: "before\tafter\x1b[31mred\x1b[0m\u202eevil",
		Status:  jira.Status{Name: "Triage", Category: jira.CategoryToDo},
	}
	got := ansi.Strip(renderRow(&iss, lay, false, st, theme, time.UTC, now, widget.Zoner{}))
	golden(t, "row_sanitised.golden", got+"\n")

	if w := ansi.StringWidth(got); w != lay.width {
		t.Errorf("a sanitised row is %d columns, want %d", w, lay.width)
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

// The icon is not a fallback for a name that does not fit: a short name gets
// it too, in front. Both cells, so the rule is one rule.
func TestRenderRow_TheIconPrecedesANameThatFits(t *testing.T) {
	g := kernel.UnicodeGlyphs()
	iss := jira.Issue{
		Key: "PROJ-7", Summary: "short names",
		Type:   jira.IssueType{Name: "Bug", AvatarID: "10303"},
		Status: jira.Status{Name: "Done", Category: jira.CategoryDone},
	}
	theme := kernel.NewTheme(kernel.ThemeNoColor, true, g)
	got := ansi.Strip(renderRow(&iss, planLayout(140, 8), false, newStyles(theme), theme, time.UTC,
		time.Date(2025, time.March, 5, 9, 0, 0, 0, time.UTC), widget.Zoner{}))
	for _, want := range []string{g.TypeBug + " Bug", g.CategoryDone + " Done"} {
		if !strings.Contains(got, want) {
			t.Errorf("the row lacks %q — the icon was only ever drawn where the name did not fit:\n%s", want, got)
		}
	}
}

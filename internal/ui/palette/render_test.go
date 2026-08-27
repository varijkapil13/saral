package palette

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

func TestView_Golden(t *testing.T) {
	t.Parallel()

	sizes := []struct {
		name string
		w, h int
	}{
		{name: "120x28", w: 120, h: 28},
		{name: "100x22", w: 100, h: 22},
		{name: "80x18", w: 80, h: 18},
	}
	for _, size := range sizes {
		t.Run(size.name, func(t *testing.T) {
			t.Parallel()

			p := fly(t, paletteDeps(), sample(), memoryTable(), size.w, size.h)
			golden(t, "palette_"+size.name+".golden", p.frame())
		})
	}
}

func TestView_GoldenWithAFilterTyped(t *testing.T) {
	t.Parallel()

	p := fly(t, paletteDeps(), sample(), memoryTable(), 120, 28)
	p.typeText("iss")
	golden(t, "palette_filter_120x28.golden", p.frame())
}

// The two halves of the list in one frame: the commands the build registered,
// then the issues already on disk, each with the age of the copy it came from.
func TestView_GoldenWithCachedIssues(t *testing.T) {
	t.Parallel()

	d, _ := cachedDeps()
	p := fly(t, d, sample(), memoryTable(), 120, 28)
	p.typeText("r")
	golden(t, "palette_issues_120x28.golden", p.frame())
}

func TestView_GoldenWithACachedIssueUnderTheCursor(t *testing.T) {
	t.Parallel()

	d, _ := cachedDeps()
	p := fly(t, d, sample(), memoryTable(), 100, 22)
	p.typeText("login")
	golden(t, "palette_issue_selected_100x22.golden", p.frame())
}

func TestView_GoldenWhenTheOnlyMatchIsOneThisSiteRefuses(t *testing.T) {
	t.Parallel()

	p := fly(t, paletteDeps(), sample(), memoryTable(), 120, 28)
	p.typeText("move")
	golden(t, "palette_refused_120x28.golden", p.frame())
}

func TestView_GoldenWithNothingRegistered(t *testing.T) {
	t.Parallel()

	p := fly(t, paletteDeps(), nil, memoryTable(), 120, 28)
	golden(t, "palette_empty_120x28.golden", p.frame())
}

func TestRenderRow_IsExactlyAsWideAsTheLayoutWhateverTheContent(t *testing.T) {
	t.Parallel()

	theme := kernel.NewTheme(kernel.ThemeNoColor, true, kernel.UnicodeGlyphs())
	st := newStyles(theme)

	commands := map[string]kernel.Command{
		"a plain one":        {ID: "issue.edit", Title: "Edit this issue", Group: "Issue", Keys: []string{"e"}},
		"nothing at all":     {ID: "x"},
		"a very long title":  {ID: "long", Title: strings.Repeat("a long command title ", 8), Group: "Something long as well", Keys: []string{"ctrl+shift+x"}},
		"wide graphemes":     {ID: "cjk", Title: "修正 the 日本語 layout", Group: "進行中", Keys: []string{"日"}},
		"an emoji and a ZWJ": {ID: "emoji", Title: "🚀 ship it 👩‍💻 today", Group: "Release", Keys: []string{"s"}},
	}

	for _, width := range []int{80, 100, 120, 200} {
		for name, cmd := range commands {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				r := row{cmd: cmd, keys: strings.Join(cmd.Keys, " / ")}
				lay := planLayout(width, widestKey([]row{r}))
				for _, sel := range []bool{false, true} {
					if got := ansi.StringWidth(renderRow(&r, lay, sel, st, theme)); got != lay.width {
						t.Errorf("the row is %d columns at width %d (selected=%t), want %d", got, width, sel, lay.width)
					}
				}
			})
		}
	}
}

func TestRenderHit_IsExactlyAsWideAsTheLayoutWhateverTheContent(t *testing.T) {
	t.Parallel()

	theme := kernel.NewTheme(kernel.ThemeNoColor, true, kernel.UnicodeGlyphs())
	st := newStyles(theme)

	hits := map[string]hit{
		"a plain one":           newHit(app.Hit{Key: "PROJ-142", Summary: "Fix the login flow", HasSummary: true, StoredAt: clockAt.Add(-time.Hour)}, clockAt),
		"no title stored":       newHit(app.Hit{Key: "PROJ-9", StoredAt: clockAt.Add(-90 * time.Hour)}, clockAt),
		"a title that is empty": newHit(app.Hit{Key: "PROJ-9", HasSummary: true, StoredAt: clockAt}, clockAt),
		"no stored time at all": newHit(app.Hit{Key: "PROJ-1", Summary: "x", HasSummary: true}, clockAt),
		"a very long title":     newHit(app.Hit{Key: "PROJ-4242424242", Summary: strings.Repeat("a long issue summary ", 8), HasSummary: true, StoredAt: clockAt.Add(-3 * time.Minute)}, clockAt),
		"wide graphemes":        newHit(app.Hit{Key: "会議-7", Summary: "会議のサポート体制", HasSummary: true, StoredAt: clockAt.Add(-2 * time.Second)}, clockAt),
		"an emoji and a ZWJ":    newHit(app.Hit{Key: "PROJ-8", Summary: "🚀 ship it 👩‍💻 today", HasSummary: true, StoredAt: clockAt.Add(-25 * time.Hour)}, clockAt),
	}

	for _, width := range []int{80, 100, 120, 200} {
		for name, h := range hits {
			t.Run(name, func(t *testing.T) {
				t.Parallel()

				lay := planLayout(width, 4)
				for _, sel := range []bool{false, true} {
					if got := ansi.StringWidth(renderHit(&h, lay, sel, st, theme)); got != lay.width {
						t.Errorf("the row is %d columns at width %d (selected=%t), want %d", got, width, sel, lay.width)
					}
				}
			})
		}
	}
}

func TestPlanLayout_GivesUpTheGroupBeforeTheTitle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		width     int
		keyWidth  int
		wantGroup bool
		wantKeys  bool
	}{
		{name: "a wide terminal keeps everything", width: 160, keyWidth: 4, wantGroup: true, wantKeys: true},
		{name: "the narrowest terminal Saral draws in keeps everything", width: 80, keyWidth: 4, wantGroup: true, wantKeys: true},
		{name: "a build where no command has a key drops the column", width: 120, keyWidth: 0, wantGroup: true},
		{name: "half a terminal gives up the group", width: 44, keyWidth: 4, wantKeys: true},
		{name: "a sliver keeps only the title", width: 24, keyWidth: 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			lay := planLayout(tt.width, tt.keyWidth)
			if got := lay.group > 0; got != tt.wantGroup {
				t.Errorf("at %d columns the group column is %t, want %t", tt.width, got, tt.wantGroup)
			}
			if got := lay.keys > 0; got != tt.wantKeys {
				t.Errorf("at %d columns the key column is %t, want %t", tt.width, got, tt.wantKeys)
			}
			if lay.title < 1 {
				t.Errorf("at %d columns the title has no room at all", tt.width)
			}
			if got := marker + lay.title + optionalWidth(lay) + lay.slack; got != lay.width {
				t.Errorf("the columns add up to %d at width %d", got, lay.width)
			}
		})
	}
}

func TestRowMemo_ForgetsARowWhoseSelectionOrThemeMoved(t *testing.T) {
	t.Parallel()

	p := fly(t, paletteDeps(), sample(), memoryTable(), 120, 28)
	drawn := p.frame()
	held := len(p.m.memo.rows)
	if held == 0 {
		t.Fatal("drawing the palette memoized nothing")
	}
	if again := p.frame(); again != drawn || len(p.m.memo.rows) != held {
		t.Error("a second frame rebuilt rows nothing had changed")
	}

	p.press("down")
	_ = p.frame()
	if got := len(p.m.memo.rows); got <= held {
		t.Error("moving the selection hit the memo for both rows, so the arrow is drawn on the wrong one")
	}

	p.send(kernel.ThemeMsg{Theme: kernel.NewTheme(kernel.ThemeDark, true, kernel.UnicodeGlyphs())})
	if len(p.m.memo.rows) != 0 {
		t.Error("a theme change left the rows it was drawn in behind")
	}
}

func TestView_DrawsNothingBeforeItHasBeenGivenASize(t *testing.T) {
	t.Parallel()

	m := build(paletteDeps(), sample(), memoryTable())
	if got := m.View(); got != "" {
		t.Errorf("an unsized palette drew %q", got)
	}
}

// allRefused is a build whose every command needs a capability this site does
// not allow. It is what a token with no permissions reaches, and it is the
// difference between "nothing registered" and "none you can run here".
func allRefused() []kernel.Command {
	run := func(kernel.Deps) tea.Cmd { return nil }
	return []kernel.Command{
		{
			ID: "issue.move", Title: "Move issues between projects", Group: "Issue",
			Requires: jira.CapBulkMove, Run: run,
		},
		{
			ID: "issue.move-one", Title: "Move this issue", Group: "Issue",
			Requires: jira.CapBulkMove, Run: run,
		},
	}
}

func TestCommandCount_TellsAnEmptyBuildApartFromOneWhoseCommandsAreAllRefused(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		key  headKey
		want string
	}{
		{name: "nothing registered", key: headKey{}, want: "nothing registered"},
		{
			name: "every command refused",
			key:  headKey{registered: 6},
			want: "none you can run here",
		},
		{
			name: "every command refused under a filter",
			key:  headKey{registered: 6, filtered: true},
			want: "none you can run here",
		},
		{
			name: "nothing registered under a filter",
			key:  headKey{filtered: true},
			want: "nothing registered",
		},
		{name: "one command", key: headKey{registered: 1, total: 1, shown: 1}, want: "1 command"},
		{name: "several", key: headKey{registered: 6, total: 6, shown: 6}, want: "6 commands"},
		{
			name: "a filter over what is allowed",
			key:  headKey{registered: 6, total: 4, shown: 2, filtered: true},
			want: "2 of 4",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := commandCount(tt.key); got != tt.want {
				t.Errorf("commandCount(%+v) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}

// Drawn from the palette rather than from a headKey built by hand: what the
// palette put in the key was the offered count, which every refusal takes one
// off.
func TestView_ABuildWhoseEveryCommandIsRefusedDoesNotClaimNothingIsRegistered(t *testing.T) {
	t.Parallel()

	p := fly(t, paletteDeps(), allRefused(), memoryTable(), 120, 28)
	frame := p.frame()
	mustContain(t, frame, "none you can run here", noBulkMove)
	mustNotContain(t, frame, "nothing registered", "Nothing has registered a command in this build.")
	// Nothing was typed, so nothing may be said about a query.
	mustNotContain(t, frame, "matches that")
}

// A filter over an all-refused build matches no command, so the reasons are not
// in m.refused either, and the empty state still owes an answer about the site
// rather than about the build.
func TestView_AFilterMatchingNothingInAnAllRefusedBuildStillSaysWhichNoneItIs(t *testing.T) {
	t.Parallel()

	p := fly(t, paletteDeps(), allRefused(), memoryTable(), 120, 28)
	p.typeText("zzzz")
	frame := p.frame()
	mustContain(t, frame, "none you can run here", "No command in this build can be run on this site.", noBulkMove)
	mustNotContain(t, frame, "nothing registered", "Nothing has registered a command in this build.")
}

func TestView_GoldenWithEveryCommandRefused(t *testing.T) {
	t.Parallel()

	p := fly(t, paletteDeps(), allRefused(), memoryTable(), 120, 28)
	golden(t, "palette_all_refused_120x28.golden", p.frame())
}

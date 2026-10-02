package search

import (
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

var zoneMark = regexp.MustCompile(`\x1b\[\d+z`)

func unicodeDeps(client jira.Client) kernel.Deps {
	d := testDeps(client)
	d.Theme = kernel.NewTheme(kernel.ThemeNoColor, true, kernel.UnicodeGlyphs())
	return d
}

func assigned(issues []jira.Issue) []jira.Issue {
	people := []string{"Ada Lovelace", "Grace Hopper", ""}
	for i := range issues {
		if name := people[i%len(people)]; name != "" {
			issues[i].Assignee = &jira.User{AccountID: "acct-" + name[:3], DisplayName: name}
		}
	}
	return issues
}

func variedStatuses(issues []jira.Issue) []jira.Issue {
	statuses := []jira.Status{
		{ID: "10200", Name: "To Do", Category: jira.CategoryToDo},
		{ID: "10201", Name: "In Progress", Category: jira.CategoryInProgress},
		{ID: "10203", Name: "Shipped", Category: jira.CategoryDone},
	}
	for i := range issues {
		issues[i].Status = statuses[i%len(statuses)]
	}
	return issues
}

func goldenSite() *jiratest.Fake {
	issues := variedStatuses(assigned(withKeyedIssues()))
	return newFake(issues)
}

func frameOf(dr *driver) string { return dr.view() + "\n" }

func TestRender_Goldens(t *testing.T) {
	t.Parallel()

	t.Run("idle", func(t *testing.T) {
		t.Parallel()
		dr := newDriver(t, unicodeDeps(goldenSite()), Seed{}, 120, 30, withAfter(immediately))
		golden(t, "idle_120x30.golden", frameOf(dr))
	})
	t.Run("idle without a project", func(t *testing.T) {
		t.Parallel()
		d := unicodeDeps(goldenSite())
		d.Project = ""
		dr := newDriver(t, d, Seed{}, 120, 30, withAfter(immediately))
		golden(t, "idle_no_project_120x30.golden", frameOf(dr))
	})
	t.Run("typing with results", func(t *testing.T) {
		t.Parallel()
		dr := newDriver(t, unicodeDeps(goldenSite()), Seed{}, 120, 30, withAfter(immediately))
		dr.typeText("login")
		golden(t, "typing_results_120x30.golden", frameOf(dr))
	})
	t.Run("browsing results", func(t *testing.T) {
		t.Parallel()
		dr := newDriver(t, unicodeDeps(goldenSite()), Seed{Query: "login"}, 120, 30, withAfter(immediately))
		dr.key("down", "j")
		golden(t, "browse_results_120x30.golden", frameOf(dr))
	})
	t.Run("browsing results at 80x24", func(t *testing.T) {
		t.Parallel()
		dr := newDriver(t, unicodeDeps(goldenSite()), Seed{Query: "login"}, 80, 24, withAfter(immediately))
		dr.key("down", "j")
		golden(t, "browse_results_80x24.golden", frameOf(dr))
	})
	t.Run("narrowed to the project", func(t *testing.T) {
		t.Parallel()
		dr := newDriver(t, unicodeDeps(goldenSite()), Seed{Query: "login"}, 120, 30, withAfter(immediately))
		dr.key("down", "tab")
		golden(t, "scope_project_120x30.golden", frameOf(dr))
	})
	t.Run("loading", func(t *testing.T) {
		t.Parallel()
		m := New(unicodeDeps(goldenSite()), Seed{Query: "login"}, withAfter(immediately))
		m.Update(kernel.SizeMsg{Width: 120, Height: 30})
		m.Update(kernel.FocusMsg{Focused: true})
		_ = m.Init()
		golden(t, "loading_120x30.golden", ansi.Strip(m.View())+"\n")
	})
	t.Run("no results", func(t *testing.T) {
		t.Parallel()
		dr := newDriver(t, unicodeDeps(goldenSite()), Seed{Query: "zebra"}, 120, 30, withAfter(immediately))
		golden(t, "noresults_120x30.golden", frameOf(dr))
	})
	for name, err := range map[string]error{
		"forbidden":   &jira.CapabilityError{Capability: jira.CapPlans, Reason: "this token cannot browse any project"},
		"ratelimited": &jira.RateLimitError{RetryAfter: 30 * time.Second},
		"transport":   &jira.TransportError{Op: "Search", Err: errors.New("dial tcp: lookup example.atlassian.net: no such host")},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := goldenSite()
			f.FailNext(err)
			dr := newDriver(t, unicodeDeps(f), Seed{Query: "login"}, 120, 30, withAfter(immediately))
			golden(t, name+"_120x30.golden", frameOf(dr))
		})
	}
	t.Run("a pinned key", func(t *testing.T) {
		t.Parallel()
		dr := newDriver(t, unicodeDeps(goldenSite()), Seed{Query: "PROJ-9"}, 120, 30, withAfter(immediately))
		dr.key("down")
		golden(t, "pinned_key_120x30.golden", frameOf(dr))
	})
	t.Run("the trailing row", func(t *testing.T) {
		t.Parallel()
		dr := newDriver(t, unicodeDeps(newFake(bulkIssues(300))), Seed{Query: "paging widget"}, 120, 30, withAfter(immediately))
		dr.key("down")
		for range 8 {
			dr.key("G")
		}
		golden(t, "more_row_120x30.golden", frameOf(dr))
	})
	t.Run("ascii glyphs", func(t *testing.T) {
		t.Parallel()
		dr := newDriver(t, testDeps(goldenSite()), Seed{Query: "login"}, 120, 30, withAfter(immediately))
		dr.key("down")
		golden(t, "ascii_glyphs_120x30.golden", frameOf(dr))
	})
}

func TestRender_HighlightsPaintTheTypedWordsAndSurviveSelection(t *testing.T) {
	t.Parallel()
	dr := newDriver(t, colourDeps(goldenSite()), Seed{Query: "login"}, 120, 30, withAfter(immediately))
	dr.key("down")

	st := dr.m.styles
	if st.hlOpen == "" || st.hlClose == "" {
		t.Fatal("the theme paints no highlight, so this proves nothing")
	}
	first := dr.m.row(1, false)
	if !strings.Contains(first, st.hlOpen) {
		t.Errorf("an unselected row carries no highlight: %q", first)
	}
	selected := dr.m.row(0, true)
	if !strings.Contains(selected, st.hlSelOpen+"Login"+st.hlSelClose) {
		t.Errorf("the selected row's highlight is not drawn over the selection style: %q", selected)
	}
	plain := ansi.Strip(selected)
	if width := ansi.StringWidth(plain); width != 120 {
		t.Errorf("the selected row is %d columns wide, want 120", width)
	}

	visible := func(s string) string { return strings.ReplaceAll(zoneMark.ReplaceAllString(s, ""), "\x1b", "<ESC>") }
	golden(t, "highlight_selected_120.golden", strings.Join([]string{
		"row 0, selected:", visible(selected), "row 1, not selected:", visible(first),
	}, "\n")+"\n")
}

func TestHighlights_WordStartsCaseInsensitivelyAndSafelyOnMultibyte(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, summary string
		words         []string
		want          []string
	}{
		{"the start of a word, any case", "Login LOGINS blogin", []string{"login"}, []string{"Login", "LOGIN"}},
		{"a prefix is painted as far as it was typed", "timeout handling", []string{"time"}, []string{"time"}},
		{"nothing mid-word", "overtime", []string{"time"}, nil},
		{"capital dotted I", "İstanbul office", []string{"i̇stanbul"}, nil},
		{"the dotted capital against its lowercase", "İstanbul office", []string{"istanbul"}, nil},
		{"a straße", "Straße address", []string{"stra"}, []string{"Stra"}},
		{"after an emoji", "🚀 launch plan", []string{"launch"}, []string{"launch"}},
		{"more than one word", "login after logout", []string{"login", "logout"}, []string{"login", "logout"}},
		{"a key", "PROJ-9 crashes", []string{"proj", "9"}, []string{"PROJ", "9"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var got []string
			for _, sp := range highlights(tc.summary, tc.words) {
				if sp.from < 0 || sp.to > len(tc.summary) || sp.from >= sp.to {
					t.Fatalf("span %v is outside %q", sp, tc.summary)
				}
				part := tc.summary[sp.from:sp.to]
				if !utf8.ValidString(part) {
					t.Fatalf("span %v cuts a character of %q in half", sp, tc.summary)
				}
				got = append(got, part)
			}
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("painted %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRender_EveryRowIsExactlyAsWideAsTheBox(t *testing.T) {
	t.Parallel()
	for _, w := range []int{80, 84, 90, 99, 100, 120, 200} {
		for _, glyphs := range []kernel.Glyphs{kernel.ASCIIGlyphs(), kernel.UnicodeGlyphs(), kernel.NerdGlyphs()} {
			d := testDeps(goldenSite())
			d.Theme = kernel.NewTheme(kernel.ThemeNoColor, true, glyphs)
			dr := newDriver(t, d, Seed{Query: "login"}, w, 24, withAfter(immediately))
			dr.key("down")
			for i, line := range strings.Split(dr.view(), "\n") {
				if line == "" {
					continue
				}
				if got := ansi.StringWidth(line); got != w {
					t.Errorf("%s glyphs at %d wide: line %d is %d columns:\n%q", glyphs.Tier(), w, i, got, line)
				}
			}
		}
	}
}

func TestRender_NarrowTerminalsDropAssigneeThenStatus(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		width            int
		status, assignee bool
	}{{80, false, false}, {90, true, false}, {120, true, true}} {
		dr := newDriver(t, unicodeDeps(goldenSite()), Seed{Query: "login"}, tc.width, 24, withAfter(immediately))
		out := dr.view()
		if got := strings.Contains(out, "In Progress") || strings.Contains(out, "To Do"); got != tc.status {
			t.Errorf("at %d wide the status column shown is %v, want %v", tc.width, got, tc.status)
		}
		if got := strings.Contains(out, "Ada Lovelace") || strings.Contains(out, "Grace Hopper"); got != tc.assignee {
			t.Errorf("at %d wide the assignee column shown is %v, want %v", tc.width, got, tc.assignee)
		}
	}
}

func TestRender_AgeSaysHowLongAgoInFourColumnsOrFewer(t *testing.T) {
	t.Parallel()
	for in, want := range map[time.Duration]string{
		10 * time.Second: "now", 12 * time.Minute: "12m", 5 * time.Hour: "5h",
		3 * 24 * time.Hour: "3d", 20 * 24 * time.Hour: "2w", 90 * 24 * time.Hour: "3mo", 800 * 24 * time.Hour: "2y",
	} {
		if got := age(testNow, testNow.Add(-in)); got != want || len(got) > ageWidth {
			t.Errorf("%s ago reads %q, want %q", in, got, want)
		}
	}
}

func TestRender_ATypedWordIsNeverTakenForEscapeSequences(t *testing.T) {
	t.Parallel()
	issues := []jira.Issue{issueOf("PROJ-1", "Login \x1b[31mred\x1b[0m screen\ttab", 1)}
	dr := newDriver(t, unicodeDeps(newFake(issues)), Seed{Query: "login"}, 120, 24, withAfter(immediately))
	if strings.Contains(dr.m.View(), "\x1b[31m") {
		t.Error("an escape sequence in a summary reached the frame")
	}
}

func TestSearch_QueryMsgFillsTheBoxAndRuns(t *testing.T) {
	t.Parallel()
	f := goldenSite()
	dr := settled(t, f, Seed{}, 120, 30)
	dr.send(QueryMsg{Text: "straße"})
	if got := dr.keys(); len(got) != 1 || got[0] != "PROJ-4" {
		t.Errorf("rows %v", got)
	}
	if dr.m.input.Value() != "straße" {
		t.Errorf("the box holds %q", dr.m.input.Value())
	}
}

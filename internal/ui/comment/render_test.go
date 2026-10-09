package comment

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
)

func TestVisibilityLabel_SaysWhoCanReadARestrictedComment(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		in   *jira.Visibility
		want string
	}{
		{name: "nothing restricting it", in: nil, want: ""},
		{name: "a role", in: &jira.Visibility{Type: "role", Value: "Developers"}, want: "only the Developers role"},
		{name: "a group", in: &jira.Visibility{Type: "group", Value: "jira-developers"}, want: "only the jira-developers group"},
		{name: "a restriction with no name", in: &jira.Visibility{Type: "role"}, want: "only one role"},
		{name: "a name with no kind", in: &jira.Visibility{Value: "Developers"}, want: "only Developers"},
		{name: "neither", in: &jira.Visibility{}, want: "restricted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := visibilityLabel(tc.in); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestList_ReadsAsASentence(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		in   []string
		want string
	}{
		{in: nil, want: ""},
		{in: []string{"mention"}, want: "mention"},
		{in: []string{"mention", "table"}, want: "mention and table"},
		{in: []string{"mention", "status", "table"}, want: "mention, status and table"},
	} {
		if got := list(tc.in); got != tc.want {
			t.Errorf("list(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The confirmation quotes the comment back so that the person who wrote it
// recognises it. The words come from the display renderer, so a heading is not
// quoted with its hashes on, and they arrive on one line however many the
// comment has.
func TestPromptParts_QuoteTheOpeningWordsOnOneLineWithNoMarkdownInThem(t *testing.T) {
	t.Parallel()

	m := build(testDeps(t, nil), "PROJ-1")
	m.width = 120
	body := adf.NewDoc(
		adf.NewNode("heading", adf.NewText("The first line of it")).WithAttrs(adf.Attrs{"level": 2}),
		adf.NewNode("paragraph", adf.NewText("The second, which nobody needs in a confirmation.")),
	)
	parts := m.promptParts(&jira.Comment{Body: body})
	if len(parts) == 0 {
		t.Fatal("the confirmation quotes nothing back")
	}
	quote := parts[len(parts)-1]
	if !strings.Contains(quote, "The first line of it") {
		t.Errorf("the confirmation quotes %q, want it to open with the comment", quote)
	}
	for _, unwanted := range []string{"#", "\n"} {
		if strings.Contains(quote, unwanted) {
			t.Errorf("the confirmation quotes %q, which carries %q", quote, unwanted)
		}
	}
	if got := ansi.StringWidth(quote); got > promptWords+4 {
		t.Errorf("the confirmation quotes %d cells, want no more than %d and its punctuation",
			got, promptWords)
	}
	if got := m.promptParts(&jira.Comment{}); len(got) != 0 {
		t.Errorf("a comment with nothing in it and no dates contributed %v", got)
	}
}

// The composer's height is the one layout rule, and it holds at every box a
// sidebar or a screen can be.
func TestComposerHeight_GrowsWithTheDraftAndNeverPastHalfTheBox(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name        string
		lines, boxH int
		want        int
	}{
		{name: "an empty draft in a tall box keeps its floor", lines: 1, boxH: 40, want: 3},
		{name: "two lines still fit the floor", lines: 2, boxH: 40, want: 3},
		{name: "a draft past the floor takes the lines it needs", lines: 6, boxH: 40, want: 7},
		{name: "a long draft stops at half the box", lines: 40, boxH: 40, want: 20},
		{name: "half of a short box is still half of it", lines: 40, boxH: 8, want: 4},
		{name: "a box too short for half of it keeps the floor", lines: 1, boxH: 4, want: 3},
		{name: "a box of one line cannot go below the floor here", lines: 1, boxH: 1, want: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := composerHeight(tc.lines, tc.boxH); got != tc.want {
				t.Errorf("composerHeight(%d, %d) = %d, want %d", tc.lines, tc.boxH, got, tc.want)
			}
		})
	}
}

// A line wider than the box is cut, and never silently: the marker is what says
// the line goes on, and the pan is what reaches the rest of it.
func TestWindow_SaysWhereALineWasCut(t *testing.T) {
	t.Parallel()

	line := "0123456789abcdefghij"
	for _, tc := range []struct {
		name string
		from int
		want string
	}{
		{name: "the left edge", from: 0, want: "012345678~"},
		{name: "panned past the start", from: 5, want: "~6789abcd~"},
		{name: "panned to the end", from: 10, want: "~bcdefghij"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := window(line, tc.from, 10, "~"); got != tc.want {
				t.Errorf("window(%q, %d, 10) = %q, want %q", line, tc.from, got, tc.want)
			}
		})
	}
	if got := window("short", 0, 10, "~"); got != "short     " {
		t.Errorf("a line that fits came back as %q, want it padded to the box", got)
	}
}

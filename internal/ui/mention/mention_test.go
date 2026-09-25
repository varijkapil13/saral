package mention

import (
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// text is an Area with the cursor at the end of its last line.
type text string

func (t text) Value() string { return string(t) }

func (t text) Line() int { return strings.Count(string(t), "\n") }

func (t text) Column() int {
	s := string(t)
	return utf8.RuneCountInString(s[strings.LastIndexByte(s, '\n')+1:])
}

func rightAway(_ time.Duration, fn func() tea.Msg) tea.Cmd { return fn }

func newFake() *jiratest.Fake { return jiratest.New(jiratest.WithProject("PROJ", jiratest.Scrum)) }

// run delivers a command's answer back to the state until nothing is left.
func run(t *testing.T, s *State, cmd tea.Cmd, search Search) {
	t.Helper()
	for range 10 {
		if cmd == nil {
			return
		}
		msg := cmd()
		next, handled := s.Update(msg, search)
		if !handled {
			t.Fatalf("the state did not take its own %T", msg)
		}
		cmd = next
	}
	t.Fatal("commands never settled")
}

func TestToken(t *testing.T) {
	t.Parallel()

	cases := []struct {
		text  string
		query string
		ok    bool
	}{
		{"@", "", true},
		{"hi @gr", "gr", true},
		{"(@ad", "ad", true},
		{"line one\n@al", "al", true},
		{"@al\nline two", "", false},
		{"mail me@example", "", false},
		{"done @[Ada](accountid:x)", "", false},
		{"@ada lovelace", "", false},
		{"no at here", "", false},
		{"@" + strings.Repeat("x", maxQuery+2), "", false},
	}
	for _, tc := range cases {
		_, _, q, ok := token(text(tc.text))
		if ok != tc.ok || q != tc.query {
			t.Errorf("token(%q) = %q, %v; want %q, %v", tc.text, q, ok, tc.query, tc.ok)
		}
	}
}

func TestMention_TypingPausesThenOffersWhoMatches(t *testing.T) {
	t.Parallel()

	f := newFake()
	var s State
	run(t, &s, s.Track(text("thanks @gr"), rightAway), Search{Finder: f, Project: "PROJ"})
	if !s.Open() || len(s.people) != 1 || s.people[0].AccountID != "acct-grace" {
		t.Fatalf("suggestions = %+v, want the one account matching gr", s.people)
	}
	lines := s.Lines(40, Look{Arrow: ">", Ellipsis: "~"})
	if len(lines) != s.Height() || !strings.Contains(lines[0], "> @Grace Hopper") {
		t.Fatalf("lines = %q", lines)
	}
}

func TestMention_OnlyTheLastQueryOfABurstIsAsked(t *testing.T) {
	t.Parallel()

	f := newFake()
	var timers []tea.Cmd
	hold := func(_ time.Duration, fn func() tea.Msg) tea.Cmd {
		timers = append(timers, fn)
		return fn
	}
	var s State
	for _, typed := range []string{"@a", "@ad", "@ada"} {
		s.Track(text(typed), hold)
	}
	for _, timer := range timers {
		run(t, &s, timer, Search{Finder: f, Project: "PROJ"})
	}
	calls := 0
	for _, c := range f.Calls() {
		if c == "FindPeople" {
			calls++
		}
	}
	if calls != 1 || s.asked != "ada" {
		t.Fatalf("FindPeople ran %d times, last asked %q; want once, for ada", calls, s.asked)
	}
}

func TestMention_AFailedSearchSaysWhy(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
	}{
		{"a capability refusal", &jira.CapabilityError{Reason: "needs Browse users and groups"}},
		{"a rate limit", &jira.RateLimitError{RetryAfter: time.Second}},
		{"a transport failure", &jira.TransportError{Op: "GET user search", Err: errors.New("connection reset")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFake()
			f.FailNext(tc.err)
			var s State
			run(t, &s, s.Track(text("@ad"), rightAway), Search{Finder: f, Project: "PROJ"})
			if s.fail == "" || len(s.people) != 0 {
				t.Fatalf("fail = %q, people = %v; want the reason and nothing offered", s.fail, s.people)
			}
			if s.Key(tea.KeyPressMsg{Code: tea.KeyEnter}, nil) {
				t.Error("enter was swallowed with nothing to insert")
			}
			if got := s.Lines(60, Look{}); len(got) != 1 || strings.TrimSpace(got[0]) != s.fail {
				t.Errorf("lines = %q", got)
			}
		})
	}
}

func TestMention_BlockedSearchAsksNothing(t *testing.T) {
	t.Parallel()

	f := newFake()
	var s State
	run(t, &s, s.Track(text("@ad"), rightAway), Search{Finder: f, Blocked: "this token cannot look accounts up"})
	if s.fail != "this token cannot look accounts up" {
		t.Fatalf("fail = %q", s.fail)
	}
	if len(f.Calls()) != 0 {
		t.Fatalf("a blocked search reached the site: %v", f.Calls())
	}
}

func TestMention_EscDismissesUntilTheNextAt(t *testing.T) {
	t.Parallel()

	var s State
	s.Track(text("@ad"), rightAway)
	if !s.Key(tea.KeyPressMsg{Code: tea.KeyEscape}, nil) || s.Open() {
		t.Fatal("esc did not put the list away")
	}
	s.Track(text("@ada"), rightAway)
	if s.Open() {
		t.Error("the dismissed @ reopened on the next keystroke")
	}
	s.Track(text("@ada @g"), rightAway)
	if !s.Open() {
		t.Error("a new @ did not open the list")
	}
}

func TestMention_StaleAndForeignAnswersAreIgnored(t *testing.T) {
	t.Parallel()

	var a, b State
	due := a.Track(text("@ad"), rightAway)
	b.Track(text("@ad"), rightAway)
	msg := due()
	if _, handled := b.Update(msg, Search{}); handled {
		t.Fatal("one textarea took another's timer")
	}
	a.Track(text("@ada"), rightAway)
	if cmd, handled := a.Update(msg, Search{Finder: newFake()}); !handled || cmd != nil {
		t.Fatal("a timer from an older query asked the site")
	}
}

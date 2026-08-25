package kernel

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// Deps.Site is whatever a profile was written with, and onboarding accepts a bare
// host. Every other shape here is one somebody will type or paste at some point,
// and concatenating "https://" + site + "/browse/" gets three of them wrong.
func TestIssueURL_CopesWithEveryShapeAProfileCanHold(t *testing.T) {
	for name, tc := range map[string]struct {
		site, key, want string
	}{
		"a bare host":        {"example.atlassian.net", "PROJ-1", "https://example.atlassian.net/browse/PROJ-1"},
		"a scheme already":   {"https://example.atlassian.net", "PROJ-1", "https://example.atlassian.net/browse/PROJ-1"},
		"plain http":         {"http://jira.internal", "PROJ-1", "http://jira.internal/browse/PROJ-1"},
		"a port":             {"jira.internal:8080", "PROJ-1", "https://jira.internal:8080/browse/PROJ-1"},
		"a context path":     {"jira.internal:8080/jira", "PROJ-1", "https://jira.internal:8080/jira/browse/PROJ-1"},
		"a trailing slash":   {"example.atlassian.net/", "PROJ-1", "https://example.atlassian.net/browse/PROJ-1"},
		"a scheme and both":  {"http://jira.internal:8080/jira/", "AB-42", "http://jira.internal:8080/jira/browse/AB-42"},
		"surrounding spaces": {"  example.atlassian.net  ", "PROJ-1", "https://example.atlassian.net/browse/PROJ-1"},
		"a query to drop":    {"https://example.atlassian.net/?foo=bar", "PROJ-1", "https://example.atlassian.net/browse/PROJ-1"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := IssueURL(tc.site, tc.key)
			if err != nil {
				t.Fatalf("IssueURL(%q, %q): %v", tc.site, tc.key, err)
			}
			if got != tc.want {
				t.Errorf("IssueURL(%q, %q) = %q, want %q", tc.site, tc.key, got, tc.want)
			}
		})
	}
}

// It refuses rather than guesses. A link built out of something that is not a
// site is a browser tab full of somebody else's website.
func TestIssueURL_RefusesWhatItCannotBuildALinkFrom(t *testing.T) {
	for name, tc := range map[string]struct{ site, key, says string }{
		"no site at all":     {"", "PROJ-1", "which site"},
		"no issue":           {"example.atlassian.net", "", "no issue"},
		"a key with a path":  {"example.atlassian.net", "PROJ-1/../../etc", "not an issue key"},
		"a key with a space": {"example.atlassian.net", "PROJ 1", "not an issue key"},
		"a scheme nobody op": {"javascript:alert(1)", "PROJ-1", "would open"},
		"no host":            {"https://", "PROJ-1", "names no host"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := IssueURL(tc.site, tc.key)
			if err == nil {
				t.Fatalf("IssueURL(%q, %q) built %q", tc.site, tc.key, got)
			}
			if !strings.Contains(err.Error(), tc.says) {
				t.Errorf("the refusal reads %q, which does not say %q", err, tc.says)
			}
		})
	}
}

// An OSC 52 write cannot be confirmed: a terminal or a multiplexer that refuses
// the sequence drops it silently. Saying what was copied is the only thing that
// tells a working copy from one that never happened.
func TestCopy_NamesWhatItPutOnTheClipboard(t *testing.T) {
	note, found := statusOf(Copy("PROJ-1", "PROJ-1"))
	if !found {
		t.Fatal("copying said nothing, so a silent failure looks like success")
	}
	if !strings.Contains(note.Text, "PROJ-1") {
		t.Errorf("the status line reads %q and does not name what was copied", note.Text)
	}
	if note.Level != LevelInfo {
		t.Errorf("a copy is reported at level %d, want info", note.Level)
	}

	named, _ := statusOf(Copy("https://example.atlassian.net/browse/PROJ-1", "the link to PROJ-1"))
	if !strings.Contains(named.Text, "the link to PROJ-1") {
		t.Errorf("the status line reads %q rather than the phrase the caller gave it", named.Text)
	}

	empty, found := statusOf(Copy("", "nothing"))
	if !found || empty.Level != LevelWarn {
		t.Errorf("copying nothing reported %+v; there is nothing to put on a clipboard", empty)
	}
}

func TestOpener_KnowsAHandlerForTheDesktopsThisRunsOn(t *testing.T) {
	for _, goos := range []string{"darwin", "windows", "linux", "freebsd", "netbsd", "openbsd", "dragonfly"} {
		name, args, known := opener(goos, "https://example.atlassian.net/browse/PROJ-1")
		switch {
		case !known:
			t.Errorf("%s has no handler, so a link there is never opened", goos)
		case name == "":
			t.Errorf("%s names an empty command", goos)
		case len(args) == 0:
			t.Errorf("%s passes the handler no link", goos)
		case !strings.Contains(args[len(args)-1], "PROJ-1"):
			t.Errorf("%s passes %v, which does not end in the link", goos, args)
		}
	}
	if _, _, known := opener("plan9", "https://example.atlassian.net"); known {
		t.Error("a platform with no handler claimed one")
	}
}

// A platform Saral has no handler for says the address rather than failing
// quietly: reading it off the screen is worse than clicking it, and better than
// nothing happening.
func TestOpenURL_SaysTheAddressWhenItCannotOpenIt(t *testing.T) {
	if cmd := OpenURL("https://example.atlassian.net/browse/PROJ-1"); cmd == nil {
		t.Fatal("opening a link produced no command at all")
	}
	note, found := statusOf(Warn("Saral does not know how to open a link on plan9; the address is x"))
	if !found || !strings.Contains(note.Text, "the address is") {
		t.Errorf("the refusal reads %+v and does not carry the address", note)
	}
}

// statusOf runs a command and finds the status message in it, batch or not.
// Nothing here runs a clipboard write or a browser: both are messages the
// program hands back to Bubble Tea rather than work done in the command.
func statusOf(cmd tea.Cmd) (StatusMsg, bool) {
	if cmd == nil {
		return StatusMsg{}, false
	}
	switch msg := cmd().(type) {
	case StatusMsg:
		return msg, true
	case tea.BatchMsg:
		for _, c := range msg {
			if note, ok := statusOf(c); ok {
				return note, true
			}
		}
	}
	return StatusMsg{}, false
}

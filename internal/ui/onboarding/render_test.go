package onboarding

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// goldenPath stands in for the XDG path so that a golden file does not carry a
// temporary directory in it.
const goldenPath = "/home/you/.config/saral/config.toml"

func goldenDriver(t *testing.T) *driver {
	t.Helper()
	return goldenDriverWith(t, jiratest.NoBulkMove, jiratest.NoPlans)
}

func goldenDriverWith(t *testing.T, mods ...jiratest.CapMod) *driver {
	t.Helper()
	d := newDriver(t, testFake(jiratest.WithCapabilities(mods...)))
	d.send(configLoadedMsg{path: goldenPath})
	return d
}

func TestView_Golden(t *testing.T) {
	t.Parallel()

	screens := map[string]func(*driver){
		"site":  func(*driver) {},
		"email": func(d *driver) { d.typeIn(testSite); d.press("enter") },
		"token": func(d *driver) {
			d.typeIn(testSite)
			d.press("enter")
			d.typeIn(testEmail)
			d.press("enter")
			d.typeIn(testToken)
		},
		"store": func(d *driver) { d.credentials() },
		"store-command": func(d *driver) {
			d.credentials()
			d.press("down", "down")
			d.typeIn("pass show jira")
		},
		"project": func(d *driver) { d.credentials(); d.press("enter") },
		"review": func(d *driver) {
			d.credentials()
			d.press("enter")
			d.typeIn("PROJ")
			d.press("enter")
		},
		"done": func(d *driver) {
			d.credentials()
			d.press("enter")
			d.typeIn("PROJ")
			d.press("enter")
			d.send(savedMsg{seq: d.model().seq, path: goldenPath, stored: "keychain entry saral:example"})
		},
		"rejected": func(d *driver) {
			d.model()
			d.typeIn(testSite)
			d.press("enter")
			d.typeIn(testEmail)
			d.press("enter")
			d.typeIn(testToken)
			d.send(connectFailedMsg{seq: d.model().seq, err: &jira.AuthError{Reason: "the token has been revoked"}})
		},
	}

	sizes := map[string][2]int{"120x40": {120, 40}, "80x20": {80, 20}}

	for name, build := range screens {
		for size, wh := range sizes {
			t.Run(name+"_"+size, func(t *testing.T) {
				t.Parallel()

				d := goldenDriver(t)
				d.send(kernel.SizeMsg{Width: wh[0], Height: wh[1]})
				build(d)
				golden(t, name+"_"+size+".golden", d.frame())
			})
		}
	}
}

func TestView_FitsTheBoxItIsGiven(t *testing.T) {
	t.Parallel()

	for _, wh := range [][2]int{{80, 20}, {100, 24}, {120, 40}, {200, 60}} {
		d := goldenDriver(t)
		d.send(kernel.SizeMsg{Width: wh[0], Height: wh[1]})
		d.forget()
		d.credentials()
		d.press("enter")
		d.typeIn("PROJ")
		d.press("enter")
		d.send(savedMsg{seq: d.model().seq, path: goldenPath, warning: strings.Repeat("a very long warning ", 12)})

		for _, frame := range d.frames {
			lines := strings.Split(frame, "\n")
			if len(lines) > wh[1] {
				t.Errorf("at %dx%d a frame is %d rows:\n%s", wh[0], wh[1], len(lines), frame)
			}
			for i, line := range lines {
				if w := lipgloss.Width(line); w > wh[0] {
					t.Errorf("at %dx%d row %d is %d columns wide:\n%s", wh[0], wh[1], i, w, line)
				}
			}
		}
	}
}

// TestView_TheSummaryScrollsWhenItDoesNotFit covers the one screen that can be
// taller than the terminal: a probe answers with as many sentences as it has
// refusals, and none of them may be unreachable.
func TestView_TheSummaryScrollsWhenItDoesNotFit(t *testing.T) {
	t.Parallel()

	d := goldenDriver(t)
	d.send(kernel.SizeMsg{Width: 80, Height: 20})
	d.credentials()
	d.press("enter")
	d.typeIn("PROJ")
	d.press("enter")
	d.atStep(stepReview)

	if strings.Contains(d.frame(), "Images") {
		t.Fatalf("the summary already fits, so this test proves nothing:\n%s", d.frame())
	}
	d.press("down", "down", "down")
	if !strings.Contains(d.frame(), "Images") {
		t.Errorf("scrolling did not reach the end of the summary:\n%s", d.frame())
	}

	d.send(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if !strings.Contains(d.frame(), "Site           "+testSite) {
		t.Errorf("the wheel did not scroll the summary back to the top:\n%s", d.frame())
	}
}

// TestView_SaysWhyTheDatesAreNotInTheAccountsZone covers the one line in the
// program that names a timezone. Every date renders in UTC when the probe could
// not establish the account's zone, and this row is the only place that can say
// so — without it a user in Berlin reads timestamps an hour out and nothing on
// screen accounts for it.
func TestView_SaysWhyTheDatesAreNotInTheAccountsZone(t *testing.T) {
	t.Parallel()

	const why = "Jira did not answer what timezone this account is in"

	unknown := goldenDriverWith(t, jiratest.NoBulkMove, jiratest.NoPlans, jiratest.NoTimeZone)
	unknown.send(kernel.SizeMsg{Width: 120, Height: 40})
	unknown.credentials()
	unknown.press("enter")
	unknown.typeIn("PROJ")
	unknown.press("enter")
	unknown.atStep(stepReview)
	unknown.mustContain("Dates in       UTC · " + why)
	golden(t, "review-unknown-zone_120x40.golden", unknown.frame())

	// The zone the account is really in explains nothing, so it says nothing.
	known := goldenDriver(t)
	known.send(kernel.SizeMsg{Width: 120, Height: 40})
	known.credentials()
	known.press("enter")
	known.typeIn("PROJ")
	known.press("enter")
	known.atStep(stepReview)
	if strings.Contains(known.frame(), why) {
		t.Errorf("the summary explains a timezone that needs no explaining:\n%s", known.frame())
	}
	known.mustContain("Dates in       UTC\n")
}

func TestView_DrawsNothingBeforeItHasBeenGivenASize(t *testing.T) {
	t.Parallel()

	if got := NewWith(testDeps(), nil).View(); got != "" {
		t.Errorf("an unsized view drew %q", got)
	}
}

func TestView_TheTokenFieldIsMasked(t *testing.T) {
	t.Parallel()

	d := newDriver(t, testFake())
	d.typeIn(testSite)
	d.press("enter")
	d.typeIn(testEmail)
	d.press("enter")
	d.typeIn(testToken)

	frame := d.frame()
	if strings.Contains(frame, testToken) {
		t.Fatalf("the token is on the screen:\n%s", frame)
	}
	if !strings.Contains(frame, strings.Repeat("*", len(testToken))) {
		t.Errorf("the token field is not masked:\n%s", frame)
	}
}

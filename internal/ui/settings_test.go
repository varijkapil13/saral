package ui

import (
	"strings"
	"testing"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// labelRoom is what a settings option's label has to fit in for four of them to
// sit on one radio row at the 80 columns docs/UX.md supports. Past it the row
// degrades to a picker, which is a legitimate shape for the projects on a site
// and a poor one for a choice of four.
const labelRoom = 16

// A settings option is a value, not an instruction. The nine appearance
// commands this screen replaced were phrased for a palette that offers actions
// — "Use the Nord colour scheme" — and a label that reads that way in a value
// slot puts the sentence back: "Colour scheme: use the Nord colour scheme".
//
// The test is the redundancy rather than the wording, because that is the part
// a machine can see: a label that repeats its own setting's title is a label
// still addressed to somebody choosing a command.
func TestSettings_AnOptionIsAValueAndNotAnInstruction(t *testing.T) {
	t.Parallel()

	deps := kernel.Deps{Theme: kernel.NewTheme(kernel.ThemeAuto, true, kernel.UnicodeGlyphs())}
	checked := 0
	for _, s := range kernel.Settings() {
		if s.Options == nil {
			continue
		}
		title := strings.ToLower(s.Title)
		for _, opt := range s.Options(deps) {
			label := strings.TrimSpace(opt.Label)
			if label == "" {
				continue
			}
			checked++
			if strings.Contains(strings.ToLower(label), title) {
				t.Errorf("%s: option %q repeats the setting's own title %q, so the row reads %q",
					s.ID, label, s.Title, s.Title+": "+label)
			}
			for _, verb := range [...]string{"use ", "turn ", "follow ", "switch ", "show "} {
				if strings.HasPrefix(strings.ToLower(label), verb) {
					t.Errorf("%s: option %q opens with %q, which is how a command reads and not a value",
						s.ID, label, strings.TrimSpace(verb))
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no settings offered an option, so this test proved nothing")
	}
}

// A choice of a handful of fixed values draws as radios, and radios only fit
// when their labels do. A setting whose options come from the site — the
// projects this account can see — is exempt: it has a picker behind it because
// there is no bound on how many there are or how they are named.
func TestSettings_AFixedChoiceFitsOnARadioRow(t *testing.T) {
	t.Parallel()

	fromTheSite := map[string]bool{"session.project": true, "session.profile": true}
	deps := kernel.Deps{Theme: kernel.NewTheme(kernel.ThemeAuto, true, kernel.UnicodeGlyphs())}
	checked := 0
	for _, s := range kernel.Settings() {
		if s.Options == nil || fromTheSite[s.ID] {
			continue
		}
		for _, opt := range s.Options(deps) {
			checked++
			if n := len([]rune(opt.Label)); n > labelRoom {
				t.Errorf("%s: option %q is %d runes, and %d is what fits four of them on an 80-column row",
					s.ID, opt.Label, n, labelRoom)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no fixed choice offered an option, so this test proved nothing")
	}
}

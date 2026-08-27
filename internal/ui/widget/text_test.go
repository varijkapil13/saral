package widget

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func stroke(s string) tea.KeyPressMsg {
	switch s {
	case "alt+k":
		return tea.KeyPressMsg{Code: 'k', Mod: tea.ModAlt}
	case "ctrl+k":
		return tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl}
	default:
		panic("unspelt stroke " + s)
	}
}

// The kernel keeps ctrl+k for the palette and never forwards it, even to a view
// that is taking typing, so the widget it used to reach has to answer to
// something else. This is the assertion the issue is about: it fails the day a
// field is built past these constructors, or the day the replacement is dropped.
func TestKillLine_TakesTheRestOfTheLineOnAltKAndNotOnCtrlK(t *testing.T) {
	t.Parallel()

	const line = "summary and the rest of it"
	const kept = "summary "

	t.Run("a single-line field", func(t *testing.T) {
		t.Parallel()

		in := NewInput()
		in.Focus()
		in.SetValue(line)
		in.SetCursor(len(kept))

		after, _ := in.Update(stroke("alt+k"))
		if got := after.Value(); got != kept {
			t.Errorf("alt+k left %q, want %q", got, kept)
		}

		in.SetValue(line)
		in.SetCursor(len(kept))
		after, _ = in.Update(stroke("ctrl+k"))
		if got := after.Value(); got != line {
			t.Errorf("ctrl+k left %q; it belongs to the palette and must not reach the field", got)
		}
	})

	t.Run("a multi-line editor", func(t *testing.T) {
		t.Parallel()

		ta := NewArea()
		ta.Focus()
		ta.SetValue(line)
		ta.SetCursorColumn(len(kept))

		after, _ := ta.Update(stroke("alt+k"))
		if got := after.Value(); got != kept {
			t.Errorf("alt+k left %q, want %q", got, kept)
		}

		ta.SetValue(line)
		ta.SetCursorColumn(len(kept))
		after, _ = ta.Update(stroke("ctrl+k"))
		if got := after.Value(); got != line {
			t.Errorf("ctrl+k left %q; it belongs to the palette and must not reach the editor", got)
		}
	})
}

// The binding is registered by eleven view packages, so what it says about itself
// is what eleven footers and overlays say.
func TestKillLine_IsSpeltTheWayAViewAdvertisesIt(t *testing.T) {
	t.Parallel()

	if got := KillLine.Help().Key; got != "alt+k" {
		t.Errorf("the binding is advertised as %q", got)
	}
	if got := KillLine.Keys(); len(got) != 1 || got[0] != "alt+k" {
		t.Errorf("the binding matches %v, want alt+k alone", got)
	}
	if KillLine.Help().Desc == "" {
		t.Error("the binding has no description, so the ? overlay lists a bare key")
	}
}

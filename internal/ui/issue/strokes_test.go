package issue

import (
	"testing"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// The stroke table is what a keypress is dispatched through, and it is turned
// inside out from the keymap at start-up — so every stroke the keymap binds has
// to be in it exactly once, and nothing else may be.
func TestStrokes_TheTableIsTheKeymapTurnedInsideOut(t *testing.T) {
	t.Parallel()

	k := defaultKeys()
	bound := map[string]string{}
	for _, b := range []kernel.Binding{
		k.Up, k.Down, k.PageUp, k.PageDown, k.HalfUp, k.HalfDown,
		k.Go, k.Top, k.Bottom, k.Left, k.Right,
		k.Pane, k.PrevPane, k.Expands, k.Edit, k.Move, k.Comments,
	} {
		for _, stroke := range b.Keys() {
			if other, clash := bound[stroke]; clash {
				t.Errorf("%q is bound to both %q and %q", stroke, other, b.Help().Desc)
			}
			bound[stroke] = b.Help().Desc
			if strokes[stroke] == actNone {
				t.Errorf("%q is bound to %q and the stroke table answers nothing for it", stroke, b.Help().Desc)
			}
		}
	}
	for stroke := range strokes {
		if _, ok := bound[stroke]; !ok {
			t.Errorf("the stroke table answers %q, which no binding advertises", stroke)
		}
	}
}

// Every action between actUp and actBottom is a motion and has a step; nothing
// else does, so a new action cannot fall through to a scroll unnoticed.
func TestStrokes_EveryMotionActionHasAStep(t *testing.T) {
	t.Parallel()

	for at := actUp; at <= actBottom; at++ {
		if _, ok := steps[at]; !ok {
			t.Errorf("action %d is among the motions and has no step", at)
		}
	}
	for _, at := range []action{
		actNone, actLeft, actRight, actGo, actPane, actPrevPane,
		actExpands, actEdit, actMove, actComments,
	} {
		if _, ok := steps[at]; ok {
			t.Errorf("action %d is not a motion and has a step, so it would scroll as well", at)
		}
	}
}

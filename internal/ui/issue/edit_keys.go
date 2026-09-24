package issue

import (
	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// moveBinding is the stroke that opens the transition picker. It lives here
// rather than in the detail pane's own keymap so that everything about it is
// in one place, and is named in that keymap so the footer and the help
// overlay advertise it.
func moveBinding() kernel.Binding {
	return kernel.Bind([]string{"t"}, "t", "change status")
}

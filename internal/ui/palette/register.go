package palette

import "github.com/varijkapil13/saral/internal/ui/kernel"

// The palette claims no footer slot: docs/UX.md keeps the digits for the views a
// session lives in, and this one is pushed over whatever it was opened from.
// ctrl+k is the kernel's key for it, so registering under kernel.PaletteViewID
// is the whole of being reachable.
//
// It registers no keys. RegisterKeys records a view's resting state, and the
// palette has none: it takes typing from the moment it opens, so
// kernel.KeyReporter answers for every state it has and a registry entry would
// never be read.
func init() {
	kernel.RegisterView(kernel.ViewSpec{
		ID:    kernel.PaletteViewID,
		Title: "Commands",
		New:   New,
	})
}

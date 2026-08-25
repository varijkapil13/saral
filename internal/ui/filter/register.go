package filter

import "github.com/varijkapil13/saral/internal/ui/kernel"

// The picker registers its keys but not a view spec: it is reached by being
// pushed from the view whose search it narrows, and a registry constructor has
// no terms to open over and nowhere to send the value it is closed on.
//
// The key and the palette entry that open it belong to that view too, for the
// same reason: it is the one that knows what is being filtered.
func init() {
	kernel.RegisterKeys(ViewID, defaultKeys().keySet())
}

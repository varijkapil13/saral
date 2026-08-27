package move

import "github.com/varijkapil13/saral/internal/ui/kernel"

// The wizard registers its keys but not a view spec: it is reached by being
// pushed with the issues to move, and a registry constructor has none to open
// over. There is no port method that lists projects either, so nothing here can
// offer a target from the registry side.
//
// The key and the palette entry that open it belong to the view holding the
// issues, for the same reason, and that view hides them with the reason in
// Requires when a token may not move issues at all.
func init() {
	kernel.RegisterKeys(ViewID, defaultKeys().keySet())
}

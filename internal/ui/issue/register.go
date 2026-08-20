package issue

import "github.com/varijkapil13/saral/internal/ui/kernel"

// The detail pane registers its keys but not a view spec: it is reached by
// being pushed with an issue, and a registry constructor has nothing to push.
func init() {
	kernel.RegisterKeys(ViewID, defaultKeys().keySet())
}

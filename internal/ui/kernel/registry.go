package kernel

import (
	"fmt"
	"sort"
	"sync"
)

// GlobalScope is the key-registry scope for keys that work in every view.
const GlobalScope = "*"

var reg = struct {
	mu       sync.RWMutex
	views    map[string]ViewSpec
	commands map[string]Command
	keys     map[string]KeySet
	errs     []error
}{
	views:    make(map[string]ViewSpec),
	commands: make(map[string]Command),
	keys:     make(map[string]KeySet),
}

// RegisterView adds a view to the registry. It is called from an init() in the
// view's own package, so two packets adding two views never edit the same line.
//
// A bad or duplicate registration is recorded rather than raised: init() runs
// before anything can handle an error, and a panic in a library package is
// worse than a startup message. Call RegistrationErrors before New.
func RegisterView(spec ViewSpec) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	switch {
	case spec.ID == "":
		reg.errs = append(reg.errs, fmt.Errorf("kernel: a view was registered with no ID"))
		return
	case spec.New == nil:
		reg.errs = append(reg.errs, fmt.Errorf("kernel: view %q registered with no constructor", spec.ID))
		return
	case spec.Slot < 0 || spec.Slot > 9:
		reg.errs = append(reg.errs, fmt.Errorf("kernel: view %q claims footer slot %d, which is not 0-9", spec.ID, spec.Slot))
		return
	}
	if _, dup := reg.views[spec.ID]; dup {
		reg.errs = append(reg.errs, fmt.Errorf("kernel: view %q is registered twice", spec.ID))
		return
	}
	if spec.Slot > 0 {
		for _, other := range reg.views {
			if other.Slot == spec.Slot {
				reg.errs = append(reg.errs, fmt.Errorf("kernel: views %q and %q both claim footer slot %d", other.ID, spec.ID, spec.Slot))
				return
			}
		}
	}
	reg.views[spec.ID] = spec
}

// RegisterCommand adds a command to the palette.
func RegisterCommand(cmd Command) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	switch {
	case cmd.ID == "":
		reg.errs = append(reg.errs, fmt.Errorf("kernel: a command was registered with no ID"))
		return
	case cmd.Run == nil:
		reg.errs = append(reg.errs, fmt.Errorf("kernel: command %q registered with nothing to run", cmd.ID))
		return
	}
	if _, dup := reg.commands[cmd.ID]; dup {
		reg.errs = append(reg.errs, fmt.Errorf("kernel: command %q is registered twice", cmd.ID))
		return
	}
	reg.commands[cmd.ID] = cmd
}

// RegisterKeys records a view's keys so the footer and the help overlay can
// show exactly what works right now. Use GlobalScope for keys that always work.
func RegisterKeys(viewID string, set KeySet) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	if viewID == "" {
		reg.errs = append(reg.errs, fmt.Errorf("kernel: keys were registered with no view ID"))
		return
	}
	if _, dup := reg.keys[viewID]; dup {
		reg.errs = append(reg.errs, fmt.Errorf("kernel: keys for %q are registered twice", viewID))
		return
	}
	reg.keys[viewID] = set
}

// Views returns every registered view, ordered by footer slot and then by ID so
// that a build is reproducible whatever order init() ran in.
func Views() []ViewSpec {
	reg.mu.RLock()
	defer reg.mu.RUnlock()
	out := make([]ViewSpec, 0, len(reg.views))
	for _, spec := range reg.views {
		out = append(out, spec)
	}
	sortViews(out)
	return out
}

// LookupView returns one registered view by ID.
func LookupView(id string) (ViewSpec, bool) {
	reg.mu.RLock()
	defer reg.mu.RUnlock()
	spec, ok := reg.views[id]
	return spec, ok
}

// Commands returns every registered command, ordered by group and then title.
func Commands() []Command {
	reg.mu.RLock()
	defer reg.mu.RUnlock()
	out := make([]Command, 0, len(reg.commands))
	for _, cmd := range reg.commands {
		out = append(out, cmd)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Group != out[j].Group {
			return out[i].Group < out[j].Group
		}
		if out[i].Title != out[j].Title {
			return out[i].Title < out[j].Title
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// KeysFor returns the keys registered for a view.
func KeysFor(viewID string) KeySet {
	reg.mu.RLock()
	defer reg.mu.RUnlock()
	return reg.keys[viewID]
}

// RegistrationErrors returns everything wrong with the registrations that ran.
// main reports these and exits; a view that failed to register is a build-time
// mistake, not something to limp along with.
func RegistrationErrors() []error {
	reg.mu.RLock()
	defer reg.mu.RUnlock()
	return append([]error(nil), reg.errs...)
}

func sortViews(specs []ViewSpec) {
	sort.Slice(specs, func(i, j int) bool {
		si, sj := specs[i].Slot, specs[j].Slot
		switch {
		case si == 0 && sj != 0:
			return false
		case sj == 0 && si != 0:
			return true
		case si != sj:
			return si < sj
		default:
			return specs[i].ID < specs[j].ID
		}
	})
}

// resetRegistry clears every registration. Tests use it; nothing else may.
func resetRegistry() {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	reg.views = make(map[string]ViewSpec)
	reg.commands = make(map[string]Command)
	reg.keys = make(map[string]KeySet)
	reg.errs = nil
}

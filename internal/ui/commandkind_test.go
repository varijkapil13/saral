package ui

import (
	"testing"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// A Kind that nothing sets is KindVerb, and KindVerb sorts last — so a command
// that forgot one does not fail to build, does not fail a palette test scoped to
// three linked packages, and simply sinks to the bottom of the list. This
// package links every view, so it is the only place the whole registry can be
// held to the ranks docs/SETTINGS.md gives it.
//
// The groups are the check rather than the IDs: a group is what a reader sees,
// and a new destination registered into "Go to" with no Kind is exactly the
// regression this catches.
func TestCommandKind_EveryGroupThatHasARankCarriesIt(t *testing.T) {
	t.Parallel()

	want := map[string]kernel.CommandKind{
		"Go to":   kernel.KindGoTo,
		"Search":  kernel.KindSearch,
		"Session": kernel.KindSession,
	}
	checked := 0
	for _, cmd := range kernel.Commands() {
		kind, ranked := want[cmd.Group]
		if !ranked {
			continue
		}
		checked++
		if cmd.Kind != kind {
			t.Errorf("%s is in group %q but carries kind %d, not %d, so it sorts with the verbs",
				cmd.ID, cmd.Group, cmd.Kind, kind)
		}
	}
	if checked == 0 {
		t.Fatal("no command sits in a ranked group, so this test proved nothing")
	}
}

// The other direction: a command that is not in one of those groups must not
// claim one of their ranks, or it is drawn under a heading it does not belong to.
func TestCommandKind_ARankIsNotClaimedOutsideItsGroup(t *testing.T) {
	t.Parallel()

	group := map[kernel.CommandKind]string{
		kernel.KindGoTo:    "Go to",
		kernel.KindSearch:  "Search",
		kernel.KindSession: "Session",
	}
	checked := 0
	for _, cmd := range kernel.Commands() {
		checked++
		if want, ranked := group[cmd.Kind]; ranked && cmd.Group != want {
			t.Errorf("%s carries kind %d, which heads %q, but its group is %q",
				cmd.ID, cmd.Kind, want, cmd.Group)
		}
	}
	if checked == 0 {
		t.Fatal("the registry holds no command, so this test proved nothing")
	}
}

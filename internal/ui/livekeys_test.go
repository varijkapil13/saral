package ui

import (
	"sort"
	"testing"
	"time"

	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/varijkapil13/saral/internal/ui/attach"
	"github.com/varijkapil13/saral/internal/ui/backlog"
	"github.com/varijkapil13/saral/internal/ui/board"
	"github.com/varijkapil13/saral/internal/ui/comment"
	"github.com/varijkapil13/saral/internal/ui/filter"
	"github.com/varijkapil13/saral/internal/ui/form"
	"github.com/varijkapil13/saral/internal/ui/issue"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/list"
	"github.com/varijkapil13/saral/internal/ui/move"
	"github.com/varijkapil13/saral/internal/ui/onboarding"
	"github.com/varijkapil13/saral/internal/ui/plan"
	"github.com/varijkapil13/saral/internal/ui/release"
	"github.com/varijkapil13/saral/internal/ui/settings"
	"github.com/varijkapil13/saral/internal/ui/sprint"
	"github.com/varijkapil13/saral/internal/ui/timeline"
	"github.com/varijkapil13/saral/pkg/jira"
)

// Four of these views take more than deps to build, so each gets one builder here
// rather than the same closure written out in three tables.
func newAttach(d kernel.Deps) kernel.View { return attach.New(d) }
func newMove(d kernel.Deps) kernel.View   { return move.New(d) }
func newPlan(d kernel.Deps) kernel.View   { return plan.New(d) }

// newFlow builds the release screen the way the versions list pushes it. A count
// above zero is what opens it on the decision rather than straight on the confirm.
func newFlow(d kernel.Deps) kernel.View {
	return release.NewFlow(d, jira.Version{ID: "10100", ProjectID: "10000", Name: "1.4.0"}, 3, nil)
}

// keyReporters names the model that answers for each registered key scope. The
// kernel cannot hold the views to this — it may not import one — and a view
// checking itself is the drift, so the sweep lives here, above every view and
// below nothing.
//
// Every scope in the key registry has to appear, so the next view fails this
// until somebody has decided which half of the table it belongs in.
var keyReporters = map[string]func(kernel.Deps) kernel.View{
	list.ViewID:        list.New,
	filter.ViewID:      func(d kernel.Deps) kernel.View { return filter.New(d) },
	form.ViewID:        form.New,
	comment.ViewID:     comment.New,
	onboarding.ViewID:  onboarding.New,
	issue.EditViewID:   func(d kernel.Deps) kernel.View { return issue.NewEdit(d, seed()) },
	issue.MoveViewID:   func(d kernel.Deps) kernel.View { return issue.NewMove(d, seed()) },
	attach.ViewID:      newAttach,
	backlog.ViewID:     backlog.New,
	board.ViewID:       board.New,
	move.ViewID:        newMove,
	plan.ViewID:        newPlan,
	release.ViewID:     release.New,
	release.FlowViewID: newFlow,
	sprint.ViewID:      sprint.New,
	timeline.ViewID:    timeline.New,
}

// staticKeys are the scopes whose view deliberately reports nothing, each with
// the reason. A view lands here because its keys genuinely do not move, not
// because nobody got round to it.
var staticKeys = map[string]struct {
	build func(kernel.Deps) kernel.View
	why   string
}{
	issue.ViewID: {
		build: func(d kernel.Deps) kernel.View { return issue.New(d, seed()) },
		why:   "the detail pane scrolls a document and every one of its keys works whatever it is showing",
	},
	kernel.SettingsViewID: {
		build: settings.New,
		why:   "every row shape answers to the same four directions and enter; nothing about the screen's own state removes one",
	},
}

func TestLiveKeys_EveryViewWhoseKeysMoveReportsThem(t *testing.T) {
	sweepEnv(t)
	scopes := kernel.KeyScopes()
	if len(scopes) == 0 {
		t.Fatal("no view registered any keys, so this sweep is checking nothing")
	}

	for _, scope := range scopes {
		build, reports := keyReporters[scope]
		static, isStatic := staticKeys[scope]
		switch {
		case reports && isStatic:
			t.Errorf("%s is in both halves of the table", scope)
			continue
		case !reports && !isStatic:
			t.Errorf("%s registers keys and this sweep does not know whether they move with its state; "+
				"add it to keyReporters, or to staticKeys with the reason its keys never move", scope)
			continue
		case isStatic:
			build = static.build
			if static.why == "" {
				t.Errorf("%s is exempt with no reason given", scope)
			}
		}

		view := build(depsFor(t))
		_, implements := view.(kernel.KeyReporter)
		switch {
		case reports && !implements:
			t.Errorf("%s is listed as reporting its live keys and does not implement kernel.KeyReporter, "+
				"so its footer is whatever it registered at start-up", scope)
		case isStatic && implements:
			t.Errorf("%s implements kernel.KeyReporter but is listed as static: %s", scope, static.why)
		}
		if kernel.KeysFor(scope).IsZero() {
			t.Errorf("%s registered an empty key set; it is the resting record and what the command sweep "+
				"holds a palette entry's key against", scope)
		}
	}

	for scope := range keyReporters {
		if !known(scopes, scope) {
			t.Errorf("keyReporters names %q, which registers no keys any more", scope)
		}
	}
	for scope := range staticKeys {
		if !known(scopes, scope) {
			t.Errorf("staticKeys names %q, which registers no keys any more", scope)
		}
	}
}

// A view that reports has to say something in the state it is built in, or the
// footer of a freshly opened view is the globals and nothing else.
func TestLiveKeys_AFreshlyBuiltViewAdvertisesSomething(t *testing.T) {
	sweepEnv(t)
	for scope, build := range keyReporters {
		reporter, ok := build(depsFor(t)).(kernel.KeyReporter)
		if !ok {
			continue
		}
		if set, _ := reporter.LiveKeys(); set.IsZero() {
			t.Errorf("%s advertises nothing in the state it opens in", scope)
		}
	}
}

// actionFree names the states that deliberately offer nothing to do, with the
// reason. A state waiting on a site has no action of its own and says so by
// advertising none, which is a different thing from a view that never got round
// to naming its actions.
//
// The states themselves are private to their packages, so each package holds its
// own states to this; what belongs here is the record of which views have one at
// all, so that the next view cannot quietly join them.
var actionFree = map[string]string{
	issue.EditViewID:  "a save in flight refuses every key until the site answers",
	issue.MoveViewID:  "a transition in flight refuses every key until the site answers",
	onboarding.ViewID: "a step being checked against the site refuses enter and shift+tab both",
	attach.ViewID:     "a pane with nothing attached, on a token that may not attach, has nothing to offer but the way out",
}

// Every state a footer can be drawn from has to name what can be done in it, or
// the row is the globals and nothing else. What this can reach from above the
// views is the state each one opens in and the resting set each one registered;
// the rest is held by each package over its own states.
func TestLiveKeys_EveryReportingStateNamesAnActionOrIsListedWithAReason(t *testing.T) {
	sweepEnv(t)
	for _, scope := range kernel.KeyScopes() {
		if got := kernel.KeysFor(scope).Acts; len(got) == 0 {
			t.Errorf("%s registered a resting set that names no action, so its footer is a row of globals "+
				"and the command sweep has nothing to hold a palette entry's key against", scope)
		}
	}
	for scope, build := range keyReporters {
		reporter, ok := build(depsFor(t)).(kernel.KeyReporter)
		if !ok {
			continue
		}
		set, _ := reporter.LiveKeys()
		if len(set.Acts) == 0 && actionFree[scope] == "" {
			t.Errorf("%s opens in a state that names no action and is not listed as action-free with a reason", scope)
		}
	}
	for scope, why := range actionFree {
		if _, reports := keyReporters[scope]; !reports {
			t.Errorf("actionFree names %q, which does not report its live keys, so it has no states to be free of", scope)
		}
		if why == "" {
			t.Errorf("%s is listed action-free with no reason", scope)
		}
	}
}

// Two things every advertised action owes the mouse and the footer's zone map: a
// first stroke the kernel can turn back into the keypress a click delivers, and a
// key label no other action in the same state shares, because the zone a click
// resolves to is minted from that label.
func TestLiveKeys_EveryAdvertisedActionCanBeClicked(t *testing.T) {
	sweepEnv(t)
	for _, scope := range kernel.KeyScopes() {
		seen := make(map[string]string)
		for _, b := range kernel.KeysFor(scope).Acts {
			label := b.Help().Key
			if other, clash := seen[label]; clash {
				t.Errorf("%s advertises %q for both %q and %q; the footer mints one zone per label, "+
					"so a click on it reaches whichever came first", scope, label, other, b.Help().Desc)
			}
			seen[label] = b.Help().Desc
			if _, ok := kernel.Stroke(b); !ok {
				t.Errorf("%s advertises %q on %v, which the kernel cannot spell back into a keypress, "+
					"so clicking it does nothing", scope, label, b.Keys())
			}
		}
	}
}

// closers names the views a discard can reach — everything something pushes —
// and parked names the ones nothing pushes, which the kernel therefore never
// discards. Like keyReporters above, the kernel cannot hold the views to this —
// it may not import one — and a view checking itself is the drift, so the sweep
// lives here.
//
// Every scope in the key registry has to appear in exactly one half, so the next
// view fails this until somebody has decided which.
var closers = map[string]func(kernel.Deps) kernel.View{
	filter.ViewID:    func(d kernel.Deps) kernel.View { return filter.New(d) },
	form.ViewID:      form.New,
	comment.ViewID:   comment.New,
	issue.ViewID:     func(d kernel.Deps) kernel.View { return issue.New(d, seed()) },
	issue.EditViewID: func(d kernel.Deps) kernel.View { return issue.NewEdit(d, seed()) },
	issue.MoveViewID: func(d kernel.Deps) kernel.View { return issue.NewMove(d, seed()) },
	// Neither of these registers a view spec, so a push with the issue or the
	// issues it is about is the only way either is ever on screen.
	attach.ViewID: newAttach,
	move.ViewID:   newMove,
	// The release screen is pushed by the versions list with the version and the
	// count; nothing opens it by name.
	release.FlowViewID: newFlow,
	// Both of these hold a footer slot, so nothing pushes them and neither Close is
	// ever called; they are here because withdrawing the method is theirs to do.
	board.ViewID: board.New,
	plan.ViewID:  newPlan,
}

// parked are the views nothing ever pushes, each with the reason. A root the
// user switches away from is kept and comes back on its digit, so a discard
// never reaches one and a Close on it would be a method nobody calls. A view
// lands here because of that and not because nobody got round to it: the day
// something pushes one of these, it owes the interface.
var parked = map[string]struct {
	build func(kernel.Deps) kernel.View
	why   string
}{
	list.ViewID: {
		build: list.New,
		why:   "the issue list holds a footer slot and nothing pushes it, so it is only ever a root",
	},
	onboarding.ViewID: {
		build: onboarding.New,
		why:   "setup is where the program starts and what the palette reopens, and neither of those is a push",
	},
	backlog.ViewID: {
		build: backlog.New,
		why:   "the backlog holds a footer slot and nothing pushes it, so it is only ever a root",
	},
	sprint.ViewID: {
		build: sprint.New,
		why:   "the sprints hold a footer slot and nothing pushes them, so they are only ever a root",
	},
	release.ViewID: {
		build: release.New,
		why:   "the versions list holds a footer slot; what it pushes is the release screen, which is not itself",
	},
	timeline.ViewID: {
		build: timeline.New,
		why:   "the timeline holds a footer slot and nothing pushes it, so it is only ever a root",
	},
	kernel.SettingsViewID: {
		build: settings.New,
		why:   "settings holds no footer slot, but ctrl+, and g s open it by name rather than push it, so it is only ever a root",
	},
}

func TestClose_EveryViewADiscardCanReachStopsItsWork(t *testing.T) {
	sweepEnv(t)
	scopes := kernel.KeyScopes()
	if len(scopes) == 0 {
		t.Fatal("no view registered any keys, so this sweep is checking nothing")
	}

	for _, scope := range scopes {
		build, pushed := closers[scope]
		root, isRoot := parked[scope]
		switch {
		case pushed && isRoot:
			t.Errorf("%s is in both halves of the table", scope)
			continue
		case !pushed && !isRoot:
			t.Errorf("%s is a view and this sweep does not know whether anything pushes it; add it to "+
				"closers, or to parked with the reason a discard never reaches it", scope)
			continue
		case isRoot:
			build = root.build
			if root.why == "" {
				t.Errorf("%s is exempt with no reason given", scope)
			}
		}

		_, implements := build(depsFor(t)).(kernel.Closer)
		switch {
		case pushed && !implements:
			t.Errorf("%s can be discarded and does not implement kernel.Closer, so a read it started "+
				"goes on running for an answer the kernel has thrown the view away for", scope)
		case isRoot && implements:
			t.Errorf("%s implements kernel.Closer and nothing would ever call it: %s", scope, root.why)
		}
	}

	for scope := range closers {
		if !known(scopes, scope) {
			t.Errorf("closers names %q, which is no longer a view", scope)
		}
	}
	for scope := range parked {
		if !known(scopes, scope) {
			t.Errorf("parked names %q, which is no longer a view", scope)
		}
	}
}

// answerable names the views that ask the site for something and therefore owe
// the kernel an address. An answer is delivered to the view that asked for it,
// and a view with no address can only be handed one by happening to be on top of
// the stack when it lands. Like the two sweeps above, the kernel cannot hold the
// views to this, so the sweep lives here.
//
// The command palette is the one view in this build that asks the site for
// nothing: its commands come out of the registry and its issues out of the local
// index, both inside the keystroke. It registers no keys, so it is named here
// rather than listed below.
var answerable = map[string]func(kernel.Deps) kernel.View{
	list.ViewID:       list.New,
	filter.ViewID:     func(d kernel.Deps) kernel.View { return filter.New(d) },
	form.ViewID:       form.New,
	comment.ViewID:    comment.New,
	onboarding.ViewID: onboarding.New,
	issue.ViewID:      func(d kernel.Deps) kernel.View { return issue.New(d, seed()) },
	issue.EditViewID:  func(d kernel.Deps) kernel.View { return issue.NewEdit(d, seed()) },
	issue.MoveViewID:  func(d kernel.Deps) kernel.View { return issue.NewMove(d, seed()) },
	attach.ViewID:     newAttach,
	backlog.ViewID:    backlog.New,
	board.ViewID:      board.New,
	move.ViewID:       newMove,
	plan.ViewID:       newPlan,
	release.ViewID:    release.New,
	// The release screen is pushed with its count already read, so the only answer
	// it waits for is the release it sends.
	release.FlowViewID: newFlow,
	sprint.ViewID:      sprint.New,
	timeline.ViewID:    timeline.New,
}

// synchronous are the views that answer every question they are asked without
// leaving the frame, each with the reason. A view lands here because nothing it
// does outlives a keystroke, not because nobody got round to giving it an
// address.
var synchronous = map[string]struct {
	build func(kernel.Deps) kernel.View
	why   string
}{
	kernel.SettingsViewID: {
		build: settings.New,
		why:   "every setting answers from Deps or a local config read; nothing here waits on the site",
	},
}

func TestAddressed_EveryViewThatAsksTheSiteForSomethingCanBeAnsweredWhereItIs(t *testing.T) {
	sweepEnv(t)
	scopes := kernel.KeyScopes()
	if len(scopes) == 0 {
		t.Fatal("no view registered any keys, so this sweep is checking nothing")
	}

	for _, scope := range scopes {
		build, asks := answerable[scope]
		local, isLocal := synchronous[scope]
		switch {
		case asks && isLocal:
			t.Errorf("%s is in both halves of the table", scope)
			continue
		case !asks && !isLocal:
			t.Errorf("%s is a view and this sweep does not know whether it asks the site for anything; "+
				"add it to answerable, or to synchronous with the reason nothing it does outlives a frame", scope)
			continue
		case isLocal:
			build = local.build
			if local.why == "" {
				t.Errorf("%s is exempt with no reason given", scope)
			}
		}

		view := build(depsFor(t))
		addressed, implements := view.(kernel.Addressed)
		switch {
		case asks && !implements:
			t.Errorf("%s asks the site for things and does not implement kernel.Addressed, so its answers "+
				"go to whatever the stack has on top when they land", scope)
			continue
		case isLocal && implements:
			t.Errorf("%s implements kernel.Addressed and has no answer to deliver: %s", scope, local.why)
			continue
		case isLocal:
			continue
		}

		if addressed.Addr() == 0 {
			t.Errorf("%s answers to the zero address, which the kernel resolves to nothing, so its "+
				"answers are dropped rather than delivered", scope)
		}
		other, _ := build(depsFor(t)).(kernel.Addressed)
		if other != nil && other.Addr() == addressed.Addr() {
			t.Errorf("%s hands two instances the same address, so an answer to one is drawn into the other", scope)
		}
	}

	for scope := range answerable {
		if !known(scopes, scope) {
			t.Errorf("answerable names %q, which is no longer a view", scope)
		}
	}
	for scope := range synchronous {
		if !known(scopes, scope) {
			t.Errorf("synchronous names %q, which is no longer a view", scope)
		}
	}
}

// seed is the row a list would have handed over: a key and nothing else, which
// is all these panes need to be built.
func seed() jira.Issue {
	return jira.Issue{ID: "10001", Key: "PROJ-1", Summary: "Something to look at"}
}

func sweepEnv(t *testing.T) {
	t.Helper()
	// Two of these views locate a drafts directory when they are built. Pointing
	// the lookup at a temporary directory keeps the sweep off whatever the person
	// running it has configured.
	t.Setenv("SARAL_CONFIG_DIR", t.TempDir())
	t.Setenv("SARAL_CACHE_DIR", t.TempDir())
}

func depsFor(t *testing.T) kernel.Deps {
	t.Helper()
	// A session is always scoped to a project, and the plans view opens on what it
	// derives from that: without one it is built in a state the program never has.
	return kernel.Deps{
		Theme:   kernel.NewTheme(kernel.ThemeNoColor, true, kernel.ASCIIGlyphs()),
		Zones:   zone.New(),
		Site:    "example.atlassian.net",
		Project: "PROJ",
		Now:     func() time.Time { return time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC) },
	}
}

func known(scopes []string, want string) bool {
	at := sort.SearchStrings(scopes, want)
	return at < len(scopes) && scopes[at] == want
}

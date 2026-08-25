package ui

import (
	"sort"
	"testing"
	"time"

	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/varijkapil13/saral/internal/ui/comment"
	"github.com/varijkapil13/saral/internal/ui/form"
	"github.com/varijkapil13/saral/internal/ui/issue"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/list"
	"github.com/varijkapil13/saral/internal/ui/onboarding"
	"github.com/varijkapil13/saral/pkg/jira"
)

// keyReporters names the model that answers for each registered key scope. The
// kernel cannot hold the views to this — it may not import one — and a view
// checking itself is the drift, so the sweep lives here, above every view and
// below nothing.
//
// Every scope in the key registry has to appear, so a seventh view fails this
// until somebody has decided which half of the table it belongs in.
var keyReporters = map[string]func(kernel.Deps) kernel.View{
	list.ViewID:       list.New,
	form.ViewID:       form.New,
	comment.ViewID:    comment.New,
	onboarding.ViewID: onboarding.New,
	issue.EditViewID:  func(d kernel.Deps) kernel.View { return issue.NewEdit(d, seed()) },
	issue.MoveViewID:  func(d kernel.Deps) kernel.View { return issue.NewMove(d, seed()) },
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
	return kernel.Deps{
		Theme: kernel.NewTheme(kernel.ThemeNoColor, true, kernel.ASCIIGlyphs()),
		Zones: zone.New(),
		Site:  "example.atlassian.net",
		Now:   func() time.Time { return time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC) },
	}
}

func known(scopes []string, want string) bool {
	at := sort.SearchStrings(scopes, want)
	return at < len(scopes) && scopes[at] == want
}

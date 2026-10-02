package ui

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/palette"
)

type namedSet struct {
	name string
	set  kernel.KeySet
}

func (n namedSet) bindings() []kernel.Binding {
	out := append([]kernel.Binding(nil), n.set.Acts...)
	out = append(out, n.set.Short...)
	for _, column := range n.set.Full {
		out = append(out, column...)
	}
	return append(out, n.set.Menu...)
}

var vocabulary = func() []*kernel.Canonical {
	all := kernel.Vocabulary()
	out := make([]*kernel.Canonical, len(all))
	for i := range all {
		out[i] = &all[i]
	}
	return out
}()

var canonByAction = func() map[kernel.Action]kernel.Canonical {
	m := make(map[kernel.Action]kernel.Canonical)
	for _, c := range kernel.Vocabulary() {
		m[c.Action] = c
	}
	return m
}()

var bareOwner = func() map[string]kernel.Action {
	m := make(map[string]kernel.Action)
	for _, c := range kernel.Vocabulary() {
		if c.Modal {
			continue
		}
		for _, k := range c.Keys {
			if !slices.Contains(c.Prefixed, k) {
				m[k] = c.Action
			}
		}
	}
	return m
}()

func label(b kernel.Binding) string { return b.Help().Key + " | " + b.Help().Desc }

func vocabularyFindings(sets []namedSet) []string {
	seen := make(map[string]bool)
	var out []string
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, ns := range sets {
		if ns.set.Mode == kernel.Modal {
			continue
		}
		for _, b := range ns.bindings() {
			m, minted := kernel.MintOf(b)
			switch {
			case !minted:
				add("unminted: " + label(b))
			case m.Local:
				for _, k := range b.Keys() {
					if owner, taken := bareOwner[k]; taken {
						add(fmt.Sprintf("local %s.%s uses %q, which is %s: %s", m.Owner, m.ID, k, owner, label(b)))
					}
				}
				for _, c := range vocabulary {
					for _, alias := range c.Aliases {
						if strings.HasSuffix(m.Owner+"."+m.ID, alias) {
							add(fmt.Sprintf("local %s.%s is named for %s: %s", m.Owner, m.ID, c.Action, label(b)))
						}
					}
				}
			case b.Help().Key != canonByAction[m.Action].Label:
				add(fmt.Sprintf("canon %s is labelled %q, not %q", m.Action, b.Help().Key, canonByAction[m.Action].Label))
			}
		}
	}
	slices.Sort(out)
	return out
}

type viewScope struct {
	name  string
	build func(kernel.Deps) kernel.View
}

func scopeBuilders(t *testing.T) []viewScope {
	t.Helper()
	byName := map[string]func(kernel.Deps) kernel.View{}
	for scope, build := range keyReporters {
		byName[scope] = build
	}
	for scope, static := range staticKeys {
		byName[scope] = static.build
	}
	byName["palette"] = palette.New
	byName["palette.project"] = func(d kernel.Deps) kernel.View {
		cmd, ok := kernel.LookupCommand("project.switch")
		if !ok {
			t.Fatal("the project picker's command is not registered, so the picker cannot be built")
		}
		push, ok := cmd.Run(d)().(kernel.PushMsg)
		if !ok {
			t.Fatal("project.switch does not push a view")
		}
		return push.View
	}

	for _, scope := range kernel.KeyScopes() {
		if _, ok := byName[scope]; !ok && scope != kernel.GlobalScope {
			t.Errorf("%s registers keys and the vocabulary sweep has no way to build it", scope)
		}
	}
	out := make([]viewScope, 0, len(byName))
	for name, build := range byName {
		out = append(out, viewScope{name, build})
	}
	slices.SortFunc(out, func(a, b viewScope) int { return strings.Compare(a.name, b.name) })
	return out
}

func scopeSets(t *testing.T, s viewScope) (sets []namedSet, structural []string) {
	t.Helper()
	if resting := kernel.KeysFor(s.name); !resting.IsZero() {
		sets = append(sets, namedSet{s.name + " resting", resting})
	}
	view := s.build(depsFor(t))
	lister, lists := view.(kernel.KeyStateLister)
	if lists {
		for i, set := range lister.KeyStates() {
			sets = append(sets, namedSet{fmt.Sprintf("%s state %d", s.name, i), set})
		}
	}
	if reporter, ok := view.(kernel.KeyReporter); ok {
		live, _ := reporter.LiveKeys()
		sets = append(sets, namedSet{s.name + " live", live})
		if !lists {
			structural = append(structural, "does not implement kernel.KeyStateLister")
		}
	}
	return sets, structural
}

type legacy struct {
	why      string
	findings []string
}

// unmigrated is the closed list of scopes still to move onto the vocabulary,
// each with exactly the violations it has today. A scope's migration deletes its
// own stanza, and nothing new may be added.
var unmigrated = map[string]legacy{

	"backlog": {
		why: "waits for issue.ShareBindings to move onto the vocabulary",
		findings: []string{
			"unminted: Y | copy the link",
			"unminted: o | open in browser",
			"unminted: y | copy the key",
		},
	},

	"board": {
		why: "waits for issue.ShareBindings to move onto the vocabulary",
		findings: []string{
			"unminted: Y | copy the link",
			"unminted: o | open in browser",
			"unminted: y | copy the key",
		},
	},

	"issue": {
		why: "s save to ctrl+s, x/X to u/U, c to ], L to &, w to W, drop the b/f/u/d/space/x/e aliases",
		findings: []string{
			"command issue.assign ends in \".assign\" and does not set Action assign",
			"command issue.create ends in \".create\" and does not set Action create",
			"does not implement kernel.KeyStateLister",
			"unminted: < | wider sidebar",
			"unminted: = | reset the split",
			"unminted: > | wider description",
			"unminted: @ | assign",
			"unminted: C | comment",
			"unminted: E | open in $EDITOR",
			"unminted: G / g e | bottom",
			"unminted: L | links",
			"unminted: W | watchers",
			"unminted: X | revert all",
			"unminted: Y | copy the link",
			"unminted: b/pgup | page up",
			"unminted: c | list the children",
			"unminted: d/ctrl+d | half page down",
			"unminted: e | edit",
			"unminted: e | edit fields",
			"unminted: enter | edit this row",
			"unminted: f/pgdn | page down",
			"unminted: g g | top",
			"unminted: o | open in browser",
			"unminted: p | open the parent",
			"unminted: s | save",
			"unminted: s | save changes",
			"unminted: shift+tab | previous pane",
			"unminted: t | change status",
			"unminted: t | status",
			"unminted: tab | next pane",
			"unminted: tab | pane",
			"unminted: u/ctrl+u | half page up",
			"unminted: w | log time",
			"unminted: x | revert this",
			"unminted: y | copy the key",
			"unminted: z | expand or collapse",
			"unminted: ←/h | pan left",
			"unminted: ↑/k | up",
			"unminted: →/l | pan right",
			"unminted: ↓/j | down",
		},
	},

	"list": {
		why: "share keys come from issue.ShareBindings",
		findings: []string{
			"unminted: Y | copy the link",
			"unminted: o | open in browser",
			"unminted: y | copy the key",
		},
	},

	"search": {
		why: "share keys come from issue.ShareBindings",
		findings: []string{
			"unminted: Y | copy the link",
			"unminted: o | open in browser",
			"unminted: y | copy the key",
		},
	},
}

func TestVocabulary_EveryScopeIsHeldToIt(t *testing.T) {
	sweepEnv(t)
	scopes := scopeBuilders(t)
	if len(scopes) == 0 {
		t.Fatal("no scope was built, so this sweep is checking nothing")
	}

	commands := commandFindings(kernel.Commands())
	bindings := 0
	checked := make(map[string]bool)
	for _, s := range scopes {
		sets, structural := scopeSets(t, s)
		for _, ns := range sets {
			bindings += len(ns.bindings())
		}
		got := slices.Concat(structural, vocabularyFindings(sets))
		got = append(got, commands[s.name]...)
		delete(commands, s.name)
		slices.Sort(got)
		checked[s.name] = true

		want, exempt := unmigrated[s.name]
		switch {
		case !exempt:
			for _, f := range got {
				t.Errorf("%s: %s", s.name, f)
			}
		case len(got) == 0:
			t.Errorf("%s is listed as unmigrated and has no violations; delete its stanza", s.name)
		case want.why == "":
			t.Errorf("%s is listed as unmigrated with no reason", s.name)
		default:
			for _, f := range got {
				if !slices.Contains(want.findings, f) {
					t.Errorf("%s: new violation not in the unmigrated list: %s", s.name, f)
				}
			}
			for _, f := range want.findings {
				if !slices.Contains(got, f) {
					t.Errorf("%s: no longer a violation, remove it from unmigrated: %s", s.name, f)
				}
			}
		}
	}
	for scope, found := range commands {
		t.Errorf("%s owns palette entries but is not a scope this sweep builds: %v", scope, found)
	}
	for scope := range unmigrated {
		if !checked[scope] {
			t.Errorf("unmigrated names %q, which is not a scope this sweep builds", scope)
		}
	}
	if bindings == 0 {
		t.Fatal("no scope offered a binding, so this sweep is checking nothing")
	}
}

func TestVocabulary_TheKernelsOwnKeysAreOnIt(t *testing.T) {
	global := namedSet{kernel.GlobalScope, kernel.DefaultGlobalKeys().KeySet()}
	if len(global.bindings()) == 0 {
		t.Fatal("the global keys offer no binding, so this is checking nothing")
	}
	for _, f := range vocabularyFindings([]namedSet{global}) {
		t.Errorf("kernel: %s", f)
	}
}

func TestVocabulary_LocalKeysAreUniqueAcrossViews(t *testing.T) {
	sweepEnv(t)
	owners := make(map[string]map[string]bool)
	for _, s := range scopeBuilders(t) {
		sets, _ := scopeSets(t, s)
		for _, ns := range sets {
			for _, b := range ns.bindings() {
				m, ok := kernel.MintOf(b)
				if !ok || !m.Local {
					continue
				}
				for _, k := range b.Keys() {
					if owners[k] == nil {
						owners[k] = map[string]bool{}
					}
					owners[k][m.Owner] = true
				}
			}
		}
	}
	for k, who := range owners {
		if len(who) > 1 {
			t.Errorf("%q is local to more than one view: %v", k, who)
		}
	}
}

func TestVocabulary_TheCheckSeesWhatItIsMeantTo(t *testing.T) {
	stray := kernel.Bind([]string{"q"}, "q", "stray")
	local := kernel.Local("probe", "sort", []string{"s"}, "s", "sort differently")
	fine := kernel.Local("probe", "unique", []string{"F"}, "F", "quick filters")
	canon := kernel.Canon(kernel.ActSort)
	relabelled := kernel.Bind(canon.Keys(), "S", "sort")
	terse := kernel.Terse(canon, "sort it")
	got := vocabularyFindings([]namedSet{
		{"probe", kernel.KeySet{Acts: []kernel.Binding{stray, local, fine, canon, relabelled, terse}}},
		{"modal", kernel.KeySet{Mode: kernel.Modal, Acts: []kernel.Binding{kernel.Bind([]string{"y"}, "y", "yes")}}},
	})
	want := []string{
		`canon sort is labelled "S", not "s"`,
		"local probe.sort is named for sort: s | sort differently",
		`local probe.sort uses "s", which is sort: s | sort differently`,
		"unminted: q | stray",
	}
	if !slices.Equal(got, want) {
		t.Errorf("findings = %q, want %q", got, want)
	}
}

func TestVocabulary_TheTableIsConsistent(t *testing.T) {
	all := kernel.Vocabulary()
	if len(all) == 0 {
		t.Fatal("the vocabulary is empty")
	}
	actions := make(map[kernel.Action]bool)
	aliases := make(map[string]kernel.Action)
	for _, c := range all {
		if actions[c.Action] {
			t.Errorf("%s is in the table twice", c.Action)
		}
		actions[c.Action] = true
		if len(c.Keys) == 0 || c.Label == "" || c.Desc == "" {
			t.Errorf("%s is missing keys, a label or a description", c.Action)
		}
		for _, alias := range c.Aliases {
			if other, dup := aliases[alias]; dup {
				t.Errorf("alias %q names both %s and %s", alias, other, c.Action)
			}
			aliases[alias] = c.Action
		}
		if c.Variant != "" {
			base, ok := canonByAction[c.Variant]
			if !ok {
				t.Errorf("%s is a variant of %q, which is not in the table", c.Action, c.Variant)
			} else if !slices.Contains(base.Keys, strings.ToLower(c.Keys[0])) && c.Action != kernel.ActBottom {
				t.Errorf("%s (%s) is not the capital of %s (%v)", c.Action, c.Keys[0], c.Variant, base.Keys)
			}
		}
		b := kernel.Canon(c.Action)
		if _, ok := kernel.Stroke(b); !ok {
			t.Errorf("%s: the kernel cannot spell %v back into a keypress", c.Action, c.Keys)
		}
		for _, k := range c.Keys {
			if _, ok := kernel.Stroke(kernel.Bind([]string{k}, k, "")); !ok {
				t.Errorf("%s: the kernel cannot spell %q back into a keypress", c.Action, k)
			}
		}
	}

	owned := make(map[string]kernel.Action)
	for _, c := range all {
		if c.Modal {
			continue
		}
		for _, k := range c.Keys {
			if slices.Contains(c.Prefixed, k) {
				continue
			}
			if other, taken := owned[k]; taken {
				t.Errorf("%q belongs to both %s and %s", k, other, c.Action)
			}
			owned[k] = c.Action
		}
	}
	if len(kernel.SortPickerKeys()) != 4 {
		t.Errorf("the sort picker has %d keys, want 4", len(kernel.SortPickerKeys()))
	}
}

func TestVocabulary_SymbolStrokesRoundTrip(t *testing.T) {
	for _, stroke := range []string{"!", "&", "#", "*", ".", "]"} {
		b := kernel.Bind([]string{stroke}, stroke, "")
		press, ok := kernel.Stroke(b)
		if !ok {
			t.Errorf("%q cannot be spelt as a keypress", stroke)
			continue
		}
		if press.String() != stroke {
			t.Errorf("%q arrives as %q", stroke, press.String())
		}
		if !kernel.Matches(press, b) {
			t.Errorf("a keypress built for %q does not match the binding it came from", stroke)
		}
	}
}

var commandScope = map[string]string{
	"issues":      "list",
	"attachments": "attach",
	"comments":    "comment",
}

func commandFindings(cmds []kernel.Command) map[string][]string {
	slots := make(map[string]bool)
	for slot := 1; slot <= 9; slot++ {
		slots[kernel.SlotGesture(slot)] = true
	}
	out := make(map[string][]string)
	for _, cmd := range cmds {
		scope, _, _ := strings.Cut(cmd.ID, ".")
		if renamed, ok := commandScope[scope]; ok {
			scope = renamed
		}
		for _, c := range vocabulary {
			for _, alias := range c.Aliases {
				if strings.HasSuffix(cmd.ID, alias) && cmd.Action != c.Action {
					out[scope] = append(out[scope], fmt.Sprintf("command %s ends in %q and does not set Action %s", cmd.ID, alias, c.Action))
				}
			}
		}
		if cmd.Action == "" {
			continue
		}
		for _, k := range cmd.Keys {
			if k != canonByAction[cmd.Action].Label && !slots[k] {
				out[scope] = append(out[scope], fmt.Sprintf("command %s shows %q, not the canonical %q", cmd.ID, k, canonByAction[cmd.Action].Label))
			}
		}
	}
	return out
}

func TestVocabulary_CommandsNameTheirAction(t *testing.T) {
	if len(kernel.Commands()) == 0 {
		t.Fatal("no command is registered, so this sweep is checking nothing")
	}
	got := commandFindings([]kernel.Command{
		{ID: "probe.assign"},
		{ID: "probe.sort", Action: kernel.ActSort, Keys: []string{"S"}},
		{ID: "probe.new", Action: kernel.ActCreate, Keys: []string{"c"}},
		{ID: "probe.open", Action: kernel.ActSlot, Keys: []string{kernel.SlotGesture(3)}},
	})
	want := []string{
		`command probe.assign ends in ".assign" and does not set Action assign`,
		`command probe.sort shows "S", not the canonical "s"`,
	}
	if !slices.Equal(got["probe"], want) {
		t.Errorf("findings = %q, want %q", got["probe"], want)
	}
}

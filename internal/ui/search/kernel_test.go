package search

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/list"
)

func session(t *testing.T, d kernel.Deps, w, h int, seed Seed) kernel.Model {
	t.Helper()
	m, err := kernel.New(d, kernel.WithSize(w, h), kernel.WithInitialView(list.ViewID), kernel.WithMouse(false),
		kernel.WithInitialPush(ViewID, "Search", func(d kernel.Deps) kernel.View {
			return New(d, seed, withAfter(immediately))
		}))
	if err != nil {
		t.Fatalf("kernel.New: %v", err)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	km := next.(kernel.Model)
	return drain(t, km, km.Init())
}

func drain(t *testing.T, m kernel.Model, cmd tea.Cmd) kernel.Model {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for steps := 0; len(queue) > 0; steps++ {
		if steps > 4000 {
			t.Fatal("commands never settled")
		}
		next := queue[0]
		queue = queue[1:]
		if next == nil {
			continue
		}
		msg := next()
		if msg == nil {
			continue
		}
		if cmds, ok := unwrapCmds(msg); ok {
			queue = append(queue, cmds...)
			continue
		}
		updated, follow := m.Update(msg)
		m = updated.(kernel.Model)
		queue = append(queue, follow)
	}
	return m
}

func press(t *testing.T, m kernel.Model, keys ...string) kernel.Model {
	t.Helper()
	for _, k := range keys {
		next, cmd := m.Update(keyPress(k))
		m = drain(t, next.(kernel.Model), cmd)
	}
	return m
}

func typed(t *testing.T, m kernel.Model, text string) kernel.Model {
	t.Helper()
	for _, r := range text {
		next, cmd := m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = drain(t, next.(kernel.Model), cmd)
	}
	return m
}

func TestKernel_TheKeysTheProgramOwnsLandInTheBoxWhileTyping(t *testing.T) {
	t.Parallel()
	d := testDeps(newFake(baseIssues()))
	m := session(t, d, 120, 30, Seed{})
	if _, ok := m.Top().(*Model); !ok {
		t.Fatalf("the top of the stack is %T, not the search", m.Top())
	}
	m = typed(t, m, "qr1j2k?g/i")
	s := m.Top().(*Model)
	if got := s.input.Value(); got != "qr1j2k?g/i" {
		t.Fatalf("typed qr1j2k?g/i and the box holds %q; the program took the rest", got)
	}
	if m.Top() != kernel.View(s) {
		t.Error("a typed letter changed which view is on top")
	}
}

func TestKernel_EscClosesTheSearchAndTheListIsBack(t *testing.T) {
	t.Parallel()
	m := session(t, testDeps(newFake(baseIssues())), 120, 30, Seed{})
	m = press(t, m, "esc")
	if _, still := m.Top().(*Model); still {
		t.Fatal("esc left the search open")
	}
}

func TestKernel_LHandsTheSearchToTheIssueList(t *testing.T) {
	t.Parallel()
	m := session(t, testDeps(newFake(baseIssues())), 120, 30, Seed{Query: "login"})
	m = press(t, m, "down", "L")
	top, ok := m.Top().(*list.Model)
	if !ok {
		t.Fatalf("L left %T on top, want the issue list", m.Top())
	}
	got := ansi.Strip(top.View())
	mustContain(t, got, `Search "login"`, "PROJ-1")
}

func TestKernel_Goldens(t *testing.T) {
	t.Parallel()
	t.Run("typing", func(t *testing.T) {
		t.Parallel()
		d := unicodeDeps(goldenSite())
		m := session(t, d, 120, 30, Seed{})
		m = typed(t, m, "login")
		golden(t, "session_search_typing_120x30.golden", ansi.Strip(m.Frame())+"\n")
	})
	t.Run("browsing", func(t *testing.T) {
		t.Parallel()
		d := unicodeDeps(goldenSite())
		m := session(t, d, 120, 30, Seed{Query: "login"})
		m = press(t, m, "down", "j")
		golden(t, "session_search_browse_120x30.golden", ansi.Strip(m.Frame())+"\n")
	})
}

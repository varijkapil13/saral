package onboarding

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// benchView builds the view at a step without going near a test's helpers, so
// that the benchmark measures the render and nothing around it.
func benchView(b *testing.B, w, h int, to step) kernel.View {
	b.Helper()
	deps := kernel.Deps{Theme: kernel.NewTheme(kernel.ThemeDark, true, kernel.UnicodeGlyphs())}
	fake := jiratest.New(
		jiratest.WithProject("PROJ", jiratest.Scrum),
		jiratest.WithIssues(jiratest.Gen(6)),
		jiratest.WithCapabilities(jiratest.NoBulkMove, jiratest.NoPlans),
	)
	v := NewWith(deps, connectorFor(fake))
	v, _ = v.Update(kernel.SizeMsg{Width: w, Height: h})

	m, ok := v.(Model)
	if !ok {
		b.Fatalf("the view is a %T", v)
	}
	m.setValue(fieldSite, "example.atlassian.net")
	m.setValue(fieldEmail, "you@example.com")
	m.setValue(fieldToken, "9d8f7a6b5c4d3e2f1a0b")
	m.setValue(fieldProject, "PROJ")
	m.suggested = []string{"PROJ", "OTHER", "THIRD"}
	m.probed, m.project = true, "PROJ"
	caps, err := fake.Capabilities(b.Context(), "PROJ")
	if err != nil {
		b.Fatalf("probing the fake: %v", err)
	}
	m.caps = caps
	_ = m.goTo(to)
	return m
}

func BenchmarkView(b *testing.B) {
	for name, tc := range map[string]struct {
		w, h int
		step step
	}{
		"site at 200x60":    {200, 60, stepSite},
		"store at 200x60":   {200, 60, stepStorage},
		"project at 200x60": {200, 60, stepProject},
		"review at 200x60":  {200, 60, stepReview},
		"review at 80x20":   {80, 20, stepReview},
	} {
		b.Run(name, func(b *testing.B) {
			v := benchView(b, tc.w, tc.h, tc.step)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				_ = v.View()
			}
		})
	}
}

// BenchmarkKeyToFrame is the budgeted path: one keystroke into a field, then
// the frame that comes out of it.
func BenchmarkKeyToFrame(b *testing.B) {
	v := benchView(b, 200, 60, stepSite)
	key := tea.KeyPressMsg{Code: 'x', Text: "x"}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		next, _ := v.Update(key)
		_ = next.View()
	}
}

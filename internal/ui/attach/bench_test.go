package attach

import (
	"strconv"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

// manyFiles is a long attachment list, built rather than fetched.
func manyFiles(n int) []jira.Attachment {
	created := time.Date(2026, time.March, 2, 9, 0, 0, 0, time.UTC)
	out := make([]jira.Attachment, 0, n)
	for i := range n {
		out = append(out, jira.Attachment{
			ID:       "att-" + strconv.Itoa(i),
			Filename: "capture-" + strconv.Itoa(i%37) + "-" + strconv.Itoa(i) + ".png",
			MimeType: "image/png",
			Size:     int64(1024 + i*17),
			Created:  created.Add(time.Duration(i) * time.Minute),
			Author:   jira.User{AccountID: "acct-" + strconv.Itoa(i%9), DisplayName: "Someone Else"},
		})
	}
	return out
}

// stocked is a pane already holding a list, with a zone manager and no site: the
// files arrive as the message a read would have produced, which is what keeps a
// benchmark off the network and off a fake's locks.
//
// The manager is the shape a running program has, so every row and the preview
// region carry a marker — which is the steady state the budget has to hold in.
func stocked(tb testing.TB, n, w, h int) *Model {
	tb.Helper()
	mgr := zone.New()
	tb.Cleanup(mgr.Close)
	d := kernel.Deps{
		Caps:    jira.Capabilities{Attachments: jira.Capability{OK: true}, TimeZone: time.UTC},
		Project: "PROJ",
		Theme:   kernel.NewTheme(kernel.ThemeDark, true, kernel.UnicodeGlyphs()),
		Zones:   mgr,
		Site:    "example.atlassian.net",
		Now:     func() time.Time { return time.Date(2026, time.March, 5, 9, 0, 0, 0, time.UTC) },
	}
	m, ok := New(d, WithIssue("PROJ-1")).(*Model)
	if !ok {
		tb.Fatal("New did not return a *Model")
	}
	m.tools = tools{}
	next, _ := m.Update(kernel.SizeMsg{Width: w, Height: h})
	m, _ = next.(*Model)
	next, _ = m.Update(listedMsg{gen: m.gen, files: manyFiles(n)})
	m, _ = next.(*Model)
	_ = m.View()
	return m
}

// BenchmarkPaneKeystroke is the path a reader spends the most keys on: the cursor
// moves and the frame is drawn again.
func BenchmarkPaneKeystroke(b *testing.B) {
	m := stocked(b, 2000, 120, 40)
	var down, top tea.Msg = keyPress("j"), keyPress("home")
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		next, _ := m.Update(down)
		m, _ = next.(*Model)
		_ = m.View()
		// Back to the top on reaching the bottom: without it every iteration past
		// the two-thousandth is a memo hit rather than the miss this measures, and
		// the number becomes a fact about the machine.
		if m.cursor >= len(m.files)-1 {
			next, _ = m.Update(top)
			m, _ = next.(*Model)
		}
	}
}

func BenchmarkPaneRedraw200x60(b *testing.B) {
	m := stocked(b, 2000, 200, 60)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = m.View()
	}
}

func BenchmarkRowRender(b *testing.B) {
	m := stocked(b, 2000, 120, 40)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.memo.reset()
		_ = m.row(0)
	}
}

// scrollOver is the steady state: the window moves between two rows both already
// rendered, so every frame is a memo hit and what is left is the frame itself.
func scrollOver(b *testing.B, n int) {
	m := stocked(b, n, 120, 40)
	var down, up tea.Msg = keyPress("down"), keyPress("up")
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		key := down
		if i%2 == 1 {
			key = up
		}
		next, _ := m.Update(key)
		m, _ = next.(*Model)
		_ = m.View()
	}
}

func BenchmarkPaneSteadyScroll2000(b *testing.B) { scrollOver(b, 2000) }

func BenchmarkPaneSteadyScroll20(b *testing.B) { scrollOver(b, 20) }

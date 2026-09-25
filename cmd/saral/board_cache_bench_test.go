package main

import (
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/store"
	"github.com/varijkapil13/saral/internal/ui/board"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// update-ns/op is the time spent inside Update, which is the frame loop.
func BenchmarkBoardPageLoads_IntoADiskCache(b *testing.B) {
	db, err := store.Open(filepath.Join(b.TempDir(), "cache.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = db.Close() })
	cache := app.NewCache(db, store.Scope{Site: "example.atlassian.net", Account: "bench"})
	fake := jiratest.New(
		jiratest.WithProject("PROJ", jiratest.Kanban),
		jiratest.WithIssues(jiratest.Gen(2000)),
		jiratest.WithPageSize(100),
	)
	deps := kernel.Deps{
		Jira:    fake,
		Caps:    jira.Capabilities{Boards: jira.Capability{OK: true}, TimeZone: time.UTC},
		Project: "PROJ",
		Theme:   kernel.NewTheme(kernel.ThemeNoColor, true, kernel.ASCIIGlyphs()),
		Cache:   cache,
	}

	var inUpdate time.Duration
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		view := board.New(deps)
		inUpdate += drive(b, view, view.Init())
	}
	b.ReportMetric(float64(inUpdate.Nanoseconds())/float64(b.N), "update-ns/op")
}

func drive(b *testing.B, view kernel.View, cmd tea.Cmd) time.Duration {
	b.Helper()
	var spent time.Duration
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		if next == nil {
			continue
		}
		msg := next()
		switch msg := msg.(type) {
		case nil, kernel.StatusMsg:
			continue
		case tea.BatchMsg:
			queue = append(queue, msg...)
			continue
		case kernel.ReplyMsg:
			start := time.Now()
			var follow tea.Cmd
			view, follow = view.Update(msg.Msg)
			spent += time.Since(start)
			queue = append(queue, follow)
		}
	}
	return spent
}

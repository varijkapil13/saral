package timeline

import (
	"context"

	tea "charm.land/bubbletea/v2"

	apptimeline "github.com/varijkapil13/saral/internal/app/timeline"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

type loadedMsg struct {
	gen int
	apptimeline.Loaded
}

// markersMsg carries the version and sprint boundaries drawn above the bars.
// They are a second read because neither of them is worth failing a chart over:
// Notes says which of them could not be had and why.
type markersMsg struct {
	gen int
	apptimeline.Markers
}

// failedMsg is a read that brought no chart back. The error travels whole so the
// refusal reaches the user in the words the site used.
type failedMsg struct {
	gen int
	err error
}

func load(ctx context.Context, loader *apptimeline.Loader, cfg apptimeline.Config, jql string, gen int) tea.Cmd {
	return func() tea.Msg {
		loaded, err := loader.Load(ctx, jql, cfg)
		if err != nil {
			return failedMsg{gen: gen, err: err}
		}
		return loadedMsg{gen: gen, Loaded: loaded}
	}
}

func markers(ctx context.Context, loader *apptimeline.Loader, project string, caps jira.Capabilities, gen int) tea.Cmd {
	return func() tea.Msg {
		marks, err := loader.Markers(ctx, project, caps)
		if err != nil {
			return nil
		}
		return markersMsg{gen: gen, Markers: marks}
	}
}

func notStored(err error) tea.Cmd {
	if err == nil {
		return nil
	}
	return kernel.Warn("these issues could not be stored for next time: " + err.Error())
}

// withCancel makes a command release its context however it ends. The cancel is
// also held on the model so that the next request can cut this one short.
func withCancel(cancel context.CancelFunc, cmd tea.Cmd) tea.Cmd {
	return func() tea.Msg {
		defer cancel()
		return cmd()
	}
}

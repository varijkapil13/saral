package timeline

import (
	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

func init() {
	const slot = 6
	keys := defaultKeys()
	kernel.RegisterView(kernel.ViewSpec{
		ID:    ViewID,
		Title: "Timeline",
		Slot:  slot,
		New:   New,
	})
	kernel.RegisterKeys(ViewID, keys.keySet())
	kernel.RegisterCommand(kernel.Command{
		ID:    "timeline.open",
		Title: "Timeline",
		Group: "Go to",
		Kind:  kernel.KindGoTo,
		Keys:  []string{kernel.SlotGesture(slot)},
		Run:   func(kernel.Deps) tea.Cmd { return kernel.Open(ViewID) },
	})
	kernel.RegisterCommand(kernel.Command{
		ID:    "timeline.zoom-in",
		Title: "Zoom the timeline in to a shorter period",
		Group: "Timeline",
		Keys:  []string{keys.ZoomIn.Help().Key},
		Run:   open(ZoomStepMsg{Finer: true}),
	})
	kernel.RegisterCommand(kernel.Command{
		ID:    "timeline.zoom-out",
		Title: "Zoom the timeline out to a longer period",
		Group: "Timeline",
		Keys:  []string{keys.ZoomOut.Help().Key},
		Run:   open(ZoomStepMsg{}),
	})
	for _, z := range []Zoom{ZoomDay, ZoomWeek, ZoomMonth, ZoomQuarter} {
		kernel.RegisterCommand(kernel.Command{
			ID:    "timeline.zoom." + z.String(),
			Title: "Timeline: one column is one " + z.String(),
			Group: "Timeline",
			Run:   open(ZoomMsg{Zoom: z}),
		})
	}
	kernel.RegisterCommand(kernel.Command{
		ID:    "timeline.today",
		Title: "Centre the timeline on today",
		Group: "Timeline",
		Keys:  []string{keys.Today.Help().Key},
		Run:   open(TodayMsg{}),
	})
	kernel.RegisterCommand(kernel.Command{
		ID:    "timeline.notes",
		Title: "Say where the timeline's dates came from",
		Group: "Timeline",
		Keys:  []string{keys.Notes.Help().Key},
		Run:   open(NotesMsg{}),
	})
}

// open switches to the chart and then names what to do to it. The palette knows
// which command was run and never which view is on screen, so the instruction
// travels as a broadcast rather than as a pointer.
func open(msg tea.Msg) func(kernel.Deps) tea.Cmd {
	return func(kernel.Deps) tea.Cmd {
		return tea.Sequence(kernel.Open(ViewID), kernel.Broadcast(msg))
	}
}

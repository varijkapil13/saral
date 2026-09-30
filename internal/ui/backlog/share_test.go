package backlog

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/uitest"
	"github.com/varijkapil13/saral/internal/ui/widget/card"
)

func unreply(msg tea.Msg) tea.Msg {
	if reply, ok := msg.(kernel.ReplyMsg); ok {
		return reply.Msg
	}
	return msg
}

func TestBacklog_RightClickingARowThenCopyLinkCopiesThatRowsLink(t *testing.T) {
	t.Parallel()

	for _, look := range []card.Look{card.Lines, card.Roomy} {
		t.Run(look.Word(), func(t *testing.T) {
			d := testDeps(newFake(12))
			m, err := kernel.New(d, kernel.WithSize(120, 38), kernel.WithInitialView(ViewID), kernel.WithMouse(true))
			if err != nil {
				t.Fatal(err)
			}
			m = drainK(t, m, m.Init())
			m = sendK(t, m, tea.WindowSizeMsg{Width: 120, Height: 38})
			m = sendK(t, m, kernel.BroadcastMsg{Msg: card.LookMsg{Look: look}})
			bm := m.Top().(*Model)
			if bm.look != look {
				t.Fatalf("the backlog is in %s, not %s", bm.look.Word(), look.Word())
			}

			target := -1
			for i := bm.top; i < bm.visibleEnd(); i++ {
				if !bm.rows[i].head && i != bm.cursor {
					target = i
				}
			}
			if target < 0 {
				t.Fatal("no issue row on screen other than the cursor's")
			}
			key := bm.issues[bm.rows[target].issue].Key

			at := uitest.ZoneDrawn(t, d.Zones, func() { _ = m.Frame() }, bm.zones.ID(bm.zoneOf(target)))
			y := at.StartY
			if look.Cards() {
				if at.EndY <= at.StartY {
					t.Fatalf("a %s card is one line tall", look.Word())
				}
				y++
			}
			m = sendK(t, m, tea.MouseClickMsg{X: at.StartX + 1, Y: y, Button: tea.MouseRight})
			if got := m.Top().(*Model).underKey(); got != key {
				t.Fatalf("the right-click left the cursor on %q, want %s", got, key)
			}

			for range uitest.MenuEntry(t, m.Frame(), "copy the link") {
				m = sendK(t, m, keyPress("down"))
			}
			_, cmd := m.Update(keyPress("enter"))
			link, err := kernel.IssueURL(d.Site, key)
			if err != nil {
				t.Fatal(err)
			}
			if got := uitest.CopiedBy(cmd, unreply); len(got) != 1 || got[0] != link {
				t.Errorf("Copy link copied %q, want %q", got, link)
			}
		})
	}
}

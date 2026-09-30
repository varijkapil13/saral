package list

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

func TestList_RightClickingARowThenCopyLinkCopiesThatRowsLink(t *testing.T) {
	t.Parallel()

	for _, look := range []card.Look{card.Lines, card.Roomy} {
		t.Run(look.Word(), func(t *testing.T) {
			d := testDeps(newFake(20))
			m := startAll(t, d, 120, 38, kernel.WithMouse(true))
			m = send(t, m, kernel.BroadcastMsg{Msg: card.LookMsg{Look: look}})
			lm := m.Top().(*Model)
			if lm.look != look {
				t.Fatalf("the list is in %s, not %s", lm.look.Word(), look.Word())
			}
			if got := lm.selectedKey(); got == "PROJ-3" {
				t.Fatal("the cursor starts on the row this right-clicks, which proves nothing")
			}

			at := uitest.ZoneDrawn(t, d.Zones, func() { _ = m.Frame() }, lm.zones.ID(rowZone("PROJ-3")))
			y := at.StartY
			if look.Cards() {
				if at.EndY <= at.StartY {
					t.Fatalf("a %s card is one line tall", look.Word())
				}
				y++
			}
			m = send(t, m, tea.MouseClickMsg{X: at.StartX + 2, Y: y, Button: tea.MouseRight})
			if got := m.Top().(*Model).selectedKey(); got != "PROJ-3" {
				t.Fatalf("the right-click left the cursor on %s", got)
			}

			for range uitest.MenuEntry(t, m.Frame(), "copy the link") {
				m = send(t, m, keyPress("down"))
			}
			_, cmd := m.Update(keyPress("enter"))
			link, err := kernel.IssueURL(d.Site, "PROJ-3")
			if err != nil {
				t.Fatal(err)
			}
			if got := uitest.CopiedBy(cmd, unreply); len(got) != 1 || got[0] != link {
				t.Errorf("Copy link copied %q, want %q", got, link)
			}
		})
	}
}

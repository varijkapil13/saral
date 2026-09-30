package board

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

func TestBoard_RightClickingACardThenCopyLinkCopiesThatCardsLink(t *testing.T) {
	t.Parallel()

	for _, look := range []card.Look{card.Lines, card.Roomy} {
		t.Run(look.Word(), func(t *testing.T) {
			d := testDeps(newFake(24))
			m, err := kernel.New(d, kernel.WithSize(140, 30), kernel.WithInitialView(ViewID), kernel.WithMouse(true))
			if err != nil {
				t.Fatal(err)
			}
			m = drainK(t, m, m.Init())
			m = sendK(t, m, tea.WindowSizeMsg{Width: 140, Height: 30})
			m = sendK(t, m, kernel.BroadcastMsg{Msg: card.LookMsg{Look: look}})
			bm := m.Top().(*Model)
			if bm.look != look {
				t.Fatalf("the board is in %s, not %s", bm.look.Word(), look.Word())
			}
			target := bm.issueAt(1, 0)
			if target == nil {
				t.Fatal("the second column holds no card")
			}
			key := target.Key
			if bm.selectedKey() == key {
				t.Fatal("the cursor starts on the card this right-clicks, which proves nothing")
			}

			at := uitest.ZoneDrawn(t, d.Zones, func() { _ = m.Frame() }, bm.zones.ID(cardZone(key)))
			y := at.StartY
			if look.Cards() {
				if at.EndY <= at.StartY {
					t.Fatalf("a %s card is one line tall", look.Word())
				}
				y++
			}
			m = sendK(t, m, tea.MouseClickMsg{X: at.StartX + 1, Y: y, Button: tea.MouseRight})
			if got := m.Top().(*Model).selectedKey(); got != key {
				t.Fatalf("the right-click left the cursor on %s, want %s", got, key)
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

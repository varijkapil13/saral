package issue

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestPane_TheMenuOffersEveryShareActAndARightClickOnAFactOpensNothing(t *testing.T) {
	t.Parallel()
	f := newFake(6)
	iss := firstAssignedIssue(t, f, 6)
	allEditableWithChoices(f, iss.Key, fakePriorityOptions...)
	d := testDeps(t, record(f))
	p := newPanel(t, New(d, readIssue(t, f, iss.Key), withDrafts(tempDrafts(t))), 120, 30)
	p.send(loadedMsg{gen: p.editor().gen, issue: readIssue(t, f, iss.Key)})

	at := p.zoneAt(d, zoneFactStatus)
	p.send(tea.MouseClickMsg{X: at.StartX + 2, Y: at.StartY, Button: tea.MouseRight})
	if m := p.editor(); m.stage != sideBrowse {
		t.Fatalf("a right-click on the status opened stage %v", m.stage)
	}

	set, _ := p.editor().LiveKeys()
	menu := make([]string, 0, len(set.Menu))
	for _, b := range set.Menu {
		menu = append(menu, b.Help().Key)
	}
	if got := strings.Join(menu, " "); got != "y Y o" {
		t.Errorf("the pane's menu offers %q, want y Y o", got)
	}
	row := make([]string, 0, len(set.Acts))
	for _, b := range set.Acts {
		row = append(row, b.Help().Key+" "+b.Help().Desc)
	}
	if !slices.Contains(row, "Y link") {
		t.Errorf("the pane's row is %q, with no Y link", row)
	}
}

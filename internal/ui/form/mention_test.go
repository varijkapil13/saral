package form

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/pkg/adf"
)

func TestForm_ADocumentFieldOffersMentions(t *testing.T) {
	t.Parallel()

	dr := openOn(t, testDeps(t, newFake(0)), 100, 30, fakeStory)
	dr.m.after = func(_ time.Duration, fn func() tea.Msg) tea.Cmd { return fn }
	dr.focus("description")
	dr.key("enter")
	if dr.m.edit != editDoc {
		t.Fatalf("enter on the description opened %v", dr.m.edit)
	}
	dr.typeText("see @gr")
	mustContain(t, dr.view(), "@Grace Hopper")
	dr.key("enter")
	if got := dr.m.area.Value(); got != "see "+adf.MentionMarkdown("Grace Hopper", "acct-grace")+" " {
		t.Fatalf("editor holds %q", got)
	}
	dr.key("esc")
	v, ok := dr.field("description").value()
	if !ok {
		t.Fatal("the description holds nothing")
	}
	body, _ := adf.Marshal(v.Doc)
	if !strings.Contains(string(body), `"type":"mention"`) {
		t.Fatalf("no mention node: %s", body)
	}
}

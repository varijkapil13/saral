package comment

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func rightAway(_ time.Duration, fn func() tea.Msg) tea.Cmd { return fn }

func peopleDeps(t *testing.T, f *jiratest.Fake) kernel.Deps {
	t.Helper()
	d := testDeps(t, f)
	d.Caps.People = jira.Capability{OK: true}
	return d
}

func TestComposer_ATokenThatCannotLookPeopleUpSaysSo(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	dr := newDriver(t, testDeps(t, f), "PROJ-1", 100, 24)
	dr.m.after = rightAway
	dr.key("a")
	dr.typeText("@gr")
	mustContain(t, dr.view(), "this token cannot look accounts up")
	for _, c := range f.Calls() {
		if c == "FindPeople" {
			t.Fatal("a token without the capability asked the site")
		}
	}
}

func TestComposer_AtOffersPeopleAndSendsARealMention(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	dr := newDriver(t, peopleDeps(t, f), "PROJ-1", 100, 24)
	dr.m.after = rightAway
	dr.key("a")
	dr.typeText("thanks @gr")
	mustContain(t, dr.view(), "@Grace Hopper", "enter or tab mentions")
	dr.key("enter")
	if got := dr.m.editor.Value(); got != "thanks "+adf.MentionMarkdown("Grace Hopper", "acct-grace")+" " {
		t.Fatalf("editor holds %q", got)
	}
	dr.key("ctrl+s")

	page, err := f.Comments(t.Context(), "PROJ-1")
	if err != nil {
		t.Fatal(err)
	}
	all, err := jira.Collect(t.Context(), page, 0)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := adf.Marshal(all[len(all)-1].Body)
	if !strings.Contains(string(body), `"type":"mention"`) || !strings.Contains(string(body), "acct-grace") {
		t.Fatalf("the comment carries no mention: %s", body)
	}
}

func TestComposer_EscClosesSuggestionsBeforeTheEditor(t *testing.T) {
	t.Parallel()

	dr := newDriver(t, testDeps(t, newFake(3)), "PROJ-1", 100, 24)
	dr.m.after = rightAway
	dr.key("a")
	dr.typeText("@ad")
	dr.key("esc")
	if dr.m.mode != writing || dr.m.mention.Open() {
		t.Fatalf("mode %v, open %v: esc should close only the suggestions", dr.m.mode, dr.m.mention.Open())
	}
	dr.key("esc")
	if dr.m.mode != browsing {
		t.Fatal("the second esc did not put the editor away")
	}
}

// editDraft leaves an edit of the thread's one comment as a draft, the way a
// session that stopped mid-edit does.
func editDraft(t *testing.T, dr *driver, text string) {
	t.Helper()
	dr.key("e")
	dr.typeText(text)
	dr.key("esc")
}

func TestComposer_ARestoredEditIsCheckedAgainstTheBodyItWasWrittenOn(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		moved bool
	}{
		{"the comment moved on the site", true},
		{"the comment is as it was", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFake(3)
			c := comment(t, f, "PROJ-1", "first version")
			d := testDeps(t, f)
			editDraft(t, newDriver(t, d, "PROJ-1", 100, 24), " plus mine")
			if tc.moved {
				if _, err := f.EditComment(t.Context(), "PROJ-1", c.ID, doc("somebody else's version")); err != nil {
					t.Fatal(err)
				}
			}

			dr := newDriver(t, testDeps(t, f), "PROJ-1", 100, 24)
			dr.key("e")
			if !strings.Contains(dr.m.editor.Value(), "plus mine") {
				t.Fatalf("the draft was not restored: %q", dr.m.editor.Value())
			}
			if got := dr.statusText(); (got == staleNote) != tc.moved {
				t.Fatalf("status %q", got)
			}
			dr.key("ctrl+s")
			stored := lastBody(t, dr)
			if tc.moved {
				if strings.Contains(stored, "plus mine") {
					t.Fatal("a draft written on an older body was sent without a word")
				}
				mustContain(t, dr.statusText(), "ctrl+s again")
				dr.key("ctrl+s")
				stored = lastBody(t, dr)
			}
			if !strings.Contains(stored, "plus mine") {
				t.Fatalf("the edit was never sent: %s", stored)
			}
		})
	}
}

func TestComposer_ADraftWithNoRecordedBaseIsTreatedAsStale(t *testing.T) {
	t.Parallel()

	f := newFake(3)
	c := comment(t, f, "PROJ-1", "first version")
	d := testDeps(t, f)
	dr := newDriver(t, d, "PROJ-1", 100, 24)
	if err := dr.m.drafts.write(draftKey{site: d.Site, issue: "PROJ-1", comment: c.ID}, "an old draft", ""); err != nil {
		t.Fatal(err)
	}
	dr.key("e")
	if !dr.m.stale || dr.statusText() != staleNote {
		t.Fatalf("stale %v, status %q", dr.m.stale, dr.statusText())
	}
}

func lastBody(t *testing.T, dr *driver) string {
	t.Helper()
	page, err := dr.m.deps.Jira.Comments(t.Context(), "PROJ-1")
	if err != nil {
		t.Fatal(err)
	}
	all, err := jira.Collect(t.Context(), page, 0)
	if err != nil {
		t.Fatal(err)
	}
	return adf.Markdown(all[len(all)-1].Body)
}

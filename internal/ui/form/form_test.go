package form

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

func TestForm_BuildsEveryFieldFromWhatTheSiteSaysTheScreenHas(t *testing.T) {
	t.Parallel()

	c := newFake(20)
	dr := openOn(t, testDeps(c), 100, 24, fakeStory)

	schema, err := c.CreateMeta(t.Context(), "PROJ", fakeStory)
	if err != nil {
		t.Fatalf("reading the create screen: %v", err)
	}
	offered := 0
	for _, meta := range schema.Fields {
		if ok, _ := offer(meta, "PROJ", dr.m.chosen); ok {
			offered++
		}
	}
	if len(dr.m.fields) != offered {
		t.Errorf("the form has %d fields, want the %d the screen offers: %v", len(dr.m.fields), offered, dr.ids())
	}

	// The fake's custom field ids are not the ones a stock site allocates, so a
	// form that wrote one down would find nothing here.
	points := dr.field("customfield_13401")
	if points.kind != kindNumber {
		t.Errorf("the story point field is a %v, want a number", points.kind)
	}
	for _, f := range dr.m.fields {
		if f.id() == "customfield_10016" {
			t.Error("a field id from another site turned up on this form")
		}
	}
}

func TestForm_GivesEachSchemaTypeTheWidgetItEarns(t *testing.T) {
	t.Parallel()

	dr := openOn(t, testDeps(newFake(20)), 100, 24, fakeStory)

	tests := map[string]kind{
		"summary":           kindText,
		"description":       kindDoc,
		"assignee":          kindUser,
		"labels":            kindLabels,
		"priority":          kindSelect,
		"duedate":           kindDate,
		"parent":            kindIssueKey,
		"customfield_13401": kindNumber,
	}
	for id, want := range tests {
		if got := dr.field(id).kind; got != want {
			t.Errorf("%s is a %v, want a %v", id, got, want)
		}
	}
}

func TestForm_AsksADifferentScreenForADifferentIssueType(t *testing.T) {
	t.Parallel()

	story := openOn(t, testDeps(newFake(20)), 100, 24, fakeStory)
	if story.field("parent").meta.Required {
		t.Error("a parent is required on a story, and only a subtask needs one")
	}

	sub := openOn(t, testDeps(newFake(20)), 100, 24, fakeSubtask)
	if !sub.field("parent").meta.Required {
		t.Error("a parent is not required on a subtask, and the screen says it is")
	}
	if sub.m.chosen.ID == story.m.chosen.ID {
		t.Fatal("both forms opened on the same issue type")
	}
}

func TestForm_DoesNotOfferAFieldItCannotSetAndSaysWhy(t *testing.T) {
	t.Parallel()

	dr := openOn(t, testDeps(newFake(20)), 100, 24, fakeStory)

	if len(dr.m.hidden) == 0 {
		t.Fatal("every field on the screen was offered, including the ones this form fixes itself")
	}
	for _, id := range []string{"project", "issuetype"} {
		for _, f := range dr.m.fields {
			if f.id() == id {
				t.Errorf("%s is on the form, and it is fixed by the screen the form was opened for", id)
			}
		}
	}
	for _, h := range dr.m.hidden {
		if strings.TrimSpace(h.reason) == "" {
			t.Errorf("%q is not offered and no reason is given", h.name)
		}
	}

	// The reasons are reachable, which is the whole point of recording them.
	dr.m.moveTo(len(dr.m.fields))
	dr.key("enter")
	mustContain(t, dr.view(), "the project it was opened for")
}

func TestForm_RefusesToCreateWhileARequiredFieldIsEmpty(t *testing.T) {
	t.Parallel()

	c := newFake(20)
	dr := openOn(t, testDeps(c), 100, 24, fakeStory)

	dr.submitRow()
	dr.key("enter")

	if dr.field("summary").problem == "" {
		t.Error("summary is empty and required, and nothing says so")
	}
	if got := dr.lastStatus(); got.Level != kernel.LevelWarn {
		t.Errorf("the status line says %+v, want a warning", got)
	}
	for _, call := range c.Calls() {
		if call == "CreateIssue" {
			t.Fatal("the issue was created with a required field empty")
		}
	}
	mustContain(t, dr.view(), "this field is required")
}

func TestForm_CreatesTheIssueWithWhatWasTypedIntoIt(t *testing.T) {
	t.Parallel()

	c := newFake(20)
	dr := openOn(t, testDeps(c), 100, 24, fakeStory)

	dr.focus("summary")
	dr.key("enter")
	dr.typeText("Speed up the nightly export")
	dr.key("enter")

	dr.focus("labels")
	dr.key("enter")
	dr.typeText("infra flaky")
	dr.key("enter")

	dr.focus("customfield_13401")
	dr.key("enter")
	dr.typeText("5")
	dr.key("enter")

	dr.focus("priority")
	dr.key("enter")
	dr.key("enter")

	dr.submitRow()
	dr.key("enter")

	if dr.pops != 1 {
		t.Errorf("the form was popped %d times, want once after a create", dr.pops)
	}
	if len(dr.casts) != 1 {
		t.Errorf("%d broadcasts were sent, want the one that refreshes what is behind the form", len(dr.casts))
	}
	key := strings.Fields(dr.lastStatus().Text)
	if len(key) == 0 || !strings.HasPrefix(key[0], "PROJ-") {
		t.Fatalf("the status line says %q, want it to name the new issue", dr.lastStatus().Text)
	}

	created, err := c.Issue(t.Context(), key[0])
	if err != nil {
		t.Fatalf("reading back %s: %v", key[0], err)
	}
	if created.Summary != "Speed up the nightly export" {
		t.Errorf("the summary reads %q", created.Summary)
	}
	if strings.Join(created.Labels, ",") != "infra,flaky" {
		t.Errorf("the labels read %v, want the two that were typed", created.Labels)
	}
	if created.Type.ID != fakeStory {
		t.Errorf("the issue is a %s, want the type the form was opened on", created.Type.Name)
	}
	points, ok := created.Fields.ByID("customfield_13401")
	if !ok || points.Number != 5 {
		t.Errorf("the story points read %+v, want 5", points)
	}
	priority, ok := created.Fields.ByID("priority")
	if !ok || len(priority.Options) != 1 || priority.Options[0].ID == "" {
		t.Errorf("the priority reads %+v, want the one that was chosen, by id", priority)
	}
}

func TestForm_PutsAValidationErrorOnTheFieldItNames(t *testing.T) {
	t.Parallel()

	c := newFake(20)
	dr := openOn(t, testDeps(c), 100, 24, fakeStory)

	dr.focus("summary")
	dr.key("enter")
	dr.typeText("Anything at all")
	dr.key("enter")

	c.FailNext(&jira.ValidationError{Fields: []jira.FieldError{
		{Field: "customfield_13401", Message: "Story Points must be a whole number of points."},
	}})
	dr.submitRow()
	dr.key("enter")

	points := dr.field("customfield_13401")
	if points.problem != "Story Points must be a whole number of points." {
		t.Errorf("the field says %q, want Jira's own words", points.problem)
	}
	if len(dr.m.banner) != 0 {
		t.Errorf("the refusal also landed in a banner: %v", dr.m.banner)
	}
	if dr.pops != 0 {
		t.Error("the form closed although the issue was refused")
	}
	if f := dr.m.focused(); f == nil || f.id() != "customfield_13401" {
		t.Error("the cursor did not move to the field that was refused")
	}
	mustContain(t, dr.view(), "Story Points must be a whole")
}

func TestForm_ShowsARefusalAboutAFieldItDoesNotHaveRatherThanDroppingIt(t *testing.T) {
	t.Parallel()

	c := newFake(20)
	dr := openOn(t, testDeps(c), 100, 24, fakeStory)

	dr.focus("summary")
	dr.key("enter")
	dr.typeText("Anything at all")
	dr.key("enter")

	c.FailNext(&jira.ValidationError{
		Fields:   []jira.FieldError{{Field: "customfield_99999", Message: "Field cannot be set."}},
		Messages: []string{"The issue could not be created."},
	})
	dr.submitRow()
	dr.key("enter")

	if len(dr.m.banner) != 2 {
		t.Fatalf("the banner reads %v, want the unattached field message and the loose one", dr.m.banner)
	}
	mustContain(t, dr.view(), "customfield_99999", "The issue could not be created.")
}

func TestForm_ReportsEveryWayTheCreateScreenCanFail(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		err   error
		wants string
	}{
		{
			name:  "a project this token may not create in",
			err:   &jira.CapabilityError{Reason: "You do not have permission to create issues in this project."},
			wants: "You do not have permission to create issues in this project.",
		},
		{
			name:  "a site that is rate limiting",
			err:   &jira.RateLimitError{RetryAfter: 30 * time.Second},
			wants: "retry in 30s",
		},
		{
			name:  "a site that could not be reached",
			err:   &jira.TransportError{Op: "GET createmeta", Err: errors.New("connection refused")},
			wants: "connection refused",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := newFake(20)
			dr := newDriver(t, testDeps(c), 100, 24)
			c.FailNext(tt.err)
			dr.send(CreateMsg{IssueTypeID: fakeStory})

			if dr.m.stage == stageFields {
				t.Fatal("a form was drawn although the screen could not be read")
			}
			if got := dr.lastStatus(); got.Level != kernel.LevelError || !strings.Contains(got.Text, tt.wants) {
				t.Errorf("the status line says %+v, want it to carry %q", got, tt.wants)
			}
		})
	}
}

func TestForm_ReportsACreateJiraRefusedForAReasonThatIsNotAField(t *testing.T) {
	t.Parallel()

	c := newFake(20)
	dr := openOn(t, testDeps(c), 100, 24, fakeStory)
	dr.focus("summary")
	dr.key("enter")
	dr.typeText("Anything at all")
	dr.key("enter")

	c.FailNext(&jira.RateLimitError{RetryAfter: 12 * time.Second})
	dr.submitRow()
	dr.key("enter")

	if got := dr.lastStatus(); got.Level != kernel.LevelError || !strings.Contains(got.Text, "retry in 12s") {
		t.Errorf("the status line says %+v, want the countdown the site asked for", got)
	}
	if dr.pops != 0 {
		t.Error("the form closed although nothing was created")
	}
	if dr.m.busy {
		t.Error("the form still thinks a create is in flight")
	}
}

func TestForm_DropsAnAnswerToAQuestionTheUserHasMovedOn(t *testing.T) {
	t.Parallel()

	dr := openOn(t, testDeps(newFake(20)), 100, 24, fakeStory)
	before := dr.ids()

	dr.send(schemaLoadedMsg{gen: 0, schema: jira.Schema{Fields: []jira.FieldMeta{{
		Field: jira.FieldRef{ID: "summary", Schema: jira.FieldSchema{Type: "string", System: "summary"}},
		Name:  "Summary", Operations: []string{"set"},
	}}}})

	if strings.Join(dr.ids(), ",") != strings.Join(before, ",") {
		t.Errorf("a stale answer replaced the form: %v", dr.ids())
	}
}

func TestForm_StopsTheWorkInFlightWhenItIsAskedSomethingElse(t *testing.T) {
	t.Parallel()

	m := newWith(testDeps(newFake(20)), newSchemaCache(schemaTTL, time.Now), newDraftStore())
	ctx, _ := m.begin()
	m.stop()

	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Errorf("the context ended as %v, want context.Canceled", ctx.Err())
	}
}

func TestForm_ReadsTheCreateScreenOnceForTwoFormsOnTheSameThing(t *testing.T) {
	t.Parallel()

	c := newFake(20)
	cache, store := newSchemaCache(schemaTTL, time.Now), newDraftStore()
	d := testDeps(c)

	for range 2 {
		dr := &driver{t: t, m: newWith(d, cache, store)}
		dr.send(kernel.SizeMsg{Width: 100, Height: 24})
		dr.run(dr.m.Init())
		dr.send(CreateMsg{IssueTypeID: fakeStory})
		if dr.m.stage != stageFields {
			t.Fatalf("the second form never reached its fields; it noted %q", dr.m.note)
		}
	}

	reads := 0
	for _, call := range c.Calls() {
		if call == "CreateMeta" {
			reads++
		}
	}
	if reads != 1 {
		t.Errorf("the create screen was read %d times, want once and then from the cache", reads)
	}
}

func TestForm_ReadsTheCreateScreenAgainWhenTheCacheIsPurged(t *testing.T) {
	t.Parallel()

	c := newFake(20)
	dr := openOn(t, testDeps(c), 100, 24, fakeStory)
	dr.send(kernel.RefreshMsg{Purge: true})

	reads := 0
	for _, call := range c.Calls() {
		if call == "CreateMeta" {
			reads++
		}
	}
	if reads != 2 {
		t.Errorf("the create screen was read %d times, want a second read after a purge", reads)
	}
}

func TestForm_PutsBackWhatWasTypedWhenTheSameScreenIsOpenedAgain(t *testing.T) {
	t.Parallel()

	c := newFake(20)
	cache, store := newSchemaCache(schemaTTL, time.Now), newDraftStore()
	d := testDeps(c)

	first := &driver{t: t, m: newWith(d, cache, store)}
	first.send(kernel.SizeMsg{Width: 100, Height: 24})
	first.run(first.m.Init())
	first.send(CreateMsg{IssueTypeID: fakeStory})
	first.focus("summary")
	first.key("enter")
	first.typeText("Half a thought")
	first.key("esc")

	second := &driver{t: t, m: newWith(d, cache, store)}
	second.send(kernel.SizeMsg{Width: 100, Height: 24})
	second.run(second.m.Init())
	second.send(CreateMsg{IssueTypeID: fakeStory})

	if got := second.field("summary").text; got != "Half a thought" {
		t.Errorf("the summary reads %q, want what was typed before the form was closed", got)
	}
	mustContain(t, second.view(), "Half a thought")
}

func TestForm_ForgetsTheDraftOnceTheIssueExists(t *testing.T) {
	t.Parallel()

	c := newFake(20)
	cache, store := newSchemaCache(schemaTTL, time.Now), newDraftStore()
	d := testDeps(c)

	dr := &driver{t: t, m: newWith(d, cache, store)}
	dr.send(kernel.SizeMsg{Width: 100, Height: 24})
	dr.run(dr.m.Init())
	dr.send(CreateMsg{IssueTypeID: fakeStory})
	dr.focus("summary")
	dr.key("enter")
	dr.typeText("Something real")
	dr.key("enter")
	dr.submitRow()
	dr.key("enter")

	if kept := store.get(screen{project: "PROJ", issueType: fakeStory}); len(kept) != 0 {
		t.Errorf("the draft outlived the issue it created: %v", kept)
	}
}

func TestForm_TakesEveryKeyWhileAFieldIsBeingEdited(t *testing.T) {
	t.Parallel()

	dr := openOn(t, testDeps(newFake(20)), 100, 24, fakeStory)
	if dr.m.WantsRawKeys() {
		t.Error("the form is swallowing keys with no editor open")
	}

	dr.focus("summary")
	dr.key("enter")
	if !dr.m.WantsRawKeys() {
		t.Fatal("the kernel would eat the digits of anything typed into this field")
	}
	dr.typeText("Release 2 of 3")
	dr.key("enter")

	if got := dr.field("summary").text; got != "Release 2 of 3" {
		t.Errorf("the summary reads %q, want every character that was typed", got)
	}
	if dr.m.WantsRawKeys() {
		t.Error("the form is still swallowing keys after the editor closed")
	}
}

func TestForm_KeepsWhatWasTypedWhenTheEditorIsClosedWithEscape(t *testing.T) {
	t.Parallel()

	dr := openOn(t, testDeps(newFake(20)), 100, 24, fakeStory)
	dr.focus("summary")
	dr.key("enter")
	dr.typeText("Do not lose this")
	dr.key("esc")

	if got := dr.field("summary").text; got != "Do not lose this" {
		t.Errorf("the summary reads %q; esc closed the editor and threw the text away", got)
	}
}

func TestForm_ChoosesOnlyFromTheValuesTheSiteAllows(t *testing.T) {
	t.Parallel()

	dr := openOn(t, testDeps(newFake(20)), 100, 24, fakeStory)
	priority := dr.field("priority")

	dr.focus("priority")
	dr.key("enter")

	if len(dr.m.choices) != len(priority.meta.AllowedValues) {
		t.Fatalf("the picker offers %d values, want the %d the screen states", len(dr.m.choices), len(priority.meta.AllowedValues))
	}
	for i, c := range dr.m.choices {
		if c.label != priority.meta.AllowedValues[i].Label {
			t.Errorf("value %d reads %q, want %q", i, c.label, priority.meta.AllowedValues[i].Label)
		}
	}
	dr.key("down", "enter")

	if got := priority.picked; len(got) != 1 || got[0].ID != priority.meta.AllowedValues[1].ID {
		t.Errorf("the priority is %+v, want the second value the screen allows", got)
	}
	if priority.validate() != "" {
		t.Errorf("a value the site allows was refused: %s", priority.validate())
	}
}

func TestForm_NarrowsAPickerAsItIsTypedInto(t *testing.T) {
	t.Parallel()

	dr := openOn(t, testDeps(newFake(20)), 100, 24, fakeStory)
	dr.focus("priority")
	dr.key("enter")

	wanted := dr.field("priority").meta.AllowedValues[len(dr.field("priority").meta.AllowedValues)-1]
	dr.typeText(strings.ToLower(wanted.Label[:3]))

	if got := len(dr.m.visibleChoices()); got != 1 {
		t.Fatalf("%d values are still on offer after narrowing to %q", got, wanted.Label[:3])
	}
	dr.key("enter")
	if got := dr.field("priority").picked; len(got) != 1 || got[0].ID != wanted.ID {
		t.Errorf("the priority is %+v, want %q", got, wanted.Label)
	}
}

func TestForm_OffersTheAuthenticatedAccountToAPersonPicker(t *testing.T) {
	t.Parallel()

	c := newFake(20)
	dr := openOn(t, testDeps(c), 100, 24, fakeStory)

	me, err := c.Me(t.Context())
	if err != nil {
		t.Fatalf("reading the account: %v", err)
	}
	dr.focus("assignee")
	dr.key("enter")

	if len(dr.m.choices) == 0 {
		t.Fatal("the person picker offers nobody at all")
	}
	dr.key("enter")

	assignee := dr.field("assignee")
	if len(assignee.picked) != 1 || assignee.picked[0].ID != me.AccountID {
		t.Errorf("the assignee is %+v, want the authenticated account by id", assignee.picked)
	}
}

func TestForm_AsksTheSiteWhichIssueTypesThisProjectUses(t *testing.T) {
	t.Parallel()

	c := newFake(20)
	dr := newDriver(t, testDeps(c), 100, 24)

	if len(dr.m.types) == 0 {
		t.Fatal("the picker offers no issue type at all")
	}
	for _, typ := range dr.m.types {
		if typ.ID == "" || typ.Name == "" {
			t.Errorf("an issue type arrived as %+v", typ)
		}
	}
	mustContain(t, dr.view(), "Which kind of issue?", dr.m.types[0].Name)

	dr.key("enter")
	if dr.m.stage != stageFields {
		t.Fatal("choosing an issue type did not open its create screen")
	}
	if dr.m.chosen.ID != dr.m.types[0].ID {
		t.Errorf("the form opened on %s, want the type under the cursor", dr.m.chosen.Name)
	}
}

func TestForm_SaysSoWhenThereIsNoProjectToCreateIn(t *testing.T) {
	t.Parallel()

	d := testDeps(newFake(20))
	d.Project = ""
	dr := newDriver(t, d, 100, 24)

	mustContain(t, dr.view(), "not scoped to a project")
	if len(dr.m.types) != 0 {
		t.Error("issue types were fetched for a session with no project")
	}
}

func TestForm_SaysSoWhenThereIsNoConnection(t *testing.T) {
	t.Parallel()

	d := testDeps(nil)
	d.Jira = nil
	dr := newDriver(t, d, 100, 24)

	mustContain(t, dr.view(), "no Jira connection")
}

func TestForm_RefusesToCloseOnlyWhileTheCreateIsInFlight(t *testing.T) {
	t.Parallel()

	dr := openOn(t, testDeps(newFake(20)), 100, 24, fakeStory)
	dr.focus("summary")
	dr.key("enter")
	dr.typeText("Typed and unsaved")
	dr.key("enter")

	if _, blocked := dr.m.BlocksClose(); blocked {
		t.Error("the form refuses to close over a draft it keeps anyway")
	}

	dr.m.busy = true
	reason, blocked := dr.m.BlocksClose()
	if !blocked || reason == "" {
		t.Error("the form would be thrown away while Jira is answering a create")
	}
}

func TestForm_SelectsARowOnAClickAndOpensItOnTheNext(t *testing.T) {
	t.Parallel()

	d := testDeps(newFake(20))
	dr := openOn(t, d, 100, 24, fakeStory)
	dr.m.moveTo(0)

	at := 2
	click := clickIn(t, d, dr.m, dr.m.View(), dr.m.rowZone(at))
	dr.send(click)
	if dr.m.cursor != at {
		t.Fatalf("the cursor is on row %d, want the row that was clicked", dr.m.cursor)
	}

	dr.send(clickIn(t, d, dr.m, dr.m.View(), dr.m.rowZone(at)))
	if dr.m.edit == editNone {
		t.Error("a second click on the selected row did not open its editor")
	}
}

func TestForm_ClickingAValueInAPickerTakesIt(t *testing.T) {
	t.Parallel()

	d := testDeps(newFake(20))
	dr := openOn(t, d, 100, 24, fakeStory)
	dr.focus("priority")
	dr.key("enter")

	dr.send(clickIn(t, d, dr.m, dr.m.View(), dr.m.choiceZone(1)))
	if dr.m.pick != 1 {
		t.Fatalf("the picker is on value %d, want the one that was clicked", dr.m.pick)
	}
	dr.send(clickIn(t, d, dr.m, dr.m.View(), dr.m.choiceZone(1)))

	priority := dr.field("priority")
	if len(priority.picked) != 1 || priority.picked[0].ID != priority.meta.AllowedValues[1].ID {
		t.Errorf("the priority is %+v, want the value that was clicked twice", priority.picked)
	}
}

func TestForm_RegistersItselfWithItsKeysAndACommand(t *testing.T) {
	t.Parallel()

	spec, ok := kernel.LookupView(ViewID)
	if !ok {
		t.Fatal("the form is not in the view registry")
	}
	if spec.Slot != 0 {
		t.Errorf("the form claims footer slot %d; docs/UX.md leaves the digits to the root views", spec.Slot)
	}
	if kernel.KeysFor(ViewID).IsZero() {
		t.Error("the form registered no keys, so the footer and the help overlay have nothing to show")
	}
	found := false
	for _, cmd := range kernel.Commands() {
		if cmd.ID == "issue.create" {
			found = true
		}
	}
	if !found {
		t.Error("the palette cannot reach the create form")
	}
}

func TestForm_SurvivesATerminalTooNarrowToDrawIn(t *testing.T) {
	t.Parallel()

	dr := openOn(t, testDeps(newFake(20)), 100, 24, fakeStory)
	for _, size := range [][2]int{{40, 10}, {20, 6}, {8, 3}} {
		dr.send(kernel.SizeMsg{Width: size[0], Height: size[1]})
		if got := dr.view(); got == "" {
			t.Errorf("nothing was drawn at %dx%d", size[0], size[1])
		}
	}
}

// clickIn scans a frame for one of the view's own zones and builds a click
// inside it. The manager records a zone on its own goroutine, so it is looked
// for until it appears.
func clickIn(t *testing.T, d kernel.Deps, m *Model, frame, name string) tea.MouseClickMsg {
	t.Helper()

	id := m.zones.ID(name)
	_ = d.Zones.Scan(frame)
	deadline := time.Now().Add(5 * time.Second)
	for d.Zones.Get(id).IsZero() {
		if time.Now().After(deadline) {
			t.Fatalf("zone %q was never rendered", id)
		}
		runtime.Gosched()
	}
	at := d.Zones.Get(id)
	return tea.MouseClickMsg{Button: tea.MouseLeft, X: at.StartX + 2, Y: at.StartY}
}

// The palette opens over the create screen, so a read given up on a blur is one
// nothing asks for again. A screen the kernel has thrown away lets it go.
func TestForm_KeepsItsReadOnABlurAndDropsItOnAClose(t *testing.T) {
	t.Parallel()

	c := newFake(20)

	kept := New(testDeps(c))
	kept, _ = kept.Update(kernel.SizeMsg{Width: 100, Height: 24})
	reading := kept.Init()
	if _, more := kept.Update(kernel.FocusMsg{}); more != nil {
		t.Fatal("losing the keyboard asked for more work")
	}
	if _, gaveUp := answer(reading).(typesFailedMsg); gaveUp {
		t.Error("the create screen gave up its read when it merely lost the keyboard")
	}

	dropped := New(testDeps(c))
	dropped, _ = dropped.Update(kernel.SizeMsg{Width: 100, Height: 24})
	cmd := dropped.Init()
	closer, ok := dropped.(kernel.Closer)
	if !ok {
		t.Fatal("the create screen does not implement kernel.Closer, so nothing stops its read")
	}
	closer.Close()

	failed, ok := answer(cmd).(typesFailedMsg)
	if !ok {
		t.Fatalf("the read came back as %T, want the failure a cancelled context produces", answer(cmd))
	}
	if !errors.Is(failed.err, context.Canceled) {
		t.Errorf("err = %v, want the context's own error", failed.err)
	}
}

// answer is what the kernel hands a view: the command's own reply with the
// envelope the kernel addresses it by taken off.
func answer(cmd tea.Cmd) tea.Msg {
	msg := cmd()
	if reply, addressed := msg.(kernel.ReplyMsg); addressed {
		return reply.Msg
	}
	return msg
}

// A create screen states what Jira will fill a field in with. Showing it and
// seeding the widget with it are not the same thing: a seeded widget is not
// empty, a field that is not empty goes into the FieldSet, and the request then
// names the value explicitly — so the project's own default stops applying to
// anything made here the moment somebody changes it.
func TestForm_ShowsTheDefaultJiraWillUseAndDoesNotPutItInTheWidget(t *testing.T) {
	t.Parallel()

	c := newFake(20)
	dr := openOn(t, testDeps(c), 100, 24, fakeStory)

	priority := dr.field("priority")
	if !priority.meta.HasDefault {
		t.Fatal("the fake's create screen states no default for the priority, so this proves nothing")
	}
	stated, says := priority.stated()
	if !says || stated == "" {
		t.Fatalf("the priority states %q, says=%v; the screen named a value and the form has to be able to show it", stated, says)
	}
	if !strings.Contains(dr.view(), "Jira will use "+stated) {
		t.Errorf("the form does not say what Jira will use for the priority:\n%s", dr.view())
	}

	if !priority.empty() {
		t.Errorf("the priority widget holds %q / %+v; a stated default is shown, never put in the widget",
			priority.text, priority.picked)
	}

	dr.focus("summary")
	dr.key("enter")
	dr.typeText("Nothing was chosen for the priority")
	dr.key("enter")

	in := dr.m.issueInput()
	if _, sent := in.Fields.ByID("priority"); sent {
		t.Error("the create request names the priority; a request that sends the site's own default " +
			"freezes it, and a later change to the project's default stops reaching Saral")
	}
}

// The screen can say a field has a default and decline to say what — the
// reporter comes from the credential. A form that read that as "no default" would
// draw the field as merely blank.
func TestForm_SaysJiraFillsAFieldInWhenTheScreenWillNotNameTheValue(t *testing.T) {
	t.Parallel()

	f := newField(jira.FieldMeta{
		Field:      jira.FieldRef{ID: "reporter", Name: "Reporter", Schema: jira.FieldSchema{Type: "user", System: "reporter"}},
		Name:       "Reporter",
		HasDefault: true,
		Operations: []string{"set"},
	}, time.UTC)

	stated, says := f.stated()
	if !says {
		t.Fatal("a field whose screen states a default reads as having none")
	}
	if stated != "" {
		t.Errorf("the stated default reads %q, want nothing to show", stated)
	}
	if got := placeholder(f, "*"); !strings.Contains(got, "Jira fills this in") {
		t.Errorf("the row reads %q, want it to say the site will fill the field in", got)
	}
}

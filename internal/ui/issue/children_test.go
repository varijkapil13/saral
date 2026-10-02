package issue

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

type searchLog struct {
	jira.Client

	mu      sync.Mutex
	queries []jira.Query
	fail    error
}

func (s *searchLog) Search(ctx context.Context, q jira.Query) (jira.Page[jira.Issue], error) {
	s.mu.Lock()
	s.queries = append(s.queries, q)
	fail := s.fail
	s.mu.Unlock()
	if fail != nil {
		return jira.Page[jira.Issue]{}, fail
	}
	return s.Client.Search(ctx, q)
}

func (s *searchLog) seen() []jira.Query {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.queries)
}

func (s *searchLog) failWith(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fail = err
}

func epicFake(t *testing.T, children int, opts ...jiratest.Option) (f *jiratest.Fake, epic string, kids []string) {
	t.Helper()
	me, other := collabMe, collabOther
	me.Kind, other.Kind = jira.AccountPerson, jira.AccountPerson
	f = newFake(4, append([]jiratest.Option{jiratest.WithMe(me), jiratest.WithPeople([]jira.User{me, other})}, opts...)...)
	made, err := f.CreateIssue(t.Context(), jira.IssueInput{ProjectKey: "PROJ", IssueTypeID: "10304", Summary: "Billing rewrite"})
	if err != nil {
		t.Fatalf("CreateIssue epic: %v", err)
	}
	for i := range children {
		kid, err := f.CreateIssue(t.Context(), jira.IssueInput{
			ProjectKey: "PROJ", IssueTypeID: "10301", Summary: fmt.Sprintf("Child number %d", i+1), ParentKey: made.Key,
		})
		if err != nil {
			t.Fatalf("CreateIssue child: %v", err)
		}
		kids = append(kids, kid.Key)
	}
	return f, made.Key, kids
}

func epicPane(t *testing.T, c jira.Client, f *jiratest.Fake, key string, h int) *panel {
	t.Helper()
	d := testDeps(t, c)
	iss := readIssue(t, f, key)
	iss.Type.HierarchyLevel = 1
	p := newPanel(t, New(d, iss, withDrafts(tempDrafts(t))), 120, h)
	p.send(loadedMsg{gen: p.editor().gen, issue: iss})
	return p
}

func childRows(m *Model) (children, more int) {
	for _, row := range m.sideRows {
		switch {
		case row.kind == rkRef && strings.HasPrefix(row.id, "ref:"+childrenGroup+":"):
			children++
		case row.kind == rkMore:
			more++
		}
	}
	return children, more
}

func TestChildren_EpicReadsParentJQLNarrowFields(t *testing.T) {
	t.Parallel()
	f, epic, _ := epicFake(t, 3)
	log := &searchLog{Client: f}
	epicPane(t, log, f, epic, 50)

	queries := log.seen()
	if len(queries) != 1 {
		t.Fatalf("the pane made %d searches, want one", len(queries))
	}
	q := queries[0]
	if want := "parent = " + epic + " ORDER BY created ASC"; q.JQL != want {
		t.Errorf("JQL is %q, want %q", q.JQL, want)
	}
	if q.MaxResults != childrenPage {
		t.Errorf("asked for %d rows, want %d", q.MaxResults, childrenPage)
	}
	for _, id := range q.Fields {
		if !slices.Contains(childProjection("").IDs, id) {
			t.Errorf("the read asked for %q, which a list row does not carry", id)
		}
	}
}

func TestChildren_StandardIssueMakesNoRequest(t *testing.T) {
	t.Parallel()
	f := collabFake()
	log := &searchLog{Client: f}
	d := testDeps(t, log)
	iss := navIssue()
	p := newPanel(t, New(d, jira.Issue{Key: iss.Key}), 120, 40)
	p.send(loadedMsg{gen: p.editor().gen, issue: iss})
	if got := log.seen(); len(got) != 0 {
		t.Errorf("an issue with subtasks and no hierarchy made searches: %v", got)
	}
	mustContain(t, p.frame(), "Subtasks", "PROJ-31")
	mustNotContain(t, p.frame(), "Children")
}

func TestChildren_FirstEightInlineThenMore(t *testing.T) {
	t.Parallel()
	f, epic, _ := epicFake(t, 12)
	p := epicPane(t, f, f, epic, 60)
	if got, more := childRows(p.editor()); got != childrenInline || more != 1 {
		t.Fatalf("cursor rows: %d children and %d more, want %d and 1", got, more, childrenInline)
	}
	mustContain(t, p.frame(), "Children · 12 · 0 done", "+4 more · ] lists them all")

	for i, row := range p.editor().sideRows {
		if row.kind == rkMore {
			p.editor().cursor = i
		}
	}
	p.editor().focus = regionDetails
	p.keys("enter")
	push := p.onlyPush()
	if push.ID != "issue.sheet" || push.Title != epic+" children" {
		t.Errorf("enter on the more row pushed %q titled %q", push.ID, push.Title)
	}
}

func TestChildren_ReadFailureShowsReasonAndRetries(t *testing.T) {
	t.Parallel()
	f, epic, _ := epicFake(t, 2)
	log := &searchLog{Client: f}
	boom := &jira.CapabilityError{Reason: "no Browse projects permission"}
	log.failWith(boom)
	p := epicPane(t, log, f, epic, 50)
	mustContain(t, p.frame(), "children could not be read")
	if reason, _ := jira.Reason(p.editor().childErr); reason != boom.Error() {
		t.Errorf("the read failed with %q", reason)
	}

	log.failWith(nil)
	iss := readIssue(t, f, epic)
	iss.Type.HierarchyLevel = 1
	p.send(loadedMsg{gen: p.editor().gen, issue: iss})
	mustContain(t, p.frame(), "Children · 2 · 0 done")
	mustNotContain(t, p.frame(), "could not be read")
}

func TestChildren_BracketKeyOpensTheSheetAndSaysWhenThereIsNone(t *testing.T) {
	t.Parallel()
	f, epic, _ := epicFake(t, 2)
	p := epicPane(t, f, f, epic, 50)
	p.keys("]")
	if push := p.onlyPush(); push.ID != "issue.sheet" {
		t.Errorf("] pushed %q", push.ID)
	}

	bare := newPanel(t, New(testDeps(t, f), readIssue(t, f, "PROJ-2")), 120, 30)
	bare.send(loadedMsg{gen: bare.editor().gen, issue: readIssue(t, f, "PROJ-2")})
	bare.keys("]")
	if len(bare.pushes) != 0 || bare.lastStatus().Text != "PROJ-2 has no children" {
		t.Errorf("] on an issue with none: %d pushes, status %q", len(bare.pushes), bare.lastStatus().Text)
	}
}

func TestChildren_SheetSeededFromThePaneMakesNoRequest(t *testing.T) {
	t.Parallel()
	f, epic, _ := epicFake(t, 4)
	log := &searchLog{Client: f}
	p := epicPane(t, log, f, epic, 50)
	p.keys("]")
	sh, ok := p.onlyPush().View.(*sheet)
	if !ok {
		t.Fatal("] did not push a sheet")
	}
	sh.Update(kernel.SizeMsg{Width: 100, Height: 20})
	if cmd := sh.Init(); cmd != nil {
		t.Error("a seeded sheet asked the site for something")
	}
	if got := len(log.seen()); got != 1 {
		t.Errorf("%d searches in all, want the pane's one", got)
	}
	if len(sh.rows) != 4 {
		t.Errorf("the sheet holds %d rows, want 4", len(sh.rows))
	}
}

func TestChildChanged_PatchesRollupWithoutFullFetch(t *testing.T) {
	t.Parallel()
	f, epic, kids := epicFake(t, 3)
	p := epicPane(t, f, f, epic, 50)
	mustContain(t, p.frame(), "Children · 3 · 0 done")

	done := jira.NewFieldSet(map[string]jira.FieldValue{
		"resolution": {Kind: jira.KindOption, Options: []jira.Option{{ID: "10501"}}},
	})
	if err := f.Transition(t.Context(), kids[0], "tr-10203", jira.IssuePatch{Fields: done}); err != nil {
		t.Fatalf("Transition: %v", err)
	}
	issueReads, metaReads, searches := countCalls(f, "IssueFields"), countCalls(f, "EditMeta"), countCalls(f, "Search")
	p.send(ChangedMsg{Key: kids[0]})

	mustContain(t, p.frame(), "Children · 3 · 1 done")
	if got := countCalls(f, "IssueFields") - issueReads; got != 1 {
		t.Errorf("a child changing cost %d issue reads, want one narrow read", got)
	}
	if countCalls(f, "EditMeta") != metaReads || countCalls(f, "Search") != searches {
		t.Error("a child changing re-read the pane's own issue or its children")
	}
}

func TestChildren_ReadsAreNotDoubledByTheOwnKeyChanging(t *testing.T) {
	t.Parallel()
	f, epic, _ := epicFake(t, 2)
	p := epicPane(t, f, f, epic, 50)
	before := countCalls(f, "Search")
	p.send(ChangedMsg{Key: epic})
	if got := countCalls(f, "Search") - before; got != 0 {
		t.Errorf("the epic's own change searched %d times before its full read landed with the old type", got)
	}
}

func TestRollup(t *testing.T) {
	t.Parallel()
	done := jira.Issue{Status: jira.Status{Category: jira.CategoryDone}}
	open := jira.Issue{Status: jira.Status{Category: jira.CategoryToDo}}
	if n, d := rollup([]jira.Issue{done, open, done}); n != 3 || d != 2 {
		t.Errorf("rollup is %d of %d", d, n)
	}
	if n, d := rollup(nil); n != 0 || d != 0 {
		t.Errorf("rollup of nothing is %d of %d", d, n)
	}
}

func TestChildren_PaletteCommandsAreRegistered(t *testing.T) {
	t.Parallel()
	for id, keys := range map[string][]string{
		"issue.parent": {"p"}, "issue.children": {"]"}, "issue.childrenInList": nil,
	} {
		cmd, ok := kernel.LookupCommand(id)
		if !ok {
			t.Errorf("%s is not registered", id)
			continue
		}
		if !slices.Equal(cmd.Keys, keys) {
			t.Errorf("%s names keys %v, want %v", id, cmd.Keys, keys)
		}
	}
}

func TestChildren_ListCommandSaysWhenNoViewRunsQueries(t *testing.T) {
	t.Parallel()
	f, epic, _ := epicFake(t, 1)
	p := epicPane(t, f, f, epic, 50)
	p.send(CollabMsg{Open: collabChildList})
	if got := p.lastStatus().Text; got != "there is no issue list to show them in" {
		t.Errorf("status is %q", got)
	}
}

type childSheet struct {
	*sheetDriver
	rec *recorder
	f   *jiratest.Fake
}

func openChildrenSheet(t *testing.T, f *jiratest.Fake, epic string, opts ...func(*childrenKind)) *childSheet {
	t.Helper()
	rec := record(f)
	iss, err := f.Issue(t.Context(), epic)
	if err != nil {
		t.Fatal(err)
	}
	kind := &childrenKind{search: app.NewSearch(rec)}
	for _, o := range opts {
		o(kind)
	}
	s := newSheet(testDeps(t, rec), iss, kind)
	s.wait = 0
	s.trail = []string{"X", epic}
	d := &sheetDriver{t: t, s: s}
	d.send(kernel.SizeMsg{Width: 120, Height: 24})
	d.run(s.Init())
	return &childSheet{sheetDriver: d, rec: rec, f: f}
}

func (c *childSheet) kind() *childrenKind {
	k, ok := c.s.kind.(*childrenKind)
	if !ok {
		c.t.Fatal("the sheet is not the children sheet")
	}
	return k
}

func (c *childSheet) first() jira.Issue {
	c.t.Helper()
	k := c.kind()
	if len(k.issues) == 0 {
		c.t.Fatal("the sheet holds no children")
	}
	return k.issues[0]
}

func (c *childSheet) issue(key string) jira.Issue {
	c.t.Helper()
	iss, err := c.f.Issue(c.t.Context(), key)
	if err != nil {
		c.t.Fatal(err)
	}
	return iss
}

func TestChildrenSheet_Pages(t *testing.T) {
	t.Parallel()
	f, epic, _ := epicFake(t, 12, jiratest.WithPageSize(5))
	d := openChildrenSheet(t, f, epic)
	if len(d.s.rows) != 5 {
		t.Fatalf("the first page holds %d rows, want 5", len(d.s.rows))
	}
	mustContain(t, d.s.note, "5+ children")
	for range 14 {
		d.keys("j")
	}
	if len(d.s.rows) != 12 {
		t.Fatalf("walking to the end loaded %d rows, want 12", len(d.s.rows))
	}
	if d.s.note != "12 children · 0 done" {
		t.Errorf("note is %q", d.s.note)
	}
	if d.s.cursor != 11 {
		t.Errorf("cursor is %d, want 11", d.s.cursor)
	}
}

func TestChildrenSheet_AssignWritesAndRevalidatesRow(t *testing.T) {
	t.Parallel()
	f, epic, _ := epicFake(t, 3)
	d := openChildrenSheet(t, f, epic)
	child := d.first()

	d.keys("@")
	if !d.s.asking || len(d.s.cands) < 2 || d.s.cands[0].key != "me" || d.s.cands[1].key != "unassigned" {
		t.Fatalf("the prompt offers %+v, want me and unassigned first", d.s.cands)
	}
	d.keys("enter")

	if got := d.issue(child.Key).Assignee; got == nil || got.AccountID != collabMe.AccountID {
		t.Fatalf("%s is assigned to %+v", child.Key, got)
	}
	if names := patchFieldNames(d.rec.lastPatch(t)); !slices.Equal(names, []string{"assignee"}) {
		t.Errorf("the patch named %v", names)
	}
	mustContain(t, d.s.rows[0].text, collabMe.DisplayName)
	if !slices.Contains(d.broadcasts, tea.Msg(ChangedMsg{Key: child.Key})) {
		t.Errorf("no ChangedMsg for %s in %v", child.Key, d.broadcasts)
	}
	if !strings.Contains(d.last().Text, child.Key+" assigned to "+collabMe.DisplayName) {
		t.Errorf("status is %q", d.last().Text)
	}
}

func TestChildrenSheet_AssignSearchesTheChildsProject(t *testing.T) {
	t.Parallel()
	f, epic, _ := epicFake(t, 2)
	d := openChildrenSheet(t, f, epic)
	d.keys("@")
	d.typed("grace")
	if len(d.s.cands) == 0 || !strings.Contains(d.s.cands[len(d.s.cands)-1].text, collabOther.DisplayName) {
		t.Fatalf("the prompt offers %+v after typing a name", d.s.cands)
	}
	d.s.pick = len(d.s.cands) - 1
	d.keys("enter")
	if got := d.issue(d.first().Key).Assignee; got == nil || got.AccountID != collabOther.AccountID {
		t.Errorf("assigned to %+v", got)
	}
}

func firstMove(d *childSheet, withFields bool) string {
	for _, c := range d.s.cands {
		for _, tr := range d.kind().moves {
			if tr.ID == c.id && (len(requiredFields(tr)) > 0) == withFields {
				return c.id
			}
		}
	}
	return ""
}

func pickCand(d *childSheet, id string) {
	for i, c := range d.s.cands {
		if c.id == id {
			d.s.pick = i
		}
	}
}

func TestChildrenSheet_TransitionNoFields(t *testing.T) {
	t.Parallel()
	f, epic, _ := epicFake(t, 2)
	d := openChildrenSheet(t, f, epic)
	child := d.first()
	d.keys("t")
	id := firstMove(d, false)
	if id == "" {
		t.Fatalf("no move without a screen among %+v", d.s.cands)
	}
	pickCand(d, id)
	d.keys("enter")
	if d.s.question == "" {
		t.Fatal("a move with nothing required was made without asking")
	}
	if d.rec.writes() != 0 {
		t.Fatal("the move was written before the answer")
	}
	d.keys("y")
	if got := d.rec.lastMove(t); got.key != child.Key || got.id != id {
		t.Errorf("moved %+v, want %s by %s", got, child.Key, id)
	}
	if d.issue(child.Key).Status.ID == child.Status.ID {
		t.Error("the child kept its status")
	}
	mustContain(t, d.s.rows[0].text, d.issue(child.Key).Status.Name)
}

func TestChildrenSheet_TransitionWithFieldsOpensPane(t *testing.T) {
	t.Parallel()
	f, epic, _ := epicFake(t, 2)
	d := openChildrenSheet(t, f, epic)
	child := d.first()
	d.keys("t")
	id := firstMove(d, true)
	if id == "" {
		t.Fatalf("no move with a screen among %+v", d.s.cands)
	}
	pickCand(d, id)
	d.keys("enter")
	if d.rec.writes() != 0 {
		t.Error("a move that needs fields was written without them")
	}
	if len(d.pushed) != 1 {
		t.Fatalf("pushed %d views", len(d.pushed))
	}
	opened := pushedPane(t, d.pushed[0])
	if opened.issue.Key != child.Key || opened.openMove != id || !slices.Equal(opened.trail, []string{"X", epic, ""}) {
		t.Errorf("opened %s for move %q with trail %v", opened.issue.Key, opened.openMove, opened.trail)
	}
}

func priorityMeta() jira.FieldMeta {
	return jira.FieldMeta{
		Field: jira.FieldRef{ID: "priority"}, Name: "Priority",
		AllowedValues: []jira.Option{{ID: "10401", Label: "Urgent"}, {ID: "10402", Label: "Normal"}},
	}
}

func TestChildrenSheet_PriorityNotOnScreen(t *testing.T) {
	t.Parallel()
	f, epic, _ := epicFake(t, 2)
	d := openChildrenSheet(t, f, epic)
	child := d.first()
	d.keys("P")
	want := "Priority is not on " + child.Key + "'s edit screen"
	if d.s.asking || d.last().Text != want {
		t.Errorf("asking %v, status %q", d.s.asking, d.last().Text)
	}
}

func TestChildrenSheet_PriorityWrites(t *testing.T) {
	t.Parallel()
	f, epic, _ := epicFake(t, 2)
	d := openChildrenSheet(t, f, epic)
	child := d.first()
	f.SetEditMeta(child.Key, priorityMeta())
	d.keys("P")
	if !d.s.asking || len(d.s.cands) != 2 {
		t.Fatalf("the prompt offers %+v", d.s.cands)
	}
	d.keys("enter")
	if got := d.issue(child.Key).Priority; got == nil || got.ID != "10401" {
		t.Errorf("priority is %+v", got)
	}
	if names := patchFieldNames(d.rec.lastPatch(t)); !slices.Equal(names, []string{"priority"}) {
		t.Errorf("the patch named %v", names)
	}
	mustContain(t, d.s.rows[0].text, "Urgent")
}

func TestChildrenSheet_FailuresSayWhyAndWriteNothing(t *testing.T) {
	t.Parallel()
	for _, tc := range failures() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f, epic, _ := epicFake(t, 2)
			d := openChildrenSheet(t, f, epic)
			child := d.first()
			d.keys("@")
			f.FailNext(tc.err)
			d.keys("enter")
			want, _ := jira.Reason(tc.err)
			if d.s.fail != want || d.last().Level != kernel.LevelError || d.s.busy {
				t.Errorf("fail %q, status %+v, busy %v", d.s.fail, d.last(), d.s.busy)
			}
			if got := d.issue(child.Key).Assignee; got != nil && got.AccountID == collabMe.AccountID {
				t.Error("the write landed")
			}
			if d.rec.writes() != 0 {
				t.Error("a patch reached the port")
			}
		})
	}
}

func TestChildrenSheet_Conflict(t *testing.T) {
	t.Parallel()
	f, epic, _ := epicFake(t, 2)
	d := openChildrenSheet(t, f, epic)
	child := d.first()
	other := collabOther.AccountID
	if err := f.UpdateIssue(t.Context(), child.Key, jira.IssuePatch{Assignee: &other}); err != nil {
		t.Fatal(err)
	}

	d.keys("@", "enter")
	if d.s.fail != "assignee changed on the site; read again" {
		t.Errorf("fail is %q", d.s.fail)
	}
	if d.rec.writes() != 0 {
		t.Error("the write went ahead over a change it had not seen")
	}
	mustContain(t, d.s.rows[0].text, collabOther.DisplayName)
	if got := d.issue(child.Key).Assignee; got == nil || got.AccountID != other {
		t.Errorf("assignee is %+v", got)
	}
}

func syntheticChildren() []jira.Issue {
	story := jira.IssueType{ID: "10301", Name: "Story"}
	task := jira.IssueType{ID: "10303", Name: "Task"}
	todo := jira.Status{Name: "To Do", Category: jira.CategoryToDo}
	doing := jira.Status{Name: "In Progress", Category: jira.CategoryInProgress}
	done := jira.Status{Name: "Done", Category: jira.CategoryDone}
	ada := &jira.User{AccountID: "u1", DisplayName: "Ada L."}
	hi, med := &jira.Priority{ID: "2", Name: "High"}, &jira.Priority{ID: "3", Name: "Medium"}
	return []jira.Issue{
		{ID: "1", Key: "PROJ-31", Summary: "Split the export job", Type: story, Status: done, Assignee: ada, Priority: med},
		{ID: "2", Key: "PROJ-32", Summary: "Drop the cron entry", Type: task, Status: todo, Priority: hi},
		{ID: "3", Key: "PROJ-33", Summary: "Write the migration", Type: story, Status: doing, Assignee: ada, Priority: hi},
		{ID: "4", Key: "PROJ-34", Summary: "Backfill last year", Type: task, Status: done, Priority: med},
		{ID: "5", Key: "PROJ-35", Summary: "Announce the cut-over", Type: story, Status: todo},
		{ID: "6", Key: "PROJ-36", Summary: "Remove the old endpoint", Type: task, Status: todo, Priority: med},
	}
}

func syntheticSheet(t *testing.T, w, h int) *sheetDriver {
	t.Helper()
	d := testDeps(t, newFake(2))
	kind := &childrenKind{seed: &childSeed{issues: syntheticChildren()}}
	s := newSheet(d, jira.Issue{Key: "PROJ-3", Summary: "Billing rewrite"}, kind)
	s.wait = 0
	dr := &sheetDriver{t: t, s: s}
	dr.send(kernel.SizeMsg{Width: w, Height: h})
	dr.run(s.Init())
	return dr
}

func TestChildrenSheet_Frames(t *testing.T) {
	t.Parallel()
	d := syntheticSheet(t, 120, 30)
	d.keys("j")
	golden(t, "sheet_children_120x30.golden", d.frame())

	asking := syntheticSheet(t, 80, 20)
	asking.keys("@")
	golden(t, "sheet_children_asking_80x20.golden", asking.frame())
}

func TestChildrenSheet_KeysAreTheFooter(t *testing.T) {
	t.Parallel()
	d := syntheticSheet(t, 120, 30)
	set, _ := d.s.LiveKeys()
	if got := actsOf(set); got != "enter open it · @ assign · t status · P priority · s sort" {
		t.Errorf("the footer says %q", got)
	}
}

func epicGolden(t *testing.T, w, h int) *panel {
	t.Helper()
	d := testDeps(t, newFake(2))
	iss := navIssue()
	iss.Type.HierarchyLevel = 1
	iss.Parent, iss.Subtasks = nil, nil
	p := newPanel(t, New(d, jira.Issue{Key: iss.Key, Summary: iss.Summary}), w, h)
	p.send(loadedMsg{gen: p.editor().gen, issue: iss})
	kids := syntheticChildren()
	for i := range 6 {
		extra := kids[i%len(kids)]
		extra.ID, extra.Key = "9"+extra.ID, "PROJ-"+fmt.Sprint(50+i)
		kids = append(kids, extra)
	}
	page := jira.Page[jira.Issue]{Items: kids}
	p.send(childrenMsg{gen: p.editor().childGen, page: page})
	return p
}

func TestChildren_EpicFrame(t *testing.T) {
	t.Parallel()
	p := epicGolden(t, 120, 38)
	p.cursorOnRef("PROJ-32")
	golden(t, "related_epic_120x38.golden", p.frame())
}

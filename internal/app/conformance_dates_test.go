package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// The cascade is driven above the port, so every rule of it has to be met by
// what the binary is actually handed: the field list a read is issued with, the
// shape a value comes back in, and an implementation of the one method rule 4
// needs on both sides of the port.
//
// The tests below are the ones the unit tables cannot be: those build an issue
// by hand, which is a shape no read produces. These go through jiratest and
// through DateFields.Projection, so the two halves of this packet's API cannot
// drift apart without something going red.

var _ SprintDates = (*jiratest.Fake)(nil)

// fakeDateFields resolves the cascade's fields against the fake's own catalogue,
// so that nothing here writes down a customfield id the fake happens to mint.
func fakeDateFields(t *testing.T) DateFields {
	t.Helper()

	catalogue, err := jiratest.New().Fields(t.Context())
	if err != nil {
		t.Fatalf("reading the fake's field catalogue: %v", err)
	}
	return ResolveDateFields(catalogue, nil, nil)
}

// A timeline reads with Projection and nothing else, so rule 4 works only if
// that field list carries the sprint value and the reader answers for the id on
// it. Both ends of that go through the fake here rather than through a hand-set
// field, which is the only way a projection that stopped naming the sprint field
// would be noticed.
func TestConformance_RuleFourResolvesThroughAReadIssuedWithTheProjection(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	f := jiratest.New(jiratest.WithProject("PROJ", jiratest.Scrum), jiratest.WithIssues(jiratest.Gen(2)))
	active := activeSprintOn(t, f, "PROJ")
	if err := f.MoveToSprint(ctx, active.ID, []string{"PROJ-1"}); err != nil {
		t.Fatalf("putting PROJ-1 in sprint %d: %v", active.ID, err)
	}

	fields := fakeDateFields(t)
	page, err := f.Search(ctx, jira.Query{JQL: "project = PROJ ORDER BY key", Fields: fields.Projection().IDs})
	if err != nil {
		t.Fatalf("reading the issues with the timeline projection: %v", err)
	}
	res, err := NewDates(fields, WithNow(testClock()), WithSprints(f)).Resolve(ctx, page.Items)
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}

	got, ok := res.Range("PROJ-1")
	if !ok {
		t.Fatalf("the resolution covers %v and not the issue in the sprint", res.Keys())
	}
	want := Range{
		Start:  jira.DateOf(active.Start.In(time.UTC)),
		End:    jira.DateOf(active.End.In(time.UTC)),
		From:   FromSprint,
		Source: active.Name + " to " + active.Name,
	}
	if got != want {
		t.Errorf("the issue in the sprint resolved %+v, want %+v (%v)", got, want, res.Warnings())
	}
}

// Rule 7 reads an issue's parent and its subtasks, and an issue read without
// them names neither however the issues are related. The link is recorded from
// both ends because a read sees only the end it asked about, so both ends are
// here: one child that names its parent and one parent that names its subtask.
func TestConformance_RuleSevenResolvesThroughAReadIssuedWithTheProjection(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	fields := fakeDateFields(t)
	start, end := day(2026, time.March, 2), day(2026, time.March, 20)
	dated := func() jira.FieldSet {
		var out jira.FieldSet
		out = out.With(fields.targetStart, jira.FieldValue{Kind: jira.KindDate, Date: start})
		return out.With(fields.targetEnd, jira.FieldValue{Kind: jira.KindDate, Date: end})
	}
	project := jira.ProjectRef{Key: "PROJ"}
	f := jiratest.New(jiratest.WithProject("PROJ", jiratest.Scrum), jiratest.WithIssues([]jira.Issue{
		{ID: "9001", Key: "PROJ-901", Project: project},
		{ID: "9002", Key: "PROJ-902", Project: project, Parent: &jira.IssueRef{Key: "PROJ-901"}, Fields: dated()},
		{ID: "9003", Key: "PROJ-903", Project: project, Subtasks: []jira.IssueRef{{Key: "PROJ-904"}}},
		{ID: "9004", Key: "PROJ-904", Project: project, Fields: dated()},
	}))

	page, err := f.Search(ctx, jira.Query{JQL: "project = PROJ ORDER BY key", Fields: fields.Projection().IDs})
	if err != nil {
		t.Fatalf("reading the issues with the timeline projection: %v", err)
	}
	res, err := NewDates(fields, WithNow(testClock()), WithSprints(f)).Resolve(ctx, page.Items)
	if err != nil {
		t.Fatalf("resolving: %v", err)
	}

	for _, tt := range []struct{ parent, linkedBy string }{
		{parent: "PROJ-901", linkedBy: "the child naming its parent"},
		{parent: "PROJ-903", linkedBy: "the parent naming its subtask"},
	} {
		got, _ := res.Range(tt.parent)
		want := Range{Start: start, End: end, From: FromChildren, Source: "1 of its children"}
		if got != want {
			t.Errorf("%s resolved %+v through %s, want %+v: the projection has to ask for the field the link arrives in",
				tt.parent, got, tt.linkedBy, want)
		}
	}
}

// A sprint value reaches a caller in one of two shapes and which one is decided
// by whether the read expanded the schema — a field list naming any non-system
// field is what makes a search send the expand, and a timeline's does. So the
// shape a timeline gets from a site is the raw JSON, and the shape the fake
// sends is the other one.
//
// This is a divergence in pkg/jira/jiratest, which this packet may not edit. The
// cascade reads both shapes, so nothing here is broken by it — but anything
// above the port written against the fake alone meets only the shape a site does
// not send a timeline, which is the failure this repo has already had twice.
func TestConformance_ASprintValueArrivesInTheShapeASchemaExpandedReadSends(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	f := jiratest.New(jiratest.WithProject("PROJ", jiratest.Scrum), jiratest.WithIssues(jiratest.Gen(2)))
	active := activeSprintOn(t, f, "PROJ")
	if err := f.MoveToSprint(ctx, active.ID, []string{"PROJ-1"}); err != nil {
		t.Fatalf("putting PROJ-1 in sprint %d: %v", active.ID, err)
	}

	fields := fakeDateFields(t)
	page, err := f.Search(ctx, jira.Query{JQL: "project = PROJ ORDER BY key", Fields: fields.Projection().IDs})
	if err != nil {
		t.Fatalf("reading the issues with the timeline projection: %v", err)
	}
	var iss jira.Issue
	for i := range page.Items {
		if page.Items[i].Key == "PROJ-1" {
			iss = page.Items[i]
		}
	}
	value, ok := iss.Fields.Get(fields.sprint)
	if !ok {
		t.Fatalf("the issue came back with no value for %s at all", fields.sprint.ID)
	}
	if value.Kind != jira.KindUnknown {
		t.Errorf("the sprint value came back as kind %d, want kind %d carrying the JSON.\n"+
			"A field list naming a custom field makes a search expand the schema; the sprint field is then declared "+
			"array of json, which the decoder has no slot for, so a site sends a timeline the bytes and not options. "+
			"The fake answers with options whatever the field list said, so it models the endpoint that sends no "+
			"schema and never the one a timeline reads. It decides the shape the way pkg/jira/cloud does — by whether "+
			"the query names a non-system field — or a view drawing bars off the fake breaks against a site.",
			value.Kind, jira.KindUnknown)
	}
}

// Rule 4 is the one rule of the cascade that needs a call, and the port declares
// it: jira.SprintReader carries Sprint(ctx, id). An adapter that does not
// implement it makes rule 4 a rule no test above the port can meet against a
// site, because the fake implements it and the suite is green either way.
//
// This is a divergence in pkg/jira/cloud, which this packet may not edit. It
// goes green the moment the sprint adapter lands.
func TestConformance_RuleFourHasAnImplementationOnBothSidesOfThePort(t *testing.T) {
	t.Parallel()

	if _, ok := any((*jiratest.Fake)(nil)).(SprintDates); !ok {
		t.Error("the fake does not answer Sprint, so rule 4 has no implementation at all")
	}
	if !cloudClientMethods(t)["Sprint"] {
		t.Error("pkg/jira/cloud's Client has no Sprint method, so rule 4 answers against the fake and falls " +
			"through against a site: an issue whose only dates are its sprint's draws no bar for a real user and " +
			"every test above the port passes. pkg/jira/port.go declares Sprint(ctx, id) and " +
			"pkg/jira/jiratest implements it; the cloud adapter and pkg/jira/cloud/assert.go's jira.SprintReader " +
			"entry are what is missing.")
	}
}

// cloudClientMethods is the method set of pkg/jira/cloud's Client, read from the
// source: internal/app may not import the adapter, and the layering test in
// internal/arch is what says so.
func cloudClientMethods(t *testing.T) map[string]bool {
	t.Helper()

	dir := filepath.Join("..", "..", "pkg", "jira", "cloud")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	fset := token.NewFileSet()
	out := make(map[string]bool)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		if file.Name.Name != "cloud" {
			t.Fatalf("%s declares package %s, so this check is looking in the wrong place", name, file.Name.Name)
		}
		for _, decl := range file.Decls {
			fn, isFunc := decl.(*ast.FuncDecl)
			if !isFunc || fn.Recv == nil || len(fn.Recv.List) != 1 {
				continue
			}
			pointer, isPointer := fn.Recv.List[0].Type.(*ast.StarExpr)
			if !isPointer {
				continue
			}
			if named, isNamed := pointer.X.(*ast.Ident); isNamed && named.Name == "Client" {
				out[fn.Name.Name] = true
			}
		}
	}
	if len(out) == 0 {
		t.Fatalf("%s declares no method on *Client, so this check proves nothing", dir)
	}
	return out
}

func activeSprintOn(t *testing.T, f *jiratest.Fake, projectKey string) jira.Sprint {
	t.Helper()

	boards, err := f.Boards(t.Context(), projectKey)
	if err != nil {
		t.Fatalf("reading the boards on %s: %v", projectKey, err)
	}
	if len(boards) == 0 {
		t.Fatalf("%s has no board, so it has no sprint either", projectKey)
	}
	page, err := f.Sprints(t.Context(), boards[0].ID, jira.SprintActive)
	if err != nil {
		t.Fatalf("reading the sprints on board %d: %v", boards[0].ID, err)
	}
	for _, sprint := range page.Items {
		if sprint.Start != nil && sprint.End != nil {
			return sprint
		}
	}
	t.Fatalf("board %d has no active sprint with both dates: %+v", boards[0].ID, page.Items)
	return jira.Sprint{}
}

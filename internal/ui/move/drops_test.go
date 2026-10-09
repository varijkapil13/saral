package move

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func meta(id, name string) jira.FieldMeta {
	return jira.FieldMeta{Field: jira.FieldRef{ID: id, Name: name}, Name: name}
}

func TestFieldName_IsTheSitesOwnNameDrawnSafely(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		meta jira.FieldMeta
		want string
	}{
		"the screen's label":             {meta: meta("customfield_1", "Kostenstelle"), want: "Kostenstelle"},
		"the catalogue name without one": {meta: jira.FieldMeta{Field: jira.FieldRef{ID: "customfield_1", Name: "Katalog"}}, want: "Katalog"},
		"the id without either":          {meta: jira.FieldMeta{Field: jira.FieldRef{ID: "customfield_2"}}, want: "customfield_2"},
		"a terminal escape is not drawn": {meta: meta("customfield_1", "Kosten\x1b[31mstelle\x07"), want: "Kostenstelle"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := fieldName(&tc.meta); got != tc.want {
				t.Errorf("fieldName = %q, want %q", got, tc.want)
			}
		})
	}
}

// screens is the fake with the target's create screen narrowed, which is the one
// thing the fake cannot be told: it answers the same screen for every project.
type screens struct {
	*jiratest.Fake
	mu      sync.Mutex
	without map[string]bool
	rename  map[string]string
	fail    map[string]error
	queries []jira.Query
}

func (s *screens) CreateMeta(ctx context.Context, project, typeID string) (jira.Schema, error) {
	s.mu.Lock()
	err := s.fail["CreateMeta:"+project]
	s.mu.Unlock()
	if err != nil {
		return jira.Schema{}, err
	}
	schema, err := s.Fake.CreateMeta(ctx, project, typeID)
	if err != nil {
		return schema, err
	}
	kept := schema.Fields[:0:0]
	for i := range schema.Fields {
		f := schema.Fields[i]
		if project == "OTHER" && s.without[f.Field.ID] {
			continue
		}
		if name, ok := s.rename[f.Field.ID]; ok && project != "OTHER" {
			f.Name = name
		}
		kept = append(kept, f)
	}
	schema.Fields = kept
	return schema, nil
}

func (s *screens) Search(ctx context.Context, q jira.Query) (jira.Page[jira.Issue], error) {
	s.mu.Lock()
	s.queries = append(s.queries, q)
	err := s.fail["Search"]
	s.mu.Unlock()
	if err != nil {
		return jira.Page[jira.Issue]{}, err
	}
	return s.Fake.Search(ctx, q)
}

func (s *screens) valueReads() []jira.Query {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]jira.Query, 0, len(s.queries))
	for _, q := range s.queries {
		if strings.HasPrefix(q.JQL, "key in (") {
			out = append(out, q)
		}
	}
	return out
}

func (s *screens) failing(call string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fail[call] = err
}

func (s *screens) healed() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fail = map[string]error{}
}

// narrowed is a target whose create screen has no labels, no priority and no
// story points, over PROJ-1 to PROJ-3, which between them hold all three. The
// issues are as a list read them, which did not ask for any of the three.
func narrowed(t *testing.T) (*screens, []jira.Issue) {
	t.Helper()
	f := newFake(6, jiratest.WithIssues(jiratest.GenFor("OTHER", 4)))
	fields, err := f.Fields(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	points, ok := jira.FieldByName(fields, "Story Points")
	if !ok {
		t.Fatal("the fake has no story point field")
	}
	s := &screens{
		Fake:    f,
		without: map[string]bool{"labels": true, "priority": true, points.ID: true},
		rename:  map[string]string{points.ID: "Geschätzte Punkte"},
		fail:    map[string]error{},
	}
	iss := seeded(t, f, "PROJ-1", "PROJ-2", "PROJ-3")
	for i := range iss {
		iss[i].Requested = jira.NewFieldMask([]string{"summary", "status", "issuetype", "project"})
	}
	return s, iss
}

func TestMove_TheConfirmScreenNamesTheFieldsTheTargetHasNoPlaceFor(t *testing.T) {
	t.Parallel()
	s, iss := narrowed(t)
	dr := newDriver(t, testDeps(s), 120, 30, WithIssues(iss))
	dr.walkTo("OTHER")

	frame := dr.view()
	mustContain(t, frame, "Not on the Story create screen in OTHER",
		"Priority -> 3 of 3 issues", "Labels -> 2 of 3 issues", "Geschätzte Punkte -> 1 of 3 issues")
	if dr.m.drop != dropDone {
		t.Fatalf("the check ended in state %d", dr.m.drop)
	}

	reads := s.valueReads()
	if len(reads) != 1 {
		t.Fatalf("the values were read %d times, want once", len(reads))
	}
	for _, id := range reads[0].Fields {
		if !s.without[id] {
			t.Errorf("the value read asked for %q, which the target's screen has a place for", id)
		}
	}

	dr.key("y")
	if n := countCalls(s.Fake, "BulkMove"); n != 1 {
		t.Errorf("the move was submitted %d times after the warning was read", n)
	}
}

func TestMove_IssuesAlreadyReadWithTheFieldsAreNotReadAgain(t *testing.T) {
	t.Parallel()
	s, iss := narrowed(t)
	for i := range iss {
		iss[i].Requested = jira.AllFields()
	}
	dr := newDriver(t, testDeps(s), 120, 30, WithIssues(iss))
	dr.walkTo("OTHER")

	mustContain(t, dr.view(), "Priority -> 3 of 3 issues")
	if reads := s.valueReads(); len(reads) != 0 {
		t.Errorf("issues read with every field were read again: %+v", reads)
	}
}

func TestMove_ASourceScreenAlreadyReadIsNotAskedForAgain(t *testing.T) {
	t.Parallel()
	s, iss := narrowed(t)
	dr := newDriver(t, testDeps(s), 120, 30, WithIssues(iss))
	dr.walkTo("OTHER")
	first := countCalls(s.Fake, "CreateMeta")

	dr.key("shift+tab", "shift+tab", "enter", "enter")
	if dr.m.step != stepConfirm {
		t.Fatalf("the walk back stopped on step %d", dr.m.step)
	}
	if got := countCalls(s.Fake, "CreateMeta") - first; got != 1 {
		t.Errorf("choosing the type again read %d create screens, want only the target's", got)
	}
}

func TestMove_AMoveWithinTheTargetProjectChecksNothing(t *testing.T) {
	t.Parallel()
	s, _ := narrowed(t)
	iss := seeded(t, s.Fake, "OTHER-1", "OTHER-2")
	dr := newDriver(t, testDeps(s), 120, 30, WithIssues(iss))
	dr.walkTo("OTHER")

	if dr.m.drop != dropNone {
		t.Errorf("a move into the project the issues are in ran the check (state %d)", dr.m.drop)
	}
	mustNotContain(t, dr.view(), "create screen", "could not be checked", "checking which fields")
	if n := countCalls(s.Fake, "CreateMeta"); n != 1 {
		t.Errorf("%d create screens were read, want only the target's", n)
	}
}

func failures() map[string]struct {
	err  error
	want string
} {
	return map[string]struct {
		err  error
		want string
	}{
		"a refusal": {
			err:  &jira.CapabilityError{Capability: jira.CapBulkMove, Reason: "You may not browse PROJ"},
			want: "You may not browse PROJ",
		},
		"a rate limit": {
			err:  &jira.RateLimitError{RetryAfter: 30 * time.Second, Endpoint: "/issue/createmeta"},
			want: "rate limited by Jira",
		},
		"a transport failure": {
			err:  &jira.TransportError{Op: "read the create screen", Err: errors.New("dial tcp: no route to host")},
			want: "no route to host",
		},
	}
}

func TestMove_ACheckThatCouldNotRunSaysSoAndStillLetsTheMoveGo(t *testing.T) {
	t.Parallel()
	for _, call := range []string{"CreateMeta:PROJ", "Search"} {
		for name, tc := range failures() {
			t.Run(call+"/"+name, func(t *testing.T) {
				t.Parallel()
				s, iss := narrowed(t)
				s.failing(call, tc.err)
				dr := newDriver(t, testDeps(s), 120, 30, WithIssues(iss))
				dr.walkTo("OTHER")

				frame := dr.view()
				mustContain(t, frame, "Which fields the move drops could not be checked", tc.want, retryHint)
				mustNotContain(t, frame, "create screen in OTHER")

				dr.key("y")
				if n := countCalls(s.Fake, "BulkMove"); n != 1 {
					t.Errorf("a failed check blocked the move: %d submits", n)
				}
			})
		}
	}
}

func TestMove_ATargetScreenThatCouldNotBeReadStillReachesTheConfirmScreen(t *testing.T) {
	t.Parallel()
	for name, tc := range failures() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s, iss := narrowed(t)
			s.failing("CreateMeta:OTHER", tc.err)
			dr := newDriver(t, testDeps(s), 120, 30, WithIssues(iss))
			dr.walkTo("OTHER")

			mustContain(t, dr.view(), "What OTHER insists on could not be read",
				"every mandatory field is kept", tc.want)
			if got := dr.lastStatus().Text; !strings.Contains(got, tc.want) {
				t.Errorf("the status line says %q rather than the site's own words", got)
			}
			dr.key("y")
			if n := countCalls(s.Fake, "BulkMove"); n != 1 {
				t.Fatalf("the move was submitted %d times", n)
			}
		})
	}
}

func TestMove_RefreshingAFailedCheckRunsTheCheckAgainWithoutLeavingTheConfirmScreen(t *testing.T) {
	t.Parallel()
	s, iss := narrowed(t)
	s.failing("Search", &jira.RateLimitError{})
	dr := newDriver(t, testDeps(s), 120, 30, WithIssues(iss))
	dr.walkTo("OTHER")
	mustContain(t, dr.view(), "could not be checked")

	s.healed()
	dr.send(kernel.RefreshMsg{})
	if dr.m.step != stepConfirm {
		t.Fatalf("a refresh took the wizard to step %d", dr.m.step)
	}
	mustContain(t, dr.view(), "Priority -> 3 of 3 issues")
	mustNotContain(t, dr.view(), "could not be checked")
}

func TestMove_TheSubmitWaitsForACheckStillInFlight(t *testing.T) {
	t.Parallel()
	s, iss := narrowed(t)
	dr := newDriver(t, testDeps(s), 120, 30, WithIssues(iss))
	dr.walkTo("OTHER")
	pending := dr.m.startDrops()
	mustContain(t, dr.view(), "checking which fields OTHER has no place for")

	dr.key("y")
	if n := countCalls(s.Fake, "BulkMove"); n != 0 {
		t.Fatalf("a move was submitted before the check answered")
	}
	mustContain(t, dr.view(), "still checking which fields OTHER has no place for")

	dr.run(pending)
	dr.key("y")
	if n := countCalls(s.Fake, "BulkMove"); n != 1 {
		t.Errorf("the move was submitted %d times once the check answered", n)
	}
}

func TestMove_AnAnswerToACheckAlreadyReplacedIsDropped(t *testing.T) {
	t.Parallel()
	s, iss := narrowed(t)
	dr := newDriver(t, testDeps(s), 120, 30, WithIssues(iss))
	dr.walkTo("OTHER")
	stale := dr.m.dropGen
	dr.m.startDrops()

	dr.send(droppedMsg{gen: stale, err: &jira.RateLimitError{}})
	if dr.m.drop != dropPending {
		t.Errorf("a stale answer landed and left the check in state %d", dr.m.drop)
	}
}

func TestMove_ManyDroppedFieldsFoldIntoOneLine(t *testing.T) {
	t.Parallel()
	s, iss := narrowed(t)
	dr := newDriver(t, testDeps(s), 120, 30, WithIssues(iss))
	dr.walkTo("OTHER")
	names := []string{"Eins", "Zwei", "Drei", "Vier", "Fünf", "Sechs", "Sieben"}
	drops := make([]dropped, 0, len(names))
	for i, n := range names {
		drops = append(drops, dropped{id: "customfield_" + n, name: n, count: len(names) - i})
	}
	dr.send(droppedMsg{gen: dr.m.dropGen, fields: drops, leaving: 7})

	frame := dr.view()
	mustContain(t, frame, "Eins -> 7 of 7 issues", "Vier -> 4 of 7 issues", "and 3 more fields: Fünf, Sechs, Sieben",
		"There is no undo.")
	mustNotContain(t, frame, "Fünf -> ")
}

func TestMove_GoldenConfirmWithDroppedFields(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		width, height int
		golden        string
		fail          error
	}{
		"wide":                 {width: 120, height: 24, golden: "confirm_drops_120x24.golden"},
		"standard":             {width: 80, height: 24, golden: "confirm_drops_80x24.golden"},
		"narrow":               {width: 44, height: 30, golden: "confirm_drops_44x30.golden"},
		"a check that failed":  {width: 80, height: 24, golden: "confirm_dropsfailed_80x24.golden", fail: &jira.RateLimitError{RetryAfter: 30 * time.Second}},
		"narrow, check failed": {width: 44, height: 30, golden: "confirm_dropsfailed_44x30.golden", fail: &jira.RateLimitError{RetryAfter: 30 * time.Second}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s, iss := narrowed(t)
			if tc.fail != nil {
				s.failing("Search", tc.fail)
			}
			dr := newDriver(t, testDeps(s), tc.width, tc.height, WithIssues(iss))
			dr.walkTo("OTHER")
			golden(t, tc.golden, dr.view())
		})
	}
}

package plan

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/varijkapil13/saral/pkg/jira"
)

type versionStub struct {
	versions map[string][]jira.Version
	errs     map[string]error
	projects map[int64][]jira.ProjectRef
	boardErr map[int64]error

	versionCalls []string
	boardCalls   []int64
}

func (s *versionStub) Versions(_ context.Context, ref string) ([]jira.Version, error) {
	s.versionCalls = append(s.versionCalls, ref)
	if err := s.errs[ref]; err != nil {
		return nil, err
	}
	return s.versions[ref], nil
}

func (*versionStub) UnresolvedCount(context.Context, string) (int, error) { return 0, nil }

func (s *versionStub) BoardProjects(_ context.Context, id int64) ([]jira.ProjectRef, error) {
	s.boardCalls = append(s.boardCalls, id)
	if err := s.boardErr[id]; err != nil {
		return nil, err
	}
	return s.projects[id], nil
}

const browseRefusal = "You must have browse project rights in order to view versions."

func projectSources(refs ...string) []jira.PlanSource {
	out := make([]jira.PlanSource, 0, len(refs))
	for _, ref := range refs {
		out = append(out, jira.PlanSource{Type: jira.PlanSourceProject, Value: ref})
	}
	return out
}

func boardSources(ids ...string) []jira.PlanSource {
	out := make([]jira.PlanSource, 0, len(ids))
	for _, id := range ids {
		out = append(out, jira.PlanSource{Type: jira.PlanSourceBoard, Value: id})
	}
	return out
}

func readOf(t *testing.T, stub *versionStub, sources []jira.PlanSource) releasesMsg {
	t.Helper()
	msg := readReleases(context.Background(), stub, "42", sources, 3)()
	got, ok := msg.(releasesMsg)
	if !ok {
		t.Fatalf("the read failed: %#v", msg)
	}
	return got
}

func TestReadReleases_LeavesOutAProjectTheTokenCannotBrowse(t *testing.T) {
	t.Parallel()

	stub := &versionStub{
		versions: map[string][]jira.Version{"10021": {{ID: "1", Name: "1.0"}}, "10453": {{ID: "2", Name: "2.0"}}},
		errs:     map[string]error{"10011": &jira.ValidationError{Messages: []string{browseRefusal}}},
	}
	got := readOf(t, stub, projectSources("10021", "10011", "10453"))

	if len(got.versions) != 2 {
		t.Errorf("kept %d versions, want the 2 the readable projects answered", len(got.versions))
	}
	if len(got.refused) != 1 || got.refused[0].kind != "project" || got.refused[0].ref != "10011" || got.refused[0].reason != browseRefusal {
		t.Errorf("refused = %+v, want project 10011 in the site's words", got.refused)
	}
}

func TestReadReleases_NamesAMissingProjectOnce(t *testing.T) {
	t.Parallel()

	stub := &versionStub{errs: map[string]error{"10011": &jira.NotFoundError{Kind: "project", ID: "10011"}}}
	got := readOf(t, stub, projectSources("10011"))
	if len(got.refused) != 1 {
		t.Fatalf("a project the site cannot find failed the read: %+v", got)
	}
	if strings.Contains(got.refused[0].reason, "10011") {
		t.Errorf("reason %q names the project the row already names", got.refused[0].reason)
	}
}

func TestReadReleases_ARateLimitStillFailsTheRead(t *testing.T) {
	t.Parallel()

	stub := &versionStub{
		versions: map[string][]jira.Version{"10021": {{ID: "1", Name: "1.0"}}},
		errs:     map[string]error{"10011": &jira.RateLimitError{}},
	}
	msg := readReleases(context.Background(), stub, "42", projectSources("10021", "10011"), 3)()
	if _, ok := msg.(failedMsg); !ok {
		t.Fatalf("a rate limit was taken as a refusal of one project: %#v", msg)
	}
}

func TestReadReleases_ReadsTheProjectsBehindABoard(t *testing.T) {
	t.Parallel()

	stub := &versionStub{
		projects: map[int64][]jira.ProjectRef{17: {{ID: "10000", Key: "EX"}, {ID: "10001", Key: "OPS"}}},
		versions: map[string][]jira.Version{"10000": {{ID: "1", Name: "1.0"}}, "10001": {{ID: "2", Name: "2.0"}}},
	}
	got := readOf(t, stub, boardSources("17"))

	if !slices.Equal(got.read, []string{"10000", "10001"}) {
		t.Errorf("read %v, want both projects behind the board", got.read)
	}
	if !slices.Equal(got.boards["17"], []string{"EX", "OPS"}) || got.names["10001"] != "OPS" {
		t.Errorf("boards = %v, names = %v, want the keys behind board 17", got.boards, got.names)
	}
	if !slices.Equal(got.owners, []string{"10000", "10001"}) || len(got.versions) != 2 {
		t.Errorf("owners = %v for %d versions", got.owners, len(got.versions))
	}
}

func TestReadReleases_ReadsAProjectSharedByTwoBoardsOnce(t *testing.T) {
	t.Parallel()

	shared := []jira.ProjectRef{{ID: "10000", Key: "EX"}}
	stub := &versionStub{
		projects: map[int64][]jira.ProjectRef{17: shared, 18: shared},
		versions: map[string][]jira.Version{"10000": {{ID: "1", Name: "1.0"}}},
	}
	sources := append(projectSources("10000"), boardSources("17", "18")...)
	got := readOf(t, stub, sources)

	if len(stub.versionCalls) != 1 {
		t.Errorf("Versions was called %d times (%v), want once for the shared project", len(stub.versionCalls), stub.versionCalls)
	}
	if len(got.versions) != 1 {
		t.Errorf("got %d versions, want the project's one", len(got.versions))
	}
}

func TestReadReleases_LeavesOutABoardTheTokenCannotSee(t *testing.T) {
	t.Parallel()

	stub := &versionStub{
		projects: map[int64][]jira.ProjectRef{18: {{ID: "10000", Key: "EX"}}},
		boardErr: map[int64]error{17: &jira.NotFoundError{Kind: "board", ID: "17", Detail: "The requested board cannot be viewed."}},
		versions: map[string][]jira.Version{"10000": {{ID: "1", Name: "1.0"}}},
	}
	got := readOf(t, stub, boardSources("17", "18"))

	if len(got.refused) != 1 || got.refused[0].kind != "board" || got.refused[0].ref != "17" ||
		got.refused[0].reason != "The requested board cannot be viewed." {
		t.Errorf("refused = %+v, want board 17 in the site's words", got.refused)
	}
	if len(got.versions) != 1 {
		t.Errorf("the readable board's releases were lost: %+v", got.versions)
	}
}

func TestReadReleases_NamesABoardThatAnsweredNoProjects(t *testing.T) {
	t.Parallel()

	got := readOf(t, &versionStub{}, boardSources("17"))
	if len(got.refused) != 1 || got.refused[0].kind != "board" || !strings.Contains(got.refused[0].reason, "named no project") {
		t.Errorf("refused = %+v, want board 17 named as having no project behind it", got.refused)
	}
}

func TestReadReleases_ARefusedBoardLicenceIsLeftOut(t *testing.T) {
	t.Parallel()

	const reason = "Jira Software is not licensed on this site."
	stub := &versionStub{boardErr: map[int64]error{17: &jira.CapabilityError{Capability: jira.CapBoards, Reason: reason}}}
	got := readOf(t, stub, boardSources("17"))
	if len(got.refused) != 1 || got.refused[0].reason != reason {
		t.Errorf("refused = %+v, want board 17 refused in the site's words", got.refused)
	}
}

func TestReadReleases_AFailureOnABoardFailsTheRead(t *testing.T) {
	t.Parallel()

	for name, err := range map[string]error{
		"a rate limit":      &jira.RateLimitError{},
		"a transport error": &jira.TransportError{Op: "GET /board/17/project"},
		"an auth failure":   &jira.AuthError{},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			stub := &versionStub{boardErr: map[int64]error{17: err}}
			msg := readReleases(context.Background(), stub, "42", boardSources("17"), 3)()
			if _, ok := msg.(failedMsg); !ok {
				t.Fatalf("%s on a board was taken as a refusal of it: %#v", name, msg)
			}
		})
	}
}

func TestReadReleases_ABoardValueThatIsNotANumberIsNamed(t *testing.T) {
	t.Parallel()

	stub := &versionStub{}
	got := readOf(t, stub, boardSources("backlog", "0"))

	if len(got.refused) != 2 || got.refused[0].ref != "backlog" || !strings.Contains(got.refused[0].reason, "board id") {
		t.Errorf("refused = %+v, want both values named as not board ids", got.refused)
	}
	if len(stub.boardCalls) != 0 {
		t.Errorf("BoardProjects was asked about %v", stub.boardCalls)
	}
}

func TestPlans_ARefusedProjectIsNamedBesideTheReleasesThatWereRead(t *testing.T) {
	t.Parallel()

	f := newFake(5)
	dr := newDriver(t, testDeps(f), 160, 30)
	dr.send(plansMsg{gen: dr.m.gen, plans: []jira.Plan{{
		ID: "42", Name: "Delivery", Status: "Active",
		Sources: projectSources("10021", "10011"),
	}}})
	dr.key("enter")
	dr.send(releasesMsg{
		gen: dr.m.gen, plan: "42",
		versions: []jira.Version{{ID: "1", Name: "Spring drop"}},
		refused:  []refusal{{kind: "project", ref: "10011", reason: browseRefusal}},
	})

	frame := dr.view()
	mustContain(t, frame, "Spring drop", "project id 10011 left out", "browse project rights")
}

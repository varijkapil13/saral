package plan

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/varijkapil13/saral/pkg/jira"
)

type versionStub struct {
	versions map[string][]jira.Version
	errs     map[string]error
	projects map[int64][]jira.ProjectRef
	boardErr map[int64]error

	details   map[string]jira.PlanDetail
	detailErr error

	keys       map[string]string
	projectErr map[string]error

	mu           sync.Mutex
	projectCalls []string
	versionCalls []string
	boardCalls   []int64
	detailCalls  []string
}

func (s *versionStub) PlanDetail(_ context.Context, id string) (jira.PlanDetail, error) {
	s.detailCalls = append(s.detailCalls, id)
	if s.detailErr != nil {
		return jira.PlanDetail{}, s.detailErr
	}
	return s.details[id], nil
}

func (s *versionStub) Versions(_ context.Context, ref string) ([]jira.Version, error) {
	s.versionCalls = append(s.versionCalls, ref)
	if err := s.errs[ref]; err != nil {
		return nil, err
	}
	return s.versions[ref], nil
}

func (s *versionStub) Project(_ context.Context, ref string) (jira.ProjectRef, error) {
	s.mu.Lock()
	s.projectCalls = append(s.projectCalls, ref)
	s.mu.Unlock()
	if err := s.projectErr[ref]; err != nil {
		return jira.ProjectRef{}, err
	}
	if key := s.keys[ref]; key != "" {
		return jira.ProjectRef{ID: ref, Key: key}, nil
	}
	return jira.ProjectRef{}, &jira.NotFoundError{Kind: "project", ID: ref}
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
	return readPlanOf(t, stub, jira.Plan{ID: "42", Sources: sources})
}

func readPlanOf(t *testing.T, stub *versionStub, plan jira.Plan) releasesMsg {
	t.Helper()
	msg := readReleases(context.Background(), stub, plan, nil, 3)()
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

func TestReadReleases_CarriesCrossSpaceReleasesAndExclusions(t *testing.T) {
	t.Parallel()

	want := jira.PlanDetail{
		CrossProjectReleases: []jira.CrossProjectRelease{{Name: "Spring launch", VersionIDs: []string{"1", "2"}}},
		ExcludedVersionIDs:   []string{"2"},
	}
	stub := &versionStub{
		versions: map[string][]jira.Version{"10000": {{ID: "1", Name: "1.0"}, {ID: "2", Name: "2.0"}}},
		details:  map[string]jira.PlanDetail{"42": want},
	}
	got := readOf(t, stub, projectSources("10000"))

	if got.detail == nil || len(got.detail.CrossProjectReleases) != 1 || got.detail.CrossProjectReleases[0].Name != "Spring launch" {
		t.Errorf("detail = %+v, want the plan's cross-space release", got.detail)
	}
	if got.detail == nil || !slices.Equal(got.detail.ExcludedVersionIDs, []string{"2"}) {
		t.Errorf("detail = %+v, want version 2 excluded", got.detail)
	}
	if got.detailErr != nil {
		t.Errorf("detailErr = %v on a detail that was read", got.detailErr)
	}
	if !slices.Equal(stub.detailCalls, []string{"42"}) {
		t.Errorf("PlanDetail was asked about %v, want the plan once", stub.detailCalls)
	}
}

func TestReadReleases_ALocalPlanAsksForNoDetail(t *testing.T) {
	t.Parallel()

	stub := &versionStub{versions: map[string][]jira.Version{"PROJ": {{ID: "1", Name: "1.0"}}}}
	got := readPlanOf(t, stub, jira.Plan{ID: "local:0:x", Local: true, Sources: projectSources("PROJ")})

	if len(stub.detailCalls) != 0 || got.detail != nil || got.detailErr != nil {
		t.Errorf("a local plan read a detail: calls %v, detail %+v, err %v", stub.detailCalls, got.detail, got.detailErr)
	}
}

func TestReadReleases_ARefusedDetailLeavesTheVersionsAndKeepsTheReason(t *testing.T) {
	t.Parallel()

	stub := &versionStub{
		versions:  map[string][]jira.Version{"10000": {{ID: "1", Name: "1.0"}}},
		detailErr: &jira.CapabilityError{Capability: jira.CapPlans, Reason: "the Plans API needs Administer Jira"},
	}
	got := readOf(t, stub, projectSources("10000"))

	if len(got.versions) != 1 || got.detail != nil || got.detailErr == nil {
		t.Errorf("versions %d, detail %+v, err %v; want the versions with the refusal beside them",
			len(got.versions), got.detail, got.detailErr)
	}
}

func TestReadReleases_ARateLimitStillFailsTheRead(t *testing.T) {
	t.Parallel()

	stub := &versionStub{
		versions: map[string][]jira.Version{"10021": {{ID: "1", Name: "1.0"}}},
		errs:     map[string]error{"10011": &jira.RateLimitError{}},
	}
	msg := readReleases(context.Background(), stub, jira.Plan{ID: "42", Sources: projectSources("10021", "10011")}, nil, 3)()
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
			msg := readReleases(context.Background(), stub, jira.Plan{ID: "42", Sources: boardSources("17")}, nil, 3)()
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
		names:    map[string]string{"10021": "EX", "10011": "OPS"},
		refused:  []refusal{{kind: "project", ref: "10011", reason: browseRefusal}},
		read:     []string{"10021"},
	})

	frame := dr.view()
	mustContain(t, frame, "1 in EX - enter browses", "project OPS left out", "browse project rights")
}

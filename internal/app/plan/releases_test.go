package plan

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

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

func readOf(t *testing.T, stub *versionStub, sources []jira.PlanSource) Releases {
	t.Helper()
	return readPlanOf(t, stub, jira.Plan{ID: "42", Sources: sources})
}

func readPlanOf(t *testing.T, stub *versionStub, plan jira.Plan) Releases {
	t.Helper()
	got, err := ReadReleases(context.Background(), stub, plan, nil)
	if err != nil {
		t.Fatalf("the read failed: %v", err)
	}
	return got
}

func sitePlanOf(sources []jira.PlanSource) jira.Plan {
	return jira.Plan{ID: "42", Name: "Delivery", Sources: sources}
}

func refusedAs[E error](r Refusal) bool {
	var target E
	return errors.As(r.Err, &target)
}

func TestReadReleases_LeavesOutAProjectTheTokenCannotBrowse(t *testing.T) {
	t.Parallel()

	stub := &versionStub{
		versions: map[string][]jira.Version{"10021": {{ID: "1", Name: "1.0"}}, "10453": {{ID: "2", Name: "2.0"}}},
		errs:     map[string]error{"10011": &jira.ValidationError{Messages: []string{browseRefusal}}},
	}
	got := readOf(t, stub, projectSources("10021", "10011", "10453"))

	if len(got.Versions) != 2 {
		t.Errorf("kept %d versions, want the 2 the readable projects answered", len(got.Versions))
	}
	if len(got.Refused) != 1 || got.Refused[0].Kind != RefusedProject || got.Refused[0].Ref != "10011" ||
		!refusedAs[*jira.ValidationError](got.Refused[0]) {
		t.Errorf("refused = %+v, want project 10011 with the site's refusal", got.Refused)
	}
}

func TestReadReleases_LeavesOutAMissingProject(t *testing.T) {
	t.Parallel()

	stub := &versionStub{errs: map[string]error{"10011": &jira.NotFoundError{Kind: "project", ID: "10011"}}}
	got := readOf(t, stub, projectSources("10011"))
	if len(got.Refused) != 1 || !refusedAs[*jira.NotFoundError](got.Refused[0]) {
		t.Fatalf("a project the site cannot find was not left out by name: %+v", got)
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

	if got.Detail == nil || len(got.Detail.CrossProjectReleases) != 1 || got.Detail.CrossProjectReleases[0].Name != "Spring launch" {
		t.Errorf("detail = %+v, want the plan's cross-space release", got.Detail)
	}
	if got.Detail == nil || !slices.Equal(got.Detail.ExcludedVersionIDs, []string{"2"}) {
		t.Errorf("detail = %+v, want version 2 excluded", got.Detail)
	}
	if got.DetailErr != nil {
		t.Errorf("DetailErr = %v on a detail that was read", got.DetailErr)
	}
	if !slices.Equal(stub.detailCalls, []string{"42"}) {
		t.Errorf("PlanDetail was asked about %v, want the plan once", stub.detailCalls)
	}
	if n := ExcludedCount(got.Versions, got.Detail); n != 1 {
		t.Errorf("ExcludedCount = %d, want 1", n)
	}
}

func TestReadReleases_ALocalPlanAsksForNoDetail(t *testing.T) {
	t.Parallel()

	stub := &versionStub{versions: map[string][]jira.Version{"PROJ": {{ID: "1", Name: "1.0"}}}}
	got := readPlanOf(t, stub, jira.Plan{ID: "local:0:x", Local: true, Sources: projectSources("PROJ")})

	if len(stub.detailCalls) != 0 || got.Detail != nil || got.DetailErr != nil {
		t.Errorf("a local plan read a detail: calls %v, detail %+v, err %v", stub.detailCalls, got.Detail, got.DetailErr)
	}
}

func TestReadReleases_ARefusedDetailLeavesTheVersionsAndKeepsTheReason(t *testing.T) {
	t.Parallel()

	stub := &versionStub{
		versions:  map[string][]jira.Version{"10000": {{ID: "1", Name: "1.0"}}},
		detailErr: &jira.CapabilityError{Capability: jira.CapPlans, Reason: "the Plans API needs Administer Jira"},
	}
	got := readOf(t, stub, projectSources("10000"))

	if len(got.Versions) != 1 || got.Detail != nil || got.DetailErr == nil {
		t.Errorf("versions %d, detail %+v, err %v; want the versions with the refusal beside them",
			len(got.Versions), got.Detail, got.DetailErr)
	}
}

func TestReadReleases_ARateLimitStillFailsTheRead(t *testing.T) {
	t.Parallel()

	stub := &versionStub{
		versions: map[string][]jira.Version{"10021": {{ID: "1", Name: "1.0"}}},
		errs:     map[string]error{"10011": &jira.RateLimitError{}},
	}
	_, err := ReadReleases(context.Background(), stub, jira.Plan{ID: "42", Sources: projectSources("10021", "10011")}, nil)
	var rl *jira.RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("a rate limit was taken as a refusal of one project: %v", err)
	}
}

func TestReadReleases_ReadsTheProjectsBehindABoard(t *testing.T) {
	t.Parallel()

	stub := &versionStub{
		projects: map[int64][]jira.ProjectRef{17: {{ID: "10000", Key: "EX"}, {ID: "10001", Key: "OPS"}}},
		versions: map[string][]jira.Version{"10000": {{ID: "1", Name: "1.0"}}, "10001": {{ID: "2", Name: "2.0"}}},
	}
	got := readOf(t, stub, boardSources("17"))

	if !slices.Equal(got.Read, []string{"10000", "10001"}) {
		t.Errorf("read %v, want both projects behind the board", got.Read)
	}
	if !slices.Equal(got.Boards["17"], []string{"EX", "OPS"}) || got.Names["10001"] != "OPS" {
		t.Errorf("boards = %v, names = %v, want the keys behind board 17", got.Boards, got.Names)
	}
	if !slices.Equal(got.Owners, []string{"10000", "10001"}) || len(got.Versions) != 2 {
		t.Errorf("owners = %v for %d versions", got.Owners, len(got.Versions))
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
	if len(got.Versions) != 1 {
		t.Errorf("got %d versions, want the project's one", len(got.Versions))
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

	if len(got.Refused) != 1 || got.Refused[0].Kind != RefusedBoard || got.Refused[0].Ref != "17" ||
		!refusedAs[*jira.NotFoundError](got.Refused[0]) {
		t.Errorf("refused = %+v, want board 17 with the site's refusal", got.Refused)
	}
	if len(got.Versions) != 1 {
		t.Errorf("the readable board's releases were lost: %+v", got.Versions)
	}
}

func TestReadReleases_NamesABoardThatAnsweredNoProjects(t *testing.T) {
	t.Parallel()

	got := readOf(t, &versionStub{}, boardSources("17"))
	if len(got.Refused) != 1 || got.Refused[0].Kind != RefusedBoard || !errors.Is(got.Refused[0].Err, ErrBoardEmpty) {
		t.Errorf("refused = %+v, want board 17 named as having no project behind it", got.Refused)
	}
}

func TestReadReleases_ARefusedBoardLicenceIsLeftOut(t *testing.T) {
	t.Parallel()

	stub := &versionStub{boardErr: map[int64]error{17: &jira.CapabilityError{Capability: jira.CapBoards, Reason: "Jira Software is not licensed on this site."}}}
	got := readOf(t, stub, boardSources("17"))
	if len(got.Refused) != 1 || !refusedAs[*jira.CapabilityError](got.Refused[0]) {
		t.Errorf("refused = %+v, want board 17 refused with the site's capability error", got.Refused)
	}
}

func TestReadReleases_AFailureOnABoardFailsTheRead(t *testing.T) {
	t.Parallel()

	for name, cause := range map[string]error{
		"a rate limit":      &jira.RateLimitError{},
		"a transport error": &jira.TransportError{Op: "GET /board/17/project"},
		"an auth failure":   &jira.AuthError{},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			stub := &versionStub{boardErr: map[int64]error{17: cause}}
			_, err := ReadReleases(context.Background(), stub, jira.Plan{ID: "42", Sources: boardSources("17")}, nil)
			if !errors.Is(err, cause) {
				t.Fatalf("%s on a board was taken as a refusal of it: %v", name, err)
			}
		})
	}
}

func TestReadReleases_ABoardValueThatIsNotANumberIsNamed(t *testing.T) {
	t.Parallel()

	stub := &versionStub{}
	got := readOf(t, stub, boardSources("backlog", "0"))

	if len(got.Refused) != 2 || got.Refused[0].Ref != "backlog" || !errors.Is(got.Refused[0].Err, ErrNotBoardID) ||
		!errors.Is(got.Refused[1].Err, ErrNotBoardID) {
		t.Errorf("refused = %+v, want both values named as not board ids", got.Refused)
	}
	if len(stub.boardCalls) != 0 {
		t.Errorf("BoardProjects was asked about %v", stub.boardCalls)
	}
}

func TestReadReleases_NamesSiteProjectSourcesByKey(t *testing.T) {
	t.Parallel()

	stub := &versionStub{keys: map[string]string{"10000": "EX", "10001": "OPS", "10002": "WEB"}}
	got := readPlanOf(t, stub, sitePlanOf(projectSources("10000", "10001", "10002")))

	for ref, want := range stub.keys {
		if name := got.Names[ref]; name != want {
			t.Errorf("Names[%s] = %q, want %q", ref, name, want)
		}
	}
	if got.NameErr != nil {
		t.Errorf("NameErr = %v, want none", got.NameErr)
	}
	slices.Sort(stub.projectCalls)
	if want := []string{"10000", "10001", "10002"}; !slices.Equal(stub.projectCalls, want) {
		t.Errorf("Project was asked about %v, want each source once", stub.projectCalls)
	}
}

func TestReadReleases_ALocalPlanAsksNoProject(t *testing.T) {
	t.Parallel()

	stub := &versionStub{keys: map[string]string{"PROJ": "PROJ"}}
	got := readPlanOf(t, stub, jira.Plan{ID: "local", Local: true, Sources: projectSources("PROJ")})

	if len(stub.projectCalls) != 0 {
		t.Errorf("Project was asked about %v for a plan the profile defines", stub.projectCalls)
	}
	if len(got.Names) != 0 {
		t.Errorf("Names = %v, want none", got.Names)
	}
}

func TestReadReleases_ANameThatCannotBeReadKeepsTheID(t *testing.T) {
	t.Parallel()

	for name, cause := range map[string]error{
		"not found":    &jira.NotFoundError{Kind: "project", ID: "10001"},
		"rate limited": &jira.RateLimitError{Endpoint: "project", RetryAfter: time.Second},
		"transport":    &jira.TransportError{Op: "GET project", Err: errors.New("connection reset")},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			stub := &versionStub{
				keys:       map[string]string{"10000": "EX"},
				projectErr: map[string]error{"10001": cause},
				versions:   map[string][]jira.Version{"10001": {{ID: "7", Name: "ops-1"}}},
			}
			got := readPlanOf(t, stub, sitePlanOf(projectSources("10000", "10001")))

			if got.Names["10000"] != "EX" {
				t.Errorf("Names = %v, want the readable project still named", got.Names)
			}
			if _, named := got.Names["10001"]; named {
				t.Errorf("Names = %v, want the unreadable project left as an id", got.Names)
			}
			if !errors.Is(got.NameErr, cause) {
				t.Errorf("NameErr = %v, want %v", got.NameErr, cause)
			}
			if len(got.Versions) != 1 || len(got.Read) != 2 {
				t.Errorf("read %d versions from %v, want the releases read regardless", len(got.Versions), got.Read)
			}
		})
	}
}

func TestReadReleases_BoardNamedProjectsAreNotAskedAgain(t *testing.T) {
	t.Parallel()

	stub := &versionStub{
		projects: map[int64][]jira.ProjectRef{17: {{ID: "10000", Key: "EX"}}},
		keys:     map[string]string{"10001": "OPS"},
	}
	sources := append(projectSources("10000", "10001"), boardSources("17")...)
	got := readPlanOf(t, stub, sitePlanOf(sources))

	if want := []string{"10001"}; !slices.Equal(stub.projectCalls, want) {
		t.Errorf("Project was asked about %v, want only the one no board named", stub.projectCalls)
	}
	if got.Names["10000"] != "EX" || got.Names["10001"] != "OPS" {
		t.Errorf("Names = %v", got.Names)
	}
}

func TestReadReleases_AKnownNameIsNotAskedAgain(t *testing.T) {
	t.Parallel()

	stub := &versionStub{keys: map[string]string{"10001": "OPS"}}
	got, err := ReadReleases(context.Background(), stub, sitePlanOf(projectSources("10000", "10001")), map[string]string{"10000": "EX"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"10001"}; !slices.Equal(stub.projectCalls, want) {
		t.Errorf("Project was asked about %v, want only the unknown one", stub.projectCalls)
	}
	if got.Names["10000"] != "EX" || got.Names["10001"] != "OPS" {
		t.Errorf("Names = %v", got.Names)
	}
}

func TestExcluded_IsNilWhereThePlanExcludesNothing(t *testing.T) {
	t.Parallel()

	if got := Excluded(nil); got != nil {
		t.Errorf("Excluded(nil) = %v", got)
	}
	if got := Excluded(&jira.PlanDetail{}); got != nil {
		t.Errorf("Excluded(empty) = %v", got)
	}
	if got := Excluded(&jira.PlanDetail{ExcludedVersionIDs: []string{"2"}}); !got["2"] || len(got) != 1 {
		t.Errorf("Excluded = %v, want version 2", got)
	}
}

package issue

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

var (
	testMe    = jira.User{AccountID: "acc-me", DisplayName: "Ada Lovelace", Active: true}
	testOther = jira.User{AccountID: "acc-other", DisplayName: "Grace Hopper", Active: true}
)

func collabFake() *jiratest.Fake {
	return jiratest.New(
		jiratest.WithProject("PROJ", jiratest.Scrum),
		jiratest.WithIssues(jiratest.Gen(4)),
		jiratest.WithMe(testMe),
		jiratest.WithPeople([]jira.User{testMe, testOther}),
	)
}

// failsWith runs call against a fake whose next request is refused, for each
// way a request fails, and wants the refusal back untouched.
func failsWith(t *testing.T, call func(*jiratest.Fake) error) {
	t.Helper()
	for name, fail := range failures() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := collabFake()
			f.FailNext(fail)
			if err := call(f); !errors.Is(err, fail) {
				t.Errorf("err = %v, want %v", err, fail)
			}
		})
	}
}

func TestParseSpent(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"1h 30m", 90 * time.Minute, true},
		{"90m", 90 * time.Minute, true},
		{"1.5h", 90 * time.Minute, true},
		{" 2H ", 2 * time.Hour, true},
		{"", 0, false},
		{"1d", 0, false},
		{"2w", 0, false},
		{"soon", 0, false},
		{"30s", 0, false},
		{"-1h", 0, false},
	} {
		got, err := ParseSpent(tc.in)
		if (err == nil) != tc.ok || got != tc.want {
			t.Errorf("ParseSpent(%q) = %v, %v", tc.in, got, err)
		}
	}
}

func TestParseStarted(t *testing.T) {
	t.Parallel()
	loc := time.FixedZone("account", 2*60*60)
	now := time.Date(2025, time.March, 5, 9, 15, 0, 0, time.UTC)
	for _, tc := range []struct {
		in   string
		want time.Time
		ok   bool
	}{
		{"", now.In(loc), true},
		{"2025-03-04", time.Date(2025, time.March, 4, 11, 15, 0, 0, loc), true},
		{"2025-03-04 08:30", time.Date(2025, time.March, 4, 8, 30, 0, 0, loc), true},
		{"2025-03-06", time.Time{}, false},
		{"yesterday", time.Time{}, false},
	} {
		got, err := ParseStarted(tc.in, now, loc)
		if (err == nil) != tc.ok || !got.Equal(tc.want) {
			t.Errorf("ParseStarted(%q) = %v, %v", tc.in, got, err)
		}
	}
}

func TestParse_RefusalsAreTheirOwnErrors(t *testing.T) {
	t.Parallel()
	loc := time.UTC
	now := time.Date(2025, time.March, 5, 9, 15, 0, 0, loc)
	for in, want := range map[string]error{"": ErrNoLength, "1d": ErrLengthInDays, "soon": ErrNotLength, "30s": ErrUnderMinute} {
		if _, err := ParseSpent(in); !errors.Is(err, want) {
			t.Errorf("ParseSpent(%q) = %v, want %v", in, err, want)
		}
	}
	for in, want := range map[string]error{"yesterday": ErrNotDate, "2025-03-06": ErrNotYet} {
		if _, err := ParseStarted(in, now, loc); !errors.Is(err, want) {
			t.Errorf("ParseStarted(%q) = %v, want %v", in, err, want)
		}
	}
}

func TestLogWork_ThenTimeLoggedReadsIt(t *testing.T) {
	t.Parallel()
	f := collabFake()
	at := time.Date(2025, time.March, 4, 9, 0, 0, 0, time.UTC)
	if err := LogWork(t.Context(), f, "PROJ-1", 90*time.Minute, at, "pairing"); err != nil {
		t.Fatal(err)
	}
	logs, _, err := TimeLogged(t.Context(), f, "PROJ-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || logs[0].Spent != 90*time.Minute || !logs[0].Started.Equal(at) {
		t.Fatalf("logged %+v", logs)
	}
	if got := adf.Markdown(logs[0].Comment); got == "" {
		t.Error("the note was not kept as the worklog's comment")
	}
}

func TestTimeLogged_Failures(t *testing.T) {
	t.Parallel()
	failsWith(t, func(f *jiratest.Fake) error {
		_, _, err := TimeLogged(t.Context(), f, "PROJ-1")
		return err
	})
}

func TestLogWork_Failures(t *testing.T) {
	t.Parallel()
	failsWith(t, func(f *jiratest.Fake) error {
		return LogWork(t.Context(), f, "PROJ-1", time.Hour, time.Now(), "")
	})
}

func TestWatching_ToggleAddAndRemove(t *testing.T) {
	t.Parallel()
	f := collabFake()
	ctx := t.Context()
	if err := Toggle(ctx, f, "PROJ-1", false); err != nil {
		t.Fatal(err)
	}
	if list, err := WatchersOf(ctx, f, "PROJ-1"); err != nil || !list.Watching {
		t.Fatalf("after watching: %+v, %v", list, err)
	}
	if err := AddWatcher(ctx, f, "PROJ-1", testOther.AccountID); err != nil {
		t.Fatal(err)
	}
	if err := Toggle(ctx, f, "PROJ-1", true); err != nil {
		t.Fatal(err)
	}
	list, err := WatchersOf(ctx, f, "PROJ-1")
	if err != nil || list.Watching || !slices.ContainsFunc(list.People, func(u jira.User) bool { return u.AccountID == testOther.AccountID }) {
		t.Fatalf("after stopping: %+v, %v", list, err)
	}
	if err := RemoveWatcher(ctx, f, "PROJ-1", testOther.AccountID); err != nil {
		t.Fatal(err)
	}
	if people, err := FindAccounts(ctx, f, "Grace", 8); err != nil || len(people) == 0 {
		t.Errorf("found %v, %v", people, err)
	}
}

func TestWatching_Failures(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	for name, call := range map[string]func(*jiratest.Fake) error{
		"watchers":  func(f *jiratest.Fake) error { _, err := WatchersOf(ctx, f, "PROJ-1"); return err },
		"watch":     func(f *jiratest.Fake) error { return Toggle(ctx, f, "PROJ-1", false) },
		"stop":      func(f *jiratest.Fake) error { return Toggle(ctx, f, "PROJ-1", true) },
		"add":       func(f *jiratest.Fake) error { return AddWatcher(ctx, f, "PROJ-1", testOther.AccountID) },
		"remove":    func(f *jiratest.Fake) error { return RemoveWatcher(ctx, f, "PROJ-1", testOther.AccountID) },
		"find":      func(f *jiratest.Fake) error { _, err := FindAccounts(ctx, f, "Grace", 8); return err },
		"assignees": func(f *jiratest.Fake) error { _, err := Assignable(ctx, f, "PROJ", "Grace", 8); return err },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			failsWith(t, call)
		})
	}
}

func TestLinks_MakeReadAndRemove(t *testing.T) {
	t.Parallel()
	f := collabFake()
	ctx := t.Context()
	types, err := LinkTypes(ctx, f)
	if err != nil || len(types) == 0 {
		t.Fatalf("link types %v, %v", types, err)
	}
	in := LinkBetween(types[0].ID, true, "PROJ-1", "PROJ-2")
	if in.From != "PROJ-2" || in.To != "PROJ-1" {
		t.Errorf("an inward link runs %s to %s", in.From, in.To)
	}
	if err := Link(ctx, f, in); err != nil {
		t.Fatal(err)
	}
	links, err := Links(ctx, f, "PROJ-1")
	if err != nil || len(links) != 1 || links[0].Other.Key != "PROJ-2" {
		t.Fatalf("links %+v, %v", links, err)
	}
	if err := Unlink(ctx, f, links[0].ID); err != nil {
		t.Fatal(err)
	}
	if links, _ := Links(ctx, f, "PROJ-1"); len(links) != 0 {
		t.Errorf("the link is still there: %+v", links)
	}
}

func TestLinks_Failures(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	for name, call := range map[string]func(*jiratest.Fake) error{
		"links": func(f *jiratest.Fake) error { _, err := Links(ctx, f, "PROJ-1"); return err },
		"types": func(f *jiratest.Fake) error { _, err := LinkTypes(ctx, f); return err },
		"link": func(f *jiratest.Fake) error {
			return Link(ctx, f, jira.LinkInput{TypeID: "1", From: "PROJ-1", To: "PROJ-2"})
		},
		"unlink": func(f *jiratest.Fake) error { return Unlink(ctx, f, "1") },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			failsWith(t, call)
		})
	}
}

func TestCloneInput_SkipsFieldsTheScreenDoesNotTake(t *testing.T) {
	t.Parallel()
	src := jira.Issue{
		Project: jira.ProjectRef{Key: "PROJ"}, Type: jira.IssueType{ID: "1"},
		Labels: []string{"a"}, Priority: &jira.Priority{ID: "3"},
		Fields: jira.NewFieldSet(map[string]jira.FieldValue{
			"f1": {Kind: jira.KindNumber, Number: 5},
			"f2": {Kind: jira.KindUnknown, Text: `{"id":1}`},
			"f3": {Kind: jira.KindText, Text: "off screen"},
		}),
	}
	schema := jira.Schema{Fields: []jira.FieldMeta{
		{Field: jira.FieldRef{ID: "labels"}, Name: "Labels"},
		{Field: jira.FieldRef{ID: "f1"}, Name: "Points"},
		{Field: jira.FieldRef{ID: "f2"}, Name: "Sprint"},
	}}
	in, names := CloneInput(src, schema)
	if !slices.Equal(names, []string{"Labels", "Points"}) {
		t.Errorf("carried %v", names)
	}
	if _, ok := in.Fields.ByID("priority"); ok {
		t.Error("the priority went although the screen does not take it")
	}
	if _, ok := in.Fields.ByID("f3"); ok {
		t.Error("a field off the screen went")
	}
	if v, ok := in.Fields.ByID("f1"); !ok || v.Number != 5 {
		t.Error("the number was not carried")
	}
}

func TestClone_CopiesTheIssueAndItsLinks(t *testing.T) {
	t.Parallel()
	f := collabFake()
	ctx := t.Context()
	types, _ := LinkTypes(ctx, f)
	if err := Link(ctx, f, jira.LinkInput{TypeID: types[0].ID, From: "PROJ-1", To: "PROJ-2"}); err != nil {
		t.Fatal(err)
	}
	src, err := ReadClone(ctx, f, "PROJ-1")
	if err != nil {
		t.Fatal(err)
	}
	if src.Src.Key != "PROJ-1" || len(src.Types) == 0 || len(src.Carried) == 0 {
		t.Fatalf("clone source %+v", src)
	}
	in := src.Input
	in.Summary = "CLONE - " + src.Src.Summary
	made, failed, err := Clone(ctx, f, in, src.Src, src.Types, true)
	if err != nil || failed != 0 || made.Key == "" {
		t.Fatalf("made %s with %d links failed: %v", made.Key, failed, err)
	}
	if links, _ := Links(ctx, f, made.Key); len(links) != 1 {
		t.Errorf("the copy has %d links, want 1", len(links))
	}
}

func TestClone_Failures(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	for name, call := range map[string]func(*jiratest.Fake) error{
		"read": func(f *jiratest.Fake) error { _, err := ReadClone(ctx, f, "PROJ-1"); return err },
		"create": func(f *jiratest.Fake) error {
			_, _, err := Clone(ctx, f, jira.IssueInput{ProjectKey: "PROJ", IssueTypeID: "10001", Summary: "x"}, jira.Issue{}, nil, false)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			failsWith(t, call)
		})
	}
}

func TestCopyLinks_CountsWhatCouldNotBeMade(t *testing.T) {
	t.Parallel()
	f := collabFake()
	links := []jira.IssueLink{{Type: "no such kind", Other: jira.IssueRef{Key: "PROJ-2"}}}
	if failed := CopyLinks(t.Context(), f, links, nil, "PROJ-1"); failed != 1 {
		t.Errorf("failed = %d, want 1", failed)
	}
}

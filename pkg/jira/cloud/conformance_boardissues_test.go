package cloud

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"testing"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// One set of assertions, run against both adapters, for the two reads that
// answer what a board holds. Everything above the port is tested against the
// fake, so a rule only the cloud adapter holds is a rule no test meets — and
// these two reads exist precisely because a board's contents cannot be worked
// out anywhere above the port.
//
// The cases are properties, not answers. The two adapters describe different
// sites and cannot agree on which issues are on a board; what they must agree on
// is that a read naming no field is refused before anything is sent, that a
// board id naming no board is refused, that every issue comes back carrying the
// narrow list it was read with, that the envelope's total agrees with what the
// walk gathers, that the sub-query a board's own configuration reports is
// accepted, and that a refusal names CapBoards.

// cardMask is the narrow field list every case below reads with.
var cardMask = []string{"summary", "status"}

func boardIssuesFromSite(t *testing.T, opts ...jiratest.ServerOption) jira.BoardReader {
	t.Helper()
	return boardsFromSite(t, opts...)
}

// conformBoardFake is the fake holding a board with issues on it, which is what
// makes a page to assert about.
func conformBoardFake(t *testing.T, kind jiratest.BoardKind, opts ...jiratest.Option) jira.BoardReader {
	t.Helper()
	return jiratest.New(append([]jiratest.Option{
		jiratest.WithProject(conformProject, kind),
		jiratest.WithIssues(jiratest.GenFor(conformProject, 9)),
	}, opts...)...)
}

// boardRead is one of the two reads. Every case runs against both, so that
// neither of them can hold a rule the other drops.
type boardRead func(ctx context.Context, r jira.BoardReader, boardID int64, q jira.BoardQuery) (jira.Page[jira.Issue], error)

func boardReads() map[string]boardRead {
	return map[string]boardRead{
		"the board": func(ctx context.Context, r jira.BoardReader, id int64, q jira.BoardQuery) (jira.Page[jira.Issue], error) {
			return r.BoardIssues(ctx, id, q)
		},
		"its backlog": func(ctx context.Context, r jira.BoardReader, id int64, q jira.BoardQuery) (jira.Page[jira.Issue], error) {
			return r.BoardBacklog(ctx, id, q)
		},
	}
}

func TestBoardIssues_BothAdaptersAnswerTheSameWay(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		cloud boardBuilder
		fake  boardBuilder
		run   func(*testing.T, jira.BoardReader, string, boardRead)
	}{
		{
			name:  "a read that names no field is refused before the site is asked",
			cloud: func(t *testing.T) jira.BoardReader { return boardIssuesFromSite(t) },
			fake:  func(t *testing.T) jira.BoardReader { return conformBoardFake(t, jiratest.Scrum) },
			run: func(t *testing.T, r jira.BoardReader, name string, read boardRead) {
				t.Helper()
				board := firstBoard(t, r)
				for _, fields := range [][]string{nil, {"  ", ""}} {
					page, err := read(t.Context(), r, board.ID, jira.BoardQuery{Fields: fields})
					if len(page.Items) != 0 {
						t.Errorf("%s answered %d issues for a read naming no field", name, len(page.Items))
					}
					var invalid *jira.ValidationError
					if !errors.As(err, &invalid) {
						t.Fatalf("%s with fields %v: got %T (%v), want a *jira.ValidationError", name, fields, err, err)
					}
					if _, named := invalid.For("fields"); !named {
						t.Errorf("the refusal says %v and does not name fields, which is what the caller has to change",
							invalid.Fields)
					}
				}
			},
		},
		{
			name:  "a board id that names no board is refused rather than sent",
			cloud: func(t *testing.T) jira.BoardReader { return boardIssuesFromSite(t) },
			fake:  func(t *testing.T) jira.BoardReader { return conformBoardFake(t, jiratest.Scrum) },
			run: func(t *testing.T, r jira.BoardReader, name string, read boardRead) {
				t.Helper()
				for _, id := range []int64{0, -1} {
					_, err := read(t.Context(), r, id, jira.BoardQuery{Fields: cardMask})
					var invalid *jira.ValidationError
					if !errors.As(err, &invalid) {
						t.Fatalf("%s with board %d: got %T (%v), want a *jira.ValidationError", name, id, err, err)
					}
					if _, named := invalid.For("boardId"); !named {
						t.Errorf("the refusal says %v and does not name boardId", invalid.Fields)
					}
				}
			},
		},
		{
			name:  "every issue carries the narrow list it was read with",
			cloud: func(t *testing.T) jira.BoardReader { return boardIssuesFromSite(t) },
			fake:  func(t *testing.T) jira.BoardReader { return conformBoardFake(t, jiratest.Scrum) },
			run: func(t *testing.T, r jira.BoardReader, name string, read boardRead) {
				t.Helper()
				board := firstBoard(t, r)
				page, err := read(t.Context(), r, board.ID, jira.BoardQuery{Fields: cardMask})
				if err != nil {
					t.Fatalf("reading %s: %v", name, err)
				}
				if len(page.Items) == 0 {
					t.Fatalf("%s answered no issue at all, and a decoder reading the wrong array key answers exactly that", name)
				}
				want := slices.Sorted(slices.Values(cardMask))
				for _, iss := range page.Items {
					if iss.Key == "" || iss.ID == "" {
						t.Errorf("an issue came back as %+v, with nothing to identify it by", iss)
					}
					if iss.Summary == "" || iss.Status.ID == "" {
						t.Errorf("%s came back without the fields the read asked for: %+v", iss.Key, iss)
					}
					if iss.Requested.Wide() {
						t.Errorf("%s reports a wide mask; the read named %v", iss.Key, cardMask)
					}
					if got := iss.Requested.IDs(); !slices.Equal(got, want) {
						t.Errorf("%s reports the mask %v, want %v: a field nobody asked about must not read as one the site had nothing for",
							iss.Key, got, want)
					}
				}
			},
		},
		{
			name:  "the total the envelope carries agrees with what the walk gathers",
			cloud: func(t *testing.T) jira.BoardReader { return boardIssuesFromSite(t) },
			fake:  func(t *testing.T) jira.BoardReader { return conformBoardFake(t, jiratest.Scrum) },
			run: func(t *testing.T, r jira.BoardReader, name string, read boardRead) {
				t.Helper()
				board := firstBoard(t, r)
				page, err := read(t.Context(), r, board.ID, jira.BoardQuery{Fields: cardMask})
				if err != nil {
					t.Fatalf("reading %s: %v", name, err)
				}
				total, counted := page.Count()
				if !counted {
					t.Fatalf("%s reports no total, and this envelope carries one", name)
				}
				all, err := jira.Collect(t.Context(), page, 0)
				if err != nil {
					t.Fatalf("walking %s: %v", name, err)
				}
				if len(all) != total {
					t.Errorf("%s reports %d issues and the walk gathered %d", name, total, len(all))
				}
				keys := make([]string, 0, len(all))
				for i := range all {
					keys = append(keys, all[i].Key)
				}
				if distinct := len(slices.Compact(slices.Sorted(slices.Values(keys)))); distinct != len(keys) {
					t.Errorf("the walk handed back %d issues and %d distinct keys, so a page repeated", len(keys), distinct)
				}
			},
		},
		{
			name: "the sub-query a board's own configuration reports is one the read accepts",
			cloud: func(t *testing.T) jira.BoardReader {
				return boardIssuesFromSite(t, jiratest.WithFixture(http.MethodGet, boardConfigRoute, "board_config_no_estimation.json"))
			},
			fake: func(t *testing.T) jira.BoardReader { return conformBoardFake(t, jiratest.Kanban) },
			run: func(t *testing.T, r jira.BoardReader, name string, read boardRead) {
				t.Helper()
				board := firstBoard(t, r)
				cfg, err := r.BoardConfig(t.Context(), board.ID)
				if err != nil {
					t.Fatalf("reading the configuration of board %d: %v", board.ID, err)
				}
				if cfg.SubQuery == "" {
					t.Fatal("this board reports no sub-query, and the case is about the board that does")
				}
				page, err := read(t.Context(), r, board.ID, jira.BoardQuery{Fields: cardMask, SubQuery: cfg.SubQuery})
				if err != nil {
					t.Fatalf("reading %s with the board's own sub-query %q: %v", name, cfg.SubQuery, err)
				}
				if len(page.Items) == 0 {
					t.Errorf("%s answered nothing once the board's own sub-query was applied", name)
				}
			},
		},
		{
			name: "a refusal names CapBoards rather than reading as a fault",
			cloud: func(t *testing.T) jira.BoardReader {
				return boardIssuesFromSite(t,
					jiratest.WithStatus(http.MethodGet, boardIssuesRoute, http.StatusForbidden, "plans_403.json"),
					jiratest.WithStatus(http.MethodGet, boardBacklogRoute, http.StatusForbidden, "plans_403.json"))
			},
			fake: func(t *testing.T) jira.BoardReader {
				return conformBoardFake(t, jiratest.Scrum, jiratest.WithCapabilities(jiratest.NoBoards))
			},
			run: func(t *testing.T, r jira.BoardReader, name string, read boardRead) {
				t.Helper()
				// The board id cannot come from a listing the same refusal
				// covers, so it is the one the fixtures describe.
				_, err := read(t.Context(), r, boardTestID, jira.BoardQuery{Fields: cardMask})
				var refused *jira.CapabilityError
				if !errors.As(err, &refused) {
					t.Fatalf("%s: got %T (%v), want a *jira.CapabilityError", name, err, err)
				}
				if refused.Capability != jira.CapBoards {
					t.Errorf("the refusal names %q, want %q", refused.Capability, jira.CapBoards)
				}
				if refused.Reason == "" {
					t.Error("the refusal carries no reason, and the reason is what the user is shown instead of the board")
				}
			},
		},
	}

	for _, tt := range cases {
		for _, adapter := range []struct {
			name string
			open boardBuilder
		}{
			{name: "cloud", open: tt.cloud},
			{name: "fake", open: tt.fake},
		} {
			for read, run := range boardReads() {
				t.Run(tt.name+"/"+read+"/"+adapter.name, func(t *testing.T) {
					t.Parallel()

					tt.run(t, adapter.open(t), read, run)
				})
			}
		}
	}
}

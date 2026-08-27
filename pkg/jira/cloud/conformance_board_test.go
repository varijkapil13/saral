package cloud

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// One set of assertions, run against both adapters, for the two reads a board
// view opens with. Everything above the port is tested against the fake, so a
// rule only the cloud adapter holds is a rule no test meets.
//
// The cases are properties, not answers. The two adapters describe different
// sites on purpose and cannot agree on which boards are in them or what their
// columns are called; what they must agree on is that an absent estimation is
// not an estimation of none, that a board without a rank field is ordered by its
// filter, that a column is identified by the status ids it maps, that a rank
// field is named the way the rest of Jira names a field, that a project key is
// trimmed and a blank one refused, and that a refusal names CapBoards.

type boardBuilder func(*testing.T) jira.BoardReader

func boardsFromSite(t *testing.T, opts ...jiratest.ServerOption) jira.BoardReader {
	t.Helper()

	s := jiratest.NewServer(opts...)
	t.Cleanup(s.Close)
	c, _ := testClient(t, s.URL())
	return c
}

// firstBoard is the flow a board view actually takes: list the project's boards,
// then read the configuration of one by the id the list handed out. Neither
// adapter's board ids may be written down in a test.
func firstBoard(t *testing.T, r jira.BoardReader) jira.Board {
	t.Helper()

	boards, err := r.Boards(t.Context(), conformProject)
	if err != nil {
		t.Fatalf("listing the boards on %s: %v", conformProject, err)
	}
	if len(boards) == 0 {
		t.Fatalf("%s has no board, so there is no configuration to read", conformProject)
	}
	if boards[0].ID == 0 {
		t.Fatalf("the first board has no id: %+v", boards[0])
	}
	return boards[0]
}

func TestBoards_BothAdaptersAnswerTheSameWay(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		cloud boardBuilder
		fake  boardBuilder
		run   func(*testing.T, jira.BoardReader)
	}{
		{
			name:  "a board is reached by the numeric id its own listing handed out",
			cloud: func(t *testing.T) jira.BoardReader { return boardsFromSite(t) },
			fake:  func(t *testing.T) jira.BoardReader { return conformFake(t) },
			run: func(t *testing.T, r jira.BoardReader) {
				t.Helper()
				board := firstBoard(t, r)
				cfg, err := r.BoardConfig(t.Context(), board.ID)
				if err != nil {
					t.Fatalf("reading the configuration of board %d: %v", board.ID, err)
				}
				if cfg.BoardID != board.ID {
					t.Errorf("the configuration is for board %d, want the one that was asked about, %d", cfg.BoardID, board.ID)
				}
				if cfg.Type != board.Type {
					t.Errorf("the listing calls the board %q and its configuration calls it %q", board.Type, cfg.Type)
				}
			},
		},
		{
			// The 404 is served by this test rather than obtained: the Agile
			// listing documents projectKeyOrId as a filter and no 404 with it.
			name: "a 404 on the listing path names the project it was asked about",
			cloud: func(t *testing.T) jira.BoardReader {
				return boardsFromSite(t, jiratest.WithStatus(http.MethodGet, boardPath, http.StatusNotFound, ""))
			},
			fake: func(t *testing.T) jira.BoardReader { return conformFake(t) },
			run: func(t *testing.T, r jira.BoardReader) {
				t.Helper()
				got, err := r.Boards(t.Context(), "NOPE")
				if len(got) != 0 {
					t.Errorf("a refused listing came back with %+v", got)
				}
				var missing *jira.NotFoundError
				if !errors.As(err, &missing) {
					t.Fatalf("got %T (%v), want a *jira.NotFoundError", err, err)
				}
				if missing.Kind != "project" || missing.ID != "NOPE" {
					t.Errorf("the 404 names %s %s, want project NOPE", missing.Kind, missing.ID)
				}
			},
		},
		{
			// Fails on the fake on purpose: Fake.Boards neither trims a project
			// key nor refuses a blank one, so this rule is one nothing above the
			// port ever meets.
			name:  "a project key is trimmed, and a blank one is refused before the site is asked",
			cloud: func(t *testing.T) jira.BoardReader { return boardsFromSite(t) },
			fake:  func(t *testing.T) jira.BoardReader { return conformFake(t) },
			run: func(t *testing.T, r jira.BoardReader) {
				t.Helper()
				got, err := r.Boards(t.Context(), "   ")
				if len(got) != 0 {
					t.Errorf("a blank project key came back with %+v", got)
				}
				var invalid *jira.ValidationError
				if !errors.As(err, &invalid) {
					t.Fatalf("got %T (%v) for a blank project key, want a *jira.ValidationError", err, err)
				}
				if _, named := invalid.For("projectKey"); !named {
					t.Errorf("the refusal says %v and does not name projectKey, which is the field to focus", invalid.Fields)
				}
				padded, err := r.Boards(t.Context(), " "+conformProject+" ")
				if err != nil {
					t.Fatalf("listing the boards on %q: %v", " "+conformProject+" ", err)
				}
				if len(padded) == 0 {
					t.Errorf("%q listed no board while %q lists one, so the same key answers two ways",
						" "+conformProject+" ", conformProject)
				}
			},
		},
		{
			name: "a board nobody has is a 404 naming the board",
			cloud: func(t *testing.T) jira.BoardReader {
				return boardsFromSite(t, jiratest.WithStatus(http.MethodGet, boardConfigRoute, http.StatusNotFound, "not_found_board.json"))
			},
			fake: func(t *testing.T) jira.BoardReader { return conformFake(t) },
			run: func(t *testing.T, r jira.BoardReader) {
				t.Helper()
				_, err := r.BoardConfig(t.Context(), 987654)
				var missing *jira.NotFoundError
				if !errors.As(err, &missing) {
					t.Fatalf("got %T (%v), want a *jira.NotFoundError", err, err)
				}
				if missing.Kind != "board" || missing.ID != "987654" {
					t.Errorf("the 404 names %s %s, want board 987654", missing.Kind, missing.ID)
				}
			},
		},
		{
			name: "a board that does not estimate sends no estimation at all",
			cloud: func(t *testing.T) jira.BoardReader {
				return boardsFromSite(t, jiratest.WithFixture(http.MethodGet, boardConfigRoute, "board_config_no_estimation.json"))
			},
			fake: func(t *testing.T) jira.BoardReader {
				return jiratest.New(jiratest.WithProject(conformProject, jiratest.Kanban))
			},
			run: func(t *testing.T, r jira.BoardReader) {
				t.Helper()
				cfg := configOfFirstBoard(t, r)
				if cfg.Estimation != nil {
					t.Errorf("Estimation = %+v, want nil: the board sent no estimation object", *cfg.Estimation)
				}
				if cfg.Estimates() {
					t.Error("Estimates() is true on a board that does not estimate")
				}
			},
		},
		{
			name: "a board that turned estimation off is not the same answer as one that never had it",
			cloud: func(t *testing.T) jira.BoardReader {
				return boardsFromSite(t, boardConfigAnswering(boardConfigWith(`"estimation":{"type":"none"},"ranking":{}`)))
			},
			fake: func(t *testing.T) jira.BoardReader {
				return jiratest.New(
					jiratest.WithProject(conformProject, jiratest.Scrum),
					jiratest.WithFields([]jira.Field{{ID: "summary", Key: "summary", Name: "Summary"}}),
				)
			},
			run: func(t *testing.T, r jira.BoardReader) {
				t.Helper()
				cfg := configOfFirstBoard(t, r)
				if cfg.Estimation == nil {
					t.Fatal("Estimation is nil, and a board saying none is a board that answered")
				}
				if cfg.Estimation.Type != jira.EstimationNone {
					t.Errorf("Estimation.Type = %q, want %q", cfg.Estimation.Type, jira.EstimationNone)
				}
				if cfg.Estimates() {
					t.Error("Estimates() is true on a board that turned estimation off")
				}
			},
		},
		{
			name:  "a rank field is named the way the rest of Jira names a field",
			cloud: func(t *testing.T) jira.BoardReader { return boardsFromSite(t) },
			fake:  func(t *testing.T) jira.BoardReader { return conformFake(t) },
			run: func(t *testing.T, r jira.BoardReader) {
				t.Helper()
				cfg := configOfFirstBoard(t, r)
				if cfg.RankFieldID == "" {
					t.Fatal("the board reports no rank field, and this case is about the one that does")
				}
				if cfg.Ordering() != jira.OrderRank {
					t.Errorf("Ordering() = %v with a rank field of %q, want OrderRank", cfg.Ordering(), cfg.RankFieldID)
				}
				if numeric(cfg.RankFieldID) {
					t.Errorf("RankFieldID = %q, which is a bare number: a field is asked for by its id, and nothing resolves this one",
						cfg.RankFieldID)
				}
			},
		},
		{
			name: "a board with no rank field is ordered by its filter and cannot be reordered",
			cloud: func(t *testing.T) jira.BoardReader {
				return boardsFromSite(t, jiratest.WithFixture(http.MethodGet, boardConfigRoute, "board_config_no_estimation.json"))
			},
			fake: func(t *testing.T) jira.BoardReader {
				return jiratest.New(jiratest.WithProject(conformProject, jiratest.Kanban))
			},
			run: func(t *testing.T, r jira.BoardReader) {
				t.Helper()
				cfg := configOfFirstBoard(t, r)
				if cfg.RankFieldID != "" {
					t.Errorf("RankFieldID = %q on a board that does not rank", cfg.RankFieldID)
				}
				if cfg.Ordering() != jira.OrderFilter {
					t.Errorf("Ordering() = %v, want OrderFilter, which is what disables the drag", cfg.Ordering())
				}
			},
		},
		{
			name:  "a column is identified by the status ids it maps, never by its name",
			cloud: func(t *testing.T) jira.BoardReader { return boardsFromSite(t) },
			fake:  func(t *testing.T) jira.BoardReader { return conformFake(t) },
			run: func(t *testing.T, r jira.BoardReader) {
				t.Helper()
				cfg := configOfFirstBoard(t, r)
				if len(cfg.Columns) == 0 {
					t.Fatal("the board has no columns, so there is nothing to group by")
				}
				seen := make(map[string]int, len(cfg.Columns)*2)
				for i, column := range cfg.Columns {
					for _, id := range column.StatusIDs {
						if strings.TrimSpace(id) == "" {
							t.Errorf("column %d maps a blank status id, which matches every unread status", i)
						}
						if first, dup := seen[id]; dup {
							t.Errorf("status %s is mapped into both column %d and column %d", id, first, i)
						}
						seen[id] = i
					}
				}
				if len(seen) == 0 {
					t.Error("no column maps a status id, so every issue on the board falls outside every column")
				}
			},
		},
		{
			name: "a refusal names CapBoards rather than reading as a fault",
			cloud: func(t *testing.T) jira.BoardReader {
				return boardsFromSite(t, jiratest.WithStatus(http.MethodGet, boardPath, http.StatusForbidden, "plans_403.json"))
			},
			fake: func(t *testing.T) jira.BoardReader {
				return jiratest.New(
					jiratest.WithProject(conformProject, jiratest.Scrum),
					jiratest.WithCapabilities(jiratest.NoBoards),
				)
			},
			run: func(t *testing.T, r jira.BoardReader) {
				t.Helper()
				_, err := r.Boards(t.Context(), conformProject)
				var refused *jira.CapabilityError
				if !errors.As(err, &refused) {
					t.Fatalf("got %T (%v), want a *jira.CapabilityError", err, err)
				}
				if refused.Capability != jira.CapBoards {
					t.Errorf("the refusal names %q, want %q", refused.Capability, jira.CapBoards)
				}
				if refused.Reason == "" {
					t.Error("the refusal carries no reason, and the reason is what the user is shown instead of the view")
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
			t.Run(tt.name+"/"+adapter.name, func(t *testing.T) {
				t.Parallel()

				tt.run(t, adapter.open(t))
			})
		}
	}
}

func configOfFirstBoard(t *testing.T, r jira.BoardReader) jira.BoardConfig {
	t.Helper()

	board := firstBoard(t, r)
	cfg, err := r.BoardConfig(t.Context(), board.ID)
	if err != nil {
		t.Fatalf("reading the configuration of board %d: %v", board.ID, err)
	}
	return cfg
}

func numeric(s string) bool {
	if s == "" {
		return false
	}
	return !slices.ContainsFunc([]byte(s), func(b byte) bool { return b < '0' || b > '9' })
}

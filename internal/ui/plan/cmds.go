package plan

import (
	"context"
	"errors"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/pkg/jira"
)

// plansMsg carries what the site answered with.
type plansMsg struct {
	gen   int
	plans []jira.Plan
}

// releasesMsg carries the versions of one plan's projects. The plan travels with
// them because the answer is filed under the plan and not under the cursor,
// which has usually moved by the time a read lands.
type releasesMsg struct {
	gen      int
	plan     string
	versions []jira.Version
	owners   []string
	refused  []refusal
	names    map[string]string
	boards   map[string][]string
	read     []string
	detail   *jira.PlanDetail
	// detailErr is why the cross-space releases were not read; the versions still arrived.
	detailErr error
}

type refusal struct {
	kind   string
	ref    string
	reason string
}

type releaseReader interface {
	jira.VersionReader
	jira.BoardProjectReader
	jira.PlanDetailReader
}

// failedMsg is a read that brought nothing back. The error travels whole so
// that a refusal reaches the user in the words the site used, and the plan says
// which read failed: an empty one is the plans themselves.
type failedMsg struct {
	gen  int
	plan string
	err  error
}

func readPlans(ctx context.Context, reader jira.PlanReader, gen int) tea.Cmd {
	return func() tea.Msg {
		plans, err := reader.Plans(ctx)
		if err != nil {
			return failedMsg{gen: gen, err: err}
		}
		return plansMsg{gen: gen, plans: plans}
	}
}

func readReleases(ctx context.Context, reader releaseReader, plan jira.Plan, gen int) tea.Cmd {
	return func() tea.Msg {
		msg, err := collectReleases(ctx, reader, plan)
		if err != nil {
			return failedMsg{gen: gen, plan: plan.ID, err: err}
		}
		msg.gen = gen
		return msg
	}
}

// collectReleases reads the versions of every project a plan draws from, directly
// or through a board, and for a site plan its cross-space releases beside them.
//
// A detail the site refuses is carried as detailErr and not returned.
//
// A project or board the token may not browse is left out and named, which is
// what the web UI does with a plan spanning projects the reader cannot see. A
// failure that says nothing about it — a rate limit, a dead host, a lapsed
// token — fails the whole read, since every other project would hit it too.
func collectReleases(ctx context.Context, reader releaseReader, plan jira.Plan) (releasesMsg, error) {
	sources := plan.Sources
	var detail *jira.PlanDetail
	var detailErr error
	if !plan.Local {
		d, err := reader.PlanDetail(ctx, plan.ID)
		if err != nil {
			detailErr = err
		} else {
			detail = &d
		}
	}
	var (
		refs    []string
		seen    = map[string]bool{}
		names   = map[string]string{}
		boards  = map[string][]string{}
		refused []refusal
	)
	add := func(ref string) {
		if !seen[ref] {
			seen[ref] = true
			refs = append(refs, ref)
		}
	}
	for _, s := range sources {
		if s.Type == jira.PlanSourceProject && strings.TrimSpace(s.Value) != "" {
			add(s.Value)
		}
	}
	seenBoard := map[string]bool{}
	for _, s := range sources {
		value := strings.TrimSpace(s.Value)
		if s.Type != jira.PlanSourceBoard || value == "" || seenBoard[value] {
			continue
		}
		seenBoard[value] = true
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id <= 0 {
			refused = append(refused, refusal{kind: "board", ref: value, reason: "the site did not name it by a board id"})
			continue
		}
		projects, err := reader.BoardProjects(ctx, id)
		if err != nil {
			if !projectRefused(err) {
				return releasesMsg{}, err
			}
			refused = append(refused, refusal{kind: "board", ref: value, reason: refusedReason(err)})
			continue
		}
		if len(projects) == 0 {
			refused = append(refused, refusal{kind: "board", ref: value,
				reason: "the site named no project behind it, which is also what it answers for a board you cannot view"})
			continue
		}
		for _, p := range projects {
			ref := p.ID
			if ref == "" {
				ref = p.Key
			}
			add(ref)
			label := ref
			if p.Key != "" {
				names[ref] = p.Key
				label = p.Key
			}
			boards[value] = append(boards[value], label)
		}
	}
	out := make([]jira.Version, 0, len(refs)*4)
	var owners, read []string
	for _, ref := range refs {
		versions, err := reader.Versions(ctx, ref)
		if err != nil {
			if !projectRefused(err) {
				return releasesMsg{}, err
			}
			refused = append(refused, refusal{kind: "project", ref: ref, reason: refusedReason(err)})
			continue
		}
		read = append(read, ref)
		for range versions {
			owners = append(owners, ref)
		}
		out = append(out, versions...)
	}
	return releasesMsg{plan: plan.ID, versions: out, owners: owners, refused: refused,
		names: names, boards: boards, read: read, detail: detail, detailErr: detailErr}, nil
}

// refusedReason is the site's words without the project, which the row names.
func refusedReason(err error) string {
	var nf *jira.NotFoundError
	if errors.As(err, &nf) {
		if nf.Detail != "" {
			return nf.Detail
		}
		return "it does not exist, or you cannot see it"
	}
	reason, _ := jira.Reason(err)
	return reason
}

// projectRefused is an answer about one project. Jira refuses a project the
// token cannot browse with a 400 rather than a 403 on the versions endpoint.
func projectRefused(err error) bool {
	var (
		capErr *jira.CapabilityError
		nf     *jira.NotFoundError
		ve     *jira.ValidationError
	)
	return errors.As(err, &capErr) || errors.As(err, &nf) || errors.As(err, &ve)
}

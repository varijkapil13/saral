package plan

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/varijkapil13/saral/pkg/jira"
)

const nameReads = 4

// Reader is what reading a plan's releases asks of the site.
type Reader interface {
	jira.VersionReader
	jira.BoardProjectReader
	jira.ProjectReader
	jira.PlanDetailReader
}

// RefusalKind is what a refusal is about.
type RefusalKind string

// The sources a release read can leave out.
const (
	RefusedBoard   RefusalKind = "board"
	RefusedProject RefusalKind = "project"
)

var (
	// ErrNotBoardID is a board source the site did not name by a board id.
	ErrNotBoardID = errors.New("board source is not a board id")
	// ErrBoardEmpty is a board the site named no project behind, which is also
	// what it answers for a board the token cannot view.
	ErrBoardEmpty = errors.New("board names no project")
)

// Refusal is one board or project a read left out, and why.
type Refusal struct {
	Kind RefusalKind
	Ref  string
	Err  error
}

// Releases are the versions of one plan's projects. Owners holds, for each
// version, the project ref it was read from.
type Releases struct {
	Versions []jira.Version
	Owners   []string
	Refused  []Refusal
	Names    map[string]string
	Boards   map[string][]string
	Read     []string
	Detail   *jira.PlanDetail
	// DetailErr is why the cross-space releases were not read; the versions still arrived.
	DetailErr error
	NameErr   error
}

// ReadReleases reads the versions of every project a plan draws from, directly
// or through a board, and for a site plan its cross-space releases beside them.
// Known names projects already resolved, so they are not asked about again.
//
// A detail the site refuses is carried as DetailErr and not returned.
//
// A project or board the token may not browse is left out and named, which is
// what the web UI does with a plan spanning projects the reader cannot see. A
// failure that says nothing about it — a rate limit, a dead host, a lapsed
// token — fails the whole read, since every other project would hit it too.
func ReadReleases(ctx context.Context, reader Reader, plan jira.Plan, known map[string]string) (Releases, error) {
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
		refused []Refusal
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
			refused = append(refused, Refusal{Kind: RefusedBoard, Ref: value, Err: ErrNotBoardID})
			continue
		}
		projects, err := reader.BoardProjects(ctx, id)
		if err != nil {
			if !projectRefused(err) {
				return Releases{}, err
			}
			refused = append(refused, Refusal{Kind: RefusedBoard, Ref: value, Err: err})
			continue
		}
		if len(projects) == 0 {
			refused = append(refused, Refusal{Kind: RefusedBoard, Ref: value, Err: ErrBoardEmpty})
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
	nameErr := nameProjects(ctx, reader, plan.Local, refs, known, names)
	out := make([]jira.Version, 0, len(refs)*4)
	var owners, read []string
	for _, ref := range refs {
		versions, err := reader.Versions(ctx, ref)
		if err != nil {
			if !projectRefused(err) {
				return Releases{}, err
			}
			refused = append(refused, Refusal{Kind: RefusedProject, Ref: ref, Err: err})
			continue
		}
		read = append(read, ref)
		for range versions {
			owners = append(owners, ref)
		}
		out = append(out, versions...)
	}
	return Releases{Versions: out, Owners: owners, Refused: refused,
		Names: names, Boards: boards, Read: read, Detail: detail, DetailErr: detailErr, NameErr: nameErr}, nil
}

func nameProjects(ctx context.Context, reader jira.ProjectReader, local bool, refs []string, known, names map[string]string) error {
	if local {
		return nil
	}
	var todo []string
	for _, ref := range refs {
		switch {
		case names[ref] != "":
		case known[ref] != "":
			names[ref] = known[ref]
		default:
			todo = append(todo, ref)
		}
	}
	var (
		mu     sync.Mutex
		g      errgroup.Group
		failed = make([]error, len(todo))
	)
	g.SetLimit(nameReads)
	for i, ref := range todo {
		g.Go(func() error {
			p, err := reader.Project(ctx, ref)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err != nil:
				failed[i] = err
			case p.Key != "":
				names[ref] = p.Key
			}
			return nil
		})
	}
	_ = g.Wait()
	for _, err := range failed {
		if err != nil {
			return err
		}
	}
	return nil
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

// Excluded is the set of version ids the plan leaves out, nil where it leaves
// none out.
func Excluded(detail *jira.PlanDetail) map[string]bool {
	if detail == nil || len(detail.ExcludedVersionIDs) == 0 {
		return nil
	}
	ids := make(map[string]bool, len(detail.ExcludedVersionIDs))
	for _, id := range detail.ExcludedVersionIDs {
		ids[id] = true
	}
	return ids
}

// ExcludedCount is how many of the versions the plan leaves out.
func ExcludedCount(versions []jira.Version, detail *jira.PlanDetail) int {
	ids := Excluded(detail)
	if ids == nil {
		return 0
	}
	n := 0
	for i := range versions {
		if ids[versions[i].ID] {
			n++
		}
	}
	return n
}

package plan

import (
	"context"
	"errors"
	"maps"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	appplan "github.com/varijkapil13/saral/internal/app/plan"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/release"
	"github.com/varijkapil13/saral/pkg/jira"
)

// ReleasesMsg browses the releases of the plan under the cursor. It is a
// broadcast for the same reason SourcesMsg is.
type ReleasesMsg struct{}

const (
	crossSpaceWords = `cross-space release = one release across several projects ("spaces")`
	localWords      = "defined in this profile, so no cross-space releases"
)

// releasesOf files what a read brought back, with the two lines the plan says
// about it built once rather than on every reflow.
func releasesOf(local bool, got *appplan.Releases) releases {
	held := releases{
		read: true, versions: got.Versions, owners: got.Owners, refused: refusalsOf(got.Refused),
		names: got.Names, boards: got.Boards, refs: got.Read,
		detail: got.Detail, detailErr: got.DetailErr,
	}
	held.head = headWords(local, &held)
	held.cross, held.crossWarn = crossWords(local, &held)
	return held
}

func refusalsOf(in []appplan.Refusal) []refusal {
	if len(in) == 0 {
		return nil
	}
	out := make([]refusal, 0, len(in))
	for _, r := range in {
		out = append(out, refusal{kind: string(r.Kind), ref: r.Ref, reason: refusedReason(r.Err)})
	}
	return out
}

func ownerLabel(local bool, held *releases, ref string) string {
	switch key := held.names[ref]; {
	case key != "":
		return key
	case local:
		return ref
	default:
		return "id " + ref
	}
}

func ownerList(local bool, held *releases) string {
	words := make([]string, 0, len(held.refs))
	for _, ref := range held.refs {
		words = append(words, ownerLabel(local, held, ref))
	}
	return strings.Join(words, ", ")
}

func headWords(local bool, held *releases) string {
	n := strconv.Itoa(len(held.versions))
	if len(held.refs) == 1 {
		return n + " in " + ownerList(local, held)
	}
	return n + " across " + ownerList(local, held)
}

func crossWords(local bool, held *releases) (text string, warn bool) {
	switch {
	case local:
		return localWords, false
	case held.detailErr != nil:
		reason, _ := jira.Reason(held.detailErr)
		return "cross-space releases not read, so the browser arranges by project: " + reason, true
	}
	cross, excluded := 0, appplan.ExcludedCount(held.versions, held.detail)
	if held.detail != nil {
		cross = len(held.detail.CrossProjectReleases)
	}
	var parts []string
	switch cross {
	case 0:
		parts = append(parts, "no cross-space releases")
	case 1:
		parts = append(parts, "1 cross-space release")
	default:
		parts = append(parts, strconv.Itoa(cross)+" cross-space releases")
	}
	if excluded > 0 {
		parts = append(parts, strconv.Itoa(excluded)+" excluded by the plan")
	}
	return strings.Join(parts, ", "), false
}

// setOf is the release browser's input for one plan: every version with the
// project it belongs to, the cross-space releases that group them, and what the
// read left out.
func setOf(plan *jira.Plan, held *releases, reload func(context.Context) (release.Set, error)) release.Set {
	members := make([]release.Member, 0, len(held.versions))
	for i := range held.versions {
		var ref string
		if i < len(held.owners) {
			ref = held.owners[i]
		}
		members = append(members, release.Member{
			Version: held.versions[i],
			Project: release.Owner{Ref: ref, Label: ownerLabel(plan.Local, held, ref)},
		})
	}
	set := release.Set{
		Title:   plan.Name + ": " + strconv.Itoa(len(held.versions)) + " releases from " + ownerList(plan.Local, held),
		Members: members,
		Reload:  reload,
	}
	switch {
	case held.detailErr != nil:
		reason, _ := jira.Reason(held.detailErr)
		set.Title += ", by project because the cross-space releases were not read: " + reason
	case held.detail != nil:
		set.Explain = crossSpaceWords
		for _, c := range held.detail.CrossProjectReleases {
			set.Groups = append(set.Groups, release.Group{Name: c.Name, VersionIDs: c.VersionIDs})
		}
		set.Excluded = appplan.Excluded(held.detail)
	}
	row := planRow{plan: *plan}
	for i := range held.refused {
		set.Notes = append(set.Notes, refusedWords(&row, held, &held.refused[i]))
	}
	return set
}

func reloadOf(reader appplan.Reader, plan jira.Plan, known map[string]string) func(context.Context) (release.Set, error) {
	return func(ctx context.Context) (release.Set, error) {
		got, err := appplan.ReadReleases(ctx, reader, plan, known)
		if err != nil {
			return release.Set{}, err
		}
		fresh := releasesOf(plan.Local, &got)
		if len(fresh.versions) == 0 {
			return release.Set{}, errors.New("the site answered no releases for this plan")
		}
		return setOf(&plan, &fresh, reloadOf(reader, plan, maps.Clone(fresh.names))), nil
	}
}

// browse pushes the release browser over the plan, which is the only place its
// versions are sortable, filterable and grouped.
func (m *Model) browse(at int) tea.Cmd {
	if at < 0 || m.deps.Jira == nil {
		return nil
	}
	plan := m.plans[at].plan
	if !m.open[plan.ID] {
		return kernel.Status("open the plan first; its releases are read when it opens")
	}
	held, ok := m.rel[plan.ID]
	switch {
	case held.loading:
		return kernel.Status("the releases are still being read")
	case !ok || !held.read || held.err != nil || len(held.versions) == 0:
		return kernel.Status("this plan has no releases to browse")
	}
	reader := appplan.Reader(m.deps.Jira)
	reload := reloadOf(reader, plan, maps.Clone(held.names))
	set := setOf(&plan, &held, reload)
	return kernel.Push(release.SetViewID, "Releases in "+plan.Name, release.NewSet(m.deps, set))
}

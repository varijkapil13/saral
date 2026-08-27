package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/varijkapil13/saral/pkg/jira"
)

// What this file can be asked for.
var (
	_ jira.VersionReader = (*Client)(nil)
	_ jira.Releaser      = (*Client)(nil)
)

const (
	versionPath = "/rest/api/3/version"
	projectPath = "/rest/api/3/project"
)

const (
	versionPageSize = 50
	// versionBound is how many versions one list reads before it is refused
	// rather than cut short, since the port cannot say it was cut short.
	versionBound = 2000
	// versionSweepBound is how many open issues one release will rewrite.
	versionSweepBound = 1000
	// versionOrder is the project's own order; offsets over any other are not
	// stable between pages.
	versionOrder = "sequence"
)

// projectVersionsPath is the paginated collection under one project.
//
// The singular segment is deliberate: /project/{key}/version answers a paged
// envelope while /project/{key}/versions answers a bare array that cannot page,
// one letter apart and a different top-level JSON type.
func projectVersionsPath(projectKey string) string {
	return projectPath + "/" + url.PathEscape(projectKey) + "/version"
}

func versionIDPath(id string) string {
	return versionPath + "/" + url.PathEscape(id)
}

func unresolvedCountPath(id string) string {
	return versionIDPath(id) + "/unresolvedIssueCount"
}

// apiVersionWrite is the body a create, an update or a release sends. Every
// field is a pointer or raw JSON so a key is on the wire only when this call
// means to change it: a key set to null empties the field and an absent key
// leaves it alone.
//
// There is no overdue: the site omits it entirely on a released version, so a
// client that round-trips the key tells the site the opposite of what it read.
// projectId is refused on an update.
type apiVersionWrite struct {
	Name        string          `json:"name,omitempty"`
	ProjectID   *json.Number    `json:"projectId,omitempty"`
	Description *string         `json:"description,omitempty"`
	StartDate   json.RawMessage `json:"startDate,omitempty"`
	ReleaseDate json.RawMessage `json:"releaseDate,omitempty"`
	Archived    *bool           `json:"archived,omitempty"`
	Released    *bool           `json:"released,omitempty"`
}

// apiFixVersionEdit is one issue's fix-version rewrite, through the edit
// endpoint's add and remove verbs rather than its fields object: sending the
// array would drop every other version the issue carries.
type apiFixVersionEdit struct {
	Update map[string][]apiVersionVerb `json:"update"`
}

type apiVersionVerb struct {
	Add    *apiVersionRef `json:"add,omitempty"`
	Remove *apiVersionRef `json:"remove,omitempty"`
}

type apiVersionRef struct {
	ID string `json:"id"`
}

// Versions lists a project's versions in the project's own order, archived ones
// included — an issue can already carry one, so filtering is a picker's job.
//
// Unresolved is nil on every version, which says nobody asked: no version read
// reports the count, and a zero reads as a version with nothing open on it.
// More versions than versionBound is refused rather than cut short.
func (c *Client) Versions(ctx context.Context, projectKey string) ([]jira.Version, error) {
	key := strings.TrimSpace(projectKey)
	if key == "" {
		return nil, invalidField("projectKey", "a project key is required to list versions")
	}
	path := projectVersionsPath(key)
	query := url.Values{"orderBy": {versionOrder}}
	build := func(startAt int) request {
		return request{
			method: http.MethodGet,
			path:   path,
			query:  pagedQuery(query, startAt, versionPageSize),
			kind:   "project",
			id:     key,
		}
	}
	op := http.MethodGet + " " + path
	page, err := offsetPages(ctx, c, build, func(resp *response) ([]jira.Version, int, bool, error) {
		items, total, isLast, err := decodeAgilePage[apiVersion](resp, op)
		if err != nil {
			return nil, -1, false, err
		}
		out := make([]jira.Version, 0, len(items))
		for i := range items {
			out = append(out, items[i].domain())
		}
		return out, total, isLast, nil
	})
	if err != nil {
		return nil, err
	}
	out, err := jira.Collect(ctx, page, versionBound+1)
	if err != nil {
		return nil, err
	}
	if len(out) > versionBound {
		return nil, &jira.ValidationError{Messages: []string{fmt.Sprintf(
			"project %s lists more than %d versions, which is more than this reads in one answer",
			key, versionBound)}}
	}
	return out, nil
}

// SaveVersion creates a version, or updates the one VersionInput.ID names.
//
// It sends no released key at all: releasing goes through ReleaseVersion, which
// cannot be called without deciding what happens to the issues still open. Name,
// description and both dates are sent as given, so an empty one empties the
// field; Archived stays nil to edit a version without unarchiving it.
//
// A create resolves the project's id first: the endpoint requires projectId and
// the project key it takes instead is deprecated.
func (c *Client) SaveVersion(ctx context.Context, v jira.VersionInput) (jira.Version, error) {
	name := strings.TrimSpace(v.Name)
	if name == "" {
		return jira.Version{}, invalidField("name", "a version needs a name")
	}
	if id := strings.TrimSpace(v.ID); id != "" {
		return c.updateVersion(ctx, id, name, v)
	}
	return c.createVersion(ctx, name, v)
}

func (c *Client) createVersion(ctx context.Context, name string, v jira.VersionInput) (jira.Version, error) {
	key := strings.TrimSpace(v.ProjectKey)
	if key == "" {
		return jira.Version{}, invalidField("projectKey", "a new version needs the project to put it in")
	}
	projectID, err := c.projectID(ctx, key)
	if err != nil {
		return jira.Version{}, err
	}
	body := apiVersionWrite{
		Name:        name,
		ProjectID:   &projectID,
		StartDate:   versionDay(v.StartDate),
		ReleaseDate: versionDay(v.ReleaseDate),
		Archived:    v.Archived,
	}
	if v.Description != "" {
		body.Description = &v.Description
	}
	r := request{
		method: http.MethodPost,
		path:   versionPath,
		body:   body,
		kind:   "project",
		id:     key,
	}
	var out apiVersion
	if err := c.doJSON(ctx, r, &out); err != nil {
		return jira.Version{}, err
	}
	return out.domain(), nil
}

func (c *Client) updateVersion(ctx context.Context, id, name string, v jira.VersionInput) (jira.Version, error) {
	description := v.Description
	r := request{
		method: http.MethodPut,
		path:   versionIDPath(id),
		body: apiVersionWrite{
			Name:        name,
			Description: &description,
			StartDate:   versionDayOrNull(v.StartDate),
			ReleaseDate: versionDayOrNull(v.ReleaseDate),
			Archived:    v.Archived,
		},
		kind: "version",
		id:   id,
	}
	var out apiVersion
	if err := c.doJSON(ctx, r, &out); err != nil {
		return jira.Version{}, err
	}
	return out.domain(), nil
}

// UnresolvedCount reports how many issues on a version are still open.
//
// Nothing else answers it: not the version read, not the paged project list, and
// not expand=issuesstatus, which buckets by status category and disagrees.
//
// The path says unresolvedIssueCount and the key in the answer says
// issuesUnresolvedCount. Neither key, or a negative number, is reported rather
// than read as a version with nothing left open on it.
func (c *Client) UnresolvedCount(ctx context.Context, versionID string) (int, error) {
	id := strings.TrimSpace(versionID)
	if id == "" {
		return 0, invalidField("versionId", "a version id is required to count what is open on it")
	}
	r := request{
		method: http.MethodGet,
		path:   unresolvedCountPath(id),
		kind:   "version",
		id:     id,
	}
	var answer struct {
		Unresolved *int `json:"issuesUnresolvedCount"`
	}
	if err := c.doJSON(ctx, r, &answer); err != nil {
		return 0, err
	}
	switch {
	case answer.Unresolved == nil:
		return 0, brokenCount(r.op(), "the answer carries no issuesUnresolvedCount, which is not the same as none")
	case *answer.Unresolved < 0:
		return 0, brokenCount(r.op(), fmt.Sprintf("the answer says %d issues are open, which is not a count", *answer.Unresolved))
	}
	return *answer.Unresolved, nil
}

func brokenCount(op, reason string) error {
	return &jira.TransportError{Op: op, Status: http.StatusOK, Err: errors.New(reason)}
}

// ReleaseVersion releases a version, having done what ReleaseInput says about
// the issues still open on it.
//
// The order is what makes it safe: the move target is checked, the count is
// read, the open issues are dealt with, and released is flipped last. A sweep
// that does not reach every one of them leaves the version unreleased.
//
// Unresolved carries what this call left open on the version: the count for a
// release anyway, and zero otherwise.
func (c *Client) ReleaseVersion(ctx context.Context, id string, in jira.ReleaseInput) (jira.Version, error) {
	version := strings.TrimSpace(id)
	if version == "" {
		return jira.Version{}, invalidField("versionId", "a version id is required to release one")
	}
	// The sweep names the version in JQL, where anything but a bare number is
	// matched against version names instead — a different version, or none.
	if !isNumeric(version) {
		return jira.Version{}, invalidField("versionId",
			"a version is released by the id the site minted for it, and "+version+" is not one")
	}
	target, err := releaseTarget(version, in)
	if err != nil {
		return jira.Version{}, err
	}
	if target != "" {
		if err := c.usableTarget(ctx, target); err != nil {
			return jira.Version{}, err
		}
	}

	open, err := c.UnresolvedCount(ctx, version)
	if err != nil {
		return jira.Version{}, err
	}
	left := open
	if open > 0 && in.Unresolved != jira.ReleaseAnyway {
		if open > versionSweepBound {
			return jira.Version{}, tooManyOpen(fmt.Sprintf("%d issues are still open on this version", open))
		}
		changed, err := c.sweepUnresolved(ctx, version, target)
		if err != nil {
			return jira.Version{}, err
		}
		if changed < open {
			return jira.Version{}, &jira.ValidationError{Messages: []string{fmt.Sprintf(
				"%d issues were open on this version and %d could be reached, so it was not released: count what is open again and retry",
				open, changed)}}
		}
		left = 0
	}

	released := true
	day := in.ReleaseDate
	if day.IsZero() {
		day = jira.DateOf(c.clock.Now())
	}
	r := request{
		method: http.MethodPut,
		path:   versionIDPath(version),
		body:   apiVersionWrite{Released: &released, ReleaseDate: versionDay(day)},
		kind:   "version",
		id:     version,
	}
	var out apiVersion
	if err := c.doJSON(ctx, r, &out); err != nil {
		return jira.Version{}, err
	}
	shipped := out.domain()
	shipped.Unresolved = &left
	return shipped, nil
}

// releaseTarget reads the policy and returns the version the open issues are
// moving to, or "" when they are not moving.
//
// An unrecognised policy is refused rather than fallen through: ReleaseAnyway is
// the zero value, so a caller that never set the field would otherwise release
// over the top of the open issues by default.
func releaseTarget(version string, in jira.ReleaseInput) (string, error) {
	switch in.Unresolved {
	case jira.ReleaseAnyway, jira.StripUnresolved:
		return "", nil
	case jira.MoveUnresolved:
		target := strings.TrimSpace(in.MoveToVersionID)
		switch target {
		case "":
			return "", invalidField("moveToVersionId", "moving the open issues needs a version to move them to")
		case version:
			return "", invalidField("moveToVersionId", "the open issues cannot be moved onto the version being released")
		}
		return target, nil
	default:
		return "", invalidField("unresolved", "say what happens to the issues still open: release anyway, move them to another version, or strip this one from them")
	}
}

// sweepUnresolved moves or strips the version on every issue still open, and
// reports how many it changed. A failure part way through carries that number:
// the issues before it have been rewritten and the release has not happened.
func (c *Client) sweepUnresolved(ctx context.Context, version, target string) (int, error) {
	keys, err := c.unresolvedIssues(ctx, version)
	if err != nil {
		return 0, err
	}
	changed := 0
	for _, key := range keys {
		if err := c.swapFixVersion(ctx, key, version, target); err != nil {
			if changed == 0 {
				return 0, err
			}
			return changed, fmt.Errorf("cloud: %s on %d of %d open issues, and then %s failed, so version %s was not released: %w",
				sweepVerb(target), changed, len(keys), key, version, err)
		}
		changed++
	}
	return changed, nil
}

func sweepVerb(target string) string {
	if target == "" {
		return "the fix version was stripped"
	}
	return "the fix version was moved"
}

// unresolvedIssues names the issues a release has to deal with: the ones
// carrying the version whose resolution is empty, which is the measure the count
// uses — the status category is a different question.
//
// A result set past versionSweepBound is refused before anything is written: a
// walk stopped at its limit is indistinguishable from one that ended.
func (c *Client) unresolvedIssues(ctx context.Context, version string) ([]string, error) {
	page, err := c.Search(ctx, jira.Query{
		JQL:        "fixVersion = " + version + " AND resolution IS EMPTY ORDER BY key ASC",
		Fields:     []string{"fixVersions"},
		MaxResults: versionPageSize,
	})
	if err != nil {
		return nil, err
	}
	issues, err := jira.Collect(ctx, page, versionSweepBound+1)
	if err != nil {
		return nil, err
	}
	if len(issues) > versionSweepBound {
		return nil, tooManyOpen(fmt.Sprintf(
			"the search for what is open on this version is still paging past %d issues", versionSweepBound))
	}
	keys := make([]string, 0, len(issues))
	for i := range issues {
		keys = append(keys, issues[i].Key)
	}
	return keys, nil
}

func tooManyOpen(what string) error {
	return &jira.ValidationError{Messages: []string{fmt.Sprintf(
		"%s, more than the %d one release will rewrite: move them from the issue list, or release anyway",
		what, versionSweepBound)}}
}

func (c *Client) swapFixVersion(ctx context.Context, key, from, to string) error {
	verbs := []apiVersionVerb{{Remove: &apiVersionRef{ID: from}}}
	if to != "" {
		verbs = append(verbs, apiVersionVerb{Add: &apiVersionRef{ID: to}})
	}
	r := request{
		method: http.MethodPut,
		path:   issuePath + "/" + url.PathEscape(key),
		body:   apiFixVersionEdit{Update: map[string][]apiVersionVerb{"fixVersions": verbs}},
		kind:   "issue",
		id:     key,
	}
	_, err := c.do(ctx, r)
	return err
}

// usableTarget is the one read a move does before it writes: a target that does
// not exist, or that no issue may carry, would otherwise be found out half way
// through a sweep that cannot be undone.
func (c *Client) usableTarget(ctx context.Context, id string) error {
	var out apiVersion
	if err := c.doJSON(ctx, request{
		method: http.MethodGet,
		path:   versionIDPath(id),
		kind:   "version",
		id:     id,
	}, &out); err != nil {
		return err
	}
	if out.Archived {
		return invalidField("moveToVersionId",
			"version "+id+" is archived, and an archived version cannot be put on an issue")
	}
	return nil
}

// projectID reads a project's numeric id, which is what creating a version
// takes. It is per site, so it is resolved rather than written down.
func (c *Client) projectID(ctx context.Context, projectKey string) (json.Number, error) {
	r := request{
		method: http.MethodGet,
		path:   projectPath + "/" + url.PathEscape(projectKey),
		kind:   "project",
		id:     projectKey,
	}
	var answer struct {
		ID flexString `json:"id"`
	}
	if err := c.doJSON(ctx, r, &answer); err != nil {
		return "", err
	}
	id := json.Number(strings.TrimSpace(string(answer.ID)))
	if _, err := id.Int64(); err != nil {
		return "", &jira.TransportError{
			Op:     r.op(),
			Status: http.StatusOK,
			Err:    fmt.Errorf("project %s came back with no numeric id, which is what a version is created against", projectKey),
		}
	}
	return id, nil
}

// versionDay is the JSON a version's date carries, and nothing for an unset one.
func versionDay(d jira.Date) json.RawMessage {
	if d.IsZero() {
		return nil
	}
	return json.RawMessage(`"` + d.String() + `"`)
}

// versionDayOrNull is versionDay for an update, where an unset date is a date to
// empty rather than one to leave alone.
func versionDayOrNull(d jira.Date) json.RawMessage {
	if day := versionDay(d); day != nil {
		return day
	}
	return json.RawMessage("null")
}

func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

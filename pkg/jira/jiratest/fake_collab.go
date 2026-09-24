package jiratest

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/varijkapil13/saral/pkg/jira"
)

// fakeLinkTypes are this site's link types. None is a stock Jira type, so code
// that looks a type up by the name "Blocks" finds nothing here.
var fakeLinkTypes = []jira.LinkType{
	{ID: "20001", Name: "Holds up", Inward: "is held up by", Outward: "holds up"},
	{ID: "20002", Name: "Echoes", Inward: "is echoed by", Outward: "echoes"},
	{ID: "20003", Name: "Leans on", Inward: "is leaned on by", Outward: "leans on"},
}

var fakeDefaultServerInfo = jira.ServerInfo{
	BaseURL:        fakeBaseURL,
	Version:        "1001.0.0-SNAPSHOT",
	BuildNumber:    100287,
	DeploymentType: jira.DeploymentCloud,
	ServerTitle:    "Example Jira",
}

// IssueFields fetches one issue with only the fields named, refusing a read that
// names none the way the adapter does.
func (f *Fake) IssueFields(ctx context.Context, key string, fields []string) (jira.Issue, error) {
	if err := f.fakeBegin(ctx, "IssueFields"); err != nil {
		return jira.Issue{}, err
	}
	mask := jira.NewFieldMask(fields)
	if mask.Len() == 0 && !mask.Wide() {
		return jira.Issue{}, fakeInvalid("fields", "a narrow issue read must name the fields it wants; Issue is the read for all of them")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	iss, ok := f.issues[strings.TrimSpace(key)]
	if !ok {
		return jira.Issue{}, fakeNotFound("issue", strings.TrimSpace(key))
	}
	out := fakeCloneIssue(iss)
	fakeApplyFieldMask(&out, mask)
	if fakeExpandsSchema(fields) {
		f.fakeUntypedAsBytes(&out)
	}
	return out, nil
}

// SprintIssues lists what a board shows of one sprint: the board's own set,
// narrowed to the issues the sprint holds.
func (f *Fake) SprintIssues(ctx context.Context, boardID, sprintID int64, q jira.BoardQuery) (jira.Page[jira.Issue], error) {
	if err := fakeSprintIDCheck(sprintID); err != nil {
		return jira.Page[jira.Issue]{}, err
	}
	return f.fakeBoardIssues(ctx, "SprintIssues", boardID, q, fakeBoardScope{sprint: sprintID})
}

// fakeBoardSprint refuses a sprint the board cannot be asked about: a board that
// runs none, and a sprint the site does not have.
func (f *Fake) fakeBoardSprint(board *jira.Board, sprintID int64) error {
	if board.Type != jira.BoardScrum {
		return fakeInvalid("boardId", "this board does not support sprints")
	}
	if _, ok := f.sprints[sprintID]; !ok {
		return fakeNotFound("sprint", strconv.FormatInt(sprintID, 10))
	}
	return nil
}

// RankIssues moves issues to just before or just after another one, and
// rewrites the rank field of every ranked issue so the order reads back through
// BoardIssues. An issue the site does not have is refused on its own and the
// rest still move, which is what the endpoint's multi-status answer does.
func (f *Fake) RankIssues(ctx context.Context, keys []string, at jira.RankPosition) error {
	if err := f.fakeBegin(ctx, "RankIssues"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	before, after := strings.TrimSpace(at.Before), strings.TrimSpace(at.After)
	switch {
	case before == "" && after == "":
		return fakeInvalid("rank", "a rank needs an issue to go before or after")
	case before != "" && after != "":
		return fakeInvalid("rank", "a rank goes before one issue or after one, not both")
	}
	anchor, below := at.Anchor()
	wanted := fakeUniqueKeys(keys)
	if slices.Contains(wanted, anchor) {
		return fakeInvalid("issues", anchor+" cannot be ranked relative to itself")
	}
	ref, ranked := fakeRefByName(f.fields, "Rank")
	if !ranked {
		return fakeInvalid("rankCustomFieldId", "this site has no rank field")
	}
	if id := strings.TrimSpace(at.FieldID); id != "" && id != ref.ID {
		return fakeInvalid("rankCustomFieldId", id+" is not this site's rank field")
	}
	if len(wanted) == 0 {
		return nil
	}
	if _, ok := f.issues[anchor]; !ok {
		return fakeNotFound("issue", anchor)
	}

	var moved []string
	var failed []jira.RankFailure
	for _, key := range wanted {
		if _, ok := f.issues[key]; ok {
			moved = append(moved, key)
			continue
		}
		failed = append(failed, jira.RankFailure{Key: key, Reason: "Issue does not exist or you do not have permission to see it."})
	}

	order := f.fakeRankOrder(ref)
	order = slices.DeleteFunc(order, func(key string) bool { return slices.Contains(moved, key) })
	at0 := slices.Index(order, anchor)
	if below {
		at0++
	}
	order = slices.Insert(order, at0, moved...)
	for i, key := range order {
		iss := f.issues[key]
		iss.Fields = iss.Fields.With(ref, jira.FieldValue{Kind: jira.KindText, Text: fmt.Sprintf("0|r%06d:", i)})
		if slices.Contains(moved, key) {
			iss.Updated = f.now
		}
	}
	if len(failed) > 0 {
		return &jira.PartialRankError{Ranked: moved, Failed: failed}
	}
	return nil
}

// fakeRankOrder is every issue in rank order: ranked issues by their rank, then
// the unranked ones in the order they were loaded, which is where a board draws
// them.
func (f *Fake) fakeRankOrder(ref jira.FieldRef) []string {
	order := slices.Clone(f.issueKeys)
	slices.SortStableFunc(order, func(a, b string) int {
		left, hasLeft := f.issues[a].Fields.Text(ref)
		right, hasRight := f.issues[b].Fields.Text(ref)
		switch {
		case hasLeft && hasRight:
			return strings.Compare(left, right)
		case hasLeft:
			return -1
		case hasRight:
			return 1
		default:
			return 0
		}
	})
	return order
}

// IssueLinkTypes lists this site's link types.
func (f *Fake) IssueLinkTypes(ctx context.Context) ([]jira.LinkType, error) {
	if err := f.fakeBegin(ctx, "IssueLinkTypes"); err != nil {
		return nil, err
	}
	return slices.Clone(fakeLinkTypes), nil
}

// LinkIssues links two issues, writing the link onto both ends. A link that
// already exists is not written twice and is not refused either, which is what
// the endpoint does with a duplicate.
func (f *Fake) LinkIssues(ctx context.Context, in jira.LinkInput) error {
	if err := f.fakeBegin(ctx, "LinkIssues"); err != nil {
		return err
	}
	typeID, from, to := strings.TrimSpace(in.TypeID), strings.TrimSpace(in.From), strings.TrimSpace(in.To)
	switch {
	case typeID == "":
		return fakeInvalid("type", "a link needs a link type id; match it by id, never by the localised name")
	case from == "":
		return fakeInvalid("inwardIssue", "a link needs the issue it starts at")
	case to == "":
		return fakeInvalid("outwardIssue", "a link needs the issue it ends at")
	case from == to:
		return fakeInvalid("outwardIssue", from+" cannot be linked to itself")
	}
	idx := slices.IndexFunc(fakeLinkTypes, func(t jira.LinkType) bool { return t.ID == typeID })
	if idx < 0 {
		return fakeNotFound("issue link type", typeID)
	}
	kind := fakeLinkTypes[idx]

	f.mu.Lock()
	defer f.mu.Unlock()
	source, ok := f.issues[from]
	if !ok {
		return fakeNotFound("issue", from)
	}
	target, ok := f.issues[to]
	if !ok {
		return fakeNotFound("issue", to)
	}
	if slices.ContainsFunc(source.Links, func(l jira.IssueLink) bool {
		return l.Type == kind.Name && l.Direction == jira.LinkOutward && l.Other.Key == to
	}) {
		return nil
	}
	id := f.fakeNextID("link")
	source.Links = append(source.Links, jira.IssueLink{
		ID: id, Type: kind.Name, Label: kind.Outward, Direction: jira.LinkOutward, Other: fakeRefOf(target),
	})
	target.Links = append(target.Links, jira.IssueLink{
		ID: id, Type: kind.Name, Label: kind.Inward, Direction: jira.LinkInward, Other: fakeRefOf(source),
	})
	source.Updated, target.Updated = f.now, f.now
	return nil
}

// DeleteLink removes a link from both of its ends.
func (f *Fake) DeleteLink(ctx context.Context, linkID string) error {
	if err := f.fakeBegin(ctx, "DeleteLink"); err != nil {
		return err
	}
	id := strings.TrimSpace(linkID)
	if id == "" {
		return fakeInvalid("linkId", "a link id is required; read it from Issue.Links")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	found := false
	for _, key := range f.issueKeys {
		iss := f.issues[key]
		kept := slices.DeleteFunc(slices.Clone(iss.Links), func(l jira.IssueLink) bool { return l.ID == id })
		if len(kept) != len(iss.Links) {
			found = true
			iss.Links = kept
			iss.Updated = f.now
		}
	}
	if !found {
		return fakeNotFound("issue link", id)
	}
	return nil
}

// Worklogs lists the time logged on an issue, oldest first, paged by offset.
func (f *Fake) Worklogs(ctx context.Context, key string) (jira.Page[jira.Worklog], error) {
	id := strings.TrimSpace(key)
	return jira.Offset(ctx, func(ctx context.Context, startAt int) ([]jira.Worklog, int, bool, error) {
		if err := f.fakeBegin(ctx, "Worklogs"); err != nil {
			return nil, -1, false, err
		}
		if id == "" {
			return nil, -1, false, fakeInvalid("issueIdOrKey", "an issue key or id is required")
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if _, ok := f.issues[id]; !ok {
			return nil, -1, false, fakeNotFound("issue", id)
		}
		all := f.worklogs[id]
		start := min(max(startAt, 0), len(all))
		end := min(start+f.pageSize, len(all))
		out := make([]jira.Worklog, 0, end-start)
		for i := start; i < end; i++ {
			out = append(out, fakeCloneWorklog(all[i]))
		}
		return out, len(all), end >= len(all), nil
	})
}

// AddWorklog logs time on an issue as the authenticated account, and takes it
// off the remaining estimate the way the endpoint does by default.
func (f *Fake) AddWorklog(ctx context.Context, key string, in jira.WorklogInput) (jira.Worklog, error) {
	if err := f.fakeBegin(ctx, "AddWorklog"); err != nil {
		return jira.Worklog{}, err
	}
	seconds := int64(in.Spent / time.Second)
	switch {
	case strings.TrimSpace(key) == "":
		return jira.Worklog{}, fakeInvalid("issueIdOrKey", "an issue key or id is required")
	case seconds <= 0:
		return jira.Worklog{}, fakeInvalid("timeSpentSeconds", "a worklog needs a positive amount of time")
	case in.Started.IsZero():
		return jira.Worklog{}, fakeInvalid("started", "a worklog needs the time the work started")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	id := strings.TrimSpace(key)
	iss, ok := f.issues[id]
	if !ok {
		return jira.Worklog{}, fakeNotFound("issue", id)
	}
	entry := jira.Worklog{
		ID:      f.fakeNextID("worklog"),
		IssueID: iss.ID,
		Author:  f.me,
		Comment: in.Comment.Clone(),
		Started: in.Started.Truncate(time.Millisecond),
		Spent:   time.Duration(seconds) * time.Second,
		Created: f.now,
		Updated: f.now,
	}
	f.worklogs[id] = append(f.worklogs[id], entry)
	tracking := jira.TimeTracking{}
	if iss.TimeTracking != nil {
		tracking = *iss.TimeTracking
	}
	tracking.TimeSpent += seconds
	tracking.RemainingEstimate = max(0, tracking.RemainingEstimate-seconds)
	iss.TimeTracking = &tracking
	iss.Updated = f.now
	return fakeCloneWorklog(entry), nil
}

func fakeCloneWorklog(in jira.Worklog) jira.Worklog {
	out := in
	out.Comment = in.Comment.Clone()
	out.UpdateAuthor = fakeClonePtr(in.UpdateAuthor)
	out.Visibility = fakeClonePtr(in.Visibility)
	return out
}

// Watchers reports who watches an issue, in the order they started.
func (f *Fake) Watchers(ctx context.Context, key string) (jira.WatcherList, error) {
	if err := f.fakeBegin(ctx, "Watchers"); err != nil {
		return jira.WatcherList{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	id := strings.TrimSpace(key)
	if _, ok := f.issues[id]; !ok {
		return jira.WatcherList{}, fakeNotFound("issue", id)
	}
	ids := f.watchers[id]
	out := jira.WatcherList{Count: len(ids), Watching: slices.Contains(ids, f.me.AccountID)}
	for _, account := range ids {
		out.People = append(out.People, *f.fakeUser(account))
	}
	return out, nil
}

// Watch adds a watcher; an empty account is the authenticated one. Watching an
// issue already watched is not an error.
func (f *Fake) Watch(ctx context.Context, key, accountID string) error {
	if err := f.fakeBegin(ctx, "Watch"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	id := strings.TrimSpace(key)
	if _, ok := f.issues[id]; !ok {
		return fakeNotFound("issue", id)
	}
	account := strings.TrimSpace(accountID)
	if account == "" {
		account = f.me.AccountID
	}
	if !f.fakeKnownAccount(account) {
		return fakeNotFound("user", account)
	}
	if !slices.Contains(f.watchers[id], account) {
		f.watchers[id] = append(f.watchers[id], account)
	}
	return nil
}

// Unwatch removes a watcher. The account is required, as it is on the wire.
func (f *Fake) Unwatch(ctx context.Context, key, accountID string) error {
	if err := f.fakeBegin(ctx, "Unwatch"); err != nil {
		return err
	}
	account := strings.TrimSpace(accountID)
	if account == "" {
		return fakeInvalid("accountId", "removing a watcher names the account, the authenticated one included")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	id := strings.TrimSpace(key)
	if _, ok := f.issues[id]; !ok {
		return fakeNotFound("issue", id)
	}
	f.watchers[id] = slices.DeleteFunc(f.watchers[id], func(held string) bool { return held == account })
	return nil
}

func (f *Fake) fakeKnownAccount(accountID string) bool {
	if accountID == f.me.AccountID {
		return true
	}
	known := func(u jira.User) bool { return u.AccountID == accountID }
	return slices.ContainsFunc(fakeUsers, known) || slices.ContainsFunc(f.people, known)
}

// ServerInfo reports what this fake site is: Jira Cloud, unless WithServerInfo
// says otherwise.
func (f *Fake) ServerInfo(ctx context.Context) (jira.ServerInfo, error) {
	if err := f.fakeBegin(ctx, "ServerInfo"); err != nil {
		return jira.ServerInfo{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.serverInfo, nil
}

// fakeLabelEdits refuses the label edits the adapter refuses before sending, so
// that a patch the site would never see is one the fake never applies.
func fakeLabelEdits(in *jira.IssuePatch) error {
	if len(in.AddLabels) == 0 && len(in.RemoveLabels) == 0 {
		return nil
	}
	if in.Labels != nil {
		return fakeInvalid("labels", "a patch replaces the labels or edits them, not both")
	}
	if slices.ContainsFunc(in.Clear, func(ref jira.FieldRef) bool { return strings.TrimSpace(ref.ID) == "labels" }) {
		return fakeInvalid("labels", "a patch clears the labels or edits them, not both")
	}
	if _, set := in.Fields.ByID("labels"); set {
		return fakeInvalid("labels", "a patch replaces the labels or edits them, not both")
	}
	seen := make(map[string]bool, len(in.AddLabels)+len(in.RemoveLabels))
	for _, label := range slices.Concat(in.AddLabels, in.RemoveLabels) {
		trimmed := strings.TrimSpace(label)
		switch {
		case trimmed == "":
			return fakeInvalid("labels", "a label to add or remove is empty")
		case strings.ContainsFunc(trimmed, unicode.IsSpace):
			return fakeInvalid("labels", strconv.Quote(trimmed)+" is not a label: a label cannot contain a space")
		case seen[trimmed]:
			return fakeInvalid("labels", "this patch adds or removes "+strconv.Quote(trimmed)+" twice")
		}
		seen[trimmed] = true
	}
	return nil
}

func (f *Fake) fakeFixVersionEdits(in *jira.IssuePatch) error {
	if len(in.AddFixVersions) == 0 && len(in.RemoveFixVersions) == 0 {
		return nil
	}
	if slices.ContainsFunc(in.Clear, func(ref jira.FieldRef) bool { return strings.TrimSpace(ref.ID) == "fixVersions" }) {
		return fakeInvalid("fixVersions", "a patch clears the fix versions or edits them, not both")
	}
	if _, set := in.Fields.ByID("fixVersions"); set {
		return fakeInvalid("fixVersions", "a patch replaces the fix versions or edits them, not both")
	}
	seen := make(map[string]bool, len(in.AddFixVersions)+len(in.RemoveFixVersions))
	for _, id := range slices.Concat(in.AddFixVersions, in.RemoveFixVersions) {
		trimmed := strings.TrimSpace(id)
		switch {
		case trimmed == "":
			return fakeInvalid("fixVersions", "a fix version to add or remove has no id")
		case seen[trimmed]:
			return fakeInvalid("fixVersions", "this patch adds or removes version "+strconv.Quote(trimmed)+" twice")
		}
		if _, ok := f.versions[trimmed]; !ok {
			return fakeInvalid("fixVersions", "version "+strconv.Quote(trimmed)+" does not exist")
		}
		seen[trimmed] = true
	}
	return nil
}

func (f *Fake) fakeApplyFixVersionEdits(iss *jira.Issue, in *jira.IssuePatch) {
	for _, id := range in.AddFixVersions {
		id = strings.TrimSpace(id)
		if slices.ContainsFunc(iss.FixVersions, func(v jira.Version) bool { return v.ID == id }) {
			continue
		}
		iss.FixVersions = append(iss.FixVersions, fakeCloneVersion(f.versions[id]))
	}
	for _, id := range in.RemoveFixVersions {
		id = strings.TrimSpace(id)
		iss.FixVersions = slices.DeleteFunc(iss.FixVersions, func(v jira.Version) bool { return v.ID == id })
	}
}

func fakeRefOf(iss *jira.Issue) jira.IssueRef {
	return jira.IssueRef{ID: iss.ID, Key: iss.Key, Summary: iss.Summary, Status: iss.Status, Type: iss.Type}
}

func fakeUniqueKeys(keys []string) []string {
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		if trimmed := strings.TrimSpace(key); trimmed != "" && !slices.Contains(out, trimmed) {
			out = append(out, trimmed)
		}
	}
	return out
}

package jiratest

import (
	"cmp"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
)

const fakeDownloadChunk = 4096

func fakeNotFound(kind, id string) error { return &jira.NotFoundError{Kind: kind, ID: id} }

func fakeInvalid(field, msg string) error {
	return &jira.ValidationError{Fields: []jira.FieldError{{Field: field, Message: msg}}}
}

// Capabilities reports what this fake site and token can do in a project.
// Boards is answered from whether that project actually has one, which is what
// makes a site-wide probe visibly wrong here.
func (f *Fake) Capabilities(ctx context.Context, projectKey string) (jira.Capabilities, error) {
	if err := f.fakeBegin(ctx, "Capabilities"); err != nil {
		return jira.Capabilities{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	caps := f.caps
	if projectKey == "" {
		unscoped := jira.Capability{Reason: "no project is selected, so per-project permissions are unknown"}
		caps.Boards, caps.BulkMove, caps.DeleteIssues = unscoped, unscoped, unscoped
		return caps, nil
	}
	proj, ok := f.projects[projectKey]
	if !ok {
		return jira.Capabilities{}, fakeNotFound("project", projectKey)
	}
	if caps.Boards.OK && proj.boardID == 0 {
		caps.Boards = jira.Capability{Reason: fmt.Sprintf("%s has no board", projectKey)}
	}
	return caps, nil
}

// Me returns the authenticated account, and refuses an account with no ID the
// way a real site's answer is refused: a caller that reads a success here as
// proof a credential works must not be able to get that proof from nobody.
// WithMe is how a test asks for the refusal.
func (f *Fake) Me(ctx context.Context) (jira.User, error) {
	if err := f.fakeBegin(ctx, "Me"); err != nil {
		return jira.User{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.me.AccountID == "" {
		return jira.User{}, &jira.TransportError{
			Op:  "read the authenticated account",
			Err: errors.New("the answer names no account"),
		}
	}
	return f.me, nil
}

// Fields returns the fake site's field catalogue. Its custom field IDs are not
// the ones a stock site allocates, so a caller that hardcoded one gets nothing.
func (f *Fake) Fields(ctx context.Context) ([]jira.Field, error) {
	if err := f.fakeBegin(ctx, "Fields"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return fakeCloneFields(f.fields), nil
}

// Search runs the subset of JQL the fake understands. It is deliberately small
// — it exists so that a view's own query can be run against the fake, not to be
// a query engine — and it is exactly this:
//
//   - the fields project, key, issuekey, status, issuetype, type, priority,
//     assignee, reporter and labels, each matching on the site's id or on the
//     display name;
//   - the operators = , IN (a, b, ...), IS EMPTY and IS NOT EMPTY;
//   - currentUser() as a value on assignee and reporter;
//   - clauses joined by AND, or by OR inside one pair of brackets;
//   - an optional trailing ORDER BY <field> [ASC|DESC].
//
// Anything else — an unbracketed OR, ~, an inequality, a date function, NOT, a
// field not on that list — is a *jira.ValidationError naming jql, so a query the
// fake cannot honour never passes as a query that matched everything.
func (f *Fake) Search(ctx context.Context, q jira.Query) (jira.Page[jira.Issue], error) {
	mask := jira.NewFieldMask(q.Fields)
	return jira.Cursor(ctx, func(ctx context.Context, token string) ([]jira.Issue, string, error) {
		if err := f.fakeBegin(ctx, "Search"); err != nil {
			return nil, "", err
		}
		f.mu.Lock()
		defer f.mu.Unlock()

		if mask.Len() == 0 && !mask.Wide() {
			return nil, "", fakeInvalid("fields", "a search must name the fields it wants; /search/jql returns almost nothing without them")
		}
		plan, err := fakeParseJQL(q.JQL)
		if err != nil {
			return nil, "", err
		}
		offset, err := fakeDecodeCursor(token)
		if err != nil {
			return nil, "", err
		}

		matched := f.fakeMatchIssues(plan)
		size := f.pageSize
		if q.MaxResults > 0 && q.MaxResults < size {
			size = q.MaxResults
		}
		if offset > len(matched) {
			offset = len(matched)
		}
		end := min(offset+size, len(matched))

		items := make([]jira.Issue, 0, end-offset)
		for _, iss := range matched[offset:end] {
			clone := fakeCloneIssue(iss)
			fakeApplyFieldMask(&clone, mask)
			items = append(items, clone)
		}
		next := ""
		switch {
		case end >= len(matched):
		case f.cursorLoop && offset > 0:
			next = fakeEncodeCursor(offset)
		default:
			next = fakeEncodeCursor(end)
		}
		return items, next, nil
	})
}

// Issue fetches one issue by key.
func (f *Fake) Issue(ctx context.Context, key string) (jira.Issue, error) {
	if err := f.fakeBegin(ctx, "Issue"); err != nil {
		return jira.Issue{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	iss, ok := f.issues[key]
	if !ok {
		return jira.Issue{}, fakeNotFound("issue", key)
	}
	return fakeCloneIssue(iss), nil
}

// CreateIssue creates an issue, refusing a missing summary or an unknown
// project, issue type or parent with a *jira.ValidationError naming the field.
func (f *Fake) CreateIssue(ctx context.Context, in jira.IssueInput) (jira.Issue, error) {
	if err := f.fakeBegin(ctx, "CreateIssue"); err != nil {
		return jira.Issue{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	if strings.TrimSpace(in.Summary) == "" {
		return jira.Issue{}, fakeInvalid("summary", "summary is required")
	}
	proj, ok := f.projects[in.ProjectKey]
	if !ok {
		return jira.Issue{}, fakeInvalid("project", fmt.Sprintf("no project %q on this site", in.ProjectKey))
	}
	typ := fakeIssueTypeByID(in.IssueTypeID)
	if typ == nil {
		return jira.Issue{}, fakeInvalid("issuetype", fmt.Sprintf("no issue type %q on this site", in.IssueTypeID))
	}
	if typ.Subtask && in.ParentKey == "" {
		return jira.Issue{}, fakeInvalid("parent", fmt.Sprintf("issue type %q is a subtask and needs a parent", typ.Name))
	}
	for _, id := range in.Fields.IDs() {
		if err := f.fakeKnownField(id); err != nil {
			return jira.Issue{}, err
		}
	}
	var parent *jira.IssueRef
	if in.ParentKey != "" {
		p, found := f.issues[in.ParentKey]
		if !found {
			return jira.Issue{}, fakeInvalid("parent", fmt.Sprintf("no issue %q to hang this off", in.ParentKey))
		}
		parent = &jira.IssueRef{ID: p.ID, Key: p.Key, Summary: p.Summary, Status: p.Status, Type: p.Type}
	}

	reporter := f.me
	iss := jira.Issue{
		ID:          f.fakeNextID("issue"),
		Key:         f.fakeNextKey(proj.ref.Key),
		Project:     proj.ref,
		Summary:     in.Summary,
		Description: in.Description,
		Type:        *typ,
		Status:      fakeStatuses[0],
		Reporter:    &reporter,
		Labels:      slices.Clone(in.Labels),
		Parent:      parent,
		Created:     f.now,
		Updated:     f.now,
		Fields:      in.Fields,
	}
	iss.Assignee = f.fakeUser(in.Assignee)
	f.fakePutIssue(&iss)
	return fakeCloneIssue(f.issues[iss.Key]), nil
}

// UpdateIssue applies a sparse patch: a nil pointer leaves a field alone, a set
// pointer writes it, and a field named in Clear is nulled.
func (f *Fake) UpdateIssue(ctx context.Context, key string, in jira.IssuePatch) error {
	if err := f.fakeBegin(ctx, "UpdateIssue"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	iss, ok := f.issues[key]
	if !ok {
		return fakeNotFound("issue", key)
	}
	return f.fakeApplyPatch(iss, &in)
}

// Transitions lists the workflow moves available on an issue right now.
func (f *Fake) Transitions(ctx context.Context, key string) ([]jira.Transition, error) {
	if err := f.fakeBegin(ctx, "Transitions"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	iss, ok := f.issues[key]
	if !ok {
		return nil, fakeNotFound("issue", key)
	}
	return fakeTransitionsFor(iss.Status), nil
}

// Transition moves an issue and applies whatever the transition screen carried.
// The move into the done category has a screen with a required resolution, so
// transition-screen validation has something to fail against.
func (f *Fake) Transition(ctx context.Context, key, transitionID string, in jira.IssuePatch) error {
	if err := f.fakeBegin(ctx, "Transition"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	iss, ok := f.issues[key]
	if !ok {
		return fakeNotFound("issue", key)
	}
	idx := slices.IndexFunc(fakeTransitionsFor(iss.Status), func(t jira.Transition) bool { return t.ID == transitionID })
	if idx < 0 {
		return fakeNotFound("transition", transitionID)
	}
	tr := fakeTransitionsFor(iss.Status)[idx]

	var resolution *jira.Resolution
	if tr.HasScreen {
		res, err := fakeResolutionFrom(in.Fields)
		if err != nil {
			return err
		}
		resolution = res
	}
	if err := f.fakeApplyPatch(iss, &in); err != nil {
		return err
	}
	iss.Status = tr.To
	if tr.To.Category == jira.CategoryDone {
		done := f.now
		iss.Resolved = &done
		iss.Resolution = resolution
	} else {
		iss.Resolved = nil
		iss.Resolution = nil
	}
	return nil
}

// Comments lists an issue's comments, oldest first, paged by offset.
func (f *Fake) Comments(ctx context.Context, key string) (jira.Page[jira.Comment], error) {
	return jira.Offset(ctx, func(ctx context.Context, startAt int) ([]jira.Comment, int, bool, error) {
		if err := f.fakeBegin(ctx, "Comments"); err != nil {
			return nil, 0, false, err
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if _, ok := f.issues[key]; !ok {
			return nil, 0, false, fakeNotFound("issue", key)
		}
		all := f.comments[key]
		start := min(startAt, len(all))
		end := min(start+f.pageSize, len(all))
		return slices.Clone(all[start:end]), len(all), end >= len(all), nil
	})
}

// AddComment adds a comment authored by the authenticated account.
func (f *Fake) AddComment(ctx context.Context, key string, body adf.Doc) (jira.Comment, error) {
	if err := f.fakeBegin(ctx, "AddComment"); err != nil {
		return jira.Comment{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.issues[key]; !ok {
		return jira.Comment{}, fakeNotFound("issue", key)
	}
	c := jira.Comment{
		ID:      f.fakeNextID("cmt"),
		Author:  f.me,
		Body:    body,
		Created: f.now,
		Updated: f.now,
	}
	f.comments[key] = append(f.comments[key], c)
	return c, nil
}

// EditComment replaces a comment's body.
func (f *Fake) EditComment(ctx context.Context, key, id string, body adf.Doc) (jira.Comment, error) {
	if err := f.fakeBegin(ctx, "EditComment"); err != nil {
		return jira.Comment{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.issues[key]; !ok {
		return jira.Comment{}, fakeNotFound("issue", key)
	}
	list := f.comments[key]
	idx := slices.IndexFunc(list, func(c jira.Comment) bool { return c.ID == id })
	if idx < 0 {
		return jira.Comment{}, fakeNotFound("comment", id)
	}
	author := f.me
	list[idx].Body = body
	list[idx].Updated = f.now
	list[idx].UpdateAuthor = &author
	return list[idx], nil
}

// DeleteComment removes a comment.
func (f *Fake) DeleteComment(ctx context.Context, key, id string) error {
	if err := f.fakeBegin(ctx, "DeleteComment"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.issues[key]; !ok {
		return fakeNotFound("issue", key)
	}
	list := f.comments[key]
	idx := slices.IndexFunc(list, func(c jira.Comment) bool { return c.ID == id })
	if idx < 0 {
		return fakeNotFound("comment", id)
	}
	f.comments[key] = slices.Delete(list, idx, idx+1)
	return nil
}

// Attachments lists an issue's attachments.
func (f *Fake) Attachments(ctx context.Context, key string) ([]jira.Attachment, error) {
	if err := f.fakeBegin(ctx, "Attachments"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.issues[key]; !ok {
		return nil, fakeNotFound("issue", key)
	}
	return slices.Clone(f.attachments[key]), nil
}

// Upload attaches files to an issue, reading each FileRef exactly once.
func (f *Fake) Upload(ctx context.Context, key string, files []jira.FileRef) ([]jira.Attachment, error) {
	if err := f.fakeBegin(ctx, "Upload"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.caps.Require(jira.CapAttachments); err != nil {
		return nil, err
	}
	if _, ok := f.issues[key]; !ok {
		return nil, fakeNotFound("issue", key)
	}
	added := make([]jira.Attachment, 0, len(files))
	for i := range files {
		size, err := fakeReadAll(&files[i])
		if err != nil {
			return nil, err
		}
		att := jira.Attachment{
			ID:       f.fakeNextID("att"),
			Filename: files[i].Name,
			MimeType: fakeMimeType(files[i].Name),
			Size:     size,
			Created:  f.now,
			Author:   f.me,
		}
		att.ContentURL = fakeBaseURL + "/rest/api/3/attachment/content/" + att.ID
		att.ThumbnailURL = fakeBaseURL + "/rest/api/3/attachment/thumbnail/" + att.ID
		f.attachments[key] = append(f.attachments[key], att)
		f.attachOwner[att.ID] = key
		added = append(added, att)
	}
	return added, nil
}

// Download streams an attachment's bytes, which are derived from its ID and so
// are the same on every run, reporting the running total as it goes.
func (f *Fake) Download(ctx context.Context, id string, w io.Writer, opt jira.DownloadOptions) error {
	if err := f.fakeBegin(ctx, "Download"); err != nil {
		return err
	}
	f.mu.Lock()
	att, ok := f.fakeAttachment(id)
	f.mu.Unlock()
	if !ok {
		return fakeNotFound("attachment", id)
	}

	data := fakeAttachmentBytes(&att)
	if opt.From < 0 || opt.From > int64(len(data)) {
		return fakeInvalid("from", fmt.Sprintf("cannot resume at byte %d of a %d byte attachment", opt.From, len(data)))
	}
	data = data[opt.From:]
	progress := opt.Progress
	var written int64
	for off := 0; off < len(data); off += fakeDownloadChunk {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := w.Write(data[off:min(off+fakeDownloadChunk, len(data))])
		written += int64(n)
		if err != nil {
			return &jira.TransportError{Op: "download attachment " + id, Err: err}
		}
		if progress != nil {
			progress(written)
		}
	}
	return nil
}

// DeleteAttachment removes an attachment.
func (f *Fake) DeleteAttachment(ctx context.Context, id string) error {
	if err := f.fakeBegin(ctx, "DeleteAttachment"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.caps.Require(jira.CapAttachments); err != nil {
		return err
	}
	key, ok := f.attachOwner[id]
	if !ok {
		return fakeNotFound("attachment", id)
	}
	list := f.attachments[key]
	idx := slices.IndexFunc(list, func(a jira.Attachment) bool { return a.ID == id })
	if idx >= 0 {
		f.attachments[key] = slices.Delete(list, idx, idx+1)
	}
	delete(f.attachOwner, id)
	return nil
}

// Versions lists a project's versions.
func (f *Fake) Versions(ctx context.Context, projectKey string) ([]jira.Version, error) {
	if err := f.fakeBegin(ctx, "Versions"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	proj, ok := f.projects[projectKey]
	if !ok {
		return nil, fakeNotFound("project", projectKey)
	}
	out := make([]jira.Version, 0, len(proj.versionIDs))
	for _, id := range proj.versionIDs {
		if v, found := f.versions[id]; found {
			out = append(out, fakeCloneVersion(v))
		}
	}
	return out, nil
}

// SaveVersion creates a version, or updates the one VersionInput.ID names. It
// cannot release one: that goes through ReleaseVersion, which has to be told
// what to do about the issues still open on it.
func (f *Fake) SaveVersion(ctx context.Context, v jira.VersionInput) (jira.Version, error) {
	if err := f.fakeBegin(ctx, "SaveVersion"); err != nil {
		return jira.Version{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.TrimSpace(v.Name) == "" {
		return jira.Version{}, fakeInvalid("name", "a version needs a name")
	}
	if v.ID != "" {
		stored, ok := f.versions[v.ID]
		if !ok {
			return jira.Version{}, fakeNotFound("version", v.ID)
		}
		stored.Name = v.Name
		stored.Description = v.Description
		stored.StartDate = v.StartDate
		stored.ReleaseDate = v.ReleaseDate
		if v.Archived != nil {
			stored.Archived = *v.Archived
		}
		return fakeCloneVersion(stored), nil
	}
	proj, ok := f.projects[v.ProjectKey]
	if !ok {
		return jira.Version{}, fakeNotFound("project", v.ProjectKey)
	}
	created := jira.Version{
		ID:          f.fakeNextID("ver"),
		ProjectID:   proj.ref.ID,
		Name:        v.Name,
		Description: v.Description,
		StartDate:   v.StartDate,
		ReleaseDate: v.ReleaseDate,
	}
	if v.Archived != nil {
		created.Archived = *v.Archived
	}
	f.versions[created.ID] = &created
	proj.versionIDs = append(proj.versionIDs, created.ID)
	return created, nil
}

// UnresolvedCount reports how many issues carry the version as a fix version
// and are not in the done status category.
func (f *Fake) UnresolvedCount(ctx context.Context, versionID string) (int, error) {
	if err := f.fakeBegin(ctx, "UnresolvedCount"); err != nil {
		return 0, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.versions[versionID]; !ok {
		return 0, fakeNotFound("version", versionID)
	}
	return len(f.fakeUnresolvedOn(versionID)), nil
}

// ReleaseVersion releases a version after doing what ReleaseInput says about
// the issues still open on it.
func (f *Fake) ReleaseVersion(ctx context.Context, id string, in jira.ReleaseInput) (jira.Version, error) {
	if err := f.fakeBegin(ctx, "ReleaseVersion"); err != nil {
		return jira.Version{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	version, ok := f.versions[id]
	if !ok {
		return jira.Version{}, fakeNotFound("version", id)
	}

	open := f.fakeUnresolvedOn(id)
	switch in.Unresolved {
	case jira.MoveUnresolved:
		if in.MoveToVersionID == id {
			return jira.Version{}, fakeInvalid("moveToVersionId", "cannot move the open issues onto the version being released")
		}
		target, found := f.versions[in.MoveToVersionID]
		if !found {
			return jira.Version{}, fakeNotFound("version", in.MoveToVersionID)
		}
		for _, iss := range open {
			fakeSwapFixVersion(iss, id, target)
			iss.Updated = f.now
		}
	case jira.StripUnresolved:
		for _, iss := range open {
			fakeSwapFixVersion(iss, id, nil)
			iss.Updated = f.now
		}
	case jira.ReleaseAnyway:
	}

	version.Released = true
	version.ReleaseDate = in.ReleaseDate
	if version.ReleaseDate.IsZero() {
		version.ReleaseDate = jira.DateOf(f.now)
	}
	out := fakeCloneVersion(version)
	left := len(f.fakeUnresolvedOn(id))
	out.Unresolved = &left
	return out, nil
}

// Boards lists the boards that draw on a project.
func (f *Fake) Boards(ctx context.Context, projectKey string) ([]jira.Board, error) {
	if err := f.fakeBegin(ctx, "Boards"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.caps.Require(jira.CapBoards); err != nil {
		return nil, err
	}
	proj, ok := f.projects[projectKey]
	if !ok {
		return nil, fakeNotFound("project", projectKey)
	}
	if proj.boardID == 0 {
		return nil, nil
	}
	return []jira.Board{*f.boards[proj.boardID]}, nil
}

// BoardConfig reads a board's columns, estimation field and rank field. The
// columns are grouped by status category, which is the only grouping that means
// the same thing on every site.
func (f *Fake) BoardConfig(ctx context.Context, boardID int64) (jira.BoardConfig, error) {
	if err := f.fakeBegin(ctx, "BoardConfig"); err != nil {
		return jira.BoardConfig{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.caps.Require(jira.CapBoards); err != nil {
		return jira.BoardConfig{}, err
	}
	board, ok := f.boards[boardID]
	if !ok {
		return jira.BoardConfig{}, fakeNotFound("board", strconv.FormatInt(boardID, 10))
	}
	cfg := jira.BoardConfig{
		BoardID:  board.ID,
		Name:     board.Name,
		Type:     board.Type,
		Columns:  fakeColumns(),
		FilterID: "filter-" + strconv.FormatInt(board.ID, 10),
	}
	// A Scrum board ranks and estimates; a Kanban board here does neither, and
	// sends no estimation object at all rather than one saying "none". A caller
	// that reads Estimation without checking is what this is here to catch.
	if board.Type == jira.BoardScrum {
		if ref, found := fakeRefByName(f.fields, "Rank"); found {
			cfg.RankFieldID = ref.ID
		}
		est := jira.Estimation{Type: jira.EstimationNone}
		if ref, found := fakeRefByName(f.fields, "Story Points"); found {
			est = jira.Estimation{Type: jira.EstimationField, Field: ref}
		}
		cfg.Estimation = &est
	} else {
		cfg.SubQuery = "resolved >= -14d OR resolved is EMPTY"
	}
	return cfg, nil
}

// Sprints lists a board's sprints, paged by offset with a total, which is the
// Agile API's model rather than the platform API's cursor.
func (f *Fake) Sprints(ctx context.Context, boardID int64, states ...jira.SprintState) (jira.Page[jira.Sprint], error) {
	return jira.Offset(ctx, func(ctx context.Context, startAt int) ([]jira.Sprint, int, bool, error) {
		if err := f.fakeBegin(ctx, "Sprints"); err != nil {
			return nil, 0, false, err
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if err := f.caps.Require(jira.CapBoards); err != nil {
			return nil, 0, false, err
		}
		if _, ok := f.boards[boardID]; !ok {
			return nil, 0, false, fakeNotFound("board", strconv.FormatInt(boardID, 10))
		}
		all := fakeInStates(f.fakeSprintsOn(boardID), states)
		start := min(startAt, len(all))
		end := min(start+f.pageSize, len(all))
		return all[start:end], len(all), end >= len(all), nil
	})
}

// CreateSprint creates a future sprint on a board.
func (f *Fake) CreateSprint(ctx context.Context, in jira.SprintInput) (jira.Sprint, error) {
	if err := f.fakeBegin(ctx, "CreateSprint"); err != nil {
		return jira.Sprint{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.boards[in.BoardID]; !ok {
		return jira.Sprint{}, fakeNotFound("board", strconv.FormatInt(in.BoardID, 10))
	}
	if strings.TrimSpace(in.Name) == "" {
		return jira.Sprint{}, fakeInvalid("name", "a sprint needs a name")
	}
	sp := jira.Sprint{
		ID:      f.fakeNextSprintID(in.BoardID),
		BoardID: in.BoardID,
		Name:    in.Name,
		Goal:    in.Goal,
		State:   jira.SprintFuture,
		Start:   fakeClonePtr(in.Start),
		End:     fakeClonePtr(in.End),
	}
	f.sprints[sp.ID] = &sp
	return fakeCloneSprint(sp), nil
}

// UpdateSprint changes only the fields the patch names. Everything it does not
// name is left exactly as it was, which is the whole reason the port has no raw
// PUT: the endpoint underneath nulls every field it is not sent.
func (f *Fake) UpdateSprint(ctx context.Context, id int64, in jira.SprintPatch) (jira.Sprint, error) {
	if err := f.fakeBegin(ctx, "UpdateSprint"); err != nil {
		return jira.Sprint{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	sp, ok := f.sprints[id]
	if !ok {
		return jira.Sprint{}, fakeNotFound("sprint", strconv.FormatInt(id, 10))
	}
	if sp.State == jira.SprintClosed {
		var bad []jira.FieldError
		if in.Start != nil {
			bad = append(bad, jira.FieldError{Field: "startDate", Message: "a closed sprint accepts only name and goal"})
		}
		if in.End != nil {
			bad = append(bad, jira.FieldError{Field: "endDate", Message: "a closed sprint accepts only name and goal"})
		}
		if len(bad) > 0 {
			return jira.Sprint{}, &jira.ValidationError{Fields: bad}
		}
	}
	if in.Name != nil {
		sp.Name = *in.Name
	}
	if in.Goal != nil {
		sp.Goal = *in.Goal
	}
	if in.Start != nil {
		sp.Start = fakeClonePtr(in.Start)
	}
	if in.End != nil {
		sp.End = fakeClonePtr(in.End)
	}
	return fakeCloneSprint(*sp), nil
}

// StartSprint moves a future sprint to active, refusing unless both dates are
// set, because the API underneath refuses too.
func (f *Fake) StartSprint(ctx context.Context, id int64) (jira.Sprint, error) {
	if err := f.fakeBegin(ctx, "StartSprint"); err != nil {
		return jira.Sprint{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	sp, ok := f.sprints[id]
	if !ok {
		return jira.Sprint{}, fakeNotFound("sprint", strconv.FormatInt(id, 10))
	}
	if sp.State != jira.SprintFuture {
		return jira.Sprint{}, fakeInvalid("state", fmt.Sprintf("only a future sprint can be started, this one is %s", sp.State))
	}
	var bad []jira.FieldError
	if sp.Start == nil {
		bad = append(bad, jira.FieldError{Field: "startDate", Message: "a sprint cannot start without a start date"})
	}
	if sp.End == nil {
		bad = append(bad, jira.FieldError{Field: "endDate", Message: "a sprint cannot start without an end date"})
	}
	if len(bad) > 0 {
		return jira.Sprint{}, &jira.ValidationError{Fields: bad}
	}
	sp.State = jira.SprintActive
	return fakeCloneSprint(*sp), nil
}

// CompleteSprint closes an active sprint.
func (f *Fake) CompleteSprint(ctx context.Context, id int64) (jira.Sprint, error) {
	if err := f.fakeBegin(ctx, "CompleteSprint"); err != nil {
		return jira.Sprint{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	sp, ok := f.sprints[id]
	if !ok {
		return jira.Sprint{}, fakeNotFound("sprint", strconv.FormatInt(id, 10))
	}
	if sp.State != jira.SprintActive {
		return jira.Sprint{}, fakeInvalid("state", fmt.Sprintf("only an active sprint can be completed, this one is %s", sp.State))
	}
	done := f.now
	sp.State = jira.SprintClosed
	sp.Complete = &done
	return fakeCloneSprint(*sp), nil
}

// MoveToSprint moves issues into a sprint, in batches of at most 50 because
// that is what the endpoint underneath accepts.
func (f *Fake) MoveToSprint(ctx context.Context, sprintID int64, keys []string) error {
	if err := f.fakeBegin(ctx, "MoveToSprint"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	sp, ok := f.sprints[sprintID]
	if !ok {
		return fakeNotFound("sprint", strconv.FormatInt(sprintID, 10))
	}
	if err := f.fakeCheckBatch(keys); err != nil {
		return err
	}
	ref, hasField := fakeRefByName(f.fields, "Sprint")
	for _, key := range keys {
		f.sprintOf[key] = sprintID
		iss := f.issues[key]
		if hasField {
			iss.Fields = iss.Fields.With(ref, jira.FieldValue{
				Kind:    jira.KindOptions,
				Options: []jira.Option{{ID: strconv.FormatInt(sp.ID, 10), Label: sp.Name}},
			})
		}
		iss.Updated = f.now
	}
	return nil
}

// MoveToBacklog moves issues out of whatever sprint they are in.
func (f *Fake) MoveToBacklog(ctx context.Context, keys []string) error {
	if err := f.fakeBegin(ctx, "MoveToBacklog"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fakeCheckBatch(keys); err != nil {
		return err
	}
	ref, hasField := fakeRefByName(f.fields, "Sprint")
	for _, key := range keys {
		delete(f.sprintOf, key)
		iss := f.issues[key]
		if hasField {
			iss.Fields = fakeRetainFields(iss.Fields, map[string]bool{ref.ID: true})
		}
		iss.Updated = f.now
	}
	return nil
}

// CreateMeta reports what a project and issue type require to create an issue.
func (f *Fake) CreateMeta(ctx context.Context, projectKey, issueTypeID string) (jira.Schema, error) {
	if err := f.fakeBegin(ctx, "CreateMeta"); err != nil {
		return jira.Schema{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	proj, ok := f.projects[projectKey]
	if !ok {
		return jira.Schema{}, fakeNotFound("project", projectKey)
	}
	typ := fakeIssueTypeByID(issueTypeID)
	if typ == nil {
		return jira.Schema{}, fakeNotFound("issuetype", issueTypeID)
	}
	schema := jira.Schema{Project: proj.ref, IssueType: *typ}
	add := func(id string, required bool, allowed []jira.Option) {
		if meta, found := f.fakeFieldMeta(id, required, allowed); found {
			schema.Fields = append(schema.Fields, meta)
		}
	}
	add("summary", true, nil)
	add("issuetype", true, nil)
	add("project", true, nil)
	add("parent", typ.Subtask, nil)
	add("description", false, nil)
	add("assignee", false, nil)
	add("labels", false, nil)
	add("priority", false, fakePriorityOptions())
	add("duedate", false, nil)
	if ref, found := fakeRefByName(f.fields, "Story Points"); found {
		add(ref.ID, false, nil)
	}
	return schema, nil
}

// BulkMove submits an asynchronous cross-project move and returns the task to
// poll for it.
func (f *Fake) BulkMove(ctx context.Context, in jira.MoveRequest) (jira.TaskRef, error) {
	if err := f.fakeBegin(ctx, "BulkMove"); err != nil {
		return jira.TaskRef{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.caps.Require(jira.CapBulkMove); err != nil {
		return jira.TaskRef{}, err
	}
	switch {
	case len(in.Keys) == 0:
		return jira.TaskRef{}, fakeInvalid("issues", "a bulk move needs at least one issue")
	case len(in.Keys) > 1000:
		return jira.TaskRef{}, fakeInvalid("issues", "a bulk move takes at most 1000 issues")
	}
	for _, key := range in.Keys {
		if _, ok := f.issues[key]; !ok {
			return jira.TaskRef{}, fakeNotFound("issue", key)
		}
	}
	if _, ok := f.projects[in.TargetProjectKey]; !ok {
		return jira.TaskRef{}, fakeNotFound("project", in.TargetProjectKey)
	}
	if fakeIssueTypeByID(in.TargetIssueTypeID) == nil {
		return jira.TaskRef{}, fakeNotFound("issuetype", in.TargetIssueTypeID)
	}

	id := f.fakeNextID("task")
	ref := jira.TaskRef{ID: id, URL: fakeBaseURL + "/rest/api/3/task/" + id}
	f.tasks[id] = &fakeTask{ref: ref, req: in, fails: f.failTask}
	f.failTask = false
	return ref, nil
}

// Task reports on a long-running task, walking it one step per poll: enqueued,
// then running at half way, then complete.
func (f *Fake) Task(ctx context.Context, ref jira.TaskRef) (jira.TaskStatus, error) {
	if err := f.fakeBegin(ctx, "Task"); err != nil {
		return jira.TaskStatus{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	task, ok := f.tasks[ref.ID]
	if !ok {
		return jira.TaskStatus{}, fakeNotFound("task", ref.ID)
	}
	status := jira.TaskStatus{Ref: task.ref}
	switch task.polls {
	case 0:
		status.State, status.Progress, status.Message = jira.TaskEnqueued, 0, "queued behind other work"
	case 1:
		status.State, status.Progress, status.Message = jira.TaskRunning, 50, "moving issues"
	default:
		if task.fails {
			status.State, status.Progress = jira.TaskFailed, 50
			status.Message = "the target project rejected the move"
			status.Failed = slices.Clone(task.req.Keys)
		} else {
			status.State, status.Progress, status.Message = jira.TaskComplete, 100, "moved"
			if task.polls == 2 {
				f.fakeApplyMove(task)
			}
		}
	}
	task.polls++
	return status, nil
}

// Plans lists Advanced Roadmaps plans, one per project.
func (f *Fake) Plans(ctx context.Context) ([]jira.Plan, error) {
	if err := f.fakeBegin(ctx, "Plans"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.caps.Require(jira.CapPlans); err != nil {
		return nil, err
	}
	out := make([]jira.Plan, 0, len(f.projectKeys))
	for i, key := range f.projectKeys {
		out = append(out, jira.Plan{
			ID:      "plan-" + strconv.Itoa(i+1),
			Name:    key + " delivery",
			Status:  "Active",
			Sources: []jira.PlanSource{{Type: jira.PlanSourceProject, Value: key}},
		})
	}
	return out, nil
}

func (f *Fake) fakeNextKey(projectKey string) string {
	highest := 0
	prefix := projectKey + "-"
	for _, key := range f.issueKeys {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		if n, err := strconv.Atoi(key[len(prefix):]); err == nil && n > highest {
			highest = n
		}
	}
	return prefix + strconv.Itoa(highest+1)
}

func (f *Fake) fakeNextSprintID(boardID int64) int64 {
	id := boardID * 100
	for {
		if _, taken := f.sprints[id]; !taken {
			return id
		}
		id++
	}
}

func (f *Fake) fakeSprintsOn(boardID int64) []jira.Sprint {
	out := make([]jira.Sprint, 0, len(f.sprints))
	for _, sp := range f.sprints {
		if sp.BoardID == boardID {
			out = append(out, fakeCloneSprint(*sp))
		}
	}
	slices.SortFunc(out, func(a, b jira.Sprint) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

func (f *Fake) fakeCheckBatch(keys []string) error {
	if len(keys) > 50 {
		return fakeInvalid("issues", "the backlog and sprint endpoints take at most 50 issues per call")
	}
	for _, key := range keys {
		if _, ok := f.issues[key]; !ok {
			return fakeNotFound("issue", key)
		}
	}
	return nil
}

func (f *Fake) fakeUser(accountID string) *jira.User {
	if accountID == "" {
		return nil
	}
	if accountID == f.me.AccountID {
		u := f.me
		return &u
	}
	for i := range fakeUsers {
		if fakeUsers[i].AccountID == accountID {
			u := fakeUsers[i]
			return &u
		}
	}
	return &jira.User{AccountID: accountID, DisplayName: accountID, Active: true, TimeZone: time.UTC}
}

func (f *Fake) fakeAttachment(id string) (jira.Attachment, bool) {
	key, ok := f.attachOwner[id]
	if !ok {
		return jira.Attachment{}, false
	}
	list := f.attachments[key]
	for i := range list {
		if list[i].ID == id {
			return list[i], true
		}
	}
	return jira.Attachment{}, false
}

func (f *Fake) fakeUnresolvedOn(versionID string) []*jira.Issue {
	out := make([]*jira.Issue, 0, len(f.issueKeys))
	for _, key := range f.issueKeys {
		iss := f.issues[key]
		if iss.Status.Category == jira.CategoryDone {
			continue
		}
		if slices.ContainsFunc(iss.FixVersions, func(v jira.Version) bool { return v.ID == versionID }) {
			out = append(out, iss)
		}
	}
	return out
}

func (f *Fake) fakeFieldMeta(id string, required bool, allowed []jira.Option) (jira.FieldMeta, bool) {
	for i := range f.fields {
		if f.fields[i].ID != id {
			continue
		}
		return jira.FieldMeta{
			Field:         f.fields[i].Ref(),
			Name:          f.fields[i].Name,
			Required:      required,
			Operations:    []string{"set"},
			AllowedValues: allowed,
		}, true
	}
	return jira.FieldMeta{}, false
}

// fakeValidatePatch answers the whole patch before any of it is applied. Jira
// validates a write as a unit, so a fake that half-applies a rejected patch
// lets a caller ship code that leaves an issue in a state the real API cannot
// produce.
func (f *Fake) fakeValidatePatch(in *jira.IssuePatch) error {
	if in.PriorityID != nil && fakePriorityByID(*in.PriorityID) == nil {
		return fakeInvalid("priority", fmt.Sprintf("no priority %q on this site", *in.PriorityID))
	}
	for _, id := range in.Fields.IDs() {
		if err := f.fakeKnownField(id); err != nil {
			return err
		}
	}
	for _, ref := range in.Clear {
		if ref.ID == "summary" {
			return fakeInvalid("summary", "summary cannot be cleared")
		}
		if _, system := fakeClearableSystemFields[ref.ID]; system {
			continue
		}
		if err := f.fakeKnownField(ref.ID); err != nil {
			return err
		}
	}
	return nil
}

// fakeKnownField refuses a field ID the site's catalogue does not carry. The
// whole point of the fake's invented field IDs is that a hardcoded
// customfield_10016 fails here rather than in front of a user.
func (f *Fake) fakeKnownField(id string) error {
	for i := range f.fields {
		if f.fields[i].ID == id {
			return nil
		}
	}
	return fakeInvalid(id, fmt.Sprintf("field %q does not exist on this site; resolve it by name through Fields()", id))
}

// fakeClearableSystemFields are the built-in fields the fake lets a patch null.
var fakeClearableSystemFields = map[string]struct{}{
	"assignee": {}, "priority": {}, "resolution": {}, "labels": {},
	"duedate": {}, "description": {}, "fixVersions": {},
}

func (f *Fake) fakeApplyPatch(iss *jira.Issue, in *jira.IssuePatch) error {
	if err := f.fakeValidatePatch(in); err != nil {
		return err
	}
	if in.Summary != nil {
		iss.Summary = *in.Summary
	}
	if in.Description != nil {
		iss.Description = *in.Description
	}
	if in.Assignee != nil {
		iss.Assignee = f.fakeUser(*in.Assignee)
	}
	if in.Labels != nil {
		iss.Labels = slices.Clone(*in.Labels)
	}
	if in.PriorityID != nil {
		iss.Priority = fakePriorityByID(*in.PriorityID)
	}
	if in.Due != nil {
		iss.Due = *in.Due
	}
	for _, id := range in.Fields.IDs() {
		if v, ok := in.Fields.ByID(id); ok {
			iss.Fields = iss.Fields.With(jira.FieldRef{ID: id}, v)
		}
	}
	drop := make(map[string]bool, len(in.Clear))
	for _, ref := range in.Clear {
		switch ref.ID {
		case "assignee":
			iss.Assignee = nil
		case "priority":
			iss.Priority = nil
		case "resolution":
			iss.Resolution = nil
		case "labels":
			iss.Labels = nil
		case "duedate":
			iss.Due = jira.Date{}
		case "description":
			iss.Description = adf.Doc{}
		case "fixVersions":
			iss.FixVersions = nil
		default:
			drop[ref.ID] = true
		}
	}
	if len(drop) > 0 {
		iss.Fields = fakeRetainFields(iss.Fields, drop)
	}
	iss.Updated = f.now
	return nil
}

func (f *Fake) fakeApplyMove(task *fakeTask) {
	target, ok := f.projects[task.req.TargetProjectKey]
	if !ok {
		return
	}
	typ := fakeIssueTypeByID(task.req.TargetIssueTypeID)
	for _, key := range task.req.Keys {
		iss, found := f.issues[key]
		if !found {
			continue
		}
		newKey := f.fakeNextKey(target.ref.Key)
		iss.Key = newKey
		iss.Project = target.ref
		if typ != nil {
			iss.Type = *typ
		}
		for _, m := range task.req.StatusMap {
			if iss.Status.ID == m.FromStatusID {
				if s := fakeStatusByID(m.ToStatusID); s != nil {
					iss.Status = *s
				}
			}
		}
		iss.Updated = f.now
		delete(f.issues, key)
		f.issues[newKey] = iss
		if i := slices.Index(f.issueKeys, key); i >= 0 {
			f.issueKeys[i] = newKey
		}
		fakeRekey(f.comments, key, newKey)
		fakeRekey(f.attachments, key, newKey)
		moved := f.attachments[newKey]
		for i := range moved {
			f.attachOwner[moved[i].ID] = newKey
		}
		if sprintID, inSprint := f.sprintOf[key]; inSprint {
			delete(f.sprintOf, key)
			f.sprintOf[newKey] = sprintID
		}
	}
}

func fakeRekey[T any](m map[string][]T, from, to string) {
	if v, ok := m[from]; ok {
		m[to] = v
		delete(m, from)
	}
}

func fakeSwapFixVersion(iss *jira.Issue, from string, to *jira.Version) {
	out := make([]jira.Version, 0, len(iss.FixVersions))
	for i := range iss.FixVersions {
		if iss.FixVersions[i].ID != from {
			out = append(out, iss.FixVersions[i])
			continue
		}
		if to != nil {
			out = append(out, fakeCloneVersion(to))
		}
	}
	iss.FixVersions = out
}

func fakeIssueTypeByID(id string) *jira.IssueType {
	for i := range fakeIssueTypes {
		if fakeIssueTypes[i].ID == id {
			t := fakeIssueTypes[i]
			return &t
		}
	}
	return nil
}

func fakeStatusByID(id string) *jira.Status {
	for i := range fakeStatuses {
		if fakeStatuses[i].ID == id {
			s := fakeStatuses[i]
			return &s
		}
	}
	return nil
}

func fakePriorityByID(id string) *jira.Priority {
	for i := range fakePriorities {
		if fakePriorities[i].ID == id {
			p := fakePriorities[i]
			return &p
		}
	}
	return nil
}

func fakePriorityOptions() []jira.Option {
	out := make([]jira.Option, 0, len(fakePriorities))
	for _, p := range fakePriorities {
		out = append(out, jira.Option{ID: p.ID, Label: p.Name})
	}
	return out
}

func fakeColumns() []jira.Column {
	categories := []jira.StatusCategory{jira.CategoryToDo, jira.CategoryInProgress, jira.CategoryDone}
	out := make([]jira.Column, 0, len(categories))
	for _, cat := range categories {
		col := jira.Column{Name: cat.String()}
		for _, s := range fakeStatuses {
			if s.Category == cat {
				col.StatusIDs = append(col.StatusIDs, s.ID)
			}
		}
		out = append(out, col)
	}
	return out
}

func fakeTransitionsFor(from jira.Status) []jira.Transition {
	out := make([]jira.Transition, 0, len(fakeStatuses))
	for _, to := range fakeStatuses {
		if to.ID == from.ID {
			continue
		}
		tr := jira.Transition{ID: "tr-" + to.ID, Name: "Move to " + to.Name, To: to}
		if to.Category == jira.CategoryDone {
			tr.HasScreen = true
			tr.Fields = []jira.FieldMeta{fakeResolutionMeta()}
		}
		out = append(out, tr)
	}
	return out
}

func fakeResolutionMeta() jira.FieldMeta {
	allowed := make([]jira.Option, 0, len(fakeResolutions))
	for _, r := range fakeResolutions {
		allowed = append(allowed, jira.Option{ID: r.ID, Label: r.Name})
	}
	return jira.FieldMeta{
		Field:         jira.FieldRef{ID: "resolution", Name: "Resolution", Schema: jira.FieldSchema{Type: "resolution", System: "resolution"}},
		Name:          "Resolution",
		Required:      true,
		Operations:    []string{"set"},
		AllowedValues: allowed,
	}
}

// fakeResolutionFrom reads the resolution off a transition screen's values.
func fakeResolutionFrom(fields jira.FieldSet) (*jira.Resolution, error) {
	missing := fakeInvalid("resolution", "this transition has a screen with a required resolution")
	v, ok := fields.ByID("resolution")
	if !ok {
		return nil, missing
	}
	var want string
	switch {
	case len(v.Options) > 0:
		want = v.Options[0].ID
		if want == "" {
			want = v.Options[0].Label
		}
	case v.Text != "":
		want = v.Text
	default:
		return nil, missing
	}
	for i := range fakeResolutions {
		if fakeResolutions[i].ID == want || strings.EqualFold(fakeResolutions[i].Name, want) {
			r := fakeResolutions[i]
			return &r, nil
		}
	}
	return nil, fakeInvalid("resolution", fmt.Sprintf("no resolution %q on this site", want))
}

func fakeReadAll(file *jira.FileRef) (int64, error) {
	if file.Open == nil {
		return 0, fakeInvalid("file", fmt.Sprintf("%s has no way to open it", file.Name))
	}
	rc, err := file.Open()
	if err != nil {
		return 0, &jira.TransportError{Op: "open " + file.Name, Err: err}
	}
	n, err := io.Copy(io.Discard, rc)
	closeErr := rc.Close()
	if err != nil {
		return 0, &jira.TransportError{Op: "read " + file.Name, Err: err}
	}
	if closeErr != nil {
		return 0, &jira.TransportError{Op: "close " + file.Name, Err: closeErr}
	}
	return n, nil
}

func fakeMimeType(name string) string {
	switch strings.ToLower(name[strings.LastIndex(name, ".")+1:]) {
	case "png":
		return "image/png"
	case "jpg", "jpeg":
		return "image/jpeg"
	case "gif":
		return "image/gif"
	case "pdf":
		return "application/pdf"
	case "json":
		return "application/json"
	case "txt", "log", "md":
		return "text/plain"
	default:
		return "application/octet-stream"
	}
}

// fakeAttachmentBytes derives an attachment's content from its ID, so that a
// download is byte-identical on every run without storing the bytes.
func fakeAttachmentBytes(att *jira.Attachment) []byte {
	if att.Size <= 0 {
		return nil
	}
	seed := int(fakeHash32(att.ID) % 26)
	out := make([]byte, att.Size)
	for i := range out {
		out[i] = byte('A' + (seed+i*7)%26)
	}
	return out
}

// fakeApplyFieldMask strips an issue down to the fields the query named, both
// the custom-field set and the struct fields. A fake that hands back a whole
// issue whatever was asked for lets a list view be written against data the
// real endpoint will not send.
//
// The mask both blanks the fields and records itself on the issue, so the fake
// cannot mask one set and claim another.
func fakeApplyFieldMask(iss *jira.Issue, mask jira.FieldMask) {
	iss.Requested = mask
	if mask.Wide() {
		return
	}
	// Identity is not a field and always comes back.
	if !mask.Has("summary") {
		iss.Summary = ""
	}
	if !mask.Has("description") {
		iss.Description = adf.Doc{}
	}
	if !mask.Has("status") {
		iss.Status = jira.Status{}
	}
	if !mask.Has("issuetype") {
		iss.Type = jira.IssueType{}
	}
	if !mask.Has("priority") {
		iss.Priority = nil
	}
	if !mask.Has("resolution") {
		iss.Resolution, iss.Resolved = nil, nil
	}
	if !mask.Has("assignee") {
		iss.Assignee = nil
	}
	if !mask.Has("reporter") {
		iss.Reporter = nil
	}
	if !mask.Has("labels") {
		iss.Labels = nil
	}
	if !mask.Has("components") {
		iss.Components = nil
	}
	if !mask.Has("fixVersions") {
		iss.FixVersions = nil
	}
	if !mask.Has("parent") {
		iss.Parent = nil
	}
	if !mask.Has("subtasks") {
		iss.Subtasks = nil
	}
	if !mask.Has("issuelinks") {
		iss.Links = nil
	}
	if !mask.Has("duedate") {
		iss.Due = jira.Date{}
	}
	if !mask.Has("created") {
		iss.Created = time.Time{}
	}
	if !mask.Has("updated") {
		iss.Updated = time.Time{}
	}
	if !mask.Has("timetracking") {
		iss.TimeTracking = nil
	}
	drop := make(map[string]bool)
	for _, id := range iss.Fields.IDs() {
		if !mask.Has(id) {
			drop[id] = true
		}
	}
	if len(drop) > 0 {
		iss.Fields = fakeRetainFields(iss.Fields, drop)
	}
}

func fakeEncodeCursor(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte("offset:" + strconv.Itoa(offset)))
}

func fakeDecodeCursor(token string) (int, error) {
	if token == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return 0, fakeInvalid("nextPageToken", "the page token was not one this site handed out")
	}
	n, err := strconv.Atoi(strings.TrimPrefix(string(raw), "offset:"))
	if err != nil || n < 0 {
		return 0, fakeInvalid("nextPageToken", "the page token was not one this site handed out")
	}
	return n, nil
}

type fakeClause struct {
	field  string
	op     string
	values []string
}

// fakeGroup is one AND term of a query: a single clause, or several joined by
// OR inside brackets, which is the only place the fake reads an OR.
type fakeGroup []fakeClause

type fakeQueryPlan struct {
	groups []fakeGroup
	order  string
	desc   bool
}

var fakeJQLFields = []string{
	"project", "key", "issuekey", "status", "issuetype", "type",
	"priority", "assignee", "reporter", "labels",
}

var fakeJQLOrders = []string{"key", "created", "updated", "summary", "status", "priority", "assignee", "project"}

func fakeJQLError(format string, args ...any) error {
	return fakeInvalid("jql", fmt.Sprintf(format, args...))
}

// fakeParseJQL reads the subset of JQL the fake supports. Anything outside it
// is an error rather than a match-everything, because a query that quietly
// stopped filtering is a test that quietly stopped testing.
func fakeParseJQL(q string) (fakeQueryPlan, error) {
	var plan fakeQueryPlan
	body := strings.TrimSpace(q)
	if i := fakeFindPhrase(body, "order by"); i >= 0 {
		tail := strings.TrimSpace(body[i+len("order by"):])
		body = strings.TrimSpace(body[:i])
		toks, err := fakeTokenize(tail)
		if err != nil {
			return plan, err
		}
		if len(toks) == 0 || len(toks) > 2 {
			return plan, fakeJQLError("ORDER BY takes one field and an optional direction, got %q", tail)
		}
		plan.order = strings.ToLower(toks[0])
		if len(toks) == 2 {
			switch strings.ToUpper(toks[1]) {
			case "ASC":
			case "DESC":
				plan.desc = true
			default:
				return plan, fakeJQLError("%q is not a sort direction", toks[1])
			}
		}
		if !slices.Contains(fakeJQLOrders, plan.order) {
			return plan, fakeJQLError("the fake cannot order by %q; it knows %s", plan.order, strings.Join(fakeJQLOrders, ", "))
		}
	}
	if body == "" {
		return plan, nil
	}
	for _, part := range fakeSplitWord(body, "and") {
		g, err := fakeParseGroup(part)
		if err != nil {
			return plan, err
		}
		plan.groups = append(plan.groups, g)
	}
	return plan, nil
}

// fakeParseGroup reads one AND term. Brackets round the whole of it mean its
// clauses are joined by OR, which is the shape a filter writes for "nobody, or
// this person". An OR anywhere else is outside the subset.
func fakeParseGroup(s string) (fakeGroup, error) {
	body, bracketed := fakeUnbracket(s)
	if !bracketed {
		c, err := fakeParseClause(s)
		if err != nil {
			return nil, err
		}
		return fakeGroup{c}, nil
	}
	var out fakeGroup
	for _, part := range fakeSplitWord(body, "or") {
		c, err := fakeParseClause(part)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// fakeUnbracket strips the pair of brackets that wraps the whole of s. A
// leading and a trailing bracket that are not each other's — "(a) OR (b)" —
// leave s alone, so it is refused as a clause rather than read as a group.
func fakeUnbracket(s string) (string, bool) {
	body := strings.TrimSpace(s)
	if len(body) < 2 || body[0] != '(' || body[len(body)-1] != ')' {
		return body, false
	}
	inner := body[1 : len(body)-1]
	if !fakeBalanced(inner) {
		return body, false
	}
	return inner, true
}

// fakeBalanced reports whether every bracket and quote in s is closed inside it.
func fakeBalanced(s string) bool {
	quote, depth := byte(0), 0
	for i := range len(s) {
		switch {
		case quote != 0:
			if s[i] == quote {
				quote = 0
			}
		case s[i] == '"' || s[i] == '\'':
			quote = s[i]
		case s[i] == '(':
			depth++
		case s[i] == ')':
			depth--
			if depth < 0 {
				return false
			}
		}
	}
	return depth == 0 && quote == 0
}

func fakeParseClause(s string) (fakeClause, error) {
	toks, err := fakeTokenize(s)
	if err != nil {
		return fakeClause{}, err
	}
	unsupported := func() error {
		return fakeJQLError("cannot parse %q; the fake understands <field> = <value>, <field> IN (<value>, ...), IS EMPTY and IS NOT EMPTY, joined by AND or by OR inside brackets", strings.TrimSpace(s))
	}
	if len(toks) == 0 {
		return fakeClause{}, unsupported()
	}
	field := strings.ToLower(toks[0])
	switch {
	case len(toks) == 3 && toks[1] == "=":
		return fakeNewClause(field, "=", []string{toks[2]}, unsupported)
	case len(toks) == 5 && toks[1] == "=" && toks[3] == "(" && toks[4] == ")":
		return fakeNewClause(field, "=", []string{toks[2] + "()"}, unsupported)
	case len(toks) >= 5 && strings.EqualFold(toks[1], "in") && toks[2] == "(" && toks[len(toks)-1] == ")":
		values, ok := fakeListValues(toks[3 : len(toks)-1])
		if !ok {
			return fakeClause{}, unsupported()
		}
		return fakeNewClause(field, "in", values, unsupported)
	case len(toks) == 3 && strings.EqualFold(toks[1], "is") && strings.EqualFold(toks[2], "empty"):
		return fakeNewClause(field, "is empty", nil, unsupported)
	case len(toks) == 4 && strings.EqualFold(toks[1], "is") && strings.EqualFold(toks[2], "not") && strings.EqualFold(toks[3], "empty"):
		return fakeNewClause(field, "is not empty", nil, unsupported)
	default:
		return fakeClause{}, unsupported()
	}
}

// fakeListValues reads the inside of an IN list: values separated by single
// commas, and nothing else — no nesting, no function call, no trailing comma.
func fakeListValues(toks []string) ([]string, bool) {
	if len(toks)%2 == 0 {
		return nil, false
	}
	out := make([]string, 0, (len(toks)+1)/2)
	for i, tok := range toks {
		if i%2 == 1 {
			if tok != "," {
				return nil, false
			}
			continue
		}
		if tok == "," || tok == "(" || tok == ")" {
			return nil, false
		}
		out = append(out, tok)
	}
	return out, true
}

func fakeNewClause(field, op string, values []string, unsupported func() error) (fakeClause, error) {
	if !slices.Contains(fakeJQLFields, field) {
		return fakeClause{}, fakeJQLError("the fake cannot filter on %q; it knows %s", field, strings.Join(fakeJQLFields, ", "))
	}
	if (op == "=" || op == "in") && (len(values) == 0 || slices.Contains(values, "")) {
		return fakeClause{}, unsupported()
	}
	return fakeClause{field: field, op: op, values: values}, nil
}

// fakeTokenize splits one clause into words, quoted strings and operators.
func fakeTokenize(s string) ([]string, error) {
	var toks []string
	for i := 0; i < len(s); {
		c := s[i]
		switch c {
		case ' ', '\t', '\n', '\r':
			i++
		case '"', '\'':
			quote := c
			i++
			start := i
			for i < len(s) && s[i] != quote {
				i++
			}
			if i >= len(s) {
				return nil, fakeJQLError("unterminated quote in %q", s)
			}
			toks = append(toks, s[start:i])
			i++
		case '=', '!', '~', '<', '>':
			start := i
			i++
			if i < len(s) && s[i] == '=' {
				i++
			}
			toks = append(toks, s[start:i])
		case '(', ')', ',':
			toks = append(toks, string(c))
			i++
		default:
			start := i
			for i < len(s) && !fakeIsDelim(s[i]) {
				i++
			}
			toks = append(toks, s[start:i])
		}
	}
	return toks, nil
}

func fakeIsDelim(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', '=', '!', '~', '<', '>', '(', ')', ',', '"', '\'':
		return true
	default:
		return false
	}
}

// fakeFindPhrase returns the index of the last case-insensitive occurrence of
// phrase that is outside quotes and stands on its own.
func fakeFindPhrase(s, phrase string) int {
	found := -1
	quote := byte(0)
	for i := 0; i+len(phrase) <= len(s); i++ {
		switch {
		case quote != 0:
			if s[i] == quote {
				quote = 0
			}
			continue
		case s[i] == '"' || s[i] == '\'':
			quote = s[i]
			continue
		}
		if strings.EqualFold(s[i:i+len(phrase)], phrase) && fakeWordBoundary(s, i, len(phrase)) {
			found = i
		}
	}
	return found
}

func fakeWordBoundary(s string, at, length int) bool {
	if at > 0 && !fakeIsDelim(s[at-1]) {
		return false
	}
	end := at + length
	return end >= len(s) || fakeIsDelim(s[end])
}

func fakeSplitWord(s, word string) []string {
	var out []string
	start := 0
	for i := 0; i+len(word) <= len(s); i++ {
		if !strings.EqualFold(s[i:i+len(word)], word) || !fakeWordBoundary(s, i, len(word)) {
			continue
		}
		if fakeInQuotes(s, i) {
			continue
		}
		out = append(out, s[start:i])
		start = i + len(word)
		i = start - 1
	}
	return append(out, s[start:])
}

func fakeInQuotes(s string, at int) bool {
	quote := byte(0)
	for i := range at {
		switch {
		case quote != 0 && s[i] == quote:
			quote = 0
		case quote == 0 && (s[i] == '"' || s[i] == '\''):
			quote = s[i]
		}
	}
	return quote != 0
}

// fakeMatchIssues applies a parsed query in insertion order, then sorts if the
// query asked for an order.
func (f *Fake) fakeMatchIssues(plan fakeQueryPlan) []*jira.Issue {
	out := make([]*jira.Issue, 0, len(f.issueKeys))
	for _, key := range f.issueKeys {
		iss := f.issues[key]
		keep := true
		for _, g := range plan.groups {
			if !f.fakeMatchGroup(iss, g) {
				keep = false
				break
			}
		}
		if keep {
			out = append(out, iss)
		}
	}
	if plan.order != "" {
		slices.SortStableFunc(out, func(a, b *jira.Issue) int {
			if plan.desc {
				return fakeCompareIssues(b, a, plan.order)
			}
			return fakeCompareIssues(a, b, plan.order)
		})
	}
	return out
}

// fakeMatchGroup is an OR: one clause answering is the whole group answering.
func (f *Fake) fakeMatchGroup(iss *jira.Issue, g fakeGroup) bool {
	for _, c := range g {
		if f.fakeMatchClause(iss, c) {
			return true
		}
	}
	return false
}

func (f *Fake) fakeMatchClause(iss *jira.Issue, c fakeClause) bool {
	values := fakeClauseValues(iss, c.field)
	switch c.op {
	case "is empty":
		return len(values) == 0
	case "is not empty":
		return len(values) > 0
	default:
		for _, want := range c.values {
			if fakeAccountField(c.field) && strings.EqualFold(want, "currentUser()") {
				want = f.me.AccountID
			}
			if slices.ContainsFunc(values, func(v string) bool { return strings.EqualFold(v, want) }) {
				return true
			}
		}
		return false
	}
}

func fakeAccountField(field string) bool { return field == "assignee" || field == "reporter" }

func fakeClauseValues(iss *jira.Issue, field string) []string {
	switch field {
	case "project":
		return []string{iss.Project.Key, iss.Project.ID}
	case "key", "issuekey":
		return []string{iss.Key, iss.ID}
	case "status":
		return []string{iss.Status.Name, iss.Status.ID}
	case "issuetype", "type":
		return []string{iss.Type.Name, iss.Type.ID}
	case "priority":
		if iss.Priority == nil {
			return nil
		}
		return []string{iss.Priority.Name, iss.Priority.ID}
	case "assignee":
		if iss.Assignee == nil {
			return nil
		}
		return []string{iss.Assignee.AccountID, iss.Assignee.DisplayName}
	case "reporter":
		if iss.Reporter == nil {
			return nil
		}
		return []string{iss.Reporter.AccountID, iss.Reporter.DisplayName}
	case "labels":
		return iss.Labels
	default:
		return nil
	}
}

func fakeCompareIssues(a, b *jira.Issue, order string) int {
	switch order {
	case "created":
		return a.Created.Compare(b.Created)
	case "updated":
		return a.Updated.Compare(b.Updated)
	case "summary":
		return strings.Compare(a.Summary, b.Summary)
	case "status":
		return strings.Compare(a.Status.Name, b.Status.Name)
	case "project":
		return strings.Compare(a.Project.Key, b.Project.Key)
	case "priority":
		return strings.Compare(fakePriorityName(a), fakePriorityName(b))
	case "assignee":
		return strings.Compare(fakeAssigneeName(a), fakeAssigneeName(b))
	default:
		return fakeCompareKeys(a.Key, b.Key)
	}
}

func fakePriorityName(iss *jira.Issue) string {
	if iss.Priority == nil {
		return ""
	}
	return iss.Priority.Name
}

func fakeAssigneeName(iss *jira.Issue) string {
	if iss.Assignee == nil {
		return ""
	}
	return iss.Assignee.DisplayName
}

// fakeCompareKeys orders issue keys the way a human reads them, so that PROJ-2
// sorts before PROJ-10.
func fakeCompareKeys(a, b string) int {
	ap, an := fakeSplitKey(a)
	bp, bn := fakeSplitKey(b)
	if c := strings.Compare(ap, bp); c != 0 {
		return c
	}
	if an != bn {
		return cmp.Compare(an, bn)
	}
	return strings.Compare(a, b)
}

func fakeSplitKey(key string) (prefix string, number int) {
	i := strings.LastIndex(key, "-")
	if i < 0 {
		return key, -1
	}
	n, err := strconv.Atoi(key[i+1:])
	if err != nil {
		return key, -1
	}
	return key[:i], n
}

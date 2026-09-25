package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
)

// IssueEditor is what a checked write runs on.
type IssueEditor interface {
	jira.IssueReader
	jira.IssueWriter
}

// ReadIssue reads through the issue endpoint because search's index trails a
// write by seconds, and the read after a save has to see the save.
func (s *Search) ReadIssue(ctx context.Context, reader jira.IssueReader, key string, p Projection) (jira.Issue, FieldLabels, error) {
	if reader == nil {
		return jira.Issue{}, FieldLabels{}, errNoClient
	}
	resolved, err := s.Resolve(ctx, p)
	if err != nil {
		return jira.Issue{}, FieldLabels{}, err
	}
	if len(resolved.IDs) == 0 {
		return jira.Issue{}, FieldLabels{}, errors.New("app: the " + projectionName(p) + " field set resolved to no field on this site, so there is nothing to ask for")
	}
	iss, err := reader.IssueFields(ctx, key, resolved.IDs)
	if err != nil {
		return jira.Issue{}, FieldLabels{}, err
	}
	return iss, resolved.Labels, nil
}

// EditBase is what a pending edit was made against: Jira Cloud answers a plain
// PUT with 204 whatever changed in between, so staleness is checked here.
type EditBase struct {
	Updated time.Time         `json:"updated,omitzero"`
	Fields  map[string]string `json:"fields,omitempty"`
}

// BaseOf fingerprints the named fields of an issue.
func BaseOf(iss jira.Issue, ids ...string) EditBase {
	out := EditBase{Updated: iss.Updated, Fields: make(map[string]string, len(ids))}
	for _, id := range ids {
		out.Fields[id] = Fingerprint(iss, id)
	}
	return out
}

// Moved names, sorted, the fields whose value on cur differs from the base.
func (b EditBase) Moved(cur jira.Issue) []string {
	var out []string
	for id, was := range b.Fields {
		if was != "" && Fingerprint(cur, id) != was {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}

// Fingerprint is a short digest of one field's value on an issue.
func Fingerprint(iss jira.Issue, id string) string {
	var raw []byte
	switch id {
	case "summary":
		raw = []byte(iss.Summary)
	case "description":
		raw, _ = adf.Marshal(iss.Description)
	case "duedate":
		raw = []byte(iss.Due.String())
	case "priority":
		if iss.Priority != nil {
			raw = []byte(iss.Priority.ID)
		}
	case "assignee":
		if iss.Assignee != nil {
			raw = []byte(iss.Assignee.AccountID)
		}
	case "status":
		raw = []byte(iss.Status.ID)
	case "labels":
		labels := slices.Clone(iss.Labels)
		slices.Sort(labels)
		raw = []byte(strings.Join(labels, "\x00"))
	default:
		if v, ok := iss.Fields.ByID(id); ok {
			raw, _ = json.Marshal(v)
		}
	}
	sum := sha256.Sum256(append([]byte(id+"\x00"), raw...))
	return hex.EncodeToString(sum[:12])
}

// CheckBase re-reads the fields a base covers and returns a *jira.ConflictError
// when any moved. An empty base asks the site nothing.
func CheckBase(ctx context.Context, reader jira.IssueReader, key string, base EditBase) error {
	if len(base.Fields) == 0 {
		return nil
	}
	ids := make([]string, 0, len(base.Fields)+1)
	for id := range base.Fields {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	cur, err := reader.IssueFields(ctx, key, append(ids, "updated"))
	if err != nil {
		return err
	}
	if moved := base.Moved(cur); len(moved) > 0 {
		return &jira.ConflictError{Resource: key, Detail: strings.Join(moved, ", ") + " changed on the site"}
	}
	return nil
}

// SaveIssue checks then writes: two requests, so a change landing between them
// is still overwritten.
func SaveIssue(ctx context.Context, c IssueEditor, key string, base EditBase, patch jira.IssuePatch) error {
	if err := CheckBase(ctx, c, key, base); err != nil {
		return err
	}
	return c.UpdateIssue(ctx, key, patch)
}

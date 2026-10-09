package issue

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"time"

	appcache "github.com/varijkapil13/saral/internal/app/cache"
	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
)

// Editor is what a checked write runs on.
type Editor interface {
	jira.IssueReader
	jira.IssueWriter
}

// ReadFields reads only the named fields of one issue.
func ReadFields(ctx context.Context, reader jira.IssueReader, key string, ids []string) (jira.Issue, error) {
	return reader.IssueFields(ctx, key, ids)
}

// EditScreen is which fields are on an issue's edit screen right now.
func EditScreen(ctx context.Context, reader jira.SchemaReader, key string) (jira.EditMeta, error) {
	return reader.EditMeta(ctx, key)
}

// Account is this session's own account.
func Account(ctx context.Context, ident jira.Identifier) (jira.User, error) {
	return ident.Me(ctx)
}

// Assignable searches the accounts that can be assigned in a project, which is
// what drops the app accounts.
func Assignable(ctx context.Context, finder jira.PeopleFinder, project, match string, limit int) ([]jira.User, error) {
	return finder.FindPeople(ctx, jira.PeopleQuery{Match: match, Project: project, Limit: limit})
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

// Save checks then writes: two requests, so a change landing between them is
// still overwritten.
func Save(ctx context.Context, c Editor, key string, base EditBase, patch jira.IssuePatch) error {
	if err := CheckBase(ctx, c, key, base); err != nil {
		return err
	}
	return c.UpdateIssue(ctx, key, patch)
}

// Moves are the transitions this issue can make right now. They are never
// cached: which exist depends on the status the issue is in at the moment of
// asking, and on conditions the workflow evaluates against this issue.
func Moves(ctx context.Context, mover jira.Mover, key string) ([]jira.Transition, error) {
	return mover.Transitions(ctx, key)
}

// Mover is what a checked transition runs on.
type Mover interface {
	jira.IssueReader
	jira.Mover
}

// Move checks the base, then transitions.
func Move(ctx context.Context, c Mover, key, transitionID string, base EditBase, patch jira.IssuePatch) error {
	if err := CheckBase(ctx, c, key, base); err != nil {
		return err
	}
	return c.Transition(ctx, key, transitionID, patch)
}

// FromCache is seed with what the cache holds for it merged underneath, and
// false when the cache holds nothing for it.
func FromCache(held appcache.IssueCache, seed jira.Issue) (jira.Issue, bool) {
	snap, ok := held.Issue(seed.Key)
	if !ok {
		return seed, false
	}
	return appcache.MergeIssue(snap.Issue, seed), true
}

// Keep stores a freshly read issue in the cache.
func Keep(held appcache.IssueCache, iss jira.Issue) error {
	return held.PutIssue(iss)
}

package issue

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	appdraft "github.com/varijkapil13/saral/internal/app/draft"
	"github.com/varijkapil13/saral/pkg/jira"
)

// CreateDraft is what was typed into one create screen and has not become an
// issue yet. docs/UX.md principle 6 is that nothing typed is ever lost, so it
// is written down as each field is committed and stays on disk until the create
// it belongs to succeeds or the user throws it away.
type CreateDraft struct {
	Site      string                `json:"site"`
	Project   string                `json:"project"`
	IssueType string                `json:"issueType"`
	SavedAt   time.Time             `json:"savedAt"`
	Values    map[string]DraftValue `json:"values,omitempty"`
}

// DraftValue is one field as it was left: the text typed into it, or the
// options chosen in its picker by the identifier a create sends.
type DraftValue struct {
	Text   string        `json:"text,omitempty"`
	Picked []DraftOption `json:"picked,omitempty"`
}

func createOptions(in []jira.Option) []DraftOption {
	if len(in) == 0 {
		return nil
	}
	out := make([]DraftOption, len(in))
	for i, o := range in {
		out[i] = DraftOption{ID: o.ID, Label: o.Label, Children: createOptions(o.Children)}
	}
	return out
}

// CreateDraftKey is the one screen a draft belongs to. The site is part of it
// so that two profiles creating in projects with the same key cannot share a
// draft.
type CreateDraftKey struct {
	Site      string
	Project   string
	IssueType string
}

// NewCreateDraft is the draft of entries, keeping only those something was put
// in.
func NewCreateDraft(key CreateDraftKey, entries []Entry, at time.Time) CreateDraft {
	out := CreateDraft{Site: key.Site, Project: key.Project, IssueType: key.IssueType, SavedAt: at}
	for i := range entries {
		e := &entries[i]
		if e.Empty() {
			continue
		}
		if out.Values == nil {
			out.Values = make(map[string]DraftValue, len(entries))
		}
		out.Values[e.ID()] = DraftValue{Text: e.Text, Picked: createOptions(e.Picked)}
	}
	return out
}

// CreateDrafts keeps the create form's drafts.
type CreateDrafts struct{ store appdraft.Store }

// NewCreateDrafts puts the create form's drafts in their own subdirectory of
// the drafts root. A store with no directory keeps nothing, which is what a
// session with nowhere to write gets.
func NewCreateDrafts(root string) CreateDrafts {
	if strings.TrimSpace(root) == "" {
		return CreateDrafts{}
	}
	return CreateDrafts{store: appdraft.Open(filepath.Join(root, "create"))}
}

// Available reports a store that keeps anything.
func (s CreateDrafts) Available() bool { return s.store.Available() }

func (s CreateDrafts) path(key CreateDraftKey) string {
	return s.store.Path(CreateSafeName(key.Site), CreateSafeName(key.Project)+"."+CreateSafeName(key.IssueType)+".json")
}

// CreateSafeName reduces a site host, a project key or an issue type id to
// something that is a filename on every platform this runs on.
func CreateSafeName(s string) string { return appdraft.SafeName(strings.TrimSpace(s), "") }

// Load reads the draft kept for a screen, if there is one.
func (s CreateDrafts) Load(key CreateDraftKey) (CreateDraft, bool, error) {
	body, ok, err := s.store.Read(s.path(key))
	if err != nil {
		return CreateDraft{}, false, fmt.Errorf("reading the draft of this new issue: %w", err)
	}
	if !ok {
		return CreateDraft{}, false, nil
	}
	var kept CreateDraft
	if err := json.Unmarshal(body, &kept); err != nil {
		return CreateDraft{}, false, fmt.Errorf("reading the draft of this new issue: %w", err)
	}
	return kept, len(kept.Values) > 0, nil
}

// Save writes a draft, replacing whatever was there. A draft with nothing in
// it removes the file instead.
func (s CreateDrafts) Save(key CreateDraftKey, d CreateDraft) error {
	if !s.Available() {
		return nil
	}
	if len(d.Values) == 0 {
		return s.Discard(key)
	}
	body, err := json.Marshal(d)
	if err != nil {
		return fmt.Errorf("writing the draft of this new issue: %w", err)
	}
	if err := s.store.Write(s.path(key), body); err != nil {
		return fmt.Errorf("writing the draft of this new issue: %w", err)
	}
	return nil
}

// Discard removes a screen's draft, which is what a create that landed and a
// draft the user threw away both mean.
func (s CreateDrafts) Discard(key CreateDraftKey) error {
	if err := s.store.Remove(s.path(key)); err != nil {
		return fmt.Errorf("removing the draft of this new issue: %w", err)
	}
	return nil
}

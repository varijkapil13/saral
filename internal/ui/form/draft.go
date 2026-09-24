package form

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
)

// draft is what was typed into one create screen and has not become an issue
// yet. docs/UX.md principle 6 is that nothing typed is ever lost, so it is
// written down as each field is committed and stays on disk until the create it
// belongs to succeeds or the user throws it away.
type draft struct {
	Site      string                `json:"site"`
	Project   string                `json:"project"`
	IssueType string                `json:"issueType"`
	SavedAt   time.Time             `json:"savedAt"`
	Values    map[string]draftValue `json:"values,omitempty"`
}

// draftValue is one field as it was left: the text typed into it, or the
// options chosen in its picker by the identifier a create sends.
type draftValue struct {
	Text   string        `json:"text,omitempty"`
	Picked []draftOption `json:"picked,omitempty"`
}

type draftOption struct {
	ID       string        `json:"id"`
	Label    string        `json:"label"`
	Children []draftOption `json:"children,omitempty"`
}

func toDraftOptions(in []jira.Option) []draftOption {
	if len(in) == 0 {
		return nil
	}
	out := make([]draftOption, len(in))
	for i, o := range in {
		out[i] = draftOption{ID: o.ID, Label: o.Label, Children: toDraftOptions(o.Children)}
	}
	return out
}

func fromDraftOptions(in []draftOption) []jira.Option {
	if len(in) == 0 {
		return nil
	}
	out := make([]jira.Option, len(in))
	for i, o := range in {
		out[i] = jira.Option{ID: o.ID, Label: o.Label, Children: fromDraftOptions(o.Children)}
	}
	return out
}

// draftKey is the one screen a draft belongs to. The site is part of it so that
// two profiles creating in projects with the same key cannot share a draft.
type draftKey struct {
	site      string
	project   string
	issueType string
}

type draftStore struct{ dir string }

// newDraftStore puts the create form's drafts in their own subdirectory of the
// drafts root. A store with no directory keeps nothing, which is what a session
// with nowhere to write gets.
func newDraftStore(root string) draftStore {
	if strings.TrimSpace(root) == "" {
		return draftStore{}
	}
	return draftStore{dir: filepath.Join(root, "create")}
}

func (s draftStore) available() bool { return s.dir != "" }

func (s draftStore) path(key draftKey) string {
	return filepath.Join(s.dir, safeName(key.site), safeName(key.project)+"."+safeName(key.issueType)+".json")
}

// safeName reduces a site host, a project key or an issue type id to something
// that is a filename on every platform this runs on.
func safeName(s string) string {
	out := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, strings.TrimSpace(s))
	if out == "" {
		return "unnamed"
	}
	return out
}

func (s draftStore) load(key draftKey) (draft, bool, error) {
	if !s.available() {
		return draft{}, false, nil
	}
	body, err := os.ReadFile(s.path(key)) //nolint:gosec // the path is built from the store's own directory
	if errors.Is(err, fs.ErrNotExist) {
		return draft{}, false, nil
	}
	if err != nil {
		return draft{}, false, fmt.Errorf("reading the draft of this new issue: %w", err)
	}
	var kept draft
	if err := json.Unmarshal(body, &kept); err != nil {
		return draft{}, false, fmt.Errorf("reading the draft of this new issue: %w", err)
	}
	return kept, len(kept.Values) > 0, nil
}

// save writes a draft, replacing whatever was there, through a temporary file
// so that a crash halfway through leaves the previous draft rather than half of
// this one. A draft with nothing in it removes the file instead.
func (s draftStore) save(key draftKey, d draft) error {
	if !s.available() {
		return nil
	}
	if len(d.Values) == 0 {
		return s.discard(key)
	}
	body, err := json.Marshal(d)
	if err != nil {
		return fmt.Errorf("writing the draft of this new issue: %w", err)
	}
	final := s.path(key)
	if err := os.MkdirAll(filepath.Dir(final), 0o700); err != nil {
		return fmt.Errorf("writing the draft of this new issue: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(final), ".draft-*")
	if err != nil {
		return fmt.Errorf("writing the draft of this new issue: %w", err)
	}
	name := temp.Name()
	fail := func(err error) error {
		_ = os.Remove(name)
		return fmt.Errorf("writing the draft of this new issue: %w", err)
	}
	if _, err := temp.Write(body); err != nil {
		_ = temp.Close()
		return fail(err)
	}
	if err := temp.Close(); err != nil {
		return fail(err)
	}
	if err := os.Chmod(name, 0o600); err != nil {
		return fail(err)
	}
	if err := os.Rename(name, final); err != nil {
		return fail(err)
	}
	return nil
}

// discard removes a screen's draft, which is what a create that landed and a
// draft the user threw away both mean.
func (s draftStore) discard(key draftKey) error {
	if !s.available() {
		return nil
	}
	if err := os.Remove(s.path(key)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("removing the draft of this new issue: %w", err)
	}
	return nil
}

func draftOf(key draftKey, fields []*field, at time.Time) draft {
	out := draft{Site: key.site, Project: key.project, IssueType: key.issueType, SavedAt: at}
	for _, f := range fields {
		if f.empty() {
			continue
		}
		if out.Values == nil {
			out.Values = make(map[string]draftValue, len(fields))
		}
		out.Values[f.id()] = draftValue{Text: f.text, Picked: toDraftOptions(f.picked)}
	}
	return out
}

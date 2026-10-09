package issue

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	appdraft "github.com/varijkapil13/saral/internal/app/draft"
	"github.com/varijkapil13/saral/pkg/jira"
)

// Draft is an edit that has not reached Jira. docs/UX.md principle 6 is that
// nothing typed is ever lost, so an edit is written down as soon as a field is
// committed and stays on disk until the write it belongs to succeeds — through
// a rejected request, a 409 and a crash alike.
type Draft struct {
	Key     string    `json:"key"`
	Site    string    `json:"site"`
	SavedAt time.Time `json:"savedAt"`
	// Values are the edited fields by field ID, in the form they were typed.
	Values map[string]string `json:"values,omitempty"`
	// Choices are the priority and assignee edits by field id. Values alone
	// cannot carry them: a display label is neither unique nor what a patch
	// sends.
	Choices map[string]NamedID `json:"choices,omitempty"`
	// Description is the document $EDITOR produced, kept as ADF because that is
	// what was reconciled against the original and re-rendering it as markdown
	// would put it through a second lossy trip.
	Description     json.RawMessage `json:"description,omitempty"`
	DescriptionText *string         `json:"descriptionText,omitempty"`

	// Picks, Docs and Pending are custom fields' edits by field id: the values
	// a choice or person field holds, a document field's reconciled ADF, and a
	// document field's inline text not yet kept.
	Picks   map[string][]DraftOption   `json:"picks,omitempty"`
	Docs    map[string]json.RawMessage `json:"docs,omitempty"`
	Pending map[string]string          `json:"pending,omitempty"`

	Base       EditBase  `json:"base,omitzero"`
	LabelsBase *[]string `json:"labelsBase,omitempty"`
}

// NamedID is one chosen option, kept as the identifier a patch sends and the
// label a person reads it as.
type NamedID struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// DraftOption is one value a custom choice row holds, a cascade's second level
// under Children.
type DraftOption struct {
	ID       string        `json:"id"`
	Label    string        `json:"label"`
	Children []DraftOption `json:"children,omitempty"`
}

// IsEmpty reports a draft that holds no edit.
func (d Draft) IsEmpty() bool {
	return len(d.Values) == 0 && len(d.Choices) == 0 && len(d.Description) == 0 && d.DescriptionText == nil &&
		len(d.Picks) == 0 && len(d.Docs) == 0 && len(d.Pending) == 0
}

// Drafts keeps drafts under one directory, one file per issue per site.
type Drafts struct{ store appdraft.Store }

// NewDrafts keeps drafts in dir. A store with no directory keeps nothing,
// which is what a session with nowhere to write gets.
func NewDrafts(dir string) Drafts { return Drafts{store: appdraft.Open(dir)} }

// Available reports a store that keeps anything.
func (s Drafts) Available() bool { return s.store.Available() }

// path is where one issue's draft lives. The site is a directory so that two
// profiles on two sites cannot overwrite each other's draft of the same key.
func (s Drafts) path(site, key string) string {
	return s.store.Path(SafeName(site), SafeName(key)+".json")
}

// SafeName reduces a site host or an issue key to something that is a
// filename on every platform this runs on.
func SafeName(s string) string { return appdraft.SafeName(strings.TrimSpace(s), ".") }

// Load reads the draft kept for an issue, if there is one.
func (s Drafts) Load(site, key string) (Draft, bool, error) {
	body, ok, err := s.store.Read(s.path(site, key))
	if err != nil {
		return Draft{}, false, fmt.Errorf("reading the draft of %s: %w", key, err)
	}
	if !ok {
		return Draft{}, false, nil
	}
	var kept Draft
	if err := json.Unmarshal(body, &kept); err != nil {
		return Draft{}, false, fmt.Errorf("reading the draft of %s: %w", key, err)
	}
	return kept, !kept.IsEmpty(), nil
}

// Save writes a draft, replacing whatever was there.
func (s Drafts) Save(d Draft) error {
	if !s.Available() {
		return nil
	}
	if d.IsEmpty() {
		return s.Discard(d.Site, d.Key)
	}
	body, err := json.Marshal(d)
	if err != nil {
		return fmt.Errorf("writing the draft of %s: %w", d.Key, err)
	}
	if err := s.store.Write(s.path(d.Site, d.Key), body); err != nil {
		return fmt.Errorf("writing the draft of %s: %w", d.Key, err)
	}
	return nil
}

// Discard removes an issue's draft, which is what a write that landed and an
// edit the user threw away both mean.
func (s Drafts) Discard(site, key string) error {
	if err := s.store.Remove(s.path(site, key)); err != nil {
		return fmt.Errorf("removing the draft of %s: %w", key, err)
	}
	return nil
}

// Held copies out of d the edits for fields missing reports, so they can wait
// for a row.
func Held(d Draft, missing func(id string) bool) Draft {
	out := Draft{Key: d.Key, Site: d.Site}
	for id, v := range d.Values {
		if missing(id) {
			out.Values = setIn(out.Values, id, v)
		}
	}
	for id, v := range d.Choices {
		if missing(id) {
			out.Choices = setIn(out.Choices, id, v)
		}
	}
	for id, v := range d.Picks {
		if missing(id) {
			out.Picks = setIn(out.Picks, id, v)
		}
	}
	for id, v := range d.Docs {
		if missing(id) {
			out.Docs = setIn(out.Docs, id, v)
		}
	}
	for id, v := range d.Pending {
		if missing(id) {
			out.Pending = setIn(out.Pending, id, v)
		}
	}
	for id, v := range d.Base.Fields {
		if missing(id) {
			out.Base.Fields = setIn(out.Base.Fields, id, v)
		}
	}
	return out
}

// WithHeld adds to d the edits still waiting for a row, without overwriting
// anything d already says about a field.
func WithHeld(d, held Draft) Draft {
	for id, v := range held.Values {
		if _, ok := d.Values[id]; !ok {
			d.Values = setIn(d.Values, id, v)
		}
	}
	for id, v := range held.Choices {
		if _, ok := d.Choices[id]; !ok {
			d.Choices = setIn(d.Choices, id, v)
		}
	}
	for id, v := range held.Picks {
		if _, ok := d.Picks[id]; !ok {
			d.Picks = setIn(d.Picks, id, v)
		}
	}
	for id, v := range held.Docs {
		if _, ok := d.Docs[id]; !ok {
			d.Docs = setIn(d.Docs, id, v)
		}
	}
	for id, v := range held.Pending {
		if _, ok := d.Pending[id]; !ok {
			d.Pending = setIn(d.Pending, id, v)
		}
	}
	for id, v := range held.Base.Fields {
		if _, ok := d.Base.Fields[id]; !ok {
			d.Base.Fields = setIn(d.Base.Fields, id, v)
		}
	}
	return d
}

func setIn[V any](m map[string]V, id string, v V) map[string]V {
	if m == nil {
		m = map[string]V{}
	}
	m[id] = v
	return m
}

// ToDraftOptions keeps chosen options in a draft.
func ToDraftOptions(in []jira.Option) []DraftOption {
	out := make([]DraftOption, len(in))
	for i, o := range in {
		out[i] = DraftOption{ID: o.ID, Label: o.Label, Children: ToDraftOptions(o.Children)}
	}
	return out
}

// FromDraftOptions reads a draft's options back.
func FromDraftOptions(in []DraftOption) []jira.Option {
	if len(in) == 0 {
		return nil
	}
	out := make([]jira.Option, len(in))
	for i, o := range in {
		out[i] = jira.Option{ID: o.ID, Label: o.Label, Children: FromDraftOptions(o.Children)}
	}
	return out
}

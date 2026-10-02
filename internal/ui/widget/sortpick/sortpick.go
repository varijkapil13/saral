// Package sortpick is the field-and-direction picker, driven by kernel.SortPickerKeys.
package sortpick

import (
	"slices"
	"strings"

	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// Field is one field a view can order by, opening in direction Desc the first time.
type Field struct {
	ID    string
	Label string
	Desc  bool
}

// Choice is an order in force. The zero value is no choice made.
type Choice struct {
	Field string
	Desc  bool
}

// Chosen reports whether a field has been named.
func (c Choice) Chosen() bool { return c.Field != "" }

// Spec is the choice as ui.toml keeps it.
func (c Choice) Spec() config.SortSpec { return config.SortSpec{Field: c.Field, Desc: c.Desc} }

// FromSpec reads a kept choice, refusing a field the view no longer offers.
func FromSpec(spec config.SortSpec, fields []Field) (Choice, bool) {
	if !slices.ContainsFunc(fields, func(f Field) bool { return f.ID == spec.Field }) {
		return Choice{}, false
	}
	return Choice{Field: spec.Field, Desc: spec.Desc}, true
}

// Picker is the fields on offer, the cursor over them and whether it is open.
type Picker struct {
	Fields []Field
	Cursor int
	Open   bool
}

var strokes = func() map[string]kernel.Action {
	out := make(map[string]kernel.Action)
	for _, b := range kernel.SortPickerKeys() {
		if m, ok := kernel.MintOf(b); ok {
			for _, k := range b.Keys() {
				out[k] = m.Action
			}
		}
	}
	return out
}()

// Start opens the picker with the cursor on the field in force.
func (p *Picker) Start(cur Choice) {
	p.Open = true
	p.Cursor = max(slices.IndexFunc(p.Fields, func(f Field) bool { return f.ID == cur.Field }), 0)
}

// Key takes one stroke while open; chose reports next as the order to put in force.
func (p *Picker) Key(stroke string, cur Choice) (next Choice, chose, handled bool) {
	if !p.Open || len(p.Fields) == 0 {
		return Choice{}, false, false
	}
	switch strokes[stroke] {
	case kernel.ActSortPrev:
		p.Cursor = (p.Cursor - 1 + len(p.Fields)) % len(p.Fields)
	case kernel.ActSortNext:
		p.Cursor = (p.Cursor + 1) % len(p.Fields)
	case kernel.ActSortChoose:
		f := p.Fields[min(p.Cursor, len(p.Fields)-1)]
		next = Choice{Field: f.ID, Desc: f.Desc}
		if cur.Field == f.ID {
			next.Desc = !cur.Desc
		}
		p.Open = false
		return next, true, true
	case kernel.ActSortCancel:
		p.Open = false
	default:
	}
	return Choice{}, false, true
}

// Arrow is the direction mark.
func Arrow(desc bool, g kernel.Glyphs) string {
	switch {
	case g.IsASCII() && desc:
		return "v"
	case g.IsASCII():
		return "^"
	case desc:
		return "↓"
	default:
		return "↑"
	}
}

// Line is the fields on offer as plain text, the cursor bracketed, the one in force marked.
func Line(fields []Field, cur Choice, cursor int, g kernel.Glyphs) string {
	var b strings.Builder
	b.WriteString("sort by:  ")
	for i, f := range fields {
		if i > 0 {
			b.WriteString("  ")
		}
		name := f.Label
		if f.ID == cur.Field {
			name += " " + Arrow(cur.Desc, g)
		}
		if i == cursor {
			name = "[" + name + "]"
		}
		b.WriteString(name)
	}
	return b.String()
}

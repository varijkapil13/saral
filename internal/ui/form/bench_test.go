package form

import (
	"strconv"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

// wideScreen is a create screen with n fields on it, which is what a site with
// a long-lived project's worth of custom fields actually answers.
func wideScreen(n int) jira.Schema {
	schema := jira.Schema{
		Project:   jira.ProjectRef{Key: "PROJ", Name: "Project"},
		IssueType: jira.IssueType{ID: "10001", Name: "Task"},
		Fields: []jira.FieldMeta{
			required(meta("summary", "Summary", jira.FieldSchema{Type: "string", System: "summary"})),
			meta("description", "Description", jira.FieldSchema{Type: "string", System: "description"}),
		},
	}
	options := make([]jira.Option, 0, 8)
	for i := range 8 {
		options = append(options, option(strconv.Itoa(i), "Value "+strconv.Itoa(i)))
	}
	for i := range n {
		id := "customfield_" + strconv.Itoa(20000+i)
		switch i % 4 {
		case 0:
			schema.Fields = append(schema.Fields, meta(id, "Number "+strconv.Itoa(i), jira.FieldSchema{Type: "number", Custom: "x:float", CustomID: 20000 + i}))
		case 1:
			schema.Fields = append(schema.Fields, meta(id, "Choice "+strconv.Itoa(i), jira.FieldSchema{Type: "option", Custom: "x:select", CustomID: 20000 + i}, options...))
		case 2:
			schema.Fields = append(schema.Fields, meta(id, "Date "+strconv.Itoa(i), jira.FieldSchema{Type: "date", Custom: "x:date", CustomID: 20000 + i}))
		default:
			schema.Fields = append(schema.Fields, meta(id, "Text "+strconv.Itoa(i), jira.FieldSchema{Type: "string", Custom: "x:textfield", CustomID: 20000 + i}))
		}
	}
	return schema
}

// built puts a create screen on a form at a given size without touching the
// network: the schema arrives as the message the adapter would have produced.
func built(tb testing.TB, fields, w, h int) *Model {
	tb.Helper()

	d := kernel.Deps{
		Caps:  jira.Capabilities{TimeZone: time.UTC},
		Theme: kernel.NewTheme(kernel.ThemeDark, true, kernel.UnicodeGlyphs()),
		Now:   func() time.Time { return time.Date(2026, time.March, 5, 9, 0, 0, 0, time.UTC) },
	}
	m := newWith(d, newSchemaCache(schemaTTL, time.Now), newDraftStore())
	next, _ := m.Update(kernel.SizeMsg{Width: w, Height: h})
	m, _ = next.(*Model)
	next, _ = m.Update(schemaLoadedMsg{gen: m.gen, schema: wideScreen(fields)})
	m, _ = next.(*Model)
	_ = m.View()
	return m
}

func scroll(b *testing.B, m *Model) {
	b.Helper()

	var down, up tea.Msg = keyPress("j"), keyPress("k")
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		key := down
		if i%2 == 1 {
			key = up
		}
		next, _ := m.Update(key)
		m, _ = next.(*Model)
		_ = m.View()
	}
}

// BenchmarkFormSteadyScroll200 and its eight-field twin are the pair that
// matters: docs/PERFORMANCE.md asks that a long screen and a short one cost the
// same per frame.
func BenchmarkFormSteadyScroll200(b *testing.B) { scroll(b, built(b, 200, 120, 40)) }

func BenchmarkFormSteadyScroll8(b *testing.B) { scroll(b, built(b, 8, 120, 40)) }

// BenchmarkFormWalk200 walks a fresh row into view on every frame, which misses
// the memo by construction.
func BenchmarkFormWalk200(b *testing.B) {
	m := built(b, 200, 120, 40)
	var down tea.Msg = keyPress("j")
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		next, _ := m.Update(down)
		m, _ = next.(*Model)
		_ = m.View()
	}
}

func BenchmarkFormRedraw200x60(b *testing.B) {
	m := built(b, 200, 200, 60)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = m.View()
	}
}

func BenchmarkFieldRender(b *testing.B) {
	m := built(b, 64, 120, 40)
	st, lay := m.styles, m.lay
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		_ = renderField(m.fields[i%len(m.fields)], lay, i%7 == 0, st)
	}
}

func BenchmarkFormTypingIntoAField(b *testing.B) {
	m := built(b, 200, 120, 40)
	next, _ := m.Update(keyPress("enter"))
	m, _ = next.(*Model)
	keys := []tea.Msg{tea.KeyPressMsg{Code: 'x', Text: "x"}, tea.KeyPressMsg{Code: tea.KeyBackspace}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		next, _ := m.Update(keys[i%2])
		m, _ = next.(*Model)
		_ = m.View()
	}
}

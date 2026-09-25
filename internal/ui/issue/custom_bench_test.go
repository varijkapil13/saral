package issue

import (
	"testing"
	"time"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// BenchmarkIssueCustomRowsScroll is the cursor walking a sidebar whose screen
// lists editable custom fields, each drawn from its own row.
func BenchmarkIssueCustomRowsScroll(b *testing.B) {
	base, err := newFake(3).Fields(b.Context())
	if err != nil {
		b.Fatal(err)
	}
	f := newFake(3, jiratest.WithFields(append(base, customCatalogue...)))
	metas := make([]jira.FieldMeta, 0, len(customCatalogue))
	for i := range customCatalogue {
		fl := &customCatalogue[i]
		metas = append(metas, jira.FieldMeta{Field: fl.Ref(), Name: fl.Name, Operations: []string{"set"}, AllowedValues: checkValues})
	}
	f.SetEditMeta("PROJ-1", metas...)
	iss, err := f.Issue(b.Context(), "PROJ-1")
	if err != nil {
		b.Fatal(err)
	}
	iss.Requested = jira.AllFields()
	d := kernel.Deps{
		Jira:  f,
		Caps:  jira.Capabilities{TimeZone: time.UTC},
		Theme: kernel.NewTheme(kernel.ThemeDark, true, kernel.UnicodeGlyphs()),
		Now:   func() time.Time { return time.Date(2025, time.March, 5, 9, 0, 0, 0, time.UTC) },
	}
	m, _ := New(d, iss).(*Model)
	next, _ := m.Update(kernel.SizeMsg{Width: 120, Height: 40})
	m, _ = next.(*Model)
	m = settle(b, m, m.Init())
	m.focus = regionDetails
	_ = m.View()
	if len(m.rows) <= len(editableRowSpecs) {
		b.Fatal("no custom row was built, so this measures nothing new")
	}
	down, up := keyPress("j"), keyPress("k")
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		press := down
		if i%2 == 1 {
			press = up
		}
		next, _ := m.Update(press)
		m, _ = next.(*Model)
		_ = m.View()
	}
}

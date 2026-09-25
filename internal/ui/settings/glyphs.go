package settings

import (
	"strings"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

// glyphsSettingID is the kernel's Glyphs row, under which the screen draws
// every icon of the tier in force: the choice is about what a font can show,
// and the only way to judge that is to see it.
const glyphsSettingID = "appearance.glyphs"

// belowRow is the line after a setting's detail, blank for every setting but
// the glyph tier's. It takes the place of the spacer, so no row moves.
func (m *Model) belowRow(s kernel.Setting) string {
	if s.ID != glyphsSettingID || m.deps.Theme == nil {
		return ""
	}
	at := struct{ gen, width int }{m.styles.gen, m.lay.width}
	if m.preview == "" || m.previewAt != at {
		t := m.deps.Theme
		m.preview = renderDetail(glyphPreview(t.Glyphs), false, false, m.lay.width, t.Glyphs.Ellipsis, m.styles)
		m.previewAt = at
	}
	return m.preview
}

// glyphPreview is every icon in a glyph set, in one line.
func glyphPreview(g kernel.Glyphs) string {
	icons := make([]string, 0, 29+len(g.Spinner))
	icons = append(icons,
		g.TypeEpic, g.TypeStory, g.TypeTask, g.TypeBug, g.TypeSubtask, g.TypeOther,
		g.CategoryToDo, g.CategoryInProgress, g.CategoryDone, g.CategoryUnknown,
		g.Bullet, g.Arrow, g.Check, g.Cross, g.Dot, g.Ellipsis, g.Stale, g.Separator,
		g.Collapsed, g.Expanded, g.Diamond, g.ProgressOn, g.ProgressNo,
		g.VLine, g.HLine, g.CornerTL, g.CornerTR, g.CornerBL, g.CornerBR,
	)
	icons = append(icons, g.Spinner...)
	return strings.Join(icons, " ")
}

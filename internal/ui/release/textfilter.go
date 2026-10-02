package release

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/widget"
)

// finder is the text filter, with the folded text of each version to hold it against.
type finder struct {
	input     textinput.Model
	needle    string
	rawNeedle string
	folds     []string
}

func newFinder(placeholder string) finder {
	in := widget.NewInput()
	in.Prompt = "/ "
	in.Placeholder = placeholder
	return finder{input: in}
}

func (m *Model) matches(i int) bool {
	return m.find.needle == "" || strings.Contains(m.find.folds[i], m.find.needle)
}

// While the filter is open every key is text, so q, j and digits are typed.
func (m *Model) startFind() tea.Cmd {
	if m.saving || m.mode != browsing || m.blurred {
		return nil
	}
	f := &m.find
	m.mode = finding
	f.input.SetWidth(max(m.width-len(f.input.Prompt)-inputChrome, 8))
	f.input.SetValue(f.rawNeedle)
	f.input.CursorEnd()
	_ = f.input.Focus()
	m.sum = ""
	m.clampScroll()
	return nil
}

func (m *Model) findKey(msg tea.KeyPressMsg) tea.Cmd {
	f := &m.find
	switch msg.String() {
	case "enter":
		m.endFind()
		return nil
	case "esc":
		f.input.SetValue("")
		m.setNeedle("")
		m.endFind()
		return nil
	}
	f.input, _ = f.input.Update(msg)
	if raw := f.input.Value(); raw != f.rawNeedle {
		m.setNeedle(raw)
	}
	return nil
}

func (m *Model) endFind() {
	m.mode = browsing
	m.find.input.Blur()
	m.sum = ""
	m.scrollToCursor()
}

func (m *Model) setNeedle(raw string) {
	under, head := m.selectedID(), m.headKeyAtCursor()
	m.find.rawNeedle = raw
	m.find.needle = strings.ToLower(strings.TrimSpace(raw))
	m.sum = ""
	m.reorder()
	m.moveOnto(under)
	if under == "" {
		m.moveOntoHead(head)
	}
}

func (m *Model) clearFind() {
	m.find.input.SetValue("")
	m.find.input.Blur()
	m.find.rawNeedle, m.find.needle = "", ""
}

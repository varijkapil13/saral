package uitest

import (
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

var clipboardType = reflect.TypeOf(tea.SetClipboard("")())

// Clipboard is what a message puts on the clipboard, if it is a clipboard write.
func Clipboard(msg tea.Msg) (string, bool) {
	v := reflect.ValueOf(msg)
	if !v.IsValid() || v.Type() != clipboardType {
		return "", false
	}
	return v.String(), true
}

// MenuEntry is how many presses of down from the first entry of the right-click
// menu in frame reach the one whose row carries desc.
func MenuEntry(t testing.TB, frame, desc string) int {
	t.Helper()
	rows := strings.Split(ansi.Strip(frame), "\n")
	first := -1
	for i, row := range rows {
		if strings.Contains(row, "What can be done here") {
			first = i + 1
		}
		if first >= 0 && strings.Contains(row, desc) {
			return i - first
		}
	}
	t.Fatalf("no menu entry reads %q:\n%s", desc, ansi.Strip(frame))
	return 0
}

// CopiedBy runs cmd to exhaustion and reports what it put on the clipboard.
// unwrap takes an envelope off a message before it is looked at, or is nil.
func CopiedBy(cmd tea.Cmd, unwrap func(tea.Msg) tea.Msg) []string {
	var out []string
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		if next == nil {
			continue
		}
		msg := next()
		if unwrap != nil {
			msg = unwrap(msg)
		}
		if cmds, ok := commands(msg); ok {
			queue = append(queue, cmds...)
			continue
		}
		if text, ok := Clipboard(msg); ok {
			out = append(out, text)
		}
	}
	return out
}

func commands(msg tea.Msg) ([]tea.Cmd, bool) {
	v := reflect.ValueOf(msg)
	if !v.IsValid() || v.Kind() != reflect.Slice || v.Type().Elem() != reflect.TypeOf(tea.Cmd(nil)) {
		return nil, false
	}
	out := make([]tea.Cmd, 0, v.Len())
	for i := range v.Len() {
		cmd, _ := v.Index(i).Interface().(tea.Cmd)
		out = append(out, cmd)
	}
	return out, true
}

package attach

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// onWindows decides how a typed path is read: there a backslash separates
// directories, so it escapes nothing.
var onWindows = runtime.GOOS == "windows"

// shellSpecial is what a terminal escapes when it pastes a dragged file's path,
// and so what a completion escapes when it adds to a bare path.
const shellSpecial = " \t\\'\"()&;|<>$`*?[]{}!#"

// cleanPath reads a path the way the shell it was pasted from would have: one
// layer of quotes comes off, the closing one only if it has been typed yet, a leading ~ is the home directory, and
// outside single quotes a backslash escapes the character after it. The line is
// one path, spaces and all, because the prompt takes one file.
func cleanPath(raw, home string, windows bool) string {
	s := strings.TrimSpace(raw)
	quote := byte(0)
	if s != "" && (s[0] == '\'' || s[0] == '"') {
		quote, s = s[0], s[1:]
		if s != "" && s[len(s)-1] == quote {
			s = s[:len(s)-1]
		}
	}
	tilde := home != "" && hasTilde(s, windows)
	if tilde {
		s = s[1:]
	}
	if !windows && quote != '\'' {
		s = unescape(s, quote == '"')
	}
	if tilde {
		s = home + s
	}
	return s
}

func hasTilde(s string, windows bool) bool {
	if s == "~" {
		return true
	}
	if len(s) < 2 || s[0] != '~' {
		return false
	}
	return s[1] == '/' || (windows && s[1] == '\\')
}

// unescape drops the backslash in front of any character, the way an unquoted
// shell word does. Inside double quotes a shell keeps the backslash unless it is
// in front of one of the four characters that are special there.
func unescape(s string, doubleQuoted bool) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) && (!doubleQuoted || strings.IndexByte("\"\\$`", s[i+1]) >= 0) {
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// completion is what one tab press found: the line with the common prefix of the
// matches added, and the matches themselves when there is more than one.
type completion struct {
	line    string
	matches []string
}

// completePath extends the typed line by the longest prefix every matching
// directory entry shares. It adds to the line in the line's own spelling —
// escaped where the line is a bare shell word, literal inside quotes — so that
// what cleanPath reads from the result is what it read before plus the entry.
// A lone match that is a directory gains a separator, which is what makes the
// next press list what is inside it. Dot entries are offered only to a prefix
// that starts with a dot.
func completePath(raw, home string, windows bool) completion {
	seps, sep := "/", "/"
	if windows {
		seps, sep = `/\`, `\`
	}
	if strings.TrimSpace(raw) == "~" && home != "" {
		return completion{line: "~" + sep}
	}
	path := cleanPath(raw, home, windows)
	cut := strings.LastIndexAny(path, seps) + 1
	dir, base := path[:cut], path[cut:]
	entries, err := os.ReadDir(orHere(dir))
	if err != nil {
		return completion{line: raw}
	}
	var names []string
	var lone os.DirEntry
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, base) || (strings.HasPrefix(name, ".") && !strings.HasPrefix(base, ".")) {
			continue
		}
		names = append(names, name)
		lone = entry
	}
	if len(names) == 0 {
		return completion{line: raw}
	}
	add := commonPrefix(names)[len(base):]
	if len(names) == 1 && isDir(dir, lone) {
		add += sep
	}
	out := completion{line: extend(raw, add, windows)}
	if len(names) > 1 {
		out.matches = names
	}
	return out
}

func orHere(dir string) string {
	if dir == "" {
		return "."
	}
	return dir
}

// isDir follows a symbolic link, because a link to a directory is somewhere a
// path can go on into.
func isDir(dir string, entry os.DirEntry) bool {
	if entry.IsDir() {
		return true
	}
	if entry.Type()&os.ModeSymlink == 0 {
		return false
	}
	info, err := os.Stat(filepath.Join(orHere(dir), entry.Name()))
	return err == nil && info.IsDir()
}

func commonPrefix(names []string) string {
	prefix := names[0]
	for _, name := range names[1:] {
		n := 0
		for n < len(prefix) && n < len(name) && prefix[n] == name[n] {
			n++
		}
		prefix = prefix[:n]
	}
	return prefix
}

// extend adds text to a typed line. A line opened with a quote takes it inside
// the quote, before the closing one if it has been typed; a bare line on a system
// whose shell escapes with a backslash takes it escaped.
func extend(raw, add string, windows bool) string {
	if add == "" {
		return raw
	}
	line := strings.TrimSpace(raw)
	if line != "" && (line[0] == '\'' || line[0] == '"') {
		if !windows && line[0] == '"' {
			add = escape(add, "\"\\$`")
		}
		if len(line) >= 2 && line[len(line)-1] == line[0] {
			return line[:len(line)-1] + add + line[len(line)-1:]
		}
		return line + add
	}
	if windows {
		return line + add
	}
	return line + escape(add, shellSpecial)
}

func escape(s, special string) string {
	var b strings.Builder
	b.Grow(2 * len(s))
	for i := 0; i < len(s); i++ {
		if strings.IndexByte(special, s[i]) >= 0 {
			b.WriteByte('\\')
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

package attach

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCleanPath(t *testing.T) {
	t.Parallel()

	const home = "/home/sam"
	for name, tc := range map[string]struct {
		raw     string
		windows bool
		want    string
	}{
		"a plain path is left alone":                     {raw: "/tmp/notes.txt", want: "/tmp/notes.txt"},
		"surrounding space is trimmed":                   {raw: "  /tmp/notes.txt \n", want: "/tmp/notes.txt"},
		"a dragged path loses its escapes":               {raw: `/Users/me/My\ File.png`, want: "/Users/me/My File.png"},
		"every escaped special is unescaped":             {raw: `/a/b\(1\)\ \&\ c\'s.png`, want: "/a/b(1) & c's.png"},
		"an escaped backslash is one backslash":          {raw: `/a/back\\slash`, want: `/a/back\slash`},
		"a lone trailing backslash is kept":              {raw: `/a/b\`, want: `/a/b\`},
		"a typed space with no escape is part of it":     {raw: "/Users/me/My File.png", want: "/Users/me/My File.png"},
		"single quotes come off and keep backslashes":    {raw: `'/a/My\ File.png'`, want: `/a/My\ File.png`},
		"double quotes come off":                         {raw: `"/a/My File.png"`, want: "/a/My File.png"},
		"inside double quotes only specials unescape":    {raw: `"/a/x\"y\ z"`, want: `/a/x"y\ z`},
		"only one layer of quotes comes off":             {raw: `"'/a/b'"`, want: "'/a/b'"},
		"a quote not yet closed still opens the path":    {raw: `'/a/b"`, want: `/a/b"`},
		"a lone quote is nothing":                        {raw: `"`, want: ""},
		"a leading tilde is the home directory":          {raw: "~/Pictures/cat.png", want: home + "/Pictures/cat.png"},
		"a tilde alone is the home directory":            {raw: "~", want: home},
		"a quoted tilde path is still the home":          {raw: `"~/My Pictures/a.png"`, want: home + "/My Pictures/a.png"},
		"an escaped dragged path under the home":         {raw: `~/My\ Pictures/a.png`, want: home + "/My Pictures/a.png"},
		"another user's tilde is not expanded":           {raw: "~bob/a.png", want: "~bob/a.png"},
		"a tilde further in is a character":              {raw: "/a/~/b", want: "/a/~/b"},
		"windows keeps every backslash as a separator":   {raw: `C:\Users\me\My File.png`, windows: true, want: `C:\Users\me\My File.png`},
		"windows drops the quotes a drag adds":           {raw: `"C:\Users\me\My File.png"`, windows: true, want: `C:\Users\me\My File.png`},
		"windows expands a tilde before a backslash":     {raw: `~\Desktop\a.png`, windows: true, want: home + `\Desktop\a.png`},
		"windows does not read a backslash as an escape": {raw: `C:\a\ b`, windows: true, want: `C:\a\ b`},
		"a unix backslash before a tilde is not home":    {raw: `~\a`, want: "~a"},
		"nothing is nothing":                             {raw: "   ", want: ""},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := cleanPath(tc.raw, home, tc.windows); got != tc.want {
				t.Errorf("cleanPath(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestCleanPath_WithNoHomeLeavesTheTilde(t *testing.T) {
	t.Parallel()

	if got := cleanPath("~/a.png", "", false); got != "~/a.png" {
		t.Errorf("with no home directory the tilde became %q", got)
	}
}

// tree is a directory of the test's own holding the named entries; a name ending
// in a slash is a directory.
func tree(t *testing.T, names ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range names {
		path := filepath.Join(root, filepath.FromSlash(name))
		if strings.HasSuffix(name, "/") {
			if err := os.MkdirAll(path, 0o750); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestCompletePath(t *testing.T) {
	t.Parallel()

	root := tree(t, "report-2026.pdf", "report-2025.pdf", "screens/", "screens/one.png",
		"My Pictures/", "notes.txt", ".hidden", "solo/", "solo/inner.png")
	for name, tc := range map[string]struct {
		typed   string
		want    string
		matches []string
	}{
		"a lone file is completed whole":              {typed: root + "/no", want: root + "/notes.txt"},
		"a lone directory gains a separator":          {typed: root + "/scr", want: root + "/screens/"},
		"a directory then lists inside it":            {typed: root + "/screens/", want: root + "/screens/one.png"},
		"several matches stop at what they share":     {typed: root + "/rep", want: root + "/report-202", matches: []string{"report-2025.pdf", "report-2026.pdf"}},
		"a shared prefix already typed names them":    {typed: root + "/report-202", want: root + "/report-202", matches: []string{"report-2025.pdf", "report-2026.pdf"}},
		"no match keeps the text":                     {typed: root + "/zzz", want: root + "/zzz"},
		"a directory that is not there keeps it":      {typed: root + "/nowhere/a", want: root + "/nowhere/a"},
		"an added space is escaped on a bare line":    {typed: root + "/My", want: root + `/My\ Pictures/`},
		"an escaped line goes on in its own spelling": {typed: root + `/My\ P`, want: root + `/My\ Pictures/`},
		"inside quotes the addition is literal":       {typed: `"` + root + `/My`, want: `"` + root + "/My Pictures/"},
		"a closed quote keeps the addition inside":    {typed: `'` + root + `/My'`, want: `'` + root + `/My Pictures/'`},
		"dot entries are hidden from a bare prefix":   {typed: root + "/", want: root + "/", matches: []string{"My Pictures", "notes.txt", "report-2025.pdf", "report-2026.pdf", "screens", "solo"}},
		"a dot prefix finds dot entries":              {typed: root + "/.h", want: root + "/.hidden"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := completePath(tc.typed, "", false)
			if got.line != tc.want {
				t.Errorf("completing %q gave %q, want %q", tc.typed, got.line, tc.want)
			}
			if strings.Join(got.matches, ",") != strings.Join(tc.matches, ",") {
				t.Errorf("completing %q named %v, want %v", tc.typed, got.matches, tc.matches)
			}
		})
	}
}

// Whatever a completion adds, the path read from the result is the path read
// before it plus the entry's own name, however the line was spelt.
func TestCompletePath_AddsExactlyTheEntryToWhatTheLineMeans(t *testing.T) {
	t.Parallel()

	root := tree(t, `odd (1) & 'x' $y.png`)
	for _, typed := range []string{root + "/od", `"` + root + "/od", `'` + root + "/od"} {
		got := completePath(typed, "", false)
		if clean := cleanPath(got.line, "", false); clean != root+`/odd (1) & 'x' $y.png` {
			t.Errorf("completing %q gave %q, which reads as %q", typed, got.line, clean)
		}
	}
}

func TestCompletePath_ATildeIsTheHomeDirectory(t *testing.T) {
	t.Parallel()

	home := tree(t, "Documents/")
	if got := completePath("~", home, false).line; got != "~/" {
		t.Errorf("a tilde alone completed to %q, want ~/", got)
	}
	if got := completePath("~/Doc", home, false).line; got != "~/Documents/" {
		t.Errorf("completing under the home gave %q, want the tilde kept", got)
	}
}

func TestCompletePath_ALinkToADirectoryIsADirectory(t *testing.T) {
	t.Parallel()

	root := tree(t, "real/")
	if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "link")); err != nil {
		t.Skipf("this filesystem makes no links: %v", err)
	}
	if got := completePath(root+"/li", "", false).line; got != root+"/link/" {
		t.Errorf("a link to a directory completed to %q", got)
	}
}

func TestCompletePath_OnWindowsAddsLiterallyWithABackslash(t *testing.T) {
	t.Parallel()

	root := tree(t, "My Pictures/")
	if got := completePath(root+"/My", "", true).line; got != root+`/My Pictures\` {
		t.Errorf("on windows the completion was %q", got)
	}
}

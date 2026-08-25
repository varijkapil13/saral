package richtext

import (
	"strings"
	"testing"
	"unicode"

	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/pkg/adf"
)

// sgr describes what the escape sequences on one line do to the terminal's
// state. A line a pane can show on its own must open every attribute it uses
// and close every attribute it opened.
type sgr struct {
	openAtEnd bool
	strayEnd  bool // a reset with nothing open before it
	notSGR    string
}

// scanSGR walks the escape sequences on a line. Anything that is not an SGR
// sequence is reported: a renderer of text has no business moving a cursor.
func scanSGR(line string) sgr {
	var out sgr
	open := false
	for at := 0; at < len(line); {
		if line[at] != 0x1b {
			at++
			continue
		}
		if at+1 >= len(line) || line[at+1] != '[' {
			out.notSGR = "an escape that is not a CSI"
			return out
		}
		end := at + 2
		for end < len(line) && (line[end] < 0x40 || line[end] > 0x7e) {
			end++
		}
		if end >= len(line) {
			out.notSGR = "an unterminated CSI"
			return out
		}
		params := line[at+2 : end]
		if line[end] != 'm' {
			out.notSGR = "CSI " + params + string(line[end])
			return out
		}
		if isReset(params) {
			if !open {
				out.strayEnd = true
			}
			open = false
		} else {
			open = true
		}
		at = end + 1
	}
	out.openAtEnd = open
	return out
}

func isReset(params string) bool {
	if params == "" {
		return true
	}
	for _, part := range strings.Split(params, ";") {
		if strings.Trim(part, "0") != "" {
			return false
		}
	}
	return true
}

// TestStyle_EveryLineIsSelfContained is the regression this package's whole line
// model exists for. ansi.Wrap does not re-open an SGR sequence on a continuation
// line, so styling before the break hands a windowed pane lines that render
// unstyled and a line that opens with a stray reset. Nothing here may do that,
// under any theme, at any width.
func TestStyle_EveryLineIsSelfContained(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"kitchen.json", "edges.json"} {
		d := load(t, name)
		for _, palette := range []Palette{plainPalette(), colourPalette()} {
			for _, markers := range []Markers{UnicodeMarkers(), ASCIIMarkers()} {
				for _, width := range []int{120, 80, 55, 40, 24, 12} {
					opt := Options{
						Width:   width,
						Styles:  NewStyles(palette),
						Markers: markers,
						Open:    map[int]bool{0: true, 1: true, 2: true},
					}
					for i, line := range Render(d, opt).Lines {
						got := scanSGR(line)
						switch {
						case got.notSGR != "":
							t.Errorf("%s at %d line %d holds %s: %q", name, width, i, got.notSGR, line)
						case got.openAtEnd:
							t.Errorf("%s at %d line %d leaves a style open, so the next line inherits it: %q",
								name, width, i, line)
						case got.strayEnd:
							t.Errorf("%s at %d line %d resets a style it never opened: %q", name, width, i, line)
						}
					}
				}
			}
		}
	}
}

// TestStyle_NoLineIsStylingAroundNothing checks the other half of that: a line
// that carries styling carries text with it, rather than an empty escape pair a
// terminal is asked to act on for no reason.
func TestStyle_NoLineIsStylingAroundNothing(t *testing.T) {
	t.Parallel()
	d := load(t, "kitchen.json")
	opt := options(60)
	opt.Styles = NewStyles(colourPalette())
	for i, line := range Render(d, opt).Lines {
		if strings.Contains(line, "\x1b") && ansi.Strip(line) == "" {
			t.Errorf("line %d is styling around nothing: %q", i, line)
		}
	}
}

// TestStyle_OneSequencePerRun is why the line model paints a run rather than
// asking lipgloss to render it: lipgloss re-styles every rune of an underlined
// or struck-through run, which is one escape pair per letter and an escape
// sequence inside any grapheme cluster the run happens to hold.
func TestStyle_OneSequencePerRun(t *testing.T) {
	t.Parallel()
	d := doc(adf.NewNode("paragraph",
		adf.NewText("a struck ", adf.NewMark("strike", nil)),
		adf.NewText("and an underlined 👨‍👩‍👧 family", adf.NewMark("underline", nil))))
	line := Render(d, options(80)).Lines[0]
	if got := strings.Count(line, "\x1b"); got > 4 {
		t.Errorf("two runs came back as %d escape sequences: %q", got, line)
	}
	if !strings.Contains(line, "👨\u200d👩\u200d👧") {
		t.Errorf("an emoji built from joiners was taken apart: %q", line)
	}
}

// TestStyle_ControlCharactersNeverReachALine is why the renderer sanitizes:
// issue text is written by anyone with a Jira login, and an escape sequence in a
// description must not be able to repaint the screen it is displayed in.
func TestStyle_ControlCharactersNeverReachALine(t *testing.T) {
	t.Parallel()
	d := load(t, "edges.json")
	for i, line := range Render(d, options(80)).Lines {
		for _, r := range ansi.Strip(line) {
			if unicode.IsControl(r) {
				t.Errorf("line %d carries the control character %q: %q", i, r, line)
			}
		}
	}
	if got := stripped(Render(d, options(80))); !strings.Contains(got, "[31m and a bell") {
		t.Errorf("the escape was dropped along with the text it was in:\n%s", got)
	}
}

// styledLine is the first line holding the word, with the styling still on it.
func styledLine(t *testing.T, r Rendered, word string) string {
	t.Helper()
	for _, line := range r.Lines {
		if strings.Contains(ansi.Strip(line), word) {
			return line
		}
	}
	t.Fatalf("no line holds %q", word)
	return ""
}

// paramsAt is the SGR in force where the word starts, as its parameters. An
// attribute and a colour arrive in one sequence, so a test asks what the
// sequence says rather than matching bytes.
func paramsAt(line, word string) []string {
	var params []string
	for at := 0; at < len(line); {
		if line[at] == 0x1b {
			end := at + 2
			for end < len(line) && (line[end] < 0x40 || line[end] > 0x7e) {
				end++
			}
			if p := line[at+2 : end]; isReset(p) {
				params = nil
			} else {
				params = strings.Split(p, ";")
			}
			at = end + 1
			continue
		}
		if strings.HasPrefix(line[at:], word) {
			return params
		}
		at++
	}
	return nil
}

func hasParam(params []string, want string) bool {
	for _, p := range params {
		if p == want {
			return true
		}
	}
	return false
}

// paintedIn reports whether the run starting at word is painted in the colour,
// which is how a test names a palette token rather than a hex value.
func paintedIn(line, word, colour string) bool {
	return strings.Contains(strings.Join(paramsAt(line, word), ";"), colour)
}

func TestStyle_MarksLandOnTheirOwnWords(t *testing.T) {
	t.Parallel()
	d := load(t, "kitchen.json")
	opt := options(300) // wide enough that a run is not split by the wrapping
	opt.Styles = NewStyles(colourPalette())
	r := Render(d, opt)

	for _, tc := range []struct {
		word, param, why string
	}{
		{"strong", "1", "strong is bold"},
		{"emphasis", "3", "em is italic"},
		{"strikethrough", "9", "strike is struck through"},
		{"underline", "4", "underline is underlined"},
	} {
		line := styledLine(t, r, tc.word)
		if !hasParam(paramsAt(line, tc.word), tc.param) {
			t.Errorf("%s: %q came out as %v", tc.why, tc.word, paramsAt(line, tc.word))
		}
	}

	// A code span is painted in the badge token, and the one text node carrying
	// both code and link keeps the badge and gains the link's underline.
	code := styledLine(t, r, "inline code")
	if !paintedIn(code, "inline code", "128;128;128") {
		t.Errorf("an inline code span is not painted in the badge token: %v", paramsAt(code, "inline code"))
	}
	linked := styledLine(t, r, "code span")
	if !paintedIn(linked, "code span", "128;128;128") || !hasParam(paramsAt(linked, "code span"), "4") {
		t.Errorf("a code span that is also a link keeps both marks: %v", paramsAt(linked, "code span"))
	}
	if line := styledLine(t, r, "a titled link"); !paintedIn(line, "a titled link", "64;64;64") {
		t.Errorf("a link is not painted in the link token: %v", paramsAt(line, "a titled link"))
	}

	// A colour off the wire is applied as it arrives.
	if line := styledLine(t, r, "coloured"); !paintedIn(line, "coloured", "255;86;48") {
		t.Errorf("a textColor mark was dropped: %v", paramsAt(line, "coloured"))
	}
	if line := styledLine(t, r, "highlighted"); !paintedIn(line, "highlighted", "48;2;254;222;200") {
		t.Errorf("a backgroundColor mark was dropped: %v", paramsAt(line, "highlighted"))
	}
}

// TestStyle_NoColorKeepsEmphasis is what NO_COLOR asks for: colour goes away and
// emphasis does not. kernel.Theme's plain mode is the precedent.
func TestStyle_NoColorKeepsEmphasis(t *testing.T) {
	t.Parallel()
	d := load(t, "kitchen.json")
	r := Render(d, options(300)) // the plain palette: attributes only

	for _, tc := range []struct {
		word, param, why string
	}{
		{"strong", "1", "strong stays bold"},
		{"emphasis", "3", "em stays italic"},
		{"underline", "4", "underline stays underlined"},
		{"strikethrough", "9", "strike stays struck through"},
		{"inline code", "7", "a code span stays a lozenge, in reverse video"},
		{"[NEEDS REVIEW]", "1", "a status lozenge stays emphasised"},
	} {
		line := styledLine(t, r, tc.word)
		if !hasParam(paramsAt(line, tc.word), tc.param) {
			t.Errorf("%s: %q came out as %v", tc.why, tc.word, paramsAt(line, tc.word))
		}
	}

	for i, line := range r.Lines {
		for _, colour := range []string{"38;2", "48;2", "38;5", "48;5"} {
			if strings.Contains(line, colour) {
				t.Errorf("line %d carries colour in a theme that has none: %q", i, line)
			}
		}
	}
}

func TestStyle_StatusCarriesItsColourEnum(t *testing.T) {
	t.Parallel()
	d := load(t, "edges.json")
	opt := options(300)
	opt.Styles = NewStyles(colourPalette())
	line := styledLine(t, Render(d, opt), "[red]")

	for _, tc := range []struct{ word, colour, token string }{
		{"[neutral]", "32;32;32", "muted"},
		{"[blue]", "64;64;64", "accent"},
		{"[purple]", "64;64;64", "accent"},
		{"[red]", "80;80;80", "danger"},
		{"[yellow]", "96;96;96", "warning"},
		{"[green]", "112;112;112", "success"},
		{"[teal]", "32;32;32", "muted, because the enum grew a value this build has not met"},
	} {
		if !paintedIn(line, tc.word, "38;2;"+tc.colour) {
			t.Errorf("%s should be painted %s and came out as %v", tc.word, tc.token, paramsAt(line, tc.word))
		}
	}
	if !strings.Contains(ansi.Strip(line), "[status]") {
		t.Errorf("a lozenge with no words still shows as one: %q", line)
	}
}

func TestStyle_PanelKindsAreToldApart(t *testing.T) {
	t.Parallel()
	m := UnicodeMarkers()
	d := load(t, "kitchen.json")
	opt := options(120)
	opt.Styles = NewStyles(colourPalette())
	got := Render(d, opt)
	plain := stripped(got)

	for _, want := range []string{
		m.Info + " INFO", m.Note + " NOTE", m.Warning + " WARNING",
		m.Success + " SUCCESS", m.Error + " ERROR", "🚀 PANEL",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("no panel rendered as %q:\n%s", want, plain)
		}
	}
	for _, tc := range []struct{ label, colour, token string }{
		{"WARNING", "96;96;96", "warning"},
		{"ERROR", "80;80;80", "danger"},
		{"SUCCESS", "112;112;112", "success"},
		{"INFO", "64;64;64", "accent"},
	} {
		if line := styledLine(t, got, tc.label); !paintedIn(line, tc.label, tc.colour) {
			t.Errorf("the %s panel is not painted %s: %v", tc.label, tc.token, paramsAt(line, tc.label))
		}
	}

	// A custom panel keeps the colour the site chose, and loses it where there
	// is no colour to be had rather than becoming an emphasis it never carried.
	if line := styledLine(t, got, "PANEL"); !paintedIn(line, "PANEL", "201;55;44") {
		t.Errorf("a custom panel dropped its panelColor: %v", paramsAt(line, "PANEL"))
	}
	if line := styledLine(t, Render(d, options(120)), "custom panel"); strings.Contains(line, "38;2") {
		t.Errorf("a custom panel painted a colour in a theme that has none: %q", line)
	}
}

func TestStyle_QuoteIsNotAPanel(t *testing.T) {
	t.Parallel()
	d := load(t, "kitchen.json")
	opt := options(120)
	opt.Styles = NewStyles(colourPalette())
	quote := styledLine(t, Render(d, opt), "A blockquote holds")
	if strings.Contains(ansi.Strip(quote), "INFO") || strings.Contains(quote, "96;96;96") {
		t.Errorf("a quote came out as a panel: %q", quote)
	}
	if !paintedIn(quote, UnicodeMarkers().VLine, "32;32;32") {
		t.Errorf("a quote's bar is painted muted: %v", paramsAt(quote, UnicodeMarkers().VLine))
	}
}

func TestStyle_UnknownNodeStaysVisiblyUnknown(t *testing.T) {
	t.Parallel()
	d := load(t, "edges.json")
	opt := options(80)
	opt.Styles = NewStyles(colourPalette())
	r := Render(d, opt)
	plain := stripped(r)
	for _, want := range []string{
		"? unsupported: futureBlock",
		"? unsupported: extension roadmap-macro",
		"? futureInline",
		"? inlineExtension chart",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("an unknown node did not say so as %q:\n%s", want, plain)
		}
	}
	line := styledLine(t, r, "unsupported: futureBlock")
	if !paintedIn(line, "?", "96;96;96") {
		t.Errorf("an unknown node reads as prose somebody wrote: %v", paramsAt(line, "?"))
	}
}

func TestStyle_MediaLeavesSomethingToFind(t *testing.T) {
	t.Parallel()
	plain := stripped(Render(load(t, "edges.json"), options(80)))
	for _, want := range []string{
		"media:3f6b1c72-9d0e-4a55-b1b7-6c6b0a9f2e41",
		"media:8a0c5d31-1b2e-4f77-9c3a-2d4e6f8a0b1c",
		"media:1c2b3a49-5d6e-4f80-9a1b-2c3d4e5f6071",
		"a screenshot",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("a media placeholder dropped %q, so a preview has nothing to resolve:\n%s", want, plain)
		}
	}
	if external := stripped(Render(load(t, "kitchen.json"), options(120))); !strings.Contains(external, "an-external-image.png") {
		t.Error("an external image dropped its URL")
	}
}

func TestStyle_TabsAreExpandedSoTheWidthIsTrue(t *testing.T) {
	t.Parallel()
	d := doc(adf.NewNode("codeBlock", adf.NewText("a\tb\n\tc")))
	r := Render(d, options(40))
	for i, line := range r.Lines {
		if strings.ContainsRune(line, '\t') {
			t.Errorf("line %d still holds a tab, which measures as one cell and prints as four: %q", i, line)
		}
		if got := ansi.StringWidth(line); got != r.Widths[i] {
			t.Errorf("line %d measures %d against a reported %d", i, got, r.Widths[i])
		}
	}
}

// TestStyle_PaddedTokenIsStillMeasured covers a caller handing in a token that
// pads: the width a pane clamps against has to include it.
func TestStyle_PaddedTokenIsStillMeasured(t *testing.T) {
	t.Parallel()
	palette := plainPalette()
	palette.Badge = palette.Badge.Padding(0, 1)
	opt := options(300)
	opt.Styles = NewStyles(palette)
	opt.Styles.Color = true // a badge with a background is what padding is for
	r := Render(load(t, "kitchen.json"), opt)
	for i, line := range r.Lines {
		if got := ansi.StringWidth(line); got != r.Widths[i] {
			t.Errorf("line %d measures %d against a reported %d: %q", i, got, r.Widths[i], line)
		}
	}
}

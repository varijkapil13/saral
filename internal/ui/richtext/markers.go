package richtext

// Markers are the glyphs put in front of a construct that plain text has no
// spelling for. Nothing here assumes a Nerd Font: the Unicode set is geometric
// and box-drawing shapes only, and the ASCII set is for the terminals and fonts
// docs/UX.md says never to assume anything about.
//
// A panel is not a blockquote, so each panel kind carries its own marker: what
// the document said about a warning is lost the moment it renders as the same
// bar an ordinary quote gets.
type Markers struct {
	Info    string
	Note    string
	Success string
	Warning string
	Error   string
	Panel   string // a panel whose kind the site chose, and whose colour is on the wire

	Decision string
	Task     string
	TaskDone string

	Media string

	Folded   string
	Unfolded string

	Bullet   string
	VLine    string
	HLine    string
	Ellipsis string

	// Cont heads a line the renderer broke rather than the author: a URL longer
	// than the pane is the usual one. Without it a reader cannot tell a break in
	// the text from a break in the layout.
	Cont string

	// Unknown heads a node type this build has never heard of. pkg/adf keeps
	// those for the round trip, and a reader who cannot see that something is
	// there cannot go and look at it in a browser.
	Unknown string
}

// UnicodeMarkers is the default set.
func UnicodeMarkers() Markers {
	return Markers{
		Info: "ℹ", Note: "✎", Success: "✓", Warning: "⚠", Error: "✗", Panel: "◆",
		Decision: "◇", Task: "☐", TaskDone: "☑",
		Media:  "▣",
		Folded: "▸", Unfolded: "▾",
		Bullet: "•", VLine: "│", HLine: "─", Ellipsis: "…",
		Cont: "↳", Unknown: "?",
	}
}

// ASCIIMarkers is the fallback for a terminal or font that cannot be trusted
// with anything else.
func ASCIIMarkers() Markers {
	return Markers{
		Info: "i", Note: "*", Success: "+", Warning: "!", Error: "x", Panel: "#",
		Decision: "<>", Task: "[ ]", TaskDone: "[x]",
		Media:  "[]",
		Folded: ">", Unfolded: "v",
		Bullet: "-", VLine: "|", HLine: "-", Ellipsis: "...",
		Cont: "\\", Unknown: "?",
	}
}

// ascii reports whether the ASCII set is in force. The set is identified by one
// of its members rather than by a flag a caller could forget to set, which is
// how kernel.Theme's glyph set is identified too.
func (m Markers) ascii() bool { return m.VLine == ASCIIMarkers().VLine }

// withDefaults fills the markers a caller left empty, so that a zero Options
// still renders a bullet rather than nothing.
func (m Markers) withDefaults() Markers {
	d := UnicodeMarkers()
	for _, f := range [...]struct {
		at   *string
		want string
	}{
		{&m.Info, d.Info}, {&m.Note, d.Note}, {&m.Success, d.Success},
		{&m.Warning, d.Warning}, {&m.Error, d.Error}, {&m.Panel, d.Panel},
		{&m.Decision, d.Decision}, {&m.Task, d.Task}, {&m.TaskDone, d.TaskDone},
		{&m.Media, d.Media}, {&m.Folded, d.Folded}, {&m.Unfolded, d.Unfolded},
		{&m.Bullet, d.Bullet}, {&m.VLine, d.VLine}, {&m.HLine, d.HLine},
		{&m.Ellipsis, d.Ellipsis}, {&m.Cont, d.Cont}, {&m.Unknown, d.Unknown},
	} {
		if *f.at == "" {
			*f.at = f.want
		}
	}
	return m
}

package adf

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// glyphs are the markers the renderer puts in front of things markdown has no
// spelling for. Nothing here may assume a Nerd Font.
type glyphs struct {
	info     string
	note     string
	success  string
	warning  string
	failure  string
	custom   string
	decision string
	expand   string
	ellipsis string
}

func glyphsFor(ascii bool) glyphs {
	if ascii {
		return glyphs{
			info: "i", note: "#", success: "+", warning: "!", failure: "x",
			custom: "<>", decision: "<>", expand: "v", ellipsis: "...",
		}
	}
	return glyphs{
		info: "ℹ", note: "✎", success: "✓", warning: "⚠", failure: "✗",
		custom: "◆", decision: "◇", expand: "▾", ellipsis: "…",
	}
}

func (w *writer) inline(nodes []Node) {
	for i := range nodes {
		w.inlineNode(nodes[i])
	}
}

func (w *writer) inlineNode(n Node) {
	switch n.Type {
	case "text":
		w.emit(marked(sanitize(n.Text), n.Marks))
	case "hardBreak":
		w.startLine()
		w.endLine()
	case "mention":
		w.emit(mentionText(n.Attrs))
	case "status":
		w.emit(statusText(n.Attrs))
	case "emoji":
		w.emit(emojiText(n.Attrs))
	case "date":
		w.emit(w.dateText(n.Attrs))
	case "inlineCard", "inlineEmbedCard":
		w.emit(w.cardText(n))
	case "media", "mediaInline":
		w.emit(w.mediaText(n))
	case "placeholder":
		if text, ok := attrString(n.Attrs, "text"); ok {
			w.emit(sanitize(text))
		}
	default:
		w.unknownInline(n)
	}
}

func (w *writer) unknownInline(n Node) {
	w.emit("[unsupported: " + originalType(n))
	switch {
	case len(n.Content) > 0:
		w.emit(": ")
		w.inline(n.Content)
	case n.Text != "":
		w.emit(": " + sanitize(n.Text))
	}
	w.emit("]")
}

// mentionText renders a mention from the display text the editor stored with
// it. The account id behind it means nothing to a reader.
func mentionText(a Attrs) string {
	text, _ := attrString(a, "text")
	text = sanitize(strings.TrimSpace(text))
	if text == "" {
		if id, ok := attrString(a, "id"); ok {
			text = sanitize(id)
		}
	}
	if text == "" {
		return "@?"
	}
	if strings.HasPrefix(text, "@") {
		return text
	}
	return "@" + text
}

// statusText renders a lozenge as bracketed text. Its colour is styling, which
// this package does not do, and its wording is whatever the author typed —
// never a status this client resolved, so it is not localised here either.
func statusText(a Attrs) string {
	text, _ := attrString(a, "text")
	text = sanitize(strings.TrimSpace(text))
	if text == "" {
		return "[status]"
	}
	return "[" + text + "]"
}

func emojiText(a Attrs) string {
	if text, ok := attrString(a, "text"); ok && text != "" {
		return sanitize(text)
	}
	if short, ok := attrString(a, "shortName"); ok && short != "" {
		return sanitize(short)
	}
	if id, ok := attrString(a, "id"); ok && id != "" {
		return ":" + sanitize(id) + ":"
	}
	return ""
}

// dateText renders the instant a date node carries, which arrives as epoch
// milliseconds in a string.
func (w *writer) dateText(a Attrs) string {
	stamp, _ := attrString(a, "timestamp")
	ms, err := strconv.ParseInt(strings.TrimSpace(stamp), 10, 64)
	if err != nil {
		if stamp == "" {
			return ""
		}
		return sanitize(stamp)
	}
	loc := w.opt.Location
	if loc == nil {
		loc = time.UTC
	}
	return time.UnixMilli(ms).In(loc).Format(time.DateOnly)
}

// cardText renders a smart link. Jira resolves these server-side for the
// browser; over the API only the URL comes back, so the URL is what a reader
// gets.
func (w *writer) cardText(n Node) string {
	url, _ := attrString(n.Attrs, "url")
	name := ""
	if data, ok := n.Attrs["data"].(map[string]any); ok {
		if url == "" {
			url, _ = data["url"].(string)
		}
		name, _ = data["name"].(string)
	}
	url = sanitize(strings.TrimSpace(url))
	name = sanitize(strings.TrimSpace(name))
	switch {
	case url == "" && name == "":
		return "[unsupported: " + originalType(n) + "]"
	case url == "":
		return name
	case name == "" || name == url:
		return "<" + url + ">"
	default:
		return link(name, url)
	}
}

// mediaText renders an attachment reference. A media node carries an id and a
// collection rather than a filename, so the id is kept: it is what a later
// packet resolves against the attachment list.
func (w *writer) mediaText(n Node) string {
	alt, _ := attrString(n.Attrs, "alt")
	alt = sanitize(strings.TrimSpace(alt))
	if alt == "" {
		alt = "media"
	}
	target, _ := attrString(n.Attrs, "url")
	if target == "" {
		if id, ok := attrString(n.Attrs, "id"); ok && id != "" {
			target = "media:" + id
		}
	}
	if target == "" {
		return "[unsupported: " + originalType(n) + "]"
	}
	return "!" + link(alt, sanitize(strings.TrimSpace(target)))
}

// angles is what an address has to have escaped before it can be wrapped in
// angle brackets, which is what holds a link together when the address itself
// contains a bracket or a space.
var angles = strings.NewReplacer("<", "%3C", ">", "%3E")

func link(text, url string) string {
	if strings.ContainsAny(url, " ()<>") {
		return "[" + text + "](<" + angles.Replace(url) + ">)"
	}
	return "[" + text + "](" + url + ")"
}

// marked wraps text in its marks in a fixed order, innermost first. Marks come
// off the wire in ProseMirror rank order, which is byte-significant and means
// nothing, so strong inside em has to render as em inside strong does.
func marked(s string, marks []Mark) string {
	if s == "" || len(marks) == 0 {
		return s
	}
	var code, underline, strike, em, strong bool
	href := ""
	for i := range marks {
		switch marks[i].Type {
		case "code":
			code = true
		case "underline":
			underline = true
		case "strike":
			strike = true
		case "em":
			em = true
		case "strong":
			strong = true
		case "link":
			if href == "" {
				href, _ = attrString(marks[i].Attrs, "href")
				href = strings.TrimSpace(href)
			}
		}
	}
	if !code && !underline && !strike && !em && !strong && href == "" {
		return s
	}

	// Emphasis around leading or trailing spaces is not emphasis; keep the
	// spaces outside the markers.
	core := strings.TrimRight(strings.TrimLeft(s, " \t"), " \t")
	if core == "" {
		return s
	}
	lead, trail := s[:len(s)-len(strings.TrimLeft(s, " \t"))], s[len(strings.TrimRight(s, " \t")):]

	if code {
		core = codeSpan(core)
	}
	for _, m := range [...]struct {
		on   bool
		wrap string
	}{
		{underline, "_"},
		{strike, "~~"},
		{em, "*"},
		{strong, "**"},
	} {
		if m.on {
			core = m.wrap + core + m.wrap
		}
	}
	if href != "" {
		href = sanitize(href)
		if core == href {
			core = "<" + href + ">"
		} else {
			core = link(core, href)
		}
	}
	return lead + core + trail
}

// codeSpan fences inline code in enough backticks to hold whatever is inside
// it, padding when the content would otherwise touch a fence.
func codeSpan(s string) string {
	fence := strings.Repeat("`", fenceRun(s)+1)
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") {
		return fence + " " + s + " " + fence
	}
	return fence + s + fence
}

func fenceRun(s string) int {
	longest, run := 0, 0
	for i := range len(s) {
		if s[i] != '`' {
			run = 0
			continue
		}
		run++
		if run > longest {
			longest = run
		}
	}
	return longest
}

// sanitize drops the control characters a terminal would act on. Issue text is
// written by anyone with a Jira login, and an escape sequence in a description
// must not be able to repaint the screen it is displayed in.
func sanitize(s string) string {
	if !hasControl(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if isControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func hasControl(s string) bool {
	for _, r := range s {
		if isControl(r) {
			return true
		}
	}
	return false
}

func isControl(r rune) bool {
	switch {
	case r == '\n' || r == '\t':
		return false
	case r < 0x20 || r == 0x7f:
		return true
	case r >= 0x80 && r <= 0x9f:
		return true
	default:
		return false
	}
}

// textOf concatenates the text of a run of nodes, which is how a code block
// carries its lines.
func textOf(nodes []Node) string {
	if len(nodes) == 1 {
		return sanitize(nodes[0].Text)
	}
	var b strings.Builder
	for i := range nodes {
		b.WriteString(nodes[i].Text)
	}
	return sanitize(b.String())
}

func isInline(typ string) bool {
	switch typ {
	case "text", "hardBreak", "mention", "status", "emoji", "date",
		"inlineCard", "inlineEmbedCard", "media", "mediaInline",
		"placeholder", "inlineExtension", "unsupportedInline":
		return true
	default:
		return false
	}
}

func inlineOnly(nodes []Node) bool {
	for i := range nodes {
		if !isInline(nodes[i].Type) {
			return false
		}
	}
	return len(nodes) > 0
}

// attrString reads a string attribute, reporting whether it was there and of
// that type — an attribute this package models can still arrive as something
// else, and a document that does that must still render.
func attrString(a Attrs, key string) (string, bool) {
	v, ok := a[key].(string)
	return v, ok
}

// attrInt reads a numeric attribute. JSON numbers decode to float64, but a
// document built in Go carries whatever the caller put there.
func attrInt(a Attrs, key string) (int, bool) {
	switch v := a[key].(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	case int64:
		return int(v), true
	case json.Number:
		n, err := v.Int64()
		return int(n), err == nil
	default:
		return 0, false
	}
}

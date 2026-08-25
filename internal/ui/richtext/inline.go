package richtext

import (
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/varijkapil13/saral/pkg/adf"
)

// inline collects the spans for a run of inline nodes. Nothing is wrapped here:
// the spans are handed to the line builder, which breaks them and styles each
// line on its own.
func (f *frame) inline(nodes []adf.Node) {
	for i := range nodes {
		f.inlineNode(nodes[i])
	}
}

func (f *frame) inlineNode(n adf.Node) {
	switch n.Type {
	case "text":
		f.markedText(n)
	case "hardBreak":
		f.spans = append(f.spans, span{brk: true})
	case "mention":
		f.add(mentionText(n.Attrs), &f.sty.Mention)
	case "status":
		text, colour := statusParts(n.Attrs)
		f.add(text, f.sty.status(colour))
	case "emoji":
		f.addText(emojiText(n.Attrs))
	case "date":
		f.add(f.dateText(n.Attrs), &f.sty.Date)
	case "inlineCard", "inlineEmbedCard":
		f.cardSpans(n)
	case "media", "mediaInline":
		f.add(f.mk.Media+" "+mediaTarget(n), &f.sty.Media)
	case "placeholder":
		if text, ok := attrString(n.Attrs, "text"); ok {
			f.add(text, &f.sty.Muted)
		}
	default:
		f.unknownSpans(n)
	}
}

// add appends one run of text, breaking it where the text itself breaks: a
// newline inside a text node is a line break rather than a character a terminal
// should be asked to print.
func (f *frame) add(text string, style *lipgloss.Style) {
	f.addPaint(text, f.paint(style))
}

// addText is the same for prose taking the style of the block it is in, which
// was asked for its sequences when the block set it.
func (f *frame) addText(text string) { f.addPaint(text, f.pctx) }

func (f *frame) addPaint(text string, p paint) {
	if text == "" {
		return
	}
	for {
		before, after, more := strings.Cut(text, "\n")
		if clean := expandTabs(sanitize(before), 0); clean != "" {
			f.spans = append(f.spans, span{text: clean, p: p})
		}
		if !more {
			return
		}
		f.spans = append(f.spans, span{brk: true})
		text = after
	}
}

// markedText renders one text node under its marks. sub and sup are spelled out
// rather than dropped: a terminal has no raised digit, and H2O read as H2O is
// wrong in a way a reader cannot see.
func (f *frame) markedText(n adf.Node) {
	m := gatherMarks(n.Marks)
	if !m.any() && !m.sub && !m.sup {
		f.addText(n.Text)
		return
	}
	p := f.markPaint(m)
	switch {
	case m.sup:
		f.addPaint("^"+n.Text, p)
	case m.sub:
		f.addPaint("_"+n.Text, p)
	default:
		f.addPaint(n.Text, p)
	}
	// The address is shown rather than hidden behind the words: a terminal
	// cannot be relied on to make a link clickable, and a link whose address a
	// reader cannot see is one they cannot follow.
	if m.href != "" && strings.TrimSpace(n.Text) != m.href {
		f.add(" ("+m.href+")", &f.sty.URL)
	}
}

func gatherMarks(list []adf.Mark) marks {
	var m marks
	for i := range list {
		switch list[i].Type {
		case "strong":
			m.strong = true
		case "em":
			m.em = true
		case "underline":
			m.underline = true
		case "strike":
			m.strike = true
		case "code":
			m.code = true
		case "link":
			if m.href == "" {
				href, _ := attrString(list[i].Attrs, "href")
				m.href = sanitize(strings.TrimSpace(href))
			}
		case "subsup":
			kind, _ := attrString(list[i].Attrs, "type")
			if kind == "sub" {
				m.sub = true
			} else {
				m.sup = true
			}
		case "textColor":
			m.fg, _ = attrString(list[i].Attrs, "color")
		case "backgroundColor":
			m.bg, _ = attrString(list[i].Attrs, "color")
		}
	}
	return m
}

// blockMarks reads the marks ADF puts on a block rather than on its text.
func blockMarks(list []adf.Mark) (a align, level int) {
	a, level = alignLeft, 0
	for i := range list {
		switch list[i].Type {
		case "alignment":
			switch kind, _ := attrString(list[i].Attrs, "align"); kind {
			case "center":
				a = alignCenter
			case "end", "right":
				a = alignRight
			}
		case "indentation":
			if n, ok := attrInt(list[i].Attrs, "level"); ok && n > 0 {
				level = min(n, 6)
			}
		}
	}
	return a, level
}

// mentionText renders a mention from the display text the editor stored with
// it. The account id behind it means nothing to a reader.
func mentionText(a adf.Attrs) string {
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

// statusParts renders a lozenge and hands back the colour enum it carries, so
// that the colour the author chose is the colour a reader sees. The brackets
// stay whatever the theme does: they are what is left of the lozenge once the
// colour is stripped, and a no-colour terminal has nothing else.
func statusParts(a adf.Attrs) (text, colour string) {
	text, _ = attrString(a, "text")
	text = sanitize(strings.TrimSpace(text))
	colour, _ = attrString(a, "color")
	if text == "" {
		return "[status]", colour
	}
	return "[" + text + "]", colour
}

func emojiText(a adf.Attrs) string {
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
func (f *frame) dateText(a adf.Attrs) string {
	stamp, _ := attrString(a, "timestamp")
	ms, err := strconv.ParseInt(strings.TrimSpace(stamp), 10, 64)
	if err != nil {
		return sanitize(stamp)
	}
	loc := f.opt.Location
	if loc == nil {
		loc = time.UTC
	}
	return time.UnixMilli(ms).In(loc).Format(time.DateOnly)
}

// cardSpans renders a smart link. Jira resolves these server-side for the
// browser; over the API only the URL comes back, so the URL is what a reader
// gets.
func (f *frame) cardSpans(n adf.Node) {
	url, name := cardParts(n)
	switch {
	case url == "" && name == "":
		f.unknownSpans(n)
	case name == "" || name == url:
		f.add(url, &f.sty.Card)
	default:
		f.add(name, &f.sty.Card)
		f.add(" ("+url+")", &f.sty.URL)
	}
}

func cardParts(n adf.Node) (url, name string) {
	url, _ = attrString(n.Attrs, "url")
	if data, ok := n.Attrs["data"].(map[string]any); ok {
		if url == "" {
			url, _ = data["url"].(string)
		}
		name, _ = data["name"].(string)
	}
	return sanitize(strings.TrimSpace(url)), sanitize(strings.TrimSpace(name))
}

// mediaTarget names an attachment or an image as a placeholder rather than as a
// broken link. The id is what a preview resolves against the attachment list,
// so the line has to leave something to find.
func mediaTarget(n adf.Node) string {
	alt, _ := attrString(n.Attrs, "alt")
	alt = sanitize(strings.TrimSpace(alt))
	target, _ := attrString(n.Attrs, "url")
	target = sanitize(strings.TrimSpace(target))
	if target == "" {
		if id, ok := attrString(n.Attrs, "id"); ok && id != "" {
			target = "media:" + sanitize(id)
		}
	}
	switch {
	case alt == "" && target == "":
		return "media"
	case alt == "":
		return target
	case target == "":
		return alt
	default:
		return alt + " (" + target + ")"
	}
}

// unknownSpans shows that a node is there, and what it holds, without
// pretending to know how it should look. pkg/adf keeps such a node for the
// round trip; a reader who cannot see that something is there cannot go and
// look at it in a browser.
func (f *frame) unknownSpans(n adf.Node) {
	f.add(f.mk.Unknown+" "+nodeName(n), &f.sty.Unknown)
	switch {
	case len(n.Content) > 0:
		f.add(": ", &f.sty.Unknown)
		f.inline(n.Content)
	case n.Text != "":
		f.addText(": " + n.Text)
	}
}

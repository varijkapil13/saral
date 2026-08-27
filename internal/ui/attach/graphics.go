package attach

import (
	"bytes"
	"context"
	"encoding/base64"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/pkg/jira"
)

// kittyChunk is how much base64 goes in one escape sequence of the kitty
// protocol. The protocol's own limit is 4096 bytes of payload per chunk.
const kittyChunk = 4096

type previewKind uint8

const (
	previewNone previewKind = iota
	// previewInline is the terminal's own graphics protocol: the bytes ride in
	// an escape sequence and the terminal draws the pixels.
	previewInline
	// previewCells is an image approximated in characters, which is what chafa
	// hands back.
	previewCells
	// previewText is the last resort: what the file is called and how big it is.
	previewText
)

type preview struct {
	id     string
	kind   previewKind
	escape string
	lines  []string
	// why says what stopped this being a picture, in a sentence, so that a pane
	// showing a name instead of an image is not a pane that looks broken.
	why string
}

// previewBox is the room the preview has, in cells. Both graphics protocols are
// told the geometry rather than measuring it, so a preview belongs to one box and
// is thrown away when the box changes.
type previewBox struct{ width, height int }

// rasterExts are the extensions worth trying to draw. MimeType can be empty or
// application/octet-stream on a real site, so the filename is the second opinion
// the port's own notes ask for.
var rasterExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".bmp": true,
	".webp": true, ".tif": true, ".tiff": true, ".svg": true,
}

// isImage reports whether this is worth trying to draw rather than hand to the desktop.
func isImage(att jira.Attachment) bool {
	if strings.HasPrefix(strings.ToLower(att.MimeType), "image/") {
		return true
	}
	return rasterExts[strings.ToLower(filepath.Ext(att.Filename))]
}

// draw is the degradation, best first: the terminal's own graphics protocol,
// then half-blocks from chafa, then the name and the size.
//
// Each rung falls to the next with the reason it could not be taken, because a
// pane that silently shows a filename where a picture was expected is one nobody
// can tell from a broken one.
func draw(ctx context.Context, t tools, att jira.Attachment, path string, box previewBox,
	mode jira.GraphicsMode,
) preview {
	p := preview{id: att.ID, kind: previewText}
	if box.width <= 0 || box.height <= 0 {
		return p
	}
	switch mode {
	case jira.GraphicsKitty:
		got, err := readAtMost(path, previewLimit)
		switch {
		case err != nil:
			p.why = err.Error()
		case !isPNG(got):
			// f=100 is the only encoded format kitty takes, and claiming it for
			// bytes that are not a PNG paints the escape sequence over the frame.
			p.why = "this terminal draws PNG inline and " + att.Filename + " is not one"
		default:
			return inline(att.ID, kittyEscape(got, box), box)
		}
	case jira.GraphicsITerm2:
		got, err := readAtMost(path, previewLimit)
		switch {
		case err != nil:
			p.why = err.Error()
		case !isInlineImage(got):
			p.why = att.Filename + " is not a PNG, a JPEG or a GIF, which is what this terminal draws inline"
		default:
			return inline(att.ID, iterm2Escape(got, box), box)
		}
	case jira.GraphicsNone, jira.GraphicsHalfBlocks:
		p.why = "this terminal reported no way to draw an image"
	}

	if t.chafa == "" {
		if mode == jira.GraphicsHalfBlocks || mode == jira.GraphicsNone {
			p.why = "chafa is not installed, and this terminal has no graphics protocol"
		}
		return p
	}
	lines, err := chafaLines(ctx, t, path, box)
	if err != nil {
		p.why = "chafa could not draw " + att.Filename
		return p
	}
	if len(lines) == 0 {
		p.why = "chafa drew nothing"
		return p
	}
	return preview{id: att.ID, kind: previewCells, lines: lines}
}

// inline is a preview the terminal paints itself. The escape goes on the first
// line and the rows it covers follow it as blanks, built here rather than per
// frame: the pane draws this many times and the escape is decided once.
func inline(id, escape string, box previewBox) preview {
	lines := make([]string, box.height)
	lines[0] = escape
	return preview{id: id, kind: previewInline, escape: escape, lines: lines}
}

// chafaLines asks chafa for the image as characters, in exactly the box the pane
// has. Nothing user-typed reaches the argument list: the path is one this program
// built under its own cache directory.
func chafaLines(ctx context.Context, t tools, path string, box previewBox) ([]string, error) {
	out, err := t.run(ctx, t.chafa,
		"--format=symbols",
		"--symbols=vhalf",
		"--size="+strconv.Itoa(box.width)+"x"+strconv.Itoa(box.height),
		path,
	)
	if err != nil {
		return nil, err
	}
	text := strings.TrimRight(string(out), "\n")
	if text == "" {
		return nil, nil
	}
	lines := strings.Split(text, "\n")
	if len(lines) > box.height {
		lines = lines[:box.height]
	}
	for i, line := range lines {
		lines[i] = ansi.Truncate(strings.TrimRight(line, "\r"), box.width, "")
	}
	return lines, nil
}

// kittyEscape carries the file to a terminal speaking kitty's graphics protocol.
// The payload is chunked because the protocol takes 4096 bytes of it at a time,
// and the geometry is given in cells so that the terminal scales rather than the
// pane guessing at pixels.
func kittyEscape(data []byte, box previewBox) string {
	payload := base64.StdEncoding.EncodeToString(data)
	var b strings.Builder
	b.Grow(len(payload) + 64)
	first := true
	for len(payload) > 0 {
		chunk := payload
		if len(chunk) > kittyChunk {
			chunk = chunk[:kittyChunk]
		}
		payload = payload[len(chunk):]
		more := "0"
		if len(payload) > 0 {
			more = "1"
		}
		b.WriteString("\x1b_G")
		if first {
			b.WriteString("f=100,a=T,t=d,c=" + strconv.Itoa(box.width) +
				",r=" + strconv.Itoa(box.height) + ",")
			first = false
		}
		b.WriteString("m=" + more + ";")
		b.WriteString(chunk)
		b.WriteString("\x1b\\")
	}
	return b.String()
}

// iterm2Escape carries the file to iTerm2, which takes the whole thing in one
// sequence and scales it into the cells it is given.
func iterm2Escape(data []byte, box previewBox) string {
	return "\x1b]1337;File=inline=1;preserveAspectRatio=1" +
		";width=" + strconv.Itoa(box.width) +
		";height=" + strconv.Itoa(box.height) +
		";size=" + strconv.Itoa(len(data)) +
		":" + base64.StdEncoding.EncodeToString(data) + "\a"
}

var (
	pngMagic  = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	jpegMagic = []byte{0xff, 0xd8, 0xff}
)

// isPNG reads the bytes rather than trusting the name or the media type. A
// graphics escape claiming a format the bytes are not paints itself over the
// frame, and both the extension and the MimeType are whatever the uploader's
// machine said.
func isPNG(data []byte) bool { return bytes.HasPrefix(data, pngMagic) }

func isInlineImage(data []byte) bool {
	switch {
	case isPNG(data), bytes.HasPrefix(data, jpegMagic):
		return true
	case bytes.HasPrefix(data, []byte("GIF87a")), bytes.HasPrefix(data, []byte("GIF89a")):
		return true
	}
	return false
}

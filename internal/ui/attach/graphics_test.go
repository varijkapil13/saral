package attach

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/varijkapil13/saral/pkg/jira"
)

func wrote(t *testing.T, name string, body []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func jpegBytes() []byte {
	return append([]byte{0xff, 0xd8, 0xff, 0xe0}, []byte("JFIF-and-then-some-pixels")...)
}

// The degradation order, rung by rung: the terminal's own protocol, then chafa's
// half-blocks, then the name and the size. Every fall-through carries the reason,
// because a filename where a picture was expected is otherwise indistinguishable
// from a broken pane.
func TestDraw_FallsThroughTheDegradationOrderWithTheReason(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		mode  jira.GraphicsMode
		file  string
		body  []byte
		chafa string
		want  previewKind
		why   string
	}{
		"kitty draws a PNG itself": {
			mode: jira.GraphicsKitty, file: "shot.png", body: pngBytes(), want: previewInline,
		},
		"kitty falls to chafa for a JPEG, which its encoded format does not cover": {
			mode: jira.GraphicsKitty, file: "shot.jpg", body: jpegBytes(),
			chafa: "chafa", want: previewCells,
		},
		"kitty with no chafa says why a JPEG is not drawn": {
			mode: jira.GraphicsKitty, file: "shot.jpg", body: jpegBytes(),
			want: previewText, why: "draws PNG inline",
		},
		"iterm2 draws a JPEG itself": {
			mode: jira.GraphicsITerm2, file: "shot.jpg", body: jpegBytes(), want: previewInline,
		},
		"iterm2 draws a PNG itself": {
			mode: jira.GraphicsITerm2, file: "shot.png", body: pngBytes(), want: previewInline,
		},
		"iterm2 falls through for something that is neither": {
			mode: jira.GraphicsITerm2, file: "shot.png", body: []byte("BM-not-really"),
			want: previewText, why: "not a PNG, a JPEG or a GIF",
		},
		"a colour terminal with chafa gets half-blocks": {
			mode: jira.GraphicsHalfBlocks, file: "shot.png", body: pngBytes(),
			chafa: "chafa", want: previewCells,
		},
		"a colour terminal without chafa gets the name": {
			mode: jira.GraphicsHalfBlocks, file: "shot.png", body: pngBytes(),
			want: previewText, why: "chafa is not installed",
		},
		"a terminal that can draw nothing at all gets the name": {
			mode: jira.GraphicsNone, file: "shot.png", body: pngBytes(),
			want: previewText, why: "chafa is not installed",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tools, seen := testTools(t)
			tools.chafa = tc.chafa
			path := wrote(t, tc.file, tc.body)
			att := jira.Attachment{ID: "att-1", Filename: tc.file, Size: int64(len(tc.body))}

			got := draw(t.Context(), tools, att, path, previewBox{width: 40, height: 12}, tc.mode)

			if got.kind != tc.want {
				t.Fatalf("drew kind %d, want %d (why: %q)", got.kind, tc.want, got.why)
			}
			if got.id != att.ID {
				t.Errorf("the preview claims attachment %q, want %q", got.id, att.ID)
			}
			if tc.why != "" && !strings.Contains(got.why, tc.why) {
				t.Errorf("the reason is %q, want it to say %q", got.why, tc.why)
			}
			if tc.want == previewCells && len(seen.argv()) != 1 {
				t.Errorf("half-blocks ran %d processes, want one", len(seen.argv()))
			}
			if tc.want != previewCells && len(seen.argv()) != 0 {
				t.Errorf("a rung that draws no half-blocks still ran %v", seen.argv())
			}
		})
	}
}

// The name and the media type both come from whoever uploaded the file, so the
// bytes are what a protocol claim is made against. Claiming PNG for something
// else paints the escape sequence over the frame.
func TestDraw_NeverClaimsAProtocolForBytesThatAreNotThatFormat(t *testing.T) {
	t.Parallel()

	tools, _ := testTools(t)
	body := []byte("this is a text file wearing a png extension")
	path := wrote(t, "liar.png", body)
	att := jira.Attachment{
		ID: "att-1", Filename: "liar.png", MimeType: "image/png", Size: int64(len(body)),
	}

	for _, mode := range []jira.GraphicsMode{jira.GraphicsKitty, jira.GraphicsITerm2} {
		got := draw(t.Context(), tools, att, path, previewBox{width: 40, height: 12}, mode)
		if got.kind == previewInline {
			t.Errorf("%s claimed an inline image for bytes that are not one", mode)
		}
		if got.escape != "" {
			t.Errorf("%s produced an escape sequence for bytes that are not an image", mode)
		}
	}
}

func TestKittyEscape_ChunksThePayloadAndNamesTheGeometryOnce(t *testing.T) {
	t.Parallel()

	body := make([]byte, 7000)
	for i := range body {
		body[i] = byte(i)
	}
	got := kittyEscape(body, previewBox{width: 37, height: 11})

	if want := "f=100,a=T,t=d,c=37,r=11,"; !strings.Contains(got, want) {
		t.Errorf("the escape does not carry %q:\n%q", want, got[:min(len(got), 120)])
	}
	if n := strings.Count(got, "f=100"); n != 1 {
		t.Errorf("the format and geometry appear %d times; they belong on the first chunk only", n)
	}
	chunks := strings.Count(got, "\x1b_G")
	if want := (base64.StdEncoding.EncodedLen(len(body)) + kittyChunk - 1) / kittyChunk; chunks != want {
		t.Errorf("the payload went in %d chunks, want %d of at most %d bytes", chunks, want, kittyChunk)
	}
	if !strings.Contains(got, "m=0;") {
		t.Error("no chunk is marked as the last, so the terminal waits for one that never comes")
	}
	if strings.Count(got, "m=1;") != chunks-1 {
		t.Errorf("%d chunks are marked as continuing, want %d", strings.Count(got, "m=1;"), chunks-1)
	}
	// The payload has to survive the chunking, or the terminal draws part of an
	// image and reports nothing wrong.
	payload := strings.NewReplacer("\x1b\\", "", "\x1b_G", "").Replace(got)
	for _, prefix := range []string{"f=100,a=T,t=d,c=37,r=11,m=1;", "m=1;", "m=0;"} {
		payload = strings.ReplaceAll(payload, prefix, "")
	}
	if payload != base64.StdEncoding.EncodeToString(body) {
		t.Error("the chunks do not reassemble into the file")
	}
}

func TestITerm2Escape_CarriesTheByteCountAndTheGeometry(t *testing.T) {
	t.Parallel()

	body := pngBytes()
	got := iterm2Escape(body, previewBox{width: 37, height: 11})

	for _, want := range []string{
		"\x1b]1337;File=inline=1", "preserveAspectRatio=1", "width=37", "height=11",
		"size=" + strconv.Itoa(len(body)), base64.StdEncoding.EncodeToString(body),
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the escape does not carry %q", want)
		}
	}
	if !strings.HasSuffix(got, "\a") {
		t.Error("the escape is not terminated, so the terminal swallows the rest of the frame")
	}
}

// A graphics escape has to measure as nothing: it occupies cells the terminal
// paints, and a frame that counted its bytes as columns would wrap.
func TestEscapes_MeasureAsNoColumnsAtAll(t *testing.T) {
	t.Parallel()

	dr, _ := loadedPane(t)
	dr.onto("screenshot.png")
	att := dr.m.files[0]
	box := dr.m.previewBox()
	dr.m.shown = inline(att.ID, kittyEscape(pngBytes(), box), box)

	frame := dr.m.View()
	if !strings.Contains(frame, "\x1b_G") {
		t.Fatal("the escape did not reach the frame")
	}
	for i, line := range strings.Split(frame, "\n") {
		if got := lipglossWidth(line); got > dr.m.width {
			t.Errorf("line %d measures %d columns against a width of %d", i, got, dr.m.width)
		}
	}
}

func TestChafa_IsGivenTheBoxAndAPathOfOurOwn(t *testing.T) {
	t.Parallel()

	tools, seen := testTools(t)
	tools.chafa = "/usr/local/bin/chafa"
	path := wrote(t, "shot.gif", []byte("GIF89a-pixels"))
	att := jira.Attachment{ID: "att-1", Filename: "shot.gif", Size: 13}

	got := draw(t.Context(), tools, att, path, previewBox{width: 31, height: 9}, jira.GraphicsHalfBlocks)
	if got.kind != previewCells {
		t.Fatalf("drew kind %d, want half-blocks (why: %q)", got.kind, got.why)
	}

	argv := seen.argv()
	if len(argv) != 1 {
		t.Fatalf("ran %d processes, want one: %v", len(argv), argv)
	}
	if argv[0][0] != tools.chafa {
		t.Errorf("ran %q, want the renderer that was found", argv[0][0])
	}
	if want := "--size=31x9"; !strings.Contains(strings.Join(argv[0], " "), want) {
		t.Errorf("the renderer was not told the box: %v", argv[0])
	}
	if last := argv[0][len(argv[0])-1]; last != path {
		t.Errorf("the renderer was given %q, want the file this program wrote", last)
	}
}

// chafa is given a box and still has to be held to it: a renderer that answered
// with more lines than the pane has would push the prompt off the bottom.
func TestChafa_OutputIsCutToTheBoxItWasGiven(t *testing.T) {
	t.Parallel()

	tools, seen := testTools(t)
	tools.chafa = "chafa"
	seen.out = []byte(strings.Repeat(strings.Repeat("#", 80)+"\n", 30))
	path := wrote(t, "shot.png", pngBytes())
	att := jira.Attachment{ID: "att-1", Filename: "shot.png", Size: 4}

	got := draw(t.Context(), tools, att, path, previewBox{width: 20, height: 6}, jira.GraphicsHalfBlocks)
	if len(got.lines) != 6 {
		t.Errorf("the renderer's %d lines were not cut to the 6 the pane has", len(got.lines))
	}
	for i, line := range got.lines {
		if got := lipglossWidth(line); got > 20 {
			t.Errorf("line %d is %d columns wide against a box of 20", i, got)
		}
	}
}

func TestIsImage_ReadsTheMediaTypeAndFallsBackToTheExtension(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		att  jira.Attachment
		want bool
	}{
		"a media type says so":                     {jira.Attachment{MimeType: "image/webp", Filename: "x"}, true},
		"an upper-case media type says so":         {jira.Attachment{MimeType: "IMAGE/PNG", Filename: "x"}, true},
		"no media type, and the extension says so": {jira.Attachment{Filename: "shot.PNG"}, true},
		"octet-stream, and the extension says so":  {jira.Attachment{MimeType: "application/octet-stream", Filename: "a.jpeg"}, true},
		"a document":                         {jira.Attachment{MimeType: "application/pdf", Filename: "a.pdf"}, false},
		"no media type and no extension":     {jira.Attachment{Filename: "capture"}, false},
		"a name that only mentions an image": {jira.Attachment{Filename: "png-notes.txt"}, false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := isImage(tc.att); got != tc.want {
				t.Errorf("isImage(%+v) = %v", tc.att, got)
			}
		})
	}
}

func TestDraw_ABoxWithNoRoomDrawsTheNameRatherThanAskingAnythingOfTheMachine(t *testing.T) {
	t.Parallel()

	tools, seen := testTools(t)
	tools.chafa = "chafa"
	path := wrote(t, "shot.png", pngBytes())
	att := jira.Attachment{ID: "att-1", Filename: "shot.png", Size: 4}

	got := draw(t.Context(), tools, att, path, previewBox{width: 40, height: 0}, jira.GraphicsKitty)
	if got.kind != previewText {
		t.Errorf("drew kind %d into a box of no height", got.kind)
	}
	if len(seen.argv()) != 0 {
		t.Errorf("a box with no room still ran %v", seen.argv())
	}
}

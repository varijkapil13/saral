package attach

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/pkg/jira"
)

// The messages the palette runs this pane's actions through. The palette knows
// which command was run and never which file is on screen, so each of these is a
// broadcast the pane answers for whatever its cursor is on.
type (
	// ShowMsg asks for the file under the cursor.
	ShowMsg struct{}
	// OpenOutsideMsg asks for it in the desktop's own handler.
	OpenOutsideMsg struct{}
	// UploadMsg opens the prompt that takes a path.
	UploadMsg struct{}
	// DeleteMsg opens the confirmation for the file under the cursor.
	DeleteMsg struct{}
)

// intent is what a download is for, so that one answer can be drawn or handed on
// without a second code path for each.
type intent uint8

const (
	noIntent intent = iota
	previewIntent
	openIntent
)

type listedMsg struct {
	gen   int
	files []jira.Attachment
}

// downloadedMsg says the bytes are on disk, at a path of this program's own. It
// is never the URL the port redirected through: that one is a credential and
// this one is a file.
type downloadedMsg struct {
	gen  int
	id   string
	why  intent
	path string
}

// progressMsg is the running total of a download. It carries the channel it came
// from so that taking one arms the wait for the next without the model holding a
// channel of its own.
type progressMsg struct {
	gen     int
	id      string
	written int64
	steps   chan int64
}

type previewMsg struct {
	gen   int
	shown preview
}

// uploadedMsg carries the attachments as the site says it stored them, which is
// what an upload answers with and the only thing worth putting on the list.
type uploadedMsg struct {
	gen   int
	added []jira.Attachment
}

// deletedMsg names the file the site has removed. The name travels with it
// because the row it came from is about to go.
type deletedMsg struct {
	gen  int
	id   string
	name string
}

// failedMsg is a request that brought nothing back. The error travels whole so
// that a refusal reaches the user in the words the site used, and why says which
// pane state has to stop waiting.
type failedMsg struct {
	gen int
	why intent
	err error
}

func list(ctx context.Context, reader jira.AttachmentReader, key string, gen int) tea.Cmd {
	return func() tea.Msg {
		files, err := reader.Attachments(ctx, key)
		if err != nil {
			return failedMsg{gen: gen, err: err}
		}
		return listedMsg{gen: gen, files: files}
	}
}

// download streams one attachment to a file of this program's own and reports the
// running total as it goes.
//
// The writing and renaming are the caller's because the port takes a writer: a
// cancelled download must not leave a truncated file where a whole one is
// expected, and only the side that knows the path can arrange that.
func download(ctx context.Context, reader jira.AttachmentReader, t tools, site string,
	att jira.Attachment, why intent, gen int, steps chan int64,
) tea.Cmd {
	return func() tea.Msg {
		defer close(steps)
		path, err := t.save(ctx, reader, site, att, func(written int64) {
			// The newest total is the only one worth waiting for, so a step that
			// finds the slot full replaces what is in it rather than blocking a
			// download on a frame.
			select {
			case <-steps:
			default:
			}
			select {
			case steps <- written:
			default:
			}
		})
		if err != nil {
			return failedMsg{gen: gen, why: why, err: err}
		}
		return downloadedMsg{gen: gen, id: att.ID, why: why, path: path}
	}
}

// awaitProgress waits for one running total. It ends on a closed channel, which
// is what the download does when it is finished or cancelled, so nothing here
// outlives the request it is reporting on.
func awaitProgress(steps chan int64, id string, gen int) tea.Cmd {
	return func() tea.Msg {
		written, open := <-steps
		if !open {
			return nil
		}
		return progressMsg{gen: gen, id: id, written: written, steps: steps}
	}
}

func render(ctx context.Context, t tools, att jira.Attachment, path string, box previewBox,
	mode jira.GraphicsMode, gen int,
) tea.Cmd {
	return func() tea.Msg {
		return previewMsg{gen: gen, shown: draw(ctx, t, att, path, box, mode)}
	}
}

func upload(ctx context.Context, a jira.Attacher, key string, file jira.FileRef, gen int) tea.Cmd {
	return func() tea.Msg {
		added, err := a.Upload(ctx, key, []jira.FileRef{file})
		if err != nil {
			return failedMsg{gen: gen, err: err}
		}
		return uploadedMsg{gen: gen, added: added}
	}
}

func remove(ctx context.Context, a jira.Attacher, id, name string, gen int) tea.Cmd {
	return func() tea.Msg {
		if err := a.DeleteAttachment(ctx, id); err != nil {
			return failedMsg{gen: gen, err: err}
		}
		return deletedMsg{gen: gen, id: id, name: name}
	}
}

func nameList(added []jira.Attachment) string {
	names := make([]string, 0, len(added))
	for i := range added {
		names = append(names, added[i].Filename)
	}
	return strings.Join(names, ", ")
}

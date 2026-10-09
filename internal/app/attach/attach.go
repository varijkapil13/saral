// Package attach lists an issue's attachments, sends one up, streams one down
// and removes one. Opening and naming the files on this machine is the caller's:
// a transfer here takes the port's FileRef or a writer and never touches a path.
package attach

import (
	"context"
	"io"
	"sync"

	"github.com/varijkapil13/saral/pkg/jira"
)

// Progress is the running total of one transfer, drained with Next. Only the
// newest total is kept: a step that finds the last one untaken replaces it
// rather than holding the transfer up on a slow reader.
type Progress struct {
	steps chan int64
	once  sync.Once
}

// NewProgress makes a Progress for one transfer.
func NewProgress() *Progress {
	return &Progress{steps: make(chan int64, 1)}
}

// Next waits for the next running total, and reports false once the transfer
// it follows has ended, however it ended.
func (p *Progress) Next() (int64, bool) {
	n, open := <-p.steps
	return n, open
}

// Close ends the progress. Send and Fetch close it when they return; closing
// it again is harmless, so a caller that fails before starting one may too.
func (p *Progress) Close() {
	if p == nil {
		return
	}
	p.once.Do(func() { close(p.steps) })
}

func (p *Progress) report(n int64) {
	select {
	case <-p.steps:
	default:
	}
	select {
	case p.steps <- n:
	default:
	}
}

// List is the attachments on an issue.
func List(ctx context.Context, r jira.AttachmentReader, key string) ([]jira.Attachment, error) {
	return r.Attachments(ctx, key)
}

// Send uploads one file to an issue and answers with the attachments as the
// site stored them. The port reports every write, so a step reaches p only when
// it moves the percentage: a large file would otherwise be a step per buffer.
// A file of no declared size reports nothing. p may be nil.
func Send(ctx context.Context, a jira.Attacher, key string, file jira.FileRef, p *Progress,
) ([]jira.Attachment, error) {
	defer p.Close()
	if p != nil {
		last := int64(-1)
		file.Progress = func(sent int64) {
			if file.Size <= 0 {
				return
			}
			pct := min(sent, file.Size) * 100 / file.Size
			if pct == last {
				return
			}
			last = pct
			p.report(sent)
		}
	}
	return a.Upload(ctx, key, []jira.FileRef{file})
}

// Fetch streams one attachment into w, reporting the bytes written to p, which
// may be nil. A failure leaves w holding a prefix; throwing that away is the
// caller's, since only the side that opened w knows what it is.
func Fetch(ctx context.Context, r jira.AttachmentReader, id string, w io.Writer, p *Progress) error {
	defer p.Close()
	var opt jira.DownloadOptions
	if p != nil {
		opt.Progress = p.report
	}
	return r.Download(ctx, id, w, opt)
}

// Remove deletes one attachment.
func Remove(ctx context.Context, a jira.Attacher, id string) error {
	return a.DeleteAttachment(ctx, id)
}

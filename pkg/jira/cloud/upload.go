package cloud

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strconv"

	"github.com/varijkapil13/saral/pkg/jira"
)

// uploadAnswerLimit bounds the answer to an upload read into memory, which is
// the stored attachments' metadata and nothing the size of a file.
const uploadAnswerLimit = 4 << 20

// errUploadAnswered is what the body writer sees when the site answered before
// it finished reading the body, which is not a failure of the file.
var errUploadAnswered = errors.New("cloud: the site answered before the upload body was finished")

// uploadAbort is a body writer failing for a reason of its own — a file that
// would not open, or read longer or shorter than it said — which is the answer
// to the upload whatever the transport made of the body stopping.
type uploadAbort struct{ err error }

func (a *uploadAbort) Error() string { return a.err.Error() }

func (a *uploadAbort) Unwrap() error { return a.err }

// uploadBody is a multipart upload that is written as it is sent. The boundary
// is fixed before the first attempt, so that the length worked out in advance
// is the length every attempt writes.
type uploadBody struct {
	boundary string
	files    []jira.FileRef
	limit    int64
}

func newUploadBody(files []jira.FileRef, limit int64) uploadBody {
	return uploadBody{boundary: multipart.NewWriter(io.Discard).Boundary(), files: files, limit: limit}
}

func (b uploadBody) contentType() string {
	return "multipart/form-data; boundary=" + b.boundary
}

// length is the exact size of the body when every file declares its size, and
// -1 when any does not, which sends the body chunked. The multipart framing is
// written once with no file content in it, and the declared sizes are the rest.
func (b uploadBody) length() int64 {
	var framing countingWriter
	form := multipart.NewWriter(&framing)
	if err := form.SetBoundary(b.boundary); err != nil {
		return -1
	}
	var total int64
	for _, file := range b.files {
		if file.Size <= 0 {
			return -1
		}
		if _, err := form.CreateFormFile(attachmentPart, filepath.Base(file.Name)); err != nil {
			return -1
		}
		total += file.Size
	}
	if err := form.Close(); err != nil {
		return -1
	}
	return total + framing.n
}

// write streams the body into w, opening each file once. A file that declared
// its size must read exactly that many bytes, because the request has already
// promised the site that length; one that did not is read to one byte past the
// site's cap and no further.
func (b uploadBody) write(ctx context.Context, w io.Writer) error {
	form := multipart.NewWriter(w)
	if err := form.SetBoundary(b.boundary); err != nil {
		return fmt.Errorf("cloud: building the upload body: %w", err)
	}
	for i := range b.files {
		if err := b.writeFile(ctx, form, b.files[i]); err != nil {
			return err
		}
	}
	if err := form.Close(); err != nil {
		return fmt.Errorf("cloud: finishing the upload body: %w", err)
	}
	return nil
}

func (b uploadBody) writeFile(ctx context.Context, form *multipart.Writer, file jira.FileRef) error {
	name := filepath.Base(file.Name)
	part, err := form.CreateFormFile(attachmentPart, name)
	if err != nil {
		return fmt.Errorf("cloud: building the upload body for %s: %w", file.Name, err)
	}
	source, err := file.Open()
	if err != nil {
		return fmt.Errorf("cloud: opening %s to upload it: %w", file.Name, err)
	}
	defer func() { _ = source.Close() }()

	sent := &uploadProgress{ctx: ctx, dst: part, report: file.Progress}
	if file.Size > 0 {
		read, err := io.CopyN(sent, source, file.Size)
		if err != nil && !errors.Is(err, io.EOF) {
			return uploadReadFailure(ctx, file.Name, err)
		}
		if read < file.Size {
			return invalidField(attachmentPart, name+" was "+strconv.FormatInt(file.Size, 10)+
				" bytes when the upload began and ran out at "+strconv.FormatInt(read, 10)+": it changed while it was being sent")
		}
		var extra [1]byte
		if n, _ := io.ReadFull(source, extra[:]); n > 0 {
			return invalidField(attachmentPart, name+" grew past the "+strconv.FormatInt(file.Size, 10)+
				" bytes it was when the upload began: it changed while it was being sent")
		}
		return nil
	}
	allowed := int64(math.MaxInt64 - 1)
	if b.limit > attachmentLimitUnknown {
		allowed = b.limit
	}
	read, err := io.CopyN(sent, source, allowed+1)
	if err != nil && !errors.Is(err, io.EOF) {
		return uploadReadFailure(ctx, file.Name, err)
	}
	if read > allowed {
		return attachmentTooBig(name, read, b.limit)
	}
	return nil
}

func uploadReadFailure(ctx context.Context, name string, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if errors.Is(err, errUploadAnswered) || errors.Is(err, io.ErrClosedPipe) {
		return err
	}
	return fmt.Errorf("cloud: reading %s to upload it: %w", name, err)
}

// uploadStream sends an upload whose body is written into the request as the
// transport reads it, through a pipe, so that a file of any size costs a copy
// buffer rather than its own length in memory.
//
// It retries what send retries and nothing more: an upload is a POST, so only
// a 429 — refused before anything was stored — is sent again, and each attempt
// opens every file afresh because the last attempt's reads are gone.
func (c *Client) uploadStream(ctx context.Context, r request, files []jira.FileRef, limit int64) (*response, error) {
	if err := checkPath(r); err != nil {
		return nil, err
	}
	body := newUploadBody(files, limit)
	for attempt := 1; ; attempt++ {
		resp, err := c.uploadAttempt(ctx, r, body)
		if err == nil && resp.ok() {
			return resp, nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		var abort *uploadAbort
		if errors.As(err, &abort) {
			return nil, abort.err
		}
		failure := c.failure(r, resp, err)
		if attempt >= c.retry.Attempts || !retryable(r, resp, err) {
			return nil, failure
		}
		wait, ok := c.waitFor(failure, attempt)
		if !ok {
			return nil, failure
		}
		if waitErr := c.clock.Wait(ctx, wait); waitErr != nil {
			return nil, waitErr
		}
	}
}

func (c *Client) uploadAttempt(ctx context.Context, r request, body uploadBody) (*response, error) {
	if err := c.acquire(ctx); err != nil {
		return nil, err
	}
	defer c.release()

	reader, writer := io.Pipe()
	wrote := make(chan error, 1)
	go func() {
		err := body.write(ctx, writer)
		writer.CloseWithError(err)
		wrote <- err
	}()
	finish := func() error {
		reader.CloseWithError(errUploadAnswered)
		if err := <-wrote; uploadFailedItself(ctx, err) {
			return &uploadAbort{err: err}
		}
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(r), reader)
	if err != nil {
		_ = finish()
		return nil, err
	}
	req.ContentLength = body.length()
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", body.contentType())
	if c.agent != "" {
		req.Header.Set("User-Agent", c.agent)
	}
	for key, values := range r.header {
		req.Header[http.CanonicalHeaderKey(key)] = values
	}
	c.creds.authorize(req)

	res, err := c.http.Do(req)
	var payload []byte
	if err == nil {
		payload, err = io.ReadAll(io.LimitReader(res.Body, uploadAnswerLimit))
		_ = res.Body.Close()
	}
	if abort := finish(); abort != nil {
		return nil, abort
	}
	if err != nil {
		return nil, err
	}
	return &response{status: res.StatusCode, header: res.Header, body: payload}, nil
}

// uploadFailedItself reports a body writer that stopped for a reason of its
// own, rather than because the site answered or the caller left.
func uploadFailedItself(ctx context.Context, err error) bool {
	return err != nil && !errors.Is(err, errUploadAnswered) && !errors.Is(err, io.ErrClosedPipe) && ctx.Err() == nil
}

// uploadProgress counts what reaches the request body and says so as it goes.
// The context is read before every write, so a cancel stops the reading of a
// file even while the transport is still draining what it was given.
type uploadProgress struct {
	ctx    context.Context
	dst    io.Writer
	report func(sent int64)
	sent   int64
}

func (p *uploadProgress) Write(b []byte) (int, error) {
	if err := p.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := p.dst.Write(b)
	p.sent += int64(n)
	if n > 0 && p.report != nil {
		p.report(p.sent)
	}
	return n, err
}

type countingWriter struct{ n int64 }

func (w *countingWriter) Write(b []byte) (int, error) {
	w.n += int64(len(b))
	return len(b), nil
}

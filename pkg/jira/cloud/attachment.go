package cloud

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/varijkapil13/saral/pkg/jira"
)

var (
	_ jira.AttachmentReader = (*Client)(nil)
	_ jira.Attacher         = (*Client)(nil)
)

const (
	attachmentBase = "/rest/api/3/attachment"
	// attachmentMetaPath carries the size cap, which is on no other endpoint, and
	// whether attachments are switched on at all.
	attachmentMetaPath = attachmentBase + "/meta"
	attachmentField    = "attachment"
	// attachmentPart is the part name every file goes under, repeated per file.
	attachmentPart       = "file"
	attachmentXSRFHeader = "X-Atlassian-Token"
	attachmentXSRF       = "no-check"
	// attachmentLimitUnknown is a cap this client did not learn, which is not a
	// cap of nothing: it means Jira decides.
	attachmentLimitUnknown = 0
	// attachmentMediaOp names the redirected-to host without naming its URL.
	attachmentMediaOp = "GET the media host an attachment download is redirected to"
	// attachmentBufferCeiling is the most of one upload this client will hold:
	// the whole multipart body is built in memory before a byte is sent.
	attachmentBufferCeiling = 64 << 20
	// attachmentsOff is caps.go's sentence for the same setting, read there from
	// /configuration.
	attachmentsOff = "Attachments are switched off for this site, which only a Jira administrator can change"
)

// errAttachmentWhole is a resume that starts exactly at the end.
var errAttachmentWhole = errors.New("cloud: the attachment is already whole")

func attachmentPath(id string) string { return attachmentBase + "/" + url.PathEscape(id) }

func attachmentContentPath(id string) string {
	return attachmentBase + "/content/" + url.PathEscape(id)
}

func issueAttachmentPath(key string) string {
	return issuePath + "/" + url.PathEscape(key) + "/attachments"
}

// apiAttachment is one attachment as a read, an upload and a standalone read all
// send it: the same keys, and the id in two JSON types, normalised to the string
// that is the only spelling above this package.
type apiAttachment struct {
	ID        flexString `json:"id"`
	Filename  string     `json:"filename"`
	MimeType  string     `json:"mimeType"`
	Size      int64      `json:"size"`
	Created   timestamp  `json:"created"`
	Author    *apiUser   `json:"author"`
	Content   string     `json:"content"`
	Thumbnail string     `json:"thumbnail"`
}

func (a apiAttachment) domain() jira.Attachment {
	out := jira.Attachment{
		ID:           string(a.ID),
		Filename:     a.Filename,
		MimeType:     a.MimeType,
		Size:         a.Size,
		Created:      a.Created.Time,
		ContentURL:   a.Content,
		ThumbnailURL: a.Thumbnail,
	}
	if a.Author != nil {
		out.Author = a.Author.domain()
	}
	return out
}

type apiAttachmentMeta struct {
	Enabled     bool  `json:"enabled"`
	UploadLimit int64 `json:"uploadLimit"`
}

// attachmentMetaCache holds the site's attachment settings for the session. Only
// an answer is kept, so a probe that failed is retried by the next upload.
type attachmentMetaCache struct {
	mu     sync.Mutex
	known  bool
	answer apiAttachmentMeta
}

func (m *attachmentMetaCache) read() (apiAttachmentMeta, bool) {
	if m == nil {
		return apiAttachmentMeta{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.answer, m.known
}

func (m *attachmentMetaCache) store(answer apiAttachmentMeta) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.answer, m.known = answer, true
}

// Attachments lists an issue's attachments. They arrive as one field on the
// issue, so the read asks for that field alone, and an absent field is an issue
// with none rather than a failure.
func (c *Client) Attachments(ctx context.Context, key string) ([]jira.Attachment, error) {
	id, err := issueKey(key)
	if err != nil {
		return nil, err
	}
	r := request{
		method: http.MethodGet,
		path:   issuePath + "/" + url.PathEscape(id),
		query:  url.Values{"fields": {attachmentField}},
		kind:   "issue",
		id:     id,
	}
	var body struct {
		Fields struct {
			Attachment []apiAttachment `json:"attachment"`
		} `json:"fields"`
	}
	if err := c.doJSON(ctx, r, &body); err != nil {
		return nil, err
	}
	listed := body.Fields.Attachment
	out := make([]jira.Attachment, 0, len(listed))
	for i := range listed {
		out = append(out, listed[i].domain())
	}
	return out, nil
}

// Upload attaches files to an issue.
//
// The site's own cap is read first, so a file this site will not take is refused
// with that number rather than sent, and the whole body is held in memory, so
// what this client will hold bounds the request as well.
func (c *Client) Upload(ctx context.Context, key string, files []jira.FileRef) ([]jira.Attachment, error) {
	id, err := issueKey(key)
	if err != nil {
		return nil, err
	}
	if err := attachmentFiles(files); err != nil {
		return nil, err
	}
	limit, err := c.attachmentLimit(ctx)
	if err != nil {
		return nil, err
	}
	body, contentType, err := attachmentBody(files, limit, attachmentBufferCeiling)
	if err != nil {
		return nil, err
	}
	r := request{
		method: http.MethodPost,
		path:   issueAttachmentPath(id),
		body:   body,
		header: http.Header{
			"Content-Type":       {contentType},
			attachmentXSRFHeader: {attachmentXSRF},
		},
		kind: "issue",
		id:   id,
	}
	var stored []apiAttachment
	if err := c.doJSON(ctx, r, &stored); err != nil {
		return nil, uploadRefusal(r.op(), err)
	}
	out := make([]jira.Attachment, 0, len(stored))
	for i := range stored {
		out = append(out, stored[i].domain())
	}
	return out, nil
}

// Download streams an attachment into w, starting at opt.From.
//
// A cancelled download returns the context's own error and leaves what is
// already in w there: the port hands this method a writer and not a path, so
// writing to a temporary file and renaming it is the caller's to do.
func (c *Client) Download(ctx context.Context, id string, w io.Writer, opt jira.DownloadOptions) error {
	att, err := attachmentID(id)
	if err != nil {
		return err
	}
	if w == nil {
		return invalidField("writer", "a download needs somewhere to write the bytes to")
	}
	if opt.From < 0 {
		return invalidField("from", "a download cannot resume before the first byte")
	}

	open, err := c.attachmentContent(ctx, att, opt.From)
	if err != nil {
		if errors.Is(err, errAttachmentWhole) {
			return nil
		}
		return err
	}
	defer open.close()

	if opt.From > 0 && open.status == http.StatusOK {
		// A 200 to a ranged request is the whole file, so the caller's own bytes
		// are dropped rather than written over the top of them.
		skipped, err := io.CopyN(io.Discard, open.body, opt.From)
		switch {
		case errors.Is(err, io.EOF):
			return attachmentShorterThan(opt.From, skipped)
		case err != nil:
			return attachmentInterrupted(ctx, open.op, err)
		}
	}

	counted := &attachmentProgress{ctx: ctx, dst: w, report: opt.Progress}
	if _, err := io.Copy(counted, open.body); err != nil {
		return attachmentInterrupted(ctx, open.op, err)
	}
	return nil
}

// DeleteAttachment removes an attachment. The path names no issue, so nothing
// here can check the id belongs to the issue on screen, and a description that
// referenced the file keeps a media node the caller now has to fix.
func (c *Client) DeleteAttachment(ctx context.Context, id string) error {
	att, err := attachmentID(id)
	if err != nil {
		return err
	}
	r := request{
		method: http.MethodDelete,
		path:   attachmentPath(att),
		kind:   "attachment",
		id:     att,
	}
	if _, err := c.do(ctx, r); err != nil {
		return err
	}
	return nil
}

// attachmentContent opens an attachment's bytes from opt.From onwards.
//
// redirect=false asks Jira for the bytes itself; a 303 to a signed media URL
// arrives anyway. That URL is a credential with minutes on it: it is followed
// once, with none of this client's Authorization on it, and never logged, put in
// an error or kept.
func (c *Client) attachmentContent(ctx context.Context, id string, from int64) (*stream, error) {
	r := request{
		method: http.MethodGet,
		path:   attachmentContentPath(id),
		query:  url.Values{"redirect": {"false"}},
		header: attachmentRange(from),
		kind:   "attachment",
		id:     id,
	}
	open, err := c.doStream(ctx, r, attachmentAnswered)
	if err != nil {
		return nil, err
	}
	switch {
	case open.status == http.StatusRequestedRangeNotSatisfiable:
		open.close()
		return nil, attachmentEnd(from, open.header)
	case !attachmentRedirected(open.status):
		return open, nil
	}
	location := open.header.Get("Location")
	status := open.status
	open.close()
	if location == "" {
		return nil, &jira.TransportError{
			Op:     r.op(),
			Status: status,
			Err:    errors.New("the site redirected this download and named nowhere to follow it to"),
		}
	}
	return c.attachmentMedia(ctx, location, from)
}

// attachmentMedia fetches the bytes from the host Jira redirected to. The signed
// URL is the credential there, so nothing of this client's own goes with it.
func (c *Client) attachmentMedia(ctx context.Context, location string, from int64) (*stream, error) {
	target, err := url.Parse(location)
	if err != nil || !target.IsAbs() {
		return nil, &jira.TransportError{
			Op:  attachmentMediaOp,
			Err: errors.New("the site redirected this download to an address this client cannot use"),
		}
	}
	if err := c.acquire(ctx); err != nil {
		return nil, err
	}
	handed := false
	defer func() {
		if !handed {
			c.release()
		}
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), http.NoBody)
	if err != nil {
		return nil, &jira.TransportError{Op: attachmentMediaOp, Err: cause(err)}
	}
	if c.agent != "" {
		req.Header.Set("User-Agent", c.agent)
	}
	for name, values := range attachmentRange(from) {
		req.Header[name] = values
	}
	res, err := c.http.Do(req) //nolint:bodyclose // closed by the stream this hands back, or below
	if err != nil {
		// cause drops the *url.Error, which repeats the whole signed URL.
		return nil, &jira.TransportError{Op: attachmentMediaOp, Err: cause(err)}
	}
	switch res.StatusCode {
	case http.StatusOK, http.StatusPartialContent:
		handed = true
		return &stream{
			status: res.StatusCode,
			op:     attachmentMediaOp,
			header: res.Header,
			body:   res.Body,
			done:   c.release,
		}, nil
	case http.StatusRequestedRangeNotSatisfiable:
		_ = res.Body.Close()
		return nil, attachmentEnd(from, res.Header)
	default:
		_ = res.Body.Close()
		return nil, &jira.TransportError{
			Op:     attachmentMediaOp,
			Status: res.StatusCode,
			Err:    errors.New(http.StatusText(res.StatusCode)),
		}
	}
}

// attachmentLimit reads how large a file this site accepts, once a session, and
// refuses the upload when the site has attachments switched off.
//
// A probe that did not answer leaves the upload to the local ceiling alone: a
// limit that could not be read must not reach a user as "the file is too big". A
// 429 is the exception, since the next request would be refused too.
func (c *Client) attachmentLimit(ctx context.Context) (int64, error) {
	if answer, ok := c.attachMeta.read(); ok {
		return attachmentCap(answer)
	}
	var meta apiAttachmentMeta
	err := c.doJSON(ctx, request{
		method: http.MethodGet,
		path:   attachmentMetaPath,
		kind:   "the site's attachment settings",
		id:     attachmentMetaPath,
	}, &meta)
	if err != nil {
		var limited *jira.RateLimitError
		if errors.As(err, &limited) {
			return attachmentLimitUnknown, err
		}
		return attachmentLimitUnknown, nil //nolint:nilerr // an unread limit leaves the size to Jira
	}
	c.attachMeta.store(meta)
	return attachmentCap(meta)
}

func attachmentCap(meta apiAttachmentMeta) (int64, error) {
	if !meta.Enabled {
		return attachmentLimitUnknown, &jira.CapabilityError{
			Capability: jira.CapAttachments,
			Reason:     attachmentsOff,
		}
	}
	return meta.UploadLimit, nil
}

// attachmentBody builds the multipart body of an upload, refusing a file the site
// will not take and a request this client will not hold. Each file is opened once.
func attachmentBody(files []jira.FileRef, limit, ceiling int64) (body []byte, contentType string, err error) {
	var buf bytes.Buffer
	form := multipart.NewWriter(&buf)
	room := ceiling
	for i := range files {
		read, err := attachmentAppend(form, files[i], limit, room)
		if err != nil {
			return nil, "", err
		}
		room -= read
	}
	if err := form.Close(); err != nil {
		return nil, "", fmt.Errorf("cloud: finishing the upload body: %w", err)
	}
	return buf.Bytes(), form.FormDataContentType(), nil
}

// attachmentAppend writes one file into the body and reports how many bytes it
// took. It reads one byte past the lower bound and no further, so a file too
// large to send costs a refusal rather than the memory to hold it.
func attachmentAppend(form *multipart.Writer, file jira.FileRef, limit, room int64) (int64, error) {
	name := filepath.Base(file.Name)
	allowed := room
	if limit > attachmentLimitUnknown && limit < allowed {
		allowed = limit
	}
	if file.Size > allowed {
		return 0, attachmentTooBig(name, file.Size, limit, room)
	}
	part, err := form.CreateFormFile(attachmentPart, name)
	if err != nil {
		return 0, fmt.Errorf("cloud: building the upload body for %s: %w", file.Name, err)
	}
	source, err := file.Open()
	if err != nil {
		return 0, fmt.Errorf("cloud: opening %s to upload it: %w", file.Name, err)
	}
	defer func() { _ = source.Close() }()

	read, err := io.CopyN(part, source, allowed+1)
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, fmt.Errorf("cloud: reading %s to upload it: %w", file.Name, err)
	}
	if read > allowed {
		return 0, attachmentTooBig(name, read, limit, room)
	}
	return read, nil
}

// attachmentTooBig names whichever bound the file did not fit in, because only
// one of the two is the site's to raise.
func attachmentTooBig(name string, size, limit, room int64) error {
	if limit > attachmentLimitUnknown && size > limit {
		return invalidField(attachmentPart, name+" is larger than the "+
			strconv.FormatInt(limit, 10)+" bytes this site accepts")
	}
	return invalidField(attachmentPart, name+" does not fit in the "+strconv.FormatInt(room, 10)+
		" bytes this client has left to hold one upload in: send fewer files, or smaller ones")
}

func attachmentFiles(files []jira.FileRef) error {
	if len(files) == 0 {
		return invalidField(attachmentPart, "an upload needs at least one file")
	}
	for _, file := range files {
		if strings.TrimSpace(file.Name) == "" {
			return invalidField(attachmentPart, "every file needs a name: it is what the attachment is called on the issue")
		}
		if file.Open == nil {
			return invalidField(attachmentPart, file.Name+" cannot be opened: it carries no way to read it")
		}
	}
	return nil
}

func attachmentID(id string) (string, error) {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return "", invalidField("id", "an attachment id is required")
	}
	return trimmed, nil
}

// attachmentRange asks for the rest of the file from one byte on, and reaches the
// media host too: the endpoint redirects whether or not a Range was sent.
func attachmentRange(from int64) http.Header {
	if from <= 0 {
		return nil
	}
	return http.Header{"Range": {"bytes=" + strconv.FormatInt(from, 10) + "-"}}
}

// attachmentAnswered is every status a download reads something out of: the
// bytes, the redirect to them, and the refusal of a range.
func attachmentAnswered(status int) bool {
	switch status {
	case http.StatusOK, http.StatusPartialContent, http.StatusRequestedRangeNotSatisfiable:
		return true
	default:
		return attachmentRedirected(status)
	}
}

func attachmentRedirected(status int) bool {
	switch status {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther,
		http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	default:
		return false
	}
}

// attachmentEnd reads what a 416 means for this offset. RFC 7233 makes bytes=N-
// unsatisfiable at N equal to the length as well as past it, so a caller resuming
// a file it already holds whole is refused by any range server and has in fact
// finished. The total is in Content-Range: bytes */N; without one, the status
// alone says only that the offset is past the end.
func attachmentEnd(from int64, header http.Header) error {
	size, known := attachmentTotal(header)
	switch {
	case known && from == size:
		return errAttachmentWhole
	case known:
		return attachmentShorterThan(from, size)
	default:
		return attachmentPastTheEnd(from)
	}
}

func attachmentTotal(header http.Header) (int64, bool) {
	_, total, ok := strings.Cut(header.Get("Content-Range"), "/")
	if !ok {
		return 0, false
	}
	size, err := strconv.ParseInt(strings.TrimSpace(total), 10, 64)
	if err != nil || size < 0 {
		return 0, false
	}
	return size, true
}

func attachmentPastTheEnd(from int64) error {
	return invalidField("from", "this attachment has fewer than "+strconv.FormatInt(from, 10)+
		" bytes, so there is nothing to resume from")
}

func attachmentShorterThan(from, size int64) error {
	return invalidField("from", "this attachment is "+strconv.FormatInt(size, 10)+
		" bytes, so there is nothing at byte "+strconv.FormatInt(from, 10)+" to resume from")
}

// attachmentInterrupted reports a copy that stopped part way, naming the host it
// was reading from. A context that ended comes back as it is, because it is the
// caller's own answer rather than the transfer failing.
func attachmentInterrupted(ctx context.Context, op string, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return &jira.TransportError{Op: op, Err: cause(err)}
}

// uploadRefusal reports the one refusal whose status says something it is not:
// the XSRF guard answers 404 with a plain-text body, which classifies as "no such
// issue". A missing issue is refused with a sentence, so a 404 carrying no reason
// at all did not come from looking the issue up.
func uploadRefusal(op string, err error) error {
	var missing *jira.NotFoundError
	if errors.As(err, &missing) && missing.Detail == "" {
		return &jira.TransportError{
			Op:     op,
			Status: http.StatusNotFound,
			Err: errors.New("the site answered 404 and gave no reason, which is how the XSRF guard in " +
				"front of this endpoint refuses a request rather than how a missing issue is reported"),
		}
	}
	return err
}

// attachmentProgress counts what reaches the writer and says so as it goes. The
// context is read before every write, so a cancel stops the writing even when the
// body has already arrived.
type attachmentProgress struct {
	ctx     context.Context
	dst     io.Writer
	report  func(written int64)
	written int64
}

func (p *attachmentProgress) Write(b []byte) (int, error) {
	if err := p.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := p.dst.Write(b)
	p.written += int64(n)
	if n > 0 && p.report != nil {
		p.report(p.written)
	}
	return n, err
}

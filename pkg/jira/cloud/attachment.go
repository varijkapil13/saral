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

	"github.com/varijkapil13/saral/pkg/jira"
)

const (
	attachmentBase = "/rest/api/3/attachment"
	// attachmentMetaPath answers whether attachments work on this site at all and
	// how large one may be. Both are per-instance and the size is on no other
	// endpoint, /configuration included.
	attachmentMetaPath = attachmentBase + "/meta"
	// attachmentField is the issue field an attachment arrives on. There is no
	// collection endpoint under an issue to read instead.
	attachmentField = "attachment"
	// attachmentPart is the multipart name every uploaded file goes under,
	// repeated once per file. Any other name is refused with an RFC 7807 400.
	attachmentPart = "file"
	// attachmentXSRF turns off the XSRF guard that stands in front of the upload
	// endpoint. Without it the answer is a 404 that has nothing to do with the
	// issue, so it is sent on every upload rather than in reply to one.
	attachmentXSRFHeader = "X-Atlassian-Token"
	attachmentXSRF       = "no-check"
	// attachmentLimitUnknown is the size cap this client did not learn, which is
	// not the same as a cap of nothing: it means Jira decides.
	attachmentLimitUnknown = 0
	// attachmentMediaOp names the second host a download talks to without naming
	// its URL, which carries a signed token and may not be written down.
	attachmentMediaOp = "GET the media host an attachment download is redirected to"
)

func attachmentPath(id string) string { return attachmentBase + "/" + url.PathEscape(id) }

func attachmentContentPath(id string) string {
	return attachmentBase + "/content/" + url.PathEscape(id)
}

func issueAttachmentPath(key string) string {
	return issuePath + "/" + url.PathEscape(key) + "/attachments"
}

// apiAttachment is one attachment, as an issue read, an upload and a single
// attachment read all send it — the same keys, and the id in two different JSON
// types: a string from the upload, a number from the standalone read. It is
// normalised to a string here, which is the only spelling anything above this
// package sees and therefore the only one two ids are ever compared in.
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

// Attachments lists an issue's attachments.
//
// There is no collection endpoint under an issue to ask: attachments arrive as
// one field on the issue, so the read asks for that field alone. Nothing pages —
// the field is a plain array with no envelope of any kind — and an issue with no
// attachments answers an absent field rather than an empty one, which is the
// same answer a site with attachments switched off gives.
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
// Every file goes in a part named "file" — the same name repeated, not file1
// and file2 — and the request carries X-Atlassian-Token: no-check, without
// which an XSRF guard in front of the endpoint answers 404. Success is a 200
// carrying a bare array of the attachments as stored.
//
// The site's own size cap is read first, so a file too large for this site is
// refused with that number rather than uploaded and refused by Jira. That read
// also says whether attachments are switched on at all, which is a site setting
// no permission makes up for.
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
	body, contentType, err := attachmentBody(files, limit)
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
// The bytes are copied from the wire as they arrive rather than buffered, so an
// attachment costs the writer and not memory, and opt.Progress is told the
// running total as it goes. A cancelled download stops within a chunk and
// returns the context's own error; what is already in w stays there, because the
// port hands this method a writer and not a path — writing to a temporary file
// and renaming it when the copy finishes is the caller's to do, and the only
// place it can be done.
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
		return err
	}
	defer open.close()

	if opt.From > 0 && open.status == http.StatusOK {
		// A 200 to a ranged request is the whole file: the bytes the caller
		// already has are dropped here rather than written over the top of them.
		if _, err := io.CopyN(io.Discard, open.body, opt.From); err != nil {
			if errors.Is(err, io.EOF) {
				return attachmentPastTheEnd(opt.From)
			}
			return attachmentInterrupted(ctx, att, err)
		}
	}

	counted := &attachmentProgress{ctx: ctx, dst: w, report: opt.Progress}
	if _, err := io.Copy(counted, open.body); err != nil {
		return attachmentInterrupted(ctx, att, err)
	}
	return nil
}

// DeleteAttachment removes an attachment.
//
// The path names the attachment and no issue, so nothing here can check that
// the id belongs to the issue on screen. A description that referenced the file
// keeps its media node, which then names an attachment the issue does not have
// and is refused as ATTACHMENT_VALIDATION_ERROR the next time that document is
// written — so a caller that deletes an attachment has a document to fix.
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
		return attachmentRefusal(err)
	}
	return nil
}

// attachmentContent opens an attachment's bytes, from opt.From onwards.
//
// Jira answers this endpoint with a 303 to a signed media URL, and it redirects
// even when the request carries a Range — so the 206 arrives from the media host
// and not from Jira. redirect=false asks Jira for the bytes itself, which is the
// answer this client would rather have; the redirect is followed anyway, because
// a site that sends one regardless is the site that was measured.
//
// The URL a redirect names is a credential with minutes on it: it is followed
// once, immediately, with none of this client's Authorization on it, and it is
// not logged, not put inside an error and not kept. A resumed download asks for a
// fresh redirect, which is the only thing that still works ten minutes later.
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
		return nil, attachmentPastTheEnd(from)
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
// URL is the credential there, so nothing of this client's own goes with it, and
// a refusal from that host is reported as the transport failure it is: a 403
// from a media URL is an expired signature, not a permission the account lacks,
// and reporting it as one would tell somebody to ask an administrator for
// something they already have.
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
		return nil, &jira.TransportError{Op: attachmentMediaOp, Err: cause(err)}
	}
	switch res.StatusCode {
	case http.StatusOK, http.StatusPartialContent:
		handed = true
		return &stream{status: res.StatusCode, header: res.Header, body: res.Body, done: c.release}, nil
	case http.StatusRequestedRangeNotSatisfiable:
		_ = res.Body.Close()
		return nil, attachmentPastTheEnd(from)
	default:
		_ = res.Body.Close()
		return nil, &jira.TransportError{
			Op:     attachmentMediaOp,
			Status: res.StatusCode,
			Err:    errors.New(http.StatusText(res.StatusCode)),
		}
	}
}

// attachmentLimit reads how large a file this site accepts, and refuses the
// upload outright when the site has attachments switched off.
//
// A probe that did not answer is not a refusal: the upload goes ahead with no
// local cap, because "the limit could not be read" must not reach a user as "the
// file is too big". A 429 is the exception, since the next request would be
// refused for the same reason the probe was.
func (c *Client) attachmentLimit(ctx context.Context) (int64, error) {
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
	if !meta.Enabled {
		return attachmentLimitUnknown, &jira.CapabilityError{
			Capability: jira.CapAttachments,
			Reason:     "Attachments are switched off for this site, which only a Jira administrator can change",
		}
	}
	return meta.UploadLimit, nil
}

// attachmentBody builds the multipart body of an upload. Each file's Open is
// called exactly once, here, because a write is never replayed.
func attachmentBody(files []jira.FileRef, limit int64) (body []byte, contentType string, err error) {
	var buf bytes.Buffer
	form := multipart.NewWriter(&buf)
	for i := range files {
		if err := attachmentAppend(form, files[i], limit); err != nil {
			return nil, "", err
		}
	}
	if err := form.Close(); err != nil {
		return nil, "", fmt.Errorf("cloud: finishing the upload body: %w", err)
	}
	return buf.Bytes(), form.FormDataContentType(), nil
}

func attachmentAppend(form *multipart.Writer, file jira.FileRef, limit int64) error {
	part, err := form.CreateFormFile(attachmentPart, filepath.Base(file.Name))
	if err != nil {
		return fmt.Errorf("cloud: building the upload body for %s: %w", file.Name, err)
	}
	source, err := file.Open()
	if err != nil {
		return fmt.Errorf("cloud: opening %s to upload it: %w", file.Name, err)
	}
	defer func() { _ = source.Close() }()

	if limit <= attachmentLimitUnknown {
		if _, err := io.Copy(part, source); err != nil {
			return fmt.Errorf("cloud: reading %s to upload it: %w", file.Name, err)
		}
		return nil
	}
	// One byte past the cap is enough to refuse the file, and is as much of it as
	// there is any point reading.
	read, err := io.CopyN(part, source, limit+1)
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("cloud: reading %s to upload it: %w", file.Name, err)
	}
	if read > limit {
		return invalidField(attachmentPart, filepath.Base(file.Name)+" is larger than the "+
			strconv.FormatInt(limit, 10)+" bytes this site accepts")
	}
	return nil
}

// attachmentFiles refuses an upload that cannot be made before it costs a
// request.
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

// attachmentRange asks for the rest of the file from one byte on. The header is
// what a resume is, and it reaches the media host too: the endpoint redirects
// whether or not a Range was sent.
func attachmentRange(from int64) http.Header {
	if from <= 0 {
		return nil
	}
	return http.Header{"Range": {"bytes=" + strconv.FormatInt(from, 10) + "-"}}
}

// attachmentAnswered is every status a download reads something out of: the
// bytes, the redirect to where the bytes are, and the refusal of a range that
// asked past the end, which is the caller's mistake rather than the site's.
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

func attachmentPastTheEnd(from int64) error {
	return invalidField("from", "this attachment has fewer than "+strconv.FormatInt(from, 10)+
		" bytes, so there is nothing to resume from")
}

// attachmentInterrupted reports a copy that stopped part way. A context that
// ended is the caller's own answer and comes back as it is; anything else is the
// transfer failing, which is as true of the writer as of the wire.
func attachmentInterrupted(ctx context.Context, id string, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return &jira.TransportError{Op: http.MethodGet + " " + attachmentContentPath(id), Err: cause(err)}
}

// attachmentRefusal names the capability on a 403 so that a view can say what
// the probe says instead of "refused". Only the writes get it: reading an
// attachment needs nothing beyond seeing the issue, so a refused read must not
// read as the whole feature being off.
func attachmentRefusal(err error) error {
	var refused *jira.CapabilityError
	if !errors.As(err, &refused) || refused.Capability != "" {
		return err
	}
	return &jira.CapabilityError{Capability: jira.CapAttachments, Reason: refused.Reason}
}

// uploadRefusal reports the one refusal whose status says something it is not.
// The XSRF guard in front of the upload answers 404 with the plain string "XSRF
// check failed", which classifies as "no such issue" and will not parse as JSON.
// A missing issue is refused with a sentence, so a 404 carrying no reason at all
// did not come from looking the issue up.
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
	return attachmentRefusal(err)
}

// attachmentProgress counts what reaches the writer and says so as it goes. The
// context is read before every write, so a cancelled download stops one chunk
// later rather than at the end of the file.
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

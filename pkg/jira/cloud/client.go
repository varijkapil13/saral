// Package cloud is the jira.Client adapter for a Jira Cloud site: the HTTP
// transport underneath every call, basic auth with an API token, the mapping
// from a refused response to the typed errors in pkg/jira, retries that honour
// Retry-After, and both of Jira's pagination models.
//
// Nothing above this package knows that Jira speaks REST, and nothing in it
// knows what a view looks like. A Client is safe for concurrent use, caps how
// many requests it has in the air at once, and collapses identical in-flight
// requests, so that a cursor moving down a list cannot fan out one fetch per
// keystroke.
package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
)

// DefaultMaxConcurrent is how many requests a client keeps in the air at once
// when the caller does not say. Jira's limits are cost-based, so several narrow
// requests cost less than one wide one — but only up to the point where the
// site starts answering 429.
const DefaultMaxConcurrent = 8

const defaultUserAgent = "saral"

// Doer is the HTTP client a Client sends through. *http.Client satisfies it; a
// test substitutes its own.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client is one Jira Cloud site and the credentials to reach it.
type Client struct {
	base   *url.URL
	creds  credentials
	http   Doer
	clock  Clock
	jitter Jitter
	retry  RetryPolicy
	agent  string

	concurrency int
	gate        chan struct{}
	flight      *singleflight
}

// Option configures a Client at construction.
type Option func(*Client)

// New builds a client for one Jira Cloud site.
//
// The token is the resolved API token and not a source to resolve one from:
// where a secret lives is the application's decision, taken in internal/config,
// and pkg must not depend on it. The site may be written as a bare host, as a
// URL, or with a port, which is what lets a test point a client at loopback.
func New(site, email, token string, opts ...Option) (*Client, error) {
	base, err := parseSite(site)
	if err != nil {
		return nil, err
	}
	account := strings.TrimSpace(email)
	if account == "" {
		return nil, errors.New("cloud: the account email is required: Jira Cloud pairs it with the API token as basic auth")
	}
	if token == "" {
		return nil, errors.New("cloud: an API token is required")
	}

	c := &Client{
		base:        base,
		creds:       credentials{email: account, token: newSecret(token)},
		clock:       systemClock{},
		jitter:      fullJitter,
		retry:       DefaultRetry(),
		agent:       defaultUserAgent,
		concurrency: DefaultMaxConcurrent,
		flight:      newSingleflight(),
	}
	for _, o := range opts {
		if o != nil {
			o(c)
		}
	}
	c.retry = c.retry.normalise()
	if c.concurrency < 1 {
		c.concurrency = 1
	}
	if c.clock == nil {
		c.clock = systemClock{}
	}
	if c.jitter == nil {
		c.jitter = fullJitter
	}
	if c.http == nil {
		c.http = defaultHTTPClient(c.concurrency)
	}
	c.gate = make(chan struct{}, c.concurrency)
	return c, nil
}

// WithHTTPClient sends through an HTTP client of the caller's own.
func WithHTTPClient(d Doer) Option {
	return func(c *Client) {
		if d != nil {
			c.http = d
		}
	}
}

// WithClock replaces the time source the retry loop waits on.
func WithClock(k Clock) Option {
	return func(c *Client) {
		if k != nil {
			c.clock = k
		}
	}
}

// WithJitter replaces the spread applied to a backoff wait.
func WithJitter(j Jitter) Option {
	return func(c *Client) {
		if j != nil {
			c.jitter = j
		}
	}
}

// WithRetry replaces the retry policy. A zero field keeps its default, so
// RetryPolicy{Attempts: 1} turns retrying off and leaves the rest alone.
func WithRetry(p RetryPolicy) Option {
	return func(c *Client) { c.retry = p }
}

// WithMaxConcurrent caps how many requests are in the air at once.
func WithMaxConcurrent(n int) Option {
	return func(c *Client) { c.concurrency = n }
}

// WithUserAgent sets the User-Agent header, which is how a site administrator
// tells this client apart from a browser in the access log.
func WithUserAgent(agent string) Option {
	return func(c *Client) { c.agent = agent }
}

// String identifies the client without revealing anything secret, which is what
// makes it safe to hand to a logger or a %v.
func (c Client) String() string {
	if c.base == nil {
		return "cloud.Client{unconfigured}"
	}
	return "cloud.Client{site: " + c.base.Host + ", account: " + c.creds.email + "}"
}

func defaultHTTPClient(concurrency int) *http.Client {
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return &http.Client{}
	}
	t := transport.Clone()
	t.MaxIdleConnsPerHost = concurrency
	t.MaxConnsPerHost = concurrency
	// A whole-client Timeout would also bound reading an attachment body, so
	// the bound is on how long the site may take to start answering.
	t.ResponseHeaderTimeout = 30 * time.Second
	return &http.Client{Transport: t}
}

// parseSite reads what a profile calls a site — "example.atlassian.net",
// "https://example.atlassian.net/", or an http://127.0.0.1 address in a test —
// into the base every request is built on.
func parseSite(site string) (*url.URL, error) {
	trimmed := strings.TrimSpace(site)
	if trimmed == "" {
		return nil, errors.New("cloud: the site is required, for example example.atlassian.net")
	}
	if !strings.Contains(trimmed, "://") {
		trimmed = "https://" + trimmed
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return nil, fmt.Errorf("cloud: reading the site %q: %w", site, err)
	}
	switch parsed.Scheme {
	case "http", "https":
	default:
		return nil, fmt.Errorf("cloud: the site %q must be an http or https address", site)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("cloud: the site %q names no host", site)
	}
	if parsed.User != nil {
		return nil, errors.New("cloud: the site must not carry credentials; the email and token are given separately")
	}
	return &url.URL{Scheme: parsed.Scheme, Host: parsed.Host, Path: strings.TrimSuffix(parsed.Path, "/")}, nil
}

// request describes one call to Jira. It is a value the retry loop can replay.
type request struct {
	method string
	path   string
	query  url.Values
	// body is marshalled to JSON, unless it is already a []byte, which is sent
	// as it stands with whatever Content-Type the caller's header carries.
	body   any
	header http.Header
	// kind and id say what a 404 on this path is about, so that the error reads
	// "issue EX-1 does not exist" rather than naming a URL.
	kind string
	id   string
	// repeatable marks a request that is safe to send again although its method
	// is not idempotent: a POST that only reads, which is what /search/jql is.
	// A write leaves it false and is then neither replayed nor coalesced.
	repeatable bool
}

func (r request) op() string { return r.method + " " + r.path }

func (r request) canRepeat() bool {
	if r.repeatable {
		return true
	}
	switch r.method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace,
		http.MethodPut, http.MethodDelete:
		return true
	default:
		return false
	}
}

// target is what a 404 or a 409 on this request is about.
func (r request) target() (kind, id string) {
	kind, id = r.kind, r.id
	if kind == "" {
		kind = "resource"
	}
	if id == "" {
		id = r.path
	}
	return kind, id
}

// call is a request whose body has been encoded, which is what the retry loop
// replays: a retry resends the same bytes rather than running an encoder again
// over a value the caller has moved on from.
type call struct {
	request
	encoded     []byte
	contentType string
}

// response is what came back, with the body already read so that the connection
// goes back to the pool before anything is decoded.
type response struct {
	status int
	header http.Header
	body   []byte
}

func (r *response) ok() bool {
	return r.status >= http.StatusOK && r.status < http.StatusMultipleChoices
}

// decode reads a JSON response body into out. A body that will not decode is a
// transport failure: the call reached Jira and came back with something this
// client cannot use. A success with no body at all leaves out untouched.
func (r *response) decode(op string, out any) error {
	if out == nil || r.status == http.StatusNoContent || len(bytes.TrimSpace(r.body)) == 0 {
		return nil
	}
	if err := json.Unmarshal(r.body, out); err != nil {
		return &jira.TransportError{
			Op:     op,
			Status: r.status,
			Err:    fmt.Errorf("the response body is not the JSON this client expected: %w", err),
		}
	}
	return nil
}

// do sends a request and returns the response the site answered 2xx with,
// having retried whatever was safe to retry.
func (c *Client) do(ctx context.Context, r request) (*response, error) {
	encoded, contentType, err := encodeBody(r)
	if err != nil {
		return nil, err
	}
	pending := call{request: r, encoded: encoded, contentType: contentType}
	if !r.canRepeat() {
		return c.send(ctx, pending)
	}
	return c.flight.do(ctx, signature(pending), func(ctx context.Context) (*response, error) {
		return c.send(ctx, pending)
	})
}

// doJSON sends a request and decodes its response body into out.
func (c *Client) doJSON(ctx context.Context, r request, out any) error {
	resp, err := c.do(ctx, r)
	if err != nil {
		return err
	}
	return resp.decode(r.op(), out)
}

// encodeBody marshals a request body once, before the first attempt.
func encodeBody(r request) (encoded []byte, contentType string, err error) {
	switch body := r.body.(type) {
	case nil:
		return nil, "", nil
	case []byte:
		return body, "", nil
	default:
		marshalled, err := json.Marshal(body)
		if err != nil {
			return nil, "", fmt.Errorf("cloud: encoding the body of %s: %w", r.op(), err)
		}
		return marshalled, "application/json", nil
	}
}

// signature is what makes two requests the same request. It is only ever built
// for a repeatable one, so no write is ever collapsed into another write.
func signature(pending call) string {
	var b strings.Builder
	b.Grow(len(pending.method) + len(pending.path) + len(pending.encoded) + 8)
	b.WriteString(pending.method)
	b.WriteByte(' ')
	b.WriteString(pending.path)
	if len(pending.query) > 0 {
		b.WriteByte('?')
		b.WriteString(pending.query.Encode())
	}
	if len(pending.encoded) > 0 {
		b.WriteByte(0)
		b.Write(pending.encoded)
	}
	return b.String()
}

func (c *Client) send(ctx context.Context, pending call) (*response, error) {
	for attempt := 1; ; attempt++ {
		resp, err := c.attempt(ctx, pending)
		if err == nil && resp.ok() {
			return resp, nil
		}
		// A context that ended is the caller's own answer rather than anything
		// Jira did, so it comes back as it is and not as a transport failure.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		failure := c.failure(pending.request, resp, err)
		if attempt >= c.retry.Attempts || !retryable(pending.request, resp, err) {
			return nil, failure
		}
		if waitErr := c.clock.Wait(ctx, c.waitFor(failure, attempt)); waitErr != nil {
			return nil, waitErr
		}
	}
}

func (c *Client) attempt(ctx context.Context, pending call) (*response, error) {
	if err := c.acquire(ctx); err != nil {
		return nil, err
	}
	defer c.release()

	var body io.Reader
	if len(pending.encoded) > 0 {
		body = bytes.NewReader(pending.encoded)
	}
	req, err := http.NewRequestWithContext(ctx, pending.method, c.endpoint(pending.request), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if pending.contentType != "" {
		req.Header.Set("Content-Type", pending.contentType)
	}
	if c.agent != "" {
		req.Header.Set("User-Agent", c.agent)
	}
	for key, values := range pending.header {
		req.Header[http.CanonicalHeaderKey(key)] = slices.Clone(values)
	}
	// Authorising last is what stops a caller's own header from replacing it.
	c.creds.authorize(req)

	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	payload, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	return &response{status: res.StatusCode, header: res.Header, body: payload}, nil
}

func (c *Client) endpoint(r request) string {
	target := url.URL{Scheme: c.base.Scheme, Host: c.base.Host, Path: c.base.Path + r.path}
	if len(r.query) > 0 {
		target.RawQuery = r.query.Encode()
	}
	return target.String()
}

// failure turns whatever went wrong into the taxonomy in pkg/jira.
func (c *Client) failure(r request, resp *response, err error) error {
	if err != nil {
		return &jira.TransportError{Op: r.op(), Err: err}
	}
	return c.apiError(r, resp)
}

// apiError maps a response Jira refused. The status is the authority: a body
// that will not parse costs the detail, never the classification.
func (c *Client) apiError(r request, resp *response) error {
	detail := parseErrorBody(resp.body)
	kind, id := r.target()
	switch resp.status {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return &jira.ValidationError{Fields: detail.fields, Messages: detail.messages}
	case http.StatusUnauthorized:
		return &jira.AuthError{Reason: detail.reason()}
	case http.StatusForbidden:
		return &jira.CapabilityError{
			Reason: detail.reasonOr("Jira refused " + r.op() + ": this token is not permitted to do it"),
		}
	case http.StatusNotFound, http.StatusGone:
		return &jira.NotFoundError{Kind: kind, ID: id}
	case http.StatusConflict:
		return &jira.ConflictError{Resource: kind + " " + id, Detail: detail.reason()}
	case http.StatusTooManyRequests:
		return &jira.RateLimitError{
			RetryAfter: retryAfter(resp.header, c.clock.Now()),
			Endpoint:   r.path,
		}
	default:
		return &jira.TransportError{
			Op:     r.op(),
			Status: resp.status,
			Err:    errors.New(detail.reasonOr(http.StatusText(resp.status))),
		}
	}
}

// jiraError is the error envelope both APIs use: per-field messages under
// errors, loose ones under errorMessages, and a bare message on the Agile
// endpoints that answer in their own shape.
type jiraError struct {
	fields   []jira.FieldError
	messages []string
}

func (e jiraError) reason() string { return strings.Join(e.messages, "; ") }

func (e jiraError) reasonOr(fallback string) string {
	if reason := e.reason(); reason != "" {
		return reason
	}
	return fallback
}

func parseErrorBody(body []byte) jiraError {
	var envelope struct {
		ErrorMessages []string        `json:"errorMessages"`
		Errors        json.RawMessage `json:"errors"`
		Message       string          `json:"message"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return jiraError{}
	}
	out := jiraError{messages: envelope.ErrorMessages, fields: parseFieldErrors(envelope.Errors)}
	if envelope.Message != "" {
		out.messages = append(out.messages, envelope.Message)
	}
	return out
}

// parseFieldErrors reads the errors object in the order Jira wrote it, which a
// map would lose and a form needs: the first message names the field to focus.
// Some endpoints send an empty array there instead of an object, which is not a
// failure and carries nothing.
func parseFieldErrors(raw json.RawMessage) []jira.FieldError {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	if _, err := dec.Token(); err != nil {
		return nil
	}
	var out []jira.FieldError
	for dec.More() {
		token, err := dec.Token()
		if err != nil {
			return out
		}
		field, ok := token.(string)
		if !ok {
			return out
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return out
		}
		out = append(out, jira.FieldError{Field: field, Message: fieldMessage(value)})
	}
	return out
}

// fieldMessage renders one entry of the errors object, which is a string on
// every endpoint that documents the shape and occasionally not on one that does
// not.
func fieldMessage(raw json.RawMessage) string {
	var message string
	if err := json.Unmarshal(raw, &message); err == nil {
		return message
	}
	return string(raw)
}

// singleflight collapses identical in-flight requests, so that rapid cursor
// movement cannot fan out N fetches of the same page. golang.org/x/sync has
// this, but promoting it from an indirect dependency to a direct one is a
// go.mod change, and those are a separate PR here.
type singleflight struct {
	mu    sync.Mutex
	calls map[string]*flight
}

type flight struct {
	done    chan struct{}
	callers int
	resp    *response
	err     error
}

func newSingleflight() *singleflight {
	return &singleflight{calls: make(map[string]*flight)}
}

// do runs fn once for a key however many callers ask for it at the same moment.
// Each caller waits on its own context, so one of them giving up neither
// cancels the others nor fails them: a call abandoned by the caller that
// started it is taken over by whoever is still waiting.
func (s *singleflight) do(ctx context.Context, key string, fn func(context.Context) (*response, error)) (*response, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		s.mu.Lock()
		shared, running := s.calls[key]
		if !running {
			shared = &flight{done: make(chan struct{})}
			s.calls[key] = shared
		}
		shared.callers++
		s.mu.Unlock()

		if !running {
			shared.resp, shared.err = fn(ctx)
			s.mu.Lock()
			delete(s.calls, key)
			s.mu.Unlock()
			close(shared.done)
			return shared.resp, shared.err
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-shared.done:
		}
		if ctx.Err() == nil && abandoned(shared.err) {
			continue
		}
		return shared.resp, shared.err
	}
}

// waiting reports how many callers are attached to the call under key.
func (s *singleflight) waiting(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if shared, ok := s.calls[key]; ok {
		return shared.callers
	}
	return 0
}

// abandoned reports whether a shared call ended because its own caller's
// context did, which is not an answer about Jira and not one to hand on.
func abandoned(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// The platform API and the Agile API write date-times differently: the platform
// writes an offset with no colon, the Agile API writes one with. Neither is
// RFC 3339 and one layout cannot read both.
const (
	platformTimeLayout = "2006-01-02T15:04:05.000-0700"
	agileTimeLayout    = "2006-01-02T15:04:05.000-07:00"
)

// parsePlatformTime reads a platform API date-time, 2021-01-17T12:34:00.000+0000.
func parsePlatformTime(s string) (time.Time, error) {
	parsed, err := time.Parse(platformTimeLayout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("cloud: %q is not a platform API date-time: %w", s, err)
	}
	return parsed, nil
}

// parseAgileTime reads an Agile API date-time, 2015-04-11T15:22:00.000+10:00.
func parseAgileTime(s string) (time.Time, error) {
	parsed, err := time.Parse(agileTimeLayout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("cloud: %q is not an Agile API date-time: %w", s, err)
	}
	return parsed, nil
}

// parseTime reads a date-time from either API. The two layouts cannot mistake
// each other's input — one wants four digits after the sign and the other wants
// a colon in the middle of them — so trying both in turn is unambiguous.
func parseTime(s string) (time.Time, error) {
	for _, layout := range []string{platformTimeLayout, agileTimeLayout, time.RFC3339Nano} {
		if parsed, err := time.Parse(layout, s); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("cloud: %q is not a date-time either Jira API writes", s)
}

// timestamp decodes a date-time from either API, and reads null and "" as unset
// rather than as a failure — Jira leaves a date off an issue that way.
type timestamp struct {
	time.Time
}

func (t *timestamp) UnmarshalJSON(data []byte) error {
	var raw *string
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("cloud: reading a date-time: %w", err)
	}
	if raw == nil || *raw == "" {
		t.Time = time.Time{}
		return nil
	}
	parsed, err := parseTime(*raw)
	if err != nil {
		return err
	}
	t.Time = parsed
	return nil
}

// ptr returns the time behind a pointer, and nil when it is unset, which is the
// shape the domain types carry an optional instant in.
func (t timestamp) ptr() *time.Time {
	if t.IsZero() {
		return nil
	}
	at := t.Time
	return &at
}

// epochMillis decodes the epoch-millisecond instants the task endpoints report
// created, started and updated in, rather than the layouts everything else uses.
type epochMillis struct {
	time.Time
}

func (t *epochMillis) UnmarshalJSON(data []byte) error {
	var raw *int64
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("cloud: reading an epoch-millisecond time: %w", err)
	}
	if raw == nil {
		t.Time = time.Time{}
		return nil
	}
	t.Time = time.UnixMilli(*raw).UTC()
	return nil
}

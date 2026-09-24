package main

import (
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/pkg/jira/cloud"
)

const redacted = "[redacted]"

var (
	secretKey  = regexp.MustCompile(`(?i)authorization|token|secret|password|email|cookie`)
	emailShape = regexp.MustCompile(`[^\s@"'<>]+@[^\s@"'<>]+\.[A-Za-z]{2,}`)
	authShape  = regexp.MustCompile(`(?i)\b(basic|bearer)\s+[A-Za-z0-9+/=._-]+`)
)

func redactAttr(_ []string, a slog.Attr) slog.Attr {
	if secretKey.MatchString(a.Key) {
		return slog.String(a.Key, redacted)
	}
	if a.Value.Kind() == slog.KindString {
		s := a.Value.String()
		s = authShape.ReplaceAllString(s, "$1 "+redacted)
		s = emailShape.ReplaceAllString(s, redacted)
		return slog.String(a.Key, s)
	}
	return a
}

func newLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug, ReplaceAttr: redactAttr}))
}

func openLog(path string) (*slog.Logger, func(), error) {
	f, err := tea.LogToFile(path, "saral")
	if err != nil {
		return nil, func() {}, withCode(exitUsage, err)
	}
	logger := newLogger(f)
	slog.SetDefault(logger)
	return logger, func() { _ = f.Close() }, nil
}

// loggingDoer leaves out the query string and the bodies: JQL and issue text are private.
type loggingDoer struct {
	next cloud.Doer
	log  *slog.Logger
	now  func() time.Time
}

func (d loggingDoer) Do(req *http.Request) (*http.Response, error) {
	start := d.now()
	resp, err := d.next.Do(req)
	attrs := []any{
		slog.String("method", req.Method),
		slog.String("path", req.URL.Path),
		slog.Duration("took", d.now().Sub(start)),
	}
	if err != nil {
		d.log.Debug("http", append(attrs, slog.String("error", err.Error()))...)
		return resp, err
	}
	d.log.Debug("http", append(attrs, slog.Int("status", resp.StatusCode))...)
	return resp, nil
}

// httpClientForLog must mirror cloud's unexported default client.
func httpClientForLog() *http.Client {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return &http.Client{CheckRedirect: noRedirect}
	}
	t := base.Clone()
	t.MaxIdleConnsPerHost = cloud.DefaultMaxConcurrent
	t.MaxConnsPerHost = cloud.DefaultMaxConcurrent
	t.ResponseHeaderTimeout = 30 * time.Second
	return &http.Client{Transport: t, CheckRedirect: noRedirect}
}

func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

func loggingOption(logger *slog.Logger) cloud.Option {
	if logger == nil {
		return nil
	}
	return cloud.WithHTTPClient(loggingDoer{next: httpClientForLog(), log: logger, now: time.Now})
}

func redactEmail(email string) string {
	local, domain, ok := strings.Cut(email, "@")
	if !ok || local == "" {
		return redacted
	}
	_, size := utf8.DecodeRuneInString(local)
	return local[:size] + "…@" + domain
}

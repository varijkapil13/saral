package form

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/jira"
)

// Option is part of the answer a caller already has when it opens the form:
// the board column or backlog section a create was started from knows its
// project, often the issue type, and sometimes the sprint.
type Option func(*Model)

// WithProject creates the issue in this project rather than the one the session
// is scoped to.
func WithProject(key string) Option {
	return func(m *Model) {
		if key = strings.TrimSpace(key); key != "" {
			m.project = key
		}
	}
}

// WithIssueType opens straight onto this issue type's create screen. The type
// picker is still one key away.
func WithIssueType(id string) Option {
	return func(m *Model) { m.sought = strings.TrimSpace(id) }
}

// WithSprint names the sprint the new issue is for. The sprint field is a
// custom field whose shape on a create screen varies, and it is often not on
// the screen at all, so it is not set at create: the sprint travels back on
// CreatedMsg and the caller moves the issue into it.
func WithSprint(sp jira.Sprint) Option {
	return func(m *Model) {
		m.sprint, m.dest = sp, ""
		if sp.ID != 0 {
			m.dest = " for " + widget.Sanitize(sp.Name)
		}
	}
}

// WithReport sends a CreatedMsg to this address once the issue exists, as well
// as the refresh every create broadcasts.
func WithReport(to kernel.Addr) Option {
	return func(m *Model) { m.report, m.reporting = to, true }
}

// CreatedMsg is the issue a form created, delivered to the address WithReport
// named, with the sprint WithSprint named.
type CreatedMsg struct {
	Issue  jira.Issue
	Sprint jira.Sprint
}

// NewWith builds the create form with part of it already answered.
func NewWith(d kernel.Deps, opts ...Option) kernel.View {
	m := newWith(d, schemas)
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// reported is the CreatedMsg for the caller that asked for one, or nil.
func (m *Model) reported(iss jira.Issue) tea.Cmd {
	if !m.reporting {
		return nil
	}
	msg := CreatedMsg{Issue: iss, Sprint: m.sprint}
	return kernel.Reply(func() tea.Msg { return msg }, m.report)
}

// destination is what the heading says the issue is for beyond its project.
func (m *Model) destination() string { return m.dest }

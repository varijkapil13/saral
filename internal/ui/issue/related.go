package issue

import (
	"errors"
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/pkg/jira"
)

func refRowID(group, key string) string { return "ref:" + group + ":" + key }

func refZone(id string) string { return "refkey:" + id }

// groupLabel is the heading a link is drawn under; an unphrased link keeps its two directions apart.
func groupLabel(l *jira.IssueLink) string {
	if l.Label != "" {
		return l.Label
	}
	name := firstNonEmpty(l.Type, "Links")
	if l.Direction == jira.LinkInward {
		return name + " (inward)"
	}
	return name
}

// fromTrail is the keys of the issue panes beneath a new one, nearest last.
func fromTrail(trail []string) modelOption {
	return func(m *Model) { m.trail = trail }
}

func (m *Model) trailBeneath() []string {
	return append(slices.Clone(m.trail), m.issue.Key)
}

func openIssue(d kernel.Deps, ref jira.IssueRef, trail []string) tea.Cmd {
	return kernel.Push(ViewID, ref.Key, New(d, jira.Issue{
		ID: ref.ID, Key: ref.Key, Summary: ref.Summary, Status: ref.Status, Type: ref.Type,
	}, fromTrail(trail)))
}

// openRelated pushes the issue, or pops when it is the pane directly beneath.
func (m *Model) openRelated(ref jira.IssueRef) tea.Cmd {
	if n := len(m.trail); n > 0 && m.trail[n-1] == ref.Key {
		return kernel.Pop()
	}
	return openIssue(m.deps, ref, m.trailBeneath())
}

func (m *Model) openParent() tea.Cmd {
	switch {
	case !m.read("parent"):
		return kernel.Warn(m.issue.Key + " has not been read yet")
	case m.issue.Parent == nil:
		return kernel.Warn(m.issue.Key + " has no parent")
	}
	return m.openRelated(*m.issue.Parent)
}

// failureText words a 404 as both causes, which Jira does not tell apart.
func (m *Model) failureText() string {
	var nf *jira.NotFoundError
	if errors.As(m.loadErr, &nf) {
		return m.issue.Key + " could not be opened: it does not exist, or this account cannot browse its project."
	}
	text, _ := jira.Reason(m.loadErr)
	return text
}

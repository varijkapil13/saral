package issue

import (
	"context"
	"strconv"

	tea "charm.land/bubbletea/v2"

	appissue "github.com/varijkapil13/saral/internal/app/issue"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/jira"
)

const clonePrefix = "CLONE - "

type cloneKind struct {
	src   jira.Issue
	in    jira.IssueInput
	types []jira.LinkType
}

var cloneKeys = newSheetKeys(sheetBind{kernel.Canon(kernel.ActOpen, "clone it"), sheetAdd})

func (k *cloneKind) keys() *sheetKeys { return cloneKeys }

func (k *cloneKind) load(s *sheet) tea.Cmd {
	key := s.key
	return s.read(&s.loads, func(ctx context.Context, c jira.SessionClient) func(*sheet) tea.Cmd {
		cl, err := appissue.ReadClone(ctx, c, key)
		return func(s *sheet) tea.Cmd {
			if err != nil {
				return s.failed(err)
			}
			src := cl.Src
			k.src, k.types, k.in = src, cl.Types, cl.Input
			rows := []sheetRow{{text: "Carried over", head: true}}
			for _, n := range cl.Carried {
				rows = append(rows, sheetRow{text: widget.Sanitize(n)})
			}
			s.setRows(rows)
			s.note = "into " + src.Project.Key + " as " + widget.Sanitize(src.Type.Name)
			return k.act(s, sheetAdd)
		}
	})
}

func (k *cloneKind) act(s *sheet, a sheetAct) tea.Cmd {
	if a != sheetAdd || k.src.Key == "" {
		return nil
	}
	return s.ask("Summary of the copy", clonePrefix+k.src.Summary, false)
}

func (k *cloneKind) changed(*sheet, string) tea.Cmd { return nil }

func (k *cloneKind) answered(s *sheet, text string, _ *sheetRow) tea.Cmd {
	if text == "" {
		s.problem = "the copy needs a summary"
		return nil
	}
	s.endAsk()
	in := k.in
	in.Summary = text
	if len(k.src.Links) == 0 || len(k.types) == 0 {
		return k.create(s, in, false)
	}
	s.confirm("Copy its "+count(len(k.src.Links), "link")+" as well?",
		func() tea.Cmd { return k.create(s, in, true) },
		func() tea.Cmd { return k.create(s, in, false) })
	return nil
}

func (k *cloneKind) create(s *sheet, in jira.IssueInput, links bool) tea.Cmd {
	src, types, d, trail := k.src, k.types, s.deps, s.trail
	return s.write(func(ctx context.Context, c jira.SessionClient) (func(*sheet) tea.Cmd, error) {
		made, failed, err := appissue.Clone(ctx, c, in, src, types, links)
		if err != nil {
			return nil, err
		}
		said := "cloned " + src.Key + " as " + made.Key
		if failed > 0 {
			said += "; " + strconv.Itoa(failed) + " of its links could not be copied"
		}
		return func(*sheet) tea.Cmd {
			return tea.Sequence(kernel.Pop(), openIssue(d, jira.IssueRef{ID: made.ID, Key: made.Key, Summary: made.Summary}, trail), kernel.Status(said))
		}, nil
	})
}

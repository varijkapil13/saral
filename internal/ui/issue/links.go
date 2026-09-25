package issue

import (
	"context"
	"strconv"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/jira"
)

type linksKind struct {
	links  []jira.IssueLink
	types  []jira.LinkType
	phrase *sheetRow
}

var linksKeys = newSheetKeys(
	sheetBind{kernel.Bind([]string{"a"}, "a", "add a link"), sheetAdd},
	sheetBind{kernel.Bind([]string{"d", "x"}, "d", "remove it"), sheetRemove},
	sheetBind{kernel.Bind([]string{"enter"}, "enter", "open it"), sheetOpen},
)

func (k *linksKind) keys() *sheetKeys { return linksKeys }

func (k *linksKind) load(s *sheet) tea.Cmd {
	key := s.key
	return s.read(&s.loads, func(ctx context.Context, c jira.SessionClient) func(*sheet) tea.Cmd {
		iss, err := c.IssueFields(ctx, key, []string{"issuelinks"})
		return func(s *sheet) tea.Cmd {
			if err != nil {
				return s.failed(err)
			}
			k.links = iss.Links
			s.setRows(linkRows(iss.Links))
			s.note = count(len(iss.Links), "link")
			return nil
		}
	})
}

func linkRows(links []jira.IssueLink) []sheetRow {
	var rows []sheetRow
	seen := map[string]bool{}
	for i := range links {
		label := firstNonEmpty(links[i].Label, links[i].Type)
		if seen[label] {
			continue
		}
		seen[label] = true
		rows = append(rows, sheetRow{text: widget.Sanitize(label), head: true})
		for j := i; j < len(links); j++ {
			if l := &links[j]; firstNonEmpty(l.Label, l.Type) == label {
				rows = append(rows, sheetRow{text: refText(l.Other), id: l.ID, key: l.Other.Key})
			}
		}
	}
	return rows
}

func refText(r jira.IssueRef) string {
	text := r.Key + "  " + widget.Sanitize(r.Summary)
	if r.Status.Name != "" {
		text += "  · " + widget.Sanitize(r.Status.Name)
	}
	return text
}

func count(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return strconv.Itoa(n) + " " + what + "s"
}

func (k *linksKind) act(s *sheet, a sheetAct) tea.Cmd {
	row := s.current()
	switch a {
	case sheetAdd:
		return k.startAdd(s)
	case sheetRemove:
		if row == nil {
			return nil
		}
		id, other := row.id, row.key
		s.confirm("Remove the link to "+other+"?", func() tea.Cmd {
			return s.write(func(ctx context.Context, c jira.SessionClient) (func(*sheet) tea.Cmd, error) {
				return k.written("removed the link to " + other), c.DeleteLink(ctx, id)
			})
		}, nil)
	case sheetOpen:
		for i := range k.links {
			if row != nil && k.links[i].ID == row.id {
				return openIssue(s.deps, k.links[i].Other)
			}
		}
	default:
	}
	return nil
}

func (k *linksKind) written(said string) func(*sheet) tea.Cmd {
	return func(s *sheet) tea.Cmd {
		return tea.Batch(k.load(s), s.changedIssue(), kernel.Status(said))
	}
}

func (k *linksKind) startAdd(s *sheet) tea.Cmd {
	k.phrase = nil
	if k.types != nil {
		return s.ask("How is it linked?", "", true)
	}
	return s.read(&s.looks, func(ctx context.Context, c jira.SessionClient) func(*sheet) tea.Cmd {
		types, err := c.IssueLinkTypes(ctx)
		return func(s *sheet) tea.Cmd {
			if err != nil {
				return s.failed(err)
			}
			if len(types) == 0 {
				return kernel.Warn("this site has no kinds of link")
			}
			k.types = types
			return s.ask("How is it linked?", "", true)
		}
	})
}

func (k *linksKind) changed(s *sheet, text string) tea.Cmd {
	s.cands = s.cands[:0]
	if k.phrase == nil {
		p := app.NewPattern(text)
		for _, t := range k.types {
			for _, ph := range [...]sheetRow{{text: t.Outward, id: t.ID, key: "out"}, {text: t.Inward, id: t.ID, key: "in"}} {
				if _, ok := p.Score(ph.text); ph.text != "" && ok && (ph.key == "out" || t.Inward != t.Outward) {
					ph.text = widget.Sanitize(ph.text)
					s.cands = append(s.cands, ph)
				}
			}
		}
		return nil
	}
	if key, ok := app.ParseKey(text); ok && key != s.key {
		s.cands = append(s.cands, sheetRow{text: key, key: key})
	} else if key, _, ok := app.ParseIssueURL(text); ok && key != s.key {
		s.cands = append(s.cands, sheetRow{text: key, key: key})
	}
	if s.deps.Cache == nil || text == "" {
		return nil
	}
	hits, err := app.SharedIndex(s.deps.Cache).Search(text, maxCands)
	if err != nil {
		return kernel.Warn("the cache on this machine could not be searched: " + err.Error())
	}
	for _, h := range hits {
		if h.Key != s.key && (len(s.cands) == 0 || s.cands[0].key != h.Key) {
			s.cands = append(s.cands, sheetRow{text: h.Key + "  " + widget.Sanitize(h.Summary), key: h.Key})
		}
	}
	return nil
}

func (k *linksKind) answered(s *sheet, _ string, pick *sheetRow) tea.Cmd {
	if k.phrase == nil {
		chosen := *pick
		k.phrase = &chosen
		return s.ask(s.key+" "+chosen.text+"… which issue? A key, or words from one opened before", "", true)
	}
	in := jira.LinkInput{TypeID: k.phrase.id, From: s.key, To: pick.key}
	if k.phrase.key == "in" {
		in.From, in.To = pick.key, s.key
	}
	said := s.key + " " + k.phrase.text + " " + pick.key
	s.endAsk()
	return s.write(func(ctx context.Context, c jira.SessionClient) (func(*sheet) tea.Cmd, error) {
		return k.written(said), c.LinkIssues(ctx, in)
	})
}

package issue

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/jira"
)

const nearEnd = 5

type childSeed struct {
	issues []jira.Issue
	page   jira.Page[jira.Issue]
}

type childOp uint8

const (
	opNone childOp = iota
	opAssign
	opStatus
	opPriority
)

type childrenKind struct {
	search *app.Search
	seed   *childSeed
	issues []jira.Issue
	page   jira.Page[jira.Issue]

	paging, rereads fetchSlot
	loadingMore     bool

	op         childOp
	target     jira.Issue
	moves      []jira.Transition
	priorities []sheetRow
}

var childrenKeys = newSheetKeys(
	sheetBind{kernel.Bind([]string{"enter"}, "enter", "open it"), sheetOpen},
	sheetBind{kernel.Bind([]string{"@"}, "@", "assign"), sheetAssign},
	sheetBind{kernel.Bind([]string{"t"}, "t", "status"), sheetStatus},
	sheetBind{kernel.Bind([]string{"P"}, "P", "priority"), sheetPriority},
)

func (k *childrenKind) keys() *sheetKeys { return childrenKeys }

func (k *childrenKind) load(s *sheet) tea.Cmd {
	k.paging.stop()
	k.paging.gen++
	k.loadingMore = false
	if k.seed != nil {
		seed := k.seed
		k.seed = nil
		k.set(s, seed.issues, seed.page)
		return nil
	}
	if k.search == nil {
		return nil
	}
	key, search := s.key, k.search
	return s.read(&s.loads, func(ctx context.Context, _ jira.SessionClient) func(*sheet) tea.Cmd {
		res, err := search.Run(ctx, app.Request{
			JQL: childrenJQL(key), Projection: app.ListProjection(), MaxResults: childrenPage,
		})
		return func(s *sheet) tea.Cmd {
			if err != nil {
				return s.failed(err)
			}
			k.set(s, res.Page.Items, res.Page)
			return nil
		}
	})
}

func (k *childrenKind) set(s *sheet, issues []jira.Issue, page jira.Page[jira.Issue]) {
	k.issues, k.page = slices.Clone(issues), page
	s.setRows(k.rows(s))
	s.note = k.note()
}

func (k *childrenKind) refresh(s *sheet) {
	s.rows = k.rows(s)
	s.note = k.note()
	s.moveBy(0)
}

func (k *childrenKind) note() string {
	n, done := rollup(k.issues)
	if k.page.HasMore() {
		return strconv.Itoa(n) + "+ children · " + strconv.Itoa(done) + " done so far"
	}
	return strconv.Itoa(n) + " " + childWord(n) + " · " + strconv.Itoa(done) + " done"
}

func childWord(n int) string {
	if n == 1 {
		return "child"
	}
	return "children"
}

func (k *childrenKind) rows(s *sheet) []sheetRow {
	t := s.deps.Theme
	type line struct{ key, typ, status, who, prio, summary string }
	lines := make([]line, len(k.issues))
	var w line
	widest := func(cur *string, text string) {
		if got := ansi.StringWidth(text); got > ansi.StringWidth(*cur) {
			*cur = text
		}
	}
	for i := range k.issues {
		iss := &k.issues[i]
		who := "unassigned"
		if iss.Assignee != nil {
			who = iss.Assignee.DisplayName
		}
		prio := ""
		if iss.Priority != nil {
			prio = iss.Priority.Name
		}
		lines[i] = line{
			key:     widget.Sanitize(iss.Key),
			typ:     widget.Sanitize(strings.TrimSpace(t.Glyphs.TypeGlyph(iss.Type) + " " + iss.Type.Name)),
			status:  widget.Sanitize(iss.Status.Name),
			who:     widget.Sanitize(who),
			prio:    widget.Sanitize(prio),
			summary: widget.Sanitize(iss.Summary),
		}
		widest(&w.key, lines[i].key)
		widest(&w.typ, lines[i].typ)
		widest(&w.status, lines[i].status)
		widest(&w.who, lines[i].who)
		widest(&w.prio, lines[i].prio)
	}
	rows := make([]sheetRow, len(lines))
	for i := range lines {
		l := &lines[i]
		text := l.key + column(l.key, ansi.StringWidth(w.key)+2) +
			l.typ + column(l.typ, ansi.StringWidth(w.typ)+2) +
			l.status + column(l.status, ansi.StringWidth(w.status)+2) +
			l.who + column(l.who, ansi.StringWidth(w.who)+2) +
			l.prio + column(l.prio, ansi.StringWidth(w.prio)+2) +
			l.summary
		rows[i] = sheetRow{text: text, id: k.issues[i].Key, key: k.issues[i].Key}
	}
	return rows
}

func (k *childrenKind) at(s *sheet) *jira.Issue {
	if row := s.current(); row != nil && s.cursor < len(k.issues) && k.issues[s.cursor].Key == row.key {
		return &k.issues[s.cursor]
	}
	return nil
}

func (k *childrenKind) more(s *sheet) tea.Cmd {
	if !k.page.HasMore() || k.loadingMore || s.cursor < len(s.rows)-nearEnd {
		return nil
	}
	k.loadingMore = true
	page := k.page
	return s.read(&k.paging, func(ctx context.Context, _ jira.SessionClient) func(*sheet) tea.Cmd {
		next, err := page.Next(ctx)
		return func(s *sheet) tea.Cmd {
			k.loadingMore = false
			if err != nil {
				return s.failed(err)
			}
			k.issues, k.page = append(slices.Clone(k.issues), next.Items...), next
			k.refresh(s)
			return nil
		}
	})
}

func (k *childrenKind) act(s *sheet, a sheetAct) tea.Cmd {
	iss := k.at(s)
	if iss == nil {
		return nil
	}
	if a == sheetOpen {
		return openSeeded(s.deps, *iss, append(slices.Clone(s.trail), ""))
	}
	if s.deps.Jira == nil {
		return kernel.Warn("there is no Jira connection in this session")
	}
	switch a {
	case sheetAssign:
		return k.startAssign(s, iss)
	case sheetStatus:
		return k.startStatus(s, iss)
	case sheetPriority:
		return k.startPriority(s, iss)
	default:
	}
	return nil
}

func (k *childrenKind) startAssign(s *sheet, iss *jira.Issue) tea.Cmd {
	if got := s.deps.Caps.Capability(jira.CapPeople); !got.OK {
		return kernel.Warn(firstNonEmpty(got.Reason, "this token cannot look accounts up"))
	}
	k.op, k.target = opAssign, *iss
	return s.ask("Assign "+iss.Key+" to… me, unassigned, or a name", "", true)
}

func (k *childrenKind) startStatus(s *sheet, iss *jira.Issue) tea.Cmd {
	target := *iss
	return s.read(&s.looks, func(ctx context.Context, c jira.SessionClient) func(*sheet) tea.Cmd {
		moves, err := c.Transitions(ctx, target.Key)
		return func(s *sheet) tea.Cmd {
			if err != nil {
				return s.failed(err)
			}
			if len(moves) == 0 {
				return kernel.Warn(target.Key + " has no moves from " + target.Status.Name)
			}
			k.op, k.target, k.moves = opStatus, target, moves
			return s.ask("Move "+target.Key+" to…", "", true)
		}
	})
}

func (k *childrenKind) startPriority(s *sheet, iss *jira.Issue) tea.Cmd {
	target := *iss
	return s.read(&s.looks, func(ctx context.Context, c jira.SessionClient) func(*sheet) tea.Cmd {
		meta, err := c.EditMeta(ctx, target.Key)
		return func(s *sheet) tea.Cmd {
			if err != nil {
				return s.failed(err)
			}
			at := slices.IndexFunc(meta.Fields, func(f jira.FieldMeta) bool { return f.Field.ID == "priority" })
			if at < 0 || len(meta.Fields[at].AllowedValues) == 0 {
				return kernel.Warn("Priority is not on " + target.Key + "'s edit screen")
			}
			k.priorities = k.priorities[:0]
			for _, o := range meta.Fields[at].AllowedValues {
				k.priorities = append(k.priorities, sheetRow{text: widget.Sanitize(firstNonEmpty(o.Label, o.ID)), id: o.ID})
			}
			k.op, k.target = opPriority, target
			return s.ask("Priority of "+target.Key+"…", "", true)
		}
	})
}

func (k *childrenKind) changed(s *sheet, text string) tea.Cmd {
	p := app.NewPattern(text)
	s.cands = s.cands[:0]
	switch k.op {
	case opAssign:
		s.cands = k.assignSeeds(s.cands, p)
		if strings.TrimSpace(text) == "" {
			s.looks.stop()
			return nil
		}
		project := projectOfKey(k.target.Key)
		return s.debounced(func(s *sheet) tea.Cmd {
			return s.read(&s.looks, func(ctx context.Context, c jira.SessionClient) func(*sheet) tea.Cmd {
				people, err := assignable(ctx, c, project, text, peopleLimit)
				return func(s *sheet) tea.Cmd {
					if err != nil {
						return s.failed(err)
					}
					s.cands = k.assignSeeds(s.cands[:0], p)
					for i := range people {
						s.cands = append(s.cands, sheetRow{text: widget.Sanitize(people[i].DisplayName), id: people[i].AccountID})
					}
					s.pick = min(s.pick, max(len(s.cands)-1, 0))
					return nil
				}
			})
		})
	case opStatus:
		for i := range k.moves {
			label := k.moves[i].Name + "  →  " + k.moves[i].To.Name
			if _, ok := p.Score(label); ok {
				s.cands = append(s.cands, sheetRow{text: widget.Sanitize(label), id: k.moves[i].ID})
			}
		}
	case opPriority:
		for i := range k.priorities {
			if _, ok := p.Score(k.priorities[i].text); ok {
				s.cands = append(s.cands, k.priorities[i])
			}
		}
	default:
	}
	return nil
}

func (k *childrenKind) assignSeeds(into []sheetRow, p app.Pattern) []sheetRow {
	for _, seed := range [...]sheetRow{{text: "Me", key: "me"}, {text: "Unassigned", key: "unassigned"}} {
		if _, ok := p.Score(seed.text); ok {
			into = append(into, seed)
		}
	}
	return into
}

func (k *childrenKind) answered(s *sheet, _ string, pick *sheetRow) tea.Cmd {
	chosen, target := *pick, k.target
	s.endAsk()
	switch k.op {
	case opAssign:
		return k.edit(s, &target, func(ctx context.Context, c jira.SessionClient) (string, error) {
			id, name := chosen.id, chosen.text
			switch chosen.key {
			case "me":
				me, err := c.Me(ctx)
				if err != nil {
					return "", err
				}
				id, name = me.AccountID, me.DisplayName
			case "unassigned":
				name = "unassigned"
			default:
			}
			err := app.SaveIssue(ctx, c, target.Key, app.BaseOf(target, "assignee"), jira.IssuePatch{Assignee: &id})
			return target.Key + " assigned to " + name, err
		})
	case opPriority:
		return k.edit(s, &target, func(ctx context.Context, c jira.SessionClient) (string, error) {
			id := chosen.id
			err := app.SaveIssue(ctx, c, target.Key, app.BaseOf(target, "priority"), jira.IssuePatch{PriorityID: &id})
			return target.Key + " priority is now " + chosen.text, err
		})
	case opStatus:
		return k.move(s, &target, chosen.id)
	default:
	}
	return nil
}

func (k *childrenKind) move(s *sheet, target *jira.Issue, id string) tea.Cmd {
	at := slices.IndexFunc(k.moves, func(tr jira.Transition) bool { return tr.ID == id })
	if at < 0 {
		return nil
	}
	tr, child := k.moves[at], *target
	if len(requiredFields(tr)) > 0 {
		return openSeeded(s.deps, child, append(slices.Clone(s.trail), ""), WithTransition(tr.ID))
	}
	s.confirm("Move "+child.Key+" to "+tr.To.Name+"?", func() tea.Cmd {
		return k.edit(s, &child, func(ctx context.Context, c jira.SessionClient) (string, error) {
			if err := app.CheckBase(ctx, c, child.Key, app.BaseOf(child, "status")); err != nil {
				return "", err
			}
			return child.Key + " moved to " + tr.To.Name, c.Transition(ctx, child.Key, tr.ID, jira.IssuePatch{})
		})
	}, nil)
	return nil
}

func (k *childrenKind) edit(s *sheet, target *jira.Issue, do func(context.Context, jira.SessionClient) (string, error)) tea.Cmd {
	child := target.Key
	return s.write(func(ctx context.Context, c jira.SessionClient) (func(*sheet) tea.Cmd, error) {
		said, err := do(ctx, c)
		var conflict *jira.ConflictError
		if errors.As(err, &conflict) {
			return func(s *sheet) tea.Cmd {
				s.fail = firstNonEmpty(conflict.Detail, conflict.Error()) + "; read again"
				return tea.Batch(k.reread(s, child), kernel.Warn(s.fail))
			}, nil
		}
		if err != nil {
			return nil, err
		}
		return func(s *sheet) tea.Cmd {
			return tea.Batch(k.reread(s, child), kernel.Broadcast(ChangedMsg{Key: child}), kernel.Status(said))
		}, nil
	})
}

func (k *childrenKind) reread(s *sheet, child string) tea.Cmd {
	return s.read(&k.rereads, func(ctx context.Context, c jira.SessionClient) func(*sheet) tea.Cmd {
		iss, err := c.IssueFields(ctx, child, app.ListProjection().IDs)
		return func(s *sheet) tea.Cmd {
			if err != nil {
				return s.failed(err)
			}
			if at := slices.IndexFunc(k.issues, func(c jira.Issue) bool { return c.Key == child }); at >= 0 {
				k.issues[at] = iss
				k.refresh(s)
			}
			return nil
		}
	})
}

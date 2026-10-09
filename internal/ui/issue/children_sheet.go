package issue

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	appissue "github.com/varijkapil13/saral/internal/app/issue"
	appmatch "github.com/varijkapil13/saral/internal/app/match"
	appquery "github.com/varijkapil13/saral/internal/app/query"
	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/internal/ui/widget/sortpick"
	"github.com/varijkapil13/saral/pkg/jira"
)

const nearEnd = 5

type childSeed struct {
	issues   []jira.Issue
	page     jira.Page[jira.Issue]
	haveRank bool
	restRead bool
}

type childOp uint8

const (
	opNone childOp = iota
	opAssign
	opStatus
	opPriority
)

type childrenKind struct {
	search *appquery.Search
	seed   *childSeed
	issues []jira.Issue
	page   jira.Page[jira.Issue]

	paging, rereads, sorting fetchSlot
	loadingMore              bool

	order    childOrder
	haveRank bool
	restRead bool
	applied  sortpick.Choice
	idx      []int
	bound    int

	op         childOp
	target     jira.Issue
	moves      []jira.Transition
	priorities []sheetRow
}

var childrenKeys = newSheetKeys(
	sheetBind{kernel.Canon(kernel.ActOpen, "open it"), sheetOpen},
	sheetBind{kernel.Canon(kernel.ActAssign), sheetAssign},
	sheetBind{kernel.Canon(kernel.ActStatus), sheetStatus},
	sheetBind{kernel.Canon(kernel.ActPriority), sheetPriority},
	sheetBind{kernel.Canon(kernel.ActSort), sheetSort},
)

func (k *childrenKind) keys() *sheetKeys { return childrenKeys }

func (k *childrenKind) load(s *sheet) tea.Cmd {
	k.paging.stop()
	k.paging.gen++
	k.loadingMore = false
	if k.seed != nil {
		seed := k.seed
		k.seed = nil
		k.haveRank, k.restRead = seed.haveRank, seed.restRead
		k.set(s, seed.issues, seed.page)
		return k.resort(s)
	}
	if k.search == nil {
		return nil
	}
	in := appissue.ChildRead{
		Key: s.key, Search: k.search, Choice: appissue.ChildSort(currentChildSort()), Order: k.order.ChildOrder, Bound: k.bound,
	}
	return s.read(&s.loads, func(ctx context.Context, c jira.SessionClient) func(*sheet) tea.Cmd {
		in.Vocab = c
		d := in.Run(ctx)
		return func(s *sheet) tea.Cmd {
			if d.Err != nil {
				return s.failed(d.Err)
			}
			cmd := k.adopt(d)
			k.set(s, d.Page.Items, d.Page)
			return cmd
		}
	})
}

func (k *childrenKind) adopt(d appissue.ChildReadDone) tea.Cmd {
	k.order.ChildOrder, k.haveRank, k.restRead = d.Order, d.HaveRank, d.RestRead
	if d.PriorityErr == nil || k.order.prioWarned {
		return nil
	}
	k.order.prioWarned = true
	return kernel.Warn(priorityWarning(d.PriorityErr))
}

func (k *childrenKind) set(s *sheet, issues []jira.Issue, page jira.Page[jira.Issue]) {
	k.issues, k.page = slices.Clone(issues), page
	k.reorder()
	s.setRows(k.rows(s))
	s.note = k.note(s)
}

func (k *childrenKind) reorder() {
	k.applied = k.order.effective(currentChildSort())
	k.idx = orderIndex(k.issues, k.applied, &k.order, k.idx)
}

func (k *childrenKind) refresh(s *sheet) {
	cur := k.cursorKey(s)
	k.idx = orderIndex(k.issues, k.applied, &k.order, k.idx)
	k.redraw(s, cur)
}

func (k *childrenKind) redraw(s *sheet, cur string) {
	s.rows = k.rows(s)
	s.note = k.note(s)
	if cur != "" {
		if at := slices.IndexFunc(s.rows, func(r sheetRow) bool { return r.key == cur }); at >= 0 {
			s.cursor = at
		}
	}
	s.moveBy(0)
}

func (k *childrenKind) cursorKey(s *sheet) string {
	if row := s.current(); row != nil {
		return row.key
	}
	return ""
}

func (k *childrenKind) note(s *sheet) string {
	n, done := appissue.Rollup(k.issues)
	text := strconv.Itoa(n) + " " + childWord(n) + " · " + strconv.Itoa(done) + " done"
	if k.page.HasMore() {
		text = strconv.Itoa(n) + "+ children · " + strconv.Itoa(done) + " done so far"
	}
	if k.applied.Chosen() {
		text += " · sort: " + sortLabel(k.applied, s.deps.Theme.Glyphs)
		if k.page.HasMore() {
			text += " · sorted over the first " + strconv.Itoa(n) + " of " + strconv.Itoa(n) + "+"
		}
	}
	return text
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
	for i, at := range k.idx {
		l := &lines[at]
		text := l.key + column(l.key, ansi.StringWidth(w.key)+2) +
			l.typ + column(l.typ, ansi.StringWidth(w.typ)+2) +
			l.status + column(l.status, ansi.StringWidth(w.status)+2) +
			l.who + column(l.who, ansi.StringWidth(w.who)+2) +
			l.prio + column(l.prio, ansi.StringWidth(w.prio)+2) +
			l.summary
		rows[i] = sheetRow{text: text, id: k.issues[at].Key, key: k.issues[at].Key}
	}
	return rows
}

func (k *childrenKind) at(s *sheet) *jira.Issue {
	if row := s.current(); row != nil && s.cursor < len(k.idx) {
		if iss := &k.issues[k.idx[s.cursor]]; iss.Key == row.key {
			return iss
		}
	}
	return nil
}

func (k *childrenKind) more(s *sheet) tea.Cmd {
	if !k.page.HasMore() || k.loadingMore || k.applied.Chosen() || s.cursor < len(s.rows)-nearEnd {
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
	if a == sheetSort {
		return k.startSort(s)
	}
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
		moves, err := appissue.Moves(ctx, c, target.Key)
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
		meta, err := appissue.EditScreen(ctx, c, target.Key)
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
	p := appmatch.NewPattern(text)
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
				people, err := appissue.Assignable(ctx, c, project, text, peopleLimit)
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

func (k *childrenKind) assignSeeds(into []sheetRow, p appmatch.Pattern) []sheetRow {
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
			if chosen.key == "unassigned" {
				id, name = "", "unassigned"
			}
			who, err := appissue.Assign(ctx, c, target, id, chosen.key == "me")
			if chosen.key == "me" {
				name = who.DisplayName
			}
			return target.Key + " assigned to " + name, err
		})
	case opPriority:
		return k.edit(s, &target, func(ctx context.Context, c jira.SessionClient) (string, error) {
			err := appissue.SetPriority(ctx, c, target, chosen.id)
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
		return pushSeeded(s.deps, child, append(slices.Clone(s.trail), ""), WithTransition(tr.ID))
	}
	s.confirm("Move "+child.Key+" to "+tr.To.Name+"?", func() tea.Cmd {
		return k.edit(s, &child, func(ctx context.Context, c jira.SessionClient) (string, error) {
			return child.Key + " moved to " + tr.To.Name, appissue.MoveFrom(ctx, c, child, tr.ID)
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
	rankID := ""
	if k.haveRank {
		rankID = k.order.RankID
	}
	return s.read(&k.rereads, func(ctx context.Context, c jira.SessionClient) func(*sheet) tea.Cmd {
		iss, err := appissue.ReadFields(ctx, c, child, appissue.ChildProjection(rankID).IDs)
		return func(s *sheet) tea.Cmd {
			if err != nil {
				return s.failed(err)
			}
			if at := slices.IndexFunc(k.issues, func(c jira.Issue) bool { return c.Key == child }); at >= 0 {
				k.issues = slices.Clone(k.issues)
				k.issues[at] = iss
				k.refresh(s)
			}
			return nil
		}
	})
}

func (k *childrenKind) sortCurrent() sortpick.Choice {
	if c := k.order.effective(currentChildSort()); c.Chosen() {
		return c
	}
	return sortpick.Choice{Field: fieldCreated}
}

func (k *childrenKind) sortLine(s *sheet) string {
	return sortpick.Line(s.picker.Fields, k.sortCurrent(), s.picker.Cursor, s.deps.Theme.Glyphs)
}

func (k *childrenKind) startSort(s *sheet) tea.Cmd {
	open := func(s *sheet) {
		s.picker.Fields = k.order.fields()
		s.picker.Start(k.sortCurrent())
	}
	if k.order.RankTried || k.search == nil {
		k.order.RankTried = true
		open(s)
		return nil
	}
	search := k.search
	return s.read(&s.looks, func(ctx context.Context, _ jira.SessionClient) func(*sheet) tea.Cmd {
		rankID, err := appissue.RankField(ctx, search)
		return func(s *sheet) tea.Cmd {
			if err == nil {
				k.order.RankTried, k.order.RankID = true, rankID
			}
			open(s)
			return nil
		}
	})
}

func (k *childrenKind) sortChosen(s *sheet, c sortpick.Choice) tea.Cmd {
	c = normalizeChoice(c)
	if c == normalizeChoice(currentChildSort()) {
		return nil
	}
	set := setChildSort(c)
	return tea.Batch(set, k.resort(s))
}

func (k *childrenKind) resort(s *sheet) tea.Cmd {
	raw := currentChildSort()
	if !k.order.needs(raw, k.haveRank, k.restRead, k.page) {
		k.applyOrder(s)
		return nil
	}
	page := k.page
	page.Items = k.issues
	in := appissue.ChildRead{
		Key: s.key, Search: k.search, Choice: appissue.ChildSort(raw), Order: k.order.ChildOrder, Page: page,
		Loaded: true, HaveRank: k.haveRank, RestRead: k.restRead, Bound: k.bound,
	}
	cmd := s.read(&k.sorting, func(ctx context.Context, c jira.SessionClient) func(*sheet) tea.Cmd {
		in.Vocab = c
		d := in.Run(ctx)
		return func(s *sheet) tea.Cmd {
			if d.Err != nil {
				return s.failed(d.Err)
			}
			warn := k.adopt(d)
			k.paging.stop()
			k.paging.gen++
			k.loadingMore = false
			k.issues, k.page = slices.Clone(d.Page.Items), d.Page
			k.applyOrder(s)
			return warn
		}
	})
	if cmd == nil {
		k.applyOrder(s)
	}
	return cmd
}

func (k *childrenKind) applyOrder(s *sheet) {
	cur := k.cursorKey(s)
	k.reorder()
	k.redraw(s, cur)
}

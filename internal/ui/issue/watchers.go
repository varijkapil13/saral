package issue

import (
	"context"
	"strconv"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/ui/kernel"
	"github.com/varijkapil13/saral/internal/ui/widget"
	"github.com/varijkapil13/saral/pkg/jira"
)

const peopleLimit = 8

type watchKind struct {
	watching bool
}

var watchKeys = newSheetKeys(
	sheetBind{kernel.Bind([]string{"w"}, "w", "watch or stop"), sheetToggle},
	sheetBind{kernel.Bind([]string{"a"}, "a", "add someone"), sheetAdd},
	sheetBind{kernel.Bind([]string{"d", "x"}, "d", "remove them"), sheetRemove},
)

func (k *watchKind) keys() *sheetKeys { return watchKeys }

func (k *watchKind) load(s *sheet) tea.Cmd {
	key := s.key
	return s.read(&s.loads, func(ctx context.Context, c jira.SessionClient) func(*sheet) tea.Cmd {
		list, err := c.Watchers(ctx, key)
		return func(s *sheet) tea.Cmd {
			if err != nil {
				return s.failed(err)
			}
			k.watching = list.Watching
			rows := make([]sheetRow, 0, len(list.People))
			for _, p := range list.People {
				rows = append(rows, sheetRow{text: widget.Sanitize(p.DisplayName), id: p.AccountID})
			}
			s.setRows(rows)
			s.note = watchNote(list)
			return nil
		}
	})
}

func watchNote(l jira.WatcherList) string {
	note := strconv.Itoa(l.Count) + " watching; you are not one of them"
	if l.Watching {
		note = strconv.Itoa(l.Count) + " watching, you among them"
	}
	if len(l.People) < l.Count {
		note += "; this account may see " + strconv.Itoa(len(l.People)) + " of them"
	}
	return note
}

func (k *watchKind) act(s *sheet, a sheetAct) tea.Cmd {
	key := s.key
	switch a {
	case sheetToggle:
		stop := k.watching
		return s.write(func(ctx context.Context, c jira.SessionClient) (func(*sheet) tea.Cmd, error) {
			if !stop {
				return k.written("watching " + key), c.Watch(ctx, key, "")
			}
			me, err := c.Me(ctx)
			if err != nil {
				return nil, err
			}
			return k.written("stopped watching " + key), c.Unwatch(ctx, key, me.AccountID)
		})
	case sheetAdd:
		return s.ask("Who should watch it? A name or an email", "", true)
	case sheetRemove:
		row := s.current()
		if row == nil {
			return nil
		}
		id, name := row.id, row.text
		return s.write(func(ctx context.Context, c jira.SessionClient) (func(*sheet) tea.Cmd, error) {
			return k.written(name + " no longer watches " + key), c.Unwatch(ctx, key, id)
		})
	default:
	}
	return nil
}

func (k *watchKind) written(said string) func(*sheet) tea.Cmd {
	return func(s *sheet) tea.Cmd { return tea.Batch(k.load(s), kernel.Status(said)) }
}

func (k *watchKind) changed(s *sheet, text string) tea.Cmd {
	if text == "" {
		s.looks.stop()
		s.cands = nil
		return nil
	}
	return s.debounced(func(s *sheet) tea.Cmd {
		return s.read(&s.looks, func(ctx context.Context, c jira.SessionClient) func(*sheet) tea.Cmd {
			people, err := c.FindPeople(ctx, jira.PeopleQuery{Match: text, Limit: peopleLimit})
			return func(s *sheet) tea.Cmd {
				if err != nil {
					return s.failed(err)
				}
				s.cands = s.cands[:0]
				for _, p := range people {
					s.cands = append(s.cands, sheetRow{text: widget.Sanitize(p.DisplayName), id: p.AccountID})
				}
				s.pick = min(s.pick, max(len(s.cands)-1, 0))
				return nil
			}
		})
	})
}

func (k *watchKind) answered(s *sheet, _ string, pick *sheetRow) tea.Cmd {
	key, id, name := s.key, pick.id, pick.text
	s.endAsk()
	return s.write(func(ctx context.Context, c jira.SessionClient) (func(*sheet) tea.Cmd, error) {
		return k.written(name + " now watches " + key), c.Watch(ctx, key, id)
	})
}

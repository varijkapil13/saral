package onboarding

import (
	"context"
	"errors"
	"maps"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/pkg/jira"
)

// busy is which call is in the air. Only one blocking call runs at a time; the
// project suggestions are the one thing fetched in the background.
type busy int

const (
	busyNone busy = iota
	busyConnect
	busyProbe
	busySave
)

func (b busy) String() string {
	switch b {
	case busyConnect:
		return "Checking the site, the email and the token"
	case busyProbe:
		return "Asking what this token can do"
	case busySave:
		return "Writing the profile"
	default:
		return ""
	}
}

type configLoadedMsg struct {
	cfg  config.Config
	path string
	err  error
}

type connectedMsg struct {
	seq     int
	client  jira.SessionClient
	account jira.User
}

type connectFailedMsg struct {
	seq int
	err error
}

type projectsFoundMsg struct {
	seq  int
	keys []string
}

type projectsUnknownMsg struct {
	seq int
	err error
}

type probedMsg struct {
	seq     int
	project string
	caps    jira.Capabilities
}

type probeFailedMsg struct {
	seq int
	err error
}

type savedMsg struct {
	seq     int
	path    string
	stored  string
	warning string
}

type saveFailedMsg struct {
	seq int
	err error
}

// loadConfig reads whatever config file is already there. A missing one is the
// first run and not a failure; one that cannot be parsed is, because writing
// over it would lose the profiles in it.
func loadConfig() tea.Msg {
	path, err := config.Path()
	if err != nil {
		return configLoadedMsg{cfg: config.Config{Mouse: true}, err: err}
	}
	cfg, err := config.LoadFile(path)
	if errors.Is(err, config.ErrNoConfig) {
		return configLoadedMsg{cfg: config.Config{Mouse: true}, path: path}
	}
	return configLoadedMsg{cfg: cfg, path: path, err: err}
}

func (m *Model) configLoaded(msg configLoadedMsg) tea.Cmd {
	m.cfgPath, m.cfgErr = msg.path, msg.err
	if msg.err == nil {
		m.cfg = msg.cfg
	}
	m.cache.reset()
	return nil
}

// stop cancels whatever is in the air, which is what closing or looking away
// from this view has to do: a verification the user has walked away from must
// not still be running.
func (m *Model) stop() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	if m.cancelBg != nil {
		m.cancelBg()
		m.cancelBg = nil
	}
	m.busy = busyNone
}

func (m Model) stale(seq int) bool { return seq != m.seq }

// start runs one blocking call under its own context and bumps the sequence
// number, so that an answer to a question the user has moved past is dropped.
func (m *Model) start(kind busy, run func(context.Context, int) tea.Msg) tea.Cmd {
	m.stop()
	m.seq++
	seq := m.seq
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	m.cancel, m.busy, m.last = cancel, kind, kind
	m.problem, m.note = "", ""
	return tea.Batch(m.spin.Tick, func() tea.Msg {
		defer cancel()
		return run(ctx, seq)
	})
}

func (m *Model) verify() tea.Cmd {
	site, email := m.value(fieldSite), m.value(fieldEmail)
	token := m.input[fieldToken].Value()
	connect := m.connect
	return m.start(busyConnect, func(ctx context.Context, seq int) tea.Msg {
		client, err := connect(site, email, token)
		if err != nil {
			return connectFailedMsg{seq: seq, err: err}
		}
		account, err := client.Me(ctx)
		if err != nil {
			return connectFailedMsg{seq: seq, err: err}
		}
		return connectedMsg{seq: seq, client: client, account: account}
	})
}

func (m *Model) connected(msg connectedMsg) tea.Cmd {
	if m.stale(msg.seq) {
		return nil
	}
	m.busy, m.last = busyNone, busyNone
	m.client, m.account = msg.client, msg.account
	m.search = app.NewSearch(msg.client)
	m.probed, m.caps = false, jira.Capabilities{}
	return tea.Batch(m.goTo(stepStorage), m.suggest())
}

// connectFailed sends the user back to the field that can fix it. A rejected
// credential and an unreachable host are different problems with different
// answers, and landing on the wrong field is how someone retypes a token that
// was right all along.
func (m *Model) connectFailed(msg connectFailedMsg) tea.Cmd {
	if m.stale(msg.seq) {
		return nil
	}
	m.busy = busyNone
	m.problem = reason(msg.err)

	var rejected *jira.AuthError
	var unreachable *jira.TransportError
	switch {
	case errors.As(msg.err, &rejected):
		return m.stay(stepToken)
	case errors.As(msg.err, &unreachable):
		m.note = "Nothing was written. The site, the email and the token are all still here."
		return m.stay(stepSite)
	}
	return m.stay(stepToken)
}

// stay puts the user on the step that can fix the problem without clearing the
// message that says what it is.
func (m *Model) stay(on step) tea.Cmd {
	problem, note := m.problem, m.note
	cmd := m.goTo(on)
	m.problem, m.note = problem, note
	return cmd
}

// suggest looks for the projects this account has been working in, so that the
// picker offers something rather than demanding a key from memory. It is a
// convenience: a failure is a note, never a refusal.
func (m *Model) suggest() tea.Cmd {
	if m.search == nil {
		return nil
	}
	if m.cancelBg != nil {
		m.cancelBg()
	}
	search, seq := m.search, m.seq
	ctx, cancel := context.WithTimeout(context.Background(), opTimeout)
	m.cancelBg = cancel
	m.looking, m.suggested, m.lookup = true, nil, ""
	return func() tea.Msg {
		defer cancel()
		keys, err := recentProjects(ctx, search)
		if err != nil {
			return projectsUnknownMsg{seq: seq, err: err}
		}
		return projectsFoundMsg{seq: seq, keys: keys}
	}
}

// suggestionLimit is how many issues the picker reads to find project keys. It
// is a page, not a walk: the answer is a handful of keys and paging further
// only finds projects the account has not touched recently.
const suggestionLimit = 50

// recentProjects reads the projects behind the account's own recent issues,
// then anything it can see at all. Both queries ask for one field.
func recentProjects(ctx context.Context, search *app.Search) ([]string, error) {
	projection := app.Projection{Name: "project picker", IDs: []string{"project"}}
	for _, jql := range []string{"assignee = currentUser() ORDER BY updated DESC", "ORDER BY updated DESC"} {
		result, err := search.Run(ctx, app.Request{JQL: jql, Projection: projection, MaxResults: suggestionLimit})
		if err != nil {
			return nil, err
		}
		if keys := distinctProjects(result.Page.Items); len(keys) > 0 {
			return keys, nil
		}
	}
	return nil, nil
}

// distinctProjects keeps the order the issues came back in, which is the order
// the query sorted them by and therefore the order worth offering.
func distinctProjects(issues []jira.Issue) []string {
	seen := make(map[string]bool, len(issues))
	keys := make([]string, 0, 4)
	for i := range issues {
		key := issues[i].Project.Key
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		keys = append(keys, key)
	}
	return keys
}

func (m *Model) probe() tea.Cmd {
	if m.client == nil {
		m.problem = "the site has not been checked yet"
		return m.stay(stepToken)
	}
	client, project := m.client, m.value(fieldProject)
	return m.start(busyProbe, func(ctx context.Context, seq int) tea.Msg {
		caps, err := client.Capabilities(ctx, project)
		if err != nil {
			return probeFailedMsg{seq: seq, err: err}
		}
		return probedMsg{seq: seq, project: project, caps: caps}
	})
}

func (m *Model) probeLanded(msg probedMsg) tea.Cmd {
	if m.stale(msg.seq) {
		return nil
	}
	m.busy, m.last = busyNone, busyNone
	m.caps, m.project, m.probed = msg.caps, msg.project, true
	return m.goTo(stepReview)
}

func (m *Model) probeFailed(msg probeFailedMsg) tea.Cmd {
	if m.stale(msg.seq) {
		return nil
	}
	m.busy = busyNone
	m.problem = reason(msg.err)

	var rejected *jira.AuthError
	if errors.As(msg.err, &rejected) {
		return m.stay(stepToken)
	}
	return nil
}

func (m *Model) save() tea.Cmd {
	profile, err := m.profile()
	if err != nil {
		m.problem = err.Error()
		return nil
	}
	switch {
	case m.cfgErr != nil:
		m.problem = "refusing to write over a config file Saral cannot read: " + m.cfgErr.Error()
		return nil
	case m.cfgPath == "":
		m.problem = "Saral cannot work out where its config file goes; set SARAL_CONFIG_DIR"
		return nil
	}
	path := m.cfgPath
	token := m.input[fieldToken].Value()
	cfg := config.Config{Active: profile.Name, Mouse: m.cfg.Mouse, Profiles: map[string]config.Profile{}}
	maps.Copy(cfg.Profiles, m.cfg.Profiles)
	cfg.Profiles[profile.Name] = profile

	return m.start(busySave, func(ctx context.Context, seq int) tea.Msg {
		// The keychain goes first: a config file naming an entry that was never
		// written is a profile that fails on every screen, which is the whole
		// thing this view exists to prevent.
		if profile.Token.Keychain != "" {
			if err := profile.StoreToken(token); err != nil {
				return saveFailedMsg{seq: seq, err: err}
			}
		}
		if err := cfg.Save(path); err != nil {
			return saveFailedMsg{seq: seq, err: err}
		}
		return savedMsg{seq: seq, path: path, stored: profile.Token.String(), warning: checkResolves(ctx, profile, token)}
	})
}

// checkResolves reads the token back the way the next start will, so that a
// variable nobody exported or a command nobody has is reported now rather than
// at the next start, when the wording would be about a broken profile.
func checkResolves(ctx context.Context, profile config.Profile, token string) string {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	got, err := profile.ResolveToken(ctx)
	switch {
	case err != nil:
		return err.Error()
	case got != token:
		return "the " + profile.Token.String() + " answers with a different token than the one just verified"
	}
	return ""
}

func (m *Model) saveLanded(msg savedMsg) tea.Cmd {
	if m.stale(msg.seq) {
		return nil
	}
	m.busy, m.last = busyNone, busyNone
	m.savedTo, m.name, m.stored = msg.path, m.profileName(), msg.stored
	cmd := m.goTo(stepDone)
	m.problem = msg.warning
	return cmd
}

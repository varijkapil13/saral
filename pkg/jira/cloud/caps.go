package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/varijkapil13/saral/pkg/jira"
)

const (
	capsPermissionsPath   = "/rest/api/3/mypermissions"
	capsConfigurationPath = "/rest/api/3/configuration"
	capsMyselfPath        = "/rest/api/3/myself"
	// The doubled segment is correct: /rest/api/3/plans does not exist.
	capsPlansPath  = "/rest/api/3/plans/plan"
	capsBoardsPath = "/rest/agile/1.0/board"
)

// capsApply writes one probe's answer into the result. A probe hands one back
// instead of writing the struct itself, so that the writing happens after the
// goroutines have joined rather than while they are still running.
type capsApply func(*jira.Capabilities)

// capsProbe is one independent question about this site, token and project.
type capsProbe func(context.Context) (capsApply, error)

// Capabilities probes what this site, token and project allow, and gives every
// negative a sentence the UI can show as it stands.
//
// The probes run concurrently and independently. A site that will not answer
// for attachments still reports its boards, because a refusal, a failure and a
// silence are three different answers and only the caller can tell which of
// them matters. Nothing here returns an error for a capability being absent:
// that is the result. The error is reserved for what makes every answer
// meaningless — a cancelled context, a credential Jira rejected, and a rate
// limit, which says to ask again rather than anything about the token.
//
// An empty projectKey probes only what is site-wide. Boards, BulkMove and
// DeleteIssues then come back unavailable with a reason saying that no project
// was named, which is deliberately not the same answer as "this token may not":
// Jira reports a project permission held in any project at all as held in the
// global context, so an unscoped probe would report Move as available to a
// token that cannot move an issue in the project actually being looked at.
func (c *Client) Capabilities(ctx context.Context, projectKey string) (jira.Capabilities, error) {
	project := strings.TrimSpace(projectKey)
	caps := jira.Capabilities{Graphics: capsDetectGraphics(os.Getenv)}

	probes := []capsProbe{c.capsAttachments, c.capsTimeZone, c.capsPlans}
	if project == "" {
		capsWithoutProject(&caps)
	} else {
		probes = append(probes,
			func(ctx context.Context) (capsApply, error) { return c.capsPermissions(ctx, project) },
			func(ctx context.Context) (capsApply, error) { return c.capsBoards(ctx, project) },
		)
	}

	applies := make([]capsApply, len(probes))
	errs := make([]error, len(probes))
	var wg sync.WaitGroup
	for i, probe := range probes {
		wg.Go(func() { applies[i], errs[i] = probe(ctx) })
	}
	wg.Wait()

	if err := capsVoid(ctx, errs); err != nil {
		return jira.Capabilities{}, err
	}
	for _, apply := range applies {
		apply(&caps)
	}
	return caps, nil
}

// capsVoid reports the failure that makes the whole probe meaningless rather
// than one capability absent. A rejected credential and a rate limit are both
// answers about the next attempt, so recording either as an absent capability
// would tell someone with a mistyped token that they lack Bulk Change — and the
// result is cached, so that would outlive the minute that caused it.
func capsVoid(ctx context.Context, errs []error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, err := range errs {
		var rejected *jira.AuthError
		if errors.As(err, &rejected) {
			return rejected
		}
	}
	for _, err := range errs {
		var limited *jira.RateLimitError
		if errors.As(err, &limited) {
			return limited
		}
	}
	// A host that never answered makes every answer as meaningless as a rejected
	// credential does. Reporting five confident denials instead would be cached
	// as "this token may do nothing", and the caller has no typed signal to tell
	// a locked-down token from a mistyped site or one dropped packet —
	// docs/ARCHITECTURE.md wants a transport failure to leave the cached answer
	// standing behind a stale badge, which it cannot do if the probe says it
	// succeeded.
	var unreachable *jira.TransportError
	for _, err := range errs {
		if err == nil {
			return nil
		}
		var broken *jira.TransportError
		if !errors.As(err, &broken) {
			return nil
		}
		unreachable = broken
	}
	if unreachable != nil {
		return unreachable
	}
	return nil
}

// capsWithoutProject answers the three per-project capabilities when there is
// no project to ask about. Saying so is the whole point: leaving them absent
// with no reason reads as "this token may not", which is a different answer.
func capsWithoutProject(caps *jira.Capabilities) {
	caps.Boards = jira.Capability{
		Reason: "No project is selected, and a board belongs to a project",
	}
	caps.BulkMove = jira.Capability{
		Reason: "No project is selected, and Jira grants Move Issues and Create Issues per project",
	}
	caps.DeleteIssues = jira.Capability{
		Reason: "No project is selected, and Jira grants Delete Issues per project",
	}
}

// capsFailed is the Reason for a probe that did not come back with an answer. A
// 403 is an answer, and Jira's own sentence for it is what the user reads: the
// site knows which permission scheme refused and this client does not. Anything
// else says what could not be checked, because "no" and "not known" must not
// read the same in the footer.
func capsFailed(subject string, err error) string {
	var refused *jira.CapabilityError
	if errors.As(err, &refused) {
		return refused.Error()
	}
	return "Jira did not answer " + subject + ": " + err.Error()
}

// capsAttachments asks whether the site has attachments switched on at all. It
// is deliberately not also a permission check: reading one needs nothing beyond
// seeing the issue, so folding Create Attachments in here would hide the whole
// view from someone who may perfectly well read it.
func (c *Client) capsAttachments(ctx context.Context) (capsApply, error) {
	var body struct {
		AttachmentsEnabled bool `json:"attachmentsEnabled"`
	}
	err := c.doJSON(ctx, request{
		method: http.MethodGet,
		path:   capsConfigurationPath,
		kind:   "the site configuration",
		id:     c.base.Host,
	}, &body)

	var got jira.Capability
	switch {
	case err != nil:
		got.Reason = capsFailed("whether attachments are enabled on this site", err)
	case body.AttachmentsEnabled:
		got.OK = true
	default:
		got.Reason = "Attachments are switched off for this site, which only a Jira administrator can change"
	}
	return func(caps *jira.Capabilities) { caps.Attachments = got }, err
}

// capsTimeZone reads the account's timezone, which is the one Jira renders its
// own dates in and the one this client must render them in too. The machine's
// zone is not it: an issue due "today" is due today where the account lives.
func (c *Client) capsTimeZone(ctx context.Context) (capsApply, error) {
	var body struct {
		TimeZone string `json:"timeZone"`
	}
	err := c.doJSON(ctx, request{
		method: http.MethodGet,
		path:   capsMyselfPath,
		kind:   "account",
		id:     "the authenticated account",
	}, &body)

	var zone *time.Location
	if err == nil && body.TimeZone != "" {
		// A machine carrying no zoneinfo database, or a zone name Go does not
		// know, is not a Jira failure and must not read as one: dates then
		// render in UTC, which is what Capabilities.Location falls back to.
		if loaded, loadErr := time.LoadLocation(body.TimeZone); loadErr == nil {
			zone = loaded
		}
	}
	return func(caps *jira.Capabilities) { caps.TimeZone = zone }, err
}

// capsPlans asks whether the Plans API answers this token at all. A 403 is the
// ordinary reply — every one of its endpoints wants Administer Jira, and the
// per-plan rights the web UI hands out do not grant it — so it is a capability
// result here rather than a failure.
func (c *Client) capsPlans(ctx context.Context) (capsApply, error) {
	_, err := c.do(ctx, request{
		method: http.MethodGet,
		path:   capsPlansPath,
		query:  url.Values{"maxResults": []string{"1"}},
		kind:   "the plan list",
		id:     capsPlansPath,
	})

	var got jira.Capability
	var missing *jira.NotFoundError
	switch {
	case err == nil:
		got.OK = true
	case errors.As(err, &missing):
		got.Reason = "This site has no Plans API, which arrives with a Jira Premium subscription"
	default:
		got.Reason = capsFailed("whether this token can read plans", err)
	}
	return func(caps *jira.Capabilities) { caps.Plans = got }, err
}

// capsBoards asks whether the project has a board. Absence is an ordinary
// answer — a project can be run entirely from the issue list — and it is not
// the same answer as the Agile API refusing to say.
func (c *Client) capsBoards(ctx context.Context, projectKey string) (capsApply, error) {
	req := request{
		method: http.MethodGet,
		path:   capsBoardsPath,
		query: url.Values{
			"projectKeyOrId": []string{projectKey},
			"maxResults":     []string{"1"},
		},
		kind: "project",
		id:   projectKey,
	}
	resp, err := c.do(ctx, req)
	// Only how many came back matters here; what is in a board is not this
	// probe's business.
	var boards []json.RawMessage
	if err == nil {
		boards, _, _, err = decodeAgilePage[json.RawMessage](resp, req.op())
	}

	var got jira.Capability
	switch {
	case err != nil:
		got.Reason = capsFailed("whether "+projectKey+" has a board", err)
	case len(boards) == 0:
		got.Reason = projectKey + " has no board"
	default:
		got.OK = true
	}
	return func(caps *jira.Capabilities) { caps.Boards = got }, err
}

// capsPermission is one entry of the mypermissions response. Name is the site's
// own wording for the permission, in the site's own language, which is what the
// user's administrator sees and therefore what to put in front of the user.
type capsPermission struct {
	Name           string `json:"name"`
	HavePermission bool   `json:"havePermission"`
}

func (p capsPermission) label(fallback string) string {
	if name := strings.TrimSpace(p.Name); name != "" {
		return name
	}
	return fallback
}

// capsRequirement is one permission a capability needs. The label is only used
// where the site sent no name of its own for it, which is every permission the
// site left out of its answer.
type capsRequirement struct {
	key   string
	label string
}

var (
	// Moving an issue to another project needs the global Bulk Change
	// permission, Move in the project it leaves and Create in the one it
	// arrives in. Only the project being probed is known here, so this answers
	// for a move out of it; the move itself has to re-check Create against
	// whichever project the user picks as the target.
	capsBulkMoveNeeds = []capsRequirement{
		{key: "BULK_CHANGE", label: "Bulk Change"},
		{key: "MOVE_ISSUES", label: "Move Issues"},
		{key: "CREATE_ISSUES", label: "Create Issues"},
	}
	capsDeleteIssuesNeeds = []capsRequirement{
		{key: "DELETE_ISSUES", label: "Delete Issues"},
	}
)

// capsWanted is the permissions parameter, which is required and decides the
// whole of the answer: Jira replies with the keys asked for and nothing else.
func capsWanted() string {
	keys := make([]string, 0, len(capsBulkMoveNeeds)+len(capsDeleteIssuesNeeds))
	for _, need := range slices.Concat(capsBulkMoveNeeds, capsDeleteIssuesNeeds) {
		keys = append(keys, need.key)
	}
	slices.Sort(keys)
	return strings.Join(slices.Compact(keys), ",")
}

// capsPermissions asks what this token may do in one project.
func (c *Client) capsPermissions(ctx context.Context, projectKey string) (capsApply, error) {
	var body struct {
		Permissions map[string]capsPermission `json:"permissions"`
	}
	err := c.doJSON(ctx, request{
		method: http.MethodGet,
		path:   capsPermissionsPath,
		query: url.Values{
			"projectKey":  []string{projectKey},
			"permissions": []string{capsWanted()},
		},
		kind: "project",
		id:   projectKey,
	}, &body)

	var bulkMove, deleteIssues jira.Capability
	if err != nil {
		unknown := jira.Capability{Reason: capsFailed("what this token may do in "+projectKey, err)}
		bulkMove, deleteIssues = unknown, unknown
	} else {
		bulkMove = capsAllow(body.Permissions, capsBulkMoveNeeds, "move issues between projects")
		deleteIssues = capsAllow(body.Permissions, capsDeleteIssuesNeeds, "delete issues")
	}
	return func(caps *jira.Capabilities) {
		caps.BulkMove, caps.DeleteIssues = bulkMove, deleteIssues
	}, err
}

// capsAllow turns what Jira said about a set of permissions into one capability
// and the sentence explaining it.
//
// A permission the answer does not mention is not a permission denied. Jira
// sends back the keys it was asked for, so a missing one means the site did not
// answer for it — an unknown key on this site, or one the endpoint dropped —
// and reporting that as a refusal invents a denial the site never made.
func capsAllow(answers map[string]capsPermission, needs []capsRequirement, action string) jira.Capability {
	var refused, unanswered []string
	for _, need := range needs {
		answer, mentioned := answers[need.key]
		switch {
		case !mentioned:
			unanswered = append(unanswered, need.label)
		case !answer.HavePermission:
			refused = append(refused, answer.label(need.label))
		}
	}

	switch {
	case len(refused) == 0 && len(unanswered) == 0:
		return jira.Capability{OK: true}
	case len(unanswered) == 0:
		return jira.Capability{Reason: fmt.Sprintf("You need the %s %s to %s",
			capsList(refused), capsNoun(len(refused)), action)}
	case len(refused) == 0:
		return jira.Capability{Reason: fmt.Sprintf("Jira did not answer for the %s %s, which is needed to %s",
			capsList(unanswered), capsNoun(len(unanswered)), action)}
	default:
		return jira.Capability{Reason: fmt.Sprintf("You need the %s %s to %s, and Jira did not answer for the %s %s",
			capsList(refused), capsNoun(len(refused)), action,
			capsList(unanswered), capsNoun(len(unanswered)))}
	}
}

func capsNoun(n int) string {
	if n == 1 {
		return "permission"
	}
	return "permissions"
}

// capsList writes a list the way a sentence does, because this one is read by a
// person rather than parsed.
func capsList(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	default:
		return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
	}
}

// capsEnv reads one environment variable. os.Getenv is the real one; a test
// passes its own so that detection can be exercised without a terminal and
// without touching the process.
type capsEnv func(string) string

// capsDetectGraphics works out how, if at all, this terminal can show an image.
// It reads the environment rather than writing a query escape sequence and
// waiting for a reply: stdout may be a pipe, and a query nobody answers either
// blocks or leaves its own bytes on somebody's screen. Being wrong costs more
// upwards than downwards — claiming a protocol the terminal lacks prints
// garbage over the frame, missing one prints blocks — so each rule below fires
// only on a terminal known to speak it.
func capsDetectGraphics(env capsEnv) jira.GraphicsMode {
	term := strings.ToLower(strings.TrimSpace(env("TERM")))
	program := strings.ToLower(strings.TrimSpace(env("TERM_PROGRAM")))
	if term == "" || term == "dumb" {
		return jira.GraphicsNone
	}
	if !capsMultiplexed(env, term) {
		switch {
		case capsSpeaksKitty(env, term, program):
			return jira.GraphicsKitty
		case capsSpeaksITerm2(env, term, program):
			return jira.GraphicsITerm2
		}
	}
	if capsHasColor(env, term) {
		return jira.GraphicsHalfBlocks
	}
	return jira.GraphicsNone
}

// capsMultiplexed reports a terminal with tmux or screen in the middle, where
// an image protocol does not survive the trip whatever the outer terminal is.
func capsMultiplexed(env capsEnv, term string) bool {
	return env("TMUX") != "" || strings.HasPrefix(term, "screen") || strings.HasPrefix(term, "tmux")
}

func capsSpeaksKitty(env capsEnv, term, program string) bool {
	switch {
	case env("KITTY_WINDOW_ID") != "", strings.Contains(term, "kitty"):
		return true
	case program == "ghostty", env("GHOSTTY_RESOURCES_DIR") != "":
		return true
	case program == "wezterm", env("WEZTERM_PANE") != "":
		return true
	default:
		return false
	}
}

func capsSpeaksITerm2(env capsEnv, term, program string) bool {
	switch {
	case program == "iterm.app", env("ITERM_SESSION_ID") != "":
		return true
	// iTerm sets LC_TERMINAL and forwards it, which is how the protocol is
	// still there after an ssh into somewhere with a plain TERM.
	case strings.EqualFold(strings.TrimSpace(env("LC_TERMINAL")), "iTerm2"):
		return true
	case program == "mintty", strings.Contains(term, "mintty"):
		return true
	default:
		return false
	}
}

// capsHasColor reports whether half blocks are worth drawing: they are two
// coloured cells stacked in one character, so with no colour there is no
// picture — and NO_COLOR is a request not to send any.
func capsHasColor(env capsEnv, term string) bool {
	if env("NO_COLOR") != "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(env("COLORTERM"))) {
	case "truecolor", "24bit":
		return true
	}
	return strings.Contains(term, "256color") || strings.Contains(term, "direct")
}

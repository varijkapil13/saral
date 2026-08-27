package cloud

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/varijkapil13/saral/pkg/jira"
)

var _ jira.PlanReader = (*Client)(nil)

// planPath is the plan list. The doubled segment is not a typo: /rest/api/3/plans
// on its own is not an endpoint.
const planPath = "/rest/api/3/plans/plan"

const planOp = http.MethodGet + " " + planPath

// planPageSize is how many plans one page asks for. Fifty is both this
// endpoint's default and its documented maximum, and it is sent anyway: a walk
// whose page size comes from the site changes length when the site does.
const planPageSize = 50

// planBound is how many plans a walk takes before it stops, which is ten pages
// of the endpoint's own maximum. A site holds a handful; the bound is there so
// that a cursor endpoint answering something unexpected cannot become an
// unbounded read.
const planBound = 500

// Plans lists the site's Advanced Roadmaps plans.
//
// Administer Jira is required on every Plans endpoint and the per-plan View and
// Edit rights the web UI hands out do not reach the API, so a refusal is the
// ordinary answer for an ordinary token. It comes back naming CapPlans, because
// the plan view then draws the plans config defines locally with the site's own
// reason beside them, and neither a bare error nor an empty list says that
// happened.
//
// Local is false on every plan here: one this method returns was read from
// Jira, and a locally defined stand-in is the view's to build.
//
// Nothing here asks for trashed or archived plans, and the schema documents no
// default for either flag, so which of them a site lists is the site's
// business: Plan.Status is what says whether a plan is live, and it is carried
// through as it arrived.
func (c *Client) Plans(ctx context.Context) ([]jira.Plan, error) {
	page, err := cursorPages(ctx, c, func(cursor string) request {
		return request{
			method: http.MethodGet,
			path:   planPath,
			query:  planQuery(cursor),
			kind:   "the site's plans",
			id:     planPath,
		}
	}, planPage)
	if err != nil {
		return nil, planRefusal(err)
	}
	plans, err := jira.Collect(ctx, page, planBound)
	if err != nil {
		return nil, planRefusal(err)
	}
	return plans, nil
}

func planQuery(cursor string) url.Values {
	query := url.Values{"maxResults": []string{strconv.Itoa(planPageSize)}}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	return query
}

func planPage(resp *response) ([]jira.Plan, string, error) {
	var page apiPlanPage
	if err := resp.decode(planOp, &page); err != nil {
		return nil, "", err
	}
	// A page carrying nothing ends the walk whatever cursor came with it:
	// planBound counts plans, so a run of empty pages never reaches it.
	if len(page.Values) == 0 {
		return nil, "", nil
	}
	out := make([]jira.Plan, 0, len(page.Values))
	for _, plan := range page.Values {
		out = append(out, plan.domain())
	}
	return out, page.next(), nil
}

// apiPlanPage is a third paging shape: an opaque cursor, like the platform API,
// beside the end-of-results flag a cursor endpoint is not supposed to know.
//
// Atlassian's schema spells that flag last and plans_ok.json spells it isLast,
// so both are read. Which spelling arrived changes an answer only on a page that
// says it is the last and still carries a cursor.
type apiPlanPage struct {
	Values         []apiPlan `json:"values"`
	NextPageCursor string    `json:"nextPageCursor"`
	Last           *bool     `json:"last"`
	IsLast         *bool     `json:"isLast"`
}

func (p apiPlanPage) next() string {
	if (p.Last != nil && *p.Last) || (p.IsLast != nil && *p.IsLast) {
		return ""
	}
	return p.NextPageCursor
}

// apiPlan is one plan. Its id is a string in the list and a number in the
// single-plan example, which is what flexString is for; status is a closed API
// enum — Active, Trashed, Archived — and not a word an administrator renames.
type apiPlan struct {
	ID           flexString      `json:"id"`
	Name         string          `json:"name"`
	Status       string          `json:"status"`
	IssueSources []apiPlanSource `json:"issueSources"`
}

func (p apiPlan) domain() jira.Plan {
	out := jira.Plan{
		ID:      string(p.ID),
		Name:    p.Name,
		Status:  p.Status,
		Sources: make([]jira.PlanSource, 0, len(p.IssueSources)),
	}
	for _, source := range p.IssueSources {
		out.Sources = append(out.Sources, source.domain())
	}
	return out
}

// apiPlanSource is one issue source. Its value is a number in all three cases,
// and for a project it is the project id rather than the key the rest of this
// client speaks in — there is no port method that resolves one to the other, so
// a project source cannot be joined to a project here.
type apiPlanSource struct {
	Type  string     `json:"type"`
	Value flexString `json:"value"`
}

func (s apiPlanSource) domain() jira.PlanSource {
	return jira.PlanSource{Type: planSourceType(s.Type), Value: string(s.Value)}
}

// planSourceType lowercases the type Jira sends, which arrives capitalised
// where the port's own values are not. An unrecognised one keeps the site's own
// word instead of being dropped — the schema has a fourth type, Custom, and a
// source left out turns a plan into a narrower plan that no error mentions.
func planSourceType(sourceType string) jira.PlanSourceType {
	return jira.PlanSourceType(strings.ToLower(strings.TrimSpace(sourceType)))
}

// planRefusal names the capability on a 403, so that the plan view can tell the
// one refusal it has a fallback for from every other way this call fails.
func planRefusal(err error) error {
	var refused *jira.CapabilityError
	if !errors.As(err, &refused) || refused.Capability != "" {
		return err
	}
	return &jira.CapabilityError{Capability: jira.CapPlans, Reason: refused.Reason}
}

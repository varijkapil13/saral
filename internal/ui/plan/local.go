package plan

import (
	"strings"

	"github.com/varijkapil13/saral/internal/app"
	"github.com/varijkapil13/saral/pkg/jira"
)

// Defined is a plan this profile defines itself, which is the shape a plan
// usually has here: every Plans endpoint needs Administer Jira, so the site
// answers for one only to an administrator's token.
//
// The tags are the shape proposed for the profile, beside the timeline mapping
// it already carries:
//
//	[[profiles.work.plans]]
//	name     = "Q3 delivery"
//	projects = ["ENG", "OPS"]
//	filters  = ["10023"]
//	jql      = "labels = roadmap"
//	start    = ["Target start", "Start date"]
//	end      = ["Target end", "duedate"]
//
// Start and End are field names, most preferred first, resolved against the
// site's catalogue by whatever draws the dates — the same rule
// [profiles.x.timeline] follows, so a plan may leave them out and take the
// profile's.
type Defined struct {
	Name     string   `toml:"name"`
	Projects []string `toml:"projects"`
	Filters  []string `toml:"filters"`
	JQL      string   `toml:"jql"`
	Start    []string `toml:"start"`
	End      []string `toml:"end"`
}

// sources are the plan's issue sources, in the same shape the site answers
// with. A project is named by its key here, where the site names it by an id
// nothing can turn back into a key, and Plan.Local is what says which of the
// two a row is holding.
func (d Defined) sources() []jira.PlanSource {
	out := make([]jira.PlanSource, 0, len(d.Projects)+len(d.Filters))
	for _, key := range trimmed(d.Projects) {
		out = append(out, jira.PlanSource{Type: jira.PlanSourceProject, Value: key})
	}
	for _, id := range trimmed(d.Filters) {
		out = append(out, jira.PlanSource{Type: jira.PlanSourceFilter, Value: id})
	}
	return out
}

// clause is the JQL this plan renders to, and the problem that stops it being
// one. Sources are joined with OR — a plan is the union of what it draws from —
// and the extra narrowing is ANDed over the lot.
func (d Defined) clause() (jql, problem string) {
	var parts []string
	if keys := trimmed(d.Projects); len(keys) > 0 {
		parts = append(parts, in("project", keys))
	}
	ids := trimmed(d.Filters)
	for _, id := range ids {
		if !digits(id) {
			return "", "a filter is named by its numeric id, and " + quote(id) + " is not one"
		}
	}
	if len(ids) > 0 {
		// Filter ids go in unquoted: a quoted number is a filter *name* to Jira,
		// and a site where one filter is called "10023" would answer for the
		// wrong search rather than refuse.
		parts = append(parts, "filter IN ("+strings.Join(ids, ", ")+")")
	}
	extra := strings.TrimSpace(d.JQL)
	switch {
	case len(parts) == 0 && extra == "":
		return "", "this plan names no project, no filter and no JQL, so there is nothing to draw"
	case len(parts) == 0:
		return extra, ""
	}
	jql = strings.Join(parts, " OR ")
	if len(parts) > 1 {
		jql = "(" + jql + ")"
	}
	if extra != "" {
		jql += " AND (" + extra + ")"
	}
	return jql, ""
}

// dates says where this plan's bars would take their start and end from, and
// the empty string when it leaves that to the profile's own mapping.
func (d Defined) dates() string {
	start, end := trimmed(d.Start), trimmed(d.End)
	if len(start) == 0 && len(end) == 0 {
		return ""
	}
	return field(start) + " " + arrow + " " + field(end)
}

func field(names []string) string {
	if len(names) == 0 {
		return "the profile's mapping"
	}
	return strings.Join(names, " or ")
}

// arrow is written out rather than taken from the theme because it is part of a
// sentence the plan rows carry, and the glyph set is not known here.
const arrow = "->"

func in(field string, values []string) string {
	if len(values) == 1 {
		return field + " = " + quote(values[0])
	}
	quoted := make([]string, 0, len(values))
	for _, v := range values {
		quoted = append(quoted, quote(v))
	}
	return field + " IN (" + strings.Join(quoted, ", ") + ")"
}

// quote spells a value as a JQL string literal. A project key is upper-case and
// dull, but this also carries whatever anybody typed into the file.
func quote(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for _, r := range s {
		if r == '"' || r == '\\' {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('"')
	return b.String()
}

func digits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func trimmed(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// derive is what stands in for the profile's plans until it has any: the
// project this session is scoped to, and one plan per saved query. An empty
// first screen reads as a broken program, and both of these are already in
// Deps.
func derive(project string, saved app.SavedQueries) []Defined {
	var out []Defined
	if key := strings.TrimSpace(project); key != "" {
		out = append(out, Defined{Name: key, Projects: []string{key}})
	}
	for _, q := range saved.All() {
		if strings.TrimSpace(q.JQL) == "" {
			continue
		}
		out = append(out, Defined{Name: q.Name, JQL: q.JQL})
	}
	return out
}

// origin says where a plan on screen came from, which is the difference a user
// cannot act on unless the screen names it.
func originOf(d Defined, derived bool) string {
	switch {
	case !derived:
		return "defined in this profile"
	case len(d.Projects) > 0:
		return "this session's project"
	default:
		return "a saved query"
	}
}

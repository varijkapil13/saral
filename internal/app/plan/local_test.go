package plan

import (
	"errors"
	"slices"
	"testing"
)

func TestDefined_Clause(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		in   Defined
		jql  string
		want error
	}{
		"one project":                  {in: Defined{Projects: []string{"ENG"}}, jql: `project = "ENG"`},
		"two projects and a filter":    {in: Defined{Projects: []string{"ENG", "OPS"}, Filters: []string{"10023"}}, jql: `(project IN ("ENG", "OPS") OR filter IN (10023))`},
		"a narrowing over the sources": {in: Defined{Projects: []string{"ENG"}, JQL: "labels = roadmap"}, jql: `project = "ENG" AND (labels = roadmap)`},
		"JQL and nothing else":         {in: Defined{JQL: "resolution IS EMPTY"}, jql: "resolution IS EMPTY"},
		"a quote in a project key":     {in: Defined{Projects: []string{`we"ird`}}, jql: `project = "we\"ird"`},
		"nothing at all":               {in: Defined{Name: "empty"}, want: ErrNothingToDraw},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			jql, err := tc.in.Clause()
			if jql != tc.jql || !errors.Is(err, tc.want) {
				t.Errorf("Clause = %q, %v; want %q, %v", jql, err, tc.jql, tc.want)
			}
		})
	}
}

func TestDefined_AFilterNamedRatherThanNumbered(t *testing.T) {
	t.Parallel()

	_, err := Defined{Filters: []string{"my filter"}}.Clause()
	var bad *FilterIDError
	if !errors.As(err, &bad) || bad.Filter != "my filter" {
		t.Errorf("err = %v, want the filter named", err)
	}
}

func TestDefined_Plan(t *testing.T) {
	t.Parallel()

	p := Defined{Projects: []string{" ENG ", ""}, Filters: []string{"10023"}}.Plan(3)
	if p.ID != "local:3:unnamed plan" || p.Name != "unnamed plan" || !p.Local {
		t.Errorf("Plan = %+v", p)
	}
	if len(p.Sources) != 2 || p.Sources[0].Value != "ENG" || p.Sources[1].Value != "10023" {
		t.Errorf("Sources = %+v", p.Sources)
	}
}

func TestDefined_Fields(t *testing.T) {
	t.Parallel()

	start, end := Defined{Start: []string{" Target start ", ""}}.Fields()
	if !slices.Equal(start, []string{"Target start"}) || len(end) != 0 {
		t.Errorf("Fields = %v, %v", start, end)
	}
}

func TestDerive(t *testing.T) {
	t.Parallel()

	got := Derive(" PROJ ", []Query{{Name: "mine", JQL: "assignee = currentUser()"}, {Name: "blank", JQL: " "}})
	if len(got) != 2 || got[0].Name != "PROJ" || !slices.Equal(got[0].Projects, []string{"PROJ"}) || got[1].Name != "mine" {
		t.Errorf("Derive = %+v", got)
	}
	if got := Derive("", nil); len(got) != 0 {
		t.Errorf("Derive with nothing = %+v", got)
	}
}

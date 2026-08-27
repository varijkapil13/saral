package cloud

import (
	"testing"

	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

// One set of assertions about a create screen, run against both adapters through
// the port role that names the method.
//
// The two sites state different defaults and always will — the fixture site's
// priority default is one of its own priorities and the fake's is one of its own
// — so the cases are properties: a stated default arrives with the value it was
// stated as, a field with no default carries no value, and a screen that states a
// default it cannot name says exactly that.
//
// The last of those has one adapter. jiratest.Fake's create screen does not offer
// the reporter at all, and the reporter is the field a site sends
// hasDefaultValue true beside a null for, so the case runs against the fixture
// site alone until the fake's screen grows the field.

type metaBuilder func(*testing.T) jira.SchemaReader

// The screens each adapter is asked for. The fixture site answers
// createmeta_bug.json for its Bug and the fake holds its own issue types, so the
// ids are per adapter and neither is written down anywhere else.
const (
	conformCloudIssueType = metaBugID
	conformFakeIssueType  = "10301"
)

func metaFromSite(t *testing.T, opts ...jiratest.ServerOption) jira.SchemaReader {
	t.Helper()

	s := jiratest.NewServer(opts...)
	t.Cleanup(s.Close)
	c, _ := testClient(t, s.URL())
	return c
}

func TestCreateMeta_BothAdaptersAnswerTheSameWayAboutADefault(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		assert func(*testing.T, jira.Schema)
	}{
		{
			name: "a default the screen states arrives as the value it stated",
			assert: func(t *testing.T, got jira.Schema) {
				t.Helper()

				stated := statedDefaults(got)
				if len(stated) == 0 {
					t.Fatal("no field on this screen states a default, and both screens are supposed to state one for the priority")
				}
				named := make([]string, 0, len(stated))
				for _, meta := range stated {
					if meta.Default.Kind == jira.KindEmpty {
						continue
					}
					named = append(named, meta.Field.ID)
					if meta.Default.Kind != jira.KindOption || len(meta.Default.Options) != 1 {
						t.Errorf("%s states a default of kind %d holding %d options, want one option",
							meta.Field.ID, meta.Default.Kind, len(meta.Default.Options))
						continue
					}
					only := meta.Default.Options[0]
					if only.ID == "" || only.Label == "" {
						t.Errorf("%s states a default of %+v; an id identifies it and a label is what a form can show",
							meta.Field.ID, only)
					}
				}
				// Without this the case passes on a decoder that discards
				// defaultValue outright: every stated default then reads as a
				// screen that named nothing, which is the reporter's own answer.
				if len(named) == 0 {
					t.Errorf("%d field(s) state a default and not one of them says what it is: %v. Both "+
						"screens state the priority's as an option, so this is what a decoder that drops "+
						"defaultValue looks like", len(stated), fieldMetaIDs(stated))
				}
			},
		},
		{
			name: "a field the screen states no default for carries no value",
			assert: func(t *testing.T, got jira.Schema) {
				t.Helper()

				for i := range got.Fields {
					meta := got.Fields[i]
					if meta.HasDefault || meta.Default.Kind == jira.KindEmpty {
						continue
					}
					t.Errorf("%s says it has no default and carries %+v anyway, so a form cannot tell "+
						"a value the site will use from one it will not", meta.Field.ID, meta.Default)
				}
			},
		},
	}

	for _, tt := range cases {
		for _, adapter := range []struct {
			name      string
			open      metaBuilder
			issueType string
		}{
			{name: "cloud", open: func(t *testing.T) jira.SchemaReader { return metaFromSite(t) }, issueType: conformCloudIssueType},
			{name: "fake", open: func(t *testing.T) jira.SchemaReader { return conformFake(t) }, issueType: conformFakeIssueType},
		} {
			t.Run(tt.name+"/"+adapter.name, func(t *testing.T) {
				t.Parallel()

				got, err := adapter.open(t).CreateMeta(t.Context(), conformProject, adapter.issueType)
				if err != nil {
					t.Fatalf("reading the create screen: %v", err)
				}
				if len(got.Fields) == 0 {
					t.Fatal("the create screen came back with no fields at all")
				}
				tt.assert(t, got)
			})
		}
	}
}

// A screen says the reporter has a default and sends a null for it, because the
// value comes from the credential and the screen has no way to know it. Read as
// "no default" a form says nothing and the field looks required-but-blank; read
// as a value it would be the zero one.
func TestCreateMeta_ADefaultTheScreenWillNotNameIsStillADefault(t *testing.T) {
	t.Parallel()

	got, err := metaFromSite(t).CreateMeta(t.Context(), conformProject, conformCloudIssueType)
	if err != nil {
		t.Fatalf("reading the create screen: %v", err)
	}
	meta, ok := metaFor(got, "reporter")
	if !ok {
		t.Fatalf("this screen has no reporter field, so it cannot hold the case: it has %v", fieldIDs(got))
	}
	if !meta.HasDefault {
		t.Error("the reporter reads as having no default, and the screen said it has one")
	}
	if meta.Default.Kind != jira.KindEmpty {
		t.Errorf("the reporter's default reads as %+v; the screen named nobody, so there is nothing to show", meta.Default)
	}
}

func statedDefaults(s jira.Schema) []jira.FieldMeta {
	out := make([]jira.FieldMeta, 0, len(s.Fields))
	for i := range s.Fields {
		if s.Fields[i].HasDefault {
			out = append(out, s.Fields[i])
		}
	}
	return out
}

func fieldMetaIDs(in []jira.FieldMeta) []string {
	out := make([]string, 0, len(in))
	for i := range in {
		out = append(out, in[i].Field.ID)
	}
	return out
}

func metaFor(s jira.Schema, id string) (jira.FieldMeta, bool) {
	for i := range s.Fields {
		if s.Fields[i].Field.ID == id {
			return s.Fields[i], true
		}
	}
	return jira.FieldMeta{}, false
}

func fieldIDs(s jira.Schema) []string {
	out := make([]string, 0, len(s.Fields))
	for i := range s.Fields {
		out = append(out, s.Fields[i].Field.ID)
	}
	return out
}

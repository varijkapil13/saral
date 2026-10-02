package jiratest_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/varijkapil13/saral/pkg/adf"
	"github.com/varijkapil13/saral/pkg/jira"
	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func fakeDocOf(text string) adf.Doc {
	return adf.NewDoc(adf.NewNode("paragraph", adf.NewText(text)))
}

func fakeTextIssue(key, summary, description string) jira.Issue {
	return jira.Issue{
		ID: key[len("TXT-"):], Key: key,
		Project:     jira.ProjectRef{Key: "TXT"},
		Summary:     summary,
		Description: fakeDocOf(description),
		Status:      jira.Status{ID: "10201", Name: "Doing", Category: jira.CategoryInProgress},
	}
}

func fakeTextSite(t *testing.T) *jiratest.Fake {
	t.Helper()
	c := jiratest.New(
		jiratest.WithProject("TXT", jiratest.Scrum),
		jiratest.WithIssues([]jira.Issue{
			fakeTextIssue("TXT-1", "Login times out", "The session ends after a minute"),
			fakeTextIssue("TXT-2", "Report export", "A null pointer appears when the Login page loads"),
			fakeTextIssue("TXT-3", "Overtime rules", "Nothing about sign in"),
			fakeTextIssue("TXT-4", "Pointer null", "Words in the other order"),
			fakeTextIssue("TXT-5", "Unrelated", "Nothing to see"),
		}),
	)
	if _, err := c.AddComment(t.Context(), "TXT-5", fakeDocOf("Seen again after the Crash on Friday")); err != nil {
		t.Fatal(err)
	}
	return c
}

func fakeSearchKeys(t *testing.T, c *jiratest.Fake, jql string) []string {
	t.Helper()
	page, err := c.Search(t.Context(), jira.Query{JQL: jql, Fields: fakeNarrow})
	if err != nil {
		t.Fatalf("Search(%s): %v", jql, err)
	}
	var keys []string
	for _, iss := range page.Items {
		keys = append(keys, iss.Key)
	}
	slices.Sort(keys)
	return keys
}

func fakeWantKeys(t *testing.T, c *jiratest.Fake, jql string, want ...string) {
	t.Helper()
	if got := fakeSearchKeys(t, c, jql); !slices.Equal(got, want) {
		t.Errorf("%s\n got %v\nwant %v", jql, got, want)
	}
}

func TestSearch_TextMatchesEveryTermAcrossSummaryDescriptionAndComments(t *testing.T) {
	t.Parallel()
	c := fakeTextSite(t)
	fakeWantKeys(t, c, jira.ParseText("login").Clause(jira.TextAll), "TXT-1", "TXT-2")
	fakeWantKeys(t, c, jira.ParseText("login pointer").Clause(jira.TextAll), "TXT-2")
	fakeWantKeys(t, c, jira.ParseText("crash friday").Clause(jira.TextAll), "TXT-5")
	fakeWantKeys(t, c, jira.ParseText("login crash").Clause(jira.TextAll))
}

func TestSearch_TextIsCaseInsensitive(t *testing.T) {
	t.Parallel()
	c := fakeTextSite(t)
	fakeWantKeys(t, c, `text ~ "LOGIN"`, "TXT-1", "TXT-2")
	fakeWantKeys(t, c, `text ~ "crash"`, "TXT-5")
}

func TestSearch_TextPrefixMatchesWordStartsOnly(t *testing.T) {
	t.Parallel()
	c := fakeTextSite(t)
	fakeWantKeys(t, c, `text ~ "time*"`, "TXT-1")
	fakeWantKeys(t, c, `text ~ "time"`)
	fakeWantKeys(t, c, `text ~ "ime*"`)
}

func TestSearch_TextPhraseNeedsTheWordsTogetherInOrder(t *testing.T) {
	t.Parallel()
	c := fakeTextSite(t)
	fakeWantKeys(t, c, `text ~ "\"null pointer\""`, "TXT-2")
	fakeWantKeys(t, c, `text ~ "\"pointer null\""`, "TXT-4")
	fakeWantKeys(t, c, `text ~ "null pointer"`, "TXT-2", "TXT-4")
}

func TestSearch_SummaryTildeLooksOnlyAtTheSummary(t *testing.T) {
	t.Parallel()
	c := fakeTextSite(t)
	fakeWantKeys(t, c, `summary ~ "login"`, "TXT-1")
	fakeWantKeys(t, c, `description ~ "login"`, "TXT-2")
	fakeWantKeys(t, c, `comment ~ "crash"`, "TXT-5")
	fakeWantKeys(t, c, `comment ~ "login"`)
	fakeWantKeys(t, c, `summary ~ "crash"`)
}

func TestSearch_TextComposesWithProjectAndOrderBy(t *testing.T) {
	t.Parallel()
	c := fakeTextSite(t)
	page, err := c.Search(t.Context(), jira.Query{
		JQL:    `project = "TXT" AND text ~ "login" ORDER BY key DESC`,
		Fields: fakeNarrow,
	})
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, iss := range page.Items {
		keys = append(keys, iss.Key)
	}
	if want := []string{"TXT-2", "TXT-1"}; !slices.Equal(keys, want) {
		t.Errorf("got %v, want %v", keys, want)
	}
	fakeWantKeys(t, c, `project = "OTHER" AND text ~ "login"`)
}

func TestSearch_TextReadsEscapedQuotesInsideTheJQLString(t *testing.T) {
	t.Parallel()
	c := fakeTextSite(t)
	fakeWantKeys(t, c, `text ~ "\"null pointer\" and order by" ORDER BY key`)
	fakeWantKeys(t, c, `text ~ "\"login\" \"page\""`, "TXT-2")
	fakeWantKeys(t, c, `text ~ "login\tpage"`, "TXT-2")
}

func TestSearch_EmptyTextMatchesNothing(t *testing.T) {
	t.Parallel()
	c := fakeTextSite(t)
	fakeWantKeys(t, c, `text ~ ""`)
	fakeWantKeys(t, c, `text ~ "   "`)
}

func TestSearch_TextRefusesLuceneOperatorsItDoesNotModel(t *testing.T) {
	t.Parallel()
	c := fakeTextSite(t)
	for _, v := range []string{`a OR b`, `a AND b`, `NOT a`, `-a`, `+a`, `a^2`, `a~`, `(a)`, `a:b`, `a*b`, `a?`, `"a`, `a/b`} {
		t.Run(v, func(t *testing.T) {
			t.Parallel()
			_, err := c.Search(t.Context(), jira.Query{JQL: `text ~ ` + jira.QuoteJQL(v), Fields: fakeNarrow})
			var ve *jira.ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("want a validation error for %q, got %v", v, err)
			}
			if _, ok := ve.For("jql"); !ok {
				t.Errorf("the validation error must name jql, got %v", ve)
			}
		})
	}
}

func FuzzFake_AcceptsEveryClauseParseTextEmits(f *testing.F) {
	for _, seed := range []string{`login timeout`, `"null pointer" crash`, `a\"b`, "x\ny", `C:\path\file.txt`, `foo AND bar`, `*?*`, `"""`} {
		f.Add(seed)
	}
	c := jiratest.New(jiratest.WithProject("TXT", jiratest.Scrum), jiratest.WithIssues([]jira.Issue{fakeTextIssue("TXT-1", "Login", "text")}))
	f.Fuzz(func(t *testing.T, in string) {
		q := jira.ParseText(in)
		if q.Empty() {
			return
		}
		jql := `project = ` + jira.QuoteJQL("TXT") + ` AND ` + q.Clause(jira.TextAll) + ` ORDER BY updated DESC`
		if _, err := c.Search(t.Context(), jira.Query{JQL: jql, Fields: fakeNarrow}); err != nil {
			t.Fatalf("Search(%s): %v", jql, err)
		}
	})
}

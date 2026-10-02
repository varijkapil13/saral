package cloud

import (
	"net/http"
	"testing"

	"github.com/varijkapil13/saral/pkg/jira/jiratest"
)

func TestProject_DecodesANumericID(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer(jiratest.WithHandler(http.MethodGet, projectPath+"/{key}",
		jsonHandler(http.StatusOK, `{"id":10042,"key":"NUM","name":"Numeric"}`)))
	defer s.Close()

	c, _ := testClient(t, s.URL())
	got, err := c.Project(t.Context(), "10042")
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if got.ID != "10042" || got.Key != "NUM" || got.Name != "Numeric" {
		t.Errorf("got %+v, want 10042 NUM Numeric", got)
	}
}

func TestProject_ReadsTheFixture(t *testing.T) {
	t.Parallel()

	s := jiratest.NewServer()
	defer s.Close()

	c, _ := testClient(t, s.URL())
	got, err := c.Project(t.Context(), "EX")
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if got.ID != "10000" || got.Key != "EX" || got.Name != "Example" {
		t.Errorf("got %+v, want 10000 EX Example", got)
	}
}

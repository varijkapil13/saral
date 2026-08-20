package cloud

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestClient_RevealsTheTokenUnderNoFormatVerb(t *testing.T) {
	t.Parallel()

	c, _ := testClient(t, "example.atlassian.net")
	encoded := base64.StdEncoding.EncodeToString([]byte(testEmail + ":" + testToken))

	subjects := map[string]any{
		"the client":       c,
		"a client value":   *c,
		"the credentials":  c.creds,
		"the secret":       c.creds.token,
		"the retry policy": c.retry,
	}
	for _, verb := range []string{"%v", "%+v", "%s", "%q", "%#v"} {
		for name, subject := range subjects {
			got := fmt.Sprintf(verb, subject)
			if strings.Contains(got, testToken) {
				t.Errorf("%s of %s printed the API token: %s", verb, name, got)
			}
			if strings.Contains(got, encoded) {
				t.Errorf("%s of %s printed the encoded credential, which is the token in one step: %s", verb, name, got)
			}
		}
	}

	if got := c.String(); !strings.Contains(got, "example.atlassian.net") || !strings.Contains(got, testEmail) {
		t.Errorf("%%v = %s, want it to name the site and the account it is for", got)
	}
	if got := c.creds.token.value(); got != testToken {
		t.Errorf("the token came back as %q through its own accessor, want the one it was built with", got)
	}
}

func TestSecret_IsEmptyRatherThanRedactedWhenThereIsNothingToHide(t *testing.T) {
	t.Parallel()

	var unset secret
	if unset.String() != "" || unset.value() != "" {
		t.Errorf("an unset secret prints as %q and reveals %q, want both empty", unset.String(), unset.value())
	}
	if got := newSecret("something").String(); got != redacted {
		t.Errorf("a set secret prints as %q, want %q", got, redacted)
	}
}

func TestAuthorize_CannotBeReplacedByAHeaderTheCallerSet(t *testing.T) {
	t.Parallel()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://example.atlassian.net/rest/api/3/field", http.NoBody)
	if err != nil {
		t.Fatalf("building a request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer somebody-elses-idea")

	creds := credentials{email: testEmail, token: newSecret(testToken)}
	creds.authorize(req)

	user, pass, ok := req.BasicAuth()
	if !ok {
		t.Fatal("the request carries no basic auth")
	}
	if user != testEmail || pass != testToken {
		t.Errorf("basic auth is %q / %q, want the account email and its API token", user, pass)
	}
	if got := req.Header.Values("Authorization"); len(got) != 1 {
		t.Errorf("the request carries %d Authorization headers, want one", len(got))
	}
}

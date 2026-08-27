package app

import "testing"

func TestParseKey_ReadsTheShapeOfAKeyAndNothingElse(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "a plain key", in: "PROJ-142", want: "PROJ-142"},
		{name: "typed in lower case", in: "proj-142", want: "PROJ-142"},
		{name: "typed in mixed case", in: "Proj-142", want: "PROJ-142"},
		{name: "padded", in: "  PROJ-142\n", want: "PROJ-142"},
		{name: "a digit inside the project key", in: "PROJ2-1", want: "PROJ2-1"},
		{name: "an underscore inside the project key", in: "MY_PROJ-1", want: "MY_PROJ-1"},
		{name: "a single-letter project key", in: "P-1", want: "P-1"},
		{name: "a view name", in: "board"},
		{name: "a view name with a digit", in: "board2"},
		{name: "no number", in: "PROJ-"},
		{name: "no project key", in: "-142"},
		{name: "a project key starting with a digit", in: "2PROJ-1"},
		{name: "a leading zero, which Jira never issues", in: "PROJ-042"},
		{name: "issue zero, which Jira never issues", in: "PROJ-0"},
		{name: "two hyphens", in: "PROJ-1-2"},
		{name: "a space inside", in: "PROJ 142"},
		{name: "a hyphen in the project key, which Jira does not allow in one", in: "MY-PROJ-1"},
		{name: "empty", in: ""},
		{name: "a browse URL is not a bare key", in: "https://example.atlassian.net/browse/PROJ-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, ok := ParseKey(tc.in)
			if ok != (tc.want != "") {
				t.Fatalf("ParseKey(%q) = %q, %v; want ok=%v", tc.in, got, ok, tc.want != "")
			}
			if got != tc.want {
				t.Errorf("ParseKey(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseIssueURL_ReadsTheKeyOutOfWhatTheWebAppProduces(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		key  string
		host string
	}{
		{
			name: "a browse link",
			in:   "https://example.atlassian.net/browse/PROJ-142",
			key:  "PROJ-142", host: "example.atlassian.net",
		},
		{
			name: "a browse link with a filter on it",
			in:   "https://example.atlassian.net/browse/PROJ-142?filter=10001",
			key:  "PROJ-142", host: "example.atlassian.net",
		},
		{
			name: "a board with an issue selected",
			in:   "https://example.atlassian.net/jira/software/projects/PROJ/boards/1?selectedIssue=PROJ-142",
			key:  "PROJ-142", host: "example.atlassian.net",
		},
		{
			name: "a backlog with an issue selected",
			in:   "https://example.atlassian.net/jira/software/c/projects/PROJ/boards/1/backlog?selectedIssue=OTHER-7",
			key:  "OTHER-7", host: "example.atlassian.net",
		},
		{
			name: "an issue under a project path",
			in:   "https://example.atlassian.net/jira/software/projects/PROJ/issues/PROJ-9",
			key:  "PROJ-9", host: "example.atlassian.net",
		},
		{
			name: "no scheme, as copied out of an address bar",
			in:   "example.atlassian.net/browse/PROJ-1",
			key:  "PROJ-1", host: "example.atlassian.net",
		},
		{
			name: "a host in capitals",
			in:   "https://EXAMPLE.atlassian.net/browse/proj-1",
			key:  "PROJ-1", host: "example.atlassian.net",
		},
		{
			name: "a site with a port and a context path",
			in:   "https://jira.example.com:8443/context/browse/PROJ-1",
			key:  "PROJ-1", host: "jira.example.com:8443",
		},
		{
			name: "a numeric selectedIssue falls back to the path",
			in:   "https://example.atlassian.net/browse/PROJ-3?selectedIssue=10042",
			key:  "PROJ-3", host: "example.atlassian.net",
		},
		{name: "a board with nothing selected", in: "https://example.atlassian.net/jira/software/projects/PROJ/boards/1"},
		{name: "the site itself", in: "https://example.atlassian.net/"},
		{name: "a bare key is not a URL", in: "PROJ-142"},
		{name: "a view name", in: "board"},
		{name: "empty", in: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			key, host, ok := ParseIssueURL(tc.in)
			if ok != (tc.key != "") {
				t.Fatalf("ParseIssueURL(%q) = %q, %q, %v; want ok=%v", tc.in, key, host, ok, tc.key != "")
			}
			if key != tc.key || (ok && host != tc.host) {
				t.Errorf("ParseIssueURL(%q) = %q on %q, want %q on %q", tc.in, key, host, tc.key, tc.host)
			}
		})
	}
}

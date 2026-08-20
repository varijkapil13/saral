package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

// The mock is installed for the whole binary so that no test can ever reach the
// real keychain, which on darwin would shell out to /usr/bin/security.
func TestMain(m *testing.M) {
	keyring.MockInit()
	os.Exit(m.Run())
}

const testSecret = "9d8f7a6b5c4d3e2f1a0b"

func envProfile(variable string) Profile {
	return Profile{
		Name:  "work",
		Site:  "example.atlassian.net",
		Email: "you@example.com",
		Token: TokenSource{Env: variable},
	}
}

func TestResolveToken_ReadsTheNamedEnvironmentVariable(t *testing.T) {
	t.Setenv("SARAL_TEST_TOKEN", testSecret)
	t.Setenv("SARAL_TEST_BLANK", "   ")

	tests := []struct {
		name        string
		variable    string
		want        string
		wantErrLike string
	}{
		{name: "a variable that is set", variable: "SARAL_TEST_TOKEN", want: testSecret},
		{name: "a variable that is blank", variable: "SARAL_TEST_BLANK", wantErrLike: "SARAL_TEST_BLANK is empty or not set"},
		{name: "a variable that is not set", variable: "SARAL_TEST_MISSING", wantErrLike: "SARAL_TEST_MISSING is empty or not set"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := envProfile(tt.variable).ResolveToken(t.Context())
			if tt.wantErrLike != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrLike) {
					t.Fatalf("got (%q, %v), want an error mentioning %q", got, err, tt.wantErrLike)
				}
				if !strings.Contains(err.Error(), `profile "work"`) {
					t.Errorf("error %q does not name the profile", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveToken: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// The keyring mock is a package-level global in go-keyring, so these subtests
// must not run in parallel with each other.
func TestResolveToken_ReadsTheKeychainEntryTheProfileNames(t *testing.T) {
	keyring.MockInit()
	t.Cleanup(keyring.MockInit)

	if err := keyring.Set("saral", "work", testSecret); err != nil {
		t.Fatalf("seeding the keychain: %v", err)
	}
	if err := keyring.Set("saral-by-email", "you@example.com", testSecret); err != nil {
		t.Fatalf("seeding the keychain: %v", err)
	}

	profile := func(entry string) Profile {
		return Profile{Name: "work", Site: "example.atlassian.net", Email: "you@example.com", Token: TokenSource{Keychain: entry}}
	}

	tests := []struct {
		name        string
		profile     Profile
		want        string
		wantErrLike []string
	}{
		{
			name:    "service:account names both halves",
			profile: profile("saral:work"),
			want:    testSecret,
		},
		{
			name:    "a bare service falls back to the profile email as the account",
			profile: profile("saral-by-email"),
			want:    testSecret,
		},
		{
			name:        "a missing entry says which entry and how to create it",
			profile:     profile("saral:absent"),
			wantErrLike: []string{"saral/absent", "does not exist", "create it with:"},
		},
		{
			name: "a bare service with no email to fall back to",
			profile: Profile{
				Name:  "work",
				Site:  "example.atlassian.net",
				Token: TokenSource{Keychain: "saral"},
			},
			wantErrLike: []string{"names no account"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.profile.ResolveToken(t.Context())
			if len(tt.wantErrLike) > 0 {
				if err == nil {
					t.Fatalf("got %q, want an error", got)
				}
				for _, want := range tt.wantErrLike {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error %q does not mention %q", err, want)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveToken: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want the seeded secret", got)
			}
		})
	}

	t.Run("a keychain that refuses to answer is reported, not swallowed", func(t *testing.T) {
		keyring.MockInitWithError(errors.New("the keychain is locked"))
		t.Cleanup(keyring.MockInit)

		_, err := profile("saral:work").ResolveToken(t.Context())
		if err == nil || !strings.Contains(err.Error(), "the keychain is locked") {
			t.Fatalf("got %v, want the provider error", err)
		}
	})
}

func TestStoreToken_WritesToTheKeychainAndNowhereElse(t *testing.T) {
	keyring.MockInit()
	t.Cleanup(keyring.MockInit)

	p := Profile{
		Name:  "work",
		Site:  "example.atlassian.net",
		Email: "you@example.com",
		Token: TokenSource{Keychain: "saral-store:work"},
	}
	if err := p.StoreToken(testSecret); err != nil {
		t.Fatalf("StoreToken: %v", err)
	}
	got, err := p.ResolveToken(t.Context())
	if err != nil {
		t.Fatalf("ResolveToken: %v", err)
	}
	if got != testSecret {
		t.Errorf("got %q, want what StoreToken wrote", got)
	}

	if err := p.StoreToken("  "); err == nil {
		t.Error("StoreToken accepted an empty token")
	}
	if err := envProfile("SARAL_TEST_TOKEN").StoreToken(testSecret); err == nil {
		t.Error("StoreToken wrote to an environment variable source")
	}
}

func TestResolveToken_RunsTheCommandWithoutAShell(t *testing.T) {
	t.Parallel()

	printf := lookPath(t, "printf")

	tests := []struct {
		name        string
		argv        []string
		timeout     time.Duration
		cancel      bool
		want        string
		wantErrLike []string
	}{
		{
			name: "the output is the token, with the trailing newline trimmed",
			argv: []string{printf, testSecret + "\n"},
			want: testSecret,
		},
		{
			name:        "a command that prints nothing",
			argv:        []string{printf, ""},
			wantErrLike: []string{"printed nothing"},
		},
		{
			name:        "a command that prints only whitespace",
			argv:        []string{printf, "  \n"},
			wantErrLike: []string{"printed nothing"},
		},
		{
			name:        "a command that is not on PATH",
			argv:        []string{"saral-no-such-token-command"},
			wantErrLike: []string{"is not on PATH", "saral-no-such-token-command"},
		},
		{
			name:        "a command that exits non-zero",
			argv:        []string{lookPath(t, "false")},
			wantErrLike: []string{"failed", "exit status 1"},
		},
		{
			name:        "a command that runs longer than its timeout",
			argv:        []string{lookPath(t, "sleep"), "30"},
			timeout:     50 * time.Millisecond,
			wantErrLike: []string{"did not finish within 50ms"},
		},
		{
			name:        "a caller that cancelled before the command was run",
			argv:        []string{printf, testSecret},
			cancel:      true,
			wantErrLike: []string{"context canceled"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			if tt.cancel {
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}
			r := commandResolver{profile: "work", argv: tt.argv, timeout: tt.timeout}
			got, err := r.Resolve(ctx)

			if len(tt.wantErrLike) > 0 {
				if err == nil {
					t.Fatalf("got %q, want an error", got)
				}
				if !strings.Contains(err.Error(), `profile "work"`) {
					t.Errorf("error %q does not name the profile", err)
				}
				for _, want := range tt.wantErrLike {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error %q does not mention %q", err, want)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveToken_ReportsStderrButNeverStdoutWhenTheCommandFails(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, testSecret), nil, 0o600); err != nil {
		t.Fatalf("seeding the directory: %v", err)
	}
	missing := filepath.Join(dir, "not-here")

	r := commandResolver{profile: "work", argv: []string{lookPath(t, "ls"), dir, missing}}
	got, err := r.Resolve(t.Context())
	if err == nil {
		t.Fatalf("got %q, want an error", got)
	}
	if !strings.Contains(err.Error(), "not-here") {
		t.Errorf("error %q does not carry the command's stderr", err)
	}
	if strings.Contains(err.Error(), testSecret) {
		t.Errorf("the command's stdout leaked into the error: %v", err)
	}
}

func TestSplitCommand_RefusesAnythingAShellWouldHaveInterpreted(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		in          string
		want        []string
		wantErrLike string
	}{
		{name: "a plain command", in: "pass jira", want: []string{"pass", "jira"}},
		{name: "runs of whitespace collapse", in: "  pass\tshow   jira/work ", want: []string{"pass", "show", "jira/work"}},
		{name: "a pipe", in: "pass jira | head -1", wantErrLike: `"|"`},
		{name: "a semicolon", in: "pass jira; rm -rf /", wantErrLike: `";"`},
		{name: "command substitution", in: "echo $(cat /etc/passwd)", wantErrLike: `"$"`},
		{name: "backticks", in: "echo `id`", wantErrLike: "`"},
		{name: "a redirect", in: "pass jira > /tmp/token", wantErrLike: `">"`},
		{name: "an ampersand", in: "pass jira &", wantErrLike: `"&"`},
		{name: "a glob", in: "cat /secrets/*.txt", wantErrLike: `"*"`},
		{name: "quoting", in: `pass "jira work"`, wantErrLike: `"\""`},
		{name: "a tilde", in: "~/bin/token", wantErrLike: `"~"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := splitCommand(tt.in)
			if tt.wantErrLike != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrLike) {
					t.Fatalf("got (%v, %v), want an error mentioning %s", got, err, tt.wantErrLike)
				}
				if !strings.Contains(err.Error(), `["sh", "-lc"`) {
					t.Errorf("error %q does not say how to ask for a shell on purpose", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("splitCommand(%q): %v", tt.in, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolver_RefusesAProfileThatNamesNoSourceOrSeveral(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		token    TokenSource
		contains string
	}{
		{name: "none", token: TokenSource{}, contains: "names no token source"},
		{name: "two", token: TokenSource{Env: "T", Keychain: "saral:work"}, contains: "names 2 token sources"},
		{name: "three", token: TokenSource{Env: "T", Keychain: "saral:work", Command: []string{"pass"}}, contains: "names 3 token sources"},
		{name: "an argv with an empty program", token: TokenSource{Command: []string{"  "}}, contains: "token.command is empty"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p := Profile{Name: "work", Site: "example.atlassian.net", Email: "you@example.com", Token: tt.token}
			if _, err := p.Resolver(); err == nil || !strings.Contains(err.Error(), tt.contains) {
				t.Fatalf("Resolver: got %v, want an error mentioning %q", err, tt.contains)
			}
			if _, err := p.ResolveToken(t.Context()); err == nil {
				t.Error("ResolveToken succeeded with an unusable token source")
			}
		})
	}
}

func TestProfileString_NeverShowsAResolvedToken(t *testing.T) {
	t.Setenv("SARAL_TEST_TOKEN", testSecret)

	p := envProfile("SARAL_TEST_TOKEN")
	token, err := p.ResolveToken(t.Context())
	if err != nil {
		t.Fatalf("ResolveToken: %v", err)
	}
	if token != testSecret {
		t.Fatalf("the test is not exercising a real token: got %q", token)
	}

	cfg := Config{Active: "work", Mouse: true, Profiles: map[string]Profile{"work": p}}
	rendered := []string{
		p.String(),
		p.Token.String(),
		fmt.Sprintf("profile %v with token %v", p, p.Token),
		fmt.Sprintf("%+v", p),
		fmt.Sprintf("%#v", p),
		fmt.Sprintf("%v", cfg),
		fmt.Sprint(cfg.Profiles),
	}
	for _, s := range rendered {
		if strings.Contains(s, testSecret) {
			t.Errorf("a formatted profile leaked the token: %s", s)
		}
		if !strings.Contains(s, "SARAL_TEST_TOKEN") && !strings.Contains(s, "Env") {
			t.Errorf("a formatted profile does not say where the token comes from: %s", s)
		}
	}
}

func lookPath(t *testing.T, name string) string {
	t.Helper()

	path, err := exec.LookPath(name)
	if err != nil {
		t.Skipf("%s is not on PATH, and these tests must not invent one: %v", name, err)
	}
	return path
}

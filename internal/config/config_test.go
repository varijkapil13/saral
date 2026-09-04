package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestLoadFile_ParsesTheConfigPrintedInTheArchitectureDocs(t *testing.T) {
	t.Parallel()

	got, err := LoadFile(filepath.Join("testdata", "documented.toml"))
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	want := Config{
		Active: "work",
		Mouse:  true,
		Profiles: map[string]Profile{
			"work": {
				Name:  "work",
				Site:  "example.atlassian.net",
				Email: "you@example.com",
				Token: TokenSource{Keychain: "saral:work"},
				Timeline: Timeline{
					Start: []string{"Target start", "Start date"},
					End:   []string{"Target end", "Due date"},
				},
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parsed config\n got %+v\nwant %+v", got, want)
	}
}

func TestLoadFile_NormalisesEveryProfileAndDefaultsMouseToOn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		file string
		want Config
	}{
		{
			name: "two profiles, a scheme and trailing slash stripped, mouse turned off",
			file: "full.toml",
			want: Config{
				Active: "personal",
				Mouse:  false,
				Profiles: map[string]Profile{
					"work": {
						Name:   "work",
						Site:   "example.atlassian.net",
						Email:  "you@example.com",
						Theme:  "no-color",
						Glyphs: "ascii",
						Token:  TokenSource{Command: []string{"pass", "show", "jira/work"}},
					},
					"personal": {
						Name:     "personal",
						Site:     "personal.atlassian.net",
						Email:    "me@example.com",
						Token:    TokenSource{Env: "JIRA_TOKEN"},
						Timeline: Timeline{Start: []string{"Start date"}, End: []string{"duedate"}},
					},
				},
			},
		},
		{
			name: "a command written as a string is split into argv",
			file: "command_string.toml",
			want: Config{
				Active: "work",
				Mouse:  true,
				Profiles: map[string]Profile{
					"work": {
						Name:  "work",
						Site:  "example.atlassian.net",
						Email: "you@example.com",
						Token: TokenSource{Command: []string{"pass", "show", "jira/work"}},
					},
				},
			},
		},
		{
			name: "a token written as a dotted key",
			file: "dotted_token.toml",
			want: Config{
				Mouse: true,
				Profiles: map[string]Profile{
					"work": {
						Name:  "work",
						Site:  "example.atlassian.net",
						Email: "you@example.com",
						Token: TokenSource{Keychain: "saral:work"},
					},
				},
			},
		},
		{
			name: "a token written as its own table header",
			file: "header_token.toml",
			want: Config{
				Mouse: true,
				Profiles: map[string]Profile{
					"work": {
						Name:  "work",
						Site:  "example.atlassian.net",
						Email: "you@example.com",
						Token: TokenSource{Command: []string{"pass", "jira"}},
					},
				},
			},
		},
		{
			name: "an empty file is a config with no profiles",
			file: "empty.toml",
			want: Config{Mouse: true, Profiles: map[string]Profile{}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := LoadFile(filepath.Join("testdata", tt.file))
			if err != nil {
				t.Fatalf("LoadFile(%s): %v", tt.file, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parsed %s\n got %+v\nwant %+v", tt.file, got, tt.want)
			}
		})
	}
}

func TestLoadFile_RefusesAFileThatIsWrongOrHoldsASecret(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		file     string
		sentinel error
		contains []string
	}{
		{
			name:     "a bare token string is a secret in a file meant to be shareable",
			file:     "bad_literal_token.toml",
			sentinel: ErrSecretInFile,
			contains: []string{`profile "work"`, "keychain", "env", "command"},
		},
		{
			name:     "a token.value key is the same secret by another name",
			file:     "bad_token_value_key.toml",
			sentinel: ErrSecretInFile,
			contains: []string{`profile "work"`, "token.value"},
		},
		{
			name:     "any password-shaped key is refused, not just the documented ones",
			file:     "bad_password_key.toml",
			sentinel: ErrSecretInFile,
			contains: []string{"profiles.work.password"},
		},
		{
			name:     "a profile with no token source",
			file:     "bad_no_token.toml",
			contains: []string{`profile "work" has no token`},
		},
		{
			name:     "a profile naming two token sources",
			file:     "bad_two_tokens.toml",
			contains: []string{"names 2 token sources"},
		},
		{
			name:     "an empty token table names none",
			file:     "bad_empty_token.toml",
			contains: []string{"names no token source"},
		},
		{
			name:     "a typo in a profile key is not silently ignored",
			file:     "bad_unknown_key.toml",
			contains: []string{"unknown key", "profiles.work.sight"},
		},
		{
			name:     "a typo inside the token table is not silently ignored",
			file:     "bad_unknown_token_key.toml",
			contains: []string{"unknown key", "profiles.work.token.keychan"},
		},
		{
			name:     "a site that is not https",
			file:     "bad_scheme.toml",
			contains: []string{"must be reached over https"},
		},
		{
			name:     "a site pasted from the browser, with a path",
			file:     "bad_site_path.toml",
			contains: []string{"must be a bare host"},
		},
		{
			name:     "a profile with no email",
			file:     "bad_no_email.toml",
			contains: []string{"email is required"},
		},
		{
			name:     "an unknown theme",
			file:     "bad_theme.toml",
			contains: []string{`theme "solarized"`, "dark, light, no-color"},
		},
		{
			name:     "an unknown glyph set",
			file:     "bad_glyphs.toml",
			contains: []string{`glyphs "nerdfont"`, "nerd, unicode, ascii"},
		},
		{
			name:     "active naming a profile that is not there",
			file:     "bad_active.toml",
			contains: []string{`active = "prod"`, "[profiles.prod]"},
		},
		{
			name:     "a token that is neither a table nor a string",
			file:     "bad_token_type.toml",
			contains: []string{"token must be a table", "Integer"},
		},
		{
			name:     "a command string that a shell would have interpreted",
			file:     "bad_command_shell.toml",
			contains: []string{`"|"`, "never run through a shell", `["sh", "-lc"`},
		},
		{
			name:     "an empty command array",
			file:     "bad_command_empty.toml",
			contains: []string{"token.command is empty"},
		},
		{
			name:     "a command that is not a string or an array",
			file:     "bad_command_type.toml",
			contains: []string{"token.command must be an array of strings", "Integer"},
		},
		{
			name:     "a file that is not TOML at all",
			file:     "bad_toml.toml",
			contains: []string{"invalid TOML"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join("testdata", tt.file)
			_, err := LoadFile(path)
			if err == nil {
				t.Fatalf("LoadFile(%s) succeeded, want an error", tt.file)
			}
			if tt.sentinel != nil && !errors.Is(err, tt.sentinel) {
				t.Errorf("error %v does not match sentinel %v", err, tt.sentinel)
			}
			if !strings.Contains(err.Error(), path) {
				t.Errorf("error %q does not name the file", err)
			}
			for _, want := range tt.contains {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not mention %q", err, want)
				}
			}
		})
	}
}

func TestLoadFile_ReportsErrNoConfigForAFileThatIsNotThere(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.toml")
	cfg, err := LoadFile(path)
	if !errors.Is(err, ErrNoConfig) {
		t.Fatalf("LoadFile of a missing file: got %v, want ErrNoConfig", err)
	}
	if !reflect.DeepEqual(cfg, Config{}) {
		t.Errorf("got %+v, want the zero Config", cfg)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error %q does not name the path it looked at", err)
	}
}

func TestLoad_ReadsTheFileUnderTheConfigDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SARAL_CONFIG_DIR", dir)

	if _, err := Load(); !errors.Is(err, ErrNoConfig) {
		t.Fatalf("Load with an empty config dir: got %v, want ErrNoConfig", err)
	}

	source, err := os.ReadFile(filepath.Join("testdata", "documented.toml"))
	if err != nil {
		t.Fatalf("reading the fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), source, 0o600); err != nil {
		t.Fatalf("writing the config: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	p, err := cfg.Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if p.BaseURL() != "https://example.atlassian.net" {
		t.Errorf("BaseURL = %q", p.BaseURL())
	}
}

func TestDirAndCacheDir_PreferTheOverrideThenXDGThenHome(t *testing.T) {
	home := filepath.Join(string(filepath.Separator), "tmp", "saral-home")

	dirs := []struct {
		kind     string
		fn       func() (string, error)
		override string
		xdg      string
		fallback string
	}{
		{kind: "config", fn: Dir, override: "SARAL_CONFIG_DIR", xdg: "XDG_CONFIG_HOME", fallback: ".config"},
		{kind: "cache", fn: CacheDir, override: "SARAL_CACHE_DIR", xdg: "XDG_CACHE_HOME", fallback: ".cache"},
	}

	for _, d := range dirs {
		tests := []struct {
			name        string
			override    string
			xdg         string
			want        string
			wantErrLike string
		}{
			{
				name:     "the override wins over everything",
				override: filepath.Join(string(filepath.Separator), "tmp", "explicit"),
				xdg:      filepath.Join(string(filepath.Separator), "tmp", "xdg"),
				want:     filepath.Join(string(filepath.Separator), "tmp", "explicit"),
			},
			{
				name: "XDG gets the application directory appended",
				xdg:  filepath.Join(string(filepath.Separator), "tmp", "xdg"),
				want: filepath.Join(string(filepath.Separator), "tmp", "xdg", dirName()),
			},
			{
				name: "with neither set it lands under the home directory on every platform",
				want: filepath.Join(home, d.fallback, dirName()),
			},
			{
				name:        "a relative override is refused",
				override:    filepath.Join("relative", "path"),
				wantErrLike: d.override,
			},
			{
				name:        "a relative XDG directory is refused, as the spec says",
				xdg:         filepath.Join("relative", "path"),
				wantErrLike: d.xdg,
			},
		}

		for _, tt := range tests {
			t.Run(d.kind+": "+tt.name, func(t *testing.T) {
				if runtime.GOOS == "windows" {
					t.Skip("the XDG layout is a unix concern")
				}
				t.Setenv("HOME", home)
				t.Setenv(d.override, tt.override)
				t.Setenv(d.xdg, tt.xdg)

				got, err := d.fn()
				if tt.wantErrLike != "" {
					if err == nil || !strings.Contains(err.Error(), tt.wantErrLike) {
						t.Fatalf("got (%q, %v), want an error mentioning %s", got, err, tt.wantErrLike)
					}
					return
				}
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got != tt.want {
					t.Errorf("got %q, want %q", got, tt.want)
				}
			})
		}
	}
}

func TestPath_IsConfigTomlInsideTheConfigDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SARAL_CONFIG_DIR", dir)

	got, err := Path()
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if want := filepath.Join(dir, "config.toml"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestSave_RoundTripsThroughLoadFileWithOwnerOnlyPermissions(t *testing.T) {
	t.Parallel()

	want := Config{
		Active: "work",
		Mouse:  false,
		Profiles: map[string]Profile{
			"work": {
				Name:     "work",
				Site:     "example.atlassian.net",
				Email:    "you@example.com",
				Token:    TokenSource{Keychain: "saral:work"},
				Timeline: Timeline{Start: []string{"Target start"}, End: []string{"Target end"}},
				Theme:    "dark",
				Glyphs:   "unicode",
			},
			"personal": {
				Name:  "personal",
				Site:  "personal.atlassian.net",
				Email: "me@example.com",
				Token: TokenSource{Command: []string{"pass", "show", "jira"}},
			},
			"my.work": {
				Name:  "my.work",
				Site:  "third.atlassian.net",
				Email: "third@example.com",
				Token: TokenSource{Env: "JIRA_TOKEN"},
			},
		},
	}

	path := filepath.Join(t.TempDir(), "nested", "config.toml")
	if err := want.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	layout := `active = "work"
mouse = false

[profiles."my.work"]
site  = "third.atlassian.net"
email = "third@example.com"
token = { env = "JIRA_TOKEN" }

[profiles.personal]
site  = "personal.atlassian.net"
email = "me@example.com"
token = { command = ["pass", "show", "jira"] }

[profiles.work]
site   = "example.atlassian.net"
email  = "you@example.com"
theme  = "dark"
glyphs = "unicode"
token  = { keychain = "saral:work" }

[profiles.work.timeline]
start = ["Target start"]
end   = ["Target end"]
`
	if string(written) != layout {
		t.Errorf("Save wrote\n%s\nwant\n%s", written, layout)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("file mode = %v, want -rw-------", perm)
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("Stat of the directory: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Errorf("directory mode = %v, want drwx------", perm)
	}

	got, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile of what Save wrote: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip\n got %+v\nwant %+v", got, want)
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("Save left %d files behind, want only config.toml", len(entries))
	}
}

// A field id is a site's own, so the list is kept per profile beside
// Timeline's field names rather than in ui.toml, and it round-trips in the
// order it was pinned rather than however a map would happen to iterate it.
func TestSave_RoundTripsPinnedFieldIDsInPinOrder(t *testing.T) {
	t.Parallel()

	want := Config{
		Active: "work",
		Mouse:  true,
		Profiles: map[string]Profile{
			"work": {
				Name: "work", Site: "example.atlassian.net", Email: "you@example.com",
				Token:  TokenSource{Env: "JIRA_TOKEN"},
				Pinned: []string{"customfield_13401", "duedate", "customfield_13402"},
			},
		},
	}

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := want.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if !strings.Contains(string(written), `pinned = ["customfield_13401", "duedate", "customfield_13402"]`) {
		t.Errorf("Save did not write the pinned array, in pin order:\n%s", written)
	}

	got, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile of what Save wrote: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip\n got %+v\nwant %+v", got, want)
	}
}

// An id the site no longer has is this profile's business to keep, not
// config's to prune: nothing here resolves a field id against a site, so a
// profile that has seen two sites keeps every id either one ever answered to.
func TestSave_KeepsAnUnrecognisedPinnedIDRatherThanPruningIt(t *testing.T) {
	t.Parallel()

	want := Config{
		Profiles: map[string]Profile{
			"work": {
				Name: "work", Site: "example.atlassian.net", Email: "you@example.com",
				Token:  TokenSource{Env: "JIRA_TOKEN"},
				Pinned: []string{"customfield_10999", "customfield_13401"},
			},
		},
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := want.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if pinned := got.Profiles["work"].Pinned; !reflect.DeepEqual(pinned, want.Profiles["work"].Pinned) {
		t.Errorf("Pinned = %v, want both ids kept: %v", pinned, want.Profiles["work"].Pinned)
	}
}

func TestSave_NeverWritesTheTokenItself(t *testing.T) {
	const secret = "9d8f7a6b5c4d3e2f1a0b"
	t.Setenv("SARAL_TEST_TOKEN", secret)

	cfg := Config{
		Active: "work",
		Mouse:  true,
		Profiles: map[string]Profile{
			"work": {
				Site:  "example.atlassian.net",
				Email: "you@example.com",
				Token: TokenSource{Env: "SARAL_TEST_TOKEN"},
			},
		},
	}
	p, err := cfg.Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	got, err := p.ResolveToken(t.Context())
	if err != nil {
		t.Fatalf("ResolveToken: %v", err)
	}
	if got != secret {
		t.Fatalf("ResolveToken = %q, want the value of the environment variable", got)
	}

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := cfg.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if strings.Contains(string(written), secret) {
		t.Errorf("the token was written to the config file:\n%s", written)
	}
	if !strings.Contains(string(written), "SARAL_TEST_TOKEN") {
		t.Errorf("the config does not say where the token comes from:\n%s", written)
	}
}

func TestSave_RefusesAConfigThatCannotBeLoadedBack(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		cfg      Config
		contains string
	}{
		{
			name: "a profile with no token source",
			cfg: Config{Profiles: map[string]Profile{
				"work": {Site: "example.atlassian.net", Email: "you@example.com"},
			}},
			contains: "names no token source",
		},
		{
			name: "a site that was never normalised",
			cfg: Config{Profiles: map[string]Profile{
				"work": {Site: "https://example.atlassian.net", Email: "you@example.com", Token: TokenSource{Env: "T"}},
			}},
			contains: "is not normalised",
		},
		{
			name: "active pointing at nothing",
			cfg: Config{Active: "prod", Profiles: map[string]Profile{
				"work": {Site: "example.atlassian.net", Email: "you@example.com", Token: TokenSource{Env: "T"}},
			}},
			contains: `active = "prod"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "config.toml")
			err := tt.cfg.Save(path)
			if err == nil {
				t.Fatalf("Save succeeded, want an error")
			}
			if !strings.Contains(err.Error(), tt.contains) {
				t.Errorf("error %q does not mention %q", err, tt.contains)
			}
			if _, statErr := os.Stat(path); statErr == nil {
				t.Errorf("a rejected config was still written to disk")
			}
		})
	}
}

func TestCurrent_FallsBackToTheOnlyProfileAndSaysSoWhenItCannot(t *testing.T) {
	t.Parallel()

	one := Profile{Name: "work", Site: "example.atlassian.net", Email: "you@example.com", Token: TokenSource{Env: "T"}}
	two := Profile{Name: "home", Site: "home.atlassian.net", Email: "me@example.com", Token: TokenSource{Env: "T"}}

	tests := []struct {
		name        string
		cfg         Config
		wantProfile string
		wantErrLike string
	}{
		{
			name:        "the active profile",
			cfg:         Config{Active: "home", Profiles: map[string]Profile{"work": one, "home": two}},
			wantProfile: "home",
		},
		{
			name:        "the only profile, with no active set",
			cfg:         Config{Profiles: map[string]Profile{"work": one}},
			wantProfile: "work",
		},
		{
			name:        "several profiles and no active one",
			cfg:         Config{Profiles: map[string]Profile{"work": one, "home": two}},
			wantErrLike: `add active = "home"`,
		},
		{
			name:        "no profiles at all",
			cfg:         Config{},
			wantErrLike: "no profile in it",
		},
		{
			name:        "an active profile that is not in the map",
			cfg:         Config{Active: "prod", Profiles: map[string]Profile{"work": one}},
			wantErrLike: `no such profile "prod" (configured: work)`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := tt.cfg.Current()
			if tt.wantErrLike != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrLike) {
					t.Fatalf("got (%v, %v), want an error mentioning %q", got, err, tt.wantErrLike)
				}
				if !errors.Is(err, ErrNoProfile) {
					t.Errorf("error %v does not match ErrNoProfile", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Current: %v", err)
			}
			if got.Name != tt.wantProfile {
				t.Errorf("got profile %q, want %q", got.Name, tt.wantProfile)
			}
		})
	}
}

func TestGet_NamesTheProfileFromTheMapKeyWhenTheStructDoesNot(t *testing.T) {
	t.Parallel()

	cfg := Config{Profiles: map[string]Profile{
		"work": {Site: "example.atlassian.net", Email: "you@example.com", Token: TokenSource{Env: "T"}},
	}}
	got, err := cfg.Get("work")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "work" {
		t.Errorf("Name = %q, want it filled in from the map key", got.Name)
	}
	if names := cfg.Names(); !reflect.DeepEqual(names, []string{"work"}) {
		t.Errorf("Names = %v", names)
	}
}

func TestNormalizeSite_AcceptsWhatPeopleActuallyPaste(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in          string
		want        string
		wantErrLike string
	}{
		{in: "example.atlassian.net", want: "example.atlassian.net"},
		{in: "  example.atlassian.net  ", want: "example.atlassian.net"},
		{in: "https://example.atlassian.net", want: "example.atlassian.net"},
		{in: "https://example.atlassian.net/", want: "example.atlassian.net"},
		{in: "HTTPS://Example.Atlassian.NET/", want: "example.atlassian.net"},
		{in: "jira.internal:8080", want: "jira.internal:8080"},
		{in: "", wantErrLike: "site is required"},
		{in: "http://example.atlassian.net", wantErrLike: "over https"},
		{in: "ftp://example.atlassian.net", wantErrLike: "over https"},
		{in: "https://", wantErrLike: "no host"},
		{in: "example.atlassian.net/jira", wantErrLike: "bare host"},
		{in: "you@example.atlassian.net", wantErrLike: "not a host name"},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()

			got, err := NormalizeSite(tt.in)
			if tt.wantErrLike != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrLike) {
					t.Fatalf("got (%q, %v), want an error mentioning %q", got, err, tt.wantErrLike)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeSite(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSave_QuotesAProfileNameThatIsNotABareKey(t *testing.T) {
	t.Parallel()

	const name = `odd "name".2`
	want := Config{
		Mouse: true,
		Profiles: map[string]Profile{
			name: {
				Name:  name,
				Site:  "example.atlassian.net",
				Email: "you@example.com",
				Token: TokenSource{Env: "JIRA_TOKEN"},
			},
		},
	}

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := want.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip\n got %+v\nwant %+v", got, want)
	}
}

func TestSave_ReportsWhyItCouldNotWrite(t *testing.T) {
	t.Parallel()

	blocker := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	cfg := Config{Profiles: map[string]Profile{
		"work": {Site: "example.atlassian.net", Email: "you@example.com", Token: TokenSource{Env: "T"}},
	}}

	err := cfg.Save(filepath.Join(blocker, "config.toml"))
	if err == nil {
		t.Fatal("Save succeeded with a file where the directory should be")
	}
	if !strings.Contains(err.Error(), blocker) {
		t.Errorf("error %q does not say which path it failed on", err)
	}
}

func TestQuote_EscapesWhatTOMLCannotHoldLiterally(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "plain", in: "example.atlassian.net", want: `"example.atlassian.net"`},
		{name: "a quote", in: `a"b`, want: `"a\"b"`},
		{name: "a backslash", in: `a\b`, want: `"a\\b"`},
		{name: "a newline and a tab", in: "a\n\tb", want: `"a\n\tb"`},
		{name: "an unnamed control character", in: "a\x00\x1bb", want: `"a\u0000\u001Bb"`},
		{name: "text outside ASCII stays as it is", in: "ünïcødé ✓", want: `"ünïcødé ✓"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := quote(tt.in); got != tt.want {
				t.Errorf("quote(%q) = %s, want %s", tt.in, got, tt.want)
			}
		})
	}
}

func TestTokenSourceString_NamesTheSourceAndNothingElse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		source TokenSource
		want   string
	}{
		{source: TokenSource{Keychain: "saral:work"}, want: "keychain entry saral:work"},
		{source: TokenSource{Env: "JIRA_TOKEN"}, want: "environment variable JIRA_TOKEN"},
		{source: TokenSource{Command: []string{"pass", "jira"}}, want: "command pass jira"},
		{source: TokenSource{}, want: "nowhere"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()

			if got := tt.source.String(); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

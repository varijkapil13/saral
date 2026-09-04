// Package config reads and writes Saral's profiles. The file never holds a
// secret: a token is named, not stored.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/BurntSushi/toml"

	"github.com/varijkapil13/saral/internal/app"
)

const (
	appName  = "saral"
	fileName = "config.toml"

	dirPerm  fs.FileMode = 0o700
	filePerm fs.FileMode = 0o600
)

// ErrNoConfig reports that there is no config file yet, which is the first-run
// state rather than a failure.
var ErrNoConfig = errors.New("no configuration file")

// ErrNoProfile reports that the named profile, or the active one, is not there.
var ErrNoProfile = errors.New("no such profile")

// ErrSecretInFile reports that the file holds, or would hold, a literal token.
var ErrSecretInFile = errors.New("a token must not be written into the config file")

// Config is the whole file: the profiles and the settings shared by them.
type Config struct {
	Active   string
	Profiles map[string]Profile
	Mouse    bool
}

// Profile is one Jira account on one site.
type Profile struct {
	Name  string
	Site  string
	Email string
	// Project is the project a session opens in. Jira grants Move, Create and
	// Delete per project and a board belongs to one, so the capability probe
	// answers differently in two projects on one site and has to be told which.
	Project  string
	Token    TokenSource
	Timeline Timeline
	Theme    string
	// Scheme is which named set of colours the theme draws from — Theme is
	// light or dark, Scheme is which colours mean accent, danger and the rest.
	// The two are independent: any scheme works in either mode.
	Scheme string
	Glyphs string
	// Queries are the searches this profile keeps, each optionally bound to a
	// number key. They are app's own type, validated by app's own rules, so that
	// a file and a keypress cannot disagree about what a saved query is; the
	// projection is not written, because a saved query opens into the issue list.
	Queries []app.SavedQuery
}

// TokenSource names where the API token comes from. Exactly one field is set.
type TokenSource struct {
	Keychain string
	Env      string
	Command  []string
}

// Timeline lists the field names, most preferred first, that carry an issue's
// start and end dates on this site.
type Timeline struct {
	Start []string `toml:"start"`
	End   []string `toml:"end"`
}

var (
	themes = []string{"", "dark", "light", "no-color"}
	// schemes mirrors kernel.Schemes by name. It cannot read that list directly
	// — kernel already imports this package for where a theme choice is saved,
	// so the reverse import would cycle — the same reason themes above is its
	// own hand-kept list rather than reading ThemeMode's.
	schemes   = []string{"", "default", "nord", "dracula", "solarized", "gruvbox"}
	glyphSets = []string{"", "unicode", "ascii"}

	secretKeys = []string{"token", "value", "secret", "password", "api_token", "api-token", "apitoken"}
)

// Dir returns the directory holding config.toml.
func Dir() (string, error) { return xdgPath("SARAL_CONFIG_DIR", "XDG_CONFIG_HOME", ".config") }

// CacheDir returns the directory holding the bbolt cache.
func CacheDir() (string, error) { return xdgPath("SARAL_CACHE_DIR", "XDG_CACHE_HOME", ".cache") }

// Path returns the full path of config.toml.
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, fileName), nil
}

// os.UserConfigDir ignores XDG_CONFIG_HOME on darwin, so the lookup is done here.
//
// The directory is named for the build: a release keeps its files under saral
// and anything built from a checkout under saral-dev, so a development copy
// cannot rewrite what an installed one reads. SARAL_CONFIG_DIR and
// SARAL_CACHE_DIR name a directory outright and are not qualified again.
func xdgPath(override, xdgVar, homeRelative string) (string, error) {
	if v := strings.TrimSpace(os.Getenv(override)); v != "" {
		if !filepath.IsAbs(v) {
			return "", fmt.Errorf("%s must be an absolute path, got %q", override, v)
		}
		return filepath.Clean(v), nil
	}
	if v := strings.TrimSpace(os.Getenv(xdgVar)); v != "" {
		if !filepath.IsAbs(v) {
			return "", fmt.Errorf("%s must be an absolute path, got %q", xdgVar, v)
		}
		return filepath.Join(v, dirName()), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locating the home directory: %w", err)
	}
	return filepath.Join(home, homeRelative, dirName()), nil
}

// Load reads the config file from its XDG location. A missing file yields
// ErrNoConfig and an empty Config, which is the signal to run onboarding.
func Load() (Config, error) {
	path, err := Path()
	if err != nil {
		return Config{}, err
	}
	return LoadFile(path)
}

// LoadFile reads a config file from an explicit path.
func LoadFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return Config{}, fmt.Errorf("%w at %s", ErrNoConfig, path)
	case err != nil:
		return Config{}, fmt.Errorf("reading %s: %w", path, err)
	}
	cfg, err := parse(data)
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

type fileConfig struct {
	Active   string                 `toml:"active"`
	Mouse    *bool                  `toml:"mouse"`
	Profiles map[string]fileProfile `toml:"profiles"`
}

type fileProfile struct {
	Site     string         `toml:"site"`
	Email    string         `toml:"email"`
	Project  string         `toml:"project"`
	Token    toml.Primitive `toml:"token"`
	Timeline Timeline       `toml:"timeline"`
	Theme    string         `toml:"theme"`
	Scheme   string         `toml:"scheme"`
	Glyphs   string         `toml:"glyphs"`
	Queries  []fileQuery    `toml:"queries"`
}

type fileQuery struct {
	Name string `toml:"name"`
	JQL  string `toml:"jql"`
	Key  int    `toml:"key"`
}

type fileToken struct {
	Keychain string         `toml:"keychain"`
	Env      string         `toml:"env"`
	Command  toml.Primitive `toml:"command"`
}

func parse(data []byte) (Config, error) {
	var f fileConfig
	md, err := toml.Decode(string(data), &f)
	if err != nil {
		return Config{}, fmt.Errorf("invalid TOML: %w", err)
	}

	cfg := Config{
		Active:   strings.TrimSpace(f.Active),
		Profiles: make(map[string]Profile, len(f.Profiles)),
		Mouse:    f.Mouse == nil || *f.Mouse,
	}
	for _, name := range slices.Sorted(maps.Keys(f.Profiles)) {
		p, err := decodeProfile(&md, name, f.Profiles[name])
		if err != nil {
			return Config{}, err
		}
		cfg.Profiles[name] = p
	}
	if err := unknownKeys(md.Undecoded()); err != nil {
		return Config{}, err
	}
	if _, ok := cfg.Profiles[cfg.Active]; cfg.Active != "" && !ok {
		return Config{}, fmt.Errorf("active = %q but there is no [profiles.%s] section", cfg.Active, cfg.Active)
	}
	return cfg, nil
}

func undecodedUnder(md *toml.MetaData, prefix ...string) []toml.Key {
	var found []toml.Key
	for _, k := range md.Undecoded() {
		if len(k) > len(prefix) && slices.Equal([]string(k)[:len(prefix)], prefix) {
			found = append(found, k)
		}
	}
	return found
}

func unknownKeys(keys []toml.Key) error {
	if len(keys) == 0 {
		return nil
	}
	for _, k := range keys {
		if len(k) > 0 && slices.Contains(secretKeys, strings.ToLower(k[len(k)-1])) {
			return fmt.Errorf("%s: %w", k, ErrSecretInFile)
		}
	}
	names := make([]string, 0, len(keys))
	for _, k := range keys {
		names = append(names, k.String())
	}
	return fmt.Errorf("unknown key %s", strings.Join(names, ", "))
}

func decodeProfile(md *toml.MetaData, name string, fp fileProfile) (Profile, error) {
	site, err := NormalizeSite(fp.Site)
	if err != nil {
		return Profile{}, fmt.Errorf("profile %q: %w", name, err)
	}
	token, err := decodeToken(md, name, fp.Token)
	if err != nil {
		return Profile{}, err
	}
	p := Profile{
		Name:     name,
		Site:     site,
		Email:    strings.TrimSpace(fp.Email),
		Project:  strings.TrimSpace(fp.Project),
		Token:    token,
		Timeline: fp.Timeline,
		Theme:    strings.TrimSpace(fp.Theme),
		Scheme:   strings.TrimSpace(fp.Scheme),
		Glyphs:   strings.TrimSpace(fp.Glyphs),
		Queries:  decodeQueries(fp.Queries),
	}
	if err := p.Validate(); err != nil {
		return Profile{}, err
	}
	return p, nil
}

func decodeQueries(in []fileQuery) []app.SavedQuery {
	if len(in) == 0 {
		return nil
	}
	out := make([]app.SavedQuery, 0, len(in))
	for _, q := range in {
		out = append(out, app.SavedQuery{
			Name: strings.TrimSpace(q.Name),
			JQL:  strings.TrimSpace(q.JQL),
			Slot: q.Key,
		})
	}
	return out
}

func decodeToken(md *toml.MetaData, name string, prim toml.Primitive) (TokenSource, error) {
	if !md.IsDefined("profiles", name, "token") {
		return TokenSource{}, tokenSourceErr(name, "has no token")
	}
	switch kind := md.Type("profiles", name, "token"); kind {
	// An empty type is the implicit table of a dotted key: token.keychain = "…".
	case "Hash", "":
	case "String":
		return TokenSource{}, fmt.Errorf("profile %q: %w; %s", name, ErrSecretInFile, tokenForms(name))
	default:
		return TokenSource{}, fmt.Errorf("profile %q: token must be a table, but this one is %s; %s",
			name, kind, tokenForms(name))
	}
	for _, k := range secretKeys {
		if md.IsDefined("profiles", name, "token", k) {
			return TokenSource{}, fmt.Errorf("profile %q: token.%s: %w; %s", name, k, ErrSecretInFile, tokenForms(name))
		}
	}

	var ft fileToken
	if err := md.PrimitiveDecode(prim, &ft); err != nil {
		return TokenSource{}, fmt.Errorf("profile %q: token: %w", name, err)
	}
	src := TokenSource{Keychain: strings.TrimSpace(ft.Keychain), Env: strings.TrimSpace(ft.Env)}

	if md.IsDefined("profiles", name, "token", "command") {
		argv, err := decodeCommand(md, name, ft.Command)
		if err != nil {
			return TokenSource{}, err
		}
		src.Command = argv
	}
	if err := unknownKeys(undecodedUnder(md, "profiles", name, "token")); err != nil {
		return TokenSource{}, fmt.Errorf("profile %q: %w", name, err)
	}
	if n := src.count(); n != 1 {
		return TokenSource{}, tokenSourceErr(name, countPhrase(n))
	}
	return src, nil
}

func decodeCommand(md *toml.MetaData, name string, prim toml.Primitive) ([]string, error) {
	switch kind := md.Type("profiles", name, "token", "command"); kind {
	case "Array":
		var argv []string
		if err := md.PrimitiveDecode(prim, &argv); err != nil {
			return nil, fmt.Errorf("profile %q: token.command must be an array of strings: %w", name, err)
		}
		return argv, validateArgv(name, argv)
	case "String":
		var raw string
		if err := md.PrimitiveDecode(prim, &raw); err != nil {
			return nil, fmt.Errorf("profile %q: token.command: %w", name, err)
		}
		argv, err := splitCommand(raw)
		if err != nil {
			return nil, fmt.Errorf("profile %q: token.command %q: %w", name, raw, err)
		}
		return argv, validateArgv(name, argv)
	default:
		return nil, fmt.Errorf("profile %q: token.command must be an array of strings, but this one is %s",
			name, kind)
	}
}

func validateArgv(name string, argv []string) error {
	if len(argv) == 0 || strings.TrimSpace(argv[0]) == "" {
		return fmt.Errorf(`profile %q: token.command is empty; write it as ["pass", "jira"]`, name)
	}
	return nil
}

func tokenSourceErr(name, problem string) error {
	return fmt.Errorf("profile %q %s; %s", name, problem, tokenForms(name))
}

func tokenForms(name string) string {
	return fmt.Sprintf(`set exactly one of token = { keychain = "%s:%s" }, `+
		`token = { env = "JIRA_TOKEN" } or token = { command = ["pass", "jira"] }`, appName, name)
}

func countPhrase(n int) string {
	if n == 0 {
		return "names no token source"
	}
	return fmt.Sprintf("names %d token sources", n)
}

// NormalizeSite reduces what a user pastes — a full URL, a trailing slash, odd
// case — to the bare host a Profile.Site must hold.
func NormalizeSite(raw string) (string, error) {
	site := strings.TrimSpace(raw)
	if site == "" {
		return "", errors.New(`site is required, e.g. site = "example.atlassian.net"`)
	}
	if i := strings.Index(site, "://"); i >= 0 {
		if !strings.EqualFold(site[:i], "https") {
			return "", fmt.Errorf("site %q must be reached over https", raw)
		}
		site = site[i+len("://"):]
	}
	site = strings.TrimRight(site, "/")
	switch {
	case site == "":
		return "", fmt.Errorf("site %q has no host", raw)
	case strings.ContainsAny(site, "/?#"):
		return "", fmt.Errorf("site %q must be a bare host with no path, e.g. example.atlassian.net", raw)
	case strings.ContainsAny(site, " \t@\\"):
		return "", fmt.Errorf("site %q is not a host name", raw)
	}
	return strings.ToLower(site), nil
}

// Validate reports whether the profile is usable, naming the profile in every
// message so a config file with several profiles is diagnosable.
func (p Profile) Validate() error {
	if strings.TrimSpace(p.Name) == "" {
		return errors.New("a profile has no name")
	}
	site, err := NormalizeSite(p.Site)
	if err != nil {
		return fmt.Errorf("profile %q: %w", p.Name, err)
	}
	if site != p.Site {
		return fmt.Errorf("profile %q: site %q is not normalised, expected %q", p.Name, p.Site, site)
	}
	if strings.TrimSpace(p.Email) == "" {
		return fmt.Errorf(`profile %q: email is required, it is the account the API token belongs to`, p.Name)
	}
	if strings.ContainsFunc(p.Project, unicode.IsSpace) {
		return fmt.Errorf("profile %q: project %q is not a project key", p.Name, p.Project)
	}
	if n := p.Token.count(); n != 1 {
		return tokenSourceErr(p.Name, countPhrase(n))
	}
	if len(p.Token.Command) > 0 {
		if err := validateArgv(p.Name, p.Token.Command); err != nil {
			return err
		}
	}
	if !slices.Contains(themes, p.Theme) {
		return fmt.Errorf("profile %q: theme %q is not one of %s", p.Name, p.Theme, strings.Join(themes[1:], ", "))
	}
	if !slices.Contains(schemes, p.Scheme) {
		return fmt.Errorf("profile %q: scheme %q is not one of %s", p.Name, p.Scheme, strings.Join(schemes[1:], ", "))
	}
	if !slices.Contains(glyphSets, p.Glyphs) {
		return fmt.Errorf("profile %q: glyphs %q is not one of %s", p.Name, p.Glyphs, strings.Join(glyphSets[1:], ", "))
	}
	return validateQueries(p.Name, p.Queries)
}

// validateQueries holds a query in the file to the rules app applies to one
// bound at the keyboard, and refuses the two things a file can say that a
// keypress cannot: two queries under one name, or two on one key. Add resolves
// both by taking the newer, which here would drop a line somebody wrote.
func validateQueries(profile string, queries []app.SavedQuery) error {
	names := make(map[string]struct{}, len(queries))
	keys := make(map[int]string, len(queries))
	for _, q := range queries {
		name := strings.TrimSpace(q.Name)
		lowered := strings.ToLower(name)
		if _, dup := names[lowered]; dup {
			return fmt.Errorf("profile %q: two saved queries are called %q", profile, name)
		}
		names[lowered] = struct{}{}
		if q.Slot <= 0 {
			continue
		}
		if other, dup := keys[q.Slot]; dup {
			return fmt.Errorf("profile %q: %q and %q both ask for key %d", profile, other, name, q.Slot)
		}
		keys[q.Slot] = name
	}
	if _, err := app.NewSavedQueries(queries...); err != nil {
		return fmt.Errorf("profile %q: %w", profile, err)
	}
	return nil
}

// BaseURL is the site's root URL, with no trailing slash.
func (p Profile) BaseURL() string { return "https://" + p.Site }

// String never reveals a token, because a profile never holds one.
func (p Profile) String() string {
	return fmt.Sprintf("profile %s (site %s, email %s, token from %s)", p.Name, p.Site, p.Email, p.Token)
}

func (t TokenSource) count() int {
	n := 0
	for _, set := range []bool{t.Keychain != "", t.Env != "", len(t.Command) > 0} {
		if set {
			n++
		}
	}
	return n
}

// String names where the token comes from, never what it is.
func (t TokenSource) String() string {
	switch {
	case t.Keychain != "":
		return "keychain entry " + t.Keychain
	case t.Env != "":
		return "environment variable " + t.Env
	case len(t.Command) > 0:
		return "command " + strings.Join(t.Command, " ")
	default:
		return "nowhere"
	}
}

// Names lists the configured profiles in a stable order.
func (c Config) Names() []string { return slices.Sorted(maps.Keys(c.Profiles)) }

// Current returns the active profile, or the only one when none is marked
// active.
func (c Config) Current() (Profile, error) {
	if c.Active != "" {
		return c.Get(c.Active)
	}
	if len(c.Profiles) == 1 {
		for name := range c.Profiles {
			return c.Get(name)
		}
	}
	if len(c.Profiles) == 0 {
		return Profile{}, fmt.Errorf("%w: the config file has no profile in it", ErrNoProfile)
	}
	return Profile{}, fmt.Errorf(`%w: none is active, add active = "%s"`, ErrNoProfile, c.Names()[0])
}

// Get returns a profile by name.
func (c Config) Get(name string) (Profile, error) {
	p, ok := c.Profiles[name]
	if !ok {
		configured := "none"
		if len(c.Profiles) > 0 {
			configured = strings.Join(c.Names(), ", ")
		}
		return Profile{}, fmt.Errorf("%w %q (configured: %s)", ErrNoProfile, name, configured)
	}
	if p.Name == "" {
		p.Name = name
	}
	return p, nil
}

// Save writes the config atomically, owner-readable only. It writes where each
// token comes from and never the token itself.
func (c Config) Save(path string) error {
	data, err := c.encode()
	if err != nil {
		return err
	}
	return writeAtomic(path, data)
}

// writeAtomic writes a file through a temporary one beside it, so an interrupted
// write leaves what was there before rather than half of what replaces it.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".saral-*.tmp")
	if err != nil {
		return fmt.Errorf("creating a temporary file in %s: %w", dir, err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()

	if err := writeAll(tmp, data); err != nil {
		return fmt.Errorf("writing %s: %w", tmp.Name(), err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("replacing %s: %w", path, err)
	}
	return nil
}

func writeAll(f *os.File, data []byte) error {
	write := func() error {
		if err := f.Chmod(filePerm); err != nil {
			return err
		}
		if _, err := f.Write(data); err != nil {
			return err
		}
		return f.Sync()
	}
	return errors.Join(write(), f.Close())
}

// encode writes the layout printed in docs/ARCHITECTURE.md, and only the keys
// listed here, which is what makes it impossible for Save to spill a token.
func (c Config) encode() ([]byte, error) {
	if _, ok := c.Profiles[c.Active]; c.Active != "" && !ok {
		return nil, fmt.Errorf("active = %q but there is no such profile", c.Active)
	}
	var b strings.Builder
	if c.Active != "" {
		fmt.Fprintf(&b, "active = %s\n", quote(c.Active))
	}
	fmt.Fprintf(&b, "mouse = %t\n", c.Mouse)

	for _, name := range c.Names() {
		p := c.Profiles[name]
		p.Name = name
		if err := p.Validate(); err != nil {
			return nil, err
		}
		fmt.Fprintf(&b, "\n[profiles.%s]\n", tomlKey(name))
		pairs := [][2]string{{"site", quote(p.Site)}, {"email", quote(p.Email)}}
		if p.Project != "" {
			pairs = append(pairs, [2]string{"project", quote(p.Project)})
		}
		if p.Theme != "" {
			pairs = append(pairs, [2]string{"theme", quote(p.Theme)})
		}
		if p.Scheme != "" {
			pairs = append(pairs, [2]string{"scheme", quote(p.Scheme)})
		}
		if p.Glyphs != "" {
			pairs = append(pairs, [2]string{"glyphs", quote(p.Glyphs)})
		}
		pairs = append(pairs, [2]string{"token", inlineToken(p.Token)})
		writePairs(&b, pairs)

		if len(p.Timeline.Start) > 0 || len(p.Timeline.End) > 0 {
			fmt.Fprintf(&b, "\n[profiles.%s.timeline]\n", tomlKey(name))
			if len(p.Timeline.Start) > 0 {
				fmt.Fprintf(&b, "start = %s\n", tomlArray(p.Timeline.Start))
			}
			if len(p.Timeline.End) > 0 {
				fmt.Fprintf(&b, "end   = %s\n", tomlArray(p.Timeline.End))
			}
		}
		for _, q := range p.Queries {
			fmt.Fprintf(&b, "\n[[profiles.%s.queries]]\n", tomlKey(name))
			query := [][2]string{{"name", quote(q.Name)}, {"jql", quote(q.JQL)}}
			if q.Slot > 0 {
				query = append(query, [2]string{"key", strconv.Itoa(q.Slot)})
			}
			writePairs(&b, query)
		}
	}
	return []byte(b.String()), nil
}

func writePairs(b *strings.Builder, pairs [][2]string) {
	width := 0
	for _, kv := range pairs {
		width = max(width, len(kv[0]))
	}
	for _, kv := range pairs {
		fmt.Fprintf(b, "%-*s = %s\n", width, kv[0], kv[1])
	}
}

func inlineToken(t TokenSource) string {
	switch {
	case t.Keychain != "":
		return "{ keychain = " + quote(t.Keychain) + " }"
	case t.Env != "":
		return "{ env = " + quote(t.Env) + " }"
	default:
		return "{ command = " + tomlArray(t.Command) + " }"
	}
}

func tomlArray(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, v := range values {
		quoted = append(quoted, quote(v))
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

func tomlKey(name string) string {
	for _, r := range name {
		bare := r == '-' || r == '_' ||
			(r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		if !bare {
			return quote(name)
		}
	}
	return name
}

func quote(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

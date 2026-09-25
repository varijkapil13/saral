package main

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/varijkapil13/saral/internal/config"
	"github.com/varijkapil13/saral/internal/ui/kernel"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if got != string(want) {
		t.Errorf("%s differs from the golden file (run with -update if the change is meant):\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

func isolated(t *testing.T) (cfgDir, cacheDir string) {
	t.Helper()
	cfgDir, cacheDir = t.TempDir(), t.TempDir()
	t.Setenv("SARAL_CONFIG_DIR", cfgDir)
	t.Setenv("SARAL_CACHE_DIR", cacheDir)
	for _, name := range []string{envProfile, envSite, envEmail, envToken} {
		t.Setenv(name, "")
	}
	return cfgDir, cacheDir
}

func TestRun_HelpAndTheHelpCommandPrintTheSame(t *testing.T) {
	cfgDir, cacheDir := isolated(t)

	outputs := make([]string, 0, 3)
	for _, args := range [][]string{{"--help"}, {"-h"}, {"help"}} {
		var out, errOut bytes.Buffer
		if err := run(args, &out, &errOut); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if errOut.Len() > 0 {
			t.Errorf("%v wrote to stderr: %q", args, errOut.String())
		}
		outputs = append(outputs, out.String())
	}
	for i := 1; i < len(outputs); i++ {
		if outputs[i] != outputs[0] {
			t.Errorf("help output %d differs from --help", i)
		}
	}
	got := strings.NewReplacer(cfgDir, "$SARAL_CONFIG_DIR", cacheDir, "$SARAL_CACHE_DIR").Replace(outputs[0])
	golden(t, "help", got)
}

func TestHelp_SaysWhatANewUserNeeds(t *testing.T) {
	isolated(t)
	var out bytes.Buffer
	if err := run([]string{"--help"}, &out, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	help := out.String()
	for _, want := range []string{
		"ctrl+k", "?", "SARAL_CONFIG_DIR", "SARAL_CACHE_DIR", "HTTPS_PROXY", "SARAL_TOKEN", "SARAL_SITE",
		"SARAL_EMAIL", "SARAL_PROFILE", "exit codes", "config.toml", "doctor", "saral PROJ-142",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("--help does not mention %q", want)
		}
	}
	for _, spec := range openableViews() {
		if !strings.Contains(help, "  "+spec.ID+" ") {
			t.Errorf("--help does not list the view %s", spec.ID)
		}
	}
	for flagName := range hiddenFlags {
		if strings.Contains(help, flagName) {
			t.Errorf("--help shows the hidden flag %s", flagName)
		}
	}
}

func TestOpenableViews_LeaveOutWhatOnlySomethingElseOpens(t *testing.T) {
	ids := openableViewIDs()
	if len(ids) < 3 {
		t.Fatalf("only %d openable views, so this proves nothing: %v", len(ids), ids)
	}
	for _, internal := range []string{kernel.SetupViewID, kernel.PaletteViewID, "comment", "form"} {
		if _, ok := kernel.LookupView(internal); !ok {
			t.Fatalf("%s is not registered, so leaving it out proves nothing", internal)
		}
		for _, id := range ids {
			if id == internal {
				t.Errorf("%s is listed as a view to open by name", internal)
			}
		}
	}
}

func TestRun_ExitCodes(t *testing.T) {
	tests := map[string]struct {
		args   []string
		config string
		code   int
		says   string
	}{
		"an unknown flag":             {args: []string{"--nope"}, code: exitUsage, says: "--help"},
		"a mistyped view":             {args: []string{"bord"}, code: exitUsage, says: `did you mean "board"`},
		"an unknown glyph tier":       {args: []string{"--glyphs", "fancy"}, code: exitUsage, says: "unicode, nerd, ascii"},
		"an unknown theme":            {args: []string{"--theme", "sepia"}, code: exitUsage, says: "sepia"},
		"an unknown scheme":           {args: []string{"--scheme", "neon"}, code: exitUsage, says: "nord"},
		"a poll with no unit":         {args: []string{"--poll", "5"}, code: exitUsage, says: "5s"},
		"a poll that is not a time":   {args: []string{"--poll", "often"}, code: exitUsage, says: "30s"},
		"two things to open":          {args: []string{"board", "backlog"}, code: exitUsage, says: "opens one thing"},
		"a scripting subcommand":      {args: []string{"issue", "view", "PROJ-1", "--json"}, code: exitUsage, says: "saral PROJ-1"},
		"a missing named profile":     {args: []string{"--profile", "nope", "--bench-first-paint"}, code: exitConfig, says: "nope"},
		"a config file that is wrong": {args: []string{"--bench-first-paint"}, config: "active = [", code: exitConfig},
		"a profile with no email": {
			args:   []string{"--bench-first-paint"},
			config: "[profiles.work]\nsite = \"example.atlassian.net\"\ntoken = { env = \"X\" }\n",
			code:   exitConfig, says: "email",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			cfgDir, _ := isolated(t)
			if tc.config != "" {
				if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(tc.config), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var out, errOut bytes.Buffer
			err := run(tc.args, &out, &errOut)
			if got := exitCodeOf(err); got != tc.code {
				t.Fatalf("saral %s exits %d (%v), want %d", strings.Join(tc.args, " "), got, err, tc.code)
			}
			if err != nil && tc.says != "" && !strings.Contains(err.Error()+errOut.String(), tc.says) {
				t.Errorf("saral %s said %q, want it to mention %q", strings.Join(tc.args, " "), err.Error()+errOut.String(), tc.says)
			}
			if out.Len() > 0 {
				t.Errorf("a failed run printed to stdout: %q", out.String())
			}
		})
	}
}

func TestExitCodeOf_IsOneForAnythingUncoded(t *testing.T) {
	if got := exitCodeOf(os.ErrPermission); got != exitOther {
		t.Errorf("an uncoded error exits %d, want %d", got, exitOther)
	}
	if got := exitCodeOf(withCode(exitAuth, withCode(exitConfig, os.ErrPermission))); got != exitConfig {
		t.Errorf("rewrapping replaced the inner code: %d", got)
	}
	if exitCodeOf(nil) != exitOK {
		t.Error("nil is not success")
	}
}

func TestArgument_ViewsByIDOrTitleAndTyposGetASuggestion(t *testing.T) {
	tests := map[string]struct {
		arg     string
		view    string
		suggest string
		none    bool
	}{
		"an ID":                        {arg: "board", view: "board"},
		"a title is an alias":          {arg: "issues", view: "list"},
		"a title in any case":          {arg: "Sprints", view: "sprints"},
		"a typo of an ID":              {arg: "timline", suggest: "timeline"},
		"a typo of a title":            {arg: "isues", suggest: "list"},
		"a word near nothing":          {arg: "xyzzyplugh", none: true},
		"the setup view is still open": {arg: kernel.SetupViewID, view: kernel.SetupViewID},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			opts, _, err := argument(tc.arg, "example.atlassian.net")
			if tc.view != "" {
				if err != nil {
					t.Fatalf("argument(%q): %v", tc.arg, err)
				}
				if got := kernel.InitialViewOf(opts...); got != tc.view {
					t.Errorf("saral %s opens %q, want %q", tc.arg, got, tc.view)
				}
				return
			}
			if err == nil {
				t.Fatalf("saral %s was accepted", tc.arg)
			}
			for _, internal := range []string{kernel.PaletteViewID, "comment", "form"} {
				if strings.Contains(err.Error(), internal+",") || strings.Contains(err.Error(), internal+")") {
					t.Errorf("the error %q offers %s, which cannot be opened by name", err, internal)
				}
			}
			switch {
			case tc.none && strings.Contains(err.Error(), "did you mean"):
				t.Errorf("the error %q suggests something for a word near nothing", err)
			case tc.suggest != "" && !strings.Contains(err.Error(), `did you mean "`+tc.suggest+`"`):
				t.Errorf("the error %q does not suggest %s", err, tc.suggest)
			}
		})
	}
}

func TestParseRoot_FlagsMayFollowTheArgument(t *testing.T) {
	var opt options
	positional, _, sub, err := parseRoot(rootFlags(&opt), []string{"board", "--project", "EX", "--theme", "dark"})
	if err != nil {
		t.Fatal(err)
	}
	if sub != nil || len(positional) != 1 || positional[0] != "board" {
		t.Errorf("positional %v, subcommand %v", positional, sub)
	}
	if opt.project != "EX" || opt.theme != "dark" {
		t.Errorf("the flags after the argument were dropped: %+v", opt)
	}
}

func TestParseRoot_StopsAtASubcommand(t *testing.T) {
	var opt options
	positional, rest, sub, err := parseRoot(rootFlags(&opt), []string{"--project", "EX", "doctor", "--profile", "work"})
	if err != nil {
		t.Fatal(err)
	}
	if sub == nil || sub.name != "doctor" {
		t.Fatalf("no subcommand found, positional %v", positional)
	}
	if strings.Join(rest, " ") != "--profile work" || opt.project != "EX" {
		t.Errorf("rest %v, project %q", rest, opt.project)
	}
}

func TestParseRoot_EverythingAfterTheTerminatorIsPositional(t *testing.T) {
	var opt options
	positional, _, sub, err := parseRoot(rootFlags(&opt), []string{"--", "--theme"})
	if err != nil || sub != nil {
		t.Fatalf("err %v, sub %v", err, sub)
	}
	if len(positional) != 1 || positional[0] != "--theme" {
		t.Errorf("positional %v", positional)
	}
}

func TestSubcommands_ShadowNoView(t *testing.T) {
	names := subcommandNames()
	if len(names) < 3 {
		t.Fatalf("only %v registered", names)
	}
	for _, name := range names {
		if id, ok := viewNamed(name); ok {
			t.Errorf("the subcommand %s hides the view %s", name, id)
		}
	}
}

func TestPollFlag(t *testing.T) {
	tests := map[string]struct {
		in   string
		want time.Duration
		hint string
	}{
		"a duration":      {in: "90s", want: 90 * time.Second},
		"minutes":         {in: "2m", want: 2 * time.Minute},
		"zero is off":     {in: "0", want: 0},
		"a bare number":   {in: "5", hint: "5s"},
		"a bare decimal":  {in: "1.5", hint: "1.5s"},
		"negative":        {in: "-5s", hint: "negative"},
		"not a duration":  {in: "soon", hint: "30s"},
		"padded is fine":  {in: " 30s ", want: 30 * time.Second},
		"an empty string": {in: "", hint: "30s"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var d time.Duration
			err := pollFlag{&d}.Set(tc.in)
			if tc.hint != "" {
				if err == nil || !strings.Contains(err.Error(), tc.hint) {
					t.Errorf("Set(%q) = %v, want an error mentioning %q", tc.in, err, tc.hint)
				}
				return
			}
			if err != nil || d != tc.want {
				t.Errorf("Set(%q) = %v, %v; want %v", tc.in, d, err, tc.want)
			}
		})
	}
}

func TestEditDistance(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{{"", "", 0}, {"board", "board", 0}, {"bord", "board", 1}, {"", "abc", 3}, {"kitten", "sitting", 3}, {"ünï", "uni", 2}} {
		if got := editDistance(tc.a, tc.b); got != tc.want {
			t.Errorf("editDistance(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestRun_ValidatesFlagsBeforeReadingTheConfig(t *testing.T) {
	cfgDir, _ := isolated(t)
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte("active = ["), 0o600); err != nil {
		t.Fatal(err)
	}
	err := run([]string{"--theme", "sepia"}, &bytes.Buffer{}, &bytes.Buffer{})
	if exitCodeOf(err) != exitUsage {
		t.Errorf("a bad flag over a bad config exits %d (%v), want the flag's usage error", exitCodeOf(err), err)
	}
	if _, err := config.Load(); err == nil {
		t.Fatal("the config was meant to be unreadable")
	}
}

package main

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func stubBrowser(t *testing.T, err error) *[]string {
	t.Helper()
	var opened []string
	was := openInBrowser
	openInBrowser = func(_ context.Context, link string) error {
		opened = append(opened, link)
		return err
	}
	t.Cleanup(func() { openInBrowser = was })
	return &opened
}

func TestOpen_PrintsTheLinkAndOpensIt(t *testing.T) {
	writeProfile(t)
	opened := stubBrowser(t, nil)
	out, err := script(t, nil, "open", "proj-7")
	if err != nil {
		t.Fatal(err)
	}
	want := "https://example.atlassian.net/browse/PROJ-7"
	if out != want+"\n" || !slices.Equal(*opened, []string{want}) {
		t.Errorf("printed %q and opened %v, want %s both times", out, *opened, want)
	}
}

func TestOpen_PrintOnlyStartsNothing(t *testing.T) {
	writeProfile(t)
	opened := stubBrowser(t, nil)
	out, err := script(t, nil, "open", "PROJ-7", "--print")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(out, "/browse/PROJ-7\n") || len(*opened) != 0 {
		t.Errorf("printed %q and opened %v", out, *opened)
	}
}

func TestOpen_NeedsNoToken(t *testing.T) {
	writeProfile(t)
	t.Setenv("SARAL_TEST_TOKEN", "")
	stubBrowser(t, nil)
	if _, err := script(t, nil, "open", "PROJ-7", "--print"); err != nil {
		t.Errorf("open asked for a token it has no use for: %v", err)
	}
}

func TestOpen_Refusals(t *testing.T) {
	t.Run("a link to another site", func(t *testing.T) {
		writeProfile(t)
		opened := stubBrowser(t, nil)
		_, err := script(t, nil, "open", "https://elsewhere.atlassian.net/browse/PROJ-7")
		wantCode(t, err, exitUsage, "elsewhere.atlassian.net")
		if len(*opened) != 0 {
			t.Errorf("opened %v", *opened)
		}
	})
	t.Run("no browser", func(t *testing.T) {
		writeProfile(t)
		stubBrowser(t, errors.New("xdg-open: not found"))
		_, err := script(t, nil, "open", "PROJ-7")
		wantCode(t, err, exitOther, "xdg-open")
	})
	t.Run("no profile", func(t *testing.T) {
		isolated(t)
		stubBrowser(t, nil)
		_, err := script(t, nil, "open", "PROJ-7")
		wantCode(t, err, exitConfig, envSite)
	})
}

func TestCompletion_Goldens(t *testing.T) {
	for _, shell := range completionShells {
		t.Run(shell, func(t *testing.T) {
			isolated(t)
			out, err := script(t, nil, "completion", shell)
			if err != nil {
				t.Fatal(err)
			}
			golden(t, "completion_"+shell, out)
		})
	}
}

func TestCompletion_OffersEveryCommandAndFlag(t *testing.T) {
	c := completionModel()
	if len(c.leaves) < 6 {
		t.Fatalf("only %d commands register flags, so this proves nothing", len(c.leaves))
	}
	var bash strings.Builder
	if err := c.bash(&bash); err != nil {
		t.Fatal(err)
	}
	for _, name := range subcommandNames() {
		if !strings.Contains(bash.String(), " "+name+" ") && !strings.Contains(bash.String(), "\""+name+" ") {
			t.Errorf("bash completion does not offer %s", name)
		}
	}
	for path, flags := range c.leaves {
		for _, f := range flags {
			if !strings.Contains(bash.String(), f.spelling()) {
				t.Errorf("bash completion does not offer %s on %s", f.spelling(), path)
			}
		}
	}
	for path := range c.leaves {
		cmd, sub, grouped := strings.Cut(path, " ")
		if _, ok := lookupSubcommand(cmd); !ok {
			t.Errorf("flags are registered for %s, which is not a command", path)
		}
		if grouped && !slices.Contains(scriptGroups[cmd], sub) {
			t.Errorf("flags are registered for %s, which %s does not take", path, cmd)
		}
	}
}

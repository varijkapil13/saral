package kernel

import (
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"path"
	"runtime"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// Copy puts text on the system clipboard and says what it put there.
//
// The write is an OSC 52 escape sequence out to the terminal, and it cannot be
// confirmed: a terminal or a multiplexer that does not allow the sequence drops
// it without a word, so there is no error to report and a copy that never
// happened is indistinguishable from one that did. Naming what was copied is the
// only thing that separates them — somebody who reads "copied PROJ-142", pastes,
// and gets nothing knows to look at their terminal rather than at Saral. That is
// why saying so is this function's job rather than the caller's to remember.
func Copy(text, what string) tea.Cmd {
	if text == "" {
		return Warn("there is nothing here to copy")
	}
	if what == "" {
		what = text
	}
	return tea.Batch(tea.SetClipboard(text), Status("copied "+what))
}

// OpenURL hands a link to whatever this desktop opens links with, and names the
// link either way. The handler is started rather than waited on, so a browser
// that takes two seconds to appear does not hold a frame, and a platform with no
// handler Saral knows says the address instead of failing quietly.
func OpenURL(link string) tea.Cmd {
	name, args, known := opener(runtime.GOOS, link)
	if !known {
		return Warn("Saral does not know how to open a link on " + runtime.GOOS + "; the address is " + link)
	}
	return func() tea.Msg {
		cmd := exec.Command(name, args...)
		if err := cmd.Start(); err != nil {
			return StatusMsg{Text: "could not open " + link + ": " + err.Error(), Level: LevelError}
		}
		// Nothing here waits on a browser, but something has to reap it.
		go func() { _ = cmd.Wait() }()
		return StatusMsg{Text: "opened " + link}
	}
}

func opener(goos, link string) (name string, args []string, known bool) {
	switch goos {
	case "darwin":
		return "open", []string{link}, true
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", link}, true
	case "linux", "freebsd", "netbsd", "openbsd", "dragonfly":
		return "xdg-open", []string{link}, true
	default:
		return "", nil, false
	}
}

// IssueURL is where an issue lives on the site this session is talking to.
//
// Deps.Site is whatever a profile was written with and nothing past onboarding
// checks its shape, so this copes with a scheme already on the front, a port, the
// context path a Data Center install can be served under, and a trailing slash.
// It refuses rather than guesses: a link built out of a site that is not one is a
// browser tab full of somebody else's website.
func IssueURL(site, key string) (string, error) {
	key = strings.TrimSpace(key)
	switch {
	case key == "":
		return "", errors.New("there is no issue here to open")
	case strings.ContainsAny(key, "/?#%\\ \t"):
		return "", fmt.Errorf("%q is not an issue key", key)
	}
	base, err := siteURL(site)
	if err != nil {
		return "", err
	}
	base.Path = path.Join(base.Path, "browse", key)
	return base.String(), nil
}

// siteURL turns whatever a profile calls the site into something a browser can
// open. A bare host is the common case and gets https; anything that names no
// host, or a scheme a browser would not follow, is refused.
func siteURL(site string) (*url.URL, error) {
	site = strings.TrimSpace(site)
	if site == "" {
		return nil, errors.New("this session was not told which site it is talking to")
	}
	if scheme, _, absolute := strings.Cut(site, "://"); absolute {
		if scheme != "http" && scheme != "https" {
			return nil, fmt.Errorf("%q is not a site a browser would open", site)
		}
	} else {
		// No //, so what is left is a host with an optional port and path. A colon
		// followed by anything but a digit is an opaque URI scheme rather than a
		// port, and prefixing https to one of those builds a link to nowhere.
		if _, after, colon := strings.Cut(site, ":"); colon && !startsWithDigit(after) {
			return nil, fmt.Errorf("%q is not a site a browser would open", site)
		}
		site = "https://" + site
	}
	u, err := url.Parse(site)
	if err != nil {
		return nil, fmt.Errorf("%q is not a site a link can be built from: %w", site, err)
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("%q names no host", site)
	}
	u.User, u.RawQuery, u.Fragment = nil, "", ""
	return u, nil
}

func startsWithDigit(s string) bool { return s != "" && s[0] >= '0' && s[0] <= '9' }

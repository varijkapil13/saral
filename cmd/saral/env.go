package main

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/varijkapil13/saral/internal/config"
)

// Flags beat these, and these beat config.toml.
const (
	envProfile = "SARAL_PROFILE"
	envSite    = "SARAL_SITE"
	envEmail   = "SARAL_EMAIL"
	envToken   = "SARAL_TOKEN"
)

const envProfileName = "environment"

func getenv(name string) string { return strings.TrimSpace(os.Getenv(name)) }

// SARAL_TOKEN becomes a token source naming itself, so its value never reaches a
// Profile. fromEnv reports a profile the file does not hold.
func resolveProfile(cfg config.Config, flagName string) (profile config.Profile, fromEnv bool, err error) {
	name := cmp.Or(strings.TrimSpace(flagName), getenv(envProfile))
	site, email, token := getenv(envSite), getenv(envEmail), os.Getenv(envToken) != ""

	profile, err = profileFor(cfg, name)
	switch {
	case err == nil:
	case name == "" && site != "":
		profile, fromEnv = config.Profile{Name: envProfileName}, true
	default:
		return config.Profile{}, false, err
	}

	if site != "" {
		normal, serr := config.NormalizeSite(site)
		if serr != nil {
			return config.Profile{}, false, fmt.Errorf("%s: %w", envSite, serr)
		}
		profile.Site = normal
	}
	if email != "" {
		profile.Email = email
	}
	if token || fromEnv {
		profile.Token = config.TokenSource{Env: envToken}
	}
	if fromEnv {
		if err := profile.Validate(); err != nil {
			return config.Profile{}, false, fmt.Errorf("the profile from %s, %s and %s: %w", envSite, envEmail, envToken, err)
		}
	}
	return profile, fromEnv, nil
}

var errEnvProfile = errors.New("this session's profile comes from the environment, so there is no file to save it in")

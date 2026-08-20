package onboarding

import (
	"errors"
	"fmt"
	"strings"

	"github.com/varijkapil13/saral/internal/config"
)

type step int

const (
	stepSite step = iota
	stepEmail
	stepToken
	stepStorage
	stepProject
	stepReview
	stepDone
	stepCount
)

// steps is the number the header counts against; the summary is where the flow
// ends rather than a step the user works through.
const steps = int(stepCount) - 1

func (s step) String() string {
	switch s {
	case stepSite:
		return "site"
	case stepEmail:
		return "email"
	case stepToken:
		return "token"
	case stepStorage:
		return "store"
	case stepProject:
		return "project"
	case stepReview:
		return "review"
	default:
		return "done"
	}
}

// title is the label on the rail down the left.
func (s step) title() string {
	switch s {
	case stepSite:
		return "Jira site"
	case stepEmail:
		return "Account email"
	case stepToken:
		return "API token"
	case stepStorage:
		return "Token store"
	case stepProject:
		return "Project"
	case stepReview:
		return "Review"
	default:
		return "Done"
	}
}

func (s step) prompt() string {
	switch s {
	case stepSite:
		return "Which Jira Cloud site?"
	case stepEmail:
		return "Which account is the API token for?"
	case stepToken:
		return "Paste the API token"
	case stepStorage:
		return "Where should the token live?"
	case stepProject:
		return "Which project should Saral open in?"
	case stepReview:
		return "This is what will be written."
	default:
		return "The profile is written."
	}
}

func (s step) field() field {
	switch s {
	case stepSite:
		return fieldSite
	case stepEmail:
		return fieldEmail
	case stepToken:
		return fieldToken
	case stepStorage:
		return fieldSecret
	case stepProject:
		return fieldProject
	default:
		return fieldNone
	}
}

type field int

const (
	fieldSite field = iota
	fieldEmail
	fieldToken
	fieldSecret
	fieldProject
	fieldCount
	fieldNone field = -1
)

func (f field) placeholder() string {
	switch f {
	case fieldSite:
		return "example.atlassian.net"
	case fieldEmail:
		return "you@example.com"
	case fieldProject:
		return "PROJ"
	case fieldToken, fieldSecret, fieldCount, fieldNone:
	}
	return ""
}

type storeKind int

const (
	storeKeychain storeKind = iota
	storeEnv
	storeCommand
	storeCount
)

func (k storeKind) String() string {
	switch k {
	case storeKeychain:
		return "keychain"
	case storeEnv:
		return "environment"
	default:
		return "command"
	}
}

func (k storeKind) title() string {
	switch k {
	case storeKeychain:
		return "OS keychain"
	case storeEnv:
		return "Environment variable"
	default:
		return "Command"
	}
}

// explain says what choosing this store actually does, because it is the only
// moment anyone thinks about where a secret lives.
func (k storeKind) explain() string {
	switch k {
	case storeKeychain:
		return "Saral writes the token into the keychain now and the config file only names the entry. " +
			"This is the one place Saral can write a secret to."
	case storeEnv:
		return "Saral writes nothing. Each start it reads the variable, so it has to be exported " +
			"where Saral runs — a shell profile, a systemd unit, a container environment."
	default:
		return "Saral writes nothing. Each start it runs the command and reads the first line of its " +
			"output. The command is never handed to a shell, so it is an argument list and not a pipeline."
	}
}

func (k storeKind) label() string {
	switch k {
	case storeKeychain:
		return "Keychain entry"
	case storeEnv:
		return "Variable name"
	default:
		return "Command"
	}
}

// shellMeta is refused in a token command for the same reason internal/config
// refuses it: the argv is never handed to a shell, so a pipeline typed here
// would silently become literal arguments.
const shellMeta = "|&;<>()$`\\\"'*?[]{}~!#\n\r"

func (m Model) tokenSource() (config.TokenSource, error) {
	value := strings.TrimSpace(m.input[fieldSecret].Value())
	switch m.store {
	case storeKeychain:
		if value == "" {
			return config.TokenSource{}, errors.New(`name the keychain entry, as service:account — for example saral:` + m.profileName())
		}
		return config.TokenSource{Keychain: value}, nil
	case storeEnv:
		if value == "" || strings.ContainsAny(value, " =") {
			return config.TokenSource{}, errors.New("name the environment variable, for example JIRA_TOKEN")
		}
		return config.TokenSource{Env: value}, nil
	default:
		if i := strings.IndexAny(value, shellMeta); i >= 0 {
			return config.TokenSource{}, fmt.Errorf("%q cannot be in the command: it is never run through a shell, so write the program and its arguments — for example: sh -lc \"pass jira | head -1\"", value[i:i+1])
		}
		argv := strings.Fields(value)
		if len(argv) == 0 {
			return config.TokenSource{}, errors.New("name the command that prints the token, for example: pass show jira")
		}
		return config.TokenSource{Command: argv}, nil
	}
}

// profile is what will be written: everything collected so far, validated by
// internal/config rather than by a second copy of its rules here.
func (m Model) profile() (config.Profile, error) {
	source, err := m.tokenSource()
	if err != nil {
		return config.Profile{}, err
	}
	p := config.Profile{
		Name:    m.profileName(),
		Site:    m.value(fieldSite),
		Email:   m.value(fieldEmail),
		Project: m.value(fieldProject),
		Token:   source,
	}
	return p, p.Validate()
}

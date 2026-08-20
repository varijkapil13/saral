package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/zalando/go-keyring"
)

const (
	defaultCommandTimeout = 15 * time.Second
	commandWaitDelay      = 2 * time.Second

	shellMetacharacters = "|&;<>()$`\\\"'*?[]{}~!#\n\r"
)

// Resolver produces the API token for one profile.
type Resolver interface {
	Resolve(ctx context.Context) (string, error)
}

// ResolveToken fetches the profile's API token from wherever it lives.
func (p Profile) ResolveToken(ctx context.Context) (string, error) {
	r, err := p.Resolver()
	if err != nil {
		return "", err
	}
	return r.Resolve(ctx)
}

// Resolver builds the token resolver the profile asks for, without running it.
func (p Profile) Resolver() (Resolver, error) {
	if n := p.Token.count(); n != 1 {
		return nil, tokenSourceErr(p.Name, countPhrase(n))
	}
	switch {
	case p.Token.Keychain != "":
		service, account, ok := strings.Cut(p.Token.Keychain, ":")
		if !ok || strings.TrimSpace(account) == "" {
			service, account = p.Token.Keychain, p.Email
		}
		if strings.TrimSpace(account) == "" {
			return nil, fmt.Errorf("profile %q: keychain entry %q names no account and the profile has no email",
				p.Name, p.Token.Keychain)
		}
		return keychainResolver{profile: p.Name, service: service, account: account}, nil
	case p.Token.Env != "":
		return envResolver{profile: p.Name, variable: p.Token.Env}, nil
	default:
		if err := validateArgv(p.Name, p.Token.Command); err != nil {
			return nil, err
		}
		return commandResolver{
			profile: p.Name,
			argv:    slices.Clone(p.Token.Command),
			timeout: defaultCommandTimeout,
		}, nil
	}
}

// StoreToken writes the token into the OS keychain the profile points at. It
// is the only supported way to put a token on disk.
func (p Profile) StoreToken(token string) error {
	r, err := p.Resolver()
	if err != nil {
		return err
	}
	kc, ok := r.(keychainResolver)
	if !ok {
		return fmt.Errorf("profile %q: the token comes from the %s, which Saral does not write to", p.Name, p.Token)
	}
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("profile %q: refusing to store an empty token", p.Name)
	}
	if err := keyring.Set(kc.service, kc.account, token); err != nil {
		return fmt.Errorf("profile %q: storing the token in keychain entry %s/%s: %w",
			p.Name, kc.service, kc.account, err)
	}
	return nil
}

type keychainResolver struct {
	profile string
	service string
	account string
}

func (r keychainResolver) Resolve(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	token, err := keyring.Get(r.service, r.account)
	switch {
	case errors.Is(err, keyring.ErrNotFound):
		return "", fmt.Errorf("profile %q: keychain entry %s/%s does not exist; create it with: %s",
			r.profile, r.service, r.account, keychainHint(r.service, r.account))
	case err != nil:
		return "", fmt.Errorf("profile %q: reading keychain entry %s/%s: %w", r.profile, r.service, r.account, err)
	case strings.TrimSpace(token) == "":
		return "", fmt.Errorf("profile %q: keychain entry %s/%s is empty", r.profile, r.service, r.account)
	}
	return token, nil
}

func keychainHint(service, account string) string {
	switch runtime.GOOS {
	case "darwin":
		return fmt.Sprintf("security add-generic-password -s %q -a %q -w", service, account)
	case "windows":
		return fmt.Sprintf("cmdkey /generic:%s /user:%s /pass", service, account)
	default:
		return fmt.Sprintf("secret-tool store --label=%q service %s username %s", service, service, account)
	}
}

type envResolver struct {
	profile  string
	variable string
}

func (r envResolver) Resolve(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	token := os.Getenv(r.variable)
	if strings.TrimSpace(token) == "" {
		return "", fmt.Errorf("profile %q: environment variable %s is empty or not set", r.profile, r.variable)
	}
	return token, nil
}

type commandResolver struct {
	profile string
	argv    []string
	timeout time.Duration
}

func (r commandResolver) Resolve(ctx context.Context) (string, error) {
	if err := validateArgv(r.profile, r.argv); err != nil {
		return "", err
	}
	timeout := r.timeout
	if timeout <= 0 {
		timeout = defaultCommandTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, r.argv[0], r.argv[1:]...)
	cmd.Stdin = nil
	cmd.WaitDelay = commandWaitDelay
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	switch err := cmd.Run(); {
	case err == nil:
	case ctx.Err() != nil:
		return "", fmt.Errorf("profile %q: token command %s: %w", r.profile, r, ctx.Err())
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		return "", fmt.Errorf("profile %q: token command %s did not finish within %s", r.profile, r, timeout)
	case errors.Is(err, exec.ErrNotFound):
		return "", fmt.Errorf("profile %q: token command %q is not on PATH", r.profile, r.argv[0])
	default:
		return "", fmt.Errorf("profile %q: token command %s failed: %w%s", r.profile, r, err, detail(&stderr))
	}

	token := strings.TrimRight(stdout.String(), "\r\n")
	if strings.TrimSpace(token) == "" {
		return "", fmt.Errorf("profile %q: token command %s printed nothing%s", r.profile, r, detail(&stderr))
	}
	return token, nil
}

// String is what the error messages print, so it must stay argv and nothing
// the command produced.
func (r commandResolver) String() string { return strings.Join(r.argv, " ") }

func detail(stderr *bytes.Buffer) string {
	const limit = 512
	s := strings.TrimSpace(stderr.String())
	if s == "" {
		return ""
	}
	if len(s) > limit {
		s = strings.ToValidUTF8(s[:limit], "") + "…"
	}
	return ": " + strings.Join(strings.Fields(s), " ")
}

// splitCommand accepts the plain-string sugar for token.command. The argv is
// never handed to a shell, so anything a shell would have interpreted is
// refused rather than silently passed through as a literal argument.
func splitCommand(raw string) ([]string, error) {
	if i := strings.IndexAny(raw, shellMetacharacters); i >= 0 {
		return nil, fmt.Errorf(`contains %q, and the command is never run through a shell; `+
			`write it as an array instead, e.g. ["sh", "-lc", "pass jira | head -1"]`, raw[i:i+1])
	}
	return strings.Fields(raw), nil
}

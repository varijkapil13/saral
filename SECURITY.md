# Security

## Reporting a vulnerability

Please report security problems privately through
[GitHub's private vulnerability reporting](https://github.com/varijkapil13/saral/security/advisories/new),
not in a public issue. If that form is not available, open an issue that says only that you have
something to report privately, with no details, and you will be given a private way to send it.

Include the version (`saral version`), what you did, and what happened. Never include a real API
token, account ID or anything from your Jira site; the in-memory demo site
(`go run -tags demo ./cmd/saral -fake`) is usually enough to show a problem.

You should get an answer within a week. Fixes go into the next release, and the release notes say
what was fixed once a fixed version is available.

## Supported versions

Only the latest release gets security fixes.

## What Saral does to keep your data safe

- **Tokens are never written to disk by Saral.** `config.toml` says where the token lives (the system
  keychain, an environment variable or a command), never the token itself, and a config key that
  looks like a secret is refused.
- **Only your Jira site is contacted.** There is no telemetry. Plain `http://` is refused except on
  loopback, and a download redirected off `https` is refused.
- **Text from Jira is made safe for the terminal.** Escape sequences, control characters and bidi
  overrides in summaries, names and comments are stripped before they are drawn, so an issue cannot
  repaint or reorder your screen.
- **Logs and reports are redacted.** `saral --log` and `saral doctor` leave out tokens, cookies,
  query strings and request bodies, and shorten email addresses.
- **Releases are signed.** Each release's `checksums.txt` is signed with cosign keyless signing and
  has a GitHub build provenance attestation; `scripts/install.sh` checks the checksum always and the
  signature whenever `cosign` or `gh` is installed. See [`docs/INSTALL.md`](docs/INSTALL.md).
- **Dependencies are watched.** CI runs `govulncheck`, and Dependabot proposes updates weekly.

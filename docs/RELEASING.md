# Releasing Saral

Pushing a tag matching `v*` runs `.github/workflows/release.yml`, which runs GoReleaser against
`.goreleaser.yaml`. That produces four archives, a `checksums.txt`, a GitHub release with a generated
changelog, and — if there is a tap to push to — a Homebrew cask.

## The thing to understand first

GoReleaser publishes the GitHub release **before** it touches the Homebrew tap, because the cask needs
the release's download URLs to exist. It has no rollback. So anything that fails after the release
pipe has run leaves a published release, a red workflow, and an install route the README advertises
but nobody can use.

Two things keep that from happening:

- **The workflow's preflight.** Before GoReleaser runs at all, the workflow validates the config and,
  if `HOMEBREW_TAP_TOKEN` is set, checks the token can actually reach the tap. A wrong repository
  name, a token that was never granted the tap, or an expired token fails there — with nothing
  published. It checks reachability, not write permission; the API has no way to prove the latter
  without writing.
- **The cask gates itself on the secret.** `skip_upload` in `.goreleaser.yaml` reads
  `HOMEBREW_TAP_TOKEN` and resolves to `true` when it is empty. The lookup is `index .Env "..."`
  rather than `.Env.HOMEBREW_TAP_TOKEN` so that an *absent* variable is empty rather than a template
  error — Actions substitutes an empty string for a missing secret, but a local `goreleaser release`
  would not set the variable at all, and both must behave the same.

So a tag pushed with no tap and no secret publishes the GitHub release, generates the cask into
`dist/homebrew/Casks/saral.rb` without uploading it, and the workflow is green. The Actions log says
which of the two it did.

Re-running a tag's workflow is a retry, not a conflict: `release.replace_existing_artifacts` lets the
same assets be uploaded again, and the build is reproducible — two builds of the same commit produce
byte-identical binaries and archives, because of `-trimpath`, `CGO_ENABLED=0` and
`mod_timestamp: {{ .CommitTimestamp }}`. That is the recovery path if a tag goes out before the tap
exists: set the tap up, re-run the workflow, and the cask lands against the release that is already
there.

## Setting up the Homebrew tap

Do this before the first tag if you want `brew install` to work from day one. Everything here is
one-time.

1. **Create the tap repository.** It must be public — Homebrew cannot install from a private tap
   without extra configuration — and it must be named exactly `homebrew-tap`, because that is what
   makes `varijkapil13/tap/saral` resolve.

   ```sh
   gh repo create varijkapil13/homebrew-tap --public \
     --description "Homebrew tap for varijkapil13" --add-readme
   ```

   `--add-readme` matters: it gives the repository a commit and a default branch for GoReleaser to
   push onto. GoReleaser creates `Casks/saral.rb` itself; do not create it by hand.

2. **Create a token that can write to the tap.** A fine-grained personal access token, scoped as
   narrowly as this:

   - Resource owner: `varijkapil13`
   - Repository access: **only** `varijkapil13/homebrew-tap`
   - Repository permissions: **Contents → Read and write**. Nothing else.
   - An expiry you will actually notice. When it lapses, releases fail at the preflight with nothing
     published, which is the safe failure but still a failure.

   A classic token with `public_repo` also works and is a worse idea: it can write to every public
   repository you own.

3. **Put it on the `saral` repository as `HOMEBREW_TAP_TOKEN`** — on `saral`, not on the tap. The
   release workflow runs in `saral` and reads the secret from there.

   ```sh
   gh secret set HOMEBREW_TAP_TOKEN --repo varijkapil13/saral
   ```

4. **Confirm both exist before tagging.**

   ```sh
   gh repo view varijkapil13/homebrew-tap --json name,visibility
   gh secret list --repo varijkapil13/saral | grep HOMEBREW_TAP_TOKEN
   ```

## Cutting a release

```sh
git switch main && git pull
make check                      # tidy, lint, race
goreleaser check                # the release config is valid
goreleaser release --snapshot --clean --skip=publish   # a full dry run into dist/

git tag -a v0.1.0 -m 'v0.1.0'
git push origin v0.1.0
gh run watch
```

Then check the three things a release is for:

```sh
gh release view v0.1.0                                  # five assets and a changelog
brew install varijkapil13/tap/saral && saral version     # macOS
curl -fsSL https://raw.githubusercontent.com/varijkapil13/saral/main/scripts/install.sh | sh
```

The tag is what stamps the version: `saral version` reports it from `-X main.version`, so a binary
built any other way says `dev`.

## The size budget

`docs/PERFORMANCE.md` caps the stripped binary at 15 MiB, and `ci.yml` checks one target. The release
build reports all four, because `report_sizes` is on:

| Target | Binary | Archive |
|---|---|---|
| linux/amd64 | 13.26 MiB | 5.16 MiB |
| darwin/amd64 | 13.20 MiB | 5.11 MiB |
| darwin/arm64 | 12.15 MiB | 4.75 MiB |
| linux/arm64 | 12.13 MiB | 4.69 MiB |

The amd64 builds are about a megabyte larger than the arm64 ones, and linux/amd64 — the one `ci.yml`
happens to measure, so the budget is guarded at its worst case — sits at 88% of the ceiling with
1.74 MiB of headroom. That is not much. A single sizeable new dependency would spend it.

## Things worth knowing about the config

- **The cask is a cask, not a formula.** GoReleaser deprecated `brews` in favour of `homebrew_casks`;
  `goreleaser check` fails on the old key. Casks are the right shape for a prebuilt binary, and they
  cost one thing: Homebrew does not support casks on Linux, so Linux users get the install script or
  `go install`. The generated cask still carries `on_linux` URL blocks, which Homebrew on Linux will
  never reach.
- **A cask has no `test do` block.** The formula shape had one, so `brew test saral` could smoke-test
  the install. Casks have no equivalent, which is why the verification above is done by hand.
- **`force_token: github`.** GoReleaser picks its SCM from whichever token it finds in the
  environment. A `GITLAB_TOKEN` sitting in a developer's shell is enough to rewrite every URL in the
  generated cask to `gitlab.com` — which is exactly what a local dry run did before this was pinned.
- **The binaries are unsigned.** The cask's `postflight` clears the quarantine attribute, which is why
  a `brew install`ed binary runs on macOS. Signing and notarizing needs an Apple Developer account
  and is not set up.
- **`repository.token` takes only the bare `{{ .Env.VAR_NAME }}` form.** `homebrew_casks[].repository`
  is a `repository` block, and GoReleaser refuses any other templating on its `token` — `{{ index .Env
  "HOMEBREW_TAP_TOKEN" }}` fails at publish time with "expected `{{ .Env.VAR_NAME }}` only", which
  `goreleaser check` and a `--skip=publish` dry run both pass, because neither resolves `token` at all.
  `v0.2.0` shipped its four archives and no cask over exactly this: the release step has already
  published by the time this error surfaces, and GoReleaser does not rewind it — see "The thing to
  understand first" above. `skip_upload` is a different field with no such restriction and keeps its
  `index .Env` form, which is there on purpose so an absent variable reads as empty rather than as a
  template error.

# Installing Saral

Saral is a single static binary with no runtime dependencies. Pick whichever of these suits you;
they all end up with the same executable.

## Homebrew (macOS)

```sh
brew install varijkapil13/tap/saral
```

Saral ships as a **cask**, not a formula, because the tap distributes the binary GoReleaser built
rather than compiling from source. Homebrew does not support casks on Linux, so on Linux use the
install script or `go install` below. If Homebrew ever asks you to disambiguate, `brew install
--cask varijkapil13/tap/saral` is the explicit form.

Upgrade with `brew upgrade saral`. Remove it with `brew uninstall saral`, or
`brew uninstall --zap saral` to take Saral's config and cache with it.

## The install script (macOS and Linux)

```sh
curl -fsSL https://raw.githubusercontent.com/varijkapil13/saral/main/scripts/install.sh | sh
```

It works out your OS and architecture, downloads that archive from the latest release, **checks it
against the release's `checksums.txt` before unpacking anything**, and moves the binary into place in
one step. A failed download, a checksum that does not match, or a directory it cannot write to all
stop it before anything is installed; it never leaves a partial binary behind.

Where it installs, in order of preference:

1. `$SARAL_INSTALL_DIR`, if you set it
2. `/usr/local/bin`, if that exists and you can write to it
3. `~/.local/bin`, created if needed

It never invokes `sudo` for you. If the directory it picked is not on your `PATH` it says so.

Three knobs, as environment variables or flags:

| Variable | Flag | Default |
|---|---|---|
| `SARAL_VERSION` | `--version` | the latest release |
| `SARAL_INSTALL_DIR` | `--install-dir` | as above |
| `SARAL_DOWNLOAD_BASE` | — | the GitHub release for that version |

To pass a flag through a pipe, `sh` needs `-s --`:

```sh
curl -fsSL .../install.sh | sh -s -- --version v0.1.0 --install-dir ~/bin
```

`SARAL_DOWNLOAD_BASE` points the download somewhere other than GitHub — a mirror, or a directory of
release artifacts on disk (`file:///path/to/dist`), which is how `scripts/install_test.sh` exercises
the script without a network.

If you would rather read the script before running it, that is the whole point of `scripts/install.sh`
being in the repository: read it, then run the copy you read.

## From source

```sh
go install github.com/varijkapil13/saral/cmd/saral@latest
```

This is the only route on a platform the releases do not cover, and the one that works if you want to
build from a branch. `saral version` will report `dev` rather than a release version, because the
version, commit and date are stamped in by the release build's linker flags.

## By hand

Every release has four archives and a `checksums.txt`:

```
saral_<version>_darwin_amd64.tar.gz
saral_<version>_darwin_arm64.tar.gz
saral_<version>_linux_amd64.tar.gz
saral_<version>_linux_arm64.tar.gz
```

Each contains the `saral` binary, `README.md`, `LICENSE` and the `docs/` directory. Verify before you
unpack:

```sh
grep " saral_0.1.0_darwin_arm64.tar.gz$" checksums.txt | shasum -a 256 -c -
tar -xzf saral_0.1.0_darwin_arm64.tar.gz saral
install -m 0755 saral /usr/local/bin/saral
```

## Supported platforms

macOS and Linux, on x86-64 and arm64. That is what the release builds cover and what the install
script will accept; anything else needs `go install`.

A shell running under Rosetta on an Apple Silicon Mac reports `x86_64`. The install script notices and
fetches the arm64 build anyway, so you do not silently end up with a translated binary.

## macOS and Gatekeeper

The binaries are not code-signed or notarized. That matters in exactly one case: a file downloaded by
a **browser** is marked quarantined, and macOS then refuses to run it. Clear the mark yourself:

```sh
xattr -d com.apple.quarantine ./saral
```

`curl`, `wget` and the install script do not set that mark, and the Homebrew cask clears it on
install, so this only comes up if you clicked a link in a browser to get the archive.

## Checking what you installed

```sh
saral version
```

prints the release version, the commit it was built from and the build date — all three stamped in at
link time, so they describe the binary in your hand rather than whatever the repository says today.

## Where Saral keeps its files

| What | Where | Override |
|---|---|---|
| `config.toml` | `~/.config/saral` | `SARAL_CONFIG_DIR`, else `XDG_CONFIG_HOME` |
| the issue cache | `~/.cache/saral` | `SARAL_CACHE_DIR`, else `XDG_CACHE_HOME` |

Both overrides must be absolute paths. Uninstalling never touches either unless you ask for it — see
`brew uninstall --zap` above, or delete the two directories.

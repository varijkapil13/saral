#!/bin/sh
# Install a released saral binary. See docs/INSTALL.md.
#
#   curl -fsSL https://raw.githubusercontent.com/varijkapil13/saral/main/scripts/install.sh | sh
#
#   SARAL_VERSION=v0.1.0        # default: the latest release
#   SARAL_INSTALL_DIR=~/bin     # default: /usr/local/bin if writable, else ~/.local/bin
#   SARAL_DOWNLOAD_BASE=...     # default: the GitHub release for SARAL_VERSION
set -eu

REPO=varijkapil13/saral
BIN=saral
SUPPORTED='darwin and linux, on amd64 and arm64'

version=${SARAL_VERSION:-}
install_dir=${SARAL_INSTALL_DIR:-}
download_base=${SARAL_DOWNLOAD_BASE:-}

say() { printf '%s\n' "$*"; }
warn() { printf '%s\n' "$BIN: $*" >&2; }
die() { printf '%s\n' "$BIN: $*" >&2; exit 1; }

usage() {
	cat <<EOF
usage: install.sh [--version TAG] [--install-dir DIR]

Downloads the released $BIN archive for this machine, verifies it against the
release's checksums.txt, and installs the binary. Supported: $SUPPORTED.
EOF
}

need_value() { [ "$#" -ge 2 ] || die "$1 needs a value"; }

while [ "$#" -gt 0 ]; do
	case $1 in
	-v | --version)
		need_value "$@"
		version=$2
		shift 2
		;;
	-d | --install-dir)
		need_value "$@"
		install_dir=$2
		shift 2
		;;
	-h | --help)
		usage
		exit 0
		;;
	*) die "unknown option: $1 (try --help)" ;;
	esac
done

if command -v curl >/dev/null 2>&1; then
	downloader=curl
elif command -v wget >/dev/null 2>&1; then
	downloader=wget
else
	die 'neither curl nor wget is installed'
fi

fetch() {
	case $downloader in
	curl) curl -fsSL --retry 3 --retry-connrefused -o "$2" "$1" ;;
	wget) wget -q --tries=3 -O "$2" "$1" ;;
	esac
}

latest_tag() {
	case $downloader in
	# The redirect from /releases/latest costs no API quota; the JSON endpoint does.
	curl) curl -fsSL --retry 3 -o /dev/null -w '%{url_effective}' \
		"https://github.com/$REPO/releases/latest" | sed 's|.*/||' ;;
	wget) wget -qO- "https://api.github.com/repos/$REPO/releases/latest" |
		sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1 ;;
	esac
}

sha256_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d' ' -f1
	elif command -v openssl >/dev/null 2>&1; then
		openssl dgst -sha256 "$1" | sed 's/.*= *//'
	else
		die 'no sha256 tool found (sha256sum, shasum or openssl); refusing to install unverified'
	fi
}

# verify_signature checks the release beyond the sha256 the checksum step above
# already did: that checksums.txt itself was produced by this repository's
# release workflow, via whichever of cosign or gh it finds. Neither is required
# to install, but a failure once one of them ran is real, not a fallback case.
verify_signature() {
	checksums=$1
	if command -v cosign >/dev/null 2>&1; then
		if fetch "$download_base/checksums.txt.sig" "$tmp/checksums.txt.sig" 2>/dev/null &&
			fetch "$download_base/checksums.txt.pem" "$tmp/checksums.txt.pem" 2>/dev/null; then
			cosign verify-blob \
				--certificate "$tmp/checksums.txt.pem" \
				--signature "$tmp/checksums.txt.sig" \
				--certificate-identity-regexp "^https://github.com/$REPO/" \
				--certificate-oidc-issuer https://token.actions.githubusercontent.com \
				"$checksums" >/dev/null 2>&1 ||
				die "cosign could not verify checksums.txt's signature for $version. Nothing was installed."
			say "$BIN: checksums.txt signature verified with cosign"
			return
		fi
		warn 'cosign is installed, but this release has no checksums.txt.sig/.pem to verify against'
	fi
	if command -v gh >/dev/null 2>&1; then
		if gh attestation verify "$tmp/$archive" --repo "$REPO" >/dev/null 2>&1; then
			say "$BIN: build provenance verified with gh attestation"
		else
			warn "gh attestation verify found no provenance to confirm $archive against; continuing on the checksum alone"
		fi
		return
	fi
	warn 'neither cosign nor gh (attestation verify) is installed; only the sha256 checksum was checked, not the signature or provenance'
}

case $(uname -s) in
Darwin) os=darwin ;;
Linux) os=linux ;;
*) os=$(uname -s) ;;
esac

case $(uname -m) in
x86_64 | amd64) arch=amd64 ;;
arm64 | aarch64) arch=arm64 ;;
*) arch=$(uname -m) ;;
esac

# A shell under Rosetta reports x86_64 on an arm64 Mac.
if [ "$os" = darwin ] && [ "$arch" = amd64 ] &&
	[ "$(sysctl -n sysctl.proc_translated 2>/dev/null || echo 0)" = 1 ]; then
	arch=arm64
fi

case $os/$arch in
darwin/amd64 | darwin/arm64 | linux/amd64 | linux/arm64) ;;
*)
	die "unsupported platform $(uname -s)/$(uname -m).
$BIN releases cover $SUPPORTED. To build from source instead:
    go install github.com/$REPO/cmd/$BIN@latest"
	;;
esac

if [ -z "$version" ]; then
	say "$BIN: resolving the latest release"
	version=$(latest_tag) || version=
	[ -n "$version" ] ||
		die 'could not resolve the latest release; set SARAL_VERSION to a tag such as v0.1.0'
fi
case $version in
v[0-9]*) ;;
*) die "expected a tag like v0.1.0, got '$version'" ;;
esac

number=${version#v}
archive="${BIN}_${number}_${os}_${arch}.tar.gz"
: "${download_base:=https://github.com/$REPO/releases/download/$version}"

if [ -z "$install_dir" ]; then
	if [ -d /usr/local/bin ] && [ -w /usr/local/bin ]; then
		install_dir=/usr/local/bin
	elif [ -n "${HOME:-}" ]; then
		install_dir=$HOME/.local/bin
	else
		die 'nowhere obvious to install to; set SARAL_INSTALL_DIR'
	fi
fi
elsewhere='Choose somewhere else with SARAL_INSTALL_DIR, or re-run under sudo.'
mkdir -p "$install_dir" 2>/dev/null || die "cannot create $install_dir
$elsewhere"
[ -w "$install_dir" ] || die "cannot write to $install_dir
$elsewhere"

tmp=$(mktemp -d 2>/dev/null || mktemp -d -t saral) || die 'cannot create a temporary directory'
trap 'rm -rf "$tmp"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
trap 'exit 129' HUP

say "$BIN: downloading $archive"
fetch "$download_base/$archive" "$tmp/$archive" ||
	die "could not download $download_base/$archive"
fetch "$download_base/checksums.txt" "$tmp/checksums.txt" ||
	die "could not download $download_base/checksums.txt"

expected=$(awk -v want="$archive" '$2 == want { print $1; found = 1 } END { exit !found }' \
	"$tmp/checksums.txt") || die "checksums.txt does not mention $archive"
case $expected in
*[!0-9a-fA-F]* | '') die "checksums.txt gave a malformed sha256 for $archive" ;;
esac
[ "${#expected}" -eq 64 ] || die "checksums.txt gave a malformed sha256 for $archive"

got=$(sha256_of "$tmp/$archive")
if [ "$got" != "$expected" ]; then
	die "checksum mismatch for $archive
    expected $expected
    got      $got
Nothing was installed."
fi
say "$BIN: checksum verified"
verify_signature "$tmp/checksums.txt"

mkdir -p "$tmp/unpacked"
tar -xzf "$tmp/$archive" -C "$tmp/unpacked" || die "could not unpack $archive"
[ -f "$tmp/unpacked/$BIN" ] || die "$archive does not contain a $BIN binary"

staged="$install_dir/.$BIN.$$.incoming"
cp "$tmp/unpacked/$BIN" "$staged" || { rm -f "$staged"; die "could not write to $install_dir"; }
chmod 0755 "$staged" || { rm -f "$staged"; die "could not make $staged executable"; }
mv -f "$staged" "$install_dir/$BIN" || { rm -f "$staged"; die "could not install to $install_dir/$BIN"; }

say "$BIN: installed $version to $install_dir/$BIN"
case ":${PATH:-}:" in
*":$install_dir:"*) ;;
*) warn "$install_dir is not on your PATH; add it to run '$BIN' by name" ;;
esac

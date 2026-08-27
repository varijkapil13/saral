#!/bin/sh
# Exercise scripts/install.sh against a fake release served from disk and from loopback.
#
#   ./scripts/install_test.sh
set -eu

here=$(cd "$(dirname "$0")" && pwd)
script="$here/install.sh"
ws=$(mktemp -d)
trap 'rm -rf "$ws"' EXIT

fails=0
ok() { printf 'ok   %s\n' "$1"; }
bad() {
	printf 'FAIL %s\n' "$1"
	[ ! -s "$ws/out" ] || sed 's/^/       | /' "$ws/out"
	fails=$((fails + 1))
}

sha256_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	else
		shasum -a 256 "$1" | cut -d' ' -f1
	fi
}

# A release holding all four archives, so whichever this machine detects is present.
release="$ws/release"
mkdir -p "$release" "$ws/stage"
printf '#!/bin/sh\necho saral v9.9.9\n' >"$ws/stage/saral"
chmod 0755 "$ws/stage/saral"
: >"$release/checksums.txt"
for target in darwin_amd64 darwin_arm64 linux_amd64 linux_arm64; do
	name="saral_9.9.9_${target}.tar.gz"
	tar -czf "$release/$name" -C "$ws/stage" saral
	printf '%s  %s\n' "$(sha256_of "$release/$name")" "$name" >>"$release/checksums.txt"
done

# install <install-dir> [extra env assignments...]
install() {
	dir=$1
	shift
	env SARAL_VERSION=v9.9.9 SARAL_DOWNLOAD_BASE="file://$release" \
		SARAL_INSTALL_DIR="$dir" "$@" sh "$script" >"$ws/out" 2>&1
}

# Neither the binary nor a staged half-copy may survive a failure.
no_leftovers() {
	[ ! -e "$1/saral" ] || return 1
	[ -z "$(find "$1" -name '.saral.*' 2>/dev/null)" ] || return 1
}

# --- installs, and the installed binary runs -----------------------------------
dir="$ws/bin"
if install "$dir" && [ -x "$dir/saral" ] && [ "$("$dir/saral")" = 'saral v9.9.9' ] &&
	grep -q 'checksum verified' "$ws/out"; then
	ok 'installs the archive for this platform, verifies it, and the binary runs'
else
	bad 'installs the archive for this platform, verifies it, and the binary runs'
fi

# --- a second run replaces the binary in place ---------------------------------
if install "$dir" && [ -x "$dir/saral" ]; then
	ok 'reinstalling over an existing binary works'
else
	bad 'reinstalling over an existing binary works'
fi

# --- a tampered archive is refused before unpacking ----------------------------
mkdir -p "$ws/bent"
cp "$release"/*.tar.gz "$ws/bent/"
sed 's/^[0-9a-f]/f/' "$release/checksums.txt" >"$ws/bent/checksums.txt"
dir="$ws/bin-bent"
if install "$dir" SARAL_DOWNLOAD_BASE="file://$ws/bent"; then
	bad 'a checksum mismatch fails the install'
elif grep -q 'checksum mismatch' "$ws/out" && no_leftovers "$dir"; then
	ok 'a checksum mismatch fails the install and leaves nothing behind'
else
	bad 'a checksum mismatch fails the install and leaves nothing behind'
fi

# --- an archive missing from checksums.txt is refused --------------------------
mkdir -p "$ws/nolist"
cp "$release"/*.tar.gz "$ws/nolist/"
printf '%064d  %s\n' 0 'saral_9.9.9_plan9_386.tar.gz' >"$ws/nolist/checksums.txt"
dir="$ws/bin-nolist"
if install "$dir" SARAL_DOWNLOAD_BASE="file://$ws/nolist"; then
	bad 'an archive missing from checksums.txt fails the install'
elif grep -q 'does not mention' "$ws/out" && no_leftovers "$dir"; then
	ok 'an archive missing from checksums.txt fails the install'
else
	bad 'an archive missing from checksums.txt fails the install'
fi

# --- a download that cannot be had leaves nothing behind -----------------------
mkdir -p "$ws/empty"
dir="$ws/bin-empty"
if install "$dir" SARAL_DOWNLOAD_BASE="file://$ws/empty"; then
	bad 'a failed download fails the install'
elif grep -q 'could not download' "$ws/out" && no_leftovers "$dir"; then
	ok 'a failed download installs nothing'
else
	bad 'a failed download installs nothing'
fi

# --- an unsupported platform names what is supported --------------------------
mkdir -p "$ws/stub"
cat >"$ws/stub/uname" <<'EOF'
#!/bin/sh
case ${1:-} in
-s) echo SunOS ;;
-m) echo sparc ;;
*) echo SunOS ;;
esac
EOF
chmod 0755 "$ws/stub/uname"
dir="$ws/bin-sparc"
if install "$dir" PATH="$ws/stub:$PATH"; then
	bad 'an unsupported platform fails'
elif grep -q 'unsupported platform SunOS/sparc' "$ws/out" &&
	grep -q 'darwin and linux, on amd64 and arm64' "$ws/out" &&
	grep -q 'go install' "$ws/out"; then
	ok 'an unsupported platform names what is supported and how to build'
else
	bad 'an unsupported platform names what is supported and how to build'
fi

# --- an unwritable target fails, and says how to pick another -------------------
dir="$ws/unwritable"
mkdir -p "$dir"
chmod 0500 "$dir"
if install "$dir"; then
	chmod 0700 "$dir"
	bad 'an unwritable install dir fails'
elif grep -q 'cannot write to' "$ws/out" && grep -q 'SARAL_INSTALL_DIR' "$ws/out"; then
	chmod 0700 "$dir"
	ok 'an unwritable install dir fails and says how to choose another'
else
	chmod 0700 "$dir"
	bad 'an unwritable install dir fails and says how to choose another'
fi

mkdir -p "$ws/locked"
chmod 0500 "$ws/locked"
dir="$ws/locked/bin"
if install "$dir"; then
	chmod 0700 "$ws/locked"
	bad 'an install dir that cannot be created fails'
elif grep -q 'cannot create' "$ws/out" && grep -q 'SARAL_INSTALL_DIR' "$ws/out"; then
	chmod 0700 "$ws/locked"
	ok 'an install dir that cannot be created fails and says how to choose another'
else
	chmod 0700 "$ws/locked"
	bad 'an install dir that cannot be created fails and says how to choose another'
fi

# --- a bad tag is rejected ----------------------------------------------------
if install "$ws/bin-tag" SARAL_VERSION=0.1.0; then
	bad 'a version without a v prefix is rejected'
elif grep -q 'expected a tag like' "$ws/out"; then
	ok 'a version without a v prefix is rejected'
else
	bad 'a version without a v prefix is rejected'
fi

# --- argument handling -------------------------------------------------------
if sh "$script" --help >"$ws/out" 2>&1 && grep -q 'usage: install.sh' "$ws/out"; then
	ok '--help prints usage'
else
	bad '--help prints usage'
fi

if sh "$script" --nope >"$ws/out" 2>&1; then
	bad 'an unknown option is rejected'
elif grep -q 'unknown option' "$ws/out"; then
	ok 'an unknown option is rejected'
else
	bad 'an unknown option is rejected'
fi

if sh "$script" --version >"$ws/out" 2>&1; then
	bad 'a flag with no value is rejected'
elif grep -q 'needs a value' "$ws/out"; then
	ok 'a flag with no value is rejected'
else
	bad 'a flag with no value is rejected'
fi

# --- the flags reach the same place the env vars do --------------------------
dir="$ws/bin-flags"
if env SARAL_DOWNLOAD_BASE="file://$release" sh "$script" \
	--version v9.9.9 --install-dir "$dir" >"$ws/out" 2>&1 && [ -x "$dir/saral" ]; then
	ok '--version and --install-dir work'
else
	bad '--version and --install-dir work'
fi

# --- the wget path, where curl is not installed -------------------------------
# GNU wget has no file:// scheme, so this half needs a server on loopback.
if command -v wget >/dev/null 2>&1 && command -v python3 >/dev/null 2>&1; then
	mkdir -p "$ws/nocurl"
	for tool in sh uname sysctl mktemp mkdir tar cp chmod mv rm sed awk cut head cat \
		wget sha256sum shasum openssl; do
		path=$(command -v "$tool" 2>/dev/null) || continue
		ln -sf "$path" "$ws/nocurl/$tool"
	done

	python3 -u -m http.server 0 --bind 127.0.0.1 --directory "$release" >"$ws/httpd" 2>&1 &
	httpd=$!
	port=
	tries=0
	while [ "$tries" -lt 20 ]; do
		port=$(sed -n 's/.*port \([0-9]*\).*/\1/p' "$ws/httpd" | head -n 1)
		[ -z "$port" ] || break
		tries=$((tries + 1))
		sleep 1
	done

	dir="$ws/bin-wget"
	if [ -z "$port" ]; then
		bad 'the wget path installs (no loopback server came up)'
	elif env SARAL_VERSION=v9.9.9 SARAL_DOWNLOAD_BASE="http://127.0.0.1:$port" \
		SARAL_INSTALL_DIR="$dir" PATH="$ws/nocurl" sh "$script" >"$ws/out" 2>&1 &&
		[ -x "$dir/saral" ] && grep -q 'checksum verified' "$ws/out"; then
		ok 'the wget path installs and verifies when curl is not installed'
	else
		bad 'the wget path installs and verifies when curl is not installed'
	fi
	kill "$httpd" 2>/dev/null || true
	wait "$httpd" 2>/dev/null || true
else
	printf 'skip the wget path (needs wget and python3)\n'
fi

if [ "$fails" -ne 0 ]; then
	printf '\n%d check(s) failed\n' "$fails"
	exit 1
fi
printf '\nall checks passed\n'

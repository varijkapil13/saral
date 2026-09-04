package config

import (
	"regexp"
	"runtime/debug"
	"strings"
	"sync"
)

// devName is the directory a build that is not a release keeps its files in.
//
// A checkout and an installed copy are two programs on one machine. Sharing a
// directory means testing a change rewrites the profile, the colour scheme and
// the project scope the installed copy reads, and the two fight over the cache's
// file lock — and it means a bug reproduced against one binary was diagnosed
// against the other's configuration.
const devName = appName + "-dev"

// pseudoVersion matches the tail Go gives a module version built from a commit
// with no tag of its own: fourteen digits of timestamp and twelve of hash. A
// release names a tag and carries neither.
//
// The separator before the timestamp is a dot after a tagged base — the common
// form is vX.Y.Z-0.20260904063733-5c1923c93f74 — and a dash only where the base
// is v0.0.0. Requiring the dash alone read every build from a checkout as a
// release, which is the whole thing this file exists to tell apart.
var pseudoVersion = regexp.MustCompile(`[-.]\d{14}-[0-9a-f]{12}$`)

// dirName is resolved once. ReadBuildInfo walks the binary's own tables, which
// is not something to repeat under every read of a path.
var dirName = sync.OnceValue(func() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || isDevVersion(info.Main.Version) {
		return devName
	}
	return appName
})

// isDevVersion reports whether a module version names a commit rather than a
// release. A prerelease tag — v0.3.0-rc1 — is a release and is not one of
// these: somebody tagged it, so it is an installed copy like any other.
func isDevVersion(version string) bool {
	// Build metadata is everything after a "+", and Go appends "+dirty" to a
	// build made with uncommitted changes. It sits past the hash, so a pattern
	// anchored at the end never sees the pseudo-version underneath it.
	v, _, _ := strings.Cut(strings.TrimSpace(version), "+")
	switch {
	case v == "", v == "(devel)", v == "unknown":
		return true
	case !strings.HasPrefix(v, "v"):
		return true
	default:
		return pseudoVersion.MatchString(v)
	}
}

// IsDevBuild reports whether this binary keeps its files apart from an
// installed copy's. It is what --version says, so that two binaries on one
// machine can be told apart without guessing which one wrote a profile.
func IsDevBuild() bool { return dirName() == devName }

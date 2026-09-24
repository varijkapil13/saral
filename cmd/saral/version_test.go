package main

import (
	"bytes"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/varijkapil13/saral/internal/config"
)

func TestStampFrom(t *testing.T) {
	installed := &debug.BuildInfo{
		Main: debug.Module{Version: "v0.4.1"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "0123456789abcdef0123"},
			{Key: "vcs.time", Value: "2026-09-01T10:00:00Z"},
		},
	}
	unset := buildStamp{version: "dev", commit: "none", date: "unknown"}
	tests := map[string]struct {
		ldflags buildStamp
		info    *debug.BuildInfo
		want    buildStamp
	}{
		"go install reads the module version and the VCS stamp": {
			ldflags: unset, info: installed,
			want: buildStamp{version: "v0.4.1", commit: "0123456789ab", date: "2026-09-01T10:00:00Z"},
		},
		"a release's ldflags win": {
			ldflags: buildStamp{version: "0.5.0", commit: "feedface", date: "2026-09-20"}, info: installed,
			want: buildStamp{version: "0.5.0", commit: "feedface", date: "2026-09-20"},
		},
		"a checkout's (devel) is left as dev": {
			ldflags: unset, info: &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, want: unset,
		},
		"no build info at all": {ldflags: unset, want: unset},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			if got := stampFrom(tc.ldflags, tc.info); got != tc.want {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestPrintVersion_TheLabelIsTheDirectoryDecision(t *testing.T) {
	isolated(t)
	var out bytes.Buffer
	if err := printVersion(&out, options{}); err != nil {
		t.Fatal(err)
	}
	first, _, _ := strings.Cut(out.String(), "\n")
	want := " release"
	if config.IsDevBuild() {
		want = " dev"
	}
	if !strings.HasSuffix(first, want) {
		t.Errorf("the version line %q does not end in%s, which is what config.Dir decided", first, want)
	}
}

func TestPrintVersion_NamesTheGlyphTierTheFlagWouldGive(t *testing.T) {
	isolated(t)
	var out bytes.Buffer
	if err := printVersion(&out, options{glyphs: "ascii"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "glyphs ascii") {
		t.Errorf("--glyphs ascii --version printed %q", out.String())
	}
}

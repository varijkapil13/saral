package main

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	"github.com/varijkapil13/saral/internal/ui/kernel"
)

func TestRun_Version(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"--version"}, {"-version"}} {
		var out, errOut bytes.Buffer
		if err := run(args, &out, &errOut); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if !strings.HasPrefix(out.String(), "saral dev (none, unknown)") {
			t.Errorf("%v printed %q", args, out.String())
		}
	}
}

func TestRun_BenchFirstPaintPrintsMicroseconds(t *testing.T) {
	t.Setenv("SARAL_CONFIG_DIR", t.TempDir())

	var out, errOut bytes.Buffer
	if err := run([]string{"--bench-first-paint"}, &out, &errOut); err != nil {
		t.Fatalf("run: %v (stderr %q)", err, errOut.String())
	}
	got := strings.TrimSpace(out.String())
	if _, err := strconv.Atoi(got); err != nil {
		t.Errorf("expected a microsecond count, got %q", got)
	}
}

func TestRun_UnknownProfileIsAnError(t *testing.T) {
	t.Setenv("SARAL_CONFIG_DIR", t.TempDir())

	var out, errOut bytes.Buffer
	if err := run([]string{"--profile", "nope", "--bench-first-paint"}, &out, &errOut); err == nil {
		t.Error("an explicitly named missing profile should fail loudly")
	}
}

func TestRun_MissingConfigStillStarts(t *testing.T) {
	t.Setenv("SARAL_CONFIG_DIR", t.TempDir())

	deps, opts, err := build(options{benchPaint: true})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if deps.Theme == nil {
		t.Error("no theme was built")
	}
	if _, _, err := kernel.FirstPaint(deps, 100, 30, opts...); err != nil {
		t.Errorf("FirstPaint: %v", err)
	}
}

func TestBuild_NoColorEnvironmentWinsOverTheFlag(t *testing.T) {
	t.Setenv("SARAL_CONFIG_DIR", t.TempDir())
	t.Setenv("NO_COLOR", "1")

	deps, _, err := build(options{theme: "dark"})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if deps.Theme.Color {
		t.Error("NO_COLOR was ignored")
	}
}

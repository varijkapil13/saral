package main

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

const (
	exitOK     = 0
	exitOther  = 1
	exitUsage  = 2
	exitConfig = 3
	exitAuth   = 4
)

type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

func withCode(code int, err error) error {
	if err == nil {
		return nil
	}
	var already *exitError
	if errors.As(err, &already) {
		return err
	}
	return &exitError{code: code, err: err}
}

func usageErrorf(format string, args ...any) error {
	return withCode(exitUsage, fmt.Errorf(format, args...))
}

func exitCodeOf(err error) int {
	if err == nil {
		return exitOK
	}
	var coded *exitError
	if errors.As(err, &coded) {
		return coded.code
	}
	return exitOther
}

type invocation struct {
	opt    options
	stdout io.Writer
	stderr io.Writer
}

// subcommand registers itself from an init() in its own file.
type subcommand struct {
	name    string
	summary string
	run     func(inv *invocation, args []string) error
}

var subcommands = map[string]subcommand{}

func registerSubcommand(s subcommand) {
	if _, dup := subcommands[s.name]; dup {
		panic("saral: subcommand " + s.name + " is registered twice")
	}
	subcommands[s.name] = s
}

func lookupSubcommand(name string) (subcommand, bool) {
	s, ok := subcommands[name]
	return s, ok
}

func subcommandNames() []string {
	names := make([]string, 0, len(subcommands))
	for name := range subcommands {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func subcommandList() string { return strings.Join(subcommandNames(), ", ") }

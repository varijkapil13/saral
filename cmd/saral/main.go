package main

import (
	"fmt"
	"os"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "version" || os.Args[1] == "--version") {
		fmt.Printf("saral %s (%s, %s)\n", version, commit, date)
		return
	}
	fmt.Fprintln(os.Stderr, "saral: not implemented yet — see docs/ROADMAP.md")
	os.Exit(1)
}

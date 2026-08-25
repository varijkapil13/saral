// Package ui pulls in every view package so that its init() runs and it
// appears in the kernel's registries.
//
// This is the one shared file a view packet may edit, and only to add its own
// single blank import in alphabetical order. One line each is why two packets
// adding two views merge cleanly.
package ui

import (
	_ "github.com/varijkapil13/saral/internal/ui/comment"
	_ "github.com/varijkapil13/saral/internal/ui/form"
	_ "github.com/varijkapil13/saral/internal/ui/issue"
	_ "github.com/varijkapil13/saral/internal/ui/list"
	_ "github.com/varijkapil13/saral/internal/ui/onboarding"
	_ "github.com/varijkapil13/saral/internal/ui/palette"
)

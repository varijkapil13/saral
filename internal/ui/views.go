// Package ui pulls in every view package so that its init() runs and it
// appears in the kernel's registries.
//
// This is the one shared file a view packet may edit, and only to add its own
// single blank import in alphabetical order. One line each is why two packets
// adding two views merge cleanly.
package ui

// No views exist yet. The first one adds a line here, like:
//
//	_ "github.com/varijkapil13/saral/internal/ui/board"

package search

// Scope is what a search covers.
type Scope uint8

// The two scopes.
const (
	ScopeSite Scope = iota
	ScopeProject
)

func quote(glyphsASCII bool, s string) string {
	if glyphsASCII {
		return `"` + s + `"`
	}
	return "“" + s + "”"
}

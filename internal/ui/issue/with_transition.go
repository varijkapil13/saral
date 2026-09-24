package issue

// WithTransition opens the pane's status picker with the transition id
// chosen once the issue's moves have been read.
func WithTransition(id string) modelOption {
	return func(m *Model) { m.openMove = id }
}

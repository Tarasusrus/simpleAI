package plugin

// Schema describes input/output contracts for skills.
type Schema struct {
	Name    string
	Version string
	JSON    map[string]any
}

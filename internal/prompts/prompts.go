// Package prompts embeds prompt templates and provides a simple accessor.
// Set PROMPTS_DIR env var to load templates from disk (hot-reload for dev/evals).
package prompts

import (
	"embed"
	"os"
	"path/filepath"
)

//go:embed all:advisor
var fs embed.FS

// Get returns the content of the named template (e.g. "advisor/advice.tmpl").
// If PROMPTS_DIR is set, loads from that directory instead (dev hot-reload).
// Panics if the template is not found — missing templates are a programming error.
func Get(name string) string {
	if dir := os.Getenv("PROMPTS_DIR"); dir != "" {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			panic("prompts: PROMPTS_DIR set but cannot read " + name + ": " + err.Error())
		}
		return string(data)
	}
	data, err := fs.ReadFile(name)
	if err != nil {
		panic("prompts: embedded template not found: " + name)
	}
	return string(data)
}

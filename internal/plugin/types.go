package plugin

import "context"

// Skill defines a minimal contract for pluggable agent capabilities.
type Skill interface {
	Manifest() Manifest
	Run(ctx context.Context, input string) (string, error)
}

// Manifest describes a skill for registry and discovery.
type Manifest struct {
	ID          string
	Name        string
	Description string
	Version     string
}

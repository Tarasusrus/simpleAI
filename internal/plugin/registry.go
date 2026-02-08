package plugin

import "fmt"

// Registry stores registered skills by ID.
type Registry struct {
	skills map[string]Skill
}

func NewRegistry() *Registry {
	return &Registry{
		skills: make(map[string]Skill),
	}
}

func (r *Registry) Register(skill Skill) error {
	if skill == nil {
		return fmt.Errorf("skill is nil")
	}
	manifest := skill.Manifest()
	if manifest.ID == "" {
		return fmt.Errorf("skill id is required")
	}
	if _, exists := r.skills[manifest.ID]; exists {
		return fmt.Errorf("skill already registered: %s", manifest.ID)
	}
	r.skills[manifest.ID] = skill
	return nil
}

func (r *Registry) Get(id string) (Skill, bool) {
	skill, ok := r.skills[id]
	return skill, ok
}

func (r *Registry) List() []Manifest {
	manifests := make([]Manifest, 0, len(r.skills))
	for _, skill := range r.skills {
		manifests = append(manifests, skill.Manifest())
	}
	return manifests
}

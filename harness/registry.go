package harness

// Extension is a named bundle of tools and system prompt sections.
type Extension struct {
	Name     string
	Tools    map[string]ToolDef
	Sections []string
}

// Registry holds installed extensions and resolves their combined surface.
type Registry struct {
	extensions map[string]Extension
	order      []string
}

func NewRegistry() *Registry {
	return &Registry{extensions: map[string]Extension{}}
}

func (r *Registry) Install(ext Extension) {
	if ext.Name == "" {
		return
	}
	if _, exists := r.extensions[ext.Name]; !exists {
		r.order = append(r.order, ext.Name)
	}
	r.extensions[ext.Name] = ext
}

func (r *Registry) Remove(name string) {
	if _, exists := r.extensions[name]; !exists {
		return
	}
	delete(r.extensions, name)
	for i, n := range r.order {
		if n == name {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
}

// Tools resolves the installed tool set in installation order; a later
// extension overrides an earlier tool with the same name.
func (r *Registry) Tools() map[string]ToolDef {
	out := map[string]ToolDef{}
	for _, name := range r.order {
		for toolName, tool := range r.extensions[name].Tools {
			out[toolName] = tool
		}
	}
	return out
}

// SystemPrompt joins the installed sections in installation order.
func (r *Registry) SystemPrompt() string {
	out := ""
	for _, name := range r.order {
		for _, section := range r.extensions[name].Sections {
			if out != "" {
				out += "\n"
			}
			out += section
		}
	}
	return out
}

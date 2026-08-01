package plugins

import "context"

type Plugin interface {
	Name() string
	Apply(ctx context.Context, cfg map[string]interface{}) error
}

// Stopper is optional: agent stops plugins that disappear from desired config
// (e.g. tcp_forward / socks_in after last tunnel is deleted).
type Stopper interface {
	Stop()
}

type Registry struct {
	items map[string]Plugin
}

func NewRegistry() *Registry {
	return &Registry{items: map[string]Plugin{}}
}

func (r *Registry) Register(p Plugin) {
	r.items[p.Name()] = p
}

func (r *Registry) Get(name string) (Plugin, bool) {
	p, ok := r.items[name]
	return p, ok
}

// All returns a snapshot of registered plugins (for stopping removed ones).
func (r *Registry) All() []Plugin {
	out := make([]Plugin, 0, len(r.items))
	for _, p := range r.items {
		out = append(out, p)
	}
	return out
}

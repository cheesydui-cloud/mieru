package plugins

import "context"

type Plugin interface {
	Name() string
	Apply(ctx context.Context, cfg map[string]interface{}) error
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

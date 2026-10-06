package signalModel

import (
	"context"
	"fmt"
)

// Hook is a code-owned reaction to a durable signal. Hook execution and retry
// are infrastructure concerns; this interface only describes the reaction.
type Hook interface {
	Name() string
	Handle(context.Context, Signal) error
}

// HookRegistry stores exact and wildcard registrations. Exact handlers run
// before wildcard handlers so system invariants are established first.
type HookRegistry struct {
	exact    map[Type][]Hook
	wildcard []Hook
}

func NewHookRegistry() *HookRegistry {
	return &HookRegistry{exact: make(map[Type][]Hook)}
}

func (r *HookRegistry) RegisterExact(t Type, hook Hook) {
	if r.hasName(r.exact[t], hook.Name()) {
		panic(fmt.Sprintf("signal: duplicate hook %q for %q", hook.Name(), t))
	}
	r.exact[t] = append(r.exact[t], hook)
}

func (r *HookRegistry) RegisterWildcard(hook Hook) {
	if r.hasName(r.wildcard, hook.Name()) {
		panic(fmt.Sprintf("signal: duplicate wildcard hook %q", hook.Name()))
	}
	r.wildcard = append(r.wildcard, hook)
}

func (r *HookRegistry) HooksFor(t Type) []Hook {
	hooks := make([]Hook, 0, len(r.exact[t])+len(r.wildcard))
	hooks = append(hooks, r.exact[t]...)
	hooks = append(hooks, r.wildcard...)
	return hooks
}

func (r *HookRegistry) hasName(hooks []Hook, name string) bool {
	for _, hook := range hooks {
		if hook.Name() == name {
			return true
		}
	}
	return false
}

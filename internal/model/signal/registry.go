package signalModel

import (
	"encoding/json"
	"fmt"
)

// Constructor creates an empty payload suitable for JSON decoding.
type Constructor func() Payload

// Registry maps stable signal types to their typed payload constructors.
type Registry struct {
	constructors map[Type]Constructor
}

func NewRegistry() *Registry {
	return &Registry{constructors: make(map[Type]Constructor)}
}

func (r *Registry) Register(t Type, constructor Constructor) {
	r.constructors[t] = constructor
}

func (r *Registry) Has(t Type) bool {
	_, ok := r.constructors[t]
	return ok
}

// Decode restores the typed payload registered for t.
func (r *Registry) Decode(t Type, raw json.RawMessage) (Payload, error) {
	constructor, ok := r.constructors[t]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownType, t)
	}
	payload := constructor()
	if err := json.Unmarshal(raw, payload); err != nil {
		return nil, fmt.Errorf("signal: decode %s: %w", t, err)
	}
	return payload, nil
}

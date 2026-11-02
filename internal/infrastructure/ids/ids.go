// Package ids is the production domain.IDGenerator adapter.
package ids

import "github.com/google/uuid"

// Generator creates UUIDv4 identifiers.
type Generator struct{}

// New returns a UUID generator.
func New() Generator { return Generator{} }

// NewID returns a new UUID string.
func (Generator) NewID() string { return uuid.NewString() }

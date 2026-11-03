package application

import (
	"context"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

// FeatureFlags answers the read-only flag surface.
type FeatureFlags struct {
	store domain.FeatureFlagStore
}

// NewFeatureFlags builds the flag service from its store.
func NewFeatureFlags(store domain.FeatureFlagStore) *FeatureFlags {
	return &FeatureFlags{store: store}
}

// List returns every platform flag ordered by name.
func (s *FeatureFlags) List(ctx context.Context) ([]domain.FeatureFlag, error) {
	return s.store.ListFeatureFlags(ctx)
}

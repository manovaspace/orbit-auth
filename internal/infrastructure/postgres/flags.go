package postgres

import (
	"context"
	"fmt"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

// ListFeatureFlags returns every platform flag ordered by name.
func (s *Store) ListFeatureFlags(ctx context.Context) ([]domain.FeatureFlag, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT name, enabled, description, updated_at FROM feature_flags ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("postgres: list feature flags: %w", err)
	}
	defer rows.Close()

	var out []domain.FeatureFlag
	for rows.Next() {
		var flag domain.FeatureFlag
		if err := rows.Scan(&flag.Name, &flag.Enabled, &flag.Description, &flag.UpdatedAt); err != nil {
			return nil, fmt.Errorf("postgres: list feature flags: %w", err)
		}
		out = append(out, flag)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: list feature flags: %w", err)
	}
	return out, nil
}

package domain

import "time"

// FeatureFlag is one named platform switch.
type FeatureFlag struct {
	Name        string
	Enabled     bool
	Description string
	UpdatedAt   time.Time
}

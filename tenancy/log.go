package tenancy

import (
	"context"
	"log/slog"
)

// LogAttrs returns the slog attributes services add to a request log line,
// currently org_id when an organization is in context. It is
// observability-neutral: pass the result to the product's own context-attribute
// registration instead of importing orbit-observability here.
func LogAttrs(ctx context.Context) []slog.Attr {
	orgID, ok := OrgFrom(ctx)
	if !ok {
		return nil
	}
	return []slog.Attr{slog.String("org_id", orgID.String())}
}

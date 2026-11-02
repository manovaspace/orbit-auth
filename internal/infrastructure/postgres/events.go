package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	messagebus "github.com/manovaspace/orbit-message-bus"
	"github.com/manovaspace/orbit-message-bus/outbox"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

// outboxSink writes lifecycle events through the transactional outbox. It is
// bound to one transaction, so the event and the change commit together.
type outboxSink struct {
	tx pgx.Tx
}

var _ domain.EventSink = (*outboxSink)(nil)

func newOutboxSink(tx pgx.Tx) *outboxSink {
	return &outboxSink{tx: tx}
}

// Emit writes one event inside the sink's transaction. The envelope carries the
// business timestamp as its timestamp and the pinned source/version metadata.
func (s *outboxSink) Emit(ctx context.Context, event domain.Event) error {
	occurredAt := event.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	payload, err := json.Marshal(event.Payload)
	if err != nil {
		return fmt.Errorf("postgres: marshal %s payload: %w", event.Subject, err)
	}
	env, err := messagebus.NewEnvelope(event.Subject, payload, domain.EventSource, domain.EventVersion)
	if err != nil {
		return fmt.Errorf("postgres: envelope %s: %w", event.Subject, err)
	}
	env.Timestamp = occurredAt.UTC()
	if err := env.Validate(); err != nil {
		return fmt.Errorf("postgres: envelope %s: %w", event.Subject, err)
	}
	if err := outbox.Insert(ctx, s.tx, event.Subject, env); err != nil {
		return fmt.Errorf("postgres: outbox %s: %w", event.Subject, err)
	}
	return nil
}

// emitEvents writes events in order through one transaction-bound sink.
func emitEvents(ctx context.Context, sink domain.EventSink, events ...domain.Event) error {
	for _, event := range events {
		if err := sink.Emit(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

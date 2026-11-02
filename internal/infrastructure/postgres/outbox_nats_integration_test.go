package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	messagebus "github.com/manovaspace/orbit-message-bus"
	"github.com/manovaspace/orbit-message-bus/bustest"
	"github.com/manovaspace/orbit-message-bus/outbox"

	"github.com/manovaspace/orbit-auth/internal/domain"
)

// TestOutboxRelayNATSDurableConsumer runs only when BUS_NATS_URL is set. It
// publishes through outbox.Relay to NATS, consumes the event with a durable
// JetStream consumer, acknowledges it, and proves a second consumer bound to
// the same durable name is not handed the acknowledged event again.
func TestOutboxRelayNATSDurableConsumer(t *testing.T) {
	url := os.Getenv("BUS_NATS_URL")
	if url == "" {
		t.Skip("set BUS_NATS_URL to run the NATS durable-consumer test")
	}
	_, db := migrationPool(t)
	ctx := context.Background()

	stream := bustest.UniqueDomain()
	subject := stream + ".membership.added"
	durable := stream + "-membership-added"

	nc, err := nats.Connect(url, nats.Timeout(5*time.Second))
	if err != nil {
		t.Fatalf("connect nats: %v", err)
	}
	t.Cleanup(nc.Close)
	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("nats jetstream: %v", err)
	}
	if _, err := js.AddStream(&nats.StreamConfig{
		Name:     stream,
		Subjects: []string{stream + ".>"},
	}); err != nil {
		t.Fatalf("add stream: %v", err)
	}
	t.Cleanup(func() { _ = js.DeleteStream(stream) })
	if _, err := js.AddConsumer(stream, &nats.ConsumerConfig{
		Durable:       durable,
		AckPolicy:     nats.AckExplicitPolicy,
		FilterSubject: subject,
	}); err != nil {
		t.Fatalf("add durable consumer: %v", err)
	}

	bus, err := messagebus.New(messagebus.Config{
		URL:     url,
		Source:  domain.EventSource,
		Version: domain.EventVersion,
	})
	if err != nil {
		t.Fatalf("connect bus: %v", err)
	}
	t.Cleanup(func() { _ = bus.Close() })

	env, err := messagebus.NewEnvelope(subject, json.RawMessage(`{"probe":true}`), domain.EventSource, domain.EventVersion)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := outbox.Insert(ctx, tx, subject, env); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	relay := &outbox.Relay{Pool: db.Pool, Bus: bus}
	published, err := relay.RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if published != 1 {
		t.Fatalf("relay published %d events, want 1", published)
	}

	first, err := js.PullSubscribe(subject, durable, nats.Bind(stream, durable))
	if err != nil {
		t.Fatalf("bind durable consumer: %v", err)
	}
	msgs, err := first.Fetch(1, nats.MaxWait(5*time.Second))
	if err != nil {
		t.Fatalf("fetch event: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("fetched %d messages, want 1", len(msgs))
	}
	got, err := messagebus.UnmarshalEnvelope(msgs[0].Data)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != env.ID {
		t.Fatalf("received envelope %s, want %s", got.ID, env.ID)
	}
	if err := msgs[0].AckSync(); err != nil {
		t.Fatalf("ack event: %v", err)
	}
	if err := first.Unsubscribe(); err != nil {
		t.Fatalf("unsubscribe first consumer: %v", err)
	}

	// A new consumer bound to the same durable name resumes after the ack, so
	// it sees no message.
	second, err := js.PullSubscribe(subject, durable, nats.Bind(stream, durable))
	if err != nil {
		t.Fatalf("rebind durable consumer: %v", err)
	}
	t.Cleanup(func() { _ = second.Unsubscribe() })
	msgs, err = second.Fetch(1, nats.MaxWait(1*time.Second))
	if !errors.Is(err, nats.ErrTimeout) {
		t.Fatalf("second consumer fetch error = %v, want timeout", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("durable consumer redelivered %d messages after ack", len(msgs))
	}
}

package observability_test

import (
	"context"
	"testing"

	"github.com/elsell/reqdb/internal/observability"
	"github.com/elsell/reqdb/internal/ports"
)

func TestBrokerPublishesNewestEvent(t *testing.T) {
	broker := observability.NewBroker()
	stream, cancel := broker.Subscribe()
	defer cancel()

	broker.Record(context.Background(), ports.Event{Name: "first"})
	broker.Record(context.Background(), ports.Event{Name: "second"})

	event := <-stream
	if event.Name != "second" || event.Sequence != 2 {
		t.Fatalf("got event %#v", event)
	}
}

func TestBrokerClosesSubscription(t *testing.T) {
	broker := observability.NewBroker()
	stream, cancel := broker.Subscribe()
	cancel()
	cancel()
	if _, open := <-stream; open {
		t.Fatal("subscription stayed open")
	}
}

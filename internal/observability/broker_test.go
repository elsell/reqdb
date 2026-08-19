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

func TestBrokerCloseEndsAllSubscriptions(t *testing.T) {
	broker := observability.NewBroker()
	first, cancelFirst := broker.Subscribe()
	second, _ := broker.Subscribe()

	broker.Close()
	broker.Close()
	cancelFirst()

	for _, stream := range []<-chan ports.Event{first, second} {
		if _, open := <-stream; open {
			t.Fatal("subscription remained open")
		}
	}
	stream, _ := broker.Subscribe()
	if _, open := <-stream; open {
		t.Fatal("subscription opened after broker close")
	}
	broker.Record(context.Background(), ports.Event{Name: "ignored"})
}

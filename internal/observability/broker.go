package observability

import (
	"context"
	"sync"

	"github.com/elsell/reqdb/internal/ports"
)

// Broker distributes recent domain changes to connected API clients.
// Slow clients lose intermediate events and keep the newest event.
type Broker struct {
	mu          sync.RWMutex
	closed      bool
	nextClient  uint64
	nextEvent   uint64
	subscribers map[uint64]chan ports.Event
}

func NewBroker() *Broker {
	return &Broker{subscribers: make(map[uint64]chan ports.Event)}
}

func (broker *Broker) Record(_ context.Context, event ports.Event) {
	broker.mu.Lock()
	if broker.closed {
		broker.mu.Unlock()
		return
	}
	broker.nextEvent++
	event.Sequence = broker.nextEvent
	for _, subscriber := range broker.subscribers {
		select {
		case subscriber <- event:
		default:
			select {
			case <-subscriber:
			default:
			}
			select {
			case subscriber <- event:
			default:
			}
		}
	}
	broker.mu.Unlock()
}

func (broker *Broker) Subscribe() (<-chan ports.Event, func()) {
	broker.mu.Lock()
	if broker.closed {
		stream := make(chan ports.Event)
		close(stream)
		broker.mu.Unlock()
		return stream, func() {}
	}
	broker.nextClient++
	id := broker.nextClient
	stream := make(chan ports.Event, 1)
	broker.subscribers[id] = stream
	broker.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			broker.mu.Lock()
			if _, exists := broker.subscribers[id]; exists {
				delete(broker.subscribers, id)
				close(stream)
			}
			broker.mu.Unlock()
		})
	}
	return stream, cancel
}

// Close ends all active subscriptions and rejects new subscriptions.
func (broker *Broker) Close() {
	broker.mu.Lock()
	defer broker.mu.Unlock()
	if broker.closed {
		return
	}
	broker.closed = true
	for id, subscriber := range broker.subscribers {
		close(subscriber)
		delete(broker.subscribers, id)
	}
}

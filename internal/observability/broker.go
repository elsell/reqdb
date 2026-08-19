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
	nextClient  uint64
	nextEvent   uint64
	subscribers map[uint64]chan ports.Event
}

func NewBroker() *Broker {
	return &Broker{subscribers: make(map[uint64]chan ports.Event)}
}

func (broker *Broker) Record(_ context.Context, event ports.Event) {
	broker.mu.Lock()
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
	broker.nextClient++
	id := broker.nextClient
	stream := make(chan ports.Event, 1)
	broker.subscribers[id] = stream
	broker.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			broker.mu.Lock()
			delete(broker.subscribers, id)
			close(stream)
			broker.mu.Unlock()
		})
	}
	return stream, cancel
}

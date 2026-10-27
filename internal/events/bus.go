package events

import (
	"context"
	"log/slog"
	"sync"
)

// HandlerFunc handles an event.
type HandlerFunc func(ctx context.Context, e Event)

// Bus provides an asynchronous in-process event publish/subscribe mechanism.
type Bus struct {
	mu         sync.RWMutex
	handlers   map[string][]HandlerFunc
	eventsChan chan Event
	quit       chan struct{}
	wg         sync.WaitGroup
	bufferSize int
}

// NewBus creates a new Event Bus with a buffered channel (default 100).
func NewBus(bufferSize int) *Bus {
	if bufferSize <= 0 {
		bufferSize = 100
	}
	b := &Bus{
		handlers:   make(map[string][]HandlerFunc),
		eventsChan: make(chan Event, bufferSize),
		quit:       make(chan struct{}),
		bufferSize: bufferSize,
	}
	b.wg.Add(1)
	go b.worker()
	return b
}

// Subscribe registers a handler for a given event type.
func (b *Bus) Subscribe(eventType string, handler HandlerFunc) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[eventType] = append(b.handlers[eventType], handler)
}

// Publish enqueues an event for asynchronous processing.
// If the buffer is full, it drops the event and logs a warning.
func (b *Bus) Publish(e Event) {
	if e == nil {
		return
	}
	select {
	case b.eventsChan <- e:
	default:
		slog.Warn("event bus buffer full, dropping event", "type", e.Type())
	}
}

// Close gracefully stops the background worker.
func (b *Bus) Close() {
	close(b.quit)
	b.wg.Wait()
}

func (b *Bus) worker() {
	defer b.wg.Done()
	ctx := context.Background()

	for {
		select {
		case <-b.quit:
			// Drain remaining events
			for {
				select {
				case e := <-b.eventsChan:
					b.dispatch(ctx, e)
				default:
					return
				}
			}
		case e := <-b.eventsChan:
			b.dispatch(ctx, e)
		}
	}
}

func (b *Bus) dispatch(ctx context.Context, e Event) {
	b.mu.RLock()
	handlers := b.handlers[e.Type()]
	b.mu.RUnlock()

	for _, h := range handlers {
		func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("panic in event handler", "type", e.Type(), "recover", r)
				}
			}()
			h(ctx, e)
		}()
	}
}

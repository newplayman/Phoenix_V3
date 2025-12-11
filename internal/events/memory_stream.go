package events

import (
	"context"
	"sync"
	"time"
)

type MemoryStream struct {
	buffer int

	mu    sync.RWMutex
	subs  map[Topic][]chan Event
	alive bool
}

func NewMemoryStream(buffer int) *MemoryStream {
	if buffer <= 0 {
		buffer = 32
	}
	return &MemoryStream{
		buffer: buffer,
		subs:   make(map[Topic][]chan Event),
		alive:  true,
	}
}

func (m *MemoryStream) Publish(ctx context.Context, topic Topic, payload interface{}) error {
	m.mu.RLock()
	if !m.alive {
		m.mu.RUnlock()
		return nil
	}
	targets := append([]chan Event(nil), m.subs[topic]...)
	m.mu.RUnlock()

	if len(targets) == 0 {
		return nil
	}

	ev := Event{
		Topic:     topic,
		Payload:   payload,
		Timestamp: time.Now(),
	}

	for _, ch := range targets {
		select {
		case ch <- ev:
		case <-ctx.Done():
			return ctx.Err()
		default:
			// drop oldest to avoid blocking
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- ev:
			default:
			}
		}
	}
	return nil
}

func (m *MemoryStream) Subscribe(topic Topic) (<-chan Event, func(), error) {
	ch := make(chan Event, m.buffer)

	m.mu.Lock()
	if !m.alive {
		m.mu.Unlock()
		close(ch)
		return ch, func() {}, nil
	}
	m.subs[topic] = append(m.subs[topic], ch)
	m.mu.Unlock()

	cancel := func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		subs := m.subs[topic]
		for i, c := range subs {
			if c == ch {
				// remove
				subs[i] = subs[len(subs)-1]
				subs = subs[:len(subs)-1]
				break
			}
		}
		if len(subs) == 0 {
			delete(m.subs, topic)
		} else {
			m.subs[topic] = subs
		}
		close(ch)
	}

	return ch, cancel, nil
}

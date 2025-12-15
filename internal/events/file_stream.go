package events

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FileStream is a local-only Stream implementation.
// It writes every published event as JSONL to a single file.
// Subscribe works in-process only (no cross-process pubsub).
//
// This driver exists for rehearsal environments where network sockets are
// restricted (e.g. no Redis TCP), but we still want a replay/audit log.
type FileStream struct {
	path string

	mu     sync.Mutex
	f      *os.File
	w      *bufio.Writer
	closed bool

	subMu sync.Mutex
	subs  map[Topic][]chan Event
}

type fileEvent struct {
	Topic     Topic           `json:"topic"`
	Timestamp time.Time       `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
}

func NewFileStream(path string) (*FileStream, error) {
	if path == "" {
		path = "logs/events.jsonl"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	fs := &FileStream{
		path: path,
		f:    f,
		w:    bufio.NewWriterSize(f, 64*1024),
		subs: map[Topic][]chan Event{},
	}
	return fs, nil
}

func (f *FileStream) Publish(ctx context.Context, topic Topic, payload interface{}) error {
	_ = ctx

	var data []byte
	switch v := payload.(type) {
	case []byte:
		data = v
	case json.RawMessage:
		data = v
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("marshal payload: %w", err)
		}
		data = b
	}

	ev := fileEvent{Topic: topic, Timestamp: time.Now(), Payload: data}
	line, err := json.Marshal(ev)
	if err != nil {
		return err
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return fmt.Errorf("file stream closed")
	}
	if _, err := f.w.Write(append(line, '\n')); err != nil {
		return err
	}
	if err := f.w.Flush(); err != nil {
		return err
	}

	// In-process fanout.
	f.subMu.Lock()
	defer f.subMu.Unlock()
	for _, ch := range f.subs[topic] {
		select {
		case ch <- Event{Topic: topic, Payload: data, Timestamp: ev.Timestamp}:
		default:
		}
	}

	return nil
}

func (f *FileStream) Subscribe(topic Topic) (<-chan Event, func(), error) {
	ch := make(chan Event, 128)
	f.subMu.Lock()
	f.subs[topic] = append(f.subs[topic], ch)
	f.subMu.Unlock()

	cancel := func() {
		f.subMu.Lock()
		defer f.subMu.Unlock()
		list := f.subs[topic]
		out := list[:0]
		for _, c := range list {
			if c == ch {
				continue
			}
			out = append(out, c)
		}
		f.subs[topic] = out
		close(ch)
	}

	return ch, cancel, nil
}

func (f *FileStream) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil
	}
	f.closed = true
	if f.w != nil {
		_ = f.w.Flush()
	}
	if f.f != nil {
		return f.f.Close()
	}
	return nil
}


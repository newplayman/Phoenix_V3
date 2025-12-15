package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisStream struct {
	client *redis.Client

	prefix string
	group  string
	block  time.Duration
	acksRequired   bool
	replayRetention time.Duration
	trimMu         struct {
		last map[Topic]time.Time
	}
}

type redisEvent struct {
	Timestamp time.Time       `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
}

type RedisOption func(*RedisStream)

func WithAcksRequired(required bool) RedisOption {
	return func(r *RedisStream) {
		r.acksRequired = required
	}
}

func WithReplayRetention(retention time.Duration) RedisOption {
	return func(r *RedisStream) {
		if retention > 0 {
			r.replayRetention = retention
		}
	}
}

func NewRedisStream(redisURL, prefix, group string, options ...RedisOption) (*RedisStream, error) {
	if redisURL == "" {
		return nil, errors.New("redis url required")
	}

	redisOpts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}

	client := redis.NewClient(redisOpts)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("ping redis failed: %w", err)
	}

	if prefix == "" {
		prefix = "phoenix"
	}
	if group == "" {
		group = "phoenix-consumer"
	}

	rs := &RedisStream{
		client: client,
		prefix: prefix,
		group:  group,
		block:  5 * time.Second,
		acksRequired: true,
	}
	rs.trimMu.last = make(map[Topic]time.Time)
	for _, opt := range options {
		opt(rs)
	}
	return rs, nil
}

func (r *RedisStream) Publish(ctx context.Context, topic Topic, payload interface{}) error {
	stream := r.streamName(topic)

	var data []byte
	switch v := payload.(type) {
	case []byte:
		data = v
	case json.RawMessage:
		data = v
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("marshal event payload: %w", err)
		}
		data = b
	}

	ev := redisEvent{
		Timestamp: time.Now(),
		Payload:   data,
	}
	raw, err := json.Marshal(ev)
	if err != nil {
		return err
	}

	if err := r.client.XAdd(ctx, &redis.XAddArgs{
		Stream: stream,
		Values: map[string]interface{}{"event": raw},
	}).Err(); err != nil {
		return err
	}
	r.maybeTrim(ctx, topic)
	return nil
}

func (r *RedisStream) Subscribe(topic Topic) (<-chan Event, func(), error) {
	stream := r.streamName(topic)
	if r.acksRequired {
		if err := r.ensureGroup(stream); err != nil {
			return nil, nil, err
		}
	}

	out := make(chan Event, 128)
	ctx, cancel := context.WithCancel(context.Background())
	consumer := fmt.Sprintf("%s-%d", r.group, time.Now().UnixNano())
	lastID := "$"

	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			var res []redis.XStream
			var err error
			if r.acksRequired {
				res, err = r.client.XReadGroup(ctx, &redis.XReadGroupArgs{
					Group:    r.group,
					Consumer: consumer,
					Streams:  []string{stream, ">"},
					Count:    100,
					Block:    r.block,
				}).Result()
			} else {
				res, err = r.client.XRead(ctx, &redis.XReadArgs{
					Streams: []string{stream, lastID},
					Count:   100,
					Block:   r.block,
				}).Result()
			}
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return
				}
				if err == redis.Nil {
					continue
				}
				time.Sleep(time.Second)
				continue
			}

			for _, streamRes := range res {
				for _, msg := range streamRes.Messages {
					lastID = msg.ID
					payloadRaw, _ := msg.Values["event"].(string)
					var stored redisEvent
					if payloadRaw != "" {
						_ = json.Unmarshal([]byte(payloadRaw), &stored)
					}

					ev := Event{
						Topic:     topic,
						Payload:   []byte(stored.Payload),
						Timestamp: stored.Timestamp,
					}

					select {
					case out <- ev:
					case <-ctx.Done():
						return
					}
					if r.acksRequired {
						_, _ = r.client.XAck(ctx, stream, r.group, msg.ID).Result()
					}
				}
			}
		}
	}()

	cancelFn := func() {
		cancel()
	}

	return out, cancelFn, nil
}

func (r *RedisStream) ensureGroup(stream string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := r.client.XGroupCreateMkStream(ctx, stream, r.group, "$").Err()
	if err != nil {
		if strings.Contains(err.Error(), "BUSYGROUP") {
			return nil
		}
		return err
	}
	return nil
}

func (r *RedisStream) streamName(topic Topic) string {
	return fmt.Sprintf("%s:%s", r.prefix, topic)
}

func (r *RedisStream) maybeTrim(ctx context.Context, topic Topic) {
	if r.replayRetention <= 0 {
		return
	}
	// Trim at most once per minute per topic.
	now := time.Now()
	if r.trimMu.last == nil {
		r.trimMu.last = make(map[Topic]time.Time)
	}
	if last := r.trimMu.last[topic]; !last.IsZero() && now.Sub(last) < time.Minute {
		return
	}
	r.trimMu.last[topic] = now

	minTS := now.Add(-r.replayRetention).UnixMilli()
	minID := fmt.Sprintf("%d-0", minTS)
	stream := r.streamName(topic)
	// Best-effort trim; ignore errors for compatibility.
	_, _ = r.client.XTrimMinIDApprox(ctx, stream, minID, 1000).Result()
}

package events

import (
	"context"
	"time"
)

type Topic string

const (
	TopicTicker     Topic = "ticker"
	TopicPoolState  Topic = "pool_state"
	TopicIntentExec Topic = "intent_exec"
	TopicStrategy   Topic = "strategy"
	TopicRisk       Topic = "risk"
	TopicAudit      Topic = "audit"
)

type Event struct {
	Topic     Topic
	Payload   interface{}
	Timestamp time.Time
}

type Stream interface {
	Publish(ctx context.Context, topic Topic, payload interface{}) error
	Subscribe(topic Topic) (<-chan Event, func(), error)
}

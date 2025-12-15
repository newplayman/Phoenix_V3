package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"phoenix-v3/internal/config"
)

type replayOutput struct {
	Stream    string          `json:"stream"`
	Topic     string          `json:"topic,omitempty"`
	ID        string          `json:"id"`
	Timestamp time.Time       `json:"timestamp,omitempty"`
	Payload   json.RawMessage `json:"payload"`
}

type wrappedEvent struct {
	Timestamp time.Time       `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
}

func normalizeCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := make(map[string]struct{})
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		key := strings.ToLower(p)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func buildStreams(prefix string, topics, streams []string) []string {
	if len(topics) == 0 && len(streams) == 0 {
		topics = []string{"ticker"}
	}
	if prefix == "" {
		prefix = "phoenix"
	}

	out := make([]string, 0, len(topics)+len(streams))
	for _, t := range topics {
		if strings.Contains(t, ":") {
			out = append(out, t)
			continue
		}
		out = append(out, fmt.Sprintf("%s:%s", prefix, t))
	}
	out = append(out, streams...)
	sort.Strings(out)
	return out
}

func topicFromStream(prefix, stream string) string {
	if prefix == "" {
		prefix = "phoenix"
	}
	p := prefix + ":"
	if strings.HasPrefix(stream, p) {
		return strings.TrimPrefix(stream, p)
	}
	return ""
}

func decodeEventField(v interface{}) (time.Time, json.RawMessage) {
	var raw []byte
	switch x := v.(type) {
	case string:
		raw = []byte(x)
	case []byte:
		raw = x
	default:
		b, _ := json.Marshal(v)
		return time.Time{}, b
	}

	var wrap wrappedEvent
	if err := json.Unmarshal(raw, &wrap); err == nil {
		if len(wrap.Payload) > 0 {
			return wrap.Timestamp, wrap.Payload
		}
	}
	return time.Time{}, raw
}

func buildXReadArgs(streams []string, ids map[string]string, count int64, block time.Duration) *redis.XReadArgs {
	args := &redis.XReadArgs{Streams: make([]string, 0, len(streams)*2), Count: count, Block: block}
	for _, s := range streams {
		id := ids[s]
		args.Streams = append(args.Streams, s, id)
	}
	return args
}

func main() {
	driver := flag.String("driver", "redis", "事件流驱动，目前仅支持 redis")
	configPath := flag.String("config", "", "可选：读取 configs/config.yaml 来获取 events.redis_url/prefix")
	redisURL := flag.String("redis-url", "", "Redis 连接串，例如 redis://:pass@localhost:6379/0")
	prefix := flag.String("prefix", "", "可选：redis stream 前缀（默认 phoenix 或来自 config）")
	topics := flag.String("topics", "", "可选：topic 列表（例如 ticker,pool_state,intent_exec）")
	streams := flag.String("streams", "", "可选：stream 名称列表（例如 phoenix:ticker,phoenix:pool_state）")
	fromID := flag.String("from", "", "开始读取的 ID；非 follow 默认 0-0，follow 默认 $")
	count := flag.Int64("count", 200, "每次最多读取条数")
	follow := flag.Bool("follow", false, "持续跟随输出（类似 tail -f）")
	block := flag.Duration("block", 5*time.Second, "follow 时 XREAD block 时长")
	readTimeout := flag.Duration("read-timeout", 10*time.Second, "非 follow 时读取超时")
	jsonl := flag.Bool("jsonl", false, "按 JSON Lines 输出（推荐 follow 使用）")
	flag.Parse()

	if *driver != "redis" {
		log.Fatalf("replay 目前仅支持 redis driver")
	}
	if *configPath != "" {
		cfg, err := config.LoadConfig(*configPath)
		if err != nil {
			log.Fatalf("读取 config 失败: %v", err)
		}
		if *redisURL == "" {
			*redisURL = cfg.Events.RedisURL
		}
		if *prefix == "" {
			*prefix = cfg.Events.RedisPrefix
		}
	}

	if *redisURL == "" {
		log.Fatalf("必须通过 -redis-url 或 -config 指定 Redis 地址")
	}

	opts, err := redis.ParseURL(*redisURL)
	if err != nil {
		log.Fatalf("解析 redis url 失败: %v", err)
	}
	client := redis.NewClient(opts)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		log.Fatalf("Redis Ping 失败: %v", err)
	}

	streamList := buildStreams(*prefix, normalizeCSV(*topics), normalizeCSV(*streams))
	if len(streamList) == 0 {
		log.Fatalf("streams/topics 为空")
	}

	ids := make(map[string]string, len(streamList))
	defaultFrom := "0-0"
	if *follow {
		defaultFrom = "$"
		if !*jsonl {
			*jsonl = true
		}
	}
	if *fromID == "" {
		*fromID = defaultFrom
	}
	for _, s := range streamList {
		ids[s] = *fromID
	}

	if !*follow {
		readCtx, cancelRead := context.WithTimeout(context.Background(), *readTimeout)
		defer cancelRead()
		res, err := client.XRead(readCtx, buildXReadArgs(streamList, ids, *count, 0)).Result()
		if err != nil {
			log.Fatalf("读取 Redis Stream 失败: %v", err)
		}

		out := make([]replayOutput, 0)
		for _, streamRes := range res {
			for _, msg := range streamRes.Messages {
				ts, payload := decodeEventField(msg.Values["event"])
				out = append(out, replayOutput{
					Stream:    streamRes.Stream,
					Topic:     topicFromStream(*prefix, streamRes.Stream),
					ID:        msg.ID,
					Timestamp: ts,
					Payload:   payload,
				})
				ids[streamRes.Stream] = msg.ID
			}
		}

		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(out); err != nil {
			log.Fatalf("输出结果失败: %v", err)
		}
		fmt.Fprintf(os.Stderr, "共读取 %d 条事件\n", len(out))
		return
	}

	enc := json.NewEncoder(os.Stdout)
	for {
		res, err := client.XRead(context.Background(), buildXReadArgs(streamList, ids, *count, *block)).Result()
		if err != nil {
			if err == redis.Nil {
				continue
			}
			log.Printf("读取 Redis Stream 失败: %v", err)
			time.Sleep(time.Second)
			continue
		}
		for _, streamRes := range res {
			for _, msg := range streamRes.Messages {
				ts, payload := decodeEventField(msg.Values["event"])
				line := replayOutput{
					Stream:    streamRes.Stream,
					Topic:     topicFromStream(*prefix, streamRes.Stream),
					ID:        msg.ID,
					Timestamp: ts,
					Payload:   payload,
				}
				if *jsonl {
					if err := enc.Encode(line); err != nil {
						log.Fatalf("输出结果失败: %v", err)
					}
				} else {
					b, _ := json.Marshal(line)
					fmt.Fprintln(os.Stdout, string(b))
				}
				ids[streamRes.Stream] = msg.ID
			}
		}
	}
}

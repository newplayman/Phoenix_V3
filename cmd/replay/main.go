package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/redis/go-redis/v9"
)

func main() {
	driver := flag.String("driver", "redis", "事件流驱动，目前仅支持 redis")
	redisURL := flag.String("redis-url", "", "Redis 连接串，例如 redis://:pass@localhost:6379/0")
	stream := flag.String("stream", "phoenix:ticker", "要回放的 stream 名称（topic）")
	fromID := flag.String("from", "0-0", "开始读取的 ID")
	count := flag.Int64("count", 100, "最多读取条数")
	flag.Parse()

	if *driver != "redis" {
		log.Fatalf("replay 目前仅支持 redis driver")
	}
	if *redisURL == "" {
		log.Fatalf("必须通过 -redis-url 指定 Redis 地址")
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

	readCtx, cancelRead := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelRead()

	res, err := client.XRead(readCtx, &redis.XReadArgs{
		Streams: []string{*stream, *fromID},
		Count:   *count,
		Block:   0,
	}).Result()
	if err != nil {
		log.Fatalf("读取 Redis Stream 失败: %v", err)
	}

	type output struct {
		ID        string          `json:"id"`
		Timestamp time.Time       `json:"timestamp"`
		Payload   json.RawMessage `json:"payload"`
	}

	var out []output

	for _, streamRes := range res {
		for _, msg := range streamRes.Messages {
			var payload json.RawMessage
			if raw, ok := msg.Values["event"].(string); ok && raw != "" {
				var wrap struct {
					Timestamp time.Time       `json:"timestamp"`
					Payload   json.RawMessage `json:"payload"`
				}
				if err := json.Unmarshal([]byte(raw), &wrap); err == nil {
					out = append(out, output{
						ID:        msg.ID,
						Timestamp: wrap.Timestamp,
						Payload:   wrap.Payload,
					})
					continue
				}
			}

			// fallback
			if raw, ok := msg.Values["event"].([]byte); ok {
				payload = raw
			} else {
				b, _ := json.Marshal(msg.Values)
				payload = b
			}
			out = append(out, output{
				ID:      msg.ID,
				Payload: payload,
			})
		}
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(out); err != nil {
		log.Fatalf("输出结果失败: %v", err)
	}

	fmt.Fprintf(os.Stderr, "共读取 %d 条事件\n", len(out))
}

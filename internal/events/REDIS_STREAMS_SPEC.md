# Redis Streams 事件流规范（Phoenix V3）

目标：让跨模块通信“可重放、可审计、可降级”。默认生产使用 Redis Streams，开发可用内存实现。

## 1. 命名
- Stream 名称：`{prefix}:{topic}`
  - `prefix` 来自 `events.redis_prefix`（默认 `phoenix`）
  - `topic` 取自 `internal/events/stream.go` 的 `Topic` 常量

## 2. 消息格式
- 每条 XADD 写入一个 field：`event`
- `event` 值为 JSON：
  - `timestamp`: RFC3339 时间
  - `payload`: 任意 JSON（模块自定义）

## 3. 消费模式
- `events.acks_required=true`：使用 Consumer Group（XREADGROUP）并在成功投递后 XACK。
- `events.acks_required=false`：使用 XREAD 逐条读取（不做 ACK），用于 dev 或只读监听。

## 4. 回放/保留
- `events.replay_retention` 控制保留窗口（例如 `24h`）。
- Publisher 会 best-effort 做 `XTRIM MINID ~`，按 topic 至多每分钟触发一次。

## 5. 降级策略
- Redis 不可用时，Bot 自动回退为内存事件流（仅进程内）。


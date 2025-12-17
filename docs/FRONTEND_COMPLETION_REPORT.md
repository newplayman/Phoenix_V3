# Web 面板实现状态报告（当前）

## 1. 概述
本报告总结 Phoenix V3 Web 面板的**当前实现状态**。

当前 `web/` 的目标是：在不触发链上执行、不过度耦合后端实现细节的前提下，提供一个**只读观测面板**，可在 mock 或真实 `/api/v1` Read APIs 下稳定构建与运行。

重要声明：
- 当前 Web 实现为 **WIP / read-only**：不包含任何写操作（不调用 preview/execute/pause/resume），不承担资金风险。
- 完整“交易控制台”（含 unlock、preview→execute、SSE、审计）仍属于**设计目标**，需后续分阶段落地并严格遵守 `docs/api/*` 契约与安全开关。

## 2. 技术栈
- **核心框架**: React 18, Vite 5
- **样式**: Tailwind CSS v3
- **数据源**:
  - mock 模式：`VITE_USE_MOCK=1`
  - 真实后端：`/api/v1/health`、`/api/v1/pools`、`/api/v1/pools/{pool_id}/state`
  - 只读执行追踪：`/api/v1/intents`、`/api/v1/intents/{intent_id}`（仅展示 steps/tx_hash；不触发写操作）

## 3. 目录结构
```
web/
├── src/
│   ├── App.jsx           # 单页只读面板（polling + mock）
│   ├── main.jsx          # 入口
│   └── index.css         # Tailwind 基础样式 + 少量自定义类
├── index.html
├── package.json
├── tailwind.config.js
└── postcss.config.js
```

## 4. 功能实现详情

### 4.1 核心功能（当前）
- 支持 mock 模式：无需后端即可演示 UI 形态（无敏感默认）。
- 支持真实后端只读轮询：
  - `GET /api/v1/health`
  - `GET /api/v1/pools`
  - `GET /api/v1/pools/{pool_id}/state`
  - `GET /api/v1/intents?pool_id=&limit=`
  - `GET /api/v1/intents/{intent_id}`（显示 steps 与 tx 链接；用于对照链上执行）
  - `GET /api/v1/tx?pool_id=&limit=`（展示 tx 列表）
  - `GET /api/v1/audit?pool_id=&limit=`（展示 operator audit 列表）
  - `GET /api/v1/stream`（SSE：展示连接状态与最近事件类型/时间戳；仅用于只读观测）

### 4.2 安全约束（当前）
- Web 不包含写操作入口；不会调用控制面 API。
- Admin Token 仅保存在 `sessionStorage`，用于只读鉴权（与后端一致）。

## 5. 运行与构建
### 开发模式
```bash
cd web
npm run dev
```
### 生产构建
```bash
npm run build
```
构建产物位于 `web/dist` 目录。

## 6. 后续优化建议
- 在保持“只读默认”的前提下，分阶段引入：
  - SSE 订阅（仅用于读事件）
  - 多页面结构与更严格的响应校验（runtime schema）
  - 写操作 UI（必须 preview→execute、默认关闭、审计与 kill-switch）

---
**自测结果**:
- `make ci`（Pass）

# Phoenix V3 部署与运行向导

本文档将指导您如何启动 Phoenix V3 系统，包括 Go 后端 (Bot) 和 React 前端 (Dashboard)。

## 1. 启动后端 (Bot)

后端负责连接交易所、计算策略、风险控制和执行交易。

**前提条件**: 已安装 Go 1.21+。

```bash
# 1. 确保在项目根目录
cd phoenix-v3

# 2. 编译项目
go build -o bot ./cmd/bot/main.go

# 3. 运行 Bot
./bot
```

**成功标志**:
您应该在控制台看到类似以下的输出：
```text
Phoenix V3 Config Loaded...
Monitor server starting on :8080
Phoenix V3 Bot Started (Phase 6: Secured).
Executing Intent intent-xxx [DryRun=true]
>>> Dry Run: Simulated Tx Execution
```

## 2. 启动前端 (Dashboard)

前端提供了一个可视化的控制面板，用于实时监控行情、策略状态和意图队列。

**前提条件**: 已安装 Node.js (建议 v18+) 和 npm。

```bash
# 1. 进入 web 目录
cd web

# 2. 安装依赖 (仅需第一次运行)
npm install

# 3. 启动开发服务器
npm run dev
```

**成功标志**:
终端会显示访问地址，通常是：
```text
  ➜  Local:   http://localhost:5173/
```

## 3. 访问 Dashboard UI

打开浏览器，访问 **http://localhost:5173/**。

### 界面功能说明：

1.  **顶部状态栏**
    *   **ETH Network / Binance Feed**: 显示当前网络和数据源连接状态（绿色为正常）。
    *   **LIVE 标签**: 表示系统处于实时运行模式。

2.  **Market Overview (市场概览)**
    *   显示当前 ETH/USDT 的实时价格（从后端 API 获取）。
    *   **区间可视化条**: 蓝色条代表当前 Bot 设定的 LP 区间，白色滑块代表当前价格位置。直观展示价格是否偏离区间中心。

3.  **Engine State (引擎状态)**
    *   **Current Tick**: 当前链上的 Tick 值。
    *   **Target Tick**: 策略计算出的理想 Tick 值。
    *   **Rebalance Now**: 手动触发再平衡（模拟按钮）。

4.  **Intent Queue (意图队列)**
    *   显示显示待执行的策略意图数量。
    *   当后端触发策略时（每5秒），这里的计数会实时跳动。

## 4. 常见问题

*   **Q: 为什么价格不更新？**
    *   A: 请确保后端 `./bot` 正在运行，并且没有因为错误退出。前端依赖 `http://localhost:8081` 的 API。

*   **Q: 如何连接真实钱包？**
    *   A: 修改 `configs/config.yaml` 中的 RPC 地址，并在环境变量或配置中填入真实私钥（注意安全）。将 `dry_run` 改为 `false`。

*   **Q: 为什么显示 "Simulated Tx Execution"?**
    *   A: 默认配置为 `dry_run: true`，处于影子模式，不会消耗真实 Gas。

---
**Enjoy your trading with Phoenix V3!**

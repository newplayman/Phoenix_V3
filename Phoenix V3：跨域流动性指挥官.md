

**产品说明 + 开发规范 + TODO 手册**

> 面向对象：
> 
> - 懂写代码，但不懂交易 / 区块链的人
>     
> - 未来要维护 / 扩展这个项目的工程师
>     
> - 产品 / 运营希望看懂系统在干什么的人
>     

---

## 第一部分：产品说明（给“人话版”的）

### 1.1 Phoenix V3 是什么？

一句话：  
**Phoenix V3 是一个自动在链上“摆摊收手续费”的机器人，听 CEX 的风向，自动在 DEX 上调整摊位位置、大小，并控制风险。**

你可以把它想象成：

- CEX = 高速公路上的监控摄像头（最快知道车流的地方）
    
- DEX = 路边摆摊的摊位（我们在这里做 LP 收手续费）
    
- Phoenix =
    
    - 眼睛：看 CEX 价格、成交
        
    - 耳朵：听链上 DEX 当前价格、流动性
        
    - 大脑：算出“摊位摆哪里、摆多大”
        
    - 手：发链上交易调整 LP
        
    - 盾牌：控制成本、避免踩雷（蜜獾币、MEV 抢跑）
        

---

### 1.2 它解决什么问题？

做 LP 有几个痛点：

1. **无常损失**：价格涨跌过头，手里的币组合比原来更亏。
    
2. **Gas 成本**：每次调整 LP 位置都要付链上手续费。
    
3. **“蜜獾币”陷阱**：有些币**能买不能卖**、高税、黑名单，一进就出不来。
    
4. **MEV 抢跑**：你往链上发一个“我要大幅调整 LP”的交易，机器人看到后会抢在你前后下单，从你身上赚一笔。
    

Phoenix V3 做的事情：

- **避免乱动**：只有“动了有意义、赚的钱比 Gas 高很多”才动。
    
- **提前躲坑**：新池子 / 新币要先过“体检”（蜜獾过滤 / PoolGuard）。
    
- **暴跌撤摊**：CEX 出现暴跌信号时，优先考虑“撤摊防身”，而不是继续硬扛。
    
- **操作可视化**：有 Dashboard 能看到：
    
    - 当前在哪些池子摆摊
        
    - 每个池子赚了多少手续费 / 花了多少 Gas
        
    - 现在机器人在干啥
        

---

### 1.3 几个必要概念（用大白话讲）

给完全不懂交易 / 区块链的工程师准备。

- **CEX**：中心化交易所，类似 Binance、OKX。就像一个互联网券商，撮合速度快。
    
- **DEX**：去中心化交易所，跑在区块链上的智能合约，任何人都可以把资金“放进去”做 LP。
    
- **LP（Liquidity Provider）**：在 DEX 里存入两种币，让别人来换，你赚手续费。
    
- **无常损失（IL）**：价格变化后，你手里的两种币的市值跟“什么都不做”比起来少了一部分，就叫无常损失。
    
- **Gas**：在链上执行一次操作的“油钱”，比如 Mint / Burn / Swap 都要给。
    
- **MEV**：别人看到你的交易提前进场 / 夹击你、从中薅走的一部分价值。
    
- **蜜獾币 / 蜜獾池**：
    
    - 只能买不能卖
        
    - 或者卖的时候会被收离谱的税
        
    - 或者项目方有一键抽走池子里钱的权力  
        → 简单理解：**很容易变成“进得去、出不来”的大坑**。
        

---

### 1.4 Phoenix 的整体工作流程（从人视角）

1. **配置策略**
    
    - 运营 / 产品定义：
        
        - 支持哪些链（比如 Base / Arbitrum / BSC）
            
        - 哪些池子可以用（合规 + 通过 PoolGuard 体检）
            
        - 每个池子的最大投入金额
            
        - 风险偏好（保守 / 中等 / 激进）
            
2. **机器人开始运行**
    
    - 持续从 CEX 拿价格和成交数据
        
    - 持续从 DEX 读 Pool 状态（当前价格、区间、流动性）
        
3. **ASMM 大脑算“理想摊位”**
    
    - 不考虑 Gas、风控，只算出数学上“最舒服的区间”
        
4. **策略层做“现实检查”**
    
    - 现在动摊位是否有意义？
        
    - 动多少才不浪费 Gas？
        
    - 目前资金是否够？风控是否允许？
        
    - 生成一个“意图”（Intent）：比如
        
        > 对 Pool X，将 LP 区间整体上移 5%，减少 30% 仓位
        
5. **调度器决定“什么时候动手”**
    
    - 看 Gas 价格、钱包 nonce 是否卡住、每条链当前负载
        
    - 排队，把高优先级意图先执行
        
6. **链上执行网关（Gateway）发交易**
    
    - 签名交易、发到 RPC 节点
        
    - 观察交易状态：Pending → Mined 或 Revert
        
    - 遇到失败重试 / 回退
        
7. **监控 & 风控**
    
    - 监控：交易成功率、PnL、Gas 使用、节点健康
        
    - 风控：亏损过大、交易连续失败、蜜獾检测出异常 → 自动熔断或撤摊
        

---

## 第二部分：系统架构总览（画给工程师看的）

### 2.1 模块列表（每个只干一件事）

1. **config（配置中心）**
    
    - 存：链列表、池子白名单 / 黑名单、策略参数、风控阈值。
        
2. **feed（CEX 数据源）**
    
    - 从 Binance / OKX 订阅实时价格和成交。
        
3. **dexstate（链上 DEX 状态监控）**
    
    - 定期读取 Pool 的当前价格、流动性、你的 LP 仓位。
        
4. **engine（ASMM 计算引擎）**
    
    - 输入：价格、波动率、持仓
        
    - 输出：建议的“LP 区间”和单边库存偏好。
        
5. **strategy（策略层）**
    
    - 对 engine 输出做现实修正：考虑 Gas、风险偏好等
        
    - 产出“意图”（Intent）。
        
6. **intent（意图队列 / 调度器）**
    
    - 给 Intent 排优先级、控制节奏。
        
7. **chain/gateway（链上网关）**
    
    - Nonce 管理、交易签名、发送、状态追踪、多 RPC 管理。
        
8. **chain/adapters（DEX 适配器）**
    
    - Uniswap V3 / Pancake V3 等，用统一接口操作 LP。
        
9. **poolguard（池子风控 / 蜜獾过滤）**
    
    - 进池前体检：检查是否蜜獾、合约风险。
        
10. **risk（资金风控）**
    
    - 最大回撤、单日最大 Gas、连续失败次数等。
        
11. **monitor（监控 & 告警）**
    
    - Prometheus 指标 + 日志 + 告警（Telegram / Webhook）。
        
12. **dashboard（Web 控制台）**
    
    - 给人看的 UI：状态、PnL、日志、开关。
        
13. **storage（存储层）**
    
    - Redis（缓存实时状态 / 信号）
        
    - Postgres/SQLite（历史记录、Rebalance 日志、PnL）。
        

---

### 2.2 简化架构图（文字理解版）

你可以这样理解调用顺序：

1. **feed + dexstate** → 填充“世界状态”（现在外面怎么了）
    
2. **engine** → 根据世界状态算出“理想状态”
    
3. **strategy** → 把“理想状态”和“当前状态”对比，得出“要做的事”（意图）
    
4. **intent** → 按优先级排队，决定“先做谁，什么时候做”
    
5. **chain/gateway + adapters** → 真正发交易
    
6. **poolguard / risk / monitor** → 在旁边监控、拉闸、告警
    

---

## 第三部分：模块详细说明（产品 + 开发双视角）

下面每个模块都按统一格式写：

- 职责（一句话）
    
- 输入 / 输出（用简单词）
    
- 实现要点（写给工程师）
    
- 和其他模块的关系
    

---

### 3.1 config 模块（配置中心）

**职责**：统一管理项目所有“可调节的参数”，不写死在代码里。

**输入 / 输出**

- 输入：配置文件（YAML/JSON）、环境变量。
    
- 输出：给其他模块提供结构体形式的配置对象。
    

**实现要点**

- 推荐用 YAML：`configs/*.yaml`
    
- 提供一个函数：
    

```go
type AppConfig struct {
    Chains    []ChainConfig
    Pools     []PoolConfig
    Strategy  StrategyConfig
    Risk      RiskConfig
    Monitoring MonitoringConfig
    // ...
}

func LoadConfig(path string) (*AppConfig, error)
```

- 支持启动时指定配置路径：`./phoenix-v3 -config ./configs/dev.yaml`
    
- 不懂交易的工程师只需要：
    
    - 把 YAML 解析成结构体
        
    - 不要擅自改里面的业务含义
        

**关系**

- 所有模块都只从 config 读参数，不自己乱写常量。
    

---

### 3.2 feed 模块（CEX 数据源）

**职责**：从 Binance / OKX 获取实时价格与成交数据，统一喂给 engine。

**输入 / 输出**

- 输入：CEX WebSocket 地址和 API Key（如果需要）
    
- 输出：标准化后的行情结构，例如：
    

```go
type Ticker struct {
    Symbol      string    // "BTCUSDT"
    Price       float64   // 最新价格
    Timestamp   time.Time // 行情时间
}
```

**实现要点**

- 为每个交易所写一个小 adapter：
    
    - `binance_client.go`
        
    - `okx_client.go`
        
- 用一个统一接口暴露：
    

```go
type Feed interface {
    Start(ctx context.Context) error
    SubscribeTicker(symbol string) (<-chan Ticker, error)
}
```

- 有一个“健康状态”结构体：
    

```go
type FeedStatus struct {
    Source       string
    Healthy      bool
    DelayMs      int64
    LastUpdateAt time.Time
}
```

**关系**

- engine / strategy 不关心这些数据从哪来的，只认 `Ticker`。
    
- monitor 订阅 `FeedStatus` 做健康检查。
    

---

### 3.3 dexstate 模块（链上 DEX 状态监控）

**职责**：定期读取链上 Pool 和 LP 仓位状态。

**输入 / 输出**

- 输入：
    
    - RPC 的读取权限
        
    - Pool 地址、你的钱包地址
        
- 输出：
    
    - 当前价格（tick）  
        -池子总流动性  
        -你的仓位（投入金额、区间）
        

**实现要点**

- 使用 go-ethereum (`ethclient`) 连接 RPC。
    
- 对 Uniswap V3 Pool 合约：
    
    - 调用 `slot0()` 获取当前 tick。
        
    - 调用 `liquidity()` 获取当前流动性。
        
- 封装为：
    

```go
type PoolState struct {
    ChainID     int64
    PoolAddress common.Address
    CurrentTick int64
    Liquidity   *big.Int
    // ...
}
```

**关系**

- engine 需要 `PoolState` 做计算。
    
- strategy 需要知道“当前 LP 区间”和“建议区间”的差距。
    

---

### 3.4 engine 模块（ASMM 计算引擎）

**职责**：纯数学模块——给定价格、波动率、持仓，算出“理想 LP 区间”。

**输入 / 输出**

- 输入：
    
    - CEX & DEX 价格
        
    - 波动率
        
    - 当前仓位
        
    - 策略参数（例如风险系数）
        
- 输出：
    
    - 建议的 LowerTick & UpperTick
        
    - 建议的仓位 Delta（偏多 / 偏空）
        

```go
type EngineInput struct {
    CexPrice   float64
    DexPrice   float64
    Volatility float64
    Position   CurrentPosition
    Params     StrategyParams
}

type EngineOutput struct {
    TargetLowerTick int64
    TargetUpperTick int64
    TargetDelta     float64
}
```

**实现要点**

- **重要**：engine 尽量做成纯函数，不要直接调用链/RPC。
    
- 所有和 Tick 相关的计算使用已经验证过的库 / 公式，不要手写魔法常量。
    

**关系**

- strategy 拿 engine 的输出，决定“要不要动、动多少”。
    
- engine 不直接发交易、不做风控。
    

---

### 3.5 strategy 模块（策略层）

**职责**：结合 engine 输出、当前实际仓位、Gas 成本、风控参数，生成“意图”。

**输入 / 输出**

- 输入：`EngineOutput`、当前 `PoolState`、`GasEstimation`、RiskConfig
    
- 输出：`Intent` 列表（可能 0 个、1 个、多个）
    

```go
type IntentType string

const (
    IntentRebalance  IntentType = "rebalance"
    IntentWithdraw   IntentType = "withdraw"
    IntentCollectFee IntentType = "collect_fee"
)

type Intent struct {
    ID           string
    Type         IntentType
    PoolID       string
    Urgency      int    // 数字越大越紧急
    Deadline     time.Time
    ExpectedPnL  float64
    // 具体参数：新 LP 区间、撤出比例等
}
```

**实现要点**

- 策略里要包含几条简单规则（写死也行，后期再参数化）：
    
    - 预期收益 <= GasCost * X → 不生成 Intent
        
    - 建议区间和当前区间差距 < Y% → 不动
        
    - 亏损超过设定阈值 → 优先生成 Withdraw Intent
        
- 没有观点就不发 Intent，**宁可不做，也不乱做**。
    

**关系**

- intent 模块只负责排队 & 调度，不管策略。
    
- strategy 不直接操作链，只产出 Intent。
    

---

### 3.6 intent 模块（意图队列 / 调度器）

**职责**：管理所有 Intent 的优先级和执行节奏。

**输入 / 输出**

- 输入：其他模块产生的 Intent（主要是 strategy）。
    
- 输出：排好序、准备执行的 Intent（交给 chain/gateway）。
    

**实现要点**

- 实现一个带优先级的队列：
    
    - 紧急撤离 > 重要 Rebalance > 提取手续费
        
- 定义一些全局节奏控制参数：
    
    - 每条链每分钟最多处理多少 Intent
        
    - 每个钱包每小时最多发多少交易
        
- 检查：
    
    - 当前 Gas 是否超过上限？
        
    - 钱包是否有 pending 的卡住交易？  
        → 这些时机要减缓或暂停出队。
        

**关系**

- chain/gateway 从这里拿“下一笔要执行的 Intent”。
    
- risk 模块可以对队列进行“清空 / 暂停”。
    

---

### 3.7 chain/gateway 模块（链上网关）

**职责**：把 Intent 变成链上交易，并可靠地发出去。

**输入 / 输出**

- 输入：Intent、钱包私钥、RPC 配置。
    
- 输出：交易哈希、最终执行结果（成功 / 失败、原因）。
    

**实现要点**

1. **Nonce 管理**
    
    - 为每个钱包维护本地 nonce 状态。
        
    - 避免并发时重复使用同一个 nonce。
        
2. **交易状态机**
    
    - 状态：Created → Signed → Broadcasted → Pending → Mined / Reverted / Dropped
        
    - Pending 超时 → 提高 gasPrice 重发，或认定为卡死。
        
3. **多 RPC 管理**
    
    - 读请求可以轮询最快 / 最稳定的那个。
        
    - 写请求选一个为主，失败后切换备份。
        
4. **接口建议**
    

```go
type TxStatus string

type TxResult struct {
    Hash    common.Hash
    Status  TxStatus
    Error   error
    // ...
}

type Gateway interface {
    Send(ctx context.Context, intent Intent) (*TxResult, error)
}
```

**关系**

- adapters 模块负责具体合约调用的 calldata 构造。
    
- gateway 只关心“发送交易 + 管理状态”。
    

---

### 3.8 chain/adapters 模块（DEX 适配器）

**职责**：为不同的 DEX（UniV3、Pancake V3 等）提供统一的 LP 操作接口。

**输入 / 输出**

- 输入：Intent 内容、Pool 配置信息。
    
- 输出：对应合约调用所需的参数（给 Gateway 打包成交易）。
    

**核心接口示例**

```go
type LiquidityManager interface {
    BuildMintTx(intent Intent) (*types.Transaction, error)
    BuildBurnTx(intent Intent) (*types.Transaction, error)
    BuildCollectTx(intent Intent) (*types.Transaction, error)
}
```

**实现要点**

- 使用 `abigen` 生成 Uniswap V3 的 Go 绑定代码。
    
- 把这些绑定封装在 `univ3/` 目录中。
    

---

### 3.9 poolguard 模块（池子体检 / 蜜獾过滤）

**职责**：对每个池子 / token 做合规体检，拦截潜在蜜獾和风险池。

**输入 / 输出**

- 输入：Pool 地址、Token 地址、链 ID。
    
- 输出：一个体检结果：
    

```go
type PoolRiskLevel string

const (
    RiskSafe     PoolRiskLevel = "safe"
    RiskWarning  PoolRiskLevel = "warning"
    RiskDanger   PoolRiskLevel = "danger"
)

type PoolCheckResult struct {
    PoolID      string
    Risk        PoolRiskLevel
    Reason      string
    LastChecked time.Time
}
```

**实现要点（无须懂交易，只需照规则实现）**

- 检查：
    
    - Token 是否标准 ERC20 / 有奇怪的 `transfer` 行为（比如手续费过高）。
        
    - 合约 Owner 是否有“随时改税 / 停止交易”的权限。
        
    - 合约是否 Proxy 可升级，且实现合约未知。
        
- 对检测规则的具体逻辑，可以以配置驱动：
    
    - 某些部署者地址列入黑名单
        
    - 某些 Factory / Router 地址列入白名单
        

**关系**

- strategy 在考虑某个池之前，必须先问 poolguard：“安全不？”
    
- risk 模块可基于 poolguard 的结果动态调整仓位上限。
    

---

### 3.10 risk 模块（资金与行为风控）

**职责**：用硬规则保护账户安全，防止“越亏越梭哈 / 系统失控”。

**输入 / 输出**

- 输入：PnL 数据、交易统计、poolguard 结果、monitor 指标。
    
- 输出：
    
    - 风控决策：继续 / 降档 / 熔断
        
    - 给策略的限制：单次最大调整比例、单日最大 Gas 等。
        

**实现要点**

- 典型规则：
    
    - 当日累计 Gas > X 美金 → 暂停新 Intent。
        
    - 任意池子最大回撤 > Y% → 禁止继续加仓，只允许撤摊。
        
    - 10 分钟内连续 5 笔交易失败 → 全局熔断，机器人只读不动。
        
- 风控结果暴露为状态：
    

```go
type RiskMode string

const (
    ModeNormal   RiskMode = "normal"
    ModeCaution  RiskMode = "caution"
    ModeFrozen   RiskMode = "frozen"
)
```

**关系**

- intent 模块执行前先看 RiskMode。
    
- dashboard 上要突出显示当前 RiskMode。
    

---

### 3.11 monitor 模块（监控 & 告警）

**职责**：记录、展示、报警。

**输入 / 输出**

- 输入：各模块上报的指标和日志。
    
- 输出：
    
    - Prometheus 指标端点
        
    - 告警消息（如 Telegram）
        

**关键指标建议**

- 交易相关
    
    - 每分钟提交交易数量
        
    - 成功率
        
    - 平均确认时间
        
- 资金相关
    
    - 每个池子的实时 PnL
        
    - 每日 Gas 消耗
        
- 系统健康
    
    - 各 RPC 节点延迟与错误率
        
    - CEX feed 延迟与断线次数
        

**关系**

- risk 模块可以订阅部分指标做“运维级熔断”。
    
- dashboard 展示部分监控指标。
    

---

### 3.12 dashboard 模块（控制面板）

**职责**：给人看的 Web 页面 + 操控按钮。

**内容建议**

- 总览页：
    
    - 当前总资金 / 总收益 / Gas 成本
        
    - 当前 RiskMode
        
- 池子列表：
    
    - 每个池子的：
        
        - 仓位、区间
            
        - 历史收益曲线
            
        - 最近 10 条操作记录
            
- 系统状态：
    
    - 各链 RPC 状态
        
    - CEX feed 状态
        
- 控制：
    
    - 一键暂停 / 恢复策略
        
    - 一键对某个池子“清仓撤摊”
        

---

## 第四部分：开发规范（写给工程师的硬规则）

### 4.1 语言 & 框架

- 后端语言：**Golang**
    
- 链交互库：`go-ethereum`
    
- 配置：YAML + `viper` / `koanf` 均可
    
- 数据库：开发阶段可以用 SQLite；生产建议 Postgres。
    
- 消息 / 队列：可以直接用内存 channel，后期如果复杂再换 Kafka / NATS。
    

---

### 4.2 项目目录结构（推荐）

```text
/phoenix-v3
├── cmd/
│   └── bot/                  # 主入口
├── internal/
│   ├── config/               # 配置加载、结构体定义
│   ├── feed/                 # CEX 数据源
│   ├── dexstate/             # 链上池子状态监控
│   ├── engine/               # ASMM 计算引擎(纯函数)
│   ├── strategy/             # 策略层(生成 Intent)
│   ├── intent/               # 意图队列 + 调度
│   ├── chain/
│   │   ├── gateway.go        # Nonce & Tx 状态机 + 多RPC
│   │   ├── adapter.go        # DEX 接口定义
│   │   └── univ3/            # UniswapV3 适配实现
│   ├── poolguard/            # 池子体检 / 蜜獾过滤
│   ├── risk/                 # 风控逻辑
│   ├── monitor/              # 指标 & 告警
│   ├── dashboard/            # Web UI 后端
│   └── storage/              # DB 封装
├── abi/                      # 合约 ABI
├── configs/                  # 配置文件
└── scripts/                  # 部署脚本
```

---

### 4.3 编码风格约定（核心几条）

1. **不得在业务逻辑中硬编码私钥、RPC 地址、Pool 地址**
    
    - 一律从配置文件 / 环境变量读取。
        
2. **每个模块只干一件事**
    
    - 不允许在 strategy 里直接连链、发交易。
        
3. **错误处理必须显式**
    
    - 禁止忽略 error。
        
4. **日志必须可检索**
    
    - 操作必须带 Context 信息：池子 ID、链 ID、钱包地址、策略版本号。
        
5. **所有对资金敏感的操作都要有审计日志**
    
    - 存 DB：时间、意图、交易哈希、结果、PnL。
        

---

### 4.4 安全规范（必须遵守）

- 私钥存储：
    
    - 不允许写到代码仓库。
        
    - 建议用环境变量 + 本地加密文件（或更安全方案）。
        
- 测试网络 / 正式网络需分开配置：
    
    - 不同 config 文件，不共用同一钱包。
        
- 初次上线必须先经历：
    
    - **本地模拟 → 测试网 → 小额主网** 的三阶段。
        

---

## 第五部分：TODO 分阶段任务清单（给项目经理用）

下面的任务都是“让不懂交易的人也知道自己要干嘛”的写法。

---

### Phase 0：基础环境 & 骨架（1–3 天）

目标：项目能编译运行，打印“我活着”。

-  初始化 Go 项目结构，按上面的目录建空包。
    
-  写一个最简单的 `cmd/bot/main.go`：
    
    - 加载配置文件
        
    - 初始化日志系统
        
    - 打印当前配置中的链列表
        
-  建一个 Dockerfile（可选）。
    

---

### Phase 1：链 & CEX 只读通路（基础 I/O）

目标：  
**“能从 CEX 和链上读到数据，并打印出来。”**

-  在 `config/` 中实现配置加载：
    
    - 链 ID、RPC URL、CEX WebSocket URL 等。
        
-  在 `feed/` 实现：
    
    - 连接 Binance/OKX WebSocket
        
    - 订阅一个固定交易对（如 BTCUSDT）
        
    - 每秒打印一次价格。
        
-  在 `dexstate/` 实现：
    
    - 使用 RPC 连接一条测试链 / 主网
        
    - 读指定 Pool 的 `slot0()`，打印当前 tick。
        
-  在 `monitor/` 做一个简单 HTTP `/healthz` 接口：
    
    - 返回 “ok” 和当前读到的区块高度。
        

工程师不需要懂 tick 含义，只需实现“能读到并打印/返回”。

---

### Phase 2：engine 纯计算上线（不发交易）

目标：  
**“能算出建议的 LP 区间，并在 Dashboard 上显示。”**

-  在 `engine/` 中实现：
    
    - 一个假想 ASMM 算法（简单版也行）：
        
        - 输入当前价格 + 波动率
            
        - 输出一个上下各 ±X% 的建议价格区间，再转成 Tick。
            
-  写一个简单的价格 → tick 转换函数（可先调用现成库）。
    
-  在 `dashboard/` 做一个简单页面：
    
    - 显示：
        
        - 当前链上价格
            
        - engine 算出的建议区间
            
        - 当前 LP 区间（暂时可以写固定值）
            

---

### Phase 3：Intent & Strategy 雏形（还不发链上交易）

目标：  
**“让系统会说：现在我建议做某个动作，但先记在账上。”**

-  在 `strategy/` 实现：
    
    - 根据 engine Output + 当前 LP 区间差距：
        
        - 差距 < 5% → 不做事
            
        - 差距 ≥ 5% → 生成一个 `IntentRebalance`
            
-  在 `intent/` 实现：
    
    - 内存优先队列，按 `Urgency` 排序。
        
    - 提供 `Enqueue(Intent)` 和 `Next()` 接口。
        
-  在后台定时任务里：
    
    - 每 10 秒把当前 Intent 队列打印一遍。
        
-  Dashboard 增加一个“待执行意图”列表。
    

---

### Phase 4：chain/gateway + DEX Adapter（开始在测试网动手）

目标：  
**“在测试网上能真实发出 Mint/Burn/Collect 交易。”**

-  用 `abigen` 生成 Uniswap V3 的 Go 绑定。
    
-  在 `chain/univ3/` 实现：
    
    - 构造 Mint / Burn / Collect 所需的调用参数（可以先写死）。
        
-  在 `chain/gateway/` 实现：
    
    - Nonce 管理（从链上读初始 nonce，本地 +1）
        
    - 发送交易，打印交易哈希
        
    - 轮询交易状态直到成功 / 失败
        
-  写一个小测试程序：
    
    - 从命令行读取参数（Pool、金额等）
        
    - 在测试网上 Mint 一点 LP，再 Burn 并 Collect。
        

此阶段不必接入 engine/strategy，先保证“手能动”。

---

### Phase 5：串起来跑“影子模式”（Paper Trading）

目标：  
**“全链路跑起来，但不真正发主网交易，只在本地模拟记录结果。”**

-  在 `strategy/` 增加一个参数：`dry_run`。
    
-  如果 dry_run = true：
    
    - strategy 仍然产生 Intent
        
    - intent 队列照样处理
        
    - chain/gateway 不发交易，只模拟：
        
        - 记一条“假交易”，写进数据库。
            
-  写一个简单的 `storage/`：
    
    - 用 SQLite 存 Intent 和模拟结果。
        
-  跑至少 24 小时，观察：
    
    - 生成了多少 Intent
        
    - 如果这些 Intent 全执行，理论上盈亏如何（先简单估算）
        

---

### Phase 6：poolguard & risk 上线（防坑 & 风控）

目标：  
**“在真金白银上之前，先把蜜獾过滤和硬风控搭好。”**

-  在 `poolguard/` 实现最基础体检：
    
    - 检查 Token `totalSupply` 是否正数
        
    - 检查合约是否在本地黑名单中
        
    - 把结果缓存起来，避免频繁重复检查
        
-  在 `risk/` 实现硬规则：
    
    - 当日 Gas 上限（从配置读）
        
    - 连续失败交易上限
        
    - 最大回撤占比（先用简单的计算方式）
        
-  strategy 在生成 Intent 前必须调用：
    
    - `risk.CanProceed(poolID, intentType)`
        

---

### Phase 7：小额主网实盘（限额 + 强监控）

目标：  
**“在主网上用小资金跑真实策略。”**

-  创建专门小资金钱包，单独配置文件。
    
-  在 `config/` 中为每个池设置：
    
    - 最大投入金额（比如 500 USD）
        
-  打开 real mode：
    
    - dry_run = false
        
    - 真实发交易，但 risk 限制都打开。
        
-  monitor 增加：
    
    - PnL 实时计算（简单版即可）
        
    - 告警：当日亏损超过 X% 时通知
        

---

### Phase 8：优化 & 增强（长期迭代）

目标：  
**“从能跑，走向跑得更好、更稳。”**

-  引入策略版本管理：
    
    - 所有交易记录带 `strategy_version`
        
-  在 `monitor/` 中增加：
    
    - 分池子的收益率统计
        
-  优化 chain/gateway：
    
    - 支持 Flashbots 或私有 RPC（按链配置）
        
-  dashboard 加：
    
    - 一键“清仓撤摊”按钮
        
    - 一键“暂停所有新 Intent，保留已有仓位”按钮
        

---

如果你愿意，下一步我可以直接帮你把 **Phase 0–1 的 Go 项目骨架** 生成出来（包括 `main.go`、`config` 结构体、几个空接口和 TODO 注释），这样你可以直接拷到本地开干。

---

## 第六部分：全局改进方案与技术规范

> 目标：把“场景 → 模块 → 数据契约”这一链路落到实处，并为后续 Phase 7/8 的实盘化奠定工程基础。

### 6.1 数据契约与模块通信

- 新增 `internal/contracts/` 包集中定义 Feed/DexState/Engine/Strategy/Intent/Gateway/Risk 的接口与结构体，所有模块只能依赖 contracts 包，禁止临时定义隐形字段。
- `internal/events/` 现包含 `MemoryStream` 与 `RedisStream` 两种实现：内存模式适合本地开发，Redis 模式（via `events.driver=redis`）会将事件写入 Redis Streams 并通过消费组分发，可持久化与重放。
- Feed、DexState 把标准化数据写入事件流，Engine/Strategy 通过订阅器读取，形成可重放的“世界状态”。目前行情侧已具备 Binance WS + CoinGecko REST 的多源汇聚（`internal/feed/aggregator.go`），按平均价输出统一事件，并在 `/healthz` 中展示每个源的状态。
- Gateway/Adapter 已接入 Uniswap V3 PositionManager：策略会把 token/fee/tick 信息写入 Intent Metadata，`internal/chain/univ3/adapter.go` 根据这些字段构造 calldata，`internal/chain/gateway/eth_gateway.go` 读取 metadata（target、calldata、value）后签名发送交易，并轮询回执。近期已修复 Adapter 对 `uint24/int24`（fee/tick）字段的 ABI 映射，统一使用 `*big.Int` 打包，避免 IntentExecutor 在真实模式报 `abi: cannot use uint32 as type ptr as argument`。DryRun 仍可通过 metadata 生成真实 calldata，用于测试网调试。
- `cmd/bot` 中新增 `startPoolWatcher`（定期拉取 Pool 状态并广播 `TopicPoolState`）与 `startIntentExecutor`（独立协程 + 简单节奏控制执行 Intent），逐步向文档描述的“调度器 + 风控”靠拢。
- Intent 队列、Gateway 之间的通信统一使用 gRPC/JSON-RPC 协议（或在单体模式下使用 channel 包装），并把协议版本号写入消息头，方便灰度兼容。

### 6.2 配置与版本治理

- `AppConfig` 增加 `StrategyVersion`、`SchemaVersion` 字段，所有生成的 Intent、交易记录都写入版本号。
- 配置文件仍放 `configs/*.yaml`，但运行时通过 Config Service（可用 etcd/Consul 或简单 HTTP 服务）下发，并支持热更新；核心配置变更需双人审批。
- 已提供 `config.NewManager`（基于 fsnotify 的文件热重载）与 `ValidateConfig` 校验逻辑，运行时可订阅配置变更事件更新策略和风控参数；未来接入远程配置中心时只需替换 Manager 的实现。
- 新增 `cmd/configcheck` 工具，可在 CI 或本地执行 `go run ./cmd/configcheck -config configs/config.yaml`，确保配置遵守 schema 与约束。
- 示例配置已切换到 Goerli（`chains[].rpc`），池子字段可直接写入 Token 地址、PositionManager、LP Amount；私钥通过 `BOT_PRIVATE_KEY` 环境变量注入，避免写入仓库。
- 在 CI 中引入 `cue` 或 JSON Schema 校验工具，对配置进行结构校验；`configs/` 目录通过 GitOps 审核，禁止在业务代码里写死常量。

### 6.3 存储与审计体系

- 存储层拆成三层：
  - 实时缓存：Redis Streams 存放最近 N 分钟行情、Intent 状态、链上事件，支持断点续跑。
  - 事务库：Postgres（或 SQLite 在开发期）记录 Intent、TxResult、PnL、Risk 决策；当前默认检测 `SUPABASE_DB_URL`，若存在即使用 Supabase Postgres，缺省时回落到本地 `phoenix.db`。
  - 历史仓库：对象存储（S3/OSS）沉淀池体检报告、回测结果、监控快照。
- storage 包需要 DAO 接口与 Migration 工具，保障 schema 变更可控；DryRun 与 RealRun 走同一写入路径，只是 Tx 状态不同。
- 目前 `TradeRecord` 已扩展 `chain_id/strategy_version/risk_mode/notional_usd/gas_cost_usd` 字段，为后续审计和 PnL 聚合提供基础数据。

### 6.4 风控策略联动

- 引入 `StrategyProfile` 映射：`RiskMode`（normal/caution/frozen）→ 引擎参数（区间宽度、目标仓位、Gas 上限），策略根据当前风控模式自动调整行为。
- `risk` 模块实现 Policy Engine，每条规则由配置驱动（条件、动作、冷却时间、严重级别），结果通过事件总线推送到 intent/strategy/dashboard。
- poolguard 结果可以动态更新单池资金上限，策略在生成 Intent 前必须引用最新的 PoolRisk。

### 6.5 执行链路一致性

- gateway 增加 deterministic replay：DryRun/RealRun 共用同一编码/签名路径，并能用离线数据完全重放本地决策。
- Intent 执行后必须回读链上仓位，与预期 tick 区间对比；若偏差超过阈值，立刻触发补救 Intent 或报警。
- Nonce、Gas 管理要写成状态机，关键状态（Created/Signed/Broadcasted/Pending/Mined/Failed）记录到 storage，方便审计。

### 6.6 CI/CD 与运维

- DevOps 需要完成 CI（GitHub Actions/GitLab CI）：`go test ./...`、`golangci-lint`、前端 `npm test`、配置 Schema 校验。
- 部署采用 Helm/Ansible，按环境（dev/dry-run、staging/测试网、prod/主网）打标签；通过 Feature Flag 控制 Gateway/PoolGuard 的灰度发布。
- 监控栈升级：Prometheus + Grafana + Alertmanager，指标统一前缀 `phoenix_*`，覆盖 feed 延迟、RPC 健康、Intent 成功率、PnL、Gas；提供 Webhook 给 dashboard 做一键熔断。
- 目前 `/healthz` 已返回 feed 健康信息（来源、延迟、最后更新时刻），后续可在此基础上扩展 Prometheus 指标与报警。

### 6.7 安全与密钥管理

- gateway 支持外部签名服务或 HSM，业务进程不再持有明文私钥；引入 Vault/KMS，授权由 DevOps 控制。
- 对每笔资金操作做签名校验与二次确认（多签或强制审批流程），审计日志必须包含操作者与审批链条。
- 池白名单、黑名单与密钥权限一起纳入审批流程，确保上线前完成安全 Review。

### 6.8 事件流与 Supabase 当前进展

- Bot 已经把 Binance 行情和 Intent 执行记录推送到事件流：默认使用 `MemoryStream`，若在配置中设置 `events.driver=redis` 并填写 `redis_url`，将切换为 Redis Streams，支持跨进程消费与持久化；订阅端自动通过消费者组（`redis_group`）来实现断点续播。
- storage 模块自动识别 Supabase Postgres 连接（`SUPABASE_DB_URL`），在生产环境可直接复用 Supabase 数据库，无需修改业务代码；本地未配置时继续使用 SQLite。
- 下一阶段：
  - 为 Redis 流添加 replay CLI，结合 Supabase 审计表验证 deterministic replay。
  - 视需要接入 NATS/JetStream（events driver 扩展点）。
  - 将 Risk/Monitor 接入事件流，基于事件驱动的熔断、报警、仪表盘刷新。

### 6.9 Sepolia 测试网闭环（行情 → 策略 → Intent → Gateway → Storage → Monitor）

- **链路基线已更新至 Sepolia：**
  - `configs/config.yaml` 切换为 `chain_id=11155111`，RPC 选用 `https://ethereum-sepolia.publicnode.com`（低延迟公网节点，可随时替换为 Infura/Alchemy），并在 pools[].`token0_decimals/token1_decimals` 中声明真实精度，供 PoolWatcher/Engine 还原 tick ↔ 价格。
  - `weth-usdc-03` 池使用真实合约：Factory `0x0227628f3F023bb0B980b67D528571c95c6DaC1c` 检测到的 0.3% 池 `0xC31a3878E3B0739866F8fC52b97Ae9611aBe427c`，`token0=USDC(0x1c7D4B196Cb0C7B01d743Fbc6116a902379C7238)`、`token1=WETH(0x7b79995e5f793A07Bc00c21412e50Ecae098E7f9)`、PositionManager `0x1238536071E1c677A632429e3655c799b22cDA52`。配置层现在强制遵守 “token0 < token1” 排列，默认下单规模为 `token0=10 USDC`、`token1=0.01 WETH`，可直接用于 dry-run/real-run。
- **密钥与数据库约定：**
  - 运行前导出 `BOT_PRIVATE_KEY=0x<your_private_key>`（务必通过环境变量注入，禁止写入 repo）。
  - Supabase DSN 示例：`export SUPABASE_DB_URL="postgres://postgres.<tenant_id>:<password>@<host>:6543/postgres"`（tenant/password 请从你自己的 Supabase/Supavisor 环境获取）。storage 在检测到该 DSN 后会自动开启 `prefer_simple_protocol` 并忽略 `AutoMigrate` 的 `relation already exists` 错误，`trade_records` 可稳定落库；`phoenix.db` 仅作为 fallback。
- **Dry-Run/Real-Run 流程：**
  1. `dry_run=true`：运行 `SUPABASE_DB_URL=... BOT_PRIVATE_KEY=... go run ./cmd/bot`，观察 `feeds`, `pool_watcher`, `intent_executor`, `gateway` 日志以及 `/healthz` 指标；确认多源行情、池状态监控、意图生成、Adapter calldata 构建链路全绿，并在 Supabase `trade_records` 中有 DryRun 记录。
  2. `dry_run=false`：补齐 Wallet 的 WETH/USDC 余额，写入真实下单 tick/amount，再运行同样命令确认 Gateway 成功拿到 ChainID=11155111，发送 tx 并在日志中记录 `tx_hash`，Supabase `tx_hash` 字段应落地。2025-12-11 的小额实盘中（wallet `0x39BF…c217`），IntentExecutor 连续发送多笔 mint：`0xcbb18aeb6e3a8f62576d442c1828b16be2d5080dd7108fcff02511a60c88ea61`、`0xe7d8088a6f0b6ed50bfd58ef786f7833ea027b156193456012bcc7bc498f587e`、`0xb921cf8ae5a5c4b879a41d0979cdd851e8e11d114cd109647fe5de91ea37892c`、`0x45f4c4a5baebf594ed437a7a64c1198a6e1e4f53c5c4cc8e4917d063f2b1e3bf`、`0x3a66b32c787599cfc7eb8c208de711383899fa989eeeecdfaa86dacbed012bc2` 均 `status=1`，证明 calldata + 授权链路 OK；当 USDC/WETH 减少后，后续 intent（如 `0x4cfd8451ba...`、`0x9ac00f6b...`）会以 `status=0` 快速失败。下一步需要接入余额/仓位检测，或在 `risk` 模块里设置“资产不足即熔断”，避免继续发送无效交易。
- **本地 dry-run 快照（2025-12-11 01:58 UTC+8）：**
  - Binance WS 仍返回 451，程序自动降级 REST 轮询并保留 CoinGecko 兜底，`/healthz` 能看到两路源的延迟与最后更新时间。
  - `dexstate` 连接 `https://ethereum-sepolia.publicnode.com` 成功，`PoolWatcher` 每 5s 输出来自链上的 slot0/liquidity；改用 ABI decoder 直接读取 int24 tick 与 `uint128 liquidity`，并结合配置中的 token decimals 即时计算 DEX 价格（例：TUSD(6)/WETH(18) 且 ETH≈3,188 时，tick 通常在 `~195k` 左右，dexPrice≈$3,188）。若你看到 tick 在 `-80k` 附近但 dexPrice 显示“看似正常/或极端异常”，通常意味着池初始化 `sqrtPriceX96` 时没有做 decimals 转换。
  - 策略每 ~5s 生成 Intent（ID `intent-1765389498233912705` 等），`IntentExecutor` 执行时输出 `Dry Run: Simulated Tx Execution`，adapter 已能够基于 metadata 构建 calldata。
  - Monitor/API（:8080）成功启动，`/healthz` 可用于 Prometheus 抓取；在补齐 DSN 后，`trade_records` 直接写入 Supabase（`docker exec supabase-db psql -c 'select * from trade_records'` 可查看）。
- **现有资产与 Swap 进度：**
  - 钱包 `0x39BF...c217` 余额：`0.34 WETH`（`WETH.deposit` tx `0x01c6693b...` 后剩余）+ `400.000016 TUSD`；自 12 月 12 日起启用持久授权（`TUSD/WETH approve PositionManager`）与链上余额风控：IntentExecutor 在 RealRun 前会调用 Gateway 的 ERC20 `balanceOf` 校验 `token0/token1` 余额，不足则自动熔断 intent，避免再出现“资金不足仍发交易”导致的 gas 浪费。
  - 2025-12-12 12:05 UTC+8 运行 `BOT_PRIVATE_KEY=... SUPABASE_DB_URL=... go run ./cmd/bot`（`dry_run=false`）：Binance WS 仍 451，程序自动 fallback REST + CoinGecko；IntentExecutor 以新 `tusd-weth-005` 配置（每笔 100 TUSD + 0.0315 WETH）连续发送 6 笔 mint，全部成功写链：`0xf9817ca3...`, `0xaf3e025f...`, `0x7654e22b...`, `0x255256c3...`, `0x1772bf55...`, `0x773ddd06...`，gas 介于 288k–407k。Supabase `trade_records` 已新增 `id=257~262`，状态仍为 `pending`（尚未实现 receipt watcher 回写），但 tx hash/nonce/时间戳可供追溯。
  - 2025-12-12 12:05 UTC+8 运行 `BOT_PRIVATE_KEY=... SUPABASE_DB_URL=... go run ./cmd/bot`（`dry_run=false`）：Binance WS 仍 451，程序自动 fallback REST + CoinGecko；IntentExecutor 以新 `tusd-weth-005` 配置（每笔 100 TUSD + 0.0315 WETH）连续发送 6 笔 mint，全部成功写链：`0xf9817ca3...`, `0xaf3e025f...`, `0x7654e22b...`, `0x255256c3...`, `0x1772bf55...`, `0x773ddd06...`，gas 介于 288k–407k。Supabase `trade_records` 已新增 `id=257~262`，现已由 Gateway 内置的 receipt watcher 实时回写 `status=success/failed`，便于审计链路闭环。
  - 同一日 12:03 UTC+8 曾以 `amount0=1,000 TUSD` 直接跑实盘，因 PositionManager 授权已被抢占且钱包余额不足，mint tx `0x9b8af63c...`、`0xf1a4eb4e...`、`0x09274dea...`、`0xdb713708...`、`0x4947b1a...`、`0x31fda1d...` 均以 `status=0` 回滚（220k gas）。结果促生了新的 TODO：① 将 `pools[].amount0/amount1` 调整为 100 TUSD / 0.0315 WETH；② 追加授权并实现余额风控，避免继续浪费 Gas。
  - 已对 `SwapRouter 0x3bFA...` 执行授权（tx `0x394303a8...`），但多次调用 `exactInputSingle`（`0x24ed0599...`,`0xfa9be808...`,`0xacbb684e...`,`0x058734da...`）均在 26K gas 左右立即回滚，`Quoter` 也返回空 revert，判断为路由合约或池子在当前 RPC 上暂未接受 WETH→USDC swap；需进一步抓包（如 debug_traceTransaction）或改用其他可用路由/直接 pool.swap callback 合约。后续动作：①寻找可用的 Sepolia 稳定币 faucet/可授权 minter；②或部署轻量 `SwapHelper` 合约直接走 `UniswapV3Pool.swap`，用来补齐 USDC/WETH 余额后再继续实盘。
  - 进一步验证官方 `SwapRouter02 0x68b3...`：地址在 Sepolia 为 EOA（`eth_getCode` 返回长度 0），`approve` + `exactInputSingle`（tx `0xf9e5c4c0...`、`0x423b4e25...`）虽然 `status=1`，但因目标无代码实际未执行 swap，也无事件日志，所以无法依赖。→ 必须自部署 SwapHelper 或切换到真实存在的 Router（可在测试网手动部署 v3-periphery）。
  - Faucet 路径：QuickNode Multi Token Faucet（https://faucet.quicknode.com/ethereum/sepolia）提供含 USDC 的测试代币，登录 GitHub 后可领取；若 Faucet 使用的 USDC 地址不同，可在配置中新增 `token0/token1`，或自建一个 faucet token 池以验证 LP 全链路。现在也可以直接部署仓库新增的 `contracts/TestUSD.sol`（TUSD）替代 USDC：2025-12-11 完成合约部署 `TUSD=0x3E49DB88bC85135b6F716E5CD573cDd42b8640c5` 与首个 fee 3000 池（`0x041EB5542E83ca11AaD466a5BaE2F8570aF78E13`，仅作历史参考）；fee=500 的 TUSD/WETH 池需要按 **rawPrice=token1Raw/token0Raw** 初始化（脚本已修复 decimals 转换），并选择覆盖当前价格的 tick 区间（例如 ETH≈3,180 时，约为 `[195180, 196190]`，仅作示例）。`configs/config.yaml` 与 `DEPLOY.md` 已同步至 `pools[0].id=tusd-weth-005`，Phoenix Bot 运行即会盯住新池。
  - **SwapHelper 合约：**
    - 新增 `contracts/SwapHelper.sol`，实现 `swapExactInputSingle` + `uniswapV3SwapCallback`，可以直接把 WETH 兑换成 USDC（或任意 token0/token1 组合），无需依赖缺失的 Router。
    - **部署记录**：使用 Docker `solc:0.8.20` 编译后，通过 python/web3 脚本部署到 Sepolia，合约地址 `0x6cA5769926FdcC8D39F78E9f00527e6D4415DBe1`（tx `0x1134f4e3529e839fbfd4eaee1b89c43484ec5de7a239b60ffb400cb7413fa8ff`）。随后给合约授权 WETH（tx `0xb63201c8c906567175635e4a59a76c2937910e5578db9b55116356022233445d`），再调用 `swapExactInputSingle` 将 0.01 WETH 换成 ~20.239098 USDC（tx `0x01af31118987c9da0beba8c0f16e9f98e5d35f8d870d65cf7a5c97e65e0d3cf0`），验证池子与回调链路正常。
    - 使用方式：① 用户对 SwapHelper `approve` tokenIn；② 调用 `swapExactInputSingle(pool, tokenIn, tokenOut, amountIn, limit)`（`limit=0` 时会自动套用全区间价格）；③ SwapHelper 在回调中向池子支付 tokenIn，并将 tokenOut 返还给调用者。部署可用 Remix/foundry，将池地址 `0xC31a...` 与 token 地址作为参数传入即可。
    - 若一次性兑换 0.05 WETH，会触发池端的价格保护（tx `0x2a63a04528ab0271ec3409135b7b0199d1c50ae3ff96e1fdf8d4ba0862331ecf` Revert: “SPL”），说明当前池深度有限，需要分批或额外注入流动性。
- **监控补充：**
  - `/healthz` 现在还会连带输出 Binance/CoinGecko Feed 状态 + PoolWatcher 最近一次 slot0 的区块高度；监控进程（Prometheus/Grafana）接入时可以直接抓取。
  - 若命中 Binance WS 451（地理限制）会自动 fallback REST，后续在海外 VPS 部署即可恢复 WS 低延迟。
- **后续动作：**
  - 补充 `Phase 7` TODO：`(1)` 完成 Sepolia dry-run 回归记录、`(2)` 关掉 dry_run 的真实 Intent 测试、`(3)` 修复 Supabase 连接（确认 tenant/user）后再观测写入延迟。
  - 端到端闭环拉齐后，再接真实 Gateway Adapter + 监控指标 Pack，进入测试链真实资金演练。

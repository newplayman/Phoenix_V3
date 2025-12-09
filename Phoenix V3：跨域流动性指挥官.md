

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
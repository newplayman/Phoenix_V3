# Phoenix V3 · Phase 1

**窄区间 LP 自动化 + 自动换币 Rebalancer（无对冲腿）  
产品说明 & 开发规范**

> 核心变化：
> 
> - 与之前版本相比，本阶段**新增自动 Rebalancer 模块**，
>     
>     - 在 LP 意图执行前，自动通过链上 Swap 把钱包内资产转换成 LP 需要的 token 组合；
>         
>     - 目标是“尽量不浪费钱包里已经有的币”，提高资金利用率。
>         
> - 仍然**不做 CEX/perp 对冲腿**，只在链上做 LP + Swap。
>     

---

## 1. 阶段一范围与目标

### 1.1 产品目标

1. **币种与池子范围**
    

- 主流币对（为稳定收益主力）：
    
    - 如：ETH/USDC, ETH/USDT, SOL/USDC, ARB/ETH, ARB/USDT 等。
        
- 二三线币对（白名单制，负责拉高收益上限）：
    
    - 条件：
        
        - 已上线 ≥1 家头部 CEX（Binance/OKX/Bybit/Coinbase 等）；
            
        - 上线时间 ≥ 3 个月；
            
        - TVL ≥ 1–5M 美金（具体值后续按链配置）；
            
        - 合约提前人工审过，无明显蜜獾/高税逻辑。
            
- 不碰：
    
    - 未上主流 CEX 的小妖币；
        
    - 已知高税/蜜獾/黑名单池。
        

2. **收益与风险风格**
    

- 收益目标（长期视角）：
    
    - 主流币 LP 组合做到**月化 5–10%** 是比较健康的 base case；
        
    - 加入白名单二三线后，在行情配合下，月化目标可上探 **10–20%+**。
        
- 风险承受：
    
    - 接受“有回撤”，但通过风控限制**单日/单月最大回撤**；
        
    - 不做对冲腿：不追求“无风险套利”，而是“稳健主动 LP 做市”。
        

3. **自动化程度**
    

- Bot 自动完成：行情 → 策略决策 → 生成 Intent →  
    → (新) Rebalancer 计算并执行 Swap → 构造 LP 交易 → 上链。
    
- 不再要求人工补仓：
    
    - 有币，就帮你自动换到合适比例再构建 LP；
        
    - 钱包中某个 token 余额不足时，会尽力用**其他 token 兑换**，在预算与风控范围内最大化 LP 规模。
        
- 支持 Web/API 手动控制：
    
    - Pause/Resume 某个池子或全局；
        
    - 手动清仓某池子；
        
    - 调整利用率上限等。
        

4. **利用钱包资产的原则**
    

> “充分利用”不等于“压干榨尽”，而是有约束地提高利用率。

- 每个池子有一个目标资金使用比例（`target_utilization_pct`）。
    
- 全局设置一个**最小预留现金/稳定币比例**（`min_idle_cash_pct`），保证钱包不会被抽干。
    
- Rebalancer 在这两个约束下，尽量分配和转换资产用于 LP。
    

---

### 1.2 明确不做（Phase 1）

- 不做任何 CEX/perp 对冲腿（Delta-neutral 等）。
    
- 不做跨链资金调度（只使用当前链钱包内资产）。
    
- 不做复杂蜜獾/高税自动检测（仅靠白名单+人工审查）。
    
- 不做复杂的 ML 策略，全部是规则 + 参数化的决策树。
    

---

## 2. 整体架构与数据流

在当前 Phoenix V3 代码基础上，Phase 1 的核心运行链路更新为：

> **Feed & DexState** → Engine → Strategy → **Rebalancer** → Risk & PoolGuard & BalanceGuard → Gateway + Univ3Adapter → Storage & Monitor

用更口语化描述，就是：

1. **行情&状态**：
    
    - 从 CEX 获取价格 & 波动率；
        
    - 从链上获取池子状态 & 当前 LP 仓位。
        
2. **策略决策**：
    
    - 生成“目标 LP 区间+想投入的资金比例”的 Intent（不管钱包现在有啥币）。
        
3. **Rebalancer（新增模块）**：
    
    - 看钱包当前所有 token 余额；
        
    - 看目标 LP 所需 token0/token1 数量；
        
    - 计算用哪些 token swap 成目标组合，以及 swap 的路径与数量；
        
    - 在风控约束下，先执行 Swap 交易。
        
4. **余额检查与 LP 构建**：
    
    - Swap 完后再走 BalanceGuard 检查余额是否满足 Intent；
        
    - 满足 → 构造 Uniswap V3 Mint/Burn 交易并上链；
        
    - 完成后记录到 Storage，用于回溯与风控统计。
        

---

## 3. 策略逻辑（Phase 1 带自动换币版本）

> 这一节偏“产品+量化规则”，方便你以后给别人解释“这系统实际上在做什么”。

### 3.1 池子与资产选择（Universe）

跟之前建议一致，只要记住一点：**池子的 Universe 是人工+配置控制的白名单。**

#### 3.1.1 主流币池

- 例子：SOL/USDC, ETH/USDC, ARB/USDT 等。
    
- 条件（可写配置）：
    
    - TVL ≥ 5–10M；
        
    - 24h vol ≥ TVL 的 10–30%；
        
    - 池子合约为官方常用池子（非奇怪 clone）。
        

#### 3.1.2 二三线币池（ 白名单 alt）

- 例子：某些 L2 公链代币/热门生态币/老牌 DeFi 项目代币。
    
- 条件：
    
    - 上主流 CEX；
        
    - 上线时间≥3 个月；
        
    - TVL ≥ 1–5M；
        
    - 手工检查合约没有黑名单函数/高额税收。
        

#### 3.1.3 组合约束

- 单币敞口限制：
    
    - 主流币：≤ 30% 总资金；
        
    - 二三线：≤ 10% 总资金。
        
- 单池子限制：
    
    - ≤ 15% 总资金。
        
- 二三线总篮子限制：
    
    - ≤ 30% 总资金。
        

---

### 3.2 区间策略（Range Policy）

仍然采用“**波动率分档 → 区间宽度模板**”方式：

1. 计算 ATR% / 波动率 σ（如最近 7 日）。
    
2. 按阈值分成低/中/高波动档。
    
3. 依据是否主流/二三线，选择对应的区间宽度模板及偏移方式。
    

示例（可以直接做成配置）：

`range_policies:   major_default:     atr_low: 0.03     atr_mid: 0.06     widths:       low: 0.05    # ±5%       mid: 0.10    # ±10%       high: 0.20   # ±20%    alt_default:     atr_low: 0.05     atr_mid: 0.10     widths:       low: 0.10       mid: 0.20       high: 0.30`

---

### 3.3 Rebalance / 撤池触发（策略侧）

触发逻辑保持和前版类似，但**多了一件事：现在生成 Intent 时不需要考虑钱包当前具体持仓结构**（因为有 Rebalancer）。

触发包括：

1. **价格靠近区间边缘**：
    
    - 价格接近上下边界一定比例（例如 15–25%）+ 有短期趋势，就触发：
        
        - REBALANCE（移动区间）；或
            
        - EXIT（撤池）。
            
2. **时间效率触发**：
    
    - 某池子过去 T 天年化 fee < X%，认定区间效率不高 → 扩大区间或撤池。
        
3. **回撤/IL 触发（简单版）**：
    
    - 粗略估算 LP vs HODL 差距，发现长期处于负收益，可选择扩大区间或退出。
        

---

## 4. 新增 Rebalancer 模块设计（重点）

### 4.1 目标与约束

**目标：**

- 给定一个 LP Intent（目标区间+目标本金占比），
    
- 在**不动 CEX，对当前链钱包内资产做比例调整**：
    
    - 尽量将“闲置资产”转换成 LP 所需的 token0/token1；
        
    - 尽量减少未利用资金，提高资金使用率。
        

**硬约束：**

1. 只使用**当前链钱包内的资产**，不做跨链、不调用 CEX。
    
2. 不使用杠杆、不借贷。
    
3. 不突破全局风控：
    
    - 保留最小闲置稳定币比例（`min_idle_cash_pct`）；
        
    - 不超出某池子的最大资金占比；
        
    - 不超出 alt 篮子资金占比。
        
4. Swap 必须满足：
    
    - 滑点 ≤ `max_swap_slippage_pct`（例如 0.5–1%）；
        
    - 单笔 swap 规模 ≤ 账户总资金的 `max_swap_trade_pct`（例如 5%）；
        
    - 单日累计 swap 成交量 ≤ `max_daily_swap_volume_pct`（例如 100–200% 资金，相当于不能疯狂洗交易）。
        

---

### 4.2 数据流与接口

#### 4.2.1 输入

`type RebalanceInput struct {     Intent        Intent       // 策略产生的操作意图     WalletBalance Balances     // 当前钱包所有 token 余额     Prices        PriceSnapshot// 各 token 的 USD 估值     PoolConfig    PoolConfig   // 本池配置     RiskState     RiskSnapshot // 风控当前状态（剩余预算） }`

- `Intent` 中至少包含：
    
    - `PoolID`
        
    - 目标区间 Range（tickLower, tickUpper）
        
    - 目标投入资金比例 `TargetNotionalPct`（相对总资金）
        

#### 4.2.2 输出

`type RebalancePlan struct {     Swaps      []SwapAction   // 需要执行的 swap 列表（可为空）     FinalLP    LPAction       // 最终 LP 操作计划（金额、token0/token1 数量）     UtilizedPct float64       // 实际利用总资金比例 }`

- `SwapAction` 示例：
    

`type SwapAction struct {     FromToken   common.Address     ToToken     common.Address     AmountIn    *big.Int     MinAmountOut *big.Int     Route       string // "router_v2", "router_v3", "1inch", etc.     SlippagePct float64 }`

- `LPAction` 示例：
    

`type LPAction struct {     PoolID     string     Amount0    *big.Int     Amount1    *big.Int     Range      Range // tickLower, tickUpper     SlippagePct float64 // LP mint 也可以做一点 slippage 控制 }`

---

### 4.3 算法步骤（逻辑说明）

以单池子 Intent 为例，Rebalancer 运行流程：

#### Step 1：估算“目标投入资金量”

1. 读取**当前账户总资产估值**（按 Prices 折算为 USD）。
    
2. 根据 Intent 的 `TargetNotionalPct` 和 PoolConfig 的 `max_cap_pct`：
    

`target_notional_usd      = min(TargetNotionalPct, pool.max_cap_pct) * total_equity`

3. 考虑全局预留现金要求 `min_idle_cash_pct`：
    
    - 若目标 notional 导致闲置资金低于该比例，则压缩目标规模。
        

#### Step 2：根据目标区间计算所需 token0/token1 数量

1. 使用 Uniswap v3 的定价公式（有现成 go 库可以用）：
    

- 输入：currentPrice, tickLower, tickUpper, targetNotionalUsd → 求解所需 amount0、amount1。
    

2. 这是 LP 精确所需的 token 组合 `(need0, need1)`。
    

#### Step 3：读取当前钱包余额 & 计算缺口

- 假设当前钱包有：
    
    - `have0`, `have1` 对应池子 token；
        
    - 还有其他 token（其他池子用币/稳定币）。
        
- 计算：
    

`delta0 = need0 - have0 delta1 = need1 - have1`

可能情形：

- delta0 > 0：token0 不够；
    
- delta1 < 0：token1 多余；
    
- 同时两个都 > 0：两个币都不够，需要用“其他资产”来换；
    
- 两个都 < 0：两个币都太多，可选择只用一部分来建 LP，或者把多余留给其他策略。
    

#### Step 4：构造 Swap 需求（高层逻辑）

目标是：

> 在不突破风控的前提下，选择合适的“支付 token 池”，尽量把币换成 LP 需求的组合。

**支付 token 优先级**（可配置）：

1. 先用**闲置稳定币**（USDC/USDT/DAI…）换；
    
2. 再用**当前池子的另一侧 token**（例如有太多 token1 来补 token0）；
    
3. 再用**其他主流币**；
    
4. 最后才触碰二三线币（一般不建议通过抛 alt 来补主流池）。
    

**Swap 规划规则：**

- 每一步 swap 的目标，是尽量将 `(have0, have1)` 逼近 `(need0, need1)`，
    
- 但全过程中：
    
    - 不触发 `min_idle_cash_pct`；
        
    - 不超过 `max_swap_trade_pct` 每笔限制；
        
    - 不超过 `max_daily_swap_volume_pct` 当日累计。
        

举一个简化例子：

- 需要：1 ETH + 等值 USDC；
    
- 钱包里有：
    
    - 0.2 ETH
        
    - 很多 USDC
        
    - 其他资产若干  
        → Rebalancer 会倾向于：
        
- 用 USDC 在 DEX 上买 0.8 ETH（受滑点与单笔额度限制）；
    
- 然后留下足够 USDC，与 1 ETH 组合建 LP。
    

再比如：

- 需要很多 ETH + USDC；
    
- 钱包主资产是大量 ARB 和 USDC；  
    → Rebalancer 可策略：
    
- 先卖一部分 ARB → USDC；
    
- 再用 USDC → 部分买 ETH；
    
- 组合成 LP 所需的 (ETH, USDC)。
    

#### Step 5：路径与滑点控制

**路径选择**（Phase 1 简化版）：

- 可以先支持 1–2 种路由：
    
    - 官方 Router（Uniswap V3 或 V2+V3 混合）；
        
    - 或预留参数接入 1inch/0x 等聚合器。
        

**滑点处理：**

- 从 DEX router/聚合器预获取报价，
    
- 按配置 `max_swap_slippage_pct` 计算 `minAmountOut`，写入 SwapAction；
    
- 若预估滑点已超过上限，直接拒绝本次 swap（对应 Intent 记录“价格太差，放弃执行”）。
    

#### Step 6：输出 RebalancePlan

- 汇总所有合理的 SwapAction，和最终 `LPAction` 中可用的 `Amount0/Amount1`。
    
- 若在预算内无法达到最初目标的 `target_notional_usd`，
    
    - 可按比例缩小 LP 规模，保证“宁可小一点，也不要强行满仓”。
        

---

### 4.4 执行顺序与事务性

1. Rebalancer 生成 `RebalancePlan`。
    
2. Executor 依序执行 `Swaps` 中的交易：
    
    - 每次发送 tx 并且等待成功/失败回执（或者绑定合理超时）。
        
    - 若中间有一笔 swap 失败：
        
        - 立即停止后续 swap；
            
        - 重新查询余额，决定：
            
            - 是否仍能建一个缩小规模版本的 LP；
                
            - 或直接放弃本 Intent。
                
3. Swap 执行成功后：
    
    - 再调用 BalanceGuard 确认 `Amount0/Amount1` 足够 LPAction。
        
    - 然后构造 LP Mint/Burn 交易。
        

> **注意：**  
> Phase 1 不要求“跨交易的强 ACID 事务”，但必须保证：
> 
> - 在任何失败路径下，账户不会被扣出超限资产；
>     
> - 风控统计信息必须如实记录成功/失败与 gas 成本。
>     

---

## 5. 其他模块（简略）

其它模块与前版总体相同，只标记 Rebalancer 引入后影响的边界。

### 5.1 Strategy

- 不再关注“当前钱包具体有多少某种币”，只负责：
    
    - 选池子；
        
    - 选区间；
        
    - 设定 `TargetNotionalPct`（比如当前总资产的 5%、10% 等）；
        
    - 决定 Action（MINT / REBALANCE / EXIT）。
        

### 5.2 BalanceGuard

- 从“前置过滤器”升级为“最后保险”：
    
    - Rebalancer 之后再检查一次余额；
        
    - 防止因为报价变化 / gas 等原因导致实际可用余额略不足。
        

### 5.3 RiskManager

Rebalancer 引入后，新增监控维度：

- `daily_swap_volume_usd`：累计 swap 成交额。
    
- `daily_swap_count`：swap 笔数。
    
- `swap_gas_cost_usd`：swap 相关 gas 消耗。
    

新增限制（可配置）：

- `max_daily_swap_volume_pct`：当日多次换币的总规模不能超过总资金的某倍。
    
- `max_daily_swap_count`：防止某些异常条件触发频繁换币。
    

---

## 6. 风控机制（重点针对“自动换币”新增风险）

重点风险：

1. **滑点 & 定价风险**：
    
    - 自动换币大量在 DEX 上操作，容易遇到低深度 / 高滑点。
        
2. **MEV & Sandwich**：
    
    - 机器人行为固定，路由固定，容易被 MEV 挖矿前后夹。
        
3. **交易爆量风险**：
    
    - 若策略逻辑有 bug，可能频繁小额 swap 造成 gas 和手续费的巨大浪费。
        

针对这些，Phase 1 需要：

- **限价+滑点控制**：
    
    - 所有 swap 必须设置 `minAmountOut`，并使用合理的 TTL。
        
- **分级限额**：
    
    - 单笔 swap 不超过资金的 X%；
        
    - 单日 swap 不超过资金的 Y%；
        
    - 单日 swap 次数 < N（比如 50）。
        
- **熔断机制**：
    
    - 若连续几笔 swap 亏损超过某个门槛（比如价格异常），可暂时禁用 Rebalancer。
        
- **审计日志**：
    
    - Storage 中记录每次 swap 的 from/to, amountIn/amountOut, gas, 价格偏离度等。
        

---

## 7. 测试要求（针对 Rebalancer 增强）

### 7.1 单元测试

- Rebalancer 核心逻辑：
    
    - 给定各种 wallet 余额 & targetNotional，测试：
        
        - 生成的 SwapAction 是否合理；
            
        - 在边界条件（余额极少、某币为 0、limit 很紧）下能否安全降规模；
            
        - 不会超出 `min_idle_cash_pct` 等硬限制。
            
- RiskManager：
    
    - Swap 相关的计数（volume/count/gas）在多笔执行后的状态是否正确；
        
    - 超限时能否拒绝新的 Rebalance 请求。
        

### 7.2 集成测试（Testnet）

在测试网做：

1. 准备一个钱包：
    
    - 放一些 ETH/USDC/ARB 等混合资产；
        
    - 特意设置“不完全符合 LP 所需比例”。
        
2. 手动触发一个 LP Intent（比如用 admin API）；
    
3. 验证：
    
    - Rebalancer 是否会自动调用 Swap 把不合适的资产转换为 LP 所需组合；
        
    - Swap 完后是否成功建 LP；
        
    - 若故意提高 `max_swap_slippage_pct` 很小，让 swap 失败，会不会优雅降级成“不执行该 Intent”。
        

### 7.3 回测 / 仿真（简易版）

Phase 1 可先做简易的**“哑仿真”**：

- 用历史价格曲线 + 假定的 Dex 价格；
    
- 模拟 Rebalancer 在不同市况下换币次数、换币规模和理论损益；
    
- 初步评估策略参数（尤其是 swap 相关的限额）是否合理。
    

---

## 8. 开发 TODO（带 Rebalancer 版）

按优先级给一个落地清单：

### P0（上 testnet 前必须）

1. **代码统一 & 可编译**：
    
    - [x] 修复 dev 分支缺失模块 / 接口不匹配问题（同之前建议）。
        
2. **Rebalancer 核心数据结构 & 逻辑实现（不要求最优路由，但要安全）**：
    
    - [x] 支持至少：
        
        - [x] 利用闲置稳定币 → 目标池子 token；
            
        - [x] 利用池子另一侧 token → 补足不足一侧。
            
3. **Swap 执行链路接到 Gateway**：
    
    - [x] 支持通过指定 Router 调用 swap，设置滑点和 TTL。
        
4. **RiskManager 增加 swap 维度统计与限制**。
    - [x] Done.
    
5. **BalanceGuard 改到 Rebalancer 之后，再做最终检查**。
    - [x] Done.
    

### P1（主网小额实盘前建议）

6. **Rebalancer 边界处理完善**：
    
    - 在余额极端不平衡、限额紧的情况下，能自动缩小 LP 规模而不是完全 fail。
        
7. **Dashboard 中增加 Rebalance/SWAP 可视化**：
    
    - 让你看得见每天换币次数和规模。
        
8. **简易仿真工具**：
    
    - 模拟不同参数组合下 Rebalance 频率与换币损耗。
        

### P2（优化）

9. 多路由支持（官方 Router + 1inch/0x 等，择优报价）。
    
10. 更细的 MEV 风险控制（比如在 gas 用量/priority fee 上做简单策略）。
    

---

## 最后一段小结

你这版需求的本质是：

> **“我愿意接受不对冲，只要系统在可控风险内，自动帮我把钱包里的资产换到合适结构，去高效跑 LP。”**

这套文档就是围绕这个目标重构出来的规范：

- 明确引入了 **Rebalancer 模块**，
    
- 但始终用 **资金利用率 + 多重限额 + 熔断** 把它拴住，
    
- 避免“自动换币”变成“自动作死”。
    

如果你愿意，下一步我们可以做更具体的一步：  
**以一个你最熟的池子（比如 SOL/USDC 或 ARB/ETH）为例，写出一份完整的 `pool` 配置 + Rebalancer 参数示例**，  
你就可以直接把它放进 `config.yaml`，作为 Phase 1 版本的第一个“实盘模板池子”。
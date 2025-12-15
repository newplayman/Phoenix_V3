# Phoenix V3 - 测试网测试执行报告

## 📋 测试执行概要

**测试时间**: 2025-12-12 15:47  
**测试网络**: Sepolia (Chain ID: 11155111)  
**钱包地址**: 0x39BFa37b4A8A7A20D0F69fd0a388e3EAe739c217

---

## ✅ 成功的部分

### 1. 系统启动 ✅
- Bot 成功连接 Sepolia RPC
- 成功订阅 Binance 价格流 (ETH: $3232)
- Monitor 服务启动正常 (端口 8082)
- 事件流系统运行正常 (file stream)

### 2. 余额查询 ✅
```
TUSD: 16 单位 (0.000016 TUSD)
WETH: 339999999999716658 单位 (0.34 WETH)
```

### 3. Allowance 检查 ✅
```
TUSD allowance: 4999000000000 > 10 (充足)
WETH allowance: 1000000000000000000 > 3150000000000 (充足)
```

### 4. 交易发送 ✅
- **交易哈希**: 0x1c2687a36221e242b91942baec16d8c6d08ca1bf8ee2f84621c339266ba7cbfd
- **Nonce**: 240
- **Gas Price**: 220169837 wei (~0.22 Gwei)
- **状态**: 交易已上链
- **链接**: https://sepolia.etherscan.io/tx/0x1c2687a36221e242b91942baec16d8c6d08ca1bf8ee2f84621c339266ba7cbfd

---

## ❌ 失败的部分

### 主要问题：交易被 Revert

**交易状态**: 
```
status=0 (失败)
gasUsed=40983
```

**失败原因分析**:

#### 1. Rebalancer 错误 🔴
```
[Rebalancer] Error: invalid ticks in intent
```

**根本原因**: 
- Strategy 生成的 intent 中 `lower_tick` 和 `upper_tick` 的值无效
- Rebalancer 检查发现 `tL >= tU`，触发错误
- 但 IntentExecutor 忽略了这个错误，继续执行

**代码位置**: [rebalancer.go:91-96](file:///root/Phoenix_V3/internal/rebalancer/rebalancer.go#L91-L96)
```go
tL, _ := parseMetaInt(input.Intent.Metadata, "lower_tick")
tU, _ := parseMetaInt(input.Intent.Metadata, "upper_tick")
if tL >= tU {
    return nil, fmt.Errorf("invalid ticks in intent")
}
```

#### 2. DEX Price 问题 🟡
```
[DEX] Pool tusd-weth-005 tick=-80666 liquidity=42875014 dexPrice=3184965375664024.50
```

**分析**:
- Current tick: -80666
- DEX price: 3,184,965,375,664,024.50 (异常高的价格！)
- CEX price: $3232

**问题**: DEX 价格计算有严重错误，导致 tick 计算异常

> 备注（2025-12-14 修订）：若该池在创建/初始化时 `sqrtPriceX96` 未按 token decimals 把“人类价格”转换为 Uniswap `rawPrice`，链上 tick 会落在 `-80k` 附近并导致 dexPrice 出现数量级异常；这属于建池脚本/初始化参数错误而非链上读数错误。本仓库已修复 `scripts/tusd_setup.py create-pool` 的 decimals 转换，并同步更新 `DEPLOY.md` 的 tick 示例。

---

## 🔍 详细问题诊断

### 问题 1: DEX 价格计算错误

**异常数据**:
```
tick = -80666
dexPrice = 3184965375664024.50
realPrice = 3232
```

**可能原因**:
1. Tick 到价格的转换公式错误
2. Token decimals 处理错误
3. Pool 配置不正确

### 问题 2: Tick 范围计算错误

**Engine 应该输出**:
- `TargetLowerTick`: 合理的下界 tick
- `TargetUpperTick`: 合理的上界 tick

**实际情况**:
- lower_tick 和 upper_tick 可能都是 0 或相等
- 导致 `tL >= tU` 条件满足

### 问题 3: IntentExecutor 错误处理

**当前行为**:
- Rebalancer 返回错误，但 IntentExecutor 仍然执行
- 使用原始 `intent.Metadata` 中的 amount0/amount1
- 忽略了 Rebalancer 计算的正确值

---

## 💡 建议的修复方案

### 修复 1: 检查并修正 DEX 价格计算

**位置**: dexstate/univ3.go

需要验证:
1. `sqrtPriceX96` 到价格的转换
2. Decimals 调整是否正确
3. Token0/Token1 顺序是否正确

### 修复 2: 完善 IntentExecutor 错误处理

**位置**: cmd/bot/main.go

```go
// 当前代码（有问题）
intent.Metadata["recipient"] = addrProvider.Address()
intent.Metadata["target"] = adapter.TargetAddress().Hex()

// 应该改为
plan, err := rebalancer.Rebalance(ctx, input)
if err != nil {
    log.Printf("[IntentExecutor] Rebalancer error: %v", err)
    return // ⚠️ 应该停止执行，而不是继续
}
```

### 修复 3: 验证 Tick 计算

需要检查:
1. Engine 的 tick 计算逻辑
2. Strategy 的 tick spacing 对齐
3. 确保 lower_tick < upper_tick

---

## 📊 Gas 消耗分析

**本次交易**:
```
Gas Used: 40,983
Gas Price: 0.22 Gwei
Cost: 0.00000902 ETH (~$0.03)
```

**分析**:
- Gas 使用量很低，说明交易在早期就失败了
- 可能在 Position Manager 的参数验证阶段就 revert 了
- 没有到达实际的 mint 逻辑

---

## 🎯 资金安全验证

### ✅ 已验证安全的部分

1. **Recipient 设置** ✅
   ```
   intent.Metadata["recipient"] = addrProvider.Address()
   ```
   - 正确设置为钱包地址
   
2. **Allowance** ✅
   - TUSD: 4,999,000,000,000 > 需要的 10
   - WETH: 1,000,000,000,000,000,000 > 需要的 3,150,000,000,000

3. **余额扣除** ✅
   - 交易失败，余额未改变
   - 没有资金丢失

### ⚠️ 未完成验证的部分

由于交易失败，以下测试未能完成:
- [ ] Mint LP 后代币是否正确扣除
- [ ] NFT 是否发送到钱包
- [ ] Collect 手续费流程
- [ ] Burn LP 代币返回流程

---

## 📝 下一步行动

### 立即行动

1. **停止当前 Bot**
   ```bash
   kill -9 $(cat logs/bot_sepolia.pid)
   ```

2. **修复 DEX 价格计算**
   - 检查 sqrtPriceX96 转换逻辑
   - 验证 decimals 处理

3. **完善错误处理**
   - Rebalancer 错误应该阻止执行
   - 添加更详细的日志

### 测试计划

1. **离线测试**
   - 使用 dry-run 模式测试 tick 计算
   - 验证 DEX 价格转换

2. **小额测试**
   - 修复后使用极小金额再次测试
   - 监控每一步的余额变化

3. **完整测试**
   - Swap 测试
   - Mint LP 测试
   - Collect Fees 测试
   - Burn LP 测试

---

## 📋 总结

**测试状态**: 🟡 **部分成功**

**成功项**:
- ✅ 系统启动和连接
- ✅ 余额查询
- ✅ Allowance 管理
- ✅ 交易签名和发送
- ✅ Recipient 地址设置正确

**失败项**:
- ❌ DEX 价格计算异常
- ❌ Tick 范围计算错误
- ❌ 交易被 revert
- ❌ 资金流转未完成测试

**风险评估**:
- 🟢 **资金安全**: 未发现资金丢失风险
- 🟡 **功能正确性**: 存在计算错误，需要修复
- 🟢 **Gas 优化**: Gas 使用在合理范围

**建议**: 
1. 修复 DEX 价格和 tick 计算逻辑
2. 完善错误处理机制
3. 重新进行测试

---

**报告生成时间**: 2025-12-12 15:50  
**报告类型**: 测试网真实交易测试  
**状态**: 需要修复后重测

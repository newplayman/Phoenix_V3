-- 创建资金追踪表
CREATE TABLE IF NOT EXISTS balance_snapshots (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
    phase TEXT,
    weth_balance TEXT,
    usdc_balance TEXT,
    eth_balance TEXT,
    notes TEXT
);

-- 创建交易记录表
CREATE TABLE IF NOT EXISTS transaction_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
    tx_hash TEXT,
    tx_type TEXT,
    gas_used INTEGER,
    gas_price TEXT,
    eth_cost TEXT,
    weth_change TEXT,
    usdc_change TEXT,
    notes TEXT
);

-- 创建LP头寸追踪表
CREATE TABLE IF NOT EXISTS position_tracking (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
    token_id TEXT,
    liquidity TEXT,
    tick_lower INTEGER,
    tick_upper INTEGER,
    amount0 TEXT,
    amount1 TEXT,
    fees0 TEXT,
    fees1 TEXT,
    status TEXT
);

import React, { useState, useEffect } from 'react';

const API_BASE = import.meta.env.VITE_API_BASE || 'http://localhost:8081';

function App() {
    const [ticker, setTicker] = useState({ price: 2045.5, symbol: 'ETH/USDT' });
    const [engineState] = useState({ lower: 1950, upper: 2150, delta: 0.05 });
    const [systemStatus, setSystemStatus] = useState({
        binanceConnected: false,
        priceSource: 'Fallback',
        healthy: true
    });
    const [intentCount, setIntentCount] = useState(0);
    const [recentTrades, setRecentTrades] = useState([]);
    const [riskStatus, setRiskStatus] = useState({
        mode: 'normal',
        dailyGasUsed: 0,
        maxDailyGas: 0,
        dailySwapVolUsd: 0,
        maxDailySwapVol: 0,
        dailySwapCount: 0,
        maxDailySwaps: 0
    });
    const [pools, setPools] = useState([]);
    const [pnlSeries, setPnlSeries] = useState([]);
    const [poolGuardMap, setPoolGuardMap] = useState({});
    const [paused, setPaused] = useState(false);
    const [controlError, setControlError] = useState('');
    const [cleanupInProgress, setCleanupInProgress] = useState(false);
    const [riskMode, setRiskMode] = useState('normal');

    useEffect(() => {
        const fetchStatus = async () => {
            try {
                const [statusRes, intentRes, tradesRes, riskRes, poolsRes, pnlRes] = await Promise.all([
                    fetch(`${API_BASE}/api/status`),
                    fetch(`${API_BASE}/api/intents`),
                    fetch(`${API_BASE}/api/trades`),
                    fetch(`${API_BASE}/api/risk`),
                    fetch(`${API_BASE}/api/pools`),
                    fetch(`${API_BASE}/api/pnl`)
                ]);

                if (statusRes.ok) {
                    const data = await statusRes.json();
                    if (data?.market?.price > 0) {
                        setTicker({
                            price: data.market.price,
                            symbol: data.market.symbol || 'ETH/USDT'
                        });
                        document.title = `Phoenix V3 | $${data.market.price.toFixed(2)}`;
                    }
                    if (data?.system) {
                        setSystemStatus({
                            binanceConnected: !!data.system.binance_connected,
                            priceSource: data.system.price_source || 'Fallback',
                            healthy: !!data.system.healthy
                        });
                    }
                    if (data?.poolguard) {
                        setPoolGuardMap(data.poolguard);
                    }
                    if (data?.control) {
                        setPaused(!!data.control.paused);
                        setCleanupInProgress(!!data.control.cleanup_in_progress);
                    }
                    if (data?.risk?.mode) {
                        setRiskMode(data.risk.mode);
                    }
                }

                if (intentRes.ok) {
                    const intents = await intentRes.json();
                    setIntentCount(intents?.pending_count ?? 0);
                }

                if (tradesRes.ok) {
                    const tradesData = await tradesRes.json();
                    setRecentTrades(tradesData?.trades ?? []);
                }

                if (riskRes.ok) {
                    const riskData = await riskRes.json();
                    if (riskData?.risk) {
                        setRiskStatus(riskData.risk);
                    }
                } else if (statusRes.ok) {
                    const data = await statusRes.json();
                    if (data?.risk) {
                        setRiskStatus(data.risk);
                    }
                }

                if (poolsRes.ok) {
                    const poolsData = await poolsRes.json();
                    setPools(poolsData?.pools ?? []);
                }

                if (pnlRes.ok) {
                    const pnlData = await pnlRes.json();
                    setPnlSeries(pnlData?.series ?? []);
                }
            } catch (err) {
                console.error('API fetch failed', err);
            }
        };

        fetchStatus();
        const interval = setInterval(fetchStatus, 1500);
        return () => clearInterval(interval);
    }, []);

    const binanceClass = systemStatus.binanceConnected ? 'text-green-400' : 'text-yellow-400';
    const binanceLabel = systemStatus.binanceConnected ? 'Stable' : 'Degraded';

    const togglePause = async () => {
        setControlError('');
        const endpoint = paused ? 'resume' : 'pause';
        try {
            const res = await fetch(`${API_BASE}/api/control/${endpoint}`, { method: 'POST' });
            if (!res.ok) {
                const txt = await res.text();
                setControlError(txt || 'control failed');
                return;
            }
            setPaused(!paused);
        } catch (err) {
            setControlError(err?.message || 'control failed');
        }
    };

    const triggerCleanup = async () => {
        setControlError('');
        try {
            const res = await fetch(`${API_BASE}/api/control/cleanup`, { method: 'POST' });
            if (!res.ok) {
                const txt = await res.text();
                setControlError(txt || 'cleanup failed');
                return;
            }
            setCleanupInProgress(true);
        } catch (err) {
            setControlError(err?.message || 'cleanup failed');
        }
    };

    const setMode = async (mode) => {
        setControlError('');
        if (!mode) return;
        if (!window.confirm(`Switch risk mode to ${mode}?`)) {
            return;
        }
        try {
            const res = await fetch(`${API_BASE}/api/control/riskmode?mode=${encodeURIComponent(mode)}`, { method: 'POST' });
            if (!res.ok) {
                const txt = await res.text();
                setControlError(txt || 'riskmode failed');
                return;
            }
            setRiskMode(mode);
        } catch (err) {
            setControlError(err?.message || 'riskmode failed');
        }
    };

    const triggerRebalance = async () => {
        setControlError('');
        if (!window.confirm('Trigger manual rebalance intent?')) {
            return;
        }
        try {
            const res = await fetch(`${API_BASE}/api/control/rebalance`, { method: 'POST' });
            if (!res.ok) {
                const txt = await res.text();
                setControlError(txt || 'rebalance failed');
                return;
            }
        } catch (err) {
            setControlError(err?.message || 'rebalance failed');
        }
    };

    return (
        <div className="min-h-screen">
            <header className="glass-panel p-4 flex justify-between items-center sticky top-0 z-50">
                <div className="flex items-center gap-3">
                    <div className="w-8 h-8 rounded-full bg-blue-500 flex items-center justify-center font-bold">P</div>
                    <h1 className="text-xl font-bold tracking-tight">
                        Phoenix V3
                        <span className="text-xs text-blue-400 border border-blue-400/30 px-2 py-0.5 rounded-full ml-2">
                            LIVE
                        </span>
                    </h1>
                </div>
                <div className="flex flex-col md:flex-row gap-2 md:gap-4 text-sm font-medium text-gray-400 text-right items-end">
                    <span>ETH Network: <span className="text-green-400">Connected</span></span>
                    <span>Binance Feed: <span className={binanceClass}>{binanceLabel}</span></span>
                    <span>Source: <span className="text-blue-300">{systemStatus.priceSource}</span></span>
                    <button
                        onClick={togglePause}
                        className={`px-3 py-1 rounded-md border text-xs font-semibold ${paused ? 'border-yellow-500/50 text-yellow-300 hover:bg-yellow-500/10' : 'border-green-500/50 text-green-300 hover:bg-green-500/10'}`}
                    >
                        {paused ? 'RESUME' : 'PAUSE'}
                    </button>
                    <button
                        onClick={triggerCleanup}
                        disabled={cleanupInProgress}
                        className={`px-3 py-1 rounded-md border text-xs font-semibold ${cleanupInProgress ? 'border-slate-700/50 text-gray-500' : 'border-red-500/50 text-red-300 hover:bg-red-500/10'}`}
                    >
                        {cleanupInProgress ? 'CLEANUP…' : 'CLEANUP'}
                    </button>
                    <select
                        value={riskMode}
                        onChange={(e) => setMode(e.target.value)}
                        className="px-2 py-1 rounded-md border border-slate-700/50 bg-slate-900 text-xs text-gray-200"
                    >
                        <option value="normal">risk: normal</option>
                        <option value="caution">risk: caution</option>
                        <option value="frozen">risk: frozen</option>
                    </select>
                </div>
            </header>

            {controlError && (
                <div className="mx-4 mt-3 p-2 text-xs rounded bg-red-900/30 border border-red-500/40 text-red-200">
                    {controlError}
                </div>
            )}

            <main className="p-6 max-w-7xl mx-auto grid grid-cols-1 md:grid-cols-3 gap-6">
                <div className="card col-span-1 md:col-span-2">
                    <h2 className="text-gray-400 text-sm uppercase tracking-wider mb-4">Market Overview</h2>
                    <div className="flex items-end gap-2 mb-6">
                        <span className="text-5xl font-bold text-white">${ticker.price.toFixed(2)}</span>
                        <span className="text-green-400 mb-1">+0.05%</span>
                    </div>

                    <div className="relative h-12 bg-slate-800 rounded-lg overflow-hidden flex items-center px-4">
                        <div className="absolute top-1/2 left-[10%] w-[80%] h-1 bg-slate-600 -translate-y-1/2 rounded-full"></div>
                        <div
                            className="absolute top-1/2 h-2 bg-blue-500 -translate-y-1/2 rounded-full transition-all duration-500"
                            style={{
                                left: `${((engineState.lower - 1000) / 2000) * 100}%`,
                                width: `${((engineState.upper - engineState.lower) / 2000) * 100}%`
                            }}
                        ></div>
                        <div className="absolute top-1/2 left-1/2 w-4 h-4 bg-white rounded-full border-4 border-slate-900 -translate-y-1/2 -translate-x-1/2 z-10 shadow-lg shadow-blue-500/50"></div>
                    </div>
                    <div className="flex justify-between mt-2 text-xs text-gray-400">
                        <span>Low: {engineState.lower}</span>
                        <span>Target: {engineState.upper}</span>
                    </div>
                </div>

                <div className="card">
                    <h2 className="text-gray-400 text-sm uppercase tracking-wider mb-4">Engine State</h2>
                    <div className="space-y-4">
                        <div className="flex justify-between p-3 bg-slate-800/50 rounded-lg border border-slate-700/50">
                            <span className="text-gray-400">Current Tick</span>
                            <span className="font-mono text-blue-300">201020</span>
                        </div>
                        <div className="flex justify-between p-3 bg-slate-800/50 rounded-lg border border-slate-700/50">
                            <span className="text-gray-400">Target Tick</span>
                            <span className="font-mono text-blue-300">201050</span>
                        </div>
                        <div className="mt-6">
                            <button onClick={triggerRebalance} className="btn-primary w-full py-3">Rebalance Now</button>
                        </div>
                    </div>
                </div>

                <div className="card col-span-1 md:col-span-3">
                    <h2 className="text-gray-400 text-sm uppercase tracking-wider mb-4">Intent Queue</h2>
                    {intentCount > 0 ? (
                        <div className="text-center py-6 text-white">
                            <p className="text-4xl font-bold">{intentCount}</p>
                            <p className="text-gray-400 mt-2">pending intents awaiting execution</p>
                        </div>
                    ) : (
                        <div className="text-center py-10 text-gray-500 italic">
                            No pending intents. System is idling.
                        </div>
                    )}
                </div>

                <div className="card col-span-1 md:col-span-3">
                    <h2 className="text-gray-400 text-sm uppercase tracking-wider mb-4">Recent Trades</h2>
                    {recentTrades.length === 0 ? (
                        <div className="text-center py-6 text-gray-500 italic">No trades yet.</div>
                    ) : (
                        <div className="overflow-x-auto">
                            <table className="w-full text-sm">
                                <thead className="text-gray-400">
                                    <tr>
                                        <th className="text-left py-2">Time</th>
                                        <th className="text-left py-2">Type</th>
                                        <th className="text-left py-2">Pool</th>
                                        <th className="text-left py-2">Status</th>
                                        <th className="text-left py-2">Gas (native)</th>
                                        <th className="text-left py-2">Swap Details</th>
                                    </tr>
                                </thead>
                                <tbody className="text-gray-200">
                                    {recentTrades.map((t) => (
                                        <tr key={t.tx_hash} className="border-t border-slate-700/50">
                                            <td className="py-2">{new Date(t.time).toLocaleString()}</td>
                                            <td className="py-2">{t.type}</td>
                                            <td className="py-2">{t.pool_id}</td>
                                            <td className="py-2">{t.status}</td>
                                            <td className="py-2 font-mono">{(t.gas_cost_native ?? 0).toFixed(6)}</td>
                                            <td className="py-2 font-mono max-w-[420px]">
                                                {t.swap_details ? (
                                                    (() => {
                                                        try {
                                                            const swaps = JSON.parse(t.swap_details);
                                                            return swaps.map((s, idx) => (
                                                                <div key={idx} className="truncate">
                                                                    {s.from_token?.slice(0, 6)}→{s.to_token?.slice(0, 6)} in {s.amount_in} out {s.actual_out || '?'} slip {(s.slippage_pct * 100).toFixed(2)}%
                                                                </div>
                                                            ));
                                                        } catch {
                                                            return <div className="truncate">invalid swap_details</div>;
                                                        }
                                                    })()
                                                ) : (
                                                    '-'
                                                )}
                                            </td>
                                        </tr>
                                    ))}
                                </tbody>
                            </table>
                        </div>
                    )}
                </div>

                <div className="card col-span-1 md:col-span-3">
                    <h2 className="text-gray-400 text-sm uppercase tracking-wider mb-4">Risk Status</h2>
                    <div className="grid grid-cols-1 md:grid-cols-3 gap-4 text-sm">
                        <div className="p-3 bg-slate-800/50 rounded-lg border border-slate-700/50">
                            <div className="text-gray-400">Mode</div>
                            <div className="mt-1 font-semibold text-white">{riskStatus.mode}</div>
                        </div>
                        <div className="p-3 bg-slate-800/50 rounded-lg border border-slate-700/50">
                            <div className="text-gray-400">Daily Gas Used</div>
                            <div className="mt-1 font-mono text-white">
                                {(riskStatus.dailyGasUsed ?? 0).toFixed(6)} / {(riskStatus.maxDailyGas ?? 0).toFixed(3)}
                            </div>
                        </div>
                        <div className="p-3 bg-slate-800/50 rounded-lg border border-slate-700/50">
                            <div className="text-gray-400">Daily Swap Volume (USD)</div>
                            <div className="mt-1 font-mono text-white">
                                {(riskStatus.dailySwapVolUsd ?? 0).toFixed(2)} / {(riskStatus.maxDailySwapVol ?? 0).toFixed(2)}
                            </div>
                        </div>
                        <div className="p-3 bg-slate-800/50 rounded-lg border border-slate-700/50">
                            <div className="text-gray-400">Daily Swap Count</div>
                            <div className="mt-1 font-mono text-white">
                                {riskStatus.dailySwapCount ?? 0} / {riskStatus.maxDailySwaps ?? 0}
                            </div>
                        </div>
                    </div>
                </div>

                <div className="card col-span-1 md:col-span-3">
                    <h2 className="text-gray-400 text-sm uppercase tracking-wider mb-4">Pools</h2>
                    {pools.length === 0 ? (
                        <div className="text-center py-6 text-gray-500 italic">No pool snapshot yet.</div>
                    ) : (
                        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                            {pools.map((p) => (
                                <div key={p.pool_id} className="p-3 bg-slate-800/50 rounded-lg border border-slate-700/50">
                                    <div className="flex justify-between text-sm">
                                        <div className="font-semibold text-white">{p.pool_id}</div>
                                        <div className="text-gray-400">chain {p.chain_id}</div>
                                    </div>
                                    {poolGuardMap[p.pool_id] && (
                                        <div className="mt-1 text-xs">
                                            <span className={poolGuardMap[p.pool_id].risk === 'danger' ? 'text-red-400' : poolGuardMap[p.pool_id].risk === 'warning' ? 'text-yellow-400' : 'text-green-400'}>
                                                {poolGuardMap[p.pool_id].risk}
                                            </span>
                                            <span className="text-gray-500 ml-2 truncate">{poolGuardMap[p.pool_id].reason}</span>
                                        </div>
                                    )}
                                    <div className="mt-2 text-xs text-gray-300 space-y-1">
                                        <div>Dex Price: {Number(p.dex_price).toFixed(6)}</div>
                                        <div>Tick: {p.current_tick}</div>
                                        <div>Liquidity: {p.liquidity}</div>
                                    </div>
                                </div>
                            ))}
                        </div>
                    )}
                </div>

                <div className="card col-span-1 md:col-span-3">
                    <h2 className="text-gray-400 text-sm uppercase tracking-wider mb-4">PnL (Daily)</h2>
                    {pnlSeries.length === 0 ? (
                        <div className="text-center py-6 text-gray-500 italic">PnL series not available yet.</div>
                    ) : (
                        <div className="overflow-x-auto">
                            <table className="w-full text-sm">
                                <thead className="text-gray-400">
                                    <tr>
                                        <th className="text-left py-2">Day</th>
                                        <th className="text-left py-2">PnL (USD)</th>
                                        <th className="text-left py-2">Gas (native)</th>
                                        <th className="text-left py-2">Net (USD)</th>
                                        <th className="text-left py-2">Trades</th>
                                    </tr>
                                </thead>
                                <tbody className="text-gray-200">
                                    {pnlSeries.map((p) => (
                                        <tr key={p.day} className="border-t border-slate-700/50">
                                            <td className="py-2">{new Date(p.day).toLocaleDateString()}</td>
                                            <td className="py-2 font-mono">{(p.pnl_usd ?? 0).toFixed(2)}</td>
                                            <td className="py-2 font-mono">{(p.gas_native ?? 0).toFixed(6)}</td>
                                            <td className="py-2 font-mono">{(p.net_pnl_usd ?? 0).toFixed(2)}</td>
                                            <td className="py-2">{p.trade_count}</td>
                                        </tr>
                                    ))}
                                </tbody>
                            </table>
                            <div className="text-xs text-gray-500 mt-2">Note: PnL is 0 until executor fills TradeRecord.PnL.</div>
                        </div>
                    )}
                </div>
            </main>
        </div>
    );
}

export default App;

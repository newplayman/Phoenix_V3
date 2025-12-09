import React, { useState, useEffect } from 'react';

function App() {
    const [ticker, setTicker] = useState({ price: 2045.50, symbol: 'ETH/USDT' });
    const [engineState, setEngineState] = useState({ lower: 1950, upper: 2150, delta: 0.05 });

    // Poll API for data
    useEffect(() => {
        const interval = setInterval(async () => {
            try {
                const res = await fetch('http://localhost:8081/api/status');
                const data = await res.json();

                if (data.market.price > 0) {
                    setTicker({
                        price: data.market.price,
                        symbol: data.market.symbol
                    });
                }
            } catch (e) {
                console.error("API fetch failed", e);
            }
        }, 1000);
        return () => clearInterval(interval);
    }, []);

    return (
        <div className="min-h-screen">
            {/* Header */}
            <header className="glass-panel p-4 flex justify-between items-center sticky top-0 z-50">
                <div className="flex items-center gap-3">
                    <div className="w-8 h-8 rounded-full bg-blue-500 flex items-center justify-center font-bold">P</div>
                    <h1 className="text-xl font-bold tracking-tight">Phoenix V3 <span className="text-xs text-blue-400 border border-blue-400/30 px-2 py-0.5 rounded-full ml-2">LIVE</span></h1>
                </div>
                <div className="flex gap-4 text-sm font-medium text-gray-400">
                    <span>ETH Network: <span className="text-green-400">Connected</span></span>
                    <span>Binance Feed: <span className="text-green-400">Stable</span></span>
                </div>
            </header>

            {/* Main Grid */}
            <main className="p-6 max-w-7xl mx-auto grid grid-cols-1 md:grid-cols-3 gap-6">

                {/* Market Status Card */}
                <div className="card col-span-1 md:col-span-2">
                    <h2 className="text-gray-400 text-sm uppercase tracking-wider mb-4">Market Overview</h2>
                    <div className="flex items-end gap-2 mb-6">
                        <span className="text-5xl font-bold text-white">${ticker.price.toFixed(2)}</span>
                        <span className="text-green-400 mb-1">+0.05%</span>
                    </div>

                    {/* Visual Bar for Range */}
                    <div className="relative h-12 bg-slate-800 rounded-lg overflow-hidden flex items-center px-4">
                        {/* Simple visual representation */}
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

                {/* Strategy Control */}
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
                            <button className="btn-primary w-full py-3">
                                Rebalance Now
                            </button>
                        </div>
                    </div>
                </div>

                {/* Intent Queue (Phase 3 Prep) */}
                <div className="card col-span-1 md:col-span-3">
                    <h2 className="text-gray-400 text-sm uppercase tracking-wider mb-4">Intent Queue</h2>
                    <div className="text-center py-10 text-gray-500 italic">
                        No pending intents. System is idling.
                    </div>
                </div>

            </main>
        </div>
    )
}

export default App

import React, { useEffect, useMemo, useState } from 'react'

const API_BASE = import.meta.env.VITE_API_BASE || 'http://localhost:8081'
const USE_MOCK = String(import.meta.env.VITE_USE_MOCK || '') === '1'

export default function App() {
  const [adminToken, setAdminToken] = useState(() => sessionStorage.getItem('phoenix_admin_token') || '')
  const [error, setError] = useState('')
  const [health, setHealth] = useState(null)
  const [pools, setPools] = useState([])
  const [poolState, setPoolState] = useState(null)

  const authHeader = useMemo(() => {
    if (!adminToken) return {}
    return { Authorization: `Bearer ${adminToken}` }
  }, [adminToken])

  const apiFetch = async (path) => {
    const res = await fetch(`${API_BASE}${path}`, { headers: { ...authHeader } })
    const json = await res.json().catch(() => null)
    if (!res.ok) {
      const code = json?.error?.code || 'request_failed'
      const msg = json?.error?.message || res.statusText
      throw new Error(`${code}: ${msg}`)
    }
    return json
  }

  useEffect(() => {
    sessionStorage.setItem('phoenix_admin_token', adminToken)
  }, [adminToken])

  useEffect(() => {
    let cancelled = false

    const tick = async () => {
      setError('')

      if (USE_MOCK) {
        const now = new Date().toISOString()
        const mockHealth = {
          bot: { online: true, last_heartbeat_ts: now, latest_block: 0, queue_depth: 0 },
          rpc: { ok: true, timeout_rate_5m: 0.0, p95_latency_ms: 0 },
          risk: { mode: 'normal', consecutive_fails: 0, daily_gas_used_eth: 0.0, daily_gas_limit_eth: 0.05 },
        }
        const mockPools = [
          {
            pool_id: 'mock-pool',
            chain_id: 421614,
            pool_address: '0x...',
            token0: { address: '0x...', symbol: 'WETH', decimals: 18 },
            token1: { address: '0x...', symbol: 'TUSD', decimals: 6 },
            fee: 500,
          },
        ]
        const mockState = {
          pool_id: 'mock-pool',
          chain_id: 421614,
          ts: now,
          dex: { tick: 0, price_stable_per_weth: 2000, liquidity: '0' },
          cex: { price_stable_per_weth: 2000, source: 'mock' },
          position: {
            token_id: '',
            tick_lower: 0,
            tick_upper: 0,
            liquidity: '0',
            in_range: false,
            distance_to_lower_pct: 0,
            distance_to_upper_pct: 0,
          },
          strategy: {
            profile: 'normal',
            sigma_daily: 0,
            width_pct: 0.01,
            vol_window: '1m',
            cooldown_active: false,
            min_interval: '30s',
          },
          risk: { mode: 'normal', consecutive_fails: 0, rebalances_last_1h: 0 },
        }
        if (!cancelled) {
          setHealth(mockHealth)
          setPools(mockPools)
          setPoolState(mockState)
        }
        return
      }

      if (!adminToken) {
        if (!cancelled) {
          setHealth(null)
          setPools([])
          setPoolState(null)
        }
        return
      }

      try {
        const h = await apiFetch('/api/v1/health')
        const p = await apiFetch('/api/v1/pools')
        const pool0 = p?.pools?.[0]?.pool_id
        const st = pool0 ? await apiFetch(`/api/v1/pools/${encodeURIComponent(pool0)}/state`) : null
        if (!cancelled) {
          setHealth(h)
          setPools(p?.pools || [])
          setPoolState(st)
        }
      } catch (e) {
        if (!cancelled) setError(String(e?.message || e))
      }
    }

    tick()
    const interval = setInterval(tick, 2000)
    return () => {
      cancelled = true
      clearInterval(interval)
    }
  }, [adminToken, authHeader])

  return (
    <div className="min-h-screen">
      <header className="glass-panel p-4 flex justify-between items-center sticky top-0 z-50">
        <div className="flex items-center gap-3">
          <div className="w-8 h-8 rounded-full bg-blue-500 flex items-center justify-center font-bold">P</div>
          <h1 className="text-xl font-bold tracking-tight">
            Phoenix V3
            <span className="text-xs text-blue-400 border border-blue-400/30 px-2 py-0.5 rounded-full ml-2">
              READ-ONLY
            </span>
            {USE_MOCK && (
              <span className="text-xs text-yellow-300 border border-yellow-400/30 px-2 py-0.5 rounded-full ml-2">
                MOCK
              </span>
            )}
          </h1>
        </div>

        <div className="flex flex-col md:flex-row gap-2 md:gap-4 text-sm font-medium text-gray-300 text-right items-end">
          {!USE_MOCK && (
            <input
              value={adminToken}
              onChange={(e) => setAdminToken(e.target.value)}
              placeholder="ADMIN_TOKEN (session)"
              className="px-2 py-1 rounded-md border border-slate-700/50 bg-slate-900 text-xs text-gray-200 w-56"
            />
          )}
          <span>
            API: <span className="text-blue-300">{API_BASE}</span>
          </span>
          <span>
            BOT:{' '}
            <span className={health?.bot?.online ? 'text-green-400' : 'text-yellow-400'}>
              {health?.bot?.online ? 'online' : 'offline'}
            </span>
          </span>
          <span>
            RISK: <span className="text-blue-300">{health?.risk?.mode || 'unknown'}</span>
          </span>
        </div>
      </header>

      {error && (
        <div className="mx-4 mt-3 p-2 text-xs rounded bg-red-900/30 border border-red-500/40 text-red-200">
          {error}
        </div>
      )}

      {!USE_MOCK && !adminToken && (
        <div className="mx-4 mt-3 p-2 text-xs rounded bg-slate-900/30 border border-slate-700/40 text-gray-200">
          Set <code>ADMIN_TOKEN</code> to use <code>/api/v1/*</code> read APIs.
        </div>
      )}

      <main className="p-6 max-w-7xl mx-auto grid grid-cols-1 md:grid-cols-3 gap-6">
        <div className="glass-card rounded-xl p-6 col-span-1 md:col-span-2">
          <h2 className="text-gray-400 text-sm uppercase tracking-wider mb-4">Health</h2>
          <div className="grid grid-cols-2 gap-3 text-sm">
            <div className="p-3 bg-slate-800/40 rounded-lg border border-slate-700/50">
              <div className="text-gray-400">Queue Depth</div>
              <div className="text-white font-mono">{health?.bot?.queue_depth ?? '-'}</div>
            </div>
            <div className="p-3 bg-slate-800/40 rounded-lg border border-slate-700/50">
              <div className="text-gray-400">RPC OK</div>
              <div className="text-white font-mono">{String(health?.rpc?.ok ?? '-')}</div>
            </div>
            <div className="p-3 bg-slate-800/40 rounded-lg border border-slate-700/50">
              <div className="text-gray-400">Consecutive Fails</div>
              <div className="text-white font-mono">{health?.risk?.consecutive_fails ?? '-'}</div>
            </div>
            <div className="p-3 bg-slate-800/40 rounded-lg border border-slate-700/50">
              <div className="text-gray-400">Daily Gas (ETH)</div>
              <div className="text-white font-mono">
                {health?.risk?.daily_gas_used_eth ?? 0} / {health?.risk?.daily_gas_limit_eth ?? 0}
              </div>
            </div>
          </div>
        </div>

        <div className="glass-card rounded-xl p-6">
          <h2 className="text-gray-400 text-sm uppercase tracking-wider mb-4">Pools</h2>
          <div className="space-y-2 text-xs">
            {(pools || []).map((p) => (
              <div key={p.pool_id} className="p-3 bg-slate-800/40 rounded-lg border border-slate-700/50">
                <div className="flex justify-between">
                  <div className="text-white font-semibold">{p.pool_id}</div>
                  <div className="text-gray-400">{p.chain_id}</div>
                </div>
                <div className="text-gray-300 mt-1">
                  {p.token0?.symbol}/{p.token1?.symbol} fee={p.fee}
                </div>
              </div>
            ))}
            {(!pools || pools.length === 0) && <div className="text-gray-400">No pools</div>}
          </div>
        </div>

        <div className="glass-card rounded-xl p-6 col-span-1 md:col-span-3">
          <h2 className="text-gray-400 text-sm uppercase tracking-wider mb-4">Pool State</h2>
          {!poolState ? (
            <div className="text-gray-400 text-sm">No state</div>
          ) : (
            <div className="grid grid-cols-1 md:grid-cols-3 gap-3 text-sm">
              <div className="p-3 bg-slate-800/40 rounded-lg border border-slate-700/50">
                <div className="text-gray-400">DEX Price (stable/WETH)</div>
                <div className="text-white font-mono">{poolState?.dex?.price_stable_per_weth ?? '-'}</div>
              </div>
              <div className="p-3 bg-slate-800/40 rounded-lg border border-slate-700/50">
                <div className="text-gray-400">Tick</div>
                <div className="text-white font-mono">{poolState?.dex?.tick ?? '-'}</div>
              </div>
              <div className="p-3 bg-slate-800/40 rounded-lg border border-slate-700/50">
                <div className="text-gray-400">In Range</div>
                <div className="text-white font-mono">{String(poolState?.position?.in_range ?? '-')}</div>
              </div>
            </div>
          )}
        </div>
      </main>
    </div>
  )
}


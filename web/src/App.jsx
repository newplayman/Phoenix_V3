import React, { useEffect, useMemo, useState } from 'react'
import { fetchEventSource } from '@microsoft/fetch-event-source'

const API_BASE = import.meta.env.VITE_API_BASE || 'http://localhost:8081'
const USE_MOCK = String(import.meta.env.VITE_USE_MOCK || '') === '1'

export default function App() {
  const [adminToken, setAdminToken] = useState(() => sessionStorage.getItem('phoenix_admin_token') || '')
  const [error, setError] = useState('')
  const [health, setHealth] = useState(null)
  const [pools, setPools] = useState([])
  const [poolState, setPoolState] = useState(null)
  const [intents, setIntents] = useState([])
  const [selectedIntentId, setSelectedIntentId] = useState('')
  const [selectedIntent, setSelectedIntent] = useState(null)
  const [txList, setTxList] = useState([])
  const [auditList, setAuditList] = useState([])
  const [streamStatus, setStreamStatus] = useState({ connected: false, lastEventType: '', lastTs: '' })

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

  const txLink = (hash) => {
    if (!hash || typeof hash !== 'string') return ''
    if (!hash.startsWith('0x') || hash.length < 10) return ''
    return `https://sepolia.arbiscan.io/tx/${hash}`
  }

  useEffect(() => {
    sessionStorage.setItem('phoenix_admin_token', adminToken)
  }, [adminToken])

  useEffect(() => {
    if (USE_MOCK) {
      setStreamStatus({ connected: true, lastEventType: 'mock', lastTs: new Date().toISOString() })
      return
    }
    if (!adminToken) {
      setStreamStatus({ connected: false, lastEventType: '', lastTs: '' })
      return
    }

    const ctrl = new AbortController()
    setStreamStatus((s) => ({ ...s, connected: false }))

    fetchEventSource(`${API_BASE}/api/v1/stream`, {
      signal: ctrl.signal,
      headers: { ...authHeader },
      openWhenHidden: true,
      onopen: async (res) => {
        if (!res.ok) {
          throw new Error(`stream_open_failed: ${res.status}`)
        }
        setStreamStatus((s) => ({ ...s, connected: true }))
      },
      onmessage: (ev) => {
        try {
          const payload = JSON.parse(ev.data || '{}')
          setStreamStatus({ connected: true, lastEventType: ev.event || payload?.type || 'message', lastTs: payload?.ts || '' })
        } catch {
          setStreamStatus((s) => ({ ...s, connected: true, lastEventType: ev.event || 'message' }))
        }
      },
      onerror: () => {
        setStreamStatus((s) => ({ ...s, connected: false }))
      },
    }).catch(() => {
      setStreamStatus((s) => ({ ...s, connected: false }))
    })

    return () => ctrl.abort()
  }, [adminToken, authHeader])

  useEffect(() => {
    let cancelled = false
    let tickN = 0

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
        const mockIntents = [
          {
            intent_id: 'intent-mock-1',
            pool_id: 'mock-pool',
            chain_id: 421614,
            type: 'rebalance',
            status: 'succeeded',
            created_at: now,
            updated_at: now,
            metadata: { position_token_id: '1' },
          },
        ]
        const mockIntentDetail = {
          intent: mockIntents[0],
          steps: [
            { step_index: 0, step_type: 'approve', status: 'mined', tx_hash: '0xabc', details: {} },
            { step_index: 1, step_type: 'approve', status: 'mined', tx_hash: '0xdef', details: {} },
            { step_index: 2, step_type: 'mint', status: 'mined', tx_hash: '0x123', details: { lower_tick: 0, upper_tick: 0 } },
          ],
        }
        const mockTx = [
          { chain_id: 421614, tx_hash: '0x123', status: 'mined', intent_id: 'intent-mock-1', pool_id: 'mock-pool' },
          { chain_id: 421614, tx_hash: '0xdef', status: 'mined', intent_id: 'intent-mock-1', pool_id: 'mock-pool' },
        ]
        const mockAudit = [
          { ts: now, actor: 'admin', action_type: 'execute_rebalance', pool_id: 'mock-pool', chain_id: 421614, request: {}, result: {} },
        ]
        if (!cancelled) {
          setHealth(mockHealth)
          setPools(mockPools)
          setPoolState(mockState)
          setIntents(mockIntents)
          setSelectedIntentId('intent-mock-1')
          setSelectedIntent(mockIntentDetail)
          setTxList(mockTx)
          setAuditList(mockAudit)
        }
        return
      }

      if (!adminToken) {
        if (!cancelled) {
          setHealth(null)
          setPools([])
          setPoolState(null)
          setIntents([])
          setSelectedIntentId('')
          setSelectedIntent(null)
          setTxList([])
          setAuditList([])
        }
        return
      }

      try {
        tickN++
        const h = await apiFetch('/api/v1/health')
        const p = await apiFetch('/api/v1/pools')
        const pool0 = p?.pools?.[0]?.pool_id
        const st = pool0 ? await apiFetch(`/api/v1/pools/${encodeURIComponent(pool0)}/state`) : null
        const txResp = pool0 && tickN % 3 === 0 ? await apiFetch(`/api/v1/tx?pool_id=${encodeURIComponent(pool0)}&limit=20`) : null
        const auditResp = pool0 && tickN % 4 === 0 ? await apiFetch(`/api/v1/audit?pool_id=${encodeURIComponent(pool0)}&limit=20`) : null
        const intentsResp =
          pool0 && tickN % 2 === 0 ? await apiFetch(`/api/v1/intents?pool_id=${encodeURIComponent(pool0)}&limit=20`) : null
        const nextIntents = intentsResp?.intents || []
        const wantIntentID = selectedIntentId || nextIntents?.[0]?.intent_id || ''
        const intentDetail = wantIntentID ? await apiFetch(`/api/v1/intents/${encodeURIComponent(wantIntentID)}`) : null
        if (!cancelled) {
          setHealth(h)
          setPools(p?.pools || [])
          setPoolState(st)
          if (intentsResp) setIntents(nextIntents)
          if (!selectedIntentId && wantIntentID) setSelectedIntentId(wantIntentID)
          setSelectedIntent(intentDetail)
          if (txResp) setTxList(txResp?.tx || [])
          if (auditResp) setAuditList(auditResp?.actions || [])
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
  }, [adminToken, authHeader, selectedIntentId])

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
            <div className="p-3 bg-slate-800/40 rounded-lg border border-slate-700/50 col-span-2">
              <div className="text-gray-400">Stream</div>
              <div className="text-white font-mono">
                {streamStatus.connected ? 'connected' : 'disconnected'} · {streamStatus.lastEventType || '-'} · {streamStatus.lastTs || '-'}
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

        <div className="glass-card rounded-xl p-6 col-span-1 md:col-span-1">
          <h2 className="text-gray-400 text-sm uppercase tracking-wider mb-4">Intents</h2>
          <div className="space-y-2 text-xs">
            {(intents || []).map((it) => (
              <button
                key={it.intent_id}
                onClick={() => setSelectedIntentId(it.intent_id)}
                className={`w-full text-left p-3 rounded-lg border ${
                  selectedIntentId === it.intent_id
                    ? 'bg-blue-900/20 border-blue-500/40'
                    : 'bg-slate-800/40 border-slate-700/50 hover:border-slate-600/60'
                }`}
              >
                <div className="flex justify-between gap-2">
                  <div className="text-white font-semibold truncate">{it.intent_id}</div>
                  <div className="text-gray-400">{it.status}</div>
                </div>
                <div className="text-gray-300 mt-1">
                  {it.type} · {it.pool_id}
                </div>
              </button>
            ))}
            {(!intents || intents.length === 0) && <div className="text-gray-400">No intents</div>}
          </div>
        </div>

        <div className="glass-card rounded-xl p-6 col-span-1 md:col-span-2">
          <h2 className="text-gray-400 text-sm uppercase tracking-wider mb-4">Intent Detail</h2>
          {!selectedIntent ? (
            <div className="text-gray-400 text-sm">Select an intent</div>
          ) : (
            <div className="space-y-4">
              <div className="text-xs text-gray-300">
                <div className="flex flex-wrap gap-x-4 gap-y-1">
                  <div>
                    <span className="text-gray-400">id</span>{' '}
                    <span className="font-mono text-white">{selectedIntent?.intent?.intent_id}</span>
                  </div>
                  <div>
                    <span className="text-gray-400">status</span>{' '}
                    <span className="font-mono text-white">{selectedIntent?.intent?.status}</span>
                  </div>
                  <div>
                    <span className="text-gray-400">type</span>{' '}
                    <span className="font-mono text-white">{selectedIntent?.intent?.type}</span>
                  </div>
                  <div>
                    <span className="text-gray-400">pool</span>{' '}
                    <span className="font-mono text-white">{selectedIntent?.intent?.pool_id}</span>
                  </div>
                  {selectedIntent?.intent?.metadata?.position_token_id && (
                    <div>
                      <span className="text-gray-400">position_token_id</span>{' '}
                      <span className="font-mono text-white">{String(selectedIntent?.intent?.metadata?.position_token_id)}</span>
                    </div>
                  )}
                </div>
              </div>

              <div className="space-y-2">
                {(selectedIntent?.steps || []).map((s) => (
                  <div key={`${s.step_index}-${s.step_type}`} className="p-3 bg-slate-800/40 rounded-lg border border-slate-700/50">
                    <div className="flex justify-between gap-2 text-xs">
                      <div className="text-white font-semibold">
                        #{s.step_index} {s.step_type}
                      </div>
                      <div className="text-gray-300">{s.status}</div>
                    </div>
                    {!!s.tx_hash && (
                      <div className="mt-1 text-xs">
                        <a className="text-blue-300 font-mono" href={txLink(s.tx_hash)} target="_blank" rel="noreferrer">
                          {s.tx_hash}
                        </a>
                      </div>
                    )}
                  </div>
                ))}
                {(!selectedIntent?.steps || selectedIntent.steps.length === 0) && <div className="text-gray-400 text-sm">No steps</div>}
              </div>
            </div>
          )}
        </div>

        <div className="glass-card rounded-xl p-6 col-span-1 md:col-span-2">
          <h2 className="text-gray-400 text-sm uppercase tracking-wider mb-4">Tx</h2>
          <div className="space-y-2 text-xs">
            {(txList || []).slice(0, 10).map((t) => (
              <div key={t.tx_hash} className="p-3 bg-slate-800/40 rounded-lg border border-slate-700/50">
                <div className="flex justify-between gap-2">
                  <div className="text-white font-semibold">{t.status}</div>
                  <div className="text-gray-400">{t.chain_id}</div>
                </div>
                <div className="mt-1">
                  <a className="text-blue-300 font-mono" href={txLink(t.tx_hash)} target="_blank" rel="noreferrer">
                    {t.tx_hash}
                  </a>
                </div>
                <div className="text-gray-300 mt-1">
                  intent={t.intent_id} · pool={t.pool_id}
                </div>
              </div>
            ))}
            {(!txList || txList.length === 0) && <div className="text-gray-400">No tx</div>}
          </div>
        </div>

        <div className="glass-card rounded-xl p-6 col-span-1 md:col-span-1">
          <h2 className="text-gray-400 text-sm uppercase tracking-wider mb-4">Audit</h2>
          <div className="space-y-2 text-xs">
            {(auditList || []).slice(0, 10).map((a, idx) => (
              <div key={`${a.ts}-${a.action_type}-${idx}`} className="p-3 bg-slate-800/40 rounded-lg border border-slate-700/50">
                <div className="flex justify-between gap-2">
                  <div className="text-white font-semibold">{a.action_type}</div>
                  <div className="text-gray-400">{a.chain_id}</div>
                </div>
                <div className="text-gray-300 mt-1">{a.ts}</div>
                <div className="text-gray-300 mt-1">
                  actor={a.actor} · pool={a.pool_id}
                </div>
              </div>
            ))}
            {(!auditList || auditList.length === 0) && <div className="text-gray-400">No audit</div>}
          </div>
        </div>
      </main>
    </div>
  )
}

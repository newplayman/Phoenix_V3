# Phase 2.3 — Contract v1 JSONL + File Control

## Event stream (default on)

- Path: `var/contract_v1.jsonl` (repo-local; no `/var/log` usage)
- Format: JSONL envelope per line:
  - `{"type":"IntentV1","ts_ms":<int64>,"data":{...}}`
  - `{"type":"RiskDecisionV1","ts_ms":<int64>,"data":{...}}`
  - `{"type":"ExecutorResultV1","ts_ms":<int64>,"data":{...}}`
  - `{"type":"StatusV1","ts_ms":<int64>,"data":{...}}`
- Write semantics: append-only; best-effort; write failures do not crash the process.
- Redaction: any key containing `secret|token|passphrase|private|api_key|access_key` is replaced with `"[REDACTED]"` before writing.

## File control (default off / absent)

- Path: `var/control.json` (repo-local)
- Missing file means default control: RUNNING, no force-dry-run, no risk override.
- Read semantics: best-effort; throttled to at most once per second; parse failures are ignored (keep last good state).

### Schema

```json
{
  "desired_state": "RUNNING|PAUSED|SAFE_MODE",
  "force_dry_run": true,
  "risk_mode": "SAFE|DENY|PAUSE|SAFE_MODE|HALT",
  "reason": "optional"
}
```

### Helper scripts

- `scripts/control_set.sh` writes `var/control.json`
- `scripts/tail_v1.sh` tails `var/contract_v1.jsonl` and filters v1 records


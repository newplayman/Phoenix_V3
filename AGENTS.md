# AGENTS.md — Phoenix_V3 Agent Working Agreement (Web & API Extended)

> Mission:
> Keep Phoenix_V3 evolvable while introducing a new Web panel and backend APIs,
> without turning the system into a tightly coupled, undocumented “UI-driven core”.

This document defines how **AI agents + human reviewers** collaborate on:
- backend services
- web dashboard
- API contracts
- documentation
- testing & security

---

## 0. Repository map (updated)

Key directories (dev branch):

- `cmd/`  
  Executable entrypoints (API server, workers, bots)

- `internal/`  
  Core domain logic (strategy, risk, tx execution, state aggregation)

- `bot/`  
  Runtime orchestration, strategy loops, schedulers

- `configs/`  
  Config templates and loaders (NO secrets)

- `contracts/`  
  On-chain contracts, ABI, bindings, chain-specific artifacts

- `web/`  
  Web dashboard frontend (INCOMPLETE / WIP)

- `docs/`  
  **Authoritative documentation**
  - API definitions
  - Web panel design
  - Architecture & flows

- `scripts/`  
  Ops, build, migration helpers

---

## 1. Agent team roles (updated)

### A. Architect (System Boundary Owner)
- Owns **module boundaries**
- Owns **API surface area**
- Ensures Web introduction does NOT leak into domain logic

### B. Backend/API Engineer
- Implements API handlers
- Maps internal domain models → API DTOs
- Keeps `docs/` API definitions in sync

### C. Frontend Engineer
- Consumes APIs strictly via documented contracts
- Must NOT infer backend behavior from implementation details
- Treats API as remote, unstable, versioned dependency

### D. API Contract Reviewer (NEW)
- Reviews API shape, naming, versioning
- Ensures backward compatibility or explicit breaking change notice
- Blocks PRs where code ≠ docs

### E. QA / Test Engineer
- Defines backend + API + Web test expectations
- Ensures unfinished features are **non-impacting**

### F. Security Engineer
- Reviews any API that exposes:
  - balances
  - positions
  - tx status
  - strategy/risk parameters

### G. Docs Engineer
- Docs are first-class deliverables
- Verifies docs/ are executable-by-humans, not aspirational

---

## 2. Web & API core principles (NON-NEGOTIABLE)

### 2.1 API is a Contract, not an Implementation Detail

- `docs/` defines:
  - endpoints
  - request/response schemas
  - error semantics
- Backend code MUST follow docs, not vice versa
- Any mismatch is a bug

> If an agent cannot find an API definition in `docs/`,
> that API **does not exist**.

---

### 2.2 Read / Write separation (critical)

Web panel APIs fall into **three categories**:

#### 1️⃣ Read APIs (default)
- metrics
- positions
- pnl
- system status
- historical records

✅ Safe for Web  
✅ Cacheable  
✅ No side effects

#### 2️⃣ Control APIs (restricted)
- enable/disable strategy
- change parameters
- rebalance triggers

🚫 Disabled by default  
🚫 Require explicit auth & audit  
🚫 Must have rollback semantics

#### 3️⃣ Execution APIs (generally FORBIDDEN)
- direct tx submission
- forced trading actions

❌ Web must NOT call these  
❌ Only bot / scheduler layer may invoke

---

### 2.3 Web must NEVER:
- import `internal/*`
- access DB directly
- infer tx success without backend confirmation
- trigger chain actions implicitly

Web is **observational first**, operational second.

---

## 3. Feature status & incomplete work policy (NEW)

Every new Web/API feature MUST declare status:

- `experimental` — exists but hidden / disabled
- `beta` — visible, read-only, no funds impact
- `stable` — production-ready

Rules:
- experimental features must be:
  - disabled by default
  - non-breaking
  - isolated from critical paths
- unfinished UI MUST NOT require unfinished backend logic

Feature status must be documented in:
- docs/
- config flags
- PR description

---

## 4. API versioning & evolution rules

- All APIs are versioned (`/api/v1/...`)
- Breaking changes require:
  - new version
  - deprecation note in docs
- Never silently change:
  - field meaning
  - units
  - precision
  - enum values

---

## 5. Testing expectations (extended)

### Backend
- Unit tests for domain logic
- Handler tests for API → domain mapping
- Mock internal services, not HTTP

### API
- Schema-level tests (even basic JSON shape validation)
- Error-path tests (invalid input, missing data)

### Web (minimum acceptable bar)
Even unfinished Web MUST have:
- build sanity check
- API mock or stub
- no hardcoded secrets/endpoints

If Web is WIP, explicitly state:
> “UI incomplete, not production-facing, read-only”

---

## 6. Review workflow additions (Web & API)

### PRs touching Web or API MUST include:
- Link to relevant `docs/` section
- API diff summary (added/changed/removed)
- Feature status declaration
- Screenshot or mock (if UI-related)

### Auto-block conditions:
- Web calls undocumented API
- API added without docs
- Backend leaks internal struct to API
- UI implies execution when backend is read-only

---

## 7. Security additions for Web/API

- Rate limiting on all public APIs
- No wallet / private key exposure
- No “admin by frontend only” logic
- All sensitive actions logged with:
  - who
  - when
  - what
  - before/after state

---

## 8. docs/ directory is SOURCE OF TRUTH

Minimum expected structure:
- `docs/api/` — endpoint definitions
- `docs/web/` — panel layout, data sources
- `docs/architecture/` — data flow & boundaries
- `docs/security/` — threat model

If code contradicts docs → code is wrong.

---

## 9. Mandatory Agent output (unchanged)

Every meaningful contribution must include:
- Summary
- Files changed
- Docs updated (yes/no + path)
- Tests run
- Risk assessment
- Follow-ups

---

## 10. Golden rule

> Web is a consumer.
> API is a contract.
> Core logic does not know the UI exists.

Any change that violates this rule must be rejected.

END.

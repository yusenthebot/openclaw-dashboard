# Changelog

## v2026.3.3 — 2026-03-03

### Fixed
- **Cache coherence bug** — `getDataRawCached()` now invalidates the parsed data cache when the raw cache is updated. Previously, `/api/refresh` could bump the shared mtime without clearing the parsed cache, causing `/api/chat` to silently use stale dashboard data for its AI context.
- **HEAD requests on `/api/refresh`** — HEAD responses no longer write a body (HTTP spec compliance). Added missing `Content-Length` header.
- **Gateway error status code** — `/api/chat` now returns HTTP 502 (Bad Gateway) instead of 200 when the upstream gateway fails. Clients can now distinguish "AI answered" from "AI is down".
- **`.env` quote stripping** — `readDotenv()` now strips surrounding double and single quotes from values (e.g., `KEY="value"` → `value`).

### Added
- **Graceful shutdown** — Go binary now handles SIGINT/SIGTERM signals and drains in-flight requests (5s timeout) before exiting. Clean container stops, no more orphaned `refresh.sh` processes or `data.json.tmp` files.
- **Gateway response size limit** — `callGateway()` now caps response body at 1MB via `LimitReader`. Prevents memory exhaustion from a misbehaving gateway.
- **Comprehensive Go test suite** — 39 tests with `-race` flag covering:
  - Cache coherence between raw and parsed caches
  - HEAD vs GET behavior for all endpoints
  - Static file allowlist and path traversal defense
  - CORS origin reflection and rejection
  - Chat input validation (empty, too long, too large, invalid JSON)
  - Gateway calls (success, errors, empty responses, oversized responses)
  - System prompt building with empty and populated data
  - Config loading (defaults, overrides, invalid JSON, zero-value clamping)
  - Dotenv parsing (comments, blanks, equals-in-value, quotes, missing file)
  - Version detection (git tag, VERSION file, fallback)

### Changed
- **Agent Bindings UI** — Changed from `flex-wrap` to a symmetric 2-column CSS grid layout. Cards are now equal-width with text overflow ellipsis for long names.

---

## v2026.2.24 — 2026-02-24

### Fixed
- **Accurate model display for sub-agents** — sub-agents now show their actual model (e.g., "GPT 5.3 Codex", "Claude Opus 4.6") instead of defaulting to the parent agent's model (k2p5). Root cause: sub-agents store model in `providerOverride`/`modelOverride` fields, which the dashboard wasn't reading.
- **5-level model resolution priority chain** — Gateway live data → providerOverride/modelOverride → session store `model` field → JSONL `model_change` event → agent default. Ensures the most accurate model is always displayed.

### Added
- **Gateway API query** in `refresh.sh` — queries `openclaw sessions --json` for live session model data as the primary source of truth. Graceful fallback if gateway is unavailable.
- **Model alias resolution** — sub-agent models now display friendly names (e.g., "GPT 5.3 Codex" instead of "openai-codex/gpt-5.3-codex").

## v2026.2.23 — 2026-02-23

### Added
- **AI Chat panel** (`💬`) — floating action button opens a chat panel backed by your OpenClaw gateway
  - Natural language queries about costs, sessions, cron jobs, alerts, and configuration
  - System prompt built from live `data.json` on every request (always up to date)
  - Stateless gateway calls via OpenAI-compatible `/v1/chat/completions` — no agent memory bleed
  - Conversation history with configurable depth (`ai.maxHistory`, default 6 turns)
  - 4 quick-action chips for common questions
  - Dismissible with Escape key or clicking outside
- **`/api/chat` endpoint** in `server.py` — POST `{"question": "...", "history": [...]}` returns `{"answer": "..."}` or `{"error": "..."}`
- **`read_dotenv()`** — parses `~/.openclaw/.env` to load `OPENCLAW_GATEWAY_TOKEN` without requiring env var exports
- **`build_dashboard_prompt()`** — compresses `data.json` into a structured ~300-token system prompt
- **`call_gateway()`** — stateless HTTP call to the OpenClaw gateway with 60s timeout
- New `ai` section in `config.json`: `enabled`, `gatewayPort`, `model`, `maxHistory`, `dotenvPath`
- 14 new tests in `tests/test_chat.py` covering config validation, dotenv parsing, prompt building, gateway error handling, and HTTP endpoint behaviour (AC-CHAT-1 through AC-CHAT-8)
- Converted `test_critical.py` and `test_hierarchy_recent.py` from pytest to stdlib `unittest` — no external test dependencies required

### Prerequisites (one-time setup in `~/.openclaw/openclaw.json`)
The gateway's `chatCompletions` endpoint is disabled by default. Enable it once:
```json
"gateway": {
  "http": { "endpoints": { "chatCompletions": { "enabled": true } } }
}
```
The gateway hot-reloads this change — no restart needed.

### Changed
- Architecture diagram updated to show `/api/chat` endpoint

---

## v2026.2.21 — 2026-02-21

### Fixed
- Handle dict-style model config for agents in refresh script

### Changed
- Ignore `.worktrees/` directory in git

---

## v2026.2.20 — 2026-02-20

### Added
- 6 built-in themes (Midnight, Nord, Catppuccin Mocha, GitHub Light, Solarized Light, Catppuccin Latte)
- Theme switcher in header bar, persisted via `localStorage`
- Custom theme support via `themes.json`
- Sub-agent activity panel with 7d/30d tabs
- Charts & trends panel (cost trend, model breakdown, sub-agent activity) — pure SVG
- Token usage panel with per-model breakdown

### Fixed
- Dynamic channel/binding status for Slack/Discord

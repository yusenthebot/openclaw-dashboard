# OpenClaw Dashboard

> A terminal-aesthetic control panel for [OpenClaw](https://github.com/openclaw/openclaw) Gateway — real-time agent monitoring, session chat with file/image upload, billing-aware cost display, and a warm "Subdued Succulent" dark theme.

![Dashboard Overview](screenshots/00-full-dashboard.png)

---

## Features

- **Session Chat** — Talk directly to your OpenClaw main session. Supports image, PDF, and file uploads — Will uses its native tools on your attachments.
- **Session Inspector** — Click any session to view the last 30 messages, role badges, timestamps, tool-call labels, and send messages inline.
- **Quick Actions** — Run cron jobs, inject messages, force refresh, or restart the Gateway — all without leaving the UI.
- **Live Log Streamer** — Real-time SSE tail of `/tmp/dash.log`. Filter by ALL / DASH / GATEWAY. Pause, resume, and clear.
- **Billing-Aware Cost Display** — Auto-detects `subscription` / `api` / `local` mode. Subscription users see token counts, not misleading dollar figures.
- **Live Telemetry Charts** — Daily cost trend, cost by model, sub-agent activity (Chart.js).
- **30-Day Cost Heatmap** — Colour-coded daily spend grid.
- **Cron Job Grid** — Status cards with last/next run timestamps.
- **Token Usage Tables** — Per-model input / output / cache-read breakdown with TODAY / 7D / 30D / ALL tabs.
- **Sub-Agent Run Cards** — Grid + table of every sub-agent run: cost, duration, model, status.
- **Agent Configuration Panel** — Full view of agents, routing chains, channels, hooks, capabilities, bindings.
- **System Metrics** — CPU / RAM / SWAP / DISK pills with configurable warn/critical thresholds.
- **Alerts Panel** — Failed crons, high cost, high context usage.

---

## Quick Start

```bash
# Build (requires Go ≥ 1.21)
git clone https://github.com/yusenthebot/openclaw-dashboard
cd openclaw-dashboard
go build -o openclaw-dashboard .
./openclaw-dashboard --port 8080
# → http://127.0.0.1:8080
```

Minimal `config.json`:

```json
{
  "bot": { "name": "MyAgent" },
  "billingMode": "subscription",
  "timezone": "UTC",
  "server": { "port": 8080, "host": "127.0.0.1" },
  "ai": { "enabled": true, "gatewayPort": 18789 }
}
```

Run in background:

```bash
nohup ./openclaw-dashboard --port 8080 > /tmp/dash.log 2>&1 &
```

---

## File Upload via Chat

| Upload type | How OpenClaw handles it |
|-------------|------------------------|
| Image (jpg/png/gif/webp) | Saved to `~/clawd/uploads/`, Will uses `image` tool |
| PDF | Will uses `pdf` tool |
| Code / text / JSON / Markdown | Will uses `Read` tool |

```bash
# curl example
curl -X POST http://127.0.0.1:8080/api/session/send \
  -F "message=What's in this image?" \
  -F "files[]=@screenshot.png"
```

---

## Configuration Reference

| Field | Default | Description |
|-------|---------|-------------|
| `bot.name` | `"OpenClaw Dashboard"` | Agent display name |
| `billingMode` | `"api"` | `subscription` / `api` / `local` |
| `timezone` | `"UTC"` | Display timezone (IANA) |
| `refresh.intervalSeconds` | `30` | Poll interval |
| `server.port` | `8080` | HTTP port |
| `server.host` | `"127.0.0.1"` | Bind address |
| `ai.gatewayPort` | `18789` | OpenClaw Gateway port |
| `alerts.dailyCostHigh` | `50` | Red alert threshold ($) |
| `alerts.dailyCostWarn` | `20` | Yellow alert threshold ($) |
| `alerts.contextPct` | `80` | Context usage warning (%) |

---

## Architecture

```
openclaw-dashboard
  ├── main.go              CLI flags, startup
  ├── server.go            HTTP server, routing, /api/refresh
  ├── config.go            Config struct, JSON loader
  ├── system_service.go    CPU/RAM/disk/swap collection
  ├── chat.go              Embedded AI assistant bridge
  ├── session_chat.go      Session chat: send/stream/history + file upload
  └── index.html           Embedded SPA (all UI/JS/CSS)
```

> The Go binary embeds `index.html` at compile time. After editing UI, rebuild: `go build -o openclaw-dashboard .`

---

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/refresh` | Full dashboard data |
| `GET` | `/api/system` | System metrics only |
| `GET` | `/api/session/history` | Last N session messages |
| `GET` | `/api/session/stream` | SSE stream of new messages |
| `POST` | `/api/session/send` | Send message + files |
| `GET` | `/api/logs/stream` | SSE log stream |
| `POST` | `/api/actions/run-cron` | Trigger a cron job |
| `POST` | `/api/actions/restart-gateway` | Restart Gateway |

---

## What's Different from Upstream

This fork of [mudrii/openclaw-dashboard](https://github.com/mudrii/openclaw-dashboard) adds:

- ✅ Session chat panel with image/file upload
- ✅ Session Inspector drawer
- ✅ Quick Actions panel
- ✅ Live Log Streamer (SSE)
- ✅ Billing mode system (subscription / API / local)
- ✅ Subdued Succulent warm dark theme
- ✅ Chart.js bar charts
- ✅ 30-Day Cost Heatmap
- ✅ Sub-agent run cards + token usage tables
- ✅ Agent configuration panel
- ✅ Vanta.js DOTS background

---

## Requirements

- OpenClaw Gateway running locally
- Go ≥ 1.21

## License

MIT

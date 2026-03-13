# OpenClaw Dashboard

> [OpenClaw](https://github.com/openclaw/openclaw) Gateway 的终端风格控制面板 — 实时 Agent 监控、Session 聊天（支持图片/文件上传）、按计费模式显示成本，以及暖色调"沙漠多肉"深色主题。

![Dashboard Overview](screenshots/00-full-dashboard.png)

---

## 功能一览

- **Session 聊天** — 直接与 OpenClaw 主 Session 对话。支持图片、PDF、代码文件上传，Will 会用原生工具处理你的附件。
- **Session 检查器** — 点击任意 Session 查看最近 30 条消息，显示角色标签、时间戳、工具调用记录，支持直接发消息。
- **快捷操作** — 一键运行 Cron 任务、注入消息、强制刷新、重启 Gateway，无需离开界面。
- **实时日志流** — SSE 实时追踪 `/tmp/dash.log`，支持 ALL / DASH / GATEWAY 过滤，可暂停、恢复、清空。
- **计费感知成本显示** — 自动识别 `subscription` / `api` / `local` 模式。订阅用户显示 Token 数量而非误导性的美元金额。
- **实时遥测图表** — 每日成本趋势、按模型分类成本、Sub-Agent 活动（Chart.js）。
- **30 天成本热力图** — 按颜色区分每日消费。
- **Cron 任务网格** — 状态卡片，显示上次/下次运行时间。
- **Token 用量表** — 按模型拆分输入/输出/缓存读取，支持 今日 / 7天 / 30天 / 全部 切换。
- **Sub-Agent 运行卡片** — 网格+表格展示每次 Sub-Agent 运行：成本、耗时、模型、状态。
- **Agent 配置面板** — 完整展示 Agent 列表、路由链、Channel、Hook、能力绑定。
- **系统指标** — CPU / 内存 / SWAP / 磁盘，支持配置警告/临界阈值。
- **告警面板** — Cron 失败、成本超限、Context 使用率过高。

---

## 快速开始

```bash
# 构建（需要 Go ≥ 1.21）
git clone https://github.com/yusenthebot/openclaw-dashboard
cd openclaw-dashboard
go build -o openclaw-dashboard .
./openclaw-dashboard --port 8080
# → http://127.0.0.1:8080
```

最简 `config.json`：

```json
{
  "bot": { "name": "MyAgent" },
  "billingMode": "subscription",
  "timezone": "Asia/Shanghai",
  "server": { "port": 8080, "host": "127.0.0.1" },
  "ai": { "enabled": true, "gatewayPort": 18789 }
}
```

后台运行：

```bash
nohup ./openclaw-dashboard --port 8080 > /tmp/dash.log 2>&1 &
```

---

## 文件上传（聊天面板）

| 文件类型 | OpenClaw 处理方式 |
|---------|-----------------|
| 图片（jpg/png/gif/webp） | 保存到 `~/clawd/uploads/`，Will 使用 `image` 工具 |
| PDF | Will 使用 `pdf` 工具 |
| 代码 / 文本 / JSON / Markdown | Will 使用 `Read` 工具 |

```bash
# curl 示例
curl -X POST http://127.0.0.1:8080/api/session/send \
  -F "message=这张图里有什么？" \
  -F "files[]=@screenshot.png"
```

---

## 配置说明

| 字段 | 默认值 | 说明 |
|------|--------|------|
| `bot.name` | `"OpenClaw Dashboard"` | Agent 显示名称 |
| `billingMode` | `"api"` | `subscription` / `api` / `local` |
| `timezone` | `"UTC"` | 显示时区（IANA 格式） |
| `refresh.intervalSeconds` | `30` | 刷新间隔（秒） |
| `server.port` | `8080` | HTTP 端口 |
| `server.host` | `"127.0.0.1"` | 绑定地址 |
| `ai.gatewayPort` | `18789` | OpenClaw Gateway 端口 |
| `alerts.dailyCostHigh` | `50` | 红色告警阈值（$） |
| `alerts.dailyCostWarn` | `20` | 黄色告警阈值（$） |
| `alerts.contextPct` | `80` | Context 使用率警告（%） |

---

## 架构

```
openclaw-dashboard
  ├── main.go              命令行参数、启动逻辑
  ├── server.go            HTTP 服务、路由、/api/refresh
  ├── config.go            配置结构体、JSON 加载
  ├── system_service.go    CPU/内存/磁盘/Swap 采集
  ├── chat.go              内嵌 AI 助手桥接
  ├── session_chat.go      Session 聊天：发送/流式/历史记录 + 文件上传
  └── index.html           嵌入式 SPA（全部 UI/JS/CSS）
```

> Go 二进制在编译时嵌入 `index.html`。修改前端后重新构建：`go build -o openclaw-dashboard .`

---

## API 接口

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET` | `/api/refresh` | 完整 Dashboard 数据 |
| `GET` | `/api/system` | 系统指标 |
| `GET` | `/api/session/history` | 最近 N 条 Session 消息 |
| `GET` | `/api/session/stream` | SSE 新消息流 |
| `POST` | `/api/session/send` | 发送消息 + 文件 |
| `GET` | `/api/logs/stream` | SSE 日志流 |
| `POST` | `/api/actions/run-cron` | 触发 Cron 任务 |
| `POST` | `/api/actions/restart-gateway` | 重启 Gateway |

---

## 相比上游的改动

本项目 Fork 自 [mudrii/openclaw-dashboard](https://github.com/mudrii/openclaw-dashboard)，新增：

- ✅ 支持图片/文件上传的 Session 聊天面板
- ✅ Session 检查器侧抽屉
- ✅ 快捷操作面板
- ✅ SSE 实时日志流
- ✅ 计费模式系统（订阅 / API / 本地）
- ✅ 暖色调"沙漠多肉"深色主题
- ✅ Chart.js 柱状图
- ✅ 30 天成本热力图
- ✅ Sub-Agent 运行卡片 + Token 用量表
- ✅ Agent 配置面板
- ✅ Vanta.js DOTS 动态背景

---

## 环境要求

- 本地运行中的 OpenClaw Gateway
- Go ≥ 1.21

## License

MIT

# 运行与运维

Ternura 有三种入口：CLI 适合一次性任务，Daemon 负责长期服务，飞书 Channel 通过 Daemon 接收和回复消息。三种入口最终复用同一套 Agent runtime。

## CLI

执行一次对话：

```bash
GOCACHE=$PWD/.gocache go run ./cmd/ternura -q "请读取 README.md 并总结项目目标"
```

执行可重复 Eval：

```bash
GOCACHE=$PWD/.gocache go run ./cmd/ternura -eval eval.example.jsonl
```

Eval 文件每行是一个独立用例，支持 `expect_contains`、`expect_not_contains`、`require_tools`、`max_model_calls`、`max_tool_calls` 和 `timeout_seconds` 断言。

完整参数：

```bash
go run ./cmd/ternura -q "prompt text"
go run ./cmd/ternura -serve
go run ./cmd/ternura -serve -addr :8081
go run ./cmd/ternura -eval eval.example.jsonl
```

| 参数 | 说明 |
| --- | --- |
| `-q` | CLI 模式的用户输入 |
| `-serve` | 启动后台服务 |
| `-addr` | HTTP 监听地址，默认 `:8080` |
| `-eval` | 运行 JSONL 回归评测，不启动 Daemon |

## Daemon

开发环境直接启动：

```bash
GOCACHE=$PWD/.gocache go run ./cmd/ternura -serve
```

Daemon 会启动：

- 飞书 WebSocket 长连接或 HTTP 事件入口。
- 后台 cron runner。
- 异步 Task API 和 Agent Card。
- Runtime Monitor、SSE 状态流和内嵌管理页。
- 健康检查接口。

长期运行时应交给 `launchd`、`screen` 或其他进程管理器，并保持同一工作区只有一个有效实例监听端口。更新版本时先构建新二进制，再终止旧进程并启动新版本，避免两个飞书长连接同时消费消息。

## 管理页

Daemon 启动后访问：

[http://127.0.0.1:8080/admin/](http://127.0.0.1:8080/admin/)

管理页是编译进 Go 二进制的静态资源，不需要单独启动前端服务。当前提供：

- 运行中任务的来源、阶段、耗时和模型/工具调用数。
- 历史任务的最终回答、trace、Evidence Ledger、指标和 Artifact。
- 运行取消，以及 checkpoint 工具调用的批准或拒绝。
- 会话数量、最近状态、消息/run/todo 计数。
- 定时任务计划、下次运行时间和最近结果。
- 模型、Provider、上下文窗口、飞书连接、Skill、Tool、MCP 和记忆计数。
- 在系统页下钻查看 Skill/Tool 清单、当前会话运行，以及长期、工具和短期记忆详情。

任务详情中的“证据账本”可以展开查看证据 ID、来源 URL、可引用状态、摘要、采集时间和内容哈希。`web_search` 结果会显示为发现/审计，成功 `web_fetch` 才显示为可引用来源。

页面通过 `/api/events` 的 SSE 流实时更新。运行概览只返回记忆计数，不返回短期记忆摘要或消息正文。

## HTTP 接口

| 接口 | 用途 |
| --- | --- |
| `GET /healthz` | 健康检查 |
| `GET /admin/` | 管理页 |
| `GET /api/runtime` | 守护进程和活动运行概览 |
| `GET /api/events` | Runtime SSE 事件流 |
| `GET /api/sessions` | 会话元数据摘要 |
| `GET /api/cron/jobs` | 定时任务只读列表 |
| `GET /api/system/<section>` | Skill、Tool、会话和记忆详情 |
| `GET /api/agent-card` | Agent 能力描述 |
| `POST /api/tasks` | 异步提交任务 |
| `GET /api/tasks/<run_id>` | 查询完整运行记录 |
| `POST /api/tasks/<run_id>/cancel` | 取消运行或待审批任务 |
| `POST /api/tasks/<run_id>/decision` | 批准或拒绝待审批工具 |
| `POST /api/feishu/events` | 飞书 HTTP 事件入口 |

提交任务：

```bash
curl -X POST http://127.0.0.1:8080/api/tasks \
  -H 'Content-Type: application/json' \
  -d '{"input":"读取 README.md 并总结"}'
```

批准待审批任务：

```bash
curl -X POST http://127.0.0.1:8080/api/tasks/run-xxx/decision \
  -H 'Content-Type: application/json' \
  -d '{"approved":true,"reason":"confirmed"}'
```

配置 `TERNURA_API_TOKEN` 后，额外添加：

```bash
-H 'Authorization: Bearer your-token'
```

## 数据落盘

运行数据保存在 `.ternura/`：

```text
.ternura/
├── index.json
├── cron/
│   └── jobs.json
├── checkpoints/
│   └── *.checkpoint
├── memory/
│   └── long_term.json
└── sessions/
    └── session-xxx/
        ├── memory.json
        ├── meta.json
        ├── messages.json
        ├── todos.json
        └── runs/
            ├── run-xxx.json
            └── run-yyy.json
```

| 文件 | 内容 |
| --- | --- |
| `index.json` | 当前 session 和所有 session 的轻量摘要 |
| `cron/jobs.json` | 调度、目标 session、回传地址和运行历史 |
| `memory/long_term.json` | 跨 session 的长期记忆 |
| `sessions/<id>/memory.json` | 当前 session 的短期记忆 |
| `meta.json` | session 元信息和 run 顺序 |
| `messages.json` | 用于恢复模型上下文的 user/assistant 历史 |
| `todos.json` | `update_todos` 维护的任务步骤 |
| `runs/*.json` | 状态、输入、回答、Artifact、trace、metrics、错误和耗时 |
| `checkpoints/*.checkpoint` | Eino interrupt 的可恢复运行状态 |

旧版 `.ternura/session.json` 仍会迁移到拆分结构，并备份为 `.ternura/session.legacy.json`。旧版 cron `schedules.json` 不再迁移。

`.ternura/` 已被 `.gitignore` 忽略。

## Session 与飞书

飞书消息进入独立的 `feishu-...` session：

- 私聊按发送者隔离。
- 群聊默认要求 @ 机器人。
- `FEISHU_TOPIC_ISOLATION=true` 时，每个话题或根消息使用独立 session。
- 飞书创建的 cron 会保存回传地址，到点后把结果发回原聊天。

在飞书发送以下内容会切换到一个真正的新 session：

```text
new session
new chat
reset session
新会话
新对话
重新开始
清空会话
```

旧会话仍保留用于审计，但它的历史消息、短期记忆、工具摘要和待办不会进入新 session 的模型上下文。

## 定时任务

明确的相对时间提醒，例如“2 分钟后提醒我吃饭”，会先经过确定性解析并直接创建 cron job，避免模型只在文本里声称已设置。

复杂时间表达、循环任务和任务管理由 `cron` 工具完成。后台 runner 到点后恢复任务绑定的 session，执行 Agent，写入 run 记录，并按需要投递到外部 Channel。

最终回复里的任务 ID 不能作为唯一事实来源；真实状态以 `.ternura/cron/jobs.json`、管理页或 `cron` 工具结果为准。

## 快速排障

检查服务：

```bash
curl -fsS http://127.0.0.1:8080/healthz
curl -fsS http://127.0.0.1:8080/api/runtime
```

常见检查顺序：

1. 确认端口只有一个 Ternura 进程监听。
2. 查看 Daemon 日志是否出现 `feishu websocket connected`。
3. 打开管理页检查飞书连接和活动运行。
4. 查询对应 `run_id`，区分模型错误、工具错误、审批等待和 Channel 发送错误。
5. 检查 `.ternura/cron/jobs.json` 或 session run 文件验证真实持久化状态。

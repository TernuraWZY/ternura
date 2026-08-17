# Ternura

Ternura 是一个使用 Go 和 Eino ADK 构建的轻量级通用 Agent。它把模型、原生 Tool、上下文工程、记忆、定时任务、人工审批和外部 Channel 组合成一套可长期运行、可恢复、可观测的 Agent runtime。

同一套 Agent 能力可以通过三种入口使用：

- **CLI**：执行一次性任务和本地调试。
- **Daemon / Task API**：运行异步任务、cron 和管理控制台。
- **Feishu Channel**：通过长连接或 HTTP 事件接收消息并回复。

## 核心能力

### Agent runtime

- Eino ADK `ChatModelAgent + Runner` 原生 ReAct 编排。
- Eino 原生 Tool、并行工具调用、动态 `tool_search` 和 MCP Tool。
- 模型重试、fallback model、运行预算和上下文超限恢复。
- checkpoint/interrupt 高风险工具审批与恢复。

### 上下文与记忆

- 保持 user/assistant/tool 语义链的 ContextBuilder。
- 大工具结果落盘、分层压缩和 LLM summary compact。
- 按 query 召回并由 AI summarizer 判断是否注入的 Active Memory。
- session 短期记忆与显式长期记忆。

### 运行与集成

- 可恢复 session、结构化 trace/metrics、Artifact 和异步 Task API。
- 飞书 WebSocket / HTTP Channel、Reaction、进度卡片和话题隔离。
- one-shot、interval、cron schedule 和结果回传。
- OpenClaw / AgentSkills 风格 `SKILL.md` 与声明式委派 Agent。
- 内嵌 Runtime 管理页，实时查看任务、审批、会话、cron 和系统状态。

## 快速开始

### 1. 环境

- Go 1.25 或更新版本。
- 一个 OpenAI-compatible 模型服务的 API Key。

### 2. 配置

```bash
cp .env.example .env
```

MiniMax 中国区最小配置：

```env
LLM_PROVIDER=minimax
MINIMAX_BASE_URL=https://api.minimaxi.com/v1
MINIMAX_API_KEY=sk-your-minimax-key-here
MINIMAX_MODEL=MiniMax-M3
```

OpenAI-compatible 最小配置：

```env
LLM_PROVIDER=openai
OPENAI_BASE_URL=https://api.openai.com/v1
OPENAI_API_KEY=sk-your-openai-key-here
OPENAI_MODEL=gpt-5.2
```

飞书、运行预算、MCP、审批和 API Token 见 [配置说明](docs/configuration.md)。

### 3. 启动 Daemon

```bash
GOCACHE=$PWD/.gocache go run ./cmd/ternura -serve
```

启动后访问：

- 管理页：[http://127.0.0.1:8080/admin/](http://127.0.0.1:8080/admin/)
- 健康检查：[http://127.0.0.1:8080/healthz](http://127.0.0.1:8080/healthz)
- Agent Card：[http://127.0.0.1:8080/api/agent-card](http://127.0.0.1:8080/api/agent-card)

### 4. 运行 CLI

```bash
GOCACHE=$PWD/.gocache go run ./cmd/ternura -q "请读取 README.md 并总结项目目标"
```

运行 JSONL Eval：

```bash
GOCACHE=$PWD/.gocache go run ./cmd/ternura -eval eval.example.jsonl
```

更多运行方式和 API 示例见 [运行与运维](docs/operations.md)。

## 项目结构

```text
.
├── agent/                 # Agent runtime、Eino ADK、ContextBuilder、Skill 与 Hook
├── agents/                # 声明式委派 Agent
├── cmd/ternura/           # 可执行入口
├── config/                # 模型和 Provider 配置
├── docs/                  # 架构、配置、运维和路线图
├── internal/app/          # 应用装配、AgentSession、Task API、memory 和管理页
├── internal/cron/         # 调度、持久化和运行历史
├── internal/feishu/       # 飞书 Channel adapter
├── internal/mcpruntime/   # MCP 服务和 Tool 装配
├── skills/                # Agent 运行时 SKILL.md
└── tool/                  # 本地 Eino Tool
```

包边界和完整运行流程见 [系统架构](docs/architecture.md)。

## 内置工具

| 分类 | 工具 |
| --- | --- |
| 工作区 | `read`、`write`、`edit`、`bash` |
| 任务状态 | `update_todos`、`compact` |
| 记忆 | `remember`、`forget_memory` |
| 调度 | `cron` |
| 网络 | `web_search`、`web_fetch` |
| 委派 | `delegate_agent` |
| 扩展 | 动态加载的 MCP Tools |

Ternura 不在统一的 `BeforeRun` 阶段做硬编码意图路由。模型根据系统提示、Skill 和 Tool schema 决定是否调用工具；Harness 只在审批、真实副作用、cron 状态和伪工具调用等边界提供保护。

## 文档

| 文档 | 内容 |
| --- | --- |
| [文档索引](docs/README.md) | 文档导航和 Markdown 文件边界 |
| [配置说明](docs/configuration.md) | Provider、飞书、预算、MCP、审批和认证 |
| [运行与运维](docs/operations.md) | CLI、Daemon、管理页、API、落盘和排障 |
| [系统架构](docs/architecture.md) | Eino ADK、AgentSession、上下文、记忆、Skill 和 Hook |
| [路线图](docs/roadmap.md) | 已完成能力和后续建设项 |

`skills/**/SKILL.md` 和 `agents/*.md` 是运行时协议文件，不属于普通说明文档，目录位置和 frontmatter 都有语义。

## 开发

运行测试：

```bash
GOCACHE=$PWD/.gocache go test ./...
```

竞态检查：

```bash
go test -race ./agent ./internal/app ./internal/feishu ./internal/mcpruntime ./tool
```

格式化 Go 文件：

```bash
gofmt -w $(rg --files -g '*.go')
```

## 安全

Ternura 可以修改文件、执行 shell 命令并访问外部系统。请保持危险工具审批开启，限制工作区和网络访问范围，不要把真实密钥写入仓库。详细建议见 [配置说明](docs/configuration.md#安全建议)。

## 参考

- [CloudWeGo Eino](https://www.cloudwego.io/docs/eino/overview/)
- [MiniMax OpenAI-compatible API](https://platform.minimax.io/docs/api-reference/text-openai-api)

# Ternura

Ternura 是一个完整、独立的轻量级通用 Agent 项目。它使用 Go 实现，通过 Eino 的 OpenAI 兼容 ChatModel 连接大模型，并为模型提供一组受控的本地工具，让模型可以围绕对话、规划、写作、分析、代码和本地自动化任务开展工作。

项目支持三种运行入口：

- CLI 模式：适合一次性任务和命令行调试
- Daemon 模式：启动飞书接入、HTTP 回调和后台 cron runner
- Feishu Channel 模式：通过长连接或 HTTP 回调接收飞书消息，把飞书接入同一个 Agent Harness

## 功能

- 基于 Eino ChatModel 的 OpenAI 兼容模型客户端
- 支持 MiniMax 中国区配置
- System Prompt 定位为通用工具型 Agent，不再限制为 coding assistant
- 支持连续对话上下文
- ContextBuilder 支持运行时上下文优先级、字符预算、历史消息裁剪和记忆按需注入
- 支持工具调用和工具结果回传
- 支持 SkillRegistry：把工具、Hook 和模型运行时说明按能力模块组合，再统一装配进 Agent
- 提供 Hook 扩展模块，可在 run、模型调用和工具调用生命周期中注入上下文、禁用工具、拦截工具和记录结果
- 内置运行时上下文 Hook：每轮注入当前时间和时区，并在识别到提醒或取消提醒意图时注入调度工具使用规范
- 内置轻量状态归因 Guard：防止模型把伪工具调用、未执行的命令、文件写入或记忆变更说成真实结果
- 内置 `read`、`write`、`edit`、`bash`、`update_todos`、`remember`、`forget_memory`、`cron` 和 `web_fetch` 工具
- 支持短期记忆和长期记忆：短期记忆按 session 自动滚动更新，长期记忆通过工具显式写入和删除，并在模型调用前通过 Active Memory 按需召回
- 提供命令行入口
- 提供后台 daemon，负责飞书事件、HTTP 回调、health check 和 cron runner
- 每次请求都会生成 `run_id`，记录运行开始、结束、失败、取消和耗时
- 支持把对话历史、工具调用 trace 和最终回复持久化为可恢复会话
- 支持保留可恢复的历史 session
- 支持一次性与循环定时任务：任务持久化后由后台 runner 到点触发 Agent，并把结果写回对应 session
- 支持飞书接入：通过 Feishu/Lark 长连接或事件回调收消息，按聊天维度隔离 session，并把 Agent 回复发回飞书

## 环境要求

- Go 1.25 或更新版本
- 一个 OpenAI 兼容模型服务的 API Key

## 配置

复制 `.env.example` 为 `.env`，然后填写模型配置。

MiniMax 中国区示例：

```env
LLM_PROVIDER=minimax
MINIMAX_BASE_URL=https://api.minimaxi.com/v1
MINIMAX_API_KEY=sk-your-minimax-key-here
MINIMAX_MODEL=MiniMax-M3
```

默认 MiniMax 模型为 `MiniMax-M3`，本地模型配置按 M3 的 1M context 能力设置 context window。

OpenAI 兼容服务示例：

```env
LLM_PROVIDER=openai
OPENAI_BASE_URL=https://api.openai.com/v1
OPENAI_API_KEY=sk-your-openai-key-here
OPENAI_MODEL=gpt-5.2
```

`.env` 已经被 `.gitignore` 忽略。不要把真实 API Key 提交到代码仓库。

飞书接入示例：

```env
FEISHU_ENABLED=true
FEISHU_APP_ID=cli_xxx
FEISHU_APP_SECRET=your-feishu-app-secret
FEISHU_EVENT_MODE=websocket
FEISHU_VERIFICATION_TOKEN=your-event-verification-token
FEISHU_ALLOW_OPEN_IDS=*
FEISHU_GROUP_POLICY=mention
FEISHU_REPLY_TO_MESSAGE=true
FEISHU_TOPIC_ISOLATION=true
FEISHU_PROCESSING_REACTION=true
FEISHU_PROCESSING_DELAY=1s
FEISHU_PROCESSING_REACTION_TYPE=OneSecond
```

默认 `FEISHU_EVENT_MODE=websocket`，对应飞书开放平台里的“长连接”订阅方式。启用长连接后不需要公网回调地址，只需要在飞书后台开启事件订阅并订阅 `im.message.receive_v1`，服务启动时会用 App ID / Secret 建立 WebSocket 长连接。

`FEISHU_PROCESSING_REACTION=true` 时，如果 Agent 在 `FEISHU_PROCESSING_DELAY` 内还没有生成最终回复，会先给原消息添加一个 `FEISHU_PROCESSING_REACTION_TYPE` 表情回应，用于确认机器人已经收到并开始处理。

运行预算配置：

```env
TERNURA_AGENT_TURN_TIMEOUT=4m
FEISHU_REPLY_TIMEOUT=15s
TERNURA_MAX_REACT_STEPS=24
TERNURA_MAX_MODEL_CALLS=16
TERNURA_MAX_TOOL_CALLS=12
TERNURA_MAX_WEB_FETCH_CALLS=5
TERNURA_WEB_FETCH_TIMEOUT=8s
TERNURA_WEB_FETCH_MAX_CHARS=5000
```

这些默认值用于防止单轮交互在工具调用和网页抓取之间无限拉长。`web_fetch` 现在会对搜索结果页、验证码/反爬页面和非 2xx HTTP 响应快速标记为不可用证据；达到抓取上限时会提示“没有 fetch 到更多有效网页信息”，模型应基于已有证据停止探索并说明未验证部分。

飞书回复发送使用独立的 `FEISHU_REPLY_TIMEOUT`，即使 Agent 本轮达到 `TERNURA_AGENT_TURN_TIMEOUT`，也会尝试向飞书客户端发送一条可见的超时说明，而不是静默失败。

如果模型把 MiniMax 风格的 `<invoke name="...">` / `</minimax:tool_call>` 工具调用标记作为普通文本吐出，grounding guard 会拦截这类伪工具调用，不会把它直接发给用户；如果还有预算，会强制重试并要求真正调用对应工具。

飞书回复里的过程信息会对邮箱地址做脱敏，避免网页抓取结果中的联系邮箱触发飞书消息审计而导致整条回复发送失败。

如果改用 HTTP 回调，可以设置：

```env
FEISHU_EVENT_MODE=http
```

然后在飞书开放平台里把事件订阅地址配置为：

```text
https://your-public-domain/api/feishu/events
```

HTTP 回调模式下，本地开发才需要用 ngrok、frp、Cloudflare Tunnel 等方式把 `http://localhost:8080/api/feishu/events` 暴露给飞书。当前 HTTP 回调实现支持未加密事件；如果在飞书后台开启了事件加密，需要先关闭加密或后续补充解密模块。长连接模式由飞书 SDK 负责建连和重连，不使用这个 HTTP 回调地址。

## 运行 CLI

在项目根目录执行：

```bash
GOCACHE=$PWD/.gocache go run ./cmd/ternura -q "请读取 README.md 并总结项目目标"
```

也可以让 Agent 使用本地工具完成任务：

```bash
GOCACHE=$PWD/.gocache go run ./cmd/ternura -q "在当前目录下创建一个 TODO.md，内容为 1. 研究 agent"
```

## 运行后台服务

启动 daemon：

```bash
GOCACHE=$PWD/.gocache go run ./cmd/ternura -serve
```

daemon 会启动：

- 飞书 WebSocket 长连接，或者 HTTP 事件回调 `/api/feishu/events`
- 后台 cron runner，到点后恢复对应 session 并执行 Agent
- 健康检查接口 `/healthz`

本地监听地址默认是 `:8080`，可以通过 `-addr` 修改：

```bash
GOCACHE=$PWD/.gocache go run ./cmd/ternura -serve -addr :8081
```

Ternura 不再内置浏览器查看页面。运行历史、trace、memory 和 cron 状态仍然会结构化落盘，后续可以通过日志、CLI、外部 channel 或独立观测工具读取。

会话恢复数据保存在项目根目录的 `.ternura/`，并拆成多个文件：

```text
.ternura/
├── index.json
├── cron/
│   └── jobs.json
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

- `index.json`：当前 session id 和所有 session 的轻量摘要，包括标题、创建/更新时间、run 数量、message 数量和 todo 数量
- `cron/jobs.json`：定时任务列表，包括任务名称、schedule、目标 session、下一次触发时间、运行历史和可选外部 channel 回传地址
- `memory/long_term.json`：跨 session 的长期记忆，由 `remember` 和 `forget_memory` 显式维护
- `sessions/<session-id>/memory.json`：当前 session 的短期记忆，运行成功后自动追加最近轮次并保持数量上限
- `meta.json`：单个 session 的元信息和 run 顺序
- `messages.json`：用于服务重启后恢复模型上下文的 user/assistant 对话历史
- `todos.json`：当前 session 的任务计划，由 `update_todos` 工具维护
- `runs/*.json`：每轮请求的详细记录，包括 `run_id`、状态、用户输入、最终回复、原始回复、trace、错误信息和耗时

旧版 `.ternura/session.json` 会在启动时自动迁移到拆分结构，并移动为 `.ternura/session.legacy.json` 作为备份；后续写入只更新拆分后的文件。

模型调用前会通过 `active_memory` Hook 构造 Active Memory recall：先用本地规则跳过问候、确认、感谢等低信号消息；需要召回时再调用模型把当前用户消息和最近几轮对话提取成 keywords/search query，并按 query 相关性召回少量长期记忆、当前 session 最近轮次和工具结果摘要。召回候选不会直接注入主模型，而是再交给同一模型压缩成紧凑 summary，最后注入 `Active Memory` runtime context block。若关键词提取或摘要生成失败，会回退到本地规则和截断后的召回内容，避免记忆模块阻断主 Agent；长期记忆只应该保存稳定、可复用的用户偏好、项目事实或常驻指令，不要保存 API Key、密码或一次性细节。

后台 runner 会按下一次触发时间唤醒，到点后按任务绑定的 session 恢复上下文、执行 Agent、写入 run 记录和对话历史。

对于“2 分钟后提醒我吃饭”这类明确相对时间提醒，后端会先走确定性解析并直接创建 `cron` job，不依赖模型自行调用工具；这样可以避免模型只回复“已设置”但实际没有写入 `cron/jobs.json` 的情况。更复杂的时间表达、循环任务和任务列表/删除仍由模型通过 `cron` 工具完成。

模型调用前会通过 `current_time` Hook 注入当前时间和时区，通过 `schedule_guidance` Hook 对明显的提醒、定时、闹钟、取消提醒等意图注入工具调用指导：相对时间优先使用 `delay_seconds`，绝对时间使用 ISO datetime `at`，循环间隔使用 `every_seconds`，cron 表达式使用 `cron_expr` 和可选 `tz`，最终回复只能报告工具真实返回的任务 id。普通的未来时间问题，例如“明天天气怎么样”，不会触发这段调度指导。

Harness 会在最终回复返回前执行状态归因校验：如果模型声称创建了定时任务、返回了 `cron-...` 或旧 `schedule-...` id，但本轮没有成功的 `cron` 工具结果，且该 id 也不存在于真实 cron store 中，最终回复会被替换为保护性说明，并在 trace 中追加 `Harness guard` 记录。外部端只应把 cron store 和工具结果视为真实任务状态，不能把模型文本里的 id 当成事实来源。

Harness 还会执行轻量工具证据校验：最终回复返回前，`Tool grounding guard` 只处理伪工具调用文本，以及命令执行、安装、文件修改、记忆写入/删除这类真实副作用声明。外部事实、行情、天气、新闻等是否需要工具主要交给模型和 Skill 指令判断，不再额外调用 verifier 模型做二次拆解和修复。建议类回复不会被拦截，例如“你可以运行以下命令”不会被当成已执行结果。

飞书消息会进入独立的 `feishu-...` session。私聊按发送者隔离，群聊和话题群默认需要 @ 机器人；`FEISHU_TOPIC_ISOLATION=true` 时群聊里的每个话题/根消息会拥有独立 session。飞书中创建的 `cron` 提醒会保存 Feishu 回传地址，到点触发后除了写入本地 session，也会把最终回复发回原飞书聊天。

在飞书里发送 `new session`、`new chat`、`reset session`、`新会话`、`新对话`、`重新开始` 或 `清空会话` 会重置当前飞书聊天映射到的 session：历史消息、短期记忆、工具结果摘要、工具原文产物和待办都会被清空。重置确认会作为 run 记录保留用于审计，但不会写入后续模型上下文。

`FEISHU_BOT_OPEN_ID` 推荐配置，这样群聊 @ 判断和消息清洗最精确；如果暂时没有配置，Ternura 会优先使用飞书事件里的 `mentioned_type` 判断机器人 mention。

`.ternura/` 已经被 `.gitignore` 忽略，不会被提交到仓库。新的 session 由外部 channel、CLI 或后台任务写入时自然产生。

## 命令行参数

```bash
go run ./cmd/ternura -q "prompt text"
go run ./cmd/ternura -serve
go run ./cmd/ternura -serve -addr :8081
```

- `-q`：CLI 模式下的用户输入
- `-serve`：启动后台 daemon
- `-addr`：HTTP 回调和 health check 监听地址，默认是 `:8080`

## 目录结构

```text
.
├── agent/            # Agent 循环、SkillRegistry、Hook 扩展点、RunContext 和系统提示词
├── cmd/ternura/      # 可执行入口，负责调用 internal/app
├── config/           # 模型和 Provider 配置
├── internal/app/     # CLI/daemon 装配、session、memory、runtime guidance 和 channel glue
├── internal/cron/    # cron store、schedule 计算和运行历史
├── internal/feishu/  # Feishu/Lark 事件回调、消息解析和 OpenAPI 发送
└── tool/             # 本地工具实现
```

## 工作流程

Ternura 会把当前对话、系统提示词和工具定义发送给模型。模型如果返回工具调用，Ternura 会执行对应的本地工具，并把工具结果追加回对话上下文。这个循环会持续到模型返回普通 assistant 消息为止。

Agent 创建时会先装配 `SkillRegistry`。一个 Skill 是一组面向某类能力的说明、工具和 Hook，例如当前内置的 `workspace`、`memory`、`schedule`、`web` 和 `grounding`。Registry 会去重工具、合并 Hook，并在模型调用前注入 `Enabled Skills` runtime context，让模型知道当前启用了哪些能力。

Ternura 不再在 `BeforeRun` 阶段做统一意图路由。是否需要 `cron`、`web_fetch`、`bash`、`remember` 或 workspace 工具，主要由模型根据系统提示、Skill 说明和工具 schema 自主决策；Harness 只在少数真实状态边界上做兜底，例如 cron store 归因和伪工具调用拦截。

Ternura 也支持 OpenClaw / AgentSkills 风格的 `SKILL.md` 目录。启动或创建 Agent 时会按优先级扫描这些位置：

1. `<workspace>/skills`
2. `<workspace>/.agents/skills`
3. `~/.agents/skills`
4. OpenClaw workspace：优先读取 `~/.openclaw/openclaw.json` 里的 `agents.defaults.workspace`，然后扫描 `<openclaw-workspace>/skills`；没有配置时回退到 `~/.openclaw/workspace/skills`
5. `~/.openclaw/skills`，用于兼容旧式 OpenClaw skill 目录
6. SkillHub 安装目录 `~/.skillhub/skills`
7. `TERNURA_SKILL_DIRS` 指定的额外目录

每个 Skill 是一个包含 `SKILL.md` 的目录，支持分组目录，例如 `skills/imported/research/SKILL.md`。`SKILL.md` 使用 YAML frontmatter：

```markdown
---
name: research
description: Research public sources and produce cited summaries.
metadata: { "openclaw": { "requires": { "env": ["SEARCH_API_KEY"] } } }
---

Use this skill when the user asks for research over public sources.
Read relevant pages, compare evidence, and cite source URLs.
```

外部 `SKILL.md` 不会直接注册新 Go 工具，它会作为模型可读的能力说明加入 `Enabled Skills`。为了控制 prompt 体积，Ternura 只注入 Skill 名称、描述、来源和 `SKILL.md` 路径；模型需要使用该 Skill 时，再通过 `read` 工具读取对应文件。若外部 Skill 和内置 Skill 同名，外部说明优先，内置工具和 Hook 会合并保留。

支持的 OpenClaw metadata gate：

- `metadata.openclaw.platforms`
- `metadata.openclaw.requires.env`
- `metadata.openclaw.requires.bins`
- `metadata.openclaw.requires.anyBins` / `any_bins`
- `metadata.openclaw.alwaysInclude`

当前没有 Ternura 配置系统对应 `requires.config`，因此声明 config 依赖的外部 Skill 会被视为不可用。可以用 `TERNURA_SKILLS` 设置允许列表，用 `TERNURA_SKILLS_DISABLED` 禁用指定 Skill，多个名称用逗号或冒号分隔。

Agent loop 内置 Hook 扩展点。后续模块可以通过 Hook 在运行过程中自动介入，而不需要把逻辑硬编码进 `agent/agent.go`：

- `BeforeRun`：运行开始前初始化预算、上下文或审计状态
- `AfterUserMessage`：用户消息进入后执行检索、分类或预处理
- `BeforeModelCall`：调用模型前注入 runtime context，或按策略禁用部分工具
- `AfterModelResponse`：模型返回后观察内容和工具调用计划
- `BeforeToolCall`：工具执行前做权限检查、人工确认或参数改写
- `AfterToolCall`：工具执行后做审计、截断、脱敏或错误分类
- `FinalizeRun`：最终回复返回前做状态归因校验、结果改写或补充 trace
- `AfterRun`：运行结束后保存产物、更新摘要或触发后台维护任务

Hook 可以通过 `RunContext` 写入临时上下文块；这些内容只会在本轮模型请求时作为 runtime context 注入，不会污染持久化对话历史。为了兼容 MiniMax 等 OpenAI-compatible Provider，runtime context 会合并进主 system prompt，而不是作为第二条 system message 发送。

`RunContext` 会记录本轮已经执行过的工具结果，Hook 可以据此判断最终回复是否有真实工具产物支撑。当前 `state_guard` 就利用这个工具结果账本和 `.ternura/cron/jobs.json`，拦截“模型说已设置，但后端没有任务”的情况。

运行结果会被结构化为：

- `trace`：本轮的 `<think>` 内容和工具调用记录
- `content`：去掉 `<think>` 后的最终回复
- `raw_content`：模型最后一次返回的原始内容

运行结果会写入 `.ternura/sessions/<session-id>/runs/`，外部 channel 和后台任务可以基于这些结构化文件恢复或追踪历史 run。

记忆模块通过 Hook 接入 Agent loop：

- `BeforeModelCall`：调用模型提取 Active Memory keywords/search query，召回相关长期记忆、短期最近轮次和工具摘要，并注入 `Active Memory` runtime context
- `AfterRun`：成功完成一轮回复后，把用户输入和最终回复追加到当前 session 的短期记忆
- `remember`：模型在用户表达稳定偏好、长期事实或常驻指令时显式写入长期记忆
- `forget_memory`：模型在用户要求遗忘或发现记忆过期时按 memory id 删除长期记忆

定时任务模块通过工具、API 和后台 runner 接入 Agent loop：

- `cron`：模型在用户明确要求稍后提醒、检查、继续、循环或取消任务时，通过 `add`、`list`、`remove` 管理定时任务
- 后台 runner：服务启动后按 `.ternura/cron/jobs.json` 的下一次触发时间唤醒，到点后用任务绑定的 session 恢复对话上下文，执行 Agent，并把结果保存为该 session 的新 run

飞书模块参考 NanoBot 的 channel adapter 边界接入 Agent loop：

- `internal/feishu`：处理飞书长连接事件、URL verification、事件 token 校验、消息去重、文本/富文本解析、群聊 @ 策略和 OpenAPI 发消息
- `internal/app/feishu_channel.go`：把飞书消息映射为稳定 session id，运行独立 Agent，并为飞书来源的 `cron` job 附加回传地址
- 飞书长连接 handler 和 HTTP handler 都会把 Agent 运行放进后台 goroutine，避免阻塞飞书事件链路

整体流程：

```text
用户输入 -> SkillRegistry -> runtime context -> 模型 -> 工具调用 -> 本地工具 -> 工具结果 -> 模型 -> grounding guard -> 最终回复
```

## 内置工具

工具实现位于 `tool/` 目录，并遵循统一接口：

```go
type Tool interface {
    einotool.InvokableTool
    ToolName() AgentTool
}
```

当前内置工具：

- `read`：读取本地文件
- `write`：写入本地文件
- `edit`：替换本地文件中的指定文本
- `bash`：执行 shell 命令并返回输出
- `update_todos`：替换当前 session 的完整任务列表，支持 `pending`、`in_progress`、`done`、`blocked` 和 `cancelled` 状态
- `remember`：写入长期记忆，支持 `preference`、`profile`、`project`、`instruction`、`fact` 和 `other` 分类
- `forget_memory`：按 memory id 删除长期记忆
- `cron`：创建、列出和删除定时任务；相对时间使用 `delay_seconds`，绝对时间使用 ISO datetime `at`，循环任务使用 `every_seconds` 或 `cron_expr`
- `web_fetch`：通过本机网络读取指定 HTTP/HTTPS URL，返回状态、最终 URL、内容类型和裁剪后的可读文本

## Provider 选择

`config.NewModelConfig()` 会根据 `LLM_PROVIDER` 选择模型配置。

- `LLM_PROVIDER=minimax`：读取 `MINIMAX_BASE_URL`、`MINIMAX_API_KEY` 和 `MINIMAX_MODEL`
- `LLM_PROVIDER=openai`：读取 `OPENAI_BASE_URL`、`OPENAI_API_KEY` 和 `OPENAI_MODEL`
- 如果没有设置 `LLM_PROVIDER`，默认使用 `openai`

## 安全说明

Ternura 可以修改文件，也可以执行 shell 命令。请把它视为一个拥有真实本地系统访问能力的自动化工具。

如果要用于更严肃的场景，建议先增加安全边界：

- 限制可读取和可编辑的目录
- 限制或移除 shell 命令执行能力
- 对破坏性操作加入人工确认
- 在沙盒或一次性工作区中运行 Agent
- 记录每一次工具调用及其参数

## Agent Harness 待办

Ternura 当前已经具备基本的 Agent loop、工具调用、trace 记录、飞书接入和后台 cron runner。要把它推进到更可靠的 Agent Harness，还需要补充这些能力：

- [x] 运行生命周期：为每次请求生成 `run_id`，统一记录开始、结束、取消、失败和耗时，前后端都围绕同一个 run 追踪状态
- [x] 可恢复会话：把对话历史、工具调用结果和最终输出持久化，支持服务重启后恢复同一 session
- [x] 计划与步骤状态：提供 `update_todos` 工具，把当前 session 的任务步骤持久化
- [x] Skill 模块：提供 `SkillRegistry`，把工具、Hook 和运行时说明按能力模块统一装配
- [x] Hook 扩展模块：提供 `RunContext` 和 run/model/tool 生命周期 Hook，支持后续记忆、权限、审计和预算模块接入
- [x] 运行预算与工具熔断：限制 ReAct 步数、模型调用数、工具调用数和 `web_fetch` 次数；网页抓取遇到搜索页、验证码、反爬或非 2xx 响应时快速返回不可用证据
- [x] 状态归因保护：在最终回复返回前校验真实工具结果和持久化状态，防止模型编造 schedule id 或虚假宣称副作用成功
- [x] 工具证据保护：最终回复会拦截伪工具调用文本，以及缺少本轮工具证据的命令执行、安装、文件修改和记忆变更等副作用声明
- [x] 定时任务：提供 `cron` 工具和后台 runner，支持 one-shot、interval 和 cron-like schedule，到点后恢复 session 上下文并执行 Agent
- [x] 定时任务指令遵循：通过当前时间 runtime context、定时任务 Skill 说明、工具 schema 和状态归因校验，引导模型真实调用 `cron`
- [x] 飞书 channel：提供 Feishu/Lark 长连接和 HTTP 事件入口、消息解析、独立 session、Agent 回复发送和 cron 触发回传
- [ ] 定时任务确认机制：对高风险 prompt 或长期运行任务增加确认、暂停、恢复和手动立即执行能力
- [ ] 飞书加密事件：支持飞书事件回调 encrypt_key 解密和签名校验
- [x] 基础上下文管理：按优先级注入 runtime context，限制 Active Memory 和历史消息进入模型前的字符预算
- [ ] 深度上下文管理：增加 token 预算统计、模型摘要、长对话压缩和更精细的上下文裁剪策略
- [x] 长期/短期记忆：通过 Hook 注入记忆上下文，短期记忆按 session 自动滚动，长期记忆通过 `remember` / `forget_memory` 显式维护
- [x] AI 关键词召回：模型调用前先提取 Active Memory keywords/search query，再用 query 召回相关长期记忆、短期轮次和工具摘要
- [ ] 记忆语义检索：为长期记忆增加 embedding 检索，只把与当前任务相关的记忆注入模型上下文
- [ ] 记忆摘要策略：把短期记忆从规则拼接升级为模型摘要，并按 token 预算保留关键事实、目标和未完成事项
- [ ] 记忆写入确认：对敏感或高影响长期记忆增加确认、编辑和拒绝流程，避免模型误写
- [ ] 记忆隔离策略：支持按 workspace、项目或用户 profile 隔离长期记忆，避免不同项目互相污染上下文
- [ ] 记忆管理面板：支持手动新增、编辑、搜索、批量删除、导出和重置长期记忆
- [ ] 记忆质量维护：记录命中次数、最近使用时间、来源 run id 和置信度，定期清理过期、重复或低价值记忆
- [ ] 工具权限模型：为 `read`、`write`、`edit`、`bash` 增加目录白名单、危险命令拦截、超时、输出大小限制和错误分类
- [ ] 人工确认机制：对写文件、执行 shell、删除或覆盖内容等高风险动作提供确认、拒绝和重试流程
- [ ] 工具调用审计：记录每次工具调用的参数、工作目录、耗时、退出码、截断状态和原始输出，支持导出完整 trace
- [ ] 任务预算控制：支持最大工具轮数、最大运行时长、最大输出 token、最大连续失败次数，避免 Agent 无限制循环
- [ ] 外部端事件协议增强：为飞书、cron 和后续 channel 增加事件序号、状态回放和错误事件规范
- [ ] 结构化事件总线：统一 `think`、final、tool_call、tool_result、error、status 等事件类型，减少外部端对字符串约定的依赖
- [ ] 观测与调试：增加结构化日志、trace 文件、模型请求参数记录、耗时统计和可开关的 debug 面板
- [ ] 评测 Harness：建立一组固定任务、mock 工具和 golden 输出，用于回归测试 Agent loop、工具调用、外部端回传和 Markdown 渲染
- [ ] 安全脱敏：对 API Key、环境变量、路径中的敏感片段和工具输出中的 secret 做日志与 UI 脱敏
- [ ] 配置状态查看：只读展示模型、Provider、工具开关和安全策略
- [ ] 多工作区支持：允许用户选择项目目录，并把工具访问范围绑定到当前工作区
- [ ] 文件变更预览：Agent 写入或编辑文件前后展示 diff，支持用户确认后应用或回滚
- [ ] 运行产物管理：保存每次 run 的 trace、最终回答、文件 diff 和错误信息，形成可回看的任务历史

## 开发

运行测试：

```bash
GOCACHE=$PWD/.gocache go test ./...
```

格式化 Go 文件：

```bash
gofmt -w $(rg --files -g '*.go')
```

## 参考

- [CloudWeGo Eino](https://www.cloudwego.io/docs/eino/overview/)
- [MiniMax OpenAI-compatible API](https://platform.minimax.io/docs/api-reference/text-openai-api)

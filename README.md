# Ternura

Ternura 是一个完整、独立的轻量级通用 Agent 项目。它使用 Go 实现，通过 Eino 的 OpenAI 兼容 ChatModel 连接大模型，并为模型提供一组受控的本地工具，让模型可以围绕对话、规划、写作、分析、代码和本地自动化任务开展工作。

项目支持两种运行方式：

- CLI 模式：适合一次性任务和命令行调试
- Web Console 模式：适合在浏览器中只读查看外部端对话、运行历史、记忆、计划和定时任务
- Feishu Channel 模式：Web 服务启动后可通过长连接或 HTTP 回调接收飞书消息，把飞书接入同一个 Agent Harness

## 功能

- 基于 Eino ChatModel 的 OpenAI 兼容模型客户端
- 支持 MiniMax 中国区配置
- System Prompt 定位为通用工具型 Agent，不再限制为 coding assistant
- 支持连续对话上下文
- 支持工具调用和工具结果回传
- 提供 Hook 扩展模块，可在 run、模型调用和工具调用生命周期中注入上下文、禁用工具、拦截工具和记录结果
- 内置运行时上下文 Hook：每轮注入当前时间和时区，并在识别到提醒或取消提醒意图时注入调度工具使用规范
- 内置状态归因 Guard：对定时任务这类真实副作用做最终回复校验，防止模型编造 schedule id 或声称未落盘的操作已成功
- 内置 `read`、`write`、`edit`、`bash`、`update_todos`、`remember`、`forget_memory` 和 `cron` 工具
- 支持短期记忆和长期记忆：短期记忆按 session 自动滚动更新，长期记忆通过工具显式写入和删除，并在模型调用前注入 runtime context
- 提供命令行入口
- 提供本地只读 Agent Observatory，作为外部 channel 的信息查询页面
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
MINIMAX_MODEL=MiniMax-M2.7
```

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
```

默认 `FEISHU_EVENT_MODE=websocket`，对应飞书开放平台里的“长连接”订阅方式。启用长连接后不需要公网回调地址，只需要在飞书后台开启事件订阅并订阅 `im.message.receive_v1`，服务启动时会用 App ID / Secret 建立 WebSocket 长连接。

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

## 运行 Web Console

启动本地服务：

```bash
GOCACHE=$PWD/.gocache go run ./cmd/ternura -serve
```

然后打开：

```text
http://localhost:8080
```

Web Console 包含：

- 左侧状态栏：展示模型、会话、上下文、记忆和事件状态
- Run 状态：完整展示当前查看 session 的最近 `run_id`、运行状态和完成耗时
- Plan 面板：只读展示当前查看 session 的任务计划，外部端运行时可通过 `update_todos` 更新步骤状态
- History 按钮：打开可滚动历史弹窗，用卡片隔离每个历史 session，并以 session 标题、轮次数量、最近输入预览和 session id 展示内容；点击某个 session 会在本地只读恢复该 session 的完整对话
- Schedule 按钮：打开定时任务弹窗，只读查看任务状态、触发时间、运行结果和错误信息
- Memory 按钮：打开记忆弹窗，用独立卡片只读查看长期记忆和当前 session 的短期记忆
- 主对话区域：只读展示外部端写入的用户消息、模型回复和 `<think>` 内容
- 主题切换：右上角按钮一键切换浅色和夜间模式，并记住上次选择
- Monaco 风格的等宽字体，便于阅读推理、工具参数和代码块
- 自动滚动：恢复 session 或外部定时任务写入新结果后会定位到最新内容；用户主动向上滚动时会暂停自动跟随
- 可恢复会话：刷新页面后会自动恢复当前 session；也可以通过顶部 `History` 按钮打开弹窗，只读切换并查看任意历史 session
- Memory 状态：展示长期记忆和当前 session 短期记忆数量，例如 `2 LT · 5 ST`
- 开发服务会对前端静态资源禁用缓存，刷新页面即可加载最新 UI 代码

模型返回会被拆成两块展示：

- `Reasoning & tool use`：默认折叠的可展开区域，使用独立浅色面板展示 `<think>` 内容和每一次工具调用的参数、结果或错误；内容较长时在面板内部滚动
- `Final`：最终回复内容，使用独立答案块展示

用户输入、推理过程、工具调用和最终回复都会按 Markdown 渲染。

Web Console 不再提供聊天输入框，也不再暴露 `/api/chat`、`/api/chat/stream` 或 `/api/reset` 路由；Agent 运行入口来自 CLI、飞书等外部端和后台 cron runner。`/api/history` 会返回已持久化的 session 列表，`/api/session?session_id=...` 用于只读拉取某个 session 的完整内容。

外部端或后台任务写入的 run 会包含运行生命周期字段：

- `run_id`：本轮运行 id
- `status`：`succeeded`、`failed`、`cancelled` 或 `running`
- `started_at` / `finished_at`：开始和结束时间
- `duration_ms`：运行耗时

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

`/api/history` 只返回 session 摘要和最后一轮 run 的轻量预览，不再返回所有 trace；前端在恢复某个 session 时才通过 `/api/session?session_id=...` 拉取该 session 的完整 runs。

`/api/memory/status` 返回当前长期记忆数量和当前 session 的短期记忆轮次数。`GET /api/memory` 返回长期记忆列表和当前 session 短期记忆详情；Web Console 只读展示这些内容，不提供删除入口。模型调用前会通过 Hook 注入一个 `Memory` runtime context block，包含长期记忆、当前 session 短期摘要和最近轮次。长期记忆只应该保存稳定、可复用的用户偏好、项目事实或常驻指令；不要保存 API Key、密码或一次性细节。

`GET /api/schedules` 返回所有定时任务。Web Console 只读轮询定时任务状态；检测到任务完成或失败后会刷新 History，如果任务属于当前查看 session，会自动把新 run 渲染到聊天区并在 Events 里提示。后台 runner 会按下一次触发时间唤醒，到点后按任务绑定的 session 恢复上下文、执行 Agent、写入 run 记录和对话历史。

对于“2 分钟后提醒我吃饭”这类明确相对时间提醒，后端会先走确定性解析并直接创建 `cron` job，不依赖模型自行调用工具；这样可以避免模型只回复“已设置”但实际没有写入 `cron/jobs.json` 的情况。更复杂的时间表达、循环任务和任务列表/删除仍由模型通过 `cron` 工具完成。

模型调用前会通过 `current_time` Hook 注入当前时间和时区，通过 `schedule_guidance` Hook 对明显的提醒、定时、闹钟、取消提醒等意图注入工具调用指导：相对时间优先使用 `delay_seconds`，绝对时间使用 ISO datetime `at`，循环间隔使用 `every_seconds`，cron 表达式使用 `cron_expr` 和可选 `tz`，最终回复只能报告工具真实返回的任务 id。普通的未来时间问题，例如“明天天气怎么样”，不会触发这段调度指导。

Harness 会在最终回复返回前执行状态归因校验：如果模型声称创建了定时任务、返回了 `cron-...` 或旧 `schedule-...` id，但本轮没有成功的 `cron` 工具结果，且该 id 也不存在于真实 cron store 中，最终回复会被替换为保护性说明，并在 `Reasoning & tool use` 中追加 `Harness guard` trace。前端只应把 `/api/schedules` 和工具结果视为真实任务状态，不能把模型文本里的 id 当成事实来源。

飞书消息会进入独立的 `feishu-...` session，不会切换 Web Console 当前 session。私聊按发送者隔离，群聊和话题群默认需要 @ 机器人；`FEISHU_TOPIC_ISOLATION=true` 时群聊里的每个话题/根消息会拥有独立 session。飞书中创建的 `cron` 提醒会保存 Feishu 回传地址，到点触发后除了写入本地 session，也会把最终回复发回原飞书聊天。

`FEISHU_BOT_OPEN_ID` 推荐配置，这样群聊 @ 判断和消息清洗最精确；如果暂时没有配置，Ternura 会优先使用飞书事件里的 `mentioned_type` 判断机器人 mention。

`.ternura/` 已经被 `.gitignore` 忽略，不会被提交到仓库。Web Console 不再创建新 session；新的 session 由外部 channel、CLI 或后台任务写入时自然产生。

## 命令行参数

```bash
go run ./cmd/ternura -q "prompt text"
go run ./cmd/ternura -serve
go run ./cmd/ternura -serve -addr :8081
```

- `-q`：CLI 模式下的用户输入
- `-serve`：启动 Web Console
- `-addr`：Web Console 监听地址，默认是 `:8080`

## 目录结构

```text
.
├── agent/            # Agent 循环、Hook 扩展点、RunContext 和系统提示词
├── cmd/ternura/      # 可执行入口，负责调用 internal/app
├── config/           # 模型和 Provider 配置
├── internal/app/     # CLI/Web 服务装配、session、memory、runtime guidance 和 channel glue
├── internal/cron/    # cron store、schedule 计算和运行历史
├── internal/feishu/  # Feishu/Lark 事件回调、消息解析和 OpenAPI 发送
├── tool/             # 本地工具实现
└── web/              # 静态前端页面
```

## 工作流程

Ternura 会把当前对话、系统提示词和工具定义发送给模型。模型如果返回工具调用，Ternura 会执行对应的本地工具，并把工具结果追加回对话上下文。这个循环会持续到模型返回普通 assistant 消息为止。

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

Web Console 会只读展示每一轮已经写入的运行。页面刷新时，前端会读取 `/api/history` 并恢复当前 session；用户在 History 弹窗里点击某个 session 时，前端只会通过 `/api/session?session_id=...` 拉取并渲染该 session，不会切换后端当前 session，也不会触发新的 Agent run。

记忆模块通过 Hook 接入 Agent loop：

- `BeforeModelCall`：读取长期记忆和当前 session 的短期记忆，并注入 `Memory` runtime context
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
用户输入 -> 模型 -> 工具调用 -> 本地工具 -> 工具结果 -> 模型 -> 最终回复
```

## 内置工具

工具实现位于 `tool/` 目录，并遵循统一接口：

```go
type Tool interface {
    ToolName() AgentTool
    Info() openai.ChatCompletionToolUnionParam
    Execute(ctx context.Context, argumentsInJSON string) (string, error)
}
```

当前内置工具：

- `read`：读取本地文件
- `write`：写入本地文件
- `edit`：替换本地文件中的指定文本
- `bash`：执行 shell 命令并返回输出
- `update_todos`：替换当前 session 的完整任务列表，支持 `pending`、`in_progress`、`done`、`blocked` 和 `cancelled` 状态；Web Console 会把它展示在左侧 Plan 面板
- `remember`：写入长期记忆，支持 `preference`、`profile`、`project`、`instruction`、`fact` 和 `other` 分类
- `forget_memory`：按 memory id 删除长期记忆
- `cron`：创建、列出和删除定时任务；相对时间使用 `delay_seconds`，绝对时间使用 ISO datetime `at`，循环任务使用 `every_seconds` 或 `cron_expr`

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

Ternura 当前已经具备基本的 Agent loop、工具调用、trace 展示和 Web Console。要把它推进到更可靠的 Agent Harness，还需要补充这些能力：

- [x] 运行生命周期：为每次请求生成 `run_id`，统一记录开始、结束、取消、失败和耗时，前后端都围绕同一个 run 追踪状态
- [x] 可恢复会话：把对话历史、工具调用结果和最终输出持久化，支持刷新页面后继续查看或恢复同一轮任务
- [x] 计划与步骤状态：提供 `update_todos` 工具，把当前 session 的任务步骤持久化并在 Web Console 的 Plan 面板展示
- [x] Hook 扩展模块：提供 `RunContext` 和 run/model/tool 生命周期 Hook，支持后续记忆、权限、审计和预算模块接入
- [x] 状态归因保护：在最终回复返回前校验真实工具结果和持久化状态，防止模型编造 schedule id 或虚假宣称副作用成功
- [x] 定时任务：提供 `cron` 工具、Schedule 弹窗和后台 runner，支持 one-shot、interval 和 cron-like schedule，到点后恢复 session 上下文并执行 Agent
- [x] 定时任务指令遵循：通过当前时间 runtime context、定时意图识别和工具 schema 约束，引导模型真实调用 `cron`
- [x] 飞书 channel：提供 Feishu/Lark 长连接和 HTTP 事件入口、消息解析、独立 session、Agent 回复发送和 cron 触发回传
- [ ] 定时任务确认机制：对高风险 prompt 或长期运行任务增加前端确认、暂停、恢复和手动立即执行
- [ ] 飞书加密事件：支持飞书事件回调 encrypt_key 解密和签名校验
- [ ] 上下文管理：增加 token 预算统计、历史压缩、长对话摘要和可控的上下文裁剪策略
- [x] 长期/短期记忆：通过 Hook 注入记忆上下文，短期记忆按 session 自动滚动，长期记忆通过 `remember` / `forget_memory` 显式维护
- [ ] 记忆语义检索：为长期记忆增加 embedding 或轻量检索，只把与当前任务相关的记忆注入模型上下文
- [ ] 记忆摘要策略：把短期记忆从规则拼接升级为模型摘要，并按 token 预算保留关键事实、目标和未完成事项
- [ ] 记忆写入确认：对敏感或高影响长期记忆增加前端确认、编辑和拒绝流程，避免模型误写
- [ ] 记忆隔离策略：支持按 workspace、项目或用户 profile 隔离长期记忆，避免不同项目互相污染上下文
- [ ] 记忆管理面板：支持手动新增、编辑、搜索、批量删除、导出和重置长期记忆
- [ ] 记忆质量维护：记录命中次数、最近使用时间、来源 run id 和置信度，定期清理过期、重复或低价值记忆
- [ ] 工具权限模型：为 `read`、`write`、`edit`、`bash` 增加目录白名单、危险命令拦截、超时、输出大小限制和错误分类
- [ ] 人工确认机制：对写文件、执行 shell、删除或覆盖内容等高风险动作提供前端确认、拒绝和重试流程
- [ ] 工具调用审计：记录每次工具调用的参数、工作目录、耗时、退出码、截断状态和原始输出，支持导出完整 trace
- [ ] 任务预算控制：支持最大工具轮数、最大运行时长、最大输出 token、最大连续失败次数，避免 Agent 无限制循环
- [ ] 外部端事件协议增强：为飞书、cron 和后续 channel 增加事件序号、状态回放和错误事件规范
- [ ] 结构化事件总线：统一 `think`、final、tool_call、tool_result、error、status 等事件类型，减少前端对字符串约定的依赖
- [ ] 观测与调试：增加结构化日志、trace 文件、模型请求参数记录、耗时统计和可开关的 debug 面板
- [ ] 评测 Harness：建立一组固定任务、mock 工具和 golden 输出，用于回归测试 Agent loop、工具调用、外部端回传和 Markdown 渲染
- [ ] 安全脱敏：对 API Key、环境变量、路径中的敏感片段和工具输出中的 secret 做日志与 UI 脱敏
- [ ] 配置状态面板：在 Web Console 中只读展示模型、Provider、工具开关和安全策略
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

- [OpenAI Go SDK](https://github.com/openai/openai-go)
- [CloudWeGo Eino](https://www.cloudwego.io/docs/eino/overview/)
- [MiniMax OpenAI-compatible API](https://platform.minimax.io/docs/api-reference/text-openai-api)

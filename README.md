# Ternura

Ternura 是一个完整、独立的轻量级通用 Agent 项目。它使用 Go 实现，通过 OpenAI 兼容的 Chat Completions 接口连接大模型，并为模型提供一组受控的本地工具，让模型可以围绕对话、规划、写作、分析、代码和本地自动化任务开展工作。

项目支持两种运行方式：

- CLI 模式：适合一次性任务和命令行调试
- Web Console 模式：适合在浏览器中进行交互式对话

## 功能

- 基于 `openai-go` 的 OpenAI 兼容模型客户端
- 支持 MiniMax 中国区配置
- System Prompt 定位为通用工具型 Agent，不再限制为 coding assistant
- 支持连续对话上下文
- 支持工具调用和工具结果回传
- 提供 Hook 扩展模块，可在 run、模型调用和工具调用生命周期中注入上下文、禁用工具、拦截工具和记录结果
- 内置 `read`、`write`、`edit`、`bash` 和 `update_todos` 工具
- 提供命令行入口
- 提供本地前端 Agent Console
- Web Console 支持流式输出
- 后端内置流式缓冲区，让模型回复按稳定节奏渐进式输出
- 每次请求都会生成 `run_id`，记录运行开始、结束、失败、取消和耗时
- 支持把对话历史、工具调用 trace 和最终回复持久化为可恢复会话
- 支持创建新 session，并保留可恢复的历史 session

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

## 运行 CLI

在项目根目录执行：

```bash
GOCACHE=$PWD/.gocache go run ./main -q "请读取 README.md 并总结项目目标"
```

也可以让 Agent 使用本地工具完成任务：

```bash
GOCACHE=$PWD/.gocache go run ./main -q "在当前目录下创建一个 TODO.md，内容为 1. 研究 agent"
```

## 运行 Web Console

启动本地服务：

```bash
GOCACHE=$PWD/.gocache go run ./main -serve
```

然后打开：

```text
http://localhost:8080
```

Web Console 包含：

- 左侧状态栏：展示模型、会话、上下文、记忆和事件状态
- Run 状态：完整展示当前请求的 `run_id`、运行状态和完成耗时
- Plan 面板：展示当前 session 的任务计划，模型可通过 `update_todos` 更新步骤状态
- History 按钮：打开可滚动历史弹窗，用卡片隔离每个历史 session，并以 session 标题、轮次数量、最近输入预览和 session id 展示内容；点击某个 session 会恢复该 session 的完整对话
- 主对话区域：展示用户消息、模型回复和 `<think>` 内容
- 底部输入区：固定在控制台底部，用更紧凑的输入框发送新的任务或问题
- New session：开启一个新的空 session，保留已有历史 session
- 主题切换：右上角按钮一键切换浅色和夜间模式，并记住上次选择
- Monaco 风格的等宽字体，便于阅读推理、工具参数和代码块
- 流式输出：模型 token 会逐步显示，避免一次性弹出完整结果
- 渐进式节奏：后端会把模型正文和 `<think>` 内容拆成小块平滑发送，工具调用结果保持即时展示
- 流式输出会保持 UTF-8 字符边界，避免中文在分片渲染时出现乱码
- 自动滚动：发送消息后会定位到最新用户消息，流式回复时会继续跟随底部；用户主动向上滚动时会暂停自动跟随
- 固定输入区：页面本身不滚动，只有主对话区域负责滚动
- 可恢复会话：刷新页面后会自动恢复当前 session；也可以通过顶部 `History` 按钮打开弹窗，切换并恢复任意历史 session
- 开发服务会对前端静态资源禁用缓存，刷新页面即可加载最新 UI 代码

输入区支持键盘操作：

- `Send` 按钮：发送当前输入
- `Enter` / `Shift+Enter`：在输入框中换行，不触发发送
- `Esc`：请求进行中时取消请求，空闲时清空输入框
- 发送不再绑定输入框回车键，避免中文输入法候选词确认时误触发送

模型返回会被拆成两块展示：

- `Reasoning & tool use`：默认折叠的可展开区域，使用独立浅色面板展示 `<think>` 内容和每一次工具调用的参数、结果或错误；内容较长时在面板内部滚动
- `Final`：最终回复内容，使用独立答案块展示

用户输入、推理过程、工具调用和最终回复都会按 Markdown 渲染。

前端默认使用 `/api/chat/stream` 接收 `text/event-stream`。旧的 `/api/chat` JSON 接口仍然保留，便于调试或接入非流式客户端。`/api/history` 会返回已持久化的 session 列表，`/api/session/select` 用于切换当前 session。

流式响应会包含运行生命周期事件：

- `run_start`：本轮运行已开始，包含 `run_id`、`status=running` 和 `started_at`
- `run_done`：本轮运行成功结束，包含 `finished_at` 和 `duration_ms`
- `run_failed`：本轮运行失败，包含错误信息和耗时
- `run_cancelled`：本轮运行被取消，包含取消时的耗时

同一轮运行中的 `start`、`content_delta`、`trace_*`、`done` 和 `error` 事件也会携带同一个 `run_id`。

会话恢复数据保存在项目根目录的 `.ternura/`，并拆成多个文件：

```text
.ternura/
├── index.json
└── sessions/
    └── session-xxx/
        ├── meta.json
        ├── messages.json
        ├── todos.json
        └── runs/
            ├── run-xxx.json
            └── run-yyy.json
```

- `index.json`：当前 session id 和所有 session 的轻量摘要，包括标题、创建/更新时间、run 数量、message 数量和 todo 数量
- `meta.json`：单个 session 的元信息和 run 顺序
- `messages.json`：用于服务重启后恢复模型上下文的 user/assistant 对话历史
- `todos.json`：当前 session 的任务计划，由 `update_todos` 工具维护
- `runs/*.json`：每轮请求的详细记录，包括 `run_id`、状态、用户输入、最终回复、原始回复、trace、错误信息和耗时

旧版 `.ternura/session.json` 会在启动时自动迁移到拆分结构，并移动为 `.ternura/session.legacy.json` 作为备份；后续写入只更新拆分后的文件。

`/api/history` 只返回 session 摘要和最后一轮 run 的轻量预览，不再返回所有 trace；前端在恢复某个 session 时才通过 `/api/session?session_id=...` 拉取该 session 的完整 runs。

`.ternura/` 已经被 `.gitignore` 忽略，不会被提交到仓库。点击 `New session` 会创建新的空 session，不会删除已有历史 session。

可以通过环境变量调整流式输出节奏：

```env
TERNURA_STREAM_CHUNK_RUNES=3
TERNURA_STREAM_INTERVAL_MS=35
```

- `TERNURA_STREAM_CHUNK_RUNES`：每次发送的字符数量，按 rune 计算，默认 `3`
- `TERNURA_STREAM_INTERVAL_MS`：相邻分片之间的等待时间，默认 `35` 毫秒；设置为 `0` 可以关闭平滑延迟

## 命令行参数

```bash
go run ./main -q "prompt text"
go run ./main -serve
go run ./main -serve -addr :8081
```

- `-q`：CLI 模式下的用户输入
- `-serve`：启动 Web Console
- `-addr`：Web Console 监听地址，默认是 `:8080`

## 目录结构

```text
.
├── agent.go          # Agent 循环和工具调用执行逻辑
├── hook.go           # Agent Harness Hook 扩展点和 RunContext
├── main/main.go      # CLI 和 Web 服务入口
├── main/session_store.go # 本地可恢复会话持久化
├── main/todo_tool.go # update_todos 和 session 持久化的适配
├── prompt.go         # 系统提示词
├── shared/config.go  # 模型和 Provider 配置
├── tool/             # 本地工具实现
└── web/              # 静态前端页面
```

## 工作流程

Ternura 会把当前对话、系统提示词和工具定义发送给模型。模型如果返回工具调用，Ternura 会执行对应的本地工具，并把工具结果追加回对话上下文。这个循环会持续到模型返回普通 assistant 消息为止。

Agent loop 内置 Hook 扩展点。后续模块可以通过 Hook 在运行过程中自动介入，而不需要把逻辑硬编码进 `agent.go`：

- `BeforeRun`：运行开始前初始化预算、上下文或审计状态
- `AfterUserMessage`：用户消息进入后执行检索、分类或预处理
- `BeforeModelCall`：调用模型前注入 runtime context，或按策略禁用部分工具
- `AfterModelResponse`：模型返回后观察内容和工具调用计划
- `BeforeToolCall`：工具执行前做权限检查、人工确认或参数改写
- `AfterToolCall`：工具执行后做审计、截断、脱敏或错误分类
- `AfterRun`：运行结束后保存产物、更新摘要或触发后台维护任务

Hook 可以通过 `RunContext` 写入临时上下文块；这些内容只会在本轮模型请求时作为 runtime context 注入，不会污染持久化对话历史。

运行结果会被结构化为：

- `trace`：本轮的 `<think>` 内容和工具调用记录
- `content`：去掉 `<think>` 后的最终回复
- `raw_content`：模型最后一次返回的原始内容

Web Console 会把每一轮运行写入当前 session。页面刷新时，前端会读取 `/api/history` 并恢复当前 session；用户在 History 弹窗里点击某个 session 时，前端会调用 `/api/session/select`，后端会切换当前 session 并用该 session 的 `messages` 恢复模型上下文。

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

## Provider 选择

`shared.NewModelConfig()` 会根据 `LLM_PROVIDER` 选择模型配置。

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
- [ ] 上下文管理：增加 token 预算统计、历史压缩、长对话摘要和可控的上下文裁剪策略
- [ ] 长期记忆：区分短期对话上下文和长期用户/项目记忆，提供显式写入、读取、删除和重置入口
- [ ] 工具权限模型：为 `read`、`write`、`edit`、`bash` 增加目录白名单、危险命令拦截、超时、输出大小限制和错误分类
- [ ] 人工确认机制：对写文件、执行 shell、删除或覆盖内容等高风险动作提供前端确认、拒绝和重试流程
- [ ] 工具调用审计：记录每次工具调用的参数、工作目录、耗时、退出码、截断状态和原始输出，支持导出完整 trace
- [ ] 任务预算控制：支持最大工具轮数、最大运行时长、最大输出 token、最大连续失败次数，避免 Agent 无限制循环
- [ ] 流式协议增强：为 SSE 增加 heartbeat、事件序号、客户端断线处理和错误事件规范，后续支持重连与回放
- [ ] 结构化事件总线：统一 `think`、final、tool_call、tool_result、error、status 等事件类型，减少前端对字符串约定的依赖
- [ ] 观测与调试：增加结构化日志、trace 文件、模型请求参数记录、耗时统计和可开关的 debug 面板
- [ ] 评测 Harness：建立一组固定任务、mock 工具和 golden 输出，用于回归测试 Agent loop、工具调用、流式输出和 Markdown 渲染
- [ ] 安全脱敏：对 API Key、环境变量、路径中的敏感片段和工具输出中的 secret 做日志与 UI 脱敏
- [ ] 配置面板：在 Web Console 中展示并允许切换模型、Provider、流式节奏、工具开关和安全策略
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
gofmt -w agent.go hook.go main/main.go main/session_store.go main/todo_tool.go shared/config.go tool/*.go prompt.go
```

## 参考

- [OpenAI Go SDK](https://github.com/openai/openai-go)
- [MiniMax OpenAI-compatible API](https://platform.minimax.io/docs/api-reference/text-openai-api)

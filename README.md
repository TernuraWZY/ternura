# Ternura

Ternura 是一个完整、独立的轻量级编码 Agent 项目。它使用 Go 实现，通过 OpenAI 兼容的 Chat Completions 接口连接大模型，并为模型提供一组受控的本地工具，让模型可以读取文件、编辑文件、写入文件和执行 shell 命令。

项目支持两种运行方式：

- CLI 模式：适合一次性任务和命令行调试
- Web Console 模式：适合在浏览器中进行交互式对话

## 功能

- 基于 `openai-go` 的 OpenAI 兼容模型客户端
- 支持 MiniMax 中国区配置
- 支持连续对话上下文
- 支持工具调用和工具结果回传
- 内置 `read`、`write`、`edit`、`bash` 四个本地工具
- 提供命令行入口
- 提供本地前端 Agent Console
- Web Console 支持流式输出
- 后端内置流式缓冲区，让模型回复按稳定节奏渐进式输出
- 支持重置内存中的会话状态

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
- 主对话区域：展示用户消息、模型回复和 `<think>` 内容
- 底部输入区：发送新的任务或问题
- Reset chat：重置当前内存会话
- Monaco 风格的等宽字体，便于阅读推理、工具参数和代码块
- 流式输出：模型 token 会逐步显示，避免一次性弹出完整结果
- 渐进式节奏：后端会把模型正文和 `<think>` 内容拆成小块平滑发送，工具调用结果保持即时展示
- 流式输出会保持 UTF-8 字符边界，避免中文在分片渲染时出现乱码

输入区支持键盘操作：

- `Enter`：发送当前输入
- `Shift+Enter`：换行
- `Esc`：请求进行中时取消请求，空闲时清空输入框

模型返回会被拆成两块展示：

- `Reasoning & tool use`：可折叠区域，使用独立浅色面板展示 `<think>` 内容和每一次工具调用的参数、结果或错误；内容较长时在面板内部滚动
- `Final`：最终回复内容，使用独立答案块展示

用户输入、推理过程、工具调用和最终回复都会按 Markdown 渲染。

前端默认使用 `/api/chat/stream` 接收 `text/event-stream`。旧的 `/api/chat` JSON 接口仍然保留，便于调试或接入非流式客户端。

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
├── main/main.go      # CLI 和 Web 服务入口
├── prompt.go         # 系统提示词
├── shared/config.go  # 模型和 Provider 配置
├── tool/             # 本地工具实现
└── web/              # 静态前端页面
```

## 工作流程

Ternura 会把当前对话、系统提示词和工具定义发送给模型。模型如果返回工具调用，Ternura 会执行对应的本地工具，并把工具结果追加回对话上下文。这个循环会持续到模型返回普通 assistant 消息为止。

运行结果会被结构化为：

- `trace`：本轮的 `<think>` 内容和工具调用记录
- `content`：去掉 `<think>` 后的最终回复
- `raw_content`：模型最后一次返回的原始内容

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

## 开发

运行测试：

```bash
GOCACHE=$PWD/.gocache go test ./...
```

格式化 Go 文件：

```bash
gofmt -w agent.go main/main.go shared/config.go tool/*.go prompt.go
```

## 参考

- [OpenAI Go SDK](https://github.com/openai/openai-go)
- [MiniMax OpenAI-compatible API](https://platform.minimax.io/docs/api-reference/text-openai-api)

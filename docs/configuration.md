# 配置说明

Ternura 从项目根目录的 `.env` 和进程环境变量读取配置。复制示例文件后再填写真实值：

```bash
cp .env.example .env
```

`.env` 已被 `.gitignore` 忽略。不要把 API Key、飞书 App Secret 或访问 Token 提交到仓库。

## 模型 Provider

### MiniMax 中国区

```env
LLM_PROVIDER=minimax
MINIMAX_BASE_URL=https://api.minimaxi.com/v1
MINIMAX_API_KEY=sk-your-minimax-key-here
MINIMAX_MODEL=MiniMax-M3
```

默认 MiniMax 模型为 `MiniMax-M3`，本地配置按 M3 的 1M context 能力设置 context window。

### OpenAI 兼容服务

```env
LLM_PROVIDER=openai
OPENAI_BASE_URL=https://api.openai.com/v1
OPENAI_API_KEY=sk-your-openai-key-here
OPENAI_MODEL=gpt-5.2
```

`config.NewModelConfig()` 根据 `LLM_PROVIDER` 选择配置。未设置时默认使用 `openai`。

## 飞书

长连接是默认接入方式，不需要公网回调地址：

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

飞书开放平台需要开启事件订阅并订阅 `im.message.receive_v1`。服务会使用 App ID 和 App Secret 建立 WebSocket 长连接。

`FEISHU_PROCESSING_REACTION=true` 时，如果 Agent 在 `FEISHU_PROCESSING_DELAY` 内没有生成最终回复，会先在原消息上添加 `FEISHU_PROCESSING_REACTION_TYPE` 表情。

如果改用 HTTP 回调：

```env
FEISHU_EVENT_MODE=http
```

事件地址为：

```text
https://your-public-domain/api/feishu/events
```

本地开发需要通过 ngrok、frp 或 Cloudflare Tunnel 暴露 `http://localhost:8080/api/feishu/events`。当前 HTTP 模式支持未加密事件；长连接模式由飞书 SDK 负责建连和重连。

`FEISHU_BOT_OPEN_ID` 建议配置，这样群聊 mention 判断和消息清洗更精确。未配置时会优先使用事件中的 `mentioned_type`。

## 运行预算

```env
TERNURA_AGENT_TURN_TIMEOUT=4m
FEISHU_REPLY_TIMEOUT=15s
TERNURA_MAX_REACT_STEPS=24
TERNURA_MAX_MODEL_CALLS=16
TERNURA_MAX_TOOL_CALLS=12
TERNURA_MAX_WEB_SEARCH_CALLS=3
TERNURA_MAX_WEB_FETCH_CALLS=5
TERNURA_WEB_SEARCH_TIMEOUT=8s
TERNURA_WEB_FETCH_TIMEOUT=8s
TERNURA_WEB_FETCH_MAX_CHARS=5000
TERNURA_PARALLEL_TOOL_CALLS=true
TERNURA_MODEL_MAX_RETRIES=2
TERNURA_DYNAMIC_TOOL_SEARCH=auto
TERNURA_DYNAMIC_TOOL_SEARCH_THRESHOLD=16
TERNURA_TOOL_APPROVAL_MODE=dangerous
```

这些限制防止单轮交互无限调用模型或在搜索与抓取之间反复循环。飞书回复使用独立的 `FEISHU_REPLY_TIMEOUT`，即使 Agent 达到总超时，也会尽量发送可见的失败说明。

主模型瞬时失败会按 `TERNURA_MODEL_MAX_RETRIES` 重试。配置 `TERNURA_FALLBACK_MODEL` 后会启用 Eino ADK 模型故障转移；未填写 `TERNURA_FALLBACK_BASE_URL` 和 `TERNURA_FALLBACK_API_KEY` 时沿用主模型连接。

动态工具搜索支持：

- `TERNURA_DYNAMIC_TOOL_SEARCH=auto`：工具数达到阈值后启用。
- `TERNURA_DYNAMIC_TOOL_SEARCH=enabled`：始终启用。
- `TERNURA_DYNAMIC_TOOL_SEARCH=disabled`：始终关闭。
- `TERNURA_DYNAMIC_TOOL_SEARCH_THRESHOLD`：`auto` 模式的工具数量阈值，默认 16。

## 工具审批

`TERNURA_TOOL_APPROVAL_MODE` 支持：

| 值 | 行为 |
| --- | --- |
| `none` | 不暂停工具调用 |
| `dangerous` | 只暂停破坏性 shell、工作区外文件修改和需审批的 MCP 工具 |
| `side_effects` | 暂停有外部副作用的工具 |
| `all` | 所有工具调用都需要审批 |

默认值是 `dangerous`。审批会使用 Eino checkpoint/interrupt 暂停运行；可以在飞书回复 `approve run-...` / `reject run-... 原因`，也可以在管理页或 Task API 中处理。恢复时不会重新执行已经完成的步骤。

## MCP

`TERNURA_MCP_CONFIG` 指定 MCP 配置文件，默认读取 `.ternura/mcp.json`。仓库的 `mcp.example.json` 展示了 `stdio` 和 Streamable HTTP 两种配置，`${ENV_NAME}` 会从环境变量展开。

MCP 工具以 `mcp_<server>_<tool>` 暴露给模型，避免不同服务的工具重名。`require_approval` 默认是 `true`；明确只读的服务可以设置为 `false`。单个 MCP 服务连接失败只会记录错误，不会阻止主 Agent 启动。

## API 认证

未设置 `TERNURA_API_TOKEN` 时，管理 API 和 Task API 只接受 loopback 请求。设置后，请求必须携带：

```http
Authorization: Bearer <token>
```

管理页会在浏览器会话中临时保存输入的 Token，不会把它写入项目文件。

## 安全建议

Ternura 可以读取和修改文件，也可以执行 shell 命令。用于长期运行或多人环境时，建议：

- 把工具访问范围限制在明确的工作区。
- 保持危险工具审批开启。
- 使用独立的低权限系统账号运行守护进程。
- 为外部访问设置 `TERNURA_API_TOKEN` 和网络边界。
- 定期检查 run trace、工具参数和持久化产物。

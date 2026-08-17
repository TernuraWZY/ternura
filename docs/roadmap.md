# 路线图

这份文档记录 Ternura 的能力状态。已完成能力只保留摘要，未完成项按主题分组，避免在根 README 中维护一条不断膨胀的混合清单。

## 已完成

### Agent runtime

- Eino ADK `ChatModelAgent + Runner` ReAct 编排。
- Eino ADK `TurnLoop` 同 Session 运行中纠偏和安全点抢占。
- Eino 原生 Tool、并行工具调用和动态 `tool_search`。
- 模型重试与可选 fallback model。
- MCP `stdio` / Streamable HTTP 工具接入。
- 多模态 Eino `schema.Message` 入口。

### 运行生命周期

- 每次请求生成统一 `run_id`。
- PI-style AgentSession 统一 Feishu、Task API 和 cron 的 start/run/finish。
- 可恢复 session、运行历史、trace、metrics 和 Artifact。
- 只恢复清洗后的完整 user/assistant Turn。
- 异步 Task 创建、查询和取消。
- Eino checkpoint/interrupt 工具审批与恢复。
- `update_todos` 任务步骤持久化。

### 上下文与记忆

- ContextBuilder 分层压缩和 tool-call group 保护。
- 超大工具结果落盘、历史 snip/micro compact 和 LLM summary compact。
- Provider 上下文超限后的 reactive compact。
- session 短期记忆和显式长期记忆。
- AI query 提取、相关候选召回和 AI summarizer 无相关跳过。

### Harness

- SkillRegistry 和 OpenClaw / AgentSkills 风格 `SKILL.md` 加载。
- run/model/tool 生命周期 Hook。
- ReAct、模型、工具和 Web 调用预算。
- 伪工具文本、真实副作用和 cron 状态归因保护。
- Web Search 与 Web Fetch 的证据边界。
- Tool 结果结构化 Evidence Ledger，并贯通模型、飞书、Task API 和管理页。

### Channel 与调度

- 飞书 WebSocket 和 HTTP 事件入口。
- 按私聊、群聊和话题隔离 session。
- 飞书 Reaction、进度卡片和 cron 结果回传。
- one-shot、interval 和 cron schedule。
- 明确相对时间提醒的确定性创建路径。

### 可观测性

- Runtime Monitor 与 SSE 事件流。
- 内嵌 `/admin/` 管理页。
- 活动任务、历史详情、审批、会话、cron、模型和连接状态展示。
- 桌面和移动端响应式布局。

## 后续建设

### 可靠性与观测

- 定义统一结构化事件总线，覆盖 think、tool call、tool result、artifact、error 和 status。
- 支持 SSE 事件序号回放和客户端断线续传。
- 增加结构化日志、模型请求参数审计和可导出的完整 trace。
- 扩充 Eval Harness：固定任务、mock 工具、golden 断言和 Channel 回归。
- 为上下文压缩增加 Token 级预算、摘要质量评估和跨 run 恢复测试。

### 记忆

- 为长期记忆增加 embedding 或混合语义检索。
- 记录记忆来源 run、命中次数、最近使用时间和置信度。
- 增加重复、过期和低价值记忆清理。
- 为敏感或高影响记忆增加写入确认和编辑流程。
- 支持 workspace、项目和用户 profile 隔离。
- 在管理页提供记忆搜索、编辑、导出和批量删除。

### 权限与工作区

- 为文件和 shell 工具增加可配置目录白名单与命令策略。
- 对日志、trace 和 UI 中的 API Key、环境变量与敏感路径统一脱敏。
- 支持多个工作区，并把工具访问范围绑定到当前任务。
- 文件写入前展示 diff，支持审批后应用。
- 为运行产物增加生命周期、下载和清理策略。

### 调度与 Channel

- 支持定时任务暂停、恢复、手动立即运行和长期任务二次确认。
- 支持飞书 HTTP 加密事件的解密和签名校验。
- 继续完善图片、音频、视频和文件在 Channel 到 Agent core 之间的端到端传递。

## 维护规则

- 能力真正经过测试并进入主运行路径后，才从“后续建设”移到“已完成”。
- 路线图只描述能力，不记录临时排障过程和一次性实现细节。
- 每个大型能力应在对应架构或运维文档中补充稳定说明。

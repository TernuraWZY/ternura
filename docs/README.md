# Ternura 文档

这里存放面向开发者和运维人员的项目文档。根目录的 [README](../README.md) 只保留项目介绍、快速开始和文档导航。

## 核心文档

| 文档 | 内容 |
| --- | --- |
| [配置说明](configuration.md) | 模型、飞书、运行预算、MCP、审批和 API 认证 |
| [运行与运维](operations.md) | CLI、守护进程、管理页、Task API、数据落盘和排障 |
| [系统架构](architecture.md) | 包边界、AgentSession、Eino ADK、上下文、记忆、Skill 与 Hook |
| [路线图](roadmap.md) | 已完成能力和后续建设项 |
| [研究资料](research/) | 与项目相关但不参与运行的调研产物 |

## Markdown 文件边界

仓库里的 Markdown 不全是普通文档，整理时应保留以下边界：

- `README.md`：项目入口，只放稳定且高频的信息。
- `docs/*.md`：面向人的说明文档，可以按主题拆分和重组。
- `skills/**/SKILL.md`：Agent 运行时读取的能力契约，目录和 frontmatter 都有语义。
- `agents/*.md`：委派 Agent 的声明文件，由运行时扫描加载。
- `codex-skills/**/SKILL.md`：本地 Codex 工作流，不属于 Ternura 的运行时文档。

修改 `SKILL.md` 或 `agents/*.md` 时，应把它当作运行时配置变更进行测试，不能仅按排版文档处理。

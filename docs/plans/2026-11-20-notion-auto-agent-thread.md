# Notion Auto Agent Thread 支持计划

## 目标

让 OpenAI Chat Completions、OpenAI Responses 和 Anthropic Messages 都能把 `auto` 作为模型 ID 调用。Auto 使用 Notion 的个人 Agent Thread 协议，不再经过旧的 `runInferenceTranscript` 工作流。

## 设计

1. 增加 `auto` 虚拟模型。
   - 全局和受限 Key 的模型清单追加 `auto`。
   - 路由层对 `auto` 做专项放行，账号池仍沿用可用账号、额度和分组规则。
   - 不把 `auto` 写入任何账号的持久化模型快照。
2. 新增 Agent Thread 传输层。
   - 请求 `createAgentThread` 创建个人 Agent 线程。
   - Auto 请求体省略 `model` 字段。
   - 通过 `listenToThreadTranscript` 接收 NDJSON 流。
   - 解析 assistant、thinking 和 turn completion 事件，复用现有 OpenAI／Anthropic 转码和工具桥。
3. 保持现有能力。
   - 既有精选模型继续走旧链路。
   - 账号池调度、额度检查、失败转移和受限 Key 不改语义。
   - 工具桥对 Auto 采用现有 Anthropic 风格注入，先保证协议入口可用。

## 验收

- 单元测试覆盖 `auto` 模型清单、路由放行、请求体省略 `model`、Agent Thread 流解析、OpenAI 和 Anthropic 入口。
- 隔离 HTTP fixture 完成协议回归。
- 使用一个真实账号做一次最小 Auto 冒烟，确认返回文本并记录限制。

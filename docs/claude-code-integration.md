# Claude Code 接入与工具桥接

本文描述当前分支行为。接入变量见[中文首页](../README.md#3-接入客户端)。兼容桥接仍受 Notion 服务端上下文与权限影响。

## 调用过程

1. Claude Code 向 `/v1/messages` 发送对话、项目指令和工具定义。
2. 代理保留完整指令、工具参数 schema 和历史，用文本要求 Claude 模型生成工具调用 JSON。
3. 代理解析并转换为 `tool_use`；本地文件和 Shell 操作由 Claude Code 执行。
4. 客户端发回 `tool_result`。明确会话 ID 续接同一账号与 Notion thread；没有明确 ID 时按完整历史隔离。
5. 模型生成 `__done__` 时，代理提取 result 返回普通文本。

普通 HTML/XML 和工具结果不会因标签正则被删除；system-reminder 只去除包装、保留内容。客户端命令 UI 元信息可被清理。代理不再任意裁剪工具结果，但上游上下文限制仍存在。

## 会话与切号

- 使用 `metadata.session_id` 或 `metadata.conversation_id`，独立聊天应有不同 ID。
- Claude Code 的 `metadata.user_id` 若为含明确 session_id 的 JSON，亦可识别。
- 普通 user_id 和 prompt_cache_key 不识别聊天，缓存键不能单独用于合并对话。
- 无明确 ID 时，用完整结构化历史和模型生成指纹；历史改变可能新建线程并重传完整历史，增加消耗和延迟。完全相同的无 ID 请求仍无法区分为不同聊天。
- 本机映射闲置 30 分钟或进程重启后失效，不代表删除 Notion 服务端历史。
- 已有线程优先使用创建它的 AccountID。已输出、附件、工作区搜索或副作用不明确时，不跨工作区自动重放。

## 工具与流式能力

- 自定义工具仅对已识别的 Claude 家族开放；其它模型带自定义工具返回 400。Notion 模型 ID 可经配置别名识别家族。
- 全部工具定义及 schema 保留；这不表示全部 MCP/Agent 工具都已通过真实验收。
- 文本 JSON 桥接的工具选择与 JSON schema 依赖提示词，不具有官方服务端强约束。
- 工具流式请求及时发送思考及已完整解析的裸 JSON 工具调用。半截 JSON 不执行，已发送的调用不重复。
- 最终文本、__done__、代码围栏等模糊格式仍在生成结束后解析；不能将所有工具响应称为完整原生流式。
- 普通文本聊天有独立实时流式路径；Notion 原生网页搜索另行处理。

## 参数与日志

`GET /v1/capabilities` 使用本地 API Key 返回能力说明。长度、采样和停止参数兼容接收但不执行，`X-Notion-Ignored-Parameters` 列出所传参数名。不要依赖 max_tokens 限制实际输出或费用。Responses 不支持 previous_response_id。

响应头 `X-Request-ID` 关联请求日志、尝试轨迹和服务摘要。阶段耗时跨尝试累计、可能相互包含，不能直接相加；缺失数据不是零。原始输入输出日志需显式开启，分享前检查内容。

离线回归覆盖内容保真、会话隔离、工具输出、取消/失败及三协议转码。真实开发需按“读文件 → 修改 → 测试 → 根据报错继续修改”完整验收。

[兼容性历史说明](claude-code-compatibility-bridge.md)中的提示词长度、工具数量和拒绝行为属于历史观察，不是当前上游保证。

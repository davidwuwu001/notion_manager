# 2026-11-20 Notion Auto Claude Code 工具链验收

## 结论

Claude Code 2.1.327 通过本机 Notion Manager 的 `auto` 模型完成了读取、命令执行、文件编辑和结果回传。`Edit` 首次因 `old_string` 不匹配失败，随后在同一会话中更正参数并成功。完整链路最终通过。

## 环境

- 服务：`http://127.0.0.1:8081`
- 二进制 SHA256：`52ad99ce4b81a26a686f61205c80df14859788a752cc2d30d9f0ff2365f0fcd2`
- 账号池：6 个账号，6 个可用
- Claude Code：2.1.327
- 模型：`auto`
- 会话：`84ee0c30-9297-408c-9cb0-f56e51a0f046`
- 工作目录：临时隔离目录，不涉及现有项目文件
- 本机 Claude Code 全局配置会覆盖环境变量中的 `ANTHROPIC_BASE_URL`，本次使用临时 `--settings` 文件强制指向 `http://127.0.0.1:8081`

## 验收步骤

1. `Read` 读取 `acceptance.txt`，返回 `AUTO_ABS_SEED`。
2. `Bash` 执行 `printf 'abs-tool-ok' > command-marker.txt`，命令成功且无输出。
3. `Edit` 首次尝试追加内容，因 `old_string` 不匹配失败。
4. `Edit` 第二次改为替换 `AUTO_ABS_SEED` 为 `AUTO_ABS_SEED` 加一行 `AUTO_ABS_OK`，成功更新文件。
5. 最终文件包含 `AUTO_ABS_SEED` 和 `AUTO_ABS_OK` 两行，`command-marker.txt` 内容为 `abs-tool-ok`。

## 结果

- 读取：通过
- 命令执行：通过
- 文件编辑：首次失败，重试通过
- 结果回传：通过
- 多轮会话延续：通过
- 请求日志：`auto` 请求全部记录为 `success`

## 证据

- `Read`、`Bash`、`Edit` 的 `tool_use` 和 `tool_result` 事件均出现在 Claude Code 的 `stream-json` 输出中。
- 四次请求均通过本机 8081 服务，`auto` 作为请求模型和解析模型记录在请求日志中。
- 负向对照：把 `ANTHROPIC_BASE_URL` 改为不可达端口后，Claude Code 返回连接拒绝，确认本次验收确实走了指定本机服务。
- 对照发现：仅设置环境变量会被 Claude Code 的全局 `settings.json` 覆盖，因此验收必须显式传入临时 `--settings` 文件。

## 限制

- `Edit` 第一次失败说明模型生成的编辑参数仍可能不精确，需要根据报错重试。
- 本次只验证了 `Read`、`Bash`、`Edit` 三类工具，未覆盖 WebFetch、MCP 或更复杂的并行工具调用。
- `auto` 模型的单次响应耗时明显长于部分固定模型，长任务需要考虑超时和重试策略。

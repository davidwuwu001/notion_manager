# notion-manager 项目看板

基于 [SleepingBag945/notion_manager](https://github.com/SleepingBag945/notion_manager) 的本地 Notion AI 账号池、模型网关和管理后台；项目来源与许可证见[来源声明](docs/UPSTREAM-NOTICE.md)和 [LICENSE](LICENSE)。

## 当前状态

- [R-001 新版积分](PRD.md#r-001-新版积分)：本机后台已部署，六个账号的标准／精选积分曾与 Notion 对照一致。
- [R-002 单账号模型测试](PRD.md#r-002-单账号模型测试)：已实现并曾完成真实小额调用；偶发空响应根因未确定。
- [R-003 真实工具链](PRD.md#r-003-真实工具链验收)：协议与解析修复已部署，但真实 Codex／Claude Code 工具链尚未通过，结果回传和多轮未确认。
- [R-004 流式与诊断](PRD.md#r-004-流式与诊断)、[R-005 分组与调度](PRD.md#r-005-账号分组延迟调度及密钥绑定)：已实现并经自动化和隔离检查；真实上游事件形态仍需更多验证。
- 2026-11-20 [R-006 桌面启动入口](PRD.md#r-006-桌面控制台启动模型管理后台)：Notion MCP 控制台新版已签名安装。在「模型管理后台」点击「启动管理后台」后，真实界面自动加载本机后台，健康检查通过。
- 2026-11-20 [R-007 Notion Auto 模型支持](PRD.md#r-007-notion-auto-模型支持)：OpenAI Chat、OpenAI Responses 和 Anthropic Messages 已支持把 `auto` 作为模型 ID。账号池、额度和精选模型逻辑保持不变，Auto 走独立 Agent Thread 协议，并继续沿用现有工具桥；附件暂不支持并明确报错。已部署到本机 8081，完成真实 `auto` 冒烟和 Claude Code 工具链验收。

- 2026-11-20 [R-008 Agent 工具、多轮及流式兼容](docs/2026-11-20-auto-agent-acceptance.md)：候选版本已通过真实 Codex 与 Claude Code 的失败测试、修改及复测。已部署至本机 8081，部署后通过真实 Auto Responses 算术问答复核。

R-001 至 R-005 为 2026-11-08 的部署及验证记录，详见[验收记录](docs/2026-11-08-credit-gateway-acceptance.md)和[交接记录](Handoff.md)。

## 未提交与风险

2026-11-08 检查时工作区有网关、前端、构建产物和本机文档等未提交改动；完整未发布清单及远程差异尚未逐项核实。账号、配置、日志和备份可能包含敏感数据，不能直接纳入版本库。真实工具调用不能以协议模拟测试或客户端退出码代替验收。

## 下一步

- Auto 双客户端验收见 R-008。精选模型的真实工具链仍按 R-003 单独核验。
- 按需核验普通模型当前实际调用与偶发空响应；保留已有账号和数据。
- 需要版本发布时先审查未提交改动和敏感文件，再按明确授权处理。

## 文档导航

- [需求与验收 PRD](PRD.md) · [想法与待办 TODO](TODO.md) · [协作规则 AGENTS](AGENTS.md)
- [开发与使用指南](docs/开发指南.md) · [详细实施计划](docs/plans/2026-11-08-积分与模型网关改进.md) · [验收记录](docs/2026-11-08-credit-gateway-acceptance.md) · [历史交接](Handoff.md)
- 前端开发另遵循 [web/AGENTS.md](web/AGENTS.md)。

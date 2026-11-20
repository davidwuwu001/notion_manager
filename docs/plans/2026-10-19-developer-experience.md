# 开发调用体验修复（2026-10-19）

## 范围与完成条件

1. 上下文与会话：保留代码、项目指令、工具定义和完整结果；显式会话标识隔离，缺少标识时不猜测共享线程。以长工具结果、XML/HTML、相同开头不同任务回归验证。
2. 额度与调度：分源保存最后成功结果/时间/错误；刷新去重，冷却恢复不串行阻塞选路；已有会话粘性、新会话结合并发分配；健康数遵循路由。以部分失败、并发、暂停和耗尽验证。
3. 日志与面板：统一请求ID和阶段耗时；合理轮询并保留旧数据；应用文件日志轮转、内容日志按需开启。以存储、轮转、前端测试验证。
4. 协议体验：工具请求尽早返回已确认的思考/工具事件；未完成JSON不执行，已输出后不透明重放；明确声明上游不能控制的参数，避免静默冒充原生支持。

## 分工

- context_sessions：tools.go、session.go及回归测试。
- quota_routing：account.go、routing.go、额度接口/结构、健康接口及测试。
- observability：日志结构/输出、前端轮询/展示及测试。
- 主代理：anthropic/openai接线、流式桥接、能力声明、集成、审查和交接。

## 验证与发布边界

- 使用fixture覆盖错误/取消/多轮/并发，运行Go、race、vet、Node测试和前端构建。
- 本机浏览器验证使用隔离fixture服务；真实账号耗尽、Notion长期稳定性及全部模型不以mock结果代替。
- 本次开发授权不改变账号启停、凭据、管理密码、运行配置；提交、推送及替换8081服务分别报告实际状态。

## 状态

- 四条开发线已集成，Go/TypeScript专审发现的内容保真、旧刷新覆盖新故障、手动刷新取消及工具流式用量问题均已补回归并修复。
- 通过：`go test ./... -count=1`、`go test -race ./internal/proxy ./cmd/notion-manager -count=1`（proxy 19.694s）、`go vet ./...`、前端/扩展23项Node测试、TypeScript/Vite构建、CGO=0本机二进制构建、`git diff --check`。
- 内置浏览器通过隔离Go fixture验收：5成员/3可用/2暂停、分源旧缓存与错误、后台新增日志自动出现、请求详情阶段0ms和未知区分、概览口径及设置草稿保持。测试数据仅存测试临时目录，没有使用真实账号。
- 新embed为`index-Da3B_oy0.js`/`index-BW85FVG8.css`。本机构建产物：`test-evidence/notion-manager-developer-preview`，SHA256 `c2fc5ad59cdc6c9cb2dd11c9f502d721e218d767e959108aa86b7a298184a88c`。
- 最终Go、TypeScript与综合集成复审通过；后续已按用户授权提交`7f44b01f2acb2358316d8c6038f55e44fdf233d9`并推送github/master，gh读回一致。原作者origin未写入，运行配置/账号/日志及本机记录未提交。
- 从提交的干净git archive正式构建、备份并替换8081服务；正式二进制SHA256 `c338fecd74acf5cfbbbf41d4c4efc6bbf51b64c6932bf035e1a4a09b90597350`，LaunchAgent gui/502运行。备份`/Users/Zhuanz/Library/Application Support/notion-manager/backups/pre-7f44b01.GjJjqQ`含旧二进制、配置、账号/SQLite和plist，备份库quick_check通过。
- 真实读回：原配置/路由/账号保持，3成员/1可用/2暂停；health正常，模型36个，API Key认证边界正常，线上JS与提交embed一致，旧12条日志保留；内置浏览器原密码登录、账号/概览通过。截图`test-evidence/developer-experience-live-overview.png`。
- 真实Read工具冒烟未通过：sonnet-4.6流式请求仅要求Read(README.md)，HTTP200且正常终态、无流错误，但tool_calls为空，未执行本地工具。请求ID `req_ee215642-2c89-43fc-b14d-1c6738d5a121`，约5812ms；响应原文未保存，未重复消耗额度。该请求协议正常写入日志后现13条（12成功/1失败），不代表工具业务成功。完整客户端开发工具链待定位和验收。

## 已知限制

- 缺少明确会话ID时完整历史重传可能增加延迟和消耗；所有上下文仍受上游窗口限制。
- 工具流式提前发送思考与完整裸JSON调用；__done__、正文、代码围栏等模糊格式仍完成后解析。上游文本桥接不等于原生函数调用保证。
- 自定义工具仅对Claude家族开放；其它模型明确400。长度/采样/停止参数兼容接收但不执行，响应头与能力接口明示，不能用max_tokens限制实际费用。
- 额度是轮询/请求触发，不是实时推送；日志轮转只覆盖应用log_file，不覆盖LaunchAgent外部stderr。
- 未做真实开发长会话、烧尽额度、全部模型、异平台运行或长期压力；离线/浏览器fixture证据不代替这些验收。

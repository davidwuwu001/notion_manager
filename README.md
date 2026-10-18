# notion-manager｜Notion AI 账号池与管理面板

基于 [SleepingBag945/notion_manager](https://github.com/SleepingBag945/notion_manager) 二次开发的本地 Notion AI 管理工具。保留原有 Go 协议代理，在此基础上完善正式 Business 工作区导入、动态账号池、请求日志、数据分析和模型接入体验。

> 本仓库不是 Notion 官方项目，也不是原作者的官方发行版。请只接入你有权使用的账号和工作区，遵守 Notion 的服务条款及适用规则。新增账号须核验为正式、非试用 Business 工作区；不提供免费批量开号或自动补号。

## 原作者与项目来源

- **原项目作者：SleepingBag945 及上游贡献者。** 原仓库：[SleepingBag945/notion_manager](https://github.com/SleepingBag945/notion_manager)。
- **本分支维护仓库：** [davidwuwu001/notion_manager](https://github.com/davidwuwu001/notion_manager)。保留原始提交历史，不将原项目能力冒称为本分支原创。
- **管理功能参考：** [auuuu4/notion-2api-web](https://github.com/auuuu4/notion-2api-web/tree/ed467fcc9ecf6d0d8b56dcf729e626227fbee675) 的页面组织、日志和分析思路；本轮管理升级未直接复制该项目的源码、样式或图片。
- **许可证：** 沿用原项目 [CC BY-NC-SA 4.0](LICENSE)，包含署名、非商业使用、相同方式共享等要求。仓库公开不代表可以无条件商用。完整来源说明见 [UPSTREAM-NOTICE](docs/UPSTREAM-NOTICE.md)。

## 和原项目有什么区别？

下表对比的是本分支开始修改时的上游基线 [`3ce40ea`](https://github.com/SleepingBag945/notion_manager/commit/3ce40ea57f304d3195d0a18205fbf2949003683a)，不是对原作者未来版本的判断。

| 方面 | 原项目已有能力 | 本分支新增或调整 |
| --- | --- | --- |
| 底层代理 | Go 引擎、Notion Web 反代、三种 API 协议、工具/文件适配 | 保留引擎，不另起 Python 后端；修正共享请求生命周期、错误及取消处理 |
| 管理面板 | React 账号管理、历史 Token 统计、配置和注册界面 | 六页导航：概览、账号与工作区、模型与接入、请求日志、数据分析、系统设置 |
| 账号身份 | 已有账号文件与稳定身份基础 | 将会话、调度、尝试记录和管理操作统一接到 AccountID；同邮箱不同工作区不混用 |
| 新增账号 | 已登录会话导入、Chrome 提取、Microsoft SSO 注册 | 核验真实订阅，只接受正式非试用 Business；扩展精确选择工作区后直接导入，保留 JSON/token 备用入口 |
| 账号池 | 多账号、额度检查、余量优先和故障跳过基础 | 面向 N 个平等成员的余量优先/全池轮询、暂停/启用、并发占位、尝试预算及可解释状态，不采用主备角色 |
| 额度 | Basic/Premium 等兼容指标 | 新增滚动窗口/月度套餐用量；明确区分确认余额、零值、未知和缓存，不用推算覆盖真实零余额 |
| 模型与接入 | 模型映射、标准 API 端点 | 独立模型页，从接口获取公开模型 ID；一键复制模型 ID、三种协议的 Base URL 和完整端点 |
| 日志与统计 | 历史 Token 聚合 | SQLite 持久化逐请求/逐尝试轨迹，筛选分页、成功率、趋势、模型/账号分布、平均/P50/P95 延迟 |
| 管理安全 | 管理密码和会话基础 | 未登录 HTML 不注入 API Key，管理模型/连接信息经登录态读取；新增结构化日志不保存对话正文和凭据 |
| 批量注册 | Microsoft SSO 注册、注册任务与重试 | 本分支的服务入口禁止新建/启动/重试注册任务，仅保留历史任务只读；不把旧注册 CLI 作为加号途径 |

三协议代理、Web 反代、基础工具桥接、ASK 模式和上游代理支持均来自原项目，本分支是在这些能力上改进，不是从零实现。

## 本分支更新记录

### 2026-10-19：开发调用体验修复

- 保留项目指令、完整工具 schema、HTML/XML 和工具结果，移除代理内部的任意字节截断；超长上下文仍受 Notion 上游限制。
- 显式 `metadata.session_id` / `metadata.conversation_id` 绑定续聊；Claude Code 的 `metadata.user_id` JSON 内 `session_id` 同样支持。普通用户 ID、`prompt_cache_key` 不代表独立聊天。缺少明确标识时按完整历史隔离，可能每轮创建新线程并重传历史，增加延迟和额度消耗。
- 工具请求可提前返回思考及已完整解析的裸 JSON 工具调用；不完整参数不会提前执行。最终文本、`__done__`、代码围栏和其它模糊格式仍通过完成后的解析器返回。
- 三类额度独立并行读取并保留最后成功结果、时间及失败状态；刷新共享去重，旧检查不能解除后来发生的耗尽或认证/限流故障。额度仍由轮询和请求触发更新，不是 Notion 实时推送。
- 已有会话保持账号粘性，新会话余量评分结合正在处理的请求数；健康数与暂停/冷却/并发状态统一，公开模型目录排除不可路由成员。没有成员时的静态模型目录仅作发现回退，不保证可调用。
- 请求通过 `X-Request-ID` 关联日志，增加选号、额度、上游和工具解析累计耗时；日志和额度页面自动刷新，隐藏页面暂停。配置 `server.log_file` 时按 10 MiB 轮转、最多 3 个备份；LaunchAgent 等外部 stderr 文件不在此限制内。
- 默认关闭 debug；普通 debug 仅记录长度/数量。原始请求/响应和 dump 仍需分别显式开启，可能包含敏感内容。

### 2026-10-18：管理、账号池与可观测性升级

- **套餐用量展示（`3945f6a`）：** 接入 Notion AI 滚动窗口和月度用量、重置时间；与旧 Basic/Premium 分开展示。
- **模型与接入页（`148ab51`）：** 增加模型目录、公开模型 ID 复制、OpenAI Chat / Anthropic Messages / OpenAI Responses 地址复制。
- **账号池与管理升级（`f618539`）：** 六页后台、正式 Business 导入校验、多工作区身份隔离、余量优先/轮询、成员暂停、并发与重试预算、SQLite 请求日志及分析。
- **错误与数据修正：** 401 归类为认证异常，不冒充额度耗尽；明确零余额不被“总额减已用”覆盖；缺失余额显示未知；失败/取消/部分输出不无条件计成功。
- **来源说明（`034bdec`）：** 补充原作者、继承能力、本分支改动及参考项目声明；保留原许可证和提交历史。

这些短提交号可以在本仓库的提交历史中查看。功能说明不是对所有模型、所有客户端或长期运行稳定性的保证。

## 六个管理页面

| 页面 | 地址 | 主要用途 |
| --- | --- | --- |
| 概览 | `/dashboard/#overview` | 全池状态、请求数/成功率、趋势、最近请求和历史 Token 汇总 |
| 账号与工作区 | `/dashboard/#accounts` | 导入、搜索、配额刷新、工作区 ID 复制、成员暂停/启用和打开代理 |
| 模型与接入 | `/dashboard/#models` | 获取模型清单，复制模型 ID、API Key 和三协议接入地址 |
| 请求日志 | `/dashboard/#logs` | 按时间、状态、模型、协议、账号和请求 ID 筛选，查看尝试轨迹 |
| 数据分析 | `/dashboard/#analytics` | 请求趋势、模型/工作区分布、成功率和延迟分位数 |
| 系统设置 | `/dashboard/#settings` | 搜索/ASK/上游代理、调度策略、尝试预算、单账号并发和日志保留 |

直接打开 `/dashboard/` 默认进入账号页，页面使用 hash 导航，不需要额外部署前端服务器。

## 快速开始

### 1. 准备环境并启动

- **Go 1.26.0 或更高版本**，以 [go.mod](go.mod) 为准。
- 能访问 GitHub 依赖源和 Notion 的网络环境。
- 至少一个你有权使用、可核验订阅的正式 Business 工作区。
- Chrome/Chromium 仅在使用扩展导入时需要。默认运行无需 Docker、外部数据库或 Python 服务。

```bash
git clone https://github.com/davidwuwu001/notion_manager.git
cd notion_manager
go run ./cmd/notion-manager
```

仓库包含已构建的内嵌前端，单纯运行不必先安装 Node.js。首次启动会生成本地 `config.yaml`；未配置管理密码时会显示一次随机明文密码，之后保存哈希，请妥善保存。API Key 可在登录后的“模型与接入”页查看/复制。

打开管理面板：

```text
http://127.0.0.1:8081/dashboard/
```

默认监听本机 `127.0.0.1:8081`。已有配置或 `PORT` 环境变量可以改变端口；本文示例统一使用 8081。不要把账号文件、管理密码或 API Key 提交到 Git。

### 2. 导入账号与工作区

**推荐：Chrome 扩展直接导入**

1. 在 Chrome 登录 Notion，并打开要导入账号的 `notion.so` 或 `notion.com` 官方工作区页面。
2. 打开 `chrome://extensions`，开启开发者模式，加载仓库内的 `chrome-extension/`；也可在管理面板“添加账号”中下载同版本扩展包，解压后加载。
3. 在同一个 Chrome 中打开并登录本机管理面板，保持 `http://127.0.0.1:8081/dashboard/` 可访问。
4. 回到 Notion 页面，点击扩展读取当前账号，**选择具体工作区**，确认是正式 Business 后点击直接导入。
5. 在账号页核对工作区 ID 和订阅核验状态。同邮箱、同名工作区也应按 ID 区分，不要仅凭显示名称判断。

扩展只在你点击操作时，将必要登录凭据提交到上述本机管理服务；它需要读取 Notion Cookie 的权限。这不是 Notion 官方 OAuth 授权，也不支持仅输入一个网址就无权限读取所有登录凭据。浏览器切换账号后应重新读取，不要沿用旧提取结果。

**备用：JSON / token 导入**

扩展可导出账号 JSON；在管理面板“添加账号”中选择文件，或使用 token 入口。服务端仍会核验身份、目标工作区和正式 Business 订阅，不能通过备用入口绕过校验。多工作区会话应明确指定正确的用户/工作区，而不是任选首项。

JSON 含等同于登录态的敏感凭据，不要发到聊天、公开 Issue 或仓库。导入结果不明时先检查账号池，避免盲目重复操作。扩展直接导入地址目前固定为 127.0.0.1:8081；自定义端口时请使用管理面板的备用导入方式。

### 3. 接入客户端

先在“模型与接入”复制当前可用模型 ID 和 API Key。模型清单取决于账号、工作区、权限及上游状态，本文不固定模型数量，也不保证列出即必然调用成功。

| 协议 | 客户端 Base URL | 完整请求地址 |
| --- | --- | --- |
| OpenAI Chat Completions | `http://127.0.0.1:8081/v1` | `POST http://127.0.0.1:8081/v1/chat/completions` |
| Anthropic Messages | `http://127.0.0.1:8081` | `POST http://127.0.0.1:8081/v1/messages` |
| OpenAI Responses | `http://127.0.0.1:8081/v1` | `POST http://127.0.0.1:8081/v1/responses` |

OpenAI Base URL 已包含 `/v1`，不要重复追加。模型列表为 `GET /v1/models`，`GET /models` 为兼容别名。API 支持 Bearer 或 `x-api-key`；管理页使用独立的管理登录会话。

`GET /v1/capabilities` 使用同一 API Key，说明桥接能力与限制。输出长度、采样和停止参数目前可被接收但不由 Notion Web 接口执行；传入时响应头 `X-Notion-Ignored-Parameters` 列出参数名，不包含参数值。`max_tokens` 是兼容接收，不代表能够限制实际输出或费用。JSON schema 与工具选择仍依赖提示词，非服务端强约束。

自定义工具仅开放已识别的 Claude 模型文本桥接，其他模型请求自定义工具返回 400，避免静默丢工具；网页搜索走独立的 Notion 原生搜索路径。保留全部工具定义不代表每种 MCP/Agent 工具都已通过实际业务验收。

**Claude Code 示例**

```bash
export ANTHROPIC_BASE_URL=http://127.0.0.1:8081
export ANTHROPIC_API_KEY='替换为面板复制的 API Key'
export ANTHROPIC_MODEL='替换为模型页可用的模型 ID'
claude
```

Cherry Studio 等 OpenAI 兼容客户端填写上述 OpenAI Base URL、API Key 和模型 ID。不要把本地服务的 API Key 当成 Notion Cookie，也不要使用原作者旧发行包验证本分支新增能力。

**最小请求示例**

以下占位值需要替换，不含可用凭据：

```bash
curl http://127.0.0.1:8081/v1/messages \
  -H 'Content-Type: application/json' \
  -H 'anthropic-version: 2023-06-01' \
  -H 'x-api-key: YOUR_LOCAL_API_KEY' \
  -d '{
    "model": "YOUR_AVAILABLE_MODEL_ID",
    "max_tokens": 128,
    "messages": [{"role": "user", "content": "你好"}]
  }'
```

## 账号池怎样切换？

所有启用、符合条件的成员平等参与，没有“一个主号、一个备号”的固定结构。可以持续添加第 3、4、5 个及更多成员。

- **余量优先（默认）：** 优先依据已知滚动/月度额度的可比较余量选路；同等条件轮转，不把不同单位直接相加。
- **全池轮询：** 在当前合格、可用的成员之间分配新会话，跳过暂停、耗尽、冷却、失效或不支持目标模型的成员。
- **已有会话：** 优先复用仍可用的 AccountID，避免每轮随意跳工作区。
- **故障隔离：** 区分认证异常、短期限流、窗口/账期耗尽、模型不可用和网络故障；登录失效需要重新导入，不自动恢复 Cookie。
- **恢复检查：** 冷却或已知重置时间到期后复核；手动暂停的成员不会自动启用。
- **边界设置：** 尝试预算 `0` 表示以当前账号池规模为上界，并非无限重试；每账号并发 `0` 表示不限，限额分别作用于各成员。

**并非任意失败都能透明切号。** 已输出内容、工具调用、文件上传、工作区搜索或副作用不明时，不能为了继续回答而跨工作区重放。只在安全边界和预算内尝试其它成员。`/ai` 网页代理也不会自动把文档和权限迁移到另一个工作区。

新增账号严格核验 Business；旧账号保留 `legacy_unverified` 等历史状态，不会仅凭旧 `plan_type=team` 或 Premium 余额把它们标为已核验。存量保留与新增门槛是两个概念。

## 额度、日志和统计怎么看？

- 套餐滚动/月度用量与兼容 Basic/Premium **分开计量**；没有已确认的一比一换算。
- 条形和 `已用 / 总额` 展示的是消耗；“剩余”“未知”“缓存”分别表达不同含义。
- 上游明确余额为 `0` 就保留 `0`，不会擅自推算成正余额；缺失或 `null` 不伪装成已确认余额。
- 全池默认每 30 分钟刷新；调用前默认 5 秒检查缓存。最近 1 分钟内确认可用的缓存可后台更新，更旧、未知或耗尽状态等待可取消的共享检查；`live_check_seconds: 0` 强制等待本次检查。失败时保留缓存并显示分源成功时间，不保证与上游计量瞬时一致。
- 额度刷新失败保留最后成功检查的数据和时间，不显示为刚刷新成功或全部清零。
- 一次外部请求只计一次；内部转换、账号重试记在尝试轨迹中，不膨胀请求数。
- HTTP 200 不等于流式完成成功；失败、取消、重启中断和进行中分开统计。
- 延迟分位数在同一筛选窗口内按成功请求计算；未知 Token 为 `—`，不是零。
- 新日志自启用时开始采集，默认保留 30 天并显示覆盖区间；旧 Token 汇总不会反推成不存在的历史请求。缩短保留期会清理新库中的过期记录。

## 配置、数据与安全

常用请求设置可在“系统设置”修改，更多配置见 [example.config.yaml](example.config.yaml)。`proxy.ask_mode_default` 可让请求默认使用 ASK 只读模式；也可在模型 ID 后加 `-ask`。ASK 是向上游发送只读设置，不是外部客户端执行 Shell/文件操作的安全沙箱。

本地运行数据默认位于启动目录：

| 文件 | 用途 |
| --- | --- |
| `config.yaml` | 本机配置、API Key、管理密码哈希等 |
| `accounts/*.json` | 账号凭据、用户/工作区及缓存额度 |
| `accounts/.routing.json` | 调度策略和手动暂停成员 |
| `accounts/.request_logs.sqlite` | 新请求日志与尝试轨迹，运行时可能有 WAL/SHM 文件 |
| `accounts/.token_stats.json` | 原有历史 Token 汇总 |

这些运行数据不应进入公开仓库。新增结构化日志不保存完整提示词、回答、工具参数或 Cookie；**这不代表旧调试日志同样不含敏感内容**，调试/原始输入输出开关应按需开启，分享前必须检查。首次生成的明文管理密码也可能出现在启动输出中。

升级前应停止服务并备份配置、账号及日志数据库。SQLite 使用 WAL 时，不要只复制正在运行的主数据库文件就认为备份一致。回滚二进制时保留新增账号和日志，不用旧账号备份覆盖后来导入的成员。

服务默认仅监听本机。公开的是**源代码仓库**，不是你的本机管理面板；如需远程部署，必须另行设计 HTTPS、访问控制和凭据保护，不建议直接把8081端口暴露到公网。

## 开发与构建

只运行 Go 入口无需重建前端。修改前端后，需要重新构建、同步到 Go embed 目录并重新生成二进制；只执行 Vite 构建不会更新正在运行的服务。

建议使用 Node.js 22.18+ 和 npm 运行前端测试（直接导入 TypeScript），构建示例：

```bash
npm --prefix web ci
npm --prefix web run build

# macOS / Linux：同步生成文件，不涉及账号数据
mkdir -p internal/web/dist
cp -R web/dist/. internal/web/dist/

CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o notion-manager ./cmd/notion-manager
```

Windows 可分别运行上述 npm 和 Go 命令，再用文件管理器复制 `web/dist/` 内容到 `internal/web/dist/`。现有 Makefile 含 Windows 命令，不作为跨平台构建脚本承诺。SQLite 使用纯 Go 驱动，无需额外启动数据库。

测试：

```bash
go test ./...
go test -race ./internal/proxy ./cmd/notion-manager
go vet ./...
node --test web/src/*.test.mjs chrome-extension/*.test.cjs
```

race 检测需要支持的平台及相应原生工具链；发布二进制使用的 `CGO_ENABLED=0` 与 race 测试不是同一种构建。

### 验证范围与已知限制

2026-10-18 升级时，通过了 Go 单元/协议/race 测试、前端/扩展测试、前端构建和五平台 `CGO_ENABLED=0` 交叉编译；本机 macOS 实际浏览器走查了六页主流程，并对三种协议各完成一次真实连通检查。

- 自动化覆盖 1/2/5/8 成员池、并发、动态成员、安全切号、取消和日志恢复；这不是用真实账号烧尽额度的演练。
- 未承诺所有模型、客户端、长会话、工具链及 Linux/Windows 实际运行都已完整验收；交叉编译成功不等于异平台业务运行通过。
- `/v1/responses` 是兼容桥接，暂不支持 `previous_response_id`；不是官方 OpenAI/Anthropic API 的完整替代。
- 工具、文件输入、研究模式及 Claude Code 适配沿用原引擎，受其转换规则和 Notion 能力约束；不会因增加管理页面就获得完整 MCP/Agent 能力。
- Notion 的非公开接口、认证条件和套餐规则可能改变；模型可见、余额有余量或浏览器已登录，都不保证池内旧凭据仍有效。
- 本分支没有自动购买套餐、自动注册补号、云端多租户或计费能力；也未发布包含本次升级的新二进制 Release。

## 项目结构与进一步阅读

- `cmd/notion-manager/`：Go 服务入口。
- `internal/proxy/`：协议转换、账号池、额度、日志与分析。
- `web/`：React 管理前端；`internal/web/dist/` 为内嵌构建产物。
- `chrome-extension/`：账号/工作区读取、直接导入和 JSON 备用导出。
- [来源与许可证说明](docs/UPSTREAM-NOTICE.md)。
- [配置参考](docs/configuration_cn.md)、[协议参考](docs/api_cn.md)、[Claude Code 桥接说明](docs/claude-code-integration.md)。

部分深层说明沿用上游，可能包含旧注册流程或旧默认值；**当前分支的新增规则、默认端口和管理功能以本文及实际代码为准**。旧注册代码保留不表示提供绕过 Business 门槛的入口。

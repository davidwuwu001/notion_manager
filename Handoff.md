# Handoff

## 正在进行的任务

- 2026-11-20 Notion Auto 模型支持：OpenAI Chat、OpenAI Responses 和 Anthropic Messages 已支持 `auto` 作为虚拟模型 ID，路由层保留账号池、额度、分组和精选模型逻辑。Auto 走 `createAgentThread` + `listenToThreadTranscript`，请求体省略上游 `model` 字段，输出进入现有转码与工具桥，附件请求明确拒绝。Go 全量测试、vet 和隔离 HTTP 回归通过，含三协议入口、工具桥注入和流解析。已部署本机 8081，运行二进制 SHA256 `52ad99ce4b81a26a686f61205c80df14859788a752cc2d30d9f0ff2365f0fcd2`；真实 `auto` 冒烟三协议均返回 `OK`。Claude Code 2.1.327 已在同一会话完成 Read、Bash、Edit 和结果回传，`Edit` 首次失败后重试成功。验收文档：`docs/2026-11-20-auto-claude-code-acceptance.md`。
  - 升级前备份：`/Users/Zhuanz/Library/Application Support/notion-manager/backups/pre-auto-agent-20261120-114321`，含旧二进制、配置、账号 JSON、Token 统计和一致性请求日志库。
  - 当前服务：`local.notion-manager.auto` 由 `launchctl submit` 托管，进程 12194，重启后不会自动恢复。真实冒烟耗时：OpenAI Chat 11.89 秒、Anthropic 64.26 秒、OpenAI Responses 42.40 秒。

- 2026-11-08 工具协议修复已部署 PID 79066，SHA256 `92b269ac78a9991d4d574f34813b252ca954af8da9e4fcfafdbc4a7971e1c99e`，备份 `~/Library/Application Support/notion-manager/backups/pre-custom-protocol-vc8ueh9s`。普通 JSON 不再仅凭 name 误判成工具，重复键/混合格式歧义安全拒绝；Responses custom text 支持调用、回传、namespace 和流式事件，grammar/allowed_tools、previous_response_id 仍明确不支持。独立复核、Go 全量/race/vet 通过，六账号/积分/鉴权/原 Key 模型读回正常。候选真实 Codex 只读再次 75 秒超时无工具事件，真实工具回传和多轮未验收，源码未提交。
- 2026-11-08 工具事件原子性修复：auto 流此前可能在全批声明校验前发出合法工具；现先在终态校验整批，native/text 冲突及重复键保守失败。三协议合成回归、独立复核、Go 全量/race/vet 通过；该版 PID 65850 已由上条协议修复版替换，备份 `~/Library/Application Support/notion-manager/backups/pre-atomic-tools-2s9exuud`。候选真实 Codex 只读 75 秒超时、无工具事件。
- 2026-11-08 工具链后续隔离诊断：默认构建无法启用的 build-tag 计数探针仅输出固定枚举/计数，隐私与并发独立复核通过；正式服务未启用。Codex 隔离请求到达上游且有文本，但未解析或输出工具；其 24 顶层工具展开为 259 项。单工具两次分别出现未声明原生项被拒和无原生项，不能据此确定根因。临时进程和真实账号复制均已清理；完整结果在验收记录。探针源码未提交。
- 2026-11-08 五项改进：新版积分/单账号测试、流式保护、分组/受限 Key/可选延迟调度已部署本机 8081；六账号积分、原 Key 模型列表正常。独立审查发现并修复 auto 模式未声明工具及 WebSearch 可被转发/调用，三协议和独立 overlay 回归通过；该版 PID 53115 已由上条修复版替换，切换备份 `~/Library/Application Support/notion-manager/backups/pre-tool-allowlist-t7q_0y5x`。真实 Codex 补验未触发读/命令/修改工具，单只读复测仍无工具事件且上次错误未复现；Claude 补验 120 秒超时，工具回传与多轮未验证；不得宣称工具链通过。详情与备份见验收记录，源码未提交。
- 2026-11-08 上游核对：原作者 origin/master 实时 HEAD 为 3ce40ea（2026-08-05），已是本地 528bc99 的祖先，本地额外 8 个提交；当时无新上游提交。社区候选包括 yinyue111/Notion2api、Youahui/notion2api、GALIAIS/Notion2API 与 maverickxone/notion2api；仅基于仓库资料调研，未运行或导入账号。
- 2026-11-08 从 MCP 积分看板导入六个账号；各账号此前以单账号隔离实例调用 GPT-6 Luna 均成功，未验证所有模型或共享池路由。旧版 Manager 使用 Basic/Premium 和过时的 window 解析导致额度差异，已由上述新版修复显示；旧指标仍为兼容数据。
- 2026-10-19概览页顶部运行状态条已按用户要求实现并上线：新增登录态只读接口`/admin/system`（服务自身uptime/PID/内存/Go版本/监听地址、账号池各状态计数与最近额度检查、请求日志存储覆盖、LaunchAgent托管状态）与前端`SystemStatusBar`，四类信息默认一行摘要、展开看详情，10秒轮询。Go完整测试/vet、新增handler测试、15项Node及tsc/构建通过；线上`index.html`已引用`index-zY01mynU.js`且该JS含`/admin/system`。运行二进制`d82b36d704c808dc9635f658baf43351bafe6fa61da475d7326422e69dea5da4`，PID 82383，health/dashboard均200，未登录访问`/admin/system`为401。备份`backups/pre-system-status.20261019-214843`含旧二进制、plist和旧hash。服务重启使dashboard会话失效，需重新登录验收。已提交`21b6ed0`（扩展1.3.2身份修复）与`528bc99`（状态条+额度错误分类）并推送github/master，远程SHA与本地一致；Handoff与docs本机记录未提交。
- 2026-10-19扩展身份不匹配已按用户要求修复：loadUserContent与getAvailableModels补当前active-user头，保留防串号校验，版本1.3.2。已更新Chrome实际加载目录和本机服务下载包；Chrome扩展管理页被浏览器安全策略禁止访问，未绕过，需要用户手动点一次“重新加载”。修复后的完整读取函数在当前Chrome登录态实测成功：身份匹配、2工作区/1正式Business、36模型。尚未重载后的弹窗验收或执行账号导入。
- 2026-10-19额度错误提示已按用户要求修正、备份并更新本机8081；未提交/推送本次改动。401、403、429、网络/超时、无效响应有独立安全分类和中英文说明，实际账号页已显示401重新导入指引。配置、路由、身份及凭据保持不变，3成员/1可用/2暂停。
- 前序额度异常只读诊断：旧成员ID前缀`9d4a9604`三额度接口均401，token-only基础身份接口亦401；正常成员`586b9159`同环境额度/身份接口均200。池内旧登录凭据当前不被接受，具体到期/撤销等原因不能仅凭401确定。恢复需重新导入有效登录态并核验原工作区及正式Business，本次提示修正不恢复凭据。
- 2026-10-19开发修复已集成并通过完整Go/race/vet、23项Node、前端/二进制构建、隔离浏览器验收及最终综合复审。已按用户授权提交、推送并替换8081服务；真实Read工具冒烟未达预期，完整开发工具链尚未验收通过。计划及证据：`docs/plans/2026-10-19-developer-experience.md`。
- 已按“线上实施升级计划”和“一起修复”授权，在本机现有8081服务上线六页后台、动态账号池、请求日志/分析及401/额度语义修正；等待用户实际客户端验收，不代表上游长期稳定性已验证。
- 主计划：`docs/plans/2026-10-18-dashboard-upgrade.md`；本次同版本上线证据、范围与未验收项：`docs/2026-10-18-dashboard-upgrade-acceptance.md`。
- 用户已进一步要求中文README和公开仓库：`https://github.com/davidwuwu001/notion_manager`现为PUBLIC，默认中文README已推送，匿名读取仓库及README均200，远程SHA与本地一致。原作者origin保留只读，新仓库remote名为github，master跟踪github/master。
- 账号按N个平等成员调度，不是主备；10月18日上线时3成员、2可用、1旧失效成员暂停。10月19日只读审计所见配置为3成员、2暂停，本轮未改变启停。历史成员仍可能legacy_unverified，未批量改写订阅身份。

## 已完成的事项

- 运行状态条实现：后端`internal/proxy/system_status.go`复用`RoutingSnapshot`与`RequestLogStore.Coverage`；LaunchAgent以父进程是否为launchd（PID 1）为主信号，再按可执行文件匹配`~/Library/LaunchAgents/*.plist`取Label，执行`launchctl print gui/<uid>/<label>`读取state和last exit code（2秒超时，失败只降级为unknown，不阻塞接口）。前端`components/SystemStatusBar.tsx`挂在概览页标题下，`types.ts`/`api.ts`/中英文案同步；`system_status_test.go`覆盖会话鉴权、字段结构与nil存储。构建后重建并同步`internal/web/dist`，旧资源目录已清理；回滚只需恢复备份二进制。
- 扩展修复25项Node（含多账号默认身份错误及错用户仍拒绝）、Go全套、语法/差异检查通过；测试先复现失败再修正，JS和综合审查无实质问题。已安装目录`/Users/Zhuanz/Library/Application Support/notion-manager/chrome-extension`的popup.js与源码、HTTP扩展zip同hash `24c2fa0f5964b5119e38e0e95a34054169a73dac19f90d16c1003ce0a75edfad`；下载manifest 1.3.2。新运行二进制hash `1a61a13eb095a203aab818ae1d3be4bef906e3588699cc18fbf4f5aa616ced49`，8081健康，3账号/1可用；备份`/Users/Zhuanz/Library/Application Support/notion-manager/backups/pre-extension-1.3.2.XLSeG0`含旧插件、二进制、配置和账号/数据。本次未提交推送，未改凭据/启停/订阅规则，未进行推理。
- 额度提示修正：Go全套、proxy/cmd race、vet、15项Node及tsc/Vite通过；错误分类回归先失败后通过，覆盖HTTP错误/无效JSON/字段缺失/响应头前及响应体读取超时和网络中断，并验证不暴露原始响应。Go/TS/综合审查完成，body超时误判已修复复核。
- 本次提示版运行hash `358a18e00dc40eb5dc1706cbe9265022b6f95712484607e6dd26e926ab0c9583`；embed `index-DyaryGhy.js`与实际HTTP内容一致。备份`/Users/Zhuanz/Library/Application Support/notion-manager/backups/pre-quota-hints.qkajMQ`含旧二进制、配置、账号/数据和plist。内置浏览器真实账号页已核验401说明及原暂停状态，截图`test-evidence/quota-authentication-hint-live.png`。未进行推理测试，未恢复失效账号；此版本尚未提交推送。
- 正式提交`7f44b01f2acb2358316d8c6038f55e44fdf233d9`（45文件）已推送`github/master`并经gh读回一致；未向原作者origin写入。暂存扫描无已知私密值命中，运行数据及本机交接文档未提交。
- 从该提交的干净git archive构建并替换项目及实际运行二进制`/Users/Zhuanz/Library/Application Support/notion-manager/notion-manager`，SHA256 `c338fecd74acf5cfbbbf41d4c4efc6bbf51b64c6932bf035e1a4a09b90597350`。LaunchAgent `gui/502/com.zhuanz.notion-manager`运行，仅监听127.0.0.1:8081。升级前备份：`/Users/Zhuanz/Library/Application Support/notion-manager/backups/pre-7f44b01.GjJjqQ`，含旧二进制、配置、账号/SQLite及plist；备份库quick_check通过。回滚优先仅恢复二进制，不覆盖后续运行数据。
- 正式服务读回：配置/路由及账号身份凭据保持；health正常，3成员/1可用/2暂停；原API Key访问capabilities和36个模型成功，无Key返回401；运行JS与提交内embed一致。内置浏览器原密码登录与概览/账号页通过，截图`test-evidence/developer-experience-live-overview.png`；升级前12条日志全部保留。
- 2026-10-19：并行完成完整上下文/schema/工具历史、显式会话隔离、分源额度缓存和singleflight、负载感知选号及故障时序保护、统一请求ID/阶段耗时、元信息日志/文件轮转、页面可见轮询、工具增量事件/三协议用量、参数能力声明；来源和限制更新README与Claude桥接说明。Go/TS专项复审已补缺陷，最终综合复审通过。
- 本轮同版本验证：`go test ./... -count=1`、`go test -race ./internal/proxy ./cmd/notion-manager -count=1`、`go vet ./...`、23项Node测试、tsc/Vite、CGO=0构建及差异检查通过。隔离内置浏览器验证分源缓存告警、自动日志更新/阶段详情、5成员可用口径及设置草稿；fixture不使用真实凭据。
- 历史开发预览产物`test-evidence/notion-manager-developer-preview`，SHA256 `c2fc5ad59cdc6c9cb2dd11c9f502d721e218d767e959108aa86b7a298184a88c`，不是正式发布hash；新embed `index-Da3B_oy0.js`/`index-BW85FVG8.css`。截图`test-evidence/developer-experience-log-detail.png`仅fixture证据，正式8081证据见上。
- 实施前固定本地基线`148ab51a043ac60f32430fb9289766b84f2d45d2`与上游参考提交`ed467fcc9ecf6d0d8b56dcf729e626227fbee675`；前序未提交成果先保护，随后按用户提交授权一并纳入本次相关代码提交。
- Go引擎保留，新增概览、日志、分析、集中设置，保留账号/工作区和模型/接入页；AccountID会话绑定、quota/round_robin、成员暂停、并发/重试边界与SQLite持久日志已接线。来源声明见`docs/UPSTREAM-NOTICE.md`。
- Premium明确0不再被推算覆盖，缺失/null/旧缓存不冒充确认余额，失败保留最后成功检查时间；Business不因旧Basic/Premium字段被永久判为免费耗尽。
- 本轮重新执行通过：`go test ./...`、`go test -race ./internal/proxy ./cmd/notion-manager`、`go vet ./...`、21项Node前端/扩展测试、前端构建、五平台CGO=0构建和差异检查；异平台未实际运行。
- 新产物`index-DPvkDcqR.js`已embed并重建上线，运行二进制与仓库根二进制SHA256一致；监听仍仅127.0.0.1:8081，配置/密码/API Key未替换。
- 最新三次真实调用全部命中新导入Business成员：Messages非流式、Chat流式、Responses流式均200、有内容、有正常终态；恰好新增3条成功日志。新库重启后保留先前4条记录，当前7条中6成功、1先前empty_response失败，未删除或伪造成功率。
- Codex内置浏览器走查六页；模型ID/三种端点复制、日志ID筛选/详情/尝试轨迹、分析统计、策略原值保存读回及退出/原密码重新登录通过。截图位于忽略目录`test-evidence/dashboard-upgrade-overview.png`。
- 已保留升级前源码/二进制备份及最终发布前二进制备份，详见验收记录。
- 代码提交：`f618539236a9198c22b6c8b1292c7dc0e88ee4d0`，`feat(dashboard): add analytics and dynamic Business account pool`，master分支、84文件。提交前完整Go测试、21项Node测试、暂存差异检查和凭据规则扫描通过。账号/密钥/运行数据、Handoff与本机事故/上线/计划记录未纳入提交，原文保留本地；工作区仍有这4类未跟踪文档。
- 来源文档提交：`034bdec96ef1d9bfc433b16938f7fcba209cee98`，中英文README和UPSTREAM-NOTICE注明原作者SleepingBag945及上游贡献者、原项目链接、本分支改动及原CC BY-NC-SA 4.0许可。通过`gh repo create --private --source=. --remote=github --push`推送全部本地master提交，未改写上游历史。远程权限ADMIN、private=true、分支SHA、三份来源文档、LICENSE blob与上游祖先关系均读回核验；远程树无账号/密钥/日志库/截图。推送前397个历史文本对象的强凭据规则扫描无命中；不等同于专业完整审计。
- 中文首页提交`20f3b881714490c1810fc91e9a8eb56cb720b858`：重写README，按上游基线3ce40ea区分继承/新增能力，补更新记录、导入/调度/三协议/构建/安全与限制；README_CN改为单一主文档入口。11个本地链接、代码围栏、GitHub Markdown渲染和差异检查通过。本轮未改业务代码或运行服务，未重复推理测试。
- 公开前复核410个历史文本对象、唯一远程分支和14个本机已知敏感值，无规则/实际值匹配，未上传运行文件；图片与已公开上游完全一致，Actions历史为空。已按用户明确授权使用gh改为公开，并匿名验证仓库public、README与本地一致及远程HEAD一致；这不是全面安全审计或新的二进制发布。

## 当前遇到的卡点问题

- 额度错误展示信息损失已修复并上线；旧缓存的通用错误安全回退，不追溯猜测原失败原因，下一次刷新才获得具体分类。失效成员仍需有效登录态，提示更新不能解决其上游401。
- 正式服务只读工具冒烟：sonnet-4.6经/v1/messages流式要求Read(README.md)，请求`req_ee215642-2c89-43fc-b14d-1c6738d5a121`返回200/正常终态、约5812ms，但tool_calls为空；未执行本地工具。tool_choice没有服务端强制保证，响应原文未留存，具体模型输出待后续定位。该请求按协议正常记success，不等于工具业务成功；库现13条（12成功/1失败）。未因此改代码或回滚。
- 本轮开发无已知测试阻塞；无明确session ID时重传完整历史、模糊工具格式缓冲、Notion上游协议与模型工具遵循率仍有真实使用限制。尚未验证长会话/真实耗尽，不应宣称等同原生直连。
- 无当前部署阻塞。旧401成员未恢复凭据，保持手动暂停；若需重新使用，须重新导入其有效登录态并核验对应工作区，不能直接启用碰运气。事故文档已补后续处置。
- 远程推送无阻塞：新仓库已按用户明确授权公开，原作者仓库仍只有读取权限且未被修改。未发布tag/Release；公开不改变原许可证的非商用等要求。
- 自动化覆盖多成员及错误/取消/安全重放，真实池仅3成员；未做真实额度耗尽、长时间压力、所有模型、跨工作区写入工具测试。保守禁止不安全透明重放。
- 用户建议的三个模型不在原生subagent可选列表；本机DevSpace也没有agents命令。本轮已告知并使用宿主当前可用原生代理，未使用指定三种模型、未安装/改模型配置。
- 既有调试日志设置保持原值，新增结构化请求日志不存正文/凭据；不能将新日志隐私保证泛化为旧调试日志也不含敏感内容。

## 后续的计划安排

1. 用户重新登录`http://127.0.0.1:8081/dashboard/#overview`后，核对运行状态条四组信息与实际一致（重点看LaunchAgent托管/上次退出码、最近额度检查时间）；代码已推送github/master，验收不通过再考虑回滚或修正。
1. 用户在Chrome扩展页手动重新加载现有1.3.2扩展后，验证弹窗读取与所选Business工作区导入。不要删除身份校验或随意选首个返回用户；不通过其他工具绕过浏览器对扩展管理页的安全阻止。
1. 后续定位真实Read工具请求未生成调用的问题，区分模型遵循率、文本桥接和tool_choice语义；未验收前不宣称等同原生开发工具链，不盲目重复推理消耗额度。
2. 使用现有Cherry Studio/Claude Code/Codex进行真实开发任务验收；遇错先按X-Request-ID查看协议/账号/阶段/终态，不盲目重放有副作用的请求。
3. 后续添加账号继续核验正式非试用Business；历史成员重新导入成功后才能更新其核验标记。
4. 后续修改获提交授权后正常推送到github/master；origin仍为原作者地址，不要向其写入。仓库已公开，继续排除本机运行记录并检查历史暴露；tag/Release属于另行操作。

## 过往踩过的坑（避免重复犯错）

- Notion旧 `plan_type=team` 不能证明正式Business；Premium余额也不是计划身份。核验实际账单，未知不伪装为确认。
- 同邮箱同名工作区必须按用户+工作区的稳定AccountID区分；导入按钮需要传所选space_id，不能重新自动选择。
- “一个账号用完换另一个”是N账号池的故障转移示例，不能据此设计成固定一主一备或固定账号顺序；以主计划第7.2节为准。
- OpenAI入口内部调用Anthropic处理器，日志不能在内外层各计一次；HTTP200、nil error和Token非零都不能单独证明请求成功。
- 迁移上游页面不等于迁移了准确统计；上游缓存流、词数估Token、失败记成功与分位数口径不得照搬。
- Go embed更新需要实际重建运行二进制；磁盘前端文件变更不等于服务面板已更新。旧运行数据与新日志库均需保留回滚。

# 管理后台升级：本机上线与验收记录

日期：2026-10-18（Asia/Shanghai），最终页面及原密码重新登录读回约21:58。

结论：首版已在现有本机服务上线；自动化、内置浏览器主流程和三协议真实连通检查通过。未提交/推送，未做真实额度耗尽、全模型或长时间业务验收。原[升级计划](plans/2026-10-18-dashboard-upgrade.md)仍是业务规则主源，本文件记录实际交付边界。

## 1. 已上线范围

- 保留Go引擎和现有账号文件、配置、管理密码、API Key及历史Token汇总。
- 六页React后台：概览、账号与工作区、模型与接入、请求日志、数据分析、系统设置。
- 平等N成员池：余量优先/全池轮询、暂停/启用、稳定AccountID、并发占位、尝试预算、故障分类与安全重放边界；没有主备角色。
- SQLite逐请求/逐尝试记录、筛选分页、元信息详情、趋势/分布/P50/P95；历史Token汇总不补造为逐请求数据。
- Business新增门槛和工作区区分；存量未核验账号不伪装成已核验或批量删除。
- Premium明确0保留、未知为空、检查失败保留最后已知值/时间；旧Basic/Premium不直接替代滚动/月度套餐状态。
- 未登录HTML不注入API Key；管理模型与连接凭据通过受保护管理接口读取。

## 2. 精确运行目标和产物

- 入口：`http://127.0.0.1:8081/dashboard/`，监听仅IPv4 loopback。
- 源码：`/Users/Zhuanz/Documents/project/notion_manager`；Git HEAD仍为`148ab51a043ac60f32430fb9289766b84f2d45d2`，大量前序/本次变更保留未提交。
- 运行目录：`/Users/Zhuanz/Library/Application Support/notion-manager`。
- 现有LaunchAgent：`com.zhuanz.notion-manager`；最终重启后PID为15911（PID会随重启变化）。
- 前端embed：`index-DPvkDcqR.js` / `index-kThuBD5Y.css`，已从实际8081返回的HTML读回；不是仅Vite产物。
- 运行二进制及源码根目录二进制SHA256均为`09b6041e76e9a0a3dc23d1d5211a21759cc1252e124a210421f0c6baf5d28b64`。
- 新库：运行目录`accounts/.request_logs.sqlite`，现场权限0600；账户/日志库在忽略范围，不进入版本库。
- 许可/来源处理见[UPSTREAM-NOTICE.md](UPSTREAM-NOTICE.md)：产品能力借鉴，不直接复制对方代码/资产，不改变本项目现有许可。

## 3. 本轮自动化证据

以下命令均在最终部署源码上执行通过：

- `go test ./...`。
- `go test -race ./internal/proxy ./cmd/notion-manager`。
- `go vet ./...`。
- `node --test web/src/*.test.mjs chrome-extension/*.test.cjs`：21通过，0失败。
- `npm --prefix web run build`：TypeScript和Vite构建通过；复制embed后`CGO_ENABLED=0 go build`通过。
- `git diff --check`通过。
- 最终源码`CGO_ENABLED=0`五平台构建通过：darwin/amd64、darwin/arm64、linux/amd64、linux/arm64、windows/amd64；只有本机macOS实际运行验收，不将交叉编译当作异平台运行证明。

自动化包含1/2/5/8成员池、动态成员/并发/重导、配额重置复核、同邮箱多工作区、SQLite重启/中断/保留区间/不可用、累计usage去重、三协议stream/non-stream、取消/部分输出/错误/多轮/安全切号。新增18个工作区搜索全局设置/header组合场景防止配置与重放判断不一致。它们是可控fixture，不是真实账号耗尽或跨工作区写入证明。

## 4. 真实三协议连通

新导入账号在管理接口读回为`verified_business`。测试使用`haiku-4.5-ask`、关闭网页和工作区搜索、只问“Reply with OK only.”；未编辑用户页面或上传文件。三次均命中新Business成员（AccountID前缀`586b9159`）。

| 接口 | 模式 | HTTP/内容/终态 | 请求日志 |
|---|---|---|---|
| `/v1/messages` | 非流式 | 200、有内容、正常结束 | success，1次尝试 |
| `/v1/chat/completions` | 流式 | 200、有内容、正常结束，无错误事件 | success，1次尝试 |
| `/v1/responses` | 流式 | 200、有内容、正常结束，无错误事件 | success，1次尝试 |

本轮日志数由4增至7，恰好新增3条成功记录。重启前4条保留，其中1条先前的`empty_response`失败未被删除或改为成功。最终分析读回7请求、6成功、1失败、85.7%成功率、0进行中，存储状态`ok`。这不代表每个模型都验证可用。

## 5. 实际浏览器与账号池读回

主面板用Codex内置浏览器，Chrome登录态使用用户明确指定的browser-harness且复用现有连接；不新开重复标签、不将凭据打印到输出。

- 六页切换及真实数据加载正常；概览与账号页可用数均来自routing。
- 当前3成员：2可用、1手动暂停；新导入1个已核验Business，另1个可用历史成员仍为`legacy_unverified`。
- 旧失效成员（ID前缀`9d4a9604`）保留但不参与调度；其旧Premium余额显示未知。若需恢复必须重导有效登录态。
- 36个公开模型；模型ID及Chat/Messages/Responses完整请求地址逐项点击，均显示成功复制提示，API Key保持隐藏。
- 按新请求ID筛选由7条变为1条；详情显示对应协议、AccountID、1次尝试和成功终态，不显示对话正文。
- 分析显示真实模型/账号分布及延迟；策略页保留原值保存后显示“已保存并读回有效配置”。
- 无管理Cookie访问`/admin/request-logs`为401；实际返回HTML不含API Key元标签。
- 内置浏览器实际退出后显示登录页，以原管理密码重新登录成功并读回概览；未改管理密码/API Key。现场将配置文件与首次升级前备份比较，内容一致。
- 本地截图：`test-evidence/dashboard-upgrade-overview.png`，已忽略，不提交。

## 6. 恢复与未覆盖项

- 首次升级前备份：运行目录`release-backup-20261018-lAIISK`（旧二进制/当时配置及账号备份）；源码基线另在`source-baseline-20261018-f6tgiV`。
- 最终更新前二进制备份：运行目录`release-final-backup-20261018-s4XAc7/notion-manager`。该目录不是日志数据库的一致备份。
- 回滚优先仅恢复对应二进制并通过原LaunchAgent启动；保留新账号、新库和当前配置，不用旧账号压缩包覆盖新导入成员。不单独复制运行中的SQLite主文件当一致备份。
- 未做真实耗尽、压力/长期运行、全部36模型、跨工作区写入工具、Linux/Windows实际运行和GitHub远端CI验收；真实池仅3成员，多成员行为由fixture覆盖。
- 旧401成员的凭据未恢复；最初失效的具体上游原因尚未证明。当前为隔离止损和有效Business成员恢复服务，不是自动修复Notion登录。
- 既有调试日志设置未改，新结构化请求库的隐私保证不代表旧调试日志也无敏感内容；不得上传原始日志/运行数据。
- 没有重装/重载Chrome扩展，也没有替用户在所有外部客户端执行完整业务回归；模型ID复制与实际调用是不同验证层。

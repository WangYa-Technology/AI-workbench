# 工作台「资产」完整审查

> 后续修复状态：A01–A09 与 R01 已完成修复验收，见[修复与最终验收文档](workspace-assets-fix-acceptance-20260927.md)。以下保留原始审查时点的结论和失败证据。

- 日期：2026-09-27（Asia/Shanghai）。范围：用户标注的 `/workspace/assets` 主区域及其直接操作链。
- 基线：HEAD `aa4c6125e7a26d80870e217bb9fec9004df5bdd8` **加当前工作区未提交改动**；不是干净发布提交。真实浏览器测试另保存完整源码 SHA-256 清单。
- 结论：**主要流程可用，但不建议无条件验收。** 发现 1 项 P1、8 项 P2；另有扫描并发恢复测试首次失败的未关闭风险。本轮没有证明可越权读取他人私有资产，也不等于给出无漏洞保证。
- 本次仅交付审查和证据，没有修改应用业务代码、数据库迁移或开发管理员的资产。真实写入测试使用专用隔离数据库、媒体目录与本地 Provider；没有外部发布、测试邮件、生产资金或生产数据库操作。

## 1. 审查范围与方法

| 维度 | 覆盖内容 | 方法与边界 |
| --- | --- | --- |
| 信息架构 | 页头、资产/收藏计数、列表/网格、空态、卡片、授权文案 | 内置浏览器实查、组件源码、独立模拟 API 场景 |
| 用户操作 | 上传→扫描→详情；上传新版本；发布抽屉/草稿；收藏/取消收藏；使用关系 | 7 条隔离真实后端 Playwright 流程，包含两条移动端用例 |
| 身份和授权 | 本人、他人、匿名、管理员、购买/交付来源 | Go/HTTP 既有测试及服务端源码；管理员不天然拥有其他用户的私人资产详情权限 |
| 文件安全 | 10 MiB 限制、MIME 检测、对象键、pending/review/rejected 禁用、扫描失败 | assets/media 全包测试及上传、扫描、内容服务源码 |
| 并发和恢复 | 幂等上传、未知提交结果、扫描租约、资产版本锁、页面切换晚响应 | Go 测试、10 条既有 UI 交接测试；扫描恢复存在一次失败，见 R01 |
| 隐私和输入 | 私有提示词、收藏/用途数据、缓存头、SQL 参数、游标、标题长度 | 源码审查、HTTP 测试、独立 Unicode/引用关系用例 |
| 可用性 | 桌面、390px、键盘入口、状态更新、失败重试 | 当前内置浏览器及自动化；没有完成完整读屏器、跨浏览器和 WCAG 认证 |
| 运维边界 | worker 扫描恢复、实际扫描器、测试隔离与清理、CI | 代码及本轮证据；没有重新验收真实 S3/恶意文件扫描服务或生产容量 |

直接审查文件包括 `WorkspacePage.vue`、`AssetPublishDrawer.vue`、`AssetMedia.vue`、`contentPresentation.ts`、上传命令/会话边界、`internal/assets/*`、媒体存储/扫描边界、HTTP `vertical.go`/注册、相关测试和迁移。未逐行重审全部支付、任务及社区子系统；这些仅覆盖资产授权、引用和发布接口边界。

## 2. 问题清单

P1：在对外使用前应修复。P2：影响准确性、可用性或防护完整性，应排入修复并明确验收。以下证据级别区分“实际复现”和“源码确认”；问题编号对应证据脚本，不将测试通过解释为缺陷已经修好。

### A01 · P1 · 发布抽屉对非本地资产默认填入未经证实的来源/费用声明

**实际复现。** 当前资产详情显示 `openai / gpt-image-2`；点击“发布资产”后，AI 披露默认是“由确定性的本地测试 Provider 创建；未发生任何付费模型调用”。该声明没有从资产来源或计费事实派生，用户不修改即可带入发布请求。这里不能反向推断该历史资产一定收费；问题是默认文本作出了没有证据支持的绝对声明。

- 源码：`web/src/components/domain/AssetPublishDrawer.vue:58` 的 `resetForm`，第 65 行统一赋值 `publish.defaultDisclosure`；第 160 行写入 payload；`web/src/i18n/messages/zh-CN.ts:891`。
- 证据：[资产详情](../data/assets-review-20260927/asset-detail.png)、[错误默认披露](../data/assets-review-20260927/disclosure-mismatch.png)。没有提交当前开发资产的发布。
- 建议：按已核验 provenance 填模型/来源；未知费用不得声称“未付费”；普通上传要求用户填写来源。缺少依据时留空并要求确认，比默认虚构声明更合适。
- 验收：local_test、OpenAI、用户上传、历史未知来源四类各有测试；保存草稿和最终发布保存的披露与确认内容一致。

### A02 · P2 · 总数与已加载数量混用，分页时低估资产/收藏/安全数量

**自有资产实际复现；收藏与安全计数源码确认。** API 总计 21 项、第一页 20 项时，主标题/结果数为 21，而页头“自有资产”为 20。收藏使用当前页 `savedWorks.length`，安全使用当前页 `cleanAssetCount`，未说明是已加载范围。继续加载会改变这些看似总量的统计。

- 源码：`WorkspacePage.vue:139`、`:219`、`:234`；服务端 `assets/service.go:555` 默认页长 20，提供资产 total；收藏没有 total。
- 复现：`assets-findings.spec.ts` 的 `AUDIT A02`，截图与 trace 位于审查证据目录。
- 建议：总量统一使用服务端聚合；无总量字段明确标为“已加载 N 项”，安全指标同样标明范围。
- 验收：0、1、20、21、41 项及混合扫描状态，加载前后计数口径稳定。

### A03 · P2 · 上传后插入新卡片，却没有更新资产总数

**实际复现。** 已有 1 项时上传成功，出现 2 张卡片，但结果区仍显示 `1 Assets`，直至重新读取列表。

- 源码：`WorkspacePage.vue:924` 的 `uploadAsset` 只更新 `assets`，未更新 `assetTotal`。
- 复现：`AUDIT A03`，独立 API fixture，无开发库写入。
- 建议：上传确认后重取第一页/总数，或在确认是新资产且不是幂等重放时更新总数；避免重试多加计数。
- 验收：普通成功、未知结果后同键重试、列表已有同 ID、空列表上传均正确。

### A04 · P2 · 收藏读取失败会使成功读取的自有资产不可见

**实际复现。** `/assets` 成功返回 1 项，`/assets/saved-works` 返回 503，整个资产结果区只显示错误，自有资产卡片消失。

- 源码：`WorkspacePage.vue:599` 对两个请求使用 `Promise.all`；`:1323` 左右用共享 error 替代整个列表。
- 复现：`AUDIT A04`。界面会将模拟错误规范化为通用服务暂不可用文案，这是正常行为，并非额外缺陷。
- 建议：按当前标签加载必要数据，或独立保留各区域的结果、错误、重试状态。
- 验收：任一 API 失败不会遮挡另一标签已成功的数据；重试不清除无关结果。

### A05 · P2 · 扫描状态在详情页不会主动更新，列表轮询也会静默终止

**详情实际复现；列表超时源码确认。** 新版本上传后立即跳转详情，如果首次读取为 pending，即使后端稍后变成 clean，详情仍一直显示扫描中。用例让第二次读取即可返回 clean，推进 30 秒后实际仍只有一次读取。

- 源码：`WorkspacePage.vue:588` 详情分支只读一次；`:898` 上传版本后跳转；`:945` 上传列表轮询仅 20×750ms，单次异常直接 return，耗尽后没有新的状态提示。
- 复现：`AUDIT A05` 使用虚拟时钟；既有正常扫描速度较快的 E2E 通过不能覆盖该场景。
- 建议：详情 pending 有可取消、有预算的状态刷新；耗尽后显示“扫描仍在进行/稍后刷新”和可见刷新按钮；错误不能表现成永远进行中。
- 验收：立即完成、超过 15 秒、短暂读失败、最终 review/rejected、切换账号/页面均有测试，离开页面停止轮询。

### A06 · P2 · 中文标题的前后端长度契约不一致

**实际复现。** 前端允许 3–120 字符，后端使用 Go `len(string)` 限制 3–120 **字节**。41 个汉字是 123 UTF-8 字节，在前端长度范围内仍被后端拒绝；文件名、版本说明也应检查同类口径。

- 源码：`WorkspacePage.vue:1290` 附近的 `minlength/maxlength`；`internal/assets/service.go:253`。
- 复现：`TestAuditUnicodeTitleByteLimit`，独立数据库，返回 `assets.ErrInvalid`。
- 建议：确定产品按 Unicode 码点还是其他字符单位计数，并在前后端统一；另设合理字节上限作为资源防护。
- 验收：ASCII、汉字、emoji、组合字符、临界 120/121；上传、版本、文件名分别覆盖。

### A07 · P2 · 使用关系漏掉非首张参考图，可能错误显示“尚未用于其他工作流”

**第二参考图实际复现。** 一个生成任务同时引用两张资产，首张通过 `source_asset_id` 关联，第二张仅在 `generation_reference_assets`。第一张有 usage，第二张返回空数组。

- 源码：`internal/assets/service.go:757`–`:785` 的 usage UNION 只联结 `generations.source_asset_id` 和 `deliveries.asset_id`；真实多参考图读取见 `internal/creation/service.go:912`。
- 复现：`TestAuditSecondaryReferenceMissingFromUsage`。测试资产用满足当前约束的合成 delivery 来源，引用关系本身为真实迁移表；不代表调用了实际 AI 模型。
- 同类源码缺口：mask_asset_id、交付附加文件 `delivery_assets` 未包含，尚未逐一动态复现。
- 建议：加入实际关系表与蒙版/附加文件关联；同一资源重复引用需去重并保持游标稳定。
- 验收：首/第二参考图、蒙版、交付主/附文件、跨版本；继续保证其他用户的私有工作流不可见。

### A08 · P2 · 授权文案引导查看详情，但详情仍没有可操作的具体条款

**当前资产实际复现、映射源码确认。** `creator-owned` 显示“查看详细授权条款”；进入详情后“授权”仍是同一段非链接文案，没有该资产对应的条款/权利范围。页面底部通用授权政策不能自动代替某资产的具体许可。

- 源码：`web/src/lib/contentPresentation.ts:7` 仅识别少数旧代码，其余 fallback 为 review；`WorkspacePage.vue` 来源证明卡片使用相同映射。
- 证据：资产列表及详情截图。购买资产若已有冻结订单条款，应继续显示真实合同，不应套用该 fallback。
- 建议：统一 license code 展示映射；未知许可明确显示“授权待核验”，已知许可提供名称、范围和可访问的对应条款。
- 验收：生成自有、上传 personal、购买新旧许可、任务合同、未知代码各有展示与导航用例。

### A09 · P2 · 私有详情、用途和收藏 JSON 没有统一禁止缓存

**源码确认，未演示缓存泄漏。** `listAssets` 显式设置 `private, no-store`，但 `getAsset`、`listAssetUsages`、`listSavedWorks` 未设置；共同 JSON helper、requireUser 和通用 middleware 也未补齐。详情可包含私人提示词、用途和订单来源。部署 nginx 的 no-store 位于 `location /`，不覆盖独立 `location /api/`。

- 源码：`internal/transport/httpapi/vertical.go:594`、`:615`、`:711`；`internal/platform/httputil/response.go:19`；`deploy/nginx.conf:118`。
- 影响：缺少明确禁止浏览器/中间层留存的契约；不能直接据此宣称已经发生跨用户共享缓存泄漏。
- 建议：私有资产 JSON 和相应错误统一 `Cache-Control: private, no-store`，增加 header 合约测试，包含反向代理部署路径。
- 验收：三个私有 GET、认证失败、拒绝访问、账户切换；不要为了修复私有 JSON 无差别破坏已授权公开媒体的策略。

## 3. 尚未关闭的测试风险

**R01：扫描并发恢复存在一次非全绿结果。**

首次完整运行 `internal/assets`：39 个顶层测试通过、1 个顶层测试失败，失败子场景为 `TestScanExecutionCrashRecovery/concurrent_recovery`，报 `reconcile: 0 expected 1 err=<nil>`。定向 `-count=3` 随后全部通过。

保留初次日志，不用后续通过覆盖。该证据说明并发恢复的单轮进展断言存在不稳定性；尚不足以判断是测试调度假设、短暂锁竞争，还是业务恢复缺陷，更不能推断永久卡死。本轮未更改测试断言或业务恢复逻辑。建议建立确定性锁屏障，记录 recovery_after、job attempt、锁占用，验证最终有界恢复及审计/通知不重复。

## 4. 已确认的保护与能力

- 资产列表以 owner_id 限定；详情和 usage 先验证 owner；普通管理员身份不是资产私有读取的绕过条件。列表对被撤销购买权益做过滤。
- 上传/版本需要 `assets:upload`；账户生命周期锁和 active 状态复核在实际写入前执行。购买副本和任务合同来源的版本上传被禁止。
- HTTP 要求有效幂等键；请求摘要绑定文件与字段；持久命令/对象写入意图处理响应丢失和不确定提交。前端同内容重试和账号切换边界有测试。
- 新版本使用资产族锁分配版本号；旧版本保留；列表只显示族中最新版本。旧版可访问不意味着最新扫描失败会自动回退列表，需要明确产品策略。
- 文件大小有服务端 10 MiB 上限及 multipart 总体预算，MIME 根据内容检测，存储对象键由服务端生成；不是相信用户文件扩展名。存储/扫描边界、越界路径、流预算等由 media 测试覆盖。
- pending/review/rejected 不提供正常内容；内容解析、Stat/Open 再授权；购买文件只按冻结 manifest 的合法索引访问，不能传任意对象键。内容返回 private/no-store 与 nosniff。
- 扫描任务绑定 job/attempt/lease 证据，失败进入重试或 review；不接受未知扫描状态作为 clean。实际病毒检出率不在本轮证明范围。
- 收藏界面明确“收藏不等于获得授权”，服务端依公开可见性与 clean 状态返回；取消收藏没有授予资产所有权。
- Vue 插值展示标题/提示词，文本预览限制读取字节并有取消/超时；未发现本范围需要 v-html 执行不可信资产文本的路径。
- SQL 主要值通过参数绑定；资产游标绑定 owner/source/purpose，限制页长。收藏/用途游标主要作为位置，不是授权凭证，仍由查询条件确定可见范围。

上述是本轮源码和测试所能支持的结论，非完整渗透测试、供应链审计或正式安全认证。

## 5. 页面体验、可访问性及性能

- 桌面：页头、来源/扫描标签、版本和详情入口可见；当前 1 项数据状态正确。页头与结果区重复描述，信息较稀疏，属 P3 优化。
- 移动端：390px 实测 clientWidth=scrollWidth=390；独立真实 E2E 的上传面板和发布抽屉用例通过。内置浏览器全页截图存在缩放呈现差异，以真实 Playwright trace 为复核依据，不据此判定网站 CSS 缺陷。
- 键盘：本轮通过 Enter 操作标签/详情/抽屉；UiTabs 支持方向键/Home/End、roving tabindex。但没有关联的 tabpanel/aria-controls，应补齐语义关联；未做读屏器和全页色彩对比测试。
- 大数据：支持游标加载更多、API 20 默认/50 上限，但资产列表没有本区域的标题搜索、来源/媒体/扫描筛选，页面随加载增长，没有虚拟化。这是容量与可发现性改进，不将小数据测试当作大库性能验收。
- 媒体：图像 lazy，文本有界预览；列表视频在 controls=false 时自动循环播放，多个视频同时出现的流量/CPU 和减少动态效果体验需要单独测量。
- “安全”仅对应 scanStatus=clean，不代表版权、商业授权或内容合法性保证。生产用语建议为“扫描通过”；实际启用扫描器也应避免总显示“确定性本地扫描”。

## 6. 实际执行与证据

统一证据目录：[data/assets-review-20260927](../data/assets-review-20260927/)。

| 验证 | 本轮结果 | 证据 |
| --- | --- | --- |
| assets 全包 | 39 顶层通过、1 失败；不是全绿 | `go-assets-media.jsonl` |
| media 全包 | 37 顶层通过 | 同上 |
| 资产相关 HTTP | 5 顶层通过 | `go-http.jsonl` |
| 并发恢复定向复测 | 连续 3 次通过；保留首次失败 | `go-scan-recheck.jsonl` |
| 前端单测 | 4 文件、42 项通过 | `frontend-unit.log` |
| 既有页面交接 UI | 10 项通过，含两条共享订单反馈测试 | `ui-handover.log`、`web/test-results/ui-contracts`、`web/playwright-report` |
| 真实后端 Playwright | 5 文件、7 项通过，报告/截图/trace 均保留 | 下方专用 run |
| 缺陷表征 UI | 4 项通过，表示缺陷成功复现 | `findings.log`、`finding-artifacts`、`findings-report` |
| 缺陷表征 Go | 2 项通过，表示 Unicode/多参考关系缺陷成功复现 | `go-findings.jsonl`、`assets_review_test.go` |
| 构建/类型检查 | 既有 UI 启动流程执行 npm run build 成功；有大 chunk 提示 | `ui-handover.log` |

真实后端 run：[`hcai_e2e_6c695e4899564df0ac1b59d5f99ef150`](../web/test-results/marketplace-runs/hcai_e2e_6c695e4899564df0ac1b59d5f99ef150/)。其中 `report/index.html` 为报告，`tests/` 包含 7 份 trace 与截图，`results.json` 为结果，`source-sha256.json` 为运行源码清单。

`isolation.json` 确认本轮拥有的进程停止、端口关闭、数据库删除、媒体删除，exitCode=0。它没有删除既有目标 10 证据库或开发库。模拟 Stripe 服务仅由复用隔离启动器启动，这 7 条资产流程不代表外部支付验收。

测试工具纠正记录：最初 UI 命令使用了不支持的 `--screenshot` CLI 参数，改用配置项；A04 最初断言原始服务文案，改为实际规范化文案；Go 多参考图 fixture 最初缺存储证据，改为符合当前约束的合成来源。后两项初始日志分别保留为 `findings-initial-harness.log`、`go-findings-initial-fixture.jsonl`，不是产品额外失败。首次 assets 并发失败独立保留，不属于这些 fixture 问题。

## 7. 重复执行与 CI 建议

从仓库根目录执行。前提：Go、Node/npm、Python3、psql、Playwright Chromium 和已安装依赖；专用 loopback PostgreSQL 用户具有 CREATEDB。下面地址仅指本机既有隔离测试设施，不连接生产库。

```bash
TEST_DATABASE_URL='postgres://hcai:hcai@127.0.0.1:55435/hcai_goal5?sslmode=disable' HCAI_REQUIRE_INTEGRATION_TESTS=1 go test -count=1 ./internal/assets ./internal/platform/media
TEST_DATABASE_URL='postgres://hcai:hcai@127.0.0.1:55435/hcai_goal5?sslmode=disable' HCAI_REQUIRE_INTEGRATION_TESTS=1 go test -count=1 ./internal/transport/httpapi -run 'Test(AssetUpload|OwnedAssetUsage|ContentDraftAndAssetVersion|SavedWorks)'
npm --prefix web test -- src/lib/uploadCommands.test.ts src/lib/textPreview.test.ts src/api/command-snapshots.test.ts src/stores/session-command-boundary.test.ts
npm --prefix web run test:ui -- asset-handover.ui.spec.ts --trace=on
E2E_DATABASE_ADMIN_URL='postgres://hcai:hcai@127.0.0.1:55435/hcai_goal5?sslmode=disable' python3 scripts/e2e-marketplace.py --config ../data/assets-review-20260927/playwright.assets.config.ts
npm --prefix web run build
node web/node_modules/@playwright/test/cli.js test --config data/assets-review-20260927/playwright.findings.config.ts
TEST_DATABASE_URL='postgres://hcai:hcai@127.0.0.1:55435/hcai_goal5?sslmode=disable' HCAI_REQUIRE_INTEGRATION_TESTS=1 go test -count=1 ./data/assets-review-20260927
```

审查配置、表征测试位于 evidence 目录，未接入常规 CI；该目录通常受 gitignore 管理，分享时需保留整个证据包。修复后应将“表征现状”的断言改成正确产品行为，并将稳定用例纳入正常 testDir 和 CI。当前 marketplace 隔离配置默认并不包含本次全部五个资产 E2E 文件，不能声称 CI 已覆盖这些新发现。CI 可复用同一隔离启动器，使用专用 `hcai_e2e_admin` 和每次独有数据库，上传 HTML/trace/截图，禁止 reuse 现存开发服务。

## 8. 建议修复顺序与退出条件

1. 先修 A01，阻止未经事实支持的发布披露。
2. 修 A09、A06、A07，补私有响应缓存契约、中文输入一致性和完整使用证据；定位 R01 并保留确定性并发回归。
3. 修 A02–A05、A08，恢复计数、故障隔离、扫描反馈和许可解释的可信度。
4. 再做本区域搜索筛选、统计口径提示、tab-panel 关联、视频性能与扫描措辞改进。

验收退出条件：9 个问题有修复或明确接受的边界及责任人；资产/媒体/相关 HTTP 测试全绿；并发恢复无未解释失败；真实隔离流程和缺陷对应回归可在清理后的本地及 CI 重跑；不把 fixture Provider 结果、管理员单资产截图或后续一次通过替代这些条件。

本审查完成并停止于文档和证据交付，不代表已经修复问题或批准发布。

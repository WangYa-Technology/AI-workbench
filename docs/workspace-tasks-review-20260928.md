# 工作台与任务广场审查

> 后续状态：七项发现已完成修复与回归，见[修复验收记录](workspace-tasks-fix-acceptance-20260928.md)。以下保留修复前的审查结论与证据。

日期：2026-09-28。范围：`/workspace/tasks`、`/market/demands`、任务详情，以及发布、提案、接单、交付、返修、验收、延期、取消、争议和资金衔接。

审查的是当前工作区（包含之前已有的未提交修改），HEAD 为 `aa4c6125e7a26d80870e217bb9fec9004df5bdd8`。本轮新增审查文档及隔离的复现证据，未修改业务实现。

## 结论

确认 **7 类待修复问题：1 类 P1、6 类 P2**。7 个浏览器探针和 1 个数据库探针均复现了对应缺陷；探针通过表示“缺陷得到证实”，不表示修复完成。

选定的既有 Go/HTTP 测试为 19 个顶层测试、14 个子测试，全部通过且无跳过；88 个前端单元测试通过。浏览器探针使用当前代码的生产构建，类型检查和构建成功。

优先修复身份变化后的私密数据残留、跨任务操作锁和交付阻塞；随后处理筛选、分页恢复和统计口径。

## T-01 / P1：身份变化后，详情继续展示上一身份的私密证据

- 位置：`web/src/pages/TaskMarketplacePage.vue:485`（仅监听路由）；详情数据赋值位于第 180–182 行，私密提案/交付/事件按缓存的 `detail` 渲染。
- 复现：以委托方身份打开任务，响应包含私密提案和历史；随后在同一路由调用应用现有的 `session.ensure(true)`，会话接口确认返回 `user: null`。
- 实际：页面已显示访客登录提示，却继续渲染原委托方的提案和历史；任务详情 GET 次数仍为 1，没有按匿名身份重新读取。页面也没有同步清理私密表单、素材和角色状态。
- 原因：组件不监听用户 ID，读取代次只随路由和加载改变；可验收/可取消等部分权限依据旧 `viewerRole`，不直接依赖当前身份。
- 影响：同一浏览器会话失效或身份变化时，后续使用者仍能看到此前已读取的私密内容。**本次证实的是前端数据残留，不是服务端匿名读取越权**；未声称正常设置页“退出登录”按钮会原地停留（该入口通常导航离开）。
- 建议：监听任务 ID 与身份变化；身份变化时立即清空私密数据、素材、表单及操作状态，失效旧请求后按新身份重读。403/404 和会话失效也应清除授权上下文。
- 验收：委托方→匿名、委托方→其他用户、承接者→其他用户均不残留私密列表、素材 URL 或操作权限；旧身份的延迟响应不能恢复这些内容。
- 证据：`confirmed session change leaves preceding client private task evidence visible`。探针使用应用真实会话 store 强制重验和拦截 API，不需要修改业务组件。

## T-02 / P2：自定义任务分类可以发布、分配，却没有可用交付类型

- 位置：`internal/tasktypes/service.go:118` 起允许新建分类；`internal/tasks/service.go:328` 读取分类存在性；第 636 行以 `asset.kind == demand.deliverable_type` 匹配交付，仅对 `text` 和 `mixed` 特判。
- 复现：通过分类服务创建 `brand_image`，图标选 `image`；发布此分类任务、分配给创作者；提交其自有、扫描 clean 的图片。
- 实际：发布和分配成功，交付返回 `tasks.ErrInvalid`。现有六种素材 kind（image/video/audio/document/prompt/workflow）均不等于 `brand_image`，因此换文件不能解决。
- 影响：配置新业务分类后，相关任务会卡在交付前。选择图片图标没有建立交付类型映射。
- 建议：分离“业务分类 code”和“允许交付的媒体 kind”，为分类建立明确契约；为已有自定义分类迁移映射。不要仅放宽为任意文件，以免破坏原任务要求。
- 验收：新增品牌图片类可交付图片、拒绝不符合约定的素材；混合和既有类型保持兼容；不能发布无法履约的分类。
- 证据：`TestReviewCustomCategoryCannotDeliver`。通过 Go overlay 临时加入测试，实际运行分类/任务服务和独立数据库；分配使用测试服务的本地模式以隔离类型问题，**未声称完成真实支付流程**。

## T-03 / P2：交付素材选择器忽略分页，较早的合规文件不可选择

- 位置：`web/src/pages/TaskMarketplacePage.vue:184` 和第 390 行；选择器及附件列表位于第 803 行附近。素材服务默认每页 20 条：`internal/assets/service.go:585`。
- 复现：素材接口第一页为 20 个待扫描文件，同时返回 `total: 21` 和 `nextCursor`；第二页有一个自有且扫描通过的图片。
- 实际：页面只请求一次不带筛选/游标的 `/assets`，在客户端过滤第一页后得到空数组；交付按钮禁用，且没有素材分页入口。页面提示先创建素材，忽略已有合规文件。
- 影响：拥有较多素材的创作者无法选择旧文件。即便第一页有可用素材，也只能在第一页中选择。
- 建议：交付素材选择器支持服务端合规筛选、搜索和分页；明确匹配后端对购买资产、任务授权资产及商品交付源的限制。指定 `assetId` 不在首屏时应单独读取并验证。
- 验收：超过 20 个素材仍能选中旧合规文件；分页失败保留选择；不合规文件不可提交；多文件最多 20 个的约束保持一致。
- 证据：`delivery cannot reach clean owned asset beyond first asset page`。

## T-04 / P2：跨任务导航后，前一个操作会遗留加载锁和表单

- 位置：`web/src/pages/TaskMarketplacePage.vue:373`，尤其第 398 行；路由处理第 485–494 行没有重置 `actionLoading`、操作表单和草稿。
- 复现：任务 A 提交取消，暂缓响应；通过 SPA 路由切到任务 B；再放行 A 的响应。
- 实际：旧结果被加载代次检查丢弃，但 `finally` 也因代次变化跳过清理。B 的提交按钮继续禁用，且取消表单和 A 的原因文本仍在 B 中。
- 影响：用户在请求未结束时离开详情，再进入其他任务，后续操作可能一直不可用，需刷新页面恢复；表单上下文还可能混入另一个任务。
- 建议：导航时为新任务初始化操作状态和草稿；采用独立的操作标识，旧请求完成不能修改新任务的加载状态；仅在同一任务/同一身份内保留重试内容。
- 验收：取消、提案、交付等请求成功/失败/超时后切换任务，目的任务均可操作；旧表单不跨任务继承，旧响应不覆盖当前任务。
- 证据：`late mutation from preceding task leaves destination action buttons disabled`，同时验证 B 详情已显示、A 响应已结束、B 提交按钮仍禁用和旧文本仍保留。

## T-05 / P2：“全部状态”在公开任务列表中变回“开放”

- 位置：`web/src/pages/TaskMarketplacePage.vue:347` 和第 491 行。
- 复现：进入 `/market/demands?status=assigned`，在可见状态选择器选择“全部状态”。
- 实际：空状态被 `applyFilters` 从 URL 省略；路由监听把缺省参数解释为 `open`，下一次接口请求仍带 `status=open`。选择器也回到开放状态。
- 影响：公开列表无法稳定浏览跨状态记录，筛选行为与用户选择不一致。
- 建议：区分“未指定，采用默认开放”和“明确选择全部”，例如在 URL 保留空字符串或使用明确的 all 标识。
- 验收：全部/开放/已分配等选择、刷新、前进后退均保持一致；“我的进展”的默认全状态不受影响。
- 证据：`all statuses selection silently resets to open in available view`，记录实际请求序列 `assigned → open`。

## T-06 / P2：加载更多失败会隐藏已经成功读取的全部任务

- 位置：`web/src/pages/WorkspacePage.vue:847` 与第 1563 行；`web/src/pages/TaskMarketplacePage.vue:224` 与第 639 行。
- 复现：第一页成功显示卡片和加载更多，第二页返回 503。
- 实际：分页错误写入共用页面 `error`，模板转为整页错误分支，之前显示的任务卡片消失。重试按钮执行首屏 `load`，而非重试原游标。
- 影响：一次分页失败阻断已加载任务的进入，并丢失浏览位置；已读数组仍在内存不等于用户仍能使用这些卡片。
- 建议：分离首屏错误和分页错误；保留卡片、游标及位置，在列表尾部给出就地重试。
- 验收：工作台和广场均在 503 后保留旧卡片；重试成功追加、不重复、不从第一页开始；真正的首屏错误仍显示完整恢复入口。
- 证据：`workspace pagination failure removes already loaded cards from view`、`market pagination failure removes already loaded cards from view`。
- 排除项：初始读取失败已有专用错误态和重试，**未复现“首屏失败被误报为空列表”**。

## T-07 / P2：工作台“总报酬”等统计只汇总当前已加载记录

- 位置：`web/src/pages/WorkspacePage.vue:301` 起，以及第 430–432 行；中英文分别标为 `Total rewards` / “总报酬”。
- 复现：完整结果共两条、每条预算 $100。首屏只加载第一条，总报酬显示 $100；点击加载更多后显示 $200，数据未发生业务变化。
- 实际：任务总数来自服务端全量 `total`，开放需求数、提案数和报酬却来自当前数组，且没有“已加载”说明。实际默认任务分页为 40 条，探针用较小分页展示同一缺陷。
- 影响：用户容易把首屏统计误认为账户总量；预算还混合发布、承接及仅投提案的任务，不能直接代表收入或待付款。
- 建议：明确命名“已加载任务预算”等口径，或由服务端提供按角色/状态定义的全量汇总；读取失败时显示未知，不显示可信的零。
- 验收：跨页口径清楚；委托预算、报价和已结算收入不混用；中英文说明一致。
- 证据：`workspace headline totals increase merely by loading another page`。

## 已核对的后端边界

| 边界 | 当前证据与结论 |
| --- | --- |
| 私密履约访问 | `Get` 按委托方/承接者/运营分离提案、交付和事件；`TestTaskPrivacyAndCommissionerReviewAccess` 通过。T-01 属于客户端缓存清理缺口。 |
| 未付款不能分配/验收 | HTTP 服务固定使用 `tasks.NewServiceWithPayments(pool, true)`；资金核对任务、提案、金额、币种及收款方。选定 Provider 任务测试通过。 |
| 幂等和并发 | 命令绑定操作/用户/目标，创建使用事务锁，需求分配使用行锁。并发创建、跨任务键及直接提案转换测试通过。 |
| 交付与授权 | 自有 clean 素材、购买/授权素材限制、最多 20 个；验收与运营接受交付共享 `taskdelivery.GrantTx`，再次检查扫描/归属。交付包和不可变授权快照测试通过。 |
| 截止、延期和争议 | 到期拒绝接单；双方确认延期；制作中争议及运营理由/版本/确认协议测试通过。 |
| 退款与转账 | 取消退款、到期与迟到付款补偿、资金身份不匹配阻断测试通过。测试 Provider 是内存实现，不是实际 Stripe 收款/银行转账。 |
| 列表隔离与游标 | 后端全量计数、稳定分页和停用发布者可见性测试通过；T-05/T-06/T-07 是前端使用缺口。 |
| 前端命令与时间 | 88 个选定单测涵盖幂等重试、身份变化时拒绝旧命令结果、请求快照、查询契约和时区/DST。它们不覆盖详情页生命周期，故不能替代 T-01/T-04 探针。 |

## 验证记录与重现

证据目录：`data/tasks-review-20260928/`。

| 验证 | 最终结果 | 证据 |
| --- | --- | --- |
| 既有任务服务及选定 HTTP 回归 | 19 顶层 + 14 子测试通过，2 包通过，无跳过 | `backend-verified.jsonl` |
| 分类交付缺陷 | 1 个数据库探针通过（确认缺陷） | `category-probe.jsonl`、`category_probe_test.go`、`go-overlay.json` |
| 前端单元测试 | 4 文件、88 用例通过 | `unit.log` |
| 浏览器缺陷复现 | 7/7 通过（确认缺陷）；每个有截图与 trace | `browser.log`、`browser-results.json`、`browser-results/` |
| 生产构建/类型检查 | Playwright webServer 执行 `npm run build` 成功 | `browser.log`、成功运行的浏览器探针 |

浏览器运行在独立端口 `15181`，拦截所有 `/api/**` 请求并断言无未预期请求，不连接人工操作中的 5173 页面或真实支付。会话切换/SPA 导航探针使用应用已有 router/store；表单和筛选操作使用实际 UI。

Go 测试连接本机 `127.0.0.1:55435/postgres`，由项目 `internal/testutil` 创建、迁移并清理独立测试数据库，不修改基础库的业务表。第一次使用数据库名 `hcai` 因该库不存在失败，记录保留在 `backend.jsonl`；更正为维护库 `postgres` 后全部选定测试通过，没有将环境失败解释为业务缺陷。

浏览器探针初版 import 相对路径有误；随后两条分页探针误断言了服务端原始文案（页面实际使用本地化错误映射）。均只修正证据脚本，最终 7/7 通过。审查没有为“让测试通过”修改业务代码。

在仓库根目录执行：

```sh
node web/node_modules/@playwright/test/cli.js test --config data/tasks-review-20260928/playwright.config.ts
TEST_DATABASE_URL='postgres://hcai:hcai@127.0.0.1:55435/postgres?sslmode=disable' HCAI_REQUIRE_INTEGRATION_TESTS=1 go test -json ./internal/tasks ./internal/transport/httpapi -run 'Test(Task|ProviderFunded|DeliveryBundleGrantAndContractSnapshot|DeadlineExpiryAgreementAndPreDeliveryDispute|InactivePublisherTasks|AdminTask)' -count=1
TEST_DATABASE_URL='postgres://hcai:hcai@127.0.0.1:55435/postgres?sslmode=disable' HCAI_REQUIRE_INTEGRATION_TESTS=1 go test -overlay data/tasks-review-20260928/go-overlay.json -json ./internal/tasks -run '^TestReviewCustomCategoryCannotDeliver$' -count=1
cd web
npx vitest run src/lib/taskCommands.test.ts src/lib/taskDateTime.test.ts src/api/command-snapshots.test.ts src/api/query-contract.test.ts
```

Overlay 与浏览器配置中保存了本机绝对路径；复制到其他机器需调整。缺陷探针保存在审查证据目录，未纳入 CI。修复时应反转为正确行为断言并迁入正式测试目录。

未重跑完整带后台服务的 `task-workflow.spec.ts`、全仓 E2E 或远程 CI；未进行真实收款、退款、转账、邮件和部署。本报告不将 UI fixture、内存 Provider 和数据库测试拼接声称为一笔真实端到端交易。

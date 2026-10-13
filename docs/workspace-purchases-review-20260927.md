# 已购内容区域审查

日期：2026-09-27。入口：`/workspace/purchases`。本轮为审查，未修改业务代码或开发账户/生产的购买、退款、授权数据；新增报告与隔离测试证据。基线是 main `aa4c6125e7a26d80870e217bb9fec9004df5bdd8` 加当前工作区，不能将该提交单独当作已验收发布版本。

## 结论

发现 3 个可确认问题：已购列表复用快捷入口缺失、文档类型标签错误、既有已购分页验收断言过时且缺少当前 CI 覆盖。本轮未发现 P0/P1 级下载授权绕过；这一结论限定于下述源码边界和实际测试，不代表生产设施验收。

内置浏览器的管理员没有已购数据，因此只用其核验中文空状态。非空、移动端、授权详情、下载和退款均使用独立 fixture/隔离数据库验证，没有向管理员账户添加购买记录。没有连接生产库、使用真实资金或发送外部邮件。

## 已确认问题

### PUR-01 · P2：列表的“用于创作”入口无法按真实能力显示

位置：`internal/assets/service.go:581`（List）、`:703`（GetOwned）、`:1146`（purchaseProvenance），`web/src/pages/WorkspacePage.vue:1519`。

`GET /assets?source=purchase` 经 `scanAsset` 返回基本资产数据，不填充 `provenance.purchase`；只有详情读取计算购买能力。列表按钮却要求 `asset.provenance?.purchase?.canReuse` 为 true。因此正常的可复用购买也看不到列表快捷入口，进入详情后同一内容才显示该按钮。

桌面 1308px 和手机 390px 的现状探针均确认“列表无入口、详情有入口”；探针按实际 List/GetOwned 的不同字段形状构造响应，不把模拟测试误称为真实 API 证据。真实字段差异由上述源码确认。

建议：明确列表动作契约。可以让列表批量返回基于统一策略计算的最小能力字段，也可以明确只提供详情入口并移除无效分支。不得仅以 licenseCode 推断允许复用，也不应每张卡片单独请求完整详情。

验收：可复用/不可复用、资源包、退款中/已退款分别验证；列表与详情一致，后端依然独立验证权限。

### PUR-02 · P3：文档类已购内容标为“Chat/对话”

位置：`web/src/pages/WorkspacePage.vue:1507`。

媒体标签使用 `create.modes` 并把 `document` 映射为 `chat`。这适合作为创作工具路由，但不适合作为已购文件类型。普通文本文件以及以 document 表示的资源包会被标成对话。

两种视口的探针均确认实际 kind=document，而列表显示 Chat。此问题同样影响共享该列表模板的自有资产。

建议：显示 `workspace.mediaKinds` 的文档标签；资源包可结合 MIME 显示“资源包”。创作工具路由继续使用独立的 creationPath 映射。

验收：image/video/audio/document/ZIP 的标签与下载类型一致，中英文及手机布局正常。

### PUR-03 · P2：已购分页验收断言过时，当前 CI 不执行该测试

位置：`web/e2e/purchased-assets-pagination.spec.ts:34`，`web/src/i18n/messages/en-US.ts:821`，`web/playwright.marketplace.config.ts:8`，`web/playwright.assets-ui.config.ts:4`，`web/playwright.payments.config.ts:8`，`.github/workflows/ci.yml:171`。

该测试仍断言 `Safe loaded items`，当前实际文案已是 `Scan passed (loaded)`；执行到该断言就会失败。同时，当前 CI 使用的支付、隔离市场和资产 UI 三个配置均没有选中此文件。它可被默认 Playwright 配置发现，但不能据此宣称当前 CI 覆盖了已购分页用例。

本轮未运行这个依赖旧启动器的完整测试；不将静态确认的旧断言写成一次真实失败日志。此前资产与订单回归能够通过，也不代表这一遗漏已经解决。

建议：更新断言，并将分页、离开页面后的迟到响应、退款后刷新、账号切换整合到无人工数据依赖的已购验收入口；将其显式纳入 CI。

## 体验优化建议（不等同于已确认漏洞）

- 已购区没有资产区已有的标题/媒体筛选，也没有网格切换；大量购买只能连续加载。建议按买家使用场景补充搜索/类型筛选，固定 source=purchase，保留 URL 与游标边界。
- 当前列表计数代表有效 entitlement 的资产数量，不代表当前可下载数量。下载还依赖订单、扫描、合同和快照状态。建议区分“权益有效”“暂不可下载”“可复用”，并提供原因及订单/支持入口；不要把“扫描通过”解释成可下载或完整版权保证。
- 下载使用直接链接。权限在页面打开后变化、文件丢失或完整性失败时，后端能拒绝，但界面缺少针对下载失败的统一恢复提示。建议补充刷新授权、查看订单及联系支持路径，保留浏览器大文件下载能力，避免把整包读入前端内存。

## 边界审查结果

| 边界 | 核对结果与证据 |
| --- | --- |
| 列表身份/对象隔离 | owner_id 过滤；purchase 必须有该用户 active entitlement；最新家族版本筛选。游标绑定用户、来源及筛选。计数和本页查询在只读 repeatable-read 事务中执行 |
| 详情元数据 | GetOwned 先核对 actualOwner。已退款买家仍可能查看自身记录，但 canDownload/canReuse 由当前策略计算；元数据可见不等于文件可读 |
| 下载授权 | `internal/productpolicy/access.go` 统一验证用户有效、权益有效、买家/订单/商品/许可关联、订单状态、来源扫描和冻结合同；外部支付缺合同、快照不就绪等情况关闭访问 |
| 衍生复用 | `internal/creation/reference_permissions.go` 使用 PurchaseAssetReuseSQL；合同中的 allowsDerivatives 优先，资源包禁止作为普通源复用。没有以 UI 按钮代替后端授权 |
| 冻结交付 | 买家从订单快照或已接受的存储定位读取，不能随当前商品/卖家配置变化切换为新文件。快照缺失或损坏不会默默回退当前商品源 |
| 读取期间并发撤权 | Content.Stat/Open 在存储操作前后重查权限；读取授权过期则关闭句柄，不交付已打开但未经复核的数据。检查针对读取开始边界；不承诺追回已发给客户端的字节 |
| 退款状态机 | refund_requested 期间有效权益仍可用；支付退款确认在事务中更新 entitlement、订单、支付状态并排队清理。退款失败回 fulfilled；迟到失败不能恢复已确认退款的权限 |
| URL/文件索引 | 包内下载只接受该订单冻结清单的规范化 fileIndex；负数、越界、重复参数、额外 key、畸形编码拒绝，不能传对象键下载任意文件 |
| HTTP/缓存/时限 | 单 Range 校验，private/no-store，文件名用 mime.FormatMediaType；下载设置绝对 7 分钟预算，校验 Stat/Open 大小及 ETag 一致性 |
| 分页/异步导航 | source=purchase 随后续页请求；合并去重；workspaceLoadVersion 防止旧响应落入新视图；换账号清除私有数据和游标 |
| 订单定位 | 详情携带精确 orderId；不在首屏的订单独立读取；无效或无权限的订单不能静默替换成另一笔；订单显示冻结许可条款 |
| 文案与布局 | 中文空状态明确入口；统计区区分已加载指标；390/1308px 探针无横向溢出；发现上述媒体标签和快捷入口问题 |

源码主要位置：`internal/assets/service.go:570`、`:703`、`:933`、`:999`、`:1031`、`:1087`、`:1146`，`internal/transport/httpapi/vertical.go:821`，`internal/payments/workflow.go:2687`，`web/src/pages/WorkspacePage.vue:640`、`:787`、`:1060`、`:1181`、`:1493`，`web/src/lib/contentPresentation.ts:20`。

## 验证及证据

证据根目录：`data/purchases-review-20260927/`。

- Go/HTTP/支付隔离测试：54 个通过事件（含子测试；assets 1、httpapi 7、payments 46），无失败、无跳过。涵盖分页/权益、历史购买权限、Range、资源包下载、退款并发重查、冻结交付和损坏拒绝。`go.jsonl`。
- 两个独立现状探针通过：390/1308px，记录 PUR-01/PUR-02、列表/详情返回路径和布局。`probes.log`、`probe-report/index.html`、`probe-results/` 截图和 trace。探针只用于记录现状，不应作为要求缺陷永远保留的正式回归。
- 原订单/异步导航旅程：24/24 通过，结果见 `journeys.log`、`journey-report/index.html`、`journey-results/`。
- 真实本地 Provider fixture + API/worker/隔离 PostgreSQL 的六类媒体购买、预览、下载、退款：6/6 通过，结果见 `real-e2e.log`，run 目录内有报告、截图、trace 和隔离清理证明。
- 当前管理员中文空状态截图：`live-empty-zh.png`；仅作视觉辅助，自动化不依赖此账户数据。

复跑主要命令（仅使用专用本地数据库）：

```sh
TEST_DATABASE_URL='postgres://hcai:hcai@127.0.0.1:55435/hcai_goal5?sslmode=disable' HCAI_REQUIRE_INTEGRATION_TESTS=1 go test -json -count=1 ./internal/assets ./internal/transport/httpapi ./internal/payments -run 'TestAssetListStablePagination|TestHistoricalPurchasedContent|TestProductBundleBuyerContent|TestPurchasedAccessRechecks|TestLegacyPurchaseReadCloses|TestHistoricalExternalPurchase|TestHistoricalContractRetains|TestPurchasedContractControls|TestProductBundleDeliveryRechecks|TestPurchasedDeliverySurvives|TestProductCheckoutAndRefundHTTPContract'
web/node_modules/.bin/playwright test --config data/purchases-review-20260927/audit.config.ts
(cd web && npx playwright test --config playwright.ui.config.ts order-delivery.ui.spec.ts asset-handover.ui.spec.ts)
E2E_DATABASE_ADMIN_URL='postgres://hcai:hcai@127.0.0.1:55435/hcai_goal5?sslmode=disable' python3 scripts/e2e-marketplace.py product-media.payment.spec.ts
```

测试设施是本地 stub，不是 Stripe/银行/对象存储官方沙箱验收；未验证生产 CDN、真实支付商户、真实对象存储或生产并发容量。本轮只报告发现，不将任何待修问题标为已修复。

真实 E2E run：`web/test-results/marketplace-runs/hcai_e2e_5b2342b06619462c92c71ee56be31cff/`。已核验 `isolation.json`：进程停止、端口关闭、测试数据库及媒体删除，exitCode=0。完整证据包：`data/workspace-purchases-review-20260927.tar.gz`（同目录 SHA256）。

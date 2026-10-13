# 资源市场目标 9：可重复的隔离后端浏览器验收

本轮只验收目标 9，不执行生产发布。新增入口 `scripts/e2e-marketplace.py` 和 `web/playwright.marketplace.config.ts`，将真实后端用例组成独立套件；业务成功响应来自 Go API、PostgreSQL 和 worker。外部 Stripe 页面与通道由 loopback fixture 模拟。

## 覆盖审查

原有 `product-payment-return.spec.ts`、`product-refund.spec.ts`、`seller-payouts.ui.spec.ts`、`payout-reviews.ui.spec.ts`、认证/退出 UI 套件大量使用 `route.fulfill({json: ...})`。它们保留为页面状态回归，不作为支付、退款、提现或权限闭环的证据。新套件的故障注入只有网络 abort，或先 `route.fetch()` 取得真实后端结果，再丢弃第一次响应/原样转发重试响应。

| 关键旅程 | 本轮真实后端用例 | 成功判据与证据 |
| --- | --- | --- |
| 访客浏览、注册返回 | `public-marketplace.spec.ts` | 访客浏览商品许可与任务；本轮媒体目录的验证码完成注册，回到原任务；390px 无横向溢出 |
| 登录返回、键盘 | `marketplace-backend.spec.ts` 第一例 | 真实退出、Email/密码登录；Enter 激活登录入口/提交、Space 接受许可；返回原商品；该买家订单数仍为零 |
| 发布、审核、撤回 | `product-publication.spec.ts` 两个宽度 | 上传并等真实扫描 clean；保存/修改/提交/管理员审核；未保存禁止提交、reviewer 普通原文件接口 403；公开商品/撤回 404、worker 通知 |
| Checkout、支付回跳、未知结果恢复 | `marketplace-backend.spec.ts` 第二例 | 第一次 Checkout 已创建但响应丢失；原键重试，Provider 只调用一次；成功回跳参数本身不产生资产；签名 Webhook 到达后刷新页面，从持久订单恢复 fulfilled |
| 已购列表、播放、下载 | `product-media.payment.spec.ts` 六种格式 | PNG/JPG/MP4/WAV/MP3/TXT 真实发布与购买；从已购列表进入详情；下载字节和 Range 206 校验，访客无授权 |
| 退款、撤权 | 同上六例 | 真实退款请求只进入 refund_requested，此时仍可访问；Provider refund.updated 经签名和 worker 后订单 refunded，原下载返回 403；公开预览保留 |
| 销售、结算、提现申请 | `marketplace-backend.spec.ts` 第二例 | 购买前设置零等待/零退款窗口的测试政策；worker 自行生成 available 结算和唯一 credit；提现丢响应原键重试，唯一 reservation；取消后唯一 release，transfer 数为零 |
| 拒付运营 | 同上 | 双投同一签名 dispute 只保留一个事件，结算 dispute_hold；财务页面请求证据，操作响应丢失后原键恢复，只有一个 immutable operation；普通账户 API 403，切换会话后详情清空 |
| 支持入口 | `support-lifecycle.spec.ts` 三例 | 用户真实版权工单、管理员回复/流转/结案、worker 通知；390px；新用户 21 条工单的实际游标第二页 |

跨路径覆盖：匿名/其他买家/非财务访问拒绝，空提现历史，资金读取网络失败及 Reload，Checkout/提现/拒付操作原键重复提交，真实退出与会话切换，390px 与键盘使用。页面文本仅作为呈现断言；资金与授权结论同时依赖 API 状态、数据库行数/状态、下载字节和拒绝响应。

## 隔离和运行方式

前提：仓库要求的 Go、Node/npm、Python 3、`psql`、Chromium 已安装；专用测试 PostgreSQL 角色具有 CREATEDB。管理员 URL 必须显式传入，且只能指向 loopback 上 `hcai_e2e_admin`（本地既有隔离容器也允许 `hcai_goal5`）。默认业务数据库和远程地址会在启动前拒绝。

可新建专用容器（端口须空闲）：

```sh
docker run -d --name hcai-marketplace-e2e \
  -p 127.0.0.1:55435:5432 \
  -e POSTGRES_USER=hcai -e POSTGRES_PASSWORD=hcai \
  -e POSTGRES_DB=hcai_e2e_admin postgres:17
npm --prefix web ci
(cd web && npx playwright install chromium)
export E2E_DATABASE_ADMIN_URL='postgres://hcai:hcai@127.0.0.1:55435/hcai_e2e_admin?sslmode=disable'
npm --prefix web run test:e2e:marketplace
# 第一轮删除本轮数据库和媒体后，再跑同一套件。
npm --prefix web run test:e2e:marketplace
```

单例诊断可用 `npm --prefix web run test:e2e:marketplace -- --grep 'lost checkout'`。不要绕过入口直接运行此 Playwright 配置。

每轮独立生成 `hcai_e2e_<UUID>` 物理数据库、`test_app` schema、临时媒体/邮件目录和三个端口。所有账户均在本轮创建；固定 fixture 账户也只存在于该轮数据库，未读取人工开发数据。不加载调用方的 Provider/SMTP/S3/扫描环境密钥，只允许 loopback Stripe fixture、local_file 邮件/存储和 deterministic 扫描。

服务运行已编译的 API/worker/fixture 二进制与直接 Vite 进程；每个服务使用独立进程组。退出时依次 TERM、等待、必要时 KILL 本轮进程组；检查端口关闭，删除明确拥有的数据库和媒体目录。没有调用旧固定 schema 的 global teardown，也不清理其他数据库或目录。

`.github/workflows/ci.yml` 的 `marketplace-isolated-browser` 使用独立 PostgreSQL 17 service、安装 Chromium 与 psql，连续运行两轮；always 上传两轮报告。这里记录的是本机以 `CI=1` 执行同一配置的结果，不声称已执行托管 GitHub Actions。

## 修复及限制

- 原启动入口固定 schema、媒体目录和 `go run` 父进程不适合作为本轮隔离保证；新增独立入口，不改写旧套件行为。
- 邮件验证码 helper 支持本轮 `E2E_MEDIA_ROOT`，避免读取固定开发目录。
- 发布截图写入每个测试的 outputPath，避免 `/tmp` 固定文件覆盖。
- 首轮发现任务 fixture 的绝对截止日期已过期，导致真实注册返回旅程失败。仅将测试 seed 改为相对当前时间 30/45/60 天；原旅程本身就是回归用例，未放宽断言。
- 默认 Playwright 配置排除仅能在新入口运行的数据库用例。
- 本轮未发现需要改动业务产品代码的问题；未做无关重构。

提现的已验证收款账户是隔离数据库中的合成输入，来自实际 Checkout 冻结的商户/模式/端点身份。本轮没有伪造订单支付、结算金额、预留或释放结果；这些结果均由真实服务产生。但账户开户/KYC、Stripe 沙箱联网、银行下发/到账、真实拒付证据提交、真实 SMTP/S3/扫描设施均不在本轮完成范围。`under_review`、内部审核和 UI 成功消息都不代表银行到账。未知结果测试覆盖应用 HTTP 响应丢失与页面重新加载，不等同于通道未知转账恢复演练。

## 本轮结果和证据

2026-09-27，本地以 `CI=1` 连续运行相同功能源码，两次均 **16/16 通过、0 skip、0 retry/flaky**。两份 `source-sha256.json` 完全一致。

| 轮次 | HTML 报告 | 耗时 | trace / 截图 | 清理证据 |
| --- | --- | --- | --- | --- |
| 完整验收 | [report](../web/test-results/marketplace-runs/hcai_e2e_530f070bf956479b8be4c1abef49b4ef/report/index.html) | 117.3s | 16 / 53 | [isolation.json](../web/test-results/marketplace-runs/hcai_e2e_530f070bf956479b8be4c1abef49b4ef/isolation.json) |
| 清理后复跑（npm 入口） | [report](../web/test-results/marketplace-runs/hcai_e2e_be8856c82f624a8488fbc6270ef5e343/report/index.html) | 114.3s | 16 / 53 | [isolation.json](../web/test-results/marketplace-runs/hcai_e2e_be8856c82f624a8488fbc6270ef5e343/isolation.json) |

全部 32 份 trace 已做 ZIP 完整性检查。两轮都记录 `processesStopped/portsClosed/databaseRemoved/mediaRemoved=true`。验收汇总：[acceptance.json](../web/test-results/marketplace-checks/acceptance.json)。

补充验证：6 个 Go/HTTP 顶层用例（含子例共 17 项 pass，0 fail/skip）、6 个前端测试文件 54 项 pass、应用 typecheck、新 E2E/配置的独立严格 TypeScript 检查、Python 语法、git diff whitespace 均通过。lint 0 error、83 条原有 Vue 样式 warning，未混入无关格式修正。4 种未配置/默认业务库/远程库/带额外连接参数的 URL 均在 setup 前被拒绝。日志位于 [marketplace-checks](../web/test-results/marketplace-checks/)。

本地工具版本：Go 1.26.8、Node 26.5.1、psql 17.10；CI job 使用 setup-go 的 go.mod 版本、Node 22、PostgreSQL 17。测试报告是本地执行证据，CI job 已配置但本轮未推送/触发远程 Actions。

执行补充用例的命令：

```sh
TEST_DATABASE_URL='postgres://hcai:hcai@127.0.0.1:55435/hcai_goal5?sslmode=disable' \
HCAI_REQUIRE_INTEGRATION_TESTS=1 go test ./internal/payments ./internal/transport/httpapi \
  -run 'Test(ProductPaymentDisputeHTTPContract|ProductCheckoutAndRefundHTTPContract|ProductCheckoutReturnParametersSurviveRetry|ProductCheckoutReconciliationToProviderResult|SellerFundsPayoutReservationIsIdempotentAndCancellable|ProductDisputeSettlementTransition)$' -count=1
npm --prefix web run test -- src/api/seller-payouts.test.ts src/api/product-disputes.test.ts src/lib/taskCommands.test.ts src/stores/session.test.ts src/stores/session-concurrency.test.ts src/stores/session-command-boundary.test.ts
npm --prefix web run typecheck
npm --prefix web run lint
```

完成后的文档链接/本节结果更新不在两轮执行前的源码快照内，功能代码与用例未再修改。

每轮根目录包含 HTML report、results.json、tests 内 trace/截图、service logs、source-sha256.json 和 isolation.json。人工及调试失败证据也保留，不能当作通过报告。

在测试运行期间不要修改被 trace 收录的源码；本轮一次调试中发生过 trace ZIP 写入长度不匹配，最终复跑冻结功能源码并核查归档完整性。

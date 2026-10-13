# 工作台「订单」区域审查

> 后续记录：O-01～O-05 已在 [`workspace-orders-fix-acceptance-20260928.md`](workspace-orders-fix-acceptance-20260928.md) 修复并验收。本文保留 2026-09-27 的历史发现和原始证据，不再表示这些问题仍未修复。

日期：2026-09-27。范围：`/workspace/orders`，含订单列表/分页、订单证据直达、支付回跳确认、交付入口、关闭未支付订单和买家退款。基线 HEAD：`aa4c6125e7a26d80870e217bb9fec9004df5bdd8`，审查实际工作区（含既有未提交更改），不是该提交的纯净树。

## 结论

确认 **5 类 P2 问题**，主要涉及退款反馈、异步状态一致性、统计口径和读取失败恢复。本轮没有发现可确认的跨买家订单泄露或退款重复记账问题；这不是对整个支付系统无缺陷的保证。此次仅审查，未修改业务源码。问题目前仍待修复。

7 个独立 Playwright 探针成功复现问题；24 个既有 UI 用例、79 个前端单元用例，以及 Go/HTTP 的 12 个顶层测试和 19 个子测试通过，无跳过。既有用例通过不代表覆盖了新增发现。

## 已确认问题与验收标准

### O-01 / P2：较晚返回的旧退款响应覆盖较新的订单版本

- 位置：`web/src/pages/WorkspacePage.vue:999`，尤其第 1008 行；对比第 75 行 `updateReturnedOrder` 已有的版本保护。
- 复现：订单为 fulfilled/version 4，提交退款后暂缓 POST 响应；点击支付确认卡的“再次查询”，GET 得到 refunded/version 6；随后返回 refund_requested/version 5 的 POST 响应。
- 实际：普通订单卡退回“退款处理中”，已更新的支付确认卡仍显示已退款。退款提交函数只校验页面加载代次，不校验订单 paymentVersion。
- 影响：用户看到过时状态，交付入口可能重新出现；入口出现不等于重新获得权限，后端下载授权仍需重新检查。本次没有证明后端撤权被绕过。
- 建议：将同订单/同支付身份的所有订单响应统一按版本合并；低版本不得覆盖高版本。关闭结账的 `updateClosedOrder` 同样直接覆盖，应在修复时纳入统一规则，但本次没有单独复现关闭操作的竞态。
- 验收：延迟的退款和关闭响应均不得回退订单状态；身份不匹配响应被拒绝；无 paymentVersion 的历史订单有明确兼容策略。
- 证据：探针 `delayed refund command overwrites newer payment refresh`。

### O-02 / P2：退款响应的成功文案误报完成和支付模式

- 位置：`web/src/pages/WorkspacePage.vue:1009`；`web/src/i18n/messages/en-US.ts:835`。后端再次读取订单的位置：`internal/transport/httpapi/marketplace.go:241` 附近。
- 实际：除 refund_requested 外，任何成功 HTTP 响应均显示“Historical Local Test refund recorded and entitlement revoked.”。
- 复现一：Stripe 订单返回 refunded，仍称其为历史本地测试退款。
- 复现二：返回 fulfilled（例如退款结果变化后读取到仍然有效的订单），仍提示退款完成且权益已撤销。
- 证据界限：两种响应的页面表现已用独立 fixture 验证；后端发起退款后会重新查询订单，响应并非固定为 refund_requested。本次未用真实 Provider 验证快速失败的时序。
- 建议：按订单状态、paymentStatus 与 paymentMode 分别表达申请受理、处理中、完成、失败/待核验；未知响应不得推断撤权。
- 验收：外部支付已退款、本地历史退款、退款失败、处理中和未知状态均有准确的中英文反馈。
- 证据：两个 `refund response claims historical test completion` 探针。

### O-03 / P2：退款提交后顶部支付卡不再跟踪新状态

- 位置：`web/src/components/domain/ProductPaymentStatus.vue:68` 和第 101 行；`web/src/pages/WorkspacePage.vue:1723`。
- 复现：从 payment=success 回跳，顶部读取 fulfilled 后结束轮询；在同页普通订单卡提交退款，POST 返回 refund_requested。
- 实际：订单卡变成退款处理中，顶部仍显示已交付。组件仅观察订单 ID、支付 ID、回跳类型和用户身份，未接收父层更新后的订单。
- 建议：共享订单状态或增加可信状态同步入口；退款开始后恢复有时限的查询，保留网络错误、卸载和身份变化的停止逻辑。
- 验收：fulfilled → refund_requested → refunded/退款失败过程中，两处展示同步；导航离开不残留轮询。已有“进入时已在退款处理中”的测试不覆盖该触发路径。
- 证据：`completed payment banner does not follow refund submission`。

### O-04 / P2：读取订单失败同时显示空态，且缺少就地重试

- 位置：`web/src/pages/WorkspacePage.vue:679` 起的 load 与第 1789 行附近的 `UiEmptyState`。
- 复现：首次列表请求返回 503。
- 实际：页面同时显示错误和“暂无订单证据”；没有订单加载重试按钮。原因是清空 orders 后，空态仅检查数组长度。
- 建议：区分 loading/error/success-empty，失败时保留明确错误与就地重试；分页失败保留已读记录。
- 验收：503 不展示“没有订单”的成功空态；无需浏览器刷新可恢复；重试成功后错误消失；真正的 200 空列表仍有正确空态。
- 证据：`failed list is also presented as empty with no retry`。

### O-05 / P2：页头统计并非全量，“有效权益”也不等于有效授权数

- 位置：`web/src/pages/WorkspacePage.vue:259`、第 373–380 行；`ProductOrderDeliveryLink.vue` 的交付状态判断。
- 复现一：仅有 refund_requested 订单时仍可进入交付副本，但有效权益为 0；退款处理中并不当然撤权。
- 复现二：订单记录从 1 变为 2，仅因为点击加载更多，标签没有说明“已加载”。当前生产默认每页 20 条。
- 影响：订单记录/已退款统计容易被当成账户总数；有效权益基于订单状态推算，不能代表实际 entitlement/download/reuse 能力。
- 建议：明确采用“已加载”口径，或提供服务端全量汇总；不要将 fulfilled 订单数命名为有效权益。若需要权益统计，应由服务端实际授权状态计算。
- 验收：跨页统计语义稳定；退款中、已撤权、交付异常等状态不会产生误导。
- 证据：`refund pending keeps delivery but active count is zero`、`order record total increases merely by loading another page`。

## 边界审查

| 边界 | 源码与本次证据 | 结论/限制 |
|---|---|---|
| 身份与订单对象 | ListOrders/GetOrder 的 buyer_id 约束；HTTP requireUser；关闭测试含匿名 401、其他用户 404 | 已有授权约束，未发现跨买家读取证据；游标不是授权凭据 |
| SQL/HTTP 输入 | 参数化 buyer/id/游标查询，limit 1–50、UUID 与游标解析；测试含无效游标/超限 | 基本检查通过；重复 query 参数、游标大小上限可进一步收紧，不据此声称存在注入 |
| 幂等与并发 | 历史退款原始账务证据/重放测试、Provider 持久派发与回调先到/响应丢失测试、关闭 expectedVersion/重复命令 | 后端选定场景通过；前端版本合并存在 O-01 |
| 状态机 | 支付回跳重新 GET，校验 order/payment ID，不信任 URL success 作为付款凭据；关闭命令校验确认与版本 | 回跳基本边界存在；页面同步与反馈存在 O-02/O-03 |
| 交付与下载授权 | 交付入口仅接受有效资源 ID 与允许状态；既有 UI 测试覆盖已退款入口移除、401/403/404/422 清除上下文 | UI 导航不是下载授权；本次未重新穷举底层文件下载/对象存储路径 |
| 敏感信息 | 订单读取 private,no-store；冻结商品/授权快照；列表限定买家；关闭恢复测试包含管理队列访问控制 | 未读取密钥或导出会话；真实商户响应脱敏不在本次验收范围 |
| 重试与超时 | 支付 GET 单次至多 10 秒、总窗口 60 秒，约 2.5 秒间隔；卸载/切身份中止 | 初次列表无就地重试 O-04；完成后发起退款不恢复查询 O-03 |
| 生命周期 | 页面请求代次隔离；退款反馈不跨页面；授权失败清除交付上下文 | 24 个相关既有 UI 用例通过 |
| 上传文件/对象键 | 订单区没有直接上传或客户端指定存储对象键的操作 | 源资产上传/扫描沿用独立资产审查，本次不重复宣称全面验证 |

## 非阻塞优化建议（不是已确认安全漏洞）

1. 订单号/商品关键词、状态、日期筛选；复制订单号及携带订单上下文的支持入口。
2. `internal/marketplace/service.go:196` 起逐单查询事件，存在 N+1 查询结构；考虑批量取事件。事件历史目前全量展开，可按需展开/分页。尚未做容量压测，不能声称已达到性能瓶颈。
3. 对超长游标及重复查询参数明确约束；增强错误态和退款竞态的 CI 回归覆盖。
4. 把统计名称、退款失败/未知结果、历史测试与真实支付文案统一审校。

## 验证与证据

证据根目录：`data/orders-review-20260927/`。全部新增探针使用固定独立 fixture 并拦截 API，在独立端口 15179 的生产构建上运行，不依赖 5173 开发服务器的人工数据。`live-orders.png` 仅记录内置浏览器下当前管理员的空订单视觉上下文，不作为业务路径通过证据。

| 验证 | 结果 | 证据 |
|---|---|---|
| 订单归属/106 条稳定分页、历史退款证据、HTTP Checkout/退款、签名退款关联、持久派发 | 11 顶层测试 + 19 子测试通过，3 包通过，无 skip | `go.jsonl` |
| 关闭 Checkout HTTP（确认、版本、幂等、归属、恢复队列权限） | 1 顶层测试通过 | `closure-go.jsonl` |
| 已有订单交付和资产交接 UI | 24 passed，含中英文及 320/1308px 订单交付路径 | `existing.log`、`existing-report/index.html`、`existing-results/` |
| 新增问题复现探针 | 7 passed，表示缺陷已复现，**不是修复验收通过** | `probes.log`、`probe-report/index.html`、`probe-results/`（截图与 trace） |
| API 请求契约/命令快照单元测试 | 79 passed | `unit.log` |
| 类型检查和生产构建 | Playwright webServer 执行 npm run build 成功 | 两次最终 UI 运行成功启动；未单独执行 lint（无业务源码修改） |

初版统计探针误用了不存在的 `.workspace-hero` 选择器，已改为实际统计组件选择器，最终 7/7 通过；该工具编写失误不作为产品缺陷。日志中的 NO_COLOR/FORCE_COLOR 提示属于测试环境颜色变量冲突，不是业务构建失败。

复现命令（仓库根目录，已安装 Go、Node、web 依赖和 Playwright Chromium）：

```sh
node web/node_modules/@playwright/test/cli.js test --config data/orders-review-20260927/playwright.config.ts
node web/node_modules/@playwright/test/cli.js test --config data/orders-review-20260927/existing.config.ts
cd web
npx vitest run src/api/command-snapshots.test.ts src/api/query-contract.test.ts
```

上述两组 Playwright 命令需顺序执行，均占用 15179。探针位于本地审查证据目录，未加入 CI，不应成为要求“保持缺陷”的正式回归测试；修复时须改写为正确行为断言并纳入版本化测试目录。现有 `.github/workflows/ci.yml` 使用 `playwright.assets-ui.config.ts`，已包含订单交付和资产交接用例；本轮没有触发远程 CI，不能声称远程执行已通过。

Go/HTTP 在 `127.0.0.1:55435` 的本地 PostgreSQL 上通过项目 testutil 创建和清理独立数据库，使用 `HCAI_REQUIRE_INTEGRATION_TESTS=1`，没有连接生产数据库。筛选表达式及统计见证据目录 `verification.json`。

## 未验证与交付边界

- 未发起真实 Stripe/Waffo/银行交易、邮件或任何生产部署/资金操作；真实设施验收仍遵循此前“先不做”的决定。
- 未重复运行整个资源市场真实 API+worker 浏览器端到端套件、恢复演练、容量测试或全仓测试。本轮页面探针是 UI 契约验证，后端行为由上述独立集成测试验证，不能合并表述成一次全栈交易验收。
- 本轮审查没有修复 O-01 至 O-05，也没有提交既有未提交变更。后续修复优先 O-01/O-02，再同步支付卡、补错误恢复与调整统计口径。

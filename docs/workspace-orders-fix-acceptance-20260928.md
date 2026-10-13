# 工作台订单区域修复验收

日期：2026-09-28。范围：`/workspace/orders` 的订单读取、分页、支付回跳、退款、交付入口和订单辅助操作。基于审查文档 [`workspace-orders-review-20260927.md`](workspace-orders-review-20260927.md) 的 O-01～O-05 实施修复；本文件是修复后的验收记录，不替代历史审查结论。

## 结论

O-01～O-05 均已修复并通过针对性回归。订单页现在对同一订单按支付身份和版本单调合并响应；退款反馈按真实订单状态区分网关退款、历史本地测试退款、处理中、失败和未知；支付状态卡会跟随订单卡发起的退款并在有限窗口内恢复查询；列表读取失败与真实空列表分离并提供就地重试；页头统计明确标记为“已加载”范围，其中第三项是“处理中 · 已加载”，不宣称当前有效授权或账户总数。

## 修复清单

| 编号 | 修复 | 验收证据 |
|---|---|---|
| O-01 | `mergeOrder` 校验订单 ID、支付身份和安全版本；关闭、退款、列表和分页读取共用合并边界；历史无版本退款保持终态。 | 延迟退款/关闭响应、错误身份和晚到 GET 的 Playwright 用例 |
| O-02 | `refundFeedback` 仅在已确认状态使用完成文案；失败和未知结果明确要求再次查询或联系支持。 | 中英文 refunded、test_refunded、refund_failed、未知和处理中用例 |
| O-03 | `ProductPaymentStatus` 接收父层订单，退款受理后替换/恢复有限轮询；身份变化、卸载、超时和失败会停止轮询。 | pending→terminal、延迟 POST、失败、60 秒上限和离开页面用例 |
| O-04 | `orderReadFailed` 与成功空列表分开；首次/分页失败提供重试且保留已读记录。 | 503 重试、分页失败保持记录和真实空列表用例 |
| O-05 | 统计名称和说明改为“已加载订单/已退款 · 已加载/处理中 · 已加载”；不把订单状态推断成 entitlement。 | 320px 溢出与分页统计用例；页面截图 |

## 配套优化

- 订单标题/订单号、状态和本地日期筛选；范围明确为当前已加载记录。
- 复制订单号、明确的支持入口、默认折叠订单事件历史和事件空态。
- `ListOrders` 将逐订单事件查询改为当前页一次批量查询，并预置无事件订单的空数组。
- 查询参数拒绝重复键、非法 limit、畸形或超过 512 字节的 cursor；OpenAPI 同步更新。

## 验证结果

- 前端单元：294 tests / 37 files passed，见 [`unit.log`](../data/orders-fixes-20260928/unit.log)。
- Go/HTTP：13 个顶层测试、19 个子测试、3 个包通过，无失败或跳过，见 [`go.jsonl`](../data/orders-fixes-20260928/go.jsonl)。覆盖订单归属、分页事件、历史退款、Checkout/退款 HTTP、签名关联、持久派发和关闭 Checkout。
- Playwright：订单、购买内容、交付和资产交接共 84 passed，见 [`ui-final.log`](../data/orders-fixes-20260928/ui-final.log)、[`report`](../web/test-results/assets-purchases-ui/report/index.html)；每个用例保存截图和 trace。配置已纳入 `web/playwright.assets-ui.config.ts`，API 全部由 fixture 拦截，不依赖开发服务器人工数据。
- 隔离全栈：6 个媒体类型的 Checkout、支付后播放/下载和退款通过，见 [`real-e2e.log`](../data/orders-fixes-20260928/real-e2e.log)。使用本地 Stripe fixture；`isolation.json` 记录独立数据库、进程、端口和媒体均已清理。
- `npm --prefix web run lint`、`npm --prefix web run typecheck`、生产构建和 `git diff --check` 通过。

## 可重复命令

```sh
npm --prefix web test
npm --prefix web run lint
npm --prefix web run typecheck
node web/node_modules/@playwright/test/cli.js test --config web/playwright.assets-ui.config.ts
TEST_DATABASE_URL='postgres://hcai:hcai@127.0.0.1:55435/hcai_goal5?sslmode=disable' \
  HCAI_REQUIRE_INTEGRATION_TESTS=1 go test -json -count=1 ./internal/marketplace ./internal/transport/httpapi ./internal/payments \
  -run 'TestOrderHistory|TestOrderListFilterHTTP|TestHistoricalRefund|TestHistoricalProductRefundHTTP|TestProductCheckoutAndRefundHTTP|TestProductRefundLostResponseSignedCorrelation|TestProductRefundCallbackBeforeResponse|TestProductRefundHistoricalSuccess$|TestProductRefundRejectsProviderWithoutRefundCapability|TestProductRefundDurableDispatch$|TestProductCheckoutClosureHTTP'
E2E_DATABASE_ADMIN_URL='postgres://hcai:hcai@127.0.0.1:55435/hcai_goal5?sslmode=disable' \
  python3 scripts/e2e-marketplace.py product-media.payment.spec.ts
```

UI 用例通过生产构建和专用端口运行；集成命令由 testutil 创建并删除临时数据库。未连接生产库，未调用真实 Stripe/Waffo、银行、SMTP、对象存储或扫描设施，未执行部署、真实资金或测试邮件操作。

## 限制与后续

统计仍是页面已加载数据，不是服务端账户汇总；订单状态也不等同于下载授权。事件历史目前只做当前页批量读取和默认折叠，尚未做容量压测或事件分页 API。支持入口的订单上下文预填已在下方补充验收；仅由用户显式提交后创建站内工单，不自动向外部系统发送。真实 Provider/银行/对象存储/扫描/SMTP 联合验收及发布演练仍需专用预发布账户和明确授权后另行执行。

## 证据保存与版本边界

本轮修改基于 HEAD aa4c6125e7a26d80870e217bb9fec9004df5bdd8 的既有脏工作区，并未提交或部署。证据归档位于 data/orders-fixes-20260928/ui-evidence.tar.gz 和 real-e2e-evidence.tar.gz；源码及证据校验和见同目录 sha256.json。解压后 UI 报告入口为 assets-purchases-ui/report/index.html，截图及追踪位于其 tests/ 下。原始 web/test-results 可能被后续测试覆盖，应保留归档。

UI 最终套件覆盖全部最终体验改动；6 个隔离全栈用例在核心修复之后、附加筛选/复制/折叠优化之前执行。CI 使用相同版本化配置，但本轮没有触发远程 CI，不能声称远程 CI 已通过。此次没有完整重跑整个仓库全部 Go 包或所有 E2E。


## 剩余可修复项补充验收（2026-09-28）

本轮直接完成本地代码可控的支持路径优化，并修复全量单测新发现的组件规范问题。没有更改支付、授权、账务或部署行为。

| 项目 | 已完成行为 | 回归证据 |
|---|---|---|
| 订单卡与支付状态卡联系支持 | 链接携带订单 UUID；支持页校验单值、格式后预选“任务或订单”和关联订单号；用户填写问题并显式提交 | 中英文入口、表单、提交 payload 和创建后关联证据断言 |
| 支持参数与取消边界 | 无效/重复 orderId 不预填；取消时移除关联信息；提交失败保留用户填写内容与订单号 | 新增 5 个 Playwright 用例，包含上述中英文流程与异常路径 |
| 支持文案 | 中英文提示核对关联订单，说明打开表单不会自动创建工单 | 中英文页面截图与 trace |
| 订单历史统一组件 | 使用现有 UiCollapsible 替代原生 details，保持默认折叠，提供 aria-expanded 状态；移除过期 summary 样式 | 全量组件规范单测与中英文 320px 展开测试；窄屏截图检查 |

订单号只是工单的待提交关联信息，不是订单归属或支付成功凭证。后端既有 support 资源校验仍通过 buyer_id 验证订单归属。新 UI 用例中的 422 是模拟拒绝，不是跨买家 HTTP 集成验证；本轮未新增或变更后端授权规则。

### 本轮最终验证

- 前端单元 **294/294，37 个文件通过**：`data/orders-support-fixes-20260928/unit-final.log`。
- 订单 UI **27/27 通过**（原 22 项 + 新增 5 项）：`data/orders-support-fixes-20260928/ui-final.log`。每个用例保存截图和 trace。
- Go/HTTP **2 项通过，无跳过**：支持工单生命周期权限与所属用户分页隔离，见 `data/orders-support-fixes-20260928/go.jsonl`。沿用本地 127.0.0.1:55435，由 testutil 创建并清理独立测试数据库。
- lint 通过：`data/orders-support-fixes-20260928/lint-final.log`。最终 Playwright webServer 重新运行 `npm run build`（内含 vue-tsc），生产构建与类型检查通过；`git diff --check` 通过。
- 首次全量单测发现 1 项失败：订单历史原生 details 不符合统一 UI 组件约束。修复为 UiCollapsible 后最终全量单测通过。初次失败日志保留在 `unit.log`，不隐藏失败过程。

复跑命令：

```sh
npm --prefix web test
npm --prefix web run lint
node web/node_modules/@playwright/test/cli.js test --config web/playwright.assets-ui.config.ts orders-experience.ui.spec.ts
TEST_DATABASE_URL='postgres://hcai:hcai@127.0.0.1:55435/hcai_goal5?sslmode=disable' \
  HCAI_REQUIRE_INTEGRATION_TESTS=1 go test -json -count=1 ./internal/transport/httpapi \
  -run 'TestSupportCopyrightLifecyclePermissions$|TestSupportOwnerHistoryHTTPPaginationAndIsolation$'
```

UI 使用版本化 CI 同款配置、专用生产预览端口和完全拦截的 API fixture，不依赖开发服务器现存人工数据；没有触发远程 CI。报告、截图和追踪归档：`data/orders-support-fixes-20260928/ui-evidence.tar.gz`，解压后入口 `assets-purchases-ui/report/index.html`；源码及证据校验和：同目录 `sha256.json`。未提交、部署或调用外部支付/银行/邮件设施。

### 保留边界

- 服务端全账户订单/授权统计仍未新增；现有“已加载”口径准确，这属于可选功能扩展，不是未修复的 O-05。
- 事件历史已批量读取并按需展开；事件分页 API 和容量压测仍未实施，不能声称已完成容量验收。
- 真实设施联合验收和发布演练仍遵循用户此前“先不做”的决定。

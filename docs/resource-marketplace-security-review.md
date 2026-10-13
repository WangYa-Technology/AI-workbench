# 资源市场目标 6：源码安全审查

日期：2026-09-27。范围为当前未提交工作树；基线中目标 1–5 的改动保留。本审查不是生产渗透测试，也不声明每个实现分支都经过形式化验证。

## 审查边界（修改前建立）

| 角色 / 操作 | 身份与对象授权 | 幂等、并发、状态机 | 信息、输入与重试 |
| --- | --- | --- | --- |
| 卖家发布 / 审核 / 商品版本 | seller_id、active 用户、admin:content、禁止自审、原件归属、独立预览 | actor/key 哈希绑定请求；版本 CAS；发布/审核/资产行锁；冻结已接受版本 | 参数化 SQL；正文上限；审核文件限 source/preview/index/version；读前后复核权限 |
| 买家 Checkout | buyer/order/product/offer/amount/currency 绑定，不能购买别人的订单 | 冻结请求及 Provider 身份；同键不同请求冲突；发送前持久化记录 | HTTP 不接受任意 Provider 身份；Stripe 23 小时重发窗；不确定结果先查询 |
| Webhook / 查询 worker | 签名、时间容差、mode、原始支付/Charge/merchant 绑定 | event 唯一键及摘要；乱序与矛盾证据保留；查询结果不授予新的发送权限 | 有界正文、网络期限、敏感错误分类；不记录原始 payload 到用户投影 |
| 交付副本 / 下载 | 订单买家、有效 entitlement、clean 原件、冻结合同；按文件索引而非存储键 | 副本 key/hash 先落库；create-only、摘要验证；失效授权不得通过旧 Content handle 读取 | MIME 探测、10 MiB 上传上限、扫描门；包文件名规范化/冲突检查；路径和符号链接保护；Range 有界 |
| 退款 / 撤权 / 结算 | 原始付款和退款操作身份、窗口及支付状态；财务权限独立于支持 | payment/order → seller funds → settlement；重复确认不重复撤权或入账；债务与已付款证据保留 | 退款不确定不能换 key 重发；通知/清理和撤权在事务内提交 |
| 提现 / 银行结果 | seller 归属、admin:finance、禁止自操作、原目标银行和 merchant/mode/currency | reservation/review/dispatch/read/result 分离；审批版本；首次发送记录和查询恢复 | DTO 不返回内部审批理由/dispatch key；20 秒调用及额外保存预算；job 控制与 heartbeat 独立 |
| 支持 / 通知 / 导出 | 工单 requester 归属、admin:support；通知 user_id；导出 buyer/seller 双重关联 | 工单版本控制；通知 source key；导出一致快照；事务后权限不可假定永久有效 | 支持不可直接退款/改授权；通知目标路径白名单；导出字段白名单、流式大小限制、排除对象键及凭据 |

## 源码核查证据

- 发布审核：`internal/marketplace/publication.go` 的 `listingActor` 锁用户及权限行；`MutateListing` 校验对象归属、自审、版本与命令摘要；`ReviewListingFileAt/RecheckListingFile` 与 `internal/transport/httpapi/product_publication.go` 在文件读取时组合资产授权。
- 商品版本 / 交付：`product_checkout_request.go` 冻结支付请求，`productdelivery/snapshot.go` 先记录对象位置、大小和摘要再写副本，ready 副本不回退到卖家可变原件；`bundle.go` 检查规范化文件名、路径、保留名和碰撞。
- 买家读取：`marketplace/service.go` 的订单查询限定 buyer_id；`assets/service.go` 的 `resolveContentAccess`、`authorizePurchaseContent` 和 `Content.Stat/Open` 联合 `productpolicy/access.go`检查账户、订单、entitlement、来源扫描与冻结合同。退款请求期间尚可读取，确认退款后才撤权；已开始发送的字节无法追回。
- Webhook / 查询：`payments/webhook.go` 使用 HMAC 常数时间比较及时间容差；`product_stripe_webhook.go` 核对最小化金融字段；`product_checkout_lookup*.go` 检查原始身份及多来源矛盾证据；`transport/httpapi/payments.go` 限制正文及 Content-Type。仅有外部返回状态并不直接授权金融效果。
- 退款 / 结算：`payments/workflow.go` 的 `BeginProductRefund/refundProductPaymentTx` 锁订单及支付、匹配原退款、事务内撤权/通知/清理；`product_refund_dispatch.go` 限制原操作重发窗口及单次发送；`product_settlement.go` 冻结费率/净额，持相同支付/卖家资金锁，保留恢复债务。
- 财务 / worker：`finance_authority.go` 支持加锁的权限复核；`seller_payout_review.go`、`seller_bank_payout_execution.go` 拒绝自操作、验证原目标、持久化首次发送与独立查询；`transfer_execution_job.go` 通过数据库控制绑定 job kind/payload 并让 heartbeat 继续。
- 存储 / HTTP 输入：`assets/service.go` 探测 MIME、限制上传大小、使用服务端生成 key；`platform/media/store.go`、`local_store_unix.go` 限单个路径分量且 openat/NOFOLLOW；`httputil/response.go` 限 1 MiB、拒绝未知字段和尾随 JSON。通用 JSON decoder 未拒绝重复字段，通用 idempotencyKey 使用 Header.Get；在本次检查路径未证明其能绕过服务层授权/状态约束，未做全局解析器重构。
- 通知 / 导出：`notifications/repository.go` 按 user_id 查询并限制 TargetPath；`datarights/marketplace_export.go`、`export_queries.go`、`export_stream.go` 使用受限投影和主体关联；前端 `lib/taskCommands.ts` 将命令绑定 actor/epoch，存储 opaque key/hash 而非正文，`lib/checkout.ts` 清除 opener。前端仅提供交互，不能替代服务端授权。
- 支持：`support/service.go` 的 owned 读取/回复按 requester_id 限定；工单 resolve 的 refund_guidance 仅是支持结论，不调用支付、撤权或余额 API。管理员读取由 HTTP 入口授权；本轮确认的写入竞态见下节。

## 确认并修复的问题 G6-01

**支持操作在 HTTP 鉴权后撤权仍能提交回复 / 结案。**

触发：先让有效 operator 的 HTTP 请求通过 `requirePermission`，在即将获取工单行锁处暂停；随后提交角色降级、账号 suspended 或删除 admin:support 权限，再继续原请求。原 `AdminReply/AdminUpdate` 仅验证工单版本和状态，未在事务内验证操作者。回归测试在修改前得到 HTTP 200（期望 403），证明旧请求可以伪装为 operator 写入消息、状态及通知。问题不直接赋予退款或下载权限。

修复只涉及 `internal/support/service.go` 和 HTTP 错误映射：工单锁之后读取并 `FOR SHARE` 锁定当前用户、角色权限，通过后才能执行写入，锁保持到提交；撤权先提交则拒绝，操作先取得授权锁则撤权等待该事务结束。复用现有 `permission_required` 前端错误码，无需 UI 或 API 类型变更。

最小回归：`internal/transport/httpapi/support_authority_test.go` 覆盖 reply/resolve × role/status/permission 六个交错，通过真实 HTTP session 验证 403，且工单版本、分配者、消息、事件和通知全部无变化。使用有界 context、明确 query gate、释放 gate 并 join goroutine，不使用 sleep 猜测时序。原支持生命周期测试仍验证正常成功路径。

## 验证记录

运行器与每轮命令、环境、耗时、退出码、源码 SHA-256 manifest 位于 `/tmp/hcai-goal6-20260927/`。测试 PostgreSQL 强制使用专用容器端口 55435，启用 HCAI_REQUIRE_INTEGRATION_TESTS。

- support-before：新增 HTTP 回归在原实现失败（200 而不是 403）。
- support-after：`go test -race ./internal/transport/httpapi -run '^TestSupport' -count=1 -timeout 3m` 通过，16.305 秒包耗时，23.112 秒总耗时。
- frontend：`npm --prefix web test` 通过，33 文件、257 测试。
- boundary-go：完整 marketplace/assets/productdelivery/productpolicy/media/notifications/datarights/httpapi 八个包测试全部通过，总耗时 199.857 秒；HTTP 包 197.300 秒。
- payment-boundaries：支付授权、Webhook、幂等/并发、退款、银行、导出及 lease 的 17 个顶层测试选择集使用 Race，通过；包耗时 72.877 秒，总耗时 75.629 秒。这不是 payments 全包通过声明。

结论：本轮确认并修复 G6-01；其余列出的已检查边界未发现足以支持额外代码修改的问题。未进行无关重构。测试结果覆盖以上列明范围，不替代生产 Provider、部署配置或外部服务验收。

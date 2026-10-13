# 商品拒付裁决运行手册

适用范围：目标 3、迁移 0178。真实 Stripe 联调与上线验收属于目标 8。权限/迁移审计及状态机见 [裁决审查](product-payment-dispute-adjudication-review.md)。

## 负责人和证据

财务运营负责分流、举证材料组织、记录 Provider 提交回执引用和升级追偿；Provider 负责外部裁决，payment worker 负责事务落地。运营页面记录的“举证已提交”只表示引用被登记，不构成 Stripe 已收到材料或已裁决的证明。不得通过 SQL 改余额、修改 Provider 状态或改写裁决台账来结案。

## 发布和升级

1. 在隔离数据库运行迁移与 Race 选择集；备份生产数据库，按发布流程先应用 0178，再启用匹配版本 worker。
2. 0178 对旧版已处理且已绑定的 won/lost/charge_refunded 最新事件排入 `payment.process_event`，payload 标记 `disputeAdjudicationBackfill=true`。这些作业复用原始 Provider 证据；预先核对待补齐的订单数量与金额，监控队列失败和复核数量。
3. 裁决事实或升级作业存在后，down 会拒绝回滚。停止写入并采用前向修复，保留事件、审计和资金历史；不要删除证据以绕过 down guard。

## 日常核对

按 dispute ID 串联 `product_payment_disputes`、`product_payment_dispute_events`、`payment_provider_events`、`product_payment_dispute_adjudications` 与 `audit_events`。核对完整 provider/mode/PaymentIntent/Charge/amount/currency 元组，确保 `payment_id/order_id/settlement_id` 一致。零匹配、多匹配或元组变化不得手工绑定另一付款。

- **won**：只能解除符合条件的 dispute hold。其他未决拒付、退款中/核对中、refund hold、已转账或 recovery required 都不能由赢诉恢复为可出款。
- **lost / charge_refunded**：确认 `dispute.loss.applied` 只出现一次；订单和权益关闭、清理/通知/审计与结算投影一起提交。界面 `refunded` 表示现有关闭访问状态，不能据此声称又发出了一笔退款。Provider refund ID 不应由拒付代码生成。
- **withdrawn / 未知 / 矛盾**：继续冻结并进入复核。向 Provider 获取真实明确的后续证据；撤回投诉或运营填写的“胜诉”都不能解除冻结。矛盾终局与 review-only 裁决为粘性复核，当前不提供手工释放或覆盖裁决的命令。
- **退款进行中**：不要创建第二笔退款。未发送的作业被拒付检查阻止；已经发送的退款继续通过原退款查询/回执处理。核查 Provider 是否发生重复退还，必要时升级财务核对。当前复核裁决不会自动重算为资金效果。
- **已转账/已出款/响应未知**：核对 `seller_recovery_obligations` 的 settlement 和净额；保留原 ledger credit、reservation、银行出款与资金来源证据。使用既有来源撤回/追偿流程获取可核验结果，不能直接减卖家余额或解除预留。

## 重试与告警

`40001`/死锁应由作业框架重试完整事务。新事件或重放必须携带原 Provider event ID；不生成新的 Provider 事实。重复事件不得增加退款调用、撤权、通知、恢复债务或裁决数。若审计/清理/账务写入失败，事务回滚，按原作业重试。

巡检 `requires_review`、拒付相关失败/耗尽作业、超期举证、仍开放的 recovery obligations；使用现有监控与只读查询核对，不把观察超时当作远端成功。无法确认身份、结果、累计金额或外部资金去向时保留冻结并升级，不进行余额补丁。

## 隔离验证命令

```sh
go test -race ./internal/payments -run 'TestProductDispute|TestIsNewerDispute' -count=1 -timeout=4m
go test ./internal/admin ./internal/transport/httpapi ./internal/platform/database -run 'Test(ProductPaymentDispute|MigrateEmptySchema)' -count=1 -timeout=4m
go vet ./internal/payments
npm --prefix web run test
npm --prefix web run typecheck
npm --prefix web exec -- openapi-typescript internal/transport/httpapi/openapi.yaml -o web/src/api/schema.d.ts --check
git diff --check
```

测试连接 `TEST_DATABASE_URL`，每个数据库 fixture 使用独立 schema 并清理；不连接生产数据库或真实 Stripe。不能将这些结果等同于全仓测试、完整 payments Race 或真实通道验收。

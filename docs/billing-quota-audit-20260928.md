# 额度与计费功能审核

日期：2026-09-28。范围包括点数订阅、生成额度预留/扣减/释放、Local Test 钱包、账单流水、管理员余额调整、Checkout 回调入口、前端工作台计费页，以及并发、幂等和迁移边界。

后续修复验证更新：2026-10-02。

## 结论

审核发现并修复 1 个 P1 级额度一致性问题；其余已检查路径未发现新的可复现安全或账务重复扣款问题。

### P1：实际生成扣费可能消耗其他生成已预留的点数（已修复）

位置：`internal/billing/points.go` 的 `CaptureGenerationPointsTx`。

生成开始时，系统把估算点数放入 `reserved_points`；生成完成时，Provider 实际用量可能高于估算。原逻辑只判断 `balance_points >= actual_charge`，但 `balance_points` 包含其他生成仍持有的预留点。并发场景下可能出现：账户余额 100、总预留 80、本次预留 20、实际扣费 50。原检查会通过，扣费后余额 50、预留 60，数据库约束会拒绝或在边界实现变化时造成可用额度错误。

修复后使用：

`balance_points - reserved_points + current_held_points >= actual_charge`

这样只允许使用本次释放的预留点和真正可用余额，不会侵占其他生成的额度。新增集成测试验证扣费被拒绝时余额、预留和 reservation 状态保持不变。

## 已核验的合同

- 生成预留按用户账户行锁定，余额不足不会创建有效预留。
- 相同生成操作重复 capture 使用既有结果，不重复写入点数流水。
- 失败、取消和恢复路径释放点数预留；钱包旧账本路径也保持原子释放。
- 订阅购买具备幂等键、活动订阅唯一约束、钱包扣款与点数入账事务一致性。
- 模型订阅白名单、点数定价规则、Provider usage 缺失和实际用量计费均有测试覆盖。
- 管理员钱包调整有权限、幂等、并发、审计和“不能低于预留余额”保护。
- 账单流水按用户隔离，支持方向、类型、日期和游标分页；前端日期筛选会把结束日期扩展到当天末尾。
- HTTP 层覆盖匿名拒绝、无账户、无订阅、模型未包含、点数不足、Checkout 幂等和回调入账。
- 前端计费页展示可用点数、预留钱包余额、订阅方案和最近点数流水；页面身份切换会清除旧账户数据。

## 后续优化与处理结果

1. 点数概览接口已支持 `cursor` 和 `limit`（1–50）分页，并返回 `nextEntryCursor`；工作台点数流水已增加加载更多入口，保留账户、方案和当前订阅信息。
2. 新增订阅到期归档方法，由 Worker 定期执行，重复执行幂等。购买事务会把到期的旧 `active` 记录归档为 `expired`，不会让过期记录阻塞新订阅；延迟续费回调仍可恢复原订阅，但新订阅存在时不会覆盖新方案。
3. 真实 Stripe/Waffo 商户、退款、银行出款和生产并发容量未在本地审核中执行；现有 Provider 测试是隔离 fixture/合同测试，不等同真实资金验收。
4. 工作区存在此前其他功能的未提交修改；本审核没有替用户拆分或提交这些改动。

## 验证证据

- `internal/billing`：11 个顶层测试、12 个子测试通过。
- `internal/creation`：68 个顶层测试、91 个子测试通过，1 个已有 crash-child 测试按测试设计跳过。
- `internal/transport/httpapi`：110 个顶层测试、107 个子测试通过。
- 前端单测：37 个文件、294 项通过。
- 新增回归：`TestCaptureGenerationPointsCannotSpendAnotherReservation` 通过。
- 新增回归：点数流水分页、账户隔离、无效游标/页大小、订阅到期归档及延迟续费恢复测试已加入。
- 测试使用 `TEST_DATABASE_URL` 指向本地 PostgreSQL 隔离数据库，并设置 `HCAI_REQUIRE_INTEGRATION_TESTS=1`；未使用生产数据库或真实支付资金。

完整日志保存在 `data/billing-audit-20260928/`。

2026-10-02 的修复验证：数据库集成测试 `internal/billing`、`internal/transport/httpapi` 和 `cmd/worker` 通过；`TestWaffoSubscriptionActivationAndRenewalWorkflow` 及点数分页、到期归档新增回归单独通过。全量 `internal/payments` 在 `TestSellerBankResumeEligibilityAndAtomicity/runtime` 达到 Go 的 10 分钟超时，因此不能记为全量通过。前端 37 个测试文件、294 项测试、类型检查和 lint 均通过；`git diff --check` 通过。

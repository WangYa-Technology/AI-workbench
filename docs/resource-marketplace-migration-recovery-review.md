# 资源市场目标 7：迁移、worker 与恢复审查

审查日期：2026-09-27。范围是当前工作树（HEAD `aa4c612` 加已有改动，包括 0178/0179），不是仅 HEAD 的已提交源码。本轮只修改文档，不变更业务代码或原运维脚本。源码快照、命令、失败与成功日志保存在 `/tmp/hcai-goal7-20260927/`；每次执行的 `*.manifest.json` 记录文件 SHA-256，`*.meta.json` 记录命令、时间、退出码。

## 边界与升级基线

只使用专用 PostgreSQL 容器 `hcai-goal5-20260926`，映射 `127.0.0.1:55435`。没有连接默认 5432、生产库或真实支付 Provider。新建 `goal7_*` 数据库，重启演练使用其中随机测试 schema；保留演练数据、媒体和备份，没有执行财务证据清理或业务清理脚本。测试自身的隔离库销毁不代表生产清理。

仓库无 release tag，不能证实实际上一生产版本。经用户授权自行选择，本轮采用上一提交 `ad93fd9` 的原始 0001–0177 SQL 作为可核验升级基线；另验证当前源码 0001–0172 基线。两个基线保留升级前任务记录，再用当前 `cmd/migrate` 升级到 0179。正式发布仍需对照实际已部署的镜像摘要、迁移清单及 SQL 哈希，不能把 `ad93fd9` 写成已确认生产发布。

## 迁移运行器与兼容边界

`internal/platform/database/migrate.go:Migrate` 按文件名字典序应用嵌入的 up SQL；单连接持有 session advisory lock `0x484341494d494752`，每个文件和对应版本记录在同一事务提交。两个 API/worker/migrate 进程对同一库启动时串行迁移；锁不是跨数据库全局锁，也不协调业务写入。失败文件回滚，之前已提交文件保留；重启跳过已记录文件。

运行器只有版本文件名和时间，没有 SQL checksum、缺号检查、未来版本拒绝或自动 down；改写已应用 SQL 不会重新执行。`cmd/api/main.go` 和 `cmd/worker/main.go` 都先迁移再提供服务，不能把应用回退当作数据库回退。无迁移专用 lock/statement timeout，等待旧长事务可使启动长时间停留。应由发布窗口设置有界等待，检查锁持有者；不能自动杀业务事务来使升级通过。

前序依赖包括 0076–0097 的订单、支付与交付契约，0110/0115 的租约及 bundle 协议，0118–0134 的退款/查询/保留证据，以及 0143–0165 的结算、账本、提现审核、资金来源、银行目标与命令。空库安装验证了全部 179 个 up 文件；重点依赖与回退限制如下。所有 down 均要求先停写并倒序处理后续依赖，且使用事务；SQL 文件不是可任意挑选的独立回滚单元。

| 迁移 | 前提、锁及约束 | down 的真实范围 |
| --- | --- | --- |
| 0166 银行执行 | 依赖 0165 命令及此前资金模型；dispatch/read/result/check journal；单次 create、单次银行账本索引；延迟 result-consumed 约束 | 显式 ACCESS EXCLUSIVE 锁；任何 dispatch/read/result 或 payout_debit/payout_return 拒绝。不撤销外部银行行为 |
| 0167 银行续办 | 依赖 0166；active-dispatch view、独立财务权限与原始任务校验；resume 只在允许窗口使用原命令 | 有任何 resume 拒绝；空证据才恢复旧 view/guard |
| 0168 执行任务约束 | funding/settlement dispatch 绑定正确 kind/payload 和可执行 job；job-v1 协议 | 可移除 guard，但失去停止任务保护；不是安全在线降级 |
| 0169 租约友好互斥 | SHARE ROW EXCLUSIVE 锁 jobs，回填独立 execution-control rows；kind/payload/status 变更修订控制行，heartbeat 不修改控制行；lock-v1 | 保留资金证据，移除可重建控制表并恢复旧锁函数；重新引入 jobs 行锁与 heartbeat 竞争 |
| 0170 来源撤回命令 | 锁提现请求/任务写入并回填 disposition controls；银行观察与撤回共享请求锁；command-v1 | 任意 reversal command 拒绝；空证据才恢复银行 guard 和旧 execution kinds |
| 0171 撤回执行 | 依赖 0170；journal-v1；dispatch、注册只读观察、result、check 和 maintenance kind | **任何 dispatch 行**即拒绝，范围不只 started_at/first-send 非空；不允许删证据降级 |
| 0172 来源消费 | 依赖 0171；closure-v1；唯一 closure/release、延迟 FK/消费检查，关联 allocation/ledger/event/audit | 任意 closure 拒绝；外部 reversal 和本地已消费释放都不能通过 down 撤销 |
| 0173 拒付根与事件 | 依赖现有支付事件/订单/结算；唯一 Provider 身份、不可变事件 trigger、结算 hold guard、payment/open/event 索引 | 任意 root/event 拒绝；无证据仍须满足恢复后的 settlement status CHECK（不能遗留 dispute_hold） |
| 0174 事件排序 | 依赖 0173；按 occurred_at DESC、provider_event_id DESC 回填；NULL 回填直接失败；NOT NULL + FK | 删除 latest_event_id 与 FK，保留原始事件；丢失排序投影能力。当前 writer 不再兼容 |
| 0175 运营证据 | 依赖 0174；actor/key 和 dispute/version 唯一；operation/submission 不可变；历史及目录索引 | 任意 operation/submission 拒绝；空证据才删除结构及运营投影列 |
| 0176 原因校验 | 依赖 0175；长度 10–2000 的 CHECK | 当前 up/down 文本相同；并非放宽校验或恢复历史错误表达式 |
| 0177 引用校验 | 依赖 0175；长度、ASCII 白名单 CHECK | 当前 up/down 文本相同；0175 已有同等语义。长自动约束名与显式名称截断不同，可留下同义重复约束，不能写成“撤销引用校验” |
| 0178 裁决 | 依赖 0173–0177；复合事件 FK、不可变裁决与身份 guard；已处理终态根入队 replay | 裁决或升级 replay job 阻止 down；不能只回退到 0177 而遗忘任务证据 |
| 0179 异常处置 | 依赖 0170–0172；双人授权、原始快照、24 小时限制、永久 no-POST 处置；修改 read/check/closure guards | 有 disposition 拒绝；回退 0172 前必须先处理 0179，不能绕过依赖删数据 |

这些索引都是普通 CREATE INDEX，不是 CONCURRENTLY；ALTER TABLE/约束验证和 view/function/trigger 替换也会获得 DDL 锁。小型隔离数据测试不证明大表的锁等待、扫描时长或磁盘容量。0174 的排序索引末列是事件行 id，而回填排序末列是 provider_event_id，不能声称索引完整覆盖 tie-break。

## 发布与恢复顺序

1. 保存匹配版本的 SQL/镜像/配置摘要和备份。核对实际版本、未知历史 CHECK、缺失事件、未决银行/来源操作、已有 replay 任务，预先在副本演练。
2. 停止旧财务写入与 worker，等待在途调用结束；对未知外部结果保留原命令和发送记录。0173+ 的新语义也不允许未经验证的新旧 writer 混跑。
3. 在有界维护窗口执行单次迁移，检查全部版本清单、约束/索引/trigger、队列及失败记录。失败时保留前序提交，修复原始证据或用前向迁移，勿改版本行伪造成功。
4. 先启动匹配的新 API/worker，确认 provider disabled/mismatch 保持关闭；观察 readiness、maintenance 和 lease 指标，验证账本、下载/退款状态后再开放写入。
5. 回退应用前逐项检查协议和 schema 兼容性。`database.Open` 对每条新连接设置 bundle/journal/lease、银行 resume、transfer job、execution lock、reversal 和 closure 协议；缺协议的老 writer 会被相应 guard 拒绝，但不是每个新增语义都有二进制版本门禁。**不能向老二进制注入新协议值骗过保护，也不能声称任何新旧混部署都安全。**
6. 已产生资金证据时默认保留 schema、前向恢复。备份恢复是灾难恢复，不会撤销 Provider 已发生的付款；恢复点之后的外部结果须按原商户、对象、命令和幂等键查询对账，禁止重建 key 后重发。

## API、worker、Provider 与多实例

`internal/transport/httpapi/server.go` 注册商品 Checkout/退款、买家订单、卖家提现/银行目标、财务审核/资金/银行续办/来源撤回/关闭/异常处置，以及拒付 list/detail/operations。财务 handler 校验 `admin:finance`，卖家/买家接口校验当前主体和归属；Webhook 使用签名/通道验证而非用户会话。三个真实 HTTP 集成测试覆盖拒付及来源关闭/异常处置入口。

`cmd/worker/main.go` 注册 payment event、product refund/settlement 及 checks、Checkout checks/lookup/identity recovery、seller funding/check、bank/check、source reversal/check；同时注册生成、扫描、交付 cleanup、通知、邮件、导出/删除及 webhook 等 job。退款、Checkout、结算、funding、bank、reversal 各自注册周期 reconciliation；0178 replay 使用已有 payment-event handler，不需新的 kind。未注册 kind 产生 `handler_not_registered` 并受有限重试约束，不是 silently succeeded。

API 与 worker 均使用 `payments.NewServiceFromConfig`。`config.Load` 拒绝 Stripe 启用但缺密钥/签名 secret、test/live 不符、未批准 live、非法 endpoint；生产还拒绝本地 provider、不安全 cookie/数据库 TLS 等。Waffo 缺商户/token 或非法环境会拒绝配置；缺 store/product 等业务配置通过 `providerConfigReady` 关闭商品能力，不能将“进程能启动”等同于支付启用。持久配置禁用、商户/环境不匹配、读库失败或 runtime 缺失均不开放 Checkout；epay 选项不等于已实现 runtime。银行缺 runtime 不消耗 first-send 机会，不能伪造成功。Provider 验证只用了本地模拟，未验证真实商户配置。

`jobs.Repository` 用 FOR UPDATE SKIP LOCKED 领取任务，同事务写 attempt 和随机 lease token；renew/complete/fail 按 owner/token/status 和锁后时钟检查。每次新 claim 更换 token；过期旧 owner 不能续租或终结。默认 worker lease 30 秒、并发 4，lease/3 heartbeat；续租超时取消 handler，终结写也有超时。SIGTERM 取消 context 并等待 handler，最多一个 lease；取消时最终写可能失败，交给过期 lease 恢复，而不是承诺同步完成全部任务。RecoverExpired 每次最多 100 条，保留不合法租约证据，达到 max_attempts 后失败。

普通重试采用 attempt² 秒或错误指定 RetryDelay，支付错误分类区分 retryable、terminal 和 reconciliation。多 worker 的互斥来自领取行锁、执行控制行、请求 disposition 锁、单次发送 journal、唯一账本/命令索引及事务；并非一个永久 leader。周期扫描可多实例运行，依赖事务重新检查和已有任务/唯一证据，不能由“扫描发生两次”推断会支付两次。0169 的独立锁允许 Provider 调用期间续租；停止/修改任务必须等待控制行，并拒绝旧隔离快照。未知结果必须查询，外部副作用不具备数据库事务的 exactly-once 保证。

## 备份、恢复与脚本审查

详见 [备份恢复手册](../deploy/BACKUP_RESTORE.md)。backup 使用 custom dump、0700 目录/0600 文件、临时文件原子 rename、目录验证和 SHA-256；不含媒体对象、角色权限或 secret。checksum sidecar 的生成并非与 archive 一起原子发布。restore 仅接受显式目标和确认串，使用 `--clean --if-exists --exit-on-error`，**非单事务**，失败可能保留部分库；默认只验证 public.schema_migrations 存在和 max(version)，不证明账本、trigger 或对象完整。

没有 sidecar 时现有 restore 仍接受；有 sidecar 才校验，且 sidecar 保存原文件路径，搬运归档后需针对实际选中的文件重新核对可信摘要。它不是签名或可信来源验证，也没有强制目标为空、隔离库名/主机、关闭 Provider 或停止 worker 的保护。应只恢复到新建离线目标，失败后保留目标调查并新建另一库演练，不能把部分库接回流量。`--clean` 只针对归档对象，不保证清掉目标所有额外对象。数据库 URI 会作为 pg 工具参数传递，应使用受控凭据机制，勿把带密码命令放入共享进程日志。

`scripts/worker-restart-drill.sh` 已在隔离库实跑，原版失败：仍 JOIN `billing_reservations`/`billing_entries`，而当前生成使用 `point_reservations`/`point_entries`；随后发现 `.audit.valid` 已不属于当前 diagnostics 响应。原脚本还默认 5432，退出清理会 DROP 测试 schema、删除目录。本轮使用 `/tmp` 副本，固定 55435、移除数据清理、按现行积分账本和 diagnostics 契约校正断言；保留全部其他重启/幂等/资产检查。**原脚本未修复，不能将副本通过写成原脚本通过。** 它验证 worker 重启期间排队任务恢复，不模拟真实支付网络分区；活动 handler 的停止/续租/失租由 Race 集成测试覆盖。

## 本轮验证与可复核证据

证据目录：`/tmp/hcai-goal7-20260927/`。所有命令使用显式测试 URL，Go 测试启用 `HCAI_REQUIRE_INTEGRATION_TESTS=1`。记录器默认 metadata 中 database 是基础测试库，演练真实目标以命令参数、Python 脚本和下表为准；不能仅依赖该固定字段。以下不是全仓测试或生产验收。

| 证据前缀 | 验证范围与结果 |
| --- | --- |
| `upgrade`、`upgrade-resumed` | 首次空库 head 成功；旧库 fixture 错用不存在的 jobs.idempotency_key，SQL 拒绝且未升级该旧库；修正 fixture 后 0172 和 ad93fd9/0177 升级成功。每个目标并发两次 Migrate、再次重启，均 179 条版本、0 无效索引、旧任务各保留 1 条 |
| `backfill` | 0173 真正有数据→0174，等时事件选最大 provider_event_id；0175–0177 空 operations down 成功；0173 有 root/event 拒绝且保留两条事件；0179→0166 空证据 down 链在事务内执行成功再整体 ROLLBACK |
| `failure` | 实际 Migrate 在 0174 缺事件时失败，列及版本行未留下；补齐测试原始事件后重启到 0179 |
| `workers` | jobs、cmd/worker、config 三个包 `-race -count=1` 通过；含并发 claim、长 handler heartbeat、停止等待、锁等待后过期、续租/终结超时、扫描取消 |
| `migrations` | 12 个指定支付顶层测试通过；覆盖空资金 roundtrip、0167/0168/0169、撤回命令/执行/closure/0179 evidence guards，以及 Provider 缺失关闭 |
| `payment-concurrency` | 7 个指定支付顶层 Race 测试通过；执行期间续租、控制变更互斥、旧快照拒绝、并发银行只发一次、已停止任务不发、停止后的撤回只读恢复 |
| `compatibility` | 5 个指定支付顶层测试通过；银行 resume/撤回/closure 协议与当前权限，以及 Stripe 环境/Waffo 商户不匹配关闭 |
| `http` | 拒付、来源关闭、异常处置 3 个 HTTP 顶层测试通过 |
| `restart`、`restart-points`、`restart-current` | 前两次保留原脚本契约漂移失败；当前契约副本通过，SIGTERM/启动、queued→succeeded、同 key 原 ID；最终 `succeeded|captured|1|1|succeeded|1|1` |
| `restore`、`dispute-restore` 及后续 `*-canonical` | 两个新建目标分别恢复含真实 HTTP 演练任务/积分账本的数据和含拒付不可变事件的数据；初次比对脚本 char/text 拼接错误、随后 CHECK 表达式分组差异均保留日志；最终逐表摘要及 schema 对照见结果记录 |

测试命令的完整 `-run` 集合、时间和结果见对应 meta/jsonl，演练 Python/Bash 副本也在该目录。CHECK 比较只归一化已人工核实的 `((a>=min AND a<=max) AND b)` 为等价的 AND 展平；没有移除 OR 分组、跳过约束或禁用 trigger。数据库备份不包含生成媒体，目标 7 不声称完成对象存储灾备、RPO/RTO 或大规模数据库容量验收。

最终恢复结果：768 张表（4 个 schema）及 192 张表（1 个 schema）的全部行摘要一致，约束、索引、用户 trigger 一致；无确认串/错误 checksum 均拒绝，目标未变，archive=0600、目录=0700。

补充 `final-restore-check`/`final-restore-canonical`：函数、sequence 状态及索引有效性一致；view 文本另有已核实的 UNION 分支自动别名和同层 OR 展平差异，仅按这三处精确表达式归一化后全部一致，未改变 AND/OR 混合逻辑。两个恢复库的 Migrate 再启动通过；在恢复的拒付事件表尝试 UPDATE 被不可变 trigger 拒绝，两条原始事件保留。

最终 Go/HTTP 共 71 个顶层测试通过、0 skip（并非 payments 全包）；12 组最终验证记录均退出 0，汇总在 `final-results.json`。初次失败日志均保留。当前非文档源码与首批测试 manifest 比对无漂移；文档相对链接及 `git diff --check` 通过。目标 7 至此完成，仅隔离验证与文档交付，不启动目标 8 或进行部署。

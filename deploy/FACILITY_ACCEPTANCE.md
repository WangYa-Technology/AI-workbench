# 资源市场目标 8：设施联合验收清单

日期：2026-09-27。范围：Stripe、Waffo、银行结果、对象存储、媒体扫描和 SMTP。验收脚本、清单和本地契约/恢复证据已准备；**本轮没有执行真实沙箱交易、商户 API 探测、对象存储写入、远程扫描或测试邮件**。用户的条件式授权不等于具体交易/收件人授权。本轮外网访问仅用于读取下列公开官方文档与 SDK 包。

## 当前配置与安全预检

| 设施 | 本轮配置证据 | 当前可用结论 |
| --- | --- | --- |
| Stripe | 进程未设置启用及凭据；项目默认 disabled | 不可执行真实 Checkout/退款/Connect 验收 |
| Waffo | 未发现启用、merchant/store/product、connector token 或私钥配置 | 不可执行真实验收；卖家 transfer/银行 payout、authenticated refund lookup、pre-snapshot identity recovery 未实现；Checkout lookup 无已核验 merchant schema/query |
| 银行 | 没有 Stripe 测试账户/银行目标/余额证据；没有独立直连银行适配器 | 当前不可用。Stripe bank runtime 已实现不等于银行能力已获授权；Waffo 银行出款不支持 |
| 对象存储 | 当前 local_file，未配置 S3 bucket/region/key | 本地文件验证不等于真实 S3 或兼容厂商验收 |
| 媒体扫描 | 当前 local_deterministic，未配置 HTTP URL/token | 未验证真实恶意文件识别能力或扫描厂商契约 |
| SMTP | 显式读取 `.env.local` 后，SMTP 所需字段存在；没有读取或输出其值 | 仅 configured_unverified。不能证明邮箱是测试目标，未 TLS/AUTH 探测、未发信 |

`.env.local` 不由 Go 配置加载器自动读取；因此分别保留进程预检和显式文件预检。未连接任何现有应用库去读取 Admin Provider 配置；持久配置、connector 独立进程密钥及远程账户状态均须另核对。本表是本轮可见配置，不推断其他部署环境。

新增命令：

```bash
# 可直接运行：无数据库、HTTP、SMTP 或 Provider 调用。
go run ./cmd/facilitycheck
# 可选显式 dotenv；只是读取字面量，不 source shell。
go run ./cmd/facilitycheck -env-file .env.local
# 已安装依赖上的合成契约套件；不会读取 dotenv 或继承支付/邮件凭据。
python3 scripts/facility-acceptance-local.py
```

预检始终输出 `externalCalls=0`、`externalAcceptancePassed=false`；退出 0 只表示配置能被本地解析，不是设施验收通过。`configured_unverified` 保留授权、测试目标、权限和远程契约门禁；金融 live 模式列为 `live_mode_blocked`。配置错误只输出固定错误码；不输出 host、邮箱、merchant、bucket、key 或异常原文。

文件参数覆盖同名进程变量，支持空行、整行注释、`export KEY=value` 和单/双引号字面量。不支持多行、变量替换、shell 转义或行尾注释；重复 key/歧义语法拒绝，失败解析不会部分应用。单引号中的 `$` 仅是字面量。不能用它执行现有 shell 配置脚本。预检不验证私钥真实性、账号权限、目标归属、GraphQL 查询安全或 SMTP 可达性；Waffo 私钥/lookup 查询属于 connector，Go API 环境不含它们是预期部署方式，不要为了让预检通过而把 connector 私钥注入 API。缺项表示此次离线清单尚未证明完整，不替代线上状态判定。

本地套件清除 Provider/SMTP/代理配置，禁用依赖下载，选择已安装且满足 go.mod 的 Go 工具链；意外选择数据库测试会在不可用的 loopback:1 上失败，不会退回默认数据库或静默 skip。需要事先安装依赖；脚本不会安装、升级或调用任何远程验收命令。它用私有临时目录保存源码哈希、JSON/TAP 日志和汇总，Go 无测试/有 skip 也不能报成功。不是操作系统级出网沙箱，范围由固定 fixture 测试选择和清空凭据控制。

## 已读取的官方契约

取回时间为 2026-09-27，URL、响应状态、内容 SHA-256 保存在 `/tmp/hcai-goal8-20260927/official/`。Stripe HTML 提取后核对正文；Waffo 使用官方 npm registry 对应 0.19.1 tarball，SHA-512 与项目 lockfile 一致，四份包内 README/API/Webhook/GraphQL 文档与本地安装副本逐字节一致。未把第三方镜像链接或本地注释当作商户授权证明。

| 来源 | 官方条款与项目对照 |
| --- | --- |
| [Stripe 幂等](https://docs.stripe.com/api/idempotent_requests) | POST key 最长 255，首次执行结果包括 500 会缓存；至少 24h 后可清理，重用已清理 key 可创建新操作。项目银行/撤回发送窗口为 23h，过窗转查询，不以超时证明失败 |
| [Stripe Checkout](https://docs.stripe.com/api/checkout/sessions/create)、[Balance](https://docs.stripe.com/api/balance/balance_object) | session 创建不等于付款完成；balance.livemode 用于认证凭据环境，不从不存在的 Account.livemode 推断模式；项目固定 API 版本 `2026-02-25.clover` |
| [Stripe Webhook](https://docs.stripe.com/webhooks) | 校验原始 body、签名及时间；可能重复、乱序；live 自动重试最长约三天，sandbox 重试规则不同，不能把测试回调次数当上线保证 |
| [Stripe 分页](https://docs.stripe.com/api/pagination)、[限流](https://docs.stripe.com/rate-limits) | starting_after/has_more；官方普通 live 全局 100/s、sandbox 25/s，普通 endpoint 默认 25/s，另有资源/并发限制；这些不是本项目可直接耗尽的预算 |
| [Stripe Payout](https://docs.stripe.com/api/payouts/create)、[Connect 测试](https://docs.stripe.com/connect/testing)、[Transfer reversal](https://docs.stripe.com/api/transfer_reversals/create) | payout 需要可用余额，初始 pending 不等于 paid；test 不向真实银行打款。退款、transfer、银行 payout 与 reversal 是不同操作，不能互相替代；官方测试银行样例仅供获批测试账户使用 |
| [Waffo 官方 SDK 0.19.1](https://registry.npmjs.org/@waffo/pancake-ts/0.19.1) | RSA-SHA256 request/Webhook；包内 Webhook guide：签名输入 t.rawBody、过去 45min/未来 1min，重投沿用原时间戳，默认 3 次重试；项目显式指定 test/prod，避免 SDK 自动试两套环境 key。官网 docs.waffo.com 本次跳转未成功，未据此声称读取在线 merchant schema |
| [S3 PutObject](https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutObject.html)、[ListObjectsV2](https://docs.aws.amazon.com/AmazonS3/latest/API/API_ListObjectsV2.html)、[一致性](https://docs.aws.amazon.com/AmazonS3/latest/userguide/Welcome.html) | 条件写 If-None-Match:*，412 已存在，409 冲突；AWS 建议相应重试，但本应用先核对原 key/digest，不覆盖。List 每页最多 1000/continuation token；AWS 的一致性与权限规则不能自动套用于未确认的兼容厂商 |
| [RFC 5321](https://www.rfc-editor.org/rfc/rfc5321)、[RFC 8314](https://www.rfc-editor.org/rfc/rfc8314) | SMTP 最终 DATA 回复与中断造成的重复风险；submission 隐式 TLS 通常 465。SMTP 250 是服务器接收，不保证入收件箱；不保证按 Message-ID 去重 |
| [项目扫描契约 ADR 0032](../docs/adr/0032-s3-media-storage-and-scanner-boundary.md) | 当前没有配置扫描厂商；项目 contract v1 是对接约定，不是任何外部产品的官方能力认证。厂商 verdict、engine/version、病毒库时效、隐私保留和限流合同仍待取得 |

## 六类设施的逐项边界

### Stripe

- **账户/环境、权限：** 必须是明确归属的 sandbox/test merchant；用只读 identity + balance 核对商户和 livemode，与原始订单快照一致。Checkout/读取、refund、Connect/account-link/transfer 权限逐项申请，不把一把存在的 `sk_test_` 当作已通过权限验证。Webhook endpoint 的 secret、API version、事件集分别核对。
- **金额/币种：** 项目商品 USD、整数 cents；最小商品 Checkout 50 cents。已有 stripecheck 只创建 50-cent unpaid session 随后 expire，不支付、不退款。任何新的资金测试须单独列金额、次数、累计上限和账户，不能把这个 smoke check 扩展成付款授权。
- **超时/重试、签名：** runtime HTTP 默认 20s；验收总 3min、清理独立 10s；不随重定向转交 secret/POST。Webhook HMAC-SHA256/timestamp，默认 300s（配置 60–900s），原始字节签名；错误模式/版本/金额不入账，已验签但冲突证据按 quarantine/review 路径留存。
- **分页、限流：** Checkout/refund 等查询采用 100/页、最多 10 页；超限、不完整、重复 cursor/对象、多个候选不是“无结果”。429 等保留分类和等待，不能用并发突发来验证官方限额。
- **幂等、故障：** 原 key + 不可变 request/account/environment 绑定。连接断开、响应丢失、服务重启、回调先到及重复/乱序需得到同一订单、一次财务消费；重启不生成新支付 ID。认证失败/参数失败不盲重试；未知结果查询原对象，不能凭 SDK 抛错重发过窗 POST。

### Waffo

- **账户/环境、权限：** 锁定 `@waffo/pancake-ts@0.19.1`；merchant/store/one-time product、test 环境、connector token、私钥由 connector 独立保管。`/checkout/identity` 是 connector 配置声明，不是远程 merchant health。核验真实商户 GraphQL schema 和只读权限之前，`WAFFO_CHECKOUT_LOOKUP_QUERY` 保持空。
- **金额/币种：** 项目核对 USD 整数 amountCents；SDK/merchant schema 的价格、税、折扣与总额必须对账，不从 decimal total 猜 cents。退票 ticket 状态不证明资金已经退回。
- **超时/重试、签名：** connector transport 20s（含响应读取）、1MiB 响应界限、拒绝 redirect，继承请求取消；HTTP 错误不会被 SDK data 字段伪装成成功。Bearer 只用于内部边界，外部 RSA 原文验证固定环境并回传 digest proof；确认 key rotation 和 SDK 公钥来源。
- **分页、限流：** lookup 最多 10 页/1000 行/每页 100，显式 pageInfo 与 cursor，返回完整金额/时间/归属。官方商户级限额未获得；不能杜撰具体 QPS，也不能开启未经商户 schema 验证的查询。
- **幂等、故障：** SDK Checkout key 使用 60s 时间桶；跨分钟相同 body 不是稳定远端幂等。退款 correlation ID 也不是已证明的远端幂等。项目依赖一次性 durable dispatch permit：未知结果、permit 提交后崩溃或本地提交失败都只能进入 reconciliation；不能重建 permit。Checkout found 仍必须精确匹配原订单；缺 refund lookup/pre-snapshot recovery 的路径继续不可用。卖家划转、Connect、银行出款不支持，禁止借 Stripe fallback 假装 Waffo 实现。

### 银行结果（Stripe Connect payout）

- **账户/环境、权限：** 测试 connected account、正确 platform merchant、manual schedule、当前 verified bank target、USD 可用余额、独立财务批准及命令快照均为发送前门禁；bank target 被撤销、账户 restricted 或权限降级不得继续发送。没有单独 bank adapter 或真实银行连通性证据。
- **金额/币种：** 原不可变 payout_request 的 USD cents，runtime 范围 1–99999999 不是允许的验收预算；实际金额必须在任务授权中单列。平台 transfer 成功不等于银行 paid。
- **超时/重试、签名：** identity/银行 HTTP 子调用有 10s 边界；23h 发送窗口，原始一次性 journal 和当前任务控制互斥；银行 read 不受当前账户是否还能新发 payout 的错误限制。Stripe 签名/鉴权不因人工结果输入而被替代。
- **分页、限流：** unknown-ID lookup 100/页、最多 10 页，限制原发送时间窗；重复、缺页、零/多候选或不可绑定返回保持 review/incomplete，不能选择第一笔。
- **幂等、故障：** paid、failed/canceled、pending/in_transit、paid 后 returned 必须分别验证；响应丢失后只读 query，禁止将新的 job ID 当新付款。并发 handlers 不重复外发/入账；停止任务阻止 first-send，独立 execution lock 允许 lease renewal。实际银行返回周期与 payout 费用未在本轮验证。

### 对象存储

- **账户/环境、权限：** 测试 bucket 与独立 prefix，最低 PutObject/GetObject（HEAD/Range）/DeleteObject，inventory 才需要受 prefix 限制的 ListBucket；禁止公共 ACL 和跨租户读，核对厂商/region/endpoint、SSE 和保留策略。SDK SigV4 + TLS；密钥只在受控环境，不写日志。
- **金额/币种：** 不适用；存储/请求/流量成本仍需测试预算。限制对象数量、字节、调用数及测试 prefix；不得拿生产对象或执行 inventory purge。
- **超时/重试：** HTTP 45s，SDK standard 最多 3 attempts；外层 deadline 另算，实际线级请求数可能多于操作数。Put 用 If-None-Match:*、SHA-256 checksum/metadata、AES256；409/412 先读取原 key 并核对完整摘要，不覆盖或换 key 隐藏未知结果。Range 响应必须匹配请求。
- **分页、限流、幂等：** S3 list 受 max-keys/token 和项目 inventory 边界约束；不完整列表不能证明“对象不存在”。厂商 429/503、区域重定向、校验和错误、未知 PUT 成功/DELETE 成功分别留证；无 bucket 版本化/对象锁/跨区恢复实际验收。

### 媒体扫描

- **账户/环境、权限：** 独立测试 scanner endpoint、Bearer token、获准的合成文件，不发送用户原件；明确病毒库/engine/version、数据驻留、保留与人工 review 含义。无厂商配置时只能证明项目适配器行为。
- **金额/币种：** 不适用；固定样本和扫描字节/次数预算。EICAR 等样本仅在已获批准的扫描测试设施使用，本轮未向外发送。
- **超时/重试、签名：** 默认 30s，配置范围 1–120s；contract 1 请求带 object key、content SHA256，响应 digest 必须相同，仅允许 clean/review/rejected，完整 JSON ≤16KiB，engine/version/reason 闭集格式；Bearer+TLS 和 digest binding 不是独立响应数字签名。无 redirect；429/5xx/transport 可重试，其余永久失败转受控 review，不能默认 clean。
- **分页、限流、幂等：** 单文件响应，无分页；厂商配额未知。相同 snapshot/bytes 重试不能为新版本写入旧 verdict；过期 worker/lease 不能提交 clean。重启后 pending/review 与通知/audit 原子恢复需验收。

### SMTP

- **账户/环境、权限：** 确认当前配置对应测试 mailbox/tenant；明确发件身份、获批收件人、供应商端 SMTP 开关及授权密码。项目是隐式 TLS、TLS≥1.2、AUTH LOGIN；不支持把 STARTTLS-only:587 当作同一契约。DNS SPF/DKIM/DMARC、供应商额度与投递审计尚未核验。
- **金额/币种：** 不适用；只允许批准的收件人和条数，不能批量发用户或复用密码重置邮件作为 smoke test。
- **超时/重试、签名：** connect 15s、Send context 20s、mailcheck 总 30s；稳定 Message-ID 辅助客户端去重，SMTP 无通用幂等键/分页；TLS 认证不等于邮件内容数字签名，DKIM 由实际服务配置决定。
- **限流、故障：** 4xx 临时拒绝、5xx 永久拒绝、AUTH/TLS 错误、DATA 后 ACK 丢失、发送成功但 DB 提交失败必须分别验证。当前 mailer 将 transport 发送错误统一为 `smtp_delivery_failed`，队列有限重试/死信，不具备对所有远端 4xx/5xx 的细分和 exactly-once；同 Message-ID 重发仍可能重复送达。250 只证明接收，最终 inbox/退信需额外证据。

## 分阶段清单与执行门禁

| 阶段 | 必需结果 | 本轮状态 |
| --- | --- | --- |
| 离线 | 六类配置清单、官方契约、权限/预算、脱敏模板、fixture 成功/拒绝/超时/重复/恢复边界 | 已完成，实际测试范围见下文 |
| 目标确认 | 明确测试账户/环境/收件人/bucket prefix；原始 merchant identity 和实际启用能力；人工授权的操作、金额/次数与清理边界 | 未提供，阻止真实设施调用 |
| 只读预检 | Stripe identity/balance；Waffo 已批准 schema；S3 权限与对象 prefix；scanner health 合同；SMTP TLS/AUTH | 本轮未执行。已有 mailcheck 不带 send 仍会认证远程 SMTP，不能当作离线工具 |
| 有限副作用 | 按批准测试案例产生对象/付款/邮件，脱敏保存每步请求与响应、消耗预算、未决资源 | 本轮未执行，不能用本地 pass 代替 |
| 恢复/收尾 | 中断前后同命令、只读恢复、完整分页、一次本地入账；对象/邮箱清理和 remaining-unknown 清单 | 合成与隔离 DB 验证通过；真实设施仍待验收 |

每类真实设施至少执行：成功、认证/权限拒绝、非法输入、限流、超时/响应丢失、重复请求/回调、服务重启、配置/凭据改变、恢复完整性；不适用项明确写 N/A 原因，不能空白计为通过。故障注入仅用本地代理/官方测试 fixture 和批准目标，禁止压测真实限额或修改生产凭据。

已有工具的真实调用范围：

| 工具 | 会做什么 | 执行前仍须补齐 |
| --- | --- | --- |
| `cmd/stripecheck` | staging/test/官方 endpoint；最多 6 次应用请求：读环境、建 unpaid Checkout、expire、建 Connect、link、delete；不确认付款 | 本次明确授权、受控 test merchant、请求/清理预算；retain pendingCleanup/unconfirmedCreations；不是退款/银行联合验收 |
| `services/waffo-connector/inspect-schema.mjs` | 已认证只读 GraphQL schema 取证 | 真实 merchant 配置与只读授权；不据字段“看起来类似”直接启用 lookup |
| `cmd/mediacheck` | 一个随机对象 Put/重复写/HEAD/GET/Range/scan/delete/确认不存在 | 没有 staging-only 或线级请求预算门禁；必须明确测试 bucket/prefix 与 scanner；本轮未调用 |
| `cmd/mediaappcheck` | 应用上传/扫描/下载等集成验收 | 仅专用隔离应用数据库/对象前缀，查看命令的真实写入和清理范围后授权 |
| `cmd/mailcheck` | 无 flag 时 TLS/AUTH；`-send-test` 向配置 SMTPFrom 发一封 | 明确批准邮箱、一次发送与隐私范围；不会把“存在 SMTP 密码”当作发信授权 |
| 银行/Waffo 金融 | 没有获批的真实端到端一次性预算 runner | 不直接运行 fixture shell 脚本代替银行验收；先按业务 API/财务流程形成可审查步骤和预算 |

`media.RunAcceptance` 在 Put 返回错误时不能证明对象没写入（stored 尚未置 true），失败时 cmd/mediacheck 不输出完整 result；自动清理没有与总验收共享的严格请求上限。真实执行前须补足持久 attempt/key 引用、未知写入只读核验和有界清理证据，不能直接把这个工具当成灾难恢复验收。其已确认对象清理失败也不能通过重跑并换随机 key 来掩盖。此处明确阻塞真实调用，未扩大本轮为适配器重构。

## 脱敏证据与本轮结果

使用 [证据模板](facility-acceptance-evidence.example.json)。真实证据按 case/run/attempt 关联，保留 UTC、source manifest、账户/目标的受控引用、环境、方法/路由模板、签名检查结果、请求预算和次数、HTTP/SMTP 状态、有限错误码、对象/幂等键的哈希引用、分页完整性、重启/恢复和清理状态。受控内部登记表才保存真实对象 ID 与幂等键以便查询；共享报告不含 secret、Authorization/签名原文、Checkout/link URL、完整邮箱/银行号、用户文件、任意 Provider error body。摘要只用于关联，不代替金额/身份绑定校验。

本轮 JSON 预检属于 **offline**；下面是 **loopback request/response assertions**，不是远程调用抓包。请求与响应的实际字段由对应测试在本地接收端/适配器断言，日志记录测试结果；未伪造远程 HTTP request-id 或银行凭证。

| 设施 | 脱敏请求/响应断言与故障证据 |
| --- | --- |
| Stripe | `TestStripeAcceptance*`：mode read 先于写、6 请求预算、失败/取消清理、unknown creation 保留原 key；`TestStripeCreateCheckoutContract`/`TestStripeFailureClassificationAndBounds`：API version/幂等请求与响应约束、401/429/5xx/超限分类；Webhook 原 body/签名/容差与重复事件 |
| 银行 | `TestStripePayout*`/`TestStripeTransferReversal*`：原 merchant/mode/amount/currency/account 绑定，lookup 分页、HTTP 超时/取消/redirect、禁止 transport POST replay；DB 的 lost-response/并发 handlers 验证只读恢复与一次账本 |
| Waffo | SDK Node 合成 checkout/refund/lookup/webhook：身份、精确金额/时间、原始 digest proof、环境签名、超限/HTTP 错误/取消；Go dispatch uncertainty 证明即使重启丢 permit 也不再 POST |
| S3 | `TestS3StoreSignedImmutableLifecycleAndRange`、`TestS3RequestsNeverFollowRedirects`、`TestS3ReadResponseMustMatchRequestedRange`：签名/条件写、校验和、冲突、Range、禁止重定向；inventory 分页边界用合成对象验证 |
| 扫描 | `TestHTTPScanner*`：Bearer/contract/digest 请求、clean/review/rejected 响应绑定；503/429、错误摘要、过大/尾随 JSON、超时/取消/redirect；scan recovery 与 market admission 保持 pending/review 安全 |
| SMTP | `TestSMTPMessageUsesConfiguredSenderAndPreservesUnicode` 和不可信 TLS 拒绝证明报文/Message-ID/错误脱敏；delivery fake Sender 失败重试/死信/撤销验证本地工作流，**不证明真实 SMTP AUTH 成功、250 后丢 ACK 或收件箱送达** |

运行证据根目录 `/tmp/hcai-goal8-20260927/`。`process-preflight.json` 和 `local-preflight.json` 仅含脱敏清单；`official/` 有公开来源快照与 hash。每组 `*.meta.json`/`*.manifest.json`/`*.jsonl` 保存命令、源码与时间。fixture runner 自己输出的私有目录包含完整 JSON/TAP 和 results；记录器的默认数据库字段不代表离线套件曾连接数据库。持久恢复选择仅连接专用 127.0.0.1:55435 测试容器，所有 Provider/Sender 都是合成 fixture。

最初 preflight 测试的 scanner token 长度不足被配置门禁拒绝，修正测试数据后通过。最初本地 runner 因本机默认 Go 1.26.5 小于 go.mod 的 1.26.8，随后因禁网 checksum 模式未能使用 shim 而失败；最终改为直接选择已安装的 1.26.8 工具链，在禁止依赖下载条件下通过。没有跳过失败或放宽应用门禁。

本轮完成准备与本地验收边界，不标记任何设施为真实联合验收通过。真实商户/银行/存储/扫描/邮箱的成功、错误、恢复、费用与清理证据仍须在具体目标和本次操作授权齐备后取得；未配置或未实现能力保持关闭。

### 最终执行记录

| 命令/证据 | 实际结果 |
| --- | --- |
| `go test -race ./cmd/facilitycheck`（`offline-preflight`） | 4 个顶层测试通过：不泄露配置、不发外部请求、拒绝歧义 dotenv/partial apply、live 与缺配置不授予验收资格 |
| `python3 scripts/facility-acceptance-local.py`（`local-contracts-final`） | 63 个 preflight/media/mailer/config + 42 个 payment 顶层 Go Race 测试，0 skip；38 个 Waffo Node tests，0 skip。已包含上述 4 个 preflight 测试，不重复计数 |
| `durable-recovery` | 10 个指定 Go Race 顶层测试通过；只连接专用测试容器，合成 Provider/邮件 sender，0 skip |
| `checkout-webhook` | 8 个指定 Go Race 顶层测试通过；Checkout/refund/transfer、错误分类、分页、签名、重复 callback/mode/version，0 skip |
| `go build ./cmd/facilitycheck ./cmd/api ./cmd/worker ./cmd/stripecheck ./cmd/mediacheck ./cmd/mailcheck` | 通过；只编译，没有执行外部验收命令 |
| `npm --prefix web run build` | typecheck + Vite build 通过；未修改前端业务 |

合计 123 个不同 Go 顶层测试、38 个 Node tests。最终 fixture runner 证据目录为 `/var/folders/11/cwpsvskj1_q6bfjnw3zgk7s00000gp/T/hcai-facility-local-4xf0y_be`；完整额外测试选择保存在 `/tmp/hcai-goal8-20260927/*.meta.json`。前端构建不是浏览器旅程验收，所有上述结果都不是远程设施 pass。

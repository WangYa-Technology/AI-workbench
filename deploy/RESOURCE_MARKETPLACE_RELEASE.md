# 资源市场目标 10：生产就绪评审与发布手册

评审日期：2026-09-27。**结论：NO-GO；本次交付审查包，不部署。** 当前对象是 HEAD `aa4c6125e7a26d80870e217bb9fec9004df5bdd8` 加未提交工作树，并非已签署的发布提交。没有生产部署授权、实际目标清单、具名值班人或真实外部设施验收，不能把隔离测试通过写成“可上线”。

- **可上线：当前不满足。** 下列全部适用门槛通过、发布负责人和财务负责人签署、具备本次部署授权后才可判 GO。
- **受限上线：当前也未批准。** 在基础设施、安全、数据恢复、告警与容量门槛通过后，可另行申请只读展示/非资金功能；必须在服务端/入口关闭未验收写入，不能只隐藏按钮。已有金融义务仍需保持可核对、可恢复，不能以“关闭付款”掩盖欠款。
- **阻塞：当前状态。** 发布镜像未构建成功、全仓最终回归不全绿、真实 Provider/存储/扫描/邮件/银行及告警接收未验收，RPO/RTO/容量和责任人未签署。

机器可读门槛见 [release-gates.json](resource-marketplace-release-gates.json)。`reviewComplete` 表示本次评审交付，不表示生产门槛已通过。

## 1. 目标 1–9 的实际证据

| 目标 | 核验结果 | 发布含义 |
| --- | --- | --- |
| 1 源码/文档基线 | 已有基线矩阵；当前新增 0178/0179 和多处未提交实现，仓库无 release tag | 必须形成新发布 SHA；旧文档版本不自动覆盖新源码 |
| 2 拒付运营 API/UI | 运营功能存在；早期 `/tmp/hcai-goal2-*` HTTP 日志有失败，不能当 pass；目标 6/7/9 后续实际 HTTP 合同和目标 9 浏览器操作通过 | 内部操作不决定 Provider 裁决、不代表已提交外部证据 |
| 3 拒付裁决 | [裁决审查](../docs/product-payment-dispute-adjudication-review.md)记录 0178、定向 Race 与迁移；包含在后续 payments 验证范围 | Stripe 真实裁决、银行资金协同验收仍欠缺 |
| 4 来源撤回处置 | [来源手册第 9 节](../docs/resource-marketplace-source-reversal.md#9-目标-4异常处置双人复核与只读恢复01792026-09-24)有 0179 双人处置/只读恢复和限制 | 不能把人工批准当作外部撤回或释放凭据 |
| 5 全仓/Race | `continuation/race-controlled.jsonl`：613 个 payments 顶层测试通过，包耗时 1481.643s，0 fail/skip。`all-controlled` 全仓在 payments 默认 10 分钟超时，退出 1；更早 `all-current.log` 含连接清理超时 | 没有当前发布提交的全仓全绿证明。不得用定向测试或加总不同快照替代。Makefile 已有有界 60m 配置，发布前须在专用资源上运行并保留最终退出码 |
| 6 安全边界 | [源码安全审查](../docs/resource-marketplace-security-review.md)：支持操作撤权竞态已修复；真实 HTTP 回归、8 个相关包及前端 257 项通过 | 支持只能记录指导，不能退款/改授权。范围外风险未被声明消除 |
| 7 迁移/恢复 | [迁移恢复审查](../docs/resource-marketplace-migration-recovery-review.md)：179 个 up；0172 与上一可核验提交/0177 升级；有证据 down 拒绝；恢复、租约和互斥验证 | `ad93fd9` 是仓库基线，不是已确认的线上上一版本 |
| 8 联合设施 | [设施清单](FACILITY_ACCEPTANCE.md)及本轮离线 preflight：Stripe/Waffo disabled，银行 unavailable，本地存储/扫描；`.env.local` SMTP 曾显示 configured_unverified | 无真实商户/银行/对象存储/扫描器/邮箱验收；该文件不代表生产部署配置。未向外发信/交易 |
| 9 E2E | [验收记录](../docs/resource-marketplace-e2e-acceptance.md)：相同源码两轮各 16/16、32 份有效 trace、106 张截图；临时数据/进程清理 | 证明真实本地 Go/PostgreSQL/worker 路径；Stripe 为 loopback 仿真，提现仅验证预留与释放，不证明银行到账 |

本轮检查目标 5 的完整 manifest，已登记路径中变化的业务文件为支持授权实现/HTTP 映射和 seed；不能据此推断整个当前发布（含后来新增文件）已被旧全仓测试覆盖。原始失败日志保留。历史材料的快照和测试范围应与最终发布 SHA 逐项比对。

## 2. 发布身份、构建和配置

当前 HEAD 与 dirty 清单、源文件 SHA-256、构建与检查日志在 [本轮证据目录](../data/release-review-20260927/)。本轮 API/worker/migrate/configcheck 原生构建及前端 typecheck + Vite build 成功；前端仍有大于 500 kB 的 chunk 提示，须纳入弱网/窄屏性能验收。

**发行镜像未通过：** `deploy/Dockerfile.runtime` 构建 migrate 在拉取 `alpine:3.22` 的 Docker Hub token 时超时（32.62s）。构建循环在此停止；没有本轮成功的 API/worker/migrate/web 容器摘要，也没有推送。旧本机 `verify-0134`/`local` 镜像不可作为 0179 发布产物。不得以原生构建冒充 Linux 容器/架构验证。

发布负责人必须从冻结、干净、已审核的提交构建四个镜像；记录 Go 1.26.8、Node 24 容器构建工具链、lockfiles、base image digest、目标 CPU 架构、镜像 OCI revision 和 registry digest；运行 `scripts/check-runtime-images.py`。标签可移动，切换只能使用 `repository@sha256:...`。CI 的 amd64/arm64 构建与供应链检查必须覆盖最终提交；本轮未触发远程 CI。

[发布 Compose 覆盖文件](compose.release.yml)保留基础模板的只读根、非 root、cap_drop/tmpfs，要求显式镜像/CPU/内存预算；API 停止宽限 30s、worker 60s。原模板默认停止宽限通常只有 10s，短于 worker 最多 30s 的退出等待。该覆盖文件已用合成 digest 做离线 `config --quiet` 验证：缺变量失败、齐备后可渲染，没有启动容器。配置校验不验证 digest 内容或容量足够。

必核配置：

| 配置/能力 | 要求和停止边界 |
| --- | --- |
| 生产基础 | `APP_ENV=production`；数据库 `sslmode=verify-full`；公共 HTTPS WEB_ORIGIN；COOKIE_SECURE=true；LOCAL_PROVIDER_ENABLED=false；可信代理 CIDR 只含实际代理；生产 S3 与 HTTP scanner 必须配置 |
| 支付选择 | 同时核对部署环境与 `/api/v1/admin/payment-providers` 持久配置，保留原 Provider 身份。`/api/v1/meta` enabled 只是准入结果，不是商户验收凭证 |
| Stripe | API key / whsec / pinned API version / merchant / livemode / Connect 权限 / webhook endpoint 与事件集合逐项一致。生产 live 还需要 `STRIPE_LIVE_MODE_APPROVED`；该变量不是本次用户部署授权 |
| Waffo/银行 | 无配置/未实现能力必须不可用。Waffo seller transfer、bank payout、authenticated refund lookup 等不得开放。Stripe 银行实现存在也必须完成已批准账户、manual payout schedule、银行目标与测试余额验收 |
| 收口新业务 | 持久 Provider 配置和经验证的入口写入规则用于暂停新 Checkout；现有订单的签名接收、查询、退款/恢复需要原商户密钥与匹配 runtime。不得直接更换 merchant/mode 来“恢复”旧单 |
| 紧急总停 | `STRIPE_ENABLED=false` 等进程级禁用也可能关闭历史查询/恢复，**不是只停新售的开关**。停止整个 worker 会同时停通知/扫描/财务；当前没有已验证的“只读财务 worker”启动选项 |
| 上传/生成/邮件 | 无相应真实设施验收时关闭入口；local_deterministic 不能作为生产病毒扫描，local_file 邮箱不能作为邮件投递。当前未提供统一 maintenance/read-only 开关，入口维护规则必须在预发布实测 |

### 密钥轮换

密钥负责人登记 secret manager 引用、版本、用途、owner、失效日和回退窗口，不在审查包中记录值。先在预发布用合成数据演练，再批准生产变更：

1. Stripe API key 轮换保持原 merchant/mode/API version 与能力；以只读身份核验和历史查询验证后，协调 API/worker 切换。保留旧 key 至观察结束再吊销。Waffo 私钥属于 connector，不注入 API。
2. Stripe webhook 配置目前是单个 secret；不能假定自动接受新旧 key。应先验证 Provider 重投与双端点/受控停收方案，或实现并审核多 key 窗口；没有该证据时不得在滚动发布中直接替换 whsec。
3. `WEBHOOK_ENCRYPTION_KEY_B64` 与 `EMAIL_ACTION_ENCRYPTION_KEY_B64` 必须独立。代码用单个 AES key 解密已有密文，未见通用 keyring/批量重新加密发布工具；环境变量替换会破坏存量解密。首次部署生成与既有密钥轮换不同；存量轮换必须有离线备份、受控重加密/待发送邮件处置方案及完整恢复验证，当前列为未验收。
4. SMTP/S3/scanner/DB 凭据按最小权限、短重叠窗口切换，验证原对象读、扫描和邮件失败路径；不要同时变更商户身份、Provider endpoint、schema 和根加密 key。数据库备份不包含这些密钥，恢复必须匹配密钥版本。

## 3. Go/no-go 门槛与责任

“角色”是应负责的岗位，不是已经指定的人。**所有具名主责、备份联系人、接收通道、批准记录当前均未提供，须填入工单后方可放行。**

| ID | 门槛 / 必须提交的证据 | 主责 / 独立复核 | 当前 |
| --- | --- | --- | --- |
| G01 | 最终审核 SHA、干净源码、四镜像 digest/架构/供应链检查、同源构建；上次实际发布 manifest | 发布负责人 / 安全负责人 | BLOCK：dirty、无正式版本、本次镜像失败 |
| G02 | 最终提交全仓 Go、payments Race、HTTP、前端、隔离 E2E 均成功；资源/超时有界、无漏测 | QA / 后端负责人 | BLOCK：专项和 E2E 通过，最新全仓成功缺失 |
| G03 | 真实部署版本/SQL 哈希、数据规模副本升级时长与锁、备份点、旧 writer 清单、兼容回退/前滚签署 | DBA / 后端负责人 | PARTIAL：隔离 0177→0179 通过；实际线上基线未知 |
| G04 | 环境/merchant/mode/权限/金额预算/签名/未知结果与历史义务查询逐项验收 | 支付集成负责人 / 财务负责人 | BLOCK：只有本地仿真；Waffo/银行未验收 |
| G05 | S3/扫描/SMTP/HTTPS/数据库 TLS/权限/密钥版本和轮换演练 | 平台与安全负责人 / DBA | BLOCK：无目标设施证据 |
| G06 | 告警发出→真实接收→主备 ACK→升级路径→恢复通知，有合成告警 ID/时间线 | 值班负责人 / 发布负责人 | BLOCK：只验证检测器，无接收人/ACK；本轮未发送消息 |
| G07 | 原商户/环境/币种分账对账、旧未决清单及 maker/checker、资金差异为零或明确暂停并处置 | 财务负责人 / 独立财务复核 | BLOCK：无实际商户结算/银行账单 |
| G08 | 加密异地备份/保留/WAL 或等效策略、媒体版本/key 同步恢复、业务 RPO/RTO 与演练 | DBA / 存储负责人 | PARTIAL：384 表/5 文件隔离恢复；没有生产 RPO/RTO |
| G09 | 同规格预发布峰值及 2 倍预期负载/大历史数据/弱网/限流/磁盘/DB 连接预算与容量余量 | 平台负责人 / QA | BLOCK：只有 100 个 readiness 请求的诊断样本 |
| G10 | 本次目标/维护窗口/主备值班表、停止权限、批准的回退分支、明确部署授权 | 发布指挥 / 业务与财务负责人 | BLOCK：未提供 |

任一适用 BLOCK 或签署缺失即 NO-GO；不以“其他项多数通过”抵销。受限上线需要单独审批哪些功能关闭和已有义务怎样处置，不能移除 G01/02/03/05/06/08/09/10。

## 4. 可重复的隔离演练

新入口 [scripts/release-rehearsal.py](../scripts/release-rehearsal.py)只接受显式的 `127.0.0.1/hcai_e2e_admin` 或本项目既有 `hcai_goal5` 专用库。新建三个随机物理库；不加载生产 dotenv 或 Provider 凭据，不复用运行中的服务。保留数据库、媒体、备份和二进制供复核，退出时停止本轮服务进程；不自动清理历史财务证据。

```sh
REHEARSAL_DATABASE_ADMIN_URL='postgres://hcai:hcai@127.0.0.1:55435/hcai_e2e_admin?sslmode=disable' \
  python3 scripts/release-rehearsal.py
```

本轮在既有专用 55435 容器执行，证据目录：
[`hcai_release_7d4f1dbdebd444e985637740ea7afaa7`](../data/release-rehearsals/hcai_release_7d4f1dbdebd444e985637740ea7afaa7/)。

| 演练 | 实际结果 / 限制 |
| --- | --- |
| 空库 / 旧基线升级 | 179 个 up；`ad93fd9` 原始 177 个 SQL → 当前 0179，全版本列表一致；保存原 SQL 哈希。不是实际生产版本验证 |
| API/worker 协调 | 两个 worker 启动/正常停止；停 worker 时入队为 `queued\|held\|0\|0`，双实例恢复后 `succeeded\|captured\|1\|1`，原 key 返回同 generation ID；非真实资金 |
| 告警检测 | 正常 `/metrics` checker 退出 0；API 停止后退出 1。无真实接收人/发信/呼叫，不声称告警送达 |
| 备份/恢复 | 空新目标运行仓库 backup/restore；384 张表逐表行数与行摘要一致，索引有效，5 个媒体文件 hash 相同；恢复后 API 下载字节及原 key 重试一致 |
| 恢复耗时 | dump 1.366s、restore 10.414s；仅本地小样本工具耗时，不是 RPO/RTO。未模拟 WAL/PITR、跨区、对象版本回放或备份后真实通道事件 |
| 诊断负载 | 100 次 readiness、并发 4，p95 7.61ms；该轻接口不测 Checkout/财务目录/导出/扫描/慢客户端，不能推导生产 QPS |
| 当前源码定向 Race | config/jobs/worker/observability 46 个顶层测试（278 含子例）通过；支付租约/并发/停止和迁移回退 guard 7 个顶层（15 含子例）通过，均 0 fail/skip |
| 发布配置/只读检查 | 覆盖 YAML 合成渲染通过；[只读 SQL](resource-marketplace-release-checks.sql)在恢复库实际运行成功 |

旧 `scripts/worker-restart-drill.sh` 的 billing 表/diagnostics 断言仍过时，不再作为本次发布证据；本轮入口采用当前 point 模型。已有目标 7 的迁移 down 证据和本轮相应 Race 用例证明应拒绝有证据降级，不是证明旧二进制可以任意回退。

## 5. 已获授权之后的发布顺序

以下是供批准后在预发布先执行的操作流程；**本轮未执行任何生产命令**。目标地址、secret 文件、镜像 digest、项目名和边缘流量操作必须填入发布工单。没有实际 LB/入口配置时不能伪造“维护开关已验证”。

### T−1 天至 T−60 分钟：冻结与预检

1. 发布指挥确认范围、主备人、维护窗口、已签署 G01–G10。核对实际线上镜像/SQL 清单，不能只查询 `max(version)`；保存当前订单/付款/提现/退款/拒付/来源撤回未决清单和队列。
2. 干净 checkout 构建/推送候选镜像和配置检查器，完成漏洞扫描及同规格 staging 演练；冻结镜像 digest、迁移 up/down 哈希和 secret 版本引用。示例检查：

```sh
set -euo pipefail
: "${RELEASE_SHA:?approved release SHA required}"
test "$(git rev-parse HEAD)" = "$RELEASE_SHA"
test -z "$(git status --porcelain)"
# 在专用 CI 数据库运行；不可使用生产 URL。
TEST_DATABASE_URL="$CI_DATABASE_URL" HCAI_REQUIRE_INTEGRATION_TESTS=1 make test
TEST_DATABASE_URL="$CI_DATABASE_URL" HCAI_REQUIRE_INTEGRATION_TESTS=1 \
  go test -race ./internal/payments -count=1 -timeout=60m
make lint
make security
# 构建四个版本化镜像；镜像必须出自同一 SHA，记录 --iidfile 和 OCI revision。
# 使用本项目 Dockerfiles，随后运行架构检查并获取 registry digest，不能用 :local 切换。
```

3. 对已注入环境的发布版 `configcheck -require-production` 做离线配置检查。`facilitycheck` 只做脱敏清单，不能代替真实设施验收。
4. DBA 创建一致备份点，离线目标恢复 DB/对象/密钥版本并验收，记录完整恢复时间。与财务记录 `Tbackup` 后外部对账窗口。备份文件及 checksum 必须经独立保管/认证，缺 checksum 不执行 restore；原脚本本身未强制此项。
5. 填好镜像覆盖变量并检查四个值均为 `@sha256:<64 hex>`，用仅可读配置目录供 compose 使用。设置内存/CPU 为已经在 staging 测得的预算，不照抄本轮合成渲染值。

```sh
: "${RELEASE_PROJECT:?approved target project required}"
: "${HCAI_PRODUCTION_ENV_FILE:?protected target env file required}"
DC=(docker compose -p "$RELEASE_PROJECT" -f deploy/compose.production.yml -f deploy/compose.release.yml)
"${DC[@]}" config --quiet              # 不输出展开后的 secrets
"${DC[@]}" config --images             # 人工核对四个不可变摘要
"${DC[@]}" pull                        # 只取批准镜像；此阶段仍不启动
```

### T0：收口、停旧 writer、迁移

1. 边缘操作员按 staging 已验证规则暂停新 Checkout/上传/发布/财务命令等写入；等待 API 在途请求结束。向用户显示维护状态。Webhook 仍交给有效接收端持久化；停接收的窗口明确返回非 2xx 让 Provider 重投，不能返回 200 丢弃。
2. 停旧 worker 再停旧 API；确认每实例退出、运行租约和首次发送 journal。Provider 未知结果登记为待核对，不能按“进程已停”视为未发送。强杀必须记录 lease/token/对象/原键，不重置 attempts/journal。
3. 单次运行迁移。迁移 runner 是逐文件事务、有 advisory lock，API/worker 自己也会启动迁移；务必让旧 writer 完全退出，不能用自动迁移替代发布协调。给迁移专用配置的连接加 `lock_timeout=5s`、`statement_timeout=300000`（示例预算，先按副本测量确认）；不要给所有在线流查询永久设置 DDL 超时。

```sh
"${DC[@]}" stop -t 60 worker
"${DC[@]}" stop -t 30 api
# MIGRATION_ENV_FILE 与目标一致，仅含迁移专用超时/凭据设置；由 DBA 管理。
HCAI_PRODUCTION_ENV_FILE="$MIGRATION_ENV_FILE" "${DC[@]}" run --rm --no-deps --pull never migrate
```

4. 从实际已应用版本**顺序**升级至 0179，不跳过历史 up。0173–0177 依赖已有支付/结算和 0166–0172；0178 replay、0179 disposition 是当前实现必需。普通索引、ALTER/回填会锁表。超时或锁阻塞立即停止放量，不自动杀业务事务、不删除版本行；已提交前序保留，修复后由同版本向前重启。
5. 用批准的只读 DSN 执行 `psql -X -f deploy/resource-marketplace-release-checks.sql`，比对全部版本与 gate manifest、索引/trigger、停前后分类计数。查询是有界、REPEATABLE READ/READ ONLY；金额分类总和不是银行对账。

### T+0 至 T+60 分钟：匹配新版本、逐段放量

1. 在入口仍暂停写入时启动一台新 API 和 web：`"${DC[@]}" up -d --no-build --pull never --no-deps api web`。验证内部 `/ready`、安全 cookie、实际 TLS、API 元数据、访客/已购授权、支持读取；失败不放量。
2. 财务复核待执行任务、原 Provider 身份与开关，批准恢复后启动一台匹配 worker：`"${DC[@]}" up -d --no-build --pull never --no-deps worker`。不能声称有“只处理读取”的 worker 模式；启动前就要排除未经批准的发送任务。观察至少两个 maintenance 周期和 60s 租约窗口。
3. 同一版本通过后再增加 API/worker 实例；旧版本不混跑。当前模板 web 依赖单服务 DNS，增加实例及 LB 健康/粘性行为须已有 staging 验证。
4. 放量建议：只读 10 分钟 → 已批准小流量写入 15 分钟 → 25% 15 分钟 → 100% 30 分钟。付款试单只能在当次任务明确授权的 sandbox/test 账户发生；本手册不授权生产资金 smoke test。每一阶段由发布指挥与财务签署后推进。
5. T+2h、T+24h、T+72h 复核资金/通道报表、到期 refund/check、银行/来源未决、通知/导出失败、备份计划和容量；测试通过后不能立即销毁旧兼容镜像、密钥版本和备份。

## 6. 观察、告警、停止条件

[现有监控契约](OBSERVABILITY.md)和 `scripts/metrics-alert-check.sh` 是检测器，不包含真实呼叫/值班集成。每 30s 从内网抓取 `/metrics`，每分钟运行 checker；同时监控 scrape 缺失、5xx、TLS/DB 连接、存储/scanner、磁盘/RAM/CPU。记录固定告警 ID、发出时间、接收通道、主备 ACK 时间和恢复时间；不发原始用户内容或凭据。

| 条件 | 阈值/窗口 | 动作 |
| --- | --- | --- |
| 重复资金效果、账本/Provider 身份矛盾、未授权下载、错误用户数据 | 任意 1 次 | 立即停止相关写入和自动发送，P0；财务/安全双人核对；不以重新下单补偿 |
| database/audit invalid、required metric 缺失、无效 lease、recovery/evidence anomaly | 任意非零或缺失 | 保持维护；保留日志/原始命令；停止放量 |
| expired lease、maintenance | expired lease age >60s；有待办的 maintenance 超 300s 未成功 | 检查旧实例/持锁事务和 handler；不直接改状态 succeeded |
| 队列/支付未决 | runnable queue >900s；checkout pending/expired >900s；refund unresolved >86400s；资金/银行/来源及 check 逾期默认 >900s | 进入相应恢复队列，按原操作查询；P0 资金矛盾优先于等待年龄 |
| API/体验（待业务签署的建议 SLO） | 5xx >1% 持续 5min；核心读 p95 >500ms 或大于基线 2 倍 10min；无权限响应语义变化 | 停止放量/收回当前批次；区分 Provider 延迟与内部拥塞 |
| 资源 | DB 使用连接 >预算 80% 5min、磁盘空闲 <20%、OOM 任意一次、tmpfs 接近硬限 | 停写、诊断、按 staging 证据调整；不得无依据无限加 worker |
| 告警无人确认 | 主责 5min 未 ACK → 备份；10min 无人 → 发布指挥与财务升级 | 暂停发布；不能继续无人观察的窗口 |

以上业务 SLO 和通知时限是本手册建议，尚非已签署生产 SLA。异常计数的清零不是资金恢复证明；回调接收不代表 worker 完成，退款请求不代表退款成功，审核/transfer 不代表银行到账。

## 7. 财务与支持人工处置

1. 财务按 Provider、原 merchant/store、live/test、USD 分组，核对 payment/order/charge、refund operation、settlement gross/fee/net、seller ledger、reservation/release、funding transfer、银行 payout/return、dispute adjudication 和 source reversal/closure。不得把测试账、钱包点数、商家结算或平台现金混为一个余额。
2. 以只读 SQL/权限 API 导出本地汇总，再与正式 Provider balance/transaction/settlement/银行账单逐项匹配，处理分页至结束并保留游标/报表范围。记录业务日期和时区、cutoff、差异金额及负责人。仓库的 `/admin/provider-cost-reconciliations` 是生成服务成本对账，**不是银行对账**。
3. UNKNOWN/timeout：冻结原 payment/request/key，先查已持久化 dispatch/read/result；使用已有财务“查询/恢复”入口且先检查当前 eligibility。Waffo 不支持的查询走商户人工核实，不重发 POST、不切换商户、不补写成功。
4. 银行/来源返还：按 [来源手册](../docs/resource-marketplace-source-reversal.md)由 maker 提议、独立 checker 核对原证据；部分/矛盾结果保持 review。只有完整匹配结果允许既有 closure 消费；禁止 UPDATE 账本、删 audit/journal、调整钱包冒充归还。
5. 拒付：按 [拒付手册](../docs/product-payment-dispute-runbook.md)在 deadline 前准备并在 Provider 侧实际提交；站内 evidence receipt 只记录既有回执，不能设置 won/lost。
6. 支持只记录 case/通知、解释维护和待核对状态，不许承诺“已退款/已到账”。交接使用本地订单/工单/命令 ID 与脱敏 request ID；财务结果由 Provider/银行证据确认后再告知用户。

## 8. 备份、容量与回退/前滚

### 数据与容量预算

- DB 和媒体/对象版本必须联合恢复；DB dump 不包含 S3、密钥、cluster role/ACL。备份采用外部加密、异地保留、可审计访问、受控删除传播；未决退款/拒付/法律保留不随普通保留期清理。
- RPO/RTO 由业务/财务与 DBA 共同签署。若候选要求 RPO≤15min、RTO≤60min，必须用同规模数据、对象、密钥和备份后通道事件回放实测；当前只有小型冷恢复，不能签此 SLA。
- `database.Open` 每 API/worker 进程最多 12 个连接，预算至少覆盖 `12 × (API数+worker数+迁移数)` 加备份/运维余量。每 worker 并发 4、lease 30s，扩容会增加 Provider 流量与 DB 锁竞争。
- 基础模板每 API/worker 有 `/tmp`256MiB、export tmpfs1280MiB、media staging tmpfs768MiB，合计最高约 2.25GiB 的 tmpfs 预算，另加 Go heap、连接、响应和 OS；tmpfs 消耗内存。没有测量不能直接给容器 512MiB 或以 mount 上限充当可用 RAM。
- staging 必须含预期历史量、列表/财务查询、10MiB 上传、并发下载/慢客户端、导出/扫描/退款/worker 租约、429/超时重试。至少 30min 预期峰值、30min 2 倍峰值、1h soak；记录延迟/错误率/RSS/连接/锁/WAL/磁盘/指标抓取耗时（metrics 自身 2s deadline）。本轮 readiness 数据只作连通性基线。

### 选择回退分支

| 时点/证据 | 允许的分支 | 禁止动作 |
| --- | --- | --- |
| 未迁移、未接受新版本写入 | 恢复已验证的旧镜像/入口规则；再次验 health/队列/资金状态 | 根据 tag 猜测镜像相同 |
| 部分 up 已提交 | 保持写入关闭，定位具体失败文件/锁/不合法历史；使用同版本 runner 重启或审核后的前向修复 | 删除 schema_migrations 行、改写已应用 SQL、跳号继续 |
| 已至0179但无新增业务 | 只有在实际生产副本上验证过新 schema + 旧二进制兼容才允许应用回退；当前没有该证明，默认前滚 | 让旧 API/worker 与新 writer 混跑、给旧程序注入新协议值骗过 guard |
| 已有银行发送/拒付裁决/运营/来源处置证据 | 保留179 schema/原 journals，匹配的新版本修复；必要时停业务，人工原通道核对 | 批量 down、删命令/事件/账本后降级、用备份覆盖外部已发生付款 |
| 灾难恢复 | 恢复到新的离线目标，匹配媒体与密钥版本；逐表/约束/trigger/索引/队列/财务核对；完成 Tbackup 后 Provider 结果补齐再切连接/入口 | 直接对在线库 `--clean`、恢复半成品就开 worker、换新 key 重发已未知的付款 |

具体 down 限制见目标 7：0166 有银行执行/银行账本拒绝，0167 resume 拒绝，0170 reversal command 拒绝，0171 **任何 dispatch** 拒绝，0172 closure 拒绝，0173 dispute root/event 拒绝，0175 operations/submissions 拒绝，0178 adjudication/replay 拒绝，0179 disposition 拒绝。0169 可重建控制结构和0174移除排序列也不等于业务兼容；0176/0177 up/down 同义，不是数据回退能力。

恢复动作必须带独立目标和审批，遵循 [BACKUP_RESTORE](BACKUP_RESTORE.md)。必须先验证可信 checksum，原 restore 脚本接受缺失 sidecar且非单事务；失败目标留在线下，重新恢复至另一个空库。保留旧目标供取证，切换前关闭其所有 writer，避免双主。DNS/LB/连接切换依目标平台单独排练并记录；本轮没有执行生产切换。

## 9. 值班交接与交付状态

发布工单必须填：发布指挥/备份、DBA/备份、支付集成/备份、财务 maker/checker、存储/安全/QA/支持联系人；工作时区、T−1h至T+2h在线安排、T+24h/T+72h责任班次、真实告警路由、谁能停写/停 worker、谁能批准恢复。

每次交接包含：当前放量比例、最终 SHA/digests、迁移与备份位置、未决数量和金额（按账域分组）、最新成功 query 时间、事件 deadline、正在执行的原 command/key、处理人、下一复核时间与用户工单。5min/10min 升级规则须在真实通道测试，当前未提供接收人，禁止宣称值班就绪。

本轮交付源码审查、可复跑隔离演练、只读 SQL、Compose 覆盖模板、门槛及证据包。**NO-GO 是已完成评审的结论，不是部署失败后自动重试许可。** 后续只有补齐门槛、签署目标与明确部署授权才开始新的发布执行；本轮到此停止。

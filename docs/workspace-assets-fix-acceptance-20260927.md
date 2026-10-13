# 工作台资产：修复与最终验收

日期：2026-09-27。范围：`/workspace/assets`、详情、发布抽屉、上传/版本、收藏、使用关系以及扫描恢复。对应[原始审查](workspace-assets-review-20260927.md)的 A01–A09、R01。

## 结论与基线

**原审查发现的 9 项问题及扫描恢复竞争窗口已修复。** 验收针对本地工作区，不是生产发布批准。HEAD 仍为 `aa4c6125e7a26d80870e217bb9fec9004df5bdd8`，工作区包含此前资源市场任务的未提交改动；本轮没有提交、部署或增加数据库迁移，也没有清理已有开发资产。

范围内采用现有 Go/Vue、会话认证、类型化 API 和任务队列。原审查报告、原失败日志和原证据包保留；不能把原来“复现缺陷即通过”的表征测试当作修复验收。本轮新增断言正确产品行为的常规测试，并接入 CI 配置。

本次写入验收使用隔离数据库、独立媒体目录和本地 Provider，不使用现有开发服务器人工数据。内置浏览器对开发管理员资产的检查仅用于补充中文界面截图，没有提交发布或改写其来源声明。

## 逐项关闭记录

| 编号 | 最终行为与边界 | 实现及回归证据 |
| --- | --- | --- |
| A01 / P1 | 新建发布表单只从生成 provenance 填入 Provider/模型，不推断费用；上传、缺少 provenance 的资产留空，发布前必须补充。已有草稿保留作者保存的披露，不批量改写历史记录。 | `assetDisclosure.ts`、`AssetPublishDrawer.vue`；local_test/OpenAI/upload/unknown 单测与 UI；真实草稿恢复、发布后披露一致。中文 OpenAI 截图 `disclosure-fixed.png`。 |
| A02 / P2 | 自有资产使用 API total；收藏与扫描通过数量明确标为“已加载”，收藏结果标题同样如此。读取失败的计数显示 `—`，不假装为 0。“扫描通过”不等于权利保证。 | `WorkspacePage.vue`、双语文案；UI 覆盖 0/1/20/21/41、混合状态、全部分页加载。 |
| A03 / P2 | 上传确认后重取第一页和 total，避免同键重放重复计数。刷新失败仍保留已确认卡片，并显示数量未知和读取重试入口，不重复上传。 | UI 覆盖正常新增、已有 ID、响应丢失后同键重试、空列表、确认成功后读取失败；既有上传命令单测及 Go 幂等测试。 |
| A04 / P2 | 自有资产和收藏请求各自处理成功/失败，仅当前标签显示对应读取错误。一个来源失败不会丢弃另一个成功结果；身份切换仍清空私有状态，旧请求受加载版本保护。 | `Promise.allSettled`、独立来源错误；双向故障/重试 UI，既有页面切换晚响应测试、会话边界单测。 |
| A05 / P2 | 详情和已加载列表的 pending 资产都自动刷新，间隔 750ms，总预算 60s，单次请求至多 10s。临时读取故障在预算内重试，401/403/404 停止；耗尽后明确暂停并可手动刷新。离开页面/身份变化中止计时器和 HTTP；详情更新保留已展开 usage 分页。 | `assetScan.ts`、`WorkspacePage.vue`、支持 signal 的 getAsset；超过 15s、短暂失败、clean/review/rejected、预算耗尽/重试、挂起读取超时、离页中止测试。 |
| A06 / P2 | 标题 3–120、文件名 1–180、版本说明 3–500，统一 Unicode 码点；拒绝非法 UTF-8，服务端同时限制字节上界。前端不再用 UTF-16 原生 maxlength 限制标题/版本说明，自动文件标题按码点截断。10MiB 文件限制不变。 | `service.go`、`text_contract_test.go`、`unicodeText.ts`；ASCII/汉字/emoji/组合字符，120/121、500/501、180/181 与无效 UTF-8；上传 UI 使用 120 汉字。 |
| A07 / P2 | generation 首图、reference 表、mask 及 delivery 主/附文件均进入使用图。每种 kind/resource 只出现一行，选本族被引用的最高版本作为代表；再按既有时间/类型/ID 游标分页。owner/author/seller/creator 条件保留。 | `service.go`、`reference_usage_test.go`、`usage_test.go`；第二参考图、仅蒙版、交付附文件、跨版本去重、1 条/页遍历和他人私有流程不可见。 |
| A08 / P2 | 识别自有、本地自有、个人、旧/新商业、任务合同；未知码明确“授权待核验”。详情补来源/权利范围说明及可用政策入口。自有记录不声称版权保证；购买优先展示订单许可名，并导航到订单冻结条款；任务继续使用交付合同原文与权利证据。 | `contentPresentation.ts`、详情模板；全部映射单测，未知/自有 UI，旧/新商业购买到冻结订单条款 UI，内置浏览器实际访问政策页面。 |
| A09 / P2 | 私有资产列表、详情、用途、收藏以及上传/版本响应在认证/解析前设置 `Cache-Control: private, no-store`，包括拒绝与输入错误。未改变公开媒体策略。 | `vertical.go`、`asset_cache_test.go`；本人 200、匿名 401、他人 403、非法 UUID/分页输入；开发服务重启后匿名 GET 返回 401/no-store 的独立证据。 |
| R01 | 未取得扫描恢复绑定锁时仅跳过，不再尝试对该绑定执行忙碌延期。真正持有绑定后遇账户/资产/job 锁竞争仍保留公平性退避。结束证据/租约判断不放宽，不额外调用扫描器。 | `scan_recovery.go`、确定性 `TestScanRecoveryMissedBindingRemainsDue`；旧逻辑 overlay 必败，新逻辑连续 10 次通过；审计/通知各一次，原有锁公平性与恢复测试全绿。 |

Unicode 的“字符”指码点，不是人眼字素：例如 `e` 加组合音符计 2，部分组合 emoji 也计多个码点。资产来源或许可代码本身不是第三方权利的证明，订单冻结合同和任务权利约定仍优先于通用解释。

## R01 根因、修复前后证据

原实现的枚举事务与逐条认领事务分开。逐条认领执行 `FOR UPDATE SKIP LOCKED` 可能因另一进程短暂持锁而没有返回行；原调用方把这种情况也当成资源忙碌，在锁释放后另起 UPDATE，把仍可立即恢复的记录推迟一分钟。并发两轮因此可能均报告 0。

回归测试通过 pgx QueryTracer 建立精确次序：枚举完成→另一事务锁住绑定→认领无行→释放锁→原调用方处理结果。测试断言绑定仍到期、无需推进时钟即可在下一轮完成、之后重复调用不再处理，并核实一次审计/通知。

`go-recovery-before-expected-failure.log` 使用 Go overlay 恢复旧处理分支，确定性失败为 `unclaimed binding was deferred`，没有改写当前业务源文件。`go-concurrency-repeat.log` 为修复后该测试及原并发恢复场景各 10 次通过。原并发场景补充两轮结束后的立即恢复检查；这反映 SKIP LOCKED 的暂时跳过语义，不以睡眠或修改 recovery_after 掩盖延期问题。

## 验证结果

证据根目录：[`data/assets-fixes-20260927`](../data/assets-fixes-20260927/)。

| 验证 | 结果 | 证据位置 |
| --- | --- | --- |
| Go 资产全包 | 43 个顶层测试通过，无跳过/失败 | `go-full.jsonl` |
| Go 媒体全包 | 37 个顶层测试通过，无跳过/失败 | `go-full.jsonl` |
| 资产 HTTP | 6 个顶层测试通过 | `go-http.jsonl` |
| usage 分页补充 | 通过 | `go-usage-pagination.log` |
| 确定性与并发恢复 | 两场景各连续 10 次通过 | `go-concurrency-repeat.log` |
| 修复前对照 | 预期失败，证明回归可捕获原问题 | `go-recovery-before-expected-failure.log` |
| 前端单测 | 6 文件、62 项通过 | `frontend-unit.log` |
| 类型检查与生产构建 | 通过；仍有既有大 chunk 提示 | `frontend-build.log`、UI 启动构建日志 |
| ESLint | 0 errors；83 个既有格式 warning 未全仓格式化 | `frontend-lint-final.log` |
| Playwright UI | 33/33 通过，全部保留截图和 trace | [UI HTML 报告](../web/test-results/assets-ui/report/index.html)，每条保留截图/trace |
| 真实后端 E2E 第一次 | 7/7，通过后清理所有本轮资源 | `e2e-real.log`、run `71771f…` |
| 清理后再次真实 E2E | 7/7，新数据库、新媒体和进程 | `e2e-clean-rerun.log`、run `4fe484…` |
| 内置浏览器补充 | 中文计数、来源披露、授权详情/政策入口已实查 | `assets-desktop.png`、`disclosure-fixed.png`、`license-detail-fixed.png` |

真实后端运行目录：

- [第一次：hcai_e2e_71771f382b1e47d887925189a8ffcd76](../web/test-results/marketplace-runs/hcai_e2e_71771f382b1e47d887925189a8ffcd76/report/index.html)
- [清理后重跑：hcai_e2e_4fe4845394bf4153b372343b93aea0d4](../web/test-results/marketplace-runs/hcai_e2e_4fe4845394bf4153b372343b93aea0d4/report/index.html)

每个 run 都保留 `source-sha256.json`、`results.json`、截图、trace 和 `isolation.json`。两份 isolation 均确认 `externalProviders=false`、processesStopped/portsClosed/databaseRemoved/mediaRemoved=true、exitCode=0。未删除既有目标 10 证据数据库或开发库。共享启动器的模拟支付设施不代表本轮验收了外部支付。

本轮中间失败也保留：首次 UI 发现新增翻译键落入嵌套配置，已修正；HTTP 测试初版误把项目分页错误 422 写成 400；旧轮询测试仍等待已中止请求的 response，已改为验证 requestfailed；虚拟时钟测试改为逐次等待网络/DOM，分页测试等待卡片数再继续；新增表单测试按原生 required 行为验证禁止提交，订单链接断言使用实际文案。这些修正有单独日志，不冒充最终通过结果。

## 可重复执行与 CI

测试要求安装 Go、Node/npm、Python3、psql、Playwright Chromium，且专用 loopback PostgreSQL 用户有 CREATEDB 权限。以下仅指本机隔离测试设施，不连接生产数据库。

```bash
TEST_DATABASE_URL='postgres://hcai:hcai@127.0.0.1:55435/hcai_goal5?sslmode=disable' HCAI_REQUIRE_INTEGRATION_TESTS=1 go test -count=1 ./internal/assets ./internal/platform/media
TEST_DATABASE_URL='postgres://hcai:hcai@127.0.0.1:55435/hcai_goal5?sslmode=disable' HCAI_REQUIRE_INTEGRATION_TESTS=1 go test -count=1 ./internal/transport/httpapi -run 'Test(AssetUpload|AssetPrivateJSON|OwnedAssetUsage|ContentDraftAndAssetVersion|SavedWorks)'
TEST_DATABASE_URL='postgres://hcai:hcai@127.0.0.1:55435/hcai_goal5?sslmode=disable' HCAI_REQUIRE_INTEGRATION_TESTS=1 go test -count=10 ./internal/assets -run 'TestScanRecoveryMissedBindingRemainsDue|TestScanExecutionCrashRecovery/concurrent_recovery'
npm --prefix web test -- src/lib/assetPresentation.test.ts src/lib/assetScan.test.ts src/lib/uploadCommands.test.ts src/lib/textPreview.test.ts src/api/command-snapshots.test.ts src/stores/session-command-boundary.test.ts
npm --prefix web exec -- playwright test --config web/playwright.assets-ui.config.ts
E2E_DATABASE_ADMIN_URL='postgres://hcai:hcai@127.0.0.1:55435/hcai_goal5?sslmode=disable' python3 scripts/e2e-marketplace.py asset-upload.spec.ts asset-publish-drawer.spec.ts asset-usage.spec.ts content-drafts-asset-versions.spec.ts saved-assets.spec.ts
npm --prefix web run build
npm --prefix web run lint
```

- `playwright.assets-ui.config.ts` 使用新构建、独立 15179 端口、全量 API fixture、`reuseExistingServer=false`，输出 HTML/JSON/截图/trace。
- `playwright.marketplace.config.ts` 已包含上述五个真实资产套件；它强制隔离启动器分配的数据库身份，不能直接复用手工开发环境。
- `.github/workflows/ci.yml` 增加资产 UI 步骤与产物上传；现有隔离 job 的两次完整运行会包含真实资产套件。Go/前端单测由既有 CI verify 执行。
- 本轮在本地执行同一入口，**没有声称已获得远程 CI 绿灯**。CI 合并后仍应核对实际 job 与产物。

## 部署边界与剩余限制

本轮无新增迁移。发布这批修复需要 API 和 worker 都加载新二进制、前端加载新构建；单独更新前端不能关闭 Unicode/引用图/缓存/恢复问题。本地开发 API、worker、Vite 已重启，保留管理员会话，付款/模型 Provider 关闭，邮件使用 local_file，存储/扫描使用本地适配器。

本轮没有重新执行生产 Nginx 容器、真实对象存储/恶意文件扫描、外部 Provider、生产容量或正式可访问性认证。A09 的动态证明为 HTTP 应用入口及本地服务，部署代理不得覆盖其 no-store 头；本轮没有修改代理缓存策略。历史草稿/发布声明不自动重写，历史来源真实性仍需作者或运营按证据核实。

原报告 P3 的搜索筛选、虚拟化、标签 tabpanel 语义、批量视频性能等没有作为此次修复范围扩展。列表卡片的既有“安全”标签仍只表示扫描状态，不证明版权或商业许可。平台通用授权政策也不替代逐笔冻结合同或第三方权利证明。

完整证据包：[`workspace-assets-fixes-20260927.tar.gz`](../data/workspace-assets-fixes-20260927.tar.gz)，旁附 SHA-256；最终源码清单在 `data/assets-fixes-20260927/source-sha256-final.json`。

最终交付是代码、回归测试和本审查/验收包；没有生产部署授权，也未操作生产资金。本轮到此停止。

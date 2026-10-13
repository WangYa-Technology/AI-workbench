# 已购内容修复与优化验收

日期：2026-09-27。对应审查：[已购内容区域审查](workspace-purchases-review-20260927.md)。本次完成报告中的三个问题及三项体验建议；历史审查与证据包保留原样。

## 完成核对

| 要求 | 最终实现 | 验收证据 |
| --- | --- | --- |
| PUR-01：列表缺复用入口 | Asset 增加可选 purchaseAccess，批量计算 orderId、canDownload、canReuse 和状态；列表按服务端能力显示复用及订单入口。最多一条附加 SQL 查询处理整页，不逐卡请求详情 | Go 列表/详情一致性；允许/禁止衍生的冻结合同、当前许可改变、资源包与退款状态测试；六类真实购买的 API 列表与详情能力相等 |
| PUR-02：文档误标 Chat | 类型标签采用媒体分类，document 为文档，application/zip 为资源包；创作工具路由继续独立映射 | 中英文 390/1308px 页面用例及截图；图片、视频、音频、文档、资源包 |
| PUR-03：测试陈旧/CI 缺覆盖 | 原分页断言更新为 Scan passed (loaded)，加入隔离市场 testMatch；新增已购体验 UI，并把订单交付 UI 加入当前 CI 使用的资产 UI 配置 | 原分页、迟到响应、真实登录切换、退款后列表移除等测试通过；CI 配置已纳入，但未触发远程 CI |
| 搜索、筛选与布局 | 已购页支持标题、媒体类型、扫描筛选及列表/网格；固定 source=purchase，查询参数保留刷新/后退，无结果可清除筛选 | URL source=upload 不能改变购买边界；真实购买按标题/类型过滤；刷新/后退/空结果 UI；共享虚拟列表桌面 2,000 项及手机 400 项回归仍通过 |
| 权益、下载、复用边界 | 统计标为有效购买权益，读取失败显示未知；卡片区分可下载、退款处理中、扫描未通过、暂不可下载；详情解释撤权、不可复用和未就绪，提供订单、刷新授权与支持入口 | 数据库状态转换和缺失合同回归；资源包可下载但不可作为普通创作来源；中英文页面证据 |
| 下载失败恢复 | 请求 bytes=0-0 进行预检，结束即取消响应体，不创建整包 Blob；403/404/其他失败分别显示恢复文案；通过后交给原生浏览器下载。120 秒预检预算，离开页面/换身份取消，迟到结果不能触发下载 | 6 个预检单测验证错误映射、响应关闭和不读入大响应；403/404/500 与离开视图 UI；六种真实文件逐字节下载/退款测试 |

## 安全与兼容

- 列表能力仍采用 `productpolicy.PurchaseAssetAccessSQL` 和 `PurchaseAssetReuseSQL`，不按许可证名称推断权限。下载、包内文件和创作请求仍独立执行当前权限检查。
- 列表能力与本页计数/结果使用相同只读 repeatable-read 事务；元数据只是即时能力提示，不是长期下载令牌。
- canDownload 表示授权及交付元数据满足策略，不保证远程文件此刻可读；点击下载还会检查实际读取结果。扫描通过、权益有效和可衍生复用是不同含义。
- 退款申请中继续使用有效权益，退款确认后撤权；旧合同、原始存储定位、跨买家隔离、受限文件索引、Range 和 no-store 回归保持通过。
- 没有新增迁移、支付动作或生产配置。API 增加可选字段，旧客户端不受影响；旧服务端缺少能力字段时，新列表不会凭空显示复用权限。
- 预检只要求一个字节，但后端为完整性验证可能仍扫描/暂存完整对象，因此会增加一次验证成本。未声称已验证生产大文件吞吐量或真实对象存储延迟。
- 预检与原生下载不是原子操作。最终请求仍会重新授权；浏览器管理进度，页面不伪报下载完成，也不能追回之前已交付的字节。原生下载开始后的错误由浏览器呈现，页面保留刷新授权、订单及支持恢复说明。

## 实际验证

证据根目录：`data/purchases-fixes-20260927/`。

| 检查 | 结果 | 位置 |
| --- | --- | --- |
| Go/HTTP/支付 | 59 个通过事件（含子用例；assets 3、httpapi 8、payments 48），无失败、无跳过 | `go-final.jsonl` |
| 前端单元测试 | 36 个文件、283 项通过 | `unit-final.log` |
| Playwright UI | 整组 61/61 通过；补充超时后已购专项 10/10 通过，合计覆盖 62 个不同用例 | `ui-final.log`、`purchases-final-ui.log`；`web/test-results/assets-purchases-ui/report/index.html` 及证据目录 `purchases-final-report/`、`purchases-final-results/` |
| 隔离真实 E2E | 第一轮 8/8；清理后增强版 9/9，通过真实 API/worker、本地支付 fixture、独立数据库 | `real-e2e.log`、`real-e2e-final.log` |
| lint | 0 错误、0 警告（max-warnings=0） | `lint-final.log` |
| TypeScript/Vite | 构建通过，无 chunk 体积警告；最后的按钮禁用调整也通过专项测试启动前的生产构建 | `build-final.log` |
| 最终摘要文案 | 中英文翻译契约 4/4 通过 | `copy-final.log` |
| 空白检查 | git diff --check 通过 | 交付检查 |

超时补验首先发现 UiButton 覆盖外部 aria-disabled，已改为其正式 disabled 属性；专项测试确认检查中禁用、120 秒超时后解除禁用并显示恢复提示。初次失败日志保留作过程证据，不作为最终验收结果。

真实 E2E 覆盖 PNG/JPG/MP4/WAV/MP3/TXT 的付费 fixture 确认、受保护副本、筛选列表、原生下载字节一致、退款中可读、退款确认后 403、从列表移除；另有原已购分页迟到响应和通过界面退出/登录其他买家后清除旧内容/游标。

两个独立运行目录：

- `web/test-results/marketplace-runs/hcai_e2e_51d6afbcb2054bc29738d8352c37ac5e/`
- `web/test-results/marketplace-runs/hcai_e2e_11ec039e60e149cab4041482606680a1/`

各目录保留报告、截图、trace 和 isolation.json。均确认外部 Provider 关闭、测试进程停止、端口关闭、专属数据库及媒体目录删除、exitCode=0。CI 的资产 UI/隔离市场配置覆盖新增场景，但本轮未执行远程 CI。

开发 API/worker 已更新；内置浏览器仍为原管理员登录，位于已购内容页。没有给开发管理员添加购买记录。`live-purchases-zh.png` 仅为中文空页的视觉复核；关键非空旅程不依赖这份人工数据。

## 复跑命令

必须指向可创建/删除测试数据库的专用本地 PostgreSQL，禁止生产连接。

```sh
TEST_DATABASE_URL='postgres://hcai:hcai@127.0.0.1:55435/hcai_goal5?sslmode=disable' HCAI_REQUIRE_INTEGRATION_TESTS=1 go test -json -count=1 ./internal/assets ./internal/transport/httpapi ./internal/payments -run 'TestPurchaseAccessList|TestAssetList|TestAssetFilters|TestAssetSearchHTTP|TestHistoricalPurchasedContent|TestProductBundleBuyerContent|TestPurchasedAccessRechecks|TestLegacyPurchaseReadCloses|TestHistoricalExternalPurchase|TestHistoricalContractRetains|TestPurchasedContractControls|TestProductBundleDeliveryRechecks|TestProductBundleFulfillmentAndAuthorized|TestPurchasedDeliverySurvives|TestProductCheckoutAndRefundHTTPContract'
npm --prefix web run lint -- --max-warnings 0
npm --prefix web run build
npm --prefix web test
(cd web && npx playwright test --config playwright.assets-ui.config.ts)
E2E_DATABASE_ADMIN_URL='postgres://hcai:hcai@127.0.0.1:55435/hcai_goal5?sslmode=disable' python3 scripts/e2e-marketplace.py product-media.payment.spec.ts purchased-assets-pagination.spec.ts asset-experience.spec.ts
```

## 交付范围

本次在原 main 工作区上完成，已有其他任务变更均保留，未提交或部署。以证据目录中的 source-sha256.txt 标识当前源码，不将原 HEAD 单独视为本轮发布版本。

完整证据：`data/workspace-purchases-fixes-20260927.tar.gz` 及同名 SHA256 文件，包含报告、相关源码、日志、页面截图与两轮真实端到端报告。开发持续写入日志与 dotenv 不进入归档。

真实支付商户、S3/扫描服务、生产容量及发布部署继续按用户此前要求延期。本次已购内容修复与优化范围完成。

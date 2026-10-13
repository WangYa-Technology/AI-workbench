# 工作台资产区：体验与文案验收

日期：2026-09-27。范围：`/workspace/assets` 自有资产、收藏、详情入口及相邻已购列表的兼容回归。

## 结论与范围

按用户最新指示“这部分先不做，先做体验优化和文案完善”，本轮体验与文案范围已完成。历史资产披露核查、真实 S3/扫描服务、生产容量、远程 CI 执行和发布部署明确延期，不属于本次完成声明。未连接生产数据库、未使用生产资金、未调用真实邮件或支付设施。

本轮基于 main 的 `aa4c6125e7a26d80870e217bb9fec9004df5bdd8` 加当前未提交工作区验证；仓库已有大量其他任务变更，不能把该 HEAD 单独当作本轮已发布版本。没有新增数据库迁移，没有提交或部署。原审查与 A01–A09/R01 修复证据保留在 `workspace-assets-review-20260927.md` 和 `workspace-assets-fix-acceptance-20260927.md`。

## 完成项

| 项目 | 最终行为 | 验证 |
| --- | --- | --- |
| 搜索与筛选 | 自有资产按标题、媒体类型、来源、扫描状态过滤；收藏按标题和媒体类型过滤。服务端先过滤再计数和分页；筛选保存在 URL，支持刷新和后退。无结果可清除筛选 | Go、HTTP、UI 及真实 E2E |
| 输入及游标 | 标题最多 120 个 Unicode 码点；参数枚举校验，重复参数拒绝；标题使用字面子串匹配，百分号/下划线不当作通配符。游标绑定身份和筛选条件 | 跨用户、无效参数、分页过滤及游标回归 |
| 大列表 | 超过 60 项后按测量高度虚拟渲染；支持列表、网格、移动端整页滚动及桌面内容区滚动；保留焦点并支持 Tab 跨虚拟行 | 桌面 2,000 项、手机 400 项，包含加载全部分页、首尾滚动和网格切换 |
| 扫描查询 | 列表按可见范围及预留行轮询待扫描资产，离开范围取消请求和定时器；详情与上传反馈保留 | 原扫描、上传及导航恢复回归通过；未做大规模并发扫描服务压测 |
| 标签无障碍 | tab 与 tabpanel 的 ID、aria-controls、aria-labelledby 关联；方向键及 Home 切换 | 键盘回归和指定 axe 规则通过 |
| 多视频列表 | 列表及收藏显示静态视频占位，提示进入详情播放，不为每张卡片创建 video 元素 | 40 项视频、减少动态效果模式：0 个 video、0 次闲置 MP4 请求 |
| 文案 | “安全/Clean”改为“扫描通过/Scan passed”；保留扫描与权利边界；筛选计数、已加载计数、空结果及视频提示采用中英文文案 | 真后端扫描状态回归，内置浏览器中文页面及空结果截图 |

主要实现：`internal/assets/service.go`、`internal/transport/httpapi/vertical.go`、OpenAPI 与生成客户端类型、`WorkspacePage.vue`、`AssetCatalogViewport.vue`、`AssetMedia.vue`、`UiTabs.vue` 及中英文消息文件。

## 验证结果

| 检查 | 结果 | 证据 |
| --- | --- | --- |
| Go 资产包与 HTTP 相关回归 | 44 + 7 = 51 项通过，无失败、无跳过 | `data/assets-completion-20260927/go-regression.jsonl` |
| 前端相关单元测试 | 6 文件、62 项通过 | 同目录 `unit.log` |
| Playwright UI | 38/38 通过，生产构建独立端口，不复用开发服务器 | `web/test-results/assets-experience-ui/report/index.html`、`results.json`、`tests/` 中截图与 trace |
| 真实 API/worker/数据库 E2E | 连续两轮，各 8/8 通过 | 下列两个独立 run |
| TypeScript 与 Vite build | 通过；构建保留大于 500 kB 的 chunk 提醒 | `build.log` |
| ESLint | 0 错误、84 警告；未扩大到全仓格式整理 | `lint-final.log` |
| diff 空白检查 | `git diff --check` 通过 | 源码交付检查 |

真实测试覆盖上传/扫描/审核、详情、发布抽屉及移动布局、下游使用关系、版本与草稿恢复、收藏引用，以及新增的中文标题组合筛选、刷新保留、无结果状态。两轮均创建专用数据库和媒体目录，外部 Provider 关闭，结束后进程停止、端口关闭、数据库删除、媒体删除，见各目录的 `isolation.json`：

- `web/test-results/marketplace-runs/hcai_e2e_2225504c00e0404b81d6f28acb2cc557/`
- `web/test-results/marketplace-runs/hcai_e2e_597bf2f2da964af2abdca1d857281223/`

容量附件记录的是列表末端采样，不是生产吞吐量或全程最大值：桌面 2,000 项加载后渲染 11 卡片，主区 620 个 DOM 节点，宽度/滚动宽度均 1108；手机 400 项渲染 5 卡片，412 个节点，宽度/滚动宽度均 390。滚动测试同时断言卡片数量上限及无横向溢出。未声称所有屏幕阅读器、浏览器、真实视频解码性能或生产数据规模均已验收。

本地内置浏览器中文辅助截图：`data/assets-completion-20260927/assets-zh-desktop.png`、`assets-zh-no-match.png`。它们只用于视觉复核；自动化验收不依赖管理员或开发服务器现存数据。开发 API/worker/Vite 已重启到当前代码，保留管理员登录和默认资产页。

## 复跑

需要仓库要求的 Go、Node、Playwright Chromium，以及专用本地 PostgreSQL 管理连接（可创建/删除测试数据库）。禁止将测试连接指向生产。

```sh
# 将连接替换成自己的隔离 PostgreSQL；测试工具只清理其拥有的数据库。
TEST_DATABASE_URL='postgres://hcai:hcai@127.0.0.1:55435/hcai_goal5?sslmode=disable' HCAI_REQUIRE_INTEGRATION_TESTS=1 go test -count=1 ./internal/assets ./internal/transport/httpapi -run 'TestAsset|TestSavedWorks|TestOwnedAsset|TestSecondaryReference|TestScanRecovery|TestScanExecutionCrashRecovery|TestContentDraftAndAssetVersion'
npm --prefix web run build
npm --prefix web run lint
npm --prefix web test -- src/lib/assetPresentation.test.ts src/lib/assetScan.test.ts src/lib/uploadCommands.test.ts src/lib/textPreview.test.ts src/api/command-snapshots.test.ts src/stores/session-command-boundary.test.ts
(cd web && npx playwright test --config playwright.assets-ui.config.ts)
E2E_DATABASE_ADMIN_URL='postgres://hcai:hcai@127.0.0.1:55435/hcai_goal5?sslmode=disable' python3 scripts/e2e-marketplace.py asset-experience.spec.ts asset-upload.spec.ts asset-publish-drawer.spec.ts asset-usage.spec.ts content-drafts-asset-versions.spec.ts saved-assets.spec.ts
```

再次执行最后一条即清理后复跑，保留不同 run 目录。CI 已配置相同资产 UI 套件、两轮独立真实验收及 always 上传证据；本轮未触发远程 CI，不能将本地通过写成托管 CI 已通过。

## 留存与延期

`data/workspace-assets-experience-20260927.tar.gz` 和旁边 SHA256 文件保存本轮日志、源码指纹、中文截图、UI 报告及两轮真实 E2E 的报告/追踪。开发日志不纳入归档，以避免继续变化。没有把环境密钥或整个 dotenv 纳入证据。

后续生产设施、历史数据、容量与发布工作按用户指示延期。现存 lint 警告、构建 chunk 提醒和真实视频解码/生产压测不在本轮完成证明内。当前限定的体验与文案范围没有已知测试失败的未完成修复；本轮到此停止。

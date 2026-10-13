# 前端 lint 与构建警告清理（2026-09-27）

本记录补充资产体验验收，替代其“剩余 lint/构建警告”的当前状态描述；原验收及归档保持原样，作为当时证据。

## 修改

- 修复 `SellerSourceReversalPanel.vue`、`ProductPaymentDisputesPage.vue`、`SellerPayoutsPage.vue` 的 83 条模板换行与属性顺序警告，保留业务逻辑。
- 为 `UiTabs.vue` 的可选 `idPrefix` 设置空字符串默认值，消除第 84 条警告。未提供前缀时依然不生成关联 ID。
- 在 `vite.config.ts` 将中英文静态词典分别分块，继续同步初始化和切换语言；保留原有 framework 分块。未提高 500 kB 警告阈值或关闭警告。

入口 JS 由约 524.14 kB 降到 168.17 kB，中文/英文词典分别约 150.12/158.36 kB，最大 JS 文件为 AdminPage，约 240.94 kB（以上均为未 gzip 的产物大小）。分块改善缓存边界；两个词典仍静态加载，因此不声称首屏总传输量减少至入口文件大小。

## 验证与复跑

证据目录：`data/frontend-warning-cleanup-20260927/`。

- `npm --prefix web run lint -- --max-warnings 0`：0 错误、0 警告（`lint.log`）。
- `npm --prefix web run build`：TypeScript 和 Vite 通过，没有构建警告（`build.log`）。
- `npm --prefix web test`：35 文件、277 项通过，包括翻译契约与 API/会话边界测试（`unit.log`）。
- Playwright：158/158 项通过，覆盖资产与卖家提现/审核页面。结果见 `ui.log`、`playwright-report/index.html`，每条测试追踪保存在 `playwright-results/`。
- `git diff --check` 通过。

浏览器验收使用生产构建和隔离端口、API fixture，不依赖开发服务器人工数据。复跑：

```sh
cd web
PLAYWRIGHT_HTML_OUTPUT_DIR=../data/frontend-warning-cleanup-20260927/playwright-report npx playwright test --config playwright.ui.config.ts asset-experience.ui.spec.ts asset-handover.ui.spec.ts asset-review-fixes.ui.spec.ts seller-payouts.ui.spec.ts payout-reviews.ui.spec.ts --output ../data/frontend-warning-cleanup-20260927/playwright-results --reporter=list,html --trace=on
```

Playwright 启动日志可能出现运行环境同时设置 `NO_COLOR` 与 `FORCE_COLOR` 的 Node 提醒。这不是源码 lint 或 Vite 构建警告，不影响测试结果；本次没有修改用户的全局终端颜色环境。没有后端业务变更、生产数据库操作或发布部署。

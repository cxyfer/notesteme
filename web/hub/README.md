# web/hub：Hub 的 Web UI

`notesteme-hub` 的網頁（M5-S9／UI-6）。與 Client UI（`web/client`）是**兩個獨立的 Vite 專案**：各自的 `package.json`、lockfile、bundle 與預算（初始 JS gzip < 300KB），只共用 `web/shared`（`@shared` 別名）。決定與取捨見 [docs/ui/README.md](../../docs/ui/README.md) 10.16、10.17，頁面清單見 [docs/ui/pages.md](../../docs/ui/pages.md) §14。

## 指令

```
npm ci
npm run dev            # Vite（:5174），/api、/auth、/mcp 代理到 NOTESTEME_HUB_DEV_BACKEND（預設 http://127.0.0.1:8090）
npm run typecheck && npm run lint && npm run format:check
npm test               # vitest（含每頁 axe、token 對比）＋ i18n 字典檢查
npm run build && npm run check:bundle
npm run gen:api        # 由 internal/hub/hubapi/openapi.json 產生 src/api/schema.d.ts；check:api 檢查已 commit
```

`npm run build` 的產物在 `dist/`，由 `web/hub/embed.go` 以 `go:embed` 包進 `notesteme-hub`；沒有建置時 Hub 回應 placeholder（`internal/hub/ui.go`）。Hub 把 `/api`、`/auth`、`/mcp`、`/healthz`、`/readyz` 交給 API，其他路徑是這個 SPA（`/assets` 與有副檔名的路徑找不到時是 404）。CSP 比 Client 嚴：沒有外部圖片與 iframe。

開發時 Hub 的 cookie 是 `Secure`：瀏覽器對 `http://localhost` 視為安全環境，所以 `npm run dev` 的登入可以用；OAuth 的 `redirect_uri` 是設定檔的 `public_url`，本機開發要讓它指向 Vite 的位址（或直接用建置後的 Hub）。

## 慣例

- 沿用 `web/client` 的慣例（README「API 呼叫」「i18n」「元件」「測試」）；`src/components` 的 Button、Dialog、TextField、SelectField、ErrorCallout、EmptyState、Spinner、Toast 是從 Client 複製的，改動時兩邊一起看。
- 是否登入以 `GET /api/v1/me` 為準（`useMe`；401 是未登入，不導頁）。其他 API 回 401 `unauthorized` 時導到 `/login?redirect=`；OAuth callback 一律回到 `/`，登入前的頁面記在 sessionStorage。
- 非 GET 請求帶 `X-CSRF-Token`（cookie `notesteme_hub_csrf`，HACC-R8）。
- 使用者上傳的一切（標題、字幕、筆記、檢舉與下架的說明、來源紀錄）都是不可信的文字：一律是文字節點，筆記用 `@shared/notes/NoteMarkdown`（不允許原始 HTML）；ESLint 禁止 `dangerouslySetInnerHTML`。
- 頁面放 `src/pages`，除了登入與首頁之外都是 lazy route（`src/router.tsx`）；新頁面要加進 `src/test/routes.a11y.test.tsx`。

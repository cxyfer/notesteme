# Notesteme Client UI（web/client）

React + Vite + TypeScript 的單頁應用（[ADR-0008](../../docs/adr/0008-web-ui-architecture.md)）。建置產物用 `go:embed` 嵌入 Go binary，由 `notesteme serve` 同源提供（`internal/webui`）。
設計見 [docs/ui](../../docs/ui/README.md)，實作現況與決定見 [docs/ui/README.md 第 10 節](../../docs/ui/README.md#10-實作現況)。

## 開發

需要 Node 24.12.0（`.nvmrc`）與 npm 11。套件版本一律寫死（`.npmrc` 的 `save-exact=true`），新增套件時挑發佈至少 2 週的穩定版，並在 PR 說明理由。

```sh
cd web/client
npm ci

# 另一個終端機，在 repo 根目錄啟動 Go 後端（預設 127.0.0.1:8080；環境變數見根目錄的 README）
go run ./cmd/notesteme serve

npm run dev        # Vite dev server，/api 與 /mcp 代理到後端；NOTESTEME_DEV_BACKEND 可以改後端位址
```

| 指令                                                        | 做什麼                                                                                          |
| ----------------------------------------------------------- | ----------------------------------------------------------------------------------------------- |
| `npm run build`                                             | 清空 `dist`（保留 `.gitkeep`）、`tsc --noEmit`、`vite build`。之後重新編譯 Go，binary 就包含 UI |
| `npm test`                                                  | vitest（含 axe、token 對比）＋ i18n 字典檢查                                                    |
| `npm run lint`／`npm run format:check`／`npm run typecheck` | eslint、prettier、tsc                                                                           |
| `npm run check:bundle`                                      | 建置後檢查初始載入的 JS（gzip）< 300KB（NFR-PERF-10），以及 `index.html` 沒有 inline script     |
| `npm run gen:api`                                           | 由 `internal/api/openapi.json` 產生 `src/api/schema.d.ts`                                       |
| `npm run check:api`                                         | `gen:api` 之後 `git diff --exit-code`（CI 的 drift 檢查）                                       |

CI：`.github/workflows/web.yml`（只在 `web/**`、`internal/api/openapi.json` 或 workflow 本身改變時執行）。

### 沒有 Node 的時候

`dist/` 只 commit 一個 `.gitkeep`，所以沒有建置前端時 `go build ./...`、`go test ./...` 照常通過，UI 回應「Web UI 尚未建置」的 placeholder（`internal/webui.Placeholder`）。Docker image 與 goreleaser 都會先建置前端；`release-check` 確認 binary 內嵌的是建置產物。

### API 型別

後端的 OpenAPI 3.1 由 Huma 產生。快照放在 `internal/api/openapi.json`，不用啟動 server、不需要資料庫：

```sh
go run ./internal/api/cmd/openapi -o internal/api/openapi.json   # 後端路由改變後執行（repo 根目錄）
cd web/client && npm run gen:api                                  # 重新產生 TypeScript 型別
```

`go test ./internal/api` 的 `TestOpenAPISnapshot` 會在快照過期時失敗；web.yml 的 `check:api` 會在型別過期時失敗。兩個產生的檔案都要 commit，不要手改。

## 專案慣例

### 目錄

```
src/
  api/          client.ts（所有 fetch）、errors.ts、describeError.ts、queryKeys.ts、queryClient.ts、schema.d.ts（產生）
  auth/         session.ts（CSRF cookie、登入後導回的路徑）
  components/   共用元件與它們的測試；頁面一律從 components/index.ts 匯入
  events/       SSE：types.ts、stream.ts（連線與重連）、invalidation.ts（事件 → query key 的表）、useEventStream.ts
  i18n/         index.ts 與 locales/{zh-Hant,zh-Hans,en}.json
  pages/        每個路由一個元件
  shell/        AppShell（導覽、skip link、SSE、401 處理）、偏好設定
  styles/       tokens.css（設計 token）、base.css（shell 與頁面）、components.css
  theme/        淺色／深色／跟隨系統
  test/         setup、axe 輔助、a11yRoutes.ts（每頁 axe 清單）、renderRoute
```

### 路由

- TanStack Router 的**程式碼式路由**，全部定義在 `src/router.tsx`（不用檔案式路由與 codegen）。
- 需要登入的頁面放在 `app` layout 底下；它的 `beforeLoad` 在沒有 CSRF cookie 時導到 `/login?redirect=<原路徑>`。後端沒有「目前 session」的路由，任何 API 回 401 `unauthorized` 時也會導到登入頁。
- `redirect` 只接受站內路徑（`safeRedirect`），避免 open redirect。
- 篩選條件放在 search params（`validateSearch`），可以加書籤或分享（docs/ui/README.md §3.3）。
- 新增頁面：在 `router.tsx` 加 route、在導覽（`shell/AppShell.tsx` 的 `NAV_GROUPS`，依里程碑）加項目、把路徑加進 `src/test/a11yRoutes.ts`。
- 首屏以外、體積大的頁面（例如 CodeMirror 的 Prompt 編輯器）用 lazy route（`createLazyRoute` 或 `lazyRouteComponent`），守住 bundle 預算。

### API 呼叫

- 一律經過 `src/api/client.ts`：`api.get("/api/queue")`、`api.post("/api/auth/login", { body })`，路徑參數用 `apiPath("/api/items/{id}", { id })`。不要在元件裡直接 `fetch`。
- client 自動帶 cookie、對非 GET 加 `X-CSRF-Token`、送 `Accept-Language`（UI 語言）。
- 錯誤一律是 `ApiError`（`status`、`code`、`message`、`body`）。**程式只依 `code` 判斷**（NFR-I18N-2）；顯示文字用 `describeError(t, err)`：字典有 `errors.<code>` 就用它，否則用後端依語言產生的 `message`。
- 資料讀取用 TanStack Query，key 一律從 `queryKeys` 產生（`src/api/queryKeys.ts`），SSE 的失效表才對得上。

### 即時更新（SSE）

- `AppShell` 呼叫一次 `useEventStream()`：連上 `GET /api/events`，依 `src/events/invalidation.ts` 的表讓 query 失效；每次（重新）連上時讓所有相關的 key 失效（斷線期間的事件收不到）。
- 斷線後指數退避重連（1 秒起、上限 30 秒、±20% jitter）；重連前探測到 401 就導到登入頁；頁面隱藏超過 60 秒就關閉連線，回到前景再連上。
- 頁面要自己處理事件時用 `useServerEvent(handler, ["item.created"])`。例如媒體庫的「有 N 個新項目」（README §3.4）：`item.created` **不**讓列表失效，由頁面計數，使用者點擊後才重新取得。

### i18n 字串

- 使用者看得到的文字一律用 `t("key")`；eslint 的 `i18next/no-literal-string` 會擋下 JSX 裡寫死的字串（NFR-I18N-1）。品牌名、技術代碼也放進字典（例如 `app.name`），讓規則不必開例外。
- 三種語言同時加 key。`npm run check:i18n` 檢查：key 完全一致、沒有空字串、插值參數一致。複數用 i18next 的後綴（中文只需要 `_other`，英文加 `_one`）。
- 用語依 docs/ui/README.md 第 5 節的用語表；按鈕直接說出會發生什麼（第 4 節）。
- 日期、數字、金額用 `formatDateTime`／`formatNumber`（`Intl`）；時間碼用 `formatTimecode`，不隨語言改變（NFR-I18N-3）。
- 顯示字幕、影片標題等內容時，元素要設定內容自己的 `lang`（中文字型堆疊依 `:lang()` 切換，design-language.md §4.2）。

### 元件與樣式

- 顏色、圓角、間距、字級只用 `tokens.css` 的變數；不要寫死色碼。深色主題靠 `light-dark()`，不用另寫一套選擇器。
- 一個畫面最多一個 `variant="primary"` 的按鈕；不可復原的操作用 `danger` 並先確認（design-language.md §6.5）。
- 狀態一律用 `StatusBadge`（形狀＋文字，不只靠顏色）；錯誤一律用 `ErrorCallout`（類別、訊息、修正動作）。
- 對話框與抽屜用 `Dialog`／`Drawer`（Radix）：焦點鎖定、Esc 關閉、關閉後焦點回到開啟前的元素。
- 新元件至少要有兩個頁面會用到才放進 `components/`；每個元件都要有 axe 測試。

### 測試與無障礙

- vitest + Testing Library（jsdom）。查詢元素優先用 role 與可見名稱（`getByRole("button", { name: "登入" })`），測試本身就是無障礙檢查。
- `expectNoA11yViolations()`（`src/test/axe.ts`）：serious 與 critical 一律失敗（NFR-A11Y-1）。jsdom 不排版，`color-contrast` 規則關閉，改由 `src/styles/contrast.test.ts` 驗證 token 的對比。
- 每個頁面的 axe 檢查集中在 `src/test/a11yRoutes.ts`，新增路由時加進去。
- 鍵盤：skip link 是第一個可聚焦的元素；換頁後焦點移到 `<main>`；焦點框用 `:focus-visible`（淺色 Ink、深色 Cue）；可點擊目標至少 24×24px。
- `prefers-reduced-motion` 時所有動畫與轉場立即完成（`base.css`）。
- 目前不用 Playwright（記憶體有限）；需要真實瀏覽器的檢查（LCP、捲動、手機版面）列在 NFR 的 `manual`。

### CSP 限制（NFR-SEC-1）

`internal/webui` 對所有 UI 回應送出 CSP，前端必須遵守：

- **不可有 inline script**（`index.html` 只能有 `<script type="module" src>`）、不可用 `eval`、`new Function`、字串形式的 `setTimeout`；eslint 已禁止。主題防閃爍也不能用 inline script（見下）。
- 不可用 `dangerouslySetInnerHTML`（NFR-SEC-2）；Markdown 渲染之後由專用元件處理。
- 圖片、字型只能來自同源；Vite 設 `assetsInlineLimit: 0`，不產生 `data:` URI。外部縮圖只允許 YouTube（`*.ytimg.com`）與 Bilibili（`*.hdslb.com`）；嵌入播放器只允許 YouTube 與 Bilibili。
- `style-src` 允許 `'unsafe-inline'`（只限 style）：Radix 的捲動鎖定會插入 `<style>`。不要依賴它寫 inline style 屬性以外的東西。
- API 與 SSE 都是同源（`connect-src 'self'`）；不要呼叫外部 API。

### 主題

`tokens.css` 用 `light-dark()` 與 `color-scheme`：JS 載入前跟隨系統（`prefers-color-scheme`）；使用者選擇的主題存在 `localStorage`（`notesteme.theme`），由 `main.tsx` 在第一次渲染前套用成 `<html data-theme>`。CSP 不允許 inline script，所以手動選擇與系統不同時，載入 JS 之前可能短暫看到系統的主題。

### Bundle 預算

初始載入的 JS（gzip）< 300KB（NFR-PERF-10，UI-0 約 137KB）。`npm run check:bundle` 計算 `index.html` 直接引用的 module script 與 modulepreload。加入大型相依（CodeMirror、TanStack Table）時放在 lazy route 裡。

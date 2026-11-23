# Notesteme UI 規格總覽

> 狀態：草案 v0.1（2026-11-11）

| 文件 | 內容 |
|---|---|
| [design-language.md](design-language.md) | 設計概念、色彩、字體、版面、核心元件（時間帶、字幕句列表、印樣）、動態 |
| [pages.md](pages.md) | 各頁面的目的、版面、元件、互動、狀態與 API |
| 本文件 | 頁面清單、共用互動模式、文案原則、用語表、無障礙、多語系、響應式 |

技術架構見 [ADR-0008](../adr/0008-web-ui-architecture.md)。

## 1. 原則

1. **所有功能都能從 UI 操作。** UI、CLI、MCP 都呼叫同一套 API，不會有只能從 CLI 執行的功能。
2. **時間是主要的組織方式。** 進度、錯誤、畫面、字幕都畫在影音自己的時間軸上（時間帶）。
3. **出錯時告訴使用者怎麼修。** 每個錯誤都附上原因，以及一個可以直接處理的按鈕。
4. **校對只靠 LLM。** 字幕與筆記在 UI 中是唯讀的對照檢視，改善結果的方式是調整 prompt 和處理方案。

## 2. 頁面清單與里程碑

| 頁面 | 路由 | 里程碑 |
|---|---|---|
| [總覽](pages.md#1-總覽) | `/` | M4（M1–M3 先導到媒體庫） |
| [新增](pages.md#2-新增) | 抽屜，可從任何頁面開啟 | M1 |
| [媒體庫](pages.md#3-媒體庫) | `/library` | M1 基本版，M4 完整版 |
| [影音詳情](pages.md#4-影音詳情) | `/items/:id` | M1 字幕；M2 對照與重新處理；M3 畫面與筆記 |
| [訂閱](pages.md#5-訂閱) | `/subscriptions`、`/subscriptions/:id` | M4 |
| [佇列](pages.md#6-佇列) | `/queue` | M1 |
| [Prompt 庫](pages.md#7-prompt-庫) | `/prompts`、`/prompts/:id` | M2 |
| [處理方案](pages.md#8-處理方案) | `/profiles`、`/profiles/:id` | M2 |
| [字幕樣式](pages.md#9-字幕樣式) | `/styles`、`/styles/:id` | M2 |
| [筆記模板](pages.md#10-筆記模板) | `/note-templates`、`/note-templates/:id` | M3 |
| [成本](pages.md#11-成本) | `/costs` | M4 |
| [設定](pages.md#12-設定) | `/settings/:section` | M1 起逐步補齊 |
| [登入](pages.md#13-登入) | `/login` | M1 |

## 3. 共用互動模式

### 3.1 指令面板

- `Ctrl K`（macOS 為 `⌘ K`）開啟。
- 可以做三類事：
  - 搜尋影音標題和字幕內容。
  - 跳到任何頁面。
  - 直接執行動作，例如「新增」「暫停佇列」「更新 yt-dlp」。

### 3.2 在任何地方貼上網址

焦點不在輸入框時，貼上 YouTube、Bilibili 或 Podcast 網址，會直接打開「新增」抽屜，並帶入貼上的網址。

### 3.3 篩選條件存在網址裡

- 媒體庫、佇列、成本頁的篩選條件都存在 URL 參數中，可以加書籤或分享。
- 媒體庫可以把常用的篩選條件存成「檢視」，例如「码农高天，校正未完成」。

### 3.4 即時更新

- 用 SSE 接收事件，更新時間帶、佇列和需要你處理的數量。
- 不會因為即時更新而打亂使用者正在看的列表。新項目只會在列表頂部顯示「有 3 個新項目」，點擊後才插入。

### 3.5 確認與復原

- 可以復原的操作（例如從列表移除）不跳確認框，改用 toast 提供「復原」。
- 無法復原的操作（例如刪除媒體檔或產出）先跳確認框，並說明會失去什麼、會保留什麼。

### 3.6 鍵盤快捷鍵

| 按鍵 | 動作 |
|---|---|
| `Ctrl K` | 指令面板 |
| `N` | 新增 |
| `/` | 聚焦到目前頁面的搜尋框 |
| `G` 然後 `L`／`Q`／`S` | 前往媒體庫、佇列、訂閱 |
| `Space` | 影音詳情頁：播放或暫停 |
| `←`／`→` | 影音詳情頁：前後移動 5 秒 |
| `[`／`]` | 影音詳情頁：上一個或下一個章節 |
| `1`–`4` | 影音詳情頁：切換分頁（字幕、畫面、筆記、紀錄） |
| `?` | 顯示所有快捷鍵 |

## 4. 文案原則

- **從使用者的角度命名。** 例如寫「通知」，不寫「webhook 設定」；寫「由 Claude Code 處理中」，不寫「leased」。
- **按鈕直接說出會發生什麼。** 例如寫「開始處理」，不寫「送出」。同一個動作在整個流程中用同一個名稱。
- **錯誤說清楚發生了什麼、怎麼修。** 不道歉，也不含糊。
- **空狀態是邀請使用者行動的時機。** 說明可以做什麼，並附上按鈕。
- 用 sentence case、簡單的動詞，不加填充字。

### 4.1 動作與回饋

| 按鈕 | 完成後的提示 |
|---|---|
| 開始處理 | 已開始處理 2 部 |
| 重新處理 | 已排入重新處理，從校正開始 |
| 匯出 | 已匯出 SRT |
| 儲存變更 | 已儲存 |
| 另存新版本 | 已存為 v4 |
| 設為預設 | 已設為預設 |
| 暫停佇列／繼續處理 | 已暫停佇列／已繼續處理 |
| 刪除媒體檔 | 已刪除媒體檔，字幕與筆記都保留 |
| 更新 yt-dlp | 已更新到 2026.11.08 |
| 改用 API 處理 | 已改用 API 處理，原本的 Agent 任務已取消 |

### 4.2 錯誤範例

| 情境 | 訊息 | 動作按鈕 |
|---|---|---|
| YouTube 403 | YouTube 拒絕了下載請求（403）。先更新 yt-dlp 再重試；如果仍然失敗，上傳 YouTube cookie。 | 更新 yt-dlp、上傳 cookie |
| Bilibili AI 字幕需要登入 | 這部影片的 AI 字幕需要登入 Bilibili 才能取得。上傳 Bilibili cookie，或改用語音轉錄。 | 上傳 cookie、改用語音轉錄 |
| Cookie 過期 | YouTube cookie 已失效，3 部影片無法下載。 | 上傳新的 cookie |
| 超出預算 | 本月預算 $20 已用完，4 個任務暫停中。提高預算，或等到 12 月 1 日自動恢復。 | 調整預算 |
| LLM 驗證失敗 | 第 10 段的校正結果和原文差異過大（相似度 0.42），已退回原文。可以換一個 prompt 或模型後重新校正。 | 重新校正這段、開啟 Prompt |
| 自動字幕還沒生成 | YouTube 還沒產生自動字幕，預計 2 小時後再檢查。 | 現在改用語音轉錄 |
| git push 被拒 | Vault 的遠端有新的 commit，push 被拒。在本機 pull 之後，再按「重新寫入」。 | 重新寫入 |

### 4.3 空狀態範例

| 頁面 | 訊息 | 動作按鈕 |
|---|---|---|
| 媒體庫 | 還沒有任何影音。貼上 YouTube、Bilibili 或 Podcast 網址，或把影片檔拖進來。 | 新增 |
| 佇列 | 佇列是空的。新增影音，或等訂閱的頻道有新內容。 | 新增 |
| 訂閱 | 還沒有訂閱。訂閱頻道後，新內容會自動處理。 | 新增訂閱 |
| 篩選結果為空 | 沒有符合條件的影音。 | 清除篩選條件 |
| 垃圾桶 | 垃圾桶是空的。刪除的影音會在這裡保留 30 天。 | — |

## 5. 用語表

| 系統概念 | 繁中 | 简中 | English |
|---|---|---|---|
| item | 影音 | 音视频 | Media |
| library | 媒體庫 | 媒体库 | Library |
| stage | 步驟 | 步骤 | Step |
| artifact | 產出 | 产出 | Output |
| provenance | 來源紀錄 | 来源记录 | Source record |
| profile | 處理方案 | 处理方案 | Preset |
| task | 任務 | 任务 | Task |
| queue | 佇列 | 队列 | Queue |
| leased（Agent） | 由 Agent 處理中 | 由 Agent 处理中 | Being handled by agent |
| awaiting_agent | 等待 Agent | 等待 Agent | Waiting for agent |
| needs_action | 需要你處理 | 需要你处理 | Needs you |
| budget_blocked | 超出預算 | 超出预算 | Over budget |
| waiting | 稍後檢查 | 稍后检查 | Checking later |
| awaiting_user | 等你決定 | 等你决定 | Awaiting your decision |
| output target | 輸出目標 | 输出目标 | Output target |
| trash | 垃圾桶 | 回收站 | Trash |
| purge | 永久刪除 | 永久删除 | Delete forever |
| subscription | 訂閱 | 订阅 | Subscription |
| channel | 頻道 | 频道 | Channel |
| cue | 字幕句 | 字幕句 | Line |
| transcript（source） | 原始字幕 | 原始字幕 | Original transcript |
| corrected | 校正字幕 | 校正字幕 | Corrected |
| translated | 譯文 | 译文 | Translation |
| keyframe | 關鍵畫面 | 关键画面 | Keyframe |
| contact sheet | 印樣 | 印样 | Contact sheet |
| note | 筆記 | 笔记 | Note |
| time ribbon | 時間帶 | 时间带 | Time ribbon |
| executor: api／agent／off | API 處理／交給 Agent／不執行 | API 处理／交给 Agent／不执行 | Use API／Hand to agent／Skip |

## 6. 無障礙

- 符合 WCAG 2.2 AA。對比數值見 [design-language.md §3.3](design-language.md#33-對比wcag-22-aa)。
- 焦點框：
  - 淺色主題：2px Ink 外框，間隔 2px。Cue 在淺色底上對比不足，所以不用。
  - 深色主題：2px Cue。
- 可點擊目標至少 24×24px。
- 狀態不能只靠顏色區分，一律加上形狀、紋理和文字。
- 時間帶的資訊都有對應的文字版本（`aria-label`，或列表中的狀態欄）。
- 即時更新：只有「完成」「失敗」「需要你處理」會透過 `aria-live="polite"` 宣告，進度百分比不宣告。
- 支援 `prefers-reduced-motion` 和 `prefers-color-scheme`。

## 7. 多語系

- UI 語言有繁中、简中、English，和字幕目標語言分開設定。
- `<html lang>` 跟隨 UI 語言，字型堆疊依語言切換（見 [design-language.md §4.2](design-language.md#42-依語言的字型堆疊)）。
- 顯示字幕內容的元素要設定字幕自己的 `lang`。
- 日期、數字、金額用 `Intl` 格式化。時間碼一律是 `MM:SS` 或 `H:MM:SS`，不隨語言改變。
- 英文字串大約比中文寬 30%，版面要用彈性寬度，不能固定寬度。

## 8. 響應式

| 寬度 | 版面 |
|---|---|
| 1280px 以上 | 左側導覽展開，影音詳情頁的播放器與字幕左右並排 |
| 768–1279px | 左側導覽收合成圖示；影音詳情頁的播放器在上、字幕在下 |
| 767px 以下 | 導覽改成底部分頁列（總覽、媒體庫、新增、佇列、更多）；媒體庫的時間帶移到標題下方；播放器固定在頂部 |

手機上的主要用途是查看進度、處理「需要你處理」的項目、貼網址新增，所以這三件事在手機上都要順手。

## 9. Hub UI（M5）

Hub 是公開的資源站，會另外設計一套風格，以和 Client 區隔。兩者共用時間帶與字幕句列表元件，但 Hub 的重點是瀏覽、比較版本與下載。規格在 M5 前補上。

## 10. 實作現況

### 10.1 UI-0：骨架（2026-11-23）

UI-1～UI-3 疊在這個骨架上。開發方式與專案慣例（目錄、路由、API 呼叫、i18n、元件、測試、CSP 限制、bundle 預算）見 [web/client/README.md](../../web/client/README.md)。

| 範圍 | 狀態 | 位置 |
|---|---|---|
| 靜態檔案服務、SPA fallback、快取、安全標頭（NFR-SEC-1） | 完成 | `internal/webui`、`internal/api/ui.go` |
| `go:embed`；沒有 Node 也能建置（placeholder） | 完成 | `web/client/embed.go`、`web/client/dist/.gitkeep` |
| Docker 多階段建置、goreleaser 先建前端、release-check 檢查內嵌產物 | 完成 | `deploy/docker/Dockerfile`、`.goreleaser.yaml`、`.github/workflows/ci.yml` |
| OpenAPI 快照與 TypeScript 型別 | 完成 | `internal/api/openapi.json`、`internal/api/cmd/openapi`、`web/client/src/api/schema.d.ts` |
| 路由、API client、401 → 登入、登入頁、登出 | 完成 | `web/client/src/router.tsx`、`src/api`、`src/pages/LoginPage.tsx` |
| App shell：導覽（M1：媒體庫、佇列、設定）、skip link、主題、介面語言 | 完成 | `web/client/src/shell` |
| SSE：快取失效表、重連、頁面隱藏時斷線 | 完成 | `web/client/src/events` |
| i18n 三語與字典檢查 | 完成 | `web/client/src/i18n`、`scripts/check-i18n.mjs` |
| 設計 token 與元件：Button、TextField、Dialog／Drawer、Toast、Spinner、StatusBadge、ErrorCallout | 完成 | `web/client/src/styles`、`src/components` |
| 頁面：`/library`、`/queue`、`/items/:id`、`/settings/:section` | `/library`（UI-1）、`/items/:id` 與 `/queue`（UI-2）完成；設定仍是空頁 | `web/client/src/pages` |
| CI：tsc、eslint、prettier、vitest（axe、對比、i18n）、build、bundle 預算、OpenAPI drift、npm audit | 完成 | `.github/workflows/web.yml` |
| 新增抽屜、媒體庫（M1 基本版）、精簡版時間帶、`N`／`/` 快捷鍵、貼上網址 | 完成（UI-1，見 10.4） | `web/client/src/add`、`src/pages/library`、`web/shared` |
| 書籤小工具分區、`/add` 落地頁、登入的 `next`、PWA（manifest 引用、service worker 註冊、icon） | 完成（M4 ENTRY，見 ENTRY 規格待決問題 E7、E9、E11） | `web/client/src/add/{entry.ts,AddEntryPage.tsx}`、`src/pages/settings/BookmarkletSection.tsx`、`public/icons`；manifest 與 `sw.js` 由 `internal/entry` 提供 |
| 指令面板（`Ctrl K`）、`G` 開頭的快捷鍵、`?` 快捷鍵說明 | 未做 | 之後的切片 |
| Prompt 庫、處理方案、字幕樣式 | 完成（UI-3a，見 10.8） | `web/client/src/pages/{prompts,profiles,styles}` |
| 設定各分區、詞彙表與術語候選、設定匯出與匯入 | 完成（UI-3b，見 10.10；後端沒有的部分見 10.10 的「未做」） | `web/client/src/pages/settings` |
| 影音詳情、佇列 | 完成（UI-2，見 10.6） | `web/client/src/pages/item`、`src/pages/queue` |
| 登入頁背景的淡化時間帶（§13） | 未做 | 等 UI-1 的時間帶元件 |
| 768–1279px 的收合導覽（只剩圖示） | 未做：沒有圖示前維持展開；UI-1 沒有引入 Phosphor Icons | 之後的切片 |
| 訂閱、成本、筆記模板、設定的通知與預算、影音詳情的畫面與筆記 | 完成（UI-4，見 10.12） | `src/pages/{subscriptions,costs,noteTemplates}`、`src/pages/settings`、`src/pages/item/NotePanels.tsx` |
| 字幕全文搜尋、術語審核、bundle 匯出與匯入、系統告警與備份、摘要與彙整、更好的字幕來源 | 完成（UI-5，見 10.14） | `src/pages/search`、`src/pages/settings`、`src/pages/item`、`src/pages/library` |
| 總覽、批次操作、Hub UI | 未做：總覽依賴 LIB-09 `/api/overview`、批次依賴 LIB-08 `POST /api/items/batch`（兩者仍是 501 stub）；Hub UI 屬 M5 | 之後的切片 |

### 10.2 UI-0 的決定

以下細節 ADR-0008 沒有規定，依 /goal 採 advisor 建議（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：

1. **工具鏈**：npm（`package-lock.json` commit、CI 用 `npm ci`）；Node 24.12.0（`.nvmrc`、`engines`、Dockerfile、CI 一致）；套件版本寫死，挑發佈至少 2 週的版本。TypeScript 用 5.9（typescript-eslint 與 openapi-typescript 還不支援 7.x）；i18next 26／react-i18next 17（27／18 是 2026-11-07 才發佈的新 major）；jsdom 29（30 以上要求 Node 24.15）。
2. **測試**：vitest + Testing Library + axe-core（直接用 axe-core，不用已停止維護的 vitest-axe）；不引入 Playwright。jsdom 無法檢查對比，改成從 `tokens.css` 計算 §3.3 的對比表。
3. **路由**：TanStack Router 的程式碼式路由，集中在 `src/router.tsx`。
4. **session 判斷**：後端沒有「目前 session」的路由，不新增；以 CSRF cookie（非 HttpOnly）當作「可能已登入」的提示，任何 401 `unauthorized` 再導到登入頁。
5. **主題防閃爍**：CSP 不允許 inline script，改用 `light-dark()` 與 `color-scheme`：JS 載入前跟隨系統；手動選擇與系統不同時可能短暫閃一下。
6. **主題與介面語言的切換**暫時放在導覽底部；UI-3 的「外觀」「語言」分區完成後再決定是否移過去。
7. **OpenAPI 取得方式**：`internal/api.OpenAPISpec()` 只註冊路由、不需要資料庫；快照 commit 在 `internal/api/openapi.json`，Go 測試檢查它與路由一致（go job 不需要 Node），web.yml 檢查由它產生的型別已 commit。
8. **非後端路徑的分派**：在 api 層 authn 之前包一層，`/api`、`/mcp`、`/healthz`、`/readyz` 照舊進入 mux；不在 mux 上註冊 `/` catch-all（會改變 405 與未知 `/api/*` 的語意）。UI 只掛在 TCP listener，不掛在 CLI 用的本機 socket。
9. **字型**：Barlow 與 Barlow Semi Condensed 用 Fontsource 的 latin 子集自架（400／500／600 與 Semi Condensed 600）；閱讀區的 Source Serif 4 等到 UI-2 需要時再加。
10. **錯誤類別**：`ErrorCallout` 的類別先依狀態碼推測（網路錯誤與 5xx 是「暫時無法完成」，其餘是「需要你處理」），頁面可以覆寫；後端的 `Error` 還沒有類別欄位（`internal/api/errors.go` 的 TODO）。

### 10.3 待決問題

- **CSP 的 `style-src 'unsafe-inline'`**（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：NFR-SEC-1 只限制 `script-src`。React 的 `style` 屬性走 CSSOM，不受 CSP 限制；但 Radix Dialog 的捲動鎖定（react-remove-scroll）會插入 `<style>` 元素，`style-src 'self'` 會擋掉它。預設允許 `'unsafe-inline'`（只限 style）；風險是 CSS injection（例如用屬性選擇器外洩資料），前提是先有 HTML injection，而 NFR-SEC-2 已禁止原始 HTML。替代方案：用 nonce（需要伺服器端渲染 `index.html`）或換掉會插入 `<style>` 的套件。
- **縮圖與嵌入的網域**（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：`img-src` 開放 `https://*.ytimg.com`、`https://*.hdslb.com`；`frame-src` 開放 `https://www.youtube.com`、`https://www.youtube-nocookie.com`、`https://player.bilibili.com`。如果之後改成由後端代理縮圖，就可以從 `img-src` 移除。UI-3a 另外加了 `blob:`（字幕樣式預覽的 PNG，見 10.7 第 1 點）。
- **`X-Frame-Options: DENY`、`Permissions-Policy`、`Cross-Origin-Opener-Policy: same-origin`** 是 NFR-SEC-1 以外另外加的標頭（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）。

### 10.4 UI-1：媒體庫與「新增」抽屜（2026-11-23）

| 範圍 | 狀態 | 位置 |
|---|---|---|
| 媒體庫列表：縮圖、標題、平台、頻道、發布日、長度、精簡版時間帶、狀態欄、需要處理的列附錯誤說明與動作 | 完成 | `src/pages/LibraryPage.tsx`、`src/pages/library/LibraryRow.tsx` |
| 篩選條件存在 URL：平台、頻道、各步驟的狀態、錯誤類別、原文語言、發布日、長度、時間來源、標題搜尋；已套用條件可個別移除或全部清除 | 完成 | `src/pages/library/filters.ts`、`FilterBar.tsx` |
| keyset cursor 無限捲動、總數、虛擬化（10,000 列只渲染可視範圍） | 完成 | `src/components/VirtualList.tsx`、`LoadMore.tsx` |
| 即時更新：「有 N 個新項目」、步驟狀態只更新那一列、重新連上的提示、aria-live 宣告 | 完成 | `LibraryPage.tsx`、`src/events` |
| 空狀態與篩選結果為空（§4.3） | 完成 | `src/components/EmptyState.tsx` |
| 「新增」抽屜：貼上網址辨識預覽（影片、播放清單、頻道、Podcast 各集、Bilibili 分P）、本機檔案上傳、處理方案、依 `code` 的錯誤與修正動作、toast | 完成 | `src/add` |
| 導覽的「新增」按鈕、`N`、焦點不在輸入框時貼上網址 | 完成 | `src/shell/AppShell.tsx`、`keyboard.ts` |
| 時間帶（精簡版）放進 `web/shared` | 完成 | `web/shared/src/StatusBand.tsx`、[web/shared/README](../../web/shared/README.md) |
| 依頻道／發布日／狀態分組、「格」檢視、儲存檢視、批次操作、垃圾桶檢視 | 未做 | 分組見 10.5 第 3 點；其餘是 M4 |
| 預估花費（`POST /api/estimate`）、「調整這次的設定」、一次貼上多個網址、把檔案拖進視窗 | 未做 | 見 10.5 第 6 點 |

### 10.5 UI-1 的決定

（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）

1. **不改後端**。列表 API 沒有步驟狀態、頻道名稱、縮圖網址，UI 用現有 API 組合：每一列另外讀 `GET /api/items/{id}`（只有渲染出來的列，虛擬列表大約 20 列）；頻道顯示 ID；縮圖只有 YouTube（由影片 ID 推得 `i.ytimg.com`），其他平台用占位。代價是捲動時的 N+1 請求（本機 SQLite，可接受）。之後在 `GET /api/items` 加 `stages` 摘要、頻道名稱與縮圖網址時，列元件改用列表的資料即可。
2. **即時更新不重抓列表**：UI-0 的失效表中 `stage.status` 與重新連上會讓 `items.lists` 失效，重抓時新項目會插到頂端（違反 §3.4、LIB-R9）。改成只讓 detail 失效；重新連上時顯示「列表可能不是最新的」由使用者重新載入。刪除與還原仍讓列表失效（通常是使用者自己的操作）。
3. **預設不分組**（LIB-R4 的「預設依頻道分組」沒做）：列表依建立時間的 keyset cursor 分頁，前端分組會讓同一個頻道散在不同頁；而且 API 沒有頻道名稱。等 API 有 `group_by`（LIB 規格待決問題已記錄）再做。
4. **篩選的 URL 參數**與 API 同名，除了發布日存 `YYYY-MM-DD`（當地時區的整天）、長度存分鐘（`dur_min`／`dur_max`）。頻道沒有列表 API，改成文字輸入，並可以點列上的頻道「只看這個頻道」。
5. **步驟狀態的對應**：`blocked` 依 `block_reason` 對應到需要你處理／超出預算／等你決定，認不得的當成需要你處理。狀態欄顯示最需要注意的步驟（失敗 > 停住 > 等待 Agent > 處理中 > 稍後檢查，否則是最後完成的步驟）。失敗的列提供「從該步驟重新處理」（`POST /api/items/{id}/rerun`），停住（非預算）的列提供「開啟帳號與 Cookie 設定」。步驟的 `message` 是後端的繁中文字，原樣顯示（沒有 code 可以查字典）。
6. **抽屜的範圍**：一次一個網址或一個檔案（上傳 API 一次只收一個檔案）；播放清單整份新增（API 沒有範圍參數，見 LIB／ING 待決問題）；Bilibili 沒有指定分P 時可以選「全部」或輸入編號（resolve 不回分P 數，不為了列出分P 呼叫會跑 yt-dlp 的 estimate）；頻道只提示訂閱（M4）。預估花費與單次覆寫留給之後的切片。
7. **新增相依**：`@tanstack/react-virtual` 3.14.26（2026-11-03 發佈；3.14.27／28 是 11-22 才發佈）。初始 JS 由約 137KB 增加到約 174KB（gzip），沒有另外做 lazy route。
8. **`web/shared` 的接線**：沿用 brief 把 StatusBand 放進 `web/shared`（web/shared/README 原本寫「穩定後再搬」）；`react` 由 `web/client` 解析，lint、prettier、tsc 都涵蓋它，Dockerfile 一併複製。
9. **測試替身**：不引入 MSW，用自寫的 `src/test/mockApi.ts`；vitest 的 `testTimeout` 提高到 15 秒（多個 child 同時跑時，整頁的互動測試在 5 秒內跑不完）。
### 10.6 UI-2：影音詳情與佇列（2026-11-23）

| 範圍 | 狀態 | 位置 |
|---|---|---|
| 詳情的標題列（標題帶原文 `lang`、頻道、平台連結、發布日、長度、原文語言、處理方案、字幕來源）與主要動作（重新處理、下載 SRT、停止處理、移到垃圾桶） | 完成 | `src/pages/item/ItemPage.tsx` |
| 完整版時間帶：72px 底軌、刻度、6px 步驟帶與逐列文字標籤、播放頭、處理範圍；`role="slider"`，←／→ 5 秒、PageUp／PageDown 30 秒、Home／End、點擊跳播、hover 顯示時間與字幕句 | 完成 | `web/shared/src/StatusBand.tsx`（`variant="full"`） |
| 播放器：YouTube 嵌入（postMessage，無回應時改成更新 `start` 重新載入）、Bilibili 嵌入（`t` 參數重新載入＋「在 Bilibili 開啟這個時間」）、本機 `<video>`／Podcast `<audio>` 指向 `/media` | 完成 | `src/pages/item/Player.tsx` |
| 字幕句列表：虛擬化、點時間碼跳播、目前句高亮（色條＋`aria-current`）、播放時跟隨與「回到目前位置」、搜尋、時間來源標示、純文字與字幕自己的 `lang` | 完成 | `src/pages/item/CueList.tsx` |
| 顯示模式：原始、正規化、校正、差異（逐字）、譯文、對照、已編輯；切換時捲回同一個時間；模式存在 URL | 完成 | `CueList.tsx`、`diff.ts`、`cues.ts`、`search.ts` |
| 步驟：狀態、執行方式、provider／model／prompt 摘要、時間、嘗試次數；failed／blocked／有訊息的 skipped 顯示錯誤類別＋訊息＋動作 | 完成 | `src/pages/item/StagePanel.tsx`、`actions.ts` |
| 紀錄分頁（`GET /runs`：狀態、時間、耗時、設定快照） | 完成 | `RunsPanel.tsx` |
| 重新處理對話框：起始與結束步驟、處理方案、單次覆寫（provider、model、prompt 與版本）、只重跑指定 chunk、422／409 顯示 | 完成 | `RerunDialog.tsx` |
| 改用 API 處理（`switch-executor`）、上傳 cookie、更新 yt-dlp、停止處理（`cancel`） | 完成 | `ItemPage.tsx`、`SmallDialogs.tsx` |
| 刪除：`deletion-preview` 的產出數、空間、花費；「同時刪除未修改過的輸出檔」與改過的檔案；回到媒體庫的 toast「復原」 | 完成 | `DeleteDialog.tsx` |
| 輸出：下載（SRT、VTT、ASS、雙語 SRT、純文字）、寫出的檔案、匯入修正過的字幕（422 指出第幾句或檔案第幾行） | 完成 | `OutputsPanel.tsx` |
| SSE：`stage.status` 讓 detail、runs 失效；產生字幕的步驟 `done` 才讓字幕失效、`publish` `done` 才讓輸出失效；頁面層級 aria-live | 完成 | `src/events/invalidation.ts` |
| 佇列：各資源類別的通道（方格＝上限、實心＝使用中）、上限 +／−、整個佇列與各類別的暫停／繼續、暫停橫幅、任務列表（虛擬化、篩選在 URL）、「需要你處理」依步驟計數、影音層級的停止處理 | 完成 | `src/pages/queue` |
| 時間帶的章節線、`Shift + ←／→`、拖曳選取範圍（重新校正這段、複製這段字幕）、說話密度、畫面刻度 | 未做 | API 沒有章節、說話密度、chunk 範圍（10.7 第 8 點） |
| 佇列的優先順序、Agent 任務、租約剩餘時間、單一任務的重試／取消、「全部重試」 | 未做 | API 缺口（10.7 第 8 點） |
| 匯出選單的 ASS 樣式選擇、Markdown 筆記、bundle；兩個版本之間的比較（紀錄分頁） | 未做 | 之後的切片（ASS 用預設樣式） |

### 10.7 UI-2 的決定

（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）

1. **不改後端**。全部用既有 API 組合；沒有新增路由、欄位，OpenAPI 與 `schema.d.ts` 沒有變動。
2. **路由參數可能是代號**（`/items/yt:…`）：detail 讀到之後，字幕、紀錄、輸出、媒體、重新處理等呼叫與 query key 一律用 `detail.id`，SSE 的失效表才對得上；用代號進來時頁面另外讓路由參數的 detail 失效。
3. **字幕的快取不放在 `items.detail` 底下**（`items.cues(id, {variant, lang})`）：每個 `stage.status` 都讓 detail 失效，上千句的字幕不能跟著重抓。只在 transcript、normalize、segment、correct、translate `done` 時失效；重新讀取期間保留舊資料（`keepPreviousData`），列表不卸載，捲動位置不變。
4. **差異的比較方式**：校正後（`corrected`）對「校正前實際存在的最近變體」（segmented → normalized → source）。兩邊依**時間重疊**分組（校正可能合併或拆句），每組以 code point 做 LCS 逐字比對（不切開 surrogate pair）；超過 400 萬格時退化成整段刪除＋新增。刪除用 `<del>`（刪除線）、新增用 `<ins>`（底線），另有 visually-hidden 的「刪除：」「新增：」文字。沒有校正結果的段落顯示原文並標「尚未校正」。**自寫元件，不用 CodeMirror merge view**：字幕是逐句的短文字，自寫的 diff 約 2KB；整個詳情頁是 lazy route（gzip 13.8KB），初始 JS 188.5KB（UI-1 是 174.2KB，增加的是佇列頁與共用的 query key／VirtualList）。
5. **模式依步驟狀態決定**（預設模式在第一次決定後固定，SSE 改變步驟狀態不會換掉正在看的模式；「校正」在 corrected 還不存在時顯示校正前的句子並標「尚未校正」；差異模式在校正完成後，沒有對應的句子代表 LLM 刪掉了整句，整段顯示成刪除；分組要求重疊超過 0.1 秒或句子長度的一半，相鄰句子重疊幾毫秒不會串成一組）：transcript `done` 才有「原始」、normalize `done` 才有「正規化」、correct 已開始（不含 skipped）才有「校正」「差異」、translate `done` 才有「譯文」「對照」、有 `edited` 字幕才有「已編輯」。預設是校正（只在校正完成時）> 正規化 > 原始。譯文的語言用最近一次執行的設定快照 `language.targets`，多個時可以選。沒有 `convert`／`localize` 的區分（pages.md §4 的「只差字形」規則）：API 沒有提供，譯文與對照只在 translate 完成時出現。
6. **YouTube 播放器不載入 IFrame API 的 script**：CSP 的 `script-src` 只有 `'self'`，不放寬。用嵌入網址的 `enablejsapi=1&origin=…` 加 postMessage（送 `listening`，收 `infoDelivery.currentTime`；跳播送 `seekTo`、`playVideo`）；訊息只接受 YouTube 的 origin 且 `source` 是這個 iframe。`listening` 每 250ms 重送直到收到第一則訊息，4 秒內沒有收到就降級成「更新 `start` 重新載入」；訊息只送到嵌入的 origin（nocookie）。協定是 YouTube 未公開文件化的部分，**需要在真實瀏覽器人工驗證**（LIB-03 是 `@manual`）。頁面的 `Referrer-Policy` 是 `same-origin`，YouTube 嵌入需要來源網域，只在 iframe 元素上設 `referrerpolicy="strict-origin-when-cross-origin"`（不改全站標頭）。Bilibili 沒有公開的控制介面，跳播用 `t` 參數重新載入，另附「在 Bilibili 開啟這個時間」。
7. **步驟的 `actions` 是後端的繁中字串**：`src/pages/item/actions.ts` 用對照表對應到三語文案與實際動作（重試、重新翻譯、重新校正這段、處理該檔案後重新發布、更換模型、開啟 Prompt → 重新處理對話框並預選該步驟；改用 API 處理 → `switch-executor`；上傳 cookie → `POST /api/cookies`；更新 yt-dlp → `POST /api/tools/yt-dlp/update`；設定 API key、語音轉錄、額度、安裝工具 → 設定頁對應分區）。認不得的字串原樣顯示並附「開啟設定」。失敗與停住的步驟一定有「從這個步驟重新處理」，等待 Agent 一定有「改用 API 處理」。
8. **API 缺口**（記在這裡，之後的後端切片處理）：
   - `Stage.actions` 應改成動作代碼（現在是繁中按鈕文字，前端只能比對字串）。
   - 沒有 log 片段（OBS-2 的「相關 log 片段」）：步驟面板改連到「紀錄」分頁，顯示 `GET /runs` 的狀態、時間與設定快照；`runs` 也沒有花費、provider 實際回應等欄位。
   - rerun 沒有 `estimate`／`estimate_usd`：對話框顯示「無法預估」。沒有拿 `POST /api/estimate` 代替（它針對網址，可能呼叫 yt-dlp）。
   - 沒有 provider 列表 API：provider 用文字輸入，建議值取自處理方案設定。
   - 沒有列出影音有哪些字幕變體與語言的 API：模式依步驟狀態推得，`edited` 用一次 `variant=edited` 的查詢確認。
   - 沒有章節、說話密度、LLM chunk 的時間範圍：時間帶只畫整條步驟狀態，沒有章節線、拖曳選取、chunk 進度。
   - `ItemDetail` 沒有處理方案名稱：從設定快照的 `profile` 取（沒有就不顯示）。
   - 佇列：`GET /api/queue` 只列內建 worker 的任務（沒有 Agent 任務、租約剩餘時間、優先順序欄位）；`POST /api/tasks/{id}/retry`、`cancel` 回 501；沒有調整優先順序的 API。頁面上明說，並改提供影音層級的「停止處理」。
   - 「需要你處理」沒有彙總 API：依步驟各查一次 `GET /api/items?status=<stage>:failed&status=<stage>:blocked&limit=1` 的 `total`（7 個請求；同一部影音可能在不同步驟各算一次）。佇列頁收到 failed／blocked／pending／done／skipped 的 `stage.status`、刪除、還原或重新連上時，2 秒 debounce 後重新讀取。
9. **其他**：用垃圾桶中的影音連結進入詳情時提供「從垃圾桶還原」；cookie 對話框關閉時清掉貼上的內容；拖曳捲軸（不只 wheel／touch／按鍵）也會停止跟隨播放。
10. **501 不重試**：TanStack Query 的預設重試排除 501（功能還沒做），佇列沒有啟用時立即顯示說明。
11. **axe 不進 iframe**（`iframes: false`）：嵌入的播放器是跨來源的；`routes.a11y.test.tsx` 用 `defaultRoutes()` 替身，不連網路。
12. **新增相依**：無。`api.patch`、`VirtualListHandle.scrollToIndex`／`firstVisibleIndex`、`formatBytes`／`formatUsd` 是新增的 API，不改既有的意義。
### 10.8 UI-3a：Prompt 庫、處理方案、字幕樣式（2026-11-23）

| 範圍 | 狀態 | 位置 |
|---|---|---|
| 導覽的「調整」組（Prompt 庫、處理方案、字幕樣式）、六條路由；`/prompts/$id` 是 lazy route | 完成 | `src/shell/AppShell.tsx`、`src/router.tsx` |
| Prompt 列表：依用途分組（分段、校正、翻譯、選圖、筆記、其他）、目前版本與最新版本、使用中的處理方案、最後修改時間；新增 prompt | 完成 | `src/pages/prompts/PromptsPage.tsx`、`NewPromptDialog.tsx` |
| Prompt 編輯：版本列（切換、未儲存時確認放棄）、CodeMirror 6（`{{變數}}` 高亮、未定義／無法解析的變數 lint 成錯誤，另附文字清單）、可用變數、另存新版本（422 `undefined_variables`）、設為預設、內建唯讀＋另存為新的 prompt | 完成 | `PromptEditorPage.tsx`、`PromptCodeEditor.tsx`、`promptModel.ts` |
| 試跑面板：影音（搜尋＋選擇）、範圍、provider、model；結果用逐字差異＋驗證結果、用量與花費；501／429／409 `needs_action` 各有說明 | 完成 | `playground.tsx` |
| 比較版本：逐行差異；可選「同一段字幕試跑兩個版本」（寫明花費兩倍）並排顯示輸出 | 完成 | `PromptEditorPage.tsx` 的 `CompareDialog` |
| 刪除 prompt：不可復原，先確認（寫明版本數）；409 `prompt_in_use` 列出 `used_by`（處理方案連到編輯頁，影音覆寫連到影音詳情，其他純文字） | 完成 | `DeleteDialog` |
| 處理方案列表與新增、複製（POST，剝掉 null）、設為預設 | 完成 | `src/pages/profiles` |
| 處理方案編輯：流程列（不執行的步驟變淡、刪除線）、編號步驟：1 字幕來源（順序、平台機器翻譯字幕）、2 分段（執行方式、provider／model、prompt＋版本、段內切分、有人工字幕時）、3 校正（＋最低相似度）、4 翻譯（＋OpenCC、目標語言、中文只差字形時）、5 輸出（目標、路徑、格式、ASS 樣式） | 完成 | `ProfileEditorPage.tsx`、`profileModel.ts` |
| 儲存：RFC 7396 merge patch（只送改過的鍵）、422 `errors[].path` 顯示在欄位旁、版本衝突提示 | 完成 | 同上 |
| 字幕樣式列表（內建在前、自訂在後）、複製成自訂樣式、匯入 .ass（讀檔內容送 `content`） | 完成 | `src/pages/styles` |
| 字幕樣式編輯：原文行／譯文行／版面三個分頁（鍵盤可切換）、目標語言與原文語言（帶入預設字型）、前端先檢查顏色與範圍、PATCH 只送改過的欄位；內建唯讀 | 完成 | `StyleEditorPage.tsx`、`styleModel.ts` |
| 預覽：`POST /api/ass-styles/preview` 回 PNG，debounce 300ms、新請求取消舊的；沒選影音、欄位有錯、失敗（「預覽尚未可用：原因」）各有狀態，不假造圖片 | 完成 | `StylePreview.tsx` |
| 逐字差異元件（`diffChars`、`DiffText`） | 完成 | `web/shared/src/diff.ts`、`DiffText.tsx` |
| 被頻道訂閱使用的處理方案，修改前提示受影響的訂閱 | 未做：M4 才有訂閱 API | — |
| 處理方案刪除、版本紀錄（`GET /api/profiles/{id}/versions`）的檢視與還原 | 未做：pages.md §8 沒有要求，留給之後 | — |
| 處理方案的 `llm`、`agent`、`subtitle_rules`、`glossary`、`supadata`、`translate.length_ratio`、`outputs[].lang`、`on_user_change` | 未做：表單不顯示，但原樣保留（儲存只送改過的鍵） | — |
| M3／M4 的步驟（sponsorblock、chapters、frames、note） | 不顯示：schema（`internal/pipeline/schema.go`）還不接受 | — |
| 內建樣式用同一張畫面渲染成縮圖並排比較 | 未做：預覽需要指定影音，每個縮圖都要跑一次 ffmpeg | — |
| libass 真實渲染的視覺驗收、CodeMirror 在真實瀏覽器的鍵盤與螢幕閱讀器操作 | owner 手動驗收 | — |

### 10.9 UI-3a 的決定

（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）

1. **唯一的後端改動：CSP 的 `img-src` 加 `blob:`**（`internal/webui/webui.go`，測試檢查 `img-src` 只有 `'self'`、`blob:` 與兩個縮圖網域）。預覽 API 回 PNG，前端以 object URL 顯示；jsdom 不執行 CSP，沒有這項時測試全綠、真實瀏覽器卻是破圖。替代方案（改成 GET 圖片網址、data URI）都要改 API 或放寬更多。風險：`blob:` 只能由同源的 JS 產生，前提是已經有 script 執行。
2. **「被哪些處理方案使用」在前端組合**：`GET /api/prompts` 沒有這項資料，列表另外讀 `GET /api/profiles`，解析 `config.stages.*.prompt`（`<id>`、`<id>@default`、`<id>@N`）。代價：多一次請求；只看處理方案目前的版本，看不到全域設定、頻道與影音層的引用（刪除時後端的 409 `used_by` 會列出全部）。
3. **可用變數寫在前端**（`promptModel.ts` 的 `PROMPT_VARIABLES`，規則與 `internal/prompts/vars.go` 一致）：後端所有類型共用 7 個變數、沒有清單 API。後端改了要同步；依類型區分時改 `promptModel.ts`。
4. **試跑只能用已儲存的版本**（API 只接受 `<id>@N`）：有未儲存的修改時提示「試跑已儲存的 vN」。試跑前沒有預估 API，只在完成後顯示用量；`estimate_usd` 是 null 時寫明「計價表沒有這個模型的價格」，不自己算。
5. **刪除 prompt 用確認框**（§3.5 不可復原的操作）：後端是硬刪除，沒有可以還原的方式。
6. **處理方案的樂觀鎖在前端補**：PATCH 沒有 If-Match。儲存前先讀一次，版本和開始編輯時不同就跳出衝突（重新載入／仍要儲存：只寫改過的鍵）；儲存後回應的版本不是「開始時 + 1」時也明確告知。兩次請求之間仍有很小的窗口；要完全關閉需要後端支援 `If-Match` 或 `expected_version`。
7. **處理方案表單編輯 config 的副本**，儲存時算 merge patch；選「沿用全域設定」就刪掉該鍵（送 null），讓步驟的 executor 回到「沿用全域設定」時整個 `stages.<步驟>` 一起刪掉（寫出來的步驟會被檢查完整性，D8）。「複製」用已儲存的設定，不含未儲存的修改。
8. **provider 沒有列表 API**：文字欄位加 `datalist`（內建的 anthropic、openai），model 自由輸入；試跑面板同樣。
9. **用語**：步驟 `segment` 沿用既有字典的「分段」（`stage.segment`），pages.md 的「斷句」沒有改既有字串；Prompt 庫的分組也用「分段」。英文的處理方案沿用用語表的 Preset。
10. **字型**：API 依 `lang` 帶入預設字型後，分不出原本是不是空字串（自動）。表單只送改過的欄位，所以打開表單不會把自動字型寫死；把字型欄位清空就送 `""`（依語言自動）。顏色用文字欄位（`<input type="color">` 會丟掉不透明度）加色塊。
11. **共用元件**：`SelectField`、`ItemPicker`（試跑與樣式預覽都要選影音；只列 `GET /api/items` 的前 50 部，用標題搜尋縮小）放進 `components/`（ItemPicker 用獨立的 `queryKeys.items.picker(q)`，不放在媒體庫 infinite query 的 `items.lists()` 前綴下）；逐字差異放 `web/shared`，與 UI-2 分支的 `pages/item/diff.ts` 形狀相同，合併時改成都從 `@shared` 匯入、刪掉重複的一份。
12. **新增相依**：`@codemirror/state` 6.7.6、`view` 6.43.15、`commands` 6.11.1、`lint` 6.9.7（都發佈超過 2 週）；transitive 的 `@lezer/common`、`style-mod` 用 npm `overrides` 鎖在 1.5.5、4.1.4（最新版未滿 2 週）。CodeMirror 只在 lazy chunk（約 104KB gzip），初始 JS 由約 174KB 增加到約 198KB（三個頁面與共用元件）。CodeMirror 透過 style-mod 插入 `<style>`，依賴 10.3 的 `style-src 'unsafe-inline'`。
13. **eslint**：`i18next/no-literal-string` 的屬性排除清單加上事件處理器（`on[A-Z].*`）與 `form`、`accept`、`list`、`stage`、`path`：這些屬性裡的字串是欄位路徑與列舉值，不是顯示的文字。
14. **測試**：`src/test/setup.ts` 補上 `Range.getClientRects`／`getBoundingClientRect`（CodeMirror 在 jsdom 需要）；預覽的 debounce 用真實計時器驗證（連續改三次只送一次、內容是最後一次）。


頁面清單（2026-11-23，M5 規格展開時暫定，視覺與文案待 Hub UI 切片補上）：登入、帳號（登入方式、Hub token、我的分享）、管理（使用者與群組、檢舉、下架申請、稽核紀錄）、下架申請表單見 [HACC](../specs/HACC-hub-accounts.md) 的介面；首頁與搜尋、影片頁（版本比較）、版本頁見 [HREP](../specs/HREP-hub-repository.md) 的介面；Client 端的「分享到 Hub」對話框與設定頁的 Hub 區段見 [HSHR](../specs/HSHR-hub-sharing.md) 的介面。

### 10.10 UI-3b：設定、詞彙表與術語候選、設定匯出與匯入（2026-11-23）

| 範圍 | 狀態 | 位置 |
|---|---|---|
| 設定頁版面：左側分區清單、右側表單、每個分區各自儲存；窄螢幕（< 768px）清單收成頂部的下拉選單；未知分區導到第一個分區 | 完成 | `src/pages/SettingsPage.tsx`、`src/styles/settings.css` |
| 服務與金鑰：7 個 provider 的 API key（只顯示末四碼，只能設定、更換或清除，清除先確認）、Supadata 取得字幕的方式、`command` provider 的唯讀說明 | 完成 | `src/pages/settings/ServicesSection.tsx` |
| 服務與金鑰：「測試連線」、模型停用公告、列出設定檔中的 `command` provider | 未做：後端沒有對應的路由（只有 `GET /api/providers/{id}`，沒有列表） | — |
| 帳號與 Cookie：上傳 YouTube／Bilibili cookie（貼上或選擇檔案）、這次上傳的數量與時間、`cookie_*` 錯誤的說明 | 完成 | `AccountsSection.tsx` |
| 帳號與 Cookie：最後一次成功使用的時間、是否失效 | 未做：後端沒有讀取 cookie 狀態的 API | — |
| 語言：預設目標語言、中文變體、原文字幕的語言優先順序（新增、移除、上下移）、平台翻譯字幕、fallback；介面語言 | 完成 | `LanguageSection.tsx` |
| 輸出：本機影片旁與資料夾的說明（在處理方案中設定）；vault 與 hook 唯讀說明「尚未啟用（M3 起提供）」 | 完成 | `OutputsSection.tsx` |
| MCP 與 Agent：token 列表（名稱、前綴、scope、建立、最後使用、到期、狀態）、建立（scope、到期 7 天／30 天／90 天〔預設〕／1 年／不設到期）、只顯示一次並可複製、撤銷先確認；Claude Code 指令；Agent 的 `max_claims`、`lease`、`timeout`、`on_timeout`、`on_validation_failure` | 完成 | `AgentSection.tsx` |
| token 輪替（AUTH-R21） | 未做：M4 | — |
| 詞彙表：全域與頻道詞彙表的檢視與整份取代；頻道的術語候選（接受、拒絕、409 `term_candidate_decided` 顯示「已被處理」並重新整理；已處理的收在 `<details>`） | 完成 | `GlossarySection.tsx` |
| 詞彙表：修改後接受、批次決定、頻道選單 | 未做：沒有 API（GLOS 是 M4；沒有頻道列表 API） | — |
| 系統：外部工具的路徑、來源與能否使用；更新 yt-dlp、回到上一個版本；資料目錄與監聽位址 | 完成 | `SystemSection.tsx` |
| 系統：工具的版本號、磁碟用量、媒體檔保留天數 | 未做：`GET /api/system` 沒有這些欄位（版本只在更新的結果中顯示） | — |
| 匯出與匯入：下載 YAML、匯入前先確認、422 逐項顯示 `errors[]`、成功時列出新建立／新版本／沒有變更／預設處理方案 | 完成 | `ConfigSection.tsx` |
| 匯入前的 dry-run 差異預覽 | 未做：後端沒有 dry-run（SEC 規格的待決問題） | — |
| 外觀：淺色／深色／跟隨系統（和導覽底部共用同一個值） | 完成 | `AppearanceSection.tsx`、`src/theme/useTheme.ts` |
| 外觀：介面密度 | 未做：design-language.md 沒有定義密度 | — |
| 登入：修改密碼（目前的密碼、新密碼、再輸入一次；錯誤與鎖定倒數） | 完成（新增後端路由，見決定 1） | `LoginSection.tsx`、`internal/api/auth_password.go` |
| 登入：工作階段列表、反向代理驗證、OIDC | 未做：M4 | — |
| 通知、預算 | 分區列出，內容是「M4 起提供。」，沒有表單（UI-4 已補上，見 10.12） | `PlannedSection.tsx` |
| 測試：每個分區一個 axe 測試（有 API 替身資料）、key 只顯示末四碼且不留在畫面上、token 只顯示一次、cookie 上傳與錯誤、改密碼錯誤與不一致、候選 409、匯入逐項錯誤、匯出、更新 yt-dlp、語言整段送出、主題同步 | 完成 | `src/pages/settings/settings.test.tsx` |

### 10.11 UI-3b 的決定

（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）

1. **唯一的後端改動：`POST /api/auth/password`**。AUTH-R2 要求「修改密碼要透過設定頁」，但沒有路由，`notesteme password set` 也還沒實作，沒有替代方案。只有 owner（token 即使有 `admin` 也 403）、與登入共用鎖定（AUTH-R6）、目前的密碼錯誤回 422 `invalid_password`（不用 401，避免被當成 session 失效而導到登入頁）、新密碼只要求非空、成功後在同一個交易中撤銷目前以外的所有 session。細節記在 AUTH 規格的待決問題。其他缺口（測試連線、cookie 狀態、工具版本、磁碟用量、匯入 dry-run、頻道列表）都**不補後端**，UI 明確寫「後端還沒有提供」；有排程的寫「M3／M4 起提供」，沒有排程的不捏造里程碑。
2. **分區**：依 pages.md §12 的順序，另外加一個「匯出與匯入」分區（`config`），不併進「系統」：兩者的權限與風險不同（匯入會取代處理方案）。詞彙表放在設定頁的「詞彙表」分區（pages.md §12 已列），不另開 `/channels/:id/glossary`。
3. **未知分區**導到第一個分區（`<Navigate replace>`，不改 `router.tsx`）；原本 UI-0 的「顯示找不到」測試一併改掉。
4. **窄螢幕**：分區清單在 767px 以下隱藏，改成頂部的 `<select>`（切換時導到該分區）。
5. **主題與介面語言的快速切換保留在導覽底部**（UI-0 決定 6 的後續），設定頁的「外觀」「語言」分區有同樣的選項；主題改由 `theme.ts` 的 `subscribeTheme`／`useTheme` 共用，兩邊不會不同步。介面語言本來就由 i18next 共用。
6. **secrets 的處理**：API key、cookie、token 明文、密碼只放在元件的 `useState`，送出時立即清空（失敗也要重新輸入）；不用 TanStack Query 的 `useMutation`（`variables`、`data` 會留在 MutationCache），不放進 URL、localStorage、toast、錯誤訊息。API key 欄位 `type="password"`、`autocomplete="new-password"`；cookie 的 textarea `autocomplete="off"`，選擇檔案時內容不顯示；token 對話框關閉時清掉。
7. **服務與金鑰的 provider 清單**照抄後端白名單（`internal/secrets/services.go` 的 `allowedServiceProviders`）：後端沒有列表 API。後端增減 provider 時要同步改 `SERVICE_PROVIDERS`。PUT services 是部分更新，每次只送正在改的那一個 provider（`api_key: ""` 是清除）。
8. **整段取代的區段**（`language`、`agent`、`supadata`）：GET 後在本地修改、整段 PUT；沒有填的欄位直接省略（不送 `""` 或 `null`，後端不接受）。`language` 的 GET 已合併預設值；`agent` 沒有存過時是 `{}`，表單顯示預設值的提示（AGT 待決問題 A3 的預設）。422 的 `errors[]` 逐項顯示 path 與訊息。
9. **輸出**：全域設定沒有 `outputs` 區段（`/api/settings/outputs` 不存在），所以「輸出」分區只說明在處理方案中設定；沒有連到 `/profiles`（UI-3a 同時在做，避免互相依賴的路由型別）。
10. **詞彙表的頻道**用文字輸入（UUID 或 `平台:頻道 ID`），選擇只在元件 state，不加 search 參數（不改 `router.tsx`）。接受候選後同時讓頻道詞彙表重新讀取；409 `term_candidate_decided` 顯示「已被處理」並重新讀取候選。
11. **token 的 Claude Code 設定片段**用 `claude mcp add --transport http notesteme <origin>/mcp --header "Authorization: Bearer <token>"`；分區上顯示占位字串，建立後的對話框可以複製含真正 token 的指令。輪替（AUTH-R21）是 M4。
12. **匯入沒有預覽**：先跳確認框說明「同名取代、不刪除、可能改預設處理方案」，再套用；成功後讓 `profiles` 的 query 失效。匯出用 Blob 下載 `notesteme-config.yaml`（`blob:` 的下載不受 CSP 的 `connect-src` 限制）。
13. **bundle**：設定頁沒有做 lazy route（不改 `router.tsx`），初始 JS 由約 174KB 增加到約 194KB（gzip，預算 300KB）。


### 10.12 UI-4：訂閱、成本、筆記模板、通知與預算、影音詳情的畫面與筆記（2026-11-23）

| 範圍 | 狀態 | 位置 |
|---|---|---|
| 導覽：工作組加「訂閱」、調整組加「筆記模板」、系統組加「成本」；五條新路由都是 lazy route | 完成 | `src/router.tsx`、`src/shell/AppShell.tsx` |
| 訂閱列表（頻道或網址、平台、檢查頻率、上次與下次檢查、處理方案、最近的錯誤）、空狀態 | 完成 | `src/pages/subscriptions/SubscriptionsPage.tsx` |
| 新增訂閱抽屜：網址、處理方案、檢查頻率、回補範圍、過濾條件（片長、Shorts、直播、標題 regex）、「同一個頻道只在一台實例上訂閱」提醒、422 逐欄、重複訂閱連到已有的訂閱 | 完成 | `SubscriptionForm.tsx`、`subModel.ts` |
| 試跑預覽（`POST /api/subscriptions/preview`、`/{id}/preview`）：會連外與消耗 rate limit 的提示、第一次要按「試跑預覽」、之後 800ms debounce 並取消舊請求、429 顯示 `retry_after_s`、`none` 模式的說明、略過原因的三語對照 | 完成 | `SubscriptionForm.tsx` |
| 訂閱詳情：PATCH 只送改過的欄位、立即檢查、停用（確認）、啟用、刪除（確認，寫明影音保留）、探索到的條目（依決定篩選、limit 每次加 200，上限 5000） | 完成 | `SubscriptionPage.tsx` |
| OPML／Takeout 匯入（multipart）與結果（新增數、重複、逐列失敗） | 完成 | `SubscriptionsPage.tsx` |
| SSE：`subscription.checked`／`check_failed` 讓訂閱的 query 失效 | 完成 | `src/events/{types,invalidation}.ts` |
| 成本頁：本月花費／預算／預計月底（2 位小數）與「調整預算」、`notices`（`prices_stale` 依 code 提供「確認計價表」）、找不到單價的次數、日期範圍與分組在 URL、依分組的長條圖（附同資料的表格）、明細（cursor 分頁，未知單價顯示「未知單價」、預留顯示「預留」）、CSV 匯出（範圍超過 366 天時停用） | 完成 | `src/pages/costs` |
| 計價表：內建與覆寫、確認日期與過期、確認內建計價表、新增或覆寫單價（已有單價時先確認舊價 → 新價）、移除覆寫（確認） | 完成 | `src/pages/costs/PricesPanel.tsx` |
| 筆記模板列表（內建在前、版本、檢查數）、新增與複製（`from`） | 完成 | `src/pages/noteTemplates/NoteTemplatesPage.tsx` |
| 筆記模板編輯（三欄）：模板內文、名稱、「閱讀」按鈕、可用變數、五項自動檢查（全部取消時送 `checks: []`）、另存新版本（422 顯示在內文旁）；內建唯讀只能複製；中欄連到 `note-default`、`note-map-default`；右欄用一部影音的筆記預覽已儲存的版本（`<id>@<版本>`），`409 no_note` 顯示說明 | 完成 | `NoteTemplateEditorPage.tsx` |
| 設定的「通知」分區：端點列表（種類、主機／末四碼、事件、啟用狀態）、新增與編輯（四種 notifier 的欄位、密鑰只寫不讀、七個事件）、傳送測試通知與結果、停用與刪除（確認）、最近的通知（outbox） | 完成 | `src/pages/settings/NotificationsSection.tsx` |
| 設定的「預算」分區：月預算、單部上限、提醒門檻、預算時區；本月花費、預留與剩餘 | 完成 | `src/pages/settings/BudgetSection.tsx` |
| 影音詳情的「畫面」分頁：印樣（只看選中的／全部候選）、時間（點擊跳播）、章節、圖說、排除原因、張數摘要與不足提示 | 完成 | `src/pages/item/NotePanels.tsx` |
| 影音詳情的「筆記」分頁：閱讀字體渲染（標題、段落、清單、callout、表格、程式碼、連結只留 http／https／mailto、wikilink 轉文字）、同一部影音的時間連結點擊跳播、`![[…frame_NNNN.jpg]]` 對應到畫面圖片、自動檢查、寫入 vault 的路徑與 commit、過時提示與「重新產生筆記」、下載 Markdown | 完成 | `NotePanels.tsx`、`src/notes` |
| 總覽（`/`，依賴 LIB-09 `/api/overview`）、媒體庫的批次操作（LIB-08 `POST /api/items/batch`）、搜尋、Hub | 未做：後端還沒有（不放會 501 的入口） | — |
| 訂閱列表的「新內容」欄、處理方案被訂閱使用時的提示 | 未做：API 沒有每個訂閱的新內容數，也沒有「哪些訂閱用這個處理方案」的查詢（可以從列表的 `profile` 組合，留給之後） | — |
| 筆記模板的檔名與資料夾版面規則 | 不提供：vault 只在設定檔定義（ADR-0020、VAULT V1），頁面只說明 | — |
| `G` 然後 `S` 的快捷鍵、`1`–`4` 切換詳情分頁 | 未做：快捷鍵仍是之後的切片（10.1） | — |
| 真實瀏覽器的視覺、鍵盤與螢幕閱讀器驗收（`@ui @manual`） | owner 手動驗收 | — |

驗證：vitest 34 個檔案 375 個測試（每個新頁面與分區都有 axe 測試，`a11yRoutes` 加了 10 條路徑）、i18n 三語各 1384 個 key、初始 JS 260.4KB gzip（UI-3b 後約 241.5KB；增加的主要是三語字串與設定頁的兩個分區，頁面本身都在 lazy chunk）。

### 10.13 UI-4 的決定

（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）

1. **不改後端**：沒有新增路由或欄位，OpenAPI 與 `schema.d.ts` 沒有變動。
2. **介面語言**：brief 寫的 zh-TW／ja 與 repo 不符；照 repo 的 `zh-Hant`、`zh-Hans`、`en`。
3. **scope**：沒有查詢目前身分與 scope 的 API（`/api/auth` 只有 login、logout、password，`/api/auth/sessions` 尚未實作）。Web UI 以 owner 的 session 登入，有全部權限，所以不依 scope 隱藏按鈕；token 才有 scope 限制，萬一收到 403 由 `ErrorCallout` 顯示。之後有 `GET /api/auth/me` 之類的路由時再依 scope 停用按鈕。
4. **金額**：API 回傳的是浮點數美元（micro-USD ÷ 1e6），前端不自己加總，只顯示伺服器算好的總額、預留與預計。頂部一行與預算分區依 COST 規格用 2 位小數；明細的單筆金額常小於 1 美分，沿用 `formatUsd`（小於 $0.01 時最多 4 位，**偏離規格「四捨五入到兩位」**）；單價最多 6 位。幣別符號用 `Intl`（繁中、簡中是 `US$`），與 UI-2 的 `formatUsd` 一致，不硬寫 `$`。`state: unknown` 顯示「未知單價」，不顯示 `$0.00`；`unknown_calls` 在頂部說明並連到計價表。
5. **訂閱的試跑預覽不在開頁時自動執行**：預覽會連外並消耗 rate limit（SUB-R10、handoff 給 UI 的提醒），開啟頁面或抽屜時不送；按過一次「試跑預覽」之後，修改設定才以 800ms debounce 即時更新（pages.md 的「即時更新」），新請求取消舊的，429 不自動重試。新增時網址要先像 http(s) 網址才能預覽。
6. **訂閱詳情是頁面，不是抽屜**：pages.md §5 畫的是抽屜，§2 的路由表有 `/subscriptions/:id`；做成獨立頁面（可加書籤，條目列表也放得下）。
7. **訂閱 PATCH 只送改過的欄位**：`backfill` 一送就會在下一次檢查重新評估（SUB-R3），沒改就不送；`overrides` 是整個取代，表單沒有它，永遠不送；處理方案改回預設送 `profile: ""`。片長在表單用分鐘（API 是秒）。
8. **條目列表沒有 cursor**：`GET /entries` 只有 `limit`，「載入更多」改成每次把 limit 加 200（上限 5000），不另做虛擬化。
9. **成本的長條圖**：用表格的列畫水平長條（`aria-hidden`），金額與次數以文字放在同一列，所以不另外需要文字替代；不加圖表套件。依日期分組時每列是一天（不是直立的長條）。
10. **CSV 匯出**是 `<a download>` 的 GET；錯誤時瀏覽器會下載到 JSON，所以前端先檢查範圍（1～366 天）不合法時不顯示連結。匯出用回應中的 `from`、`to`（伺服器依預算時區決定的預設值），不用瀏覽器的時區計算本月。
11. **計價表放在成本頁**（`#prices`），`prices_stale` 的「調整單價」捲到那裡；pages.md §11 沒有畫計價表，COST-03 與 COST-R13 的動作需要它。覆寫已有的單價時先在對話框內顯示「舊價 → 新價」，再按一次「確認覆寫」。
12. **通知的密鑰**比照 UI-3b 決定 6：只放在元件 state，送出時立即清空（失敗也要重新輸入），不用 `useMutation`；編輯時沒有重新輸入就不送（後端保留原值，NOTIFY-R6），清空選填的非機密欄位送 `null`。ntfy 的 token 目前沒有「移除」的操作（PATCH 的 `null` 可以刪，UI 沒有做）。
13. **還沒有產生端的事件**（`digest`、`alert`、`source.better_available`：DIG、OPS 告警、SRC-10 尚未實作）仍可勾選，旁邊標示「尚未會發出」。
14. **預算分區** PUT 只送 `monthly_usd`、`per_item_usd`、`warn_at_percent`、`timezone` 四個欄位（GET 另外回傳的 `month`、`spent_usd` 等送回去會 422）；空白送 `null`（不限）。時區用文字輸入，驗證交給後端。
15. **筆記模板**：沒有單筆 GET，編輯頁從列表找；預覽只能用已儲存的版本（API 吃 `<id>@<版本>`），有未儲存的修改時提示。三欄的中欄只連到 Prompt 庫：沒有 API 把模板和 prompt 綁在一起（處理方案分別選）。可用變數與五項檢查寫在前端（`NOTE_CHECKS`、`TEMPLATE_VARS`，與 NOTE-R7、R11 一致；後端改了要同步）。
16. **Markdown renderer 自寫**（`src/notes/markdown.ts`、`NoteMarkdown.tsx`）：輸出 React element，不用 `dangerouslySetInnerHTML`，不加相依；原始 HTML 一律是文字，連結只留 http／https／mailto，`![](…)` 外部圖片只顯示替代文字，wikilink 只留文字，標題層級往下移一層（頁面已有 h1）。只支援筆記會用到的語法（巢狀清單攤平成一層）。
17. **筆記中的畫面嵌入**以檔名的 `frame_NNNN` 對應到 `GET /api/items/{id}/frames/{n}?size=thumb`（`internal/notes/render.go` 的 `%04d` 就是畫面編號）。待決：frames 重跑後版本會換，筆記若還沒重新產生，NNNN 可能指到新版本的另一張畫面（vault 內的檔案是 pin 住的舊版本，不受影響）；需要的話 API 要讓筆記帶畫面版本。
18. **時間連結跳播**只在連結指向同一部影音時（比對主機、路徑與 YouTube 的 `v`；`t=` 或 `#t=`）攔截，其他連結開新分頁。
19. **畫面與筆記只在切到該分頁時讀取**，404 `no_frames`／`no_note` 是「還沒有」，顯示說明不當成錯誤；SSE 的 `stage.status` 在 frames、note、publish `done` 時讓兩者失效。詳情頁的分頁順序是字幕、畫面、筆記、步驟、紀錄、輸出（README §3.6 的 1–4 是字幕、畫面、筆記、紀錄）。
20. **共用檔案只增不改**：`router.tsx`、`queryKeys.ts`、locale JSON、`SettingsPage.tsx`（通知、預算改接新的元件，`PlannedSection` 保留但目前沒有分區使用）、`a11yRoutes.ts`、`fixtures.ts`；`router.test.tsx` 的導覽清單與 `settings.test.tsx` 的「通知與預算只說明 M4 起提供」測試依新行為修改。新的樣式在 `src/styles/m4.css`（由 `main.tsx` 載入）。

### 10.14 UI-5：搜尋、術語審核、bundle、系統告警與備份、摘要與彙整、更好的字幕來源（2026-11-24）

| 範圍 | 狀態 | 位置 |
|---|---|---|
| 字幕全文搜尋 `/search?q=&lang=`（lazy route）：送出前檢查空白與 200 個 code point、伺服器 422 顯示訊息、依影音分組、命中範圍依 `highlights` 切成 `<mark>`（React 節點，不用 innerHTML）、前後文、字幕帶 `lang`、游標「載入更多」與游標失效重讀、點結果跳到影音詳情的那一句（`?t=`） | 完成 | `src/pages/search` |
| 媒體庫搜尋欄下的「在字幕內容中搜尋」入口 | 完成 | `src/pages/LibraryPage.tsx` |
| 設定「詞彙表」分區的術語審核（GLOS-R7）：狀態與頻道篩選、依頻道分組、來源影音與時間、接受／改寫後接受（預填原詞）／拒絕、加到這個頻道或全域、多選後一次接受或拒絕（上限 100）、422 的 `decisions.<n>` 對回那一列、409 `glossary_full` 與 `term_candidate_decided` 分開處理、游標分頁 | 完成（lazy） | `src/pages/settings/TermReview.tsx` |
| 影音詳情的「匯出 bundle」（`<a download>`） | 完成 | `src/pages/item/ItemPage.tsx` |
| 設定「系統」分區的「匯入 bundle」：multipart 上傳、「影音已存在時加成新版本」（`on_existing=add_versions`）、上限提示（不在前端擋）、422 的訊息與 `entry`／逐欄錯誤、413、409 已存在時提示勾選、成功時顯示匯入與略過數並連到影音 | 完成（lazy） | `src/pages/settings/BundleImport.tsx` |
| 設定「系統」分區的系統告警（種類、訊息、對象、開始時間與已持續多久）與每日備份（最近一次成功、下一次執行或已停用、最近一次失敗的原因） | 完成（lazy） | `src/pages/settings/OpsStatus.tsx` |
| 影音詳情的摘要（譯名、摘要、重點，純文字、帶 `lang`）與摘要後暫停的「繼續處理」「取消處理」；步驟分頁的 `繼續處理`、`取消`、`重新產生摘要`、`改用更好的字幕重跑`、`重新查詢 SponsorBlock`、`從章節重跑` 對應到動作 | 完成 | `src/pages/item/{SummaryBlock.tsx,actions.ts}` |
| 媒體庫「等你決定」（`/library?blocked=awaiting_user`，彙整的連結）與「繼續處理已載入的 N 部」（確認後每 100 部一批） | 完成（近似篩選，見 10.15 第 4 點） | `src/pages/library/{filters.ts,ContinueBar.tsx}` |
| 設定「通知」分區的彙整：列表、新增與編輯（PATCH 只送改過的欄位）、只能選勾了「彙整」事件的端點（1～5 個）、停用與刪除先確認、預覽 | 完成（lazy） | `src/pages/settings/DigestsPanel.tsx` |
| 影音詳情的「有更好的字幕來源」提示（`estimate_usd` 為 null 時寫「無法預估費用」）與「改用更好的字幕重跑」（從字幕步驟重新處理）；排定的重新 Probe 時間 | 完成 | `src/pages/item/BetterSource.tsx` |
| 通知分區移除「彙整」「系統告警」「有更好的字幕來源」的「尚未會發出」標示 | 完成 | `src/pages/settings/NotificationsSection.tsx` |
| AUTH 的 token 輪替、工作階段列表、OIDC 登入按鈕 | AUTH 切片已完成，沒有重做 | `src/pages/settings/{AgentSection,LoginSection}.tsx`、`src/pages/LoginPage.tsx` |
| 總覽（LIB-09）、媒體庫的批次操作（LIB-08）、Hub UI | 未做：`/api/overview`、`/api/items/batch` 仍是 501 stub，不放入口 | — |
| 匯出時選擇產出種類 | 未做：`GET /api/items/{id}/bundle` 沒有參數（見 10.15 第 6 點） | — |
| 真實瀏覽器的視覺、鍵盤與螢幕閱讀器驗收（`@ui @manual`：SRCH、GLOS、BND、OPS、DIG、SRC-10） | owner 手動驗收 | — |

驗證：vitest 42 個檔案 414 個測試（新頁面與新區塊都有 axe 測試，`a11yRoutes` 加了 `/search` 兩條路徑）、i18n 三語各 1537 個 key、初始 JS 271.2KB gzip（UI-4 之後約 261.6KB；增加的主要是三語字串，設定的四個新區塊與搜尋頁都在 lazy chunk）。**沒有改後端**（OpenAPI 與 `schema.d.ts` 不變）。

### 10.15 UI-5 的決定與待決問題

（2026-11-24，依 /goal 採 advisor 建議，待 owner 追認）

1. **搜尋是獨立的 lazy route `/search`**，入口在媒體庫的搜尋欄下方（「在字幕內容中搜尋「…」」，帶入目前的標題搜尋文字）；不加導覽項目，也不做指令面板（§3.1 仍是之後的切片）。SRCH 的 `@ui @manual` 場景寫「在搜尋欄選擇『字幕內容』」，這裡是搜尋欄旁的連結，不是同一個欄位的切換。查詢與語言存在 URL。
2. **搜尋結果依影音分組**（SRCH 待決問題 S5 允許前端分組）：只把相鄰的同一部影音放在一起，順序照 API（SRCH-R7）。單頁 50 句；OpenAPI 把 `limit` 定成字串，照字串送。
3. **跳轉時間**：影音詳情的 `?t=` 只收整數秒（會 floor），結果的 `start` 是毫秒，所以送 `ceil(start)`，超過這一句的 `end` 時退回 `floor(start)`，避免定位到前一句。
4. **「等你決定」是近似篩選**：`GET /api/items` 沒有依 `block_reason` 篩選，`/library?blocked=awaiting_user` 換成 `status=summary:done&status=summary:skipped&status=segment:blocked`（DIG-R6 的下一步是 `segment`）。會混入因預算等其他原因停在分段的影音，以及漏掉「這次執行不含 segment」時停在更後面步驟的影音；橫幅寫明，批次繼續時伺服器會以 `not_awaiting_user` 略過。**待決**：建議 API 加 `blocked=<reason>` 篩選（或在列表回傳 `awaiting_user`）。
5. **批次繼續**只送「已載入」的影音（不是篩選結果的全部），先確認（之後的步驟會計入預算），每 100 部一批，toast 顯示繼續與略過的數量。沒有做逐列選取（那屬於 LIB-08 的批次操作）；DIG 的 `@ui @manual` 場景「Continuing several items from the library」若要求逐列勾選，等 LIB-08 一起做。
6. **bundle 匯出沒有選產出種類**：API 沒有參數，匯出現行版本的全部產出（BND-R1）。**待決**：pages.md §4 的匯出選單與 brief 提到「選產出種類」，需要的話 API 要加 `kinds` 參數。
7. **bundle 匯入的大小上限**只在設定檔（`bundles.import.max_upload_bytes`），API 不提供，前端只提示「預設 64 MiB」、不在前端擋，以 413 的訊息為準。409 `bundle_item_exists` 的訊息提到 `on_existing=add_versions`（給 CLI／API 的說法），UI 改寫成「勾選『影音已存在時，把產出加成新版本』」並提供一鍵勾選。**待決**：409 沒有 `item_id`，不能直接連到已存在的影音；BND 的 `@ui @manual` 場景要求影音詳情顯示「從 bundle 匯入」與來源實例，`GET /api/items/{id}` 沒有 `imported_from`，目前沒有顯示。
8. **術語審核放在「詞彙表」分區的最上面**（GLOS G1），既有的單一頻道清單（UI-3b）保留不動。單筆與批次都走 `POST /api/term-candidates/decide`（同一條錯誤處理路徑）；每一列的「加到哪裡」預設是這個頻道，批次接受時各列用自己的選擇。**待決（API 與規格不一致）**：GLOS-R5 說來源影音永久刪除時 `item` 是 `null`，但 OpenAPI 的 `TermCandidateReview.item` 不是 nullable；前端照 null 處理（顯示「來源影音已刪除」）。
9. **彙整放在「通知」分區**（DIG 介面）：時區預設為瀏覽器的時區；端點只列出勾了 `digest` 事件的 notifier（編輯時已選但取消勾選事件的端點也列出，方便移除）；沒有這樣的端點時「新增彙整」停用並說明。預覽把伺服器組好的文字原樣放在 `<pre>`。
10. **系統告警只顯示目前成立的告警**：`GET /api/system` 沒有已解除的告警或歷史，「恢復狀態」以「目前沒有告警」與說明（解除時會送出解除通知）表示。持續時間以讀到資料的時間計算，用 `Intl` 的單位格式（分鐘／小時／天），不另加字串。沒有放「立即備份」（`/api/backup` 是下載備份檔，不在 brief 範圍）。
11. **摘要與「有更好的字幕來源」放在標題資訊與時間帶之間**；摘要是 LLM 輸出，一律當純文字（保留換行），帶 `summary.language`。「改用更好的字幕重跑」打開既有的重新處理對話框並帶 `from: transcript`（handoff 的 SRC-10 說明），金額由對話框照 UI-2 的方式處理；提示中的預估為 null 時寫「無法預估費用」，不顯示 $0.00。沒有 SSE 事件告知 `better_source` 出現，詳情在步驟狀態改變或重新開啟時才會更新。
12. **術語的拒絕（單筆與多選批次）不跳確認框**：和 UI-3b 的單筆拒絕一致，GLOS 的 `@ui @manual` 場景（「選取其餘兩個並一起拒絕」）也沒有確認步驟；拒絕只改候選的狀態，不刪除任何詞彙表內容（被拒絕的詞之後仍可能再被提出）。代價是一次最多 100 項、UI 上不能撤回；需要的話之後加 toast 的「復原」（API 要能把候選改回 pending）。
13. **匯出 bundle 用 `<a download>`**：沿用 UI-4 決定 10（CSV 匯出）的做法；出錯時瀏覽器會下載到 JSON 錯誤。影音詳情只會顯示不在垃圾桶中的影音，所以 404 `in_trash` 不會從這裡發生；bundle 只含字幕、筆記與選中的畫面，大小通常在幾十 MiB 以內（BND N4），不另做進度顯示。
14. **跳轉後的字幕定位**：影音詳情的字幕列表在掛載時就跟隨 `?t=` 的目前句捲動（UI-2 的行為），測試確認 `?t=2` 時目前句是第 2 句。
15. **共用檔案只增不改**：`router.tsx`（`/search`）、`queryKeys.ts`（`search`、`termCandidatesAll`、`termReview`、`digests`）、locale JSON（新增 key；移除不再使用的 `settings.notify.notYetEmitted`）、`SettingsPage.tsx` 沒有改；`GlossarySection`、`SystemSection`、`NotificationsSection` 各只加一個 lazy 掛載點。步驟動作的對照表（`actions.ts`）加了六個後端字串，`ActionKind` 的 `rerun` 多了選填的 `from`，另加 `continue`、`cancel`。新的樣式在 `src/styles/ui5.css`（由 lazy 模組載入），媒體庫的兩個小樣式加在 `library.css`。

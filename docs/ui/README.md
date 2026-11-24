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
| 自動字幕還沒生成 | YouTube 還沒產生自動字幕，預計 2 小時後再檢查。 | 現在改用語音轉錄（`POST /api/items/:id/use-asr-now`，SRC-R6：略過等待、立刻改用下一個來源；transcript 不是 `waiting` 或任務正在執行時回 409 `not_waiting`） |
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

- **修訂（2026-11-24，M5-S9／UI-6，依 /goal 採 advisor 建議，待 owner 追認）**：Hub UI 的頁面清單與路由見 [pages.md §14](pages.md#14-hub-uim5)，實作與決定見 10.16、10.17。配色是淺色「文庫」（米白紙底、深綠墨色、黃銅色記號）與深色「閱覽室」，token 名稱與 Client 相同（`web/hub/src/styles/tokens.css`）；版面是頂端列，不是 Client 的左側導覽。

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
| 指令面板（`Ctrl K`） | 未做 | 之後的切片 |
| `G` 開頭的快捷鍵、`?` 快捷鍵說明、影音詳情的 `1`–`4` | 完成（UI-7，見 10.18） | `src/shell/{AppShell,ShortcutsDialog}.tsx`、`src/pages/item/ItemPage.tsx` |
| Prompt 庫、處理方案、字幕樣式 | 完成（UI-3a，見 10.8） | `web/client/src/pages/{prompts,profiles,styles}` |
| 設定各分區、詞彙表與術語候選、設定匯出與匯入 | 完成（UI-3b，見 10.10；後端沒有的部分見 10.10 的「未做」） | `web/client/src/pages/settings` |
| 影音詳情、佇列 | 完成（UI-2，見 10.6） | `web/client/src/pages/item`、`src/pages/queue` |
| 登入頁背景的淡化時間帶（§13） | 未做 | 等 UI-1 的時間帶元件 |
| 768–1279px 的收合導覽（只剩圖示） | 未做：沒有圖示前維持展開；UI-1 沒有引入 Phosphor Icons | 之後的切片 |
| 訂閱、成本、筆記模板、設定的通知與預算、影音詳情的畫面與筆記 | 完成（UI-4，見 10.12） | `src/pages/{subscriptions,costs,noteTemplates}`、`src/pages/settings`、`src/pages/item/NotePanels.tsx` |
| 字幕全文搜尋、術語審核、bundle 匯出與匯入、系統告警與備份、摘要與彙整、更好的字幕來源 | 完成（UI-5，見 10.14） | `src/pages/search`、`src/pages/settings`、`src/pages/item`、`src/pages/library` |
| 總覽、批次操作、垃圾桶 | 完成（UI-7，見 10.18） | `src/pages/{overview,trash}`、`src/pages/library` |
| Hub UI（`web/hub`）、Client 的「分享到 Hub」與設定「Hub」分區 | 完成（UI-6／M5-S9，見 10.16） | `web/hub`、`internal/hub/ui.go`、`web/client/src/pages/hub` |

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
| 依頻道／發布日／狀態分組、「格」檢視、儲存檢視 | 分組完成（收尾 D1，見 10.22）；「格」檢視完成（UI-8）；儲存檢視未做 | 儲存檢視不在 M4 範圍 |
| 批次操作、垃圾桶檢視 | 完成（UI-7，見 10.18） | `src/pages/library/BulkBar.tsx`、`src/pages/trash` |
| 預估花費（`POST /api/estimate`） | 完成（10.24 第 3 點） | `src/add/AddDrawer.tsx` |
| 「調整這次的設定」、一次貼上多個網址、把檔案拖進視窗 | 未做 | 見 10.5 第 6 點 |

### 10.5 UI-1 的決定

（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）

1. **不改後端**。列表 API 沒有步驟狀態、頻道名稱、縮圖網址，UI 用現有 API 組合：每一列另外讀 `GET /api/items/{id}`（只有渲染出來的列，虛擬列表大約 20 列）；頻道顯示 ID；縮圖只有 YouTube（由影片 ID 推得 `i.ytimg.com`），其他平台用占位。代價是捲動時的 N+1 請求（本機 SQLite，可接受）。之後在 `GET /api/items` 加 `stages` 摘要、頻道名稱與縮圖網址時，列元件改用列表的資料即可。
2. **即時更新不重抓列表**：UI-0 的失效表中 `stage.status` 與重新連上會讓 `items.lists` 失效，重抓時新項目會插到頂端（違反 §3.4、LIB-R9）。改成只讓 detail 失效；重新連上時顯示「列表可能不是最新的」由使用者重新載入。刪除與還原仍讓列表失效（通常是使用者自己的操作）。
3. ~~**預設不分組**（LIB-R4 的「預設依頻道分組」沒做）：列表依建立時間的 keyset cursor 分頁，前端分組會讓同一個頻道散在不同頁；而且 API 沒有頻道名稱。等 API 有 `group_by`（LIB 規格待決問題已記錄）再做。~~ 收尾 D1 已做：API 加了 `group_by` 與頻道名稱，媒體庫預設依頻道分組（見 10.22）。
4. **篩選的 URL 參數**與 API 同名，除了發布日存 `YYYY-MM-DD`（當地時區的整天）、長度存分鐘（`dur_min`／`dur_max`）。頻道沒有列表 API，改成文字輸入，並可以點列上的頻道「只看這個頻道」。
5. **步驟狀態的對應**：`blocked` 依 `block_reason` 對應到需要你處理／超出預算／等你決定，認不得的當成需要你處理。狀態欄顯示最需要注意的步驟（失敗 > 停住 > 等待 Agent > 處理中 > 稍後檢查，否則是最後完成的步驟）。失敗的列提供「從該步驟重新處理」（`POST /api/items/{id}/rerun`），停住（非預算）的列提供「開啟帳號與 Cookie 設定」。步驟的 `message` 是後端的繁中文字，原樣顯示（沒有 code 可以查字典）。
6. **抽屜的範圍**：一次一個網址或一個檔案（上傳 API 一次只收一個檔案）；播放清單整份新增（API 沒有範圍參數，見 LIB／ING 待決問題）；Bilibili 沒有指定分P 時可以選「全部」或輸入編號（resolve 不回分P 數，不為了列出分P 呼叫會跑 yt-dlp 的 estimate）；頻道只提示訂閱（M4）。預估花費與單次覆寫留給之後的切片。（10.24 已補預估花費與頻道的「建立訂閱」；單次覆寫仍未做。）
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
| 佇列的單一任務重試／取消、「失敗與停住」清單與「全部重試」 | 完成（收尾 D1，見 10.22） | `src/pages/queue/FailedTasks.tsx`、`QueuePage.tsx` |
| 佇列的 Agent 任務（由哪個 Agent 處理、租約剩餘時間的倒數、改用 API 處理） | 完成（見 10.23） | `src/pages/queue/QueuePage.tsx` |
| 佇列的優先順序 | 未做 | API 缺口（10.7 第 8 點） |
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
   - `Stage.actions` 應改成動作代碼（現在是繁中按鈕文字，前端只能比對字串）。部分處理（收尾 D2，2026-11-24，依 /goal 採 advisor 建議，待 owner 追認）：`Stage` 多了 `action_codes`（只列出有穩定代碼的動作，目前只有 `use_asr_now`），`mapActions` 先依代碼對應、同一個動作的文字不重複；其他動作仍比對文字，沒有大改。
   - 沒有 log 片段（OBS-2 的「相關 log 片段」）：步驟面板改連到「紀錄」分頁，顯示 `GET /runs` 的狀態、時間與設定快照；`runs` 也沒有花費、provider 實際回應等欄位。
   - rerun 沒有 `estimate`／`estimate_usd`：對話框顯示「無法預估」。沒有拿 `POST /api/estimate` 代替（它針對網址，可能呼叫 yt-dlp）。（有更好的字幕來源時改顯示 `better_source.estimate_usd`，見 10.24 第 2 點。）
   - 沒有 provider 列表 API：provider 用文字輸入，建議值取自處理方案設定。
   - 沒有列出影音有哪些字幕變體與語言的 API：模式依步驟狀態推得，`edited` 用一次 `variant=edited` 的查詢確認。
   - 沒有章節、說話密度、LLM chunk 的時間範圍：時間帶只畫整條步驟狀態，沒有章節線、拖曳選取、chunk 進度。
   - `ItemDetail` 沒有處理方案名稱：從設定快照的 `profile` 取（沒有就不顯示）。
   - 佇列：~~`GET /api/queue` 只列內建 worker 的任務~~：AGT A13 補上 Agent 任務與租約到期時間，佇列頁已顯示（見 10.23）；仍沒有優先順序欄位與調整優先順序的 API，頁面上明說。~~`POST /api/tasks/{id}/retry`、`cancel` 回 501~~：收尾 D1 已實作，佇列頁接上單一任務的取消與重試（見 10.22）。
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
| 輸出：本機影片旁與資料夾的說明（在處理方案中設定）；vault 與 hook 唯讀說明「尚未啟用（M3 起提供）」（10.24 第 8 點改成唯讀列出設定檔的 vault 與 hook） | 完成 | `OutputsSection.tsx` |
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

1. **唯一的後端改動：`POST /api/auth/password`**。AUTH-R2 要求「修改密碼要透過設定頁」，但沒有路由，`notesteme password set` 當時還沒實作（現在已實作，見 AUTH 待決問題 A28），沒有替代方案。只有 owner（token 即使有 `admin` 也 403）、與登入共用鎖定（AUTH-R6）、目前的密碼錯誤回 422 `invalid_password`（不用 401，避免被當成 session 失效而導到登入頁）、新密碼只要求非空、成功後在同一個交易中撤銷目前以外的所有 session。細節記在 AUTH 規格的待決問題。其他缺口（測試連線、cookie 狀態、工具版本、磁碟用量、匯入 dry-run、頻道列表）都**不補後端**，UI 明確寫「後端還沒有提供」；有排程的寫「M3／M4 起提供」，沒有排程的不捏造里程碑。
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
| 筆記模板編輯（三欄）：模板內文、名稱、「閱讀」按鈕、可用變數、五項自動檢查（全部取消時送 `checks: []`）、另存新版本（422 顯示在內文旁）；內建唯讀只能複製；中欄連到 `note-default`、`note-map-default`；右欄用一部影音的筆記預覽已儲存的版本（`<id>@<版本>`），`409 no_note` 顯示說明（10.24 改成有修改時用草稿預覽、內文改用 CodeMirror） | 完成 | `NoteTemplateEditorPage.tsx` |
| 設定的「通知」分區：端點列表（種類、主機／末四碼、事件、啟用狀態）、新增與編輯（四種 notifier 的欄位、密鑰只寫不讀、七個事件）、傳送測試通知與結果、停用與刪除（確認）、最近的通知（outbox） | 完成 | `src/pages/settings/NotificationsSection.tsx` |
| 設定的「預算」分區：月預算、單部上限、提醒門檻、預算時區；本月花費、預留與剩餘 | 完成 | `src/pages/settings/BudgetSection.tsx` |
| 影音詳情的「畫面」分頁：印樣（只看選中的／全部候選）、時間（點擊跳播）、章節、圖說、排除原因、張數摘要與不足提示 | 完成 | `src/pages/item/NotePanels.tsx` |
| 影音詳情的「筆記」分頁：閱讀字體渲染（標題、段落、清單、callout、表格、程式碼、連結只留 http／https／mailto、wikilink 轉文字）、同一部影音的時間連結點擊跳播、`![[…frame_NNNN.jpg]]` 對應到畫面圖片、自動檢查、寫入 vault 的路徑與 commit、過時提示與「重新產生筆記」、下載 Markdown | 完成 | `NotePanels.tsx`、`src/notes` |
| 總覽（`/`，依賴 LIB-09 `/api/overview`）、媒體庫的批次操作（LIB-08 `POST /api/items/batch`）、搜尋、Hub | 未做：後端還沒有（不放會 501 的入口） | — |
| 訂閱列表的「新內容」欄 | 完成（10.24 第 6 點，SUB-R14） | `src/pages/subscriptions/SubscriptionsPage.tsx` |
| 處理方案被訂閱使用時的提示 | 未做：沒有「哪些訂閱用這個處理方案」的查詢（可以從列表的 `profile` 組合，留給之後） | — |
| 筆記模板的檔名與資料夾版面規則 | 不提供：vault 只在設定檔定義（ADR-0020、VAULT V1），頁面只說明 | — |
| `G` 然後 `S` 的快捷鍵、`1`–`4` 切換詳情分頁 | 未做：快捷鍵仍是之後的切片（10.1） | — |
| 真實瀏覽器的視覺、鍵盤與螢幕閱讀器驗收（`@ui @manual`） | owner 手動驗收 | — |

驗證：vitest 34 個檔案 375 個測試（每個新頁面與分區都有 axe 測試，`a11yRoutes` 加了 10 條路徑）、i18n 三語各 1384 個 key、初始 JS 260.4KB gzip（UI-3b 後約 241.5KB；增加的主要是三語字串與設定頁的兩個分區，頁面本身都在 lazy chunk）。

### 10.13 UI-4 的決定

（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）

1. **不改後端**：沒有新增路由或欄位，OpenAPI 與 `schema.d.ts` 沒有變動。
2. **介面語言**：brief 寫的 zh-TW／ja 與 repo 不符；照 repo 的 `zh-Hant`、`zh-Hans`、`en`。
3. **scope**：沒有查詢目前身分與 scope 的 API（`/api/auth` 只有 login、logout、password，`/api/auth/sessions` 當時尚未實作，AUTH 切片已補上）。Web UI 以 owner 的 session 登入，有全部權限，所以不依 scope 隱藏按鈕；token 才有 scope 限制，萬一收到 403 由 `ErrorCallout` 顯示。之後有 `GET /api/auth/me` 之類的路由時再依 scope 停用按鈕。
4. **金額**：API 回傳的是浮點數美元（micro-USD ÷ 1e6），前端不自己加總，只顯示伺服器算好的總額、預留與預計。頂部一行與預算分區依 COST 規格用 2 位小數；明細的單筆金額常小於 1 美分，沿用 `formatUsd`（小於 $0.01 時最多 4 位，**偏離規格「四捨五入到兩位」**）；單價最多 6 位。幣別符號用 `Intl`（繁中、簡中是 `US$`），與 UI-2 的 `formatUsd` 一致，不硬寫 `$`。`state: unknown` 顯示「未知單價」，不顯示 `$0.00`；`unknown_calls` 在頂部說明並連到計價表。
5. **訂閱的試跑預覽不在開頁時自動執行**：預覽會連外並消耗 rate limit（SUB-R10、handoff 給 UI 的提醒），開啟頁面或抽屜時不送；按過一次「試跑預覽」之後，修改設定才以 800ms debounce 即時更新（pages.md 的「即時更新」），新請求取消舊的，429 不自動重試。新增時網址要先像 http(s) 網址才能預覽。
6. **訂閱詳情是頁面，不是抽屜**：pages.md §5 畫的是抽屜，§2 的路由表有 `/subscriptions/:id`；做成獨立頁面（可加書籤，條目列表也放得下）。
7. **訂閱 PATCH 只送改過的欄位**：`backfill` 一送就會在下一次檢查重新評估（SUB-R3），沒改就不送；`overrides` 是整個取代，表單沒有它，永遠不送；處理方案改回預設送 `profile: ""`。片長在表單用分鐘（API 是秒）。
8. **條目列表沒有 cursor**：`GET /entries` 只有 `limit`，「載入更多」改成每次把 limit 加 200（上限 5000），不另做虛擬化。
9. **成本的長條圖**：用表格的列畫水平長條（`aria-hidden`），金額與次數以文字放在同一列，所以不另外需要文字替代；不加圖表套件。依日期分組時每列是一天（不是直立的長條）。
10. **CSV 匯出**是 `<a download>` 的 GET；錯誤時瀏覽器會下載到 JSON，所以前端先檢查範圍（1～366 天）不合法時不顯示連結。匯出用回應中的 `from`、`to`（伺服器依預算時區決定的預設值），不用瀏覽器的時區計算本月。
11. **計價表放在成本頁**（`#prices`），`prices_stale` 的「調整單價」捲到那裡；pages.md §11 沒有畫計價表，COST-03 與 COST-R13 的動作需要它。覆寫已有的單價時先在對話框內顯示「舊價 → 新價」，再按一次「確認覆寫」。
12. **通知的密鑰**比照 UI-3b 決定 6：只放在元件 state，送出時立即清空（失敗也要重新輸入），不用 `useMutation`；編輯時沒有重新輸入就不送（後端保留原值，NOTIFY-R6），清空選填的非機密欄位送 `null`。ntfy 的 token 目前沒有「移除」的操作（PATCH 的 `null` 可以刪，UI 沒有做）。
13. **還沒有產生端的事件**（`digest`、`alert`、`source.better_available`：DIG、OPS 告警、SRC-10 尚未實作）仍可勾選，旁邊標示「尚未會發出」（已過期：DIG、OPS、SRC-10 實作後這個標示已經移除）。
14. **預算分區** PUT 只送 `monthly_usd`、`per_item_usd`、`warn_at_percent`、`timezone` 四個欄位（GET 另外回傳的 `month`、`spent_usd` 等送回去會 422）；空白送 `null`（不限）。時區用文字輸入，驗證交給後端。
15. **筆記模板**：沒有單筆 GET，編輯頁從列表找；預覽只能用已儲存的版本（API 吃 `<id>@<版本>`），有未儲存的修改時提示（10.24 第 7 點已改成用草稿預覽）。三欄的中欄只連到 Prompt 庫：沒有 API 把模板和 prompt 綁在一起（處理方案分別選）。可用變數與五項檢查寫在前端（`NOTE_CHECKS`、`TEMPLATE_VARS`，與 NOTE-R7、R11 一致；後端改了要同步）。
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
4. **「等你決定」是近似篩選**：`GET /api/items` 沒有依 `block_reason` 篩選，`/library?blocked=awaiting_user` 換成 `status=summary:done&status=summary:skipped&status=segment:blocked`（DIG-R6 的下一步是 `segment`）。會混入因預算等其他原因停在分段的影音，以及漏掉「這次執行不含 segment」時停在更後面步驟的影音；橫幅寫明，批次繼續時伺服器會以 `not_awaiting_user` 略過。**待決**：建議 API 加 `blocked=<reason>` 篩選（或在列表回傳 `awaiting_user`）。（後端補洞切片已在 `GET /api/items` 加上 `blocked=<原因>`，見 LIB 待決問題；UI-8 已改用它，見 10.21 第 1 點。）
5. **批次繼續**只送「已載入」的影音（不是篩選結果的全部），先確認（之後的步驟會計入預算），每 100 部一批，toast 顯示繼續與略過的數量。沒有做逐列選取（那屬於 LIB-08 的批次操作）；DIG 的 `@ui @manual` 場景「Continuing several items from the library」若要求逐列勾選，等 LIB-08 一起做。
6. **bundle 匯出沒有選產出種類**：API 沒有參數，匯出現行版本的全部產出（BND-R1）。**待決**：pages.md §4 的匯出選單與 brief 提到「選產出種類」，需要的話 API 要加 `kinds` 參數。
7. **bundle 匯入的大小上限**只在設定檔（`bundles.import.max_upload_bytes`），API 不提供，前端只提示「預設 64 MiB」、不在前端擋，以 413 的訊息為準。409 `bundle_item_exists` 的訊息提到 `on_existing=add_versions`（給 CLI／API 的說法），UI 改寫成「勾選『影音已存在時，把產出加成新版本』」並提供一鍵勾選。**待決**：409 沒有 `item_id`，不能直接連到已存在的影音；BND 的 `@ui @manual` 場景要求影音詳情顯示「從 bundle 匯入」與來源實例，`GET /api/items/{id}` 沒有 `imported_from`，目前沒有顯示。（後端補洞切片已補：409 `bundle_item_exists` 帶 `item_id`、詳情有 `imported_from`，見 BND 待決問題 N23、N24；UI-8 已加上連結與顯示，見 10.21 第 2、3 點。）
8. **術語審核放在「詞彙表」分區的最上面**（GLOS G1），既有的單一頻道清單（UI-3b）保留不動。單筆與批次都走 `POST /api/term-candidates/decide`（同一條錯誤處理路徑）；每一列的「加到哪裡」預設是這個頻道，批次接受時各列用自己的選擇。**待決（API 與規格不一致）**：GLOS-R5 說來源影音永久刪除時 `item` 是 `null`，但 OpenAPI 的 `TermCandidateReview.item` 不是 nullable；前端照 null 處理（顯示「來源影音已刪除」）。（後端補洞切片已把 schema 改成 nullable。）
9. **彙整放在「通知」分區**（DIG 介面）：時區預設為瀏覽器的時區；端點只列出勾了 `digest` 事件的 notifier（編輯時已選但取消勾選事件的端點也列出，方便移除）；沒有這樣的端點時「新增彙整」停用並說明。預覽把伺服器組好的文字原樣放在 `<pre>`。
10. **系統告警只顯示目前成立的告警**：`GET /api/system` 沒有已解除的告警或歷史，「恢復狀態」以「目前沒有告警」與說明（解除時會送出解除通知）表示。持續時間以讀到資料的時間計算，用 `Intl` 的單位格式（分鐘／小時／天），不另加字串。沒有放「立即備份」（`/api/backup` 是下載備份檔，不在 brief 範圍）。
11. **摘要與「有更好的字幕來源」放在標題資訊與時間帶之間**；摘要是 LLM 輸出，一律當純文字（保留換行），帶 `summary.language`。「改用更好的字幕重跑」打開既有的重新處理對話框並帶 `from: transcript`（handoff 的 SRC-10 說明），金額由對話框照 UI-2 的方式處理（10.24 第 2 點已改成顯示 `better_source.estimate_usd`）；提示中的預估為 null 時寫「無法預估費用」，不顯示 $0.00。沒有 SSE 事件告知 `better_source` 出現，詳情在步驟狀態改變或重新開啟時才會更新。
12. **術語的拒絕（單筆與多選批次）不跳確認框**：和 UI-3b 的單筆拒絕一致，GLOS 的 `@ui @manual` 場景（「選取其餘兩個並一起拒絕」）也沒有確認步驟；拒絕只改候選的狀態，不刪除任何詞彙表內容（被拒絕的詞之後仍可能再被提出）。代價是一次最多 100 項、UI 上不能撤回；需要的話之後加 toast 的「復原」（API 要能把候選改回 pending）。
13. **匯出 bundle 用 `<a download>`**：沿用 UI-4 決定 10（CSV 匯出）的做法；出錯時瀏覽器會下載到 JSON 錯誤。影音詳情只會顯示不在垃圾桶中的影音，所以 404 `in_trash` 不會從這裡發生；bundle 只含字幕、筆記與選中的畫面，大小通常在幾十 MiB 以內（BND N4），不另做進度顯示。
14. **跳轉後的字幕定位**：影音詳情的字幕列表在掛載時就跟隨 `?t=` 的目前句捲動（UI-2 的行為），測試確認 `?t=2` 時目前句是第 2 句。
15. **共用檔案只增不改**：`router.tsx`（`/search`）、`queryKeys.ts`（`search`、`termCandidatesAll`、`termReview`、`digests`）、locale JSON（新增 key；移除不再使用的 `settings.notify.notYetEmitted`）、`SettingsPage.tsx` 沒有改；`GlossarySection`、`SystemSection`、`NotificationsSection` 各只加一個 lazy 掛載點。步驟動作的對照表（`actions.ts`）加了六個後端字串，`ActionKind` 的 `rerun` 多了選填的 `from`，另加 `continue`、`cancel`。新的樣式在 `src/styles/ui5.css`（由 lazy 模組載入），媒體庫的兩個小樣式加在 `library.css`。

### 10.16 UI-6：Hub UI 與 Client 的分享頁面（M5-S9，2026-11-24）

| 範圍 | 狀態 | 位置 |
|---|---|---|
| Hub UI 的獨立 Vite 專案（React 19、TanStack Router／Query、i18next 三語、vitest＋axe、token 對比測試、i18n 字典檢查、bundle 預算、OpenAPI 型別），`go:embed` 由 `notesteme-hub` 提供；CSP、`no-cache`、SPA fallback；`/api`、`/auth`、`/mcp`、`/healthz`、`/readyz` 交給 API | 完成 | `web/hub`、`web/hub/embed.go`、`internal/hub/ui.go`、`internal/webui`（`NewWithOptions`） |
| 登入頁（依序列出已啟用的登入方式，HACC-R1）、登入前的頁面在登入後回來 | 完成 | `web/hub/src/pages/LoginPage.tsx` |
| 首頁的瀏覽與篩選（平台、頻道、影片、語言、種類、變體、三個模型、排除未記錄的模型、排序；在 URL；cursor 分頁；422 的欄位錯誤）、結果的分數、授權、上傳者與「模型未經記錄」標示（HREP-R16） | 完成 | `src/pages/browse` |
| 影片頁（依種類、變體、語言分組，HREP-R17）、兩個字幕版本並排比較（`web/shared` 的 StatusBand 與 DiffText） | 完成 | `src/pages/video` |
| 版本頁：來源紀錄、衍生關係（「以 @alice 的原始字幕重新生成」）、下載、內容預覽（字幕、筆記用 `web/shared` 的 NoteMarkdown、章節）、投票、檢舉、下架申請的連結、版主的隱藏、「在你的 Client 重新生成」的版本 ID（HREP-R6、R13～R15、R17、R18） | 完成 | `src/pages/version` |
| 下架申請表單與狀態查詢（不需要登入，HACC-R15） | 完成 | `src/pages/takedown` |
| 帳號頁：登入方式的連結與移除（`reauth_required`、`last_identity`）、Hub token 的建立（只顯示一次）與撤銷、我的分享（移除原因、撤回） | 完成 | `src/pages/account` |
| 管理頁：檢舉（駁回、移除、狀態篩選）、下架申請（移除、駁回、聯絡 email）、使用者（指派與移除群組、停用與恢復）、稽核紀錄 | 完成 | `src/pages/admin` |
| Client：影音詳情的「分享到 Hub」對話框（產出、授權、可見度、畫面、聲明、預覽會送出的欄位、成功後連到 Hub 的版本頁、`hub_not_configured` 的「設定 Hub」）、已分享清單、`hub` 字幕來源的標示 | 完成（lazy） | `web/client/src/pages/hub/{HubShareDialog,HubShares}.tsx`、`src/pages/item/ItemPage.tsx` |
| Client：設定「Hub」分區（URL 唯讀、token 的設定與移除、測試連線、ASR 前查詢的開關與說明確認、用 Hub 的版本重新生成） | 完成（lazy） | `web/client/src/pages/hub/HubSection.tsx`、`src/pages/SettingsPage.tsx` |
| CI：`web.yml` 的 `hub` job、goreleaser 先建 `web/hub`、release-check 確認 `notesteme-hub` 內嵌真正的 UI、Dockerfile 的 `web-hub` stage | 完成（只改設定檔，沒有在 CI 上跑過） | `.github/workflows`、`.goreleaser.yaml`、`deploy/docker/Dockerfile` |
| 真實瀏覽器的視覺、鍵盤與螢幕閱讀器驗收（`@ui @manual`：HACC、HREP、HSHR） | owner 手動驗收 | — |

驗證：Hub UI vitest 6 個檔案 60 個測試（每頁 axe，`src/test/routes.a11y.test.tsx` 13 條路徑）、i18n 三語各 335 個 key、初始 JS 163.0KB gzip；Client vitest 43 個檔案 424 個測試、i18n 三語各 1591 個 key、初始 JS 274.0KB gzip（UI-5 之後 271.2KB，增加的是三語字串；分享對話框與 Hub 分區都在 lazy chunk）。後端只改了 Hub 的靜態檔案服務（`internal/hub/ui.go`、`internal/webui`），API 與兩份 OpenAPI 快照不變。

### 10.17 UI-6 的決定與待決問題

（2026-11-24，依 /goal 採 advisor 建議，待 owner 追認）

1. **Hub UI 是獨立的 Vite 專案 `web/hub`**，不是 Client 的第二個 entry，也沒有改成 npm workspace。理由：Hub 是獨立部署的 binary（ADR-0030），UI 的 CSP、bundle 預算、相依套件的升級節奏與 Client 無關；workspace 要改 Client 的 lockfile、CI 與 Dockerfile 的建置順序，也會和平行的切片衝突。代價：相依套件安裝兩份、幾個基礎元件（Button、Dialog、TextField、SelectField、ErrorCallout、EmptyState、Spinner、Toast）與 API client 的寫法是從 Client 複製的。之後若要收斂，把這些移到 `web/shared`（需要 i18n 由呼叫端注入）。
2. **只把筆記的 Markdown 解析與渲染搬到 `web/shared/src/notes`**（Hub 的筆記預覽要用同一份「不允許原始 HTML」的實作，HREP-R18）；Client 的 `src/notes/{markdown.ts,NoteMarkdown.tsx}` 改成轉接檔，以 `@shared/notes/…` 的檔案路徑匯入（經過 `@shared` 的 index 會被打包進 Client 的初始 chunk，多 2KB）。Client 與 Hub 的 `tsconfig.json` 都加了 `@shared/*`。
3. **Hub 的靜態檔案服務**：`internal/hub/ui.go` 在 API 之前分派（`/api`、`/auth`、`/mcp` 與其下、`/healthz`、`/readyz` 交給 API，其他交給 UI；同 Client 不在 mux 上註冊 `/` catch-all）。`internal/webui` 加了 `NewWithOptions`（CSP 與 placeholder 可以覆寫，`New` 的行為不變）。Hub 的 CSP 比 Client 嚴：`img-src 'self'`、`media-src 'none'`、`frame-src 'none'`（Hub 不嵌入播放器、不載入外部縮圖，HREP-R12）。驗收 harness 透過 `Server.Handler()` 用到同一個分派，`@M5` 場景不受影響。
4. **登入狀態**：以 `GET /api/v1/me` 為準（401 是未登入，不導頁），所以 `site.visibility: public` 時未登入也能瀏覽；其他 API 回 401 `unauthorized` 時導到 `/login?redirect=`。OAuth callback 一律轉到 `/`（HACC A11，不做 open redirect），登入前的頁面存在 sessionStorage，登入回到首頁後再導過去（只接受站內路徑）。
5. **待決（HACC A11）**：OAuth callback 的錯誤（`oauth_state_invalid`、`oauth_denied`、`oauth_provider_error`、`account_suspended`、連結時的 `identity_in_use`）仍回 JSON，瀏覽器會直接看到 JSON。建議 Hub 對瀏覽器的 callback 改成轉到 `/login?error=<code>`（UI 再顯示訊息），但這會改變 HACC-R3 等場景檢查的狀態碼，需要 owner 決定；這個切片沒有改。
6. **首頁的「搜尋」只有 metadata 篩選**（HREP 不做全文搜尋）：篩選存在 URL，按「套用篩選」才查詢；伺服器的 422 `errors[].path`（`query.<參數>`）顯示在對應欄位旁。
7. **並排比較用 `GET /versions/:id/download?format=cues-json`**：`/content` 要 `regenerate`，一般成員（`members`）沒有；`download` 的回應雖然有 `Content-Disposition: attachment`，`fetch` 仍可讀取。只比較字幕；勾選兩個之後才寫進 URL（`?compare=a,b`，可以分享）。時間帶是 `StatusBand` 的完整版（兩條帶子是兩個版本有字幕的時間範圍），點一句或移動播放頭時兩邊一起跳，下方用 `DiffText` 顯示這個時間點兩句的差異。沒有 `download` 權限時不提供比較與預覽，只顯示說明。
8. **待決（API）**：版本沒有「我投的票」，按鈕的按下狀態只反映這個頁面上剛投的票，重新整理後不保留；建議 `GET /api/v1/versions/:id` 加 `my_vote`。
9. **隱藏沒有對應的「取消隱藏」**（HREP R10 的待決）：版本頁的隱藏確認框寫明「要恢復時需等新的檢舉被駁回」，不另加入口。
10. **在 Client 重新生成**：版本頁對 `source`／`normalized` 的字幕顯示版本 ID 與「複製」，說明貼到 Client 的「設定 → Hub」。Client 的 Hub 分區接受版本 ID 或 Hub 版本頁的網址（只取 `/versions/<id>`）。
11. **使用者管理的群組名稱是輸入框**（附 `contributors`、`moderators` 的建議）：API 沒有列出設定檔群組的路由；`admins`（403 `admin_by_config`）與 `members` 不提供移除按鈕，自己的帳號不提供停用。**待決**：需要的話 API 加「群組清單」。
12. **錯誤訊息的語言**：Hub 的錯誤訊息與欄位錯誤目前只有繁中；UI 對常見的 code（`errors.<code>`，例如 `own_version`、`already_reported`、`last_identity`）用自己的三語字典，其他顯示伺服器的訊息。**待決**：Hub 是否依 `Accept-Language` 產生訊息（同 Client 的 NFR-I18N-2）。
13. **Client 的「分享到 Hub」**：可選的產出由步驟狀態推測（API 沒有「這部影音有哪些產出」的清單），預設是「全部」（不送 `artifacts`，伺服器決定，HSHR-R5）；改成「只分享勾選的」時才送清單，不存在的產出由伺服器回 422 `artifact_not_found`。授權沒有預設值（HSHR-R3 要求明確選擇）。「預覽會送出的欄位」列出影音的欄位，產出的內容與來源紀錄只用文字說明。**待決**：要逐欄預覽來源紀錄，需要「試做 bundle」的 API。本機檔案不顯示按鈕。
14. ~~**待決（API）**：`GET /api/items/:id` 沒有 `hub_origin`（上傳者），只顯示「來自 Hub 的原始字幕」~~：HSHR S23 加了 `hub_origin`，影音詳情已顯示「來自 Hub：@alice 的原始字幕」與 Hub 版本頁的連結（見 10.23）。
15. **已分享清單**連到 Hub 的「帳號」頁（Hub 沒有單一分享的頁面）；撤回只在 Hub 進行（HSHR S12）。`hub_url` 不是 https 時不產生連結。
16. **設定「Hub」分區**加在「系統」之後（`SETTINGS_SECTIONS` 只加一項），lazy 載入。ASR 前查詢的說明一律顯示，開啟前要勾「我了解」，送出 `acknowledge: <disclosure_version>`。
17. **commit 的切法**：Hub UI 的頁面共用 router、字典與樣式，拆成每頁一個 commit 時中間的 commit 無法單獨建置，所以 Hub UI 是一個 commit；Go 端、`web/shared` 的搬移、CI 與 Client 各自一個。


### 10.18 UI-7：總覽、媒體庫的批次操作、已讀狀態、垃圾桶、快捷鍵（2026-11-24）

| 範圍 | 狀態 | 位置 |
|---|---|---|
| 總覽 `/`（lazy，取代導到媒體庫）：現在（最多 3 部，步驟與任務進度、精簡版時間帶、「還有 N 部」連到佇列）、需要你處理（沒有項目時整個區塊隱藏，每項有直接處理的按鈕與「查看」）、提醒（`terms_pending` 去審核、`prices_stale` 到成本頁）、今天（依時間由新到舊）、近期完成、本月花費與預算、磁碟用量；空媒體庫的空狀態；SSE 節流重讀與每分鐘重讀；導覽第一項「總覽」，標誌連到 `/` | 完成 | `src/pages/overview` |
| 媒體庫的多選：每列的勾選框（可及名稱含標題，Shift 點擊選一段）、「選取已載入的 N 部」、「選取符合條件的全部 N 部」（最多 500 部）、換篩選時清除選取 | 完成 | `src/pages/LibraryPage.tsx`、`src/pages/library/LibraryRow.tsx` |
| 批次操作列（lazy）：重新處理（選起始步驟）、改用其他處理方案（選方案與起始步驟）、匯出 bundle、刪除先 `dry_run`，確認框顯示數量、預估費用、無法預估的任務數、預算剩餘與 `exceeds` 警告、刪除的合計與「同時刪除未修改過的輸出檔（N 個）」、不能處理的影音與原因；分批執行、進度、停止、逐項結果（成功／失敗／cancelled 與原因）、bundle 的下載連結；標為已讀／未讀直接執行並可復原 | 完成 | `src/pages/library/{BulkBar.tsx,bulk.ts}` |
| 「等你決定」的檢視：有選取時批次操作列多一個「繼續處理」（只送勾選的，確認框顯示數量與預估費用）；沒有選取時保留 UI-5 的「繼續處理已載入的 N 部」 | 完成（列表仍是近似篩選，見 10.19 第 9 點） | `src/pages/library/BulkBar.tsx` |
| 已讀狀態（LIB-R19）：列上的「未讀」標示（圓點加文字）、篩選（URL 的 `read_state`）、影音詳情的「標為已讀／未讀」 | 完成 | `src/pages/library`、`src/pages/item/ReadStateButton.tsx` |
| 垃圾桶 `/trash`（lazy）：刪除時間、幾天後永久刪除、還原（不確認）、永久刪除與清空（先確認）；媒體庫搜尋欄下方的入口 | 完成 | `src/pages/trash` |
| `G` 然後 `L`／`Q`／`S`、`?` 快捷鍵說明、影音詳情的 `1`–`4` | 完成 | `src/shell`、`src/pages/item/ItemPage.tsx` |
| 匯入 409 連到既有影音、影音詳情的「從 bundle 匯入」、術語審核 `item` 的型別、「等你決定」改用 `blocked=` 篩選 | 未做：後端補洞切片在交付時還沒有合併到 master（見 10.19 第 9 點）。（UI-8 已做，見 10.20、10.21） | — |
| 指令面板、影音詳情的 `Space`／`←`／`→`／`[`／`]`、依頻道分組、「格」檢視、儲存檢視 | 未做：之後的切片（儲存檢視不在 M4）。（UI-8 已做指令面板、播放快捷鍵與「格」檢視；依頻道分組已在收尾 D1 完成（10.22），儲存檢視仍未做） | — |
| 真實瀏覽器的視覺、鍵盤與螢幕閱讀器驗收（`@ui @manual`：LIB-08、LIB-09、DIG 的「從媒體庫挑選暫停的影音」） | owner 手動驗收 | — |

驗證：vitest 46 個檔案 444 個測試（總覽、垃圾桶、批次對話框、快捷鍵說明都有 axe；`a11yRoutes` 加 `/`、`/trash`）、i18n 三語各 1695 個 key、初始 JS 281.5KB gzip（UI-6 之後 274.0KB）。**沒有改後端**（OpenAPI 與 `schema.d.ts` 不變）。

### 10.19 UI-7 的決定與待決問題

（2026-11-24，依 /goal 採 advisor 建議，待 owner 追認）

1. **總覽取代 `/` 的導頁**：`/` 是 lazy 的總覽頁，導覽加「總覽」為第一項（工作組）。伺服器登入後的 `next` 預設是 `/`，所以登入後落在總覽；`safeRedirect` 的備用值仍是 `/library`（只在 `next` 不安全時用到）。手機的底部分頁列仍沿用左側導覽的項目（README §8 的「總覽、媒體庫、新增、佇列、更多」版面沒有另外做）。（UI-8 已做，見 10.21 第 6 點。）
2. **總覽的時區**帶瀏覽器的 IANA 時區（L-M8）；伺服器回 422（不認識的時區）時不帶 `tz` 再讀一次，改用預算時區。
3. **總覽的更新**：`stage.status`、`item.*` 事件以 1 秒節流讓總覽失效，重新連上時也是；`today`、`month`、`disk` 有些變化沒有事件（例如預算、磁碟），另外每分鐘重讀。
4. **「需要你處理」的按鈕**：`actions` 是後端的繁中文字。成本的「調整預算」（到設定的預算分區）與「查看成本」在總覽對應；其他沿用影音詳情的對照表（`item/actions.ts`）：重跑類直接 `POST /rerun`（從那個步驟或對照表指定的步驟），「繼續處理」送 `/api/items/continue`，設定類打開對應分區；取消、改用 API、更新 yt-dlp 等需要更多說明的動作改為打開影音詳情的步驟分頁。每項另有「查看」。只列 API 回傳的最多 50 項，超過時提示到媒體庫。
5. **總覽的「今天」**：**待決（API）**：`OverviewEvent` 只有 `{at, type, item, title}`，`stage.status` 沒有步驟與狀態，`items.batch` 沒有動作與數量，所以只能寫「「…」的步驟有新狀態」「執行了一次批次操作」。建議 API 加 `stage`、`status`（與 `items.batch` 的 `action`、`succeeded`、`failed`）。事件的影音只有代號（不是 ID），「今天」的列不連到影音詳情。不認識的類型顯示類型名稱與標題。
6. **空狀態**：各區塊都沒有東西時再讀 `GET /api/items?limit=1`，總數是 0 才顯示媒體庫的空狀態（只有等待中的影音時各區塊也是空的，不能當成空媒體庫）。「現在」沒有影音時顯示「目前沒有正在處理的影音。」而不隱藏（pages.md 只要求「需要你處理」隱藏）。
7. **批次的執行方式**：`dry_run` 一次送全部選取（≤500）；實際執行分批（刪除每批 10 部，其他 50 部），以便顯示進度與「停止（目前這批完成後）」，沒有送出的影音標成 `cancelled`。不在請求中途 abort：中途斷線時伺服器會把剩下的標成 cancelled（L-M15），但瀏覽器拿不到逐項結果。代價是每批各寫一筆 `items.batch` 事件，`dry_run` 與實際執行之間的資料可能已經改變（以實際執行的逐項結果為準）。實際執行只送 `dry_run` 中 `ok: true` 的影音；不能處理的影音在確認框列出原因。
8. **確認方式依動作**：重新處理、改用處理方案、匯出 bundle、刪除、繼續處理都先 `dry_run` 再確認（LIB-R23）；`budget.exceeds` 只顯示警告、不擋確認（LIB-R17：超過預算的步驟是 `budget_blocked`，回應仍是 `ok: true`）；`unknown_estimates > 0` 時寫「另有 N 個任務無法預估費用」。**偏離 LIB-R23 的字面**：標為已讀／未讀可以復原，依 README §3.5 不跳確認框，直接執行並在 toast 提供「復原」（送相反的動作）。匯出 bundle 依 L-M11 在結果中列出每部的下載連結（瀏覽器會擋連續的程式化下載），只接受 `/api/` 開頭的路徑。刪除的結果另提醒可以從垃圾桶還原。
9. **後端補洞切片（`blocked=` 篩選、匯入 409 的 `item_id`、`imported_from`、`TermCandidateReview.item` nullable）在交付時還沒有合併到 master**：「等你決定」維持 UI-5 的近似篩選（10.15 第 4 點）與「繼續處理已載入的 N 部」，另外可以逐列勾選後「繼續處理」（DIG 的 `@ui @manual` 場景）；匯入 409、「從 bundle 匯入」、術語審核維持 UI-5 的行為（10.15 第 7、8 點）。後端合併後，`toApiQuery` 是唯一要改的地方（`blocked` 換成 API 的篩選）。（UI-8 已處理，見 10.20、10.21。）
10. **「繼續處理」的預估**：`/api/items/continue` 沒有 `dry_run`，確認框用 `POST /api/items/batch {action: rerun, from: segment, dry_run: true}` 的 `estimated_usd` 當近似值（DIG-R6 的下一步是 segment），文字標明「以從分段重新處理估算」。送出時送全部勾選的影音，由伺服器略過不是等你決定的（`not_awaiting_user`，結果列出原因）。**待決**：建議 `/api/items/continue` 加 `dry_run`。
11. **選取的範圍**：選取記在當下的篩選條件上，換篩選（含標題搜尋）時清除，避免畫面外、條件外的影音被一起處理；執行完後列表重新載入，關閉結果對話框時才清除選取（結果持續顯示）。「選取符合條件的全部」用同一組條件分頁讀出 ID（每頁 200），是當下的快照；符合的超過 500 部時不提供，改提示上限。載入超過 500 列後全選、或 Shift 範圍選取超過 500 部時，走 `dry_run` 的動作停用並提示上限，標為已讀／未讀（分批送）仍可用。
12. **已讀狀態的顯示**：每部影音預設是未讀（LIB-R19），列上「未讀」以圓點加文字放在標題下的資訊列，不放進標題連結（連結的名稱維持標題）。打開詳情不會自動標為已讀；詳情頁的按鈕用批次 API 送單一影音（沒有單一影音的已讀路由）。
13. **垃圾桶是獨立的 lazy route `/trash`**，入口是媒體庫搜尋欄下方的「垃圾桶」連結，**偏離 pages.md** 的「檢視切換中的『垃圾桶（N）』」：媒體庫沒有檢視切換，而且顯示 N 要在每次打開媒體庫時多讀一次 `GET /api/trash`（沒有分頁，會列出全部）。剩餘天數以 `purge_at` 無條件進位；空狀態寫「預設 30 天」（保留天數在設定檔，API 沒有提供）。
14. **快捷鍵**：`G` 之後 1.5 秒內按 `L`／`Q`／`S`；`?` 打開的說明只列出已經做好的快捷鍵（`Ctrl K`、影音詳情的播放控制還沒做，不列；UI-8 已做並加進說明，見 10.21 第 4、5 點）。影音詳情的 `1`–`4` 依 README §3.6 是字幕、畫面、筆記、紀錄。焦點在輸入框或有對話框時不作用。
15. **bundle 預算**：所有新頁面、批次操作列、快捷鍵說明都是 lazy；主 chunk 只多了選取狀態、勾選框、已讀篩選、快捷鍵的處理與三語字串。總覽 lazy chunk 引用主 chunk 的模組（`useItemDetail`、`stageView`、`@shared` 的時間帶、`AddContext`）使 Rollup 多切出幾個小 chunk，初始 JS 也算進去。沒有調整預算。
16. **共用檔案只增不改**：`router.tsx`（`/` 改成總覽、加 `/trash`）、`queryKeys.ts`（`overviewAll`、`overview`）、locale JSON（只新增 key）、`LibraryPage.tsx`（選取狀態、批次操作列與垃圾桶入口）、`LibraryRow.tsx`（選填的勾選框與未讀標示）、`filters.ts`／`FilterBar.tsx`（`read_state`）、`AppShell.tsx`（導覽項目與快捷鍵）。`router.test.tsx` 的「`/` 導到媒體庫」與 `LoginPage.test.tsx` 的兩個登入後落點依新行為修改。新的樣式在 `src/styles/ui7.css`（由 lazy 模組載入），列的勾選框與未讀標示加在 `library.css`。

### 10.20 UI-8：後端補洞的欄位、指令面板、播放快捷鍵、底部分頁列、「格」檢視（2026-11-24）

| 範圍 | 狀態 | 位置 |
|---|---|---|
| 媒體庫的 `?blocked=<原因>` 直接送 `GET /api/items` 的 `blocked`（格式 `^[a-z_]{1,64}$`，取代 UI-5 的近似條件）；「等你決定」改為逐列勾選（勾選後也可以「選取符合條件的全部」，最多 500 部）再由批次操作列「繼續處理」；其他原因顯示通用的條件標籤 | 完成 | `src/pages/library/{filters.ts,FilterBar.tsx}`、`src/pages/LibraryPage.tsx` |
| bundle 匯入的 409：`bundle_item_exists` 連到既有影音（`item_id`）並保留「一鍵勾選加成新版本」；`bundle_item_in_trash` 提示在垃圾桶並連到 `/trash` | 完成 | `src/pages/settings/BundleImport.tsx` |
| 影音詳情的「從 bundle 匯入」：來源實例、Notesteme 版本、匯入時間（純文字；實例 ID 是空字串時寫「不明」；`null` 時不顯示） | 完成 | `src/pages/item/ItemPage.tsx` |
| AUTH `--insecure-no-auth` 的頂部警告橫幅 | 已確認與後端補洞切片一致（`InsecureBanner` 與測試），沒有重做 | `src/shell/AppShell.tsx` |
| 指令面板（README §3.1）：`Ctrl K`／`⌘ K`（lazy）；影音標題搜尋與「在字幕內容中搜尋」、所有頁面、動作（新增、暫停佇列或繼續處理、更新 yt-dlp）；combobox＋listbox，↑／↓／Enter | 完成 | `src/shell/CommandPalette.tsx` |
| 影音詳情的 `Space`（播放或暫停）、`←`／`→`（前後 5 秒）、`[`／`]`（上一個或下一個章節）；`?` 的說明加上這些與 `Ctrl K` | 完成（Bilibili 不接 `Space` 與方向鍵，見 10.21 第 5 點） | `src/pages/item/{ItemPage.tsx,Player.tsx,playKeys.ts}`、`src/shell/ShortcutsDialog.tsx` |
| 767px 以下的底部分頁列（README §8）：總覽、媒體庫、新增、佇列、更多（對話框：其餘頁面、偏好、登出） | 完成 | `src/shell/{TabBar.tsx,AppShell.tsx}` |
| 媒體庫的「列｜格」切換與「格」檢視（pages.md §3） | 完成（lazy，沒有多選，見 10.21 第 7 點） | `src/pages/library/LibraryGrid.tsx` |
| 媒體庫依頻道分組 | 完成（收尾 D1，見 10.22） | `src/pages/library/groups.ts`、`src/pages/LibraryPage.tsx` |
| 儲存檢視 | 未做：LIB 寫明不在 M4，也沒有 `/api/views` | — |
| 總覽「今天」的事件缺步驟、狀態與影音 ID | 未做（不改後端）：維持 UI-7 的近似呈現，仍是 10.19 第 5 點的待決 | — |
| 真實瀏覽器的視覺、鍵盤與螢幕閱讀器驗收（`@ui @manual`：BND「匯入的筆記不含 raw HTML」與「從 bundle 匯入」、DIG「從媒體庫挑選暫停的影音」） | owner 手動驗收 | — |

驗證：vitest 51 個檔案 466 個測試（指令面板、底部分頁列與「更多」、格檢視、從 bundle 匯入、等你決定的檢視都有 axe）、i18n 三語各 1714 個 key、初始 JS 283.3KB gzip（UI-8 之前 282.1KB；指令面板與格檢視在 lazy chunk，增加的是分頁列、快捷鍵的處理與三語字串）。**沒有改後端**（OpenAPI 與 `schema.d.ts` 不變）。

### 10.21 UI-8 的決定與待決問題

（2026-11-24，依 /goal 採 advisor 建議，待 owner 追認）

1. **`blocked` 篩選**：網址接受任何符合 API 格式的原因（`^[a-z_]{1,64}$`），原樣送 API。只有 `awaiting_user` 有切換按鈕、提示與批次操作列的「繼續處理」；其他原因（例如佇列或外部連結帶進來的 `budget_blocked`）的條件標籤直接顯示原因代碼（UI 沒有原因的翻譯表）。**移除 UI-5 的「繼續處理已載入的 N 部」**：brief 要求改成逐列勾選，現在的流程是勾選 →（需要時在批次操作列「選取符合條件的全部」，沿用 UI-7 的 500 部上限；這個按鈕在批次操作列裡，要先勾選至少一列才會出現，提示文字也這樣寫）→ 批次操作列「繼續處理」→ 確認框顯示數量與預估費用（DIG 的 `@ui @manual` 場景）。預估仍借用 `rerun from=segment` 的 `dry_run`（10.19 第 10 點，`/api/items/continue` 沒有 `dry_run`，仍待決）。
2. **匯入 409**：`bundle_item_exists` 的訊息與「一鍵勾選」不變，下方另放「打開既有的影音」連結（`item_id` 是非空字串才顯示；`ErrorCallout` 的動作只能放按鈕）。`bundle_item_in_trash` 用前端的文案（「先還原或永久刪除，再匯入」）與「前往垃圾桶」，不提供「加成新版本」（伺服器對垃圾桶中的影音不接受）。
3. **「從 bundle 匯入」放在標題下的資訊列**（`<dl>`），文字是「實例 <ID>（Notesteme <版本>），<時間> 匯入」；`bundle_sha256` 不顯示（太長，API 有）。manifest 的字串不可信，一律是 React 的文字節點；測試確認 `<img onerror>` 不會變成元素。
4. **指令面板**：`Ctrl K` 開啟，macOS 只認 `⌘ K`（Mac 上的 `Ctrl K` 是文字輸入框與 CodeMirror 的「刪到行尾」，不攔；`?` 的說明在 Mac 顯示 `⌘ K`）。焦點在輸入框時也能開；已有對話框、或按鍵已被其他元件處理（`defaultPrevented`）時不作用。影音搜尋沿用 `ItemPicker` 的 query key 與查詢（同一個 key 同一種資料，最多顯示 6 部），輸入延遲 200ms；「在字幕內容中搜尋「…」」導到 `/search?q=`，不在面板裡重做結果列表。「暫停佇列／繼續處理」依 `GET /api/queue` 的 `paused` 只出現一個，讀取失敗（例如佇列沒有啟用的 501）時不出現。動作不另外確認（佇列頁與設定頁的同一個按鈕也不確認），結果用 toast。面板的 `<li role="presentation">` 包分組（`role="group"` 加標題），axe 無違規。
5. **播放快捷鍵**：焦點在按鈕、連結、輸入框、分頁、時間帶的 slider、字幕句等有自己用途的元素上時不接手（`playKeys.ts` 的清單），有對話框時也不作用；播放器不能控制時不 `preventDefault`（不擋 `Space` 捲動）。`←`／`→` 只移動位置、不改變播放或暫停。YouTube 要等播放器回應 postMessage 之後才有作用（降級成重新載入時不作用），播放狀態取自 `infoDelivery` 的 `playerState`。**Bilibili 不接 `Space` 與方向鍵**：嵌入播放器沒有控制介面，每次跳轉都要重新載入 iframe，連按會很糟。`[`／`]` 用 `GET /api/items/{id}` 的 `chapters`（`HH:MM:SS.mmm`），離目前章節開頭超過 2 秒時 `[` 先回到開頭；跳章節和點時間帶一樣會開始播放；沒有章節時不作用。
6. **底部分頁列只在 767px 以下渲染**（`matchMedia`），那時側邊導覽不渲染，避免兩組相同的連結同時出現在無障礙樹中（jsdom 沒有 `matchMedia`，測試另外模擬）。「新增」在中間；「更多」是對話框，列出其餘頁面（依工作、調整、系統分組）、偏好（主題、語言）與登出，點頁面後關閉。README §8 的其他手機版面（媒體庫的時間帶移到標題下方、播放器固定在頂部）不在這次範圍。
7. **「格」檢視**：網址的 `view=grid`（可以加書籤），不是篩選條件：不送 API、不影響選取與「清除篩選」（清除時保留）。只用列表已載入的資料（每格是縮圖、標題、長度、發布日，不像列檢視每列再讀 detail，所以沒有時間帶與狀態）、不虛擬化（一頁 100 部加「載入更多」）。**格檢視沒有多選與批次操作**：要選取時切回列檢視。縮圖只有 YouTube（由影片 ID 推得），其他平台是占位（API 沒有縮圖網址）。
8. ~~**依頻道分組沒有做（待決，API）**：pages.md §3 寫「預設依頻道分組，也可以改成依發布日或依狀態分組」，但 `GET /api/items` 只依建立時間分頁、沒有排序參數，前端只能把相鄰的同頻道列放在一起（同一個頻道會散在很多組），而且列表只有 `channel_id`。建議 API 加 `sort=channel|published|status`（或分組的計數）與頻道名稱，再做分組。**儲存檢視**不在 M4（LIB），沒有做。~~ 收尾 D1 已做（見 10.22）。
9. **總覽「今天」**：沒有改後端，維持 UI-7 的近似呈現；10.19 第 5 點的待決（`stage`、`status`、批次的 `action` 與數量、影音 ID）仍有效。
10. **bundle 預算**：指令面板與格檢視是 lazy（樣式在 `src/styles/ui8.css`，由 lazy 模組載入）；主 chunk 只多了分頁列、`Ctrl K` 與播放鍵的處理、三語字串，拿掉 `ContinueBar` 抵掉一些。沒有調整預算，也沒有拆分 locale。
11. **共用檔案只增不改**：`router.tsx`、`queryKeys.ts`、`client.ts` 沒有改；locale JSON 新增 key，並移除不再使用的 `library.awaiting.{continueAll,confirmTitle,confirmBody,done}`、改寫 `library.awaiting.hint`；`LibraryPage.tsx` 加「列｜格」切換、格檢視與等你決定的提示（取代 `ContinueBar`）；`AppShell.tsx` 把導覽分組抽成 `NavGroups`（側邊導覽與「更多」共用）並加 `Ctrl K` 與分頁列；`base.css` 的手機版導覽改成 `.tabbar`，`library.css` 移除不再用到的手機版 `.nav__add` 規則並加 `.library__view`。


### 10.22 收尾 D1：佇列的重試／取消、媒體庫的分組（2026-11-24）

（依 /goal 採 advisor 建議，待 owner 追認；後端的決定見 PIPE 規格與 LIB 規格的待決問題）

| 項目 | 狀態 | 位置 |
| --- | --- | --- |
| 佇列：每個任務的「取消…」（`POST /api/tasks/{id}/cancel`，只停這個任務所屬的步驟）與影音層級的「停止處理…」並存，同一個確認對話框 | 完成 | `src/pages/QueuePage.tsx` |
| 佇列：「失敗與停住」清單（`GET /api/queue` 的 `failed`）：每列重試（`POST /api/tasks/{id}/retry`）、超過一列時「全部重試」 | 完成 | `src/pages/queue/FailedTasks.tsx` |
| 媒體庫：預設依頻道分組，可以切換成依發布日、依狀態或不分組；每組第一列上方顯示組名 | 完成 | `src/pages/library/groups.ts`、`src/pages/LibraryPage.tsx` |

1. **重試與取消的單位是步驟**：一個 LLM 步驟有多個任務（plan、chunk、assemble），取消或重試其中一個就是整個步驟，對話框寫明「其他步驟不動」，佇列頁上方的說明區分「取消」與「停止處理」。409 的 `task_not_retryable`、`task_not_cancellable` 有三語字典（`errors.task_not_*`），其他錯誤照 `describeError`，都用 toast。
2. **「全部重試」依序送出**：同時送出上百個請求沒有好處；一個失敗不影響其他，結束後的 toast 寫成功的數量與最後一個錯誤。按鈕不另外確認（重試不會付兩次費：LLM 的 chunk 快取照常重用）。
3. **被取消的步驟也列在「失敗與停住」**，標籤是「已取消」，重試就會接著處理（後端把之後被取消的步驟一起改回等待）。
4. **佇列頁改成 lazy route**：這次加的對話框與清單只在佇列頁用到。
5. **分組的網址參數**是 `group`（`published`、`status`、`none`；省略時是預設的 `channel`，不合法的值丟掉），不是篩選條件：「清除篩選」時保留，切回預設時從網址移除。送 API 的 `group_by` 跟著它，不分組時省略；換分組會換 query key，從第一頁重讀（游標不能跨分組方式使用）。
6. **組名**：頻道用 API 的 `channel_name`，沒有名稱時用頻道 ID，沒有頻道寫「沒有頻道」；發布日依當地時區的日期，相鄰的同一天合併成一組，沒有發布時間寫「沒有發布日期」；狀態用 API 的 `group`（有步驟失敗、需要你處理、處理中、其他）。組名是列表中的 `<h2>`（在該組第一列的 `listitem` 裡，虛擬列表依實際高度量測），「格」檢視只照分組的順序，不顯示組名。
7. **bundle 預算**：媒體庫改成 lazy route（和佇列頁一樣），分組只多了很小的 `groups.ts` 與字串；初始 JS 由 281.6KB 降到 271.6KB／預算 300KB（gzip）。
8. **已知限制**：依狀態或頻道分組時，翻頁期間組鍵可能改變（LIB 待決問題），已載入的頁不會跟著移動；依 LIB-R9 不自動重抓。

### 10.23 佇列的 Agent 任務、影音詳情的 Hub 來源（2026-11-24）

（2026-11-24，依 /goal 由實作者暫定，待 owner 追認）

| 項目 | 狀態 | 位置 |
| --- | --- | --- |
| 佇列：Agent 任務也列在任務列表（AGT A13）：被領取的顯示「由 Claude Code 處理中」與「租約剩 7:42」（每秒倒數），等待中的顯示「等待 Agent」；Agent 的列有「改用 API 處理」（`POST /api/tasks/{id}/switch-executor`）；通道的使用中／等待仍只算內建 worker | 完成 | `src/pages/queue/QueuePage.tsx` |
| 影音詳情：字幕來源是 Hub 時顯示「來自 Hub：@alice 的原始字幕」與「在 Hub 查看這個版本」（`GET /api/items/{id}` 的 `hub_origin`，HSHR S23） | 完成 | `src/pages/hub/HubOrigin.tsx`、`src/pages/item/ItemPage.tsx` |

1. **Agent 任務在列表中的位置**：沿用既有的排序（被領取的在前，同狀態維持 API 的順序）。API 先列內建 worker 的任務（A13），所以被 Agent 領取的排在被內建 worker 領取的之後、所有等待中的之前。篩選不另外加「執行方式」：資源類別照 `resource_class`（Agent 任務是 `llm`），狀態的「執行中」包含被 Agent 領取的。
2. **狀態的用字**：被領取的是「由〈Agent 名稱〉處理中」（README 用字表的「由 Agent 處理中」，`agent` 是領取者的 token 名稱；沒有名稱時用「由 Agent 處理中」），等待中的用既有的「等待 Agent」。token 名稱是使用者自己取的文字，一律當文字節點顯示。
3. **租約剩餘時間**：照 pages.md §6 的「租約剩 7:42」，用 `formatTimecode`（MM:SS，超過一小時是 H:MM:SS），每秒倒數；整頁共用一個計時器，只在畫面上有 Agent 租約時計時。外層是 `<time datetime>`，滑過時顯示到期的絕對時間。已經過了 `lease_until` 但還沒被放回佇列時顯示「租約已到期，稍後放回佇列」，不顯示 `00:00`。
4. **租約延長的更新**：Agent 呼叫 `extend_lease` 不會發 SSE 事件（`stage.status` 不變），所以有 Agent 租約時每 30 秒重新讀取佇列；沒有 Agent 租約時不輪詢（照舊靠 SSE）。
5. **通道不算 Agent 任務**：`lanes` 的使用中與等待照 API（只算內建 worker，AGT-R2、A13），UI 不自己加總；任務列表上方的說明改成「交給 Agent 的任務也列在這裡……它們不佔上面各資源類別的並行名額」，避免看到 LLM 通道「使用中 0」卻有 Agent 任務時誤會。
6. **「改用 API 處理」只在 Agent 的列**：送 `{executor: "api"}`，不另外確認（影音詳情的同一個動作也不確認）；成功的 toast 沿用「〈步驟〉已改用 API 處理。」，`api_not_configured`、`no_agent_tasks` 等錯誤用既有的字典。「失敗與停住」清單不加這個按鈕：那些任務已經結束，切換只會得到 409 `no_agent_tasks`。Agent 任務照樣有「取消…」與「停止處理…」（取消以步驟為單位，Agent 的任務一起取消、租約失效）。
7. **Hub 來源的顯示條件**：有 `hub_origin` 時一律以它為準；`uploader` 是空字串、或 `hub_origin` 是 `null` 但使用中的來源是 `hub` 時，維持通用的「來自 Hub 的原始字幕」。
8. **上傳者名稱**：Hub 的顯示名稱是不可信的文字（S23 已去掉控制與格式字元、最多 64 字），UI 只用文字節點渲染，超過 32 個字（以 code point 計，不切開 surrogate pair）時截斷加「…」，完整名稱放在 `title`。
9. **版本頁的連結只給 https 的 Hub**：網址是 `hub_origin.url` 加 `/versions/<version_id>`，沿用 `hubLink`（「已分享到 Hub」的同一個規則，10.17 第 15 點），`http:`、`javascript:` 等都不產生連結，比「接受 http／https」更保守；另開分頁並帶 `rel="noopener noreferrer"`。

### 10.24 Client UI 的 `@ui @manual` 缺口（2026-11-24）

一次唯讀對帳找出七個 `@ui @manual` 場景背後的畫面功能還沒有做或只做了一部分，這個切片補齊。下面的決定都是「（2026-11-24，依 /goal 由實作者暫定，待 owner 追認）」。

| 項目 | 狀態 | 位置 |
| --- | --- | --- |
| 影音詳情的「更多」選單（刪除移進去；DEL 的「The delete dialog explains the consequences and can be undone」） | 完成 | `src/components/MoreMenu.tsx`、`src/pages/item/ItemPage.tsx` |
| 「改用更好的字幕重跑」的對話框顯示預估花費（SRC 的「The item page offers the upgrade with its estimated cost」） | 完成 | `src/pages/item/RerunDialog.tsx` |
| 訂閱列表的「新內容」欄（SUB 的「The subscriptions page lists each subscription with its schedule」） | 完成（後端加 `new_items`，SUB-R14） | `internal/subscription/service.go`、`src/pages/subscriptions/SubscriptionsPage.tsx` |
| 筆記模板編輯的即時預覽與未知變數的標示（NOTE 的「Editing a template shows a live preview on a real item」） | 完成（預覽 API 加草稿欄位，NOTE-R12） | `src/pages/noteTemplates/{NoteTemplateEditorPage.tsx,templateVars.ts}`、`src/pages/prompts/PromptCodeEditor.tsx` |
| 總覽的系統告警與最近沒有送出的通知（OPS 的「Active alerts are shown on the overview」） | 完成（沒有改後端） | `src/pages/overview/AlertsSection.tsx` |
| 設定 › 輸出唯讀列出 vault 與 hook；處理方案的輸出目標可以選 vault 與 hook（VAULT 的「The output settings show the vaults and hooks from the config file」） | 完成（後端加 `GET /api/hooks`，VAULT-R10、V18） | `src/pages/settings/OutputsSection.tsx`、`src/pages/profiles/{ProfileEditorPage.tsx,profileModel.ts}`、`src/api/outputCatalog.ts`、`internal/api/vaults.go` |
| 「新增」抽屜的預估花費（ENTRY 的「Clicking the bookmarklet on a video page fills in the add drawer」）與貼上頻道時的「建立訂閱」（SUB 的「Pasting a channel address offers to subscribe」） | 完成 | `src/add/AddDrawer.tsx`、`src/pages/subscriptions/SubscriptionsPage.tsx`、`src/router.tsx` |

1. **「更多」選單是 disclosure，不是 `role="menu"`**：按鈕帶 `aria-expanded`，展開後是一組普通按鈕；Esc 或點外面就收起（不在失焦時收起：Safari 與 macOS 的 Firefox 點按鈕不給焦點，會在點到項目之前就收起）。選了項目之後先把焦點還給「更多」再打開對話框，所以對話框關閉時焦點回到「更多」。目前只有「刪除…」一項（pages.md §4 只列刪除）；按鈕文字從「移到垃圾桶…」改成「刪除…」，對齊場景與 pages.md 的用字，確認框的標題仍是「移到垃圾桶？」。
2. **重新處理對話框的預估**：rerun 的 API 仍然沒有 `dry_run`。影音有 `better_source` 時，`estimate_usd`（SRC-R23）就是「用現行設定從 `transcript` 做到最後」的金額，所以只在起點是「字幕」、而且沒有改做到哪裡、處理方案、provider、模型、prompt 與 chunk 時顯示「用目前的設定從字幕步驟做到最後，預估花費 $X。」；其他情況與 `estimate_usd` 為 null 時仍是「無法預估」，不自己推算。不論從哪個按鈕打開對話框都一樣（從字幕步驟重跑時一定會改用更好的來源）。
3. **「新增」抽屜的預估花費**：辨識成功、不是頻道、Bilibili 的分P 格式正確、Podcast 至少勾一集時，用和「開始處理」相同的內容（網址、處理方案、分P、集）呼叫 `POST /api/estimate`；內容停止變動 400ms 後才送出、不重試、同樣的內容 60 秒內不重送，因為估算可能呼叫 yt-dlp（10.5 第 6 點的顧慮仍在，只是改成「辨識成功之後才估」）。只顯示已知金額：全部未知寫「無法預估花費。」（不顯示 $0.00），部分未知寫「預估花費至少 $X，有些部分無法預估。」；伺服器的 `notes` 原樣列在下面。估算失敗（例如 429）只寫一行原因，不擋「開始處理」。書籤、分享帶進來的網址（ENTRY-R2、R4）走同一個抽屜，所以也會顯示預估。
4. **貼上頻道時的「建立訂閱」**：頻道（`subscribable`）在預覽中顯示「這是頻道。要追蹤新內容，請建立訂閱。」與「建立訂閱」按鈕，「開始處理」維持停用。按下後關閉抽屜，前往 `/subscriptions?subscribe=<貼上的網址>`，訂閱頁直接打開「新增訂閱」抽屜並帶入網址（抽屜的說明就是「同一個頻道只在一台實例上訂閱，避免重複處理。」），關閉時從網址移除參數。帶的是貼上的網址（resolve 對頻道回的 `url` 就是原網址）；SUB-R1 不接受的形式（例如 `/c/<名稱>`、UP 主空間）由建立時的 422 說明。Podcast feed 仍然可以勾選集數新增，另外多一個「建立訂閱」的文字按鈕（pages.md §2 寫「貼上頻道或 RSS 網址時」提示訂閱），不擋新增。訂閱頁的參數只做長度上限（2048），不在前端判斷格式。
5. **總覽的告警讀既有的 API，沒有加到 `/api/overview`**：brief 原本要在 `OverviewResult` 加 `alerts`，但 OPS 規格的介面寫「`GET /api/system` 加上 `alerts`……總覽（LIB-09）讀這兩個欄位」，`system.go` 的註解也這樣寫；而且 `/api/overview` 是 `read` scope、`/api/system` 只有 owner，告警訊息含排程的錯誤與影音標題，放進 overview 會讓只有 `read` 的 token 也看得到。所以總覽另外讀 `GET /api/system` 的 `alerts`，與 `GET /api/notifications?state=dropped`（NOTIFY-R8 寫 `notify.dropped`「總覽顯示」）。兩個查詢各自每 5 分鐘重讀（告警每 15 分鐘才檢查一次），不跟著 SSE 失效、不重試；讀不到（403、501、其他錯誤）或兩者都是空的時整區隱藏。告警列出種類、訊息、開始時間與已持續多久（沿用設定「系統」的字串；`formatElapsed` 從 `OpsStatus` 搬到 `src/i18n`）。沒有送出的通知只列**建立時間在最近 24 小時內**的（API 沒有「何時 dropped」，只有 `created_at`；24 小時與 NOTIFY-R10 的去重視窗相同），最多 5 則，連到通知設定。「今天」裡的 `notify.dropped` 事件照舊保留（那是時間軸，這裡是狀態），兩處都會出現。
6. **訂閱的「新內容」**：SUB 規格原本沒有定義這個數字，定為「這個訂閱建立、還沒有標為已讀、不在垃圾桶中的影音數」（SUB-R14、SUB 待決問題 S11）：讀過就歸零，與媒體庫的已讀狀態一致。後端在訂閱的每個回應加 `new_items`（列表用一次 `GROUP BY` 算出），場景是 `features/SUB-subscriptions.feature` 的「The subscription list counts the new items that are still unread」（`@M4`）。列表在「處理方案」與「狀態」之間多一欄「新內容」，數字不連到媒體庫（`GET /api/items` 沒有依訂閱篩選）。
7. **筆記模板的草稿預覽與未知變數**：預覽 API 原本只接受已儲存的 `<id>@<版本>`。`POST /api/note-templates/:id/preview` 加選填的 `body`、`read_button`、`checks`（NOTE-R12、NOTE 待決問題 N23），伺服器套在模板的副本上、走與儲存相同的檢查（未知變數 422），不建立版本；場景是 `features/NOTE-notes.feature` 的「An unsaved draft can be previewed without creating a version」「An unsaved draft with an unknown variable is refused」（`@M3`）。編輯頁有修改時把草稿一起送出（停止輸入 500ms 後），說明改成「預覽的是編輯中的內容，還沒有儲存。」；沒有修改時照舊預覽 `<id>@<版本>`。內文從 textarea 改成 Prompt 庫的 CodeMirror 編輯器（`PromptCodeEditor` 多了 `scan` 參數，沒有複製一份）：筆記模板的掃描（`templateVars.ts`）只看 `{{ … }}` 中的欄位參照（`.Title`、`.Item.URL`），在 NOTE-R11 清單中的高亮、不在的加波浪底線並列出「不認識的變數 .X。」（與後端訊息相同）；`if`／`else`／`end`、函式、字串常數與註解不判斷，交給後端，寧可少標也不誤標。模板頁因此也載入 `prompt-editor.css`。
8. **vault 與 hook 的清單**：`GET /api/vaults` 早就有（VAULT-R1），hook 沒有 API，新增唯讀的 `GET /api/hooks`（`admin`），只回 id（VAULT-R10、VAULT 待決問題 V18：`argv` 可能含參數形式的秘密）；場景是 `features/VAULT-vault-output.feature` 的「The vaults and hooks from the config file are listed by id only」（`@M3`）。設定 › 輸出把「尚未啟用」換成兩個唯讀清單：vault 列 id、adapter（本機資料夾或 git repo）與根資料夾，hook 只列 id，沒有任何輸入框（V1：只能在設定檔修改）。處理方案的輸出目標多了「Vault」：選用設定檔的 vault（預設第一個）、vault 內的資料夾（選填，`folder`）、勾選寫入後的 hook；vault 沒有「字幕樣式」，字幕檔格式可以全部不勾（送 `[]`，只寫筆記與圖片，VAULT-R5）。換目標時只留下新目標認得的欄位（`retarget`，對照 `internal/pipeline/schema.go` 的兩組欄位），避免伺服器回「不認識的欄位。」；處理方案中已經寫了、但設定檔沒有的 vault 或 hook 仍會列出，讓使用者看得到、改得掉。`lang`、`on_user_change` 仍不在表單中、原樣保留（10.8）。

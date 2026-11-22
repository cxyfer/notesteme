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
| 指令面板（`Ctrl K`）、`G` 開頭的快捷鍵、`?` 快捷鍵說明 | 未做 | 之後的切片 |
| 影音詳情、佇列 | 未做 | UI-2 |
| 設定各分區 | 未做 | UI-3 |
| Prompt 庫、處理方案、字幕樣式 | 完成（UI-3a，見 10.6） | `web/client/src/pages/{prompts,profiles,styles}` |
| 影音詳情、佇列 | 完成（UI-2，見 10.6） | `web/client/src/pages/item`、`src/pages/queue` |
| 設定各分區、Prompt 庫、處理方案、字幕樣式 | 未做 | UI-3 |
| 登入頁背景的淡化時間帶（§13） | 未做 | 等 UI-1 的時間帶元件 |
| 768–1279px 的收合導覽（只剩圖示） | 未做：沒有圖示前維持展開；UI-1 沒有引入 Phosphor Icons | 之後的切片 |

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
### 10.6 UI-3a：Prompt 庫、處理方案、字幕樣式（2026-11-23）

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

### 10.7 UI-3a 的決定

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

### 10.6 UI-3b：設定、詞彙表與術語候選、設定匯出與匯入（2026-11-23）

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
| 通知、預算 | 分區列出，內容是「M4 起提供。」，沒有表單 | `PlannedSection.tsx` |
| 測試：每個分區一個 axe 測試（有 API 替身資料）、key 只顯示末四碼且不留在畫面上、token 只顯示一次、cookie 上傳與錯誤、改密碼錯誤與不一致、候選 409、匯入逐項錯誤、匯出、更新 yt-dlp、語言整段送出、主題同步 | 完成 | `src/pages/settings/settings.test.tsx` |

### 10.7 UI-3b 的決定

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
4. **差異的比較方式**：校正後（`corrected`）對「校正前實際存在的最近變體」（segmented → normalized → source）。兩邊依**時間重疊**分組（校正可能合併或拆句），每組以 code point 做 LCS 逐字比對（不切開 surrogate pair）；超過 400 萬格時退化成整段刪除＋新增。刪除用 `<del>`（刪除線）、新增用 `<ins>`（底線），另有 visually-hidden 的「刪除：」「新增：」文字。沒有校正結果的段落顯示原文並標「尚未校正」。**自寫元件，不用 CodeMirror merge view**：字幕是逐句的短文字，自寫的 diff 約 2KB；整個詳情頁是 lazy route（gzip 13.4KB），初始 JS 188.3KB（UI-1 是 174.2KB，增加的是佇列頁與共用的 query key／VirtualList）。
5. **模式依步驟狀態決定**：transcript `done` 才有「原始」、normalize `done` 才有「正規化」、correct 已開始（不含 skipped）才有「校正」「差異」、translate `done` 才有「譯文」「對照」、有 `edited` 字幕才有「已編輯」。預設是校正 > 正規化 > 原始。譯文的語言用最近一次執行的設定快照 `language.targets`，多個時可以選。沒有 `convert`／`localize` 的區分（pages.md §4 的「只差字形」規則）：API 沒有提供，譯文與對照只在 translate 完成時出現。
6. **YouTube 播放器不載入 IFrame API 的 script**：CSP 的 `script-src` 只有 `'self'`，不放寬。用嵌入網址的 `enablejsapi=1&origin=…` 加 postMessage（送 `listening`，收 `infoDelivery.currentTime`；跳播送 `seekTo`、`playVideo`）；訊息只接受 YouTube 的 origin 且 `source` 是這個 iframe。4 秒內沒有收到訊息就降級成「更新 `start` 重新載入」。協定是 YouTube 未公開文件化的部分，**需要在真實瀏覽器人工驗證**（LIB-03 是 `@manual`）。頁面的 `Referrer-Policy` 是 `same-origin`，YouTube 嵌入需要來源網域，只在 iframe 元素上設 `referrerpolicy="strict-origin-when-cross-origin"`（不改全站標頭）。Bilibili 沒有公開的控制介面，跳播用 `t` 參數重新載入，另附「在 Bilibili 開啟這個時間」。
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
   - 「需要你處理」沒有彙總 API：依步驟各查一次 `GET /api/items?status=<stage>:failed&status=<stage>:blocked&limit=1` 的 `total`（7 個請求；同一部影音可能在不同步驟各算一次）。
9. **501 不重試**：TanStack Query 的預設重試排除 501（功能還沒做），佇列沒有啟用時立即顯示說明。
10. **axe 不進 iframe**（`iframes: false`）：嵌入的播放器是跨來源的；`routes.a11y.test.tsx` 用 `defaultRoutes()` 替身，不連網路。
11. **新增相依**：無。`api.patch`、`VirtualListHandle.scrollToIndex`／`firstVisibleIndex`、`formatBytes`／`formatUsd` 是新增的 API，不改既有的意義。

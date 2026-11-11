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

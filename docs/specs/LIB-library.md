# LIB：媒體庫、影音詳情與佇列頁

- 狀態：draft
- 里程碑：M1（列表、基本篩選、標題搜尋、字幕分頁、佇列頁）、M2（對照、差異、重新處理選單）
- 相關：plan 第 14 節；UI [媒體庫](../ui/pages.md#3-媒體庫)、[影音詳情](../ui/pages.md#4-影音詳情)、[佇列](../ui/pages.md#6-佇列)、[設計語言](../ui/design-language.md)
- 驗收場景：[`features/LIB-library.feature`](../../features/LIB-library.feature)

## 目的

在 Web UI 中瀏覽、篩選所有影音，查看字幕與處理狀態，並掌握佇列。資料面的行為透過 API 驗證；視覺與互動以 UI 規格為準，用 `@ui` 場景驗收。

## User stories

| ID | Story | 里程碑 |
|---|---|---|
| LIB-01 | As the owner, I want to filter my library by channel, platform, stage status and error class, so that I can find what needs attention. | M1 |
| LIB-02 | As the owner, I want to search my library by title, so that I can find an item quickly. | M1 |
| LIB-03 | As the owner, I want to read an item's subtitles next to its player and jump to any line, so that I can check the result against the video. | M1 |
| LIB-04 | As the owner, I want to see each resource lane of the queue with its limit and running tasks, so that I understand why something has not started yet. | M1 |
| LIB-05 | As the owner, I want the library and item pages to update live while processing, so that I do not have to reload. | M1 |
| LIB-06 | As the owner, I want to compare the original, corrected and translated subtitles and see the differences, so that I can judge what the LLM changed. | M2 |
| LIB-07 | As the owner, I want to rerun an item from any stage or with another prompt or model, so that I can improve a result without starting over. | M2 |

## 規則

| ID | 規則 | 里程碑 |
|---|---|---|
| LIB-R1 | `GET /api/items` 支援的篩選：`channel`、`platform`、`status`（`<stage>:<status>`，可多個）、`error_class`、`published_from`、`published_to`、`duration_min`、`duration_max`、`lang`、`read_state`、`timing`（例如只看含 `interpolated` 的影音）。多個條件之間為 AND。 | M1 |
| LIB-R2 | 篩選條件反映在 UI 的 URL 參數中，重新整理或分享網址後得到相同的結果。 | M1 |
| LIB-R3 | 標題搜尋不分大小寫，中文與英文都可以用部分字串搜尋（包括 2 個字以下）。 | M1 |
| LIB-R4 | 列表預設依頻道分組，可以改成依發布日或狀態分組；分頁使用游標，單頁最多 200 筆。 | M1 |
| LIB-R5 | 影音詳情的字幕分頁可以切換「原始」與「正規化」（M1），以及「校正」「差異」「譯文」「對照」（M2）；切換模式不改變捲動位置。 | M1 |
| LIB-R6 | 點擊字幕句會讓播放器跳到該句的 start。YouTube 用 IFrame Player API，本機檔案與 Podcast 用 HTML5 media，後端支援 Range 請求。 | M1 |
| LIB-R7 | 時間來源不是 `source` 的 cue 有可辨識的標示，並可以篩選。 | M1 |
| LIB-R8 | 佇列頁依資源類別分通道顯示：並行上限、使用中、等待中；上限可以直接調整。 | M1 |
| LIB-R9 | 收到 SSE 事件時只更新受影響的列；新增的影音只顯示「有 N 個新項目」，不打亂目前的列表。 | M1 |
| LIB-R10 | 空狀態與篩選結果為空時的文案見 UI README §4.3。 | M1 |
| LIB-R11 | 「重新處理」可以選擇起始步驟，並可換 prompt 或模型；會產生新版本，舊版本保留並可比較。 | M2 |
| LIB-R12 | 「差異」模式逐字標示校正前後的差異；`localize` 模式額外提供「譯文」與「對照」。 | M2 |

## 介面

- API：`GET /api/items`、`GET /api/items/:id`、`GET /api/items/:id/cues`、`GET /api/items/:id/media`、`GET /api/queue`、`GET /api/events`
- UI：見上方連結

**`GET /api/items`（LIB-R1、R3、R4、R7）**

- 查詢參數：
  - `platform`：`youtube`、`bilibili`、`podcast`、`local`。
  - `channel`：頻道 ID。
  - `status`：`<stage>:<狀態>`，可以重複。stage 必須是 pipeline 的步驟，狀態必須是已知的步驟狀態，否則回 422。同一個 stage 的多個值是 OR，不同 stage 之間是 AND。stage 的狀態取最新的一次執行。
  - `error_class`、`lang`（原文語言）。
  - `published_from`、`published_to`：RFC 3339。
  - `duration_min`、`duration_max`：秒，不得小於 0。
  - `timing`：timing origin。條件是影音目前的 transcript 產出中，至少有一個 cue 的 timing 是這個值。
  - `q`：標題搜尋，去掉前後空白，最多 200 字元。
  - `limit`：1～200，預設 50。
  - `cursor`。
  - `read_state`：M1 沒有資料模型，帶這個參數回 501。
- 多個條件之間是 AND。
- 回應：`{items, total, next_cursor}`。
  - `total` 是符合條件的總數，和目前在第幾頁無關。
  - 最後一頁省略 `next_cursor`，不回 null。
- 排序：`created_at` 由新到舊，相同時依 `id`。
- 游標：
  - 不透明的 base64url 字串，內容是位置與篩選條件的指紋。
  - 格式不正確、被竄改、或篩選條件改變時，回 422 `invalid_request`，訊息「游標不正確或已失效，請從第一頁重新讀取。」。
  - 游標只代表位置，不帶權限。
- 標題搜尋：
  - 比對的是用 Go 的 `strings.ToLower` 轉換後的標題（存在 `items.title_fold`），用 `instr` 做子字串比對。
  - 沒有萬用字元，2 個字以下也能搜。
  - 不用 SQLite 的 `LIKE` 或 `lower()`，因為兩者只對 ASCII 不分大小寫。也不用 FTS5 trigram，因為它不能比對 3 個字以下的字串。
- timing 篩選：transcript 產出寫入時，記錄 cues 中出現過的 timing origin（`artifacts.timings_json`）。

**`GET /api/queue`（LIB-R8）**：`{lanes: [{class, limit, running, waiting, paused}]}`。

## 不在範圍內

- 字幕全文搜尋、儲存檢視、批次操作（M4，故事地圖 SRCH、LIB-08）。
- 畫面與筆記分頁（M3）。

## 待決問題

- 場景修改（2026-11-15，LIB 切片 DoR，owner 同意）：Background 的 `yt:agile43xxxx` 由 `has been ingested` 改成 `has been processed up to "metadata"`，R8 的 download `waiting` 由 2 改成 1。原因是新增後、取得 metadata 之前標題是空的，不能用來驗證標題搜尋（ING-R12：新增時不同步取得 metadata）。
- LIB-R4 的「依頻道、發布日、狀態分組」是 UI 的呈現。API 目前只依建立時間排序，`group_by` 等 UI 切片再設計。
- 標題搜尋不做 NFKC（全形英數）與簡繁互通，例如「假装」找不到「假裝」，之後再評估。
- 舊的 transcript 產出沒有 `timings_json`，不回填。M1 尚未發布，沒有舊資料。
- R6（本機檔案的 Range 請求）要先有 ING-05 的本機檔案新增與 inbox watcher，留給 ING 切片。
- Bilibili 嵌入播放器能否控制播放位置（M0）。不行時，點擊字幕句改成「在 Bilibili 開啟這個時間」。

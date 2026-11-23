# LIB：媒體庫、影音詳情與佇列頁

- 狀態：draft（M4 章節 LIB-R13～R23：ready）
- 里程碑：M1（列表、基本篩選、標題搜尋、字幕分頁、佇列頁）、M2（對照、差異、重新處理選單）、M4（批次操作、總覽，LIB-R13～R23）
- 相關：plan 第 14 節；[ADR-0021](../adr/0021-m4-scheduler-subscriptions-and-outbox.md)（第 8 點）；UI [總覽](../ui/pages.md#1-總覽)、[媒體庫](../ui/pages.md#3-媒體庫)、[影音詳情](../ui/pages.md#4-影音詳情)、[佇列](../ui/pages.md#6-佇列)、[設計語言](../ui/design-language.md)
- 驗收場景：[`features/LIB-library.feature`](../../features/LIB-library.feature)；M4：[`features/LIB-bulk-overview.feature`](../../features/LIB-bulk-overview.feature)

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
| LIB-08 | As the owner, I want to select many items and act on them at once, so that I can rerun, pause or delete in bulk. | M4 |
| LIB-09 | As the owner, I want an overview page, so that I see what needs my attention first. | M4 |

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
| LIB-R13 | `POST /api/items/batch`，body `{action, ids, from?, profile?, delete_outputs?, dry_run?}`，對多部影音執行同一個動作。`action` 是 `rerun`、`change_profile`、`delete`、`mark_read`、`mark_unread`、`export_bundle`。`ids` 是影音 ID 或代號（`yt:…`、`bili:<BV>/p<n>`、`pod:…`、`local:…`）；`bili:<BV>` 不帶分P時展開成所有分P。展開、去掉重複之後必須是 1～500 部。驗證失敗時回 422 `invalid_request`，整個請求不執行：`ids` 是空的「至少要選一部影音。」；超過 500 部「一次最多選 500 部影音。」；不認識的 `action`「不支援的批次動作 <action>。」；`rerun`、`change_profile` 的 `from` 缺少或不是 pipeline 的步驟「from 必須是 pipeline 的步驟。」；`change_profile` 的 `profile` 缺少或不存在「找不到處理方案 <名稱>。」。`errors[].path` 是欄位名稱。 | M4 |
| LIB-R14 | `dry_run: true` 不改變任何資料、不呼叫外部服務、不寫事件，只回報影響：`{action, dry_run: true, count, items[{id, ref, title, ok, code?, message?}], estimated_usd, unknown_estimates, budget{remaining_usd, exceeds}}`。`count` 是 `ok: true` 的影音數。`rerun`、`change_profile` 的 `estimated_usd` 是每部影音從 `from` 起所有付費任務依 COST-R5 估算的總和（`change_profile` 用新處理方案的生效設定）；無法估算的任務不加總，計入 `unknown_estimates`。`budget.remaining_usd` 是 COST-R4 的 `remaining_usd`；`exceeds` 為 `estimated_usd` 大於它。沒有月預算時 `remaining_usd` 為 `null`、`exceeds` 為 `false`。其他動作的 `estimated_usd` 為 0。 | M4 |
| LIB-R15 | 實際執行時逐部處理，每部是獨立的交易：一部失敗不影響其他部，也不回復已經成功的。回應 200：`{action, dry_run: false, count, succeeded, failed, items[{id, ref, ok, code?, message?}]}`，`items` 依請求中的順序。單部的錯誤碼與訊息和單一影音的 API 相同：找不到 `not_found`「找不到這部影音。」；在垃圾桶中 `in_trash`「這部影音在垃圾桶中。」。完成後寫一筆事件 `items.batch`，欄位 `action`、`count`、`succeeded`、`failed`。 | M4 |
| LIB-R16 | scope 依動作，在讀取影音之前對整個請求判斷一次；缺少時回 403 並帶 `required_scope`，不執行任何影音。`rerun`、`change_profile`、`mark_read`、`mark_unread` 需要 `ingest`；`delete` 需要 `admin`（DEL-R12）；`export_bundle` 和 BND 的匯出相同。`dry_run` 需要的 scope 和實際執行相同。session 擁有全部權限。MCP 不提供批次操作的 tool。 | M4 |
| LIB-R17 | `rerun` 對每部影音等同 `POST /api/items/:id/rerun {from}`（LIB-R11）。`change_profile` 先把影音的處理方案改成 `profile`，之後的重跑也使用它，再從 `from` 重跑。建立的任務照 COST-R5～R8 預留並檢查預算，超過預算的步驟 `blocked`（`budget_blocked`）；這種情況在批次回應中仍是 `ok: true`。 | M4 |
| LIB-R18 | `delete` 對每部影音套用 DEL 的所有規則：DEL-R1、R2；`delete_outputs` 照 DEL-R9；權限照 DEL-R12。`dry_run` 的回應另外有 `deletion{artifacts, bytes, spent_usd, outputs, modified_outputs}`，是各影音 DEL-R3 影響範圍的合計：`outputs` 是輸出檔總數，`modified_outputs` 是其中被改過的數量。垃圾桶中的影音在 `items` 中是 `in_trash`，不計入合計。 | M4 |
| LIB-R19 | `mark_read`、`mark_unread` 設定 `items.read_state`：`unread`（新增時的預設）或 `read`。`GET /api/items/:id` 與 `GET /api/items` 的每個項目都回傳 `read_state`。M4 起，`GET /api/items` 的 `read_state` 篩選（LIB-R1）接受 `read`、`unread`，不再回 501；其他值回 422。打開影音詳情不會自動標為已讀。 | M4 |
| LIB-R20 | `export_bundle` 把所有 `ids` 匯出成一個 bundle，回應與限制以 BND 規格的匯出為準。`dry_run` 只回 `count` 與 `items`，不建立 bundle。 | M4 |
| LIB-R21 | `GET /api/overview?tz=`（`read`）回傳總覽：<br>（1）`now{items, total, more}`：有步驟 `running` 的影音，依最早開始的執行中步驟排序，最多 3 部，每部 `{id, ref, title, stage, progress{done, total}}`（`progress` 是那個步驟的任務完成數）；`more` 等於 `total` 減去列出的數量。<br>（2）`needs_you{items, total}`：有步驟 `blocked`（任何原因）或 `failed` 的影音，每部一列 `{id, ref, title, stage, status, reason, message, actions}`，取 pipeline 順序中第一個這樣的步驟；依該步驟的更新時間由新到舊，最多 50 部。<br>（3）`today`：`tz` 的今天 00:00 起的事件，由新到舊，最多 50 筆，每筆 `{at, type, item?, title?}`，只列給使用者看的類型（見介面）。<br>（4）`recent_done`：最近 5 部所有步驟都已結束（`done` 或 `skipped`）的影音。<br>（5）`month{month, spent_usd, reserved_usd, monthly_usd, timezone}`：COST-R4 的數字。<br>（6）`disk`（LIB-R22）與 `notices`（例如 COST-R13）。<br>垃圾桶中的影音不出現在任何區塊。`tz` 預設為 `budget.timezone`，不認識時回 422「不認識的時區 <值>，請用 IANA 時區名稱，例如 Asia/Taipei。」。 | M4 |
| LIB-R22 | 磁碟用量 `disk{data_bytes, media_bytes, artifacts_bytes, uploads_bytes, free_bytes, measured_at}`：`/data` 及其下 `media`、`artifacts`、`uploads` 的檔案大小總和，以及 `/data` 所在檔案系統的可用空間。不跟隨 symlink；結果快取 5 分鐘（以注入的 clock 判斷），一次統計最多 30 秒，逾時時回傳上一次的結果與 `measured_at`，從來沒有統計完成時各數字為 `null`。只顯示，不刪除任何檔案（媒體檔保留策略不在 M4，ADR-0021 第 8 點）。 | M4 |
| LIB-R23 | UI：總覽（UI 第 1 節）的「現在」最多列 3 部並顯示「還有 N 部」連到佇列，「需要你處理」每一項都有直接處理的按鈕、沒有項目時整個區塊隱藏，「今天」依時間排列，底部顯示本月花費與預算。媒體庫（UI 第 3 節）選取多列後出現批次操作列；執行之前先以 `dry_run` 取得影響範圍，確認對話框顯示數量與預估費用，刪除時另外顯示 DEL-R3 的合計；部分失敗時列出失敗的影音與原因。 | M4 |

## 介面

- API：`GET /api/items`、`GET /api/items/:id`、`GET /api/items/:id/cues`、`GET /api/items/:id/media`、`GET /api/queue`、`GET /api/events`
- UI：見上方連結

**`GET /api/items`（LIB-R1、R3、R4、R7）**

- 查詢參數：
  - `platform`：`youtube`、`bilibili`、`podcast`、`local`。
  - `channel`：頻道 ID。
  - `status`：`<stage>:<狀態>`，可以重複。stage 必須是 pipeline 的步驟，狀態必須是已知的步驟狀態，否則回 422。同一個 stage 的多個值是 OR，不同 stage 之間是 AND。stage 的狀態取最新的一次執行；沒有任何執行紀錄的 stage 視為 `pending`。值中不能用逗號分隔多個條件，含逗號時回 422 並提示改成重複 `status` 參數。
  - `error_class`、`lang`（原文語言）。
  - `published_from`、`published_to`：RFC 3339。
  - `duration_min`、`duration_max`：秒，不得小於 0。
  - `timing`：timing origin。條件是影音目前的 transcript 產出中，至少有一個 cue 的 timing 是這個值。
  - `q`：標題搜尋，去掉前後空白，最多 200 字元。
  - `limit`：1～200，預設 50。
  - `cursor`。
  - `read_state`：M1 沒有資料模型，帶這個參數回 501（M4 起見 LIB-R19）。
- 多個條件之間是 AND。
- 回應：`{items, total, next_cursor}`。
  - `total` 是符合條件的總數，和目前在第幾頁無關。
  - 最後一頁省略 `next_cursor`，不回 null。
- 排序：`created_at` 由新到舊，相同時依 `id`。
- 游標：
  - 不透明的 base64url 字串，內容是位置，以及涵蓋位置與篩選條件的 HMAC。HMAC 金鑰只存在記憶體，server 重新啟動後舊游標失效（回 422）。
  - 格式不正確、被竄改、或篩選條件改變時，回 422 `invalid_request`，訊息「游標不正確或已失效，請從第一頁重新讀取。」。
  - 游標只代表位置，不帶權限。
- 標題搜尋：
  - 比對的是用 Go 的 `strings.ToLower` 轉換後的標題（存在 `items.title_fold`），用 `instr` 做子字串比對。
  - 沒有萬用字元，2 個字以下也能搜。
  - 不用 SQLite 的 `LIKE` 或 `lower()`，因為兩者只對 ASCII 不分大小寫。也不用 FTS5 trigram，因為它不能比對 3 個字以下的字串。
- timing 篩選：transcript 產出寫入時，記錄 cues 中出現過的 timing origin（`artifacts.timings_json`）。

**`GET /api/queue`（LIB-R8）**：`{lanes: [{class, limit, running, waiting, paused}]}`。

### M4

- API：`POST /api/items/batch`（scope 依動作，LIB-R16）、`GET /api/overview`（`read`）；`GET /api/items` 的 `read_state` 篩選（LIB-R19）；`GET /api/items/:id` 的 `read_state`。
- 資料表：`items.read_state`（`TEXT NOT NULL DEFAULT 'unread'`，plan 第 21 節已列）；建議加索引 `stage_runs (status) WHERE status IN ('running', 'blocked', 'failed')` 給總覽使用。
- 總覽的 `today` 列出的事件類型：`item.created`、`item.deleted`、`item.restored`、`item.purged`、`items.batch`、狀態變成 `done`、`failed`、`blocked` 的 `stage.status`，以及 M4 的 `budget.warning`、`notify.dropped` 與 SUB 規格定義的訂閱事件。`running`、`pending` 等過程中的 `stage.status` 不列出。
- 處理方案、`pipeline.Stages`、`outputs`：不影響。
- CLI：既有的 `notesteme items rm <ref>...`（DEL-R13）已經可以一次刪除多部；M4 不新增批次的 CLI 子命令。
- UI：總覽（UI 第 1 節）、媒體庫的批次操作列（UI 第 3 節）。

## 不在範圍內

- 字幕全文搜尋見 [SRCH 規格](SRCH-search.md)；儲存檢視不在 M4 範圍內。批次操作見 LIB-R13～R20。
- 畫面與筆記分頁（M3）。

### M4

- 單部影音的暫停與批次暫停（L-M1）。
- 刪除媒體檔的批次動作與媒體檔保留策略（L-M2，ADR-0021 第 8 點）。
- 字幕全文搜尋（SRCH 規格）。
- 總覽的自訂版面與小工具。

## 待決問題

- 本機媒體的串流（LIB-R6，2026-11-21，LOCAL 切片實作時決定）：`GET /api/items/:id/media` 需要 `read` scope，支援 `Range`、`If-Range`（206、416），ETag 為內容的 `sha256`。用路徑加入的檔案可能在原處被替換，只有目前的大小與修改時間和記錄雜湊時相同才送出 ETag，否則只靠 `Last-Modified` 判斷條件請求；上傳的副本不可變，有雜湊就送出。Content-Type 只依副檔名的白名單決定（mp4、m4a、mp3、mkv、webm、ogg、opus、wav、flac、mov、aac），其他一律 `application/octet-stream`，並加上 `X-Content-Type-Options: nosniff` 與 `Content-Security-Policy: sandbox; default-src 'none'`。影音在垃圾桶中回 404 `in_trash`；沒有本機媒體（例如 YouTube）回 404 `no_media`；檔案不見了回 404 `media_missing`。Podcast 的媒體在 PODCAST 切片接上。
- 場景修改（2026-11-15，LIB 切片 DoR，owner 同意）：Background 的 `yt:agile43xxxx` 由 `has been ingested` 改成 `has been processed up to "metadata"`，R8 的 download `waiting` 由 2 改成 1。原因是新增後、取得 metadata 之前標題是空的，不能用來驗證標題搜尋（ING-R12：新增時不同步取得 metadata）。
- 場景修改（2026-11-15，LIB 審查的 mutation testing 後，owner 同意）：
  - R1「Filter by stage status」改成同時篩選 `metadata:done` 與 `normalize:done`，驗證不同 stage 之間是 AND；只有一個 stage 時，改成 OR 也會通過。
  - R8 的 download 上限由 2 改成 3。2 是預設值，上限沒有套用或寫死都會通過。
  - 「`waiting` 不含執行中的任務」、「垃圾桶中的影音不在列表與 `total` 中」、排序、`limit` 與最後一頁沒有 `next_cursor`，由單元測試守住。
- 某個 stage 還沒有任何執行紀錄時（例如只處理到 metadata 的影音的 normalize），視為 `pending`，`status=<stage>:pending` 會列出它（2026-11-15，LIB 審查後決定）。
- LIB-R4 的「依頻道、發布日、狀態分組」是 UI 的呈現。API 目前只依建立時間排序，`group_by` 等 UI 切片再設計。
- 標題搜尋不做 NFKC（全形英數）與簡繁互通，例如「假装」找不到「假裝」，之後再評估。
- 舊的 transcript 產出沒有 `timings_json`，不回填。M1 尚未發布，沒有舊資料。
- R6（本機檔案的 Range 請求）要先有 ING-05 的本機檔案新增與 inbox watcher，留給 ING 切片。
- Bilibili 嵌入播放器能否控制播放位置（M0）。不行時，點擊字幕句改成「在 Bilibili 開啟這個時間」。
- LIB-R11 場景已修改 `.feature`（2026-11-22，M2 S3 實作時發現，依 /goal 採 advisor 建議，待 owner 追認）。原場景的 Background 沒有指定處理方案，內建 `default` 的 `correct` 沒有 provider，依 X1 是 `skipped`（`not_configured`），第一版 corrected 不會產生，斷言「has 2 versions」失敗（S3 之前因為 `processed up to` 的 M2 目標 pending 而看不出來）。改成：場景內建立 `llm-lib` 處理方案（`correct` 用 anthropic）、用 `rerun` 帶 `"profile": "llm-lib"` 產生第一版（設定快照在新增當下已凍結，不能靠改預設處理方案），再用 openai 的覆寫重跑第二次，斷言兩個版本都保留。
- 影音路由接受代號（2026-11-23，M2 S9 實作 MCP 的 `item` 參數時決定，依 /goal 採保守預設，**待 owner 追認**）：`GET /api/items/{id}`、`GET /api/items/{id}/cues`、`GET /api/items/{id}/subtitles`、`POST /api/items/{id}/subtitles/import` 的 `{id}` 也接受影音代號（`yt:…`、`bili:…/p1`，URL 編碼後放進路徑）；代號在垃圾桶中時回 404 `in_trash`，與用 ID 存取相同。其他影音路由仍只接受 ID。細節見 MCP 規格待決問題 S2。
- UI-1 的媒體庫（2026-11-23，依 /goal 採 advisor 建議，**待 owner 追認**；細節見 [ui/README 10.5](../ui/README.md#105-ui-1-的決定)）：
  - LIB-R4 的「預設依頻道分組」沒做，列表依建立時間排序、不分組：前端分組會讓同一頻道散在不同的 cursor 頁，而且 `GET /api/items` 沒有頻道名稱。等 API 有 `group_by` 再做。
  - `GET /api/items` 沒有步驟狀態、頻道名稱與縮圖網址，UI 對每個渲染出來的列另外讀 `GET /api/items/{id}`（N+1，只有可視範圍）。建議之後在列表回應加 `stages` 摘要（各步驟最新的 `status`、`block_reason`、`error_class`）、頻道名稱與縮圖網址。
  - LIB-R9：`stage.status` 與 SSE 重新連上都不讓列表失效（只更新那一列的詳情），否則重抓時新項目會插入頂端。
- 新增抽屜的 YouTube 播放清單（2026-11-23，UI-1，**待 owner 追認**）：ING 與 API 沒有範圍參數，UI 只說明「會新增整份播放清單」，不讓使用者選範圍。要選範圍需要 `POST /api/items` 加 `entries`（或 `playlist_items`）並由 resolve 列出清單內容。

### M4

- L-M1：LIB-08 的故事寫「rerun, pause or delete」，但 UI 第 3 節的批次操作列與 M4 的動作清單都沒有暫停，資料模型也沒有「暫停單部影音」（PIPE-R10 只有整個佇列與資源類別）。決定（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：M4 不做暫停；要停下時用 `delete`（可還原）或暫停資源類別。之後要做，需要另外定義單部影音的暫停狀態與恢復方式，並修改故事的文字。
- L-M2：UI 第 3 節的批次操作列有「刪除媒體檔」，M4 的動作清單是刪除影音（DEL）。決定（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：「刪除媒體檔」屬於媒體檔保留策略，延到 M4 之後（ADR-0021 第 8 點、故事地圖第 7 節）；LIB-09 只顯示磁碟用量（LIB-R22）。UI 第 3 節要把批次操作列改成「重新處理、改用其他處理方案、匯出 bundle、刪除、標為已讀」。
- L-M3：批次的路由用 UI 第 3 節既有的 `POST /api/items/batch`，不另取 `/api/items/bulk`（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）。名稱和 Batch API（BATCH 規格）相近，但路徑不同，不會混淆。
- L-M4：`ids` 接受代號（LIB-R13），沿用 M2 S9「影音路由接受代號」的決定；驗收場景的 harness 只替換路徑中的代號，body 中的代號要由 production 解析。
- L-M5：上限 500 部由單元測試驗證（場景寫不出 501 個代號），場景只驗證空的 `ids`。
- L-M6：`export_bundle` 的回應、大小上限與 scope 以 BND 規格為準（BND-R1、BND 規格待決問題 N1）；場景只驗證 `dry_run`。
- L-M7：`internal/api/items_list_test.go` 目前斷言 `?read_state=unread` 回 501；實作 LIB-R19 時要同時改這個單元測試（不是 `.feature`）。
- L-M8：總覽的 `today` 用請求的 `tz`，預設是 `budget.timezone`（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）。UI 從瀏覽器取得時區後帶上 `tz`；Client 沒有其他「使用者時區」的設定。
- L-M9：`POST /api/items/batch` 的 501 stub 先要求 `ingest`（多數動作需要的 scope），因為 stub 不讀 body、判斷不了動作（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）。S13 實作時改成在讀取影音之前依動作判斷（LIB-R16：`delete` 要 `admin`、`export_bundle` 同 BND 的匯出）。`GET /api/overview`（`read`）也是 501 stub。`items_list_test.go` 的 `read_state` 501（L-M7）維持不變。
- M4-S13 LIB-08、09 的實作決定（2026-11-24，依 /goal 採 advisor 建議，待 owner 追認）：
  - L-M7、L-M9 已處理：`items_list_test.go` 改成 `read_state` 只接受 `read`、`unread`（其他值 422）；`POST /api/items/batch` 與 `GET /api/overview` 從 `m4_stubs.go` 移除。
  - L-M10 批次重跑的 `estimated_usd` 是近似值：從 `from`（經 `rerunStart` 調整後）起，每個會執行的步驟依目前的生效設定解析；LLM 引擎的步驟（api）依**目前現有**的輸入字幕切分 chunk，每個 chunk 一個請求，依 COST-R5 的 token 估算與單價（測試控制的金額優先，和 `Reserve` 共用 `cost.Service.Estimate`）；不扣除步驟層與 chunk 層的快取命中（是上限）；下游步驟的輸入還沒產生時用目前現有的字幕代替。agent 不經 API 計費，不計入；frames 的 vision、Gemini 筆記沒有事先估算的方法，各計一個 `unknown_estimates`；輸入字幕不存在或找不到單價的請求也計入 `unknown_estimates`。transcript 的 Groq ASR 目前沒有計量（COST C17），不計入。驗證與生效設定和單一影音的重跑相同（`prepareRerun`），所以 dry_run 中某部影音會失敗的原因和實際執行相同（例如 501、422 的 `code`）。
  - L-M11 **需要 owner 決定**：LIB-R20 寫「把所有 `ids` 匯出成一個 bundle」，但 BND 規格「不在範圍內」列了「一次匯出或匯入多部影音的單一 bundle」，BND 的 UI 也寫「多部影音時逐一下載，不合併成一個檔」，bundle v0 的 manifest 只有一部影音。決定：`export_bundle` 實際執行時不產生合併的檔案，每部 `ok: true` 的影音回傳 `bundle_url`（`/api/items/{id}/bundle`），由用戶端逐一下載；`dry_run` 照 LIB-R20 只回數量與 items。要改成單一檔案需要先定義多部影音的 bundle 格式（ADR-0005）。
  - L-M12 場景「Changing the profile in bulk reruns with the new profile」的 `the effective config of … for "correct" contains: provider: openai` 寫的是步驟層的設定，而其他場景（PRF、ING）寫的是整份快照（`language: …`）。不改 `.feature`：`steps.md` 4.11 補充「預期內容的頂層鍵都不是快照的頂層區段時，改與 `stages.<步驟>` 比對」。
  - L-M13 驗證與權限的順序：路由層只要求已登入（scope 不互相包含，只有 `ingest` 的 token 也要能 `rerun`）；先檢查 `action`（不支援或缺少時 422，缺少時訊息是「不支援的批次動作 。」），再依動作檢查 scope（LIB-R16），然後依序檢查 `ids`、`from`、`profile`，一次只回第一個錯誤；`profile` 缺少時訊息是「找不到處理方案 。」（照 LIB-R13 的字面）。之後才讀影音、展開與去掉重複，再檢查 500 部的上限。本體上限 128 KiB，不認識的欄位由 Huma 回 422。
  - L-M14 回應：同一部影音只留第一次出現的（ID 與代號指向同一部也算重複）；找不到的影音以原始寫法去重複，`ref` 是原始寫法、沒有 `id`；`title` 只在 dry_run 出現。`estimated_usd`、`unknown_estimates`、`budget` 出現在所有動作的 dry_run（場景要求 `export_bundle` 的 dry_run 有 `estimated_usd: 0`，與 LIB-R20「只回 count 與 items」不同，這裡以場景為準）。單部的錯誤碼與訊息經單一影音 API 的 `mapErr`，所以和單一影音相同。
  - L-M15 執行：每部各自呼叫單一影音的方法（`Rerun`、`Trash`、`SetReadState`），沒有放寬任何規則：垃圾桶中的影音 `in_trash`，刪除照 DEL（含 `delete_outputs` 與等待執行中的任務最多 30 秒，所以 500 部的刪除在最壞情況下很慢）；目前單一影音的重跑沒有 rate limit 或佇列上限，批次也沒有另外加。`items.batch` 在實際執行完寫一次（全部失敗也寫），也送給 SSE；dry_run 不寫。
  - L-M16 總覽：「需要你處理」的 `reason` 是 blocked 的 `block_reason`，failed（或沒有 `block_reason`）時是 `error_class`；依那個步驟最後改變的時間（結束、開始、建立中最晚有值的）由新到舊。「現在」的 `progress` 是那個步驟已完成的任務數與不含取消的任務總數。「今天」排除目前在垃圾桶中的影音的事件，但保留 `item.deleted`（是移到垃圾桶這件事本身，帶著當時的標題）；`item.purged` 的影音已不存在，照常列出。`recent_done` 只看最近 2000 筆已結束的 stage run，媒體庫很大時查詢仍有上限。所有區塊只看每個步驟最新的一次執行。沒有成本服務時 `month` 用預設預算（UTC、`monthly_usd: null`）。磁碟用量在 `internal/diskusage`（LIB-R22），Windows 等非 unix 平台的 `free_bytes` 是 `null`。
  - L-M17 沒有 MCP tool（LIB-R16）與 CLI 子命令（介面：M4 不新增批次的 CLI）。前端的總覽頁與批次操作列屬於之後的 UI 切片。
- UI-2 的影音詳情與佇列（2026-11-23，依 /goal 採 advisor 建議，**待 owner 追認**；細節見 [ui/README 10.7](../ui/README.md#107-ui-2-的決定)）：
  - LIB-R12 的「差異」：比較 `corrected` 與校正前實際存在的最近變體（segmented → normalized → source），兩邊依時間重疊分組後逐字比對（code point 的 LCS）。`localize` 與 `convert` 的區分 API 沒有提供，「譯文」「對照」只在 translate 完成時出現。
  - LIB-R6：YouTube 不載入 IFrame API 的 script（CSP `script-src 'self'`），改用嵌入網址的 `enablejsapi` 加 postMessage；播放器 4 秒沒有回應時改成更新 `start` 參數重新載入。Bilibili 沒有公開的控制介面，用 `t` 參數重新載入並附「在 Bilibili 開啟這個時間」。postMessage 協定沒有正式文件，LIB-03（`@manual`）驗收時要在真實瀏覽器確認。
  - LIB-R8：佇列頁的上限調整、暫停／繼續已接上；`GET /api/queue` 不列 Agent 任務、沒有優先順序欄位，單一任務的重試／取消是 501，頁面改提供影音層級的「停止處理」並說明範圍。

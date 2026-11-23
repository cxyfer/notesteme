# SUB：訂閱

- 狀態：ready
- 里程碑：M4
- 相關：plan 第 5、13、16、17、19、21 節；ADR-0004、ADR-0011、ADR-0014、[ADR-0021](../adr/0021-m4-scheduler-subscriptions-and-outbox.md)（第 1、2、6、7 點）；[DEL-R11](DEL-delete.md)、[GRD](GRD-guards.md)、[SRC-R7](SRC-transcript-sources.md)；UI [訂閱](../ui/pages.md#5-訂閱)、[新增](../ui/pages.md#2-新增)（貼上頻道網址時的「建立訂閱」）
- 驗收場景：[`features/SUB-subscriptions.feature`](../../features/SUB-subscriptions.feature)

## 目的

追蹤頻道、播放清單、Bilibili 合集與 Podcast feed，把新內容自動交給 pipeline，不用每次手動貼網址。訂閱是無人值守運作的入口（M4 驗收條件：VPS 上訂閱 3 個頻道、運作一週），所以要能控制花費（回補範圍、過濾）、看得到為什麼略過、不被平台封鎖（隨機延遲、rate limit），而且 feed 內容一律當作不可信的輸入。

## User stories

| ID | Story |
|---|---|
| SUB-01 | As the owner, I want to subscribe to channels, playlists, Bilibili collections and podcast feeds, so that new content is processed without me. |
| SUB-02 | As the owner, I want to choose how far back a subscription goes and what it skips, so that I only pay for what I care about. |
| SUB-03 | As the owner, I want a dry run of a subscription, so that I can see what it would pick up before it costs anything. |
| SUB-04 | As the owner, I want to import my subscriptions from OPML and Google Takeout, so that I do not re-enter them by hand. |
| SUB-05 | As the owner, I want live streams and premieres processed only after the recording is available, so that I do not get half a transcript. |
| SUB-06 | As the owner, I want subscription checks spread out and rate-limited, so that the platforms do not block me. |
| SUB-07 | As an agent, I want to list and create subscriptions through MCP, so that I can follow a channel for the owner. |

## 規則

| ID | 規則 |
|---|---|
| SUB-R1 | `POST /api/subscriptions {url, profile?, overrides?, schedule?, backfill?, filters?, enabled?}` 建立訂閱，回 201 與訂閱內容（`id`、`kind`、`platform`、`external_id`、`url`、`title`、`schedule`、`backfill`、`filters`、`enabled`、`created_at`、`next_check_at`）。建立時只依網址的格式辨識，不連外（探索在排程器內執行，ADR-0021 第 2 點）：YouTube 頻道 `https://www.youtube.com/channel/<UC 開頭的 24 字元 ID>`（`kind: channel`，`external_id` 是頻道 ID）、`https://www.youtube.com/@<handle>`（`kind: channel`，`external_id` 暫時是 `@<handle>`，第一次檢查時以 yt-dlp 換成頻道 ID）；播放清單 `https://www.youtube.com/playlist?list=<ID>`（`kind: playlist`）；Bilibili 合集 `https://space.bilibili.com/<mid>/lists/<sid>?type=season` 與舊格式 `https://space.bilibili.com/<mid>/channel/collectiondetail?sid=<sid>`（`kind: collection`，`external_id` 是 `<mid>:<sid>`）；主機是 YouTube 或 Bilibili（`youtube.com`、`www.youtube.com`、`m.youtube.com`、`youtu.be`、`bilibili.com` 與其子網域、`b23.tv`）但不是上述格式的網址（例如 `/c/<名稱>`、`/user/<名稱>`、UP 主空間 `space.bilibili.com/<mid>/video`）回 422 `unsupported_url`；其他主機的 `http`／`https` 網址一律視為 Podcast feed（`kind: feed`，`external_id` 是正規化後的網址：scheme 與主機轉小寫、去掉預設 port 與 fragment）。單一影片或分P的網址回 422、code `not_a_collection`、訊息「這是單一影片，請直接新增，不需要訂閱。」；其他無法辨識的網址（含非 `http`／`https`）回 422、code `unsupported_url`、訊息「無法辨識這個網址。訂閱支援 YouTube 頻道與播放清單、Bilibili 合集與 Podcast RSS。」。同一個 `(kind, platform, external_id)` 已有訂閱時回 409、code `duplicate_subscription`、訊息「已經訂閱了這個來源。」，回應帶既有的 `subscription_id`。`url` 缺少或為空字串時回 422、`errors[].path` 為 `url`、訊息「請提供要訂閱的網址。」；`profile` 不存在時回 422，`errors[].path` 為 `profile`。 |
| SUB-R2 | 探索來源（ADR-0021 第 2 點；`discover` 是訂閱層的排程工作 `subscription.check`，不是 `pipeline.Stages` 的成員）：YouTube 頻道每次檢查讀 RSS `https://www.youtube.com/feeds/videos.xml?channel_id=<ID>`（免費，最新約 15 部）；回補（SUB-R3）只在需要時呼叫一次 `yt-dlp --flat-playlist -J --playlist-end <上限> https://www.youtube.com/channel/<ID>/videos`。播放清單與 Bilibili 合集每次檢查都用 `yt-dlp --flat-playlist -J --playlist-end <上限>`（經 `internal/toolexec`，只用 YouTube 與 Bilibili 的 extractor，GRD-R6）。Podcast feed 每次檢查讀 feed 本身。M4 不使用 Supadata `channel/videos`。過濾需要列表沒有提供的欄位（片長、直播狀態）時，對每個**新**條目呼叫一次 `yt-dlp -J --skip-download` 補齊，結果存在 `subscription_entries`，之後不再查詢。探索到的每個條目（含被略過的）都寫入 `subscription_entries`（`decision`：`created`、`skipped`、`deferred`，加上 `reason`）。 |
| SUB-R3 | 回補範圍 `backfill: {mode, count?}`，預設 `{mode: none}`。「新內容」的判斷：頻道（`kind: channel`）是發布時間不早於訂閱的 `created_at`；播放清單、合集與 feed 的發布時間不可靠（加入清單的時間和發布時間無關），改為「第一次成功檢查時（基準清單）沒有出現的條目」。新內容以 `subscription` 優先級建立；回補的舊內容以 `backfill` 優先級建立（`internal/queue` 的 `PrioritySubscription`、`PriorityBackfill`）。`none`：舊內容一律 `skipped`（`before_subscription`）。`recent`：`count` 是 1～200 的整數，取發布時間最新的 `count` 部（沒有發布時間時依列表順序：頻道與 feed 的前 `count` 個、播放清單與合集的最後 `count` 個），其餘 `skipped`（`backfill_limit`）。`all`：最多 `subscriptions.backfill_max` 部（設定檔，預設 500，上限 5000），超過的 `skipped`（`backfill_limit`）。節流：每次檢查最多建立 `subscriptions.backfill_per_check` 部回補影音（設定檔，預設 50），其餘記為 `deferred`（`backfill_per_check`），在之後的檢查依序建立。`mode` 不是這三個值時回 422、`errors[].path` 為 `backfill.mode`；`count` 不在範圍內時 `errors[].path` 為 `backfill.count`、訊息「最近幾部必須是 1 到 200 之間的整數。」。`PATCH` 修改 `backfill` 時，下一次檢查重新評估先前因 `before_subscription`、`backfill_limit` 略過的條目，只評估一次。 |
| SUB-R4 | 過濾 `filters: {min_duration_s?, max_duration_s?, exclude_shorts, live, title_include?, title_exclude?}`。預設 `exclude_shorts: true`、`live: wait`，其他不設。片長以秒計，`min_duration_s` 必須小於 `max_duration_s`，不符時 422、`errors[].path` 為 `filters.max_duration_s`、訊息「最長片長必須大於最短片長。」。Shorts 的判斷：探索得到的網址路徑是 `/shorts/<ID>`。標題 regex 用 Go `regexp`（RE2 語法，沒有回溯，時間與輸入長度成線性），比對 SUB-R11 清理後的標題；每個 regex 最多 200 個字元，超過時 422、訊息「標題的正規表示式最長 200 個字元。」；語法錯誤時 422、訊息「標題的正規表示式無效。」，兩者的 `errors[].path` 都指到該欄位（例如 `filters.title_exclude`）。條目依序檢查，取第一個成立的原因：`tombstoned`、`in_trash`、`already_in_library`（SUB-R5）→ `live`（`live: skip` 時）→ `shorts` → `too_short` → `too_long` → `title_excluded`（符合 `title_exclude`）→ `title_not_included`（設了 `title_include` 但不符合）→ 回補範圍的原因（SUB-R3）。修改 `filters` 不回頭移除已建立的影音，也不重新評估已略過的條目。 |
| SUB-R5 | 不加回、不重複：有 tombstone 的條目（DEL-R11，比對 `platform`、`external_id`、`part`）記為 `skipped`（`tombstoned`），不建立影音也不移除 tombstone；在垃圾桶中的記為 `skipped`（`in_trash`），不還原；媒體庫中已有同一部影音（手動新增或其他訂閱建立的）記為 `skipped`（`already_in_library`），不改變它的 `subscription_id`。同一個訂閱中，`subscription_entries` 以 `(subscription_id, platform, external_id, part)` 為唯一鍵，已經記錄過的條目在之後的檢查不再評估（`deferred` 與 SUB-R3 的 `PATCH` 例外）。Bilibili 合集的條目是一個 BV 號，建立時依 ING 的規則展開成全部分P。 |
| SUB-R6 | 試跑預覽：`POST /api/subscriptions/:id/preview`（body 可帶尚未儲存的 `backfill`、`filters`，覆蓋已存的設定）與 `POST /api/subscriptions/preview {url, backfill?, filters?}`（還沒建立的訂閱）。回 200 `{would_process: [...], would_skip: [...]}`，每列 `{ref, title, published_at, duration_s}`，`would_skip` 另有 `reason`（同 SUB-R4 的順序與原因）；兩個清單都依發布時間由新到舊、最多各 200 列，超過時 `truncated: true`。預覽用和檢查相同的探索與過濾，但不建立影音、不寫入 `subscription_entries`、不建立基準清單、不改變排程；預覽中「新內容」以「現在」代替 `created_at`，所以 `none` 模式下列表中的條目都是 `before_subscription`。預覽的對外請求一樣受 SUB-R10 的 rate limit 限制，沒有額度時回 429、code `rate_limited`、訊息「這個平台的請求太頻繁，請稍後再試。」，回應帶 `retry_after_s`。 |
| SUB-R7 | 匯入：`POST /api/subscriptions/import`（multipart 的 `file` 欄位），依內容判斷格式：OPML（根元素 `opml`，每個有 `xmlUrl` 的 `outline` 是一個 Podcast feed，`text` 或 `title` 當暫定標題）與 Google Takeout 的 `subscriptions.csv`（表頭 `Channel Id,Channel Url,Channel Title`，每列是一個 YouTube 頻道）。不需要 OAuth，匯入時不連外。檔案上限 1 MiB，超過時 413、code `too_large`、訊息「匯入檔超過大小上限（1 MiB）。」；最多 500 個訂閱，超過時 422、code `too_many`、訊息「匯入檔最多 500 個訂閱，這個檔案有 <n> 個。」；格式無法辨識、XML 含 `<!DOCTYPE`、CSV 表頭不符時 422、code `invalid_format`、訊息「無法辨識匯入檔的格式。支援 OPML 與 Google Takeout 的 subscriptions.csv。」。上述錯誤都不建立任何訂閱。檔案內重複、或和既有訂閱相同（SUB-R1 的唯一鍵）的只建立一次，列在 `duplicates`；單列無法辨識（例如 OPML 的 `xmlUrl` 不是 `http`／`https`）的列在 `failed: [{line, url, message}]`，不影響其他列。回 200 `{created, duplicates: [url], failed: [...]}`。匯入的訂閱一律 `backfill: none`、使用預設處理方案與預設過濾，避免一次匯入幾百個頻道的回補花費。 |
| SUB-R8 | 直播與首播：`filters.live: wait`（預設）時，直播中（`is_live`）與尚未開始的直播或首播（`is_upcoming`）照常建立影音，由 `metadata` 依 SRC-R7 等到有 VOD 才繼續，不算失敗、不消耗重試次數；`post_live`（直播結束、VOD 還在處理）同樣等待。`live: skip` 時 `is_live`、`is_upcoming`、`post_live`、`was_live`（直播回放）都記為 `skipped`（`live`）。`live` 不是這兩個值時 422、`errors[].path` 為 `filters.live`。 |
| SUB-R9 | 排程（ADR-0021 第 1 點）：`schedule` 是 Go duration 字串，介於 `1h` 與 `168h` 之間，預設 `6h`，不符時 422、`errors[].path` 為 `schedule`、訊息「檢查頻率必須介於 1 小時到 7 天之間。」。建立後第一次檢查排在 `created_at`（下一輪排程器就執行，不加延遲）；之後的檢查排在「這次執行的時間＋`schedule`＋jitter」，jitter 依 ADR-0021 第 1 點以 `sha256(subscription.check | 訂閱 id | 時段序號)` 換算到 `[0, min(schedule × 10%, scheduler.jitter_max))`。停機或重啟後過期的檢查只補跑一次。檢查失敗依 ADR-0021 退避（1 分鐘、5 分鐘、30 分鐘，之後固定 6 小時），成功後 `failures` 歸零。`POST /api/subscriptions/:id/check` 把下一次檢查改成現在（下一輪排程器執行，不在請求中同步探索），回 202 `{next_check_at}`。同一輪有多個到期的檢查時，依 `next_run_at`、再依訂閱的建立順序（`created_at` 相同時依寫入資料庫的順序，例如 SQLite 的 rowid）執行（rate limit 用完時，排在後面的先延後，SUB-R10）。`enabled: false` 的訂閱不排程。`GET /api/subscriptions/:id` 回傳 `last_checked_at`、`next_check_at`、`last_error`、`last_error_class`。 |
| SUB-R10 | rate limit：探索的對外請求（RSS、yt-dlp 的列表與補齊呼叫）依平台經過 token bucket（以注入的 clock 計時）：`youtube` 每分鐘 30 個、`bilibili` 每分鐘 6 個、Podcast feed 每個主機每分鐘 10 個；容量等於每分鐘的數量。數值由設定檔的 `subscriptions.rate_limits.{youtube, bilibili, feed_host}` 調整（格式 `<n>/m`，n 為 1～600），不能從 API 修改。額度用完時這次檢查停在那裡：已處理的條目保留，排程工作的下一次執行改成額度恢復的時間，不算失敗（`failures` 不變、`last_error` 不變）。平台回 429（RSS）或 yt-dlp 回報 `HTTP Error 429`、Bilibili 回 412 時，該平台的 bucket 清空並暫停 15 分鐘；這次檢查依 SUB-R12 記為 `retryable`，下一次檢查取「SUB-R9 的退避時間」與「bucket 恢復的時間」中較晚的一個（第一次 429 的下一次檢查是 15 分鐘後）。pipeline 本身的 yt-dlp 呼叫（`metadata`、`transcript`）不經過這些 bucket。 |
| SUB-R11 | feed 與探索結果是不可信的輸入：所有 HTTP 請求（YouTube RSS、Podcast feed）走 netguard 的 client（GRD-R1～R5）：私有網段與 DNS rebinding 一律封鎖（設定檔 `network.allow_private` 的 allowlist 例外，GRD-R3），回應上限 10 MiB，逾時 30 秒，最多轉址 5 次。XML 含 `<!DOCTYPE` 時整份拒絕（不展開任何 entity），元素巢狀深度上限 64。每次列表最多採用 1000 個條目（依文件順序，其餘忽略）；yt-dlp 的輸出上限 10 MiB。條目的標題與說明：解碼 HTML entity 一次，移除 `<script>`、`<style>` 元素與其內容，再移除其他標籤（保留文字），移除控制字元與雙向控制字元（U+202A～U+202E、U+2066～U+2069），連續空白合併成一個並去掉前後空白，最長 300 個字元（超過時截斷並加上「…」）。條目的網址與 enclosure 不是 `http`／`https` 時，略過這個條目（不寫入 `subscription_entries`）。 |
| SUB-R12 | 檢查失敗的分類，寫入 `last_error`（訊息）與 `last_error_class`，並發出事件 `subscription.check_failed {subscription, class, message}`；成功時清除兩者並發出 `subscription.checked {subscription, created, skipped}`：連線錯誤、逾時、5xx、429 → `retryable`，訊息「暫時無法檢查訂閱（HTTP 503），稍後會自動重試。」（連線錯誤時括號內是「連線失敗」），依 SUB-R9 退避；feed 回 404 或 410 → `permanent`，訊息「訂閱來源已不存在（HTTP 404）。請確認網址，或刪除這個訂閱。」；yt-dlp 回報頻道、播放清單或合集不存在或不公開 → `permanent`，訊息「找不到這個頻道、播放清單或合集，或它不是公開的。」；yt-dlp 回報 `HTTP Error 403` → `needs_action`，訊息「YouTube 拒絕了訂閱的檢查請求（403）。先更新 yt-dlp 再重試；如果仍然失敗，上傳 YouTube cookie。」；內容不是有效的 RSS 或 Atom（含 SUB-R11 的 `<!DOCTYPE`）→ `permanent`，訊息「訂閱來源的內容不是有效的 RSS 或 Atom。」；超過大小上限、指向內部網路 → `permanent`，訊息沿用 GRD-R5、GRD-R2。`permanent` 與 `needs_action` 不退避，下一次檢查照 `schedule` 排定（feed 可能恢復）。第一次檢查把 `@handle` 換成頻道 ID 後，若已有同一個頻道的訂閱，這個訂閱改成 `enabled: false`，`last_error_class` 為 `permanent`，訊息「已經訂閱了這個來源。」。錯誤訊息與 log 不含 yt-dlp 的完整輸出、cookie 或 feed 的內容。 |
| SUB-R13 | 介面與 scope：`GET /api/subscriptions`、`GET /api/subscriptions/:id`、`GET /api/subscriptions/:id/entries` 需要 `read`；`POST`、`PATCH /api/subscriptions/:id`、`DELETE /api/subscriptions/:id`、`preview`、`check`、`import` 需要 owner 的 session 或 `admin`（預覽與檢查會連外，匯入會建立大量物件）。`DELETE` 回 204，刪除訂閱與它的 `subscription_entries`、`scheduled_jobs`，已建立的影音保留，`subscription_id` 改為空。MCP：`list_subscriptions`（`read`，回傳與 `GET /api/subscriptions` 相同的 `subscriptions` 陣列）、`create_subscription`（`admin`，參數與 `POST /api/subscriptions` 相同，錯誤的 code 與訊息也相同）；MCP 不提供修改與刪除。CLI：`notesteme subs ls [--json]`、`subs add <url> [--profile p] [--schedule 6h] [--backfill none\|recent:<N>\|all] [--json]`、`subs show <id> [--json]`、`subs preview <id\|url> [--json]`、`subs check <id>`、`subs rm <id> [--yes]`、`subs import <file> [--json]`；與 API 的規則與錯誤相同，`rm` 沒有 `--yes` 又不在終端機上執行時結束碼為 2（同 DEL-R13）。由訂閱建立的影音在 `GET /api/items/:id` 帶 `subscription_id`。 |

## 介面

- API：
  - `GET /api/subscriptions`（`read`）：`{subscriptions: [...]}`，依 `created_at` 排序。
  - `POST /api/subscriptions`（`admin`）、`GET/PATCH/DELETE /api/subscriptions/:id`
  - `GET /api/subscriptions/:id/entries?decision=&limit=`（`read`）：`{entries: [{ref, platform, external_id, part, title, published_at, duration_s, live_status, decision, reason, item_id, seen_at}]}`，依 `seen_at`、再依發布時間由新到舊。
  - `POST /api/subscriptions/:id/preview`、`POST /api/subscriptions/preview`、`POST /api/subscriptions/:id/check`、`POST /api/subscriptions/import`（`admin`）
- 訂閱的欄位：`id`、`kind`（`channel`、`playlist`、`collection`、`feed`）、`platform`（`youtube`、`bilibili`、`podcast`）、`external_id`、`url`、`title`（第一次檢查時從來源取得，匯入時先用檔案中的標題）、`profile`、`overrides`、`schedule`、`backfill`、`filters`、`enabled`、`created_at`、`last_checked_at`、`next_check_at`、`last_error`、`last_error_class`。
- MCP：`list_subscriptions`（`read`）、`create_subscription`（`admin`）
- CLI：見 SUB-R13
- 設定檔（只能寫在設定檔）：`scheduler.jitter_max`（ADR-0021，預設 `30m`）、`subscriptions.backfill_max`（500）、`subscriptions.backfill_per_check`（50）、`subscriptions.rate_limits.{youtube, bilibili, feed_host}`（`30/m`、`6/m`、`10/m`）
- 資料表（ADR-0021 第 7 點）：`subscriptions`（plan 第 21 節的欄位，加上 `next_check_at`、`last_error`、`last_error_class`、`baseline_at`）、`subscription_entries`、`items.subscription_id`；排程在 `scheduled_jobs`（`kind = subscription.check`，`key` 是訂閱 id）
- 事件：`subscription.created`、`subscription.checked`、`subscription.check_failed`、`subscription.deleted`
- 資源類別：探索在排程器內同步執行，不占用任務的資源類別（ADR-0021 第 6 點）；建立的影音依一般的步驟使用各自的資源類別
- `pipeline.Stages`：不變（`discover` 不是影音的步驟）

## 與其他規格的關係

- ING：條目以既有的 ingest 服務建立（Bilibili 展開分P、Podcast 的 feed key 與頻道，ING-R4、ADR-0017）；`POST /api/items` 收到頻道網址時的 422「這是頻道。要追蹤新內容，請建立訂閱。」（ING-R5）不變，UI 在這時提供「建立訂閱」。
- DEL：tombstone 與垃圾桶（DEL-R10、DEL-R11）在 SUB-R5 檢查；刪除訂閱不刪除影音。
- SRC：直播與首播的等待沿用 SRC-R7；SRC-10（SRC-R18 起）對訂閱建立的影音一樣適用。
- DIG：訂閱的新內容可以先跑 `summary` 並彙整（DIG 規格）；SUB 只負責建立影音與套用訂閱的處理方案。
- NOTIFY：`subscription.check_failed` 是否通知、無人值守告警的門檻由 NOTIFY 與 OPS 規格決定。
- GRD：SUB-R11 的對外請求全部走 netguard；yt-dlp 的 extractor 限制沿用 GRD-R6。
- ADR-0011：同一個頻道只在一個實例上訂閱，新增訂閱時 UI 顯示提醒，產品不偵測其他實例。

## 不在範圍內

- Supadata `channel/videos`（扣 credit，RSS 加 yt-dlp 已足夠，見 S2）。
- Bilibili UP 主的整個空間（`space.bilibili.com/<mid>/video`）、自架 RSSHub（見 S3）。
- 需要登入才看得到的清單（會員限定、私人播放清單）與 YouTube 的 OAuth 訂閱同步。
- 「探索任務」進佇列（ADR-0021 考慮過的選項）。
- 多實例之間的訂閱協調（ADR-0011）。

## 待決問題

- S1：「新內容」的判準。ADR-0021 第 2 點以「發布時間在訂閱建立之後」區分 `subscription` 與 `backfill` 優先級；播放清單、合集與 Podcast 的發布時間和「什麼時候出現在清單中」無關，Podcast 的 `pubDate` 也常常不可靠。決定（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：頻道用發布時間，其他三種用「第一次成功檢查的基準清單」（SUB-R3）。這是對 ADR-0021 第 2 點的補充，ADR 追認時一併確認。
- S2：Supadata `channel/videos` 不用。RSS 只有最新約 15 部，對「只抓新的」已經足夠（檢查間隔最短 1 小時）；回補用 `yt-dlp --flat-playlist`。Supadata 只回傳 ID 又扣 credit，之後 RSS 被封鎖時再考慮（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）。
- S3：Bilibili 只支援合集（season），不支援 UP 主的整個空間與「系列」（series）。plan 第 13 節寫「space 頁或合集」；space 頁的 yt-dlp 抓取容易觸發風控（412），先只做合集，space 頁回 `unsupported_url`（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）。
- S4：建立訂閱時不連外（SUB-R1），`@handle` 在第一次檢查才換成頻道 ID，所以 `@handle` 與 `/channel/<ID>` 的重複要到第一次檢查才發現（SUB-R12 停用後建立的那個）。代價是建立時看不到標題、也不會立刻發現打錯的網址（第一次檢查才失敗）；好處是建立與匯入都不受 rate limit 影響，也不在請求中等待外部服務（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）。
- S5：過濾與回補的數值（`recent` 最多 200、`backfill_max` 500、`backfill_per_check` 50、rate limit 30／6／10 每分鐘）是保守預設，無人值守運作一週（M4 驗收）後依實際被限流的情況調整；相關場景用設定檔把數值調小。
- S6：Shorts 只以探索得到的網址（`/shorts/<ID>`）判斷。YouTube RSS 對 Shorts 的 `link` 是 `/shorts/`，頻道的 `/videos` 分頁本來就不含 Shorts；播放清單中的 Shorts 用 `/watch?v=` 網址，無法辨識，會被當成一般影片。不用片長推測（Shorts 最長 3 分鐘，和一般短片重疊）（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）。
- S7：`live: wait` 直接建立影音、由 `metadata` 等待（SRC-R7），影音在等待期間會出現在媒體庫中。另一種做法是在 `subscription_entries` 等到 VOD 才建立影音，但那要在探索中重複查詢直播狀態並增加 rate limit 的負擔。等待超過多久放棄，由 SRC-R7 決定，不在 SUB 另訂。
- S8：修改 `filters` 不重新評估已略過的條目（SUB-R4）。想要「放寬過濾後補抓」時，改 `backfill`（SUB-R3 的 `PATCH` 會重新評估回補範圍的條目，但不會重新評估被過濾略過的條目）。之後有需要再加「重新評估全部」的動作。
- S9：UI 第 5 節的線框在「只抓之後的新內容」時仍顯示「會處理 8 部」。依 SUB-R6，預覽在 `none` 模式下會把列表中的舊內容列為 `before_subscription`；線框要在 UI 切片時改成「之後的新內容才會處理；目前列表中的 12 部都不會處理」之類的說明，並讓使用者切換回補範圍時即時更新。
- S10：jitter 的確定性（同一個訂閱、同一個時段一定得到同一個延遲）與 30 秒的單輪上限由 `internal/app` 的單元測試把關；驗收場景只驗證延遲落在範圍內，以及 `scheduler.jitter_max: 0s` 時的精確時間。
- M4-S0：`subscriptions` 原本列在 SEC-R2 的「由資料庫管理」的頂層欄位，現在是設定檔區段（`backfill_max`、`backfill_per_check`、`rate_limits`；訂閱本身仍由 API 管理）（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）。`backfill_max`、`backfill_per_check` 必須是正整數（S0 不檢查兩者的大小關係）；`rate_limits.<平台>` 的格式 `<n>/m`、n 為 1～600（SUB-R10）。`scheduler.jitter_max` 用 Go 的期間寫法，可以是 `0s`。排程器的接點在 `internal/scheduler`（不是 S10 寫的 `internal/app`）：jitter 的確定性與 30 秒的單輪上限由 `internal/scheduler` 的單元測試把關。
- M4-S2（訂閱核心）的切法（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：S2 實作 SUB-R1～R5、R8～R12 與 R13 的 REST 路由；S3 實作 R6 預覽、R7 匯入與 R13 的 MCP、CLI。`POST /api/subscriptions/preview`、`/{id}/preview`、`/import` 仍是 501 stub（scope 已是 `admin`，所以 R13 的 scope 場景在 S2 通過）。R8 放在 S2：`live: wait` 只是照常建立影音、由 SRC-R7 的 `metadata` 等待，`live: skip` 是過濾鏈的一環，兩者都不需要 S3 的功能。閘門 `the YouTube feeds are served from the registered videos` 只對「所有 `@SUB-R*` 都屬於上述範圍、且沒有 `@mcp`／`@cli`」的場景放行；只有 `@SUB-01` 的其他領域場景（DIG 等）照舊停在閘門。
- M4-S2 實作細節（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：
  - 下一次檢查的唯一來源是 `scheduled_jobs.next_run_at`（`subscription.check` 的 `Kind.Interval` 為 0，handler 自己傳回 `Next`）；成功、`permanent`、`needs_action` 都是「這次執行的時間＋schedule＋jitter」（不退避），`retryable` 回錯誤讓排程器退避，429／412 時下一次不早於 bucket 恢復（ADR-0021 修訂的 `RetryAfter`）。額度不足是 `Deferred`，不連線、不更新 `last_checked_at`。
  - rate limit 的 bucket 在記憶體中（容量＝每分鐘的額度、重啟後重新計算）；key 是 `youtube`、`bilibili`、`feed:<host>`。429／412 時清空並暫停 15 分鐘，恢復時只有一顆額度。
  - 每次 feed 檢查都帶 `Connection: close`，不重用連線池裡的連線，所以每次檢查都重新解析 DNS 並經過 netguard 的位址檢查（SUB-R11 的 DNS rebinding）。
  - 404／410 是「訂閱來源已不存在（HTTP n）…」；其他 4xx（408、429 除外）是 `permanent`，訊息「訂閱來源回應了錯誤（HTTP n）。請確認網址，或刪除這個訂閱。」；408 與 5xx 是 `retryable`。
  - Bilibili 合集的條目以第 1 個分P 記錄（代號 `bili:<BV>/p1`），新增時用不帶 `?p=` 的 BV 網址，分P 由 ingest 展開（ING）；yt-dlp 以舊格式網址 `space.bilibili.com/<mid>/channel/collectiondetail?sid=<sid>` 與 extractor `BilibiliCollectionList` 列出。YouTube 用 `youtube:tab,youtube:playlist`。這兩組 extractor 名稱只在假 yt-dlp 驗證過，**要用真的 yt-dlp 實測**。
  - 播放清單、合集一次最多列 1000 筆（`--playlist-end 1000`，與 feed 的條目上限相同）；頻道平常只讀 RSS；第一次檢查（或修改 `backfill` 之後）且回補範圍不是 `none` 時，另以 `/channel/<ID>/videos` 的 flat 列表取得 RSS 以外的舊影片（`--playlist-end` 取回補上限）。`@handle` 以 `yt-dlp -J https://www.youtube.com/@handle --playlist-end 1` 換成頻道 ID。
  - 過濾需要片長或直播狀態、但列表沒有提供時，以 yt-dlp `-J` 查單一影片；查不到時記為 `deferred`（`details_unavailable`），新增失敗時記為 `deferred`（`ingest_failed`），下一次檢查再試，不影響其他條目、不算檢查失敗。兩個原因不在 SUB-R3～R5 的清單中，是內部的暫時狀態。
  - `PATCH` 修改 `backfill` 時，下一次檢查重新評估因回補範圍略過（`before_subscription`、`backfill_limit`）的條目，已建立的影音不變（S8）。
  - 由訂閱建立的影音記下 `items.subscription_id`；刪除訂閱時清成 null（影音保留，SUB-R13）。訂閱新增不清除 tombstone（使用者刪除過的影片不會因為訂閱又回來，SUB-R5）。
  - 409 的 `subscription_id` 加在共用的錯誤格式 `apierr.Error`（前例是 M5 的 `required_permission`），所以 Hub 的 OpenAPI 快照（`internal/hub/hubapi/openapi.json`）的 `Error` 也多了這個 Client 才會用到的選用欄位；Hub 不會填它。要分開的話，改成 Client 自己的錯誤型別。
  - `GET /api/items` 的 `Item` 與詳情一律帶 `subscription_id`（可為 null，SUB-R13），web 的測試資料一併補上。
  - 網址正規化之後必須能再辨識成同一個網址才接受（fuzz 找到 `http://0::0` 會被正規化成無法解析的主機）。
- 場景修改（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：SUB-R4「Invalid filters are rejected with the field and the reason」第 4 個範例的 `title_include` 原本只有 195 個 `a`，達不到規則的「超過 200 個字元」，改成 201 個 `a`；斷言不變。


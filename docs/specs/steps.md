# 步驟詞彙

> 狀態：草案 v0.1（2026-11-12）

驗收場景只能使用這份文件列出的步驟。需要新步驟時，先加到這裡並說明意思，再寫進場景。步驟實作放在 `features/steps/`，依本文件的分組拆檔。

## 1. 領域用語

| English | 繁中 | 說明 |
|---|---|---|
| owner | 使用者 | Client 唯一的使用者 |
| agent | Agent | 透過 MCP 領取任務的外部 Agent |
| worker | 內建 worker | `serve` 內執行任務的元件 |
| item | 影音 | 一部影片、一集 Podcast 或一個本機檔案；Bilibili 每個分P 是一個 item |
| stage | 步驟 | 例如 `transcript`、`correct` |
| artifact | 產出 | 步驟產生的資料，有版本與來源紀錄 |
| variant | 變體 | 字幕產出的種類：`source`、`normalized`、`segmented`、`corrected`、`translated`、`edited` |
| provenance | 來源紀錄 | 產出由哪個 provider、model、prompt、參數、上游產出生成 |
| profile | 處理方案 | 可重複套用的 pipeline 設定 |
| overrides | 單次覆寫 | 只影響這一次執行的設定 |
| task | 任務 | 佇列中的執行單位；LLM 步驟的每個 chunk 是一個任務 |
| lease | 租約 | Agent 領取任務後的持有期限 |
| caption track | 字幕軌 | 平台提供的字幕：`manual`、`auto`、`platform-translated` |
| transcript source | 字幕來源 | 平台字幕、Supadata、ASR 等，ID 見第 3 節 |
| quality check | 品質檢查 | 判斷字幕來源的結果能不能用 |
| rejected | 不採用 | 沒有通過品質檢查的來源結果 |
| cue | 字幕句 | 一個有起訖時間的字幕單位 |
| timing origin | 時間來源 | `source`、`merged`、`silence`、`interpolated`、`model` |
| break marker | 斷點標記 | `segment` 的 LLM 輸出，標示在哪裡斷句 |
| sentence group | 句群 | 跨越多個 cue 的一句話，翻譯時的單位 |
| split marker | 切分標記 | 句群譯文中標示各 cue 邊界的記號 |
| glossary | 詞彙表 | 校正與 ASR 使用的專有名詞清單 |
| term candidate | 術語候選 | `correct` 提出、等待使用者核可的詞彙 |
| output target | 輸出目標 | `vault`、`sidecar`、`folder` |
| render | 即時渲染 | 從 cues JSON 產生 SRT、VTT、ASS |
| Needs you | 需要你處理 | 狀態為 `blocked`、原因為 `needs_action` 的項目 |
| scope | 權限範圍 | token 的權限：`read`、`agent`、`ingest`、`admin` |
| inbox | inbox | 放入檔案就會自動新增的本機資料夾 |
| trash | 垃圾桶 | 刪除後、永久刪除前的暫存區，預設保留 30 天 |
| purge | 永久刪除 | 從垃圾桶清除，`/data` 中不留這部影音的內容 |
| tombstone | 刪除紀錄 | 永久刪除後留下的識別資料，避免 inbox 與訂閱把影音加回來 |

步驟狀態：`pending`、`running`、`waiting`、`awaiting_agent`、`blocked`、`done`、`skipped`、`failed`。
`blocked` 的原因：`needs_action`、`budget_blocked`、`awaiting_user`。
錯誤類別：`retryable`、`wait`、`needs_action`、`budget_blocked`、`permanent`。

## 2. 影音的引用方式

場景中用短代號指稱影音，不寫完整網址：

| 代號 | 意思 | 對應網址 |
|---|---|---|
| `yt:<id>` | YouTube 影片 | `https://www.youtube.com/watch?v=<id>` |
| `bili:<bvid>/p<n>` | Bilibili 影片的第 n 個分P | `https://www.bilibili.com/video/<bvid>?p=<n>` |
| `pod:<feed>/<guid>` | Podcast feed 中的一集 | 由 fixture 中的 feed 定義 |
| `local:<path>` | 本機檔案，路徑相對於 inbox | — |

`When the owner ingests` 接受代號或完整網址；完整網址用於驗證網址辨識。

其他代入規則：

- 請求路徑中的代號（例如 `GET /api/items/yt:abc123xxxxx`）會換成實際的影音 ID；`channel-of:<代號>` 換成該影音所屬頻道的 ID。影音在垃圾桶中，或已永久刪除時，代號仍然換成原本的 ID；永久刪除後再新增，換成新的 ID。
- CLI 指令與請求中的 `{token:<名稱>}` 會換成該 token 的實際內容。
- 預期結果的 JSON 中，值為 `"*"` 代表「欄位存在即可，不比對內容」。
- 物件只比對預期中出現的欄位。陣列依序比對：預期的第 n 個元素對應實際的第 n 個元素，實際陣列可以比預期長；要確認數量時，另外用計數的步驟。
- 影音或 fixture 不存在時，`Given the item … has been processed up to …` 依代號從 `features/fixtures/items/` 建立。

## 3. 字幕來源 ID

| ID | 來源 |
|---|---|
| `youtube-manual`、`youtube-auto` | YouTube 人工字幕、自動字幕 |
| `bilibili-cc`、`bilibili-ai` | Bilibili CC 字幕、AI 字幕 |
| `podcast-transcript` | `<podcast:transcript>` |
| `local-sidecar`、`local-embedded` | 本機的外掛字幕檔、內嵌字幕軌 |
| `supadata` | Supadata |
| `groq`、`whisper-1`、`voxtral` | ASR provider（依設定檔中的 ID） |

## 4. 步驟

參數記號：`{string}` 是雙引號字串，`{int}` 是整數，`{word}` 是不含空白的單字。`:` 結尾的步驟後面接 doc string 或表格。

- 數量後面的名詞可以用單數或複數（`1 item`、`2 items`），步驟實作的 regex 都接受。
- 只有 `@manual` 場景可以使用這份清單以外的句子，因為它們不會被自動執行。
- 單次覆寫的 YAML 可以包含 `until: <stage>`，等同 API 的 `until` 欄位。
- 測試環境的預設值（ADR-0015）：`NOTESTEME_PASSWORD`、`NOTESTEME_MASTER_KEY` 都有測試用的值，`PATH` 只含假的外部工具。場景要驗證缺少它們的情況時，用 `Given the environment variable {string} is not set` 移除。
- `Given` 步驟只累積設定。server 還沒啟動時，第一個送出請求的步驟以當時的設定啟動它；`Given the owner is signed in` 也在第一次送出請求時才建立 session。
- 場景明確讓 server 停下之後（`When the server shuts down gracefully`），讀取狀態的 `Then` 步驟不會隱含啟動 server，而是唯讀查詢資料庫；否則新 server 啟動時的租約回收（PIPE-R11）會蓋掉要驗證的結果。下一個送出請求的步驟仍然會啟動 server。
- 「任務正在執行」同時要求資料庫中的租約有效，且對應的外部工具程序仍然存活。
- 場景中的 `/data`、`/config` 等路徑，會換成這個場景專用的暫存目錄。對應規則（虛擬路徑 → 實際路徑）：
  - `/data` → `<場景根目錄>/data`、`/config` → `<場景根目錄>/config`、`/media` → `<場景根目錄>/media`、`/backups` → `<場景根目錄>/backups`。
  - `/tmp/<x>` → `<場景根目錄>/tmp/<x>`：場景中的 `/tmp/...`（例如上傳用的 `/tmp/fansub.ass`）不寫到真正的 `/tmp`。目前只有檔案步驟套用；之後 `the owner uploads "/tmp/..."` 等步驟實作時，也要用同一個對應。
- After hook 的 secret 掃描（NFR-SEC-3）除了 log 與假工具的呼叫紀錄，也涵蓋 `/backups`：其中的 `.tar.gz` 解壓後，逐一檢查每個項目的內容不含 master key 的值（OPS-R1）。
- `Given {string} has {word} captions in {string}` 沒有對應的 fixture 時，產生 120 句的字幕軌。

### 4.1 環境與時間（`steps/env.go`）

| 步驟 | 意思 |
|---|---|
| `Given the clock is at {string}` | fake clock 設為指定的 RFC 3339 時間 |
| `When the clock advances by {string}` | 時間前進，例如 `"2h"`、`"25h"`；到期的排程與租約會在下一次 worker 執行時處理 |
| `Given the config file contains:` | `/config/notesteme.yaml` 的內容（YAML） |
| `Given the environment variable {string} is {string}` | 設定環境變數 |
| `Given the environment variable {string} is not set` | 移除環境變數 |
| `Given the server is running` | 以目前的設定啟動 `serve` |
| `Given the server is not running` | 確認沒有 `serve` 持有資料目錄 |
| `Given the provider {string} has no API key` | 移除 provider 的 API key（fake provider 預設有 key：Supadata 的 `NOTESTEME_SUPADATA_API_KEY`、Groq 的 `NOTESTEME_GROQ_API_KEY`）。參數是字幕來源 ID（第 3 節）時，同時移除環境變數與 fake 的 key 要求，所以產品沒檢查 key 就呼叫來源會拿到轉錄結果、場景失敗；來源還沒實作（`source.Implemented`）時 pending；必須放在 server 啟動之前（環境變數只在啟動時讀取）。LLM provider 的版本還沒實作，pending |
| `When the server starts` | 啟動 `serve`，等到 `/readyz` 回應或啟動失敗 |
| `When the server restarts` | 模擬程序中斷後重新啟動（不經過正常關閉） |
| `When the server shuts down gracefully` | 送出關閉訊號，等待正常關閉 |
| `Then the server fails to start with {string}` | 啟動失敗，錯誤訊息包含指定文字 |

### 4.2 存取（`steps/access.go`）

| 步驟 | 意思 |
|---|---|
| `Given the owner password is {string}` | 設定登入密碼 |
| `Given the owner is signed in` | 建立一個有效的 session |
| `Given a token {string} with scopes {string}` | 建立 token，scope 以逗號分隔，例如 `"read,agent"` |
| `Given the token {string} expired` | 讓 token 過期 |
| `When someone signs in with the password {string}` | `POST /api/auth/login` |
| `When someone signs in from {string} with the password {string}` | 從指定的來源位址送出 `POST /api/auth/login`；測試中用 `127.0.0.0/8` 內的位址 |
| `When the owner sends {string}` | 用 owner 的 session 送出請求，例如 `"GET /api/items"`；非 GET 請求自動帶 CSRF token |
| `When the owner sends {string} with:` | 同上，帶 JSON body |
| `When the owner sends {string} without a CSRF token` | 用 session 送出請求，但不帶 CSRF token |
| `When the client sends {string}` | 不帶任何身分的請求 |
| `When the client sends {string} with the token {string}` | 帶 bearer token 的請求 |
| `When the client sends {string} with the token {string} and:` | 同上，帶 JSON body |
| `When the client sends {string} with the token {string} from the origin {string}` | 帶 bearer token 與 `Origin` 標頭的請求 |
| `When the owner sends {string} with the header {string} set to {string}` | 用 session 送出請求，並加上指定的標頭 |
| `Given the owner is listening to events` | 以 owner 的 session 訂閱 `GET /api/events`（SSE） |
| `Then an event is received:` | 訂閱中收到符合 JSON 子集的事件 |
| `Then the response status is {int}` | HTTP 狀態碼 |
| `Then the response contains:` | 回應的 JSON 包含指定的欄位與值（子集比對） |
| `Then the response header {string} contains {string}` | 回應標頭 |

### 4.3 平台與來源的 fixture（`steps/sources.go`）

| 步驟 | 意思 |
|---|---|
| `Given a YouTube video {string} titled {string}` | 建立 fake 的 YouTube 影片；沒有另外指定時，原文語言為 `en` |
| `Given a YouTube video {string} with:` | 同上，用表格（`field`、`value`）指定 `title`、`duration`、`language`、`availability`、`chapters`、`live_status` 等。`availability` 為 `subscriber_only` 時，假 yt-dlp 在下載字幕與媒體時要求含 `.youtube.com` cookie 的 `--cookies` 檔，否則回報 `ERROR: [youtube] <id>: Join this channel to get access to members-only content like this video, or contact the channel owner.`（取得資訊不失敗，辨識網址的 Probe 仍能看到 `availability`） |
| `Given a Bilibili video {string} with {int} parts` | 建立 fake 的 Bilibili 影片（`1 part` 亦可）；參數是不帶 `bili:` 的 BV 號。各分P 沒有字幕軌、沒有原文語言，標題預設為 BV 號；之後用 `bili:<BV>/p<n>` 逐分P 設定。分P 不存在（或影片還沒建立）時，使用該代號的步驟失敗 |
| `Given a podcast feed {string} with the episodes:` | 建立 fake 的 feed，表格欄位：`guid`、`title`、`audio`；feed 的網址為 `https://feeds.example.com/<名稱>.xml` |
| `Given a local file {string} in the inbox` | 在 inbox 放入 fixture 檔案 |
| `Given {string} has {word} captions in {string}` | 平台字幕軌；`{word}` 為 `manual`、`auto`、`platform-translated`。Bilibili 的 AI 字幕屬於 `auto`（Bilibili 沒有 `platform-translated`） |
| `Given {string} has no caption tracks` | 平台沒有任何字幕 |
| `Given the auto captions of {string} are not ready yet` | YouTube 自動字幕還沒生成：假 yt-dlp 不列出、也不下載自動字幕與平台翻譯字幕，人工字幕不受影響。只接受 `yt:` |
| `Given the source {string} returns {string} for {string}` | 字幕來源回傳指定的 fixture。平台來源（`youtube-*`、`bilibili-*`）寫進假 yt-dlp 對應種類的字幕軌（`*-manual`、`bilibili-cc` 是 `manual`，`*-auto`、`bilibili-ai` 是 `auto`），來源與影音的平台必須相符；Bilibili 的 fixture 是字幕 JSON（`body[{from, to, content}]`） |
| `Given the source {string} has nothing for {string}` | 字幕來源沒有結果 |
| `Given the source {string} requires the cookie {string}` | 需要指定的 cookie 才能取得。平台來源（`bilibili-*`）的參數是 cookie 的平台：假 yt-dlp 沒有收到該平台的有效 cookie 檔（`--cookies` 指到 Netscape 格式、含 `.bilibili.com` 網域 `SESSDATA` 的檔案）時，不列出也不下載該來源的字幕軌，結束碼 0，並在 stderr 印出 login WARNING（有 `--no-warnings` 時不印）。平台來源還沒實作（`source.Implemented`）時 pending |
| `Given the source {string} accepts files up to {string}` | ASR 的單檔大小上限，例如 `"25MB"` |
| `Given the audio of {string} is {string}` | 音訊 fixture，例如長度或大小不同的檔案 |
| `Given yt-dlp fails for {string} with {string}` | yt-dlp 對這個影音的所有操作（取得資訊、下載字幕、下載媒體）都以結束碼 1 失敗，stderr 為指定的錯誤輸出。只接受 `yt:` |

### 4.4 影音與產出（`steps/items.go`）

| 步驟 | 意思 |
|---|---|
| `When the owner resolves {string}` | `POST /api/resolve`，辨識網址 |
| `When the owner ingests {string}` | `POST /api/items`，使用預設的處理方案 |
| `When the owner ingests {string} with the profile {string}` | 指定處理方案 |
| `When the owner ingests {string} with the overrides:` | 帶單次覆寫（YAML） |
| `When the owner uploads {string}` | 透過 API 上傳本機檔案 |
| `When the owner reruns {string} from {string}` | 從指定步驟重新處理 |
| `Given the item {string} has been ingested` | 已新增，worker 還沒執行 |
| `Given the item {string} has been ingested as {word}` | 以指定的優先級新增：`manual`、`subscription`、`backfill` |
| `Given {string} is a mixed-language item` | 把影音的原文語言標記為 `mixed` |
| `Given the item {string} has been processed up to {string}` | 已完成到指定步驟，用 fixture 填入產出 |
| `Given the {string} transcript of {string} is:` | 指定變體的字幕；表格欄位：`#`、`start`、`end`、`text`，可加 `words`、`timing` |
| `Given the {string} transcript of {string} in {string} is:` | 同上，指定語言 |
| `When the worker runs until idle` | worker 持續執行，直到沒有可以執行的任務 |
| `Then the library contains {int} items` | 影音總數，不含垃圾桶中的影音。server 已明確停下（`When the server shuts down gracefully`）時，不隱含啟動 server，改用唯讀方式查詢 DB |
| `Then the library contains {string}` | 影音存在，且不在垃圾桶中。server 已明確停下時，同上，改用唯讀方式查詢 DB |
| `Then the stage {string} of {string} is {string}` | 步驟狀態 |
| `Then the stage {string} of {string} is blocked with the reason {string}` | 狀態為 `blocked`，且 `block_reason` 相符 |
| `Then the stage {string} of {string} failed with the error class {string}` | `failed` 及錯誤類別 |
| `Then the stage {string} of {string} waits until {string}` | 狀態為 `waiting`，`not_before` 與指定的 RFC 3339 時間是同一個時間點（不比時區寫法） |
| `Then the stage {string} of {string} retries at {string}` | `pending`（重試中），下一次嘗試的時間為指定時間 |
| `Then {string} shows the message {string}` | 影音的狀態訊息（UI 與 API 共用的文案）：依 pipeline 的順序，取第一個「不是 `done` 而且有 `message`」的步驟，訊息必須完全相同；沒有這樣的步驟就失敗 |
| `Then {string} offers the actions {string}` | 可以執行的修正動作：同一個步驟（同上）的 `actions`；參數以逗號切開並去掉前後空白，與 `actions` 依序完全相等 |
| `Then the transcript of {string} came from {string}` | 採用的字幕來源 |
| `Then the source {string} was rejected for {string} because {string}` | 品質檢查不通過的原因：影音詳情的 `sources` 中，有一筆 `source` 相符、`state` 為 `rejected`、`reason` 相符 |
| `Then the original language of {string} is {string}` | 原文語言 |
| `Then the original language of {string} is marked as mixed` | 標記為中英夾雜等混合語言 |
| `Then the {string} transcript of {string} is:` | 字幕內容，表格欄位同上；只比對表格中出現的欄位 |
| `Then the {string} transcript of {string} in {string} is:` | 同上，指定語言 |
| `Then the {string} transcript of {string} has {int} cues` | cue 數 |
| `Then the {string} transcript of {string} in {string} has {int} cues` | 同上，指定語言 |
| `Then every cue of the {string} transcript of {string} follows the subtitle rules for {string}` | 每個 cue 都符合指定文字系統（`cjk`、`latin`）的字幕規則（plan 第 8.5 節） |
| `Then every cue of the {string} transcript of {string} has the timing {string}` | 所有 cue 的時間來源（`GET /api/items/:id/cues`）；沒有任何 cue 視為失敗 |
| `Then the {string} artifact of {string} has the provenance:` | 現行產出的來源紀錄（`provenance_json`）；表格欄位：`field`、`value`。`field` 用點號表示巢狀欄位，數字的段落是陣列索引（例如 `tool_versions.yt-dlp`），值一律以字串比較（數字用原本的寫法）。`parents` 是上游產出的變體名稱清單（值以逗號分隔、依序相等，例如 `source`）。符合名稱的現行產出必須剛好一個；harness 經測試用的 hook 讀取，不新增 API |
| `Then the {string} artifact of {string} has {int} versions` | 版本數，包含被取代的版本 |
| `Then the item {string} has no {string} artifact` | 沒有這種產出 |
| `Given the media of {string} is cached` | 媒體快取中已有這部影音下載好的檔案 |
| `Then the trash contains {string}` | 影音在垃圾桶中 |
| `Then the trash does not contain {string}` | 影音不在垃圾桶中 |
| `Then the trash contains {int} items` | 垃圾桶中的影音數 |
| `Given the cost ledger has an entry of {string} USD for {string}` | 成本帳本中有一筆這部影音的實際金額 |
| `Then the cost ledger has an entry of {string} USD titled {string}` | 成本帳本中有一筆這個金額的紀錄，`item_title` 為指定標題 |

### 4.5 Pipeline 與佇列（`steps/queue.go`）

| 步驟 | 意思 |
|---|---|
| `Given the concurrency limit of {string} is {int}` | 資源類別的並行上限 |
| `Given the queue is paused` | 整個佇列暫停 |
| `Given the resource class {string} is paused` | 只暫停一個資源類別 |
| `When the owner pauses the queue` | `POST /api/queue/pause` |
| `When the owner resumes the queue` | `POST /api/queue/resume` |
| `When the worker starts one round` | worker 依優先順序領取一輪任務並開始執行，但不等完成 |
| `Then {int} tasks of {string} are running` | 某資源類別正在執行的任務數 |
| `Then 1 task of {string} is running` | 同上，單數寫法 |
| `Given the implementation version of the stage {string} is {int}` | 模擬步驟的 `impl_version` 改變 |
| `Then the next task belongs to {string}` | 下一個被領取的任務屬於哪個影音 |
| `Then the task of {string} for {string} has {int} attempts` | 任務的嘗試次數；第一個參數是步驟 |
| `Then the source {string} was called {int} times` | 字幕來源被呼叫的次數 |
| `Then no task is running` | 沒有執行中的任務 |

### 4.6 LLM（`steps/llm.go`）

| 步驟 | 意思 |
|---|---|
| `Given the LLM provider {string} answers {string} with {string}` | 指定步驟的請求都回傳這個 fixture |
| `Given the LLM provider {string} answers {string} with {string} and then {string}` | 依序回傳；第二個之後一直回傳最後一個 |
| `Given the LLM provider {string} answers {string} for chunk {int} with {string}` | 只針對某個 chunk |
| `Given the LLM provider {string} fails with HTTP {int}` | 所有請求都回傳指定的 HTTP 錯誤 |
| `Given the LLM provider {string} fails with HTTP {int} once` | 只有第一次失敗 |
| `Then the LLM provider {string} received {int} requests` | 請求總數 |
| `Then the LLM provider {string} received {int} requests for {string}` | 指定步驟的請求數 |
| `Then request {int} to {string} contains {string}` | 第 n 個請求的 prompt 包含指定文字 |
| `Then request {int} to {string} does not contain {string}` | 第 n 個請求的 prompt 不包含指定文字 |
| `Then requests {int} and {int} to {string} share the same prefix before the chunk` | prompt 的固定前綴相同 |
| `Then the {string} chunks of {string} are:` | chunk 切分結果；表格欄位：`chunk`、`cues`、`context` |

### 4.7 Agent 與 MCP（`steps/agent.go`）

| 步驟 | 意思 |
|---|---|
| `Given an agent {string} using the token {string}` | 建立一個 Agent 身分 |
| `When the agent {string} claims up to {int} tasks of kind {string}` | 呼叫 `claim_tasks` |
| `Then the agent {string} holds {int} tasks` | 目前持有的任務數 |
| `When the agent {string} submits {string} for its task {int}` | 對第 n 個持有的任務呼叫 `submit_task`，結果用 fixture |
| `When the agent {string} submits {string} for all its tasks` | 對所有持有的任務依序提交 |
| `Then no task is held by both {string} and {string}` | 兩個 Agent 持有的任務沒有交集 |
| `When the agent {string} extends the lease of its task {int}` | 呼叫 `extend_lease` |
| `When the agent {string} releases its task {int} with the reason {string}` | 呼叫 `release_task` |
| `Then the submission is accepted` | 最近一次提交成功 |
| `Then the submission is rejected with {string}` | 最近一次提交失敗，錯誤包含指定文字 |
| `When the MCP client with the token {string} calls {string} with:` | 呼叫 MCP tool，參數為 JSON |
| `When the MCP client with the token {string} lists the tools` | `tools/list` |
| `When the MCP client with the token {string} lists the prompts` | `prompts/list` |
| `Then the tool result contains:` | tool 結果的 JSON 包含指定內容（子集比對） |
| `Then the tool call fails with {string}` | tool 呼叫失敗，錯誤包含指定文字 |
| `Then the listed names include {string}` | 列出的 tool 或 prompt 名稱包含指定項目，以逗號分隔 |
| `Then the listed names do not include {string}` | 列出的名稱不包含任何一個指定項目 |
| `Then the description of the tool {string} contains {string}` | tool 的說明文字 |

### 4.8 CLI（`steps/cli.go`）

| 步驟 | 意思 |
|---|---|
| `When the owner runs {string}` | 執行 CLI，例如 `"notesteme items ls --json"`。只執行 `notesteme`；`docker …` 目前為 pending（OPS 切片處理）；不會隱含啟動 server |
| `Then the command exits with {int}` | 結束碼。指令回報 `not_implemented`（尚未實作的子命令，或 API 回 501）且結果來自前一個步驟時為 pending |
| `Then the command output contains {string}` | stdout 或 stderr 包含指定文字 |
| `Then the command output is JSON containing:` | stdout 是 JSON，且包含指定內容 |
| `Then the command ran in {word} mode` | `standalone` 或 `client` |

### 4.9 檔案（`steps/files.go`）

| 步驟 | 意思 |
|---|---|
| `Given the file {string} contains:` | 建立檔案：內容照字面寫入（doc string 的內容，不解析也不轉換）；上層目錄不存在時自動建立（例如 `/backups/old.tar.gz` 的 `/backups`） |
| `Then the file {string} exists` | 檔案存在 |
| `Then the file {string} does not exist` | 檔案不存在 |
| `Then the file {string} contains {string}` | 檔案內容包含指定文字。`.tar.gz` 先 gunzip，再逐一比對每個 tar 項目的內容（檔名不算），任何一個項目包含就成立；其他壓縮檔照原本的方式解開 |
| `Then the file {string} does not contain {string}` | 檔案內容不包含指定文字。`.tar.gz` 先 gunzip，再逐一比對每個 tar 項目的內容，所有項目都不包含才成立；其他壓縮檔照原本的方式解開 |
| `Then the data directory does not contain the created token` | `/data` 中不含最近一次建立的 token 明文 |
| `Then the file {string} is:` | 檔案內容完全相同 |
| `Then the file {string} has the mode {string}` | 檔案權限，例如 `"0600"` |
| `Then the cookie file given to yt-dlp has the mode {string}` | 目前執行中、帶 `--cookies` 的假 yt-dlp 呼叫所收到的 cookie 檔存在，且權限相符。暫存檔每次呼叫一個檔名（SEC-R7），所以不寫死路徑。「執行中」以假 yt-dlp 記錄的程序 ID 判斷（被 gate 擋住的程序仍存活）；沒有這樣的呼叫時失敗 |
| `Then no cookie file remains in {string}` | 曾交給假 yt-dlp 的 cookie 檔都已不存在，其中至少一個放在該目錄（場景路徑對應到測試資料目錄），而且該目錄中沒有名稱含 `cookie` 的檔案（cookie 暫存檔的命名）。沒有帶 `--cookies` 的呼叫時失敗：沒有東西可以驗證 |
| `Then the data directory does not contain {string}` | `/data` 中所有檔案（包括 DB）都不含這段文字 |
| `Then the data directory has no files of {string}` | `/data` 的 artifacts、media、uploads、tmp 中沒有屬於這部影音的檔案 |
| `Then the media cache has no files of {string}` | `/data/media` 中沒有屬於這部影音的檔案 |
| `Then the logs do not contain {string}` | log 中不含這段文字 |

### 4.10 網路（`steps/network.go`）

| 步驟 | 意思 |
|---|---|
| `Given the host {string} resolves to {string}` | fake DNS |
| `Given the host {string} resolves to {string} and then {string}` | 第二次之後解析成不同的 IP，用來模擬 DNS rebinding |
| `Given the URL {string} redirects to {string}` | fake origin 回傳轉址 |
| `Given the URL {string} serves {string}` | fake origin 回傳 fixture（`features/fixtures/http/`）。YouTube 與 Bilibili 的網址由 yt-dlp 處理、不是 HTTP 請求：播放清單網址（`/playlist?list=…`）改為登記 `features/fixtures/ytdlp/` 的 fixture 作為假 yt-dlp `--flat-playlist -J` 的輸出（內容是 yt-dlp 的 JSON，`entries` 中每部影片有 11 字元的 `id`）；其他 YouTube、Bilibili 網址不支援這個步驟 |
| `Given the URL {string} serves {int} bytes` | fake origin 回傳指定大小的內容 |
| `Then no connection was made to {string}` | 沒有對指定的 IP 或主機建立連線 |

### 4.11 Prompt 與處理方案（`steps/config.go`）

| 步驟 | 意思 |
|---|---|
| `Given the prompt {string} version {int} is:` | 建立 prompt 的某個版本 |
| `Given the default prompt for {string} is {string}` | 某步驟的預設 prompt |
| `Given the profile {string} is:` | 建立處理方案（YAML） |
| `Given the default profile is {string}` | 設定預設處理方案 |
| `Given the global settings are:` | 全域設定（YAML） |
| `Given the channel of {string} has the overrides:` | 頻道層的覆寫（YAML） |
| `Then the effective config of {string} for {string} contains:` | 某步驟最後生效的設定（YAML 子集比對）；第一個參數是影音，第二個是步驟 |

### 4.12 維運（`steps/ops.go`）

| 步驟 | 意思 |
|---|---|
| `Given the data directory was created by schema version {int}` | 用指定 schema 版本建立 `/data` 的 DB：harness 以 `store.MigrationsFS()` 加上 goose 的 `UpTo(N)` 建立；N 比內建的最新版本大時，補上合成的 migration 讓版本到 N |
| `Given the current schema version is {int}` | 程式內建的最新 schema 版本是 N：透過 `TestControls.Migrations` 只提供版本 1 到 N 的 migration，之後啟動的 server 都用這份 |
| `Given migration {int} fails` | 第 N 支 migration 會失敗：以一支 `-- +goose NO TRANSACTION` 的 migration 取代第 N 支，先做一部分變更（建立一個 marker 表）再失敗，讓「失敗時只還原快照才會乾淨」可以被驗證 |
| `Then the database is at schema version {int}` | DB 目前的 schema 版本：goose 版本表的最新版本是 N，而且 `sqlite_master` 的 schema 與「全新 DB 跑到 N」的參考 schema 完全相同，所以失敗 migration 留下的 marker 表也會被發現 |
| `Given yt-dlp {string} is installed` | 在 OPS 規格規定的目錄結構中建立假的 venv：`/data/tools/yt-dlp/<版本>/` 是 venv，`/data/tools/yt-dlp-venv` 是指向它的 symlink；venv 內的假 yt-dlp 與假 python 回報該版本 |
| `Given the yt-dlp release {string} passes the self-check` | 可更新到的版本，且自我檢查會通過。假 python 支援 `-m venv`、`-m pip install yt-dlp[default]==V` 與 `-U`（裝出來的 venv 回報版本 V）；不連外網 |
| `Given the yt-dlp release {string} fails the self-check` | 可更新到的版本，但自我檢查會失敗：只有對「檢查影片」的 `Probe` 失敗，`--version` 照常成功（要驗證的是 Probe 的那一步）。安裝的支援同上 |
| `Then yt-dlp {string} is the active version` | 目前使用中的 yt-dlp：`/data/tools/yt-dlp-venv` 解析後指向的 venv，其中的 yt-dlp 回報這個版本（驗證更新真的切換、回退真的沒有切換） |
| `Given an event from {string} exists` | 建立一筆指定時間的事件 |
| `Then no event older than {int} days remains` | 事件都在保留期限內 |
| `Then the number of events is {int}` | 持久化的事件筆數（包括場景中用 `an event from … exists` 建立的）。server 已明確停下時改用唯讀方式查詢 DB |

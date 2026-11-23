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
| sponsor segment | 贊助片段 | SponsorBlock 標記的業配、自我宣傳等片段（`sponsor_segments`，M3） |
| chapter | 章節 | 平台提供或 `chapters` 步驟產生的章節（M3） |
| candidate frame | 候選畫面 | `frames` 以場景偵測與密度下限找出、去重後的畫面（M3） |
| key frame | 關鍵畫面 | 選中、會放進筆記的候選畫面（M3） |
| contact sheet | 拼圖 | 送給 vision 模型、加上編號的 3×3 候選畫面（M3） |
| note | 筆記 | `note` 步驟依模板產生的 Markdown（M3） |
| note template | 筆記模板 | 筆記的結構（frontmatter、資訊表、章節骨架），LLM 只寫內文（M3） |
| note check | 自動檢查 | 筆記寫出後的檢查項目（NOTE-R7，M3） |
| vault | vault | 設定檔定義的 Obsidian vault 輸出位置：`fs` 或 `git`（M3） |
| hook | hook | 設定檔定義、發布後執行的命令（M3） |

步驟狀態：`pending`、`running`、`waiting`、`awaiting_agent`、`blocked`、`done`、`skipped`、`failed`。
`blocked` 的原因：`needs_action`、`budget_blocked`、`awaiting_user`。
`skipped` 的原因：`cancelled`、`off`、`not_configured`、`no_outputs`、`same_language`、`not_applicable`（沒有任何輸出目標適用這部影音，例如 sidecar 對線上影音，OUT-R10）、`manual_captions`（來源是人工字幕，segment 預設略過，SEG-R1）；M3 另有 `platform_chapters`（平台已有章節，CHAP-R1）、`audio_only`（沒有影像串流，FRM-R1）、`gemini`（筆記由 Gemini 直接生成，GEM-R2），以及暫時的 `not_implemented`（M3-S0：設定要求執行、但這個 M3 步驟還沒有實作；各切片實作之後就不會再出現）。
錯誤類別：`retryable`、`wait`、`needs_action`、`budget_blocked`、`permanent`。

## 2. 影音的引用方式

場景中用短代號指稱影音，不寫完整網址：

| 代號 | 意思 | 對應網址 |
|---|---|---|
| `yt:<id>` | YouTube 影片 | `https://www.youtube.com/watch?v=<id>` |
| `bili:<bvid>/p<n>` | Bilibili 影片的第 n 個分P | `https://www.bilibili.com/video/<bvid>?p=<n>` |
| `pod:<feed>/<guid>` | Podcast feed 中的一集。`<feed>` 是 production 從 feed 網址推導出的 key（`show.xml` → `show`），guid 可以含 `/`，以第一個 `/` 切開 | 新增時送 `{"url": "https://feeds.example.com/<feed>.xml", "episodes": ["<guid>"]}` |
| `local:<path>` | 本機檔案，路徑相對於 inbox | — |

`When the owner ingests` 接受代號或完整網址；完整網址用於驗證網址辨識。

`local:` 代號的細節（ING-05）：

- inbox 中的檔案：`local:<相對 inbox 的路徑>`，以 `/` 分隔，例如 `local:lecture.mp4`。新增時送出 `{"path": "<inbox>/<路徑>"}`。
- `media_roots` 中或 inbox 之外的檔案：`local:<解析後的絕對路徑>`。
- 上傳的檔案：`local:<檔名>`，與既有影音或 inbox 頂層檔案撞名時，在副檔名前加 `-2`、`-3`（例如 `local:talk-2.m4a`）。
- 請求路徑中的 `local:` 代號（例如 `GET /api/items/local:lecture.mp4/media`）取最長的已知代號，後面的 `/media` 等段落保留。

其他代入規則：

- 請求路徑中的代號（例如 `GET /api/items/yt:abc123xxxxx`）會換成實際的影音 ID；`channel-of:<代號>` 換成該影音所屬頻道的 ID。影音在垃圾桶中，或已永久刪除時，代號仍然換成原本的 ID；永久刪除後再新增，換成新的 ID。
- CLI 指令與請求中的 `{token:<名稱>}` 會換成該 token 的實際內容。
- 預期結果的 JSON 中，值為 `"*"` 代表「欄位存在即可，不比對內容」。
- 物件只比對預期中出現的欄位。陣列依序比對：預期的第 n 個元素對應實際的第 n 個元素，實際陣列可以比預期長；要確認數量時，另外用計數的步驟。
- 場景沒有準備字幕來源時，`Given the item … has been processed up to …` 依代號從 `features/fixtures/items/` 的 fixture 建立（條件見 4.4）。

## 3. 字幕來源 ID

| ID | 來源 |
|---|---|
| `youtube-manual`、`youtube-auto` | YouTube 人工字幕、自動字幕 |
| `bilibili-cc`、`bilibili-ai` | Bilibili CC 字幕、AI 字幕 |
| `podcast-transcript` | `<podcast:transcript>` |
| `local-sidecar`、`local-embedded` | 本機的外掛字幕檔、內嵌字幕軌 |
| `supadata` | Supadata |
| `groq`、`whisper-1`、`voxtral` | ASR provider（依設定檔中的 ID） |
| `hub` | 從 Hub 取得的原始字幕（M5：重新生成與 ASR 前查詢，HSHR-R9、R14） |

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
  - `/vault` → `<場景根目錄>/vault`（M3：`fs` vault 的路徑；設定檔、請求 body 與檔案步驟都套用。M3-S0 已加上這個對應（`harness.VirtualVault`），場景建立時一併建立這個目錄）。
  - `/tmp/<x>` → `<場景根目錄>/tmp/<x>`：場景中的 `/tmp/...`（例如上傳用的 `/tmp/fansub.ass`）不寫到真正的 `/tmp`。檔案步驟、`the owner uploads "/tmp/..."`、`When the owner sends … with:` 的請求 body 與 `When the owner runs` 的指令參數（例如 `notesteme config import /tmp/settings.yaml`）都套用這個對應（讓 body 裡的 `path` 指到 `Given the file … contains:` 寫的檔案，例如 OUT-R8、R13～R15）。
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
| `Given the provider {string} has no API key` | 移除 provider 的 API key（fake provider 預設有 key：Supadata 的 `NOTESTEME_SUPADATA_API_KEY`、Groq 的 `NOTESTEME_GROQ_API_KEY`）。參數是字幕來源 ID（第 3 節）時，同時移除環境變數與 fake 的 key 要求，所以產品沒檢查 key 就呼叫來源會拿到轉錄結果、場景失敗；來源還沒實作（`source.Implemented`）時 pending；必須放在 server 啟動之前（環境變數只在啟動時讀取）。LLM provider（`anthropic`、`openai`）會移除 `NOTESTEME_<ID>_API_KEY`，並取消 fakellm 的 key 要求 |
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
| `Then the response contains:` | 回應的 JSON 包含指定的欄位與值（子集比對）。回應中的實際路徑先換回虛擬路徑（`/data/…`）再比對，預期值不展開 |
| `Then the response header {string} contains {string}` | 回應標頭 |

### 4.3 平台與來源的 fixture（`steps/sources.go`）

| 步驟 | 意思 |
|---|---|
| `Given a YouTube video {string} titled {string}` | 建立 fake 的 YouTube 影片；沒有另外指定時，原文語言為 `en`，頻道為 `Fixture Channel`（`UCfixture000000000000000`），所有假的 YouTube 影片都在同一個頻道。`with:` 可以用 `channel`、`channel_id` 覆寫 |
| `Given a YouTube video {string} with:` | 同上，用表格（`field`、`value`）指定 `title`、`duration`、`language`、`availability`、`chapters`、`live_status` 等。`availability` 為 `subscriber_only` 時，假 yt-dlp 在下載字幕與媒體時要求含 `.youtube.com` cookie 的 `--cookies` 檔，否則回報 `ERROR: [youtube] <id>: Join this channel to get access to members-only content like this video, or contact the channel owner.`（取得資訊不失敗，辨識網址的 Probe 仍能看到 `availability`） |
| `Given a Bilibili video {string} with {int} parts` | 建立 fake 的 Bilibili 影片（`1 part` 亦可）；參數是不帶 `bili:` 的 BV 號。各分P 沒有字幕軌、沒有原文語言，標題預設為 BV 號；之後用 `bili:<BV>/p<n>` 逐分P 設定。分P 不存在（或影片還沒建立）時，使用該代號的步驟失敗 |
| `Given a podcast feed {string} with the episodes:` | 建立 fake 的 feed，表格欄位依序為 `guid`、`title`、`audio`（guid 不重複、不為空）；feed 的網址為 `https://feeds.example.com/<名稱>.xml`。名稱必須符合 `^[a-z0-9]+(-[a-z0-9]+)*$`、最多 40 字元，且不是 `rss`、`feed`、`podcast` 等泛用名稱（否則 production 推導出的 feed key 會不同），不符合時步驟失敗。`audio` 是 `features/fixtures/podcast/` 的檔名時，音檔在 `https://media.example.com/<名稱>/<檔名>`；是完整網址時，取網址的檔名當 fixture，並把那個網址交給 fake origin（不改變主機的 DNS 解析序列）。fixture 不存在或不是假媒體檔時步驟失敗。每次請求都依當時的狀態產生 RSS：`<language>en</language>`、enclosure 的實際大小、`itunes:duration`（marker 有 `listed=<秒>` 時用它，否則用實際長度），腳本化字幕後才有 `<podcast:transcript>` |
| `Given a local file {string} in the inbox` | 在 inbox 放入 `features/fixtures/local/` 的同名 fixture 檔案，修改時間設為 fake clock 的現在；fixture 不存在時失敗 |
| `Given {string} has {word} captions in {string}` | 平台字幕軌；`{word}` 為 `manual`、`auto`、`platform-translated`。Bilibili 的 AI 字幕屬於 `auto`（Bilibili 沒有 `platform-translated`） |
| `Given {string} has no caption tracks` | 平台沒有任何字幕 |
| `Given the auto captions of {string} are not ready yet` | YouTube 自動字幕還沒生成：假 yt-dlp 不列出、也不下載自動字幕與平台翻譯字幕，人工字幕不受影響。只接受 `yt:` |
| `Given the source {string} returns {string} for {string}` | 字幕來源回傳指定的 fixture。平台來源（`youtube-*`、`bilibili-*`）寫進假 yt-dlp 對應種類的字幕軌（`*-manual`、`bilibili-cc` 是 `manual`，`*-auto`、`bilibili-ai` 是 `auto`），來源與影音的平台必須相符；Bilibili 的 fixture 是字幕 JSON（`body[{from, to, content}]`）。`podcast-transcript` 在 feed 的這一集加上 `<podcast:transcript>`，fixture 放在 `features/fixtures/podcast/`，副檔名只接受 `.vtt`、`.srt`、`.json`；feed 或這一集不存在、同一個 feed 中兩集用同一個字幕檔名時失敗；`was called … times` 計算字幕網址被請求的次數 |
| `Given the source {string} has nothing for {string}` | 字幕來源沒有結果 |
| `Given the source {string} requires the cookie {string}` | 需要指定的 cookie 才能取得。平台來源（`bilibili-*`）的參數是 cookie 的平台：假 yt-dlp 沒有收到該平台的有效 cookie 檔（`--cookies` 指到 Netscape 格式、含 `.bilibili.com` 網域 `SESSDATA` 的檔案）時，不列出也不下載該來源的字幕軌，結束碼 0，並在 stderr 印出 login WARNING（有 `--no-warnings` 時不印）。平台來源還沒實作（`source.Implemented`）時 pending |
| `Given the source {string} accepts files up to {string}` | ASR 的單檔大小上限，例如 `"25MB"` |
| `Given the audio of {string} is {string}` | 音訊 fixture（`features/fixtures/media/`），例如長度或大小不同的檔案。`local:` 把 fixture 的長度、壓縮後大小與靜音區段併進 inbox 中的檔案；`yt:`、`bili:` 寫進假 yt-dlp 中這部影片的對應屬性。檔案不在 inbox、fixture 不存在時失敗 |
| `Given yt-dlp fails for {string} with {string}` | yt-dlp 對這個影音的所有操作（取得資訊、下載字幕、下載媒體）都以結束碼 1 失敗，stderr 為指定的錯誤輸出。只接受 `yt:` |

### 4.4 影音與產出（`steps/items.go`、`steps/trash.go`）

| 步驟 | 意思 |
|---|---|
| `When the owner resolves {string}` | `POST /api/resolve`，辨識網址 |
| `When the owner ingests {string}` | `POST /api/items`，使用預設的處理方案 |
| `When the owner ingests {string} with the profile {string}` | 指定處理方案 |
| `When the owner ingests {string} with the overrides:` | 帶單次覆寫（YAML） |
| `When the owner uploads {string}` | 透過 API 上傳本機檔案（`POST /api/items`，multipart 的 `file` 欄位）。參數是 `features/fixtures/local/` 的檔名；以 `/` 開頭時是場景中的路徑（對應規則見第 4 節） |
| `When the owner reruns {string} from {string}` | 從指定步驟重新處理 |
| `Given the item {string} has been ingested` | 已新增，worker 還沒執行 |
| `Given the item {string} has been ingested as {word}` | 以指定的優先級新增：`manual`、`subscription`、`backfill` |
| `Given {string} is a mixed-language item` | 把影音的原文語言標記為 `mixed` |
| `Given the item {string} has been processed up to {string}` | 已完成到指定步驟。依序判斷：(1) 場景已準備字幕來源（寫入 `source` 變體的字幕〔其他變體不算〕、符合 `caption_priority` 的字幕軌、腳本化的已實作來源；`local:` 另外包含 inbox 中同名的外掛字幕檔，以及 fixture 帶有文字字幕串流）：照常執行 pipeline 到該步驟；(2) 代號是 `yt:` 或 `bili:`、假 yt-dlp 中這部影片完全沒有字幕軌、場景也沒有寫入字幕或腳本化來源，且 `features/fixtures/items/<代號>.json`（`:`、`/` 換成 `-`）存在：先建立影音並做到 metadata，用 fixture 寫入 `source`，再照常執行剩下的步驟；fixture 的 `ref` 必須與檔名一致；`local:`、`pod:` 一律不套用 fixture；(2c) 目標是 `segment`、`correct`、`translate`、`publish` 時：X1 已決定（沒有 provider 或沒有輸出就是 `skipped`），所以目標步驟是 `done` 或 `skipped`（原因不是 `cancelled`）都算已處理到，其餘（`failed`、`blocked`、`pending`…）失敗；影音已存在時，只有 `done` 的步驟算完成，`skipped` 的步驟會從那一步重跑（補上設定後才會生效）；(3) 其他情況 pending。目標在 transcript 之前（如 `metadata`）不需要字幕。fixture 格式見 `features/fixtures/README.md` |
| `Given the {string} transcript of {string} is:` | 指定變體的字幕；表格欄位：`#`、`start`、`end`、`text`，可加 `words`、`timing`。`words` 是以空白分隔的 `text@start-end`，時間可以是時間碼（`00:00:01.000`）或秒數（`1.0`） |
| `Given the {string} transcript of {string} in {string} is:` | 同上，指定語言 |
| `When the worker runs until idle` | worker 持續執行，直到沒有可以執行的任務 |
| `Then the library contains {int} items` | 影音總數，不含垃圾桶中的影音。server 已明確停下（`When the server shuts down gracefully`）時，不隱含啟動 server，改用唯讀方式查詢 DB |
| `Then the library contains {string}` | 影音存在，且不在垃圾桶中。server 已明確停下時，同上，改用唯讀方式查詢 DB |
| `Then the stage {string} of {string} is {string}` | 步驟狀態。影音在垃圾桶中（`GET /api/items/:id` 回 404 `in_trash`）時，改以唯讀方式查詢 DB，影音的 ID 取自新增時記下的對照表 |
| `Then the stage {string} of {string} is blocked with the reason {string}` | 狀態為 `blocked`，且 `block_reason` 相符 |
| `Then the stage {string} of {string} is skipped with the reason {string}` | 狀態為 `skipped`，且略過的原因（`block_reason`，例如 `same_language`、`not_configured`、`off`）相符 |
| `Then the stage {string} of {string} failed with the error class {string}` | `failed` 及錯誤類別 |
| `Then the stage {string} of {string} waits until {string}` | 狀態為 `waiting`，`not_before` 與指定的 RFC 3339 時間是同一個時間點（不比時區寫法） |
| `Then the stage {string} of {string} retries at {string}` | `pending`（重試中），下一次嘗試的時間為指定時間 |
| `Then {string} shows the message {string}` | 影音的狀態訊息（UI 與 API 共用的文案）：依 pipeline 的順序，取第一個「不是 `done` 而且有 `message`」的步驟；沒有的話，取第一個「`done` 而且有 `message`」的步驟（提醒，例如 SRC-R15 的「時間可能偏移」）。訊息必須完全相同；都沒有就失敗。比對不符時，若緊接在前一個步驟送出的請求回 501 `not_implemented`，視為 pending（ADR-0015）。最近一次送出的請求回應是這部影音的 409 `in_trash`（新增時被擋下，`item_id` 與代號對應的 ID 相同）時，比對那個 409 回應的 `message`；否則影音在垃圾桶中（`GET /api/items/:id` 回 404 `in_trash`）時，比對那個 404 回應的 `message` |
| `Then {string} offers the actions {string}` | 可以執行的修正動作：同一個步驟（同上）的 `actions`；參數以逗號切開並去掉前後空白，與 `actions` 依序完全相等。比對不符時，若緊接在前一個步驟送出的請求回 501 `not_implemented`，視為 pending（ADR-0015）。最近一次送出的請求回應是這部影音的 409 `in_trash`（同上）時，比對那個 409 回應的 `actions`；否則影音在垃圾桶中（`GET /api/items/:id` 回 404 `in_trash`）時，比對那個 404 回應的 `actions` |
| `Then the transcript of {string} came from {string}` | 採用的字幕來源 |
| `Then the source {string} was rejected for {string} because {string}` | 品質檢查不通過的原因：影音詳情的 `sources` 中，有一筆 `source` 相符、`state` 為 `rejected`、`reason` 相符 |
| `Then the original language of {string} is {string}` | 原文語言 |
| `Then the original language of {string} is marked as mixed` | 標記為中英夾雜等混合語言 |
| `Then the {string} transcript of {string} is:` | 字幕內容，表格欄位同上；只比對表格中出現的欄位。`#` 欄從 1 起連號時，字幕的句數必須與表格列數相同，並依序比對；`#` 不是從 1 起連號（例如只列 `41`、`42`）時，依 `#` 比對第 N 句（該句的編號也必須是 N），不檢查總句數 |
| `Then the {string} transcript of {string} in {string} is:` | 同上，指定語言 |
| `Then the {string} transcript of {string} has {int} cues` | cue 數 |
| `Then the {string} transcript of {string} in {string} has {int} cues` | 同上，指定語言 |
| `Then every cue of the {string} transcript of {string} follows the subtitle rules for {string}` | 每個 cue 都符合指定文字系統（`cjk`、`latin`）的字幕規則（plan 第 8.5 節）：顯示 0.8–7 秒；依每行字數排版（拉丁文字 42 字元、依空白換行；CJK 取範圍的寬鬆端 25 字、依字數）不超過 2 行。斷言獨立計算，不呼叫產品的規則檢查；沒有任何 cue 時失敗 |
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

fixture 在 `features/fixtures/llm/<名稱>`，沒有 `.json` 時自動補上。步驟取自請求強制的 tool 或 schema 名稱 `notesteme_<stage>`，chunk 取自 `<transcript_data>` 的 `chunk`。請求數包含失敗的請求。`fails with HTTP` 優先於所有 `answers`；`for chunk` 優先於不指定 chunk 的 `answers`。`request N`（從 1 起算，依抵達順序）的「prompt」是解碼後的所有文字內容，再加上整份請求 body（compact、不跳脫 HTML），所以 `contains` 也會比對到 schema、參數等 JSON 欄位名稱與值。「前綴」是真正的資料區塊（可解析的 `<transcript_data>`）之前的部分。

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
| `When the agent {string} claims up to {int} tasks of kind {string}` | 呼叫 `claim_tasks`（`kinds` 是這一種，`max` 是 N）；結果也是最近一次的 tool 呼叫，`the tool result contains:`、`the tool call fails with` 斷言它；tool 回錯誤時步驟本身不失敗 |
| `Then the agent {string} holds {int} tasks` | 目前持有的任務數：領取到、還沒交出去的任務中，資料庫裡仍以領取時的 `lease_token` 被這個 Agent（token 名稱）領取的 |
| `When the agent {string} submits {string} for its task {int}` | 對第 n 個領取到、還沒交出去的任務（依領取順序）呼叫 `submit_task`；輸出由 LLM fixture 以這個任務的 `context`、`cues` 產生（同 fake LLM），fixture 的 `model` 送成 `model` |
| `When the agent {string} submits {string} for all its tasks` | 對所有目前持有的任務依序提交；任何一個沒有被接受時步驟失敗 |
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
| `When the owner runs {string}` | 執行 CLI，例如 `"notesteme items ls --json"`。只執行 `notesteme` 與 `docker`；`docker compose … config --format json` 由 fakecompose 模擬，其他 `docker` 指令失敗（不是 pending）；不會隱含啟動 server |
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
| `Then the file {string} contains {string}` | 檔案內容包含指定文字。`.tar.gz` 先 gunzip，再逐一比對每個 tar 項目的內容（檔名不算），任何一個項目包含就成立；其他壓縮檔照原本的方式解開。解不開、或 `.tar.gz` 沒有任何一般檔案項目（「封存檔沒有任何檔案」）時失敗 |
| `Then the file {string} does not contain {string}` | 檔案內容不包含指定文字。`.tar.gz` 先 gunzip，再逐一比對每個 tar 項目的內容，所有項目都不包含才成立；其他壓縮檔照原本的方式解開。解不開、或 `.tar.gz` 沒有任何一般檔案項目（「封存檔沒有任何檔案」）時失敗：空的封存檔不能證明「不含」 |
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

語意補充（2026-11-22，M2 S1/S2 設計時決定；不新增句型）：

- 累積與寫入：`the prompt … version … is:`、`the profile … is:`、`the default profile is …`、`the channel of … has the overrides:` 都比照 `the global settings are:`，步驟只累積，第一個送出請求的步驟才依序寫入。順序是全域設定 → prompt（依宣告順序）→ 處理方案 → 預設處理方案 → 頻道覆寫 → 佇列。寫入的請求回 501 時，那個步驟 pending；其他非預期的狀態碼讓步驟失敗，訊息附上回應內容。理由：在 Background 就送出請求會提早啟動 server，之後 `the provider … has no API key` 這類必須在 server 啟動前的步驟就來不及了。這些寫入不覆寫「最近一次回應」，所以 `previousRequestNotImplemented` 的判斷不受影響。
- `the prompt {s} version {N} is:`：N=1 送出 `POST /api/prompts {id, kind, body}`，N>1 送出 `POST /api/prompts/{id}/versions {body}`；回應的 `version` 必須等於 N。`kind` 取自 id 以 `-`、`.` 切開後第一個已知的類型（`localize` 算 `translate`），推斷不出來時步驟失敗。
- `the profile {s} is:`：YAML 轉成 `POST /api/profiles {name, config}`，必須回 201。
- `the default profile is {s}`：`POST /api/profiles/{s}/default`，必須回 200。
- `the channel of {s} has the overrides:`：從 fake 平台取得影音所屬頻道的 natural key，送出 `PATCH /api/channels/<key>/overrides`，必須回 200。`yt:` 是 `youtube:<fake 影片的 channel_id>`（預設 `UCfixture000000000000000`）；`bili:` 是 `bilibili:<uploader_id>`；`pod:` 是 `podcast:<feed>`；`local:` 沒有頻道，步驟失敗。
- `the effective config of {s} for {s} contains:`：讀取該步驟最新一筆 stage run 綁定的快照（測試 hook `Server.StageConfig`），以 YAML 子集比對，但陣列必須完全相等（否則驗證不到「陣列整個取代」）。`metadata` 的設定不含頻道層，因為頻道在 `metadata` 完成時才知道。
- `When the owner ingests {s} with the profile {s}`：請求的 body 加上 `profile`。
- `Given the default prompt for {s} is {s}`：沒有任何 feature 用到，語意也不清楚，維持 pending。

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
| `Then the number of events is {int}` | 持久化的事件筆數：計算整張 events 表（包括場景中用 `an event from … exists` 建立的，不只是場景自己建立的）。server 已明確停下時改用唯讀方式查詢 DB |

### 4.13 SponsorBlock 與章節（`steps/chapters.go`，M3）

M3 的句型（4.13～4.16）在 2026-11-23 規格展開時加入，目前全部註冊為 pending 的步驟樁（`godog.ErrPending`），由 M3 的實作切片換成真正的實作。假服務的設計見 `features/fixtures/README.md` 的「M3 的假服務」；M3-S0 已把它們接進 harness（`World.SponsorBlock`、`World.Gemini`、`World.FakeGit()`、`World.HookRecord()`、PATH 上的 `git` 與 `notesteme-test-hook`、虛擬路徑 `/vault`），步驟樁本身仍是 pending。

每個 M3 的 `.feature` 在 Background 中、第一個會送出請求的步驟之前，都有一個 M3 的句型（假服務或 M3 產出的 seeding）。處理方案中的 M3 欄位（`stages.chapters` 等）在 M3-S0 之前會被 PRF-R4 以 422 拒絕（M3-S0 之後已經接受，見 PRF 的介面）；這個安排只保證**在第一個 M3 句型實作之前** `@M3` 場景一律是 pending。閘門句型有共用（`SponsorBlock has the segments for` 是 SPB 與 CHAP 的閘門、`the chapters of … are:` 是 NOTE 的閘門、`the note of … is:` 是 VAULT 的閘門），實作其中一個之後，其他領域的場景會往下跑到 M3 的設定而失敗。所以 M3 的第一個實作切片（M3-S0）先讓處理方案與設定檔的 schema 接受全部 M3 欄位（`stages.{sponsorblock,chapters,frames,note}`、`outputs[].target: vault`、設定檔的 `vaults`、`sponsorblock`），M3 的路由回 501 stub（同 M2 S0），之後各切片以 `@M3` 的逐場景結果（junit）和切片開始前比對，不期待「目標領域以外全部 pending」。

M3-S1（2026-11-23）實作了 SponsorBlock 的四個句型，並讓它們只對 SPB 的場景放行（harness 依場景的 tag 判斷：有 `@SPB-` 開頭的 tag 才執行，`@SPB-R7` 另外要等 `note` 步驟實作）；其他領域（CHAP、FRM、NOTE、M4 的 SRC-10）的場景在這些句型上維持 pending，等各自的切片實作時再放寬（SPB 待決問題 B12）。這不是新句型。

M3-S2（2026-11-23）實作 `chapters` 之後，SponsorBlock 的四個句型也對有 `@CHAP-` 開頭 tag 的場景放行（`pipeline.StageAvailable("chapters")`，CHAP 待決問題 H11）；FRM、NOTE、`@SPB-R7`、M4 的 SRC-10 不變。`Given the chapters of … are:` 仍是步驟樁（NOTE 的閘門）。

M3-S3（2026-11-23）實作了 4.14 的 `the video of … has the scenes:`、`the best video format of … is …`、`the tool result has an image of type …`，只對 FRM 的場景放行（有 `@FRM-` 開頭的 tag）；SponsorBlock 的句型也對 FRM 的場景放行（FRM-R6）。`the frames of … are:` 仍是步驟樁（NOTE、VAULT 的切片）。這些都不是新句型。

既有句型在 M3 的語意補充（2026-11-23，不新增句型；由實作切片在對應的步驟實作）：

- `Given the item … has been processed up to …`：目標也可以是 `sponsorblock`、`chapters`、`frames`、`note`。產品的 `pipeline.StageAvailable` 回報這個步驟還沒有任何 executor 實作時 pending（M3-S0 之後四個步驟都在 `pipeline.Stages` 中，改用這個判斷；與 `source.Implemented` 同類，只限這四個名稱；其他不認識的名稱仍然失敗）；實作之後依 (2c) 的規則判斷（`done` 或 `skipped` 都算處理到）。決定重跑的起點時，跳過還沒有實作的 M3 步驟（它們一律 `skipped`（`not_implemented`），從它們重跑回 501）。
- `Then the {string} artifact of {string} has the provenance:`、`has {int} versions`、`the item … has no … artifact`：名稱也可以是 M3 產出的 `kind`：`sponsor_segments`、`chapters`、`frames`、`note`。
- 4.6 的 LLM 步驟：步驟名稱也可以是 `chapters`、`frames`、`note`、`note_map`（tool 或 schema 名稱 `notesteme_<stage>`）；provider 也可以是 `gemini`，由 `features/internal/fakegemini` 回應（`Given Gemini accepts YouTube videos up to …` 登記它），fixture 一樣放在 `features/fixtures/llm/`，`output` 會被包成 Gemini 回應的 `candidates[0].content.parts[0].text`。`frames` 的請求含有圖片區塊，fakellm 要接受 anthropic 的 `image` block 與 openai 的 `image_url`（FRM 規格 F4）。
- `Given the provider {string} has no API key`：也接受 `gemini`（移除 `NOTESTEME_GEMINI_API_KEY`）。

| 步驟 | 意思 |
|---|---|
| `Given SponsorBlock has the segments for {string}:` | 假 SponsorBlock（`features/internal/fakesponsorblock`）登記這部影片的片段；表格欄位 `category`、`start`、`end`（時間碼），可加 `action`（預設 `skip`）。只接受 `yt:`。假服務依 SponsorBlock 的 hash 前綴 API 回應（SPB-R2）：前綴相同的其他影片也會出現在回應中，由產品挑出相符的 `videoID` |
| `Given SponsorBlock fails for {string} with HTTP {int}` | 查詢這部影片（含相同 hash 前綴的查詢）時，假 SponsorBlock 回應指定的狀態碼 |
| `Then SponsorBlock received {int} requests for {string}` | 假 SponsorBlock 收到、hash 前綴與這部影片相同的請求數（含失敗的請求） |
| `Then SponsorBlock never received the video ID of {string}` | 假 SponsorBlock 收到的所有請求（路徑、query、標頭、body）都不含這部影片的 11 字元 ID；完全沒有收到請求時失敗（沒有東西可以驗證） |
| `Given the chapters of {string} are:` | 直接寫入現行的 `chapters` 產出（同 `the … transcript of … is:` 的 seeding，影音不存在時先新增並做到 metadata）；表格欄位 `start`（時間碼）、`title`，可加 `cue`；有 `cue` 欄時 `origin` 是 `llm`，否則是 `platform` |

### 4.14 關鍵畫面（`steps/frames.go`，M3）

| 步驟 | 意思 |
|---|---|
| `Given the video of {string} has the scenes:` | 假 ffmpeg（`features/internal/fakeframes`）中這部影片的畫面：表格欄位 `at`（時間碼）、`score`（場景分數 0～1）、`color`（`#rrggbb`）。場景偵測（`select='gt(scene,T)'` 加 `showinfo`）只回報分數大於 `T` 的列；任何時間點擷取的畫面都是「時間不晚於它的最後一列」的純色圖（第一列之前是黑色），所以去重與密度下限都可以預測。也會讓假 yt-dlp 為這部影片提供影像串流；沒有這個步驟的 YouTube 影片下載後沒有影像串流 |
| `Given the best video format of {string} is {string}` | 假 yt-dlp 的格式清單中，這部影片最高畫質的影像格式（`寬x高`，例如 `"3840x2160"`）；另外一律有 `1280x720` 以下的格式。假 ffmpeg 擷取的圖片尺寸依實際下載的格式與縮放參數計算 |
| `Given the frames of {string} are:` | 直接寫入現行的 `frames` 產出與圖片（seeding）；表格欄位 `#`、`at`、`selected`（`yes`／`no`），可加 `caption`、`chapter`、`color`。圖片是 1280x720 的純色 JPEG |
| `Then the tool result has an image of type {string}` | 最近一次 tool 呼叫的結果含有 MCP 的 image content，`mimeType` 相符，資料可以解碼成該格式的圖片 |

### 4.15 筆記與 Gemini（`steps/notes.go`，M3）

「筆記」指這部影音現行的 `note` 產出（`GET /api/items/:id/note` 的 `markdown`，語言是預設的筆記語言），不是寫到 vault 的檔案；vault 中的檔案用 4.9 的檔案步驟驗證。

| 步驟 | 意思 |
|---|---|
| `Given the note of {string} is:` | 直接寫入現行的 `note` 產出（seeding）：doc string 是完整的 Markdown（含 frontmatter），語言是筆記語言（NOTE-R5），來源紀錄的 `checks.passed` 為 `true`。影音不存在時先新增並做到 metadata。用於 VAULT 與 MCP 這類只需要「已經有筆記」的場景 |
| `Then the note of {string} contains {string}` | 筆記的 Markdown 包含指定文字（逐字比對，不做 Markdown 解析） |
| `Then the note of {string} does not contain {string}` | 筆記的 Markdown 不包含指定文字；沒有筆記時失敗（不能證明「不含」） |
| `Then the note of {string} has the frontmatter:` | 以 YAML 解析筆記開頭的 frontmatter，表格欄位 `field`、`value`；值一律以字串比較（空白的 `value` 代表空字串，欄位必須存在） |
| `Given Gemini accepts YouTube videos up to {string}` | 假 Gemini（`features/internal/fakegemini`）可以讀取的片長上限，例如 `"45m"`：`file_data.file_uri` 指到的影片（以假 yt-dlp 中的片長判斷）超過上限時回 400 `INVALID_ARGUMENT`；不是公開影片時回 403 `PERMISSION_DENIED`。也登記假 Gemini 為 LLM provider `gemini`（4.6 的 LLM 步驟可以用 `gemini`，步驟名稱固定是 `note`） |

### 4.16 Vault 與 hook（`steps/vault.go`，M3）

`git` vault 的遠端由假 `git`（`features/internal/fakegit`，放在 `PATH` 中）模擬：remote URL 對應到場景目錄中的假遠端狀態，不連任何網路。第一次 clone `https://`、`ssh://` 或 `user@host:path` 網址時，假遠端自動建立成空的 repo（分支依 `--branch`）；其他網址一律失敗。hook 用 `PATH` 中的假程式 `notesteme-test-hook`（`features/internal/fakehook`）：把 argv、環境變數與工作目錄記錄下來；`--exit N` 以結束碼 N 結束、`--hang` 一直等到被結束、`--print-bytes N` 印出 N 個位元組後再印 `END-OF-HOOK-OUTPUT`。

| 步驟 | 意思 |
|---|---|
| `Given the vault remote rejects pushes as {string}` | 假 git 遠端拒絕所有 push：`non-fast-forward`（stderr 為 git 的 `! [rejected] … (non-fast-forward)`，結束碼 1）或 `bad credentials`（`fatal: Authentication failed for '<remote>'`，結束碼 128） |
| `Given the vault remote fails the next push with a network error` | 下一次 push 以 `fatal: unable to access '<remote>': Could not resolve host` 失敗（結束碼 128），之後恢復正常 |
| `Then the vault remote received a commit with the files:` | 假遠端最後一次被推送的 commit 中，相對於上一個 commit 有變動的檔案正好是表格（欄位 `path`，相對於 repo 根目錄）列出的這些 |
| `Then the vault remote received a single new commit` | 場景開始後，假遠端的分支總共只多了一個 commit |
| `Then every git command for the vault ran with {string}` | 假 git 記錄的每一次呼叫，argv 都含有這個元素（例如 `core.hooksPath=/dev/null`，`-c` 後面的值）；沒有任何呼叫時失敗 |
| `Then no git command for the vault contained {string}` | 假 git 記錄的每一次呼叫，argv 的任何元素都不含這段文字；沒有任何呼叫時失敗 |
| `Then the hook {string} ran with the arguments:` | 設定檔中這個 hook 最近一次執行的 argv（不含第一個元素）與 doc string 的 JSON 字串陣列完全相同；實際路徑先換回虛擬路徑（`/vault/…`）再比對 |
| `Then the hook {string} did not run` | 場景中這個 hook 沒有執行過 |
| `Then the hook {string} saw the environment variable {string}` | hook 最近一次執行時的環境中有這個變數 |
| `Then the hook {string} did not see the environment variable {string}` | hook 最近一次執行時的環境中沒有這個變數；hook 沒有執行過時失敗 |

### 4.17 訂閱、事後的更好來源與新增入口（`steps/subscriptions.go`，M4）

M4 的句型（4.17～4.21）在 2026-11-23 規格展開時加入，目前全部註冊為 pending 的步驟樁（`godog.ErrPending`），由 M4 的實作切片換成真正的實作。跨規格的決定見 [ADR-0021](../adr/0021-m4-scheduler-subscriptions-and-outbox.md)，假服務的設計見 `features/fixtures/README.md` 的「M4 的假服務」。

M4 的 `.feature` 在每個非 `@manual` 場景中、第一個會啟動 server 或送出請求的既有步驟之前，都有一個 M4 的句型（閘門），所以**在閘門句型實作之前** `@M4` 場景一律是 pending。M4 的路由不存在時回 404 而不是 501，設定檔含 M4 的鍵時 server 會啟動失敗，所以 M4 的第一個實作切片（M4-S0）比照 M2 S0、M3-S0：先讓處理方案與設定檔的 schema 接受全部 M4 欄位（`stages.summary`、`stages.<stage>.batch`、`language.better_source`、`scheduler`、`notify`、`backup.daily`、`alerts`、`budget`、`auth.proxy`、`auth.oidc`、`server.public_url` 等），M4 的路由回 501 stub，之後各切片以 `@M4` 的逐場景結果（junit）和切片開始前比對。M4-S0 已完成這一步（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）；清單中的 `budget` 是筆誤：`budget` 仍是 SEC-R2 的「由資料庫管理」的欄位（以 `PUT /api/settings/budget` 設定，見 COST 規格的待決問題），設定檔寫了仍會啟動失敗。閘門句型有共用（`the notification endpoints are reachable` 是 NOTIFY、DIG 彙整與 OPS 告警的閘門；COST 的 `the cost estimate of every … task is …` 是 LIB-08、09 的閘門；BATCH 依賴 COST 的句型），實作其中一個之後，其他領域的場景會往下跑而失敗，要在同一個切片確認或一起合併。

M4 的新句型都用 `r.Step` 註冊（不限關鍵字），因為部分 `Given` 句型出現在 `When` 之後的 `And`。

各 feature 的閘門：

- `features/SUB-subscriptions.feature` 的 Background 第一步是 `Given the YouTube feeds are served from the registered videos`，所以每個 SUB 場景在任何既有步驟之前就 pending。這也讓 Background 之後的 `a YouTube video … with:` 可以用新欄位 `shorts`、`the URL … serves …` 可以登記 Bilibili 合集網址（兩者在現行實作會讓步驟失敗）。
- `features/SRC-better-source.feature` 的 Background 第一步是 `Given the platform adds manual captions in "en" to "yt:late01xxxxx" at …`，在 `the config file contains:`（含 M4 的 `scheduler`）、`the global settings are:`、`the item … has been processed up to …` 之前。
- `features/ENTRY-entry-points.feature` 的每個非 `@manual` 場景，第一個送出請求的步驟都是本檔的新句型（`the browser opens …`、`someone signs in with the password … to continue to …`），本身就是 pending。

#### SUB：訂閱（`steps/subscriptions.go`，M4）

| 步驟 | 意思 |
|---|---|
| `Given the YouTube feeds are served from the registered videos` | 把 `features/internal/fakefeed` 登記到 fakenet 的 `www.youtube.com`，只處理 `/feeds/videos.xml?channel_id=<ID>`：依當下登記的假 YouTube 影片（`a YouTube video …`，同一個 `channel_id` 的）產生 YouTube 的 Atom feed，依 `published_at` 由新到舊、最多 15 筆；每筆有 `yt:videoId`、`yt:channelId`、`title`、`published`、`link`（`shorts` 為 `yes` 的影片是 `https://www.youtube.com/shorts/<ID>`，其他是 `/watch?v=<ID>`），沒有片長與直播狀態（同真實的 RSS）。沒有 `published_at` 的影片視為 `2026-01-01T00:00:00Z` 發布。頻道沒有任何影片時回只有 `<feed>` 標頭的 Atom（真實的 YouTube 對存在但沒有影片的頻道也是如此）。同時讓假 yt-dlp 支援頻道的 `--flat-playlist -J https://www.youtube.com/channel/<ID>/videos`（依同樣的排序列出所有非 Shorts 的影片，entry 帶 `id`、`url`、`title`、`duration`、`live_status`，遵守 `--playlist-end`）與 `-J https://www.youtube.com/@<handle>`（handle 是頻道名稱轉小寫並去掉空白，例如 `Fixture Channel` → `@fixturechannel`；回傳 `channel_id`）。每個 SUB 場景的 Background 第一步（閘門）。 |
| `Given the YouTube feed of the channel {string} fails with HTTP {int}` | 之後 fakefeed 對這個頻道 ID 的 feed 請求都回應指定的狀態碼（body 是一行純文字），直到 `recovers`。可以在場景中途使用；不需要先有影片 |
| `Given the YouTube feed of the channel {string} recovers` | 取消 `fails with HTTP`，恢復依登記的影片回應 |
| `Then the YouTube feed of the channel {string} was requested {int} times` | fakefeed 收到、`channel_id` 等於這個 ID 的請求數（含失敗的回應）。`1 time` 亦可 |
| `Given the subscription {string} for {string}` | 以預設設定建立訂閱，第一個參數是場景內的別名（`^[a-z0-9-]+$`），第二個是網址。比照 4.11 的語意補充：只累積，server 啟動時（或已在執行時立即）依宣告順序送出 `POST /api/subscriptions {url}`，順序排在處理方案與預設處理方案之後；必須回 201，否則步驟失敗並附上回應（回 404 或 501 時 pending）。記下回應的 `id` 作為別名的對照。這個寫入不覆寫「最近一次回應」。訂閱的 `created_at` 是寫入時的 fake clock |
| `Given the subscription {string} for {string} with:` | 同上，doc string 是 YAML，轉成 JSON 後與 `url` 合併成 `POST /api/subscriptions` 的 body（可含 `backfill`、`filters`、`schedule`、`profile`、`overrides`、`enabled`） |
| `Then the subscription {string} has the entries:` | `GET /api/subscriptions/<id>/entries` 的條目與表格完全相同（不看順序）：欄位 `video`（影音的短代號，比對條目的 `ref`）、`decision`、`reason`（空白代表沒有原因或空字串）。條目數必須等於表格列數；別名不存在時失敗 |
| `Then the subscription {string} has no entries` | 同上，條目數為 0 |
| `Then the next check of the subscription {string} is at {string}` | `GET /api/subscriptions/<id>` 的 `next_check_at` 與指定的 RFC 3339 時間是同一個時間點（不比時區寫法） |
| `Then the next check of the subscription {string} is between {string} and {string}` | `next_check_at` 不早於第一個時間、早於第二個時間（左閉右開） |
| `Then the number of subscriptions is {int}` | `GET /api/subscriptions` 的 `subscriptions` 長度；server 已明確停下時改用唯讀方式查詢 DB |
| `Given the podcast feed {string} adds the episode {string} titled {string} with the audio {string}` | 在既有的假 feed（`a podcast feed … with the episodes:` 建立的）最前面加上一集：guid、標題、音檔的規則同 4.3 的 `a podcast feed`（fixture 不存在、guid 重複、feed 不存在時失敗）；`pubDate` 是步驟執行時的 fake clock。可以在場景中途使用 |
| `When the owner imports the subscriptions from {string}` | 用 owner 的 session 送出 `POST /api/subscriptions/import`（multipart 的 `file` 欄位，自動帶 CSRF token）。參數以 `/` 開頭時是場景中的路徑（第 4 節的對應規則，通常由 `the file … contains:` 寫入）；否則是 `features/fixtures/subscriptions/` 的檔名。回應成為「最近一次回應」 |
| `When the owner imports a generated OPML file with {int} feeds` | 以 `fakefeed.OPML` 產生含 N 個 `outline`（`https://feeds.example.com/gen-<n>.xml`，每個 `text` 約 100 個字元）的 OPML，照上一個步驟送出。9000 個 outline 約 1.2 MB，用來驗證大小上限；501 個約 70 KB，用來驗證筆數上限 |

既有句型的語意補充（SUB，2026-11-23，不新增句型；由 SUB 實作切片在對應的步驟實作）：

- **別名替換**：`{subscription:<別名>}` 在 `When the owner sends …`、`When the client sends …`（含 `with:` 的 body）的路徑與 body、`When the owner runs …` 的參數，以及 `Then the response contains:`、`Then the tool result contains:`、`Then the command output is JSON containing:`、`Then an event is received:` 的**預期值**中，都換成該別名的訂閱 id（比照 CLI 的 `{token:<名稱>}`）。別名不存在時步驟失敗。這是「預期值不展開」的唯一例外：訂閱 id 是產品產生的，場景無法寫死。
- `Given a YouTube video {string} with:`：
  - `published_at`（RFC 3339）已在 `features/steps/sources.go` 支援，steps.md 4.3 的欄位清單沒有列出，補上。fakefeed 的 Atom、頻道 `/videos` 的 flat-playlist 都用它排序。
  - 新欄位 `shorts`（`yes`／`no`，預設 `no`）：`yes` 的影片在 fakefeed 的 Atom 中 `link` 是 `/shorts/<ID>`，不出現在頻道 `/videos` 的 flat-playlist；`yt-dlp -J` 的 `webpage_url` 也是 `/shorts/<ID>`。現行的 `setVideoField` 遇到它會失敗，所以只在 SUB 的閘門之後使用。
  - `live_status` 也接受 `is_upcoming`、`post_live`、`was_live`（yt-dlp 的值），假 yt-dlp 的 `-J` 照樣輸出；fakefeed 不輸出直播狀態（同真實的 RSS）。
- `Given the URL {string} serves {string}`：除了 YouTube 播放清單，也接受 Bilibili 合集網址（`space.bilibili.com/<mid>/lists/<sid>?type=season`、`/channel/collectiondetail?sid=<sid>`）與 YouTube 頻道的 `/videos` 網址，同樣把 `features/fixtures/ytdlp/` 的 fixture 登記為假 yt-dlp `--flat-playlist -J` 的輸出（頻道的 fixture 優先於 `the YouTube feeds are served from the registered videos` 的自動清單）。`www.youtube.com/feeds/videos.xml` 不支援這個步驟（由 fakefeed 處理）。
- `When the worker runs until idle`：ADR-0021 第 1 點的排程器是 worker 的 `Periodic`，所以「直到沒有可以執行的任務」也包含「沒有到期的排程工作」；排程工作建立的影音、影音建立的任務都會在同一次執行中處理完。被 rate limit 延後（SUB-R10）的檢查不算到期。
- `When the server starts`、`When the server restarts`：在 server 停止期間 `the clock advances by …` 之後啟動時，過期的排程工作要等到 `the worker runs until idle` 才執行（啟動本身不執行排程），讓場景可以在中間斷言。

#### SRC-10：事後出現更好的來源（`steps/subscriptions.go`，M4）

| 步驟 | 意思 |
|---|---|
| `Given the platform adds {word} captions in {string} to {string} at {string}` | 假 yt-dlp 在 fake clock 到達指定的 RFC 3339 時間之後，才為這部影音列出（並能下載）這條字幕軌；`{word}` 是 `manual` 或 `auto`，語言與影音代號的規則同 `{string} has {word} captions in {string}`（只接受 `yt:`、`bili:`）。影音可以在這個步驟之後才宣告（步驟只記錄，假 yt-dlp 每次呼叫時依當下的 fake clock 判斷），所以它可以放在 Background 第一步當閘門。字幕內容與 `has … captions in …` 產生的相同（120 句）。實作時 harness 要讓假 yt-dlp 讀得到 fake clock（例如在每次 `the clock is at`、`the clock advances by` 時寫進場景目錄的狀態檔）。到期後被列出的字幕軌，在 `GET /api/items/:id` 的 `sources` 中只有重跑 `transcript` 之後才會出現（SRC-R19） |

既有句型的語意補充（SRC-10，2026-11-23，不新增句型）：

- `Given the item {string} has been processed up to {string}`：處理完成時，fake clock 不前進，所以 `transcript` 的完成時間就是步驟執行時的 fake clock；SRC-R18 的第一次重新 Probe 從這個時間起算 12 小時。
- `Then {string} shows the message {string}`、`Then {string} offers the actions {string}`：SRC-R21 的訊息在 `done` 的 `transcript` 步驟上，SRC-R20 的提醒在 `done` 的 `sponsorblock` 步驟上，依既有的「沒有未完成步驟的訊息時，取第一個 `done` 而且有 `message` 的步驟」規則比對。
- `Then an event is received:`：`source.better_available` 事件的 `item` 是影音的短代號（同 `item.created`）。

#### ENTRY：其他新增入口（`steps/subscriptions.go`，M4）

| 步驟 | 意思 |
|---|---|
| `When the browser opens {string}` | 不帶任何身分（沒有 session cookie、沒有 bearer token），以瀏覽器的方式送出 `GET <路徑>`：`Accept: text/html,application/xhtml+xml,*/*`、不跟隨轉址。路徑照字面送出（query 已經是 URL 編碼的形式，不再編碼）。回應成為「最近一次回應」，`the response status is`、`the response header … contains …`、`the response contains:` 都斷言它 |
| `When the browser opens {string} signed in as the owner` | 同上，但帶 `Given the owner is signed in` 建立的 session cookie（沒有這個前提時步驟失敗）；不帶 CSRF token（GET 不需要） |
| `Then the browser is sent to {string}` | 最近一次回應是 3xx，`Location` 標頭與指定的值完全相同（相對路徑，不展開成絕對網址；`Location` 是絕對網址時失敗，因為 ENTRY-R2、R4、R5 規定是相對路徑） |
| `When someone signs in with the password {string} to continue to {string}` | `POST /api/auth/login {password, next}`，不帶 session（同 `someone signs in with the password {string}`，但 body 多了 `next`，照字面送出）；回應成為「最近一次回應」 |

### 4.18 摘要、通知與無人值守（`steps/notify.go`，M4）

共通語意：

- 「累積」的步驟比照 4.11 的語意補充：步驟本身不送請求，第一個送出請求的步驟才依序寫入。寫入順序接在既有順序之後：全域設定 → prompt → 處理方案 → 預設處理方案 → 頻道覆寫 → 佇列 → **notifier（依宣告順序）→ 彙整設定（依宣告順序）**。寫入回 501 時那個步驟 pending；其他非預期的狀態碼讓步驟失敗，訊息附上回應內容。這些寫入不覆寫「最近一次回應」。
- 「通知端點」是一個網址（`https://<host>/<path>`），比對時只看主機（不分大小寫）與路徑（完全相同），忽略 query。端點由 `features/internal/fakenotify` 扮演，只有 `the notification endpoints are reachable` 登記之後才有。

既有句型在 M4 的語意補充（不新增句型；由實作切片在對應的步驟實作）：

- `Given the item … has been processed up to …`：目標也可以是 `summary`。`summary` 的執行器還沒有實作（`pipeline.StageAvailable("summary")` 為 false）時 pending（同 M3 的四個步驟；M4-S0 已把 `summary` 加進 `pipeline.Stages`，所以不能再用 `ValidStage` 判斷（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認））；實作之後依 (2c) 判斷（`done` 或 `skipped` 都算處理到）。處理到 `summary` 時不套用 `pause_after`（`until` 在 `summary` 結束，DIG-R6 的暫停只發生在接著要執行下一個步驟時）。
- `Then the {string} artifact of {string} has the provenance:`、`the item … has no … artifact`：名稱也可以是 `summary`。
- 4.6 的 LLM 步驟：步驟名稱也可以是 `summary`（tool 或 schema 名稱 `notesteme_summary`）；`summary` 的請求沒有 `<transcript_data>` 的 `chunk`（DIG-R2 只送一個請求），`for chunk` 的句型不適用。
- `Then the stage {string} of {string} …`：步驟名稱也可以是 `summary`（M4-S0 起是 `pipeline.Stages` 的成員，預設 `skipped`、原因 `off`）。
- `Then the command output is JSON containing:`：`docker compose … config` 的變數代入使用 `the compose .env file contains:` 的值（加在 `features/steps/cli.go` 的 `composeEnv` 之上）。

#### DIG

| 步驟 | 意思 |
|---|---|
| `Given the summary profile is:` | 累積一個處理方案 `summary-test`（doc string 是 YAML，寫入方式同 `the profile … is:`），並設為預設處理方案（同 `the default profile is …`）。YAML 必須含 `stages.summary`，否則步驟失敗。`pipeline.StageAvailable("summary")` 為 false 時 pending（DIG 的閘門；M4-S0 起 `ValidStage` 已是 true，改以 `StageAvailable` 判斷（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）） |
| `Given the summary of {string} is:` | 直接寫入現行的 `summary` 產出（seeding，同 `the note of … is:`）：doc string 是 JSON `{summary, key_points, title_translation, language}`（`language` 省略時是 `zh-Hant`），`summary` 步驟為 `done`，結束時間是 fake clock 的現在，來源紀錄的 `checks.passed` 為 `true`。影音不存在時先新增並做到 metadata。用於彙整這類只需要「已經有摘要」的場景 |
| `Given the digest {string} is:` | 累積一個彙整設定：doc string 是 `POST /api/digests` 的 JSON body（不含 `id`，`id` 是參數），必須回 201 |

#### NOTIFY

| 步驟 | 意思 |
|---|---|
| `Given the notification endpoints are reachable` | 啟動假通知服務（`features/internal/fakenotify`），登記到 fakenet：`ntfy.sh`、`discord.com`、`api.telegram.org`、`hooks.example.com` 依序解析到公開的測試 IP `93.184.216.60`、`.61`、`.62`、`.63`，由同一個 fake 回應（場景可以用 `the host … resolves to …` 改寫解析結果，例如 DNS rebinding；連到這四個 IP 的請求一律交給 fakenotify）；每個請求預設回 ntfy `200 {"id":…}`、Discord `204`、Telegram `200 {"ok":true,…}`、一般 webhook `204`。其他主機不登記（照 fakenet 既有的 trap）。也是 NOTIFY、OPS-06～08 場景的閘門 |
| `Given the notifier {string} is:` | 累積一個 notifier：doc string 是 `POST /api/notifiers` 的 JSON body（不含 `id`，`id` 是參數），必須回 201 |
| `Given the notification endpoint {string} answers HTTP {int} for the next {int} requests` | fakenotify 對這個端點接下來 N 個請求回指定狀態碼（body 是空 JSON 物件；429 時加 `Retry-After: 120`），之後恢復預設回應 |
| `Given the notification endpoint {string} answers HTTP {int}` | fakenotify 對這個端點的所有請求都回指定狀態碼（同上），優先於 `for the next … requests` |
| `Then the notification endpoint {string} received {int} requests` | fakenotify 收到這個端點的請求數（含回錯誤的請求） |
| `Then the last request to the notification endpoint {string} contains:` | 這個端點最近一次收到的請求，body 解析成 JSON 後包含 doc string 的欄位與值（子集比對，同 `the response contains:`）；沒有收到請求或 body 不是 JSON 時失敗 |
| `Then the last request to the notification endpoint {string} has the header {string} containing {string}` | 最近一次請求的標頭值（多個值以 `,` 連接）包含指定文字；標頭不存在時失敗 |
| `Then the last request to the notification endpoint {string} is signed with {string}` | 最近一次請求有 `X-Notesteme-Timestamp`（Unix 秒，等於送出時 fake clock 的時間）與 `X-Notesteme-Signature`，後者等於 `sha256=` 加上 `hex(HMAC-SHA256(key=參數, msg=<timestamp> + "." + <原始 body>))`；由 fakenotify 獨立計算，不呼叫產品的程式 |
| `Then the notification endpoint {string} received a request containing {string}` | 這個端點收到的任何一個請求，原始 body 或 JSON 解碼後的任何字串值包含指定文字 |
| `Then no request to a notification endpoint contains {string}` | fakenotify 收到的所有請求（路徑、query、標頭、原始 body、JSON 解碼後的字串值）都不含這段文字；完全沒有收到請求時失敗（沒有東西可以驗證） |

#### OPS-06～08

| 步驟 | 意思 |
|---|---|
| `Given the backup directory {string} contains:` | 在場景的目錄（路徑對應見 steps.md 第 4 節）建立表格（欄位 `file`）列出的一般檔案，內容是固定的假資料（不是有效的備份）；上層目錄不存在時自動建立。建立的檔案登記到 After hook 的 secret 掃描的例外（它們不是 tar.gz） |
| `Then the backup directory {string} contains exactly:` | 該目錄中的項目（一般檔案與 symlink，不含子目錄）名稱正好是表格（欄位 `file`）列出的這些，不比順序 |
| `Given the data volume is {int} percent full` | 測試 hook（`TestControls.DiskUsage`）讓產品讀到的 `/data` 所在檔案系統用量是指定百分比；不寫入真的檔案。沒有這個步驟的場景，harness 預設 10%（OPS 待決問題 O10），CI 機器的真實磁碟用量不會觸發告警。可以在 server 啟動後再次使用，下一次 `alerts` 檢查時生效 |
| `Given the cookie for {string} was last used successfully at {string}` | 累積：第一個送出請求的步驟先以 `POST /api/cookies` 上傳該平台（`youtube`、`bilibili`）的測試 cookie，再經測試 hook 把它的「最後一次成功使用」設為指定的 RFC 3339 時間（上傳時間也設為同一時間） |
| `Given yt-dlp recorded {int} failures out of {int} calls in the last hour` | 測試 hook 寫入 yt-dlp 呼叫統計：在 fake clock 的現在之前 1 小時內，共 N 次呼叫、其中 M 次失敗（平均分布在這 1 小時內）；之前 24 小時沒有紀錄。第一個參數大於第二個時步驟失敗 |
| `Given the instance is deployed with the {string} profile` | 把參數原樣設為環境變數 `NOTESTEME_DEPLOYMENT`（不檢查值，場景也用不合法的值驗證 OPS-R21），必須在 server 啟動之前；CLI 的 `When the owner runs` 也帶這個環境變數。OPS-08 的閘門 |
| `When the server starts with the flag {string}` | 同 `When the server starts`，但在 `serve` 後面加上指定的旗標（例如 `--insecure-no-auth`） |
| `Given the compose .env file contains:` | 表格（欄位 `field`、`value`）模擬 `deploy/docker/.env`：之後的 `docker compose … config --format json`（fakecompose）以這些變數代入，優先於場景的環境變數；`value` 空白代表空字串。不寫入真的 `.env` |

### 4.19 成本、預算與 Batch API（`steps/costs.go`，M4）

閘門的共用：`features/COST-cost-budget.feature` 的閘門是 `the price of … is … USD per …`；`features/BATCH-batch-api.feature` 的閘門是 `the batch API of … completes after …`；`features/LIB-bulk-overview.feature` 的閘門是 COST 的 `the cost estimate of every … task is … USD`（後面還有 `the price of …`）。所以 COST 切片實作這兩個句型之後、LIB-R13～R22 實作之前，LIB-bulk-overview 的場景會從 pending 變成 failed；COST 切片要先確認 LIB 的場景仍是 pending（例如 `POST /api/items/batch`、`GET /api/overview` 回 501），或和 LIB 切片一起合併。BATCH 的場景同時依賴 COST 的句型（價格、usage），BATCH 切片排在 COST 之後。

#### 成本與預算（COST）

`Given` 的 seeding 步驟只累積，第一個送出請求的步驟才寫入（比照 4.11 的 `the global settings are:`）；寫入順序排在全域設定、處理方案之後，佇列之前。寫入的請求回 501 時那個步驟 pending。

| 步驟 | 意思 |
|---|---|
| `Given the price of {string} for {string} is {string} USD per {string}` | 計價覆寫：provider、model、金額、`unit_type`（`input_mtok`、`output_mtok`、`audio_hour`、`credit`）。寫入時送 `PUT /api/prices {provider, model, unit_type, usd_per_unit}`（`checked_at` 為 fake clock 的現在），必須回 200。同一組 `(provider, model, unit_type)` 寫兩次時，後面的取代前面的 |
| `Given the budget is:` | 預算設定；表格欄位 `field`、`value`（`monthly_usd`、`per_item_usd`、`warn_at_percent`、`timezone`），值是 JSON 的寫法（`0.10`、`null`、`"Asia/Taipei"` 可以不加引號）。寫入時送 `PUT /api/settings/budget`，必須回 200 |
| `Given the built-in prices were checked on {string}` | 測試控制：內建計價表的確認日期（`cost.PricesAsOf`）改為指定的 `YYYY-MM-DD`；必須在 server 啟動之前 |
| `Given the cost estimate of every {string} task is {string} USD` | 測試控制（ADR-0015）：這個步驟（例如 `correct`）每個任務的估算固定為指定值（視為可以估算），取代 COST-R5 的估算結果，預留與 LIB-R14 的 `dry_run` 都使用它；單價是否存在（COST-R7）仍照常判斷。這是一般 API 的金額：batch 的預留再乘以 batch 折扣（BATCH-R6、R7）。必須在 server 啟動之前 |
| `Given the cost ledger has an entry of {string} USD for {string} at {string}` | 同 4.4 的 `the cost ledger has an entry of … USD for …`（實際金額），但 `at` 是指定的 RFC 3339 時間。影音必須已經存在 |
| `Given the cost ledger has a reservation of {string} USD for {string}` | 成本帳本中有一筆這部影音的預留（`state: reserved`），沒有對應的任務（模擬程序中斷留下的預留，COST-R5）。影音必須已經存在 |
| `Given the LLM usage reported by {string} is {int} input and {int} output tokens` | fakellm 對這個 provider（`anthropic`、`openai`）的每個回應，`usage` 固定回報這些 token 數（取代依長度的估算）。batch 的結果（fakebatch）也使用 |
| `Given the LLM cost reported by {string} is {string} USD` | fakellm 對這個 provider 的每個成功回應在 `usage.cost` 帶這個金額（OpenRouter 的格式，COST-R2）。只接受 `openai`（OpenAI 相容），其他 provider 步驟失敗 |
| `Then the cost ledger entries of {string} are:` | 這部影音的成本紀錄依 `at`、`rowid` 排序後與表格依序比對，筆數必須相同。欄位可以是 `stage`、`provider`、`model`、`attempt`、`outcome`、`state`、`usd`、`unit_type`、`price_source`（`unit_price_json.source`）、`batch`（`unit_price_json.batch`，`true`／`false`）；只比對表格中出現的欄位。`usd` 以數值比較（誤差 1e-9），其他以字串比較。經測試 hook 讀取 DB（同 `Server.CostEntries`），不新增 API |
| `Then the cost export has a row with {string} set to {string}` | 最近一次回應的 body 依 RFC 4180 解析成 CSV（第一列是欄位名稱）；至少一列的這個欄位（解析後、未去掉 `'` 的值）與指定值完全相等。body 不是 CSV、沒有這個欄位時失敗 |

#### Batch API（BATCH）

假服務是 `features/internal/fakebatch`（見最後一節）。批次中的每個請求由 fakellm 依 4.6 的 `answers`、`for chunk`、`fails with HTTP` 回答，但**不計入** `the LLM provider … received …`（那只算一般 API 的請求）。

| 步驟 | 意思 |
|---|---|
| `Given the batch API of {string} completes after {string}` | 登記 fakebatch 為這個 provider（`anthropic`、`openai`）的 batch 端點：每個 batch 在建立後經過指定時間（fake clock，例如 `"10m"`）變成已完成，結果在那之後才查得到。完成之前收到取消時，batch 結束、所有請求為 `canceled`（不計費）；完成之後收到取消時不改變任何東西（結果照常可以取回） |
| `Given the batch API of {string} never completes` | 同上，但 batch 永遠停在處理中（模擬逾時，BATCH-R5）；被取消時才結束。同一個 provider 的 `completes after`、`never completes` 以最後一個為準（場景可以覆寫 Background） |
| `Given the batch API of {string} fails the requests for chunk {int}` | 這個 provider 的 batch 中，chunk 編號（同 4.6 的 `for chunk`）相符的請求結果是 `errored`（Anthropic）或 `error`（OpenAI 的 error file），其他請求照常 |
| `Given the batch API of {string} points its results to {string}` | 只接受 `anthropic`：batch 的 `results_url` 指向指定的網址（用來驗證產品不會連到其他主機，BATCH-R11）|
| `Then the batch count of {string} is {int}` | fakebatch 為這個 provider 建立的 batch 數（`batches` 不在 vocab 的 `countNouns` 中，所以數字放在句尾） |
| `Then the batch request count of {string} is {int}` | 這個 provider 所有 batch 中的請求總數 |
| `Then the cancelled batch count of {string} is {int}` | 收到取消請求的 batch 數（同一個 batch 取消多次只算一次） |

### 4.20 全文搜尋、bundle 與術語審核（`steps/library.go`，M4）

#### 全文搜尋（SRCH）

| 步驟 | 意思 |
|---|---|
| `Given the search library has {string} titled {string} with the {string} transcript:` | seeding：建立假的 YouTube 影片（只接受 `yt:`，標題為第二個參數，原文語言 `en`），做到 `metadata`（同 `Given the item … has been processed up to "metadata"`），再以表格寫入第三個參數指定變體的現行字幕（表格欄位與寫入方式同 `Given the {string} transcript of {string} is:`）。變體必須是 `source`、`normalized`、`segmented`、`corrected`、`edited` 之一，否則步驟失敗。同一個代號已經存在時不重建影片，只寫入字幕（標題必須相同，否則失敗）。寫入走產品的 artifact store，所以會觸發 SRCH-R2 的索引更新。同一個場景中依步驟順序建立的影音，`created_at` 嚴格遞增（fake clock 的現在加上序號毫秒；不靠 UUIDv7 的單調性），讓 SRCH-R7 的「新的在前」可以斷言 |
| `Given the search library has {string} titled {string} with the {string} transcript in {string}:` | 同上，第四個參數是原文語言（例如 `zh-Hant`）：影片的原文語言設為這個值，字幕也寫成這個語言 |
| `When the search runs for {string}` | 用 owner 的 session 送出 `GET /api/search?mode=fulltext&q=<參數>`，`q` 以 `url.QueryEscape` 編碼；回應成為最近一次的回應（`Then the response status is`、`Then the response contains:` 斷言它） |
| `When the search runs for the query:` | 同上，查詢是 doc string 的內容（照字面，只去掉結尾的一個換行），用於含 `"` 或很長的查詢；Scenario Outline 的 `<query>` 可以放在 doc string 中 |
| `Then the search index has no rows for {string}` | 以唯讀方式查詢 DB：`search_cues` 中沒有這部影音（影音 ID 取自代號對照表，永久刪除後仍是原本的 ID）的任何列，而且 `cues_fts` 的 `integrity-check` 通過（外部內容表與索引一致）。代號從未對應到影音 ID 時失敗 |
| `Then the search index has rows for {string}` | 以唯讀方式查詢 DB：`search_cues` 中至少有一列屬於這部影音；用來確認 `has no rows` 不是因為查錯資料表而成立 |

#### Bundle（BND）

bundle 一律在執行時由步驟產生（Go 的 `archive/zip`，必要時用 `CreateRaw` 自訂 header），不 commit 任何二進位 fixture。路徑參數套用 `/tmp/<x>` → `<場景根目錄>/tmp/<x>` 的對應。「合法 bundle」是固定內容（下表第一列），讓場景可以直接寫出產出的路徑與文字。

| 步驟 | 意思 |
|---|---|
| `Given the bundle {string} contains the item {string} titled {string}` | 產生一個合法的 bundle v0（BND-R6 的 manifest）：`item` 為代號對應的平台與 `external_id`（只接受 `yt:`，`part: null`，`original_lang: en`，`availability: public`），`created_by` 為 `{instance_id: "0190a1b2-0000-7000-8000-000000000001", app_version: "0.4.0"}`；兩個產出：`transcript`／`corrected`／`en`，id `0190a1b2-0000-7000-8000-0000000000a1`，`artifacts/0190a1b2-0000-7000-8000-0000000000a1.json`，兩句 cue（`00:00:01`–`00:00:03` `Bundled cue one.`、`00:00:03`–`00:00:06` `Bundled cue two.`），來源紀錄 `{executor: api, provider: anthropic, model: fixture-model}`；`note`，id `…0000000000a2`，`artifacts/0190a1b2-0000-7000-8000-0000000000a2.md`，內容 `# Bundled note\n\nNotes from another instance.\n`。`files` 依序列出這兩個檔案的 `path`、`sha256`、`size`。項目以 `deflate` 寫入，mode `0644` 的一般檔案。檔案先在記憶體中組成，後面的 `also has`、`has the manifest field`、`has the content of`、`has the file … replaced` 都改這份內容；第一個送出請求或執行指令的步驟之前才寫到磁碟 |
| `Given the bundle {string} also has the entries:` | 在 bundle 的最後加上項目。表格欄位：`path`（必填，照字面當作項目名稱，不做任何正規化）、`type`（預設 `file`）、`size`（預設 `16B`，可以用 `B`、`KiB`、`MiB`）、`fill`（`text`〔預設，重複的 `x`〕、`zeros`、`random`〔以 path 為種子的確定性亂數〕）、`target`、`declared_size`、`listed`（`yes`〔預設〕或 `no`）。`type`：`file`（一般檔案）、`dir`（目錄項目，名稱補上結尾的 `/`）、`symlink`（Unix mode `0120777`，內容是 `target`）、`char-device`（`0020644`）、`fifo`（`0010644`）、`socket`（`0140644`）、`hardlink`（另寫一個 central directory 項目，名稱為 `path`，local header offset 指向名稱為 `target` 的項目，不另寫資料）、`encrypted-file`（設定 general purpose flag 的加密位元，內容照寫）。`declared_size` 有值時，local header 與 central directory 宣告的解壓大小改成這個值（實際寫入的仍是 `size`）。`listed: yes` 的一般檔案同時加進 manifest 的 `files`（正確的 `sha256` 與實際 `size`），其他種類一律不加。`target` 指到不存在的項目、`type` 不認得時步驟失敗 |
| `Given the bundle {string} has the manifest field {string} set to:` | 把 manifest 中點號路徑（數字段落是陣列索引；索引等於陣列長度時附加一項；中間的物件不存在時建立）的值換成 doc string 解析出的 JSON；doc string 不是合法 JSON 時步驟失敗。不重新計算任何 `sha256` |
| `Given the bundle {string} has the content of {string} changed` | 把這個項目內容的最後一個 byte 加 1（mod 256），manifest 不變；項目不存在時步驟失敗 |
| `Given the bundle {string} has the file {string} replaced with:` | 這個項目的內容換成 doc string，manifest 的 `files` 中對應的 `sha256`、`size` 同時更新（所以只有內容格式的檢查會失敗）；項目不存在時步驟失敗 |
| `Given the bundle {string} is the text:` | 在路徑寫入 doc string 的內容（不是 zip） |
| `Given the bundle instance ID is {string}` | 這個實例的 `instance_id`（匯出 manifest 的 `created_by.instance_id`）設為指定的 UUID：在第一次啟動 server 之前寫入設定（測試用的 hook，實作切片決定存放位置），不送出請求 |
| `When the bundle {string} is imported` | 用 owner 的 session 送出 `POST /api/bundles/import`（multipart，欄位 `file`，檔名取路徑的最後一段，帶 CSRF token）；回應成為最近一次的回應 |
| `When the bundle {string} is imported with {string}` | 同上，第二個參數是 query string（例如 `"on_existing=add_versions"`），接在路徑的 `?` 後面 |
| `When the bundle {string} is imported with the token {string}` | 同上，但不帶 session，改用 bearer token（`Authorization: Bearer`） |
| `Then the bundle {string} passes its own checks` | 讀取磁碟上的 bundle，以和產品相同的規則（BND-R4～R7）驗證：所有項目名稱與種類合法、manifest 通過 schema、`files` 與 zip 雙向一致、每個檔案的 `sha256` 與 `size` 相符。驗證程式在 `features/steps` 中獨立實作，不呼叫產品的 `internal/bundle`（避免同一個錯誤兩邊都通過）。檔案不存在或不是 zip 時失敗 |
| `Then the bundle {string} lists the artifacts:` | manifest 的 `artifacts` 中，表格（欄位 `kind`、`variant`、`lang`）的每一列都有一個對應的產出；空白的 `variant` 代表產出沒有 `variant` 欄位，`*` 代表任何值。實際可以多出其他產出（seeding 可能另外建立上游變體）；被取代的版本不在 bundle 中由單元測試守住 |
| `Then the bundle {string} has the manifest values:` | 以點號路徑讀取 manifest（表格欄位 `field`、`value`），值一律以字串比較（數字用 JSON 的寫法，`null` 為 `null`） |
| `Then the bundle {string} contains no local paths` | bundle 中每個項目的名稱與內容（JSON 與 Markdown 都以原始 bytes 比對）都不含：場景根目錄的實際路徑、`/data/`、`/config/`、`/media/`、`/vault/`、inbox 的實際路徑、執行測試的使用者家目錄。任何一個出現就失敗，並列出項目名稱 |
| `Then the bundle {string} does not contain {string}` | bundle 中每個項目的名稱與內容都不含這段文字；bundle 沒有任何檔案項目時失敗（沒有東西可以驗證） |

#### 術語審核（GLOS）

| 步驟 | 意思 |
|---|---|
| `Given the term candidates {string} are pending for the channel of {string}` | seeding：第一個參數以逗號切開、去掉前後空白，每個詞在這部影音所屬頻道（natural key 的推導同 `the channel of … has the overrides:`）寫入一筆 `pending` 的術語候選，`item_id` 是這部影音；影音還沒新增時先新增並做到 `metadata`（同 `the note of … is:` 的 seeding）。比照 4.11 的語意，步驟只累積，第一個送出請求的步驟才寫入（在處理方案之後、影音的 seeding 之後）；`created_at` 依登記順序遞增（fake clock 的現在加上序號毫秒），讓 GLOS-R5 的排序可以斷言。寫入走產品的 store（與 C4 的 assemble 相同的清理與 UNIQUE），詞被清理掉或重複時步驟失敗 |
| `When the term candidate {string} of the channel of {string} is decided with:` | 以 owner 的 session 送出 `GET /api/channels/<頻道 ID>/term-candidates` 找到 `term` 完全相同的候選（找不到時步驟失敗），再送出 `PATCH /api/channels/<頻道 ID>/term-candidates/<候選 id>`，body 是 doc string 的 JSON；PATCH 的回應成為最近一次的回應 |
| `When the term candidates are decided in one batch:` | 表格欄位 `item`（代號，用來找頻道）、`term`（候選原本的詞）、`state`，可加 `decided_term`、`scope`（空白代表省略該欄位）。依序以 GET 找出每個候選的 id（同上），組成 `{decisions: [{candidate, state, term?, scope?}]}`，送出 `POST /api/term-candidates/decide`；回應成為最近一次的回應 |
| `Then the glossary {string} has exactly the terms {string}` | 參數是 `global` 或 `channel-of:<代號>`：送出 `GET /api/glossaries/global/default` 或 `GET /api/glossaries/channel/<頻道 ID>`，`terms` 必須與第二個參數（以逗號切開、去掉前後空白；空字串代表沒有任何詞）依序完全相等。不改寫最近一次的回應 |

### 4.21 進階登入與工作階段（`steps/signin.go`，M4）

設定類的步驟（`the reverse proxy … signs users in`、`allows the users`、`the OIDC provider … signs in`、`allows the subjects`）只累積，和 `Given the config file contains:` 的內容合併後寫成 `/config/notesteme.yaml`（同一個鍵兩邊都寫時步驟失敗）；server 在第一個送出請求的步驟或 `When the server starts` 時以合併後的設定啟動。

「指定來源位址」的做法同 `When someone signs in from {string} with the password {string}`（127.0.0.0/8 內的位址，TCP 連線本身就從這個位址發出，不是只加 `X-Forwarded-For`），所以 AUTH-R13 驗證的是**對端位址**。

#### 反向代理驗證（AUTH-06）

| 步驟 | 意思 |
|---|---|
| `Given the reverse proxy at {string} signs users in with the header {string}` | 設定：`trusted_proxies` 加上 `<IP>/32`，`auth.proxy.user_header` 設為第二個參數 |
| `Given the reverse proxy allows the users {string}` | 設定：`auth.proxy.allowed_users`（以逗號切開、去掉前後空白） |
| `When the reverse proxy at {string} forwards {string} with the header {string} set to {string}` | 從指定的來源位址送出請求（例如 `"GET /api/items"`），不帶 session 與 bearer token，加上指定的標頭；使用「proxy 瀏覽器」專用的 cookie jar（場景內共用，與 owner 的 session 分開），所以第一次請求設定的 cookie 會帶到之後的請求。非 GET 請求**不**帶 CSRF token。回應成為最近一次的回應 |
| `When the reverse proxy at {string} forwards {string} with the header {string} set to {string} and the CSRF token` | 同上，另外把 jar 中 `notesteme_csrf` 的值放進 `X-CSRF-Token`；jar 中沒有 `notesteme_csrf` 時步驟失敗 |
| `When the reverse proxy at {string} forwards {string} with the token {string} and the header {string} set to {string}` | 同上一個步驟，但另外帶 `Authorization: Bearer <token>`（token 以名稱查 `World.Refs`），不使用 cookie jar |
| `Then the auth log contains {string}` | server 的 log（場景開始後的所有紀錄）有一行包含這段文字 |

#### OIDC（AUTH-07）

| 步驟 | 意思 |
|---|---|
| `Given the OIDC provider {string} signs in the subject {string}` | 啟動假 IdP（`features/internal/fakeoidc`），以 fakenet 把參數中 URL 的主機（例如 `id.example.com`）導到它，discovery 的 `issuer` 就是這個字串；authorize endpoint 不顯示畫面，直接以這個 `sub` 同意登入。設定：`auth.oidc.issuer` 為這個字串、`client_id: notesteme`、`redirect_url: <測試 server 的 base URL>/api/auth/oidc/callback`；環境變數 `NOTESTEME_OIDC_CLIENT_SECRET` 設為 `fake-oidc-client-secret-7c1e`，假 IdP 只接受這個 secret（`client_secret_basic`）。假 IdP 的時間取自場景的 fake clock |
| `Given the OIDC provider allows the subjects {string}` | 設定：`auth.oidc.allowed_subjects`（以逗號切開） |
| `Given the OIDC provider issues ID tokens with {string}` | 假 IdP 之後簽發的 id_token 帶有指定的缺陷：`a bad signature`（簽章的最後一個 byte 被改）、`an unknown key`（用不在 JWKS 中的金鑰與 `kid` 簽章）、`no signature`（`alg: none`、簽章為空）、`an HMAC signature`（`HS256`，以 client secret 為金鑰）、`the wrong issuer`、`the wrong audience`、`the wrong nonce`、`an expiry 120s ago`、`an expiry 30s ago`、`an issue time 120s ahead`、`an issue time 30s ahead`（相對於 fake clock）。其他值時步驟失敗 |
| `Given the OIDC provider denies the sign-in with {string}` | authorize endpoint 改為以 `error=<參數>&state=<原本的 state>` 轉回 `redirect_uri` |
| `When the OIDC sign-in starts` | 用新的「OIDC 瀏覽器」cookie jar（不帶任何身分）送出 `GET /api/auth/oidc/start`，不跟隨轉址；回應成為最近一次的回應 |
| `When the OIDC sign-in starts with the next page {string}` | 同上，帶 `?next=<參數>`（`url.QueryEscape` 編碼） |
| `When the OIDC sign-in is completed` | 取最近一次 start 回應的 `Location`，在程序內呼叫假 IdP 的 authorize endpoint（不經過產品），再把它轉回的 callback URL 用同一個 jar 送給產品（不跟隨之後的轉址）；callback 的回應成為最近一次的回應。最近一次 start 不是 302 或 `Location` 不是假 IdP 的 authorize endpoint 時步驟失敗 |
| `When the OIDC sign-in is completed with the state {string}` | 同上，但送給產品之前把 callback URL 的 `state` 換成參數 |
| `When the OIDC callback is replayed` | 用同一個 jar 再送一次最近一次的 callback URL |
| `Then the OIDC authorization request has the parameter {string} set to {string}` | 最近一次 start 回應的 `Location` 中，這個 query 參數只出現一次且值完全相同；值為 `"*"` 時只要求存在且不是空字串 |
| `Then the OIDC sign-in redirects to {string}` | 最近一次回應是 302，`Location` 與參數完全相同 |
| `Then the OIDC callback set no session cookie` | 最近一次回應的 `Set-Cookie` 中沒有 `notesteme_session`（或只有清除用的空值），jar 中也沒有有效的 `notesteme_session` |
| `Then the OIDC token request used PKCE with {string}` | 假 IdP 收到的最近一次 token 請求：authorize 時的 `code_challenge_method` 是參數（例如 `S256`），`code_verifier` 依該方法計算後與 `code_challenge` 相同，而且以 `client_secret_basic` 帶了正確的 client secret |
| `When the OIDC session sends {string}` | 用「OIDC 瀏覽器」的 jar 送出請求；非 GET 請求帶 jar 中的 CSRF token |

#### 工作階段與 token（AUTH-08）

| 步驟 | 意思 |
|---|---|
| `Given the session {string} is signed in from {string}` | 以場景的登入密碼（`NOTESTEME_PASSWORD` 的測試值），從指定的來源位址送出 `POST /api/auth/login`，回應必須是 200；cookie 存在以名稱區分的 jar（與 owner 的 session 分開），也記下 `notesteme_session` 的值供 `the session list does not contain …` 使用 |
| `When the session {string} sends {string}` | 用這個名稱的 jar 從同一個來源位址送出請求；非 GET 請求帶 jar 中的 CSRF token；回應成為最近一次的回應 |
| `When the session {string} is revoked by the owner` | 先用這個 session 的 jar 送出 `GET /api/auth/sessions`，取 `current: true` 那一筆的 `id`（不改寫最近一次的回應），再用 owner 的 session 送出 `DELETE /api/auth/sessions/<id>`；DELETE 的回應成為最近一次的回應 |
| `Then the session list does not contain the secret of the session {string}` | 最近一次回應的原始 body 不含這個 session 的 `notesteme_session` 值，也不含它的 sha256（十六進位與 base64url 兩種寫法） |
| `When the token {string} is rotated as {string}` | 用 owner 的 session 送出 `POST /api/tokens/<第一個參數>/rotate`；回應是 200 且有 `token` 時，把新的明文記為 `World.Refs["token:"+第二個參數]`（之後 `with the token {string}` 用第二個參數），原本名稱的 `Refs` 不變（所以用第一個參數送出的請求帶的是舊 secret）。回應成為最近一次的回應 |
### 4.30 Hub 實例與帳號（`steps/hub.go`，M5）

M5 的句型（4.30～4.32）在 2026-11-23 規格展開時加入，目前全部註冊為 pending 的步驟樁（`godog.ErrPending`），由 M5 的實作切片換成真正的實作。M5-S1（2026-11-23）實作了 4.30 的閘門、Hub 設定、假供應商、登入流程、帳號數、`the response does not contain` 與送到 Hub 的 session／匿名請求；閘門只對登入範圍的 HACC 場景放行（HACC 待決問題 A11）。Hub token、群組、停用與 MCP 的句型，以及 4.31、4.32 仍是步驟樁。測試替身的設計見 [ADR-0030](../adr/0030-hub-deployment-and-storage.md) 第 3 點與 `features/fixtures/README.md` 的「M5 的假服務」。

M5 的領域用語：

| English | 繁中 | 說明 |
|---|---|---|
| Hub | Hub | 分享字幕與筆記的資源站（`notesteme-hub`，M5） |
| hub member | Hub 使用者 | 登入 Hub 的人；場景中以名稱（例如 `"alice"`）指稱 |
| hub admin | Hub 管理員 | `admins` 群組的成員 |
| identity | 登入方式 | 一個 OAuth 供應商的帳號，以 `(provider, subject)` 識別 |
| Hub token | Hub token | Hub 的 PAT（`nsh_…`），權限是 Hub 的 `read`、`download`、`regenerate`、`upload`、`moderate`、`admin` |
| share | 分享 | 一次上傳到 Hub 的 bundle |
| version | 版本 | Hub 上一部影片的一份產出內容，以 `(video, kind, variant, lang, sha256)` 識別 |
| derivation | 衍生關係 | 版本之間的 `parent` 或 `regenerated_from` |
| takedown request | 下架申請 | 要求移除版本的申請，不需要帳號 |
| lookup | ASR 前查詢 | Client 以影片 ID 雜湊的前綴查詢 Hub（HSHR-R13） |

規則：

- **閘門**：每個 M5 的 `.feature` 在 Background 的第一步是閘門句型，二選一：`Given a Hub is running`（真 Hub，HACC、HREP、HMCP）或 `Given the owner's client is connected to a Hub`（fakehub，HSHR）。一個 `.feature` 只用其中一個（ADR-0030 第 3 點）。閘門實作之後，場景會往下跑到 Client 的設定驗證：Client 的設定檔與處理方案在產品實作之前不認得 `hub:` 區段，所以 M5 的第一個實作切片（M5-S0）先讓 Client 的設定 schema 接受 `hub.url`、`hub.precheck`，Client 的 M5 路由（`/api/settings/hub`、`/api/items/:id/hub/share`、`/api/hub/import`）回 501 stub（同 M2 S0、M3-S0），之後各切片以 `@M5` 的逐場景結果（junit）和切片開始前比對。
- **兩邊共用的 seeding**（`Given the Hub has the version … with:`、`Given the Hub version … has the cues:`）寫進 Background 啟動的那一個 Hub；兩個都沒有或兩個都有時步驟失敗。
- **代入**：請求路徑、請求 body 與 CLI 指令參數中的 `{version:<名稱>}` 換成該版本在 Hub 上的實際 ID（名稱來自 `the Hub has the version …` 或 `… is known as …`），同 `{token:<名稱>}`；`{user:<名稱>}` 換成這位 Hub 使用者的帳號 ID；`{takedown:last}` 換成最近一次建立的下架申請 ID，`{report:last}`、`{share:last}` 同理（`{share:last}` 是最近一次 `shares the bundle` 成功時回應的 `share_id`）。預期結果的 JSON 不展開。
- **最近一次回應**：送到 Hub 的請求（`the hub member … sends`、`the Hub client sends`、登入流程的步驟）和送到 Client 的請求共用「最近一次回應」，所以 `Then the response status is {int}`、`Then the response contains:`、`Then the response header {string} contains {string}` 都可以斷言 Hub 的回應。
- **時間**：真 Hub 使用場景的 fake clock（`Given the clock is at`、`When the clock advances by`），帳號齡、state 的期限、PAT 的期限與限流都以它計算。
- **網路**：真 Hub 的對外連線走場景的 fakenet；`fakeoauth` 以真的主機名稱登記（`accounts.google.com`、`oauth2.googleapis.com`、`openidconnect.googleapis.com`、`www.googleapis.com`、`github.com`、`api.github.com`、`connect.linux.do`；Google 的名稱實作時對照官方文件），所以 `Then no connection was made to {string}` 也可以驗證 Hub（HREP-R12）。
- **登入**：`"<名稱>"` 是一個人，在每個供應商都可以有一個同名的使用者；沒有用 `the OAuth provider … has the user …` 定義時，第一次使用就以預設值建立：`subject` 為 `<供應商>-<名稱>`（Google 的 `sub` 就是這個字串；GitHub 與 Linux.do 的使用者資訊跟真實 API 一樣回傳數字 `id`，由 `fakeoauth.NumericID(subject)` 推導，所以設定檔的 `admins` 要指定 GitHub、Linux.do 的身分時，寫數字形式的 `subject`）、`login` 為名稱、`email` 為 `<名稱>@example.com` 且 `email_verified: true`、`trust_level: 0`、沒有 `orgs`。Hub 使用者的 session 以名稱保存，最後一次成功登入的 session 生效。
- Hub 的預設設定（沒有 `the Hub config contains:` 時）：`public_url: https://hub.example.org`，三個供應商都設定了測試用的 `client_id` 與 secret，`site.visibility: authenticated`，其他依各規格的預設值。

| 步驟 | 意思 |
|---|---|
| `Given a Hub is running` | 閘門：以目前累積的 Hub 設定在程序內啟動真 Hub（`internal/hub`，SQLite 與 fs blob 放在場景目錄的 `hub/`），並在 fakenet 登記 `hub.example.org` 與三個 `fakeoauth` 供應商 |
| `Given the Hub config contains:` | Hub 設定檔（`notesteme-hub.yaml`，YAML）的內容，和預設設定合併（物件逐鍵合併，陣列整個取代）；必須在第一個送到 Hub 的請求之前，之後的步驟只累積，Hub 在下一個請求時以新設定重新啟動（資料保留） |
| `Given the OAuth provider {string} has the user {string} with:` | 假供應商（`google`、`github`、`linuxdo`）的使用者；表格欄位 `field`、`value`：`subject`、`login`、`email`、`email_verified`（`true`／`false`）、`trust_level`（Linux.do）、`orgs`（GitHub，以逗號分隔） |
| `Given the OAuth provider {string} denies the next sign-in` | 下一次 authorize 時，假供應商以 `error=access_denied` 轉回 callback |
| `When {string} starts signing in to the Hub with {string}` | `GET /auth/<供應商>/start`，不跟隨轉址；記下 302 回應（`Location` 是假供應商的 authorize URL）與 `notesteme_hub_oauth` cookie，等待之後的步驟完成這次登入 |
| `When the OAuth provider {string} completes the pending sign-in` | 以這個人在該供應商的使用者完成等待中的 authorize，帶 cookie 打開供應商轉回的 callback；最近一次回應是 Hub 對 callback 的回應。成功時保存 session |
| `When the pending sign-in returns with a different state` | 同上，但把 callback 的 `state` 換成另一個隨機值（cookie 不變） |
| `When the last sign-in callback is replayed` | 帶同一個 cookie 再打開一次最近一次的 callback URL（同一個 `code` 與 `state`） |
| `When {string} signs in to the Hub with {string}` | `starts signing in` 加上 `completes the pending sign-in`；失敗時步驟本身不失敗，用 `Then the response …` 斷言 |
| `Given the hub member {string} signed in with {string}` | 同上，但登入沒有成功時步驟失敗（前提用） |
| `When the hub member {string} links the {string} user {string}` | 用這位 Hub 使用者的 session 送出 `POST /api/v1/me/identities/<供應商>/link`，以第三個參數指稱的假供應商使用者完成 authorize 與 callback；最近一次回應是 link 請求失敗時的回應，否則是 callback 的回應 |
| `Then the OAuth provider {string} received a valid PKCE verifier` | 假供應商最近一次 token 請求帶有 `code_verifier`，而且 `BASE64URL(SHA256(verifier))` 等於對應 authorize 請求的 `code_challenge`、`code_challenge_method` 為 `S256` |
| `Then the number of Hub accounts is {int}` | Hub 的帳號總數（唯讀查詢 Hub 的 DB） |
| `Then the response does not contain {string}` | 最近一次回應的 body（原始文字，不解析）不含這段文字；用於「沒有洩漏」的斷言。最近一次回應的 body 是空的時候失敗（不能證明「不含」） |
| `When the hub member {string} sends {string}` | 用這位 Hub 使用者的 session 送到 Hub 的請求；非 GET 請求自動帶 CSRF token |
| `When the hub member {string} sends {string} with:` | 同上，帶 JSON body |
| `When the hub member {string} sends {string} without a CSRF token` | 用 session 送出請求，但不帶 `X-CSRF-Token` |
| `When the Hub client sends {string}` | 不帶任何身分、送到 Hub 的請求 |
| `When the Hub client sends {string} with:` | 同上，帶 JSON body |
| `Given a Hub token {string} for {string} with the permissions {string}` | 以這位 Hub 使用者的 session 送出 `POST /api/v1/me/tokens`（權限以逗號分隔），保存回應中的 token；請求沒有成功時步驟失敗 |
| `When the Hub client sends {string} with the Hub token {string}` | 帶 `Authorization: Bearer <Hub token>` 的請求 |
| `When the Hub client sends {string} with the Hub token {string} and:` | 同上，帶 JSON body |
| `When the Hub client sends {string} with the Hub token {string} from the origin {string}` | 同上，加上 `Origin` 標頭；用於 HMCP-R2（`POST /mcp` 時 body 是 MCP 的 `initialize` 請求） |
| `Given the hub member {string} is in the group {string}` | 直接在 Hub 的 DB 以 `source: manual` 把這位使用者加入群組（`admins` 以 `source: config`）；使用者必須已經登入過 |
| `Given the hub member {string} is suspended` | 直接在 Hub 的 DB 停用這位使用者 |
| `When the Hub MCP client with the Hub token {string} lists the tools` | 對 Hub 的 `/mcp` 呼叫 `tools/list`（同一個 token 在場景中只 `initialize` 一次，之後每個呼叫是一個 HTTP 請求；Hub 重新啟動後重新 `initialize`）；`Then the listed names include …` 等 4.7 的步驟斷言它 |
| `When the Hub MCP client with the Hub token {string} calls {string} with:` | 對 Hub 的 `/mcp` 呼叫 tool，參數為 JSON；`Then the tool result contains:`、`Then the tool call fails with {string}` 斷言它 |

### 4.31 Hub 的內容（`steps/hub_content.go`，M5）

bundle 由步驟在場景目錄中產生，不 commit 二進位 fixture：字幕內容依列的順序產生確定的 cues（每個 `content` 值對應一份固定的內容，兩列的 `content` 相同就產生完全相同的 bytes），筆記與章節同理。

| 步驟 | 意思 |
|---|---|
| `Given the Hub has the version {string} of {string} with:` | seeding：直接在 Hub（真 Hub 的 DB 與 blob，或 fakehub 的記憶體）建立一個 `visible` 的版本，名稱是第一個參數；第二個參數是影音代號（`yt:`、`bili:`、`pod:`）。表格欄位 `field`、`value`：`kind`（預設 `transcript`）、`variant`（預設 `source`）、`lang`（預設 `en`）、`uploader`（Hub 使用者名稱，預設 `uploader`；沒有登入過時直接在 DB 建立這位使用者與一個 `github` 身分；fakehub 只記名稱）、`transcribe_model`、`correct_model`、`translate_model`、`model_verified`（預設 `true`）、`visibility`（預設依 Hub 的 `content_defaults`，HACC-R14：`frames` 是 `private`，其他跟隨站台）、`license`（預設 `CC-BY-4.0`）、`score`（預設 0）、`channel`（頻道的 `external_id`）、`content`（同上的內容名稱；沒有 `content` 也沒有 `the Hub version … has the cues:` 時，依版本名稱產生 3 句確定的 cues）、`state`（預設 `visible`；`removed` 同時寫入 `removed_hashes`，只用於真 Hub） |
| `Given the Hub version {string} has the cues:` | 指定這個版本的字幕內容；表格欄位同 4.4 的 `#`、`start`、`end`、`text`。版本的 `sha256` 依內容重新計算 |
| `Given a bundle {string} of {string} with the artifacts:` | 在場景目錄產生一個 bundle v0（BND 規格），名稱是第一個參數，影音是第二個參數；manifest 的 `item` 預設 `title: Fixture Video`、`availability: public`、`original_lang: en`、`license: CC-BY-4.0`，`created_by` 只有 `app_version`。表格每列一個產出，欄位：`kind`、`variant`、`lang`，可加 `content`、`source`、`executor`（預設 `api`）、`provider`、`model`、`model_verified`、`cookie_required`、`parents`（同一個 bundle 中其他列的 `<variant>:<lang>`，以逗號分隔，寫成 `parents_sha256`）、`hub_origin`（Hub 版本的名稱，寫成 `hub_origin.{version_id,sha256}`）。欄位留空或是 `<absent>` 時不寫進來源紀錄。影音代號可以是 `local:`（manifest 的 `platform` 為 `local`，用來驗證 Hub 拒絕本機檔案） |
| `Given the bundle {string} has the manifest fields:` | 覆寫 manifest 的欄位；表格欄位 `field`（點號路徑，例如 `item.availability`、`license`、`artifacts.0.provenance.extra`）、`value`（字串；`<absent>` 代表刪除這個欄位；`<repeat:<字元>:<次數>>` 產生重複的字串，例如 `<repeat:x:17408>`，用來觸發大小上限） |
| `Given the bundle {string} has a note containing:` | 在 bundle 加上一個 `note` 產出（`lang: en`），內容是 doc string（Markdown） |
| `Given the bundle {string} is made unsafe with {string}` | 讓 bundle 違反 BND 的一條規則：`an absolute path`（加入 `/etc/notesteme` 項目）、`a parent directory entry`（`artifacts/../x`）、`a symlink`（Unix mode 為 symlink 的項目）、`a sha256 mismatch`（改動一個產出檔案的內容）、`a media file`（加入 `artifacts/<id>.mp4`，內容以 MP4 的 `ftyp` 簽章開頭，並列在 manifest 中） |
| `When the hub member {string} shares the bundle {string}` | 用 session 送出 `POST /api/v1/shares`（multipart：`bundle`、`attest=true`） |
| `When the hub member {string} shares the bundle {string} with:` | 同上，multipart 的其他欄位由表格（`field`、`value`）指定或覆寫，例如 `attest`、`visibility`；`value` 為 `<absent>` 時不送這個欄位 |
| `Given the Hub version of {string} for {string} is known as {string}` | 給 Hub 上的版本一個名稱：第一個參數是影音代號，第二個是 `<kind>:<variant>:<lang>`（例如 `transcript:corrected:en`；`note` 與 `chapters` 寫成 `note::en`）；這樣的版本必須剛好一個（任何狀態）。之後可以用 `{version:<名稱>}` 指稱它 |
| `Then the Hub has {int} versions of {string}` | Hub 上這部影片的版本數（任何狀態；唯讀查詢 DB） |
| `Then the Hub version {string} is {string}` | 版本的狀態：`visible`、`hidden`、`removed`、`withdrawn`（唯讀查詢 DB） |
| `Then the Hub version {string} is derived from {string} as {string}` | Hub 的 `derivations` 中有一筆 child 為第一個版本、parent 為第二個版本、`relation` 相符（`parent`、`regenerated_from`）的紀錄 |
| `Then the Hub stores the content {string} once` | Hub 的 blob 中，這個內容名稱的 sha256 剛好一個檔案，而且引用它的版本至少一個 |

### 4.32 Client 與 Hub（`steps/hub_client.go`，M5）

這一組只用於 fakehub（`features/internal/fakehub`）：Client 的 `hub.url` 指到 `https://hub.example.org`，fakehub 以這個主機名稱掛在 fakenet 上，實作 ADR-0032 第 1 點的 Client 契約，並記錄收到的每個請求（方法、路徑、query、標頭、body；multipart 的 bundle 會解開保存）。

| 步驟 | 意思 |
|---|---|
| `Given the owner's client is connected to a Hub` | 閘門：啟動 fakehub；Client 的設定檔加上 `hub.url: https://hub.example.org`（設定檔已經有 `hub.url` 時不覆寫）；server 啟動後以 `PUT /api/settings/hub` 設定 token（fakehub 接受這個 token，權限預設 `read,download,regenerate,upload`） |
| `Given the client's Hub token has the permissions {string}` | fakehub 對 Client 的 token 只授予這些權限（以逗號分隔）；缺少的權限回 403，帶 `required_permission` |
| `Given the client's Hub token is not set` | server 啟動後不設定 token（`GET /api/settings/hub` 的 `token_set` 為 `false`） |
| `Given the Hub fails with HTTP {int}` | fakehub 對所有請求回應指定的狀態碼（429 時附 `retry_after_s: 30` 與 `Retry-After: 30`） |
| `Given the Hub does not respond` | fakehub 收到請求後不回應，直到 Client 逾時；Client 的逾時以 fake clock 判斷（HSHR 待決問題 S7），場景不真的等待 |
| `Given the Hub serves tampered content for the version {string}` | `GET /api/v1/versions/:id/content` 回傳和版本 `sha256` 不符的內容（仍然是合法的 cues JSON） |
| `Then the Hub received {int} requests` | fakehub 收到的請求總數（設定 token 不會連線到 Hub，HSHR-R1，所以閘門不產生請求） |
| `Then the Hub was asked for a lookup {int} times` | fakehub 收到的 `GET /api/v1/lookup` 次數 |
| `Then the Hub received a share of {string} with the artifacts:` | fakehub 最後收到的分享是這部影片的，bundle 中的產出（不分順序）正好是表格列出的這些；表格欄位 `kind`、`variant`、`lang`（`note`、`chapters` 的 `variant` 留空） |
| `Then the last bundle sent to the Hub has the manifest fields:` | fakehub 最後收到的 bundle 的 manifest；表格欄位 `field`（點號路徑，數字是陣列索引）、`value`（字串比較；`<absent>` 代表欄位不存在；`*` 代表欄位存在即可，值可以是空陣列或空字串） |
| `Then the last bundle sent to the Hub does not contain {string}` | fakehub 最後收到的 bundle 中，manifest 與每個檔案的內容都不含這段文字；沒有收到任何 bundle 時失敗 |
| `Then the Hub never received the video ID of {string}` | fakehub 收到的所有請求（路徑、query、標頭、body，不含分享的 bundle）都不含這部影片的平台 ID；沒有收到任何請求時失敗（沒有東西可以驗證） |

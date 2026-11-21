# ING：新增影音

- 狀態：ready
- 里程碑：M1（MCP 的 `ingest` 在 M2，見 MCP）
- 相關：plan 第 3.3、5、7 節；UI [新增](../ui/pages.md#2-新增)
- 驗收場景：[`features/ING-ingest.feature`](../../features/ING-ingest.feature)

## 目的

把網址或檔案交給 Notesteme，建立影音並開始處理。這是最常用的操作，所以送出前就要讓使用者知道會處理哪些內容、大約花多少錢、有沒有需要先處理的事。

## User stories

| ID | Story |
|---|---|
| ING-01 | As the owner, I want to paste video URLs and have them processed, so that I get subtitles without running any tools by hand. |
| ING-02 | As the owner, I want Notesteme to tell me what a URL points to before I submit it, so that I know exactly what will be processed. |
| ING-03 | As the owner, I want to choose which parts of a multi-part Bilibili video to process, so that I do not pay for parts I do not need. |
| ING-04 | As the owner, I want to pick episodes from a podcast feed, so that I can process a single episode without subscribing. |
| ING-05 | As the owner, I want to add local media by uploading it or dropping it into an inbox folder, so that files I already have get subtitles too. |
| ING-06 | As the owner, I want adding something that is already in my library to point me to it instead of duplicating it, so that I never pay twice for the same item. |
| ING-07 | As the owner, I want to adjust settings for a single run without editing my profile, so that one-off needs do not change my defaults. |
| ING-08 | As the owner, I want to see an estimated cost and any required cookies before I start, so that there are no surprises after I submit. |

## 規則

| ID | 規則 |
|---|---|
| ING-R1 | 辨識的網址種類：YouTube 影片（`watch`、`youtu.be`、`shorts`）（影片 ID 為 11 個字元）、YouTube 播放清單、YouTube 頻道（`@handle`、`/channel/`）、Bilibili 影片（`BV` 號，可帶 `p`）、Bilibili 個人空間、Podcast RSS feed。YouTube 與 Bilibili 只看網址；其他網址透過對外 HTTP client（GRD）實際抓取，內容是 RSS 才辨識成 Podcast feed。其他網址回傳「無法辨識這個網址。目前支援 YouTube、Bilibili、Podcast RSS 與本機檔案。」 |
| ING-R2 | 影音以 `(platform, external_id, part)` 識別。新增已存在的影音不會建立新項目，回應標示 `already_in_library` 並附上影音 ID。影音在垃圾桶中時見 DEL-R10。 |
| ING-R3 | Bilibili 每個分P 是一個影音。預設新增全部分P，也可以只指定部分分P。 |
| ING-R4 | YouTube 播放清單在新增時展開成多部影音，每部各自套用 ING-R2。展開結果是一次性的，不會持續追蹤（持續追蹤是 M4 的訂閱）。 |
| ING-R5 | 頻道與 Bilibili 個人空間不能直接新增。辨識結果標示 `subscribable: true`；直接送出時回傳 422 與「這是頻道。要追蹤新內容，請建立訂閱。」 |
| ING-R6 | Podcast feed 辨識後列出各集，新增時必須指定 `episodes`；沒有指定時回傳 422。 |
| ING-R7 | 單次覆寫只影響這次新增的影音，記錄在影音的 `overrides` 中，不修改處理方案。 |
| ING-R8 | 估算花費不會建立任何影音。估算結果包含預估金額，以及預計使用的字幕來源；平台有可用字幕時，不把 ASR 費用算進去。 |
| ING-R9 | 需要 cookie 的內容（`availability` 為 `needs_auth`、`subscriber_only`、`premium_only`，或 Bilibili AI 字幕）在辨識時就標示 `requires_cookie`，並附上設定頁的連結。 |
| ING-R10 | inbox 中的檔案在大小連續 10 秒不變後才新增，避免處理到還在複製中的檔案。內容 `sha256` 相同的檔案不重複新增。 |
| ING-R11 | inbox 與上傳只接受 ffprobe 能辨識出音訊或影片串流的檔案；其他檔案留在原處並記錄原因，不建立影音。 |
| ING-R12 | 新增後立即建立 `metadata` 任務，優先順序為「手動新增」（最高，見 PIPE）。 |

## 介面

- API：`POST /api/resolve`、`POST /api/estimate`、`POST /api/items`（JSON 或 multipart 上傳）
- CLI：`notesteme ingest <url|path> [--profile p] [--until <stage>] [--json]`
- MCP（M2）：`ingest`
- UI：[新增抽屜](../ui/pages.md#2-新增)

`POST /api/items` 的主要欄位：

```json
{
  "url": "https://www.bilibili.com/video/BV1xx?p=1",
  "parts": [1, 3],
  "episodes": ["guid-1"],
  "profile": "standard",
  "overrides": { "language": { "targets": ["en"] } },
  "until": "transcript"
}
```

YouTube 播放清單（ING-R4）展開時由 yt-dlp 列出清單內容：清單不存在、私人或沒有可用影片時回 422；yt-dlp 其他失敗回 502，code 為 `upstream_failed`，yt-dlp 的輸出只寫進 log，不回傳給 client。整份清單在同一個交易中建立，失敗時不留下任何影音。

## 不在範圍內

- 建立訂閱（M4，見故事地圖 SUB）。
- 直播中的內容：新增後在 `metadata` 判定為 `wait`，等 VOD 出來（見 SRC）。

## 待決問題

- 上傳檔案的大小上限，暫定 4GB，可在設定檔調整。LOCAL 切片解讀為 4 GiB（4294967296 bytes），設定鍵 `library.upload_max_bytes`；超過時回 413 `upload_too_large`（2026-11-21，待 owner 確認）。
- inbox 的穩定判定時間暫定 10 秒，是否需要依檔案大小調整，M1 實作時觀察。LOCAL 切片的實作：每 5 秒掃描一次；第一次看到檔案時，以「修改時間與現在」較早者起算，之後大小或修改時間改變就重新起算，連續 10 秒不變才新增（2026-11-21）。每輪最多處理 10,000 個還沒判定的檔案、最多走訪 100,000 個項目（目錄、被略過的檔案與 symlink 都算，否則大量被略過的項目會讓每輪掃描走完整棵樹）；已經判定而且大小與修改時間都沒變的檔案不算進處理上限，所以超過上限的檔案會在之後的掃描輪到。超過任一上限時清單不完整，這一輪不刪除消失檔案的紀錄。
- ING-05 的細節（2026-11-21，LOCAL 切片實作時決定，待 owner 確認）：
  - 識別：本機檔案的 `platform` 為 `local`，`part` 為 0。inbox 中的檔案以相對 inbox 的路徑為 `external_id`；`media_roots` 中或 inbox 之外的檔案以解析後的絕對路徑為 `external_id`；上傳的檔案以清理過的檔名為 `external_id`，與既有影音撞名時在副檔名前加 `-2`、`-3`。內容的 `sha256` 另外記在影音上。
  - 去重（ING-R10）：inbox 與上傳都以 `sha256` 去重。上傳時相同內容已存在回 200 `already_in_library`，在垃圾桶中回 409；inbox 遇到相同內容只記錄，不建立影音。同一個 inbox 路徑的內容被換掉時，記為衝突，不自動重新處理。
  - ING-R11 的「記錄原因」：寫入資料庫（`inbox_entries.reason`）與 log，API 之後再公開。上傳不是影音時回 422 `unsupported_media`。
  - 用 `path` 新增（API、CLI client 模式）不在新增時檢查檔案是否存在或是不是影音，只檢查路徑是否在根目錄內（GRD-R7）；檔案不存在時，`metadata` 步驟以 `permanent` 失敗，訊息「找不到這個檔案。」。所以 GRD-R7 的場景中不存在的 `/data/inbox/talk.mp4` 仍回 201。同時給 `url` 與 `path` 回 422。
  - inbox 自動新增遇到 tombstone（DEL-R11）時不建立影音，也不移除 tombstone；手動新增與上傳照舊移除同一個鍵的 tombstone，上傳另外移除 `sha256` 相同的 tombstone。
  - 上傳的檔名與 inbox 頂層的檔案共用同一個命名空間：選上傳的鍵時，跳過已存在的影音與 inbox 頂層的同名項目。反過來，inbox 中之後才出現、與既有上傳同名的檔案會記為衝突，不會新增（要改名才能新增），記為技術債。
  - 上傳的資源限制（2026-11-21，審查後決定）：每次讀取的閒置逾時 2 分鐘；同時最多 2 個上傳，超過回 429 `too_many_uploads`「目前有其他上傳正在進行，請稍後再試。」；multipart 最多 16 個 part；`until` 盡早驗證，不等檔案傳完。
- 播放清單（ING-R4）展開的項目數目前沒有上限，整份清單在一個交易中建立。是否需要上限、超過時要截斷還是拒絕，等 ING 切片決定（2026-11-13，GRD 切片實作 ING-R4 時發現）。
- 播放清單展開時 yt-dlp 的錯誤訊息（例如 `The playlist does not exist.`）是依 yt-dlp 原始碼判斷的，還沒有對真實 yt-dlp 驗證；ING 切片做 `@manual` 煙霧測試時確認。
- ING-R3 的細節（2026-11-14，SRC-B 切片實作時決定）：
  - 網址沒有 `p` 時，以 yt-dlp `--flat-playlist -J` 展開全部分P，同一個交易建立；只有一個分P 時結果是 `p1`。`Recognize` 對沒有 `p` 的網址回傳 `Part = 0`（代表「未指定」）；影音的短代號仍是 `bili:<BV>/p<n>`（`Ref` 以 `p1` 起算）。
  - 網址有 `p=N` 時，只建立那一個分P，不呼叫 yt-dlp，所以 `N` 超出範圍要等處理時才知道。
  - 網址已有 `p` 又給 `parts` 回 422；`parts` 的編號小於 1、或超出影片的分P 範圍也是 422；`parts` 中重複的編號去掉，結果依分P 順序。
  - 展開失敗沿用 ING-R4 的對應：影片不存在或地區限制回 422；找不到 yt-dlp、要求登入與其他失敗回 502（`upstream_failed`），yt-dlp 的輸出只寫進 log。
  - Bilibili 的依賴沒有設定（程式沒有接線）時，新增 Bilibili 影片仍回 501。
- Podcast（ING-R1、R2、R6，2026-11-21，PODCAST 切片實作時決定，待 owner 確認；決策見 ADR-0017）：
  - 每一集是 `(podcast, "<feed key>/<guid>", 0)`，代號 `pod:<feed key>/<guid>`。feed key 由 feed 網址最後一段推導（去副檔名、轉小寫、非英數換成 `-`，遇到 `rss`、`feed` 等泛用名稱往前取一段），存在 `channels` 中；不同 feed 撞名時加 `-2`。`items.url` 存正規化的 feed 網址（主機根目錄的 feed 補上結尾的 `/`；http 與 https、搬家的 feed 視為不同的 feed，不合併）。
  - guid 是去掉前後空白後的 `<guid>`，沒有 guid 時用音檔網址；同一個 feed 中重複的 guid 以第一個為準。接受 RSS 與 Atom。feed 中的相對網址（音檔、逐字稿、連結）以實際抓到 feed 的網址（轉址之後）解析成絕對網址；`xml:base` 不支援；沒有 guid、改用音檔網址當 guid 時，guid 保留 feed 中的原始字串。feed 的編碼支援 UTF-8 與 Windows-1252（宣告 ASCII、ISO-8859-1 的也以 Windows-1252 解碼，同 WHATWG），其他編碼視為無法辨識的網址（技術債：需要時引入 `golang.org/x/text`）。
  - 新增時沒有 `episodes` 回 422 `invalid_input`「這是 Podcast feed，請在 episodes 指定要新增的集。」；找不到的 guid 回 422「feed 中找不到這一集：<guid>。」；沒有音檔回 422「這一集沒有音檔：<guid>。」；帶 `parts` 回 422「Podcast 沒有分P 可以選擇。」。抓 feed 失敗比照辨識回 422 `unrecognized_url`。
  - 新增時對選中各集的音檔網址做一次只解析、不連線的檢查（`netguard.CheckURL`）。全部位址都被擋時，新增就回 422 `blocked_address`；DNS 失敗只記 log，留給 metadata 處理。真正的防護仍在連線時（GRD-R2）。
  - 「沒有指定集」的場景只斷言 422，現有程式回 `unrecognized_url` 也會通過；正確的錯誤碼由單元測試把關。建議在場景補上 `the response contains`，需要 owner 同意。
  - 辨識只列出前 500 集，新增可以指定 feed 中的任何一集。CLI 沒有 `--episodes`。
- 估算花費（ING-R8，2026-11-21，ESTIMATE 切片實作時決定，待 owner 確認）：
  - `POST /api/estimate` 的 body 與 `POST /api/items` 相同（`url`、`parts`、`episodes`、`overrides`；`until` 忽略；`profile` 比照新增回 501）。辨識與展開共用新增的流程，錯誤碼也相同；不寫入資料庫，Podcast 也不建立 feed key 的頻道。本機檔案（`path`）還不支援，回 422 `invalid_input`。需要 `ingest` scope。
  - 回應：`items[{ref, title, duration_s, expected_source, asr_usd, source_usd, llm_usd, total_usd, requires_cookie, already_in_library, notes}]`、`total_usd`、`currency`（`USD`）、`prices_as_of`、`notes`。金額為 `null` 代表不知道，`0` 代表確定不用錢；`total_usd` 只加總已知的金額，有未知金額時 `notes` 帶「部分金額未知」。
  - 每部影音以平台的 Probe 取得字幕軌與長度，依生效的 `language` 用與 transcript 步驟相同的規則選軌；選到平台字幕（或 Podcast 的 `<podcast:transcript>`）時 `asr_usd` 為 0。沒選到時，依 `fallback` 的順序找第一個有設定（實際查詢 API key，沒有 key 的來源略過，與處理時記為 `not_configured` 一致）、支援這個平台的來源：Groq 以長度乘上內建單價（`whisper-large-v3-turbo` 0.04、`whisper-large-v3` 0.111 USD／音訊小時）；Supadata 以 credit 計價，`source_usd` 為 `null`。都沒有時 `expected_source` 為 `null`。
  - Podcast 的長度是 feed 宣稱的 `itunes:duration`，不下載音檔；實際費用以下載後量到的長度為準。`ref` 與新增一樣以 feed 網址查 feed key：已經新增過的 feed 用它的 key（可能是 `-2`），並據此標示 `already_in_library`；還沒新增過的 feed 用新增此刻會取得的候選 key（之後別的 feed 先新增時仍可能改變）。
  - 長度不是有限值、為負或超過 1000 小時時視為不知道長度（`duration_s` 為 `null`，金額未知），避免不可信的 feed 讓金額變成無限大。
  - Groq 設定了自訂的 `base_url`（可能是其他相容服務）時，內建單價不適用，`asr_usd` 為 `null`。
  - Supadata 不處理這個平台（Podcast、本機檔案）時，估價先判斷平台再看 API key，與處理時 `runFallback` 先看 `supports` 一致：不為用不上的來源在 `notes` 提示「沒有設定 API key」（2026-11-22，Codex 審查後決定）。Groq 沒有平台的 `supports` 判斷，處理時是先執行再回報沒有 key，估價維持先看 key。
  - 預計來源是預測：執行時平台字幕、feed 字幕或 Supadata 失敗，會改用下一個來源，實際費用可能不同。直播或還沒首播的影音在 `notes` 提醒要等結束後才會處理。
  - 資源上限：一次最多 Probe 100 部（播放清單更長時只估前 100 部，頂層 `notes` 說明總數）；整個程序同時最多 4 個估價 Probe（跨請求共用）；一次估價的 Probe 總時間上限 3 分鐘，用完時還沒輪到的影音標為未知（「估價逾時」）。
  - 一次估算多部影音時，單一部 Probe 失敗只在該項的 `notes` 說明；全部都失敗時，頂層 `notes` 也說明，仍回 200。只有一部時回傳錯誤。`llm_usd` 在 M2 實作 LLM 之前一律為 `null`。內建計價表在 `internal/cost`，使用者自訂單價屬於 M4（COST-03）。
- ING-R9 的 Bilibili 部分在未登入時做不到：Bilibili 的 AI 字幕需要 cookie，但未登入時 yt-dlp 不列出字幕，辨識（`POST /api/resolve`）看不出這部影片有沒有 AI 字幕，無法標示 `requires_cookie`。目前只有 YouTube 的 `availability` 會標示；Bilibili 的標示記為待修的規則，等 M0 驗證後再決定（可能改成只在 transcript 步驟 `blocked` 時提示，見 SRC-R8）。

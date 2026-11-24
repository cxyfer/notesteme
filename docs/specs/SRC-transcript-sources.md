# SRC：字幕來源與品質

- 狀態：ready
- 里程碑：M1（SRC-10：M4，SRC-R18～R23）
- 相關：plan 第 5.2、6.1、6.3、6.4、7 節；ADR-0003、ADR-0006、ADR-0009、[ADR-0021](../adr/0021-m4-scheduler-subscriptions-and-outbox.md)（M4）；UI [影音詳情](../ui/pages.md#4-影音詳情)、[總覽](../ui/pages.md#1-總覽)（需要你處理）
- 驗收場景：[`features/SRC-transcript-sources.feature`](../../features/SRC-transcript-sources.feature)；SRC-10（M4）：[`features/SRC-better-source.feature`](../../features/SRC-better-source.feature)

## 目的

用最便宜、最可靠的方式拿到原始字幕：先用平台字幕，必要時才付費轉錄；拿到的結果不能用時自動換下一個來源；卡住時明確告訴使用者要做什麼。

## User stories

| ID | Story |
|---|---|
| SRC-01 | As the owner, I want platform captions to be used whenever they exist, in my preferred order of languages and kinds, so that I get good subtitles at no cost. |
| SRC-02 | As the owner, I want paid sources to be used only when no platform caption fits, in the order I set, so that I control what I pay for. |
| SRC-03 | As the owner, I want unusable results such as the wrong language, hallucinations or music-only tracks to be skipped automatically, so that bad input never reaches the later stages. |
| SRC-04 | As the owner, I want Notesteme to wait and check again when captions are not ready yet, so that I do not pay for transcription that would soon be free. |
| SRC-05 | As the owner, I want a clear message and a fix action when a source needs something from me, so that I can unblock it in one step. |
| SRC-06 | As the owner, I want long recordings to be transcribed even when they exceed a provider's file limit, so that length is never a reason to fail. |
| SRC-07 | As the owner, I want to know which source a transcript came from and how its timing was obtained, so that I can judge how far to trust it. |
| SRC-08 | As the owner, I want the original language detected when the platform does not provide it, so that the right languages and prompts are used. |
| SRC-09 | As the owner, I want subtitles that come with my local files or podcast feeds to be used before any transcription, so that existing work is reused. |
| SRC-10 | As the owner, I want to be told when a better transcript source appears after I processed a video, so that I can upgrade it. |

SRC-10 屬於 M4（故事地圖第 5 節），規則是 SRC-R18～R23；其餘故事屬於 M1。

## 規則

| ID | 規則 |
|---|---|
| SRC-R1 | 依 `language.caption_priority` 的順序尋找平台字幕軌，使用第一個符合的。`original` 代表影音的原文語言。 |
| SRC-R2 | 平台機器翻譯的字幕軌（`platform-translated`）預設不採用，除非 `allow_platform_translated: true`。 |
| SRC-R3 | 平台字幕都不符合時，依 `language.fallback` 的順序使用其他來源，預設為 `[supadata, groq]`。 |
| SRC-R4 | 每個來源的結果都要通過品質檢查，沒通過就記錄原因、標記 `rejected`，改用下一個來源：涵蓋率低於片長的 50%（`low_coverage`）、語言和預期不符（`language_mismatch`）、重複句比例超過 30%（`repetition`）、超過 80% 的 cue 只有音樂標記（`music_only`）。門檻是預設值，依 M0 結果調整。 |
| SRC-R5 | 所有來源都用完仍沒有可用的字幕時，`transcript` 為 `failed`（`permanent`），訊息為「找不到可用的字幕來源。」，動作為「設定語音轉錄服務」。 |
| SRC-R6 | YouTube 自動字幕還沒生成時，`transcript` 為 `waiting`，2 小時後再檢查，不消耗重試次數。從發布時間算起超過 24 小時仍沒有，就改用下一個來源。訊息為「YouTube 還沒產生自動字幕，預計 2 小時後再檢查。」，動作為「現在改用語音轉錄」。 |
| SRC-R7 | 直播中或首播尚未開始的影音，`metadata` 為 `waiting`，直到有 VOD。 |
| SRC-R8 | 來源需要 cookie 但沒有可用的 cookie 時，步驟為 `blocked`（`needs_action`），訊息說明需要哪個平台的 cookie，動作為「上傳 cookie」與「改用語音轉錄」。 |
| SRC-R9 | yt-dlp 回傳 403 時，步驟為 `blocked`（`needs_action`），訊息為「YouTube 拒絕了下載請求（403）。先更新 yt-dlp 再重試；如果仍然失敗，上傳 YouTube cookie。」，動作為「更新 yt-dlp」與「上傳 cookie」。yt-dlp 回報影片不存在（已刪除、私人、無法取得）時，步驟為 `failed`（`permanent`），不重試。 |
| SRC-R10 | 需要 API key 的來源沒有設定 key 時，步驟為 `blocked`（`needs_action`），動作為「設定 API key」。 |
| SRC-R11 | 原文語言依序採用：平台中繼資料、ASR 回報的語言、文字語言偵測。兩種語言都各占 20% 以上的字元時，標記為 `mixed`，主要語言取占比較高的一種。 |
| SRC-R12 | ASR 前處理：轉成單聲道 16kHz 並壓縮。仍超過 provider 的單檔上限時，在靜音處切塊並保留重疊；合併時修正時間偏移、移除重疊區的重複內容，結果的時間必須單調遞增。 |
| SRC-R13 | 過濾 Whisper 幻覺：`no_speech_prob` 高於 0.6 且 `compression_ratio` 高於 2.4 的 segment 移除；連續 3 次以上完全相同的句子只保留一次。 |
| SRC-R14 | 本機檔案依序使用：同名的外掛字幕檔（`<stem>.<lang>.srt`／`.vtt`）、ffprobe 偵測到的內嵌字幕軌，最後才用 ASR。Podcast 有 `<podcast:transcript>` 時優先使用。 |
| SRC-R15 | Podcast 下載音檔時記錄 `sha256` 與實際長度；和 transcript 的最後一個 cue 相差超過 2% 時，影音顯示「時間可能偏移」。 |
| SRC-R16 | 原始字幕的來源紀錄包含：`source`、`timing`（時間粒度）、`tool_versions`（使用 yt-dlp 時包含其版本）。每個 cue 的時間來源為 `source`；Gemini 為 `model`。 |
| SRC-R17 | 每次 `Probe` 的結果都記錄在 `source_probes`，API 的影音詳情可以列出各來源的狀態與不採用的原因。 |
| SRC-R18 | （M4）重新 Probe 的對象與時段：`yt:` 與 `bili:` 的影音，`transcript` 為 `done`，採用的來源不是評估順序（`caption_priority` 的各條規則，再 `fallback`）中的第一條，而且 `transcript` 完成時距離發布時間（`published_at`；沒有時用影音的 `created_at`）不到 7 天，才排定排程工作 `source.reprobe`（ADR-0021 第 1 點，`key` 是影音 id）。第一次在 `transcript` 完成後 12 小時（只有 SponsorBlock 部分時，從 `sponsorblock` 完成起算，SRC-R20），之後每 12 小時一次（加 ADR-0021 的 jitter），最後一次不晚於發布時間＋7 天（`until`）；停機後補跑時已經超過 `until` 的不再執行。發現更好的來源（SRC-R21、R22）、影音移到垃圾桶或永久刪除、處理方案的 `language.better_source` 是 `off`、或從 `transcript` 以前的步驟重跑時，結束這個工作（重跑後依新的結果重新判斷）。Podcast 與本機檔案不重新 Probe。`GET /api/items/:id` 回傳 `reprobe: {next_at, until, runs, sources, sponsorblock}`，沒有排程時是 `null`。 |
| SRC-R19 | （M4）重新 Probe 只列出平台的字幕軌（`yt-dlp -J`，不下載字幕與媒體），經過 SUB-R10 的平台 rate limiter；不呼叫 Supadata、Groq 等 `fallback` 來源，也不把結果寫進字幕。「更好的來源」是評估順序排在現行來源之前、而且符合規則的平台字幕軌（`allow_platform_translated` 照常適用，SRC-R2）。結果記錄在 `source_probes`（`trigger: reprobe`），但 `GET /api/items/:id` 的 `sources` 仍然只列現行字幕那一次 Probe 的結果（SRC-R17），直到重跑 `transcript`。 |
| SRC-R20 | （M4）同一個 `source.reprobe` 工作也重新查詢 SponsorBlock：YouTube 影音的 `sponsorblock` 步驟完成、`stages.sponsorblock.enabled` 為 `true`、還在發布後 7 天內時（即使字幕來源已經是第一條，`reprobe.sponsorblock` 為 `true`）。查詢方式同 SPB-R2；結果與現行產出不同時建立新版本的 `sponsor_segments`，相同時不建立（SPB-R8）；查詢失敗時保留現行產出、不顯示訊息（SPB-R5 的提醒只在步驟執行時顯示）。片段有更新時，`sponsorblock` 步驟帶提醒「SponsorBlock 片段有更新，從章節重跑才會套用到章節、關鍵畫面與筆記。」，動作為「從章節重跑」；不自動重跑後續步驟。 |
| SRC-R21 | （M4）預設只通知（`language.better_source: notify`）：發現更好的來源時，在同一個交易中寫入事件 `source.better_available {item, current: {source, lang, kind}, better: {source, lang, kind}, estimate_usd, action}`（`action` 為 `notify` 或 `rerun`）與通知 outbox（NOTIFY 規格；`dedupe_key` 為 `source.better_available:<影音 id>:<來源>:<語言>`），每部影音每個更好的來源只通知一次。`transcript` 步驟維持 `done`，帶訊息「發現更好的字幕來源：<來源名稱>（<語言>）。重跑會再花一次 LLM 費用，預估 $<金額>。」（金額取到小數點後兩位；`estimate_usd` 為 `null` 時，後半句是「重跑會再花一次 LLM 費用，目前無法估算金額。」），動作為「改用更好的字幕重跑」（`POST /api/items/:id/rerun {from: transcript}`）。來源名稱：`youtube-manual` 是「YouTube 人工字幕」、`youtube-auto` 是「YouTube 自動字幕」、`bilibili-cc` 是「Bilibili CC 字幕」、`bilibili-ai` 是「Bilibili AI 字幕」。 |
| SRC-R22 | （M4）處理方案（與單次覆寫、頻道覆寫）的 `language.better_source` 可以是 `notify`（預設）、`rerun`、`off`，其他值回 422、`errors[].path` 為 `language.better_source`。`rerun`：發現更好的來源時照 SRC-R21 寫入事件（`action: rerun`），並以影音原本的設定快照從 `transcript` 重跑，任務用 `backfill` 優先級，花費照 COST 規格預留與檢查（超過預算時為 `blocked`，原因 `budget_blocked`）；每部影音最多自動重跑一次。影音已經有 `edited` 變體（使用者手動修改過字幕）時不自動重跑，改成只通知。`off`：不排定 `source.reprobe` 的字幕部分（SponsorBlock 的重新查詢不受影響）。 |
| SRC-R23 | （M4）`GET /api/items/:id` 的 `better_source: {source, lang, kind, found_at, estimate_usd}`（沒有時為 `null`）。`estimate_usd` 用與 `POST /api/estimate`（ING-08）相同的估算，範圍是從 `transcript` 到最後一個步驟、影音現行的設定快照；計價表沒有價格時為 `null`（PRM 規格的 `estimate_usd`）。UI 在重跑前顯示這個金額。從 `transcript` 重跑時重新 Probe，自然採用更好的來源；重跑後 `better_source` 清除，`source.reprobe` 依新的結果重新判斷（通常是結束）。 |

## 介面

- API：`GET /api/items/:id`（含 `sources` 清單）、`GET /api/items/:id/cues?variant=source`
- CLI：`notesteme items show <item>`
- 設定：`language.caption_priority`、`language.allow_platform_translated`、`language.fallback`；設定檔中的 `asr.providers`

`GET /api/items/:id` 的 `sources` 每一列（SRC-R17）：

| 欄位 | 說明 |
|---|---|
| `source` | 來源 ID：`youtube-manual`、`youtube-auto`、`bilibili-cc`、`bilibili-ai`、`supadata`、`groq` 等。 |
| `lang` | 這一列對應的語言；`caption_priority` 的 `original` 在原文語言未知時為 `original`。可能為空（來源沒有回報）。 |
| `kind` | 平台字幕軌的種類：`manual`、`auto`、`platform-translated`；其他來源為空。 |
| `state` | `used`（採用）、`rejected`（有結果但不採用）、`unavailable`（沒有結果或無法取得）。 |
| `reason` | 不採用或無法取得的原因；`used` 為空。見下表。 |

`reason` 的值：

| 值 | state | 說明 |
|---|---|---|
| `no_track` | `unavailable` | 平台沒有符合這條規則的字幕軌。 |
| `original_language_unknown` | `unavailable` | 規則用 `original`，但原文語言未知。 |
| `platform_translated` | `rejected` | 平台機器翻譯的字幕軌，`allow_platform_translated` 為 false（SRC-R2）。 |
| `empty` | `rejected` | 字幕軌下載後沒有任何 cue。 |
| `not_ready` | `unavailable` | YouTube 自動字幕還沒生成，正在等待（SRC-R6）。 |
| `not_configured` | `unavailable` | 來源沒有設定（缺 API key 或尚未實作），略過。 |
| `no_transcript` | `unavailable` | 來源沒有這部影片的字幕（Supadata 的 206）。 |
| `permanent` | `unavailable` | 來源回報這個請求永遠不會成功，改用下一個來源。 |
| `needs_action` | `unavailable` | 來源需要使用者處理（API key 被拒、方案不支援、需要 cookie（SRC-R8）），步驟為 `blocked`。 |
| `download_failed` | `unavailable` | 語音轉錄來源需要的音訊下載失敗（yt-dlp 的問題，不是來源本身的問題）；步驟的狀態與訊息沿用下載錯誤的（例如 403 為 `blocked`，SRC-R9）。 |
| `language_mismatch` | `rejected` | 來源回報的語言和影音的原文語言不同（SRC-R4）。 |
| `music_only` | `rejected` | 超過 80% 的 cue 只有音樂標記（SRC-R4）。 |
| `low_coverage` | `rejected` | cue 涵蓋的時間低於片長的 50%（SRC-R4）。 |
| `repetition` | `rejected` | 重複句的比例超過 30%（SRC-R4）。 |
| `unsupported` | `unavailable` | 來源不支援這個平台（例如 Supadata 不處理本機檔案），略過，不送出任何資料。 |
| `unreadable` | `rejected` | 本機的外掛字幕或內嵌字幕讀不到、超過大小上限、解析或抽取失敗；改用下一條字幕，最後才用 fallback。 |

順序是評估順序：先 `caption_priority` 的各條規則，再 `fallback` 的各個來源。只列最近一次 Probe 的結果；重跑或等待後再檢查時，舊的結果被取代。SRC-R4 的品質檢查只套用在 `fallback` 來源的結果（見待決問題）。

名詞：來源紀錄（provenance）的 `timing` 是這個來源的時間粒度，值為 `word`、`segment`、`cue`、`chunk`、`model`；cue 的 `timing` 是這句時間的來源（SRC-R16）。兩者是不同的欄位，不要混用。Groq 在 M1 的來源紀錄 `timing` 是 `segment`。

### M4：事後出現更好的來源（SRC-10）

- API：`GET /api/items/:id` 加上 `reprobe`（SRC-R18）與 `better_source`（SRC-R23）；`POST /api/items/:id/rerun {from: transcript}`（既有）是「改用更好的字幕重跑」的動作。
- MCP：`get_item` 回傳相同的 `reprobe`、`better_source`。
- 處理方案：`language.better_source`（`notify`、`rerun`、`off`）。
- 事件：`source.better_available`（SRC-R21），也是 NOTIFY 規格可以訂閱的事件。
- 排程：`scheduled_jobs` 的 `source.reprobe`（`key` 是影音 id），間隔 12 小時；排程器內同步執行，受平台 rate limiter 限制，不占用任務的資源類別（ADR-0021 第 6 點）。
- 資料表：`source_probes` 加 `trigger`（`stage`、`reprobe`）；`items` 加 `better_source_json`（或另立資料表，實作時決定）。
- `pipeline.Stages`：不變。

## 不在範圍內

- ~~發布後 7 天內重新 `Probe` 與「發現更好的來源」通知（M4，故事地圖 SRC-10）~~：M4 已排入，見 SRC-R18～R23。
- 詞彙表傳給 ASR（M2，見 COR）。
- Gemini 當作字幕來源（M3）。

### M4

- 重新 Probe 時改用其他付費來源（例如 ASR 結果不好時改用 Supadata）：只比較平台字幕軌（SRC-R19）。
- Podcast 事後補上 `<podcast:transcript>` 的偵測（見待決問題 R5）。
- SponsorBlock 片段更新後自動重跑 `chapters`、`frames`、`note`（SRC-R20 只提醒）。

## 待決問題

- 品質檢查的門檻（SRC-R4）、幻覺過濾的門檻（SRC-R13），依 M0 樣本調整；相關場景標記 `@pending-m0`。
- Groq 是否提供 word 級時間戳（M0）。沒有的話，Groq 的時間粒度改為 `segment`。
- SRC-R10 與預設 `fallback: [supadata, groq]` 的關係：如果沒有 key 就 `blocked`，只想用其中一個來源的人，每部沒有平台字幕的影音都會被擋住。暫定：`fallback` 中沒有 key 的來源依序略過，記為 `not_configured`；全部都因為缺 key 或未設定而無法執行時，才 `blocked`（`needs_action`，動作「設定 API key」）。key 被拒（Supadata 的 401）或方案不支援（Supadata 的 402）一律 `blocked`。Supadata 的 403 代表內容私人或受限、206 代表沒有字幕，都視為這個來源沒有結果，繼續下一個來源（依 [Supadata 的錯誤碼說明](https://docs.supadata.ai/errors/list)）。現有場景只有 groq 一個來源，兩種解讀的結果相同（2026-11-13，PIPE 切片實作 Supadata 時發現）。
- SRC-R6 的「自動字幕還沒生成」是推定：YouTube 沒有「還沒生成」的訊號，只能從「發布不到 24 小時、`automatic_captions` 完全是空的（含平台翻譯軌）、`caption_priority` 有 `auto` 規則」推斷。沒有語音的影片永遠不會有自動字幕，會白等 24 小時才改用下一個來源；動作「現在改用語音轉錄」（略過等待）見下面「SRC-R6 的現在改用語音轉錄」。每次檢查固定間隔 2 小時，不截到 24 小時的期限，所以實際改用下一個來源的時間可能比發布後 24 小時晚最多 2 小時（2026-11-14，SRC 切片實作時決定）。
- SRC-R6 的 24 小時期限依發布時間的精度而定：yt-dlp 有 `timestamp` 時是精確時間，期限為發布時間 + 24 小時；只有 `upload_date`（日期精度，當天 00:00 UTC）時，實際發布時間可能是當天任何時刻，期限改為該日 00:00 UTC + 48 小時（等於把不確定的那一天補上），寧可多等也不讓晚發布的影片提早付費轉錄。代價是這類影片最多多等一天（2026-11-14，SRC 審查後修正）。
- SRC-R9 的訊息字串還沒對真實的 yt-dlp 驗證：目前依 yt-dlp 原始碼，比對 stderr 中以 `ERROR:` 開頭、含 `HTTP Error 403` 的行（涵蓋 `unable to download video data` 與 `Unable to download webpage`）。實際的 403 多半在下載媒體（ASR 需要的音訊）時發生，不是 `-J` 或字幕；等 `media.go` 與真實環境驗證後再確認字串與分類。
- SRC-R4 的 `language_mismatch` 在 M1 只比較來源回報的語言與影音的原文語言（只比主要語言，`zh` 與 `zh-Hans` 視為相同）；任一邊未知時略過檢查。文字語言偵測（SRC-R11 第三層）在收尾 D2 已完成，但只用來補原文語言與 `mixed`，不用來判斷 `language_mismatch`。其他門檻（`low_coverage`、`repetition`、`music_only`）已在收尾 D3 實作，見下方「SRC-R4 其餘品質檢查」。
- SRC-R5 失敗之後才補上 API key，影音不會自動重跑：步驟已是 `failed`（`permanent`），不是 `blocked`，PIPE-R4 的自動回到 `pending` 不適用。使用者要手動重跑 transcript。key 設定在步驟 `blocked`（SRC-R10）時才會自動繼續（2026-11-14，SRC 切片實作時發現）。
- ~~SRC-R10、R17 的場景與規格不一致~~：已結案（2026-11-14，owner 同意）。R10 原本在設定檔用 `openai-compatible` 定義 groq，和 ADR-0013、SEC-R2（一般 provider 由 DB 管理）以及 steps.md「fake provider 預設有 key」衝突，改用 `Given the provider "groq" has no API key`；Groq 的預設 preset 內建在程式中。R17 的 `sources` 依評估順序列出，平台字幕的列排在前面，場景補上這三列。
- Groq 的 API key 暫時用環境變數 `NOTESTEME_GROQ_API_KEY`（與 Supadata 一致）；SEC-R1 的字面規則會得到 `NOTESTEME_PROVIDERS_GROQ_API_KEY`，SEC-R4 加密存放時一併整理命名（2026-11-14）。
- `language_mismatch` 在 Groq 送出語言提示時幾乎不會觸發：Whisper 會回報被強制指定的語言，與影音的原文語言一定相同。真正的語言檢查屬 SRC-R4 的文字語言偵測（偵測轉錄出來的文字，而不是信任來源回報的語言），待該門檻實作後才有意義。語言比對前，舊代碼與變體先正規化成同一個代碼（nb、nn → no，fil → tl，iw → he，in → id，jw → jv）；Whisper 的語言提示只送兩個字母的 ISO-639-1 代碼，三字母的 yue、haw 等不送，讓 Whisper 自行偵測（2026-11-14，SRC 審查後修正）。
- ~~Groq 單一音檔的上限是 25 MiB（免費方案），在切塊（SRC-R12）完成前，超過的音檔不上傳，直接 `permanent`（訊息「…長音訊的切塊還沒實作。」）~~：**已過期（SRC-R12 切塊已完成，見下方「ASR 前處理與切塊」）**。現況：前處理後超過上限的音檔在靜音處切塊後分別上傳；切塊後仍有塊超過上限（多切 `maxExtraChunks` 次仍不行）時 `permanent`（訊息「音檔切塊後仍超過語音轉錄服務的大小上限。」），改用下一個來源；Groq 回 413 時的訊息是「音檔超過語音轉錄服務的大小上限（25 MiB）。」，只寫進 log 與錯誤（SRC-R5 的步驟訊息是固定的）。音訊下載另外帶 `--max-filesize 2G`（GRD-R5 的音檔上限，以 bytes 傳給 yt-dlp），超過時為 `permanent`；這個行為依 yt-dlp 原始碼，尚未對真實環境驗證（2026-11-14，SRC 審查後修正；2026-11-24 收尾 D3 更新）。
- R17 場景補上 groq 那一列的 `lang`（2026-11-14，owner 同意），讓 Whisper 語言全名轉成 BCP-47 有場景驗證。
- SRC-R6 的 24 小時期限在場景中只驗證「0.5 小時仍等待」與「25.5 小時已放棄」，期限的確切值由 `internal/pipeline` 的單元測試把關（owner 決定不加 23 小時的檢查點，2026-11-14）。
- SRC-R8 是推定：Bilibili 未登入時，yt-dlp 不列出任何字幕（結束碼 0），只在 stderr 印出 `WARNING: [BiliBili] Subtitles are only available when logged in.`，所以分不出平台有沒有 CC 或 AI 字幕，也看不到語言。實作的條件是：Probe 偵測到這行 WARNING、沒有選到字幕軌、`caption_priority` 有 `kind: auto` 的規則，三者都成立才 `blocked`（`needs_action`，`resume_on` 為 `cookie:bilibili`，`bilibili-ai` 記為 `unavailable`／`needs_action`）。已登入卻沒有任何字幕時不會有這行 WARNING，照 fallback 處理。因此 Bilibili 的 Probe 不能帶 `--no-warnings`（會吃掉這行）。這些語意依 yt-dlp 原始碼確認，等 M0 用真實環境驗證（2026-11-14，SRC-B 切片實作時決定）。動作「改用語音轉錄」目前只顯示；Groq 對非 YouTube 的平台仍是 `permanent`（音訊下載還沒支援）。
- Bilibili 字幕軌的對應規則：yt-dlp 在 `--write-subs` 時才列出 `subtitles`，鍵是 Bilibili 的 `lan`，格式是 srt，另外一律有 `danmaku`（xml 彈幕，選軌時忽略）；沒有 `automatic_captions`。鍵是 `ai-<lang>` 的是 AI 字幕，對應 kind `auto`、來源 `bilibili-ai`，語言去掉 `ai-`；其他鍵對應 kind `manual`、來源 `bilibili-cc`。兩種字幕的來源紀錄 `timing` 都是 `cue`。下載時用原始鍵（`--sub-langs <原始鍵> --sub-format srt`，讀 `<tmpl>.<原始鍵>.srt`）。yt-dlp 的錯誤分類：stderr 的 `ERROR:` 行含 `-404`、`-10403` 或地區限制訊息 → `permanent`；含 `-101`（未登入）→ `needs_action`（訊息「Bilibili 要求登入才能存取這部影片。上傳 Bilibili cookie 後再重試。」，動作「上傳 cookie」，`resume_on` 同上）；412（風控）與其他 → `retryable`。這些訊息依原始碼推測，沒有對真實 yt-dlp 驗證。
- Bilibili 沒有語言中繼資料，原文語言（SRC-R11）只能從字幕軌推斷：`item.original_lang` 未知、而 Probe 看得到 AI 字幕軌時，transcript 步驟用該軌的語言當原文語言，`lang_source` 為 `metadata`（取第一條 AI 字幕軌，依鍵排序）。只有 CC 字幕時不推斷，`caption_priority` 的 `original` 規則記為 `original_language_unknown`。未登入時看不到 AI 字幕軌，所以推斷不了，這是 SRC-R8 推定的一部分（2026-11-14）。
- PIPE-R4 的場景補上斷言 Bilibili 的原文語言由 AI 字幕軌推斷（2026-11-15，owner 同意）。`ResumeBlocked` 只恢復同一個恢復條件（token）的步驟，這點沒有場景驗證：被誤恢復的步驟重跑後會再次被擋下，狀態與嘗試次數看不出差別。由 `internal/pipeline` 與 `internal/store` 的單元測試把關（owner 決定不新增任務數的句型）。
- 本機檔案的來源（SRC-R14，2026-11-21，LOCAL 切片實作時決定，待 owner 確認）：
  - 外掛字幕檔只認 `<stem>.<lang>.srt`／`.vtt`，`<lang>` 必須像語言代碼；沒有語言代碼的 `<stem>.srt` 不採用。有多個時，優先 `caption_priority` 中的語言，否則 `.srt` 先於 `.vtt`、再依檔名排序。
  - 內嵌字幕只採用文字字幕（subrip、ass、ssa、mov_text、webvtt），點陣字幕（PGS、DVD、DVB）略過。有多條時，優先 `caption_priority` 中的語言，否則依 default、非 forced、串流順序。
  - 原文語言未知時，用選中的本機字幕的語言推斷，`lang_source` 為 `metadata`（比照 Bilibili 的 AI 字幕軌）。容器中音訊串流的語言標記不採用（常常是錯的或 `und`）。
  - Supadata 不處理本機檔案，記為 `unavailable`／`unsupported`，不會把本機路徑送給第三方。
  - 外掛字幕與內嵌字幕的語言碼一律正規化成 BCP-47（三字母碼轉成兩字母、主語言小寫、地區大寫，例如 `movie.eng.srt` 為 `en`、`movie.zh-hant-tw.vtt` 為 `zh-Hant-TW`）（2026-11-21，審查後補上）。
  - 選中的外掛字幕或內嵌字幕讀不到、解析失敗時，不讓步驟失敗：這一條記為 `rejected`，依序改用下一個外掛字幕、內嵌字幕，最後才用 fallback（與空字幕的處理一致）（2026-11-21，審查後決定）。
  - 本機檔案可以被原地替換：非上傳的本機影音每次 probe 都比對大小與修改時間，不同就重算 `sha256`；選中的外掛字幕的內容 `sha256` 也進快取鍵，修改字幕後重跑會重新讀取（2026-11-21，審查後補上）。
- ASR 前處理與切塊（SRC-R12，2026-11-21，LOCAL 切片實作時決定）：所有平台（含 YouTube）的 ASR 都先用 ffmpeg 轉成單聲道 16kHz 的 Opus（32 kbps）。仍超過 provider 的單檔上限時，以上限的 90% 估算塊數，在每個切點前後 30 秒內找最近的靜音中點（`silencedetect=noise=-35dB:d=0.3`），找不到就直接切；每塊前後各多取 5 秒重疊。合併時每個 cue 依中點歸屬到一塊，時間單調遞增，重疊區中時間重疊且文字相同的連續 cue 只留一句。切出的塊仍超過上限時，多切一塊重新規劃，最多多切 3 塊。編碼參數、`-format_whitelist` 清單與 silencedetect 的門檻要用真實 ffmpeg 確認（M0）。
- Podcast 的來源（SRC-R14、R15，2026-11-21，PODCAST 切片實作時決定，待 owner 確認）：
  - 音檔在 `metadata` 步驟下載到 `/data/media/<id>/episode.<ext>`（上限 2 GiB、逾時 2 小時），記錄 `sha256` 與 ffprobe 量到的長度；每次跑 `metadata` 都重新下載，因為動態插入的廣告會讓內容改變。重新下載後，舊的 `episode.*` 等新的結果寫入資料庫之後才刪除；任務被取消、過期或寫回失敗時影音仍指向舊檔，留到下一次下載再清。新檔名與目前快照所指的檔名相同（副檔名沒變）時，新檔改存成 `episode-<sha256 前 8 碼>.<副檔名>`，不在 commit 之前蓋掉舊檔，否則取消或寫回失敗後，播放與 ASR 會讀到和快照的 `sha256` 不符的內容（2026-11-22，Codex 審查後決定）。ASR 需要音檔而快取檔不在時才補下載，不驗證 `sha256`（理由同上）。
  - `podcast-transcript` 從 `metadata` 存下的 feed 快照取軌，不重新抓 feed，所以 feed 移除這一集後仍可繼續。支援 VTT、SRT 與 Podcasting 2.0 JSON（沒有 type 時看內容判斷；這種字幕在辨識結果中也算 `has_transcript`，來源紀錄的 `timing` 依實際解析的格式記錄）；HTML 與純文字不支援。軌的語言是 `language` 屬性，沒有時用 feed 的語言。多條時依 `caption_priority` 的語言、再依格式 VTT > SRT > JSON。
  - 字幕抓取失敗（4xx、被防護擋下、解析失敗）記為 `unavailable`／`permanent`（每條失敗的字幕軌一筆來源紀錄），先改用 feed 宣告的下一條字幕軌（把失敗的軌拿掉後重新選軌，2026-11-22，Codex 審查後決定），都不能用才改用 fallback；5xx、429 與連線錯誤讓步驟重試。Supadata 對 podcast 是 `unsupported`。
  - SRC-R15 的「實際長度」是 ffprobe 量到的音檔長度，不是 feed 的 `itunes:duration`；與最大的 cue 結束時間相差超過 2%（不含）時，`transcript` 步驟仍為 `done`，帶 message「時間可能偏移」，沒有動作，來源紀錄加上 `timing_check`。只檢查 `podcast-transcript`，ASR 的結果不檢查。done 步驟的訊息在 UI 顯示為提醒（`shows the message` 的語意已補上這種情況，需要 owner 確認）。
  - Podcasting 2.0 JSON 的 segment 只有每個 body 都沒有空白且不含 CJK 時才視為字詞級，合併成 cue 並填入 `Words`；含 CJK 的文件一律一個 segment 一個 cue。CJK 沒有空白可以分辨字詞與整句，「大家好」「歡迎收聽」這類短句靠字數判斷會被誤合併，所以不靠長度猜測；代價是逐字切分的 CJK 文件會有很多很短的 cue（2026-11-22，Codex 審查後決定，取代 2026-11-21「不超過 4 個字」的做法）。
- SRC-R11 的 ASR 語言（2026-11-21）：原文語言未知、ASR 回報了語言時，寫回影音的原文語言，`lang_source` 為 `asr`。這個語言是 transcript 步驟自己的產出，所以重跑 transcript 時不當成輸入：快取鍵、送給 ASR 的語言提示與 `language_mismatch` 檢查都視為原文語言未知，否則重跑會因為快取鍵改變而重複付費。第三層的文字語言偵測見下一條「SRC-R11 的文字語言偵測」。
- ~~SRC-R11「Code-switching speech is marked as mixed」暫時標為 `@wip`（2026-11-15，owner 同意）：`the original language of … is …` 實作後，這個場景會失敗。場景沒有宣告語言，harness 預設 `en`，場景卻預期由文字偵測得到 `zh`；production 也還沒有文字語言偵測。等文字語言偵測的切片一併解決這個矛盾後，再拿掉 `@wip`。~~ 已處理，見下一條。
- SRC-R6 的「現在改用語音轉錄」（2026-11-24，依 /goal 採 advisor 建議，待 owner 追認）：新增窄路由 `POST /api/items/:id/use-asr-now`（scope `ingest`，同 `switch-executor`；不沿用 rerun，因為 rerun 會建立新的步驟紀錄並重新 Probe，而這個動作只是讓同一個等待中的任務提前執行）。只在 transcript 的最新紀錄是 `waiting`、而且它的任務還沒被領取（`pending`）時接受：在同一個交易中把任務的 `not_before` 改成現在、payload 加上 `skip_auto_wait: true`，步驟改回 `pending`（訊息與動作清掉），然後喚醒 worker；其他情況回 409 `not_waiting`（任務正在執行時，執行結束又會排回 2 小時後，旗標會失效，所以拒絕，讓使用者稍後再按）。執行時 `skip_auto_wait` 讓 SRC-R6 的等待不成立，直接走 fallback；旗標留在任務的 payload（重新排入佇列不會清掉），所以同一個任務之後的重試也不會回到等待。等待期間的領取本來就不計入次數（PIPE-R2），這個動作不另外消耗重試次數，fallback 真正執行的那一次照常計入。重跑 transcript 會建立新的任務，旗標不延續（重跑時若仍在 24 小時內會再次等待）。`Stage` 多了 `action_codes`（`use_asr_now`），前端依代碼對應；Bilibili 的「改用語音轉錄」（SRC-R8）仍只顯示（Groq 還不能下載 Bilibili 音訊）。
- SRC-R11 的文字語言偵測與 `mixed`（2026-11-24，依 /goal 採 advisor 建議，待 owner 追認）：
  - 實作在 `internal/langdetect.Detect`：純 Go、不連網，依文字系統的份量判斷。份量的單位：漢字、假名、諺文、泰文一個字一份，其他字母文字一個「詞」一份。規格寫「字元」，但照字母逐一計算時，中英夾雜的字幕（例如場景的 `auto-zh-en-mixed`：29 個漢字、13 個英文詞、約 70 個英文字母）英文會變成主要語言，與場景預期的 `zh` 相反；改成「一個英文詞與一個漢字等重」，該 fixture 是 zh 69%、en 31%，標為 `mixed`、主要語言 `zh`。這是對「字元」的解讀。
  - 假名的份量達到漢字的五分之一時，漢字與假名都算日文；中文的簡繁都算 `zh`（`items.original_lang` 只寫主語言，字形另由 `zhconv.DetectScript` 判斷，不寫入）。拉丁字母與西里爾字母的語言依常見功能詞判斷（en、fr、de、es、pt、it；ru、uk）；一個都對不到時，主要語言不判斷（空字串），夾雜在其他語言中的第二語言視為 `en`（多半是英文術語）。總份量少於 8 時判斷不了，不改動任何欄位。
  - 套用的時機：`transcript` 產生新的原始字幕時（平台字幕或 fallback 來源）。原文語言依序採用平台中繼資料、ASR 回報、文字偵測；只有前兩層都沒有時才寫入偵測的主要語言，`lang_source` 為 `detected`（API 文件原本就列了這個值）。`mixed` 不論原文語言來自哪一層，都依這次的字幕文字重新判斷（達到時設為 true，否則清掉）；快取命中時不重新判斷。
  - 快取鍵：`detected` 與 `asr` 一樣是 transcript 自己的產出，重跑時視為原文語言未知（快取鍵、ASR 的語言提示、`language_mismatch`），重跑不會重複付費（`internal/pipeline/textlang_test.go`）。`mixed` 不進 transcript 的快取鍵；它是 correct、translate 的輸入（COR-R5），改變時下游的快取鍵跟著變，這是預期的。
  - 場景「Code-switching speech is marked as mixed」拿掉 `@wip`，並把第一步從 `a YouTube video … titled …` 改成 `a YouTube video … with:`（`title` 與 `language: zh`）：場景原本沒有宣告語言，harness 預設 `en`，依 SRC-R11 的順序平台中繼資料優先，不可能得到 `zh`；原文語言留空時，預設的 `caption_priority`（`original` 的規則）又選不到 zh 的自動字幕，會改走 fallback。宣告 `zh` 是最小修改：場景驗證文字判斷的 `mixed`，第三層的語言判斷由單元測試把關。待 owner 追認；若要由場景驗證第三層，需要先做「原文語言未知時從 YouTube 自動字幕（`*-orig`）推斷」（handoff §6 SRC）。

- SRC-R4 其餘品質檢查與 SRC-R13 幻覺過濾（收尾 D3，2026-11-24，依 /goal 採 advisor 建議，待 owner 追認）：
  - 適用範圍：品質檢查只套用在 `fallback` 來源（Supadata、Groq）的結果，平台字幕軌（YouTube、Bilibili、本機、Podcast 的字幕）不檢查。理由：拒絕免費的平台字幕會改用付費的來源，這是花錢的決定，應該由使用者用 `caption_priority` 決定；而且自動字幕的音樂影片（全是 `[Music]`）改用 ASR 也多半一樣。規則字面是「每個來源」，要不要擴大到平台字幕軌待 owner 決定。
  - 檢查順序固定：`language_mismatch` → `music_only` → `low_coverage` → `repetition`，第一個不通過的就是原因（音樂標記不算重複句，所以先判斷音樂）。比較都不含等號：剛好 50%、30%、80% 都通過。
  - `low_coverage`：cue 區間的聯集（裁到 `[0, 片長]`，重疊只算一次）除以片長。分母：語音轉錄來源（Groq）用它回報的實際音訊長度（`verbose_json` 的 `duration`，切塊時是 ffprobe 量到的長度），沒有回報時不檢查；其他來源用影音的片長（平台中繼資料），不知道片長時不檢查。ASR 不拿平台的片長比，是因為它轉錄的就是那段音訊，平台的片長是不可信輸入（場景 `Unusable results are rejected…` 的 `duration: 600` 搭配只有 6.5 秒的 `groq-en.json`，也依此通過）。
  - `repetition`：文字去掉前後空白、壓縮空白、轉小寫（數字與標點保留）後，與前面某一句相同的句子數，除以不是音樂標記、不是空白的句子數；少於 5 句時不判斷（太短的結果一句重複就超過 30%）。
  - `music_only`：去掉代表音樂的括號標記（`[Music]`、`（音樂）`、`[BGM]` 等，多語的固定清單）與音符（♪♫♬♩🎵🎶）、標點、空白之後什麼都不剩，而且至少去掉一個標記。`♪ 歌詞 ♪` 不算；`[Applause]` 不算音樂。
  - 門檻是程式中的常數（`internal/pipeline/quality.go`、`internal/source/groq` 的 `Hallucination*`、`minHallucinatedRun`），沒有做成設定檔可以覆寫：與 SRC-R15 的 `timingDriftLimit` 等既有門檻一致，M0 之後依樣本調整常數。要不要開放設定待 owner 決定。
  - 費用後果（待 owner 決定）：Groq 也套用這些檢查，所以 Groq 的結果因 `low_coverage`（例如片頭或間奏音樂超過一半）或 `repetition` 被拒絕時，轉錄已經付費，步驟仍以「找不到可用的字幕來源」失敗；手動重跑會再付一次，而且多半再被拒絕。可選的方向：把 ASR 排除在 SRC-R4 之外，或 ASR 被拒絕時仍採用、只附提醒。
  - 快取：transcript 的 `impl_version` 沒有提升。已完成的步驟不會因為新的檢查而重跑（否則所有已付費的 ASR 都要重新轉錄）；之後重跑 transcript、快取鍵不變時仍會命中舊結果。
  - SRC-R13：`no_speech_prob > 0.6` 而且 `compression_ratio > 2.4` 的 segment 在 `groq.Transcribe` 建立 cue 前移除（兩者都要成立，剛好等於門檻保留，沒有回報欄位視為 0）；全部被移除時視為沒有結果（`no_transcript`）。「連續 3 次以上完全相同的句子只保留一次」在接回單一時間軸之後（含切塊接縫）做：比較去掉前後空白後的文字（大小寫不同不算相同），留第一句、時間不變，cue 重新編號；兩次重複保留，交錯（A B A B）不算連續。音訊長度仍是 Groq 回報的長度，不因移除結尾的幻覺而縮短。
  - 場景：`Unusable results are rejected and the next source is used`（4 個 example）與 `Whisper hallucinations are filtered out` 補上 fixture 後通過（`supadata-covers-200s.json`、`supadata-repeating.json`、`supadata-music-only.json`、`groq-hallucinated-tail.json`、`local/quiet-ending.m4a`），仍保留 `@pending-m0`（門檻要用 M0 的真實樣本確認，拿掉標籤由 owner 決定）。

### M4（SRC-10）

- R1：重新 Probe 的間隔 12 小時（SRC-R18）。7 天內最多約 14 次，每次只有一個 `yt-dlp -J`，對 rate limit 的影響很小；YouTube 的人工字幕多半在發布後一兩天內補上。決定（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）。
- R2：處理方案的欄位叫 `language.better_source`，放在 `language` 底下，和 `caption_priority`、`fallback` 同一區段（字幕來源的設定都在 `language`）；`stages.transcript.*` 目前不是處理方案接受的步驟名稱（PRF S0）。決定（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）。
- R3：「較低優先」以評估順序判斷：現行來源不是評估順序中的第一條就算（SRC-R18），所以用了 `caption_priority` 第二條規則（例如原文的自動字幕）也會重新 Probe；只有更前面的規則對到字幕軌才算「更好」（SRC-R19）。語言不同的規則（例如 `zh-Hans` 的人工字幕排在原文自動字幕之後）照順序判斷，不另外比較品質。
- R4：SponsorBlock 的重新查詢掛在同一個 `source.reprobe` 工作（SRC-R20），即使字幕來源已經是第一條也會查，因為 plan 第 6.4 節的理由（片段常在發布幾天後才有人提交）和字幕來源無關。代價是每部新的 YouTube 影音 7 天內多約 14 次 hash 前綴查詢（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）。
- R5：Podcast 不重新 Probe（SRC-R18）。Podcast 的 transcript 偶爾在發布後才補上，但 feed 每次抓取都是完整的檔案，量比 `yt-dlp -J` 大；訂閱的 Podcast 已經會定期抓 feed（SUB），之後可以從訂閱的檢查順便比對 `<podcast:transcript>`，屬於 SUB 與 SRC 之間的後續工作。
- R6：自動重跑（`rerun`）不會覆蓋使用者手動修改過的字幕（SRC-R22）：重跑 `transcript` 會讓 `edited` 變體失去對應的上游，這和「不覆寫使用者的修改」的原則衝突（VAULT、COR）。這種影音只通知，由使用者決定。
- R7：場景中「平台事後補上字幕」用 `the platform adds … captions in … to … at …`，由假 yt-dlp 依 fake clock 決定是否列出這條字幕軌（steps.md 4.17）；實作時 harness 要把 fake clock 的現在傳給假 yt-dlp（例如寫進場景目錄的狀態檔）。
- M4-S0：`language.better_source`（`notify`、`rerun`、`off`）已加進處理方案的 schema 與 `pipeline.Language`（`omitempty`：沒有設定時不寫進快照，既有快照 sha 不變；`transcript` 的快取鍵只取用到的欄位，不含它）（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）。沒有設定時視為 `notify`，由 S7 讀取。
- SRC-10 實作的決定（M4-S7，2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：
  - R8：`source_probes` 加的欄位叫 `probe_trigger`（`stage`、`reprobe`），不是規格寫的 `trigger`（SQL 關鍵字）；`LatestProbes` 只取 `stage` 的最近一次，所以 `sources` 照 SRC-R19 只列現行字幕那一次 Probe。重新 Probe 的狀態另立資料表 `source_reprobes`（`until_at`、`runs`、`sources`、`sponsorblock`、`better_json`、`auto_reruns`），下一次的時間只在 `scheduled_jobs`；`better_json` 損壞時視為沒有更好的來源，不讓影音詳情失敗。
  - R9：`until` 是 `min(published_at, created_at)＋7 天`：發布時間來自平台（不可信），比新增時間晚時改用新增時間，否則未來的發布時間會讓影音每 12 小時被探測好幾年。第一次排在完成後 12 小時、不加 jitter（規格只對「之後每 12 小時」要求 jitter），之後是 `NextWithJitter`，最後一次截在 `until`。
  - R10：~~`estimate_usd` 目前一律是 `null`（`EstimateItem.LLMUSD` 還沒有實作）~~（已過期，收尾 D3 更新，2026-11-24，依 /goal 採 advisor 建議，待 owner 追認）：`estimate_usd` 是從 `transcript` 重跑到最後一個步驟、影音記錄的處理方案的 LLM 費用，用與批次重跑 `dry_run`（LIB-R14）相同的 `EstimateRerun`（COST-R5 的估算，以影音現有的字幕代替更好的來源的字幕）；更好的來源一定是平台字幕，ASR 費用為 0。有任何付費任務無法估算（找不到單價、vision 等沒有估算方法的呼叫、輸入字幕還不存在）、估算失敗、或**沒有任何付費的 LLM 任務**（步驟都是 `local`、`off` 或沒有設定 provider）時是 `null`，訊息是「目前無法估算金額」。最後一種情況是為了不讓訊息「重跑會再花一次 LLM 費用，預估 $0.00。」誤導，也讓既有場景（harness 預設沒有付費 LLM 步驟）的 `null` 與訊息維持不變；與 ING 估價的 `llm_usd` 在這種情況回 `0` 不同（那裡的 `0` 代表確定不用錢）。只在發現更好的來源時算一次，之後單價或設定改變不會更新。事件 `source.better_available` 在 `null` 時不帶 `estimate_usd`（共用的事件結構用 `omitempty`，其他事件才不會多出 `null`）。
  - R11：自動重跑（`rerun`）用 `POST /api/items/:id/rerun` 同一條路徑：影音記錄的處理方案（最新版本）重新組合設定，不是 SRC-R22 字面的「影音原本的設定快照」，`until` 沿用影音原本的值，任務是 `backfill` 優先級（`RerunRequest.Priority`）。場景分不出兩者的差別；要嚴格沿用原本的快照需要 `Rerun` 新增「沿用 transcript 那一筆快照」的選項。
  - R12：重新 Probe 只受 SUB-R10 的平台 rate limiter 限制（與訂閱共用 bucket，沒有額度時延後到下一顆額度的時間，不算失敗），沒有另外的每日額度：每部影音 7 天內最多約 14 次 `yt-dlp -J`。
  - R13：「每部影音每個更好的來源只通知一次」：發現後字幕部分就結束，事件只寫一次；使用者重跑 transcript、之後又發現同一個來源時，只靠 outbox 的 `dedupe_key`（`source.better_available:<影音>:<來源>:<語言>`）在去重視窗內擋下。移到垃圾桶後還原不會重新排程（要重跑 transcript 才會重新判斷）。
  - R14：平台回報的語言（不可信）正規化後只接受 BCP-47 形狀的短代碼（`^[A-Za-z]{2,8}(-[A-Za-z0-9]{1,8}){0,3}$`）才算更好的來源，才會寫進訊息、事件與通知。
  - R15：從 `sponsorblock` 以前重跑時結束 SponsorBlock 部分，從 `transcript` 以前重跑時另外結束字幕部分並清除 `better_source`；步驟重新完成後依新的結果重新判斷（SRC-R18）。自動重跑的次數不清除（每部影音最多一次）。重新 Probe 寫回時以交易中最新的狀態為準，不蓋掉期間重跑所做的重設。
  - R16：場景與規則的衝突（已依建議修改這兩個場景的 `.feature`（2026-11-24，依 /goal 採 advisor 建議，待 owner 追認）；原本兩個場景是 failed）：
    - 「Re-probing only lists the platform captions and never calls a paid source」：`yt:asr001xxxxx` 的 `published_at` 是處理前 2 小時、沒有任何字幕軌、`caption_priority` 有 `auto` 規則，依 SRC-R6 `transcript` 會等待自動字幕（`waiting`），`the item … has been processed up to "transcript"` 失敗。建議把這個場景的 `published_at` 改成 `2026-11-19T06:00:00Z`（處理時已超過 SRC-R6 的 24 小時，`until` 為 11-26T06，字幕 11-21T00 補上，推進 24h 後找得到）。
    - 「The better source carries the estimated cost and a manual rerun uses it」：`the owner reruns … from "transcript"` 不帶 `until`，重跑一路做到 `sponsorblock`；依 SRC-R20，YouTube 影音的 SponsorBlock 部分在 7 天內仍要重新查詢，實際回應是 `"reprobe": {"sources": false, "sponsorblock": true, …}`，場景預期 `"reprobe": null`。建議把最後的預期改成 `{ "better_source": null, "reprobe": { "sources": false } }`（這個場景要驗的是重跑後字幕部分結束，SRC-R23），不寫死 `sponsorblock`。

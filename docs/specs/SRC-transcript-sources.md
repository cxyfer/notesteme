# SRC：字幕來源與品質

- 狀態：ready
- 里程碑：M1
- 相關：plan 第 5.2、6.1、6.3、7 節；ADR-0003、ADR-0006、ADR-0009；UI [影音詳情](../ui/pages.md#4-影音詳情)、[總覽](../ui/pages.md#1-總覽)（需要你處理）
- 驗收場景：[`features/SRC-transcript-sources.feature`](../../features/SRC-transcript-sources.feature)

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
| `unsupported` | `unavailable` | 來源不支援這個平台（例如 Supadata 不處理本機檔案），略過，不送出任何資料。 |
| `unreadable` | `rejected` | 本機的外掛字幕或內嵌字幕讀不到、超過大小上限、解析或抽取失敗；改用下一條字幕，最後才用 fallback。 |

順序是評估順序：先 `caption_priority` 的各條規則，再 `fallback` 的各個來源。只列最近一次 Probe 的結果；重跑或等待後再檢查時，舊的結果被取代。其他品質檢查的原因（`low_coverage`、`repetition`、`music_only`）等 SRC-R4 其餘門檻實作後加入。

名詞：來源紀錄（provenance）的 `timing` 是這個來源的時間粒度，值為 `word`、`segment`、`cue`、`chunk`、`model`；cue 的 `timing` 是這句時間的來源（SRC-R16）。兩者是不同的欄位，不要混用。Groq 在 M1 的來源紀錄 `timing` 是 `segment`。

## 不在範圍內

- 發布後 7 天內重新 `Probe` 與「發現更好的來源」通知（M4，故事地圖 SRC-10）。
- 詞彙表傳給 ASR（M2，見 COR）。
- Gemini 當作字幕來源（M3）。

## 待決問題

- 品質檢查的門檻（SRC-R4）、幻覺過濾的門檻（SRC-R13），依 M0 樣本調整；相關場景標記 `@pending-m0`。
- Groq 是否提供 word 級時間戳（M0）。沒有的話，Groq 的時間粒度改為 `segment`。
- SRC-R10 與預設 `fallback: [supadata, groq]` 的關係：如果沒有 key 就 `blocked`，只想用其中一個來源的人，每部沒有平台字幕的影音都會被擋住。暫定：`fallback` 中沒有 key 的來源依序略過，記為 `not_configured`；全部都因為缺 key 或未設定而無法執行時，才 `blocked`（`needs_action`，動作「設定 API key」）。key 被拒（Supadata 的 401）或方案不支援（Supadata 的 402）一律 `blocked`。Supadata 的 403 代表內容私人或受限、206 代表沒有字幕，都視為這個來源沒有結果，繼續下一個來源（依 [Supadata 的錯誤碼說明](https://docs.supadata.ai/errors/list)）。現有場景只有 groq 一個來源，兩種解讀的結果相同（2026-11-13，PIPE 切片實作 Supadata 時發現）。
- SRC-R6 的「自動字幕還沒生成」是推定：YouTube 沒有「還沒生成」的訊號，只能從「發布不到 24 小時、`automatic_captions` 完全是空的（含平台翻譯軌）、`caption_priority` 有 `auto` 規則」推斷。沒有語音的影片永遠不會有自動字幕，會白等 24 小時才改用下一個來源；動作「現在改用語音轉錄」（略過等待）的處理尚未實作，目前只顯示。每次檢查固定間隔 2 小時，不截到 24 小時的期限，所以實際改用下一個來源的時間可能比發布後 24 小時晚最多 2 小時（2026-11-14，SRC 切片實作時決定）。
- SRC-R6 的 24 小時期限依發布時間的精度而定：yt-dlp 有 `timestamp` 時是精確時間，期限為發布時間 + 24 小時；只有 `upload_date`（日期精度，當天 00:00 UTC）時，實際發布時間可能是當天任何時刻，期限改為該日 00:00 UTC + 48 小時（等於把不確定的那一天補上），寧可多等也不讓晚發布的影片提早付費轉錄。代價是這類影片最多多等一天（2026-11-14，SRC 審查後修正）。
- SRC-R9 的訊息字串還沒對真實的 yt-dlp 驗證：目前依 yt-dlp 原始碼，比對 stderr 中以 `ERROR:` 開頭、含 `HTTP Error 403` 的行（涵蓋 `unable to download video data` 與 `Unable to download webpage`）。實際的 403 多半在下載媒體（ASR 需要的音訊）時發生，不是 `-J` 或字幕；等 `media.go` 與真實環境驗證後再確認字串與分類。
- SRC-R4 的 `language_mismatch` 在 M1 只比較來源回報的語言與影音的原文語言（只比主要語言，`zh` 與 `zh-Hans` 視為相同）；任一邊未知時略過檢查。文字語言偵測與其他門檻（`low_coverage`、`repetition`、`music_only`）仍待實作。
- SRC-R5 失敗之後才補上 API key，影音不會自動重跑：步驟已是 `failed`（`permanent`），不是 `blocked`，PIPE-R4 的自動回到 `pending` 不適用。使用者要手動重跑 transcript。key 設定在步驟 `blocked`（SRC-R10）時才會自動繼續（2026-11-14，SRC 切片實作時發現）。
- ~~SRC-R10、R17 的場景與規格不一致~~：已結案（2026-11-14，owner 同意）。R10 原本在設定檔用 `openai-compatible` 定義 groq，和 ADR-0013、SEC-R2（一般 provider 由 DB 管理）以及 steps.md「fake provider 預設有 key」衝突，改用 `Given the provider "groq" has no API key`；Groq 的預設 preset 內建在程式中。R17 的 `sources` 依評估順序列出，平台字幕的列排在前面，場景補上這三列。
- Groq 的 API key 暫時用環境變數 `NOTESTEME_GROQ_API_KEY`（與 Supadata 一致）；SEC-R1 的字面規則會得到 `NOTESTEME_PROVIDERS_GROQ_API_KEY`，SEC-R4 加密存放時一併整理命名（2026-11-14）。
- `language_mismatch` 在 Groq 送出語言提示時幾乎不會觸發：Whisper 會回報被強制指定的語言，與影音的原文語言一定相同。真正的語言檢查屬 SRC-R4 的文字語言偵測（偵測轉錄出來的文字，而不是信任來源回報的語言），待該門檻實作後才有意義。語言比對前，舊代碼與變體先正規化成同一個代碼（nb、nn → no，fil → tl，iw → he，in → id，jw → jv）；Whisper 的語言提示只送兩個字母的 ISO-639-1 代碼，三字母的 yue、haw 等不送，讓 Whisper 自行偵測（2026-11-14，SRC 審查後修正）。
- Groq 單一音檔的上限是 25 MiB（免費方案），在切塊（SRC-R12）完成前，超過的音檔不上傳，直接 `permanent`（訊息「音檔超過語音轉錄服務的大小上限（25 MiB），長音訊的切塊還沒實作。」），改用下一個來源，沒有其他來源可用時依 SRC-R5 失敗。這個訊息目前只寫進 log 與錯誤，不顯示在步驟上（SRC-R5 的訊息是固定的）；實務上 25 MiB 約等於十幾分鐘的壓縮音訊，長影音在切塊完成前無法用 Groq 轉錄。音訊下載另外帶 `--max-filesize 2G`（GRD-R5 的音檔上限，以 bytes 傳給 yt-dlp），超過時為 `permanent`；這個行為依 yt-dlp 原始碼，尚未對真實環境驗證（2026-11-14，SRC 審查後修正）。
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
  - 音檔在 `metadata` 步驟下載到 `/data/media/<id>/episode.<ext>`（上限 2 GiB、逾時 2 小時），記錄 `sha256` 與 ffprobe 量到的長度；每次跑 `metadata` 都重新下載，因為動態插入的廣告會讓內容改變。ASR 需要音檔而快取檔不在時才補下載，不驗證 `sha256`（理由同上）。
  - `podcast-transcript` 從 `metadata` 存下的 feed 快照取軌，不重新抓 feed，所以 feed 移除這一集後仍可繼續。支援 VTT、SRT 與 Podcasting 2.0 JSON（沒有 type 時看內容判斷；這種字幕在辨識結果中也算 `has_transcript`，來源紀錄的 `timing` 依實際解析的格式記錄）；HTML 與純文字不支援。軌的語言是 `language` 屬性，沒有時用 feed 的語言。多條時依 `caption_priority` 的語言、再依格式 VTT > SRT > JSON。
  - 字幕抓取失敗（4xx、被防護擋下、解析失敗）記為 `unavailable`／`permanent`，改用 fallback；5xx、429 與連線錯誤讓步驟重試。Supadata 對 podcast 是 `unsupported`。
  - SRC-R15 的「實際長度」是 ffprobe 量到的音檔長度，不是 feed 的 `itunes:duration`；與最大的 cue 結束時間相差超過 2%（不含）時，`transcript` 步驟仍為 `done`，帶 message「時間可能偏移」，沒有動作，來源紀錄加上 `timing_check`。只檢查 `podcast-transcript`，ASR 的結果不檢查。done 步驟的訊息在 UI 顯示為提醒（`shows the message` 的語意已補上這種情況，需要 owner 確認）。
- SRC-R11 的 ASR 語言（2026-11-21）：原文語言未知、ASR 回報了語言時，寫回影音的原文語言，`lang_source` 為 `asr`。這個語言是 transcript 步驟自己的產出，所以重跑 transcript 時不當成輸入：快取鍵、送給 ASR 的語言提示與 `language_mismatch` 檢查都視為原文語言未知，否則重跑會因為快取鍵改變而重複付費。第三層的文字語言偵測仍未實作。
- SRC-R11「Code-switching speech is marked as mixed」暫時標為 `@wip`（2026-11-15，owner 同意）：`the original language of … is …` 實作後，這個場景會失敗。場景沒有宣告語言，harness 預設 `en`，場景卻預期由文字偵測得到 `zh`；production 也還沒有文字語言偵測。等文字語言偵測的切片一併解決這個矛盾後，再拿掉 `@wip`。

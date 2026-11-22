# features/fixtures

驗收場景（`features/*.feature`）使用的測試資料。假工具與假來源（`features/internal/faketool`、
`features/internal/fakesource`）從這裡讀取內容；設計見 [ADR-0015](../../docs/adr/0015-acceptance-test-harness.md)。

## 目錄結構

| 目錄 | 內容 | 誰讀取 |
|---|---|---|
| `captions/` | 平台字幕軌：YouTube 是 VTT，例如 `en-manual.vtt`；Bilibili 是字幕 JSON（`body[{from, to, content}]`），例如 `bilibili-ai-zh.json` | 假 yt-dlp 寫字幕時 |
| `sources/` | 字幕來源（ASR、Supadata）回傳的 JSON，例如 `supadata-en.json`、`groq-en.json` | `fakesource` 的 handler |
| `ytdlp/` | 假 yt-dlp 的輸出樣本（info JSON 的片段、錯誤輸出）；播放清單網址的 `the URL … serves "<name>"` 讀這裡，例如 `playlist-3-videos.json` | 假 yt-dlp（`--flat-playlist -J`） |
| `http/` | fake origin 提供的 HTTP 內容（`the URL … serves "<name>"`，網址不是 YouTube／Bilibili 時），例如網頁、RSS feed | `fakenet` 上的 handler |
| `items/` | `Given the item … has been processed up to …` 使用的影音資料 | steps |
| `media/` | `Given the audio of … is "<name>"` 使用的音訊屬性：一行假媒體標記（沒有 item），帶 `duration`、`compressed`（ASR 前處理之後的大小）、`silences`，例如 `audio-40mb-after-compression.m4a`、`speech-with-pause-at-6.2s.m4a`（靜音 6.2–6.5 秒，SEG-R8） | steps（併進 inbox 檔或假 yt-dlp 的標記） |
| `local/` | `Given a local file "<name>" in the inbox` 與 `When the owner uploads "<name>"` 使用的本機檔案：影音（標記加填充）、sidecar 字幕、不是影音的檔案，以及內嵌字幕的內容（`embedded/`） | steps（複製到 inbox 或上傳）、假 ffmpeg（抽內嵌字幕） |
| `podcast/` | `Given a podcast feed … with the episodes:` 的音檔（假媒體檔），以及 `the source "podcast-transcript" returns "<檔名>" for …` 的字幕（`.vtt`、`.srt`、`.json`） | steps（交給 `fakepodcast`） |
| `llm/` | fake LLM provider 的回應 | fake LLM |

目錄在第一個 fixture 出現時才建立；沒有用到的目錄不需要預先放空檔案。

## 命名規則

- 檔名使用場景裡出現的名稱，讓人可以從場景直接找到檔案。
  - `Given the source "supadata" returns "supadata-en.json" for "yt:abc123xxxxx"` 對應 `sources/supadata-en.json`。
  - 字幕軌的 fixture 同理，放在 `captions/`。
- 名稱用小寫英文、數字與 `-`，語言標記放在名稱裡，例如 `groq-no-language-ja.json`。
- 一個 fixture 對應一種情境。不同情境不要共用檔案後再靠欄位覆寫；需要差異就新增檔案。
- fixture 要小：只放場景驗證所需的內容。大量內容改用「產生 N 句」（見下方）。
- 不放真實的 API key、cookie、個人資料或從真實服務複製的受版權保護內容。

### 特殊名稱

- `http-<狀態碼>`（例如 `http-503`）：不是檔案，`fakesource` 直接以該 HTTP 狀態碼回應。

## 字幕來源的回應格式

- `supadata-*.json`：Supadata `GET /v1/transcript` 的回應（`lang`、`availableLangs`、`content[{text, offset, duration, lang}]`，時間單位為毫秒）。
  `supadata-in-german.json` 是德文（`lang: de`），用來驗證語言不符時不採用（SRC-R4、R17）。
- `groq-no-language-ja.json`：Groq 回報 `language: "japanese"`，用來驗證 metadata 沒有原文語言時由 ASR 偵測（SRC-R11）。
- `groq-two-chunks.json`：約 80 個 segment、單一絕對時間軸（0 到 3004 秒）：#1–#40 在 1497 秒之前結束，
  #41（1498–1502 "and that is where the overlap starts"）與 #42（1502–1506 "so we remove the duplicate line"）落在兩個切塊的重疊區，
  之後到 3004 秒。切塊上傳時（標記有 `offset`），假 Groq 只回傳完全落在 `[offset, offset+duration]` 內的 segments，
  時間減去 offset，所以兩塊都含有 #41、#42，接縫的去重由程式負責（SRC-R12）。
- `groq-*.json`：Groq（OpenAI 相容 `POST /audio/transcriptions`）的 `verbose_json` 回應：
  `language`（全名，例如 `english`，不是 `en`）、`duration`（秒）、`segments[{id, start, end, text}]`（秒）。
  fake 的端點要求請求帶 `response_format=verbose_json`，並以 `Authorization: Bearer <key>` 驗證；
  測試環境預設有 key（`NOTESTEME_GROQ_API_KEY`），`Given the provider "groq" has no API key` 才會移除。

## 影音 fixture（`items/`）

`Given the item … has been processed up to …` 的影音資料。檔名是影音代號，把 `:`、`/` 換成 `-`
（`yt:pg100xxxxxx` → `items/yt-pg100xxxxxx.json`，`bili:BV1xx411c7mD/p1` → `items/bili-BV1xx411c7mD-p1.json`）：

```json
{"ref":"yt:pg100xxxxxx","source":{"lang":"en","cues":[{"start":"00:00:01.000","end":"00:00:03.000","text":"…"}]}}
```

- `ref` 必須與檔名一致，不一致時步驟失敗。
- `source` 是 `source` 變體的字幕：`lang` 與 cue（`start`、`end`、`text`；時間為 `HH:MM:SS.mmm`）。
  cue 保持乾淨（不重疊、長度正常），正規化之後每個 cue 的時間來源（timing）都是 `source`。
- 適用條件（三條規則，依序判斷）：
  1. 場景已準備字幕來源（寫入字幕、符合 `caption_priority` 的平台字幕軌、腳本化的已實作 fallback 來源）：
     不看 fixture，照常執行 pipeline。
  2. 代號是 `yt:` 或 `bili:`（假 yt-dlp 管的平台）、假 yt-dlp 中這部影片完全沒有字幕軌、場景也沒有寫入字幕或腳本化來源，
     而且 fixture 存在：先建立影音並做到 metadata，再用 fixture 寫入 `source`，接著照常執行剩下的步驟（例如 normalize）。
     `local:`、`pod:` 一律不套用 fixture，即使檔案存在也一樣。
  3. 其他情況（沒有 fixture、不符合上面的條件，或代號是 `local:`、`pod:`）：步驟 pending。
- 目標步驟在 transcript 之前（例如 `metadata`）時不需要字幕，不使用 fixture。
- 目標是 M2 的步驟（`segment`、`correct`、`translate`、`publish`）時，語意等 X1 決定，目前一律 pending，
  不會用到 fixture（steps.md 4.4 規則 (2c)）。這些 fixture 目前只由單元測試驗證。

### `generate`：用規則產生大量 cue

`source` 可以用 `generate` 取代 `cues`（兩者只能有一個）。`generate` 是區塊的陣列：

```json
{"ref":"yt:long01xxxxx","source":{"lang":"en","generate":[
  {"count":400,"step":"3s","length":"2.5s","text":"Line {n} of the long talk about slow and steady systems that scale to users"}
]}}
```

- `count`：這個區塊的句數。`step`：相鄰兩句開始時間的間隔。`length`：每句的長度，必須在 300ms 與 `step` 之間
  （太短會被 NRM-R2 併入前一句；超過 `step` 會被 NRM-R4 截短）。`step`、`length` 是 Go 的時間長度（`3s`、`7.5s`、`300ms`）。
- `text`：範本，`{n}` 換成從 1 起算的全域 cue 編號（補零到 4 位，跨區塊連續）。
- 每個區塊接在前一個區塊的結尾（前一個區塊的開始時間加上 `count × step`）；第一個區塊從 `00:00:00.000` 開始。
  可用 `start`（`HH:MM:SS.mmm`）明確指定。

M2 的影音 fixture 與用途（限制由 `features/steps/processing_test.go` 驗證）：

| 檔案 | 語言 | 內容 | 用途 |
|---|---|---|---|
| `yt-long01xxxxx` | en | 400 句，每 3 秒一句；文字固定 76 個 ASCII 字元，每句 20 tokens | LLM Background；LLM-R1 的 1-200、198-400 |
| `yt-chap01xxxxx` | en | 三章 40、70、40 句的短句（約 8 tokens）；第 41 句從 05:00、第 111 句從 12:00 開始 | LLM-R1 章節切塊 |
| `yt-long08xxxxx` | en | 1600 句，同 long01 的每句長度 | AGT Background 的 8 個 chunk |
| `yt-talk01xxxxx` | en | 120 句，每 5 秒一句（約 10 分鐘），Kafka 主題，涵蓋 06:00–08:00 | PRF、PRM、TRN |
| `yt-zh001xxxxxx` | zh-Hans | 12 句簡體中文，主題是服務拆分 | COR-R4、SEG-R10 |
| `yt-mix001xxxxx` | zh | 12 句中英夾雜 | COR-R5 |
| `yt-tw001xxxxxx` | zh-Hant-TW | 10 句繁體中文 | TRN-R10 |

- token 估算依 LLM-execution 待決問題 L2：漢字、假名、諺文一字算 1，其他字元每 4 個算 1（無條件進位），每句再加 1。
- 這些 fixture 的文字不可以含有場景要驗證「由 production 加入」的字串（例如 `Apache Kafka`、`KRaft`、`<transcript_data>`、
  `cache_control`、`数据库`、`保留原本的語言`），否則那些 `contains` 斷言會恆真；清單在 `TestVideoFixturesCommon`。

## 字幕軌

假 yt-dlp 的字幕軌（`faketool.Track`）有三種內容來源：

1. 指定 `captions/` 內的檔名：原樣寫出。
2. 指定句數：產生該句數的 VTT。
3. 都沒有指定：**預設產生 120 句**。場景中 `has 120 cues` 的斷言依賴這個預設，不要改動。

產生的 VTT 每句 2 秒，內容為 `Sentence <i> of <影片 ID> (<語言>).`。

字幕軌的種類：`manual`（人工）、`auto`（自動，Bilibili 的 AI 字幕屬於這類）、
`platform-translated`（平台機器翻譯）。

### Bilibili 字幕軌

Bilibili 的 fixture 是 Bilibili 的字幕 JSON（`body[{from, to, content}]`，單位秒，例如 `bilibili-ai-zh.json`）。
假 yt-dlp 依真實的 yt-dlp 行為處理：

- 轉成 SRT。`-J --write-subs` 把 SRT 放在 `subtitles` 欄位的 `data`（`ext: srt`），鍵是 Bilibili 的 `lan`：
  `manual` 的鍵是語言，`auto`（AI 字幕）的鍵是 `ai-<語言>`；另有一個 `danmaku`（xml）。沒有 `automatic_captions`。
  下載時 `--write-subs` 寫出 `<樣板>.<鍵>.srt`（`--sub-format vtt` 仍然是 srt），`--write-auto-subs` 什麼都不寫。
- 沒有 fixture 的字幕軌依句數產生 SRT（預設 120 句）。
- `Given the source "bilibili-ai" requires the cookie "bilibili"` 之後，假 yt-dlp 沒有收到有效的 cookie 檔
  （`--cookies` 指到 Netscape 格式、含 `.bilibili.com` 網域的 `SESSDATA`）時，不列出也不下載該種類的字幕軌，
  結束碼 0，stderr 印 `WARNING: [BiliBili] Subtitles are only available when logged in. …`（有 `--no-warnings` 時不印）。
- 影片沒有 `p` 參數而且有多個分P 時，`-J` 回傳 `url` entries（`https://www.bilibili.com/video/<BV>?p=<n>`）；
  有 `p` 時，info 的 `id` 是 `<BV>_p<n>`。用 `uploader`、`uploader_id`，沒有 `channel`、`language`、`availability`。
- 場景代號 `bili:<BV>/p<n>` 對應假工具狀態中的 `<BV>/p<n>`（標題、字幕軌逐分P 設定）。

### cookie 檔

假 yt-dlp 依真實的行為載入 `--cookies` 的檔案：第一行必須是 `# Netscape HTTP Cookie File`，其餘每行是
tab 分隔的 7 個欄位，否則回報 `does not look like a Netscape format cookies file`、結束碼 1。
會員限定（`availability: subscriber_only`）的 YouTube 影片，下載字幕與媒體時沒有含 `.youtube.com` cookie 的檔案就失敗
（`取得資訊` 不失敗，讓辨識網址的 Probe 仍能看到 availability）。每次呼叫都記錄 cookie 檔的路徑、權限與程序 ID
（`calls.jsonl`），`the cookie file given to yt-dlp has the mode` 以此判斷。

## 假媒體檔

假 yt-dlp 下載媒體、`local/` 的影音檔、假 ffmpeg 的輸出，內容都是一行標記（標記 v2）：

```
NOTESTEME-FAKE-MEDIA item=<代號> duration=<秒>[ streams=<type>/<codec>[/<lang>[/<fixture>]],…][ compressed=<bytes>][ silences=a-b,…][ offset=<秒>]
```

- `item` 是平台 ID，例如 `abc123xxxxx`、`BV1xx411c7mD`；Bilibili 第 2 分P 以後為 `BV1xx411c7mD/p2`；
  本機檔案使用場景中的代號，例如 `local:meetup.mp4`。`media/` 的屬性 fixture 沒有 item。
- `streams`：沒有時等於一條音訊串流（`audio/aac`）。`type` 是 `audio`、`video`、`subtitle`、`data`、`attachment`；
  `lang` 用 ISO 639-2（`eng`、`jpn`），可以省略；`fixture` 是相對 `local/` 的路徑（內嵌字幕的內容，可含 `/`）。
  文字字幕（`subrip`、`mov_text` 等）要有 fixture；沒有 fixture 的字幕串流視為點陣字幕，抽取會失敗。
- `compressed`：ASR 前處理（轉成 opus）之後的檔案大小；假 ffmpeg 轉碼的輸出以 sparse `Truncate` 補到這個大小，內容開頭是新的標記。
- `silences`：靜音區間（原始時間軸的秒），假 ffmpeg 的 silencedetect 依此輸出。
- `offset`：這是從原檔切出來的一塊（起點在原檔第 offset 秒，長度為 `duration`）；`offset=0` 也算（第一塊）。
- 假 ffprobe 讀標記回報長度與串流（含 `tags.language`、`disposition`）；每種類型的第一條串流是預設串流（`disposition.default`）。
- fake ASR 讀取上傳檔案的標記，決定回傳哪個 `sources/` fixture；有 `offset` 時只回傳該區間內的 segments。
- 檔案內容還可以在標記之後接任意填充（用來模擬檔案大小）。
- 標記的產生與解析在 `faketool.FormatMedia`、`faketool.ParseMedia`（`MediaMarker`、`ParseMediaMarker` 是沒有 streams 時的簡化版）。

### `local/` 的檔案

| 檔案 | 內容 |
|---|---|
| `talk.mp4`、`talk-copy.mp4` | 位元組完全相同（`item=local:talk.mp4`，影片加音訊）：驗證內容相同只新增一次 |
| `notes.pdf` | 不是標記的文字：不是影音 |
| `lecture.mp4` | 影片、英文音訊與一條內嵌文字字幕（`mov_text`，內容 `embedded/lecture-en.srt`），8 KiB（Range 要有內容） |
| `lecture.en.srt` | sidecar 字幕（真正的 SRT，內容與內嵌字幕不同，才分得出用了哪一個） |
| `meetup.mp4`、`long-talk.m4a` | 沒有字幕的影片、音訊：要靠 ASR |
| `movie-with-subs.mkv` | 影片、音訊與內嵌 `subrip` 字幕（`embedded/movie-en.srt`） |
| `lecture.m4a`、`talk.m4a` | 上傳用，只有音訊 |
| `embedded/*.srt` | 內嵌字幕的內容，由假 ffmpeg 的抽字幕複製 |

放進 inbox 的檔案，修改時間設為假時鐘的現在（`os.Chtimes`），inbox 的穩定判斷才會依假時鐘進行。

### 假 ffprobe 與 ffmpeg 的參數

假工具照 production 契約逐項檢查參數：缺少必要選項、值不同或有契約以外的選項，一律結束碼 2（stderr 說明原因），
每次呼叫都記在 `calls.jsonl`。輸入不存在或不是標記時結束碼 1（`Invalid data found when processing input`）。

- ffprobe：`-v error -hide_banner -protocol_whitelist file -format_whitelist <清單> -show_format -show_streams -of json <path>`。
  `-protocol_whitelist file` 與 `-format_whitelist` 必須有，清單不能含 `hls`、`concat`。
- ffmpeg 轉碼（ASR 前處理）：`-nostdin -hide_banner -v error -protocol_whitelist file -format_whitelist <清單> -i <in> -vn -sn -dn -map 0:a:0 -ac 1 -ar 16000 -c:a libopus -b:a 32k -application voip -f ogg -y <out>`；
  切塊在 `-i` 前加 `-ss <S> -t <L>`，輸出標記帶 `offset`、`duration`，大小依時長比例。
- ffmpeg 靜音偵測：`-nostdin -hide_banner -nostats -v info -protocol_whitelist file -ss <from> -t <len> -i <prepared> -af silencedetect=noise=-35dB:d=0.3 -f null -`；
  stderr 輸出 `[silencedetect @ 0x…] silence_start: X` 與 `silence_end: Y | silence_duration: Z`，時間相對視窗起點，跨出視窗的靜音在邊界截斷。
- ffmpeg 抽字幕：`-nostdin -hide_banner -v error -protocol_whitelist file -format_whitelist <清單> -i <in> -map 0:<idx> -c:s srt -f srt -y <out>`；
  把串流指定的 `local/` fixture 複製到輸出；串流不是字幕或沒有 fixture（點陣字幕）時失敗。

平台來源用 `Given the source "youtube-manual" / "youtube-auto" returns "<檔名>" for …` 腳本化時，
步驟把 `captions/<檔名>` 寫進該影片對應種類的字幕軌（語言取自這個種類既有的字幕軌，沒有就用影片的語言）。
注意：目前假 yt-dlp 一律把內容當 VTT 寫出，所以 `auto-zh-en-mixed.json` 的內容是 VTT，只是沿用場景中的檔名；
`youtube-auto` 改用 json3 之前（NRM 待決問題）維持這樣。

### `podcast/` 的檔案

音檔是假媒體檔（標記 v2，沒有 `item`）；`fakepodcast` 提供時把標記的 `item` 改成這一集的代號（`pod:<feed>/<guid>`），
後面的填充原樣保留，支援 HEAD 與 Range。標記多一個只給 feed 用的欄位：

- `listed=<秒>`：feed 的 `<itunes:duration>` 標示的長度；沒有時 feed 沿用標記的 `duration`（即 ffprobe 量到的長度）。
  `ParseMedia` 忽略這個欄位，提供出去的音檔也不帶它（`FormatMedia` 不輸出）。

| 檔案 | 內容 |
|---|---|
| `ep41.mp3` | `duration=1500`，音訊 |
| `ep42.mp3` | `duration=1800`，音訊（GRD 的 `https://rebind.example.com/ep42.mp3` 也取這個檔案：完整網址取 path 的檔名） |
| `ep43-with-extra-ads.mp3` | `duration=3720 listed=3600`：音檔比 feed 標示的多 120 秒（插播廣告） |
| `ep42-transcript.vtt` | 真的 WEBVTT，3 句，最後一句結束於 00:29:58.000，與 `ep42.mp3`（1800 秒）相差 0.11%：不提醒 |
| `ep43-transcript.vtt` | 3 句，最後一句結束於 01:00:00.000，與 `ep43-with-extra-ads.mp3`（3720 秒）相差 120/3720 = 3.23%：超過 2%，提醒「時間可能偏移」（SRC-R15） |

- feed 網址是 `https://feeds.example.com/<名稱>.xml`，每次請求都依當時的狀態產生 RSS：`<language>en</language>`、
  enclosure 的 `length` 是實際提供的位元組數、`<itunes:duration>`；字幕被腳本化之後才出現 `<podcast:transcript>`。
- `audio` 欄位是檔名時，網址為 `https://media.example.com/<名稱>/<檔名>`；是完整網址時登記到那個網址（fixture 取 path 的檔名）。
  字幕的網址同樣是 `https://media.example.com/<名稱>/<字幕檔名>`。
- feed、音檔與字幕都登記在 fakenet 上，連線與 DNS 解析走 fakenet 的序列，所以 `the host … resolves to … and then …` 對它們有效。
- fixture 不存在、音檔不是假媒體檔時，步驟立即失敗。

## LLM 的回應（`llm/`）

fixture 有三種形式（`features/internal/fakellm/fixture.go`）：靜態的 `output`、不是 JSON 的 `text`，以及依請求的 `<transcript_data>` 產生輸出的 `generator`（可帶 `args`）。

- `echo_cues`：`{"cues": [{i, text}]}`，一對一的步驟（correct）用；`args` 有 `drop_last`、`extra`、`replace`。
- `segment_breaks`（SEG）：`{"text", "breaks"}`，`text` 是 cues 的 text 以一個空白接起來；`args.mode`：
  `keep`（每句都斷開，不合併也不切分）、`merge`（沒有斷點，整個 chunk 合併）、`inside_word`（只有一個斷點，切在第一個拉丁字詞中間，對不回輸入）。
  `segment-keep-all`、`segment-merge-all`、`segment-unalignable` 用它；其他 `segment-*` 是靜態輸出，只對場景中寫出的那一句有效
  （`breaks` 是新字幕在 `text` 中開始的字元位置，例如 `segment-split-after-kafka` 的 14 是 `It is fast.` 的開頭）。
- `translate_by_target`（`translate-echo-target`）：`args.texts` 的 key 是目標語言、value 是每一句的譯文模板（`{i}` 換成 cue 編號）。
  目標語言取自系統指示中的「`目標語言：<lang>。`」（`internal/llm` 的 `TranslateSpec` 寫在輸出規則中）；這句被改寫時 generator 會回 400 並記違規，
  要一起修改。輸入的 text 含 n-1 個切分標記 `⟨|⟩` 時，輸出也有 n 段。模板的長度要讓長度比落在 0.3 到 3.0 之間（TRN-R5），
  中文模板要用繁體專用字、日文模板要有假名（語言偵測）。
- Agent 的提交（AGT 的 `the agent … submits …`）用同一份 fixture：harness 以 Agent 領取到的 chunk（`context`、`cues`）當成 `<transcript_data>` 產生輸出
  （`fakellm.RenderSubmission`）。頂層可以多一個 `model`：Agent 回報的模型（`submit_task` 的 `model`，AGT-R10），fakellm 回答 API 請求時不使用。
  `correct-echo-reported-model` 是 `echo_cues` 加上 `model: fixture-model`。
- `translate-*`：TRN 場景的靜態譯文。`translate-group-*` 與 `translate-per-cue` 的句號對應場景中的 cue；
  `translate-way-too-long` 的長度比超過 3.0，`translate-long-for-short-cue` 的長度比正常但每秒字數超過 9（TRN-R6）。

## 新增 fixture 的步驟

1. 先在場景中寫出名稱（使用 `steps.md` 已有的句型）。
2. 在對應的目錄建立同名檔案，內容盡量小。
3. 執行 `go test ./features/...`，確認場景讀得到檔案；找不到檔案時，假工具與假來源會明確回報
   檔名，而不是靜默回傳空內容。

## M3 的假服務（2026-11-23，規格展開時設計，接進 harness 由 M3 的實作切片負責）

M3 的步驟（`docs/specs/steps.md` 4.13～4.16）目前都是 pending 的步驟樁。下列假服務已有可以單獨測試的骨架，
還沒有登記到 fakenet 或 `faketool.Dispatch`；實作切片接上它們時，要確認預設 tag 的場景不退步。

| 套件 | 取代 | 狀態與 fixture | 接到哪裡 |
|---|---|---|---|
| `features/internal/fakesponsorblock` | SponsorBlock 的 hash 前綴 API（`sponsor.ajay.app`，SPB-R2） | 由表格步驟登記片段，沒有檔案；沒有片段時回 404 | fakenet；**每個場景預設登記**（ADR-0020 第 2 點），既有的 YouTube 場景才不會撞上 trap |
| `features/internal/fakeframes` | ffmpeg 的場景偵測（`showinfo` 的 stderr）與畫面擷取（純色 JPEG） | 由 `the video of … has the scenes:` 的表格登記，沒有檔案 | 假 ffmpeg（`faketool/ffmpeg.go` 依參數分派：`select='gt(scene,T)',showinfo` 與 `-ss <t> -frames:v 1`）；假 yt-dlp 為有場景的影片提供影像串流與格式清單（`the best video format of …`） |
| `features/internal/fakegemini` | Gemini `generateContent`（GEM-R4） | 回答讀 `llm/` 的 fixture（`{"output": …}`，output 包成 `candidates[0].content.parts[0].text`）；片長與公開狀態取自假 yt-dlp 的影片 | fakenet（`generativelanguage.googleapis.com`）；LLM 步驟的 provider `gemini` |
| `features/internal/fakegit` | `git` CLI 與遠端（VAULT-R7～R9） | 狀態在場景目錄的 `fake/git/`（`remote.json`、`calls.jsonl`、clone 中的 `.git/fakegit.json`） | `faketool.Dispatch` 依 argv[0] `git` 分派；以假工具自己的位置找到場景（產品會清掉 git 的環境變數）；第一次 clone `https`／`ssh` 網址時自動建立空的假遠端 |
| `features/internal/fakehook` | 使用者的 hook 程式（VAULT-R10、R11） | 紀錄在場景目錄的 `fake/hooks.jsonl` | `faketool.Dispatch` 依 argv[0] `notesteme-test-hook` 分派 |

- vision 選圖（FRM-R7）用既有的 fakellm，步驟名稱 `frames`；fakellm 要接受圖片內容區塊（FRM 規格 F4）。
- `fs` vault 寫到場景的 `/vault`（`<場景根目錄>/vault`，steps.md 第 4 節的路徑對應）。

### M3 的 LLM fixture（`llm/`）

| fixture | 用途 |
|---|---|
| `chapters-two`、`chapters-missing-cue`、`chapters-out-of-order`、`chapters-title-too-long`、`chapters-in-sponsor` | CHAP：兩個章節（cue 2、4）與四種驗證失敗 |
| `frames-pick-six`、`frames-pick-missing` | FRM-R7：第 1 張拼圖的 6 個編號、不存在的編號 12 |
| `note-minimal`、`note-two-sections`、`note-with-frames` | NOTE：一節（cue 1）、兩節（cue 2、4，內文引用 cue 5）、引用畫面 1 與 3 |
| `note-missing-cue`、`note-unselected-frame`、`note-starts-with-level-3` | NOTE-R3 的驗證失敗 |
| `note-hostile` | NOTE-R8：HTML、`javascript:` 連結、內文標題、`![[…]]`、Templater 與 `dataviewjs` |
| `note-map-part-1`～`3`、`note-reduce` | NOTE-R6 的 map-reduce |
| `gemini-note-two-sections`、`gemini-note-past-end`、`gemini-note-hostile` | GEM：以時間（`start`）代替 cue 的輸出 |

這些 fixture 的文字不含場景要驗證「由 production 加入」的字串（時間連結、`&lt;script&gt;`、`**Injected heading**` 的粗體），否則斷言會恆真。

## M4 的假服務（2026-11-23，規格展開時設計，接進 harness 由 M4 的實作切片負責）

M4 的步驟（`docs/specs/steps.md` 4.17～4.21）目前都是 pending 的步驟樁。下列假服務已有可以單獨測試的骨架（`features/internal/fakefeed`、`fakenotify`、`fakebatch`、`fakeoidc`），還沒有登記到 fakenet 或 `faketool.Dispatch`；實作切片接上它們時，要確認預設 tag 的場景不退步。

### 訂閱、SRC-10、ENTRY

| 套件 | 取代 | 狀態與 fixture | 接到哪裡 |
|---|---|---|---|
| `features/internal/fakefeed` | YouTube 頻道的 RSS（`www.youtube.com/feeds/videos.xml?channel_id=`，Atom）；另外提供產生 RSS 2.0、OPML、超大回應、畸形 XML、含 `<!DOCTYPE` 的 XML 的函式 | `Server`：影片清單（`SetVideos`，由 harness 在每次請求時從假 yt-dlp 的影片同步，或直接 `Add`）、每個頻道的失敗狀態碼（`Fail`／`Recover`）、請求紀錄（`Requests(channelID)`）；`SetRaw(path, contentType, body)` 讓任意路徑回應固定內容。產生器：`Atom`、`RSS`、`OPML`、`Oversized`、`Malformed`、`WithDoctype` | fakenet 的 `www.youtube.com`（只限 `/feeds/videos.xml`；其他 YouTube 網址仍由假 yt-dlp 處理、不經 HTTP）；由 `the YouTube feeds are served from the registered videos` 登記，**不**預設登記（只有 SUB 場景需要） |
| 假 yt-dlp（`features/internal/faketool`，既有） | 頻道 `/videos` 的 flat-playlist、`@handle` 的解析、Bilibili 合集的 flat-playlist | 頻道清單從已登記的影片產生；Bilibili 合集用 `features/fixtures/ytdlp/` 的 fixture | 既有的 `faketool.Dispatch`；SUB 切片擴充 `ytdlp.go` 的網址判斷 |
| `features/fixtures/ytdlp/sub-bili-collection.json` | Bilibili 合集 `9:77` 的 flat-playlist 輸出 | 兩個 BV 號：`BV1co411c7mA`、`BV1co411c7mB` | `the URL "https://space.bilibili.com/9/lists/77?type=season" serves …` |
| `features/fixtures/ytdlp/sub-playlist-2-videos.json` | 第二個播放清單 `PL456` | 兩部影片：`pl2v01xxxxx`、`pl2v02xxxxx` | SUB-R10 的 rate limit 場景 |
| `features/fixtures/http/sub-feed-two-episodes.xml` | 兩集的 Podcast RSS（guid `sub-ep-1`、`sub-ep-2`） | 靜態檔 | DNS rebinding 與 allowlist 的場景（只建立基準清單，不下載音檔） |
| `features/fixtures/http/sub-hostile-feed.xml` | 含 HTML、`<script>`、雙向控制字元、`javascript:` enclosure 的 feed | 靜態檔；清理後的標題是 `Hostile Show`、`Episode 1` | SUB-R11 |
| `features/fixtures/http/sub-malformed.xml`、`sub-doctype.xml` | 沒有關閉標籤的 XML、含 entity 定義的 `<!DOCTYPE` | 靜態檔；分別含標記字串 `SUB-MALFORMED-MARK`、`SUB-ENTITY-EXPANDED`；log 中出現它們代表產品把 feed 的內容寫進 log（SUB-R12） | SUB-R11、SUB-R12 |

### 摘要、通知與無人值守

| 套件 | 取代 | 狀態與 fixture | 接到哪裡 |
|---|---|---|---|
| `features/internal/fakenotify` | ntfy 的 JSON 發布 API（`POST <server>/`）、Discord webhook（`POST /api/webhooks/<id>/<token>`）、Telegram Bot API（`POST /bot<token>/sendMessage`）、一般 webhook 的接收端 | 狀態在記憶體：每個請求記錄 method、host、path、query、標頭、原始 body 與收到時間；可以設定某個端點「接下來 N 次」或「一律」回指定狀態碼（429 加 `Retry-After`）；`VerifySignature` 獨立計算 HMAC。沒有檔案 fixture | fakenet：`ntfy.sh`、`discord.com`、`api.telegram.org`、`hooks.example.com`（`the notification endpoints are reachable` 才登記，不是每個場景預設，所以既有場景不受影響） |
| `features/fixtures/llm/summary-*.json` | `summary` 步驟的 LLM 回答（步驟名稱 `summary`，tool／schema `notesteme_summary`） | `summary-kernel`（正常）、`summary-too-many-points`（8 個重點，DIG-R3、R4 的驗證失敗） | 既有的 fakellm |

- 假 yt-dlp 的呼叫統計、磁碟用量與 cookie 的最後成功時間都經 `TestControls` 的 hook 寫入，不新增假工具。
- 每日備份用既有的備份程式碼與真的檔案系統；失敗用「目錄其實是一般檔案」製造（`Given the file … contains:`），不需要假服務。

### 成本與 Batch API

| 套件 | 取代 | 狀態與 fixture | 接到哪裡 |
|---|---|---|---|
| `features/internal/fakebatch` | Anthropic Message Batches（`POST /v1/messages/batches`、`GET /v1/messages/batches/{id}`、`GET …/{id}/results`、`POST …/{id}/cancel`）與 OpenAI Batch API（`POST /v1/files`、`POST /v1/batches`、`GET /v1/batches/{id}`、`POST /v1/batches/{id}/cancel`、`GET /v1/files/{id}/content`） | 狀態在記憶體；完成時間以注入的 clock 判斷。每個請求的回答由 `Answerer` 產生：harness 接 fakellm（共用 4.6 的 answers，不記入一般請求數）；套件內建的 `FixtureAnswerer` 直接讀 `features/fixtures/llm/<名稱>.json` 的 `{"output": …}`，依 tool／schema 名稱 `notesteme_<stage>` 選 fixture，給單元測試用 | fakenet：和 fakellm 同一個主機（`api.anthropic.com`、`api.openai.com`），依路徑分派：`/v1/messages/batches*`、`/v1/batches*`、`/v1/files*` 給 fakebatch，其他給 fakellm。沒有 `the batch API of …` 步驟的場景，batch 路徑回 404 並記一筆違規（產品不應該在 `batch: off` 時呼叫） |

- 不需要新的 fixture 檔：答案沿用 `features/fixtures/llm/`（`correct-echo`、`correct-kafka-kraft`）。
- fakellm 要加兩個測試控制（COST）：固定的 `usage`（`the LLM usage reported by …`）與 `usage.cost`（`the LLM cost reported by …`），也提供給 fakebatch 的結果使用。

### 搜尋、bundle、術語審核與進階登入

| 套件 | 取代 | 狀態與 fixture | 接到哪裡 |
|---|---|---|---|
| `features/internal/fakeoidc` | OpenID Connect provider（AUTH-07） | `Server`（`New(Config{Issuer, ClientID, ClientSecret, Subject, Now})`）：discovery（`issuer` 是設定的字串，不是 httptest 的 URL）、`/jwks`（一把 RS256 公鑰，`kid: fake-key-1`）、`/authorize`（不顯示畫面，以 `Subject` 同意；`Deny(code)` 改回傳 `error`）、`/token`（`authorization_code`、`client_secret_basic`、PKCE 只接受 `S256`、code 只能用一次）。`SetFault` 讓之後的 id_token 帶缺陷（`Faults` 列出全部：壞簽章、不在 JWKS 的 key、`alg: none`、以 client secret 簽的 `HS256`、錯的 `iss`／`aud`／`nonce`、`exp` 早 30／120 秒、`iat` 晚 30／120 秒）；`TokenRequests()` 記錄每個 token 請求的 client 驗證與 PKCE 結果。時間取自注入的 `Now`。單元測試內有一個最小的 verifier，證明每種缺陷真的驗證失敗（30 秒的兩種在 60 秒偏差內通過）。只用標準函式庫 | 實作切片在 `features/steps/signin.go` 的 `the OIDC provider … signs in the subject …` 建立它，以 fakenet 把 issuer 的主機導到 `httptest` server（TLS；harness 的 CA 要被產品信任，同其他 https 假服務），`Now` 接場景的 fake clock |
| （不需要新套件）bundle 產生器 | 惡意與合法的 `.notesteme.zip` | 在 `features/steps/library.go` 內以 `archive/zip` 於執行時產生（BND 的步驟），不 commit 二進位 fixture；`hardlink`、`declared_size`、Unix mode 用 `CreateRaw` 與 `FileHeader.ExternalAttrs`／`CreatorVersion`（Unix）設定。大檔一律由場景調低設定檔的上限後用小檔觸發；最大的項目是 2 MiB 的全零檔（壓縮比）與 1.5 MiB 的亂數檔（上傳上限） | 只在步驟中使用 |
| （不需要新 fixture）SRCH、GLOS | — | SRCH 與 GLOS 的字幕以 seeding 句型寫入；LLM 沿用既有的 `correct-kafka-kraft`、`correct-echo` fixture | — |

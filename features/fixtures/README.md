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
| `media/` | `Given the audio of … is "<name>"` 使用的音訊描述（長度、大小） | 假 yt-dlp、fake ASR |
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
  2. 假 yt-dlp 中這部影片完全沒有字幕軌、場景也沒有寫入字幕或腳本化來源，而且 fixture 存在：
     先建立影音並做到 metadata，再用 fixture 寫入 `source`，接著照常執行剩下的步驟（例如 normalize）。
  3. 其他情況（沒有 fixture 或不符合上面的條件）：步驟 pending。
- 目標步驟在 transcript 之前（例如 `metadata`）時不需要字幕，不使用 fixture。

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

假 yt-dlp 下載媒體時，寫出的檔案內容只有一行標記：

```
NOTESTEME-FAKE-MEDIA item=<平台ID> duration=<秒>
```

- `item` 是平台 ID，例如 `abc123xxxxx`、`BV1xx411c7mD`；Bilibili 第 2 分P 以後為 `BV1xx411c7mD/p2`；
  本機檔案使用場景中的代號，例如 `local:meetup.mp4`。
- 假 ffprobe 讀取這行標記回報長度；fake ASR 讀取上傳檔案的標記，決定回傳哪個 `sources/` fixture。
- 檔案內容還可以在標記之後接任意填充（用來模擬檔案大小）。
- 標記的產生與解析在 `faketool.MediaMarker`、`faketool.ParseMediaMarker`。

平台來源用 `Given the source "youtube-manual" / "youtube-auto" returns "<檔名>" for …` 腳本化時，
步驟把 `captions/<檔名>` 寫進該影片對應種類的字幕軌（語言取自這個種類既有的字幕軌，沒有就用影片的語言）。
注意：目前假 yt-dlp 一律把內容當 VTT 寫出，所以 `auto-zh-en-mixed.json` 的內容是 VTT，只是沿用場景中的檔名；
`youtube-auto` 改用 json3 之前（NRM 待決問題）維持這樣。

## 新增 fixture 的步驟

1. 先在場景中寫出名稱（使用 `steps.md` 已有的句型）。
2. 在對應的目錄建立同名檔案，內容盡量小。
3. 執行 `go test ./features/...`，確認場景讀得到檔案；找不到檔案時，假工具與假來源會明確回報
   檔名，而不是靜默回傳空內容。

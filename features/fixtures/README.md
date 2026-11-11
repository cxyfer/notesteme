# features/fixtures

驗收場景（`features/*.feature`）使用的測試資料。假工具與假來源（`features/internal/faketool`、
`features/internal/fakesource`）從這裡讀取內容；設計見 [ADR-0015](../../docs/adr/0015-acceptance-test-harness.md)。

## 目錄結構

| 目錄 | 內容 | 誰讀取 |
|---|---|---|
| `captions/` | 平台字幕軌的 VTT 檔，例如 `en-manual.vtt` | 假 yt-dlp 寫字幕時 |
| `sources/` | 字幕來源（ASR、Supadata）回傳的 JSON，例如 `supadata-en.json`、`groq-en.json` | `fakesource` 的 handler |
| `ytdlp/` | 假 yt-dlp 的輸出樣本（info JSON 的片段、錯誤輸出） | 假 yt-dlp |
| `http/` | fake origin 提供的 HTTP 內容（`the URL … serves "<name>"`），例如網頁、RSS feed | `fakenet` 上的 handler |
| `items/` | `Given the item … has been processed up to …` 使用的影音資料 | steps |
| `media/` | `Given the audio of … is "<name>"` 使用的音訊描述（長度、大小） | 假 yt-dlp、fake ASR |
| `llm/` | fake LLM provider 的回應 | fake LLM |

目錄在第一個 fixture 出現時才建立；沒有用到的目錄不需要預先放空檔案。

## 命名規則

- 檔名使用場景裡出現的名稱，讓人可以從場景直接找到檔案。
  - `Given the source "supadata" returns "supadata-en.json" for "yt:abc123"` 對應 `sources/supadata-en.json`。
  - 字幕軌的 fixture 同理，放在 `captions/`。
- 名稱用小寫英文、數字與 `-`，語言標記放在名稱裡，例如 `groq-no-language-ja.json`。
- 一個 fixture 對應一種情境。不同情境不要共用檔案後再靠欄位覆寫；需要差異就新增檔案。
- fixture 要小：只放場景驗證所需的內容。大量內容改用「產生 N 句」（見下方）。
- 不放真實的 API key、cookie、個人資料或從真實服務複製的受版權保護內容。

### 特殊名稱

- `http-<狀態碼>`（例如 `http-503`）：不是檔案，`fakesource` 直接以該 HTTP 狀態碼回應。

## 字幕軌

假 yt-dlp 的字幕軌（`faketool.Track`）有三種內容來源：

1. 指定 `captions/` 內的檔名：原樣寫出。
2. 指定句數：產生該句數的 VTT。
3. 都沒有指定：**預設產生 120 句**。場景中 `has 120 cues` 的斷言依賴這個預設，不要改動。

產生的 VTT 每句 2 秒，內容為 `Sentence <i> of <影片 ID> (<語言>).`。

字幕軌的種類：`manual`（人工）、`auto`（自動，Bilibili 的 AI 字幕屬於這類）、
`platform-translated`（平台機器翻譯）。

## 假媒體檔

假 yt-dlp 下載媒體時，寫出的檔案內容只有一行標記：

```
NOTESTEME-FAKE-MEDIA item=<平台ID> duration=<秒>
```

- `item` 是平台 ID，例如 `abc123`、`BV1xx411c7mD`；Bilibili 第 2 分P 以後為 `BV1xx411c7mD/p2`；
  本機檔案使用場景中的代號，例如 `local:meetup.mp4`。
- 假 ffprobe 讀取這行標記回報長度；fake ASR 讀取上傳檔案的標記，決定回傳哪個 `sources/` fixture。
- 檔案內容還可以在標記之後接任意填充（用來模擬檔案大小）。
- 標記的產生與解析在 `faketool.MediaMarker`、`faketool.ParseMediaMarker`。

## 新增 fixture 的步驟

1. 先在場景中寫出名稱（使用 `steps.md` 已有的句型）。
2. 在對應的目錄建立同名檔案，內容盡量小。
3. 執行 `go test ./features/...`，確認場景讀得到檔案；找不到檔案時，假工具與假來源會明確回報
   檔名，而不是靜默回傳空內容。

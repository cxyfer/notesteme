# OUT：字幕輸出與匯入

- 狀態：ready
- 里程碑：M1（SRT、VTT、純文字、cues JSON 的即時渲染）、M2（ASS、雙語、輸出目標、匯入外部字幕）
- 相關：plan 第 11 節；ADR-0003、ADR-0010；UI [影音詳情](../ui/pages.md#4-影音詳情)、[字幕樣式](../ui/pages.md#9-字幕樣式)
- 驗收場景：[`features/OUT-subtitle-output.feature`](../../features/OUT-subtitle-output.feature)

## 目的

字幕只有一種真實格式（cues JSON），任何時候都能即時取得需要的格式；需要時自動寫到指定位置，且不覆蓋使用者改過的檔案。在外部工具編輯過的字幕也能匯入，接著往下處理。

## User stories

| ID | Story | 里程碑 |
|---|---|---|
| OUT-01 | As the owner, I want to download subtitles in SRT, VTT or plain text at any time, so that I can use them in any player or tool. | M1 |
| OUT-02 | As the owner, I want bilingual ASS subtitles with a readable default style, so that I can watch with both languages on screen. | M2 |
| OUT-03 | As the owner, I want to customise an ASS style and preview it on a real frame, so that I see the result before I export. | M2 |
| OUT-04 | As the owner, I want subtitles written next to my local video files automatically, so that media servers pick them up without manual copying. | M2 |
| OUT-05 | As the owner, I want files I have edited by hand never to be overwritten, so that my changes are safe when items are reprocessed. | M2 |
| OUT-06 | As the owner, I want to import subtitles I fixed in an external editor, so that translation and notes build on my corrections. | M2 |

## 規則

| ID | 規則 | 里程碑 |
|---|---|---|
| OUT-R1 | 支援的格式：`srt`、`vtt`、`txt`、`cues-json`（M1）；`ass`、`bilingual-srt`（M2）。格式都是取用時從 cues JSON 即時渲染，不存成產出。 | M1 |
| OUT-R2 | 沒有指定變體時，使用處理得最完整的版本：`edited` > `translated` > `corrected` > `segmented` > `normalized`。也可以用 `variant` 與 `lang` 指定。 | M1 |
| OUT-R3 | 指定 `from`、`to` 時，只輸出和這段時間有重疊的 cue，時間維持原值，不重新歸零。 | M1 |
| OUT-R4 | 下載檔名為 `<標題>.<lang>.<ext>`，標題會過濾 NTFS 不允許的字元。 | M1 |
| OUT-R5 | 雙語輸出把原文與譯文放在同一個 cue；原文與目標語言相同時（包括 `convert` 模式下只差字形），一律只輸出單語。 | M2 |
| OUT-R6 | 內建 ASS 樣式為 `default`、`translation-top`、`translation-only`、`large`、`boxed`、`minimal`，不能修改，只能複製成自訂樣式。 | M2 |
| OUT-R7 | ASS 的預設字型依目標語言的文字系統：`zh-Hant` 用 Noto Sans CJK TC、`zh-Hans` 用 Noto Sans CJK SC、拉丁語系用 Noto Sans。 | M2 |
| OUT-R8 | 可以從現有的 `.ass` 檔匯入 `[V4+ Styles]` 成為自訂樣式；檔案中沒有這個段落時回傳 422。 | M2 |
| OUT-R9 | 樣式預覽用 ffmpeg + libass 把樣式渲染在指定影音、指定時間的畫面上，回傳 PNG。 | M2 |
| OUT-R10 | 輸出目標 `sidecar` 只用於本機檔案，寫成 `<影片檔名>.<lang>.<ext>`；影音不是本機檔案時略過，並記錄原因。`folder` 寫到設定的資料夾。 | M2 |
| OUT-R11 | 每個寫出的檔案都記錄在 `outputs`，包含寫入時的 `sha256`。重新寫入時，目標檔案的 hash 和紀錄相同才覆蓋。 | M2 |
| OUT-R12 | 目標檔案被改過時依設定處理：`save_as_new`（預設）另存為 `<名稱> (notesteme YYYY-MM-DD).<ext>`；`skip` 不寫入，`publish` 為 `blocked`（`needs_action`）。 | M2 |
| OUT-R13 | 匯入外部編輯過的字幕（SRT、VTT、ASS），存成新的 `edited` 版本，來源紀錄的 `executor` 為 `human`。匯入不會自動重跑下游步驟。 | M2 |
| OUT-R14 | 匯入後，既有的譯文與筆記標示「字幕已更新，譯文可能過時」，並提供「重新翻譯」動作。 | M2 |
| OUT-R15 | 匯入的檔案無法解析或時間不單調時回傳 422，指出第幾句有問題。 | M2 |

## 介面

- API：`GET /api/items/:id/cues?variant=&lang=&from=&to=`、`GET /api/items/:id/subtitles?format=&variant=&lang=&style=&from=&to=`、`POST /api/items/:id/subtitles/import`（M2）
- ASS 樣式（M2）：`GET/POST/PATCH /api/ass-styles`、`POST /api/ass-styles/preview`、`POST /api/ass-styles/import`
- CLI：`notesteme export <item> --format srt|vtt|txt|ass [--lang] [--variant]`、`notesteme import <subtitle> --item <id>`（M2）
- MCP（M2）：`get_transcript`、`import_transcript`

## 不在範圍內

- vault 輸出目標與筆記（M3）。
- 字幕編輯器（非目標）。

## 待決問題

- `sidecar` 的檔名語言代碼要用 BCP-47（`zh-Hant-TW`）還是 Jellyfin、Plex 較容易辨識的 ISO 639（`zh`、`chi`），M2 實作前用兩套播放器實測。
- OUT-R9 預覽場景：steps.md 4.4 規則 (1) 改成只有寫入 `source` 變體的字幕才算「場景已準備字幕來源」（2026-11-22，owner 同意，已修改）。Background 只 seed 了 `corrected`，預覽影音沒有任何字幕軌，改走規則 (2) 的 fixture（`features/fixtures/items/yt-abc123xxxxx.json`）。
- OUT-R5 同語言場景（`Same-language content is never rendered as bilingual`）期待 `GET …/subtitles?format=bilingual-srt` 的回應內容是 JSON `{ "bilingual": false }`，但 `bilingual-srt` 的回應本體必須是 SRT 檔（OUT-R1、R4）。S7 的做法：單語時照樣輸出單語 SRT，並用回應標頭 `X-Notesteme-Bilingual: false|true` 表示結果（ASS 也有）；這個場景維持 failed。建議把場景最後一步改成 `And the response header "X-Notesteme-Bilingual" contains "false"`（`steps.md` 已有這個步驟），等 owner 同意再改 `.feature`。
- OUT-R8、R13～R15 的 `path` 是伺服器上的檔案路徑。S7 的做法：不套用 GRD-R7 的 inbox 與 `media_roots` 限制，只收字幕副檔名（`.srt`、`.vtt`、`.ass`、`.ssa`）的一般檔案、大小有上限、解析失敗的回應不含檔案內容；理由是這些檔案只會被解析成字幕或樣式，不會被當成媒體處理。代價是持有 `ingest`（字幕）或 `admin`（樣式）scope 的 token 可以讓伺服器讀取任何路徑的字幕副檔名檔案。也可以用 `content` 直接帶檔案內容，CLI 一律這樣送。要不要改成受 GRD-R7 限制，由 owner 決定。場景中的 `/tmp/…` 在請求 body 裡也要對應到場景根目錄（`steps.md` 第 4 節已補）。
- OUT-R9 預覽的 scope：沿用 S0 的 stub，是 `read`。預覽會執行 ffmpeg（CPU 與暫存檔），所以同時最多 2 個、逾時 30 秒，但持有 `read` token 的 client 仍然可以觸發。要不要改成 `ingest`，由 owner 決定。
- OUT-R9 預覽沒有影片可以抽幀時（線上影音、只有音訊的本機檔案、影片檔找不到），改用深灰色的合成畫布（1280x720）疊上字幕；預覽的字幕是最接近指定時間的那句，沒有字幕時用範例文字。
- 內建樣式的語意（OUT-R6）：`default` 是底部、譯文大、原文小的雙語；`translation-top` 把譯文放到畫面上方；`translation-only` 只顯示譯文；`large`、`boxed`（不透明底框）、`minimal` 是尺寸與外框的變化。字型一律留空代表「自動」，依目標語言（譯文行）與原文語言（原文行）帶入（OUT-R7）。匯入 `.ass`（OUT-R8）時，`Default`（沒有就用第一個）樣式當譯文行，下一個樣式當原文行，只有一個樣式時原文行由譯文行縮小產生。
- OUT-R15 的 `line` 是第幾句字幕（從 1 起算，對應「第幾句有問題」），`file_line` 是檔案中的行號（可能沒有）。
- 雙語輸出的原文是「影音的原文語言」以外的另一種語言時不成立：目標語言就是影音的原文語言時一律輸出單語；其他情況原文取非 `translated` 變體中處理得最完整的字幕（OUT-R2 的順序）。
- 匯入的 `edited` 只保證被下載（OUT-R2）優先使用，並把既有的譯文標示過時。correct、translate 要不要以 `edited` 為輸入，以及筆記（M3）的過時標示，規格沒有定義，留給對應的切片。匯入不保留字詞級時間（`words`）。

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
- [O] OUT-R9 預覽場景在 S0 後仍然 failed（2026-11-21，M2 S0 切片逐一檢查場景時發現，需要 owner 決定怎麼改）。Background seed 了 `corrected` 變體，steps.md 4.4 規則 (1) 因此認為「場景已準備字幕來源」，照常跑真的 pipeline；但這部影片沒有任何字幕來源，`transcript` 會 failed。需要 owner 決定：修改場景（例如不用 seed 的 Background，或補上字幕來源），或修改規則 (1)（例如「只有 seed `source` 才算準備來源」，之後這部影片改走規則 (2) 的 fixture）。

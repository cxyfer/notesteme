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

- API：`GET /api/items/:id/cues?variant=&lang=&from=&to=`、`GET /api/items/:id/subtitles?format=&variant=&lang=&style=&from=&to=`、`POST /api/items/:id/subtitles/import`（M2）、`GET /api/items/:id/outputs`（M2，`read`，列出寫出的檔案）
- ASS 樣式（M2）：`GET/POST/PATCH /api/ass-styles`、`POST /api/ass-styles/preview`、`POST /api/ass-styles/import`
- CLI：`notesteme export <item> --format srt|vtt|txt|ass [--lang] [--variant]`、`notesteme import <subtitle> --item <id>`（M2）
- MCP（M2）：`get_transcript`、`import_transcript`

## 不在範圍內

- vault 輸出目標與筆記（M3）。
- 字幕編輯器（非目標）。

## 待決問題

- `sidecar` 的檔名語言代碼要用 BCP-47（`zh-Hant-TW`）還是 Jellyfin、Plex 較容易辨識的 ISO 639（`zh`、`chi`）：S8 保守採 BCP-47（和場景的 `lecture.en.srt` 一致，語言碼取自實際輸出的字幕）。**待實測 Jellyfin、Plex**（需要真的播放器，開發環境做不到）；要改成 ISO 639 只需改 `internal/pipeline/publish.go` 的 `outputName`。
- OUT-R9 預覽場景：steps.md 4.4 規則 (1) 改成只有寫入 `source` 變體的字幕才算「場景已準備字幕來源」（2026-11-22，owner 同意，已修改）。Background 只 seed 了 `corrected`，預覽影音沒有任何字幕軌，改走規則 (2) 的 fixture（`features/fixtures/items/yt-abc123xxxxx.json`）。
- OUT-R5 同語言場景（`Same-language content is never rendered as bilingual`）原本期待 `GET …/subtitles?format=bilingual-srt` 的回應內容是 JSON `{ "bilingual": false }`，但 `bilingual-srt` 的回應本體必須是 SRT 檔（OUT-R1、R4）。決定（2026-11-22，依 /goal 採 advisor 建議，待 owner 追認；已修改 `.feature`）：單語時照樣輸出單語 SRT，並用回應標頭 `X-Notesteme-Bilingual: false|true` 表示結果（ASS 也有）；場景最後一步改成 `And the response header "X-Notesteme-Bilingual" contains "false"`。`steps.md` 沒有「回應本文不含」的句型，之後要直接斷言本文時再加。
- OUT-R8、R13～R15 的 `path` 是伺服器上的檔案路徑。決定（2026-11-22，依 /goal 採 advisor 建議，待 owner 追認）：不套用 GRD-R7 的 inbox 與 `media_roots` 限制，因為 GRD-R7 管的是「新增影音」，而場景本身就用 `/tmp/…`；只收字幕副檔名（`.srt`、`.vtt`、`.ass`、`.ssa`）的一般檔案、副檔名在解析 symlink 之後才檢查（`x.srt -> /etc/shadow` 會被拒絕）、大小有上限、解析失敗的回應不含檔案內容。代價是持有 `ingest`（字幕）或 `admin`（樣式）scope 的 token 可以讓伺服器讀取任何路徑、字幕副檔名的檔案。也可以用 `content` 直接帶檔案內容，CLI 一律這樣送。場景中的 `/tmp/…` 在請求 body 裡也要對應到場景根目錄（`steps.md` 第 4 節已補）。
- OUT-R9 預覽的 scope：決定（2026-11-22，依 /goal 採 advisor 建議，待 owner 追認）改成 `ingest`，因為預覽會執行 ffmpeg（CPU 與暫存檔）；同時最多 2 個、逾時 30 秒。
- OUT-R9 預覽沒有影片可以抽幀時（線上影音、只有音訊的本機檔案、影片檔找不到），改用深灰色的合成畫布（1280x720）疊上字幕；預覽的字幕是最接近指定時間的那句，沒有字幕時用範例文字。
- 內建樣式的語意（OUT-R6）：`default` 是底部、譯文大、原文小的雙語；`translation-top` 把譯文放到畫面上方；`translation-only` 只顯示譯文；`large`、`boxed`（不透明底框）、`minimal` 是尺寸與外框的變化。字型一律留空代表「自動」，依目標語言（譯文行）與原文語言（原文行）帶入（OUT-R7）。匯入 `.ass`（OUT-R8）時，`Default`（沒有就用第一個）樣式當譯文行，下一個樣式當原文行，只有一個樣式時原文行由譯文行縮小產生。
- OUT-R15 的 `line` 是第幾句字幕（從 1 起算，對應「第幾句有問題」），`file_line` 是檔案中的行號（可能沒有）。
- 雙語輸出的原文是「影音的原文語言」以外的另一種語言時不成立：目標語言就是影音的原文語言時一律輸出單語；其他情況原文取非 `translated` 變體中處理得最完整的字幕（OUT-R2 的順序）。
- 匯入的 `edited` 只保證被下載（OUT-R2）優先使用，並把既有的譯文標示過時。correct、translate 要不要以 `edited` 為輸入，以及筆記（M3）的過時標示，規格沒有定義，留給對應的切片。匯入不保留字詞級時間（`words`）。
- S8 的輸出目標（2026-11-22，依 /goal 採保守預設，待 owner 追認）：
  - **沒有紀錄的既有檔案視為使用者的檔案**。OUT-R11 說「hash 和紀錄相同才覆蓋」，沒有紀錄就不可能相同，所以只有目標檔案不存在時才直接寫入；檔案存在而沒有紀錄（從未寫過、紀錄遺失）一律走 R12。「A file edited by hand」（先 `processed up to "publish"` 再手改）與 skip 場景都需要這個語意。不是一般檔案（symlink、目錄）也視為使用者的，不跟隨 symlink。目標的內容已經和要寫的相同時不寫入，並補上紀錄。
  - 寫入：先寫同資料夾的暫存檔再 rename；新檔用硬連結建立（目標已存在就失敗，競態中別人先建立的檔案不會被覆蓋），檔案系統不支援硬連結時退回 rename；權限 0644；寫入限制在目標資料夾內（`os.Root`）。每個檔案寫完立刻記錄（`outputs` 一個 `(item, path)` 一列，重寫時更新雜湊）；寫完後、記錄前當機時，下次沒有紀錄會被視為使用者的檔案，是安全的方向。
  - `save_as_new`：日期用注入的時鐘，**UTC**；同一天再次另存時，另存的檔案沒被改過（雜湊與紀錄相同）就覆蓋同一個，否則換成 `<名稱> (notesteme YYYY-MM-DD 2).<ext>`、`… 3)…`。`skip`：這個檔案不寫入，同一次發布的其他檔案照常寫出，`publish` 為 `blocked`（`needs_action`），訊息列出被改過的檔案，動作「處理該檔案後重新發布」；處理完後從 `publish` 重跑。
  - `sidecar` 只用於本機檔案；上傳的檔案是資料目錄裡的副本，也不適用。沒有任何輸出目標適用時 `publish` 為 `skipped`（原因 `not_applicable`）；混合目標時只略過不適用的那個。影片檔不見了（不知道要放哪）是 `permanent` 失敗。字幕放在影片代號指的路徑旁邊，不跟隨影片檔本身的 symlink。
  - `folder`：`path` 必須是絕對路徑，寫入前解析 symlink 與 `..`，並套用和 GRD-R7 相同的限制（serve 模式只能在 inbox 與 `media_roots` 之內，standalone 不受限制）；不存在時建立。檔名同下載（OUT-R4，`<標題>.<lang>.<ext>`）。限制是因為持有 `ingest` scope 的 token 可以用單次覆寫帶 `outputs`，不限制就等於任意寫檔；**待 owner 追認**（也可以改成另外的 `output_roots` 設定）。
  - `bilingual-srt` 的副檔名也是 `srt`，寫出時檔名加 `.bilingual`（`lecture.en.bilingual.srt`），不會和單語的 `srt` 互相覆蓋；`ass` 的樣式用 `style`（預設 `default`）。
  - `lang` 沒寫時用字幕的預設選擇（OUT-R2）；指定的語言沒有字幕時其他輸出照常寫出，步驟 `done` 並以訊息說明缺哪些語言（不是失敗，因為 `translate` 可能被設成略過）。
  - `outputs` 的每一項在處理方案與覆寫中驗證（`target`、`formats`、`lang`、`style`、`path`、`on_user_change`，錯誤回 `errors[{path,message}]`）；`target: vault` 在 M3 提供，目前回 422（執行時遇到也是 `permanent`「尚未支援」）。
  - `outputs` 資料表和 plan 第 21 節的草稿不同：沒有 `id`、`style_id`、`artifact_ids_json`，多了 `lang`、`size`，以 `(item_id, path)` 為主鍵；之後 vault 需要時再擴充。影音永久刪除時紀錄隨之刪除（寫出的檔案本身不動，DEL-R9）。
- **`.feature` 的缺陷（待 owner 決定，S8 沒有修改它）**：OUT-R10～R12 的 4 個場景中，「skip」場景在 S8 通過；其餘 3 個照字面執行時沒有適用的輸出目標，仍然是紅燈：
  - 「Subtitles are written next to a local video」只定義了 profile `media-server`，沒有把它設成預設，之後的 `reruns … from "publish"` 沒有帶 profile，套用的 `outputs` 是空的，`publish` 因此是 `skipped`（`no_outputs`），檔案不會產生。建議在 profile 之後加 `And the default profile is "media-server"`（和 PRF 的 F1、F2 同類）。
  - 「A sidecar target is skipped for online videos」對 Background 已經 seed 過的 `yt:abc123xxxxx` 做 `ingests`：影音已存在（ING-R2），不會重新處理，`publish` 維持 `pending`。建議改成和 skip 場景一樣的重跑：`When the owner sends "POST /api/items/yt:abc123xxxxx/rerun" with:` `{ "from": "publish", "profile": "media-server" }`。
  - 「A file edited by hand is saved alongside…」完全沒有定義帶 `outputs` 的 profile，沒有第一次寫出，就沒有「改過」的檔案。建議在 Background 之後、`processed up to "publish"` 之前加上 `And the profile "media-server" is:`（內容同上）與 `And the default profile is "media-server"`。
  - 上面三處修改後，4 個場景在 S8 的實作下都通過（用複本在 `-godog.paths` 驗證過，沒有動到 `.feature`）。
- 輸出的 `lecture.en.srt` 正好符合 SRC-R14 的 `local-sidecar` 命名：之後從 `transcript` 重跑時，會把自己寫出的字幕當成外掛字幕來源讀回。S8 沒有處理（要在 `listSidecars` 排除「`outputs` 有紀錄、雜湊相同」的檔案，會動到 LOCAL 的程式）；**待 owner 決定**要不要排除。
- vault 輸出目標、hook（ADR-0010 第 4 點）屬於 M3，S8 沒有實作；sidecar 的 hook 同樣沒有。

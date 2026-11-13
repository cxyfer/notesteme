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

- 上傳檔案的大小上限，暫定 4GB，可在設定檔調整。
- inbox 的穩定判定時間暫定 10 秒，是否需要依檔案大小調整，M1 實作時觀察。
- 播放清單（ING-R4）展開的項目數目前沒有上限，整份清單在一個交易中建立。是否需要上限、超過時要截斷還是拒絕，等 ING 切片決定（2026-11-13，GRD 切片實作 ING-R4 時發現）。
- 播放清單展開時 yt-dlp 的錯誤訊息（例如 `The playlist does not exist.`）是依 yt-dlp 原始碼判斷的，還沒有對真實 yt-dlp 驗證；ING 切片做 `@manual` 煙霧測試時確認。

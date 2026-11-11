# LIB：媒體庫、影音詳情與佇列頁

- 狀態：draft
- 里程碑：M1（列表、基本篩選、標題搜尋、字幕分頁、佇列頁）、M2（對照、差異、重新處理選單）
- 相關：plan 第 14 節；UI [媒體庫](../ui/pages.md#3-媒體庫)、[影音詳情](../ui/pages.md#4-影音詳情)、[佇列](../ui/pages.md#6-佇列)、[設計語言](../ui/design-language.md)
- 驗收場景：[`features/LIB-library.feature`](../../features/LIB-library.feature)

## 目的

在 Web UI 中瀏覽、篩選所有影音，查看字幕與處理狀態，並掌握佇列。資料面的行為透過 API 驗證；視覺與互動以 UI 規格為準，用 `@ui` 場景驗收。

## User stories

| ID | Story | 里程碑 |
|---|---|---|
| LIB-01 | As the owner, I want to filter my library by channel, platform, stage status and error class, so that I can find what needs attention. | M1 |
| LIB-02 | As the owner, I want to search my library by title, so that I can find an item quickly. | M1 |
| LIB-03 | As the owner, I want to read an item's subtitles next to its player and jump to any line, so that I can check the result against the video. | M1 |
| LIB-04 | As the owner, I want to see each resource lane of the queue with its limit and running tasks, so that I understand why something has not started yet. | M1 |
| LIB-05 | As the owner, I want the library and item pages to update live while processing, so that I do not have to reload. | M1 |
| LIB-06 | As the owner, I want to compare the original, corrected and translated subtitles and see the differences, so that I can judge what the LLM changed. | M2 |
| LIB-07 | As the owner, I want to rerun an item from any stage or with another prompt or model, so that I can improve a result without starting over. | M2 |

## 規則

| ID | 規則 | 里程碑 |
|---|---|---|
| LIB-R1 | `GET /api/items` 支援的篩選：`channel`、`platform`、`status`（`<stage>:<status>`，可多個）、`error_class`、`published_from`、`published_to`、`duration_min`、`duration_max`、`lang`、`read_state`、`timing`（例如只看含 `interpolated` 的影音）。多個條件之間為 AND。 | M1 |
| LIB-R2 | 篩選條件反映在 UI 的 URL 參數中，重新整理或分享網址後得到相同的結果。 | M1 |
| LIB-R3 | 標題搜尋不分大小寫，中文與英文都可以用部分字串搜尋（包括 2 個字以下）。 | M1 |
| LIB-R4 | 列表預設依頻道分組，可以改成依發布日或狀態分組；分頁使用游標，單頁最多 200 筆。 | M1 |
| LIB-R5 | 影音詳情的字幕分頁可以切換「原始」與「正規化」（M1），以及「校正」「差異」「譯文」「對照」（M2）；切換模式不改變捲動位置。 | M1 |
| LIB-R6 | 點擊字幕句會讓播放器跳到該句的 start。YouTube 用 IFrame Player API，本機檔案與 Podcast 用 HTML5 media，後端支援 Range 請求。 | M1 |
| LIB-R7 | 時間來源不是 `source` 的 cue 有可辨識的標示，並可以篩選。 | M1 |
| LIB-R8 | 佇列頁依資源類別分通道顯示：並行上限、使用中、等待中；上限可以直接調整。 | M1 |
| LIB-R9 | 收到 SSE 事件時只更新受影響的列；新增的影音只顯示「有 N 個新項目」，不打亂目前的列表。 | M1 |
| LIB-R10 | 空狀態與篩選結果為空時的文案見 UI README §4.3。 | M1 |
| LIB-R11 | 「重新處理」可以選擇起始步驟，並可換 prompt 或模型；會產生新版本，舊版本保留並可比較。 | M2 |
| LIB-R12 | 「差異」模式逐字標示校正前後的差異；`localize` 模式額外提供「譯文」與「對照」。 | M2 |

## 介面

- API：`GET /api/items`、`GET /api/items/:id`、`GET /api/items/:id/cues`、`GET /api/items/:id/media`、`GET /api/queue`、`GET /api/events`
- UI：見上方連結

## 不在範圍內

- 字幕全文搜尋、儲存檢視、批次操作（M4，故事地圖 SRCH、LIB-08）。
- 畫面與筆記分頁（M3）。

## 待決問題

- Bilibili 嵌入播放器能否控制播放位置（M0）。不行時，點擊字幕句改成「在 Bilibili 開啟這個時間」。

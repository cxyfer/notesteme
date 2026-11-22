# CHAP：自動章節

- 狀態：ready
- 里程碑：M3
- 相關：plan 第 5、12 節；ADR-0002、ADR-0003、ADR-0019、[ADR-0020](../adr/0020-m3-stages-notes-and-vault.md)；UI [影音詳情](../ui/pages.md#4-影音詳情)（章節清單）
- 驗收場景：[`features/CHAP-chapters.feature`](../../features/CHAP-chapters.feature)

## 目的

每部影音都有章節：平台有就直接用，沒有就由 LLM 依字幕提出。LLM 只回傳起點的 cue 編號與標題，時間一律由程式依 cue 換算（ADR-0003）。章節是筆記結構（NOTE）與畫面歸屬（FRM）的基礎，產生失敗時不阻擋後續步驟。

## User stories

| ID | Story |
|---|---|
| CHAP-01 | As the owner, I want chapters for every video, so that long videos are easy to navigate. |

## 規則

| ID | 規則 |
|---|---|
| CHAP-R1 | `metadata` 取得的平台章節有 2 個以上時直接採用：寫入 `origin: platform` 的 `chapters` 產出，`chapters` 步驟為 `skipped`（`platform_chapters`），不呼叫 LLM。平台只有 1 個章節或沒有章節時，依 `stages.chapters` 的設定產生。 |
| CHAP-R2 | LLM 的輸入是原文語言中處理得最完整的字幕（`edited` > `corrected` > `segmented` > `normalized`，不用譯文），每句只給 `i` 與 `text`（LLM-R8、ADR-0019）。字幕超過 chunk 上限時依 LLM-R1 切 chunk，每個 chunk 只能提出起點落在自己範圍內的章節，結果依起點合併。 |
| CHAP-R3 | 輸出是 `{chapters: [{cue, title}]}`。驗證：`cue` 必須是這次輸入中存在的句號、嚴格遞增；章節數 1～`stages.chapters.max_chapters`（預設 20）；`title` 去掉頭尾空白後 1～80 字，不含換行與控制字元。不符合時依 LLM-R5 帶著錯誤說明重試（例如「chapters[1].cue 99 不在輸入中」）。 |
| CHAP-R4 | 重試用盡時改為沒有章節：步驟為 `done`，存成空的 `chapters` 產出（`origin: llm`），影音顯示「無法產生章節：模型回傳的內容沒有通過驗證（chapters[1].cue 99 不在輸入中）。筆記不會分章節。」，動作為「重新產生章節」與「開啟 Prompt」。後續步驟照常執行。 |
| CHAP-R5 | 章節的時間由程式換算：第一個章節一律從 `00:00:00.000` 開始，其餘章節的起點是該 cue 的 `start`；每個章節到下一個章節的起點（最後一個到片長）結束。 |
| CHAP-R6 | SponsorBlock 片段內的 cue（SPB-R7）不放進輸入，所以章節起點不會落在片段內。 |
| CHAP-R7 | 章節存成產出（`kind: chapters`，ADR-0020 第 4 點）。`GET /api/items/:id` 與 MCP `get_item` 回傳 `chapters: [{title, start, end, origin}]`。來源紀錄另外記錄 `origin` 與 `parents`（LLM 時是輸入字幕的變體與 `sponsor_segments`）。 |
| CHAP-R8 | `stages.chapters.executor` 可以是 `api`、`agent`、`off`（PRF-R6）；`off` 時為 `skipped`（`off`），影音沒有章節。`agent` 時任務的 `kind` 是 `chapters`，和其他 LLM 步驟相同（AGT）。 |

## 介面

- 處理方案：`stages.chapters.{executor, provider, model, prompt, max_chapters}`；內建 prompt `chapters-default`（PRM）
- API：`GET /api/items/:id` 的 `chapters`；`POST /api/items/:id/rerun` 的 `from: chapters`
- MCP：`get_item` 的 `chapters`；Agent 任務的 `kind: chapters`
- UI：影音詳情的章節清單與時間帶上的章節分段（[pages.md 第 4 節](../ui/pages.md#4-影音詳情)，屬於 UI 切片）
- 資源類別：`llm`

## 不在範圍內

- 手動編輯章節（之後可以用匯入或 API 補，不在 M3）。
- 以產生的章節重新切 M2 步驟的 chunk（ADR-0020 第 1 點）。
- 摘要（`summary`，M4 與 DIG 一起決定）。

## 待決問題

- H1：步驟順序（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：`chapters` 接在 `translate`、`sponsorblock` 之後（ADR-0020），輸入是校正後的字幕，所以章節的 cue 編號和筆記引用的 cue 相同；plan 第 5 節原本寫「正規化字幕、`segment` 之前」。
- H2：平台章節的門檻是 2 個（CHAP-R1）。YouTube 自己要求至少 3 個章節才顯示；但 yt-dlp 也會從說明欄解析出只有 1 個的「章節」，那不能代表結構，所以 1 個視為沒有。
- H3：重試用盡時不讓步驟失敗，而是沒有章節（CHAP-R4）：章節是輔助資料，筆記沒有章節仍然可以寫；讓步驟 `failed` 會擋住 `frames`、`note`、`publish`。
- H4：`max_chapters` 預設 20、標題上限 80 字是保守的初值，沒有 M0 數據；影片很長（例如 3 小時）時可能不夠，使用者可以調高（上限 100）。
- H5：章節標題是不可信的 LLM 輸出。這裡只做長度與控制字元的檢查；放進筆記與 UI 時的跳脫由 NOTE-R8 與 UI 負責。

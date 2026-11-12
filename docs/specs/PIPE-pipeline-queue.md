# PIPE：Pipeline 與任務佇列

- 狀態：draft
- 里程碑：M1（chunk 任務與 Agent executor 在 M2，見 LLM、AGT）
- 相關：plan 第 5、9 節；ADR-0002、ADR-0004；UI [佇列](../ui/pages.md#6-佇列)
- 驗收場景：[`features/PIPE-pipeline-queue.feature`](../../features/PIPE-pipeline-queue.feature)

## 目的

讓每部影音的處理可以排程、重試、續跑，而且不重複付費；使用者隨時看得到每個步驟的狀態，也能控制佇列。

## User stories

| ID | Story |
|---|---|
| PIPE-01 | As the owner, I want each stage of an item to show a clear status, so that I always know what is happening and what is waiting for me. |
| PIPE-02 | As the owner, I want temporary failures retried automatically and permanent ones explained, so that I only step in when it is actually needed. |
| PIPE-03 | As the owner, I want work that has already been done to be reused, so that reprocessing never charges me twice for the same result. |
| PIPE-04 | As the owner, I want to limit how much runs at once and pause the queue, so that Notesteme does not overload my machine or my accounts. |
| PIPE-05 | As the owner, I want items I add by hand to run before background work, so that what I am waiting for comes first. |
| PIPE-06 | As the owner, I want processing to resume after a crash or restart, so that I never have to start an item over. |
| PIPE-07 | As the owner, I want to rerun an item from any stage and keep the older results, so that I can compare and go back. |

## 規則

| ID | 規則 |
|---|---|
| PIPE-R1 | 步驟依 DAG 執行，上游完成後才建立下游任務。`until` 指定的步驟完成後就停止，之後的步驟為 `pending` 但不建立任務。 |
| PIPE-R2 | 步驟狀態與錯誤類別的對應見 plan 第 5.2、5.3 節：`retryable` 重試中為 `pending`、用盡後為 `failed`；`wait` 為 `waiting`；`needs_action`、`budget_blocked` 為 `blocked`；`permanent` 為 `failed`。 |
| PIPE-R3 | `retryable` 錯誤以指數退避重試（30 秒、2 分鐘），最多 3 次嘗試。 |
| PIPE-R4 | `blocked` 的步驟在使用者執行修正動作（例如上傳 cookie）後自動回到 `pending`；也可以手動重試。 |
| PIPE-R5 | 快取鍵相同的步驟直接重用既有產出，不呼叫外部服務，也不建立新版本。快取鍵見 plan 第 5.1 節。 |
| PIPE-R6 | 步驟的 `impl_version` 改變後，舊的快取不再命中。 |
| PIPE-R7 | 重跑產生新版本時，舊版本標記 `superseded_by`。同一組 `(item, kind, variant, lang)` 保留最近 3 版；被輸出目標或 bundle 引用的版本（`pinned`）不會被清除。 |
| PIPE-R8 | 每個資源類別（`download`、`asr`、`llm`、`frames`）有並行上限，可以即時調整，調整後的下一次領取就生效。 |
| PIPE-R9 | 領取順序：手動新增 > 訂閱的新內容 > 回補；同一級中，等待越久的任務優先度越高（aging），預設每 10 分鐘提高 1 級。 |
| PIPE-R10 | 暫停整個佇列或某個資源類別時，執行中的任務會做完，但不會領取新任務。 |
| PIPE-R11 | 重新啟動時，上一次執行的 worker 持有的租約放回 `pending`；`running` 的步驟重新推導狀態；`/data/tmp` 清空。這次中斷不計入重試次數。 |
| PIPE-R12 | 正常關閉時先停止領取，最多等 30 秒讓執行中的任務完成，之後主動釋放其餘租約。 |
| PIPE-R13 | 每筆 stage run 都記錄生效設定的快照（`config_snapshot_id`），API 可以查到當時的設定。 |
| PIPE-R14 | 步驟狀態改變時，透過 SSE（`GET /api/events`）推送事件。 |
| PIPE-R15 | 可以取消某部影音的處理；取消後，執行中的子程序整組結束，步驟為 `skipped`（原因 `cancelled`）。 |

## 介面

- API：`GET /api/queue`、`PATCH /api/queue/limits`、`POST /api/queue/pause`、`POST /api/queue/resume`、`POST /api/tasks/:id/retry`、`POST /api/tasks/:id/cancel`、`POST /api/items/:id/cancel`、`POST /api/items/:id/rerun`、`GET /api/items/:id/runs`、`GET /api/events`（SSE）
  - `PATCH /api/queue/limits`：body 是 `{ "<資源類別>": <上限> }`，只改有給的類別；上限至少 1，不認識的類別回 422。
  - `POST /api/queue/pause`、`POST /api/queue/resume`：沒有 body 時作用在整個佇列；`{ "class": "asr" }` 只作用在該資源類別。
  - `POST /api/items/:id/cancel`：取消該影音所有未結束的步驟（PIPE-R15）；`POST /api/tasks/:id/cancel` 只取消單一任務。
- CLI：`notesteme items ls --status <stage>=<status>`、`notesteme items rerun <item> --from <stage>`

## 不在範圍內

- LLM 的 chunk 任務與驗證重試（M2，見 LLM）。
- `agent` executor 與租約延長（M2，見 AGT）。
- 預算（M4）。

## 待決問題

- 退避的時間與次數是否依資源類別區分（例如 `download` 遇到 429 時等更久），M1 實作時觀察實際的 rate limit。
- aging 的級距：手動與回補只差 2 級，依 PIPE-R9 每 10 分鐘提高 1 級，回補等 20 分鐘就會排在新的手動新增前面。M1 先照規則實作；之後依實際使用調整級距或 aging 速度，但差距不能超過 17 級，否則「等 3 小時的回補排在前面」的場景不成立（2026-11-13，PIPE 切片設計時發現）。
- `transcript` 的資源類別要在建立任務時決定，但這時還不知道平台字幕是否可用。暫定：`fallback` 的第一個來源是 ASR，且平台字幕不符合 `caption_priority` 時歸為 `asr`，否則歸為 `download`。長期可以把下載音訊與 ASR 拆成兩個任務。M1 目前一律歸為 `download`，等 ASR 來源實作時一起處理（「Pausing one resource class」場景因 groq 尚未實作而 pending）。
- 執行期回收過期租約延到 AGT：同一份 `/data` 只有一個 `serve`，內建任務也不延長租約，執行期回收會讓超過租約時間的 ASR 任務被重複執行。M1 只在啟動時回收（PIPE-R11）。
- **場景驗證不到規則，需要修改場景（待 owner 同意）**（2026-11-13，PIPE 切片審查以變異測試發現）：
  - PIPE-R7「Only the three most recent versions are kept」：第三次 rerun（`mode: auto`）與初次處理的快取鍵相同，不會產生新版本，總共剛好 3 版；拿掉清除邏輯場景仍然通過。建議：初次處理改用 `mode: native` 以外的另一組設定，或多一次 rerun，讓同一組產出累積到 4 個不同版本，再斷言只剩 3 版。清除邏輯先以單元測試驗證。
  - PIPE-R13「Each run records the settings it used」：陣列只比對前 N 個元素，預設的 `fallback: [supadata, groq]` 也符合 `["supadata"]`，分不出覆寫與預設值。建議：斷言改成覆寫獨有的值，例如 `"targets": ["en"]`（預設不是 `en`）。

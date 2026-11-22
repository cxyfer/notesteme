# DIG：摘要與彙整

- 狀態：ready
- 里程碑：M4
- 相關：plan 第 5、13、15 節；ADR-0005、ADR-0019、[ADR-0021](../adr/0021-m4-scheduler-subscriptions-and-outbox.md)（第 1、3、5 點）；[NOTIFY](NOTIFY-notifications.md)、[LLM](LLM-execution.md)、[PRF](PRF-profiles.md)；UI [影音詳情](../ui/pages.md#4-影音詳情)、[訂閱](../ui/pages.md#5-訂閱)、[設定](../ui/pages.md#12-設定)（通知）
- 驗收場景：[`features/DIG-digest.feature`](../../features/DIG-digest.feature)

## 目的

訂閱會帶進很多影音，但不是每一部都值得花 `segment`、`correct`、`translate` 的錢。`summary` 步驟先用一次 LLM 呼叫產生摘要與標題譯文；「摘要後暫停」讓影音停在摘要，等 owner 在 UI 或彙整中挑選要繼續的影音。每日或每週的彙整把這段期間的新摘要整理成一則通知，經 NOTIFY 的管道送出。

## User stories

| ID | Story |
|---|---|
| DIG-01 | As the owner, I want new subscription items summarised and sent to me as a daily or weekly digest, so that I can decide what is worth a full run. |
| DIG-02 | As the owner, I want a profile that pauses after the summary, so that only the items I pick go on to cost more. |

## 規則

| ID | 規則 |
|---|---|
| DIG-R1 | `summary` 加進 `pipeline.Stages`，位置在 `normalize` 之後、`segment` 之前（ADR-0021 第 3 點）。`stages.summary.executor` 可以是 `api`、`agent`、`off`，預設 `off`（略過的原因 `off`）；內建的處理方案都不開啟。`agent` 時任務的 `kind` 是 `summary`（AGT）。`executor: off` 時設定 `pause_after: true` 回 422，`errors[].path` 為 `stages.summary.pause_after`，訊息「摘要關閉時不能設定摘要後暫停。」。資源類別 `llm`。 |
| DIG-R2 | 輸入：`normalized` 字幕，每句只給 `i` 與 `text`（ADR-0019），加上標題、頻道名稱與說明的前 2000 字，全部放在資料區塊中（LLM-R9）。一部影音只送**一個**請求，不切 chunk。片長超過 `stages.summary.max_duration`（預設 `2h`，可設 `10m`～`6h`）或估算的輸入超過 `stages.summary.max_input_tokens`（預設 32000，可設 1000～200000）時不呼叫 LLM：步驟為 `skipped`（`too_long`），訊息「影片長度 2:30:00 超過摘要的上限 2:00:00，略過摘要。」（超過 token 上限時為「字幕太長（約 41000 tokens），超過摘要的上限 32000 tokens，略過摘要。」）。 |
| DIG-R3 | 輸出（schema `notesteme_summary`）是 `{summary, key_points, title_translation}`。驗證：`summary` 去掉頭尾空白後 1～1200 字，除了換行之外不含控制字元；`key_points` 0～7 項，每項 1～200 字、單行；`title_translation` 是 `null` 或 1～200 字、單行。不符合時依 LLM-R5 帶著錯誤說明重試（例如「key_points 有 8 項，上限是 7 項」）。摘要語言是 `stages.summary.language`，沒有設定時同 NOTE-R5 的筆記語言規則（`language.targets` 的第一個，沒有目標語言時用原文語言），以 `{{summary_lang}}` 放進系統指示。影音的原文語言與摘要語言相同時，`title_translation` 一律存成 `null`（模型回傳了也丟掉）。 |
| DIG-R4 | 重試用盡時不讓步驟失敗：步驟為 `done`，不寫入 `summary` 產出，影音顯示「無法產生摘要：模型回傳的內容沒有通過驗證（key_points 有 8 項，上限是 7 項）。」，動作為「重新產生摘要」與「開啟 Prompt」。後續步驟照常執行（`pause_after: true` 時照 DIG-R6 暫停）。彙整中這部影音顯示「（沒有摘要）」。 |
| DIG-R5 | 摘要存成產出（`kind: summary`）：`{text, key_points, title_translation, language}`。`GET /api/items/:id` 與 MCP `get_item` 回傳 `summary`（同上的欄位）；沒有摘要產出時沒有這個欄位。來源紀錄是 LLM-R10 的欄位，加上 `parents: normalized`、`summary.language`。從 `summary` 或更早的步驟重跑才重新產生；結果與現行產出完全相同時不建立新版本。 |
| DIG-R6 | 摘要後暫停（`stages.summary.pause_after: true`，預設 `false`）：`summary` 結束（`done`，或 `skipped` 的 `too_long`）後，下一個步驟（`segment`；這次執行不包含 `segment` 時是再下一個）為 `blocked`，原因 `awaiting_user`，訊息「摘要完成，等你決定要不要繼續處理。」，動作為「繼續處理」與「取消」。暫停中的影音不建立任務、不預留預算、不會因為重啟或時間經過而自己繼續，也不算 OPS-R14 的「沒有進展」。從 `segment` 或之後的步驟重跑不會暫停（owner 已經決定）；從 `summary` 或更早的步驟重跑會再次暫停。 |
| DIG-R7 | 繼續：`POST /api/items/:id/continue`（scope `ingest`）把 `awaiting_user` 的步驟改回 `pending`，以影音原本的優先級建立任務，回 202 `{continued: "<步驟>"}`；之後的費用照 COST 的預算規則預留與檢查。影音沒有 `awaiting_user` 的步驟時回 409 `not_awaiting_user`，訊息「這部影音沒有在等待你決定是否繼續。」。批次：`POST /api/items/continue {ids}`，`ids` 1～100 個，超過時 422（`errors[].path` 為 `ids`）；回 200 `{continued: [id], skipped: [{id, code}]}`（`id` 原樣回傳請求中 `ids` 的寫法，例如 `yt:…` 代號或 UUID）（`code` 是 `not_found`、`in_trash` 或 `not_awaiting_user`），個別失敗不影響其他影音。沒有被繼續的影音一直停在 `awaiting_user`，不再產生任何 LLM 請求；`POST /api/items/:id/cancel` 把它改為取消（PIPE-R15）。 |
| DIG-R8 | 彙整設定（`/api/digests`，scope `admin`）：`{id, period, at, weekday, timezone, notifiers, scope, enabled}`。`id` 符合 `^[a-z0-9][a-z0-9-]{0,39}$`；`period` 是 `daily` 或 `weekly`；`at` 是 `HH:MM`（預設 `08:00`）；`weekday` 只在 `weekly` 時需要（`mon`～`sun`）；`timezone` 是 IANA 時區名稱（預設 `UTC`，不認得時 422，訊息「不認得的時區：<值>。」）；`notifiers` 是 1～5 個已存在的 notifier id，而且那個 notifier 的 `events` 必須包含 `digest`（否則 422，訊息「notifier <id> 沒有勾選「訂閱彙整」。」）；`scope` 是 `subscriptions`（預設，只有訂閱建立的影音，`items.subscription_id` 不是 null）或 `all`。每個彙整是一個排程工作（`scheduled_jobs.kind = digest`，`key` 是 id），在該時區的 `at`（每週為 `weekday` 的 `at`）執行，不加隨機延遲；當地時間不存在（夏令時間跳過）時在之後第一個有效的時間執行，重複的時間取第一次。 |
| DIG-R9 | 內容的範圍：`summary` 步驟在 `(上次排定的執行時間, 這次排定的執行時間]` 之間結束（`done` 或 `skipped`）的影音；第一次執行時從「這次排定的執行時間減去一個週期」起算。以排定的時間（`next_run_at`）而不是實際執行的時間計算，停機後補跑一次時範圍仍然連續。每部影音在同一個彙整中只出現一次（`digest_entries`），垃圾桶中的影音不列入。一則彙整最多列 50 部，其餘以「還有 N 部，請到 Notesteme 查看。」帶過。沒有任何影音時**不送出**、不寫入 outbox，只記錄這次執行時間。下一次執行排在實際執行時間之後的下一個 `at`（對齊當地時間，不是「執行時間＋間隔」）；停機錯過好幾個週期時只補跑一次，範圍涵蓋整段錯過的期間。 |
| DIG-R10 | 內容格式：標題「Notesteme 每日彙整：2026-11-24（3 部）」（每週為「Notesteme 每週彙整：2026-11-17～2026-11-23（3 部）」，日期是彙整時區的當地日期）。每部影音一段：清理後的標題（NOTIFY-R9）、有的話接「（<標題譯文>）」、頻道、片長 `H:MM:SS`、摘要（超過 280 字截斷並加「…」）、暫停中的影音加上「等你決定」。設定檔有 `server.public_url` 時，每部影音附上 `<public_url>/items/<影音 id>`，暫停中的影音另外附上 `<public_url>/library?blocked=awaiting_user`；連結只指向 UI，開啟時要登入（AUTH），**不含** token、session 或一次性代碼。沒有 `server.public_url` 時不附連結，結尾加「設定 server.public_url 後，彙整會附上連結。」。webhook 收到結構化的 JSON（NOTIFY-R4）：`digest: {id, period, start, end}` 與 `items: [{id, ref, title, title_translation, channel, duration_s, summary, awaiting_user, url}]`（`summary` 不截斷，最多 50 部），其他管道收到文字，長度依 NOTIFY-R9 截斷。 |
| DIG-R11 | 預覽：`POST /api/digests/:id/preview`（scope `admin`）回 200 `{items: [{id, title, title_translation, summary, awaiting_user}], text}`，內容與「現在執行」相同，但不送出、不寫入 `digest_entries`、不改變下次執行時間。 |

## 介面

- 處理方案：`stages.summary.{executor, provider, model, prompt, language, pause_after, max_duration, max_input_tokens}`；內建 prompt `summary-default`（PRM，類型 `summary`）。
- 設定檔：`server.public_url`（新鍵，只能寫在設定檔：`https://` 或 `http://` 開頭、不含 path 以外的 query 與 fragment；NOTIFY 的通知也使用）。
- API：
  - `GET /api/items/:id` 的 `summary`；`POST /api/items/:id/rerun` 的 `from: summary`。
  - `POST /api/items/:id/continue`、`POST /api/items/continue`（scope `ingest`）。
  - `GET /api/digests`（`admin`）、`POST /api/digests`、`PATCH /api/digests/:id`、`DELETE /api/digests/:id`、`POST /api/digests/:id/preview`（`admin`）。
- MCP：`get_item` 的 `summary`；新 tool `continue_item(item)`（scope `ingest`，回應與 API 相同）；Agent 任務的 `kind: summary`。
- CLI：`notesteme items continue <id>...`（client 模式呼叫批次 API）。
- UI：影音詳情顯示摘要、重點與「繼續處理」按鈕；媒體庫的「等你決定」篩選與批次「繼續處理」；設定的「通知」分區下有彙整設定（UI 切片）。
- 資料表：`digests (id, period, at, weekday, timezone, notifier_ids_json, scope, enabled, created_at, last_run_at)`、`digest_entries (digest_id, item_id, run_at)`，主鍵 `(digest_id, item_id)`（ADR-0021 第 7 點）。
- 排程：`scheduled_jobs.kind = digest`。
- 資源類別：`summary` 是 `llm`；彙整是排程工作，不進佇列。
- `pipeline.Stages`：多一個 `summary`；`plannedStages` 同步移除它（ADR-0021「影響」）。

## 與其他規格的關係

- NOTIFY：彙整是事件 `digest`，經 outbox 送出，重送、dedupe（`digest:<id>:<執行時間>`）與內容清理照 NOTIFY 的規則。
- COST：`summary` 的費用照一般 LLM 步驟記帳與預留；`continue` 後的步驟才預留後續費用。
- SUB：`scope: subscriptions` 依 `items.subscription_id` 判斷；訂閱可以套用一個開啟 `summary` 與 `pause_after` 的處理方案。
- OPS-R14：`awaiting_user` 不算任務沒有進展。

## 不在範圍內

- 在 Telegram 或 Discord 裡直接按按鈕繼續（需要 bot 回呼與對外開放的 endpoint）；彙整只附 UI 連結，在 UI 勾選。
- 長片的 map-reduce 摘要（超過上限就略過，見待決問題 D2）。
- 用摘要自動決定要不要繼續（例如依關鍵字）。
- 暫停中的影音過一段時間自動取消（見待決問題 D5）。
- 彙整寄 email。

## 待決問題

- D1：`summary` 放在 `normalize` 之後、`segment` 之前（ADR-0021 第 3 點），是 ADR-0020「新步驟接在 `translate` 之後」的例外；加入 `pipeline.Stages` 後每部影音多一筆 `skipped`（`off`）的 `stage_runs`，實作切片要先確認預設 tag 的 308 個場景與 `features/passing.txt` 不退步。
- D2：一部影音只送一個請求，超過 2 小時或 32000 tokens 就略過（DIG-R2）（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）。理由：摘要是為了「決定要不要花錢」，長片用 map-reduce 會讓摘要本身就變貴；上限可以調高到 6 小時、200000 tokens，搭配長 context 的模型。
- D3：重試用盡時步驟 `done`、沒有摘要（DIG-R4），比照 CHAP-R4：摘要是輔助資料，讓步驟 `failed` 會擋住後續步驟（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）。
- D4：`scope: subscriptions` 依賴 SUB 規格的 `items.subscription_id`。DIG 的驗收場景沒有建立訂閱（訂閱的句型屬於 SUB），所以場景用 `scope: all` 驗證內容，另一個場景驗證手動新增的影音不在預設的 `subscriptions` 範圍內；「訂閱的影音出現在彙整中」的正向場景，等 SUB 的句型定案後由整合者在 SUB 或 DIG 補上。 已補上（2026-11-23，整合時）：場景「Items picked up by a subscription appear in the default digest」同時用 SUB 的句型（訂閱與假 YouTube feed）與 DIG 的句型。
- D5：暫停中的影音不會自動取消（DIG-R7）（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）。暫停不花錢，只占媒體庫的一列；之後需要的話加 `stages.summary.pause_expire_days`。
- D6：彙整的連結只指向 UI，開啟時要登入（DIG-R10）。不使用「免登入的一次性連結」：通知內容會經過第三方（Discord、Telegram 的伺服器），連結裡的任何憑證都等於外洩。
- D7：彙整沒有隨機延遲（DIG-R8）：彙整只寫 outbox，不打平台的 API，延遲沒有意義；場景可以斷言精確的送出時間。
- D9：DIG-R6 的「下一個步驟」只在這次執行不包含 `segment` 時往後找；ADR-0021 第 3 點寫「被略過時是再下一個」。`segment` 是否略過（例如人工字幕，SEG）要執行時才知道，所以人工字幕的影音會停在 `segment`，繼續之後 `segment` 立刻 `skipped`。結果相同（都還沒花錢），但 ADR 的措辭要改成這裡的寫法。
- D10：`digests` 的欄位與 ADR-0021 第 7 點不同：沒有 `channels_json`（以 `scope` 取代；要限定頻道之後再加），多了 `at`、`weekday`、`timezone`、`scope`、`enabled`、`created_at`、`last_run_at`。ADR 要同步。
- D11：彙整的下次執行時間對齊當地的 `at`（DIG-R9），不是 ADR-0021 第 1 點的「這次執行的時間＋間隔」：每日彙整應該每天在同一個時間送出，停機後補跑一次也不該讓之後每天的時間跟著偏移。ADR 第 1 點要加上「依當地時間對齊的工作」這個例外（每日備份 OPS-R11 相同）。
- D8：DIG-R9 的範圍以「`summary` 步驟結束的時間」判斷，而不是影音建立的時間，讓「探索在彙整之前、摘要在彙整之後才完成」的影音落在下一則彙整，不會漏掉。`summary` 是 `off` 的影音也會在 `summary` 被略過時落入範圍（只有標題，顯示「（沒有摘要）」）。

# BATCH：Batch API

- 狀態：ready
- 里程碑：M4
- 相關：plan 第 8.2、9、15 節；ADR-0004、[ADR-0019](../adr/0019-llm-wire-contract.md)、[ADR-0021](../adr/0021-m4-scheduler-subscriptions-and-outbox.md)（第 1、4、6、7 點）；[COST](COST-cost-budget.md)、[LLM](LLM-execution.md)
- 驗收場景：[`features/BATCH-batch-api.feature`](../../features/BATCH-batch-api.feature)

## 目的

訂閱與回補這類不急的 LLM 工作改用 provider 的 Batch API（Anthropic Message Batches、OpenAI Batch API），單價約是一般 API 的一半。等結果的期間不占用並行上限；等太久就改走一般 API，不會讓影音卡住。

## User stories

| ID | Story |
|---|---|
| BATCH-01 | As the owner, I want non-urgent LLM work sent through provider batch APIs, so that background processing costs less. |

## 規則

| ID | 規則 |
|---|---|
| BATCH-R1 | 處理方案的 `stages.<stage>.batch` 是 `auto` 或 `off`（預設 `off`），可以設在 `summary`、`segment`、`correct`、`translate`、`chapters`、`note`；其他值或其他步驟回 422 `invalid_request`，`errors[].path` 指到該欄位（例如 `stages.correct.batch`），訊息「batch 只能是 auto 或 off。」或「這個步驟不支援 batch。」。`auto` 只在以下條件都成立時使用 batch：任務的優先序是 `subscription` 或 `backfill`（手動新增與重跑是 `manual`，一律走一般 API）；`executor` 是 `api`；provider 的 `type` 是 `anthropic` 或 `openai`，而且沒有自訂 `base_url`（相容服務不一定支援 batch）。不符合時照一般 API 執行，不是錯誤。 |
| BATCH-R2 | 送出：worker 領取到符合 BATCH-R1 的任務時，一次收集同一輪中所有可以領取、provider 與 model 相同的這類任務（最多 100 個），送出成一個 batch；這次送出占用 `llm` 資源類別一次（ADR-0021 第 6 點）。每個請求的內容與一般 API 完全相同（ADR-0019，含 prompt cache 的標記），`custom_id` 是任務 ID。Anthropic：`POST /v1/messages/batches`。OpenAI：先以 `POST /v1/files`（`purpose: batch`，JSONL）上傳，再 `POST /v1/batches`（`endpoint: /v1/chat/completions`、`completion_window: 24h`）。送出成功後寫入 `batches (id, provider, external_id, state, submitted_at, deadline_at, task_ids_json)`，任務的 `batch_id` 指向它。送出本身失敗時（429、5xx、連線錯誤）這些任務改走一般 API。 |
| BATCH-R3 | 等待結果時，任務與步驟的狀態是 `waiting`，不占用並行上限；`deadline_at` 是送出後 24 小時。影音的訊息為「已送出 Batch API，等待結果，最晚 <deadline_at 的 UTC 時間，YYYY-MM-DD HH:MM> UTC。」。 |
| BATCH-R4 | 輪詢：排程工作 `batch.poll`（`key` 是 `batches.id`，ADR-0021 第 1 點）每 5 分鐘查詢一次。結束後（Anthropic `processing_status: ended`；OpenAI `status: completed`）取回結果，依 `custom_id` 交給對應的任務：成功的結果照一般 API 的回應處理（LLM-R4、R9 的驗證；通過就完成這個 chunk）；驗證失敗時用一般 API 重試，計入 LLM-R5 的驗證重試次數。個別請求失敗（Anthropic 的 `errored`、`expired`、`canceled`；OpenAI error file 中的列）時，只有那個任務改走一般 API，其他任務不受影響。batch 被 provider 標為 `failed`、`expired`、`cancelled` 時，所有還沒有結果的任務改走一般 API。 |
| BATCH-R5 | 逾時：`batch.poll` 執行時已經超過 `deadline_at`、batch 仍未結束時，向 provider 送出取消（Anthropic `POST /v1/messages/batches/{id}/cancel`；OpenAI `POST /v1/batches/{id}/cancel`），所有還沒有結果的任務立刻改走一般 API（優先序不變）；之後才回來的結果一律捨棄（`outcome: discarded`，BATCH-R9）。 |
| BATCH-R6 | batch 的單價是一般單價（COST-R2 的覆寫或內建）乘以 provider 的 batch 折扣，內建折扣 Anthropic 與 OpenAI 都是 0.5。每個請求的結果寫一筆 `cost_ledger`，`unit_price_json.batch` 為 `true`，另外記 `batch_discount`；provider 回報金額時仍以回報為準。改走一般 API 的請求以一般單價計算（`batch: false`）。 |
| BATCH-R7 | 預留（COST-R5、R6）：送出前為每個請求以 batch 單價預留，和一般任務一樣在同一個寫入交易中檢查預算；超過預算的任務不放進 batch，依 COST-R8 `blocked`。結果回來時改成實際金額；改走一般 API 時刪除 batch 的預留，以一般單價重新預留與檢查，超過時 `blocked`（`budget_blocked`）。 |
| BATCH-R8 | 來源紀錄（LLM-R10）：經 batch 完成的 chunk 在 `chunks.<n>` 另外記錄 `mode: batch`、`batch_id`（provider 的 batch ID）、`submitted_at`、`ended_at`；改走一般 API 的 chunk 為 `mode: api`，並記錄 `fallback_reason`（`timeout`、`request_failed`、`batch_failed`、`invalid_output`、`submit_failed`）。有任何 chunk 經過 batch 時，產出的來源紀錄頂層 `batch` 為 `true`。 |
| BATCH-R9 | 取消：影音刪除、步驟取消（PIPE-R15）或重跑時，它在 batch 中的任務移除，不再等待；一個 batch 中所有任務都被移除時，向 provider 取消這個 batch。取消後 `batch.poll` 仍然繼續輪詢，直到 provider 回報結束（取消時已經處理完的請求 provider 照樣計費）；這時已經沒有對應任務的成功結果不寫入產出，但要記帳（`outcome: discarded`、`state: actual`，金額依 BATCH-R6），provider 標為 `canceled` 的請求不記帳。同一輪中排程工作先於任務執行（ADR-0021 第 1 點），所以 `discarded` 的紀錄排在重跑的呼叫之前。 |
| BATCH-R10 | 重新啟動後，`batches` 中未結束的 batch 繼續由 `batch.poll` 輪詢，不重新送出；`waiting` 的任務不屬於租約回收（PIPE-R11）的範圍。 |
| BATCH-R11 | 對外請求的限制：只連到 provider 的 `base_url` 所在的主機；Anthropic 的 `results_url` 主機與 `base_url` 不同時不取用，這個 batch 視為 `batch_failed`（所有任務改走一般 API），影音的訊息附上提醒「Batch API 回傳了其他主機的結果網址，已改用一般 API。」。結果檔逐行解析，單一結果最多 10 MiB、整個結果檔最多 256 MiB，超過時視為 `batch_failed`；API key 不寫入 log、事件與 `batches`（只存 provider 的 batch ID）。 |

## 介面

- 處理方案：`stages.<stage>.batch: auto | off`（BATCH-R1 列出的步驟）。
- 設定檔：沒有新欄位。`deadline_at`（24 小時）、輪詢間隔（5 分鐘）、每個 batch 最多 100 個請求先寫死，見待決問題 H2。
- 排程工作：`batch.poll`（ADR-0021 第 1 點）。
- 資料表：`batches (id, provider, external_id, state, submitted_at, deadline_at, task_ids_json)`、`tasks.batch_id`（ADR-0021 第 7 點）。`batches.state`：`submitted`、`ended`、`cancelling`、`cancelled`、`failed`。
- 資源類別：送出占用 `llm` 一次；等待中的任務是 `waiting`，不占用並行上限（ADR-0021 第 6 點）。
- API：`GET /api/items/:id` 的步驟在等待 batch 時帶 `batch: {provider, submitted_at, deadline_at}`。沒有新路由。
- `pipeline.Stages`、`outputs`：不影響。
- 測試替身：`features/internal/fakebatch`（`features/fixtures/README.md` 的「M4 的假服務」）。

## 與其他規格的關係

- COST：batch 單價與預留（COST-R1、R5、R6）。
- SUB、PIPE：只有訂閱探索建立的 `subscription`、`backfill` 優先序的任務會用 batch（ADR-0021 第 2 點）。
- LLM：每個請求的內容、驗證與重試照 LLM 規格；batch 只改變送出與取回的方式。
- AGT：`executor: agent` 不使用 batch。

## 不在範圍內

- Gemini 的 batch 模式、其他 provider 或相容服務的 batch。
- 手動新增的影音使用 batch（手動新增的人通常在等結果）。
- 把已經在等 batch 的任務手動改成一般 API 的 UI 動作（H3）。
- 為了湊滿 batch 而延後送出（H4）。

## 待決問題

- H1：預設 `batch: off`（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）。開啟 `auto` 會讓訂閱的影音最多晚 24 小時才完成，而且改變既有場景的行為；內建的處理方案都不開啟，由使用者在處理方案中選擇。
- H2：逾時 24 小時、輪詢 5 分鐘、一個 batch 最多 100 個請求先寫死（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）。兩家的 batch 都承諾 24 小時內完成；個人規模一輪很少超過 100 個 chunk。之後有需要再開放設定。
- H3：等 batch 的影音沒有「改用一般 API」的動作（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）。要立刻處理時，使用者可以從那個步驟重跑（重跑是 `manual` 優先序，走一般 API，原本的 batch 任務依 BATCH-R9 移除）。
- H4：送出時只收集同一輪可以領取的任務，不等其他影音（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）。等湊滿會讓第一部影音多等，也讓 fake clock 的場景更難預測；batch 的折扣與 batch 大小無關。
- H5：驗收場景用 `the batch API of … completes after …` 在 fake clock 上指定完成時間。排程補跑只執行一次（ADR-0021 第 1 點），所以 `When the clock advances by "15m"` 只會輪詢一次；場景的時間都依這點設計（完成時間 10 分鐘、逾時用 25 小時）。
- M4-S0：`stages.<stage>.batch` 在 `executor` 之後、`provider`／`model` 的完整性檢查之前檢查，所以 BATCH-R1 的「batch 只能是 auto 或 off。」是 `errors[0]`（場景的陣列子集比對依索引）（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）。`sponsorblock`、`frames` 與 pipeline 的其他步驟（例如 `transcript`）寫了 `batch` 時，在 `stages.<stage>.batch` 回「這個步驟不支援 batch。」；`transcript` 這類不能設定的步驟只有 `batch` 一個欄位時，只回這一個錯誤（不再另外回「這個步驟沒有可設定的欄位。」）。`executor: batch` 原本的訊息「batch 屬於 M4，尚未支援。」改成「batch 不是 executor；請改用 stages.<步驟>.batch: auto。」（PRF 的 S0 決定中提到的 422 仍然成立）。
- M4-S5 的實作決定（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：
  - 接上 batch 的步驟：經 LLM chunk engine 的 `segment`、`correct`、`chapters`、`note`。`translate` 的 chunk 走自己的路徑（`runTranslateLLM`，含 TRN-R5～R7 的逐單位驗證），`summary` 不經 chunk engine；這兩個步驟設定 `batch: auto` 仍然通過驗證，但照一般 API 執行，待後續切片接上。
  - 「同一輪」的收集：被領取的 chunk 任務送出時，在同一個寫入交易中把還沒被領取（`pending`、到期、builtin、`llm` 類別、同 provider 與 model、優先序不高於 `subscription`）的任務一起放進 batch，最多 100 個；同一輪已經被其他 worker 槽位領取的任務會各自送出另一個 batch（不跨租約搶任務）。
  - `batches.task_ids_json` 存物件陣列 `[{task, run, chunk, reservation}]`（不是單純的任務 ID），讓結果回來時不必再查任務就能對應 stage run、chunk 與預留；另外記 `model` 與 `notice`（BATCH-R11 的提醒）。資料表在 migration `00024_batches.sql`；`llm_chunks` 加上 `batch_json` 記 BATCH-R8 的紀錄。
  - 送出時先在交易中寫入 `batches`（`external_id` 空白）、把任務設為 `waiting`、排好 `batch.poll`，交易之後才呼叫 provider；成功時寫入 `external_id`。程序在兩者之間中斷時，第一次輪詢看到空白的 `external_id` 就把任務改走一般 API（`submit_failed`）；provider 端可能已經建立的 batch 不會被取回或取消（無法得知它的 ID），可能重複計費一次。
  - 個別請求的結果：`errored` 與無法解碼的成功結果記一筆 `outcome: http_error`、$0 的實際紀錄（provider 不對失敗的請求收費），改走一般 API（`request_failed`）；`expired`、`canceled` 與沒有出現在結果檔中的請求刪除預留、不記帳。結果檔中沒有對應任務的 `custom_id` 忽略。
  - 驗證失敗（`invalid_output`）照樣以 batch 單價記帳；還有驗證重試額度時改走一般 API，第一次一般請求就附上驗證錯誤的說明並計入 LLM-R5 的次數；額度已經是 0 時直接照 LLM-R5 退回（不再送一般 API），這個 chunk 記 `mode: batch` 並出現在 `fallbacks`。
  - 輪詢失敗（查詢狀態、取回結果、沒有 client）時回傳錯誤，由排程器退避重試；已經超過 `deadline_at` 仍然失敗時不再等待：還沒有結果的任務改走一般 API（`batch_failed`），batch 的預留全部刪除，`batches.state` 標為 `failed` 並停止輪詢。之後 provider 端才產生的費用不會被記帳（無法取回結果）。
  - 逾時或全部移除時送出取消；取消後 provider 回報仍在處理時 `batches.state` 是 `cancelling`，繼續輪詢、不重送取消。開著（`submitted`、`cancelling`）的 batch 的預留不算 PIPE-R11 的孤兒預留，即使任務已經取消；batch 結束之後才清掉。
  - `stages.<stage>.batch` 不列入 LLM 產出的 params sha（同樣的輸入經 batch 或一般 API 是同一份產出，快取照樣命中；改 `batch` 不會讓現行產出變成過期）。BATCH-R11 的提醒在步驟完成且沒有其他訊息時成為步驟的訊息。
  - `GET /api/items/:id` 的步驟欄位 `batch` 只在步驟是 `waiting` 且有等待中的 batch 時出現（`omitempty`）；MCP 與 CLI 沒有新增欄位。
  - 驗收場景的 `provenance` 斷言中，值 `*` 代表「存在、不是空字串也不是 null」（`submitted_at`、`ended_at` 的時間由 fake clock 決定），見 steps.md 的 M4-S5 說明。
  - Batch API 的 client 只用在內建 `base_url` 的 `anthropic`、`openai`（`llm.BatchSupported`），不跟隨重新導向；Anthropic 的 `results_url` 要求與 `base_url` 同 scheme 與主機（含連接埠），OpenAI 的結果檔與錯誤檔一律以檔案 ID 從 `base_url` 下載。provider 回傳的 batch ID 與檔案 ID 只接受 `[A-Za-z0-9_-]`（最多 128 字）：送出時不合格視為送出失敗（`submit_failed`），之後不合格時輪詢回錯誤、依排程退避重試，直到逾時。

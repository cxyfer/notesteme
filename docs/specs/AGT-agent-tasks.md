# AGT：Agent 任務

- 狀態：draft
- 里程碑：M2
- 相關：plan 第 8.1、9 節；ADR-0002、ADR-0004；UI [佇列](../ui/pages.md#6-佇列)
- 驗收場景：[`features/AGT-agent-tasks.feature`](../../features/AGT-agent-tasks.feature)

## 目的

讓 Agent 透過 MCP 一次領取多個 LLM 任務、在租約內完成並提交；提交結果和 API executor 走同一套驗證。沒有 Agent 來領時，依設定改用 API 或通知使用者。

## User stories

| ID | Story |
|---|---|
| AGT-01 | As an agent, I want to claim several tasks at once with everything I need to do them, so that I can work through the queue efficiently. |
| AGT-02 | As the owner, I want two agents working at the same time never to get the same task, so that work is not done or paid for twice. |
| AGT-03 | As an agent, I want my invalid submissions explained while I keep the task, so that I can fix and resubmit. |
| AGT-04 | As the owner, I want tasks abandoned by an agent to return to the queue, so that nothing gets stuck. |
| AGT-05 | As the owner, I want tasks nobody claims to fall back to the API after a while, so that unattended processing still finishes. |
| AGT-06 | As the owner, I want results from agents marked as agent-reported, so that I know the model name could not be verified. |

## 規則

| ID | 規則 |
|---|---|
| AGT-R1 | `executor: agent` 的步驟建立任務後，步驟狀態為 `awaiting_agent`，直到任務被領取。 |
| AGT-R2 | `claim_tasks(kinds, max)` 回傳最多 `min(max, agent.max_claims)` 個任務，也不會超過 `llm` 資源類別剩餘的並行數。每個任務附帶 `lease_token`、`lease_until`，以及完成任務所需的全部上下文：渲染後的 prompt、chunk 的字幕、輸出的 JSON schema。 |
| AGT-R3 | 領取是原子操作：兩個 Agent 同時領取時，不會拿到相同的任務。 |
| AGT-R4 | 租約預設 10 分鐘；`extend_lease` 延長一個租約期間。租約到期的任務回到佇列，`attempts` 加 1。 |
| AGT-R5 | `submit_task` 使用和 API executor 相同的驗證。失敗時回傳錯誤說明，租約保留，Agent 可以修正後重交。 |
| AGT-R6 | 同一份租約連續 3 次驗證失敗，任務放回佇列；處理方案設定 `agent.on_validation_failure: fallback_api` 時改用 API executor。 |
| AGT-R7 | 任務被重新領取後，舊的 `lease_token` 失效，用它提交會被拒絕，錯誤為 `lease_expired`。 |
| AGT-R8 | `release_task` 立即把任務放回佇列，並記錄原因；不計入 `attempts`。 |
| AGT-R9 | 任務超過 `agent.timeout`（預設 24 小時）沒有被領取時，依 `agent.on_timeout` 處理：`fallback_api`（預設）改用 API executor，處理方案必須為這個步驟設定 `provider` 與 `model`，否則改為通知；`notify` 時步驟為 `blocked`（`needs_action`），訊息為「這個任務等待 Agent 超過 24 小時。」，動作為「改用 API 處理」。 |
| AGT-R10 | Agent 提交的產出，來源紀錄為 `executor: agent`、`agent: <token 名稱>`、`model_reported`（Agent 自己回報的模型，可省略）、`model_verified: false`。 |
| AGT-R11 | 使用者可以把等待中或已被領取的 Agent 任務改用 API 執行；原本的租約立即失效。 |
| AGT-R12 | 領取與提交需要 `agent` scope。 |

## 介面

- MCP：`claim_tasks`、`extend_lease`、`submit_task`、`release_task`
- API：`POST /api/tasks/:id/switch-executor`、`POST /api/items/:id/switch-executor`（以步驟為單位切換）
- 處理方案：`agent.{max_claims, lease, timeout, on_timeout, on_validation_failure}`

## 不在範圍內

- 在主機上排程 headless Agent 消化佇列：在 Notesteme 外部設定（ADR-0002）。

## 待決問題

S10 實作時的決定（2026-11-23，依 /goal 採保守預設，待 owner 追認）：

- A1 並行上限（AGT-R2 的「`llm` 資源類別剩餘的並行數」）：解讀成「這個 Agent 已持有的 `llm` 任務加上這次領取的，不超過 `llm` 類別的並行上限」（`PATCH /api/queue/limits` 的設定值；測試用的 `SerialLLM` 不影響）。字面的「上限減去所有被領取的 `llm` 任務」不可行：AGT-R3 場景中兩個 Agent 各領 3 個，同時被領取 6 個，超過預設上限 4。整個佇列或 `llm` 類別暫停時不發出新任務（PIPE-R10）。計數與領取在同一個寫入交易中。
- A2 介面細節：
  - `claim_tasks(kinds, max)`：`kinds` 只能是 `correct`、`segment`（省略時兩種都領）；`max` 省略時用 `max_claims`。回傳 `{tasks: [...]}`，每項是 `task_id`、`lease_token`、`lease_until`（RFC 3339，UTC）、`kind`、`item`（代號）、`item_id`、`chunk`、`chunks`、`prompt`（`{system, user}`：和 API executor 送給 provider 的相同，`user` 是 `<video_metadata>` 加上 `<transcript_data>`）、`context`、`cues`（`[{i, text}]`）、`output_schema`。
  - `extend_lease(task_id, lease_token)`：新的到期時間是「現在加上一個 `agent.lease`」，不是在原本的到期時間上累加；已到期（即使還沒被放回）的租約不能延長。
  - `submit_task(task_id, lease_token, output, model?)`：`output` 是 JSON 物件，也接受內容是 JSON 的字串；`model` 是 AGT-R10 的 `model_reported`（去掉控制字元，最多 200 字）。租約已到期（即使還沒被放回）、被放回、被別人領取或改用 API 時一律回 `lease_expired`。
  - `release_task(task_id, lease_token, reason?)`：原因去掉控制字元，最多 500 字，記在 `tasks.release_reason`。
  - ADR-0004 的簽名只有 `lease_token`；實作多要求 `task_id`，讓錯誤能分辨「任務不存在」與「租約失效」。
- A3 `agent.*` 的來源與預設值：`max_claims` 不屬於任何影音，讀全域設定加預設處理方案（預設 4）；`lease`（預設 10m）、`timeout`（預設 24h）、`on_timeout`（預設 `fallback_api`）、`on_validation_failure`（`requeue`｜`fallback_api`，預設 `requeue`）讀任務所屬 stage run 的設定快照（notify 場景只有那次重跑用 `agent-notify`）。
- A4 執行方式：`executor: agent` 的 LLM 步驟和 api 共用 LLM 引擎（ADR-0019 的 plan → chunk → assemble）：plan 與 assemble 由內建 worker 執行，只有 chunk 任務的 executor 是 `agent`（資源類別 `llm`）。所以步驟建立後先是 `pending`，plan 完成、chunk 任務建立後才是 `awaiting_agent`（AGT-R1）；有任務被領取時是 `running`，沒有任務被領取、還有任務等待時回到 `awaiting_agent`。指定重跑的 `chunks`（LLM L12）只用於 api。
- A5 驗證（AGT-R5）：Agent 的提交用同一個 `Spec.Validate`、同一份 chunk 輸入（`prepareLLM` 重建，與 `RunChunk` 相同），寫回用與 api chunk 任務相同的 `finishChunkTx`（所有 chunk 都完成才建立 assemble 任務）；correct 的一對一、相似度與 segment 的 `text_changed`、`alignment` 都在其中，沒有另外一份。
- A6 translate：translate 的驗證與退回（TRN-R5～R7，多目標、句群與切分標記）在 `runTranslateLLM` 中，還沒有抽成 Agent 提交可以共用的形式。在那之前 `translate` 加 `executor: agent` 時步驟以 permanent 失敗，訊息「translate 還不支援 agent 執行方式，請改用 api。」，`claim_tasks` 也不接受 `translate`，不會有沒經過 TRN 檢查的 Agent 結果被採用。
- A7 attempts：領取時 `attempts` 加 1；租約到期放回時保留（AGT-R4 的「加 1」），`release_task` 放回時減回去（AGT-R8）；同一份租約連續 3 次驗證失敗放回時保留（計入）。Agent 任務的 attempts 沒有上限（到期多少次都會放回）。重新啟動（PIPE-R11）與正常關閉（PIPE-R12）不動 Agent 的租約：租約屬於 Agent，不屬於 worker。
- A8 逾時（AGT-R9）：「沒有被領取」從任務建立、或最近一次被放回佇列時起算（`tasks.deadline_at`）。`fallback_api` 時這個 stage run 所有等待中與被領取中的 Agent 任務一起改用 API；`notify`（或 `fallback_api` 但沒有 `provider`、`model`）時步驟 `blocked`，訊息依設定的時長（24h 寫成「24 小時」），只通知一次；這時任務仍可以被 Agent 領取，有 Agent 正在處理其他 chunk 時不打斷。sweeper 是 worker 每一輪開頭的定期工作，production 閒置時睡到最早的租約到期或領取期限。
- A9 來源紀錄（AGT-R10）：有任何 chunk 是 Agent 提交的，產出的 `executor` 是 `agent`、`agent` 是 token 名稱（多個 Agent 時以逗號連接）、`model_reported` 只在每個 Agent chunk 都回報同一個模型時寫、`model_verified: false`；沒有任何 chunk 由 provider 產生時不寫 `provider`、`model`。`chunks[]` 中 Agent 提交的項目另有 `executor`、`agent`、`model_reported`。Agent 的結果不寫步驟層的快取鍵，chunk 層也不重用（`executor = 'api'` 的 chunk 才重用），之後同設定的 api 重跑不會把未驗證的結果當成 provider 的。
- A10 切換執行方式（AGT-R11）：`POST /api/items/:id/switch-executor` 的 body 是 `{stage, executor}`，`POST /api/tasks/:id/switch-executor` 是 `{executor}`；`executor` 目前只能是 `api`，scope 是 `ingest`（AUTH 規格的 M2 路由 scope）。回 200 `{item_id, stage, executor, tasks}`；沒有可以切換的 Agent 任務回 409 `no_agent_tasks`，處理方案沒有為這個步驟設定 `provider` 與 `model` 回 409 `api_not_configured`。切換後原租約立即失效（舊 token 回 `lease_expired`），任務交給內建 worker，步驟回到 `pending`。
- A12 重跑：重跑取代 Agent 步驟時，舊 stage run 中等待中與被領取中的 Agent 任務一起結束（`failed`，租約清除），舊 token 回 `lease_expired`；內建 worker 已領取的任務照舊由 apply 丟棄結果。
- A11 新增的資料欄位：migration 00016 的 `tasks.agent_failures`、`tasks.release_reason`、`llm_chunks.executor`、`llm_chunks.agent`、`llm_chunks.model_reported`。
- 已知限制：提交沒有 token 用量（`usage` 是 0）；`claim_tasks` 組不出某個任務的上下文時（例如輸入的產出已被清除），那個任務放回佇列、不計次數，只記 log。
- M3 修訂（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：`claim_tasks` 的 `kinds` 增加 `chapters`、`note`、`note_map`（省略時仍只領 `correct`、`segment`，避免既有 Agent 突然收到新的任務）；`frames` 選圖在 FRM 的 F6 決定圖片如何傳遞之前不交給 Agent（ADR-0020、[CHAP-R8](CHAP-chapters.md)、[NOTE](NOTE-notes.md)）。既有 M2 場景不變。M3-S2 已讓 `kinds` 接受 `chapters`（`note`、`note_map` 等 NOTE 的切片）。
- A13 佇列 API 的 Agent 任務（2026-11-24，依 /goal 由實作者暫定，待 owner 追認）：`GET /api/queue` 的 `tasks` 也列出 Agent 的任務（等待中與被領取中），排在內建 worker 的任務之後，各自先列被領取的、再列 pending 的（各自的領取順序）；每項多三個欄位：`executor`（`builtin`｜`agent`）、`agent`（領取者的 token 名稱，只有被 Agent 領取中的任務才有）、`lease_until`（租約到期時間，RFC 3339 UTC，名稱同 AGT-R2；只有被 Agent 領取中的任務才有，其他是 `null`）。內建 worker 的租約是程序內部的心跳，持有者是 worker 的 ID，不外露也不給 `lease_until`。`lanes` 的 `running`、`waiting` 仍只算內建 worker 的任務：Agent 領取時的上限是「每個 Agent 持有的 llm 任務不超過 llm 的並行上限」（AGT-R2），不佔內建 worker 的名額，算進去會出現 running 大於 limit。佇列頁的 Agent 任務顯示（是哪個 Agent、租約剩多少時間）留給 UI 的切片（[pages.md 第 6 節](../ui/pages.md)）。驗收：`features/AGT-agent-tasks.feature` 的「The queue shows agent tasks with their lease」。

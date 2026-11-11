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

- 無。

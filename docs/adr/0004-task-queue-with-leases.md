# ADR-0004：用租約制任務佇列，Agent 可以一次領多個任務

- 狀態：已採納
- 日期：2026-11-11
- 修訂：2026-11-12，chunk 即任務、`wait` 不計重試、aging 寫進排序、重啟回收、Agent 逾時、可替換的 backend

## 背景

- 一部影片會拆成多個步驟，有些可以平行處理，例如 LLM 的 chunk。
- 同時在處理的影片可能很多，例如訂閱的頻道一次推出好幾部。
- Agent 透過 MCP 領取 LLM 任務，而且要能一次領多個。
- 外部 provider 都有 rate limit。

## 決策

### 資料結構

```
tasks (id, stage_run_id, kind, executor, resource_class, state, priority, payload_json,
       chunk_index, result_path, lease_token, lease_owner, lease_until, attempts,
       max_attempts, not_before, deadline_at, created_at)
```

- `state`：`pending → leased → done`；失敗時轉為 `failed`，或回到 `pending` 等待重試。
- 任務依 DAG 建立：上游完成後才建立下游任務。
- LLM 步驟的每個 chunk 是一個任務，各自快取與重試。全部 chunk 完成後才組成步驟的產出。
- 回報 `wait`（例如自動字幕還沒生成）時，任務回到 `pending` 並設定 `not_before`。這次領取不計入 `attempts`，最長等待時間由 `deadline_at` 限制，逾時就改用下一個來源。

### 領取方式

SQLite 開 WAL 模式，用一條 SQL 原子地領取任務：

```sql
UPDATE tasks
SET state = 'leased', lease_token = :token, lease_owner = :owner,
    lease_until = :until, attempts = attempts + 1
WHERE id IN (
  SELECT id FROM tasks
  WHERE state = 'pending' AND executor = :executor AND kind IN (:kinds)
    AND (not_before IS NULL OR not_before <= :now)
  ORDER BY priority + (:now - created_at) / :aging_interval DESC, created_at
  LIMIT :max
)
RETURNING *;
```

- 時間欄位存成 Unix 毫秒整數，aging 可以直接在排序條件中計算。

### 並行與限流

- 每個資源類別有並行上限，例如 `download=2`、`asr=3`、`llm=4`、`frames=CPU 核心數`，可以在 UI 即時調整。
- 每個 provider 用 token bucket 限制 RPM 和 TPM。
- Agent 每次領取的上限由 `agent.max_claims` 控制；某個資源類別已滿時，就不再發出該類別的任務。
- 並行計數、限流與事件推送都在 `serve` 的記憶體中。同一份 `/data` 同時只有一個擁有者（`serve` 持有 flock，CLI 改走 client 模式），所以不會重複計算。

### Agent 的 MCP 流程

| Tool | 行為 |
|---|---|
| `claim_tasks(kinds, max)` | 回傳最多 `max` 個任務；每個任務附帶 `lease_token`、`lease_until`、上下文（prompt、字幕 chunk、輸出 schema） |
| `extend_lease(lease_token)` | 延長租約 |
| `submit_task(lease_token, result)` | 驗證結果。成功就完成任務；失敗就回傳錯誤，租約保留讓 Agent 修正後重交 |
| `release_task(lease_token, reason)` | 主動放回佇列 |

- 租約到期（預設 10 分鐘）的任務自動回到 `pending`。
- `submit_task` 用 `lease_token` 確保冪等：任務已被別人重新領走時，拒絕舊 token 的提交。
- 同一份租約連續驗證失敗 N 次後，任務放回佇列，或依設定改用 `api` executor。
- 任務超過 `agent.timeout`（預設 24 小時）都沒有 Agent 領取時，預設改用 `api` executor（見 ADR-0002）。

### 重啟與關閉

- 每次啟動都產生新的 worker ID。啟動時，舊 worker ID 持有的 `leased` 任務放回 `pending`，`running` 的 stage_run 重新推導狀態。
- 子程序放在獨立的 process group 中執行（Windows 用 Job Object），取消或關閉時整組一起結束。
- 正常關閉時先停止領取新任務，等候一段時間後主動釋放租約。
- 啟動時清空 `/data/tmp`。

### 可替換的 backend

- 佇列、限流（token bucket）、事件匯流排都定義成介面。預設實作是 SQLite 加上記憶體。
- 選配 Postgres 與 Redis（ADR-0007）：
  - Postgres 的佇列用 `FOR UPDATE SKIP LOCKED` 領取，可以安全地跑多個 worker；scheduler 用 advisory lock 確保只有一份。
  - Redis 提供跨 process 共用的限流與事件推送。
- M4 的 `batch` executor 也走同一個佇列：把同類任務收集成一批送出，再輪詢 provider 的結果。

### 優先順序

- 手動新增 > 訂閱的新內容 > 回補舊內容。
- 等待越久的任務，優先度逐漸提高（aging），避免一直排不到。
- 可以暫停或恢復整個佇列，也可以只暫停某個資源類別。

## 考慮過的選項

- **River 等 Postgres 佇列**：Client 用的是 SQLite，不適用。Hub 之後如果需要，可以再評估。
- **外部 broker（Redis、NATS）**：單機自架的情境下太重。
- **只在記憶體中排程**：重啟後就遺失，也無法支援 Agent 的租約。

## 影響

- 好處：
  - 重啟後可以續跑。
  - 多個 Agent 同時工作也安全。
  - 並行狀況可以直接在 UI 上看到。
- 代價：
  - SQLite 一次只能有一個寫入者，寫入量大時要批次處理。
  - 單機規模足夠；需要多台 worker 時，改用選配的 Postgres＋Redis backend。

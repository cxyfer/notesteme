# ADR-0021：M4 的排程器、訂閱探索、摘要步驟、預算預留與通知 outbox

- 狀態：提議中（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）
- 日期：2026-11-23

## 背景

M4「追蹤與部署」的驗收條件是「VPS 上訂閱 3 個頻道，無人值守運作一週」（plan 第 24 節）。規格展開時（[SUB](../specs/SUB-subscriptions.md)、[ENTRY](../specs/ENTRY-entry-points.md)、[BND](../specs/BND-bundles.md)、[DIG](../specs/DIG-digest.md)、[BATCH](../specs/BATCH-batch-api.md)、[GLOS](../specs/GLOS-glossary-review.md)、[SRCH](../specs/SRCH-search.md)、[NOTIFY](../specs/NOTIFY-notifications.md)、[COST](../specs/COST-cost-budget.md)，以及 SRC、LIB、AUTH、OPS 規格的 M4 章節）遇到幾個跨規格的決定：

- 訂閱檢查、SRC-10 的重新 Probe、彙整、每日備份、無人值守告警、通知重送、Batch 結果輪詢都是「定時執行」的工作。plan v0.3 已決定排程自寫（不用 gocron），因為驗收測試必須能確定排程已經處理完（ADR-0015、NFR-TEST-2）。
- `worker` 已有 `Periodic` 介面，`internal/app/housekeeping.go` 用它做事件清理（OPS-R10）與垃圾桶清理（DEL-R4）：下一次執行的時間存在 `settings`，重啟後過期的工作只執行一次。
- plan 第 5 節把 `discover` 列為步驟，但它的輸入是訂閱而不是影音；`summary` 在 plan 第 5 節排在 `normalize` 之後、`segment` 之前，而 ADR-0020 的策略是新步驟一律接在 `translate` 之後，避免影響既有的 308 個場景。
- 預算要在平行任務之間不超支（COST-02）；通知要在通知端點暫時失敗時重送，而且能用 fake clock 驗證。

## 決策

### 1. 排程器（scheduler）

- 排程器是 worker 的一個 `Periodic`（和 housekeeping 並列，`internal/app/periodics.go`），不另開 goroutine。每一輪開頭以注入的 clock 取得 `now`，執行所有 `next_run_at <= now` 的排程工作，再讓 worker 領取任務。worker 的 `nextWake` 要把最早的 `next_run_at` 納入，閒置的 worker 到期時會醒來。
- 驗收測試用既有的 `When the clock advances by …` 加上 `When the worker runs until idle` 驅動排程（steps.md 4.1 的語意：到期的排程與租約在下一次 worker 執行時處理），不新增「排程器 tick」的句型。
- 新資料表 `scheduled_jobs (kind, key, next_run_at, last_run_at, last_ok_at, last_error, failures)`，主鍵 `(kind, key)`。`kind` 例如 `subscription.check`（`key` 是訂閱 id）、`source.reprobe`（`key` 是影音 id）、`digest`（`key` 是彙整設定 id）、`backup.daily`、`alerts`、`notify.deliver`、`batch.poll`、`budget.month`（月初讓 `budget_blocked` 的步驟恢復，COST-R9）。housekeeping 既有的兩項工作暫時維持在 `settings`，之後再搬。
- 重啟或長時間停機後：過期的工作**只補跑一次**，下一次依「這次執行的時間＋間隔」排定，不補跑每一個錯過的時段。例外：彙整（`digest`）、每日備份（`backup.daily`）與 `budget.month` 對齊設定時區的當地時間（例如每天 07:00），下一次排在「下一個對齊的時間點」，同樣只補跑一次、沒有隨機延遲（DIG 規格 D7、D11，OPS 規格）。
- 工作失敗時依 `failures` 退避重試（1 分鐘、5 分鐘、30 分鐘，之後固定 6 小時），不會因為停在過去的 `next_run_at` 而每一輪都重試（同 housekeeping）。
- 隨機延遲（SUB-06）是確定性的：`jitter = sha256(kind | key | 時段序號)` 換算成 `[0, min(間隔 × 10%, scheduler.jitter_max))`，`scheduler.jitter_max` 只能寫在設定檔，預設 `30m`；設成 `0s` 時沒有延遲，場景可以斷言精確的下次檢查時間。
- 同時到期的工作依 `next_run_at`、再依建立順序執行（SUB-R9）。被平台 rate limit 擋下的檢查不算失敗，下一次排在額度恢復的時間；收到 429 時取退避時間與額度恢復時間中較晚的一個（SUB-R10）。
- 每一輪排程器的總執行時間上限 30 秒（以注入的 clock 判斷，測試中不會觸發）；沒執行完的工作留到下一輪，不阻塞任務領取。

### 2. 訂閱探索（discover）不是影音的步驟

- `discover` 是訂閱層的工作（`scheduled_jobs.kind = subscription.check`），不是 `pipeline.Stages` 的成員，也不建立 `stage_runs`。plan 第 5 節表格中的 `discover` 列以這裡為準。
- 探索在排程器內同步執行（不進任務佇列），但所有對外請求都經過每個平台的 rate limiter（token bucket，以注入的 clock 計時，數值見 SUB 規格）；yt-dlp 呼叫經 `internal/toolexec`。
- 探索建立的影音直接以既有的 ingest 服務建立：發布時間在訂閱建立之後的影音用 `PrioritySubscription`，回補的舊影音用 `PriorityBackfill`（`internal/queue` 已有這兩個優先級）。`items.subscription_id` 指向訂閱。頻道訂閱以發布時間判斷「新內容」；播放清單、Bilibili 合集與 Podcast feed 的順序與發布時間無關，改以「第一次成功檢查時的清單」為基準，之後才出現的條目算新內容（SUB 規格 S1）。
- 每個探索到的條目都記錄在 `subscription_entries`（含被略過的條目與原因），讓試跑預覽、略過原因與「不重複加入」可以查得到。

### 3. `summary` 步驟

- `summary` 加進 `pipeline.Stages`，位置在 `normalize` 之後、`segment` 之前（照 plan 第 5 節）。這是 ADR-0020「新步驟接在 translate 之後」的例外：「摘要後暫停」（DIG-02）的目的是在 `segment`、`correct`、`translate` 花錢之前停下來，放在後面就沒有意義。
- 預設 `stages.summary.executor: off`，略過的原因是 `off`。內建的處理方案都不開啟。
- 驗收要求：加入 `pipeline.Stages` 之後，預設 tag 的 308 個場景與 `features/passing.txt` 不能退步。每部影音會多一筆 `skipped`（`off`）的 `stage_runs`，實作 child 要確認沒有斷言依賴「步驟清單的長度」或「下一個步驟是 segment」。有的話停下來回報，不要改 `.feature`。
- 「摘要後暫停」（`stages.summary.pause_after: true`）：`summary` 完成後，下一個步驟（`segment`；這次執行不包含 `segment` 時是再下一個）的狀態為 `blocked`，原因 `awaiting_user`。人工字幕的影音也停在 `segment`，繼續之後 `segment` 才依 SEG-R1 略過。細節見 DIG 規格 DIG-R6、D9。

### 4. 預算預留

- 預算檢查與預留在**同一個寫入交易**中完成：讀取本月 `actual + reserved` 的總和、比較上限、寫入 `reserved` 列（store 只有一條寫入連線，交易之間是序列化的）。平行任務因此不會一起超支（COST-02）。
- 任務完成時把 `reserved` 改成 `actual`（同一列更新 `state`、`usd`），失敗或取消時刪除預留。程序中斷留下的預留在重啟時（租約回收，PIPE-R11）一併釋放。
- Agent 模式的金額是 `unknown`，不預留、不計入預算。
- 月份以設定的時區（`budget.timezone`，預設 `UTC`）的日曆月計算。

### 5. 通知 outbox

- 通知事件在產生的那個交易中寫入 `notification_outbox (id, notifier_id, event, dedupe_key, payload_json, state, attempts, next_attempt_at, last_error, created_at, sent_at)`，由排程工作 `notify.deliver` 送出。
- 送出失敗依 1 分鐘、5 分鐘、30 分鐘、2 小時、6 小時退避，第 6 次失敗後標記 `dropped`；端點回應屬於 `permanent` 的錯誤（例如 400、401、403、404）時第一次就 `dropped`（NOTIFY 規格 N1），並寫入事件 `notify.dropped`（UI 的總覽顯示，不再以通知送出，避免迴圈）。
- 同一個 `dedupe_key`（例如 `budget.warning:2026-11`）在一個視窗內只送一次，數值見 NOTIFY 規格。
- 通知端點的設定（`notifiers.config_ciphertext`）用 SEC 的信封加密存放；webhook 的請求走 netguard（GRD）。

### 6. 資源類別

| 工作 | 資源類別 |
|---|---|
| 訂閱探索、SRC-10 的重新 Probe 與 SponsorBlock 重新查詢 | 排程器內同步執行，受平台 rate limiter 限制，不占用任務的資源類別 |
| `summary` 的 LLM 呼叫 | `llm` |
| Batch API 的送出與輪詢 | 送出占用 `llm` 一次；等待中的任務是 `waiting`，不占用並行上限 |
| bundle 匯出與匯入、每日備份 | 同步請求或排程工作，不進佇列 |
| 通知送出 | 排程工作 `notify.deliver` |

### 7. 資料庫 migration 的方向

M4 的 migration 接在 M3 的 migration 之後（M3 規劃 `00017`；實際編號以實作時的最大號加一為準，不寫死）。建議依切片拆成多支：

| 資料表或欄位 | 規格 |
|---|---|
| `subscriptions`（plan 第 21 節的欄位，加上 `next_check_at`、`last_error`、`last_error_class`）、`subscription_entries (subscription_id, platform, external_id, part, title, published_at, duration_s, live_status, decision, reason, item_id, seen_at)`、`items.subscription_id` | SUB |
| `scheduled_jobs` | 本 ADR 第 1 點 |
| `digests (id, period, at, weekday, timezone, notifier_ids_json, scope, enabled, created_at, last_run_at)`、`digest_entries`（欄位以 DIG 規格為準） | DIG |
| `notifiers`（plan 第 21 節）、`notification_outbox` | NOTIFY |
| `price_overrides`（plan 第 21 節）、`settings` 中的 `budget` 區段 | COST |
| `batches (id, provider, external_id, state, submitted_at, deadline_at, task_ids_json)`、`tasks.batch_id` | BATCH |
| `glossary_candidates` 加 `decided_term`、`decided_scope`、`decided_at`（不加 `suggestion`，候選只有 `term`，COR 規格 C4、GLOS 規格 G2） | GLOS |
| `search_cues`（每個語言一份現行字幕的 cue 與 `text_fold`）與 `cues_fts`（FTS5 `trigram case_sensitive 1`，以 `search_cues` 為外部內容表、trigger 同步） | SRCH |
| `oidc_identities (issuer, subject, last_login_at)`、OIDC 登入流程的暫存狀態（state、nonce、PKCE verifier，見 AUTH 規格）、`sessions` 加 `method`（`password`、`proxy`、`oidc`） | AUTH |
| `alert_states (kind, key, active_since, notified_at, resolved_at)`、`tool_call_stats`、cookie 的 `last_ok_at` | OPS |

### 8. 不在 M4 範圍內

- Postgres＋Redis backend：plan 第 26 節是「M4 之後視需要」，M4 只支援 SQLite。
- 多實例之間的訂閱協調（ADR-0011）。
- 媒體檔保留策略（story-map 第 7 節暫定與 LIB-09 一起）：LIB-09 只顯示磁碟用量；自動刪除媒體快取延到 M4 之後（LIB 規格的待決問題）。

## 考慮過的選項

- **gocron 或 `time.Ticker`**：plan v0.3 已排除，驗收測試無法確定排程已經處理完。
- **探索建立 `discover` 任務進佇列**：可以沿用並行上限與租約，但 `tasks` 目前一定屬於某個 `stage_run`，要放寬資料模型；探索的請求量很小，平台 rate limiter 已經足夠。之後真的需要再改。
- **`summary` 接在 `translate` 之後**：不影響 M2 的場景，但「摘要後暫停」就省不到 `segment`、`correct`、`translate` 的費用，DIG-02 失去意義。
- **通知直接在事件發生時同步送出**：簡單，但端點失敗就遺失通知，也讓事件的交易依賴外部服務。

## 影響

- 好處：所有定時工作只有一個入口，fake clock 可以完整驅動；重啟只補跑一次，不會在停機一週後一口氣打出幾百個請求。
- 代價：探索在排程器內同步執行，一個很慢的 feed 會延後同一輪的其他工作（有 30 秒的單輪上限與每個請求的逾時）；`summary` 加在中間，要重新確認既有場景。
- 之後要注意：`nextWake` 要納入 `scheduled_jobs`；`summary` 加入 `pipeline.Stages` 時同步移除 `plannedStages`；`Effective.Stage` 對 `summary` 不繼承其他步驟的 provider。

## 修訂

- 2026-11-23，M4-S0 實作（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：
  - 排程器在 `internal/scheduler`（不是 `internal/app/periodics.go`），以 `worker.Periodic` 接進 `internal/app` 的 `periodics`（一般模式與 MCP 模式都有）；註冊制，每個切片在 worker 開始之前以 `Scheduler.Register` 登記 kind，第一次 `Run` 或 `NextAt` 之後不能再登記（`ErrFrozen`）。註冊表是空的時候不查資料庫，所以 S0 的 worker 行為不變。
  - `scheduled_jobs` 是 migration `00018`，除了第 1 點的欄位另有 `created_at`（同時到期時依 `next_run_at`、再依 rowid 執行）。
  - Handler 的結果有 `Next`（對齊當地時間的工作自己算）、`Done`（刪除那一列）與 `Deferred`（例如 rate limit：只改下一次的時間，不算成功也不算失敗）。
  - jitter 以毫秒為單位（資料庫的時間精度），時段序號是「預設的下一次 ÷ 間隔」的毫秒數；範圍 `[0, min(間隔 × 10%, scheduler.jitter_max))`。
  - 退避 1 分鐘、5 分鐘、30 分鐘，之後固定 6 小時；Handler panic 視為失敗；`last_error` 保留 500 個字元。
  - `summary` 已加進 `pipeline.Stages`，`plannedStages` 已清空（第 3 點與「影響」的提醒）；預設 `executor: off` 的快照 sha 不變，執行器要等 S6 DIG。
- 2026-11-23，M4-S1 實作（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：
  - 第 5 點：端點回應 `permanent` 的錯誤（netguard 封鎖、3xx、408／429 以外的 4xx）第一次就 `dropped`（NOTIFY N1）；`notify.dropped` 只寫進 `events` 表。
  - `notify.deliver` 不刪除排程列，下一次是「最早的 pending 項目」與「一分鐘之後」取較早的；有新的 outbox 項目時提前到現在。端點的失敗記在 outbox，不回傳給排程器。
  - 第 7 點：migration `00019_notify`；`notifiers` 加 `config_nonce`，`notification_outbox` 加 `last_attempt_at`、不加外鍵（見 NOTIFY 待決問題「M4-S1 實作的決定」）。

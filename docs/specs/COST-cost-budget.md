# COST：成本與預算

- 狀態：ready
- 里程碑：M4
- 相關：plan 第 5.2、15、21 節；ADR-0004、ADR-0015、ADR-0019、[ADR-0021](../adr/0021-m4-scheduler-subscriptions-and-outbox.md)（第 1、4、5 點）；UI [成本](../ui/pages.md#11-成本)、[設定的「預算」分區](../ui/pages.md#12-設定)
- 驗收場景：[`features/COST-cost-budget.feature`](../../features/COST-cost-budget.feature)

## 目的

讓使用者知道錢花在哪裡，並確保花費不會失控：每一次付費的 API 呼叫都記帳，開始前先估算並預留，月預算與單部上限以「實際加預留」計算，平行任務不會一起超支；計價表可以覆寫，太久沒確認時提醒。

## User stories

| ID | Story |
|---|---|
| COST-01 | As the owner, I want every API call recorded with its cost, so that I know exactly where my money goes. |
| COST-02 | As the owner, I want a monthly budget and a per-item cap, so that costs never run away. |
| COST-03 | As the owner, I want to override built-in prices and be reminded when they are stale, so that estimates stay accurate. |

## 規則

金額的計算與寫法：

- 加總、比較與預算檢查一律以整數 micro-USD（1e-6 美元，四捨五入）進行，不直接加總浮點數；`cost_ledger.usd` 寫入時先四捨五入到 6 位小數。所以 `0.10 + 0.20` 是 `0.30`，「相等不算超過」（COST-R6）不受浮點誤差影響。
- API 的 JSON 中金額是數字，等於 micro-USD 除以 1e6（最多 6 位小數）。
- 給使用者看的訊息：美元，四捨五入到小數兩位，前面加 `$`，例如 `$0.04`、`$20.00`。
- CSV 的 `usd` 欄（COST-R15）：不帶 `$` 的十進位數字，至少 2 位、最多 6 位小數，多餘的 0 去掉，例如 `0.10`、`0.0123`。

| ID | 規則 |
|---|---|
| COST-R1 | 每一次對付費服務（LLM、ASR、Supadata）送出的處理請求寫一筆 `cost_ledger`，包括 429／5xx 重試、驗證失敗（LLM-R5）與連線錯誤。Batch API 的管理呼叫（建立、查詢、取結果、取消、上傳檔案）不記帳，改為每個請求的結果記一筆（BATCH-R6）。欄位：`stage_run_id`、`task_id`、`provider`、`model`、`attempt`（這個任務的第幾次呼叫，從 1 起算，驗證重試與 HTTP 重試都遞增）、`outcome`（`ok`、`invalid_output`：回應可以計費但沒有通過驗證、`http_error`、`network_error`、`discarded`：見 BATCH-R9）、`units` 與 `unit_type`（LLM 是 `tokens`，`units` 為輸入加輸出；ASR 是 `audio_hour`；Supadata 是 `credit`）、`usd`、`state`。`unit_price_json` 保存當時的計價明細：`source`（`provider`、`override`、`builtin`、`unknown`）、`checked_at`，LLM 另有 `input_mtok`、`output_mtok`、`input_tokens`、`output_tokens`、`batch`（BATCH-R6）。之後改單價不影響已寫入的紀錄。`http_error` 與 `network_error` 的 `usd` 為 0，除非 provider 在錯誤回應中回報了金額。 |
| COST-R2 | 金額的決定順序：(1) provider 在回應中回報的金額（OpenAI 相容服務的 `usage.cost`，例如 OpenRouter），`source: provider`；(2) `price_overrides` 中相符的 `(provider, model, unit_type)`，`source: override`；(3) 內建計價表，`source: builtin`；(4) 都沒有時 `state: unknown`、`usd: 0`、`source: unknown`。LLM 的輸入與輸出單價分別查（`input_mtok`、`output_mtok`），任一個找不到就算找不到。provider 設了自訂的 `base_url` 時不套用內建計價表（ING 待決問題中 Groq 的作法），覆寫仍然適用。 |
| COST-R3 | Agent 模式（`executor: agent`）提交的結果記為 `state: unknown`、`usd: 0`；Agent 有回報 token 數時一併記在 `unit_price_json`。這類紀錄不預留、不計入月預算與單部上限，也不會因為預算而 `blocked`。 |
| COST-R4 | 預算設定是 `GET`／`PUT /api/settings/budget`（`admin`），`PUT` 取代整個區段，沒給的欄位回到預設值：`monthly_usd`（數字或 `null`，預設 `null` 代表不限）、`per_item_usd`（同上）、`warn_at_percent`（整數，預設 80）、`timezone`（IANA 時區，預設 `UTC`）。`GET` 另外回傳 `month`（`YYYY-MM`）、`spent_usd`（本月 `actual` 的總和）、`reserved_usd`（本月 `reserved` 的總和）、`remaining_usd`（`monthly_usd` 為 `null` 時是 `null`）。不正確時回 422 `invalid_request`，`errors[]` 的 `path` 與 `message`：`monthly_usd`「月預算必須介於 0 到 100000 美元之間。」、`per_item_usd`「單部上限必須介於 0 到 100000 美元之間。」、`warn_at_percent`「提醒門檻必須是 1 到 100 的整數。」、`timezone`「不認識的時區 <值>，請用 IANA 時區名稱，例如 Asia/Taipei。」、其他欄位「不認識的欄位。」。預算只存在資料庫（`budget` 是 `dbManagedFields`，不能寫在設定檔）。 |
| COST-R5 | 每個付費任務開始前估算金額並寫入一筆 `state: reserved` 的紀錄：worker 在領取任務的同一個寫入交易中，依領取的順序（優先序、建立時間，PIPE-R9）逐一估算、檢查（COST-R6）並預留；沒有通過檢查的任務不算領取，不占用並行名額。估算：LLM 的輸入 token 以 LLM 規格 L2 的方法計算整個請求；輸出 token 是請求中字幕資料區塊的 token 數乘以 1.2（無條件進位），沒有字幕資料區塊的請求用請求的 `max_tokens`；ASR 以音訊長度乘以單價；單價依 COST-R2 的 (2)～(3)。完成時把同一列改成 `actual`、寫入實際金額（實際金額可以比預留高或低）；任務失敗、取消或放回佇列時刪除預留；程序中斷留下、沒有對應執行中任務的預留，在重新啟動時（租約回收，PIPE-R11）刪除。驗證重試與 HTTP 重試各自重新估算與預留。 |
| COST-R6 | 預算檢查與預留在**同一個寫入交易**中完成（ADR-0021 第 4 點）：讀取本月 `actual + reserved` 的總和，加上這次的估算，超過 `monthly_usd` 就不預留；同時讀取這部影音所有紀錄（不分月份、包括重跑）的 `actual + reserved`，加上估算，超過 `per_item_usd` 也不預留。相等不算超過。平行任務因此不會一起超支：先預留的任務占住額度，後面的任務看到的是已經扣掉的餘額。`state: unknown` 的紀錄不計入。 |
| COST-R7 | 有設定 `monthly_usd` 或 `per_item_usd`，但依 COST-R2 找不到單價（無法估算）時，任務不執行，視同超過預算（COST-R8），訊息「找不到 <provider>／<model> 的單價，無法檢查預算。」，動作為「設定單價」「調整預算」。兩者都沒設定時照常執行，紀錄為 `state: unknown`。 |
| COST-R8 | 超過預算時，任務結束、不送出請求、不算一次嘗試（沿用 `engine.go` 對 `ClassBudgetBlocked` 的處理，不放回佇列，避免 worker 反覆領取），步驟為 `blocked`，原因 `budget_blocked`，錯誤類別 `budget_blocked`，恢復條件（`resume_on`）為 `budget`。訊息：超過月預算時「本月預算 $<monthly> 已用 $<actual+reserved>（含預留），這個步驟預估 $<估算>，超過預算。」；超過單部上限時「這部影音已花費 $<actual+reserved>，這個步驟預估 $<估算>，超過單部上限 $<per_item>。」（兩者都超過時用月預算的訊息）。動作為「調整預算」「查看成本」。已經在執行的任務不受影響。 |
| COST-R9 | `PUT /api/settings/budget` 成功後，以及預算時區的每月 1 日 00:00（排程工作 `kind: budget.month`，ADR-0021 第 1 點），所有 `resume_on: budget` 的 `blocked` 步驟依既有的恢復路徑（`Pipeline.ResumeBlocked`，同 cookie 的 `resume_on`）重新建立任務，在下一次 worker 執行時重新檢查；仍然超過的再次 `blocked`，訊息依當時的數字。 |
| COST-R10 | 本月 `actual + reserved` 第一次達到 `monthly_usd × warn_at_percent / 100` 時（預留或改成實際金額的那個交易中判斷），寫入事件 `budget.warning`，欄位 `month`、`percent`（無條件捨去的整數）、`spent_usd`、`reserved_usd`、`monthly_usd`（和其他事件一樣放在事件的頂層，SSE 與 `GET /api/events` 相同），同一個交易中寫入通知 outbox，`dedupe_key` 為 `budget.warning:<YYYY-MM>`（ADR-0021 第 5 點）。同一個月只寫一次，調整預算不會重設；`monthly_usd` 為 `null` 時不提醒。 |
| COST-R11 | 「本月」是 `budget.timezone` 中的日曆月：`cost_ledger.at` 以 UTC 存放，查詢時換算。改時區之後，已經寫入的紀錄依新時區重新歸月。 |
| COST-R12 | 計價表：`GET /api/prices`（`read`）列出內建與覆寫的單價 `prices[{provider, model, unit_type, usd_per_unit, source, checked_at, stale}]`，依 `provider`、`model`、`unit_type` 排序，同一組有覆寫時只列覆寫；`checked_at` 與 `builtin_checked_at` 是日期 `YYYY-MM-DD`（UTC）；另外回傳 `builtin_checked_at`、`builtin_stale`。`PUT /api/prices`（`admin`）以 body `{provider, model, unit_type, usd_per_unit, checked_at?}` 新增或取代一筆覆寫，`checked_at` 預設為現在；`DELETE /api/prices`（`admin`）以 body `{provider, model, unit_type}` 移除覆寫，不存在時回 404 `not_found`。驗證（422 `invalid_request`）：`unit_type` 只能是 `input_mtok`、`output_mtok`、`audio_hour`、`credit`（「單位只能是 input_mtok、output_mtok、audio_hour 或 credit。」）；`usd_per_unit` 介於 0 到 10000（「單價必須介於 0 到 10000 美元之間。」）；`provider`、`model` 為 1～200 字元、不含控制字元（「名稱必須是 1 到 200 個字元。」）；`checked_at` 不能晚於現在（「確認日期不能晚於今天。」）。 |
| COST-R13 | 計價表太久沒確認時提醒（COST-03）：內建計價表的確認日期是 `cost.PricesAsOf` 與 `POST /api/prices/confirm`（`admin`，把確認日期設為今天）兩者中較晚的一個；覆寫的確認日期是它的 `checked_at`。距今超過 90 天（第 91 天起）為 `stale: true`。有任何 stale 的計價時，`GET /api/costs` 與 `GET /api/overview` 的 `notices` 有一筆 `{code: prices_stale, message, actions}`，訊息「計價表已經 <天數> 天沒有確認，估價可能不準確。」（天數取最久的一筆），動作為「確認計價表」「調整單價」。 |
| COST-R14 | `GET /api/costs?group_by=&from=&to=`（`read`）：`group_by` 是 `day`（預設）、`provider`、`model`、`stage`、`channel`、`item`；`from`、`to` 是預算時區中的日期 `YYYY-MM-DD`，包含兩端，預設為本月 1 日到今天。回應 `{from, to, timezone, total_usd, reserved_usd, unknown_calls, groups[{key, label, usd, reserved_usd, calls}], budget{month, monthly_usd, spent_usd, reserved_usd, projected_usd}, notices}`。`day` 依日期由舊到新，其他依 `usd` 由大到小；已永久刪除的影音以 `item_title` 為 `label`（DEL-R7）。`projected_usd` 是本月 `actual` 除以已過的天數（含今天）再乘以本月天數。明細是 `GET /api/costs/entries?from=&to=&limit=&cursor=`，`limit` 1～500、預設 100，依時間由新到舊；每筆 `{at, item_id, ref, item_title, channel, stage, provider, model, units, unit_type, usd, state, attempt, outcome, unit_price}`，`unit_price` 是 COST-R1 的 `unit_price_json`，`item_title` 在影音還存在時是目前的標題。驗證（422 `invalid_request`）：日期格式不對「日期必須是 YYYY-MM-DD。」、`to` 早於 `from`「結束日期不能早於開始日期。」、範圍超過 366 天「查詢範圍最多 366 天。」、不認識的 `group_by`「group_by 只能是 day、provider、model、stage、channel 或 item。」。 |
| COST-R15 | CSV 匯出 `GET /api/costs/export.csv?from=&to=`（`read`，參數同 COST-R14）：`Content-Type: text/csv; charset=utf-8`，`Content-Disposition: attachment; filename="notesteme-costs-<from>-<to>.csv"`。欄位依序為 `at`（RFC 3339，預算時區）、`item_id`、`item_title`、`channel`、`stage`、`provider`、`model`、`units`、`unit_type`、`usd`、`state`、`attempt`、`outcome`。防範 CSV injection：文字欄位（`item_title`、`channel`、`provider`、`model`、`stage`、`outcome` 等，不只標題）的值以 `=`、`+`、`-`、`@`、tab 或 CR 開頭時，前面加一個 `'`；數字欄位（`units`、`usd`、`attempt`）由程式產生，不加。之後依 RFC 4180 加引號（含 `,`、`"`、換行時）。一次最多 100000 筆，超過時回 422「一次最多匯出 100000 筆，請縮小日期範圍。」。 |
| COST-R16 | 成本頁（UI 第 11 節）：頂部一行「本月花費 $3.42，預算 $20.00，預計月底約 $9.80。」與「調整預算」；依日期的長條圖可以切換分組；明細表格可以篩選與匯出 CSV；有 `notices` 時在頂部顯示。設定的「預算」分區編輯 COST-R4 的四個欄位。 |

## 介面

- API：
  - `GET /api/settings/budget`、`PUT /api/settings/budget`（`admin`，既有的 `settings-get`／`settings-put` 路由加上 `budget` 區段；目前回 501）
  - `GET /api/prices`（`read`）、`PUT /api/prices`、`DELETE /api/prices`、`POST /api/prices/confirm`（`admin`）
  - `GET /api/costs`、`GET /api/costs/entries`、`GET /api/costs/export.csv`（`read`）
- 事件：`budget.warning`（COST-R10）；步驟 `blocked` 的既有事件帶 `block_reason: budget_blocked`。
- 排程工作：`budget.month`（`key` 固定為 `monthly`，ADR-0021 第 1 點的 `scheduled_jobs`）。
- 資料表：`cost_ledger`（既有，00007；`state` 新增使用 `reserved`，`unit_price_json` 依 COST-R1）；新表 `price_overrides (provider, model, unit_type, usd_per_unit, checked_at)`，主鍵 `(provider, model, unit_type)`；`settings` 中的 `budget` 區段與 `prices.builtin_checked_at`。建議加索引 `cost_ledger (at)`、`cost_ledger (state) WHERE state = 'reserved'`。
- 程式：`internal/cost` 的 `Lookup` 先查覆寫再查內建（`prices.go` 開頭的註解）；內建計價表需要補上 LLM 的單價才有意義（目前只有 Groq），補的時候更新 `PricesAsOf`。預留與檢查放在 store 的單一寫入交易，由 worker 在 `engine` 執行任務之前呼叫；`errclass.ClassBudgetBlocked` 與 `engine.go` 既有的 `budget_blocked` 分支沿用，`ResumeOn` 使用 `budget`。
- `pipeline.Stages`、`outputs`：不影響。
- MCP、CLI：M4 不提供（見待決問題 C6）。
- UI：成本頁（第 11 節）、設定的「預算」分區（第 12 節）、總覽的本月花費（LIB-R21）。

## 與其他規格的關係

- DEL-R7：永久刪除後紀錄保留，`item_title` 用於 COST-R14 的 `label` 與 COST-R15 的 `item_title`。
- BATCH：batch 單價（BATCH-R6）與 batch 的預留（BATCH-R7）沿用本規格的帳本與檢查。
- NOTIFY：`budget.warning` 與 `budget_blocked` 的通知由 NOTIFY 送出；本規格只負責寫入事件與 outbox。
- LIB-R14、LIB-R17：批次重跑的 `dry_run` 用 COST-R5 的估算方法，並以 COST-R6 的餘額判斷 `exceeds`。
- ING-R8（`POST /api/estimate`）：估價改用 COST-R2 的單價順序（覆寫優先）；回應格式不變。

## 不在範圍內

- 多幣別：一律美元。
- 依 provider 帳單自動對帳。
- 預算用完時自動切換到便宜的模型。
- 依頻道或處理方案分別設定預算。
- MCP 的成本查詢 tool 與 CLI 子命令（C6）。

## 待決問題

- C1：預算路由用 `PUT`，不是 UI 第 11 節寫的 `PATCH`（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）。既有的 `/api/settings/{section}` 只有 `GET`、`PUT`，語意是取代整個區段（PRF D16），`internal/api/items_test.go` 也已經斷言 `budget` 區段目前回 501；另開 `PATCH` 會讓設定 API 有兩種語意。UI 第 11 節的 `PATCH /api/settings/budget` 要改成 `PUT`。
- C1a：`internal/api/items_test.go` 目前斷言 `PUT /api/settings/budget {"a":1}` 回 501、`GET` 回 501；實作 COST-R4 時要同時改這個單元測試（`{"a":1}` 改成 422「不認識的欄位。」），不是 `.feature`。
- C2：找不到單價時（COST-R7），只要有設定任一種預算就擋下（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）。理由是「花費不會失控」：放行的話，沒有單價的模型可以無限制地花錢。沒有設定預算時照常執行，既有的場景（`fixture-model` 不在計價表中）不受影響。
- C3：單部上限計算這部影音的所有紀錄，不分月份、包括重跑（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）。要重跑已經到上限的影音，先調高上限。
- C4：輸出 token 估為字幕資料區塊的 1.2 倍（COST-R5，2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）。`segment`、`correct` 的輸出和輸入差不多長；`translate` 到 CJK 時 token 數通常比英文少，1.2 倍偏保守。實際值和預留的差異由 COST-R5 改成實際金額時吸收，不另外調整；M4 之後可以依帳本的實際比例校正。
- C5：`budget.warning` 每月只寫一次，調高預算不會重設（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）。避免反覆調整預算時重複通知；NOTIFY 的 `dedupe_key` 也是以月份為單位。
- C6：M4 不提供成本的 MCP tool 與 CLI 子命令（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）。ADR-0008 要求能力一致，但故事地圖 COST 的驗收只提到 UI；`notesteme costs` 之後再補。
- C7：排程工作 `budget.month` 是新的 `kind`（ADR-0021 第 1 點列的是例子，沒有窮舉）。已補進 ADR-0021 第 1 點的清單。
- C8：驗收場景用兩個測試控制讓金額可以預測（ADR-0015）：`the LLM usage reported by …`（fakellm 回報固定的 token 數）與 `the cost estimate of every … task is …`（取代估算的結果，只影響預留的金額，不影響 COST-R5 的估算方法本身，估算方法由單元測試驗證）。
- C10：金額以整數 micro-USD 計算（規則表上方的說明，2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）。`cost_ledger.usd` 維持 00007 的 REAL 欄位，只在寫入與加總時四捨五入；之後要改成整數欄位再另開 migration。驗收場景的金額比對（`Then the response contains:`）是浮點數完全相等，不這樣做的話 `0.10 + 0.20` 會失敗。
- C9：provider 回報的金額只認 OpenAI 相容回應的 `usage.cost`（OpenRouter 的格式）；Anthropic 與 OpenAI 官方 API 目前不回報金額。其他格式之後再加。

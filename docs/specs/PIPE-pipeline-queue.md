# PIPE：Pipeline 與任務佇列

- 狀態：ready
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
| PIPE-R14 | 步驟狀態改變時，透過 SSE（`GET /api/events`）推送事件。事件型別：`item.created`（`type`、`item`、`item_id`）、`stage.status`（另加 `stage`、`status`）。 |
| PIPE-R15 | 可以取消某部影音的處理；取消後，執行中的子程序整組結束，步驟為 `skipped`（原因 `cancelled`）。 |

## 介面

- API：`GET /api/queue`、`PATCH /api/queue/limits`、`POST /api/queue/pause`、`POST /api/queue/resume`、`POST /api/tasks/:id/retry`、`POST /api/tasks/:id/cancel`、`POST /api/items/:id/cancel`、`POST /api/items/:id/rerun`、`GET /api/items/:id/runs`、`GET /api/events`（SSE）
  - `PATCH /api/queue/limits`：body 是 `{ "<資源類別>": <上限> }`，只改有給的類別；上限至少 1，不認識的類別回 422。
  - `POST /api/queue/pause`、`POST /api/queue/resume`：沒有 body 時作用在整個佇列；`{ "class": "asr" }` 只作用在該資源類別。
  - `POST /api/items/:id/cancel`：取消該影音所有未結束的步驟（PIPE-R15）；`POST /api/tasks/:id/cancel` 只取消單一任務。
- CLI：`notesteme items ls --status <stage>=<status>`、`notesteme items rerun <item> --from <stage>`
- `blocked` 的恢復（PIPE-R4）：
  - 步驟因為需要使用者補上某個東西而 `blocked` 時，記錄恢復條件 `resume_on`，格式是 `<種類>:<名稱>`，目前只有 `cookie:youtube` 與 `cookie:bilibili`（`errclass.Error.ResumeOn`）。沒有 `resume_on` 的 `blocked`（例如缺 API key）不會自動恢復，要手動重試。`resume_on` 只在步驟是 `blocked` 時保留，離開 `blocked` 就清空。
  - `POST /api/cookies` 儲存成功後，以 `cookie:<platform>` 呼叫 `ResumeBlocked`：在同一個交易中，找出沒有刪除的影音裡每個（影音、步驟）最新一筆、`resume_on` 相符的 `blocked` 步驟，改回 `pending`，並以原本任務的優先順序與資源類別建立新任務，嘗試次數從 0 開始。
  - 執行中補上條件：步驟在條件滿足之前開始執行、執行期間使用者才補上（例如 yt-dlp 沒帶 cookie 開始跑，這時上傳 cookie），`ResumeBlocked` 找不到 `running` 的步驟（回 0）。步驟之後回報需要 cookie 時，寫回結果的同一個交易會先檢查：`resume_on` 是 `cookie:<platform>` 時，讀 secrets 表 `(cookie, <platform>)` 的 `updated_at`；它**嚴格晚於**這次執行的開始時間（以毫秒比較，開始時間在 `Handle` 開頭用注入的 clock 記錄）就改為 requeue，不寫 `blocked`：步驟設 `pending`、原任務 `failed`、以原任務的優先順序與資源類別建立新任務，不計入嘗試次數。開始當下就存在的 cookie（例如過期被拒絕）不算，所以仍然 `blocked`，不會無限重跑。token 的解析在 `resumeSecret`，之後有新種類的 `resume_on` 時在那裡擴充。
  - `POST /api/cookies` 呼叫 `ResumeBlocked` 時使用 `context.WithoutCancel` 加 30 秒逾時，客戶端在 cookie 儲存後斷線不會中斷恢復。
  - 恢復是冪等的：恢復後步驟已不是 `blocked`，再呼叫一次不會重複建立任務；token 不符、已刪除的影音都不恢復。恢復失敗不影響 cookie 已經儲存的事實（API 仍回 200，錯誤記在 log）。

## 不在範圍內

- LLM 的 chunk 任務與驗證重試（M2，見 LLM）。
- `agent` executor 與租約延長（M2，見 AGT）。
- 預算（M4）。

## 待決問題

- 退避的時間與次數是否依資源類別區分（例如 `download` 遇到 429 時等更久），M1 實作時觀察實際的 rate limit。
- aging 的級距：手動與回補只差 2 級，依 PIPE-R9 每 10 分鐘提高 1 級，回補等 20 分鐘就會排在新的手動新增前面。M1 先照規則實作；之後依實際使用調整級距或 aging 速度，但差距不能超過 17 級，否則「等 3 小時的回補排在前面」的場景不成立（2026-11-13，PIPE 切片設計時發現）。
- `transcript` 的資源類別要在建立任務時決定，但這時還不知道平台字幕是否可用。已採用的暫定規則：`metadata` 完成、建立 `transcript` 任務時，`fallback` 的第一個來源是 ASR（目前只有 groq），且剛取得的平台字幕軌都不符合 `caption_priority` 時歸為 `asr`，否則歸為 `download`。從 `transcript` 重跑時不知道字幕軌，一律歸為 `download`。這是推定：歸為 `asr` 的任務之後可能因為平台字幕出現（例如等待自動字幕）而不需要 ASR，歸為 `download` 的任務在 fallback 到 groq 時仍佔用 `download` 的名額，不受 `asr` 的並行上限與暫停控制。長期可以把下載音訊與 ASR 拆成兩個任務（2026-11-14，SRC 切片實作）。
- 執行期回收過期租約延到 AGT：同一份 `/data` 只有一個 `serve`，內建任務也不延長租約，執行期回收會讓超過租約時間的 ASR 任務被重複執行。M1 只在啟動時回收（PIPE-R11）。
- PIPE-R7、R13 的場景原本驗證不到規則（2026-11-13，PIPE 切片審查以變異測試發現），經 owner 同意已修改：R7 改以 `normalize` 的實作版本累積 4 個版本，斷言只剩 3 版（Supadata 的 `mode` 只有三種，湊不出 4 個不同的快取鍵）；R13 改為斷言覆寫獨有的 `targets: [en]`。
- PIPE-R7 的清除另外保留「被其他產出當作來源（parent）的版本」，避免來源紀錄指向已刪除的產出；有衍生產出時，同一組可能超過 3 版。被清除版本的 stage run 不再指向產出，之後視為快取未命中。
- transcript 資源類別的兩個方向都有場景：`asr` 暫停時，需要 ASR 的 transcript 會等待；有平台字幕時，transcript 照常執行（後者是 2026-11-14 經 owner 同意新增的場景）。
- 「執行中補上條件」的限制（2026-11-14，SRC-B 審查）：secrets 的 `updated_at` 只有毫秒精度，與開始時間在同一毫秒內上傳的 cookie 判斷為「不晚於」，仍會 `blocked`，使用者要手動重試；這個視窗極小，換取開始當下已存在的 cookie 不會被誤判。其他種類的 `resume_on` 目前沒有，之後若有，要先決定各自的「何時滿足」依據。
- Bilibili 沒有語言中繼資料：`metadata` 判斷 `transcript` 的資源類別時，與 `transcript` 步驟選軌共用同一個推斷（`inferOriginalLang`：沒有原文語言時用 AI 字幕軌的語言），否則預設的 `caption_priority`（original）會因原文語言為空而誤歸為 `asr`（2026-11-14，SRC-B 審查）。
- 步驟的執行方式在「前進到該步驟」時，依該 stage run 的設定快照解析（`ResolveExecutor`），不是在新增時決定（2026-11-21，M2 S0 切片設計時決定）。可以略過的步驟（`executor: off`、`api` 沒有 provider 或 model、`publish` 沒有輸出目標）直接標成 `skipped`，不建任務、不帶 message，並在同一個交易中繼續解析下一步；`until` 之後的步驟維持 `pending`。skipped 的原因寫在 `block_reason`（`cancelled`、`off`、`not_configured`、`no_outputs`），事件仍是 `stage.status`，不另外帶原因。skipped 不帶 message，是為了不蓋掉 SRC-R15「時間可能偏移」這類 `done` 步驟上的提醒。
- 重跑時，若這次要直接建任務的第一個步驟還沒有實作，就回 501，不寫入任何資料；前進時才遇到沒有實作的 executor（例如單次覆寫指定了 provider），任務會以 `permanent` 失敗，訊息是「這個步驟還沒有實作（<步驟>／<executor>）。」。M2 的步驟陸續完成後，這條規則自然失效（2026-11-21，M2 S0 切片設計時決定）。
- `translate` 在 S0 只有一筆 stage run（`lang` 為空）。依目標語言各一筆的設計屬於 TRN 切片：`latestRuns` 目前只以步驟名稱為 key，詳情與前進要先決定怎麼處理多個語言（2026-11-21，M2 S0 切片設計時發現）。
- D6：快照的頻道層（2026-11-22，M2 S1/S2 設計時決定，待 owner 確認；細節見 PRF 的 D6）：快照記下組成它的各層（`config_snapshots.layers_json`）。`metadata` 完成而且影音的頻道確定之後，若與快照記下的頻道不同，就重建快照，並把同一批還沒開始（`pending`、沒有 `started_at`）的下游 stage run 改綁到新快照；已經開始的步驟與 M1 的舊快照不動。所以同一批 stage run 不一定共用同一份快照，`metadata` 的快照不含頻道層。快照的 sha256 改為對 config 與 layers 一起計算，`config_json` 本身在沒有頻道覆寫時與 S0 相同。
- `translate` 的自動 opencc 與 `same_language`（2026-11-22，M2 S6a 實作時決定，待 owner 確認）。`ResolveExecutor(eff, stage, StageContext)` 多一個上游字幕語言參數（`StageContext.SourceLang`：translate 輸入字幕的語言，不是 `items.original_lang`；沒有字幕時退回 `original_lang`）。`translate` 依序判斷：`off` → 略過（`off`）；每個目標都與原文相同（或只差字形而沒有轉換表）→ 略過（`same_language`）；其餘目標全是 convert → `opencc`（不論設定的是 api 或 agent，不需要 provider）；其他情況照原本規則。`skipped` 的原因因此多了 `same_language`。預設設定下，中文原文的影音（例如 Bilibili zh-Hans）走完整條 pipeline 時，`translate` 從 `skipped（not_configured）` 變成跑一個本機的 opencc 任務並 `done`，並多一份 `translated`／`zh-Hant-TW` 產出。
- `translate` 在 S6a 仍只有一筆 stage run，一個 stage run 可以產生多份 `translated` 產出（每個目標語言一份）；依語言各一筆的設計留給 TRN 的 S6b（2026-11-22，M2 S6a 實作時決定，待 owner 確認；見 TRN 的 S6-Q4）。
- Agent 的租約與 PIPE-R11、R12（2026-11-23，M2 S10 實作時決定，依 /goal 採保守預設，待 owner 追認）：重新啟動時放回租約、正常關閉時主動釋放租約，都只作用在內建 worker 的任務（executor 不是 `agent`）。Agent 的租約屬於 Agent，重新啟動後仍然有效；到期時由 AGT 的 sweeper 放回並計入 attempts（AGT-R4，見 AGT 規格 A7）。內建 worker 的任務在執行期間到期仍只在啟動時回收（原本的限制不變）。
- M3-S0：`pipeline.Stages` 加入 `sponsorblock`、`chapters`、`frames`、`note`（ADR-0020 第 1 點）（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）。設定要求執行而還沒有實作的 M3 步驟，在 `planChain` 改成 `skipped`（新的暫時原因 `not_implemented`），不建任務，鏈繼續往下，M1、M2 的影音照常做到 `publish`；`ResolveExecutor` 照規格回報執行方式，各切片在 `stageImplemented` 登記後就會執行。替代方案：建立任務並在 `Handle` 失敗（M2 S0 的做法），但預設開啟的 `sponsorblock` 會讓每部 YouTube 影音失敗，308 個場景退步。從還沒有實作的 M3 步驟重跑回 501（`StageAvailable`），不留下半套的重跑。M3 之前建立的影音沒有這四個步驟的 stage run：前進時跳過不存在的步驟（`planChainFor`），`translate` 完成後直接前進到 `publish`，不補建。

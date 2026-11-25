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
| PIPE-R16 | 步驟成為 `failed` 或 `blocked` 時，保存讓它停下的那次執行最後最多 20 行 log（每行最多 500 bytes），寫入之前遮蔽 secret；`GET /api/items/:id` 該步驟帶 `log_excerpt`，Web UI 的步驟面板可以展開、複製這段「log 片段」，不需要進容器看 log（NFR-OBS-2）。其他狀態不帶。 |

## 介面

- API：`GET /api/queue`、`PATCH /api/queue/limits`、`POST /api/queue/pause`、`POST /api/queue/resume`、`POST /api/tasks/:id/retry`、`POST /api/tasks/:id/cancel`、`POST /api/items/:id/cancel`、`POST /api/items/:id/rerun`、`GET /api/items/:id/runs`、`GET /api/events`（SSE）
  - `PATCH /api/queue/limits`：body 是 `{ "<資源類別>": <上限> }`，只改有給的類別；上限至少 1，不認識的類別回 422。
  - `POST /api/queue/pause`、`POST /api/queue/resume`：沒有 body 時作用在整個佇列；`{ "class": "asr" }` 只作用在該資源類別。
  - `POST /api/items/:id/cancel`：取消該影音所有未結束的步驟（PIPE-R15）；`POST /api/tasks/:id/cancel` 只取消單一任務。
- CLI：`notesteme items ls --status <stage>=<status>`、`notesteme items rerun <item> --from <stage>`
- log 片段（PIPE-R16）：
  - 來源：任務每次執行時，log handler（`internal/logctx`）把這次執行輸出的 log（通過 level 過濾的）另外記在記憶體中，只保留最後 20 行；每行是 slog 的 text 格式，不含 `request_id`、`task`、`item`、`stage`（片段本來就屬於這個步驟），超過 500 bytes 的截斷並以「…」結尾（不切斷 UTF-8 字元）。步驟自己回報停下、沒有錯誤時另記一行「步驟停下」，片段不會是空的。
  - 遮蔽：已知的 secret（環境變數中像 secret 的值、資料庫中所有 secret 的明文，與 BND-R2 的洩漏掃描同一份清單）換成 `[已遮蔽]`；另外遮蔽 `Bearer <token>` 與 `api_key=…`、`token: …`、`password=…`、`cookie=…` 等寫法的值。片段寫進資料庫（也就會進備份），所以在寫入之前遮蔽；讀不到 secret 時不保存片段。
  - 保存：`stage_runs.log_excerpt`（migration 00035，可為 NULL）；寫入步驟狀態時一併寫入，只有 `failed` 與 `blocked` 有值。API 只在步驟是 `failed` 或 `blocked` 時輸出 `log_excerpt`（以換行分隔；沒有時省略）。
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
- M4-S4：同分任務的領取順序（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）。`ClaimTasks` 與 `ListQueueTasks` 的排序在 `score DESC, created_at` 之後再加 `stage_run_id, rowid`（Go 端的重新排序相同）。同一個交易中建立的任務 `created_at` 相同，原本的順序由 SQLite 的掃描順序決定；平行的 LLM 步驟同時完成規劃時，哪一部影音先領到 chunk 任務不固定，COST 的「平行任務不會一起超支」場景因此時好時壞。stage run 的 ID 是時間排序的 UUIDv7，較早建立的步驟先執行，與 PIPE-R9 的意圖一致。
- 單一任務的重試與取消（2026-11-24，收尾 D1 實作時決定，依 /goal 採 advisor 建議，待 owner 追認）：
  - 操作的單位是任務所屬的 stage run：LLM 步驟一筆 stage run 底下有 plan、chunk、assemble 等多個任務，取消或重試其中一個就是對整個步驟。只能操作該步驟最新的一筆；被重跑取代的舊步驟回 409。影音在垃圾桶中回 404 `in_trash`，任務不存在回 404 `not_found`（「找不到這個任務。」）。scope 是 `admin`（沿用 stub），回應是影音的最新詳情（同 `POST /api/items/:id/cancel`）。
  - `POST /api/tasks/:id/cancel`：在一個交易中把步驟改為 `skipped`（原因 `cancelled`），同一個步驟所有未結束（pending、leased、等待 batch）的任務改為 `cancelled` 並清掉租約（Agent 的舊 token 之後回 `lease_expired`），再請 worker 結束執行中的（子程序整組結束），等它們結束才回應。**其他步驟不動**：下游還沒有任務，維持 `pending`，之後重試這個步驟就會照常前進。步驟已經是被取消的 `skipped` 時回 200 且什麼都不做（冪等）；已經結束（`done`、`failed`、其他原因的 `skipped`）回 409 `task_not_cancellable`。`blocked` 算未結束，可以取消。
  - `POST /api/tasks/:id/retry`（PIPE-R4 的手動重試）：可以重試的是 `failed`、`blocked`（不論原因與有沒有 `resume_on`）、以及原因是 `cancelled` 的 `skipped`；其他狀態與其他原因的 `skipped`（`off`、`not_configured`、`no_outputs`、`same_language`，重試也一樣會略過）回 409 `task_not_retryable`。步驟還有未結束的任務時（例如晚到的 chunk 還在執行，或已經重試過一次在等待）也回 409，避免雙擊建立兩組任務。
  - 重試沿用同一筆 stage run 與設定快照，不產生新版本：步驟改回 `pending`（清掉說明、動作、`resume_on`），以原任務的優先順序與資源類別建立新任務（transcript 歸為 `asr` 的推定不重新判斷），嘗試次數從 0 開始，作法同 `ResumeBlocked`。LLM 步驟沿用這筆 stage run 已有的 chunk，已有結果的不再送出（不重複付費）。同一部影音在它之後、被取消的步驟（影音層級的取消會把後面的步驟都改成 `skipped`／`cancelled`）改回 `pending`，完成後照常前進。
  - 事件：取消與重試都經由 `SetStageState` 發布 `stage.status`（`skipped`、`pending`），沒有另外的事件型別。
  - `GET /api/queue` 多一個 `failed` 清單給佇列頁的重試按鈕：最新一筆是 `failed`、`blocked` 或被取消、沒有未結束任務、而且有任務的步驟（附最新任務的 ID），由新到舊最多 200 筆；垃圾桶中的影音與沒有任務的步驟（例如影音取消後的下游、`awaiting_user`）不列出。migration 00033 加了被取消步驟的部分索引。
  - MCP 與 CLI 的規格沒有對應的 tool 與子命令，這次不新增。
- NFR-REL-2「讀取時比對 sha256，不符就標記損毀並重跑該步驟」的實作（2026-11-24，收尾 D1 實作時決定，依 /goal 採 advisor 建議，待 owner 追認）：
  - **比對的時機**：（1）快取命中（`DoneStageByCacheKey`，transcript、normalize、LLM 步驟的步驟層快取）要重用某份產出時，重新計算整個檔案的 sha256（先比大小）；（2）讀取字幕產出時（下游步驟的輸入：normalize、LLM 步驟、translate；API 的 `/cues`、`/subtitles`、雙語的原文、試跑）——`readTranscript` 本來就讀進整個檔案，雜湊的成本可以忽略。**不在每個請求檢查其他大檔**：本機媒體串流仍依大小＋修改時間決定 ETag（LIB 待決問題），frames 的圖片、筆記、摘要、章節維持原本「讀到不符時回報 `ErrCorruptArtifact`」，不自動重跑（已知限制）。檔案不見也視為損毀；權限等其他讀取錯誤原樣回傳，不重跑付費的步驟。
  - **標記**：`artifacts.corrupt_at`（migration 00034）。標記用條件式更新，只有第一個標記成功的呼叫端觸發重跑；並行或之後讀到同一份壞檔只回報錯誤，不重複重跑，也不會「偵測、重跑、再偵測」地循環。標記過的產出 SQL 不再當成快取命中（`DoneStageByCacheKey` 排除），Go 端的檔案檢查是第二層（從未被讀到的壞檔在重用時才發現）。
  - **重跑**：快取命中時發現的，就是這個步驟自己重新產生（當成沒有命中）。讀取時發現的，從產生它的步驟（來源紀錄的 `stage`）以 `Rerun` 重新處理：沿用影音的 `until`，設定依影音目前的處理方案與覆寫（和使用者手動重跑相同，可能與當初的快照不同），優先順序是手動。損毀的產出要仍是現行版本才重跑。下游步驟讀到損毀的輸入時，這次執行以 permanent 結束，但它已被重跑取代，結果會被丟棄。匯入、編輯（`stage` 不是 pipeline 的步驟）與從 bundle、Hub 播種（`seeded`）的產出不自動重跑，只標記並回報。
  - **成本**：重跑走一般的流程。LLM 步驟的 chunk 層快取（`llm_chunks`，不依賴產出檔）照常重用，已付費的 chunk 不會再呼叫 provider、不新增花費；ASR、下載等其他付費步驟會重新呼叫一次（重新產生壞掉的結果本來就要付費，預算檢查照常）。
  - API 讀到損毀的產出回 409 `artifact_corrupt`，訊息說明已重新處理哪個步驟（或無法自動重新產生）。
- log 片段的來源（2026-11-25，依 /goal 由實作者暫定，待 owner 追認；PIPE-R16、NFR-OBS-2）：採用「每次任務執行一個有上限的記憶體緩衝」，由 log handler 在輸出時同步寫入，不是讀 log 檔或全域的環形緩衝。理由：log 輸出到 stdout，程式不一定能讀回；全域緩衝要再依任務過濾，並行時其他任務的 log 會把這個任務的擠掉。代價是只有「這次執行」的 log：retryable 用盡後的片段是最後一次嘗試的；LLM 步驟由某個 chunk 讓步驟停下時，片段是那個 chunk 任務的。`budget_blocked` 在建立任務前判定的、Agent 執行逾時改成 `blocked` 的（沒有任務在執行）沒有片段，UI 只顯示錯誤類別、訊息與修正動作。
- NFR-REL-1～3 的故障注入（2026-11-25，依 /goal 由實作者暫定，待 owner 追認）：
  - **故障點**：`internal/faultpoint` 的 `Hit(點, 範圍)`，只在 `-tags faultinject` 建置有作用，一般建置是空函式、被內聯掉，正式 binary 沒有任何 faultpoint 的符號（`internal/faultpoint/faultpoint_test.go` 以 `go list` 與 `go tool nm` 驗證）。點的位置：每個任務的開始（`stage.start`，讀到步驟之後）、中間（`stage.mid`，步驟的工作做完、外部呼叫已回來、產出檔已寫出，結果還沒寫回）、提交前（`stage.commit`，寫回的交易做完所有寫入、還沒 commit）；產出檔的暫存檔寫完並 fsync、還沒 rename（`artifact.tmp-written`），rename 並 fsync 目錄之後、DB 記錄之前（`artifact.renamed`）；付費呼叫回來、結果還沒落地（`paid.returned`，範圍是 `groq`、`supadata` 或 `llm.<步驟>`），以及 LLM chunk 的結果已經 commit、步驟還要等 assemble 才標記完成（`paid.landed`，範圍 `llm.<步驟>`）。
  - LLM 步驟一筆 stage run 有 plan、chunk、assemble 多個任務，`stage.*` 每個任務都會命中一次，故障注入輪流在第 1、2、3 次命中時結束程序。
  - 字幕來源（ASR、Supadata）的結果與「步驟完成」在同一個交易中寫入，沒有「已落地、還沒標記完成」的中間狀態，所以只有 `paid.returned`；這個點被殺時，重新啟動最多再呼叫一次（還沒落地的結果本來就沒有拿到）。
  - 測試在 `features/fault`（見 `docs/specs/nfr.md` 第 11 節）。
- 產出暫存檔的清理（2026-11-25，故障注入發現，依 /goal 由實作者暫定，待 owner 追認）：在暫存檔寫完、rename 之前被 `kill -9`，`artifacts/<item>/.tmp-*` 會永遠留著（PIPE-R11 只清 `/data/tmp`）。啟動時、取得資料目錄的鎖之後、worker 開始之前，以 `pipeline.CleanArtifactTemps` 刪掉 `artifacts/` 第一層目錄中的 `.tmp-*` 一般檔案（只有 `writeArtifactFile` 用這個前綴；不跟隨 symlink，frames 的子目錄不處理）。rename 之後、DB 記錄之前被殺留下的正式檔**不刪**：沒有記錄指向它，不影響正確性，只多佔空間；要刪就得判斷它真的沒有被任何記錄引用，風險比留著大（已知限制，故障注入每次都統計它的數量）。
- 斷電與 `synchronous`（2026-11-25，依 /goal 由實作者暫定，待 owner 追認）：
  - DB 是 WAL＋`synchronous(NORMAL)`（`internal/sqlitedb`）。程序被 `kill -9` 時，已 commit 的交易都在 WAL 中，不會遺失；OS 當機或斷電時，**DB 不會損毀**，但最後一次 WAL fsync（checkpoint）之後 commit 的交易可能遺失，重新啟動後 DB 回到較早的一致狀態。
  - 冪等性的評估：遺失的如果是一般步驟的寫回，重新啟動後依 PIPE-R11 把步驟放回 `pending` 重跑。每個步驟都以 DB 中的上游產出為輸入、寫出新的產出（新的 ID），快取鍵相同就重用；重跑的結果與第一次相同，最多多一份沒有記錄指向的產出檔（同上，已知限制）。遺失的如果是使用者剛做的修改（設定、筆記、刪除…），使用者要重做一次；這不違反 NFR-REL-1 的「DB 不會損毀」，但要寫在 NFR 的解讀中。
  - **付費結果不能這樣遺失**：記錄付費結果的交易遺失，重新啟動後重跑就會再付一次錢，違反 NFR-REL-3。所以這些交易改用 `Store.TxDurable`：在獨占的寫入連線上以 `synchronous=FULL` commit，commit 回來時 WAL 已經 fsync，連同 WAL 中在它之前的交易都不會因斷電遺失。用在：付費字幕來源（Supadata、ASR）的寫回、`api` executor 的任務（LLM 的 plan、chunk、assemble）、Agent 的提交、Batch API 的結果。之後斷電只可能遺失它之後的一般交易，依上一點重跑即可。
  - 為什麼不把整個 DB 改成 `FULL`：`ref-wsl` 上每次 commit 從約 0.05ms 變成約 7ms（約 130 倍），一般寫入（session、設定、事件）全部變慢，完整的 `features/` 也會超過 NFR-TEST-2 的 5 分鐘。只對付費結果 fsync，代價是 `internal/pipeline` 的單元測試從約 122 秒變成約 142 秒。Hub 共用 `sqlitedb.Open`，不受影響（Hub 不跑 pipeline）。
  - 斷電無法在 CI 實測，列在[人工驗收清單](../manual-acceptance.md#11-nfr-rel-1斷電後-db-不損毀付費結果不遺失)。

# PRM：Prompt 庫

- 狀態：draft
- 里程碑：M2
- 相關：plan 第 8.3 節；UI [Prompt 庫](../ui/pages.md#7-prompt-庫)
- 驗收場景：[`features/PRM-prompts.feature`](../../features/PRM-prompts.feature)

## 目的

校對只靠 LLM，所以 prompt 是改善結果最主要的地方。prompt 要有版本、能試跑、能比較，而且改版不會悄悄影響正在使用它的處理方案。

## User stories

| ID | Story |
|---|---|
| PRM-01 | As the owner, I want every prompt change saved as a new version, so that I can always go back and know which version produced a result. |
| PRM-02 | As the owner, I want to try a prompt on part of a real item before using it, so that I can judge it without changing any results or paying for a full run. |
| PRM-03 | As the owner, I want profiles to keep using the version they were set to until I promote a new one, so that experiments never change my everyday output. |
| PRM-04 | As the owner, I want mistakes in prompt variables caught when I save, so that a typo never breaks a run later. |

## 規則

| ID | 規則 |
|---|---|
| PRM-R1 | prompt 的類型：`summary`、`chapters`、`segment`、`correct`、`translate`（包含 `localize-*`）、`frames.select`、`note`。 |
| PRM-R2 | prompt 版本建立後不能修改；儲存變更一律建立新版本，版本號遞增，並計算 `sha256`。 |
| PRM-R3 | 可用的變數：`title`、`channel`、`description`、`chapters`、`glossary`、`source_lang`、`target_lang`。儲存時發現未定義的變數就回傳 422，並列出變數名稱。 |
| PRM-R4 | 內建 prompt（`segment-default`、`correct-default`、`translate-default`、`localize-zh-hant-tw`、`localize-zh-hans-cn`）不能修改，只能另存為新的 prompt。 |
| PRM-R5 | 處理方案引用 prompt 時記錄版本號。prompt 有新版本時，處理方案不會自動改用；「設為預設」才會更新預設版本，引用 `default` 的處理方案從下一次執行開始改用。 |
| PRM-R6 | 試跑對指定影音的指定時間範圍執行 prompt，回傳輸出、驗證結果與預估花費；不寫入任何產出，也不改變步驟狀態。 |
| PRM-R7 | 比較兩個版本時，回傳 prompt 內容的差異；指定影音與範圍時，另外回傳兩者在同一段字幕上的輸出。 |
| PRM-R8 | 刪除 prompt 時，如果有處理方案正在使用，回傳 409 並列出這些處理方案。 |

## 介面

- API：`GET/POST /api/prompts`、`DELETE /api/prompts/:id`（PRM-R8）、`GET /api/prompts/:id/versions`、`POST /api/prompts/:id/versions`、`POST /api/prompts/:id/default`、`POST /api/prompts/playground`、`POST /api/prompts/:id/compare`
- MCP：prompts capability（見 MCP）
- 試跑與比較（scope `ingest`，會花錢）：
  - `POST /api/prompts/playground`：body `{prompt, item, from, to, provider, model}`；`prompt` 是 `<id>`、`<id>@default` 或 `<id>@N`，`item` 是影音 ID 或短代號，`from`／`to` 是 `HH:MM:SS` 或 `MM:SS`（可帶小數，範圍是開始時間在 `[from, to)` 的句子）。回應 `{stage, prompt:{id,version,sha256}, cues, output:[{index,start_ms,end_ms,text,original?}], validation:{passed,errors[]}, usage, estimate_usd}`。
  - `POST /api/prompts/:id/compare`：body `{a, b, item?, from?, to?, provider?, model?}`；回應 `{a, b, body_diff:[{op: equal|delete|insert, text}], outputs?:{a, b}}`。`outputs` 只在帶 `item` 時才有（需要 `from`、`to`、`provider`、`model`），是兩次試跑，**花費是試跑的兩倍**。

## 不在範圍內

- `summary`、`chapters`、`frames.select`、`note` 的內建 prompt（M3、M4 隨各步驟提供）。

## 待決問題

- D11：prompt 引用格式是 `<id>`、`<id>@default`、`<id>@<N>`（N ≥ 1）（2026-11-22，M2 S1/S2 設計時決定）。`<id>` 等同 `<id>@default`，跟隨「設為預設」；`@N` 固定版本。步驟與 prompt 的類型必須相符（segment、correct、translate；`localize-*` 屬於 translate）。版本在新增或重跑取快照時解析，只解析會執行的 LLM 步驟，寫進快照該步驟的 `prompt_version`、`prompt_sha256`；解析失敗時留空，不讓新增失敗，由執行該步驟時以 `permanent` 失敗。
- D12：內建 prompt（PRM-R4 的 5 個）放在程式內（`internal/prompts/builtin/<id>/v<N>.md`，embed），不寫入資料庫（2026-11-22，M2 S1/S2 設計時決定）。使用者的 prompt 存在資料庫，id 不可以和內建的相同。內建的不能新增版本、不能設預設、不能刪除，都回 409 `builtin_prompt`。M2 發布前可以直接修改 v1 的內容，發布之後改內容要新增 v2。
- D13：刪除 prompt 是硬刪除（2026-11-22，M2 S1/S2 設計時決定，待 owner 確認）。`used_by` 依序列出引用它的處理方案名稱（依字母排序，只看目前版本）、`settings`（全域 stages 有引用時）、`channel:<platform>:<external_id>`、`item:<影音代號>`（影音層的覆寫，P7）。PRM-R8 只要求處理方案；多列出其他來源是為了避免懸空的引用，場景的 `used_by: ["standard"]` 是前綴比對，仍然成立。
- P1：PRM-R7 的 compare 請求 body 帶 `provider` 與 `model`，與 playground 一致（2026-11-22，owner 同意）。影音用的是 `default` 處理方案（沒有 provider），compare 無法從快照取得；場景的 body 已加上 `"provider": "anthropic", "model": "fixture-model"`。
- P6：不帶版本號的 prompt 引用（例如 `correct-default`）跟隨預設，`<id>` 等同 `<id>@default`（2026-11-22，owner 同意）。依 PRM-03「until I promote」；實際用到的版本記在快照與來源紀錄（見 D11）。
- P7：刪除 prompt 的「使用中」檢查涵蓋影音層的覆寫，而且檢查與刪除在同一個交易（2026-11-22 審查時發現，owner 先接受；2026-11-23 M2 收尾補做，依 /goal 採保守預設，待 owner 追認）。原本的缺口：新增時帶的 `overrides`（存在 `items.overrides_json`，重跑會沿用）可以引用 prompt，刪除後重跑會在執行時以 permanent 失敗；`used_by` 的檢查走唯讀連線，與刪除不在同一個交易，檢查之後才建立的引用也會懸空。決定：
  - **同一個寫入交易**：`Service.Delete` 在單一 `store.Tx` 內「讀取使用者清單 → 有人用就回 409 `prompt_in_use` → 沒人用才刪除」。寫入只有一條連線，交易持有它時，其他寫入（處理方案、全域設定、頻道、影音新增引用）要等交易結束，所以檢查之後、刪除之前不會再冒出新的引用。掃描在傳進來的交易 Repo 上做（`Deps.UsedBy` 的簽章多一個 `*store.Repo`，app 接到 `Pipeline.PromptUsersIn`）；`GET` 類的「使用中」資訊仍用 `Pipeline.PromptUsers`（唯讀連線），行為不變。
  - **掃描 `items.overrides_json`**：SQL 先以 `overrides_json LIKE '%"prompt"%'` 與 id 子字串篩出可能的列，程式再解析 JSON，確認 `stages.<s>.prompt`（`<id>`、`<id>@default`、`<id>@N`）真的引用它。`used_by` 以 `item:<影音代號>`（例如 `item:yt:abc123xxxxx`）表示，順序是處理方案、`settings`、頻道、影音（依建立順序）。最多列 20 個影音，更多時最後附一筆 `…（另有 N 個影音）`，不把整個媒體庫塞進錯誤訊息。
  - **垃圾桶中的影音算使用中**：DEL-R4／還原規格沒有說垃圾桶中的影音不再需要它的設定，還原後重跑會沿用 `overrides_json`，所以算；影音永久刪除（含到期清理）之後才不算。代價是垃圾桶中有影音引用時刪不掉 prompt，要先還原改設定或永久刪除那部影音。
  - **仍有的窗口（已知限制）**：寫入端（`POST/PATCH /api/profiles`、`PUT /api/settings/stages`、頻道與影音的 overrides）是在自己的交易**之外**驗證 prompt 存在，所以「寫入端剛驗證完、刪除交易接著提交、寫入端才提交」仍會留下一個懸空引用；這和 D13 一樣以執行時 permanent 失敗（訊息指出找不到 prompt）收尾。要完全關閉，寫入端要在自己的交易內重新驗證 prompt 引用，排在之後的切片。
- P8：頻道層在 `metadata` 完成時改綁快照（PRF D6），處理方案、全域各層取自新增時記下的內容，但 `<id>` 與 `<id>@default` 的 prompt 引用會以改綁當下的預設版本重新解析（2026-11-22，M2 S1/S2 審查時發現，待 owner 確認）。PRM-R5 只說「從下一次執行開始」，這些步驟還沒開始，所以不算違規，但和「各層在新增時凍結」不一致。
- P9：PRM-R6 的場景修改（2026-11-23，M2 PRM 試跑切片，依 /goal 採保守預設，待 owner 追認）：`A playground run changes nothing` 原本用預設處理方案（沒有 provider）處理到 `correct`，`correct` 會是 `skipped`，後面的「corrected 有 1 個版本」「stage correct 是 done」不可能成立。場景改成先建立 `standard` 處理方案（`stages.correct` 用 api、anthropic、fixture-model、`my-correct@1`）並設為預設，`answers` 移到 `processed up to` 之前（與 PRF 場景相同），都是 `steps.md` 既有的句型。
- P10：試跑的實作決定（2026-11-23，同上，待 owner 追認）：
  - 要跑哪個步驟由 prompt 的類型決定（`localize-*` 屬於 translate）：類型不是 segment、correct、translate 回 422；是 LLM 步驟但還不能試跑回 501：沒有登記在 `llmStages`，或不是「一對一 cue」的通用流程（segment 用 `assemble` 自己合併，translate 由 `runTranslateLLM` 處理多個目標與句群、沒有 `apply`／`fallback`）；這兩個步驟的試跑要等各自的切片補上。一對一的步驟登記後不用改試跑。
  - 輸入是影音現行的上游字幕（COR-R8 的順序：從這個步驟之前找 `segmented`，其次 `normalized`），沒有字幕回 422；範圍內沒有句子回 422；範圍內最多 200 句（`MaxPlaygroundCues`），超過回 422，不送出任何請求。
  - 詞彙表、`llm` 區段（chunk 設定、驗證重試次數）與步驟專屬欄位（例如 `min_similarity`）取自該影音最近一次的設定快照，沒有快照時用內建預設；provider、model、prompt 由請求決定。影片標題、說明與詞彙表和正式執行一樣只放在 `<video_metadata>`（JSON 跳脫），不進系統指示。
  - 同步執行，不經過 task queue 與 stage run，不寫入產出、`llm_chunks`、術語候選與事件。受限流約束：超過 RPM／TPM 不等待，回 429 `rate_limited` 與 `retry_after_s`；驗證重試可以透支額度（LLM-R5）。同時進行的試跑最多 2 個，超過也回 429。整體時間上限 3 分鐘，逾時回 502。
  - 錯誤碼：缺 API key、模型不存在是 `needs_action`（409，帶 `actions`）；provider 失敗（5xx、逾時）是 `upstream_failed`（502）；其他永久失敗 422。
  - 驗證重試用盡的 chunk 以步驟的退回內容輸出（correct 是原文），`validation.passed` 為 `false` 並在 `errors` 列出 chunk 與原因。
  - `estimate_usd`：只是試跑前的預估，不是帳本。試跑本身會依 COST-R1 記入成本帳本並受預算限制（COST 待決問題 C17，2026-11-24，依 /goal 由實作者暫定，待 owner 追認；原本這句寫「沒有 M4 的成本帳本，也不寫入帳本」是 M2 時期的狀態）。內建計價表（`internal/cost`）有該 model 的輸入與輸出單價時，以 provider 回報的 token 用量計算（沒有用量時用 L2 的估算，輸出以輸入為上界）；**目前計價表沒有任何 LLM 的價格，所以一律是 `null`**（欄位存在；`"*"` 接受 `null`），不編價格。
  - `item` 接受影音 ID 與短代號（場景的 body 不會被 harness 代換成 ID）；影音在垃圾桶中回 404 `in_trash`。
- M3 修訂（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：prompt 的 kind 增加 `chapters`、`frames`、`note`、`note_map`，PRM-R4 的內建 prompt 增加 `chapters-default`、`frames-default`、`note-default`、`note-map-default`、`note-gemini-default`，同樣不能修改（[CHAP](CHAP-chapters.md)、[FRM](FRM-frames.md)、[NOTE](NOTE-notes.md)、[GEM](GEM-gemini.md)、ADR-0020）。既有 M2 場景不變。

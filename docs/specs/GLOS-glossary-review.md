# GLOS：術語審核

- 狀態：ready
- 里程碑：M4
- 相關：plan 第 3、9 節（詞彙表回饋）；[COR 規格](COR-correct.md) COR-R4、COR-R6 與待決問題 C2、C3、C4；[ADR-0021](../adr/0021-m4-scheduler-subscriptions-and-outbox.md) 第 7 點；UI [設定](../ui/pages.md#12-設定)、[總覽](../ui/pages.md#1-總覽)（見待決問題 G1）
- 驗收場景：[`features/GLOS-glossary-review.feature`](../../features/GLOS-glossary-review.feature)

## 目的

`correct` 在 M2 已經會把模型注意到的新詞收集成術語候選（COR-R6、C4），但只能逐一用 API 核可。M4 提供審核：一次看到所有頻道待審的候選，接受時可以改寫、選擇加進頻道或全域的詞彙表，也可以一次決定很多個；之後的校正就使用新的詞彙表。

候選的收集、清理、`UNIQUE (channel_id, term)`、500 個的累積上限，以及單一候選的核可與拒絕（`PATCH /api/channels/:id/term-candidates/:candidate`）都沿用 COR 規格的 C4，本規格不重寫，只加上 M4 新的部分。

## User stories

| ID | Story |
|---|---|
| GLOS-01 | As the owner, I want to review the term candidates collected during correction, so that my glossary grows without manual typing. |

## 規則

| ID | 規則 |
|---|---|
| GLOS-R1 | C4 的 `PATCH /api/channels/:id/term-candidates/:candidate`（`admin`）的 body 加上兩個選填欄位：`term`（接受時實際加進詞彙表的寫法）與 `scope`（`channel` 或 `global`）。省略時與 M2 相同：`scope` 是 `channel`，`term` 是候選原本的詞。`state` 是 `rejected` 時不能帶 `term` 或 `scope`，否則回 422 `invalid_request`，訊息「拒絕時不能指定 term 或 scope。」；`scope` 是其他值時回 422，`errors[].path` 為 `scope`。決定後候選記錄 `decided_term`、`decided_scope`、`decided_at`，原本的 `term` 不變（所以同一個詞不會再被提出，C4）。回應是候選的完整內容（含這三個欄位）。冪等與 409 `term_candidate_decided` 的規則沿用 C4：已經是同一個 `state` 時回 200，內容維持第一次的決定。 |
| GLOS-R2 | `term` 的清理與上限：控制字元與格式字元（`\p{Cc}`、`\p{Cf}`）換成空白，連續空白合併成一個，去掉前後空白；清理後是空字串時回 422 `invalid_request`，訊息「術語不能是空白。」；超過 100 個字元（code point，與 C2 詞彙表的詞相同）時回 422，訊息「術語最多 100 個字元。」，兩者的 `errors[].path` 都是 `term`。詞彙表中已有大小寫相同的詞時不重複加入，候選仍記為 `accepted`。目標詞彙表已有 500 個詞（C2 的上限，全域與頻道相同）時回 409 `glossary_full`，訊息「詞彙表已有 500 個詞，請先刪除不用的詞再接受新的術語。」，候選維持 `pending`。 |
| GLOS-R3 | `scope: global` 把詞加進全域詞彙表（`/api/glossaries/global/default`），所以每個頻道之後的校正都會用到（C2 的合併順序：全域、頻道、處理方案）；`scope: channel` 只加進候選所屬頻道的詞彙表。寫入詞彙表與更新候選在同一個寫入交易中完成。 |
| GLOS-R4 | 批次決定：`POST /api/term-candidates/decide`（`admin`），body `{decisions: [{candidate, state, term?, scope?}]}`，每一項的規則同 GLOS-R1、R2；最多 100 項，超過回 422，`errors[].path` 為 `decisions`；同一個 `candidate` 出現兩次回 422，`errors[].path` 為 `decisions.<n>.candidate`（第二次出現的索引）。整批是**一個交易**：任何一項回 422、409（`term_candidate_decided`、`glossary_full`）或 404（找不到候選）時整批都不套用，回應的 `errors[].path` 指到那一項（例如 `decisions.2.term`）。成功回 200 `{candidates: [...]}`，順序與 `decisions` 相同。 |
| GLOS-R5 | 跨頻道的審核清單：`GET /api/term-candidates?state=pending[&channel=<id>][&limit=][&cursor=]`（`read`）。`state` 是 `pending`（預設）、`accepted`、`rejected`、`all`，其他值回 422，`errors[].path` 為 `state`；`channel` 接受頻道的 UUID 或 `<platform>:<external_id>`（同 C2）；`limit` 為 1～200，預設 50；游標規則同 LIB 的 `GET /api/items`。回應 `{candidates: [{id, term, state, decided_term, decided_scope, decided_at, channel: {id, key, title}, item: {id, ref, title}, created_at}], total, next_cursor}`，依 `created_at` 由舊到新、相同時依 `id`（先看最早提出的）；`item` 是第一次提出這個詞的影音，該影音已永久刪除時為 `null`。 |
| GLOS-R6 | 接受之後，**下一次執行**的 `correct` 使用新的詞彙表：詞彙表依 C2 合併、依 C3 放進 `<video_metadata>` 的 `glossary`，接受時改寫過的詞以改寫後的寫法出現，原本的寫法不出現（除非它另外在詞彙表中）。已經完成的校正不會自動重跑；拒絕的詞不會進詞彙表。 |
| GLOS-R7 | UI：設定頁的「詞彙表」分區列出 GLOS-R5 的待審清單（依頻道分組，顯示提出它的影音與時間），每一列可以「接受」、「改寫後接受」（輸入框預填原詞）、「拒絕」，並選擇「加到這個頻道」或「加到全域」；可以多選後一次接受或拒絕（GLOS-R4）。總覽的「需要你處理」在有待審候選時顯示「有 N 個術語等你確認。」與「去審核」按鈕。 |

## 介面

- API：
  - `PATCH /api/channels/:id/term-candidates/:candidate`（`admin`）：body 加上 `term`、`scope`（GLOS-R1）。
  - `POST /api/term-candidates/decide`（`admin`，GLOS-R4）。
  - `GET /api/term-candidates`（`read`，GLOS-R5）。
  - `GET /api/channels/:id/term-candidates`（C4）的每一項也加上 `decided_term`、`decided_scope`、`decided_at`。
- MCP、CLI：無（見「不在範圍內」）。
- UI：GLOS-R7；文案「有 N 個術語等你確認。」、「改寫後接受」、「加到這個頻道」、「加到全域」。
- 資料表方向（ADR-0021 第 7 點）：`glossary_candidates` 加上 `decided_term`、`decided_scope`、`decided_at`；`(state, created_at)` 加索引供 GLOS-R5 使用。**不加** `suggestion`（見 G2）。
- 資源類別：同步請求。
- 對 `pipeline.Stages`／`outputs` 的影響：無；`correct` 的詞彙表組裝（C2、C3）不變，只是讀到更多詞。

## 與其他規格的關係

- COR：C4 的收集與單一決定照舊；GLOS-R1 只是在同一條路由的 body 加上選填欄位，M2 的呼叫方式（只有 `state`）行為不變。
- COR C2：詞彙表的上限（500 個詞、每個詞 100 個字元）沿用；GLOS-R2 的 409 是 C2 上限的另一個入口。
- COR C3、S6-Q2：中文詞的字形轉換發生在組裝 prompt 時，審核時存的是使用者輸入的寫法。
- AUTH：讀取用 `read`、寫入用 `admin`，與 AUTH 待決問題中「讀取設定物件用 `read`、寫入一律 `admin`」一致。
- LIB-09（總覽）：「需要你處理」的術語提醒由總覽的切片呈現，資料來自 GLOS-R5 的 `total`。

## 不在範圍內

- 模型提出的建議寫法（plan 資料模型的 `suggestion`）：C4 的輸出 schema 沒有這個欄位（G2）。
- 詞彙表本身的編輯頁（新增、刪除、排序既有的詞）：仍用 `PUT /api/glossaries/:scope/:id`，UI 的編輯器屬於設定頁的另一個切片。
- 接受後自動重跑已完成的校正。
- MCP tool 與 CLI 子命令：審核是 owner 在 UI 中做的事，Agent 的 token 也不應該能改詞彙表（`admin`）。
- 處理方案層的 `glossary`（PRF-R1）：審核只寫入全域與頻道詞彙表。

## 待決問題

- G1：pages.md 沒有術語審核的畫面。決定（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：放在設定頁新的「詞彙表」分區（GLOS-R7），總覽的「需要你處理」連過去；UI 切片要在 pages.md 第 12 節的分區表加上這一列，並補上版面示意。
- G2：ADR-0021 第 7 點列了 `glossary_candidates.suggestion`，但 COR C4 已決定候選只有 `term`（OpenAI strict 模式不允許選填屬性，fixture 也只有 `term`）。決定（2026-11-23，依 /goal 採保守預設，待 owner 追認）：不加 `suggestion`；使用者的改寫存在 `decided_term`。ADR-0021 第 7 點在整合時改成 `decided_term`、`decided_scope`、`decided_at`。
- G3：修改後的詞最多 100 個字元（GLOS-R2），比候選本身的 64 個字元（C4）寬，因為它會直接進詞彙表，上限應該跟詞彙表的詞（C2）一致。
- G4：批次決定一律整批交易（GLOS-R4）。部分成功比較寬容，但 UI 很難說清楚哪些成功了；整批失敗時回應會指出第一個出錯的項目，使用者修正後重送。
- G5：GLOS-R5 由舊到新排序，讓最早提出、累積最久的候選先被處理；UI 可以切換由新到舊（`order=desc`，之後需要時再加）。

### GLOS 切片實作時的決定

以下皆為（2026-11-24，依 /goal 採 advisor 建議，待 owner 追認）。實作在 `internal/pipeline/glossary_review.go`、`internal/api/term_review.go`，migration `00029_glossary_review`。

- G6：拒絕時 `decided_term`、`decided_scope` 是 `null`，只有 `decided_at` 有值；pending 時三個都是 `null`。`GET /api/channels/:id/term-candidates`、PATCH 與批次決定的回應都帶這三個欄位。
- G7：`decisions` 是空陣列時回 422，`errors[].path` 為 `decisions`（「至少要有一項。」）；規格只寫了上限。
- G8：批次的錯誤 path：整批層級的錯誤（項目數、重複的 `candidate`）先檢查；之後**依序逐項**「檢查 → 套用」，回報的就是第一個出錯的項目（G4）。欄位錯誤是 `decisions.<n>.<欄位>`（`state`、`term`、`scope`），找不到候選（404）是 `decisions.<n>.candidate`，409（`term_candidate_decided`、`glossary_full`）是 `decisions.<n>`。批次中每一項的 `state` 也不用 schema 的 enum，才能指到 `decisions.<n>.state`。
- G9：`GET /api/term-candidates?channel=` 找不到頻道時（natural key 或 UUID）回 200 的空清單（`total: 0`），不回 404；篩選條件沒有符合的資料不是錯誤。C4 的 `GET /api/channels/:id/term-candidates` 對找不到的 UUID 仍回 404（不變）。natural key 的平台不合法時回 422（同 C2）。
- G10：M2 的 PATCH（只有 `state`）在頻道詞彙表已有 500 個詞時，原本回 422（C2 的上限檢查），現在依 GLOS-R2 回 409 `glossary_full`，候選維持 `pending`。詞彙表已滿但已有大小寫相同的詞時照樣接受（不需要新的位置）。
- G11：詞彙表已有大小寫相同的詞時，詞彙表保留原本的寫法，`decided_term` 記錄這次送出的寫法（例如詞彙表有 `Kafka`、候選是 `kafka`，`decided_term` 是 `kafka`）。同一批中兩個候選加入大小寫相同的詞時只加一次。
- G12：稽核：決定的內容與時間記在候選的 `decided_*` 欄位，另寫一行 info log（候選 id、頻道 id、state、scope；不寫詞的內容，避免把不可信的文字寫進 log）；沒有另外建稽核表（同 AUTH A21）。
- G13：`scope: global` 不需要額外的權限：PATCH 與批次決定都已要求 `admin`，與 `PUT /api/glossaries/global/default` 相同。
- G14：審核清單的游標沿用 `GET /api/items` 的格式（HMAC、版本、`created_at` 毫秒與 id）；篩選指紋加上 `term-candidates` 前綴並涵蓋 `state` 與 `channel`，影音列表的游標不能拿來這裡用，換了篩選條件的游標回 422。
- G15：改寫的術語與模型提出的候選用同一套清理（`llm.CleanTerm`：`\p{Cc}`、`\p{Cf}` 與空白換成空白、合併連續空白、去掉前後空白），長度以清理後的 code point 計算。像指令的文字（例如 `</video_metadata>`）在長度內仍會接受：詞彙表依 C3 放在 `<video_metadata>` 的 JSON 中，系統指示說明它是參考資料，這裡不另外過濾。
- G16：驗收測試的 seeding（`the term candidates … are pending`）經 `app.Server.SeedTermCandidate` 寫入產品的 store，清理與 UNIQUE 同 C4 的收集；`created_at` 是 fake clock 的現在加上登記序號毫秒，接在彙整（`flushDigests`）之後寫入。

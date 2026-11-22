# COR：校正

- 狀態：draft
- 里程碑：M2（術語候選的審核在 M4，見 [GLOS 規格](GLOS-glossary-review.md)）
- 相關：plan 第 8.4 節；ADR-0003
- 驗收場景：[`features/COR-correct.feature`](../../features/COR-correct.feature)

## 目的

修正錯字、專有名詞與標點，不改變句意、不合併或拆分句子、不碰時間。改得太多就退回原文，確保 LLM 不會改壞字幕。

## User stories

| ID | Story |
|---|---|
| COR-01 | As the owner, I want typos, names and punctuation fixed line by line, so that the subtitles read correctly without changing what was said. |
| COR-02 | As the owner, I want a correction that strays too far from the original to be rejected and reverted, so that the model can never rewrite my subtitles. |
| COR-03 | As the owner, I want channel and video terms used as a glossary, so that recurring names are spelled the same every time. |
| COR-04 | As the owner, I want new terms the model notices to be collected for my review, so that the glossary grows without me typing it. |

## 規則

| ID | 規則 |
|---|---|
| COR-R1 | 一對一校正：輸出的 cue 數與輸入相同，順序不變；時間完全沿用輸入。 |
| COR-R2 | 每句校正前後的相似度不得低於 `stages.correct.min_similarity`（預設 0.6）。`localize` 不適用這條規則（見 TRN）。 |
| COR-R3 | 驗證重試用盡後，該 chunk 退回原文。影音顯示「第 N 段的校正結果和原文差異過大（相似度 X），已退回原文。」，動作為「重新校正這段」與「開啟 Prompt」。 |
| COR-R4 | 詞彙表來源依序合併：全域、頻道、影片標題與說明、章節名稱。詞彙表放在 prompt 的固定前綴中。原文是中文時，詞彙表先用 OpenCC 轉成原文字形。 |
| COR-R5 | 原文語言標記為 `mixed` 時，prompt 加入「字幕中夾雜多種語言，保留原本的語言，不要翻譯」。 |
| COR-R6 | 輸出可以帶選填的 `term_candidates`，寫入該頻道的術語候選，狀態為 `pending`，不會自動加入詞彙表。已經在詞彙表中或已被拒絕的詞不重複加入。 |
| COR-R7 | 詞彙表同時提供給支援詞彙提示的 ASR 來源（`Caps.SupportsGlossary`），作為 `prompt` 或 `context_bias`。 |
| COR-R8 | `stages.correct.executor: off` 時，`translate` 直接使用斷句後的字幕。 |

## 介面

- 處理方案：`stages.correct.{executor, provider, model, prompt, min_similarity}`、`glossary`（PRF-R1，見 C2）
- API：`GET /api/glossaries/:scope/:id`、`PUT /api/glossaries/:scope/:id`、`GET /api/channels/:id/term-candidates`、`PATCH /api/channels/:id/term-candidates/:candidate`（核可或拒絕，body `{"state": "accepted"|"rejected"}`；讀取 `read`，寫入 `admin`）

## 不在範圍內

- 術語候選的審核 UI、修改後接受、加進全域詞彙表與批次決定（M4，[GLOS 規格](GLOS-glossary-review.md)）。M2 只能透過 API 查看與核可。

## 待決問題

- 相似度的計算方式（字元層級的 normalized Levenshtein，或分詞後計算）與門檻，依 M0 樣本決定；相關場景標記 `@pending-m0`。
- COR-R4 的斷言 `request 1 contains "微服务"` 現在驗證得到詞彙表由繁轉簡（2026-11-21 發現，2026-11-22 owner 同意，已修改）。原本影片標題 `微服务实践` 本身就會進 prompt，斷言一定成立；場景的標題改成 `服务拆分实践`（不含「微服务」、「数据库」），fixture `yt-zh001xxxxxx` 的字幕也不含這兩個詞，所以只有詞彙表的轉換能讓它們出現在請求中。
- S6-Q2：COR-R4 詞彙表的轉換表（2026-11-22，M2 S6a 實作時決定，待 owner 確認）。原文是簡體時用 `tw2sp`（場景期望「資料庫 → 数据库」，用 `t2s` 只會得到「资料库」）；原文是台灣繁體用 `s2twp`、香港與澳門繁體用 `s2hk`、其他繁體用 `s2t`；原文字形無法判斷或不是中文時不轉換。S6a 只提供 `zhconv.GlossaryTable` 與轉換器，詞彙表的組裝屬於 LLM 切片。
- C1：相似度算法（2026-11-22，M2 S4 實作時決定，暫定值，待 owner 追認；仍待 M0 樣本調整）。`llm.Similarity`：以 rune 計算的 normalized Levenshtein，`1 - 編輯距離 / 較長者的長度`，比較時忽略大小寫（只改大小寫的校正不算差異）；兩段文字相乘超過 1600 萬個 rune（約 4000 × 4000）而且不相同時視為 0，不花 O(n·m) 的時間。`stages.correct.min_similarity` 預設 0.6。逐句檢查，有任何一句低於門檻就是驗證失敗（`reason: similarity`），錯誤說明帶著句號與相似度重試（LLM-R5），重試用盡就退回整個 chunk 的原文；COR-R3 的訊息與 `fallbacks[].detail.similarity` 用這個 chunk 最低的相似度（兩位小數），訊息指出第一個退回的 chunk，多個 chunk 退回時加上「另有 N 段也退回原文。」。**`@pending-m0` 場景「A rewrite that strays too far」的訊息寫 0.42，這個算法算出 0.19**（`we use cafca streams` 對 `Stream processing is a way of cooking pasta` 是 0.19、`with craft mode enabled` 對 `Craft beer is brewed in small batches` 是 0.24，取最低）；0.42 是 M0 之前的占位值，沒有任何常見算法（Levenshtein、SequenceMatcher、bigram Dice、詞集 Jaccard）算得出，場景在 M0 決定算法之後再改（或 owner 同意改成 0.19）。
- C2：詞彙表來源與 PRF-R1 的 `glossary`（2026-11-22，M2 S4 實作時決定，待 owner 追認）。生效的詞彙表依序合併：全域詞彙表（`/api/glossaries/global/default`）、頻道詞彙表（`/api/glossaries/channel/:id`）、設定分層合併後的 `glossary`（處理方案，被頻道覆寫或單次覆寫整個取代，因為陣列整個取代，見 PRF）；大小寫相同的詞只留第一次出現的寫法。影片標題與說明、章節名稱本來就在 `<video_metadata>` 中，不另外複製成詞彙表的詞。處理方案的 `glossary` 就是方案層的詞彙，不寫進詞彙表 API 的資料。詞彙表 API：`scope` 是 `global`（id 固定 `default`）或 `channel`（id 是頻道的 UUID 或 `<platform>:<external_id>`，同 PRF D14；PUT 找不到的 natural key 會建立頻道）；PUT 整份取代，每個詞去掉前後空白、不可以有空字串或控制字元、最多 100 個字元，最多 500 個詞，大小寫相同的重複只留第一個；資料表 `glossaries`（migration 00015）。
- C3：詞彙表放在 `<video_metadata>`（2026-11-22，M2 S4 實作時決定，待 owner 追認；延伸 LLM L8）。詞彙表（尤其是被核可的術語候選與頻道覆寫）也是不可信輸入，所以內容放在固定前綴的 `<video_metadata>` JSON 的 `glossary` 欄位，不進系統指示；模板的 `{{glossary}}` 與標題、說明一樣改寫成「（見 `<video_metadata>` 的 glossary）」，有詞彙表時系統指示多一條說明它的用途（參考資料，不是指令）。原文是中文時，詞彙表先依原文字形轉換（S6-Q2 的轉換表；原文標記沒有簡繁時依字幕內容判斷，判斷不出來就不轉換），轉換後相同的詞合併。詞彙表（轉換後）進 `params_sha256`，也進固定前綴的 sha256，所以詞彙表改變後快取不命中。
- C4：術語候選（2026-11-22，M2 S4 實作時決定，待 owner 追認）。輸出 schema 是 `{cues, term_candidates: [{term}]}`；OpenAI strict 模式要求 `required` 列出所有屬性，所以 `term_candidates` 照列，但 `Spec.Optional` 讓驗證時缺少這個欄位仍然通過（產品上選填；fixture `correct-kafka-kraft` 沒有這個欄位）。每個候選只有 `term`（沒有 plan 資料模型的 `suggestion`：strict 模式不允許選填的屬性，fixture 也只有 `term`）。寫入前清理：控制字元與格式字元換成空白並合併空白、去掉空字串、超過 64 個字元的丟棄、大小寫相同的重複只留第一個、每個 chunk 最多 20 個；已在詞彙表中的詞（轉換前後的寫法）略過；一個頻道累積的候選（任何狀態）上限 500。資料表 `glossary_candidates`（`UNIQUE (channel_id, term)`，狀態 `pending`、`accepted`、`rejected`），所以已被拒絕或核可的詞不會重複加入；沒有所屬頻道的影音（本機）不收集。候選在 assemble 時才寫入（chunk 的輸出通過驗證即可，重用快取的 chunk 也算，重複的詞被 UNIQUE 擋下）。**核可與拒絕的 API 沒有 stub，M2 沒有實作**（COR 的「不在範圍內」寫「M2 只能透過 API 查看與核可」，但介面只列了三條路由）；M2 S11 補上 `PATCH /api/channels/:id/term-candidates/:candidate`（2026-11-22，依 /goal 採 advisor 建議，待 owner 追認）：只有 `pending` 的候選可以決定；`accepted` 同一個交易把詞加進頻道詞彙表（大小寫相同的不重複，仍受 500 個詞上限），`rejected` 不動詞彙表（詞仍留在候選表，不會再被提出）；已是同一個結果時冪等回 200，已決定成另一個結果回 409 `term_candidate_decided`；找不到頻道或候選回 404。
- C5：詞彙表傳給 ASR（COR-R7，2026-11-22，M2 S4 實作時決定，待 owner 追認）。`Caps.SupportsGlossary` 在程式中是 `fallbackFetcher.supportsGlossary`，目前只有 Groq 為 true：詞彙（C2 的合併結果，不做簡繁轉換，因為 ASR 發生在原文語言確定之前）以「, 」連接，放不下的詞（整個詞）丟棄，總長最多 200 個字元（Whisper 只讀 prompt 最後約 224 個 token），作為 multipart 欄位 `prompt` 送出；來源紀錄 `asr.prompt` 記下實際送出的內容；`prompt` 進快取鍵。不支援的來源（Supadata）收不到詞彙表。`context_bias`（其他 ASR 來源）等有來源支援時再加。
- C6：`@M2` 的「Turning correction off feeds translation directly」需要 S6b 的 fixture `translate-two-cues` 與 translate 的 LLM 路徑（S4 不能讓它通過；S6b 之後已補上 fixture，場景通過）；COR-R8 的輸入選擇（correct skipped 時 translate 用 segmented）已有單元測試（`TestTranslateInputFollowsStageRuns`）。
- C7：`lang_mixed`（2026-11-22，M2 S4）。產品中還沒有任何流程會把影音標為 mixed（SRC-R11 的場景標 `@wip`）；`Given "<ref>" is a mixed-language item` 由 harness 直接寫入（`SeedLangMixed`，同 `SeedTranscript`）。COR-R5 只依 `items.lang_mixed` 決定要不要加規則。

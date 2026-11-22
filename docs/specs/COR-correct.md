# COR：校正

- 狀態：draft
- 里程碑：M2（術語候選的審核 UI 在 M4，見故事地圖 GLOS-01）
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

- 處理方案：`stages.correct.{executor, provider, model, prompt, min_similarity}`
- API：`GET /api/glossaries/:scope/:id`、`PUT /api/glossaries/:scope/:id`、`GET /api/channels/:id/term-candidates`

## 不在範圍內

- 術語候選的審核 UI（M4）。M2 只能透過 API 查看與核可。

## 待決問題

- 相似度的計算方式（字元層級的 normalized Levenshtein，或分詞後計算）與門檻，依 M0 樣本決定；相關場景標記 `@pending-m0`。
- [弱] COR-R4 的斷言 `request 1 contains "微服务"` 驗證不到詞彙表的轉換（2026-11-21，M2 S0 切片建立 fixture 時發現，需要 owner 同意修改場景）。影片標題 `微服务实践` 本身就會進 prompt，所以這個斷言一定成立；只有 `数据库` 真正驗證得到詞彙表由繁轉簡。需要 owner 決定：把場景的影片標題改成不含「微服务」，或接受這個斷言較弱。

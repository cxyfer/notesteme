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
- COR-R4 的斷言 `request 1 contains "微服务"` 現在驗證得到詞彙表由繁轉簡（2026-11-21 發現，2026-11-22 owner 同意，已修改）。原本影片標題 `微服务实践` 本身就會進 prompt，斷言一定成立；場景的標題改成 `服务拆分实践`（不含「微服务」、「数据库」），fixture `yt-zh001xxxxxx` 的字幕也不含這兩個詞，所以只有詞彙表的轉換能讓它們出現在請求中。
- S6-Q2：COR-R4 詞彙表的轉換表（2026-11-22，M2 S6a 實作時決定，待 owner 確認）。原文是簡體時用 `tw2sp`（場景期望「資料庫 → 数据库」，用 `t2s` 只會得到「资料库」）；原文是台灣繁體用 `s2twp`、香港與澳門繁體用 `s2hk`、其他繁體用 `s2t`；原文字形無法判斷或不是中文時不轉換。S6a 只提供 `zhconv.GlossaryTable` 與轉換器，詞彙表的組裝屬於 LLM 切片。

# TRN：翻譯與中文字形

- 狀態：draft
- 里程碑：M2
- 相關：plan 第 7、8.4 節；ADR-0002、ADR-0003
- 驗收場景：[`features/TRN-translate.feature`](../../features/TRN-translate.feature)

## 目的

把校正後的字幕翻成一或多個目標語言。跨越多個 cue 的長句整句翻譯，再依切分標記分回各 cue，時間不變。中文之間只差字形時，可以用 OpenCC 直接轉換，或交給 LLM 做在地化。

## User stories

| ID | Story |
|---|---|
| TRN-01 | As the owner, I want subtitles translated into one or more target languages, so that I can watch content in the language I prefer. |
| TRN-02 | As the owner, I want long sentences that span several cues translated as a whole, so that the translation reads naturally instead of in fragments. |
| TRN-03 | As the owner, I want bad translations caught and handled safely, so that I never get empty, untranslated or wrong-language lines. |
| TRN-04 | As the owner, I want Simplified and Traditional Chinese converted cheaply, or localised with the right regional wording when I ask for it, so that I choose between cost and quality. |
| TRN-05 | As the owner, I want existing platform subtitles in my target language reused, so that I do not pay to translate what already exists. |

## 規則

| ID | 規則 |
|---|---|
| TRN-R1 | 每個目標語言各有一筆 stage run 與一份 `translated` 產出；所有目標語言共用同一份校正字幕，不重跑校正。 |
| TRN-R2 | 只有一句跨越多個 cue 時才組成句群；判斷依據是句末標點。其餘 cue 一句對一句翻譯。 |
| TRN-R3 | 句群的譯文必須包含 cue 數減一個切分標記（`⟨|⟩`）；程式依標記把譯文分回各 cue，時間不變。 |
| TRN-R4 | 切分標記數不符時，該句群改成逐 cue 翻譯（多一次請求），不計入驗證重試次數。 |
| TRN-R5 | 譯文驗證：語言偵測結果為目標語言；字元數比例在 `stages.translate.length_ratio`（預設 0.3 到 3.0）之間；不可為空；不可和原文相同，除非原文是專有名詞或不超過 3 個字元。 |
| TRN-R6 | 譯文超過閱讀速度上限時只記錄警告（產出的 `warnings`），不判定失敗。 |
| TRN-R7 | 驗證重試用盡後，該 cue 保留原文並記錄在 `fallbacks` 中。 |
| TRN-R8 | 原文是中文、目標是另一種中文字形時：`variant_strategy: convert`（預設）使用 `opencc` executor，依目標地區選轉換表（`zh-Hant-TW` 用 `s2twp`、`zh-Hant-HK` 用 `s2hk`、`zh-Hans` 用 `t2s`），不呼叫 LLM。 |
| TRN-R9 | `variant_strategy: localize` 時，先用 OpenCC 轉換，再交給 LLM 修正用詞與語氣，預設 prompt 為 `localize-<目標>`。相似度以 OpenCC 轉換後的文字為基準計算。 |
| TRN-R10 | 原文語言和目標語言完全相同時，`translate` 為 `skipped`。 |
| TRN-R11 | 平台同時有原文與目標語言的人工字幕時，直接採用目標語言的字幕軌，依時間重疊對齊到原文的 cue，不呼叫 LLM；來源紀錄的 `executor` 為 `platform`。 |

## 介面

- 處理方案：`language.targets`、`language.variant_strategy`、`stages.translate.{executor, provider, model, prompt, length_ratio}`

## 不在範圍內

- 平台機器翻譯字幕的採用規則見 SRC-R2。

## 待決問題

- 切分標記的記號（`⟨|⟩`）是否會被模型改寫，M0 用實際模型測試；必要時改用 JSON 陣列輸出。相關場景標記 `@pending-m0`。
- `translate` 依目標語言分成多筆 stage run（`stage_runs.lang`）：M2 S0 先保持一筆（`lang` 為空），詳情、前進與重跑怎麼處理多個語言由 TRN 切片決定（見 PIPE 的待決問題，2026-11-21）。

# SEG：斷句

- 狀態：draft
- 里程碑：M2
- 相關：plan 第 6.2、8.5 節；ADR-0003
- 驗收場景：[`features/SEG-segment.feature`](../../features/SEG-segment.feature)

## 目的

把正規化字幕重新切成適合閱讀的句子。LLM 只決定在哪裡斷句，時間一律由程式依來源計算，並標示時間是怎麼來的。

## User stories

| ID | Story |
|---|---|
| SEG-01 | As the owner, I want subtitles split into readable sentences, so that each line on screen is a complete thought. |
| SEG-02 | As the owner, I want subtitle timings to come only from the source, never from the model, so that lines never drift away from the speech. |
| SEG-03 | As the owner, I want to choose how strict timing must be, so that I can trade readability against timing accuracy. |
| SEG-04 | As the owner, I want good manual captions left as they are, so that I do not pay to re-split something already well split. |

## 規則

| ID | 規則 |
|---|---|
| SEG-R1 | 來源是人工字幕時，`segment` 預設為 `skipped`；處理方案設定 `stages.segment.on_manual: run` 時才執行。 |
| SEG-R2 | LLM 的輸出只有斷點標記（以 chunk 內的字元位置表示），不包含任何時間。輸出中出現時間欄位視為驗證失敗。 |
| SEG-R3 | 斷點標記對回文字時使用模糊比對，容許 LLM 改動標點與空白。對不上的 chunk 視為驗證失敗；重試用盡後，該 chunk 保留正規化字幕的切法。 |
| SEG-R4 | 去掉空白後，斷句前後的文字必須完全相同；不同時視為驗證失敗。 |
| SEG-R5 | 合併相鄰的 cue 時，時間取第一句的 start 到最後一句的 end，時間來源為 `merged`。 |
| SEG-R6 | 在單一 cue 內切分且有 word 級時間時，切點的時間為斷點前一個字詞的 end，時間來源為 `source`。 |
| SEG-R7 | 沒有 word 級時間時依 `timing.split_timing` 處理：`forbid` 不允許段內切分，忽略這類斷點；`when_needed`（預設）只在 cue 超過長度或時長上限時切；`free` 依 LLM 的斷點切。 |
| SEG-R8 | 段內切分沒有 word 級時間時，先在 cue 範圍內用 `silencedetect` 找停頓點（時間來源 `silence`），找不到才依字數比例插值（`interpolated`）。 |
| SEG-R9 | `timing.split_timing_audio: never` 時不為了切分下載音訊，直接插值；`auto` 時只下載音訊，不下載影片。 |
| SEG-R10 | 斷句結果套用 plan 第 8.5 節的字幕規則（每行字數、行數、顯示時長）。`when_needed` 與 `free` 時，超過上限的 cue 一定會被切分；`forbid` 時不切分，只在產出的 `warnings` 中記錄。 |

## 介面

- 處理方案：`stages.segment.*`、`timing.split_timing`、`timing.split_timing_audio`、`subtitle_rules.<script>.*`

## 不在範圍內

- forced alignment（例如 wav2vec2），保留給本地 ASR adapter。

## 待決問題

- `when_needed` 的長度與時長門檻，依 M0 量測的 segment 長度分佈調整；斷點對齊的成功率也在 M0 量測。相關場景標記 `@pending-m0`。
- SEG-R10 場景補上 `And the LLM provider "openai" answers "segment" with "segment-keep-all"`（2026-11-21 發現，2026-11-22 owner 同意，已修改）。Background 把 `segment` 設成 `openai`，`processed up to "segment"` 真的執行 segment 時，fakellm 沒有腳本會回 500 並記違規。fixture `segment-keep-all` 不合併也不切分，每句原樣輸出；它在 SEG 切片（S5）建立。

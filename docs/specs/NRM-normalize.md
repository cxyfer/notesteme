# NRM：正規化

- 狀態：draft
- 里程碑：M1
- 相關：plan 第 5、6.2、7 節；ADR-0003
- 驗收場景：[`features/NRM-normalize.feature`](../../features/NRM-normalize.feature)

## 目的

把各來源格式不一的原始字幕整理成乾淨、時間正確的 cue 序列，作為後續所有步驟的共同輸入。正規化的結果只依賴來源，不依賴目標語言。

## User stories

| ID | Story |
|---|---|
| NRM-01 | As the owner, I want rolling auto captions cleaned into readable lines, so that the subtitles do not repeat themselves. |
| NRM-02 | As the owner, I want subtitle timings to always move forward without overlaps, so that every player shows them correctly. |
| NRM-03 | As the owner, I want the normalized transcript to be the same no matter which target languages I choose, so that changing targets never forces earlier stages to run again. |

## 規則

| ID | 規則 |
|---|---|
| NRM-R1 | 滾動字幕去重：後一句的文字以前一句的文字開頭時，只保留新增的部分；完全相同的連續 cue 合併成一句，時間取第一句的 start 到最後一句的 end。 |
| NRM-R2 | 碎句合併：長度不到 300ms、或只有標點的 cue，併入前一句；時間來源標為 `merged`。 |
| NRM-R3 | 去除格式標記：HTML 標籤（`<i>`、`<font>`）、ASS 覆寫碼（`{\an8}`）、重複的空白。音樂與音效標記（`[Music]`、`♪`）保留，交給後續步驟判斷。 |
| NRM-R4 | 時間檢查：cue 依 start 排序。重疊時，把前一句的 end 修正為後一句 start 減 40ms；修正後短於 300ms 就和後一句合併。結果中每一句的 start 都不早於前一句的 end。 |
| NRM-R5 | 空白 cue 移除。 |
| NRM-R6 | 有 word 級資料時，合併與修正都保留各字詞原本的時間。 |
| NRM-R7 | 正規化不做簡繁轉換，也不讀取目標語言設定。同一份原始字幕在不同的目標語言下，產生完全相同的正規化字幕，並共用同一個快取鍵。 |
| NRM-R8 | 正規化字幕的來源紀錄指向原始字幕，並記錄 `impl_version`。 |

## 介面

- API：`GET /api/items/:id/cues?variant=normalized`

## 不在範圍內

- 斷句與重新切分（M2，見 SEG）。
- 語言偵測的規則見 SRC-R11。

## 待決問題

- 300ms、40ms 的門檻是否要依來源調整，M0 量測 segment 長度分佈後再決定。

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

- 處理方案：`stages.segment.*`（含 `on_manual: skip | run`，預設 `skip`）、`timing.split_timing`、`timing.split_timing_audio`、`subtitle_rules.<script>.*`
- LLM 輸出（`notesteme_segment`）：`{"text": string, "breaks": [integer]}`，見待決問題 G1。
- 產出的來源紀錄另有 `warnings[]`（`{cue, rule, value, limit}` 或 `{rule: "silence_unavailable", detail}`）與 `timing`（這次用的 `split_timing`、`split_timing_audio`）。

## 不在範圍內

- forced alignment（例如 wav2vec2），保留給本地 ASR adapter。

## 待決問題

- `when_needed` 的長度與時長門檻，依 M0 量測的 segment 長度分佈調整；斷點對齊的成功率也在 M0 量測。相關場景標記 `@pending-m0`。
- SEG-R10 場景補上 `And the LLM provider "openai" answers "segment" with "segment-keep-all"`（2026-11-21 發現，2026-11-22 owner 同意，已修改）。Background 把 `segment` 設成 `openai`，`processed up to "segment"` 真的執行 segment 時，fakellm 沒有腳本會回 500 並記違規。fixture `segment-keep-all` 不合併也不切分，每句原樣輸出；它在 SEG 切片（S5）建立。
- 以下是 S5 實作時的決定（2026-11-22，依 /goal 採保守預設，待 owner 追認）：
  - **G1：SEG-R2～R4 的矛盾與輸出格式**。R2 說輸出「只有斷點標記（以字元位置表示）」，但 R3 要「模糊比對、容許改動標點與空白」、R4 要「去掉空白後文字相同」，只有位置的輸出沒有文字可比對；R3 容許改標點，R4 卻只容許改空白。fixture 名稱 `segment-adds-commas`、`segment-rewrites-text`、ADR-0003（「LLM 只回傳文字和斷點標記」）與 `segment-default` prompt（「每個輸入的字都必須在輸出中恰好出現一次」）都假設輸出有文字。保守預設：
    - 輸出 `{"text", "breaks"}`：`text` 是模型依序接起來的 chunk 文字，`breaks` 是每句新字幕在 `text` 中開始的字元（Unicode code point）位置，嚴格遞增、在 `(0, len(text))` 之內；沒有任何時間欄位，出現 schema 以外的欄位（例如時間）一律驗證失敗（R2）。
    - 比對只看「比對字元」：空白、標點（`unicode.IsPunct`）與格式字元（Cf）以外的字元，大小寫要相同。比對字元的序列與輸入不同時驗證失敗，原因 `text_changed`（R4）；相同時斷點一定對得回去，但斷點不能產生空的字幕，也不能切在拉丁文字的字詞中間（兩個比對字元之間沒有空白、兩邊都不是漢字或假名），否則原因 `alignment`（R3）。比對與對齊都是線性時間，`text` 超過輸入字元數的 4 倍加 256 時直接 `text_changed`。
    - **產出的文字一律從輸入切出**，模型對標點與空白的改動不採用（校對是 `correct` 的工作）。所以 R3 的「容許改動標點」只影響對齊，R4 在寫出前再當成不變式檢查一次。
    - 影響：要求模型數字元，命中率可能不高；重試用盡時退回原本的切法，不會寫出錯的字幕。M0 量測對齊成功率時一併檢查這個格式；若改成「只輸出切好的句子陣列」之類的格式，fakellm 的 `segment_breaks` 與靜態 fixture 一起改。
  - 斷點落在兩句之間代表保留原本的邊界，兩句之間沒有斷點就是合併（R5）；chunk 的第一句一律斷開，重疊區的斷點以前一個 chunk 為準（LLM-R1）。斷點之前的標點歸前一段（`We use Kafka.`）。CJK 句子合併時不加空白。
  - 段內切分的時間（R6～R8）：有 word 級時間、而且切點剛好在字詞之間時，前一段結束在前一個字詞的 end、後一段從後一個字詞的 start 開始，兩段的其他邊界沿用 cue 的 start 與 end（`source`）；這不受 `split_timing` 限制（R7 只管沒有 word 級時間的切分），字詞的文字與 cue 對不上時當成沒有 word 級時間。沒有 word 級時間時，`when_needed` 的門檻就是字幕規則（cue 排版後超過行數、或超過最長顯示時長；M0 後調整）。停頓點取這句範圍內、中點最接近字數比例估計值的靜音，前一段結束在靜音開始、後一段從靜音結束開始（`silence`）；找不到時依非空白字元數比例插值，兩段相接（`interpolated`）。整份音訊只下載一次、跑一次 `silencedetect`，暫存在 `<data>/tmp`，用完刪除，不留下產出。下載或 ffmpeg 回報 permanent、needs_action，或沒有 ffmpeg、平台不支援下載時改用插值並記在 `warnings`，產出不寫入步驟層的快取鍵；retryable 的錯誤讓 assemble 任務重試。
  - 一句字幕的時間來源取它兩端中最不可靠的（`source` < `merged` < `silence` < `interpolated` < `model`），由多句組成時至少是 `merged`；沒有被改動的句子沿用輸入的值（空的視為 `source`）。
  - **R9 與 R10 的衝突**：R9 的場景（`never`，依字數插值）預期剛好 2 句，但插值出的第一段約 8.6 秒，超過 7 秒的上限；若 R10 遞迴強制切分會變成 3 句。保守預設：R10 的強制切分只作用在沒有被 LLM 段內切開的句子：由多句合併而超過上限的，先在原本的句子邊界切回去（時間仍來自來源）；一整句超過上限的，由程式在字詞之間（優先在標點之後）切成估計每段都符合上限的最少段數，時間依 word 級時間、停頓點或插值；LLM 段內切出的片段仍超過上限時不再切分，只記在 `warnings`（避免在插值的時間上再插值）。`forbid` 時一律不切，只記 `warnings`。太短（低於最短顯示時長）的句子只記 `warnings`。影響：`when_needed`／`free` 的產出仍可能有超過上限的句子（都列在 `warnings`）。
  - 字幕規則的預設值（plan 第 8.5 節）：拉丁文字每行 42 字元、CJK 每行 22 字（範圍 20–25 取中間，M0 後調整）、每句 2 行、顯示 0.8–7 秒；閱讀速度不在 R10 的範圍，`max_cps` 目前沒有使用。文字系統依字幕語言判斷（`zh`、`ja`、`ko` 是 `cjk`），語言不明時依文字中漢字與假名的比例。行數以貪婪換行計算，不在文字中插入換行。
  - R1 的「人工字幕」是採用的字幕來源 kind 為 `manual`（YouTube 人工字幕、Bilibili CC 字幕）；本機外掛或內嵌字幕、Podcast transcript 不算。`stages.segment.on_manual` 是 `skip`（預設）或 `run`。原因的判斷順序是 `off` → `not_configured` → `manual_captions`。
  - `timing`、`subtitle_rules` 會影響 segment 的產出，加進 `params_sha256` 與步驟層的快取鍵（只有 segment；correct 的快取鍵不變）；chunk 層的快取鍵不含它們（模型的斷點不受影響），所以只改這兩個設定的重跑會重用已付費的 chunk，只重新計算時間。
- **R8 場景的表格**（2026-11-22，S5 發現，依 /goal 採保守預設，待 owner 追認）：`Without word timings a long cue is split at a silence` 的期望表格只有 `| 1 | 00:00:06.200 | silence |` 一列；依 `steps.md` 的規則，`#` 從 1 起連號時字幕的句數必須與表格列數相同，但這個場景切分之後一定是 2 句。保守預設：不改 `steps.md` 的部分表格語意（會讓其他場景的斷言變弱），在表格補上第 2 列 `| 2 | 00:00:12.000 | silence |`（第二段從靜音結束開始、到原本 cue 的 end，時間來源 `silence`）。這個修改在單獨的 commit，不同意時 revert 那個 commit 即可。
- **R10 的 CJK 範例**（2026-11-22，S5 發現，待 owner 決定）：`Subtitle rules are enforced per script` 的 `yt:zh001xxxxxx` 範例沒有 `Given a YouTube video "yt:zh001xxxxxx"`，假 yt-dlp 不認得這部影片，metadata 以 permanent 失敗，無法處理到 segment（`@pending-m0`，預設 tag 不執行）。`latin` 範例在暫定值下通過。建議在 Scenario Outline 加一欄 `title`，並加上 `And a YouTube video "<ref>" titled "<title>"`（`yt:talk01xxxxx` 已在 Background 宣告，要確認重複宣告沒有副作用），或把 CJK 範例拆成獨立的場景。這次沒有修改。

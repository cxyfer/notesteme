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
- S6-Q1：轉換表矩陣（2026-11-22，M2 S6a 實作時決定，待 owner 確認）。`convert` 依目標地區選表：簡體 → 台灣繁體 `s2twp`、香港與澳門繁體 `s2hk`、其他繁體 `s2t`；台灣繁體 → 簡體 `tw2sp`、香港與澳門 `hk2s`、其他繁體 → 簡體 `t2s`；繁體地區之間：來源是台灣或香港與澳門時，先用來源地區的反向字典（`TWVariantsRev`、`HKVariantsRev`）轉成 OpenCC Standard 再套用目標地區的字典（`hk2tw`、`tw2hk`，上游沒有這兩份設定，由 `zhconv` 組合），來源地區未知才直接用 `t2tw`、`t2hk`（它們的輸入是 Standard 字形，直接套用會漏掉「紅綫」、「屋裡」這類來源地區的字形）。`localize` 的前置轉換只轉字形、不做詞彙轉換：簡體 → 台灣繁體用 `s2tw`（TRN-R9 的場景要求 prompt 中是「這個視頻的質量很好」，用 `s2twp` 會被轉成「影片」），台灣繁體 → 簡體用 `tw2s`。TRN-R8 的文字（「依目標地區選轉換表」）不必修改；TRN-R9 建議補一句「先用不含詞彙轉換的表」。實作是 `zhconv.TableFor`，矩陣有單元測試。
- S6-Q3：沒有 script 的中文，與「相同語言」的擴充（2026-11-22，M2 S6a 實作時決定，待 owner 確認）。原文語言是 `zh`（沒有 script、也沒有可推定的地區）時，用字幕文字的字元判斷簡繁（`zhconv.DetectScript`：只在 `TSCharacters` 鍵中的字算繁體、只在 `STCharacters` 鍵中的字算簡體，可區分的字少於 3 個或兩者差距不到 2 倍時判斷不出來）；判斷不出來就當成需要 LLM 翻譯。兩邊都是中文、原文字形已知，但沒有對應的轉換表（例如 `zh-Hant-TW` → `zh-Hant`、`zh-Hans` → `zh-Hans-CN`、目標是沒有字形的 `zh`）時，也視為相同語言，`translate` 為 `skipped`（`same_language`）。這擴充了 TRN-R10 的「完全相同」，與 OUT-R5「只差字形時單語」一致。
- S6-Q4：`translate` 的 stage run 數（2026-11-22，M2 S6a 實作時決定，待 owner 確認）。S6a 維持一筆 stage run、多份 `translated` 產出（每個目標語言一份，`stage_runs.artifact_id` 指向第一個目標的產出）。TRN-R1 的「每個目標各一筆 stage run」留給 S6b：依語言拆開會動到 `latestRuns`、詳情、列表篩選、刪除、CLI 與 `stageIs`，與並行中的 PRF、LLM 切片重疊。
- S6-Q5：目標混合、而且 LLM 沒有設定（2026-11-22，M2 S6a 實作時決定，待 owner 確認）。例如 `targets: [zh-Hant-TW, en]` 而 `api` 沒有 provider：整個 `translate` 是 `skipped`（`not_configured`），連中文的轉換也不做。`executor: off` 優先於自動轉換（`off` 時中文原文不會自動轉換）；若 X1 改成預設 `off`，預設設定下中文影音也就不會自動轉換字形。S6b 再檢討，例如依語言拆開後只略過需要 LLM 的目標。
- S6-Q6（S6b 之後 TRN-R9、TRN-R11 已通過）：TRN-R9、TRN-R11 在 S6b 之前是 `failed`（2026-11-22，M2 S6a 實作時決定，待 owner 確認）。S1 之後 `the profile … is:` 生效，TRN-R9 用的是 `the owner sends "POST …/rerun" with:`，rerun 回 501 之後 `the worker runs until idle` 照常通過，接著 `request 1 to "anthropic" contains` 失敗；TRN-R11 的 `the owner ingests` 讓 `translate` 以「尚未實作」失敗。兩者都是「功能還沒做」，但不屬於 steps.md 4.4 允許的 pending 種類。選擇的做法：不為它們新增「依參數回傳 pending」的邏輯，接受它們在 S6b（LLM 翻譯）與平台字幕軌（TRN-R11）完成之前是 failed。另一個做法（未採用）：在 harness 加一條慣例，`the worker runs until idle` 緊接在回 501 的請求之後時回傳 pending。
- S6-Q7：依賴與授權（2026-11-22，M2 S6a 實作時決定，待 owner 確認）。採用自寫的 `internal/zhconv`，內嵌 OpenCC 官方字典（Apache-2.0，`ver.1.4.2`，附 LICENSE 與 NOTICE），不引用任何 Go port：現有的 port 都依賴 GPLv2 的 `cedar-go`，而 Docker image 會發布到 GHCR。見 ADR-0018。
- 明確設定 `stages.translate.executor: opencc` 時，`variant_strategy: localize` 的目標也只做確定性轉換（用含詞彙轉換的 `convert` 表），目標含非中文時 `translate` 以 `permanent` 失敗，訊息是「opencc 只能轉換中文字形（目標 en）。」（2026-11-22，M2 S6a 實作時決定，待 owner 確認）。
- opencc 的快取只看現行版本：同一份上游與同一組目標、字典版本時重用；設定 A → B → A 時不「恢復」舊版，而是重新轉換（opencc 不花錢），與 LLM 步驟的 PIPE-R5 恢復行為不同。快取鍵含 `zhconv.DataVersion`，字典更新後舊快取不再命中。
- `translate` 的輸入依 COR-R8 選：現行產出中 `corrected` > `segmented` > `normalized`，同一種變體取最近產生的。`translated` 產出不保留字詞級時間。
- S6-Q8：撤下不再要求的譯文，與語言標記的正規化（2026-11-22，M2 S6a 本地雙審後決定，待 owner 確認）。(1) `language.targets` 減少語言、或全部目標都與原文相同（`same_language`，不論是排入前或執行時才發現）時，不再要求的語言的現行 `translated` 產出被撤下：`superseded_by` 指向自己，保留歷史版本與檔案；不指向別的語言的新版本，因為 `PurgeOldArtifacts` 只在同一語言內修正 `superseded_by`。(2) 目標語言的標記正規化後才用於產出的 `lang`、快取鍵與取代（`zh_hant_tw` → `zh-Hant-TW`）。(3) `langtag` 保留 extlang 與 variant／extension／私用子標籤：`sr-Latn-ekavsk` 與 `sr-Latn-ijekavsk` 不是相同語言，`zh-cmn-Hans` 的 script 仍可解析。(4) translate 的輸入不採用現行 stage run 不是 `done` 的步驟（含 `normalize`）留下的產出，`Rerun` 在寫入前解析執行方式時，`from` 之後（含）的步驟也視為不是 `done`，交易內會用交易的讀取再解析一次（兩次之間 `normalize` 可能剛完成）。(5) translate 被略過（`same_language`、`not_configured`、`off`）時，也撤下不在目標集合內的現行譯文（`StageResolution.Wanted`）。(6) 現行譯文的檔案損毀或遺失時，opencc 的快取視為未命中，重新轉換。(7) 雙方明確指定不同 extlang（`zh-cmn` 與 `zh-yue`）是不同的語言，走 `TargetTranslate`，不只轉字形。已知限制：OpenCC 1.4.2 遇到字典沒有命中的完整表意文字描述序列（IDS，例如 `⿵门马`）會原樣保留，`zhconv` 目前逐字處理，會轉換其中的部件；字幕幾乎不會出現 IDS，留待之後處理。(8) 字形偵測（`DetectScript`）只讓會被轉成別的字的專用字計票，簡繁同形的字（只、回、家、了）不參與；明確指定漢字以外 script 的標記（`zh-Latn`、`zh-Bopo`）不推定簡繁、也不交給 OpenCC。(9) 最新成功的 `normalize`／`segment`／`correct` run 指向的產出，優先於建立順序最近的（同一變體可以同時有多個語言的現行產出）。字幕查詢（`lang`）接受等價的拼法。另有兩個已知限制留待之後：從 `translate` 重跑時上游（例如 `transcript`、`normalize`）仍在排程中，規劃器用 `OriginalLang` 選執行方式、先建任務，會因沒有輸入而失敗（所有步驟的重跑都有這個既有行為，應由 PIPE 統一處理「等待排程中的上游」）；獨立的中文語種標記（`yue`、`cmn`，Groq 會回傳 `yue`）不被視為中文，需要先在來源層對應成 `zh-yue`。第四輪本地審查另外提出、同樣留待之後的限制：(a) 前進（`apply`）時依同一批的 `segment` 快照解析整條略過鏈，不是每個下游 stage run 自己的快照；從 `translate` 重跑並單獨覆寫（例如 `executor: off`）而上游仍在排程中時，會被上游完成後的前進蓋掉，與上面「重跑時上游仍在排程」同屬 PIPE 的 D6 快照議題。(b) translate 被略過、而來源字幕換版（例如簡體換成英文，沒有設定 LLM）時，語言仍在目標清單內的舊譯文維持現行；要依來源紀錄（`parents`）撤下。(c) `stageContext` 為了判斷簡繁讀不到字幕（檔案損毀）時維持原樣、不回報錯誤：回報會讓損毀的字幕連「從 transcript 重跑」這種修復動作都被擋下，應該改由詳情的警告呈現。
- 以 `edited` 為輸入（2026-11-22，依 /goal 採 advisor 建議，待 owner 追認）：OUT-06 的目標是「翻譯與筆記建立在使用者的修正之上」。S7 匯入的 `edited` 變體目前不會被 translate 當輸入，OUT-R14 的「重新翻譯」實際會從 `corrected` 重翻。S6b 要改成：影音有 `edited` 時 translate 以它為輸入（來源紀錄的上游指向 `edited`）；correct 不以 `edited` 為輸入，因為 `edited` 已是人工校對後的結果。
- S6b-Q1：TRN-R10 場景的斷言加強（2026-11-22，依 /goal 採 advisor 建議，待 owner 追認；已修改 `.feature` 與 `steps.md`）。`Same source and target language skips translation` 原本只斷言 `skipped`，「沒有 provider → `not_configured`」也會讓它通過。新增句型 `Then the stage {string} of {string} is skipped with the reason {string}`（狀態 `skipped` 且 `block_reason` 相符），場景最後一步改成斷言 `same_language`。這是加強斷言，不是放寬。
- S6b-Q2：TRN-R1 場景的設定（2026-11-22，M2 S6b，依 /goal 採 advisor 建議，待 owner 追認；已修改 `.feature`）。`Several targets share one corrected transcript` 的 Background 處理方案沒有設定 `correct`，`processed up to "correct"` 時 `correct` 是 `skipped`（steps.md (2c) 也算處理到），所以 `corrected` 有 0 個版本，`has 1 version` 一定失敗。修法：這個場景自己宣告處理方案 `trn-corrected`（`correct` 與 `translate` 都是 `api`）、設為預設，並加上 `answers "correct" with "correct-echo"`，保留 `has 1 version` 與所有斷言；不改 Background（否則 R10、R11 等場景都會開始跑 `correct`）。這是補上場景缺少的前置條件，不是放寬斷言。
- S6b-Q3：`translate` 的 stage run 數（延續 S6-Q4，2026-11-22，依 /goal 採保守預設，待 owner 追認）。LLM 翻譯仍是一筆 stage run、每個目標一份 `translated` 產出：所有 LLM 目標的 chunk 放在同一個 stage run 的 `llm_chunks`，以全域編號區分（第一個 LLM 目標是 1..n1，第二個接著 n1+1..），每一列的 `plan_json` 帶著完整的目標清單。TRN-R1 的「每個目標各一筆 stage run」仍未做（理由同 S6-Q4）。被沿用（見 Q11）的目標不產生 chunk，全域編號會有空洞。已知限制：`POST /rerun` 的 `chunks` 以最近一次 stage run 的 chunk 列數（`LatestLLMChunkCount` 是 `COUNT(*)`）做上限檢查，有空洞時合法的較大編號會被 422 擋下（例如 zh 被沿用、ja 是第 4 到 6 個，只有 3 列，`chunks: [5]` 被拒），而空洞的編號通過檢查卻沒有作用；應改成以 chunk 的最大編號、或依目標指定 chunk。
- S6b-Q4：語言偵測（TRN-R5，2026-11-22，依 /goal 採保守預設，待 owner 追認）。規格沒有指定實作，採用純 Go 的 `internal/langdetect`：只看文字系統的份量（漢字、假名、諺文、泰文以字計，其他字母文字以詞計），目標文字系統的份量至少要是其他文字的一半；中文遇到假名達漢字的五分之一視為日文、目標指定簡繁而文字明確是另一種字形（`zhconv.DetectScript`）時不符；日文有 4 個以上漢字而沒有假名時視為中文。拉丁字母的語言之間（en、fr、de…）無法區分，只確認是拉丁字母；不認識的語言、沒有任何字母的文字一律視為符合。不引入統計模型或大型相依。
- S6b-Q5：長度比與「與原文相同」的細節（2026-11-22，依 /goal 採保守預設，待 owner 追認）。字元數以 Unicode 字元（rune）計，含標點與空白，句群先拿掉切分標記；原文不超過 3 個字元時不檢查長度比。專有名詞：原文（去掉前後標點）等於詞彙表的詞條，或是不超過 5 個、每個都以大寫字母或數字開頭的詞（`Kafka.`）。與原文相同而可以豁免時，語言與長度比也不檢查。檢查順序：空的 → 與原文相同 → 語言 → 長度比。
- S6b-Q6：`length_ratio` 下限 0.3 對英翻中太緊（2026-11-22，M2 S6b，待 owner 決定）。依規格字面以字元數計算時，合理的英翻中常常低於 0.3，例如「It worked well today.」（21 字元）→「今天很順利。」（6 字元）是 0.29，會觸發驗證重試，重試用盡就退回原文，既花錢又降低品質。預設：照規格字面實作。替代方案：(a) 中日韓字加權（例如一字算 2 到 3 個字元）；(b) 改用 LLM 規格 L2 的 token 估算；(c) 依語言對調整預設範圍（英 → 中 0.15 到 1.5）。
- S6b-Q7：句群（TRN-R2）的細節（2026-11-22，依 /goal 採保守預設，待 owner 追認）。句末標點是 `.!?。！？…‼⁇⁈⁉`（忽略結尾的空白與右引號、右括號）；整份字幕完全沒有句末標點（例如沒有標點的 ASR）時不組句群；句群最多 5 個 cue，更長的一句話依序切成多個句群；一句話的任何 cue 含有切分標記 `⟨|⟩`（不可信的字幕原文）時，這句話全部逐 cue 翻譯。送給模型的單位是 `{i, text}`，`i` 是句群第一個 cue 的編號，句群的 `text` 以標記串接；chunk 的切分、重疊與前文以翻譯單位計，不是以 cue 計。譯文切開後任何一段是空的，與標記數不符一樣改成逐 cue 翻譯（TRN-R4）；逐 cue 的補送有自己的驗證重試，不計入第一個請求的次數，限流上一律透支、不等待（等待會讓整個 chunk 放回佇列而重送第一個請求）。localize 一律一個 cue 一個單位。
- S6b-Q8：退回與警告（TRN-R6、R7，2026-11-22，依 /goal 採保守預設，待 owner 追認）。驗證重試用盡時，最後一次符合 schema 的輸出中通過驗證的句子照樣採用，只有沒通過的句子退回（`fallbacks[{cue, reason, chunk}]`）；輸出不是 JSON 或不符 schema 時整個 chunk 退回。localize 退回 OpenCC 轉換後的文字（字形已是目標），translate 退回原文。閱讀速度以字母與數字計（標點、空白不算），上限依 plan 第 8.5 節：中文、日文、韓文 9 字／秒，其他 17 字元／秒；超過時記 `warnings[{cue, reason: reading_speed, cps}]`，退回的句子不檢查。有退回的譯文不寫 `cache_key`，下一次重跑會重新送出。
- S6b-Q9：localize（TRN-R9，2026-11-22，依 /goal 採保守預設，待 owner 追認）。相似度是字元層級 normalized Levenshtein，門檻是常數 0.3（`stages.translate` 不接受 `min_similarity`，PRF 的檢查會拒絕；場景中「這個視頻的質量很好」→「這部影片的品質很好」是 0.44）；之後與 COR 的相似度函式合併。prompt 是 `localize-<目標的小寫標記>`，在 plan 時以 `@default` 解析（記在 chunk 的 `plan_json`，chunk 與 assemble 用同一版），不使用 `stages.translate.prompt`；找不到時，簡體退回 `localize-zh-hans-cn`、沒有地區的繁體退回 `localize-zh-hant-tw`，仍找不到就以 permanent 失敗，訊息建議新增 prompt 或改用 `convert`。沒有新增內建 prompt：場景用到的 `localize-zh-hant-tw` 已存在，`BuiltinIDs` 的數量（5）是 PRM 的契約；香港、澳門繁體的 localize 目前會失敗。
- S6b-Q10：沿用平台的人工字幕軌（TRN-R11，2026-11-22，依 /goal 採保守預設，待 owner 追認）。只在需要 LLM 的目標（translate、localize）上判斷，而且只在影音現行的原始字幕來自同一平台的人工字幕軌（`youtube-manual`、`bilibili-cc`）時才重新 probe 一次；probe 失敗時照常交給 LLM（記 log）。字幕軌語言與目標相同，或兩者都是中文而字形、地區相同（`zh-TW` 與 `zh-Hant-TW`）時採用。每一句平台字幕歸到時間重疊最多的原文 cue（同樣多取較早的），同一個 cue 的多句依時間串接（中日韓不加空白）；沒有任何重疊的 cue 保留原文，記在 `fallbacks`（`no_overlap`）。stage run 的 executor 仍是 `api`，產出的來源紀錄 `executor` 是 `platform`、`source` 是平台的人工字幕來源。LLM 沒有設定時 `translate` 仍是 `skipped`（`not_configured`，S6-Q5），不會改用平台字幕軌。
- S6b-Q11：LLM 翻譯的快取（2026-11-22，依 /goal 採保守預設，待 owner 追認）。每個目標有自己的快取鍵（輸入、固定前綴、prompt、provider、model、參數），現行譯文的來源紀錄 `cache_key` 相同而且檔案完好時直接沿用，不建立新版本；與 opencc 相同只看現行版本，設定 A → B → A 時不恢復舊版，但 chunk 層的快取（LLM-R8）仍會命中，不會重複付費。
- 以 `edited` 為輸入的實作（2026-11-22，M2 S6b）：translate（LLM 與 opencc）的輸入先依 COR-R8 選出，再看有沒有語言相同、`parents` 含有這份輸入的現行 `edited`，有就改用它（來源紀錄的 `derived_from` 指向 `edited`）；由舊的輸入衍生的 `edited` 不採用（例如從 correct 重跑產生了新的 corrected）。correct 不經過這個選擇，不以 `edited` 為輸入。

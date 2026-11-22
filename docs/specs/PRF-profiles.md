# PRF：處理方案與設定覆寫

- 狀態：draft
- 里程碑：M2（PRF 切片實作處理方案；ING-R7 隨 PRF 切片通過）
- 相關：plan 第 2、7 節；ADR-0002；UI [處理方案](../ui/pages.md#8-處理方案)
- 驗收場景：[`features/PRF-profiles.feature`](../../features/PRF-profiles.feature)

## 目的

把一套 pipeline 設定存成可以重複套用的處理方案，並用清楚的覆寫順序決定每一次執行最後生效的設定。

## User stories

| ID | Story |
|---|---|
| PRF-01 | As the owner, I want to save a set of pipeline settings as a named profile, so that I can apply the same setup to many items. |
| PRF-02 | As the owner, I want global, profile, channel and per-run settings layered in a fixed order, so that I can predict the settings in effect. |
| PRF-03 | As the owner, I want to choose per LLM stage whether it runs through the API, an agent or not at all, so that I can mix cost and convenience. |
| PRF-04 | As the owner, I want invalid profiles rejected with the exact field at fault, so that a broken profile never reaches the queue. |

## 規則

| ID | 規則 |
|---|---|
| PRF-R1 | 處理方案包含：`stages.<stage>.{executor, provider, model, prompt, ...}`、`language.*`、`timing.*`、`llm.*`、`agent.*`、`subtitle_rules.*`、`outputs`、`glossary`（字串陣列，不可以有空字串；它與 COR 的 `/api/glossaries/{scope}/{id}` 的關係見 COR 規格的 C2：方案層的詞彙，與全域、頻道詞彙表合併、去重）。 |
| PRF-R2 | 覆寫順序：全域 → 處理方案 → 頻道 → 單次執行。map 逐層深度合併；list 整個取代。 |
| PRF-R3 | 處理方案每次儲存都建立新版本；影音記錄使用的處理方案版本。修改處理方案不影響已完成的步驟，重新處理時才使用新版本。 |
| PRF-R4 | 儲存時驗證整份內容：未知的欄位、型別錯誤、引用不存在的 provider 或 prompt，都回傳 422 並指出欄位路徑。 |
| PRF-R5 | `executor: api` 必須指定 `provider` 與 `model`；`executor: agent` 與 `executor: off` 不需要。`opencc` 只能用於 `translate`。 |
| PRF-R6 | `executor: off` 時，下游步驟改用上游的產出（ADR-0002）。 |
| PRF-R7 | 內建的 `default` 處理方案不能刪除。正在被使用的處理方案刪除時回傳 409，並列出使用中的訂閱（M4）或預設設定。 |
| PRF-R8 | 頻道層的覆寫存在頻道上，套用到該頻道之後的所有執行。 |

## 介面

- API：`GET/POST/PATCH/DELETE /api/profiles`、`GET /api/profiles/:id/versions`、`POST /api/profiles/:id/default`、`GET/PATCH /api/channels/:id/overrides`、`GET /api/items/:id/runs`（含生效設定）
- M3 的欄位（M3-S0 起接受，PRF-R4 的檢查與欄位路徑同 M2；語意由各規格的切片實作）：

  | 欄位 | 型別與範圍 | 規格 |
  |---|---|---|
  | `stages.sponsorblock.enabled` | bool，預設 `true` | SPB-R1 |
  | `stages.sponsorblock.categories` | 字串陣列，元素是 `sponsor`、`selfpromo`、`interaction`、`intro`、`outro`、`preview`、`music_offtopic`、`filler`；預設前三個 | SPB-R3 |
  | `stages.chapters.{executor, provider, model, prompt}` | executor 是 `api`（預設）、`agent`、`off` | CHAP-R8 |
  | `stages.chapters.max_chapters` | 整數 1～100，預設 20 | CHAP-R3、H4 |
  | `stages.frames.{executor, provider, model, prompt}` | executor 是 `rules`、`api`、`agent`、`off`（預設）；`agent` 回 422「選圖還不支援交給 Agent。」 | FRM-R1 |
  | `stages.frames.{scene_threshold, dedupe_threshold}` | 數字 0～1 | FRM-R2、R3 |
  | `stages.frames.{min_interval_s, dedupe_window, max_frames}` | 整數，1～3600、1～100、1～100 | FRM-R2～R4 |
  | `stages.frames.per_minute` | 數字，大於 0、不超過 60 | FRM-R4 |
  | `stages.frames.hint_words` | 字串陣列 | FRM-R5 |
  | `stages.note.{executor, provider, model, prompt}` | executor 是 `api`（預設）、`agent`、`off` | NOTE-R1 |
  | `stages.note.engine` | `pipeline`（預設）或 `gemini`；`gemini` 時 provider 的 type 必須是 `gemini` | GEM-R1 |
  | `stages.note.{template, lang, include_translation, map_reduce_after}` | 字串、BCP-47、bool、期間 | NOTE-R5、R6、R11 |
  | `stages.note.gemini.max_duration` | 期間 | GEM-R3 |
  | `outputs[].target: vault` | 欄位只有 `vault`（必填、設定檔定義的 id）、`formats`（可以是空陣列）、`lang`、`folder`（相對、不含 `..`）、`on_user_change`、`hooks`（設定檔定義的 id）；`path`、`style` 是「不認識的欄位。」 | VAULT-R1、R2、R10 |

## 不在範圍內

- 修改處理方案前提示受影響的訂閱（M4）。

## 待決問題

- X1：`default` 處理方案的 LLM 步驟預設值（2026-11-21，M2 S0 切片；2026-11-22 owner 同意）。`segment`、`correct`、`translate` 預設 `executor: api`、`prompt: <stage>-default`，沒有 provider 或 model 時為 `skipped`（`not_configured`）；`outputs` 預設 `[]`，`publish` 因此 `skipped`（`no_outputs`）。沒設定就略過，不改成預設 `off`。
- S0 只檢查 `stages` 的步驟名稱（`segment`、`correct`、`translate`）、`executor` 的值（`batch` 回 422，屬於 M4）、`provider`、`model`、`prompt` 是字串，以及 `outputs` 是陣列；步驟專屬欄位（例如 `min_similarity`）不拒絕。PRF-R4 的完整驗證與欄位路徑屬於 PRF 切片（2026-11-21，M2 S0 切片設計時決定）。
- X5：設定預設處理方案用 `POST /api/profiles/:id/default`（`admin`），和 PRM 的 `POST /api/prompts/:id/default` 對稱（2026-11-21，M2 S0 切片設計時決定，場景的 `the default profile is` 用到）。
- D6：頻道層在 `metadata` 完成時才併入（2026-11-22，M2 S1/S2 設計時決定，owner 同意）。YouTube 與 Podcast 的頻道要到 `metadata` 完成才知道，所以快照在新增與重跑時先取（記下各層，不含頻道層）；`metadata` 完成時，若影音的頻道與快照記下的不同，就用記下的各層加上目前的頻道覆寫重建快照，並把同一批還沒開始的下游步驟改綁到新快照。M1 的舊快照不重建。替代方案：新增時用 yt-dlp 探測頻道（播放清單變成 N 次呼叫，新增不再離線），或每一步前進時都重建（處理途中改頻道覆寫，同一部影音前後步驟的設定不一致）。
- D8：完整性檢查（PRF-R5）只套用在處理方案文件中有寫出來的步驟（2026-11-22，M2 S1/S2 設計時決定，owner 同意）。全域、頻道與單次覆寫是部分的設定層，不檢查完整性；沒有 provider 或 model 的 `api` 步驟，執行時依 X1 記為 `skipped`（`not_configured`）。否則 PRF-R4 範例 1 的 422 與 LIB 只帶 provider、model 的重跑覆寫會互相矛盾。
- F4：頻道覆寫與處理方案的 PATCH 用 RFC 7396 merge patch（2026-11-22，M2 S1/S2 設計時決定）。`null` 代表從這一層刪除該鍵，陣列與純量整個取代；存好的各層不含 `null`，輸入的完整文件含 `null` 回 422。
- D10：provider 目錄是內建的 `anthropic`、`openai`，加上 S3 之後設定檔 `llm.providers` 登記的（2026-11-22，M2 S1/S2 設計時決定）。S1 只依賴 `ProviderCatalog` 介面；S3 之前，引用其他 provider 的處理方案會回 422。
- D14：頻道覆寫的端點是 `GET/PATCH /api/channels/{id}/overrides`，不提供 PUT（2026-11-22，M2 S1/S2 設計時決定）。`{id}` 可以是頻道的 UUID 或 `<platform>:<external_id>`（例如 `youtube:UCfixture000000000000000`）；用 natural key 做 PATCH 時，頻道不存在就建立（`title` 留空，等 `metadata` 補上），因為場景在影音新增之前就設定頻道覆寫。
- D16：全域設定可以 `PUT /api/settings/{section}` 的區段是 `language`、`timing`、`llm`、`agent`、`subtitle_rules`、`stages`、`supadata`，body 都是物件（2026-11-22，M2 S1/S2 設計時決定）。`outputs`、`glossary` 是陣列，不開放全域設定。
- F1：PRF-R2「Each layer」場景在 `the profile "standard" is:` 之後加 `And the default profile is "standard"`（2026-11-22，owner 同意，已修改）。新增時用的是預設處理方案，原本 `timing.split_timing` 會是 `when_needed` 而不是 `forbid`。
- F2：PRF-R3「Editing a profile…」同樣加 `And the default profile is "standard"`（2026-11-22，owner 同意，已修改）。這樣把 PATCH 誤當成重跑的實作會讓版本數變成 2，斷言才驗證得到 PRF-R3。
- F5：PRF-R8 與 PRF-R2「Each layer」在 `When the owner ingests …` 之後加 `And the worker runs until idle`（2026-11-22，owner 同意，已修改）。頻道要到 `metadata` 完成才知道（D6），Then 不應該改變狀態，新增時也不探測頻道。
- G1：PRF-R1 補上 `glossary`（字串陣列，不可以有空字串）（2026-11-22，owner 同意，已修改）。COR 的頻道覆寫用了頂層的 `glossary`；它與 COR 的 `/api/glossaries/{scope}/{id}` 的關係由 COR 切片決定。
- O-4：本文件開頭改成「處理方案在 M2（PRF 切片）實作；ING-R7 隨 PRF 切片通過」（2026-11-22，owner 同意，已修改）；ING 的 ING-R7 待決問題已關閉。
- M3-S0：M3 欄位的預設值（`sponsorblock.enabled: true`、`chapters`／`note` 的 `api`、`frames` 的 `off`）只在讀取時補上，不寫進快照（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）。寫進快照會改變每個既有處理方案的快照 sha 與快取鍵；快照只記錄處理方案實際寫出的值。
- M3-S0：PRF-R5 的完整性改看步驟的預設 executor（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：`frames` 預設 `off`，處理方案只寫 `stages.frames.scene_threshold` 時不要求 provider；`chapters`、`note` 預設 `api`，同 M2 的 LLM 步驟。
- M3-S0：`stages.note.template` 只檢查是非空字串（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）；`note_templates` 資料表屬於 NOTE 的切片，在那之前不檢查模板是否存在。`prompt` 照 PRM 的規則檢查是否存在與類型，所以 `chapters-default`、`note-default`、`frames-default`、`note-gemini-default` 加進 prompt 庫之前，引用它們的處理方案會回 422（CHAP、NOTE 的 Background 在閘門解除後會停在這裡，由實作切片補上內建 prompt）。


# PRF：處理方案與設定覆寫

- 狀態：draft
- 里程碑：M2（M1 只有內建的 `default` 處理方案與單次覆寫）
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
| PRF-R1 | 處理方案包含：`stages.<stage>.{executor, provider, model, prompt, ...}`、`language.*`、`timing.*`、`llm.*`、`agent.*`、`subtitle_rules.*`、`outputs`。 |
| PRF-R2 | 覆寫順序：全域 → 處理方案 → 頻道 → 單次執行。map 逐層深度合併；list 整個取代。 |
| PRF-R3 | 處理方案每次儲存都建立新版本；影音記錄使用的處理方案版本。修改處理方案不影響已完成的步驟，重新處理時才使用新版本。 |
| PRF-R4 | 儲存時驗證整份內容：未知的欄位、型別錯誤、引用不存在的 provider 或 prompt，都回傳 422 並指出欄位路徑。 |
| PRF-R5 | `executor: api` 必須指定 `provider` 與 `model`；`executor: agent` 與 `executor: off` 不需要。`opencc` 只能用於 `translate`。 |
| PRF-R6 | `executor: off` 時，下游步驟改用上游的產出（ADR-0002）。 |
| PRF-R7 | 內建的 `default` 處理方案不能刪除。正在被使用的處理方案刪除時回傳 409，並列出使用中的訂閱（M4）或預設設定。 |
| PRF-R8 | 頻道層的覆寫存在頻道上，套用到該頻道之後的所有執行。 |

## 介面

- API：`GET/POST/PATCH/DELETE /api/profiles`、`GET /api/profiles/:id/versions`、`POST /api/profiles/:id/default`、`GET/PATCH /api/channels/:id/overrides`、`GET /api/items/:id/runs`（含生效設定）

## 不在範圍內

- 修改處理方案前提示受影響的訂閱（M4）。

## 待決問題

- [O] X1：`default` 處理方案的 LLM 步驟預設值（2026-11-21，M2 S0 切片，需要 owner 同意）。S0 先依選項 C 實作：`segment`、`correct`、`translate` 預設 `executor: api`、`prompt: <stage>-default`，沒有 provider 或 model 時為 `skipped`（`not_configured`）；`outputs` 預設 `[]`，`publish` 因此 `skipped`（`no_outputs`）。需要 owner 決定：預設是這樣（沒設定就略過），還是改成預設 `off`。若改成 `off`，只要改預設值與原因（`off`），pipeline 的結構與所有場景的結論不變，因為兩者都是「全部 skipped、沒有 message」。另外，場景中 `processed up to` 對 `segment`、`correct`、`translate`、`publish` 目前一律 pending（steps.md 4.4 規則 (2c)），等這項決定後才能定義它們的語意。
- S0 只檢查 `stages` 的步驟名稱（`segment`、`correct`、`translate`）、`executor` 的值（`batch` 回 422，屬於 M4）、`provider`、`model`、`prompt` 是字串，以及 `outputs` 是陣列；步驟專屬欄位（例如 `min_similarity`）不拒絕。PRF-R4 的完整驗證與欄位路徑屬於 PRF 切片（2026-11-21，M2 S0 切片設計時決定）。
- X5：設定預設處理方案用 `POST /api/profiles/:id/default`（`admin`），和 PRM 的 `POST /api/prompts/:id/default` 對稱（2026-11-21，M2 S0 切片設計時決定，場景的 `the default profile is` 用到）。
- D6：頻道層在 `metadata` 完成時才併入（2026-11-22，M2 S1/S2 設計時決定，待 owner 確認）。YouTube 與 Podcast 的頻道要到 `metadata` 完成才知道，所以快照在新增與重跑時先取（記下各層，不含頻道層）；`metadata` 完成時，若影音的頻道與快照記下的不同，就用記下的各層加上目前的頻道覆寫重建快照，並把同一批還沒開始的下游步驟改綁到新快照。M1 的舊快照不重建。替代方案：新增時用 yt-dlp 探測頻道（播放清單變成 N 次呼叫，新增不再離線），或每一步前進時都重建（處理途中改頻道覆寫，同一部影音前後步驟的設定不一致）。
- D8：完整性檢查（PRF-R5）只套用在處理方案文件中有寫出來的步驟（2026-11-22，M2 S1/S2 設計時決定，待 owner 確認）。全域、頻道與單次覆寫是部分的設定層，不檢查完整性；沒有 provider 或 model 的 `api` 步驟，執行時依 X1 記為 `skipped`（`not_configured`）。否則 PRF-R4 範例 1 的 422 與 LIB 只帶 provider、model 的重跑覆寫會互相矛盾。
- F4：頻道覆寫與處理方案的 PATCH 用 RFC 7396 merge patch（2026-11-22，M2 S1/S2 設計時決定）。`null` 代表從這一層刪除該鍵，陣列與純量整個取代；存好的各層不含 `null`，輸入的完整文件含 `null` 回 422。
- D10：provider 目錄是內建的 `anthropic`、`openai`，加上 S3 之後設定檔 `llm.providers` 登記的（2026-11-22，M2 S1/S2 設計時決定）。S1 只依賴 `ProviderCatalog` 介面；S3 之前，引用其他 provider 的處理方案會回 422。
- D14：頻道覆寫的端點是 `GET/PATCH /api/channels/{id}/overrides`，不提供 PUT（2026-11-22，M2 S1/S2 設計時決定）。`{id}` 可以是頻道的 UUID 或 `<platform>:<external_id>`（例如 `youtube:UCfixture000000000000000`）；用 natural key 做 PATCH 時，頻道不存在就建立（`title` 留空，等 `metadata` 補上），因為場景在影音新增之前就設定頻道覆寫。
- D16：全域設定可以 `PUT /api/settings/{section}` 的區段是 `language`、`timing`、`llm`、`agent`、`subtitle_rules`、`stages`、`supadata`，body 都是物件（2026-11-22，M2 S1/S2 設計時決定）。`outputs`、`glossary` 是陣列，不開放全域設定。
- [O] F1：PRF-R2「Each layer」場景建立了 `standard`，但新增時用的是預設處理方案 `default`，所以 `timing.split_timing` 會是 `when_needed` 而不是 `forbid`（2026-11-22，M2 S1/S2 設計時決定）。建議在 `the profile "standard" is:` 之後加 `And the default profile is "standard"`（句型已存在）。依規格優先要修改 `.feature`，需要 owner 同意；在那之前這個場景會 failed。
- [O] F2：PRF-R3「Editing a profile…」的影音用的是 `default`，PATCH `standard` 本來就不會影響它，斷言驗證不到 PRF-R3（2026-11-22，M2 S1/S2 設計時決定）。建議同樣加上 `And the default profile is "standard"`，這樣把 PATCH 誤當成重跑的實作會讓版本數變成 2。需要 owner 同意修改 `.feature`。
- [O] F5：PRF-R8 與 PRF-R2「Each layer」在新增後立刻斷言頻道層，但頻道要到 `metadata` 完成才知道（2026-11-22，M2 S1/S2 設計時決定）。建議（F5-a）在 `When the owner ingests …` 之後加 `And the worker runs until idle`。不建議讓 `the effective config …` 先推動 worker（Then 不應該改變狀態），也不建議新增時探測頻道（見 D6）。需要 owner 同意修改 `.feature`；在那之前 PRF-R8 會 failed。
- [O] G1：COR 的頻道覆寫用了頂層的 `glossary`（`features/COR-correct.feature`），但 PRF-R1 的清單沒有 `glossary`（2026-11-22，M2 S1/S2 設計時決定）。S1 先接受 `glossary`（字串陣列，不可以有空字串），避免場景在寫入設定時就失敗；PRF-R1 要補上 `glossary`，它與 COR 的 `/api/glossaries/{scope}/{id}` 的關係由 COR 切片決定。
- [O] O-4：本文件開頭寫「M1 只有內建的 `default` 處理方案與單次覆寫」，但 ING-R7（M1）已經用了 `standard`（2026-11-22，M2 S1/S2 設計時決定）。建議改成「處理方案在 M2（PRF 切片）實作；ING-R7 隨 PRF 切片通過」，並關閉 handoff 第 7 節的這個待決問題；需要 owner 同意。

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

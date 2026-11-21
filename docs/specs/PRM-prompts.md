# PRM：Prompt 庫

- 狀態：draft
- 里程碑：M2
- 相關：plan 第 8.3 節；UI [Prompt 庫](../ui/pages.md#7-prompt-庫)
- 驗收場景：[`features/PRM-prompts.feature`](../../features/PRM-prompts.feature)

## 目的

校對只靠 LLM，所以 prompt 是改善結果最主要的地方。prompt 要有版本、能試跑、能比較，而且改版不會悄悄影響正在使用它的處理方案。

## User stories

| ID | Story |
|---|---|
| PRM-01 | As the owner, I want every prompt change saved as a new version, so that I can always go back and know which version produced a result. |
| PRM-02 | As the owner, I want to try a prompt on part of a real item before using it, so that I can judge it without changing any results or paying for a full run. |
| PRM-03 | As the owner, I want profiles to keep using the version they were set to until I promote a new one, so that experiments never change my everyday output. |
| PRM-04 | As the owner, I want mistakes in prompt variables caught when I save, so that a typo never breaks a run later. |

## 規則

| ID | 規則 |
|---|---|
| PRM-R1 | prompt 的類型：`summary`、`chapters`、`segment`、`correct`、`translate`（包含 `localize-*`）、`frames.select`、`note`。 |
| PRM-R2 | prompt 版本建立後不能修改；儲存變更一律建立新版本，版本號遞增，並計算 `sha256`。 |
| PRM-R3 | 可用的變數：`title`、`channel`、`description`、`chapters`、`glossary`、`source_lang`、`target_lang`。儲存時發現未定義的變數就回傳 422，並列出變數名稱。 |
| PRM-R4 | 內建 prompt（`segment-default`、`correct-default`、`translate-default`、`localize-zh-hant-tw`、`localize-zh-hans-cn`）不能修改，只能另存為新的 prompt。 |
| PRM-R5 | 處理方案引用 prompt 時記錄版本號。prompt 有新版本時，處理方案不會自動改用；「設為預設」才會更新預設版本，引用 `default` 的處理方案從下一次執行開始改用。 |
| PRM-R6 | 試跑對指定影音的指定時間範圍執行 prompt，回傳輸出、驗證結果與預估花費；不寫入任何產出，也不改變步驟狀態。 |
| PRM-R7 | 比較兩個版本時，回傳 prompt 內容的差異；指定影音與範圍時，另外回傳兩者在同一段字幕上的輸出。 |
| PRM-R8 | 刪除 prompt 時，如果有處理方案正在使用，回傳 409 並列出這些處理方案。 |

## 介面

- API：`GET/POST /api/prompts`、`DELETE /api/prompts/:id`（PRM-R8）、`GET /api/prompts/:id/versions`、`POST /api/prompts/:id/versions`、`POST /api/prompts/:id/default`、`POST /api/prompts/playground`、`POST /api/prompts/:id/compare`
- MCP：prompts capability（見 MCP）

## 不在範圍內

- `summary`、`chapters`、`frames.select`、`note` 的內建 prompt（M3、M4 隨各步驟提供）。

## 待決問題

- D11：prompt 引用格式是 `<id>`、`<id>@default`、`<id>@<N>`（N ≥ 1）（2026-11-22，M2 S1/S2 設計時決定）。`<id>` 等同 `<id>@default`，跟隨「設為預設」；`@N` 固定版本。步驟與 prompt 的類型必須相符（segment、correct、translate；`localize-*` 屬於 translate）。版本在新增或重跑取快照時解析，只解析會執行的 LLM 步驟，寫進快照該步驟的 `prompt_version`、`prompt_sha256`；解析失敗時留空，不讓新增失敗，由執行該步驟時以 `permanent` 失敗。
- D12：內建 prompt（PRM-R4 的 5 個）放在程式內（`internal/prompts/builtin/<id>/v<N>.md`，embed），不寫入資料庫（2026-11-22，M2 S1/S2 設計時決定）。使用者的 prompt 存在資料庫，id 不可以和內建的相同。內建的不能新增版本、不能設預設、不能刪除，都回 409 `builtin_prompt`。M2 發布前可以直接修改 v1 的內容，發布之後改內容要新增 v2。
- D13：刪除 prompt 是硬刪除（2026-11-22，M2 S1/S2 設計時決定，待 owner 確認）。`used_by` 依序列出引用它的處理方案名稱（依字母排序，只看目前版本）、`settings`（全域 stages 有引用時）、`channel:<platform>:<external_id>`。PRM-R8 只要求處理方案；多列出其他來源是為了避免懸空的引用，場景的 `used_by: ["standard"]` 是前綴比對，仍然成立。
- [O] P1：PRM-R7 的 compare 請求 body 沒有 `provider` 與 `model`（2026-11-22，M2 S1/S2 設計時決定）。影音用的是 `default` 處理方案（沒有 provider），compare 無法從快照取得，一定失敗。建議 body 加上 `"provider": "anthropic", "model": "fixture-model"`，與 playground 一致。需要 owner 同意修改 `.feature`。
- [O] P6：PRM-R5「處理方案引用 prompt 時記錄版本號」沒有說不帶版本號的引用（例如 `correct-default`）是建立時固定版本，還是跟隨預設（2026-11-22，M2 S1/S2 設計時決定）。依 PRM-03「until I promote」，目前實作 `<id>` 等同 `<id>@default`，跟隨預設；實際用到的版本記在快照與來源紀錄（見 D11）。需要 owner 確認。
- P7：刪除 prompt 的 `used_by` 不涵蓋影音層的覆寫（2026-11-22，M2 S1/S2 審查時發現，待 owner 確認）。新增時帶的 `overrides`（存在 `items.overrides_json`，重跑會沿用）可以引用 prompt，刪除後重跑會在執行時以 permanent 失敗。另外 `used_by` 的檢查與刪除不在同一個交易，檢查之後才建立的引用也會懸空。目前接受（D13 的取捨）；若要處理，可以在刪除前掃描 `items.overrides_json`，並把檢查與刪除放進同一個寫入交易。
- P8：頻道層在 `metadata` 完成時改綁快照（PRF D6），處理方案、全域各層取自新增時記下的內容，但 `<id>` 與 `<id>@default` 的 prompt 引用會以改綁當下的預設版本重新解析（2026-11-22，M2 S1/S2 審查時發現，待 owner 確認）。PRM-R5 只說「從下一次執行開始」，這些步驟還沒開始，所以不算違規，但和「各層在新增時凍結」不一致。

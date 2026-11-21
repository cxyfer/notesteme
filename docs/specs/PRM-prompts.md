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

- 無。

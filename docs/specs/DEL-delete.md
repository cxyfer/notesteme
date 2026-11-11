# DEL：刪除影音與垃圾桶

- 狀態：draft
- 里程碑：M1（輸出檔的處理在 M2，因為輸出目標從 M2 開始）
- 相關：plan 第 5.1、14、21 節；ADR-0007、ADR-0010、ADR-0014；UI [媒體庫](../ui/pages.md#3-媒體庫)、[影音詳情](../ui/pages.md#4-影音詳情)
- 驗收場景：[`features/DEL-delete.feature`](../../features/DEL-delete.feature)

## 目的

定義刪除一部影音時，哪些資料會被清掉、哪些會留下、什麼時候清。刪除先進垃圾桶，避免誤刪後要重付轉錄與 LLM 的費用。永久刪除後，`/data` 中不留這部影音的內容，但花費紀錄保留，預算才會正確。Notesteme 寫到外部的檔案，以及使用者自己的檔案，預設都不動。

## User stories

| ID | Story | 里程碑 |
|---|---|---|
| DEL-01 | As the owner, I want deleted items to go to a trash first, so that a mistake does not cost me the transcription and LLM fees again. | M1 |
| DEL-02 | As the owner, I want to see what will be lost before I delete an item, so that I decide with the facts in front of me. | M1 |
| DEL-03 | As the owner, I want a permanently deleted item to leave nothing behind in my data directory except my spending records, so that my disk is freed and my budget stays correct. | M1 |
| DEL-04 | As the owner, I want files written to my vault or media folders left alone unless I ask, and never deleted if I edited them, so that deleting an item cannot destroy my own work. | M2 |
| DEL-05 | As the owner, I want deleted items not to come back on their own, so that deleting something means it stays gone. | M1 |
| DEL-06 | As the owner, I want only admin tokens to be able to delete, with no MCP tool for it, so that an agent misled by a transcript cannot wipe my library. | M1 |
| DEL-07 | As the owner, I want to delete, restore and empty the trash from the CLI, so that I can script cleanups. | M1 |

## 資料的處理方式

| 資料 | 移到垃圾桶時 | 永久刪除時 |
|---|---|---|
| `items`、`stage_runs`、`source_probes` | 保留，標記 `deleted_at` | 刪除 |
| `tasks` | 執行中的取消（PIPE-R15），其餘移除 | — |
| 所有版本的產出與 `/data/artifacts` 的檔案（含 `pinned`） | 保留 | 刪除 |
| 平台影音的媒體快取（`/data/media`） | **立即刪除**，可以重新下載 | — |
| 上傳的原始檔（`/data/uploads`） | 保留 | 刪除 |
| 全文索引（`cues_fts`） | 不再出現在搜尋結果 | 刪除 |
| `outputs` 紀錄 | 保留 | 刪除 |
| 輸出目標寫出的檔案（vault、sidecar、folder） | 預設保留；指定時刪除沒被改過的（DEL-R9） | 不動 |
| 這部影音還沒審核的術語候選 | 保留 | 刪除 |
| 這部影音的 `events` | 新增 `item.deleted` | 刪除，改寫一筆 `item.purged` |
| `cost_ledger` | 保留 | **保留**：`item_id` 改為空，另存標題 |
| 已核可的術語、`channels`、`config_snapshots` | 保留 | 保留；`config_snapshots` 沒有任何引用時才清除 |
| inbox 與 `media_roots` 中使用者自己的檔案 | 不動 | 不動 |
| 已匯出的 bundle、備份 | 不動 | 不動 |

## 規則

| ID | 規則 | 里程碑 |
|---|---|---|
| DEL-R1 | `DELETE /api/items/:id` 把影音移到垃圾桶，回傳 200。垃圾桶中的影音不出現在媒體庫列表、標題搜尋、全文搜尋與任何 MCP tool 中；`GET /api/items/:id` 與其下的端點回傳 404，錯誤碼 `in_trash`。 | M1 |
| DEL-R2 | 移到垃圾桶時，處理中的步驟依 PIPE-R15 取消，等待中的任務移除，不再建立新任務。平台影音的媒體快取立即刪除；其他資料保留到永久刪除。 | M1 |
| DEL-R3 | `GET /api/items/:id/deletion-preview` 回傳影響範圍：產出數、可釋放的位元組數、已花費的金額（`spent_usd`），以及輸出檔清單，每個檔案標示 `modified` 與否。這個請求不改變任何資料。 | M1 |
| DEL-R4 | 垃圾桶中的影音保留 `library.trash_retention_days` 天（預設 30），每天清理一次，到期的永久刪除。`GET /api/trash` 列出垃圾桶，`POST /api/trash/:id/restore` 還原，`DELETE /api/trash/:id` 立即永久刪除，`DELETE /api/trash` 清空。 | M1 |
| DEL-R5 | 還原後，影音回到刪除前的狀態。被取消的步驟為 `skipped`（原因 `cancelled`），提供「重試」動作。媒體快取在需要時重新下載。 | M1 |
| DEL-R6 | 永久刪除時，依上表清除這部影音在 `/data` 中的所有檔案與紀錄。完成後寫入一筆 `item.purged` 事件，只記錄代號與標題。 | M1 |
| DEL-R7 | 永久刪除時，成本帳本保留：`item_id` 改為空，`item_title` 記錄當時的標題，金額照常計入月預算。 | M1 |
| DEL-R8 | 永久刪除不會動到 inbox 與 `media_roots` 中使用者自己的檔案。 | M1 |
| DEL-R9 | 輸出目標寫出的檔案預設保留。刪除時指定 `delete_outputs: true`，只刪除內容 hash 和 `outputs` 紀錄相同的檔案；改過的檔案保留，列在回應的 `kept_outputs`（原因 `modified`）；已經不存在的檔案略過。還原時，被刪除的輸出檔透過重新執行 `publish` 寫回。 | M2 |
| DEL-R10 | 新增垃圾桶中的影音時不建立新項目，回傳 409、錯誤碼 `in_trash`、訊息「這部影音在垃圾桶中。」，動作為「還原」。永久刪除後再新增，視為全新的影音，費用重新計算。 | M1 |
| DEL-R11 | 永久刪除時留下 tombstone（`platform`、`external_id`、`part`，本機檔案另加 `sha256`）。inbox 不會自動重新新增有 tombstone 的檔案，M4 的訂閱也不會。手動新增仍然可以，新增後移除 tombstone。 | M1 |
| DEL-R12 | 刪除、還原、永久刪除、清空垃圾桶需要 owner 的 session 或 `admin` scope；`GET /api/trash` 需要 `read`。MCP 不提供任何刪除或還原的 tool。 | M1 |
| DEL-R13 | CLI：`notesteme items rm <ref>... [--delete-outputs] [--purge] [--yes]`、`notesteme items restore <ref>...`、`notesteme trash ls`、`notesteme trash empty [--yes]`。`--purge` 跳過垃圾桶直接永久刪除。Bilibili 代號不帶分P時，套用到所有分P。沒有 `--yes` 時，在終端機上要求確認；不在終端機上執行時，結束碼為 2。 | M1 |

## 介面

- API：`DELETE /api/items/:id`（body 可帶 `{ "delete_outputs": true }`）、`GET /api/items/:id/deletion-preview`、`GET /api/trash`、`POST /api/trash/:id/restore`、`DELETE /api/trash/:id`、`DELETE /api/trash`
- CLI：見 DEL-R13
- UI：
  - 影音詳情的「更多」選單 →「刪除」，確認對話框顯示 DEL-R3 的影響範圍，以及「同時刪除未修改過的輸出檔（N 個）」選項（M2）。
  - 刪除後顯示「已移到垃圾桶。」與「復原」。
  - 媒體庫的「垃圾桶」檢視：剩餘天數、還原、永久刪除、清空垃圾桶。
- 設定：`library.trash_retention_days`
- 資料模型：`items.deleted_at`、`cost_ledger.item_title`、`item_tombstones (platform, external_id, part, sha256, purged_at)`

## 不在範圍內

- 批次刪除（M4，故事地圖 LIB-08）。M1 的 API 一次刪一部；CLI 可以一次指定多個代號。
- 已分享到 Hub 的內容（M5），刪除本機影音不會撤下 Hub 上的版本。
- 備份中的資料：永久刪除的內容仍存在於之前的備份中，直到備份輪替；從舊備份還原會讓它回來。
- 只刪除媒體檔而保留影音（媒體檔保留策略，見故事地圖第 7 節）。

## 待決問題

- 目前沒有。取消處理時，預算中的預留金額依 COST 的規則直接釋放（M4），不需要另外處理。

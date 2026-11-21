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
- 設定：`library.trash_retention_days`（設定檔，預設 30，範圍 1～3650）

**移到垃圾桶**（`DELETE /api/items/:id`）
- 在一個交易中完成：
  - 設定 `deleted_at`。
  - 尚未結束的步驟改成 `skipped`（原因 `cancelled`），動作「重試」。
  - 任務改成 cancelled。
  - 寫入 `item.deleted` 事件。
- commit 之後，先依 PIPE-R15 取消並等待執行中的任務，再刪除媒體快取目錄 `<資料目錄>/media/<item-id>/`。等待的範圍包含已經被「取消」改成 cancelled、但還在執行的任務；等不到任務結束（逾時）時保留媒體快取，到永久刪除時再一起刪除。
- 回應 200：`{id, ref, deleted_at, purge_at, kept_outputs: []}`。
- 已經在垃圾桶中時回 404 `in_trash`。
- `delete_outputs` 在 M1 接受但沒有作用，因為還沒有輸出目標。

**垃圾桶中的影音**
- `GET /api/items/:id` 與其下的端點（cues、subtitles、runs、rerun、cancel、deletion-preview）回 404。
- 回應內容：`{code: "in_trash", message: "這部影音在垃圾桶中。", actions: ["還原"], item_id}`。
- 新增時遇到垃圾桶中的影音，回 409，內容相同（DEL-R10）。
- 播放清單或分P 展開時，垃圾桶中的項目不重新建立，在回應的 `items` 中標示 `in_trash: true`；全部都在垃圾桶中時才回 409。
- 手動新增時，在同一個交易中移除 tombstone。

**還原**（`POST /api/trash/:id/restore`）
- `deleted_at` 改回 NULL，步驟維持 `skipped` 並提供「重試」，寫入 `item.restored` 事件。
- 影音不在垃圾桶中時回 404。

**永久刪除**（`DELETE /api/trash/:id`、`DELETE /api/trash`）
1. 確認影音在垃圾桶中，再取消並等待殘留的任務。
2. 在一個交易中完成：
   - `cost_ledger`：`item_title` 記錄標題，`item_id` 改為 NULL。
   - 寫入 tombstone。
   - 刪除這部影音的 `events`，改寫一筆 `item.purged`。事件的 `item` 是代號，另外帶 `title`；`events` 表中的 `item_id` 留空。
   - 刪除 `items`，其他紀錄依 cascade 刪除。
   - 刪除沒有任何引用的 `config_snapshots`。
3. commit 之後，刪除 `artifacts`、`media`、`uploads` 下的 `<item-id>` 目錄。
   - 檔案在 DB 之後才刪，避免 DB 失敗時留下指向已刪檔案的紀錄。
   - 刪除失敗只記錄 log。
   - 刪檔的路徑只用 DB 中、已確認是標準形式 UUID 的 item ID。
- 清空垃圾桶時，每部影音各用一個交易，回應 `{purged: n}`。中途失敗時回 500，錯誤內容帶 `purged`（已刪除的數量）。不用 207：CLI 與 client 看到 2xx 會當成成功。
- 取消任務前，先在同一個交易中確認影音仍在垃圾桶中，並列出它所有的任務。等不到任務結束時，什麼都不刪。

**刪除預覽**（`GET /api/items/:id/deletion-preview`）
- 回應：`{artifacts, bytes, spent_usd, outputs: []}`。
  - `artifacts`：所有版本的產出數。
  - `bytes`：產出加上 media、uploads 的大小。
  - `spent_usd`：這部影音實際花費的總和。
- 唯讀，不改變任何資料。

**定期清理**
- housekeeping 的工作，下次執行時間存在 settings 的 `trash_cleanup_next_at`，每 24 小時一次，失敗時 5 分鐘後重試。
- 永久刪除 `deleted_at <= now − 保留天數` 的影音。時間一律來自注入的 clock。

**tombstone**
- 平台影音的比對鍵是 `(platform, external_id, part)`，`external_id` 用網址辨識後的標準形式。
- 本機檔案另外比對 `sha256`（ING-05）。

**CLI**
- `items restore` 從 `GET /api/trash` 解析代號。
- 所有代號都先解析成功，才開始刪除。
- `--purge` 先移到垃圾桶，再永久刪除；影音已經在垃圾桶中時，直接永久刪除。
- 只有 stdin 是終端機時才詢問確認，否則結束碼 2，訊息提示 `--yes`。
- `trash ls`、`trash empty [--yes]`。

**權限**：AUTH 的 token 完成之前，只有 owner 的 session 與本機可以執行。token 完成後，刪除類操作對應 `admin`，`GET /api/trash` 對應 `read`。
- 資料模型：`items.deleted_at`、`cost_ledger.item_title`、`item_tombstones (platform, external_id, part, sha256, purged_at)`

## 不在範圍內

- 批次刪除（M4，故事地圖 LIB-08）。M1 的 API 一次刪一部；CLI 可以一次指定多個代號。
- 已分享到 Hub 的內容（M5），刪除本機影音不會撤下 Hub 上的版本。
- 備份中的資料：永久刪除的內容仍存在於之前的備份中，直到備份輪替；從舊備份還原會讓它回來。
- 只刪除媒體檔而保留影音（媒體檔保留策略，見故事地圖第 7 節）。

## 待決問題

- 取消處理時，預算中的預留金額依 COST 的規則直接釋放（M4），不需要另外處理。
- 場景修改（2026-11-15，DEL 切片 DoR，owner 同意）：R4 的 `item.purged` 事件欄位由 `"ref"` 改成 `"item"`，與 CLI、PIPE 場景的事件形狀一致（`item` 就是代號）。
- 場景修改（2026-11-17，DEL 審查的 mutation testing 後，owner 同意）：
  - R3 的預覽場景加一筆 0.42 USD 的花費，斷言 `artifacts: 1`、`spent_usd: 0.42`、`outputs: []`。原本全部用 `"*"`，預覽算錯也會通過。
  - R1 再斷言垃圾桶中的影音，字幕端點回 404。
  - 保留期限的邊界（剛好第 30 天）、每天只清一次、tombstone 的寫入與移除、預覽不寫入資料，都由單元測試守住。清理一天只跑一次，場景無法剛好停在第 30 天的清理時間。
- `library.trash_retention_days` 暫定放在設定檔。是否改由 DB 的 settings 管理（UI 可調），之後再決定；若改到 DB，要一併更新 GRD 的「由資料庫管理的欄位」清單。
- 播放清單或分P 展開時遇到部分項目在垃圾桶中的處理方式，規則沒有寫，暫定如介面一節。
- 永久刪除後再從舊備份還原，DB 會指向已經不存在的產出檔（備份不含 `/data/artifacts`），這種情況的處理要記入 OPS 的待決問題。
- M1 還沒有 `cues_fts`、`outputs`、術語候選。之後的切片接上時，永久刪除要一併清除。
- 本機檔案（2026-11-21，LOCAL 切片實作時決定）：上傳的檔案存在 `/data/uploads/<id>/`，永久刪除時一併刪除；inbox 與 `media_roots` 中的原檔是 owner 的，永遠不刪（DEL-R8）。tombstone 記錄內容的 `sha256`；inbox 遇到 `sha256` 相同，或同一個路徑而 tombstone 沒有 `sha256` 的檔案時，不建立影音，也不移除 tombstone（DEL-R11）。

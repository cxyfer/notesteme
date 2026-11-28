# 升級 fixture（NFR-CMP-5）

`schema-<版本>/` 是該 schema 版本的資料目錄（`notesteme.db` 加上 `artifacts/` 的字幕產出檔，
由 `internal/benchdata` 產生：3 部影音、每部 3 個字幕版本、各 40 句）。`TestUpgradeFromReleasedSchemas`
（`internal/store/upgrade_test.go`）把每一份複製到暫存目錄，以 `store.Open` 升級到最新的 schema，
檢查影音、現行字幕與全文搜尋索引都還在，以及升級前留下了 `pre-migration-<版本>.db` 快照（OPS-R3）。

fixture 提交後不再修改，也不刪除：它代表「已經發布的版本留在使用者機器上的資料」。

## 每次發布前

1. 確認 `internal/store/migrations/` 最新的版本號 N。
2. 如果還沒有 `schema-N/`，執行：

   ```sh
   go test ./internal/store -run TestUpgradeFromReleasedSchemas -update-upgrade-fixture -count=1
   ```

   這會以目前的程式產生 `schema-N/`，接著照常跑一次升級測試。
3. 把 `schema-N/` 一起提交，並在發布紀錄寫下這個版本對應的 schema 版本。

## 首次發布前後

各發布版對應的 fixture：

| 發布版 | schema |
|---|---|
| `v0.1.0` | 38（`schema-38/`） |

以下是首次發布前的紀錄：`schema-37/` 是 2026-11-25 以現行程式產生的 fixture，
代表「目前的 schema」。首次發布時：

- 若發布版的 schema 仍是 37，`schema-37/` 就是那個發布版的 fixture，不需要新增。
- 若發布前又加了 migration，依上面的步驟新增 `schema-<發布版的版本>/`；`schema-37/` 保留（多測一個更舊的起點）。
- 之後每一個發布版都依上面的步驟新增一份，CI（`go test ./...`）就會驗證「從每個發布版升級」。

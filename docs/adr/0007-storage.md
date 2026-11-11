# ADR-0007：Client 用 SQLite 加檔案，Hub 用 Postgres 加 S3

- 狀態：已採納
- 日期：2026-11-11

## 背景

- Client 是單一使用者的自架服務，在 WSL Docker 或 VPS 上執行，也要能以原生 binary 執行。
- 媒體檔與產出可能很大（影片、畫面），但資料庫的查詢量不大。
- 需要能搜尋字幕全文，包括中文。

## 決策

### Client

- **SQLite**，使用 `modernc.org/sqlite`：
  - 純 Go 實作，不需要 CGO，可以直接跨平台編譯。
  - 開 WAL 模式。
- **全文搜尋**用 FTS5 trigram tokenizer。trigram 無法用索引查少於 3 個字的詞，但中文常見的詞都是 2 個字，所以 2 字以下的查詢改用 `instr` 掃描。個人規模的資料量足以應付。
- **產出存在檔案系統**：`/data/artifacts/<item-id>/<artifact-id>.<ext>`。DB 只存路徑、`sha256` 與來源紀錄。
- **媒體快取**放在 `/data/media`，套用保留策略。例如 N 天後刪影片；選中的畫面複製成產出後，就刪除其餘候選畫面。
- **Migration** 用 goose，**查詢**用 sqlc。

### Hub

- Postgres（pgx）存資料，S3 相容物件儲存（R2 或 MinIO）存檔案。
- sqlc 為兩種資料庫分別維護查詢檔，domain 型別共用。

## 考慮過的選項

- **`mattn/go-sqlite3`**：需要 CGO，跨平台編譯比較麻煩。
- **`ncruces/go-sqlite3`**（Wasm 實作）：可行，作為備選。
- **Client 也用 Postgres**：自架時要多維護一個服務，不值得。
- **ORM（例如 GORM）**：查詢不透明；sqlc 有型別安全，SQL 也看得到。

## 影響

- 好處：
  - Client 只有一個 DB 檔案，備份就是複製 `/data`。
- 代價：
  - SQLite 一次只能有一個寫入者，寫入要集中並批次處理。
  - 兩種資料庫的查詢檔要分開維護。
- `/data` 要放在 Linux 檔案系統上。在 WSL 上不要放 `/mnt/d`，因為跨檔案系統的效能差，檔案鎖的語意也不同。

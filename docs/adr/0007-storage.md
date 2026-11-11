# ADR-0007：Client 預設用 SQLite 加檔案，Hub 用 Postgres 加 S3

- 狀態：已採納
- 日期：2026-11-11
- 修訂：2026-11-12，新增 `/data` 單一擁有者、選配 Postgres＋Redis backend、備份與 migration 快照、產出版本保留

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
- **全文索引只收現行版本**的校正字幕與譯文字幕，被取代的舊版本不進索引。
- **產出存在檔案系統**：`/data/artifacts/<item-id>/<artifact-id>.<ext>`。DB 只存路徑、`sha256` 與來源紀錄。
  - 同一組 `(item, kind, variant, lang)` 預設保留最近 3 版，畫面只保留現行版本。
  - 被輸出目標或 bundle 引用的版本標記 `pinned`，不會被清除。
- **媒體快取**放在 `/data/media`，套用保留策略。例如 N 天後刪影片；選中的畫面複製成產出後，就刪除其餘候選畫面。
- **Migration** 用 goose，**查詢**用 sqlc。
- **單一擁有者**：`serve` 啟動時對 `/data` 取得 flock。CLI 偵測到 lock 就自動改走 client 模式，沒有 lock 時才直接開 DB。這樣 scheduler、並行計數與限流都只有一份，CLI 執行的任務事件也會出現在 UI。
- **備份**：
  - 預設每日用 `VACUUM INTO` 產生一致的 DB 快照，連同 `/config` 保留 7 份。WAL 模式下直接複製 DB 檔可能得到不一致的內容。
  - 產出檔寫入後不再修改，交給 rsync、restic 這類外部工具做增量備份。
  - 啟動時有待執行的 migration，先留一份快照再升級。不支援降級，要回到舊版就還原快照。
  - `notesteme backup`、`notesteme restore` 提供手動操作。

### 選配：Postgres＋Redis backend

- store、佇列、限流、事件匯流排都定義成介面，預設實作是 SQLite 加上記憶體。
- 需要多個 worker 或多台機器共用佇列時，可以改用 Postgres（資料與佇列）加 Redis（限流與事件）。實作時機排在 M4 之後，視需要再做。
- Hub 本來就用 Postgres，兩者可以共用同一套 Postgres 查詢。
- 中文全文搜尋在 Postgres 上要另外處理，例如 `pg_trgm` 或 `pg_bigm`，和 SQLite 的 FTS5 各寫一份。
- 使用 Postgres backend 時，備份改用 `pg_dump`。

### Hub

- Postgres（pgx）存資料，S3 相容物件儲存（R2 或 MinIO）存檔案。
- sqlc 為兩種資料庫分別維護查詢檔，domain 型別共用。

## 考慮過的選項

- **`mattn/go-sqlite3`**：需要 CGO，跨平台編譯比較麻煩。
- **`ncruces/go-sqlite3`**（Wasm 實作）：可行，作為備選。
- **Client 預設用 Postgres＋Redis**：多個 worker 共用佇列最簡單，但原生 binary 不再是零依賴，部署變成 3 個容器，中文搜尋也需要額外的擴充。改為選配。
- **ORM（例如 GORM）**：查詢不透明；sqlc 有型別安全，SQL 也看得到。

## 影響

- 好處：
  - Client 預設只有一個 DB 檔案，部署與備份都簡單。
- 代價：
  - SQLite 一次只能有一個寫入者，寫入要集中並批次處理。
  - 兩種資料庫的查詢檔要分開維護。
  - 選配的 Postgres＋Redis backend 要多一套實作與測試。
- `/data` 要放在 Linux 檔案系統上。在 WSL 上不要放 `/mnt/d`，因為跨檔案系統的效能差，檔案鎖的語意也不同。

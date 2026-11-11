# OPS：備份、健康檢查與外部工具

- 狀態：draft
- 里程碑：M1（每日排程備份與無人值守告警在 M4，見故事地圖 OPS-06、OPS-07）
- 相關：plan 第 18、18.1 節；ADR-0007、ADR-0009
- 驗收場景：[`features/OPS-operations.feature`](../../features/OPS-operations.feature)

## 目的

讓 Notesteme 可以放心地長時間運作：資料可以備份與還原，升級前自動留快照，容器的健康狀態看得到，yt-dlp 可以更新、更新壞了會自動回退。

## User stories

| ID | Story | 里程碑 |
|---|---|---|
| OPS-01 | As the owner, I want to back up and restore my instance with one command, so that a broken disk or a bad upgrade never loses my library. | M1 |
| OPS-02 | As the owner, I want a snapshot taken automatically before every database migration, so that I can always go back to the previous version. | M1 |
| OPS-03 | As the owner, I want health endpoints that Docker can use, so that a stuck container is restarted automatically. | M1 |
| OPS-04 | As the owner, I want to update yt-dlp without rebuilding the image, and have a bad update rolled back automatically, so that YouTube changes do not break my instance for long. | M1 |
| OPS-05 | As the owner, I want the default Docker setup to be reachable only from my own machine, so that I do not expose it by accident. | M1 |

## 規則

| ID | 規則 | 里程碑 |
|---|---|---|
| OPS-R1 | `notesteme backup` 用 `VACUUM INTO` 產生一致的 DB 快照，連同 `/config` 打包成 `notesteme-backup-<時間>.tar.gz`，並更新 `notesteme-backup-latest.tar.gz` 指向最新的一份；`serve` 執行中也可以備份。備份不包含 master key 與 `/data/artifacts`、`/data/media`。 | M1 |
| OPS-R2 | `notesteme restore <file>` 只能在 `serve` 停止時執行；還原前先為目前的 DB 留一份快照。備份的 schema 版本比目前的程式新時拒絕還原。 | M1 |
| OPS-R3 | 啟動時有待執行的 migration，先在 `/data/backups/` 留一份快照（`pre-migration-<版本>`）再升級。migration 失敗時自動還原快照並拒絕啟動。 | M1 |
| OPS-R4 | 不支援降級：DB 的 schema 版本比程式新時拒絕啟動，訊息提示還原 migration 前的快照。 | M1 |
| OPS-R5 | `/healthz` 在程序存活時回傳 200。`/readyz` 在 DB 可寫入、ffmpeg 與 yt-dlp 可執行時回傳 200，否則回傳 503 並列出失敗項目。兩者都不需要登入，也不回傳任何內容資料。 | M1 |
| OPS-R6 | yt-dlp 安裝在 `/data/tools/yt-dlp-venv`。更新後執行自我檢查：`--version`，以及對設定的檢查影片跑一次 `Probe`。失敗就回退到上一版，並在 UI 顯示「yt-dlp 更新後自我檢查失敗，已回到 <版本>。」 | M1 |
| OPS-R7 | 可以把 yt-dlp 固定在某個版本；固定後排程更新會略過。 | M1 |
| OPS-R8 | 外部工具的尋找順序：設定檔的路徑 → 環境變數（`NOTESTEME_FFMPEG` 等）→ `PATH`。 | M1 |
| OPS-R9 | `deploy/docker` 的 `wsl` profile 預設只把 port 映射到 `127.0.0.1`，並設定 `HEALTHCHECK` 使用 `/readyz`。 | M1 |
| OPS-R10 | `events` 只保留最近 90 天，每天清理一次。 | M1 |

## 介面

- CLI：`notesteme backup [--out <dir>]`、`notesteme restore <file>`、`notesteme tools update [--pin <version>]`
- API：`GET /healthz`、`GET /readyz`、`GET /api/system`、`POST /api/tools/yt-dlp/update`、`POST /api/tools/yt-dlp/rollback`

## 不在範圍內

- 每日排程備份與保留 7 份（M4，OPS-06）。
- 無人值守告警（M4，OPS-07）。
- 產出檔的增量備份：交給 rsync、restic 等外部工具，文件提供範例。

## 待決問題

- 自我檢查用的「已知影片」要內建一部，還是讓使用者設定。暫定內建一部公開且長期存在的 YouTube 影片，並允許在設定檔覆寫。

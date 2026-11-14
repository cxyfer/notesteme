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

**備份（OPS-R1）**

- `POST /api/backup` 回傳 tar.gz 串流。server 不寫入 client 指定的路徑，以免成為任意寫檔的管道。CLI 把串流寫到 `--out`（預設 `<資料目錄>/backups`）。
- 檔名是 `notesteme-backup-<UTC 時間>.tar.gz`，時間格式如 `20261112T100000Z`（不含冒號）。`notesteme-backup-latest.tar.gz` 是指向它的相對 symlink；Windows 改成複製一份。
- 內容：
  - `manifest.json`：`schema_version`、`created_at`、`notesteme_version`。
  - `notesteme.db`：用另一條沒有 `query_only` 的連線執行 `VACUUM INTO`，先寫到暫存名稱，完成後再 rename。
  - `config/notesteme.yaml`：有這個檔案時才放。
- 不含 master key 的值。
- 只有 owner（session 或本機）可以備份，token 回 403：備份包含整個 DB。
- 串流中途失敗時中斷連線，client 看到截斷的串流，不會留下看似完整的檔案。

**還原（OPS-R2）**

- 不經 API，只取得資料目錄的鎖；這是 CLI-R2 的例外。鎖由 serve 持有時，結束碼 1，訊息「請先停止 notesteme serve，再執行 restore。」。先檢查鎖，再讀備份檔。
- 只接受已知名稱的項目，而且必須是一般檔案；`../` 與 symlink 一律拒絕。
- 還原前先把目前的 DB 留成 `pre-restore-<時間>.db`，並刪除 `-wal`、`-shm`。
- 不覆寫 `/config`，因為 compose 可能以唯讀方式掛載。設定檔解到 `<資料目錄>/backups/restored-config-<時間>/`，由使用者自行替換。
- 備份的資料金鑰是用備份當時的 master key 加密的。master key 輪替過的話，還原後 serve 會以 SEC-R5 的訊息拒絕啟動，restore 的完成訊息要提醒這一點。

**migration（OPS-R3、R4）**

- 新的 DB（版本 0）不留快照。快照先寫到暫存名稱再 rename，權限 0600。
- migration 失敗時，還原快照並拒絕啟動，訊息「migration <N> 失敗：<原因>；已還原 /data/backups/pre-migration-<舊版本>.db」。
- DB 比程式新時拒絕啟動，訊息「資料庫的 schema 版本 <DB> 比程式支援的 <程式> 新，不支援降級；請改用新版程式，或還原 /data/backups/pre-migration-*.db。」。goose 本身不會擋這種情況，要自己比較版本。

**yt-dlp（OPS-R6、R7、R8）**

- 目錄結構：`/data/tools/yt-dlp/<id>/` 各是一個 venv，`/data/tools/yt-dlp-venv` 是指向目前版本的 symlink。venv 不能搬移，所以用切換 symlink 的方式更新，不用 rename。image 內建的版本在 `/opt/yt-dlp`，是沒有受管理版本時的後備。
- 更新流程：在新目錄安裝 → 自我檢查（`--version` 加上對內建檢查影片跑一次 Probe）→ 通過才切換 symlink；失敗就刪掉新目錄，回應 `rolled_back: true`。保留上一版供 rollback。檢查影片在 M1 內建，不開放覆寫。
- 安全限制：
  - 只有 owner（session 或本機）可以觸發，token 回 403。
  - 版本字串必須符合 `^\d{4}\.\d{2}\.\d{2}(\.\d+)?$`，防止注入 pip 參數。
  - 不接受自訂的 package index。
- pip 一律帶 `--isolated`，忽略使用者與系統的 pip.conf 和 `PIP_*` 環境變數；子程序的環境只有 `PATH`、`HOME`、`LANG`。
- 固定版本：`POST /api/tools/yt-dlp/update {"pin": "<版本>"}`，解除是 `{"unpin": true}`；CLI 是 `notesteme tools update --pin <版本>`，`--unpin` 解除。固定後，手動與排程的更新都略過，回報「yt-dlp 已固定在 <版本>，略過更新。」，結束碼 0。
- 尋找順序：設定檔 → 環境變數 → 受管理的 venv → `PATH`。工具由設定檔或環境變數指定時，update 回 409。
- 回應：`{version, rolled_back, message, reason?}`。`reason` 是自我檢查失敗的簡短原因（單行、遮蔽 cookie、最多 200 字元），`message` 維持上面的固定文字。CLI 在回退時結束碼 1，略過（固定）時 0。
- 錯誤碼：版本格式不符 422；`tool_externally_managed`、`update_in_progress`、`no_previous_version`、`tool_state_corrupt`（`state.json` 損毀，訊息指出位置，可刪除後重試）、`tool_unsupported_platform`（Windows 不支援受管理的 venv）都是 409；安裝失敗 502 `upstream_failed`，回應只有「yt-dlp 安裝失敗，詳見 server log。」，pip 的輸出只寫進 log。
- 更新與 rollback 在 server 關閉時取消，並清理新的 venv。
- 狀態（固定版本、上一版）存在 `/data/tools/yt-dlp/state.json`（0600）。`-U` 更新時安裝前不知道版本，venv 目錄名稱是 `latest-<UTC 時間>`。
- `GET /api/system` 的回應：`listen`、`listen_source`、`data`、`data_source`、`tools.<名稱>.{path, source, available, error}`（source 是 `config_file`、`env`、`managed` 或 `path`）、`yt_dlp.{pinned, previous}`（沒有時為 null）。設定檔中的路徑不能執行時，仍然回報路徑與來源，`available: false`。`state.json` 損毀時仍回 200，`yt_dlp` 為 `{pinned: null, previous: null, error}`。

**事件（OPS-R10）**

- `events` 表持久化 SSE 事件，與發布在同一個交易中寫入，也為之後 SSE 的 replay 預作準備。
- 清理是 worker 的定期工作：每輪開頭依 settings 的 `events_cleanup_next_at` 判斷是否到期，到期就刪除 90 天前的事件，並排定下一次（24 小時後）；失敗時 5 分鐘後重試。閒置的 worker 也會在到期時醒來。

## 不在範圍內

- 每日排程備份與保留 7 份（M4，OPS-06）。
- 無人值守告警（M4，OPS-07）。
- 產出檔的增量備份：交給 rsync、restic 等外部工具，文件提供範例。

## 待決問題

- 自我檢查遇到暫時性失敗（網路、限流、YouTube 的 bot 偵測）也照規則回退，原因放在 `reason`；第一次安裝（沒有上一版）時新 venv 會被刪除。是否要區分「yt-dlp 本身壞掉」與「網路問題」，等 M0 看實際的失敗率再決定（2026-11-15，OPS 審查）。
- 自我檢查用的「已知影片」：M1 先內建一部公開、長期存在的 YouTube 影片，不開放設定檔覆寫（2026-11-15，OPS 切片設計時決定）。這部影片能不能長期使用，要在 M0 實測。
- 執行期從 PyPI 安裝 yt-dlp 有供應鏈風險，而且 pip 的連線不經過 netguard。owner 同意照規格做，但要加上上一節的安全限制（2026-11-15）。
- OPS-R7 原本只寫「排程更新會略過」，場景則要求手動更新也略過；兩種都略過。
- OPS-R9 的 `wsl` profile：compose 的 profile 以服務為單位，同一個服務不能依 profile 換 port。目前 `notesteme` 服務本來就只映射到 `127.0.0.1`。`vps` profile 之後只額外加上反向代理。`docker compose config` 在沒有 `NOTESTEME_MASTER_KEY` 時會失敗，因為 compose.yaml 用了 `:?`。
- 驗收中的「目前 schema 版本是 N」是用截斷 migration 清單來模擬。之後若啟動流程讀到 N 以後才建立的表，R3、R4 的場景會壞，屆時要改成相對版本。
- 場景修改（2026-11-15，owner 同意）：
  - R2「Restore brings the library back」在 restore 前先讓 server 正常關閉。`has been ingested` 會隱含啟動 server，而 server 執行中不能 restore。
  - R1 斷言備份中不含 master key 的值，原本只檢查變數名稱。
  - R10 加上一筆保留期限內的事件，並斷言剩下 1 筆。
- 場景修改（2026-11-15，OPS 審查的 mutation testing 後，owner 同意）：
  - R6 兩個場景斷言 `yt-dlp <版本> is the active version`，驗證 symlink 真的切換或沒有切換。原本只看 API 回應，更新後沒切換、回退時其實切換了都不會被發現。
  - R10 再加一筆 53 天前的事件，剩下 2 筆，讓保留期寫成 30 天也會被發現；新增「Events are cleaned up at most once a day」，驗證清理後有排定下一次，24 小時內不重複清理。
  - R2「Restore brings the library back」斷言 `pre-restore-<時間>.db` 存在。
  - R2「備份的 schema 版本比目前的程式新時拒絕還原」沒有場景：現有步驟做不出「schema 較新的備份檔」，由 `internal/app/restore_test.go` 的單元測試守住。


# OPS：備份、健康檢查與外部工具

- 狀態：draft（M4 章節 OPS-R11～R21：ready）
- 里程碑：M1；M4 加上每日排程備份、無人值守告警與 VPS 的 compose profile（OPS-06～08，規則 OPS-R11～R21）
- 相關：plan 第 18、18.1 節；ADR-0007、ADR-0009、[ADR-0021](../adr/0021-m4-scheduler-subscriptions-and-outbox.md)（第 1、5 點）；[NOTIFY](NOTIFY-notifications.md)
- 驗收場景：[`features/OPS-operations.feature`](../../features/OPS-operations.feature)（M1）、[`features/OPS-unattended.feature`](../../features/OPS-unattended.feature)（M4）

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
| OPS-06 | As the owner, I want a backup taken every day and the last seven kept, so that I never need to remember. | M4 |
| OPS-07 | As the owner, I want to be alerted when something quietly stops working, so that problems do not go unnoticed for days. | M4 |
| OPS-08 | As the owner, I want a VPS compose profile with HTTPS, so that I can expose the instance safely. | M4 |

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
| OPS-R11 | 每日備份：排程工作 `backup.daily`（ADR-0021 第 1 點）每天在 `backup.daily.timezone`（預設 `UTC`）的 `backup.daily.at`（預設 `03:00`）執行一次，內容與 `notesteme backup` 相同（OPS-R1），寫到 `backup.daily.dir`（預設 `/data/backups`），並更新該目錄的 `notesteme-backup-latest.tar.gz`。`backup.daily.enabled` 預設 `true`。這些設定只能寫在設定檔（避免 API 把備份寫到任意路徑）。停機錯過時只補跑一次。 | M4 |
| OPS-R12 | 保留：每日備份成功後，`backup.daily.dir` 中只保留名稱完全符合 `^notesteme-backup-\d{8}T\d{6}Z\.tar\.gz$` 的最新 `backup.daily.keep`（預設 7，可設 1～365）份（依名稱中的時間排序），刪除更舊的。不刪除其他任何檔案（`pre-migration-*`、`pre-restore-*`、`notesteme-backup-latest.tar.gz`、使用者自己的檔案）、不跟隨 symlink、不進子目錄。備份失敗時不刪除任何檔案、清掉暫存檔，排程依 ADR-0021 第 1 點退避重試，並送出 `alert` 事件（種類 `backup_failed`），訊息「每日備份失敗：<原因>。」；同一串連續失敗只告警一次，下一次成功時送「已恢復：每日備份成功。」。 | M4 |
| OPS-R13 | 無人值守告警：排程工作 `alerts` 每 15 分鐘檢查 OPS-R14～R18 的條件（server 啟動後立即檢查一次）。條件成立時以 NOTIFY 的 `alert` 事件送出一次 `{kind, key, message, since, resolved: false}`，條件持續成立時不重複告警；條件解除時送一次 `resolved: true`（文字管道的標題「Notesteme 已恢復：<摘要>」），之後再成立才再告警。狀態存在 `alert_states (kind, key, active_since, notified_at, resolved_at)`。門檻都在設定檔的 `alerts.*`：`alerts.enabled: false` 停用全部，個別門檻設成 `0` 停用那一項。沒有任何 notifier 勾選 `alert` 時仍然記錄狀態，總覽照樣顯示。 | M4 |
| OPS-R14 | 任務沒有進展（`stuck_task`）：`pending` 或 `running` 的步驟超過 `alerts.stuck_task_hours`（預設 6）小時沒有改變狀態。不包括 `waiting`、`blocked`（含 DIG-R6 的 `awaiting_user`），以及 owner 暫停的佇列或資源類別中的步驟。每部影音一則（`key` 是影音 id），訊息「「<標題>」的 <步驟> 超過 6 小時沒有進展。」。 | M4 |
| OPS-R15 | 排程沒有成功（`job_stale`）：`scheduled_jobs` 中 `failures > 0`，而且最後一次成功（沒有成功過時用建立的時間）早於 `alerts.job_stale_hours`（預設 24）小時前。`key` 是 `<kind>:<key>`，訊息「排程 <kind> 超過 24 小時沒有成功執行：<last_error>」。`alerts` 與 `notify.deliver` 自己不在檢查範圍內（通知壞掉時告警也送不出去，由總覽顯示）。 | M4 |
| OPS-R16 | 磁碟用量（`disk_usage`）：`/data` 所在檔案系統的用量大於或等於 `alerts.disk_usage_percent`（預設 90）%；用量低於門檻減 5 個百分點才算解除，避免在門檻附近反覆告警。訊息「/data 的磁碟用量是 93%，超過 90%。」。 | M4 |
| OPS-R17 | cookie 太久沒有成功使用（`cookie_stale`）：已上傳的 cookie 最後一次成功使用（帶這個 cookie 的 yt-dlp 呼叫結束碼為 0；沒有成功過時用上傳的時間）早於 `alerts.cookie_stale_days`（預設 14）天前。`key` 是平台，訊息「YouTube cookie 超過 14 天沒有成功使用，可能已經失效。請重新上傳。」（Bilibili 同理）。重新上傳或成功使用後解除。 | M4 |
| OPS-R18 | yt-dlp 失敗率升高（`ytdlp_failure_rate`）：最近 1 小時至少有 `alerts.ytdlp_min_calls`（預設 5）次 yt-dlp 呼叫，而且失敗的比例大於或等於 `alerts.ytdlp_failure_ratio`（預設 0.5）。「失敗」是結束碼不是 0，而且不是影片本身不存在或私人（SRC-R9 的 `permanent`）。訊息「最近 1 小時 yt-dlp 失敗 4/5 次。先更新 yt-dlp；如果仍然失敗，上傳 cookie 或確認 VPS 的 IP 是否被封鎖。」。呼叫統計以 5 分鐘為一格，保留 48 小時。 | M4 |
| OPS-R19 | `deploy/docker/compose.yaml` 的 `vps` profile 加上 `vps-check` 與 `caddy` 兩個服務。`caddy` 使用固定版本的 image（`caddy:2.8.4-alpine`），把 `80`、`443`（tcp）與 `443/udp` 映射到所有介面（只有這三個 port 不綁 `127.0.0.1`），以 `deploy/docker/Caddyfile`（唯讀掛載）對 `{$NOTESTEME_DOMAIN}` 自動取得 HTTPS 憑證、反向代理到 `notesteme:8080`，並加上 `Strict-Transport-Security`；`depends_on` 是 `vps-check`（`service_completed_successfully`）與 `notesteme`（`service_healthy`）。`notesteme` 服務在所有 profile 都只映射到 `127.0.0.1`（OPS-R9 不變），並傳入 `NOTESTEME_DEPLOYMENT: ${NOTESTEME_DEPLOYMENT:-local}`。 | M4 |
| OPS-R20 | `notesteme deploy-check vps`（`vps-check` 服務執行，資料目錄唯讀掛載）依序檢查，遇到第一個問題就以結束碼 1 結束並印出：沒有 `NOTESTEME_DOMAIN`，或它是 IP 或 `localhost`：「vps profile 需要在 .env 設定 NOTESTEME_DOMAIN（例如 notes.example.com）。」；`NOTESTEME_DEPLOYMENT` 不是 `vps`：「vps profile 需要在 .env 設定 NOTESTEME_DEPLOYMENT=vps。」；DB 中沒有密碼（資料目錄還沒有 DB 時視為沒有）、也沒有 `NOTESTEME_PASSWORD`：「vps profile 必須設定登入密碼。請在 .env 設定 NOTESTEME_PASSWORD 後重新啟動。」；生效的 `trusted_proxies`（設定檔或 `NOTESTEME_TRUSTED_PROXIES`）是空的：「vps profile 必須把 Caddy 設成 trusted_proxies。請在 .env 設定 NOTESTEME_TRUSTED_PROXIES（compose 的預設值是 172.31.250.10/32）。」。全部通過時結束碼 0，印出「vps 部署檢查通過。」。檢查不通過時 Caddy 不會啟動，實例不會對外開放。 | M4 |
| OPS-R21 | `NOTESTEME_DEPLOYMENT` 只接受 `local`（預設）與 `vps`，其他值拒絕啟動：「NOTESTEME_DEPLOYMENT 只能是 local 或 vps。」。`vps` 時 `serve` 拒絕 `--insecure-no-auth`：「vps 部署不能使用 --insecure-no-auth。請設定 NOTESTEME_PASSWORD。」（AUTH-R3 的例外不適用），session cookie 一律帶 `Secure`。 | M4 |
| OPS-R22 | `GET /api/system` 回報程式版本（`version`，建置時寫入，開發版是 `dev`）、資料庫的 schema 版本（`schema_version`，最新已套用的 migration）與每個外部工具回報的版本（`tools.<名稱>.version`，yt-dlp、ffmpeg、ffprobe、Deno）。工具的版本以執行一次版本指令取得並快取：快取鍵是工具解開 symlink 後的實際路徑、大小與修改時間（更新或回退 yt-dlp 後重新取得），沒有快取時所有工具同時執行、最多等 2 秒；不能執行、逾時或認不出版本時省略 `version`，其他欄位照常回傳。 | M1 |

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
- `GET /api/system` 的回應：`version`、`schema_version`（OPS-R22）、`listen`、`listen_source`、`data`、`data_source`、`tools.<名稱>.{path, source, available, error, version}`（source 是 `config_file`、`env`、`managed` 或 `path`）、`yt_dlp.{pinned, previous}`（沒有時為 null）。設定檔中的路徑不能執行時，仍然回報路徑與來源，`available: false`。`state.json` 損毀時仍回 200，`yt_dlp` 為 `{pinned: null, previous: null, error}`。

**事件（OPS-R10）**

- `events` 表持久化 SSE 事件，與發布在同一個交易中寫入，也為之後 SSE 的 replay 預作準備。
- 清理是 worker 的定期工作：每輪開頭依 settings 的 `events_cleanup_next_at` 判斷是否到期，到期就刪除 90 天前的事件，並排定下一次（24 小時後）；失敗時 5 分鐘後重試。閒置的 worker 也會在到期時醒來。

**M4：每日備份、無人值守告警與 VPS（OPS-R11～R21）**

- 設定檔（只能寫在設定檔）：
  - `backup.daily.{enabled, at, timezone, dir, keep}`；`dir` 在容器中建議另外掛載（例如 `/backups`），備份才不會和資料放在同一個 volume。
  - `alerts.{enabled, stuck_task_hours, job_stale_hours, disk_usage_percent, cookie_stale_days, ytdlp_min_calls, ytdlp_failure_ratio}`，預設值見 OPS-R14～R18。
- 環境變數：`NOTESTEME_DEPLOYMENT`（`local`、`vps`）；`NOTESTEME_DOMAIN`（`vps` profile 的 Caddy 與 `deploy-check` 使用）；`NOTESTEME_TRUSTED_PROXIES`（逗號分隔的 CIDR，與設定檔的 `trusted_proxies` 取聯集；compose 預設為 caddy 的固定位址，見 O23）。
- CLI：`notesteme deploy-check vps`（不經 API、不取得資料目錄的鎖，只以唯讀方式開 DB；CLI-R2 的例外，同 restore）。
- API：`GET /api/system` 加上 `alerts: [{kind, key, message, since}]`（目前成立的告警）與 `backup: {last_ok_at, last_error, next_run_at}`；總覽（LIB-09）讀這兩個欄位。
- 排程：`scheduled_jobs.kind` 的 `backup.daily`（`key` 固定 `daily`）與 `alerts`（`key` 固定 `check`）。
- 資料表：`alert_states (kind, key, active_since, notified_at, resolved_at)`，主鍵 `(kind, key)`；`tool_call_stats (tool, bucket_start, calls, failures)`；`secrets` 的 cookie 列加上 `last_ok_at`（yt-dlp 帶 cookie 成功時更新）。
- compose：`deploy/docker/compose.yaml` 的 `vps` profile（`vps-check`、`caddy`）、`deploy/docker/Caddyfile`、`.env.example` 加上 `COMPOSE_PROFILES=vps`、`NOTESTEME_DEPLOYMENT=vps`、`NOTESTEME_DOMAIN` 的說明。
- 資源類別：備份與告警都是排程工作，不進佇列（ADR-0021 第 6 點）。

## 不在範圍內


### M4

- 每日排程備份與保留 7 份：已在 M4 規格化，見 OPS-R11、R12。
- 無人值守告警：已在 M4 規格化，見 OPS-R13～R18。
- 備份上傳到雲端儲存（S3 等）：交給 rclone、restic 等外部工具。
- 告警的「基準線」比較（例如和上週同時段的失敗率比較）：OPS-R18 先用固定門檻。
- Caddy 以外的反向代理（Traefik、nginx）的設定範例：文件可以補，但不在 compose 中提供。
- 產出檔的增量備份：交給 rsync、restic 等外部工具，文件提供範例。

## 待決問題

- 備份包含上傳的原始檔（2026-11-21，LOCAL 切片審查後決定，待 owner 確認）。OPS-R1 只排除可以重建的 `/data/artifacts` 與 `/data/media`，但上傳的檔案只存在 `/data/uploads/<id>/`，沒有其他來源。
  - 備份在 manifest、DB、設定檔之後加入 `uploads/<id>/<name>`，manifest 多一個選填欄位 `uploads: [{path, size, sha256}]`（沒有上傳時省略，格式與之前完全相同）。收錄的是 DB 中上傳的影音所指向的一般檔案；`sha256` 取自 DB，寫入時再算一次，不符就讓備份失敗。檔案不見或不是一般檔案時略過並記 log。
  - 上傳檔的清單取自備份中的 DB 快照，不是線上的 DB。從快照之前到備份寫完為止，永久刪除仍會清掉 DB，但上傳原始檔要等備份結束才刪除。程序在這段期間結束時，留下的目錄由啟動時的孤兒清理刪除。這樣快照中的每個上傳影音，原始檔都在備份裡。
  - 還原時只接受 manifest 中列出、符合 `uploads/<小寫 UUID>/<name>` 的一般檔案；大小或 `sha256` 不符、manifest 列出卻缺少，都拒絕還原。空間檢查包含這些檔案。上傳檔先解到暫存目錄，驗證通過後才整個換上，失敗時維持原狀。
  - 還原後的 `/data/uploads` 只有備份中的檔案；原本的 `/data/uploads` 不刪除，移到 `/backups/pre-restore-uploads-<時間>/`（比照 pre-restore 的 DB 快照），由使用者自行清理。`notesteme restore` 會印出這個位置。
  - manifest 的大小上限由 64 KiB 放寬為 16 MiB，讓大量上傳檔的清單放得下。
  - 沒有 uploads 的舊備份仍可還原。
  - 代價：上傳多時備份會很大（每個檔案最多 4 GiB）。之後可以考慮 `--exclude-uploads`，或改成增量備份。
- 啟動時清理孤兒的上傳目錄（2026-11-21）：上傳是先搬到 `/data/uploads/<id>/` 再寫入 DB，中間 crash 會留下沒有影音的目錄。serve 與 standalone 取得 `/data` 鎖之後，刪除名稱是 UUID 而且 DB 中沒有對應影音的目錄。
- 永久刪除之後再從舊備份還原（2026-11-17，DEL 審查）：DB 會讓影音回到媒體庫，但產出檔已不存在。
  - 讀字幕會回 500。
  - 重跑時快取命中只看 DB，步驟顯示 done，內容仍是壞的。
  - 之後要讓快取命中時確認檔案存在，缺檔時回明確的錯誤碼。
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

### M4（2026-11-23 規格展開）

- O1：每日備份預設寫到 `/data/backups`（OPS-R11），和資料在同一個 volume，磁碟壞掉時備份一起不見（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）。理由：預設值必須在沒有額外掛載時也能運作；文件與 `.env.example` 建議另外掛載 `/backups` 並設定 `backup.daily.dir`。
- O2：保留只看檔名（OPS-R12），不讀 manifest：使用者手動執行 `notesteme backup --out <同一個目錄>` 產生的檔案也算在 7 份之內。這樣規則簡單、可以預測；想另外保留的備份放到別的目錄即可。
- O3：OPS-R18「突然升高」先用固定門檻（最近 1 小時、至少 5 次、失敗 50% 以上），不和過去的基準線比較（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）。真實的失敗率要等 M0 在 VPS 上量測，之後再調整預設值或加上基準線；所以 OPS-R18 的場景標 `@pending-m0`。
- O10：`backup.daily` 預設開啟、`alerts` 在 server 啟動時就檢查，實作之後**每個**場景都會跑這兩個排程：fake clock 跨過 03:00 UTC 的既有場景（例如前進 `24h`、`25h`）會在 `/data/backups` 產生備份，`alerts` 會讀真的磁碟用量。harness 要預設 `TestControls.DiskUsage` 為 10%、沒有 `alert` notifier 時告警不會送出；OPS 的 M4 切片要確認預設 tag 的場景與 `features/passing.txt` 不退步（同 ADR-0021 第 3 點對 `summary` 的要求），有退步就停下來回報，不要改既有的 `.feature`。
- O4：告警解除後再成立時會再告警（OPS-R13），但 NOTIFY-R10 的去重視窗（預設 24 小時，鍵 `alert:<種類>:<對象>`）會讓 24 小時內反覆成立的告警只通知第一次。這是刻意的：在門檻附近擺盪的條件不應該每 15 分鐘通知一次。
- O5：VPS 的密碼檢查放在 `vps-check` 服務（OPS-R20），不在 compose 的變數代入中用 `${NOTESTEME_PASSWORD:?}`：compose 先代入整份檔案再依 profile 篩選服務（fakecompose 也一樣），放在 `vps` 的服務中會讓 `wsl` 與預設 profile 沒有密碼時也無法 `config`；而且密碼可能已經存在 DB 中（`NOTESTEME_PASSWORD` 只在第一次啟動時使用）。代價：`docker compose config` 本身不會失敗，要到 `up` 時才由 `vps-check` 擋下。
- O6：`notesteme` 服務是所有 profile 共用的（compose 的 profile 以服務為單位，見上面 OPS-R9 的待決問題），所以 `vps` 的行為差異靠 `NOTESTEME_DEPLOYMENT` 傳進去；`vps-check` 會確認它是 `vps`，避免使用者只開 profile、忘了設定。
- O7：`job_stale` 的範圍包含 `backup.daily`（OPS-R15），所以每日備份持續失敗超過 24 小時時，會在 `backup_failed` 之後再收到一則 `job_stale`。兩則的意思不同（「失敗了」與「已經一天沒有成功」），不合併。
- O8：每部影音一則 `stuck_task`（OPS-R14），很多影音同時卡住時靠 NOTIFY-R11 的速率上限限制。之後可以改成彙總成一則。
- O9：ADR-0021 第 7 點的 migration 清單沒有 `alert_states`、`tool_call_stats` 與 cookie 的 `last_ok_at`，要在 ADR 補上（OPS 的 M4 切片）。
- M4-S0：`backup.daily.*`、`alerts.*` 與 `NOTESTEME_DEPLOYMENT` 已解析與驗證（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：`at` 是 `HH:MM`、`timezone` 用 `time.LoadLocation` 驗證（binary 內嵌 `time/tzdata`，容器沒有時區資料也能啟動；`Local` 不接受）、`dir` 是絕對路徑（預設 `<資料目錄>/backups`）、`keep` 為 1～365；`alerts` 的門檻是 0 以上的整數（`disk_usage_percent` 0～100、`ytdlp_failure_ratio` 0～1），上限是 S0 暫定的。`NOTESTEME_DEPLOYMENT` 只接受 `local`、`vps`（OPS-R21 的第一句）；`vps` 的其他行為（拒絕 `--insecure-no-auth`、cookie 一律 `Secure`）屬於 S12。排程工作 `backup.daily`、`alerts` 要等 S12 在 `internal/scheduler` 登記。

### M4 OPS 實作的決定（2026-11-24，依 /goal 採 advisor 建議，待 owner 追認）

- O11（`stuck_task` 的「沒有改變狀態」）：`stage_runs` 沒有「狀態改變的時間」欄位，也不為此新增（改狀態的查詢由 pipeline／queue 共用）。改由既有欄位推算：`running` 取最後一次領取（`started_at`）；排隊中（`pending`，以及等 Agent 領取的 `awaiting_agent`，R14 的「pending」包含它）取「最新的未完成任務建立時間」「最晚的重試時間」與「最後一次領取（Agent 租約到期放回佇列時保留）」中最晚的一個。只看有未完成任務（`pending`、`leased`）的步驟，還在等前一步的步驟不算；垃圾桶中的影音不算。代價：內建 worker 的租約在重啟時放回佇列（`started_at` 清空）後，會從任務建立的時間算起，可能提早告警。每部影音取最先建立而且卡住的步驟。
- O12（`backup_failed` 的訊息）：通知與 `last_error` 的 `<原因>` 是固定的幾種說明（磁碟空間不足、路徑中有不是目錄的項目、沒有寫入權限、唯讀的檔案系統、無法產生資料庫快照、其他寫入錯誤），不放系統錯誤字串（含絕對路徑，NOTIFY-R9）；完整錯誤只寫進 log。成功時解除並送「已恢復：每日備份成功。」（文字管道標題「Notesteme 已恢復：每日備份成功。」）。`backup_failed` 由 `backup.daily` 管理，不受 `alerts.enabled` 影響。
- O13（保留）：剛寫好的那一份一定保留，即使目錄中有名稱時間更晚的檔案（例如使用者放進一份未來時間的檔名，`keep: 1` 時不會把今天的備份刪掉）；保留數仍是 `keep`。刪除經 `os.Root`，刪除前再確認一次是一般檔案。清理失敗只記 log，不算備份失敗。
- O14（空間檢查）：寫出前以「資料庫快照＋設定檔＋上傳檔」的大小與目錄的剩餘空間（statfs）比較；不足時不寫出。寫入中失敗（例如 ENOSPC）時刪除暫存檔，`latest` 不更新。`notesteme-backup-latest.tar.gz` 的暫存 symlink 改用隨機名稱，手動備份與每日備份同時寫入同一個目錄時不會互相覆蓋（M1 的實作是固定名稱）。
- O15（排程）：啟動時 `backup.daily` 的排定時間已經過了（停機錯過）或正在失敗重試時保留；否則依目前的 `at`、`timezone` 重排（設定可能改了）。`enabled: false` 時刪除排程列（`GET /api/system` 的 `backup.next_run_at` 為 null）。`alerts.enabled: false` 時刪除 `alerts` 的排程列，並安靜地解除它管理、仍然成立的告警（不送 resolved），總覽不會一直顯示停用前的狀態；個別門檻設成 0 時也是安靜地解除。serve 啟動時排定這兩個工作失敗只記 log，不擋啟動（備份與告警是附加的維運功能）。
- O16（`alert_states.message`）：表多一個 `message` 欄位，`GET /api/system` 的 `alerts[].message` 讀它。告警持續成立時只更新文字（例如磁碟用量的百分比、yt-dlp 的次數）不重複通知；磁碟用量在門檻與門檻減 5 之間時沿用成立時的文字。
- O17（alert 事件的形狀）：`notify.Alert` 是 `{kind, key, message, since, resolved}`，另有選填的 `summary` 給文字管道的標題（沒有時用 `message`）；resolved 的 `message` 是「已恢復：<摘要>」、`since` 是原本成立的時間。磁碟用量告警的 `key` 與訊息一律寫 `/data`（容器中的名稱），不寫實際的資料目錄路徑。
- O18（yt-dlp 的統計）：YouTube 與 Bilibili 的 yt-dlp 呼叫都記在 `tool_call_stats` 的 `yt-dlp`；runner 本身失敗（無法執行）算失敗，被取消的不記錄；`permanent`（影片不存在、私人、地區限制）不算失敗但算呼叫次數。帶 cookie、結束碼 0 時更新該平台 cookie 的 `last_ok_at`。`cookie_stale` 的基準是「`last_ok_at` 與上傳時間（`updated_at`）較晚的一個」，重新上傳即解除。統計在每次 `alerts` 檢查時清掉 48 小時前的時間格。
- O19（`deploy-check vps`）：網域除了 IP 與 `localhost`，也拒絕 `*.localhost`、沒有點的名稱與網址形式（含 scheme、port、路徑）。DB 以 `mode=ro` 開啟；唯讀掛載下打不開（WAL 的 `-shm` 建不起來）時改以 `immutable=1` 再試一次；DB 存在但仍打不開時結束碼 1 並說明，不當成「沒有密碼」。訊息印在 stderr，通過時印在 stdout；`--json` 輸出 `{ok, message}`。
- O20（`--insecure-no-auth`）：serve 現在認得這個旗標；`vps` 部署拒絕（OPS-R21，在密碼檢查之前）。`local` 部署時 AUTH-R3 的無密碼模式還沒有實作，所以同樣拒絕並說明（之前是「unknown argument」、結束碼 2，現在是啟動失敗、結束碼 1），由 AUTH-R3 的切片補上。後端補洞切片已補上 `local` 的無驗證模式（見 AUTH 待決問題 A23～A26），`local` 部署在條件成立時可以啟動，否則以新的訊息拒絕（結束碼 1）；`vps` 的拒絕不變。（2026-11-24，依 /goal 採 advisor 建議，待 owner 追認）
- O21（compose）：`vps-check` 不設 `restart`（否則永遠不會 `service_completed_successfully`）、資料 volume 唯讀；`caddy` 另有 `caddy-data`、`caddy-config` 兩個 volume 保存憑證；Caddyfile 加 `encode zstd gzip`。`compose.yaml` 的 vps 變數一律 `:-`（O5）。M5 的 `hub` profile 不在這個切片。真正的 `docker compose config`、映像建置與 VPS 上的憑證申請要由 owner 或 CI 驗證（本機沒有 docker）。
- O22（doctor）：OPS-R11～R21 沒有要求 doctor 的檢查，這個切片沒有加；之後可以考慮檢查 `backup.daily.dir` 可寫入。
- O23（安全修補 C：vps 的 trusted_proxies）：vps profile 的所有登入都經 Caddy，沒有 `trusted_proxies` 時來源位址都是 Caddy 的容器位址，任何人連續錯 5 次密碼就讓 owner 一起被鎖（AUTH-R6）。處理：（1）新增環境變數 `NOTESTEME_TRUSTED_PROXIES`（逗號分隔的 CIDR，與設定檔的 `trusted_proxies` 取聯集；這是 SEC-R1「環境變數優先」的例外：compose 對所有 profile 都傳入預設值，取代的話會默默蓋掉使用者在設定檔寫好的 proxy，`auth.proxy` 的驗證會失效。有設定時來源記為 `env`）；（2）`compose.yaml` 把預設網路固定為 `172.31.250.0/24`、`caddy` 固定為 `172.31.250.10`，`notesteme` 與 `vps-check` 的 `NOTESTEME_TRUSTED_PROXIES` 預設為 `172.31.250.10/32`（所有 profile 都傳入；沒有 caddy 時這個位址沒有人使用，而 compose 的 listen 是 `0.0.0.0` 本來就不能用 `--insecure-no-auth`）；（3）`deploy-check vps` 在密碼之後檢查 `trusted_proxies` 不是空的（OPS-R20 的第四項）；（4）`serve` 在 `vps` 部署且 `trusted_proxies` 是空的時啟動記一筆 warn。驗收步驟 `the instance is deployed with the "vps" profile` 比照 compose 一併設定 `NOTESTEME_TRUSTED_PROXIES`，`.feature` 不變。固定網段可能與主機既有網路衝突，`.env.example` 與 compose 的註解說明要一起改的三處。（2026-11-24，依 /goal 採 advisor 建議，待 owner 追認）
- O24（安全修補 C：保留與未來時間的檔名）：OPS-R12 依名稱中的時間保留最新的 `keep` 份；名稱時間在未來的檔案（時鐘曾經錯誤，或手動放入）會一直排在最新，永遠佔住保留名額，真正的備份反而被刪掉。處理：名稱時間比剛寫好的那一份還晚的檔案視為最舊，最先刪除（`keep: 1` 時原本就會刪掉它，行為一致）。代價：時鐘被調回過去之後，之前「未來」的真實備份會先被刪除。（2026-11-24，依 /goal 採 advisor 建議，待 owner 追認）
- O25（yt-dlp 的排程更新，OPS-R6、R7、ADR-0009 第 2 點）：排程工作 `tools.yt-dlp.update`（`key` 固定 `daily`），設定檔 `updates.yt_dlp.{enabled, at, timezone}`，預設 `true`、`04:00`、`UTC`（ADR-0009 寫「排程自動更新」，所以預設開啟；時間避開 03:00 的每日備份）。排定規則同 `backup.daily`（O15：錯過只補跑一次、失敗重試時保留、`enabled: false` 時刪除工作並安靜地解除告警）。每次呼叫和手動更新相同的 `ytdlp.Manager.Update`（同一個實例，互斥鎖涵蓋手動與排程），不帶參數（更新到最新版）。結果的對應：固定版本（OPS-R7）回報略過、已經是最新版、成功切換都算成功；自我檢查失敗而回退時以告警 `ytdlp_update_failed`（`key` 為 `daily`，文字是 OPS-R6 的「yt-dlp 更新後自我檢查失敗，已回到 <版本>。」）經 NOTIFY 通知，排程本身算成功、隔天再試；安裝失敗（pip、venv）時同一個告警（文字固定「yt-dlp 排程更新失敗：安裝沒有完成（詳見 server log）。」，不含 pip 輸出與路徑），回傳錯誤由排程器退避；下一次成功時解除並送「已恢復：yt-dlp 已更新到 <版本>。」。路徑由設定檔或環境變數指定（`tool_externally_managed`）、Windows（`tool_unsupported_platform`）不是失敗，什麼都不做（否則 `job_stale` 會一直告警）；手動更新正在進行時延後 30 分鐘，不算失敗。更新在排程器內同步執行，期間不領取任務（pip 最多 10 分鐘），所以選在清晨。驗收測試的 harness 在設定檔沒有明確寫 `enabled` 時關閉排程更新（`TestControls.NoYtDlpAutoUpdate`）：fake clock 常常跨過 04:00，假的 python 會真的換掉 yt-dlp 或送出告警，影響無關的場景；新增的三個場景明確開啟。真實的 PyPI 更新沒有實測（只用假 python）。（2026-11-24，依 /goal 採 advisor 建議，待 owner 追認）

### NFR-OBS-1 的實作決定（2026-11-25，依 /goal 由實作者暫定，待 owner 追認）

- O26（log 的 `request_id`、`item`、`stage`、`task`）：slog 的 JSON handler 外面包一層 `internal/logctx.Handler`，從 context 補上這四個欄位（呼叫端或 `With` 已經寫了同名欄位時不重複）。`request_id` 的來源：
  - **HTTP 請求**（Client 與 Hub）：最外層的 middleware 決定，請求帶合法的 `X-Request-Id`（1～64 個英數字與 `.`、`_`、`-`）時沿用，否則產生 16 個十六進位字元；回應標頭 `X-Request-Id` 是同一個值，驗證失敗等錯誤回應也有。不合法的值不沿用，避免把任意內容寫進 log。錯誤回應的 body 不新增欄位（`apierr.Error` 沒有 trace 欄位，OpenAPI 不變），用回應標頭對照 log。
  - **任務的每一次執行**：worker 產生一個新的 `request_id`，並帶上 `task`；pipeline 讀到步驟後補上 `item`、`stage`。Agent 提交結果（MCP 的 HTTP 請求）沿用該請求的 `request_id`，另外帶上任務的三個欄位；Batch API 的結果與 Agent 領取時組不出上下文的 log 也補上三個欄位。
  - **每一次定期工作**（清理、排程、訂閱檢查等）：worker 產生一個新的 `request_id`。
  - **解讀**：NFR-OBS-1 的「每筆帶 `request_id`」以「每個請求、每次任務執行、每次定期工作」為範圍。程序層級的 log（啟動、關閉、放回租約、worker 領取失敗、`net/http` 自己的錯誤 log）不屬於任何請求，沒有 `request_id`；它們的時間與訊息已足以對照。log 呼叫必須用 `*Context` 版本並傳入 context 才拿得到這些值，pipeline 與 worker 中有 context 可用的呼叫都已改寫。其他套件中不帶 context 的 log 呼叫（2026-11-25 實測 11 處）：10 處是程序層級（啟動時的警告、排定每日工作失敗、本機 socket、Hub 啟動），1 處在上傳的處理中（`internal/api/upload.go` 建立暫存檔失敗，函式沒有 context 可用），這一處沒有 `request_id`。
  - 檢查：`internal/app/logfields_test.go` 用 production 組出的 logger 實際送出請求、執行失敗的任務，逐行解析 JSON log，斷言每筆請求 log 有 `request_id` 且等於回應標頭、每筆帶 `task` 的 log 有 `item`、`stage`、`request_id`（NFR-OBS-1 的 `ci`）。

### OPS-R22 的實作決定（2026-11-25，依 /goal 由實作者暫定，待 owner 追認）

- O27（OPS-R22：`/api/system` 的版本，NFR-OBS-3；2026-11-25，依 /goal 由實作者暫定，待 owner 追認）：NFR-OBS-3 要求 `/api/system` 回傳程式、schema 與工具的版本，原本的驗證欄寫 SEC-R1，但 SEC-R1 講的是設定的優先順序、場景只斷言 `listen_source`，版本一直沒有被實作也沒有被驗證。決定：
  - 欄位只新增、不改既有的意義（OpenAPI 只加欄位）：頂層 `version`（字串，`internal/app.version`，建置時以 `-ldflags` 寫入）、`schema_version`（整數，`store.SchemaVersion`；讀不到時省略並記 log），`tools.<名稱>.version`（字串，不知道時省略）。
  - 工具版本的指令和 doctor 相同（yt-dlp、Deno 是 `--version`，ffmpeg、ffprobe 是 `-version`）；從第一行取版本號（ffmpeg 是 `version` 後面的字、Deno 是工具名稱後面的字、yt-dlp 是整行），超過 64 字元或含控制字元時視為認不出來。
  - 不讓 `/api/system` 因為外部指令變慢：結果以「解開 symlink 後的路徑、大小、修改時間」為鍵快取在 server 中（成功 10 分鐘、失敗 1 分鐘），沒有快取時四個工具同時執行、共用 2 秒的期限。所以第一次查詢最多多等 2 秒；受管理的 yt-dlp 更新或回退後 symlink 指向的檔案改變，下一次查詢就會重新取得。
  - 驗證：OPS 的場景「The system information reports the program, schema and tool versions」（`features/OPS-operations.feature`）斷言 `version` 存在、`schema_version` 等於目前的 schema 版本（新句型 `the response field {string} is the current schema version`，不寫死會隨 migration 增加的數字）、受管理的 yt-dlp 與假 ffmpeg 的版本；快取、symlink 與逾時由 `internal/app/toolversions_test.go` 守住。假工具沒有 Deno，場景不斷言 Deno。

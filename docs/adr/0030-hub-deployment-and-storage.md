# ADR-0030：Hub 的部署形態、儲存與測試替身

- 狀態：提議中（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）
- 日期：2026-11-23
- 修訂（M5-S0，2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：骨架照本 ADR 建立——`cmd/notesteme-hub`（`serve`、`migrate`、`doctor`、`version`，`admin` 只檢查參數）、`internal/hub`（組裝）、`hubconfig`、`hubapi`、`hubstore`（介面與 `storetest`；`sqlite` 有自己的 goose migration 與 `sqlc.yaml`，CI 的 sqlc job 加了 diff 與 vet；`postgres` 是回 `尚未實作` 的佔位）、`hubblob`（`fs`；`s3` 是佔位）、`hubcli`。共用套件另外抽出 `internal/apierr`（錯誤格式與 `huma.NewError` 的全域設定，Client 與 Hub 在同一個程序內只能有一份）與 `internal/sqlitedb`（SQLite 連線設定；`internal/store` 會帶進 source 與 toolexec，Hub 不能 import）。`internal/hub` 的測試以 `go list -deps` 確認 Hub 不 import pipeline、queue、worker、api、app、store 等套件。版本號的 `-X` 目標是 `internal/hub.version`。Dockerfile 的 `notesteme-hub` target 放在 Client 的 stage 之前（預設 target 仍是 Client），goreleaser 加了 linux 的 `notesteme-hub` build；compose 的 `hub` profile 與 Postgres 的 CI service container 等 Postgres、S3 backend 實作時再加。Hub 的 OpenAPI 快照在 `internal/hub/hubapi/openapi.json`（`go run ./internal/hub/hubapi/cmd/openapi -o internal/hub/hubapi/openapi.json`）；`fakehub.Routes` 的 contract test 原本只確認路由存在，2026-11-24 補上回應必要欄位的比對（以 Hub OpenAPI 的 `required` 為準，見 [HSHR 待決問題 S22](../specs/HSHR-hub-sharing.md)）。
- 修訂（Hub 正式環境基礎建設，2026-11-24，依 /goal 採 advisor 建議，待 owner 追認）：`hubstore/postgres` 實作完成（pgx 連線池＋sqlc 的 postgresql engine，migration 00001～00005 與 SQLite 一對一，goose 以 advisory lock 避免多個實例同時套用）。語意對齊的作法：SQLite 的寫入只有一條連線、交易天然序列化，Postgres 用 READ COMMITTED 加「讀了再寫就先 `SELECT … FOR UPDATE` 鎖住版本或帳號（獨立一句，等鎖之後的下一句取新的 snapshot）」，交易遇到 40001、40P01、23505 時整筆重試（重試時走「已存在」的分支）；時間是 timestamptz、寫入前截到毫秒，布林是 boolean，ID 與排序用的文字欄位 `COLLATE "C"`（與 SQLite 的 BINARY 相同，不受資料庫 locale 影響），`rowid` 的同毫秒排序改用 identity 欄位 `seq`，`provenance_json` 用 TEXT（JSONB 會改寫位元組）。`storetest` 加了並行（第一次登入、移除身分、同一人檢舉、自動隱藏門檻、撤回、同內容分享、停用與投票）與精度、排序的共用案例，兩種 backend 都要通過。設定：`store.dsn` 可以寫在設定檔但不能含密碼，`NOTESTEME_HUB_STORE_DSN` 優先；DSN 沒有指定時連線池 10、連線逾時 10s、`statement_timeout` 30s；錯誤訊息不回顯 DSN。`hubblob.S3`（aws-sdk-go-v2 的 s3，靜態憑證，不讀主機的 AWS 設定與 IMDS；關掉新版 SDK 預設的 CRC32 checksum，改用 Content-MD5 與下載時的長度／sha256 檢查；物件鍵 `[<prefix>/]<sha256>`；單一物件上限同 `bundles.max_file_bytes`）：`blobs.s3.*` 或 `NOTESTEME_HUB_S3_*`，secret 只來自環境變數。上面「Hub 唯一的對外請求是 OAuth 供應商，也走 netguard」補充：Postgres 與 S3 是 owner 設定的連線（常在私有網路上，例如 compose 中的 MinIO），不經過 netguard。部署：compose 的 `hub` profile 放在獨立的 `deploy/docker/compose.hub.yaml`（project `notesteme-hub`）而不是 Client 的 `compose.yaml`，因為 compose 在套用 profile 之前就對整個檔案做變數代入（Hub 的必要密鑰會讓 Client 的 `docker compose up` 失敗），且 Client 的 `notesteme` 服務沒有 profile、在 `--profile hub` 時也會啟動；`.dockerignore` 補上 `web/hub`。CI 加 `hub-backends` job（Postgres service container、MinIO 以 `docker run` 啟動，因為 service container 不能指定 command）與 Postgres 的 `sqlc diff`／`vet`。
- 修訂（S3 相容服務改用 SeaweedFS，2026-11-24，依 /goal 採 advisor 建議，待 owner 追認）：CI 第一次真的跑起來時發現 MinIO 社群版的 image 已經拉不到（`quay.io/minio/minio` 要認證、Docker Hub 沒有該 tag，GitHub repo 在 2026-04 封存），原本固定的 `RELEASE.2025-04-22T22-12-26Z` 讓 `compose.hub.yaml` 的 `hub` profile 與 CI 的 `hub-backends` job 都無法啟動。決定：compose 的 S3 服務改成 SeaweedFS（`chrislusf/seaweedfs:4.55`，Apache-2.0，單一容器 `weed server -s3`，identity 檔由 `NOTESTEME_HUB_S3_*` 在啟動時產生；服務更名 `hub-s3`、`hub-s3-init`，bucket 由 `curlimages/curl` 以 SigV4 呼叫 S3 API 建立）；CI 的 `hub-backends` 用同一版 release binary（固定 sha256）。`hubblob` 的程式不變（只用 PutObject、GetObject、HeadObject、DeleteObject、HeadBucket）；改用 R2 或 AWS S3 的方式不變。已知的代價：SeaweedFS 單容器在 `hub-internal` 上還開了沒有認證的內部服務（filer 8888、master 9333、Iceberg 8181 等），network 是 internal 且只有 Hub 與 migrate 能連，風險低，但 Hub 容器被攻破時可繞過 S3 的簽章；MVP 沒有已知的 MinIO 部署，所以沒有提供資料遷移。待 owner 決定：是否維持 SeaweedFS 為內附的預設，或改成只支援外部 S3（R2、AWS S3）。

## 背景

M5「Hub」是分享字幕與筆記的資源站（plan 第 3.2、20 節）。規格展開時（[HACC](../specs/HACC-hub-accounts.md)、[HREP](../specs/HREP-hub-repository.md)、[HSHR](../specs/HSHR-hub-sharing.md)、[HMCP](../specs/HMCP-hub-mcp.md)）需要先決定幾件跨規格的事：

- plan 第 4 節的架構圖把 Hub 畫成獨立的 `notesteme-hub`；第 23 節的 repo 結構有 `internal/hub/`、`web/hub/`；ADR-0001 決定 Client 與 Hub 共用 Go、同一個 repo、共用 domain 套件與 bundle 格式。
- ADR-0007 決定 Hub 用 Postgres（pgx）加 S3 相容物件儲存；但驗收場景要在程序內執行（ADR-0015），本機與開發機都沒有 Postgres，記憶體也吃緊。
- Hub 的驗收場景有兩種被測系統：Hub 本身（OAuth、權限、倉庫、投票、檢舉、下架、MCP），以及 Client 與 Hub 的串接（分享、重新生成、ASR 前查詢）。對 Client 而言 Hub 是外部服務，依 [specs/README 5.1](../specs/README.md#51-原則) 要用 test double。

## 決策

### 1. 部署形態：獨立的 binary

- Hub 是獨立的程式 `cmd/notesteme-hub`，不是 `notesteme` 的子命令。子命令：`serve`、`migrate`、`admin`（`admin grant|revoke|suspend`，給第一位管理員或救援用）、`doctor`。
- 程式放在 `internal/hub/...`：`hubapi`（Huma handlers，OpenAPI 3.1）、`hubauth`（OAuth、session、PAT、權限）、`hubstore`（sqlc）、`hubblob`（檔案儲存介面）、`hubrepo`（bundle 驗證、去重、衍生關係、篩選）、`hubmcp`（MCP server）。
- 和 Client 共用的套件：`internal/subtitle`（cues 模型、SRT／VTT 渲染）、`internal/bundle`（M4 的 BND 驗證器，Hub 直接呼叫）、`internal/netguard`（Hub 唯一的對外請求是 OAuth 供應商，也走這個 client）、`internal/secrets`（OAuth client secret 的信封加密）、`internal/errclass`。
- Hub 不 import `internal/pipeline`、`internal/queue`、`internal/worker`：Hub 不處理影音，不執行任何外部工具（yt-dlp、ffmpeg），也不抓取 bundle 中出現的任何網址（見 HREP-R12）。
- 設定：`/config/notesteme-hub.yaml`，加上 `NOTESTEME_HUB_*` 環境變數（比照 ADR-0013 的分層）。OAuth client secret、session 金鑰只能來自環境變數或設定檔，不提供 API 修改；群組規則、站台可見度、管理員清單只能寫在設定檔（HACC 規格）。
- Docker：另一個 image target `notesteme-hub`；compose 另有 `hub` profile（Hub、Postgres、S3 相容儲存；原為 MinIO，見修訂），不和 Client 的 `wsl`／`vps` profile 混用。

### 2. 儲存：Postgres 為正式環境，SQLite 為參考 backend

- `hubstore` 用 sqlc，維護兩份查詢檔：`internal/hub/hubstore/sqlite/`（參考 backend：驗收測試、開發、小型自架）與 `internal/hub/hubstore/postgres/`（正式環境）。domain 型別共用，兩份都通過同一組 store 介面的單元測試（ADR-0007 已預留「sqlc 為兩種資料庫分別維護查詢檔」）。
- `hubblob` 是介面，有 `fs`（參考實作，存在 `<data>/blobs/<sha256 前 2 碼>/<sha256>`）與 `s3`（R2、AWS S3、SeaweedFS 等 S3 相容服務）兩種實作。blob 以內容的 sha256 為鍵，天然去重。
- 設定：`store.driver: sqlite|postgres`（預設 `sqlite`，`store.dsn`）、`blobs.driver: fs|s3`（預設 `fs`）。
- Postgres 與 S3 的驗證：CI 之後加 service container 跑 store 介面的單元測試；驗收場景一律跑 SQLite＋fs。
- migration 用 goose，Hub 的 migration 從 `00001` 起獨立編號（不和 Client 的 `internal/store/migrations` 共用）。資料表方向見 [ADR-0031](0031-hub-identity-permissions-and-content.md) 第 5 節。

### 3. 測試替身：依被測系統切開

- **Hub 本身的行為**用程序內的真 Hub：harness 以 `httptest` 啟動 `hubapi`，SQLite 與 blobs 放在場景目錄的 `hub/`。OAuth 供應商用 `features/internal/fakeoauth`，以真的主機名稱掛在 fakenet 上（fakenet 有 CA，可以處理 TLS），Hub 的 OAuth endpoint 寫死在程式中、不能設定，所以場景驗到的就是正式的 URL。
- **Client 與 Hub 的串接**用 `features/internal/fakehub`：記憶體實作 Hub 的 client 契約（[ADR-0032](0032-client-hub-protocol-and-privacy.md)），記錄收到的每個請求（驗證「沒有送出影片 ID」「沒有送出本機路徑」），並支援故障注入（HTTP 錯誤、內容 sha256 不符、超過大小上限、逾時）。
- **一個 `.feature` 只對一種 Hub**：Background 的第一步是閘門句型，二選一：`Given a Hub is running`（真 Hub）或 `Given the owner's client is connected to a Hub`（fakehub）。兩邊共用的 seeding 句型（`Given the Hub has the version …`）寫進 Background 啟動的那一個；兩個都沒有或兩個都有時步驟失敗。
- **契約漂移**：fakehub 的路由清單是 Go 常數 `fakehub.Routes`。實作 child 在 Hub 有 OpenAPI 快照（比照 Client 的 `internal/api` 快照測試）之後，補一個 contract test：`fakehub.Routes` 的每一條都存在於 Hub 的 OpenAPI，且回應的必要欄位相同。

## 考慮過的選項

- **`notesteme hub serve` 子命令**：部署簡單，但 Client 的 binary 會帶著 OAuth、Postgres driver 與 Hub UI，Client 是單一使用者的工具，Hub 是多使用者的公開服務，攻擊面與更新節奏都不同。
- **驗收場景直接用 Postgres（testcontainers 或 embedded-postgres）**：最接近正式環境，但要下載 binary 或跑 Docker，本機記憶體不夠，CI 時間也會再增加（handoff 已記錄 CI 約 23 分鐘）。
- **Client 的場景也用真 Hub**：端到端最真實，但 Hub 與 Client 的實作切片就不能平行進行，故障注入也要另外做一層代理。保留一個 `@manual` 的端到端驗收（plan 第 24 節的 M5 驗收條件）代替。

## 影響

- 好處：
  - 驗收場景在沒有 Postgres 的環境也能跑；Hub 與 Client 的實作切片可以平行。
  - Hub 不執行外部工具、不抓取使用者提供的網址，攻擊面小。
- 代價：
  - 兩份 sqlc 查詢檔要一起維護；Postgres 特有的行為（例如並行投票的鎖）要靠 store 介面的單元測試在 CI 的 Postgres 上驗證。
  - fakehub 與真 Hub 可能漂移，要靠 contract test 守住。
- ADR-0007 的 Hub 段落不改；本 ADR 補上「SQLite 是 Hub 的參考 backend」。

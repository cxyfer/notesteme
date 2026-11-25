# 非功能需求

> 狀態：草案 v0.1（2026-11-12）
> 相關文件：[規格與驗收](README.md)、[計畫](../plan.md)、[UI 規格](../ui/README.md)

這份文件列出跨能力的要求：效能、資源、可靠度、安全、相容性、無障礙、多語系與可測試性。功能行為寫在各份能力規格與 `.feature` 檔；這裡只寫「做得多好」。

## 1. 寫法與驗證方式

- 每項需求有 ID（`NFR-<類別>-<n>`）、目標、驗證方式與里程碑。
- 數值都是**暫定值**，M1 實作時用參考環境實測後再調整；調整要記在第 10 節的修訂紀錄。
- 驗證方式分成四種：

  | 方式 | 說明 | 在哪裡跑 |
  |---|---|---|
  | `bench` | Go benchmark 或負載腳本，資料由產生器建立（`internal/benchdata`） | CI 每晚以完整規模執行（`.github/workflows/bench.yml`）；`workflow_dispatch` 可以只跑某一組、或縮小 `lib-10k` 的比例；PR 有相關變更時自動跑縮小規模的 smoke（位置見第 11.3 節） |
  | `ci` | 每個 PR 都跑的自動檢查（lint、掃描、axe、bundle 大小） | CI 每個 PR |
  | `feature` | 已經有對應的驗收場景，列出規則 ID | `go test ./features/...` |
  | `manual` | 發布前的人工檢查清單 | 每個里程碑結束前 |

- 一項需求沒有達標時，視同驗收場景失敗：不能把該里程碑標記完成。

### 1.1 參考環境

| 名稱 | 規格 | 用途 |
|---|---|---|
| `ref-wsl` | WSL2，4 vCPU、8GB RAM、NVMe SSD | 效能與資源的主要基準 |
| `ref-vps` | 2 vCPU、4GB RAM、SSD | M4 無人值守與資源下限 |

### 1.2 參考資料量

| 名稱 | 內容 |
|---|---|
| `lib-10k` | 10,000 部影音、500 個頻道；每部平均 600 句，共約 600 萬句 cue；每部 3 個版本的產出 |
| `item-long` | 一部 3 小時的影片，約 3,000 句 cue |

## 2. 效能

| ID | 目標 | 驗證 | 里程碑 |
|---|---|---|---|
| NFR-PERF-1 | `GET /api/items`（含篩選、單頁 200 筆）在 `lib-10k` 上 p95 < 200ms。 | `bench` | M1 |
| NFR-PERF-2 | `GET /api/items/:id` 與字幕句列表（`/cues`）在 `item-long` 上 p95 < 150ms。 | `bench` | M1 |
| NFR-PERF-3 | 標題搜尋（LIB-R3，含 2 字以下的掃描）在 `lib-10k` 上 p95 < 300ms。 | `bench` | M1 |
| NFR-PERF-4 | 字幕即時渲染（SRT、VTT、純文字；M2 起含 ASS 與雙語）在 `item-long` 上 < 100ms。 | `bench` | M1 |
| NFR-PERF-5 | 步驟狀態改變到 SSE 事件送出 < 1 秒。 | `bench` | M1 |
| NFR-PERF-6 | 使用 fixture（不含網路）時，一部有人工字幕的 YouTube 影片從新增到可以下載 SRT < 5 秒。用來確認 pipeline 本身沒有多餘的等待。 | `bench` | M1 |
| NFR-PERF-7 | `serve` 冷啟動到 `/readyz` 回傳 200 < 3 秒，不含 migration 與 yt-dlp 自我檢查。 | `bench` | M1 |
| NFR-PERF-8 | CLI 獨立模式的 `items ls --json` 在 `lib-10k` 上 < 1 秒。 | `bench` | M1 |
| NFR-PERF-9 | 全文搜尋在 `lib-10k` 上 p95 < 500ms；2 字以下的掃描查詢 p95 < 2 秒。 | `bench` | M4 |
| NFR-PERF-10 | Web UI：初始載入的 JS（gzip 後）< 300KB；`ref-wsl` 上本機連線的 LCP < 2 秒；媒體庫列表虛擬化，捲動 10,000 列不掉幀。 | `ci`（bundle 大小）、`manual`（LCP 與捲動） | M1 |

## 3. 資源

| ID | 目標 | 驗證 | 里程碑 |
|---|---|---|---|
| NFR-RES-1 | `serve` 閒置時 RSS < 150MB；處理中 < 500MB，不含 ffmpeg、yt-dlp 等子程序。 | `bench` | M1 |
| NFR-RES-2 | 佇列沒有任務時 CPU 使用率 < 1%：不做忙碌輪詢，worker 以通知喚醒，排程以計時器觸發。 | `bench` | M1 |
| NFR-RES-3 | 字幕渲染、匯出與下載都以串流輸出，記憶體用量不隨片長線性成長。 | `bench` | M1 |
| NFR-RES-4 | `/data/tmp` 在任務結束（成功、失敗或取消）時清掉該任務的子目錄；啟動時整個清空（PIPE-R11）。 | `feature`（PIPE-R11、PIPE-R15） | M1 |
| NFR-RES-5 | Docker image（含 ffmpeg、python3、yt-dlp、Deno）< 800MB；原生 binary < 80MB。 | `ci` | M1 |
| NFR-RES-6 | `ref-vps` 上無人值守運作一週，RSS 沒有持續成長（一週內的增量 < 20%）。 | `manual` | M4 |

## 4. 可靠度與資料安全

| ID | 目標 | 驗證 | 里程碑 |
|---|---|---|---|
| NFR-REL-1 | 任何時間點強制結束程序（`kill -9`）或斷電，DB 都不會損毀；重新啟動後依 PIPE-R11 繼續。用故障注入測試在每個步驟的開始、中間、提交前結束程序，各跑 100 次。 | `bench`（故障注入） | M1 |
| NFR-REL-2 | 產出檔先寫到暫存檔、`fsync` 後再 rename；DB 只在檔案就位後才記錄。讀取時比對 `sha256`，不符就標記損毀並重跑該步驟。 | `bench`（故障注入） | M1 |
| NFR-REL-3 | 付費呼叫的結果先寫入產出或任務結果，再標記步驟完成；中斷後重新啟動不會為已經拿到的結果再付一次錢。 | `feature`（PIPE-R5）、`bench`（故障注入） | M1 |
| NFR-REL-4 | SQLite 使用 WAL 與 `busy_timeout`；所有寫入經過同一個寫入連線，不出現 `SQLITE_BUSY` 錯誤。 | `bench`（並行寫入） | M1 |
| NFR-REL-5 | 還原（OPS-R2）在 `lib-10k` 上 < 5 分鐘；備份不阻擋 API 超過 1 秒。 | `bench` | M1 |
| NFR-REL-6 | 每日備份（OPS-06）讓資料遺失最多 24 小時。 | `manual` | M4 |
| NFR-REL-7 | 外部服務（平台、ASR、LLM）長時間無法使用時，不影響瀏覽媒體庫、下載既有字幕與 MCP 查詢。 | `bench`（fake provider 全部失敗） | M1 |

## 5. 安全

各能力的安全行為已經寫在 AUTH、SEC、GRD 的規格與場景中。這裡補充跨能力、不容易寫成單一場景的要求。

| ID | 目標 | 驗證 | 里程碑 |
|---|---|---|---|
| NFR-SEC-1 | 所有 HTML 回應帶 CSP：`default-src 'self'`；`frame-src` 只開放 YouTube 與 Bilibili 的嵌入網域；`script-src` 不允許 `unsafe-inline` 與 `unsafe-eval`；`object-src 'none'`；`frame-ancestors 'none'`。另外設定 `X-Content-Type-Options: nosniff` 與 `Referrer-Policy: same-origin`。 | `ci`（回應標頭測試） | M1 |
| NFR-SEC-2 | 渲染 Markdown（筆記、prompt 預覽、錯誤訊息）時不允許原始 HTML；連結只允許 `http`、`https` 與站內路徑；圖片只允許站內產出。字幕、標題等不可信的文字一律以文字節點輸出，不使用 `dangerouslySetInnerHTML`。 | `ci`（lint 規則與 XSS 測試字串） | M1 |
| NFR-SEC-3 | Secrets 不出現在 log、錯誤訊息、命令列參數與 `/api/system`。測試在每個 `feature` 場景結束時掃描 log 與子程序參數，出現已知的 secret 測試值即失敗。 | `feature`（SEC-R6）、`ci` | M1 |
| NFR-SEC-4 | 每個 PR 跑 `govulncheck` 與前端依賴的弱點掃描；有已知可利用的高風險弱點時不能合併。 | `ci` | M1 |
| NFR-SEC-5 | 容器以非 root 使用者執行；`/data` 下的 DB、secrets 相關檔案權限為 `0600`，目錄為 `0700`。 | `ci`（image 檢查） | M1 |
| NFR-SEC-6 | 處理不可信輸入的解析器（字幕檔、yt-dlp JSON、RSS、bundle manifest）有 fuzz 測試，CI 每晚至少跑 10 分鐘，不得 panic。 | `bench`（`go test -fuzz`） | M1（bundle 在 M4） |

## 6. 可觀測性

| ID | 目標 | 驗證 | 里程碑 |
|---|---|---|---|
| NFR-OBS-1 | Log 使用 `slog` 輸出 JSON；每筆帶 `request_id`，與任務相關的 log 另帶 `item`、`stage`、`task`。 | `ci` | M1 |
| NFR-OBS-2 | 每個 `failed` 或 `blocked` 的步驟都能在 UI 看到錯誤類別、訊息、修正動作與相關的 log 片段，不需要進容器看 log。 | `feature`（PIPE-R2、SRC 的 blocked 場景） | M1 |
| NFR-OBS-3 | `/api/system` 回傳程式版本、schema 版本、yt-dlp、ffmpeg、Deno 的版本與設定來源。 | `feature`（版本：OPS-R22；設定來源：SEC-R1、OPS-R8） | M1 |

## 7. 相容性

| ID | 目標 | 驗證 | 里程碑 |
|---|---|---|---|
| NFR-CMP-1 | 原生 binary 支援 Linux（amd64、arm64）、Windows（amd64）、macOS（arm64）；Docker image 支援 amd64 與 arm64。 | `ci`（goreleaser 建置與冒煙測試） | M1 |
| NFR-CMP-2 | Web UI 支援 Chrome、Edge、Firefox、Safari 的最新兩個主要版本，以及 iOS Safari 與 Android Chrome。 | `manual` | M1 |
| NFR-CMP-3 | HTTP API 由 Huma 產生 OpenAPI 3.1；API 在 M1 之後的變更不可移除欄位或改變欄位意義，需要時新增欄位或端點。破壞性變更要在 ADR 記錄，並在 CLI 與 API 之間檢查版本。 | `ci`（OpenAPI diff） | M1 |
| NFR-CMP-4 | 新版的 CLI 可以連線到舊一個小版本的 `serve`，反之亦然；不相容時明確提示升級哪一邊。 | `bench` | M2 |
| NFR-CMP-5 | Migration 只能往前；每個 migration 在 `lib-10k` 規模的資料上測試，並在 CI 驗證從上一個發布版本升級。 | `ci` | M1 |

## 8. 無障礙與多語系

細節見 [UI 規格第 6、7、8 節](../ui/README.md#6-無障礙)。

| ID | 目標 | 驗證 | 里程碑 |
|---|---|---|---|
| NFR-A11Y-1 | 符合 WCAG 2.2 AA。每個頁面用 axe 自動檢查，沒有 serious 與 critical 等級的問題。 | `ci` | M1 |
| NFR-A11Y-2 | 所有功能都能只用鍵盤完成；焦點框依 UI 規格；可點擊目標至少 24×24px。 | `manual` | M1 |
| NFR-A11Y-3 | 狀態不只靠顏色區分；時間帶都有文字版本；`prefers-reduced-motion` 時沒有動畫。 | `manual`、`ci`（axe） | M1 |
| NFR-I18N-1 | UI 支援繁中、简中、English。CI 檢查三種語言的翻譯 key 一致，元件中沒有寫死的使用者可見字串。 | `ci` | M1 |
| NFR-I18N-2 | API 的錯誤訊息依 `Accept-Language` 選擇語言，預設繁中；錯誤另外帶穩定的 `code`，程式只依 `code` 判斷。 | `feature`（錯誤訊息場景使用預設語言） | M1 |
| NFR-I18N-3 | 日期、數字、金額用 `Intl` 格式化；時間碼不隨語言改變；顯示字幕的元素設定字幕自己的 `lang`。 | `manual` | M1 |
| NFR-RWD-1 | 767px 以下的寬度可以完成三件主要的事：查看進度、處理「需要你處理」、貼網址新增。 | `manual` | M1 |

## 9. 可測試性

| ID | 目標 | 驗證 | 里程碑 |
|---|---|---|---|
| NFR-TEST-1 | `go test ./features/...` 不連真實的網路服務；對外連線一律經過 test double，測試中出現未攔截的對外連線即失敗。 | `ci` | M1 |
| NFR-TEST-2 | 所有時間相關的行為（重試、租約、排程、鎖定）透過可注入的時鐘，場景不使用真實的等待。完整的 `features/` 在 CI 上 < 5 分鐘。 | `ci` | M1 |
| NFR-TEST-3 | `internal/subtitle`、`internal/pipeline`、`internal/queue`、`internal/netguard` 的單元測試涵蓋率 ≥ 80%。 | `ci` | M1 |
| NFR-TEST-4 | 每個 PR 檢查場景與規格的對應：每條規則至少一個場景、每個場景的標籤都存在於規格中、每個步驟都在步驟詞彙中。 | `ci`（見 [README 第 7 節](README.md#7-追溯)） | M1 |

## 10. 修訂紀錄

| 日期 | 版本 | 內容 |
|---|---|---|
| 2026-11-12 | v0.1 | 初版，所有數值為暫定值。 |
| 2026-11-23 | v0.1 | 新增第 11 節：Web UI 相關 NFR 的實作位置（UI-0）。數值未改。 |
| 2026-11-25 | v0.1 | CI 補上四項檢查，數值未改（2026-11-25，依 /goal 由實作者暫定，待 owner 追認）：**NFR-TEST-2** 以不開 `-race` 的執行為準（`-race` 讓場景慢 6～8 倍，另列為 `bdd` job 的安全檢查）；預設 tag 與 `@M3,@M4,@M5` 兩個 job 平行，各自在 CI 內計時並以 300 秒為門檻，完整 features 的牆鐘時間是兩者的最大值；首次實測 89 秒與 123 秒（含編譯）。**NFR-RES-5** 的 image 大小在 `deploy/docker/smoke-test.sh`、binary 大小在 release-check 檢查，以 10^6 計（比 2^20 更嚴）；首次實測 image 322MB、binary 最大 32.2MB（Windows）。**NFR-CMP-3** 的 OpenAPI diff 用 `oasdiff breaking`（v1.37.0）比對推送／PR 之前的 `internal/api/openapi.json` 與 `internal/hub/hubapi/openapi.json`，有破壞性變更時失敗；真的要破壞時依規定先寫 ADR，再把那一行加進 `.github/oasdiff-err-ignore.txt`。**NFR-SEC-4** 的前端弱點掃描除了 PR，再由 web.yml 的 `schedule` 每天跑一次。**NFR-SEC-4 的「不能合併」**需要 branch protection 的 required status checks：MVP 期間不設（會讓 /goal 授權的直接推 master 推不上去），建議 MVP 結束後開啟。 |
| 2026-11-25 | v0.1 | 第 11 節對照實作更新（2026-11-25，依 /goal 由實作者暫定，待 owner 追認）：**NFR-A11Y-3**、**NFR-I18N-3**、**NFR-RWD-1**、**NFR-PERF-10** 的「尚未涵蓋」原本寫著等 UI-1、UI-2 的頁面，這些頁面已經完成，自動檢查的位置改寫為實際的元件與測試；`manual` 的部分改指向新的[人工驗收清單](../manual-acceptance.md)。數值未改。 |
| 2026-11-25 | v0.1 | **NFR-OBS-1** 的實作與 `ci` 檢查（2026-11-25，依 /goal 由實作者暫定，待 owner 追認）：`internal/logctx` 從 context 補上 `request_id`、`item`、`stage`、`task`；HTTP 請求的 `request_id` 與回應標頭 `X-Request-Id` 相同，任務的每次執行與每次定期工作各有一個；檢查在 `internal/app/logfields_test.go`。「每筆」的範圍見 [OPS 待決問題 O26](OPS-operations.md#nfr-obs-1-的實作決定2026-11-25依-goal-由實作者暫定待-owner-追認)：程序層級的 log（啟動、關閉、放回租約、領取失敗）不屬於任何請求，沒有 `request_id`（同日改為帶 boot ID，見本表 NFR-OBS-1 的下一筆）。目標未改。 |
| 2026-11-25 | v0.1 | **NFR-OBS-2** 補上「相關的 log 片段」（2026-11-25，依 /goal 由實作者暫定，待 owner 追認）：新增 PIPE-R16，步驟 `failed` 或 `blocked` 時保存讓它停下的那次執行最後最多 20 行 log（每行最多 500 bytes，寫入前遮蔽 secret），`GET /api/items/:id` 的步驟帶 `log_excerpt`，Web UI 的步驟面板可以展開與複製。來源機制與沒有片段的情況（建立任務前判定的 `budget_blocked`、Agent 逾時）見 [PIPE 待決問題](PIPE-pipeline-queue.md#待決問題)。目標未改。 |
| 2026-11-25 | v0.1 | **NFR-CMP-4** 與 NFR-CMP-3 的「在 CLI 與 API 之間檢查版本」（2026-11-25，依 /goal 由實作者暫定，待 owner 追認）：新增 CLI-R9，CLI 與 `serve` 交換 `X-Notesteme-Version`，major 相同、minor 相差不超過 1 為相容（0.x 也一樣，理由見 [CLI 待決問題](CLI-cli.md#待決問題)），不相容時 `serve` 回 426 `client_incompatible`、CLI 也依回應標頭判斷，都指出要升級哪一邊；判斷在 `internal/compat`。驗證方式寫的是 `bench`：2026-11-25 以 `-ldflags -X internal/app.version=<版本>` 建出 0.2.0、0.3.0、0.4.0、0.5.0、0.6.0 五支 binary，`serve` 用 0.4.0，各版 CLI 經本機 socket 執行 `items ls`：0.3.0、0.5.0 結束碼 0；0.2.0 結束碼 1「CLI 版本太舊（CLI 0.2.0，serve 0.4.0），請升級 CLI。」；0.6.0 結束碼 1「serve 版本太舊（serve 0.4.0，CLI 0.6.0），請升級 serve，或換成 0.4 版的 CLI。」；沒有版本標頭的請求（`curl /healthz`）200 且回應不帶版本。這項實測同日寫成 `cmd/notesteme/compat_test.go`（見本表 NFR-CMP-4 的下一筆）；其他自動檢查是 `features/CLI-cli.feature` 的 CLI-R9 場景（用測試覆寫雙方報告的版本）與 `internal/compat`、`internal/cli/version_test.go` 的單元測試。目標未改。 |
| 2026-11-25 | v0.1 | **NFR-I18N-2** 的後端（2026-11-25，依 /goal 由實作者暫定，待 owner 追認）：新增 AUTH-R23，API 錯誤的 `message` 依 `Accept-Language` 選繁中（預設）、简中或 English，`code` 不變；協商與三語目錄在 `internal/apierr`（`lang.go`、`catalog.go`）。**部分達成**：`internal/api` 建立錯誤的 232 處中，字串常值 110 處、帶參數的範本 10 處、固定句子的 domain 錯誤 9 處已有三語；其餘 103 處（domain 套件組出的訊息、設定驗證的逐欄位訊息、外部服務帶回的訊息等）仍只有繁中，清單與分類見 [AUTH 待決問題 A29](AUTH-access.md#待決問題)；Hub API 與 Huma 的 `details` 不在範圍內。目標未改。 |
| 2026-11-25 | v0.1 | **NFR-OBS-1** 達成「每筆」（2026-11-25，依 /goal 由實作者暫定，待 owner 追認）：context 中沒有 request ID 的程序層級 log 帶這個程序的 boot ID（`boot-` 加 16 個十六進位字元，`logctx.BootID()`），所以每一筆 log 都有 `request_id`；原本不帶 context 的 log 呼叫都改傳 context，正式程式碼中只剩 1 處不帶（本機 socket 拒絕連線，`net.Listener.Accept` 沒有 context，帶 boot ID）。`ci` 檢查兩項：`internal/app/logfields_test.go` 斷言每一筆 log 都有 `request_id`、程序層級的 log 是 boot ID；`internal/logctx/calls_test.go` 以型別資訊掃描 `internal/` 與 `cmd/`，新增不帶 context 的 `slog` 呼叫會失敗（例外清單 `noContextAllowed` 要寫理由）。細節見 [OPS 待決問題 O26](OPS-operations.md#nfr-obs-1-的實作決定2026-11-25依-goal-由實作者暫定待-owner-追認)。目標未改。 |
| 2026-11-25 | v0.1 | **NFR-CMP-4** 的 `bench` 寫成自動測試（2026-11-25，依 /goal 由實作者暫定，待 owner 追認）：`cmd/notesteme/compat_test.go` 的 `TestCLIAndServeVersionsAcrossBinaries` 以 `-ldflags -X internal/app.version=<版本>` 建出 0.2.0～0.6.0 五支 binary，`serve` 0.4.0，各版 CLI 經本機 socket 執行 `items ls`：0.3.0、0.4.0、0.5.0 結束碼 0；0.2.0、0.6.0 結束碼 1 並指出升級哪一邊；沒有版本標頭的 `/healthz` 回應不帶版本。驗證欄仍是 `bench`（建出真的 binary 對打），但跑在 CI 的 `go` job 而不是每晚：五支只差 link，建置快取熱時約 3 秒，`-short` 時略過。決定記在 [CLI 待決問題](CLI-cli.md#待決問題)。目標未改。 |
| 2026-11-25 | v0.1 | **NFR-I18N-2** 補完（2026-11-25，依 /goal 由實作者暫定，待 owner 追認）：原本只有繁中的 103 處與 Hub 的 API 都走 `internal/apierr` 的三語目錄，`internal/api` 與 Hub 的 API 都是 0 處未走目錄。domain 套件照舊回傳繁中，目錄以原本的 `fmt` 格式字串反解出參數再填進目標語言的範本，所以既有場景的預期文字不變；欄位錯誤的頂層 `message` 改用範本 `field_invalid`；Huma 的驗證錯誤的 `message` 帶第一個錯誤的位置與三語的原因（`details` 照 Huma 的原文）；Hub 共用 `apierr.LocalizeTransformer` 與 `WriteFor`。`errors[]`、`details` 等其他欄位照 AUTH-R23 不隨語言改變，外部服務帶回的原文不翻譯，只翻譯包住它的句子。涵蓋數字、例外與理由見 [AUTH 待決問題 A29](AUTH-access.md#待決問題)。目標未改。 |
| 2026-11-25 | v0.1 | **NFR-REL-1～4、REL-7** 的自動驗證與首次實測（2026-11-25，依 /goal 由實作者暫定，待 owner 追認）：位置見第 11.1、11.2 節，設計決定見 [PIPE 待決問題](PIPE-pipeline-queue.md#待決問題)。**REL-1～3**：故障注入在 51 個點（12 個步驟的開始、中間、提交前，產出檔 rename 前後，付費呼叫回來與落地之後）各 `kill -9` 再重新啟動；首次在 GitHub runner 上以完整規模執行（`fault.yml` 的內容，2026-11-25）：51 個點 × 100 次＝5100 次迭代全部通過，耗時 28 分 31 秒（並行 4，job 約 30 分鐘），每次都確認 DB 完整、pipeline 做完且結果與沒中斷的基準相同、每份已記錄產出的 sha256 相符、沒有殘留暫存檔、付費呼叫最多多一次（`paid.landed` 一次都不多）；重新啟動後留下、沒有 DB 記錄指向的正式產出檔共 1086 個（已知限制，不影響正確性）。REL-2 的損毀測試五種字幕產出全部通過。CI 的 `go` job 每點 1 次，約 1 分鐘；每晚由 `fault.yml` 跑每點 100 次。故障注入找到並修正兩個違反 PIPE-R11 的缺陷：產出暫存檔不會被清掉、`frames` 在 commit 前就刪掉候選畫面。**斷電**：`kill -9` 驗證不到，付費結果的交易改用 `synchronous=FULL`（`Store.TxDurable`），其他交易維持 `NORMAL`，人工步驟見[人工驗收清單](../manual-acceptance.md#11-nfr-rel-1斷電後-db-不損毀付費結果不遺失)。**REL-4**：store／queue 與 API 兩層的並行寫入測試（`-race`）沒有出現 `SQLITE_BUSY`，不需要修正；驗證欄仍寫 `bench`，實際以 `ci` 的測試達成。**REL-7**：寫成 PIPE-R17 的驗收場景（`feature`），不另做 `bench`。目標未改。 |
| 2026-11-25 | v0.1 | **`bench` 類 NFR 的每晚 CI 與首次實測**（2026-11-25，依 /goal 由實作者暫定，待 owner 追認）：PERF-1～9、RES-1～3、REL-5、CMP-5 寫成 `internal/nfrbench` 的 Go benchmark，資料由 `internal/benchdata` 產生，`.github/workflows/bench.yml` 每晚以完整規模執行；位置與首次 CI 數字見第 11.3 節。門檻就是本文件的數字，超過時 bench 失敗。解讀：**1.2 的「每部 3 個版本的產出」**解讀為 3 個字幕版本（`source`、`normalized`、`segmented` 各一份產出檔，`segmented` 進全文索引），不是 3 次重跑；**PERF-4、5、6、7、8** 沒有寫百分位數，以取樣的**最大值**判定（最保守）：PERF-4 每種格式 30 次、PERF-5 20 次、PERF-6 10 部、PERF-7 與 PERF-8 各 10 次；**PERF-4** 經過真正的下載端點量（讀檔、驗證 sha256、解碼、渲染、寫出），比只量渲染函式更嚴；**PERF-5** 量「送出重跑請求」到「client 收到該步驟的 `stage.status`」，多含一次 HTTP 請求，是上限；**PERF-7** 的「不含 migration 與 yt-dlp 自我檢查」：先啟動一次讓 migration 跑完，之後每次都是新的程序；`/readyz` 目前不做 yt-dlp 的自我檢查（OPS-R6 的 TODO），不需要扣掉這段時間；**PERF-1、3、9** 每種查詢各自以 p95 判定（PERF-1 14 種 × 50 次、PERF-3 7 種 × 50 次、PERF-9 每種 50 次、2 字以下 30 次；PERF-9 的單頁是 API 預設的 20 筆）；**RES-1** 執行真正的 binary 讀 `/proc/<pid>/status`：閒置取就緒後 10 秒內每 200ms 的 VmRSS 最大值，處理中取 VmHWM（20 部 3 小時人工字幕的影片，yt-dlp 的替身是子程序，不計入）；**RES-2** 就緒 5 秒後取樣 60 秒的 utime＋stime；**RES-3** 的「不隨片長線性成長」：1 小時（1,000 句）與 10 小時（10,000 句）各下載 3 次，取請求期間存活 heap 比請求前多出的量的最小值，10 小時 ÷ max(1 小時, 512KB) < 3（成長不超過線性的 30%；512KB 是這個量法的解析度）；**REL-5** 的「備份不阻擋 API」：`POST /api/backup` 期間連續打 `GET /api/items?limit=50`，最慢的一次 < 1 秒；還原以真正的 binary 執行 `notesteme restore`（含還原前的快照）；**CMP-5** 的「每個 migration 在 lib-10k 規模的資料上測試」：從空的 DB 逐版往前套用，每版之前把已經存在的資料表填滿 lib-10k 的資料，最後判定 `integrity_check`、FTS 的 integrity-check 與每張表的列數（問題數 0）；因為要產生約 5GB 的資料，放在每晚而不是每個 PR。「從上一個發布版本升級」：尚無發布版，以目前 schema（37）的 fixture 代替，`internal/store/upgrade_test.go` 在 CI 的 `go` job 每次都跑；發布後的步驟見第 11.3 節。**首次 CI 實測**（scratch run 45519420142，優化前）全部達標的項目見第 11.3 節。**優化後達標**：**PERF-8** 優化前兩次 CI 是 774ms 與 1173ms（後者那台 runner 整體慢約 3 成），把列表每頁逐一查頻道名稱改成一次查詢；優化的效果以同一台機器的 A/B 為準：本機 `items ls --json` 1.08～1.37s → 0.77～0.82s（約快 3～4 成）。優化後的 CI 只有一次樣本 529ms（run 45531362669，較快的 runner，同一次 run 的 PERF-2、4 也比第一次快約 3～4 成）；以同樣比例推估，較慢的 runner 上約 750～800ms，仍低於 1 秒。**已知未達標**（`known-misses` job，`continue-on-error`，照門檻判定、不 skip）：**PERF-9** 命中很多的查詢做了一輪最佳化（全文搜尋的命中只掃一次，`COUNT(*) OVER ()` 取代另一次計數），優化前→後（CI run 45519420142 → 45525989803／45531362669，三次是不同的 runner：45525989803 整體慢約 3 成、45531362669 快約 3～4 成，所以前後的差異混有 runner 的差異）：常見詞 1560→945／609ms、3 字中文 2045→1589／1028ms、以語言篩選常見詞 1618→960／616ms，已知未達標：實測 609～1589ms vs 目標 500ms；2 字以下的掃描 英文 4192→2590／1786ms、中文 4435→2995／1989ms、1 字 3227→2065／1396ms，較快的 runner 上通過、較慢的超過，已知未達標：實測 1396～2995ms vs 目標 2000ms（不穩定，仍列在 known-misses）。剩下的成本是 modernc SQLite 的 trigram 索引與 instr 掃描本身（每晚的資料約 600 萬句），要再快需要換索引結構或 cgo 的 SQLite，超出一輪有限度的最佳化；**RES-3** 的 ASS、雙語 SRT 與 bundle 匯出沒有修，單軌串流不適用：ASS 要整份算樣式、雙語要配對兩軌、bundle 在記憶體中組 zip，已知未達標：實測 7.45～10.62×（ASS 7.45×、雙語 SRT 8.19×、bundle 10.62×，增量約 6.5～7.6MB）vs 目標 < 3×；SRT、VTT、純文字與 cues JSON 改為逐句串流後達標（0.25～0.50×）。目標未改。 |

## 11. 實作位置（Web UI）

UI-0（2026-11-23）之後各項 UI 相關需求的自動檢查放在哪裡；`manual` 的部分仍要在里程碑結束前人工檢查，步驟與記錄方式見[人工驗收清單](../manual-acceptance.md)。

| ID | 實作與自動檢查 | 尚未涵蓋 |
|---|---|---|
| NFR-PERF-10 | `web/client/scripts/check-bundle.mjs`（web.yml 的 bundle budget，UI-0 約 137KB／300KB）；列表虛擬化：`src/components/VirtualList.tsx`（`@tanstack/react-virtual`，用在媒體庫、佇列與字幕句列表）、`src/components/list.test.tsx` | LCP、10,000 列捲動的實際流暢度（`manual`，見[人工驗收清單](../manual-acceptance.md#9-nfr-perf-10web-ui-的-lcp-與-10000-列捲動)） |
| NFR-SEC-1 | `internal/webui`（CSP 與其他標頭）；`internal/webui/webui_test.go` 斷言標頭與 `index.html` 沒有 inline script；`check-bundle.mjs` 再檢查一次建置產物 | — |
| NFR-SEC-2 | eslint 禁止 `dangerouslySetInnerHTML`（`web/client/eslint.config.js`） | Markdown 渲染與 XSS 測試字串（有 Markdown 的頁面再做） |
| NFR-SEC-4 | web.yml 的 `npm audit --audit-level=high`（PR 與每天的 `schedule`） | branch protection 的 required checks（MVP 結束後開啟） |
| NFR-A11Y-1 | `web/client/src/test/axe.ts`、`src/test/a11yRoutes.ts`（每頁）、元件測試；對比由 `src/styles/contrast.test.ts` 計算 | — |
| NFR-A11Y-2 | skip link、換頁後焦點移到 `<main>`、`:focus-visible` 焦點框、Dialog 焦點鎖定與歸還（`src/router.test.tsx`、`src/components/components.test.tsx`） | 完整的鍵盤操作、焦點框與目標大小的實機檢查（`manual`，見[人工驗收清單](../manual-acceptance.md#5-nfr-a11y-2全鍵盤可用焦點框目標-24px)） |
| NFR-A11Y-3 | 狀態不只靠顏色：`StatusBadge`（形狀＋文字，`src/components/StatusBadge.tsx`、`components.test.tsx`）、時間帶各狀態的紋理（`web/shared/src/status-band.css`）。時間帶的文字版本：精簡版是 `role="img"` 加 `aria-label` 摘要（`web/shared/src/StatusBand.tsx`、`src/shared/StatusBand.test.tsx`）、完整版是 `role="slider"` 加 `aria-valuetext` 與逐列的文字標籤（`src/shared/StatusBandFull.test.tsx`）、媒體庫的列另有文字版的狀態欄（`src/pages/library/LibraryRow.tsx` 的 `StatusBadge`）。`prefers-reduced-motion`：`src/styles/base.css`（全域關閉動畫與轉場）、`src/styles/tokens.css`（動態時間為 0）、`src/styles/components.css`（Spinner 不轉）、`web/shared/src/status-band.css`（「顯影」改成靜態斜線紋） | 灰階與色覺模擬下仍分得出狀態、作業系統開啟「減少動態」時實際沒有動畫（`manual`，見[人工驗收清單](../manual-acceptance.md#6-nfr-a11y-3狀態不只靠顏色時間帶文字版本reduced-motion)） |
| NFR-OBS-2 | 步驟面板的 log 片段：`src/pages/item/StagePanel.tsx`（`<details>` 收合、等寬、`translate="no"`、複製），`src/pages/item/ItemPage.test.tsx`（只在 failed／blocked 顯示、複製、axe）；後端見 PIPE-R16 | — |
| NFR-I18N-1 | `web/client/scripts/check-i18n.mjs`（key、空字串、參數）、eslint `i18next/no-literal-string` | — |
| NFR-I18N-2 | Client 與 Hub 的 API client 送 `Accept-Language`、只依 `code` 判斷（`web/client/src/api/client.ts`、`describeError.ts`，`web/hub/src/api/` 同名檔案）；Client 與 Hub 的後端依它選訊息語言（AUTH-R23，`internal/apierr`）。呼叫點的涵蓋由 `internal/api/i18n_sites_test.go`（Client，0 處未走目錄）與 `internal/apierr/domain_texts_test.go`（domain 套件與 Hub，0 處未走目錄）檢查；場景見 AUTH-access.feature 的 `@AUTH-R23` | 照 AUTH-R23 不翻譯的範圍（不是缺口）：`errors[].message`、`details` 等其他欄位、外部服務帶回的原文、MCP tool 的錯誤與 CLI 的輸出（見 [AUTH 待決問題 A29](AUTH-access.md#待決問題)） |
| NFR-I18N-3 | `formatDateTime`、`formatNumber`、`formatTimecode`（`src/i18n/index.ts`）；字幕元素的 `lang`：字幕句 `src/pages/item/CueList.tsx`、影音標題 `ItemPage.tsx`、筆記 `NotePanels.tsx`、摘要 `SummaryBlock.tsx`、時間帶的說明 `web/shared/src/StatusBand.tsx`（`describeLang`），`src/pages/item/ItemPage.test.tsx` 斷言標題與字幕句的 `lang` | 三種 UI 語言下實際的日期、數字、金額格式與字型，時間碼不隨語言改變（`manual`，見[人工驗收清單](../manual-acceptance.md#7-nfr-i18n-3intl-格式化時間碼不隨語言字幕-lang)） |
| NFR-CMP-2 | `light-dark()` 等 CSS 功能都在支援範圍內 | 實機檢查（`manual`，見[人工驗收清單](../manual-acceptance.md#4-nfr-cmp-2瀏覽器支援)） |
| NFR-CMP-3 | `internal/api/openapi.json` 快照（`TestOpenAPISnapshot`）、web.yml 的型別 drift 檢查、ci.yml 的 `openapi-diff`（`oasdiff breaking`，比對推送或 PR 之前的快照） | 與「上一個發布版本」的 diff（尚無發布版）。「CLI 與 API 之間檢查版本」：CLI-R9（`internal/compat`、`internal/api/middleware.go` 的 `versionMiddleware`、`internal/cli/backend.go`），見 NFR-CMP-4 的修訂紀錄 |
| NFR-RWD-1 | 767px 以下的底部分頁列（總覽、媒體庫、新增、佇列、更多）：`src/shell/TabBar.tsx`、`src/shell/tabbar.test.tsx`、`src/styles/base.css`；三件事的頁面都已存在：進度（總覽的「現在」、佇列）、「需要你處理」（總覽、媒體庫的狀態欄）、貼網址新增（`src/add/AddDrawer.tsx`） | 在 767px 以下的真實手機或窄視窗完成這三件事（`manual`，見[人工驗收清單](../manual-acceptance.md#8-nfr-rwd-1767px-以下完成三件事)） |

### 11.1 故障注入的位置（NFR-REL-1～3）

第 11 節的表格是 Web UI；這一小節是可靠度類 NFR 的故障注入（2026-11-25，依 /goal 由實作者暫定，待 owner 追認）。規則與設計決定記在 [PIPE 的待決問題](PIPE-pipeline-queue.md#待決問題)（「NFR-REL-1～3 的故障注入」「產出暫存檔的清理」「frames 候選畫面的刪除時機」「斷電與 `synchronous`」四項）。

- **故障點**：`internal/faultpoint`。只在 `-tags faultinject`（unix）建置有作用；一般建置的 `Hit` 是空函式，正式 binary 不含故障注入的程式碼（`faultpoint_test.go`：`go list` 確認一般建置只編入 `off.go`、`cmd/notesteme` 依賴的是這一份，`go tool nm` 確認正式 binary 沒有任何 faultpoint 的符號；faultinject tag 只能出現在 `internal/faultpoint` 與測試檔）。`NOTESTEME_FAULT=<名稱>:<n>` 讓程序在該點第 n 次命中時對自己送 `SIGKILL`；`NOTESTEME_FAULT_TRACE` 記錄每次命中。

  | 點 | 位置 | 對應 |
  |---|---|---|
  | `stage.start/<步驟>` | `pipeline.Handle` 讀到步驟之後 | REL-1「步驟的開始」 |
  | `stage.mid/<步驟>` | `pipeline.Handle` 的步驟工作做完（外部呼叫已回來、產出檔已寫出）、結果寫回之前 | REL-1「中間」 |
  | `stage.commit/<步驟>` | `applyResult` 的交易做完所有寫入、commit 之前 | REL-1「提交前」 |
  | `artifact.tmp-written` | `writeArtifactFile` 的暫存檔 fsync 之後、rename 之前 | REL-2 |
  | `artifact.renamed` | `writeArtifactFile` rename 並 fsync 目錄之後、DB 記錄之前 | REL-2 |
  | `paid.returned/<來源或 llm.步驟>` | 付費字幕來源（`runFallback`）或 LLM chunk（`llmChunk`、`trnChunk`）的結果回來、還沒落地 | REL-3 |
  | `paid.landed/llm.<步驟>` | LLM chunk 的結果已 commit、步驟還要等 assemble 才標記完成 | REL-3 |

  劇本命中的點共 51 個：12 個步驟 × 3 個時點、產出檔的 2 個點、`paid.returned/groq`，以及 `summary`、`segment`、`correct`、`translate`、`chapters`、`note` 六個 LLM 步驟的 `paid.returned` 與 `paid.landed`。基準執行沒有命中其中任何一個就失敗（`requiredPoints`），重構掉某個點或劇本不再經過某個步驟時，每晚的 100 次不會默默變成空跑。

- **harness**：`features/fault`（整個套件只在 `-tags faultinject` 建置）。父程序重新執行同一個測試 binary 當子程序，子程序以 godog 跑固定劇本：`features/steps` 的步驟與 fake provider（不連真實網路，NFR-TEST-1）；一部沒有平台字幕、有三個場景切換的 YouTube 影音，處理方案打開 pipeline 的全部 12 個步驟：字幕來自 groq（ASR），`summary`、`segment`、`correct`、`translate`、`chapters` 走 `api` executor（anthropic），`frames` 依規則挑畫面，`note` 用 `video-notes` 範本，`publish` 寫到 fs vault 並輸出 SRT，加上 `metadata`、`normalize`、`sponsorblock`。每次迭代：
  1. 子程序在指定的點被 `SIGKILL`；父程序確認結束原因是 SIGKILL，而且 trace 的最後一行就是那個點（避免空跑）。
  2. 複製被殺當下的 DB＋WAL，`PRAGMA integrity_check` 與 `foreign_key_check`。
  3. 另一個子程序在同一份 `/data` 上重新啟動，處理到閒置（PIPE-R11）；再檢查一次完整性。
  4. 斷言：沒有未結束的任務、每個步驟都是 `done` 或 `skipped`、步驟狀態與現行產出（字幕句的時間與文字）與沒中斷的基準相同、每份已記錄產出的檔案存在且 sha256 與大小相符、沒有殘留的 `.tmp-*`。
  5. 付費呼叫：fake provider 收到請求時先把「主機、路徑、body 的 sha256」附加到 `/data` 之外的計數檔並 fsync，再回應。每個請求最多比基準多一次、多一次的最多一個（被殺時在途中的那一個）；在 `paid.landed` 被殺時一個都不能多。
- **損毀測試**（REL-2）：`TestCorruptArtifactIsDetectedAndRerun` 對 `source`、`normalized`、`segmented`、`corrected`、`translated` 五份字幕產出各改一個 byte，經 API 讀取時回 409 `artifact_corrupt`、產出被標記 `corrupt_at`、產生它的步驟重跑，做完後結果與基準相同。重新產生 `source` 要再呼叫一次 groq（規格已記錄的成本），其他四種不多付錢（LLM 的 chunk 層快取照常重用）。
- **次數**：每個點的迭代次數由 `NOTESTEME_FAULT_ITERATIONS` 控制。CI 的 go job 每點 1 次、並行 4（`.github/workflows/ci.yml`，`ref-wsl` 上約 1 分鐘，含損毀測試）；每晚的 `.github/workflows/fault.yml`（`schedule` 每天 19:47 UTC，也可 `workflow_dispatch` 指定次數）每點 100 次、並行 4，失敗時把保留的資料目錄上傳成 artifact。首次實測見第 10 節的修訂紀錄。
- **找到的缺陷**：故障注入找到兩個真的違反 PIPE-R11 的問題，都已修正並有回歸測試：rename 之前被殺留下的 `.tmp-*` 永遠不會被清掉（`pipeline.CleanArtifactTemps`、`internal/pipeline/artifacts_clean_test.go`），以及 `frames` 在 commit 之前就刪掉候選畫面、重新啟動後步驟永久失敗（`internal/pipeline/frames_restart_test.go`）。
- **做不到的**：斷電。`kill -9` 不會遺失已 commit 的交易，所以故障注入驗證不到 `synchronous(NORMAL)` 在斷電時可能遺失最後幾筆交易的情況；依據與人工步驟見[人工驗收清單](../manual-acceptance.md#11-nfr-rel-1斷電後-db-不損毀付費結果不遺失)。

### 11.2 並行寫入與外部服務中斷的位置（NFR-REL-4、REL-7）

（2026-11-25，依 /goal 由實作者暫定，待 owner 追認）

- **NFR-REL-4**：兩個測試，CI 的 `go` job 以 `-race` 跑（`go test (race, without the scenarios)`）。
  - `internal/queue/concurrency_test.go` 的 `TestConcurrentWritesNeverBusy`（store／queue 層）：8 個寫入者各建立 25 部影音（一半用 `TxDurable`）、4 個 worker 反覆領取與完成任務、佇列設定的寫入者、3 個讀取者、`VACUUM INTO` 備份，以及 4 個在交易中「先讀再寫」同一個設定的計數者。斷言沒有任何 `SQLITE_BUSY`／`database is locked`／`BUSY_SNAPSHOT`、每個任務剛好完成一次、計數器剛好是 200（沒有遺失更新）、2 分鐘內做完（沒有卡住）。刻意把寫入連線改成 4 條時這個測試會失敗（`database is locked (5) (SQLITE_BUSY)`），證明它驗證得到「所有寫入經過同一個寫入連線」。
  - `internal/app/concurrent_writes_test.go` 的 `TestConcurrentAPIWritesNeverBusy`（API 層）：8 個用戶端經 HTTP 各新增 10 部影音，穿插改佇列上限、暫停與繼續、改全域設定，同時有讀取者與備份；斷言沒有 5xx、log 中沒有 `SQLITE_BUSY`、最後的資料與寫入的一致。
  - 驗證欄寫 `bench`，但要驗證的是「不出現」而不是數值，所以寫成 `ci` 的測試；`busy_timeout` 沿用 `internal/sqlitedb` 的設定，沒有調整。
- **NFR-REL-7**：PIPE-R17 的驗收場景（`features/PIPE-pipeline-queue.feature`，`@PIPE-R17`），由 `Given every external service is unavailable` 讓假 yt-dlp 與 fakenet 上所有 fake origin 持續失敗；驗證瀏覽媒體庫、影音詳情、SRT／VTT 下載與 MCP `get_transcript` 照常回應，新增的影音在重試用盡後以 `failed`（`retryable`）結束。理由見 [PIPE 待決問題](PIPE-pipeline-queue.md#待決問題)「PIPE-R17 與 NFR-REL-7 的驗證方式」。

### 11.3 bench 的位置

（2026-11-25，依 /goal 由實作者暫定，待 owner 追認）

- **資料**：`internal/benchdata`（`lib-10k`、`item-long`，決定論：同一個 seed 每晚的資料與命中數相同），`go run ./internal/benchdata/cmd/genlib -out <目錄> -profile lib-10k [-scale 0.05]` 產生；正式 binary 不依賴它（`TestNotImportedByBinaries`）。完整的 `lib-10k` 在 GitHub runner 上產生約 10～14 分鐘、約 5GB。
- **bench**：`internal/nfrbench`。in-process 的 `serve`（真正的 HTTP handler、worker 與 SQLite，網路全部拒絕，yt-dlp／ffmpeg 是替身），或以 `NOTESTEME_BENCH_BIN` 執行真正建置的 binary。每個 bench 自己取樣固定次數（`-benchtime=1x`），以 `b.ReportMetric` 報告 p50／p95（或最大值），超過門檻時失敗；每一筆結果寫進 `NOTESTEME_BENCH_REPORT` 的 JSONL，`scripts/ci/bench-summary.py` 轉成 job summary。沒有 `NOTESTEME_BENCH_LIB` 時以 `NOTESTEME_BENCH_SCALE`（預設 0.01）的資料執行，只能當 smoke。
- **觸發**（`.github/workflows/bench.yml`）：`schedule` 每天 18:43 UTC；`workflow_dispatch` 的 `group`（`all`、`item`、`lib10k`、`known-misses`）與 `scale`；`pull_request` 在相關目錄有變更時只跑 `smoke`（×0.01，本機約 2 分鐘），不影響 `ci.yml`。每個 job 上傳 `bench-report.jsonl` 與 go test 的輸出。每晚三個 job 平行，牆鐘時間約 15～30 分鐘。

| ID | Benchmark | job | 判定 | 兩次 CI 實測（run 45519420142 → 45531362669，不同 runner） |
|---|---|---|---|---|
| NFR-PERF-1 | `BenchmarkPerf1ItemsList` | `lib10k` | 14 種查詢，各 p95 < 200ms | 最慢 `group_status` 104 → 81ms |
| NFR-PERF-2 | `BenchmarkPerf2ItemDetail` | `item` | p95 < 150ms | 詳情 1.7 → 0.9ms、cues 11.8 → 7.5ms |
| NFR-PERF-3 | `BenchmarkPerf3TitleSearch` | `lib10k` | 7 種查詢，各 p95 < 300ms | 最慢 `q_and_filter` 26 → 20ms |
| NFR-PERF-4 | `BenchmarkPerf4Render` | `item` | 每種格式最大值 < 100ms | 最慢 ASS 55 → 41ms |
| NFR-PERF-5 | `BenchmarkPerf5SSE` | `item` | 最大值 < 1s | 15 → 11ms |
| NFR-PERF-6 | `BenchmarkPerf6IngestToSRT` | `item` | 最大值 < 5s | 85 → 86ms |
| NFR-PERF-7 | `BenchmarkPerf7ColdStart` | `item` | 最大值 < 3s | 153 → 142ms |
| NFR-PERF-8 | `BenchmarkPerf8CLIItemsLs` | `lib10k` | 最大值 < 1s | 774 → 529ms（優化後） |
| NFR-PERF-9 | `BenchmarkPerf9FullTextSearch`；`BenchmarkKnownMissPerf9` | `lib10k`；`known-misses` | 各 p95 < 500ms；2 字以下 < 2s | 罕見詞 7.9 → 3.3ms、片語 165 → 72ms；已知未達標見第 10 節 |
| NFR-RES-1 | `BenchmarkRes1RSS` | `item` | 閒置 < 150MB、處理中 < 500MB | 閒置 110 → 112MB、處理中 201MB |
| NFR-RES-2 | `BenchmarkRes2IdleCPU` | `item` | < 1% | 0.02% |
| NFR-RES-3 | `BenchmarkRes3Streaming`；`BenchmarkKnownMissRes3` | `item`；`known-misses` | 10h ÷ max(1h, 512KB) < 3× | SRT／VTT／純文字／cues JSON 最大 0.41 → 0.50×；ASS／雙語／bundle 已知未達標見第 10 節 |
| NFR-REL-5 | `BenchmarkRel5BackupRestore` | `lib10k` | 備份期間 API 最大值 < 1s、還原 < 300s | 13 → 8ms、67 → 58s |
| NFR-CMP-5 | `BenchmarkCmp5Migrations`；`TestUpgradeFromReleasedSchemas` | `lib10k`；`ci.yml` 的 `go` job | 問題數 0；每份 fixture 升級後資料完整 | 37 個 migration 0.73 → 0.58s、問題 0 |

兩次實測之間，PERF-8 與 PERF-9 有程式變更（第 10 節），其他項目沒有；那些項目的差異來自 runner（第二次的 runner 整體快約 3～4 成）。

- **已知未達標**：命名為 `BenchmarkKnownMiss*`，只在 `known-misses` job 跑（`continue-on-error: true`，summary 標 ⚠️ KNOWN-MISS），照門檻判定、超過照實列出，不 skip。達標之後把那一組查詢搬回原本的 bench，並在第 10 節記錄。
- **CMP-5 的升級 fixture**：`internal/store/testdata/upgrade/schema-37/` 是目前 schema 的資料目錄（尚無發布版）。每次發布前，若 `testdata/upgrade/schema-<發布版的 schema 版本>/` 不存在，執行 `go test ./internal/store -run TestUpgradeFromReleasedSchemas -update-upgrade-fixture -count=1` 產生並提交；fixture 提交後不再修改也不刪除，之後 CI 驗證從每一個發布版升級。細節見 `internal/store/testdata/upgrade/README.md`。

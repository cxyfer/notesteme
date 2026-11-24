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
  | `bench` | Go benchmark 或負載腳本，資料由產生器建立 | CI 每晚；PR 有相關變更時手動觸發 |
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
| 2026-11-25 | v0.1 | **NFR-OBS-1** 的實作與 `ci` 檢查（2026-11-25，依 /goal 由實作者暫定，待 owner 追認）：`internal/logctx` 從 context 補上 `request_id`、`item`、`stage`、`task`；HTTP 請求的 `request_id` 與回應標頭 `X-Request-Id` 相同，任務的每次執行與每次定期工作各有一個；檢查在 `internal/app/logfields_test.go`。「每筆」的範圍見 [OPS 待決問題 O26](OPS-operations.md#nfr-obs-1-的實作決定2026-11-25依-goal-由實作者暫定待-owner-追認)：程序層級的 log（啟動、關閉、放回租約、領取失敗）不屬於任何請求，沒有 `request_id`。目標未改。 |
| 2026-11-25 | v0.1 | **NFR-OBS-2** 補上「相關的 log 片段」（2026-11-25，依 /goal 由實作者暫定，待 owner 追認）：新增 PIPE-R16，步驟 `failed` 或 `blocked` 時保存讓它停下的那次執行最後最多 20 行 log（每行最多 500 bytes，寫入前遮蔽 secret），`GET /api/items/:id` 的步驟帶 `log_excerpt`，Web UI 的步驟面板可以展開與複製。來源機制與沒有片段的情況（建立任務前判定的 `budget_blocked`、Agent 逾時）見 [PIPE 待決問題](PIPE-pipeline-queue.md#待決問題)。目標未改。 |
| 2026-11-25 | v0.1 | **NFR-CMP-4** 與 NFR-CMP-3 的「在 CLI 與 API 之間檢查版本」（2026-11-25，依 /goal 由實作者暫定，待 owner 追認）：新增 CLI-R9，CLI 與 `serve` 交換 `X-Notesteme-Version`，major 相同、minor 相差不超過 1 為相容（0.x 也一樣，理由見 [CLI 待決問題](CLI-cli.md#待決問題)），不相容時 `serve` 回 426 `client_incompatible`、CLI 也依回應標頭判斷，都指出要升級哪一邊；判斷在 `internal/compat`。驗證方式寫的是 `bench`，目前以 `features/CLI-cli.feature` 的 CLI-R9 場景（用測試覆寫雙方報告的版本）與 `internal/compat`、`internal/cli/version_test.go` 的單元測試代替；兩個真正不同版本的 binary 互連要等第一個發布版本之後，放進發布前的檢查。目標未改。 |

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
| NFR-I18N-2 | API client 送 `Accept-Language`、只依 `code` 判斷（`src/api/client.ts`、`src/api/describeError.ts`） | — |
| NFR-I18N-3 | `formatDateTime`、`formatNumber`、`formatTimecode`（`src/i18n/index.ts`）；字幕元素的 `lang`：字幕句 `src/pages/item/CueList.tsx`、影音標題 `ItemPage.tsx`、筆記 `NotePanels.tsx`、摘要 `SummaryBlock.tsx`、時間帶的說明 `web/shared/src/StatusBand.tsx`（`describeLang`），`src/pages/item/ItemPage.test.tsx` 斷言標題與字幕句的 `lang` | 三種 UI 語言下實際的日期、數字、金額格式與字型，時間碼不隨語言改變（`manual`，見[人工驗收清單](../manual-acceptance.md#7-nfr-i18n-3intl-格式化時間碼不隨語言字幕-lang)） |
| NFR-CMP-2 | `light-dark()` 等 CSS 功能都在支援範圍內 | 實機檢查（`manual`，見[人工驗收清單](../manual-acceptance.md#4-nfr-cmp-2瀏覽器支援)） |
| NFR-CMP-3 | `internal/api/openapi.json` 快照（`TestOpenAPISnapshot`）、web.yml 的型別 drift 檢查、ci.yml 的 `openapi-diff`（`oasdiff breaking`，比對推送或 PR 之前的快照） | 與「上一個發布版本」的 diff（尚無發布版）。「CLI 與 API 之間檢查版本」：CLI-R9（`internal/compat`、`internal/api/middleware.go` 的 `versionMiddleware`、`internal/cli/backend.go`），見 NFR-CMP-4 的修訂紀錄 |
| NFR-RWD-1 | 767px 以下的底部分頁列（總覽、媒體庫、新增、佇列、更多）：`src/shell/TabBar.tsx`、`src/shell/tabbar.test.tsx`、`src/styles/base.css`；三件事的頁面都已存在：進度（總覽的「現在」、佇列）、「需要你處理」（總覽、媒體庫的狀態欄）、貼網址新增（`src/add/AddDrawer.tsx`） | 在 767px 以下的真實手機或窄視窗完成這三件事（`manual`，見[人工驗收清單](../manual-acceptance.md#8-nfr-rwd-1767px-以下完成三件事)） |

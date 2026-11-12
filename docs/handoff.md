# 交接：M1 開發進度

> 更新：2026-11-13，`master` 位於 `625f5d7`
> 用途：讓下一個 session 或協作者不用重讀全部對話就能接手。內容會隨切片推進而更新；規則與決策以規格、ADR 為準，這裡只做索引與摘要。

## 1. 現況

- **Repo**：`github.com/cxyfer/notesteme`（private），base branch 是 `master`。Go module 路徑沿用 `github.com/cxyfer/notesteme`。
- **已完成**：
  - 切片 0：可執行的驗收骨架。
  - 切片 1：已登入的 owner 新增有人工字幕的 YouTube 影片，下載到 SRT。
  - 依審查結果修正，以及 PR #1（引入 sqlc）。
- **驗收結果**（預設 tag：`@M1 && ~@manual && ~@pending-m0 && ~@wip && ~@ui`）：187 個場景，50 passed、137 pending、0 failed、0 undefined。

  | Feature | passed | pending |
  |---|---|---|
  | AUTH | 12 | 11 |
  | CLI | 1 | 8 |
  | DEL | 0 | 21 |
  | GRD | 2 | 18 |
  | ING | 12 | 12 |
  | LIB | 0 | 10 |
  | NRM | 9 | 2 |
  | OPS | 1 | 12 |
  | OUT | 8 | 0 |
  | PIPE | 0 | 18 |
  | SEC | 2 | 8 |
  | SRC | 3 | 17 |

  M1 的 OUT 場景全部通過，但 OUT 的 M2 部分還沒做，所以規格狀態仍是 `ready`，不是 `implemented`。

- **規格狀態**：AUTH、SEC、ING、SRC、NRM、OUT 是 `ready`，其餘都是 `draft`。還沒有任何規格達到 `implemented`，因為每份都還有 pending 的場景。

## 2. 下一步

使用者暫定的切片順序：**PIPE（佇列、重試、重啟恢復）** → CLI 與 `/data` 鎖 → GRD → 其餘 SRC 來源 → OPS → LIB → DEL。

開始 PIPE 前：

1. 讓 `scout` 列出 `features/PIPE-pipeline-queue.feature` 的 `@M1` 場景與用到的步驟（目前 18 個場景全部 pending）。
2. 對 PIPE 做 DoR 檢查。目前的待決問題只有「退避是否依資源類別區分」，有預設值。通過後把狀態改成 `ready`。
3. PIPE 需要但還沒實作的部分，見第 6 節的「PIPE」。

## 3. 怎麼執行

```
go build ./... && go vet ./... && gofmt -l .
go test -count=1 -race -timeout 7m ./...                    # 單元測試＋驗收場景＋規格一致性檢查
go test ./features -run TestFeatures -v                     # 只跑驗收場景（預設 tag）
go test ./features -run TestFeatures -godog.tags="@PIPE-R3" # 指定 tag；排除條件會自動併入
go generate ./internal/store/                               # 重新產生 sqlc 查詢
go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 diff -f internal/store/sqlc.yaml   # 檢查生成的程式碼是最新的
go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 vet  -f internal/store/sqlc.yaml

# 每次回報前都要跑的規格檢查（docs/specs/README.md 5.3）
python3 -I scripts/spec/check_steps.py .
python3 -I scripts/spec/check_trace.py .
uv run --with gherkin-official python -I scripts/spec/parse_all.py features
```

- 帶 `-godog.*` 旗標時寫 `./features`，不要寫 `./features/...`，因為子 package 沒有定義這些旗標。
- Go 版的規格檢查在 `features/speccheck_test.go`，和 Python 版的結果一致：1491 個步驟、167 個句型、216 條規則、108 則故事、275 個場景。另外會檢查 `steps.md` 的每個句型都已註冊。

## 4. 工作規則

這些規則來自使用者，不能違反：

- **規格優先**：行為和規格不一致時，不可以為了讓測試通過去改 `.feature`。先寫進該規格的「待決問題」並問使用者；同意後，規格與場景放在同一個 commit 修改。
- **只用 `steps.md` 的句型**：需要新句型時，先加進 `steps.md`，並在回報中列出。
- **紅燈要紅得正確**：寫完 step definition 先跑一次，確認失敗原因是「功能還沒做」。目前多數失敗會被「501 → pending」轉成 pending，要看到真正的紅燈時，可以暫時關掉這個判斷。
- **測試不連外網**（NFR-TEST-1）；時間一律用注入的 fake clock（NFR-TEST-2）。
- **不要讓已實作的步驟依參數回傳 pending 來避開失敗**。場景的步驟都已實作、行為還沒做時：
  - 小的行為就直接實作。
  - 大的就讓真正還沒實作的那個步驟維持 pending。
- **subagent 分工**：
  - `architect`（opus，唯讀）：設計。
  - `builder`（sonnet）：實作；每次只給一個範圍明確的工作，並行時檔案所有權不重疊，並使用 `isolation: "worktree"`。
  - `scout`（haiku，唯讀）：查詢與摘要。
  - 每個切片綠燈後，另開一個 `builder` 只做審查：只給 diff 與規格，不給結論，只回報不修改。
  - 最後的驗收指令一定要親自跑，subagent 說通過不算證據。
- **Commit**：沿用 repo 歷史的格式，例如 `✨ feat(auth): …`、`🧪 test(features): …`、`📝 docs(specs): …`。每個切片綠燈後各自 commit。
- **Git**：
  - 現在已有 remote：開 `<repo>/.worktrees/<slug>/` 的 worktree 與分支，推送並建立 PR。
  - 沒有使用者明確授權不合併。合併用 squash，subject 保留 PR 編號。
  - 不直接 push `master`。
- **回報內容**：
  - 通過的場景（數量、tag、`go test` 摘要）。
  - 仍 pending 或失敗的場景與原因。
  - 新增或修改的步驟、ADR、待決問題。
  - commit 清單。
  - 需要使用者決定的事，附上建議。

## 5. 架構重點

驗收測試的完整設計見 [ADR-0015](adr/0015-acceptance-test-harness.md)。

- **入口**：`cmd/notesteme/main.go` 是唯一讀 `os.Environ` 與 `time.Now` 的地方，把它們轉成 `app.Env` 後交給 `internal/app`。`app.Env` 包含環境變數 map、時鐘、`netguard.Deps`、外部工具 runner、listener、log，以及只給測試用的 `Test *TestControls`（手動 worker、低成本雜湊）。
- **測試中的執行方式**：
  - server 與 CLI 都在測試程序內執行。
  - `When the server restarts` 用 `Server.Kill()` 模擬，不釋放租約。
  - worker 是手動模式：
    - `RunRound` 領取一輪任務、標記為 running，並在 goroutine 中開始執行，不等完成就回傳。要斷言任務停在 running 時，用假工具的 gate 擋住，不要依賴時序。
    - `RunUntilIdle` 執行到沒有任務為止。
- **測試替身**（`features/internal/`）：
  - `harness.World`：每個場景一份，包含暫存根目錄、env map、fake clock、fakenet、假工具狀態。
  - `fakenet`：可指定的 DNS 解析加程序內的 fake origin；也會攔截 `http.DefaultTransport` 等繞過 netguard 的連線。
  - `faketool`：測試 binary 以 `yt-dlp`、`ffmpeg`、`ffprobe` 的名稱被執行時，改扮假工具，並把每次呼叫記在 `calls.jsonl`。
  - `fakesource`：腳本化的 ASR 與 Supadata。
- **步驟的約定**：
  - 送出請求的步驟會在 server 尚未啟動時隱含啟動它。
  - 還沒實作的 API 回 `501 not_implemented`，斷言步驟遇到時轉成 pending。例外：預期 401，或預期 403 且請求缺憑證時，不轉。
  - After hook 發現的違規（未攔截的連線、secret 外洩）記在收集器中，`TestFeatures` 結束後讓測試失敗。
  - 回應 JSON 是子集比對；陣列依序比對前 N 個元素。
- **程式地圖**（`internal/`）：

  | 套件 | 內容 |
  |---|---|
  | `app` | server 啟動、啟動防護、flock、手動 worker、測試用的 seeding |
  | `config` | 設定檔與環境變數（含 `asr`、`trusted_proxies`） |
  | `store` | SQLite（單一寫入連線、WAL）、goose migration（`migrations/`）、sqlc 查詢（`queries/` → `sqlcdb/`） |
  | `auth` | argon2id 密碼、session、CSRF（double-submit）、依來源位址的登入鎖定 |
  | `secrets` | master key 包裝資料金鑰（XChaCha20-Poly1305） |
  | `api` | Huma 路由、驗證與 CSRF middleware、items、cues、subtitles、resolve、settings |
  | `queue`、`worker` | 租約佇列、退避（30 秒、2 分鐘）、worker |
  | `pipeline` | metadata → transcript → normalize、重跑、快取鍵、網址辨識 |
  | `transcript` | 依 `caption_priority` 選字幕軌 |
  | `source/youtube` | yt-dlp 的 Probe 與字幕下載 |
  | `toolexec` | 子程序（process group、輸出上限、環境變數 allowlist、OPS-R8 的尋找順序） |
  | `netguard` | 對外 HTTP：在連線時封鎖私有網段與 metadata 位址 |
  | `subtitle` | cue 模型、VTT／SRT 解析、Normalize（NRM-R1～R6）、渲染、檔名 |
  | `errclass`、`clock` | 錯誤類別、可注入的時鐘 |

## 6. 已知待辦與技術債

審查時發現，但刻意延到對應切片處理的項目：

- **PIPE**：
  - 執行期沒有回收過期的租約，只在啟動時回收；`CompleteTask` 等不檢查 `lease_token`。
  - 正常關閉沒有 30 秒的寬限期（PIPE-R12）。
  - 產出檔的 sha256 不符時，應該標記損毀並重跑（NFR-REL-2）。
  - 並行上限沒有依資源類別區分（PIPE-R8）。
  - 暫停（PIPE-R10）、保留最近 3 版（PIPE-R7）、取消（PIPE-R15）都還沒做。
  - `Given the implementation version …` 需要可覆寫的 `impl_version`。
- **SRC**：
  - yt-dlp 的錯誤分類：目前全部當成 `retryable`，403 還沒對應到 SRC-R9。
  - 原文語言未知時，應該從 `*-orig` 字幕軌推斷。
  - 還沒做：Supadata、ASR、自動字幕等待（SRC-R6）、cookie（SRC-R8）、品質檢查（SRC-R4）。
  - 做 `youtube-auto` 前，要評估改用 yt-dlp 的 `json3` 格式（見 NRM 待決問題）。
- **GRD**：netguard 的 allowlist 還沒接到設定檔；`the host … resolves to` 等網路步驟還沒實作。
- **OPS**：migration 前的快照（OPS-R3）；DB schema 比程式新時拒絕啟動（OPS-R4）；`/readyz` 限速。
- **AUTH**：
  - token（AUTH-R7～R9）還沒做，bearer 一律回 401。
  - `X-Forwarded-Proto` 應該只採用可信 proxy 的值。
  - IPv6 的鎖定以單一位址計數。
- **其他**：
  - `Given the item … has been processed up to …` 還沒實作，需要 `features/fixtures/items/`。
  - 字幕讀取端沒有串流（NFR-RES-3 只做到輸出端）。
  - 有 HTML 回應時要加上 CSP 等標頭（NFR-SEC-1）。
  - 還沒有 CI。建立時要包含：`go test -race ./...`、規格檢查、`sqlc diff`、`govulncheck`。

## 7. 仍有效的待決問題

都有預設值，不阻塞開發；細節見各規格的「待決問題」段落。

| 規格 | 問題 | 預設 |
|---|---|---|
| AUTH | session 閒置期限 30 天是否太長 | 30 天 |
| SEC | master key 的格式 | 32 bytes base64 直接用，否則用 argon2id 推導 |
| ING | 上傳大小上限；inbox 穩定判定時間 | 4GB；10 秒 |
| SRC | 品質檢查與幻覺過濾的門檻；Groq 是否有 word 級時間戳 | 依 M0；相關場景為 `@pending-m0` |
| NRM | 300ms、40ms、1 秒等門檻；自動字幕改用 `json3` 的評估 | 暫定值；做 `youtube-auto` 前決定 |
| OUT | sidecar 檔名的語言代碼 | M2 實作前實測 |
| PIPE | 退避是否依資源類別區分 | 不區分 |
| CLI | Windows 原生 binary 的 client 模式 | 只支援 `--server` |
| OPS | 自我檢查用的已知影片 | 內建一部，可在設定檔覆寫 |

## 8. 已確定的技術選擇

- **Go 版本**：go.mod 維持 `go 1.25`，依賴釘在不會把 `go` 指令升到 1.26 的版本（例如 huma v2.34.1、goose v3.26.0、modernc sqlite v1.39.1）。執行 `go get -u` 前要先確認這點。
- **sqlc**：
  - 不放進 go.mod，因為 sqlc v1.31.1 要求 go 1.26；用 `go run …@v1.31.1` 執行，需要 gcc 與 cgo。
  - `queries/*.sql` 的註解只能用英文：sqlc 的 sqlite parser 遇到非 ASCII 字元會讓後面查詢的參數錯位。
  - `ClaimTasks` 用 JSON 陣列加 `json_each`，不用 `sqlc.slice`。
- **排程**：自寫，不用 gocron v2（plan v0.3）。
- **godog**：v0.16.0，M1 期間使用非 strict 模式；M1 完成時改成 strict，並拿掉「501 → pending」的約定。

## 9. 環境注意事項

- 本機是 WSL2，Go 1.26.8。
- Bash 工具有一個 hook 會把指令改寫成 `rtk …` 的形式。自動模式會要求用改寫後的指令重送一次，照做即可。
- 自動模式會擋下破壞性的 git 操作，例如 `git branch -D`、`git push --delete`。需要時請使用者執行，或先在設定中允許。
- 待清理（內容都已在 `master`）：
  - `.worktrees/store-sqlc` 與 `refactor/store-sqlc` 分支（本機與遠端）。
  - `.claude/worktrees/` 下 6 個 builder 用過的 worktree，以及 `worktree-agent-*` 分支。
  - `.worktrees/m1-skeleton`、`feat/m1-skeleton`、`docs/bdd-specs`、`docs/plan-review-revisions` 都已併入 `master`，可以一併移除。

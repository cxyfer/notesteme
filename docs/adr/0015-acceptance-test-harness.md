# ADR-0015：驗收測試在程序內執行，外部依賴在注入點替換

- 狀態：已採納
- 日期：2026-11-12
- 修訂：2026-11-13，排程確定自寫，不用 gocron v2；server 停下後的狀態斷言不隱含啟動；CLI 的入口改為 `cli.Execute`

## 背景

- 驗收場景（`features/*.feature`）由 godog 執行，是 M1 的完成標準（[規格 README](../specs/README.md)）。
- NFR-TEST-1：測試不能連真實的網路服務，出現沒有攔截的對外連線就要失敗。NFR-TEST-2：時間相關的行為都要用可注入的時鐘，完整的 `features/` 在 CI 上要少於 5 分鐘。
- 場景會操作 `serve` 的啟動、正常關閉與非正常中斷（`When the server restarts`），也會執行 CLI 的獨立模式與 client 模式（`/data` 的 flock，ADR-0007）。
- yt-dlp、ffmpeg 一律以子程序執行（ADR-0009）；對外 HTTP 一律經過 `internal/netguard`（ADR-0014）。
- 很多場景沒有寫 `Given the server is running`，只有 `Given the owner is signed in` 就直接送出請求。

## 決策

### 執行方式

- **`serve` 在測試程序內執行。** 程式的入口是 `internal/app`：
  - `cli.Execute(ctx, args, IO, Env)` 是所有子命令的入口，`cmd/notesteme/main.go` 只把 `os` 的內容轉成 `app.Env` 再呼叫它；`serve` 與 `--version` 由 `cli.Execute` 轉給 `app.Run`（`internal/cli` 依賴 `internal/app`，反過來不行）。
  - `app.Env` 帶入環境變數 map、時鐘、網路依賴、外部工具 runner、listener 與 log 輸出。程式裡只有 `main.go` 可以讀 `os.Environ` 與 `time.Now`。
  - `When the server restarts` 用 `Server.Kill()` 模擬：取消 root context、結束子程序的 process group、關閉 DB 與檔案，不執行釋放租約的流程。真正的 `kill -9` 由 NFR-REL-1 的故障注入 bench 驗證。
- **CLI 也在程序內執行**，呼叫 `cli.Execute`，回傳結束碼與實際使用的模式（`standalone` 或 `client`），`Then the command ran in {word} mode` 直接比對這個值。三種模式都透過 HTTP API：獨立模式用程序內的 handler，本機 client 用 `/data/notesteme.sock`，`--server` 用 TCP 與 bearer。`the owner runs` 不會隱含啟動 server。
  - `/data` 的鎖用 `flock(2)`，鎖專用檔 `/data/notesteme.lock`。Linux 上同一程序內兩個 open file description 的 flock 也會互斥，所以程序內的 CLI 能偵測到程序內的 `serve`。不使用 `fcntl` 鎖，它在同一程序內不互斥。
  - 鎖檔內容記錄持有者與連線位址，client 模式依此連到 `serve`。
- 另外保留一個冒煙測試：實際建置 binary，執行 `--version` 與 `serve` 的啟動與關閉，補上程序內執行跳過 `main()` 的部分。

### 測試替身

| 依賴 | 替身 | 注入點 |
|---|---|---|
| 時間 | `clock.Fake`（`Now`、`Set`、`Advance`） | `app.Env.Clock` |
| worker | 手動模式：`RunRound`（領取並標記 running 後回傳）、`RunUntilIdle`（執行到沒有可執行的任務，上限 1000 輪） | `app.Env.Test` |
| yt-dlp、ffmpeg、ffprobe | 測試 binary 本身：`TestMain` 依 `argv[0]` 的檔名改扮假工具，讀場景的狀態檔與 `features/fixtures/` | 每個場景的 `bin/` 放 symlink，`PATH` 指到這裡 |
| DNS 與 HTTP origin | `fakenet`：resolver 依 `resolves to` 回傳指定的 IP（`and then` 依序回傳，模擬 rebinding）；dialer 把通過 netguard 檢查的目標轉到本機的 fake origin。origin 以 `Route(url, handler)` 登記：每個主機一組路由，以 path 加 query 完全相同才算比對成功，沒有登記的路徑回 404，可與整個主機共用的 `Handle` 共存（先 Route，再退回 Handle）。內建回應：`redirects to` 回 302、`serves "<name>"` 回 `features/fixtures/http/<name>`、`serves N bytes` 回總長剛好 N bytes 並帶 Content-Length 的合法 RSS（開頭與結尾之間以註解填充，讓大小上限的場景驗證的是上限，而不是「不是 feed」） | `netguard.Deps{Resolver, Dialer, RootCAs}` |
| ASR、Supadata 等字幕來源 | 腳本化的 fake origin，依步驟回傳 fixture、回報沒有結果或要求 cookie，並記錄呼叫次數 | 同上，掛在 fakenet 上 |
| `/data`、`/config`、inbox | 每個場景一個 `os.MkdirTemp("", "nst")` 的根目錄 | `NOTESTEME_CONFIG`、`NOTESTEME_DATA` |
| 環境變數 | 每個場景一份 map，不呼叫 `os.Setenv` | `app.Env.Vars` |

- **假工具**：
  - 每次呼叫都把 argv、工作目錄與 cookie 檔權限附加到 `calls.jsonl`，供呼叫次數、secret 掃描（NFR-SEC-3）與 SEC-R7 使用。
  - 遇到未知參數就以結束碼 2 結束，及早發現程式和替身的介面不一致。
  - 狀態檔中有「gate」時會阻塞，讓 `When the worker starts one round` 之後任務維持 `running`。
- **netguard 的介面**：netguard 自己解析 DNS、檢查 IP，只把通過檢查的 `Target{Host, IP, Port}` 交給注入的 `Dialer`；production 的 `Dialer` 用 `net.Dialer.Control` 再檢查一次實際連線的 IP。這樣測試走的是真實的檢查路徑，只有最後一跳被替換。
- **未攔截的連線讓測試失敗**：
  - fakenet 遇到沒有登記的目標就記錄違規，場景的 After hook 讓場景失敗。
  - `TestMain` 把 `http.DefaultTransport` 換成一律失敗的版本，`net.DefaultResolver` 改用一律失敗的 `Dial`，繞過 netguard 的連線也會失敗。
  - `Then no connection was made to {string}` 比對 fakenet 的連線紀錄。
- **時間**：重試、租約、`not_before`、登入鎖定都以「DB 中的時間 ≤ `Clock.Now()`」判斷，不 sleep。`When the clock advances by …` 只推進時鐘，到期的工作在下一次 worker 執行時處理。Scheduler 也必須提供可由測試同步驅動的介面，所以排程自己寫，不用 gocron v2（plan 第 22 節）。

### 場景的隔離與代入

- `World` 每個場景建立一份，持有根目錄、環境變數、fake clock、fakenet、server 與 CLI 的最後結果。
- 虛擬路徑 `/data`、`/config` 等在設定檔內容、CLI 參數、請求 body、檔案路徑中改寫成場景根目錄下的路徑（只在路徑段的邊界比對）；比對輸出前做反向改寫。影音代號與 `{token:<名稱>}` 依 [steps.md 第 2 節](../specs/steps.md#2-影音的引用方式)代入。
- 預設環境：`NOTESTEME_PASSWORD`、`NOTESTEME_MASTER_KEY` 有測試用的預設值，`PATH` 只含場景的 `bin/`。場景用 `Given the environment variable … is not set` 移除。
- **需要 server 的步驟會隱含啟動 server**：`Given` 步驟只累積設定，第一個送出請求的步驟才以當時的設定啟動。`Given the owner is signed in` 也是在第一次送出請求時才建立 session。
  - 例外：場景明確停下 server（`When the server shuts down gracefully`）之後，讀取狀態的 `Then` 步驟直接唯讀開啟 SQLite 查詢，不啟動新 server。新 server 啟動時會回收租約（PIPE-R11），會讓 PIPE-R12 的斷言恆真（2026-11-13，PIPE 切片審查以變異測試發現）。
  - 「任務正在執行」除了 DB 中的租約，也檢查被 gate 擋住的假工具程序是否仍然存活（`kill(pid, 0)`），否則取消、正常關閉沒有結束子程序也驗證不到。
- listener 一律綁 `127.0.0.1:0`，並記錄設定的位址與實際位址的對照。
- 場景之間沒有共用狀態，可以用 `-godog.concurrency` 並行；先設為 1，穩定後再調高。

### godog 的設定

- `features/features_test.go` 的 `TestFeatures` 執行全部 `.feature` 檔。
- 預設 tag：`@M1 && ~@manual && ~@pending-m0 && ~@wip && ~@ui`。用 `-godog.tags` 指定時，排除條件會自動併入每一個 OR 項，除非指定的 tag 本身就是被排除的 tag。godog 的 tag 表達式只支援 `&&`、`,`（OR）與 `~`，不支援括號。
- M1 期間使用非 strict 模式，pending 不算失敗。
  - 還沒實作的 API 路由先回傳 `501`（`code: not_implemented`）。斷言回應的步驟遇到這種回應時回傳 pending，而不是失敗，讓「步驟已實作、功能還沒做」的場景維持 pending；404 等其他狀態仍然算失敗。改成 strict 前要拿掉這個約定。
  - After hook 發現的違規（未攔截的連線、secret 外洩）另外記在收集器中，`TestFeatures` 結束後逐筆讓測試失敗。godog 會把 hook 的錯誤和 pending 合併，非 strict 模式下不算失敗，所以不能只靠 hook 的回傳值。改由 `features/speccheck_test.go` 靜態檢查，確保沒有 undefined 與 ambiguous 的步驟：
  - Gherkin 語法（取代 `parse_all.py`）。
  - 每個步驟都在 `steps.md` 中（取代 `check_steps.py`）。
  - 規則、故事與 tag 的對應（取代 `check_trace.py`），有問題就失敗。
  - `steps.md` 的句型和 `features/steps/` 註冊的定義雙向相等；每個步驟剛好對到一個定義。
- 句型轉成 regex：`{string}` → `"([^"]*)"`、`{int}` → `(-?\d+)`、`{word}` → `(\S+)`；`{int}` 後面以 `s` 結尾的名詞，`s` 可以省略。
- 句型完全相同、只差在 `Given` 和 `Then` 的兩組步驟（`the {string} transcript of {string} is:` 與指定語言的版本）依關鍵字分開註冊，其他步驟不分關鍵字。

## 考慮過的選項

- **`serve` 以子程序執行**：最接近真實部署，但時鐘、worker、資料 seed 都要透過測試專用的 endpoint 跨程序控制，慢又容易不穩定。
- **CLI 每次都執行建置好的 binary**：拿不到同一個 fake clock 與 fakenet。
- **另外 `go build` 一支假的 yt-dlp**：可行，但多一次建置，還要對齊 build flags。
- **在 `http.RoundTripper` 層替換網路**：會跳過 netguard 檢查實際 IP 的路徑，SSRF 場景驗證不到真實行為。
- **gocron v2 加 fake clock**：時鐘推進後仍然非同步觸發，`When the worker runs until idle` 無法確定已經處理完。
- **strict 模式**：切片 0 所有步驟都是 pending，strict 會讓整個測試失敗；M1 完成時改為 strict。

## 影響

- 好處：
  - 一個場景約 0.1～0.3 秒，完整的 `features/` 容易維持在 5 分鐘內。
  - 時間、網路、外部工具都是確定性的，沒有真實等待。
  - 測試走的是 netguard 的真實檢查路徑。
- 代價：
  - production 程式要從第一天就經過 `app.Env` 取得環境變數、時間與網路，不能直接用 `os`、`time.Now`、`http.DefaultClient`。用 lint 規則強制。
  - 程序內的 `restarts` 只是近似，真正的程序中斷要靠故障注入 bench。
  - 假工具要跟著程式使用的 yt-dlp 參數一起維護；未知參數直接失敗，可以及早發現不一致。
- 之後要注意：
  - `serve` 的 log 寫到每個場景各自的 buffer，After hook 掃描 log 與 `calls.jsonl` 中的 secret 測試值（NFR-SEC-3）。
  - client 模式的 CLI 等待任務完成時，harness 要在 CLI 執行期間推動手動 worker。

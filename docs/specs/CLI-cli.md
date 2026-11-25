# CLI：CLI 與資料擁有權

- 狀態：ready
- 里程碑：M1
- 相關：plan 第 17 節；ADR-0007
- 驗收場景：[`features/CLI-cli.feature`](../../features/CLI-cli.feature)

## 目的

CLI 和 Web UI 的能力一致，適合給腳本與 Agent 使用。同一份資料同時只有一個擁有者，CLI 會自動判斷要直接開 DB，還是透過正在執行的 `serve`。

## User stories

| ID | Story |
|---|---|
| CLI-01 | As the owner, I want CLI commands to work the same whether or not the server is running, so that I never have to think about which mode I am in. |
| CLI-02 | As an agent, I want machine-readable output and meaningful exit codes, so that I can script Notesteme reliably. |
| CLI-03 | As the owner, I want a CLI on Windows to talk to the instance running in WSL Docker, so that my agents on Windows can use it. |
| CLI-04 | As the owner, I want the existing `watch` skill to become a thin wrapper over the CLI, so that agents only spend effort on the parts that need thinking. |

## 規則

| ID | 規則 |
|---|---|
| CLI-R1 | `serve` 啟動時對資料目錄取得 flock；已經有其他 `serve` 持有時，拒絕啟動，訊息為「資料目錄已經由另一個 notesteme serve 使用中。」 |
| CLI-R2 | 其他 CLI 指令偵測到 flock 時，自動改用 client 模式，透過 `serve` 的本機 socket 執行；沒有 flock 時用獨立模式直接開 DB。本機 socket 是 `/data/notesteme.sock`（權限 0600），只接受與 `serve` 相同 uid（或 root）的連線（uid 檢查只在 Linux；其他平台只靠檔案權限），以 owner 身分執行，不需要 session 或 token。`serve` 正在啟動或另一個 CLI 指令持有 flock 時，CLI 會短暫重試（約 2 秒）再回報。資料目錄的路徑太長、無法建立 socket 時，`serve` 照常啟動但不提供 socket，CLI 提示改用 `--server`。獨立模式取得 flock 期間，`serve` 拒絕啟動。 |
| CLI-R3 | 指定 `--server <url> --token <t>` 時一律使用 client 模式，不檢查本機的資料目錄。token 也可以用環境變數 `NOTESTEME_TOKEN` 提供（`--token` 優先）。不跟隨轉址，避免 token 被帶到其他位址；`http://` 且不是 loopback 時警告 token 會以明文傳送。 |
| CLI-R4 | client 模式中執行的操作，事件會出現在 `serve` 的 SSE 中；獨立模式中，scheduler 不會啟動。 |
| CLI-R5 | 所有查詢類指令都支援 `--json`，輸出欄位和 HTTP API 的回應一致。`ingest --json` 每部影音輸出一個物件：影音的欄位，加上 `stages`（步驟名稱對應狀態，例如 `{ "transcript": "done" }`）；多部影音時每行一個物件（JSON Lines）。 |
| CLI-R6 | 結束碼：`0` 成功；`1` 執行錯誤；`2` 參數錯誤（在判斷模式之前檢查，API 回 400、422 也算）；`3` 需要使用者處理（`blocked`）；`4` 等待中（`waiting` 或仍在 `pending`，例如 `--wait` 超過 `--timeout` 時）。 |
| CLI-R7 | `notesteme ingest --until <stage> --wait` 會等到指定步驟完成或停住才結束，並依 CLI-R6 回傳結束碼。 |
| CLI-R8 | `notesteme doctor` 依序檢查 ffmpeg（含 libass）、ffprobe、yt-dlp、Deno、各 provider 的 API key 與網路連線，每項顯示 `ok`、`warn` 或 `fail` 與修正方式；有任何 `fail` 時結束碼為 1。Deno 與 API key 缺少時為 `warn`。 |
| CLI-R9 | CLI 對 `serve` 的每個請求帶 `X-Notesteme-Version`（程式版本）；`serve` 收到時回應也帶自己的版本。major 相同、minor 相差不超過 1 為相容（新版的 CLI 可以連到舊一個小版本的 `serve`，反之亦然，NFR-CMP-4）；版本無法解析（例如開發版 `dev`）或沒有帶時視為相容。不相容時 `serve` 回 426 `client_incompatible`，CLI 也檢查回應標頭，兩邊都以結束碼 1 提示要升級哪一邊：「CLI 版本太舊（CLI <版本>，serve <版本>），請升級 CLI。」或「serve 版本太舊（serve <版本>，CLI <版本>），請升級 serve，或換成 <major.minor> 版的 CLI。」沒有這個標頭的請求（瀏覽器、MCP、Agent）不檢查，回應也不帶版本。 |

## 介面

```
notesteme ingest <url|ref|path> [--profile p] [--until <stage>] [--wait [--timeout <duration>]] [--json]
notesteme items ls [--status <stage>=<status>] [--channel <c>] [--json]
notesteme items show <item> [--json]
notesteme items rerun <item> --from <stage>
notesteme export <item> --format srt|vtt|txt|ass [--lang <l>] [--variant <v>] [--style <name>] [--out <path>]
notesteme import <subtitle> --item <item> [--lang <l>] [--format srt|vtt|ass]   （OUT-R13；M2）
notesteme doctor [--json]
notesteme mcp [--token <t>] [--list-tools] [--json]   （MCP-R1；M2：stdio 的 MCP server，轉送到 API 的 /mcp）
notesteme backup | restore   （ADR-0007；在 OPS 實作，不屬於 M1 的 CLI 切片）
notesteme config export [--out <path>] [--json]   （SEC-R10；M2：輸出 DB 管理的設定，不含 secrets）
notesteme config import <file> [--json]   （SEC-R10；M2：先驗證整份，有錯誤就整份不套用）
全域參數：--server <url> --token <t> --data <dir>（可以放在子命令之前或之後）
```

- 版本檢查（CLI-R9）：標頭 `X-Notesteme-Version` 的值是程式版本（`notesteme --version` 的版本）。`serve` 的 426 回應另帶 `Upgrade: Notesteme/<serve 的版本>`（RFC 9110 規定 426 要帶 `Upgrade`）與自己的 `X-Notesteme-Version`；body 是一般的錯誤格式，`code` 為 `client_incompatible`。檢查在驗證之前，版本不相容的 CLI 不會先看到 401。
- `<item>` 與 `ingest` 的位置參數可以用影音代號（`yt:<id>`、`bili:<BV>/p<N>`，見 steps.md 第 2 節）。
- 獨立模式需要 `NOTESTEME_MASTER_KEY`，和 `serve` 相同。沒有 `--wait` 時，獨立模式只建立任務、不執行；之後由 `serve` 或下一次帶 `--wait` 的指令處理。

## 不在範圍內

- `watch` skill 本身的內容在 skill repo 中維護，這裡只定義它依賴的 CLI 行為；改寫結果以 `@manual` 場景驗收。

## 待決問題

- client 模式的本機 socket 在 Windows 原生 binary 上如何處理（named pipe 或只支援 `--server`），M1 實作時決定；預設只支援 `--server`。
- `doctor` 的網路連線檢查要等 OPS 的「自我檢查用的已知影片」，M1 的 CLI 切片先不做（測試中真的連外會被 fakenet 記為違規）。
- 場景修改（2026-11-13，經 owner 同意）：「JSON output matches the API」的 Given 改為處理到 `metadata`，原本只新增、不執行 worker，標題一定是空的；「A bad argument exits with 2」改為帶 `<item>` 並給不支援的 `--format`，原本缺少 `<item>` 就會回 2，驗證不到格式檢查。
- `config import` 的結束碼（2026-11-23，依 /goal 採保守預設，待 owner 追認）：內容不正確（API 回 422 `config_invalid`）時是 **1**，不是 CLI-R6 對 API 422 規定的 2，因為檔案的內容不是命令列參數，而且 SEC-R10 的場景要求 1；`Result.ErrorCode` 是 `config_invalid`。其他 400、422 仍是 2。細節見 SEC 規格的待決問題。
- CLI 與 `serve` 的版本檢查（2026-11-25，依 /goal 由實作者暫定，待 owner 追認；CLI-R9、NFR-CMP-3、NFR-CMP-4）：
  - **0.x 的規則**：與 1.x 以後相同，major 相同、minor 相差不超過 1 即相容（0.5 的 CLI 可以連 0.4 的 `serve`）。另一種讀法是 0.x 把 minor 當成 major（任何 minor 不同都不相容），但那樣「新版 CLI 連到舊一個小版本的 `serve`」在 0.x 期間就做不到，等於收緊到比 NFR-CMP-4 要求的更嚴、而且讓唯一的 0.x 期間沒有這項相容性，所以不採用。0.x 的 API 依 NFR-CMP-3 只新增、不移除，相差一個 minor 的雙方可以互通。
  - **無法判斷時放行**：沒有標頭、開發版（`dev`）、無法解析的版本（含超過 64 bytes 的值）都視為相容，不擋；patch、`-rc.1`、`git describe` 的後綴不影響判斷。
  - **狀態碼**：用 426 Upgrade Required，因為要改變的是用戶端；依 RFC 9110 帶 `Upgrade: Notesteme/<版本>`，值是 `serve` 的版本，表示 CLI 要對齊的對象。`serve` 比 CLI 舊太多時同樣回 426 `client_incompatible`（對這個 `serve` 而言，這個 CLI 不相容），訊息指出要升級 `serve`。
  - **兩邊都檢查**：較新的 `serve` 會擋較舊的 CLI；較舊、沒有這項檢查的 `serve` 不會回報版本，CLI 無從判斷，視為相容（這是本規則開始之前的版本，之後的版本都會回報）。
  - **只對帶標頭的請求回報版本**：瀏覽器、MCP 與 Agent 的請求不會在回應標頭看到版本，避免未登入的請求也能取得版本資訊；登入後仍可從 `/api/system` 查詢。
  - **實際的 binary 互通（NFR-CMP-4 的 `bench`）**：`cmd/notesteme/compat_test.go` 的 `TestCLIAndServeVersionsAcrossBinaries` 以 `go build -ldflags "-X github.com/cxyfer/notesteme/internal/app.version=<版本>"` 建出 0.2.0～0.6.0 五支 binary，`serve` 用 0.4.0，各版 CLI 經本機 socket 執行 `items ls`，斷言 0.3.0、0.4.0、0.5.0 結束碼 0，0.2.0 與 0.6.0 結束碼 1 並印出上面兩種訊息，沒有版本標頭的 `/healthz` 回應不帶版本、帶標頭時回應帶 `serve` 的版本。它跑在 CI 的 `go` job（不帶 `-short`，`-short` 時略過；建置快取熱的時候約 3 秒），不是每晚的 bench：五支 binary 只差 link，成本低，放在每次推送才擋得住回歸（2026-11-25，依 /goal 由實作者暫定，待 owner 追認）。

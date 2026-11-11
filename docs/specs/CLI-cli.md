# CLI：CLI 與資料擁有權

- 狀態：draft
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
| CLI-R2 | 其他 CLI 指令偵測到 flock 時，自動改用 client 模式，透過 `serve` 的本機 socket 執行；沒有 flock 時用獨立模式直接開 DB。 |
| CLI-R3 | 指定 `--server <url> --token <t>` 時一律使用 client 模式，不檢查本機的資料目錄。 |
| CLI-R4 | client 模式中執行的操作，事件會出現在 `serve` 的 SSE 中；獨立模式中，scheduler 不會啟動。 |
| CLI-R5 | 所有查詢類指令都支援 `--json`，輸出欄位和 HTTP API 的回應一致。 |
| CLI-R6 | 結束碼：`0` 成功；`1` 執行錯誤；`2` 參數錯誤；`3` 需要使用者處理（`blocked`）；`4` 等待中（`waiting`，例如 `--wait` 逾時時）。 |
| CLI-R7 | `notesteme ingest --until <stage> --wait` 會等到指定步驟完成或停住才結束，並依 CLI-R6 回傳結束碼。 |
| CLI-R8 | `notesteme doctor` 逐項檢查 yt-dlp、ffmpeg（含 libass）、ffprobe、Deno、各 provider 的 API key 與網路連線，每項顯示 `ok`、`warn` 或 `fail` 與修正方式；有任何 `fail` 時結束碼為 1。 |

## 介面

```
notesteme ingest <url|path> [--profile p] [--until <stage>] [--wait] [--json]
notesteme items ls [--status <stage>=<status>] [--channel <c>] [--json]
notesteme items show <item> [--json]
notesteme items rerun <item> --from <stage>
notesteme export <item> --format srt|vtt|txt [--lang <l>] [--variant <v>] [--out <path>]
notesteme doctor [--json]
全域參數：--server <url> --token <t> --data <dir>
```

## 不在範圍內

- `watch` skill 本身的內容在 skill repo 中維護，這裡只定義它依賴的 CLI 行為；改寫結果以 `@manual` 場景驗收。

## 待決問題

- client 模式的本機 socket 在 Windows 原生 binary 上如何處理（named pipe 或只支援 `--server`），M1 實作時決定；預設只支援 `--server`。

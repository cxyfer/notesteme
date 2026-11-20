# 交接：M1 開發進度

> 更新：2026-11-15。PR #2（交接檔）、#3（CI 與 Docker）、#4（PIPE）已合併；#5（CLI）已合併；GRD 切片是 PR #6（`feat/guards`）；SRC-A 切片在 `feat/sources`、SRC-B 切片在 `feat/bilibili`、OPS 切片在 `feat/ops`、LIB 切片在 `feat/library`（依序疊在 `feat/guards` 上，都還沒 push）。
> 用途：讓下一個 session 或協作者不用重讀全部對話就能接手。內容會隨切片推進而更新；規則與決策以規格、ADR 為準，這裡只做索引與摘要。

## 1. 現況

- **Repo**：`github.com/cxyfer/notesteme`（private），base branch 是 `master`。Go module 路徑沿用 `github.com/cxyfer/notesteme`。
- **已完成**：
  - 切片 0：可執行的驗收骨架。
  - 切片 1：已登入的 owner 新增有人工字幕的 YouTube 影片，下載到 SRT。
  - 依審查結果修正，以及 PR #1（引入 sqlc）。
  - PIPE 切片：依類別的並行上限與暫停、優先順序與 aging、重啟回收、正常關閉的寬限期、取消、SSE 事件、lease_token 檢查、保留最近 3 版、執行紀錄 API、最小的 Supadata 來源。
  - YouTube 影片 ID 改為嚴格的 11 個字元（場景代號一併改名，例如 `abc123xxxxx`）；yt-dlp 回報影片不存在時不重試。
  - CLI 切片：`internal/cli`（`ingest`、`items`、`export`、`doctor`）、standalone 與 client 模式、本機 Unix socket（`/data/notesteme.sock`）、`--server` 加 token、結束碼、`internal/doctor`、`item.created` 事件、通過場景的棘輪（`features/passing.txt`）。
  - GRD 切片：netguard 的 allowlist（網段、單一 IP、主機名稱）接到設定檔 `network.allow_private`；轉址上限 5 次；回應大小上限（feed 10 MiB、音檔 2 GiB，超過即拒絕不截斷）；被擋的請求回 422（`blocked_address`、`too_many_redirects`、`response_too_large`、`unsupported_scheme`）並歸為 permanent；`internal/pathguard`（inbox 與 `media_roots`，解析 symlink、`..`、dangling symlink）在 serve 與本機 socket 回 403 `path_outside_roots`，standalone 不受限；YouTube 播放清單展開（ING-R4，yt-dlp `--flat-playlist -J`，同一個交易建立）。
  - SRC-A 切片：Groq ASR 來源（`internal/source/groq`，OpenAI 相容的 `verbose_json`，key 用環境變數 `NOTESTEME_GROQ_API_KEY`，預設 preset 內建）、yt-dlp 音訊下載（`--max-filesize 2G`，Groq 單檔 25 MiB 上限）；fallback 錯誤改為與來源無關（`errclass.ErrNoResult`、`ErrNotConfigured`，訊息與動作由來源的 `errclass.Error` 帶）；yt-dlp 403 → `needs_action`（SRC-R9）；等待 YouTube 自動字幕（SRC-R6）；`language_mismatch`（SRC-R4 的一部分）；transcript 依第一個 fallback 來源歸 `asr` 或 `download`；doctor 檢查 Groq key；harness 新增 fake Groq（Bearer）、stage 的 blocked／waiting／message／actions 斷言、provenance 與 cue timing 步驟，secret 掃描也涵蓋假工具的參數。
  - SRC-B 切片：Bilibili 來源（`internal/source/bilibili`，yt-dlp `--use-extractors BiliBili`；Probe 不帶 `--no-warnings`，靠登入警告判斷需要 cookie；字幕軌 `ai-<lang>` 是 `bilibili-ai`，其他是 `bilibili-cc`，timing 都是 `cue`；從 AI 字幕軌推斷原文語言）；分P 展開（ING-R3）；SRC-R8 需要 cookie 時 blocked；cookie 存放（`internal/cookies`：正規化成 Netscape、`secrets` 表以資料金鑰 XChaCha20-Poly1305 加密、每次呼叫一個 0600 暫存檔）；`POST /api/cookies`（512 KiB 上限、控制字元拒絕、回應不含值）；`stage_runs.resume_on` 與 `ResumeBlocked`（上傳 cookie 後恢復同一個 token 的 blocked 步驟；執行中才補上 cookie 時改為 requeue）；YouTube 的 cookie 注入、members-only 與 403 帶 `cookie:youtube`；playlist 的 extractor 修正為 `youtube:tab,youtube:playlist`；yt-dlp stderr 中的 cookie 警告行會遮蔽；harness 的假 yt-dlp 照真實語意模擬 Bilibili、`--use-extractors`（fullmatch）、Netscape cookie 檔，secret 掃描涵蓋 cookie 值與關閉後的 `/data`，CLI 步驟有 30 秒的真實時間逾時。
  - OPS 切片：
    - 備份：`POST /api/backup` 串流 tar.gz，內容是 manifest、`VACUUM INTO` 的 DB 快照、設定檔；只限 owner；CLI 寫到 `--out`，並更新 `latest` symlink。
    - 還原：`notesteme restore` 只取得資料目錄的鎖，不經 API；解壓只接受白名單項目，並檢查剩餘空間；先留 pre-restore 快照，含 WAL。
    - migration：前置快照，失敗時還原並拒絕啟動，DB 比程式新時拒絕啟動。
    - 新增 `events` 表：與發布在同一個交易中寫入，worker 每天清理 90 天前的事件。
    - yt-dlp：受管理的 venv 加 symlink 切換、自我檢查失敗時回退、固定版本、`pip --isolated`。
    - `toolexec.Locate` 的尋找順序：設定檔 → 環境變數 → 受管理的 venv → PATH，`/readyz`、`/api/system`、doctor 都使用這個順序。
    - `GET /api/system`。
    - 設定檔的 `asr.providers`；`POST /api/providers` 的 command 類型回 403，`GET /api/providers/{id}` 唯讀。
    - harness：fake python 與 pip、fakecompose（離線模擬 `docker compose config`）、指定 schema 版本的 DB 與 schema 比對、`.tar.gz` 內容比對、After hook 掃描 `/backups`。
  - LIB 切片：
    - `GET /api/items` 支援篩選：platform、channel、status（同一個 stage 是 OR、不同 stage 是 AND，沒有執行紀錄視為 pending）、error_class、lang、發布日、時長、timing、標題搜尋。
    - 標題搜尋用 `items.title_fold`（Go 的 `ToLower`）加 `instr`。
    - 分頁用 keyset cursor，`total` 另外查，排序由新到舊；cursor 帶篩選條件的指紋，只接受標準形式。
    - `artifacts.timings_json`。
    - migration 00006（`stage_runs_latest` 索引取代 `stage_runs_item`）。
    - `/api/queue` 改名為 `lanes`、`waiting`。
    - CLI 讀取所有頁面，`--status`、`--channel` 接上 API。
    - harness：影音 fixture（`features/fixtures/items/`，只適用於 `yt:`、`bili:`，而且影片沒有任何字幕軌、場景沒有準備任何來源時才用）；`listItems` 跟著 cursor 讀完所有頁面，有循環防護。
- **驗收結果**（預設 tag：`@M1 && ~@manual && ~@pending-m0 && ~@wip && ~@ui`）：191 個場景，134 passed、57 pending、0 failed、0 undefined（SRC-R11 mixed 標為 `@wip`，不在預設 tag 內）。

  | Feature | passed | pending |
  |---|---|---|
  | AUTH | 12 | 11 |
  | CLI | 8 | 1 |
  | DEL | 0 | 21 |
  | GRD | 19 | 3 |
  | ING | 15 | 9 |
  | LIB | 9 | 1 |
  | NRM | 9 | 2 |
  | OPS | 14 | 0 |
  | OUT | 8 | 0 |
  | PIPE | 19 | 0 |
  | SEC | 8 | 3 |
  | SRC | 13 | 6 |

  PIPE、OPS 的 M1 場景全部通過。LIB 剩下的 R6（本機檔案的 Range 請求）要等 ING-05 的本機檔案新增與 inbox watcher。SRC 剩下的六個：R11 本機檔案的語言偵測要 ING-05（mixed 標為 `@wip`，要文字語言偵測）；R12、R14 三個、R15 要本機檔案或 podcast。CLI 剩下的一個：`--server` 加 token 要等 AUTH 的 token（AUTH-R7～R9）。GRD 剩下的三個：DNS rebinding 要等 ING 的 podcast feed，GRD-R7 預期 201 的兩個例子要等 ING-05（本機檔案的新增）。

  M1 的 OUT 場景全部通過，但 OUT 的 M2 部分還沒做，所以規格狀態仍是 `ready`，不是 `implemented`。

- **規格狀態**：AUTH、SEC、ING、SRC、NRM、OUT、PIPE、CLI、GRD 是 `ready`，其餘都是 `draft`。還沒有任何規格達到 `implemented`，因為每份都還有 pending 的場景。

## 2. 下一步

使用者暫定的切片順序：PIPE（完成）→ CLI 與 `/data` 鎖（完成）→ GRD（完成，PR #6）→ SRC-A（完成，`feat/sources`）→ SRC-B（完成，`feat/bilibili`）→ OPS（完成，`feat/ops`）→ LIB（完成，`feat/library`）→ **DEL**。使用者要求每個切片完成後自動開始下一個。

接下來：

1. PR #6（GRD）等 CI 與 Codex 審查，通過後由 owner 合併（自動模式不允許 agent 執行 `gh pr merge`）。
2. PR #6 整理好之後（owner push `feat/guards`、把 base 改成 `master`），依序 push `feat/sources`、`feat/bilibili`、`feat/ops`、`feat/library` 並開 PR（base 分別是前一個分支；前一個合併後改 base 為 `master`，並用 `-s ours` 併入 master）。
3. DEL 切片：先做 DoR（`features/DEL-*.feature` 的 `@M1` pending 場景與步驟）。多數 DEL 場景用 `yt:abc123xxxxx` 加 `processed up to`，要新增對應的影音 fixture，或在場景中準備字幕來源。
4. 每個切片完成後執行 `go test ./features -run TestFeatures -update-passing` 更新棘輪清單，並檢查新增的通過場景。
5. `features/fixtures/items/` 目前只有 `yt-pg100xxxxxx.json`。DEL、NRM、SEC 中用 `processed up to`、但沒有準備字幕來源的場景仍是 pending，要補對應的 fixture。

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
- **`docker` 指令**：`When the owner runs "docker compose -f … config --format json"` 由 `features/internal/fakecompose` 離線模擬（讀真正的 `deploy/docker/compose.yaml`），其他 docker 指令與不是 `notesteme` 的指令一律失敗，不是 pending。
- **godog 的 tag OR 用逗號**：`-godog.tags='@A,@B'`。`'@A || @B'` 會選到 0 個場景卻顯示通過。
- **斷言要能失敗**：PIPE 切片的審查用變異測試發現 5 個場景拿掉對應行為後仍然通過。每個切片的審查都要挑幾個場景做變異測試：在暫存 worktree 拿掉對應的 production 行為，場景必須變紅。
- **測試不連外網**（NFR-TEST-1）；時間一律用注入的 fake clock（NFR-TEST-2）。
- **不要讓已實作的步驟依參數回傳 pending 來避開失敗**。場景的步驟都已實作、行為還沒做時：
  - 小的行為就直接實作。
  - 大的就讓真正還沒實作的那個步驟維持 pending。
  - 目前允許的 pending 只有下列幾種：
    - 斷言遇到 production 回應的 `501 not_implemented`，或 CLI 回報 `not_implemented`（`the command exits with` 遇到尚未實作的子命令或 API 的 501），而且這個回應或結果必須是前一個步驟送出、執行的。
    - 腳本化來源的步驟查 production 的 `source.Implemented`，得知來源還沒實作。
    - `processed up to` 的影音沒有字幕來源，也沒有對應的影音 fixture（`itemFixtureFor` 的規則 3，見 steps.md 4.4）。下列 3 個 DEL 場景在 DEL 切片中要補 fixture：
      - The deletion preview shows what will be lost without changing anything
      - Adding an item that is in the trash offers to restore it
      - An item added again after a permanent delete starts fresh
    - 步驟本身還沒實作。
- **通過場景的棘輪**（`features/passing.txt`）：
  - 「501 → pending」這類規則可能讓已通過的場景退步成 pending，整套測試卻仍然是綠的（審查用變異測試證明：讓 `GET /api/items` 回 501，CLI 場景 5 從 passed 變成 pending）。
  - `features/passing.txt` 一行一個通過的場景，格式 `<feature 檔名>: <場景名稱>`；Scenario Outline 的每個 example 加上 ` [example N]`（整份 Examples 的第幾列）。
  - `TestFeatures` 跑完後讀取每個場景的結果（自訂的 godog formatter，見 `features/ratchet_test.go`）：清單中的場景不是 passed（pending、failed、undefined、沒有被執行）就讓測試失敗並列出場景；通過但不在清單中的場景只提示。只有預設 tag 才要求清單中的場景都被執行；用 `-godog.tags` 指定其他 tag 時只檢查有執行到的。
  - **每個切片完成後要更新 `features/passing.txt`**：`go test ./features -run TestFeatures -update-passing`，檢視 diff 後一起 commit。清單中的場景不得退步；改場景名稱或刪除場景時，清單也要同步修改。
- **指令被權限限制擋下時不要繞過**（例如把指令寫進腳本再執行）；停下來回報給使用者。
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
  - `When the server shuts down gracefully` 走正常關閉。測試中寬限期為 0（`TestControls.SkipShutdownGrace`）；30 秒的行為由 worker、app 的單元測試用 fake clock 驗證。
  - worker 是手動模式：
    - `RunRound` 領取一輪任務、標記為 running，並在 goroutine 中開始執行，不等完成就回傳。要斷言任務停在 running 時，用假工具的 gate 擋住，不要依賴時序。
    - `RunUntilIdle` 執行到沒有任務為止。
- **測試替身**（`features/internal/`）：
  - `harness.World`：每個場景一份，包含暫存根目錄、env map、fake clock、fakenet、假工具狀態。
  - `fakenet`：可指定的 DNS 解析加程序內的 fake origin；也會攔截 `http.DefaultTransport` 等繞過 netguard 的連線。
  - `faketool`：測試 binary 以 `yt-dlp`、`ffmpeg`、`ffprobe` 的名稱被執行時，改扮假工具，並把每次呼叫記在 `calls.jsonl`。
  - `fakesource`：腳本化的 ASR 與 Supadata（掛在 `api.supadata.ai`，只接受 `x-api-key`）。
  - gate：`World.ArmGate`／`OpenGate`。被擋住的假 yt-dlp 各寫一個 `.waiting.<pid>`，並寫出 `/data/tmp/partial-download.part`。計數時只算仍存活的程序。
- **步驟的約定**：
  - 送出請求的步驟會在 server 尚未啟動時隱含啟動它。
  - 例外：場景明確讓 server 正常關閉之後，讀取狀態的 `Then` 步驟改為唯讀查詢 SQLite（`harness/db.go`），不啟動新 server。新 server 啟動時的租約回收會蓋掉要驗證的結果。
  - 「任務正在執行」要同時符合 DB 中 leased 的任務數與存活的假工具程序數。
  - `the next task belongs to` 實際執行一輪領取（arm gate 後 `RunRound`），檢查被領取的任務屬於哪部影音。
  - 還沒實作的 API 回 `501 not_implemented`，斷言步驟遇到時轉成 pending。例外：預期 401，或預期 403 且請求缺憑證時，不轉。
  - After hook 發現的違規（未攔截的連線、secret 外洩）記在收集器中，`TestFeatures` 結束後讓測試失敗。
  - 回應 JSON 是子集比對；陣列依序比對前 N 個元素。
- **程式地圖**（`internal/`）：

  | 套件 | 內容 |
  |---|---|
  | `app` | server 啟動、啟動防護、flock、手動 worker、測試用的 seeding |
  | `config` | 設定檔與環境變數（含 `asr`、`trusted_proxies`） |
  | `store` | SQLite（單一寫入連線、WAL）、goose migration（`migrations/`，前置快照、拒絕降級，`Options.Migrations` 可替換）、`VACUUM INTO` 快照、sqlc 查詢（`queries/` → `sqlcdb/`） |
  | `auth` | argon2id 密碼、session、CSRF（double-submit）、依來源位址的登入鎖定 |
  | `secrets` | master key 包裝資料金鑰（XChaCha20-Poly1305） |
  | `api` | Huma 路由、驗證與 CSRF middleware、items、cues、subtitles、resolve、settings |
  | `queue`、`worker` | 租約佇列、退避（30 秒、2 分鐘）、依類別的上限與暫停（`queue/control.go`，存在 settings 表）、worker 的 Drain 與 Cancel |
  | `events` | 記憶體中的事件匯流排；store 在同一個交易中寫入 `events` 表，commit 之後發布，`GET /api/events` 以 SSE 推送；`app/housekeeping.go` 每天清理 90 天前的事件 |
  | `pipeline` | metadata → transcript → normalize、重跑、快取鍵、網址辨識、取消、保留最近 3 版、可覆寫的 `impl_version` |
  | `transcript` | 依 `caption_priority` 選字幕軌 |
  | `source` | `Implemented` 宣告已實作的來源（目前 youtube-manual、youtube-auto、supadata） |
  | `source/youtube` | yt-dlp 的 Probe 與字幕下載 |
  | `source/supadata` | Supadata 的 transcript API（不跟隨轉址、依官方錯誤碼分類） |
  | `toolexec` | 子程序（process group、輸出上限、環境變數 allowlist）；`Locate` 依 OPS-R8 的順序找工具並回報來源 |
  | `tools/ytdlp` | 受管理的 yt-dlp venv：更新、自我檢查、回退、固定版本、`state.json` |
  | `backup` | 備份封存格式、安全解壓（白名單、大小與空間檢查） |
  | `netguard` | 對外 HTTP：在連線時封鎖私有網段與 metadata 位址 |
  | `subtitle` | cue 模型、VTT／SRT 解析、Normalize（NRM-R1～R6）、渲染、檔名 |
  | `errclass`、`clock` | 錯誤類別、可注入的時鐘 |

## 6. 已知待辦與技術債

審查時發現，但刻意延到對應切片處理的項目：

- **PIPE**：
  - 執行期回收過期租約延到 AGT（理由見 PIPE 待決問題），M1 只在啟動時回收。
  - 產出檔的 sha256 不符時，應該標記損毀並重跑（NFR-REL-2）。
  - `transcript` 的資源類別一律是 `download`，ASR 來源實作時要依 PIPE 待決問題判斷。
  - `POST /api/tasks/{id}/retry`、`/cancel`（單一任務）仍回 501。
  - SSE：沒有寫入期限（要用真實時間，與 fake clock 衝突，需要另外設計）；登出後串流不會斷開；事件沒有 `id:` 與 replay。
  - `queue/control.go` 更新時持有寫鎖做 SQLite 寫入；pause 回傳後，進行中的一輪領取仍可能多領一個任務（毫秒級）。
  - `GET /api/queue` 只列 `executor=builtin` 的任務，agent 任務留給 AGT。
- **SRC**：
  - yt-dlp 的錯誤分類：影片不存在、私人、已移除是 `permanent`（依 stderr 的 `ERROR: [youtube] <id>: …`，見 `source/youtube/client.go`，還沒對真實的 yt-dlp 驗證過訊息）；`HTTP Error 403` 是 `needs_action`（SRC-R9）；其餘都當成 `retryable`。
  - 原文語言未知時，應該從 `*-orig` 字幕軌推斷。
  - 還沒做：cookie（SRC-R8）、品質檢查（SRC-R4，只做了 `language_mismatch`）、音訊切塊（SRC-R12）、幻覺過濾（SRC-R13）。
  - Supadata：API key 只能用環境變數 `NOTESTEME_SUPADATA_API_KEY`，還沒加密存放（SEC-R4）；非同步 job（202）還不會輪詢，目前當 permanent；暫時性錯誤時不會寫入 `source_probes`。
  - 做 `youtube-auto` 前，要評估改用 yt-dlp 的 `json3` 格式（見 NRM 待決問題）。`features/fixtures/captions/auto-zh-en-mixed.json` 目前內容是 VTT，決定後與假 yt-dlp 一起改。
- **CLI**：
  - client 模式加 `--wait`、而 serve 是手動 worker 時，harness 還不會在背景推動 worker，步驟會失敗並說明（目前沒有場景用到）。
  - `items ls --status`、`--channel` 回 2（尚未支援）；本機路徑的 `ingest` 要等 upload／path API（目前 API 回 501）。
  - `doctor` 的網路連線檢查要等 OPS 的已知影片；API key 檢查 Supadata 與 Groq。
  - JSON Lines 輸出在步驟中只比對第一個物件。
  - 非 Linux 平台的本機 socket 只靠檔案權限，沒有 uid 檢查。
- **GRD**：
  - GRD-R7、R8 的 403 場景在路徑檢查失效時會變成 pending（501），不是 failed，只靠 `passing.txt` 棘輪把關，不可以順手更新掉。
  - 直接輸入非 http 網址的場景驗證不到我們自己的 scheme 檢查（Go 的 `http.Client` 也會拒絕），由轉址場景與單元測試把關；GRD-R6 的 `--use-extractors` 只由單元測試把關（見 GRD 待決問題）。
  - 設定檔中「由資料庫管理的欄位」清單（profiles、prompts、budget(s)、settings、subscriptions、tokens、secrets、cookies、language、notifications）是實作時自訂的，需要 owner 確認；拿掉其中一項，`KnownFields` 仍會拒絕，場景驗證不到專用訊息。
  - `netguard.Deps` 同時有舊的 `Allowlist []netip.Prefix` 與新的 `AllowPrivate`，`features/internal/fakenet` 改用新欄位後可移除舊欄位。
  - pathguard 檢查與實際開檔之間的 TOCTOU 屬於 ING-05。
  - 播放清單沒有項目數上限；yt-dlp 的播放清單錯誤訊息還沒對真實 yt-dlp 驗證（見 ING 待決問題）。
  - Supadata 回應超過大小上限時仍走 `isPermanent` 換下一個來源，最後訊息是「找不到可用的字幕來源」，看不到大小上限的訊息。
- **SRC**：
  - Groq：`verbose_json` 欄位、語言全名、25 MiB 上限、yt-dlp `--max-filesize` 的輸出字串都還沒對真實服務驗證；長音訊在切塊（SRC-R12）完成前直接 permanent，使用者只看到「找不到可用的字幕來源」，看不到大小原因。
  - Groq 上傳用 chunked（`io.Pipe`），要求 Content-Length 的 proxy 會失敗。
  - Supadata 仍是先讀完 body 再依狀態碼分類（Groq 已改成先看狀態碼）。
  - `the provider … has no API key` 對 LLM provider 依參數回 pending（steps.md 4.1 註明 LLM 版本未實作），M2 實作 LLM 時要補。
  - 動作「現在改用語音轉錄」（SRC-R6）只顯示，點擊的處理還沒做。
- **SRC-B**：
  - SRC-R8 是推定：Bilibili 未登入時只說「字幕需要登入」，分不出 CC 或 AI，也看不到語言；Bilibili 的錯誤字串、412 風控都依 yt-dlp 原始碼推測，等 M0 實測。
  - 假 yt-dlp 的兩處與真實不同：members-only 只在下載時失敗（`-J` 不失敗，為了不讓 ING-R9 的 `availability` 偵測失效）；空的 cookie 檔視為未登入（production 不會產生空檔）。production 之後應把 `-J` 的 members-only 錯誤歸為需要 cookie。
  - `ResumeBlocked` 只恢復同一個 token，沒有場景驗證（被誤恢復的步驟會再次 blocked，看不出差別），只靠單元測試。
  - 執行中補上 cookie 的判斷用毫秒比較，同一毫秒內上傳仍會 blocked。
  - 「改用語音轉錄」對 Bilibili 只顯示（Groq 還不能下載 Bilibili 音訊）；沒有 cookie、也沒有字幕的 Bilibili 影片會照 SRC-R5 失敗，訊息「設定語音轉錄服務」可能誤導。
  - ING-R9 的 Bilibili 部分（在 resolve 標示需要 cookie）做不到，規則待修。
  - cookie 的 GET／DELETE API 還沒做；有 cookie 就每次呼叫都帶，帳號被標記的風險寫在 SEC 待決問題。
- **OPS**：
  - `/readyz` 限速。
  - 自我檢查的檢查影片 `jNQXAC9IVRw` 寫死，能否長期使用、暫時性失敗是否也回退，等 M0 實測（見 OPS 待決問題）。
  - OPS-R9 的 `wsl` profile 實際上沒有任何服務宣告；`HEALTHCHECK` 沒有場景驗證。
  - 「備份的 schema 版本比程式新時拒絕還原」只由 `internal/app/restore_test.go` 把關（現有步驟做不出這種備份檔）。
  - 備份串流沒有寫入期限：慢速的 client 會讓 `<data>/tmp/backup-*` 的快照留到連線結束。
  - 受管理的 yt-dlp 在 Windows 上不支援（回 409）。
  - 每天的排程更新（OPS-R7 的「排程更新」）還沒做，目前只有手動更新。
  - 修改 provider 的 API 還沒有路由（SEC-R3 說「新增或修改」都回 403）；`POST /api/providers` 的非 command 類型回 501。
  - fixture 例外：`Given the file … contains:` 寫入、之後內容沒被改過的檔案，After hook 照字面掃描，不當成封存檔解壓（R2 刻意寫入不合法的 `.tar.gz`）。
- **AUTH**：
  - token（AUTH-R7～R9）還沒做，bearer 一律回 401。
  - `X-Forwarded-Proto` 應該只採用可信 proxy 的值。
  - IPv6 的鎖定以單一位址計數。
- **其他**：
  - `Given the item … has been processed up to …` 會真的執行 pipeline；沒有準備字幕來源的場景要有 `features/fixtures/items/` 的 fixture（見第 2 節）。
- **LIB**：
  - 詳情 API（`Pipeline.Item` 的 `stages`）不列出沒有執行紀錄的 stage，列表篩選則視為 `pending`，UI 切片要決定是否補上。
  - 標題搜尋不做 NFKC 與簡繁互通；00006 的回填只處理 ASCII 的大小寫（M1 尚未發布，沒有舊資料）。
  - `group_by`（LIB-R4 的分組）、`read_state`（回 501）還沒做；`status` 只認 M1 的三個 stage。
  - `CountItemsFiltered` 每次掃全表（10k 筆約 3ms）；status 篩選在 10k 筆約 62ms。列表與 COUNT 是兩段相同的 WHERE，靠單元測試確認一致。
  - CLI 的 `resolveItem` 每次逐頁找，API 沒有依代號篩選的參數。
  - 只靠單元測試守住：`waiting` 不含執行中的任務、垃圾桶中的影音不在列表、排序、`limit` 與最後一頁沒有 `next_cursor`。
  - 字幕讀取端沒有串流（NFR-RES-3 只做到輸出端）。
  - 有 HTML 回應時要加上 CSP 等標頭（NFR-SEC-1）。
  - CI（`.github/workflows/ci.yml`，PR #3）：gofmt、`go mod tidy -diff`、vet、`go test -race`、`sqlc diff`、Docker image 與冒煙測試；push 到 `master` 或 `v*` tag 時發布 image 到 GHCR。還沒有 Python 版規格檢查（Go 版在 `go test` 中）與 `govulncheck`。

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
| PIPE | aging 級距：回補等 20 分鐘就排在新的手動新增前面 | 照規則；差距不能超過 17 級 |
| PIPE | `transcript` 的資源類別 | 見第 6 節 |
| PIPE | **R7、R13 的場景驗證不到規則，需要修改 `.feature`** | 等使用者同意；清除邏輯先以單元測試驗證 |
| SRC | 沒有 API key 的來源與預設 fallback（SRC-R10） | 略過沒有 key 的來源，全部都不能用才 blocked；Supadata 依官方錯誤碼分類 |
| CLI | Windows 原生 binary 的 client 模式 | 只支援 `--server` |
| OPS | 自我檢查用的已知影片 | M1 內建一部，不開放覆寫；M0 實測 |
| OPS | 自我檢查的暫時性失敗是否回退 | 照規則回退，原因放在 `reason` |

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
  - `.claude/worktrees/` 下 builder 用過的 worktree（切片 1 到 OPS 的都是），以及 `worktree-agent-*` 分支。
  - `.worktrees/m1-skeleton`、`feat/m1-skeleton`、`docs/bdd-specs`、`docs/plan-review-revisions` 都已併入 `master`，可以一併移除。

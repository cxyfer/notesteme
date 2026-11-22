# 交接：開發進度（M1 與 M2）

> 更新：2026-11-22。PR #1～#19 已合併（LOCAL #13、PODCAST #14、ESTIMATE #15、M2 S0 #16、M2 S1/S2 #17、規格與 `.feature` 修改 #18、M2 S6a #19）。owner 已同意的規格與 `.feature` 修改已完成（第 2 節第 4 點）。**M1 的後端已完成，但 Web UI 與真實樣本驗收還沒做，M1 尚未收尾（第 1 節「M1 尚未收尾」）。**
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
  - DEL 切片：
    - 移到垃圾桶：在一個交易中標記 `deleted_at`，步驟改成 skipped，原因 cancelled，動作「重試」；之後取消任務、刪除媒體快取。
    - 垃圾桶中的影音：所有讀取都回 404 `in_trash`；新增時回 409；播放清單與分P 中在垃圾桶的項目標示 `in_trash`。
    - 還原、垃圾桶列表。
    - 永久刪除，在一個交易中完成：cost ledger 保留標題、tombstone、刪除 events 並改寫 `item.purged`、cascade、孤兒 `config_snapshots`；之後刪除 `artifacts`、`media`、`uploads` 下的 `<id>` 目錄。
    - 清空垃圾桶、刪除預覽、每天清理到期的影音（`library.trash_retention_days`）。
    - CLI：`items rm`、`items restore`、`trash ls`、`trash empty`；不在終端機時沒有 `--yes` 就回 2，終端機判斷用 isatty。
    - migration 00007：`item_tombstones`、`cost_ledger`。
    - worker 的定期工作改在 round 鎖之外執行，避免清理呼叫 `Cancel` 時死結。
    - Rerun 與 Trash 的競態已修：engine 遇到垃圾桶中的影音時，取消任務並把步驟改成 skipped。
    - harness：拿掉 `titled` 預設語言不算「宣告」的例外（與 steps.md 一致）；`substRefs` 改用 `itemID`；垃圾桶中的 stage 斷言改讀 DB；`World.Refs` 在每次新增時更新。
  - AUTH-SEC 切片（`feat/auth-secrets`，疊在 `feat/delete` 上）：
    - migration `00008_api_tokens.sql`（`api_tokens` 表 + `api_tokens_active_name` partial unique index）與 `queries/{tokens,keys,secrets}.sql`（英文註解）。
    - API Token（`internal/auth/tokens.go`）：`nst_` + 32 bytes base64url（SHA-256 雜湊儲存、明文只在建立時回傳一次、`last_used_at` 寫入節流每小時一次）；名稱拒絕 `/` 與控制字元；共用 `auth.ParseTokenDuration`（整數 `d` 天數與 Go duration，拒絕空值／0／負數／`+`）。
    - Scope 權限矩陣（`internal/api/scopes.go`）：透過 `registerScoped`／`registerOwnerOnly` 在 Huma `Operation.Middlewares`（解析請求 body 之前）檢查 `requireScope`／`requireOwner`；精確比對 `read`、`agent`、`ingest`、`admin`（無隱含繼承），缺少時回 403 `forbidden` 並帶 `required_scope`；涵蓋 `DEL-R12`（`GET /api/trash` 與 `deletion-preview` 為 `read`，刪除／還原／清空為 `admin`）、`GET /api/events`（`read`）、`POST /api/estimate`（`ingest`）、`POST /api/tasks/{id}/retry|cancel`（`admin`）、`GET/PUT /api/settings/{section}` 與 `POST /api/providers`（`admin`）。
    - Token API（`internal/api/tokens.go`）：`GET /api/tokens`、`POST /api/tokens`（409 `token_exists`、422 驗證）、`DELETE /api/tokens/{id}`（支援 id 或作用中 name）。
    - 外部服務 API key（`internal/secrets/services.go`、`internal/api/settings_services.go`）：`GET/PUT /api/settings/services` 加密寫入 `secrets` 表（`Keyring.Seal("provider_api_key", ...)`）、`GET` 即時解密計算 `api_key_last4`、空字串刪除、`nil` 保留、控制字元 422 且不回顯值；`supadata`、`groq`、`pipeline`、`app/sources.go` 支援環境變數優先於 DB 解密 fallback（存完即生效、免重啟）。
    - Master key 輪替與重設（`internal/secrets/rotate.go`、`internal/app/secrets.go`）：在 `store.LockDir` 離線鎖與單一交易中完成（不呼 `openCore`/`secrets.Open`，確保遺失 master key 時仍可 `reset` 並保留影音庫）。
    - CLI（`internal/cli/tokens.go`、`internal/cli/secrets.go`）：`tokens create|ls|revoke`（經 `openBackend` 支援 standalone／本機 socket／`--server`）、`secrets rotate-key --new-key-env` 與 `secrets reset [--yes]`（離線鎖、拒絕 `--server`、非終端機缺 `--yes` 回 2）。
    - 驗收步驟（`features/steps/{access,files,scenario,secrets,processing}.go`）：實作 `a token … with scopes …`、`the token … expired`（經 `Server.ExpireToken`）、`the client sends … with the token …`（含 `and:` 與 `POST /api/items` 預設 body）、`the data directory does not contain the created token`、`the logs do not contain …`；`noteRequest` 追蹤 `PUT /api/settings/services` 的 `api_key`；`itemProcessedUpTo` 在未呼叫 `the server is running` 時收掉暫時啟動的 server。
  - LOCAL 切片（ING-05，PR #13，決策見 ADR-0016）：
    - migration 00009：`items` 的 `source_path`、`content_sha256`、`origin`；`inbox_entries`；tombstone 記 `sha256`。
    - 識別：`local:<相對 inbox 的路徑>`、`local:<絕對路徑>`（media_roots）、`local:<檔名>`（上傳，撞名加 `-2`，也避開 inbox 頂層的同名檔）。
    - `internal/pathguard/open.go`：`Resolve`、`Open`（`os.OpenRoot`、`O_NONBLOCK`、只收一般檔案）、`With`。
    - `internal/media`：固定 argv 的 ffprobe／ffmpeg，一律帶 `-protocol_whitelist file` 與 `-format_whitelist`；`CanonicalLang`。
    - 新增入口：`{"path"}`（延遲驗證）、multipart 上傳（4 GiB、閒置逾時 2 分鐘、並行 2、part 16，先檢查 scope 再讀 body）、`internal/inbox` watcher（穩定 10 秒、ffprobe 篩選、`sha256` 去重、tombstone）；`IngestLocal` 的判斷順序是鍵 → tombstone → `sha256`。
    - `GET /api/items/{id}/media`：`ServeContent`、ETag、Content-Type 白名單、`nosniff`、CSP sandbox。
    - 字幕來源 `local-sidecar`、`local-embedded`；讀不到時記 `rejected`／`unreadable`，改用下一條；Supadata 對 local 是 `unsupported`。
    - ASR（`internal/pipeline/asr.go`、`internal/audio`）：所有平台共用前處理（Opus 16kHz），超過上限時在靜音處切塊、依中點接回。ASR 語言寫回原文語言（`lang_source=asr`），之後重跑不把它當輸入。
    - 非上傳的本機檔案每次 probe 比對大小與修改時間，不同就重算 `sha256`；外掛字幕內容的 `sha256` 進快取鍵。
    - 備份包含 `uploads/`（manifest 記大小與 `sha256`），還原時原本的 `uploads/` 移到 `/backups/pre-restore-uploads-<時間>/`；啟動時清理孤兒的 `uploads/<id>`。
    - harness：marker v2（`streams`、`compressed`、`silences`、`offset`）、會轉碼／切塊／靜音偵測／抽字幕的假 ffmpeg、假 Groq 依 `offset` 過濾、`features/fixtures/{local,media}/`；字幕表格的 `#` 不從 1 起連號時依 `#` 比對。
  - PODCAST 切片（ING-04、SRC-R14／R15、GRD-R2 rebinding，疊在 PR #13 上，決策見 ADR-0017）：
    - `internal/podcast`：RSS／Atom 解析（itunes、podcast namespace、`<podcast:transcript>`）、`CanonicalURL`、`FeedSlug`。
    - 單集是 `(podcast, "<feed key>/<guid>", 0)`；feed key 存在 `channels`（`PodcastFeedKey`，撞名加 `-2`），不需要 migration。
    - 新增：`expandPodcast` 驗證 `episodes`，並用 `netguard.CheckURL` 對音檔網址做只解析、不連線的預檢；`insertTargets` 與一般新增共用同一個交易迴圈。
    - metadata：`runPodcastMetadata` 抓 feed、存快照（`metadata_json.podcast`）、下載音檔到 `/data/media/<id>/episode.<ext>`（2 GiB、`sha256`、ffprobe 長度）。
    - 來源 `podcast-transcript`：讀快照、支援 VTT／SRT／Podcasting 2.0 JSON；抓取失敗改走 fallback；SRC-R15 偏移超過 2% 時 transcript 為 done 帶「時間可能偏移」。
    - podcast 的 ASR 音訊來源與媒體端點共用 LOCAL 的 `transcribeASR`、`MediaFile`。
    - harness：`features/internal/fakepodcast`（每次依狀態產生 RSS、音檔改寫 marker 的 item、字幕呼叫計數）、`features/fixtures/podcast/`（marker 的 `listed=` 是 feed 宣告的長度）；`shows the message` 沒有非 done 的訊息時取 done 步驟的訊息。
  - ESTIMATE 切片（ING-R8，疊在 PODCAST 上）：`POST /api/estimate` 與新增共用辨識與展開（`expandRequest`），依生效的 `language` 用 transcript 步驟的規則選軌，平台字幕不計 ASR；fallback 依序估 Groq（內建計價表 `internal/cost`）或 Supadata（credit 計價，金額 `null`）。不寫入資料庫，本機檔案回 422。決定記在 ING 的待決問題。
  - M2 S0 骨架（`feat/m2-s0`，疊在 ESTIMATE 上；決策記在 PIPE、LLM、PRF、AUTH 等規格的待決問題，ADR-0002 加修訂行）：
    - `pipeline.Stages` 擴充為 7 步：metadata、transcript、normalize、segment、correct、translate、publish。
    - `ResolveExecutor`／`planChain`／`applyChain`（`internal/pipeline/executor.go`）：前進到某步時才依快照解析 executor；`off`、`api` 沒有 provider／model、`publish` 沒有輸出就直接 `skipped`（`block_reason` 為 `off`、`not_configured`、`no_outputs`），不建任務；要執行而尚未實作的步驟在寫入前回 501。
    - 設定快照的 `stages`（只限 LLM 步驟）與 `outputs`；`RerunRequest` 加 `Profile`、`Chunks`（回 501）。
    - queue：`llm` 類別上限 `Control.SetCap`；`TestControls.SerialLLM` 讓 llm 任務依序執行。
    - API：29 條 M2 路由的 501 stub（`internal/api/m2_stubs.go`，scope 檢查在讀 body 之前）；stage 回應加 `executor`；CLI `items show` 顯示 `skipped（not_configured）`、`blocked（reason）`；`--wait` 對沒有後續步驟的舊影音改看最後一個存在的步驟。
    - harness：`features/internal/fakellm`（Anthropic、OpenAI 的 wire 契約檢查、依序／依 chunk 回答、`Fail`、generator 登記與 `echo_cues`）、`features/steps/llm.go` 的 LLM 步驟、`features/fixtures/llm/` 9 個 fixture、`features/fixtures/items/` 7 個 M2 影音 fixture；`processed up to <M2 步驟>` 依規則 pending；`from the origin`；帶 bearer token 的請求視為完整憑證（預期 403 時 501 → pending）。
  - M2 S1 處理方案＋S2 Prompt 庫（`feat/m2-prf`，疊在 S0 上；決策 D1～D16 記在 PRF、PRM 規格的待決問題）：
    - 處理方案以名稱識別（`profiles`、migration 00010），內建 `default` 不能修改或刪除；`POST/GET/PATCH/DELETE /api/profiles`、`POST /api/profiles/{id}/default`。
    - 設定分層 builtin → 全域 → 處理方案 → 頻道 → 單次覆寫 → 重跑（`internal/pipeline/layers.go`），物件依 RFC 7396 合併、陣列整個取代；快照記下各層（`layers`）。頻道層在 `metadata` 完成時才併入（D6）：頻道與快照記下的不同時重建快照，並把還沒開始的下游步驟改綁。
    - 驗證（`internal/pipeline/schema.go`、`internal/validate`）：未知欄位、型別、列舉、provider、prompt 引用（類型要和步驟相符），422 回 `errors[{path,message}]`（D9）；完整性只檢查處理方案中寫出來的 `api` 步驟（D8）。
    - 頻道覆寫：`PATCH /api/channels/{key}/overrides`，key 是 `<platform>:<external_id>`（D14）。
    - Prompt 庫（`internal/prompts`、migration 00011）：版本只增不改，內建 prompt 嵌入在 `internal/prompts/builtin/<id>/v1.md`（D12）；引用寫成 `<id>`、`<id>@default`、`<id>@N`（D11）；變數在儲存時檢查；刪除時 `used_by` 依處理方案、`settings`、頻道的順序列出（D13）。
    - harness：`features/steps/config.go` 的處理方案、prompt、頻道覆寫步驟（Given 累積，第一個請求前寫入）、`jsonmatch.go` 的子集比對（陣列完全相等）；CLI 步驟遇到尚未寫入的處理方案等設定會隱含啟動 server；`words` 欄位接受秒數（`1.5`）。
    - 變異測試 10 個都會變紅。`DeleteProfile` 拿掉 builtin 檢查時，PRF-R7 場景仍然通過（內建的 `default` 同時是預設處理方案，「使用中」也回 409），只有單元測試 `TestProfiles_DeleteDefault409` 抓得到；`GET /api/profiles/{id}` 回傳合併了內建預設的 config 時，子集比對驗證不到。
  - M2 S6a TRN 確定性轉換（PR #19，ADR-0018）：
    - `internal/zhconv`：自己實作的 OpenCC 轉換器（最長詞優先，內嵌 Apache-2.0 的 OpenCC 1.4.2 字典，`scripts/opencc/update.sh` 重新產生），不依賴 GPLv2 的 port；`langtag` 判斷 BCP-47 的文字與地區，決定 `s2t`、`s2twp`、`s2hk`、`t2s`、`tw2sp` 等轉換表。
    - `translate` 步驟的 `opencc` executor（`runTranslateOpenCC`）：中文文字系統轉換不經過 LLM；`zhconv.GlossaryTable` 提供 COR-R4 詞彙表的轉換表，詞彙表的組裝屬於 LLM 切片。
    - `@M2`：20 passed、23 failed、74 pending；TRN「Chinese script conversion uses OpenCC」3 個範例通過，「Localisation runs OpenCC first…」要等 S6b。
- **驗收結果**（預設 tag：`@M1 && ~@manual && ~@pending-m0 && ~@wip && ~@ui`）：191 個場景，188 passed、3 pending、0 failed、0 undefined（SRC-R11 mixed 標為 `@wip`，不在預設 tag 內）。

  | Feature | passed | pending |
  |---|---|---|
  | AUTH | 23 | 0 |
  | CLI | 9 | 0 |
  | DEL | 19 | 2 |
  | GRD | 22 | 0 |
  | ING | 24 | 0 |
  | LIB | 10 | 0 |
  | NRM | 10 | 1 |
  | OPS | 14 | 0 |
  | OUT | 8 | 0 |
  | PIPE | 19 | 0 |
  | SEC | 11 | 0 |
  | SRC | 19 | 0 |

  AUTH、CLI、GRD、ING、LIB、OPS、OUT、PIPE、SEC、SRC 的 M1 場景全部通過（ING-R7 隨 S1 通過）。剩下的 3 個 pending：
  - **NRM-R7（1 個）**：場景的影片原文語言是預設的 `en`，卻只有 `zh-Hans` 的自動字幕，預設的 `caption_priority` 不會選它，所以 `processed up to` 依規則 3 pending。場景要改（例如改成 `manual captions`，或宣告原文語言 `zh-Hans`），需要 owner 同意。
  - **DEL `@mcp`（2 個）**：需等 M2 的 MCP tool（`get_transcript`、`tools/list`）。

  M1 的 OUT 場景全部通過，但 OUT 的 M2 部分還沒做，所以規格狀態仍是 `ready`，不是 `implemented`。

- **規格狀態**：AUTH、SEC、ING、SRC、NRM、OUT、PIPE、CLI、GRD 是 `ready`，其餘都是 `draft`。還沒有任何規格達到 `implemented`，因為每份都還有 pending 的場景。
- **M1 尚未收尾（2026-11-22 盤點）**：後端與驗收場景（上表）已完成，但 plan 第 24 節對 M1 的定義還有下列項目沒交付，容易被「場景全綠」掩蓋：
  - **Web UI 尚未開始**：沒有 `web/`、沒有靜態檔案服務、沒有 UI 的 `go:embed`；`docs/ui/mockups/` 只有 `library.html`、`item.html` 兩份草稿。plan 要求 M1 一起交付的頁面（新增、媒體庫、影音詳情、佇列、設定、登入）和 UI 相關的 NFR（NFR-SEC-1 CSP、NFR-PERF-10、NFR-A11Y-1～3、NFR-I18N-1、NFR-CMP-2、NFR-RWD-1）都還沒做。決定與預設見第 7 節；plan 第 24、26 節已記錄。
  - **真實樣本驗收沒做**：M1 驗收條件「四個平台共 10 部樣本都能一鍵產出 SRT」從未用真實的 yt-dlp、Groq、Supadata 驗證，驗收場景都跑在假工具上。第 6 節「還沒對真實服務驗證」的項目屬於同一類。
  - **人工驗收**：M1 有 7 個 `@manual` 場景還沒驗收。其中 5 個同時是 `@ui`（DEL-R3、LIB-R2、LIB-R5／R6、LIB-R9、LIB-R10），要等 Web UI；另外 2 個是 CLI-03、CLI-04（`watch` skill 改成薄包裝）。M2 還有 LIB-R12（`@ui @manual`）。
  - **CI 還缺**：`govulncheck`（NFR-SEC-4）、涵蓋率門檻（NFR-TEST-3）、goreleaser（NFR-CMP-1）。fuzz 目前只有 `internal/subtitle`，其他不可信輸入的解析器（RSS、yt-dlp JSON）沒有，CI 也沒有每晚排程（NFR-SEC-6）。
  - **M0 結論沒有回寫紀錄**：6 個 `@pending-m0` 場景（SRC-R4、SRC-R13、COR-R2／R3、TRN-R4、SEG-R3、SEG-R10）的門檻仍是暫定值，盤點時沒有看到 M0 實測結果寫回規格或 ADR。
  - **README 過時**：仍寫「只有 `serve` 與 `--version` 可用」，實際上 CLI 已完成。

## 2. 下一步

切片進度：PIPE → CLI 與 `/data` 鎖 → GRD → SRC-A → SRC-B → OPS → LIB → DEL → AUTH-SEC → LOCAL → PODCAST → ESTIMATE → M2 S0 骨架 → M2 S1 PRF＋S2 PRM → 規格修改 → M2 S6a TRN OpenCC（PR #1～#19，全部已合併）。下一步是 S7 OUT 渲染，再到 S3 LLM 核心；Web UI 切片的排入時機待 owner 決定（第 7 節）。

接下來：

1. 疊加 PR 的合併方式（已用在 #13～#17）：前一個 squash 合併後，把下一個的 base 改成 `master`，再把下層分支的修正 merge（不 rebase）進來，最後用 `-s ours` 併入 master，並確認 `git diff master...HEAD` 的 patch-id 與合併前相同。每個 PR 都要等 Codex 審查通過、CI 綠才 squash 合併（owner 已授權，不用另外詢問）；同一個 PR 最多連續重審 3 次，超過要交 owner 決定（#14 就因為這樣由 owner 再授權了幾輪）。
2. 剩下 M1 的項目：NRM-R7（見第 1 節，需要 owner 決定）、Web UI、真實樣本驗收、人工驗收、CI 補強（見第 1 節「M1 尚未收尾」）。
3. M2 的切片順序（S0 之後）：並行 S1 PRF（含 ING-R7）、S2 PRM、S6a TRN 確定性（OpenCC）、S7 OUT 渲染 → S3 LLM 核心（provider client、限流、執行層）→ 並行 S4 COR、S5 SEG、S6b TRN LLM、S8 OUT publish → S9 MCP → S10 AGT → S11 收尾。Web UI 切片不在這個順序內，預設與 S9 並行（第 7 節）。S0 之後 `@M2` 是 117 個場景：115 pending、2 failed，兩個失敗都需要 owner 決定（MCP stdio 場景與 Background 衝突、OUT-R9 預覽的影音沒有字幕來源）。規格與 `.feature` 修改後（第 2 節第 4 點）是 17 passed、26 failed、74 pending：Background 的處理方案、prompt 步驟可以執行了，場景會停在後面的步驟；S6a 之後（2026-11-22 實測）是 20 passed、23 failed、74 pending。下列 failed 都是之後的切片才會做的功能，沒有需要 owner 決定的：
   - 缺 LLM fixture（S5、S6b 要補）：SEG 7 個、TRN 8 個、localize 1 個；缺 media fixture `speech-with-pause-at-6.2s.m4a`（S5）。
   - 行為還沒做：COR glossary 傳給 ASR（S4）、SEG 手動字幕跳過 segment（S5，目前 `api` executor 回「還沒有實作」）、TRN 沿用手動字幕軌 1 個（S6a 之後仍失敗，OpenCC 的 3 個範例已通過）、OUT sidecar 對線上影音是 `skipped`（S8）。
4. **owner 決議（2026-11-22）：下列建議全部同意，其中規格與 `.feature` 的修改已在 `📝 docs(specs)` commit 完成，各規格對應的待決問題已關閉。** 決議內容：
   - **LLM 核心（擋 S3）**：
     - X1：沒有 provider 就 `skipped`（`not_configured`），維持 S0 的做法。
     - L1：LLM-R8 場景的 `received 4 requests` 改成 5。
     - L2：token 估算採 CJK 一字 1、其他每 4 字元 1（進位）、每句加 1、重疊句不計入。
     - L6：API key 環境變數 `NOTESTEME_ANTHROPIC_API_KEY`、`NOTESTEME_OPENAI_API_KEY`，其他 provider 用 `NOTESTEME_<ID>_API_KEY`。
     - wire 契約：同意，S3 寫成 ADR，fakellm 一起改。
     - P6：不帶版本號的 prompt 引用跟隨預設（`<id>` 等同 `<id>@default`）。
   - **S1/S2 設計決策**：
     - 同意：D6、D8、D13、X5、D10～D12、D14、D16、F4。
     - P7、P8 先接受。P7 之後可補掃描 `items.overrides_json` 並把檢查與刪除放進同一個交易，排在 M2 收尾。
   - **要改 `.feature` 或規格（已同意，已修改）**：
     - F1、F2：PRF-R2「Each layer」與 PRF-R3 加 `And the default profile is "standard"`。
     - F5：PRF-R8 與 PRF-R2 的 ingest 之後加 `And the worker runs until idle`。
     - P1：PRM-R7 compare 的 body 加 `"provider": "anthropic", "model": "fixture-model"`。
     - G1（PRF 版）：PRF-R1 補上 `glossary`（字串陣列），它與 COR `/api/glossaries` 的關係由 COR 切片決定。
     - O-4：PRF 開頭改成「處理方案在 M2（PRF 切片）實作；ING-R7 隨 PRF 切片通過」，並關閉 ING-R7 的待決問題。
     - MCP-R1：stdio 場景搬到 CLI 的 feature（`CLI-cli.feature` 的 `@M1` 改標在各個 M1 場景上，這個場景標 `@M2`）。
     - OUT-R9：`steps.md` 4.4 規則 (1) 改成只有 seed `source` 才算「場景已準備來源」，其餘走 fixture；改完 `@M2` 只有預覽場景由 failed 變 pending，沒有其他場景改變。
     - SEG-R10：補上 `the LLM provider "openai" answers "segment" with "segment-keep-all"`；fixture `segment-keep-all`（不合併也不切分、每句原樣輸出）要在 S5 建立。
     - COR-R4：影片標題改成 `服务拆分实践`；fixture `yt-zh001xxxxxx` 不含「微服务」、「数据库」，不用改。
   - **不用現在決定**：M0 量測的各項（`max_tokens`、重疊句數、`when_needed` 門檻、相似度算法、`⟨|⟩`）、OUT sidecar 檔名（先實測 BCP-47 與 ISO 639）、PIPE 的退避與 aging、translate 多語言（TRN 切片）。
   - **規格裡找不到原文，尚未處理**：X7、L7、A1、O1～O4、SEG-R2～R4 矛盾（handoff 的 G1）。AGT 的待決問題寫「無」。要處理時先讀相關 `.feature` 再問使用者。
   - **還沒回覆**：M2 路由的 scope（profiles 與 channel overrides 是否改 admin、ass-styles preview 是否改 ingest）。

   原本的待決清單（保留供對照）：X1 LLM 步驟預設值（S0 依建議 C：沒有 provider 就 skipped）、X7 兩個 Agent 同時領任務沒有場景、L1 LLM-R8 請求數 4 應為 5、L7 cost_ledger、G1 SEG-R2～R4 矛盾、P1 PRM-R7 body、P6 不帶版本號的 prompt 引用跟隨預設、F1 PRF-R2 Background、F2 PRF-R3 斷言驗證不到、F5 頻道層要等 metadata（場景加 `the worker runs until idle`）、O-4 PRF 的 M1 敘述、S1/S2 的 D1～D16 待確認（特別是 D6 頻道層在 metadata 才併入、D8 完整性只檢查處理方案、D13 硬刪除與 used_by 順序）、A1 AGT 並行上限、MCP stdio 場景搬到 CLI、O1～O4 OUT、COR-R4 斷言偏弱、SEG-R10 缺 answers、M2 路由的 scope（profiles 與 channel overrides 是否改 admin、ass-styles preview 是否改 ingest）。
5. 每個切片完成後執行 `go test ./features -run TestFeatures -update-passing` 更新棘輪清單，並檢查新增的通過場景。棘輪只涵蓋預設 tag（`@M1`）：M2 已通過的場景（例如 PRF、PRM 的 15 個）目前沒有棘輪保護，要靠每個切片手動跑 `-godog.tags=@M2` 比對；M2 完成、預設 tag 納入 `@M2` 時再一起進棘輪。
6. `features/fixtures/items/` 有 `yt-pg100xxxxxx.json`、`yt-abc123xxxxx.json`（後者只在影片沒有任何字幕軌時用到，目前只有 SEC 的場景用得到）。
7. 大切片的做法（LOCAL、PODCAST 用過）：architect 先出設計，主代理把設計凍結成契約檔（Go 介面、argv、marker 語法、資料模型），再分給檔案所有權不重疊的 builder 並行實作；harness 的工作包可以從一開始就並行。整合後主代理跑完整驗收與變異測試，另開 builder 只看 diff 與規格做審查。

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
- **pending 判斷的 helper**：`previousRequestNotImplemented()`（`features/steps/scenario.go`）只在前一個步驟的請求回 501 `not_implemented` 時才轉 pending，用在 `stageIs`、`no task is running` 與垃圾桶步驟。
- **godog 的 tag OR 用逗號**：`-godog.tags='@A,@B'`。`'@A || @B'` 會選到 0 個場景卻顯示通過。
- **斷言要能失敗**：PIPE 切片的審查用變異測試發現 5 個場景拿掉對應行為後仍然通過。每個切片的審查都要挑幾個場景做變異測試：在暫存 worktree 拿掉對應的 production 行為，場景必須變紅。
- **測試不連外網**（NFR-TEST-1）；時間一律用注入的 fake clock（NFR-TEST-2）。
- **不要讓已實作的步驟依參數回傳 pending 來避開失敗**。場景的步驟都已實作、行為還沒做時：
  - 小的行為就直接實作。
  - 大的就讓真正還沒實作的那個步驟維持 pending。
  - 目前允許的 pending 只有下列幾種：
    - 斷言遇到 production 回應的 `501 not_implemented`，或 CLI 回報 `not_implemented`（`the command exits with` 遇到尚未實作的子命令或 API 的 501），而且這個回應或結果必須是前一個步驟送出、執行的。
    - 腳本化來源的步驟查 production 的 `source.Implemented`，得知來源還沒實作。
    - `processed up to` 的影音沒有字幕來源，也沒有對應的影音 fixture（`itemFixtureFor` 的規則 3，見 steps.md 4.4）。
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
  - Supadata：非同步 job（202）還不會輪詢，目前當 permanent；暫時性錯誤時不會寫入 `source_probes`。（API key 已支援 DB 加密存放與 fallback，見 SEC-R4。）
  - 做 `youtube-auto` 前，要評估改用 yt-dlp 的 `json3` 格式（見 NRM 待決問題）。`features/fixtures/captions/auto-zh-en-mixed.json` 目前內容是 VTT，決定後與假 yt-dlp 一起改。
- **CLI**：
  - client 模式加 `--wait`、而 serve 是手動 worker 時，harness 還不會在背景推動 worker，步驟會失敗並說明（目前沒有場景用到）。
  - 本機路徑的 `ingest` 一律送 `{"path"}`（standalone 走程序內 handler，路徑不受限）。
  - `doctor` 的網路連線檢查要等 OPS 的已知影片；API key 檢查 Supadata 與 Groq。
  - JSON Lines 輸出在步驟中只比對第一個物件。
  - 非 Linux 平台的本機 socket 只靠檔案權限，沒有 uid 檢查。
- **GRD**：
  - GRD-R7、R8 的 403 場景在路徑檢查失效時會變成 pending（501），不是 failed，只靠 `passing.txt` 棘輪把關，不可以順手更新掉。
  - 直接輸入非 http 網址的場景驗證不到我們自己的 scheme 檢查（Go 的 `http.Client` 也會拒絕），由轉址場景與單元測試把關；GRD-R6 的 `--use-extractors` 只由單元測試把關（見 GRD 待決問題）。
  - 設定檔中「由資料庫管理的欄位」清單（profiles、prompts、budget(s)、settings、subscriptions、tokens、secrets、cookies、language、notifications）是實作時自訂的，需要 owner 確認；拿掉其中一項，`KnownFields` 仍會拒絕，場景驗證不到專用訊息。
  - `netguard.Deps` 同時有舊的 `Allowlist []netip.Prefix` 與新的 `AllowPrivate`，`features/internal/fakenet` 改用新欄位後可移除舊欄位。
  - 程式自己讀檔已改用 `Guard.Open`（`os.OpenRoot`）；交給 ffmpeg／ffprobe 的路徑仍有檢查與開檔之間的 TOCTOU（之後可在 toolexec 加 ExtraFiles，改傳 `/dev/fd/3`）。
  - 播放清單沒有項目數上限；yt-dlp 的播放清單錯誤訊息還沒對真實 yt-dlp 驗證（見 ING 待決問題）。
  - Supadata 回應超過大小上限時仍走 `isPermanent` 換下一個來源，最後訊息是「找不到可用的字幕來源」，看不到大小上限的訊息。
- **SRC**：
  - Groq：`verbose_json` 欄位、語言全名、25 MiB 上限、yt-dlp `--max-filesize` 的輸出字串都還沒對真實服務驗證。切塊（SRC-R12）已完成，但 Opus 位元率、靜音門檻、重疊長度、`-format_whitelist` 清單都是預設值，要用真實的 ffmpeg 與 Groq 確認（M0）；切塊不能續傳（`tasks.chunk_index` 尚未使用）。
  - LOCAL：
    - inbox 的判定原因只寫進 `inbox_entries` 與 log，還沒有 API。
    - inbox 中之後才出現、與既有上傳同名的檔案會記為衝突，不會新增。
    - 外掛字幕壞掉改走 ASR 時，metadata 決定的資源類別不知道這件事（只影響並行分類）。
    - 上傳的並行上限以資料目錄為鍵、全程序共用；閒置逾時用實際時間（讀取期限是 net 層的絕對時間，不能用 fake clock）。
    - `internal/backup.ParseUploadPath` 與 `pipeline.ValidUploadName` 各有一份檔名規則，條件一致。
    - ffprobe 的 disposition 在假工具中固定為「每種類型的第一條是 default」。
  - Groq 上傳用 chunked（`io.Pipe`），要求 Content-Length 的 proxy 會失敗。
  - Supadata 仍是先讀完 body 再依狀態碼分類（Groq 已改成先看狀態碼）。
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
- **AUTH / SEC**：
  - M1 的 token（`AUTH-R7～R10`）與 secrets（`SEC-R4、R6、R8、R9`）已完成；`AUTH` 的 `@mcp` origin 檢查（`AUTH-R11`）留待 M2。
  - `doctor` 維持 `noBackend: true`，目前仍只檢查環境變數 API key（透過 `/api/settings/services` 寫入 DB 的 key 仍會顯示 `warn`）。
  - `cookieBodyLimit` 包在 `mux` 外層、早於 Huma middleware，因此超過 512 KiB 的 `POST /api/cookies` 帶 bearer token 時會先回 `422 cookie_too_large` 而不是 `403`。
  - `RevokeAPIToken` 以 `id = ? OR name = ?` 比對（假設 token 名稱不會與另一個 token 的 UUIDv7 相同）；`CreateToken` 已禁止 `/` 與控制字元。
  - `X-Forwarded-Proto` 應該只採用可信 proxy 的值。
  - IPv6 的鎖定以單一位址計數。
- **其他**：
  - `Given the item … has been processed up to …` 會真的執行 pipeline；沒有準備字幕來源的場景要有 `features/fixtures/items/` 的 fixture（見第 2 節）。
- **DEL**：
  - 只靠單元測試守住：
    - 保留期限的邊界（剛好第 30 天）。
    - 每天只清一次。
    - tombstone 的寫入與移除。
    - 預覽不寫入資料。
    - purge 時取消殘留任務、刪除 events、不過度刪除。
  - 權限：`DEL-R12` 已接上（`GET /api/trash`、`deletion-preview` 為 `read`，其餘刪除／還原為 `admin`），`@mcp` 兩則留待 M2。
  - 手動新增與上傳移除 tombstone；inbox 自動新增遇到 tombstone 時不建立、不移除（ING-05 已接上）。M4 的訂閱接上時，比照 inbox 不移除。
  - tombstone 的 `sha256` 只有本機檔案有值（平台影音為空）。
  - `cues_fts`、`outputs`、術語候選還不存在，之後接上時，永久刪除要一併清除（uploads 已接上）。
  - 永久刪除後從舊備份還原會留下壞的產出（見 OPS 待決問題）。
  - `removeDirs` 失敗只記 log，沒有孤兒目錄的掃描。
  - `app.Server.SeedCost`、`CostEntries` 只給 harness 用。
- **LIB**：
  - 詳情 API（`Pipeline.Item` 的 `stages`）不列出沒有執行紀錄的 stage，列表篩選則視為 `pending`，UI 切片要決定是否補上。
  - 標題搜尋不做 NFKC 與簡繁互通；00006 的回填只處理 ASCII 的大小寫（M1 尚未發布，沒有舊資料）。
  - `group_by`（LIB-R4 的分組）、`read_state`（回 501）還沒做；`status` 只認 M1 的三個 stage。
  - `CountItemsFiltered` 每次掃全表（10k 筆約 3ms）；status 篩選在 10k 筆約 62ms。列表與 COUNT 是兩段相同的 WHERE，靠單元測試確認一致。
  - CLI 的 `resolveItem` 每次逐頁找，API 沒有依代號篩選的參數。
  - 只靠單元測試守住：`waiting` 不含執行中的任務、垃圾桶中的影音不在列表、排序、`limit` 與最後一頁沒有 `next_cursor`。
  - 字幕讀取端沒有串流（NFR-RES-3 只做到輸出端）。
  - 有 HTML 回應時要加上 CSP 等標頭（NFR-SEC-1）。
  - CI（`.github/workflows/ci.yml`）：gofmt、`go mod tidy -diff`、vet、`go test -race`、`sqlc diff`，不建置 image。image 由 `docker-publish.yml` 只在 `v*` tag 時建置、冒煙測試後發布到 GHCR（私人 repo，避免浪費 Actions 額度）。還沒有 Python 版規格檢查（Go 版在 `go test` 中）與 `govulncheck`。

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
| LOCAL | steps.md 的語意補充（部分 `#` 表格依 `#` 比對；`processed up to` 規則 (1) 對 `local:` 也算外掛與內嵌字幕）、用路徑新增的延遲驗證、上傳上限 4 GiB、YouTube ASR 也走前處理、備份包含 uploads | 已依預設實作（PR #13），待 owner 確認 |
| NRM | NRM-R7 場景的字幕軌不會被 `caption_priority` 選中 | 建議改成 `manual captions` 或宣告原文語言 `zh-Hans`，需 owner 同意改 `.feature` |
| UI | Web UI 切片（M1 欠下的 UI）的排入時機與前端範圍 | 與 S9 MCP 並行，先做 M1 的頁面；前端工具鏈（建置、測試、i18n）與是否沿用 `docs/ui/mockups/` 的設計，開工前由 owner 決定 |

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
- 待清理（內容都已在 `master`；`feat/auth-secrets` 的 Wave 2 暫存 worktree `.worktrees/auth-secrets-cli`、`.worktrees/auth-secrets-steps` 已清除；疊加的 PR 分支 `feat/sources`～`feat/auth-secrets` 保留等待 push 與建立 PR）：
  - `.worktrees/store-sqlc` 與 `refactor/store-sqlc` 分支（本機與遠端）。
  - `.claude/worktrees/` 下 builder 用過的 worktree（切片 1 到 OPS 的都是），以及 `worktree-agent-*` 分支。
  - `.worktrees/m1-skeleton`、`feat/m1-skeleton`、`docs/bdd-specs`、`docs/plan-review-revisions` 都已併入 `master`，可以一併移除。

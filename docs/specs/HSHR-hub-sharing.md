# HSHR：Client 的分享、重新生成與 ASR 前查詢

- 狀態：draft
- 里程碑：M5
- 相關：plan 第 6、19、20 節；ADR-0005、ADR-0013、ADR-0014；[ADR-0030](../adr/0030-hub-deployment-and-storage.md)、[ADR-0032](../adr/0032-client-hub-protocol-and-privacy.md)；[SRC](SRC-transcript-sources.md)、[PIPE](PIPE-pipeline-queue.md)、[SEC](SEC-config-secrets.md)
- 驗收場景：[`features/HSHR-hub-sharing.feature`](../../features/HSHR-hub-sharing.feature)（Client＋fakehub，閘門 `Given the owner's client is connected to a Hub`）

## 目的

讓 Client 把校正後的字幕與筆記連同來源紀錄分享到 Hub、拿別人的原始字幕在自己的 Client 重新生成，以及（使用者同意後）在付費轉錄之前先查 Hub。三件事都會把資料送出本機，所以送出什麼、什麼不能送、失敗時怎麼辦都要精確：分享只送白名單欄位，非公開與需要登入的內容不送，查詢只送影片 ID 雜湊的前綴，Hub 回來的內容一律當作不可信的輸入驗證。

## User stories

| ID | Story |
|---|---|
| HUB-02 | As the owner, I want to share my corrected subtitles and notes to the Hub with their provenance, so that others can reuse my work. |
| HUB-04 | As the owner, I want to regenerate a video in my client from someone else's raw subtitles, so that I skip paying for transcription. |
| HUB-05 | As the owner, I want my client to check the Hub before paying for ASR, so that I reuse what already exists. |

## 規則

| ID | 規則 |
|---|---|
| HSHR-R1 | 連線設定：`hub.url` 只能寫在設定檔，必須是 `https` 網址，否則 `serve` 啟動失敗，訊息「設定 hub.url 必須是 https 網址。」。Hub token 用 `PUT /api/settings/hub {token}`（`admin`）設定，存成 secret（SEC-R4），`GET /api/settings/hub`（`admin`）只回 `{url, token_set, precheck}`，不含 token。對 Hub 的請求走 netguard（ADR-0014），一律帶 `Authorization: Bearer <token>`。沒有 `hub.url` 或沒有 token 時，分享與重新生成回 409 `hub_not_configured`「尚未設定 Hub。請在設定檔加入 hub.url，並在設定頁填入 Hub token。」，`actions: ["設定 Hub"]`。 |
| HSHR-R2 | 分享：`POST /api/items/:id/hub/share {license, attest, artifacts?, include_frames?, visibility?}`（`admin`）產生 bundle 並送到 Hub 的 `POST /api/v1/shares`，成功回 201 `{share_id, versions: [{kind, variant, lang, version_id, deduplicated}]}`，並記入 `hub_shares`；之後 `GET /api/items/:id` 的 `hub_shares` 列出 `{share_id, hub_url, created_at}`。CLI：`notesteme hub share <item> --license <SPDX> --yes [--artifact <variant>[:<lang>]]… [--include-frames]`，`--yes` 等同 `attest: true`。MCP 沒有分享的 tool。 |
| HSHR-R3 | 送出之前在 Client 檢查，不通過時不連線到 Hub：`attest` 不是 `true` 時 422 `attest_required`「分享前要確認你有權分享這些內容。」；沒有 `license` 時 422，`errors[].path` 為 `license`，訊息「請選擇授權方式。」；`license` 不是 `CC-BY-4.0`、`CC-BY-SA-4.0`、`CC-BY-NC-4.0`、`CC-BY-NC-SA-4.0`、`CC0-1.0` 時 422 `license_invalid`「不支援的授權：<值>。」。 |
| HSHR-R4 | 不能分享的內容（Client 檢查，不連線到 Hub；Hub 也會再檢查，HREP-R3），回 422 `not_shareable`：本機檔案「本機檔案不能分享到 Hub。」；`availability` 不是 `public`「非公開影音不能分享到 Hub。」；要分享的任何產出，其衍生鏈上的 `source` 來源紀錄是 `cookie_required: true`「需要登入才能取得的字幕不能分享到 Hub。」。影音在垃圾桶中時回 404 `in_trash`（同 DEL-R1）。 |
| HSHR-R5 | 預設分享的內容：每個字幕變體與語言的現行版本、`chapters`、`note`。`frames` 只在 `include_frames: true` 時才放（Hub 端一律 `private`，HREP-R8）。媒體檔、候選畫面、被取代的版本一律不放。`artifacts` 指定時只放列出的產出（`[{kind, variant?, lang?}]`）；不存在時回 422 `artifact_not_found`「這部影音沒有「<variant>（<lang>）」的字幕。」。來源紀錄為 `executor: hub` 的 `source`（從 Hub 取得的原始字幕）不在預設的內容中，因為它已經在 Hub 上；由它衍生的 `normalized` 等變體是 Client 自己的產出，照常分享。 |
| HSHR-R6 | 送出的資料只有 ADR-0032 第 2 點的白名單：manifest 的 `created_by` 只有 `app_version`，沒有 `instance_id`；來源紀錄只保留白名單欄位，另外一律加上 `parents_sha256`（沒有上游時是空陣列）；衍生鏈上有 `hub` 來源時，每個產出的來源紀錄都帶上那個來源的 `hub_origin`（讓 Hub 記錄 `regenerated_from`，HREP-R6）；bundle 的任何內容都不含本機路徑（`/data`、`/media`、inbox 的路徑）、secrets 與 token。 |
| HSHR-R7 | `transcript` 步驟寫入 `source` 產出時，來源紀錄加上 `cookie_required`：來源需要登入才拿得到時為 `true`（`bilibili-ai`、`availability` 不是 `public` 的平台字幕與媒體），否則為 `false`。缺這個欄位的舊產出依 ADR-0032 第 2 點推導。 |
| HSHR-R8 | Hub 的錯誤依 ADR-0032 第 5 點對應成 Client 的 502（`hub_unavailable`、`hub_unauthorized`、`hub_forbidden`、`hub_rate_limited`）、404（`hub_version_not_found`）或原樣的 422，訊息照表，`hub_rate_limited` 附 `retry_after_s`。 |
| HSHR-R9 | 重新生成：`POST /api/hub/import {version, profile?, overrides?}`（`ingest`）先取得版本資訊（`GET /api/v1/versions/:id`）；不是字幕、或 `variant` 不是 `source`、`normalized` 時回 422 `not_raw`「只能用別人的原始字幕（source 或 normalized）重新生成。」。接著取得 `GET /api/v1/versions/:id/content` 並驗證（HSHR-R10），通過之後才依版本的 `(platform, external_id, part)` 找到或新增影音（新增時照常執行 `metadata`，影音資訊取自平台，不採用 Hub 上的標題），寫入 `source` 變體，`transcript` 步驟 `done`、來源 `hub`，不呼叫任何其他字幕來源；之後依處理方案從 `normalize` 繼續。回 202 `{item: {id, ref}}`。來源紀錄：`executor: hub`、`source: hub`、`hub_origin: {url, version_id, sha256, uploader}`，以及 Hub 上原本的模型欄位（`model_verified` 照抄）。CLI：`notesteme hub import <version-id> [--profile <p>]`。 |
| HSHR-R10 | Hub 回來的內容是不可信的輸入：大小上限 64 MiB；內容的 sha256 與版本的 `sha256` 不符時回 502 `hub_integrity`「Hub 回傳的內容與版本的 sha256 不符，已拒絕使用。」（`permanent`），什麼都不寫入；cues JSON 以匯入字幕的驗證器（OUT-R13～R15 的上限）檢查，不通過時回 422 `hub_content_invalid`「Hub 回傳的字幕格式不正確：<原因>。」。 |
| HSHR-R11 | 重新生成的影音已經存在時，新的 `source` 版本取代現行版本（舊版本保留，同 PIPE-07），從 `normalize` 重跑；影音在垃圾桶中時回 409 `in_trash`（同 DEL-R10）。 |
| HSHR-R12 | ASR 前查詢的設定：`hub.precheck.enabled` 預設 `false`。`PUT /api/settings/hub {precheck: {enabled: true, acknowledge}}` 時，`acknowledge` 必須等於目前的 `disclosure_version`（`hub-precheck-v1`），否則回 422 `acknowledgement_required`，回應帶 `disclosure` 與 `disclosure_version`。`GET /api/settings/hub` 的 `precheck` 是 `{enabled, disclosure, disclosure_version}`；`disclosure` 為「開啟後，Notesteme 在付費轉錄（Supadata、ASR）之前，會把平台名稱與影片 ID 的 SHA-256 前 4 個十六進位字元送到 <hub.url>，並用你的 Hub token 驗證身分。Hub 會知道你的帳號在什麼時候查詢了哪個前綴，但無法從前綴得知是哪一部影片。本機檔案與非公開影音不會查詢。」。 |
| HSHR-R13 | 查詢的時機：開啟時，`transcript` 步驟在**即將呼叫第一個付費來源**（`supadata` 與 ASR provider；平台字幕、`podcast-transcript`、本機字幕都沒有可用的結果之後）時，查詢一次 `GET /api/v1/lookup?platform=<platform>&prefix=<sha256(video_key) 的前 4 個十六進位字元>`（ADR-0032 第 3 點）。本機檔案、`availability` 不是 `public` 的影音、沒有設定 Hub 時不查。請求的路徑、query、標頭與 body 都不含影片 ID、標題與網址。 |
| HSHR-R14 | 命中與挑選：Client 用完整的 `video_key_sha256` 找出自己的影片；候選是語言與影音原文語言相同（原文語言不明時不限）的版本，依序偏好 `variant: source`、`model_verified: true`、`score` 高、`sha256` 字典序小的一個。取得內容與驗證同 HSHR-R10；通過時 `transcript` 步驟 `done`、來源 `hub`，來源紀錄同 HSHR-R9，不呼叫付費來源，成本帳本沒有這一步的紀錄。沒有候選時照常呼叫付費來源。 |
| HSHR-R15 | 查詢失敗不擋 pipeline：逾時（5 秒）、連線錯誤、5xx、429、401、403、內容驗證失敗時，照常呼叫付費來源，`transcript` 完成後顯示提醒：「無法查詢 Hub（<原因>），這次改用付費來源。」（`<原因>` 為「HTTP 503」、「連線逾時」、「token 無效或權限不足」、「內容驗證失敗」之一）。每次執行 `transcript` 最多查詢一次。 |

## 介面

- 設定檔：`hub.url`（只能寫在設定檔）、`hub.precheck.enabled`（也可以用 API 開啟，但要確認說明）。
- API（Client）：

  | 路由 | scope |
  |---|---|
  | `GET /api/settings/hub`、`PUT /api/settings/hub` | `admin` |
  | `POST /api/items/:id/hub/share` | `admin`（把資料送到外部服務） |
  | `POST /api/hub/import` | `ingest`（會觸發 LLM 步驟，花錢） |
  | `GET /api/items/:id` 的 `hub_shares` | `read` |

- CLI：`notesteme hub share`、`notesteme hub import`、`notesteme hub status`（顯示 `GET /api/settings/hub` 與 `GET /api/v1/me` 的結果，用來測試連線）。
- MCP：Client 的 MCP 不新增 tool（分享與重新生成會送出資料或花錢，留在 API 與 CLI）；Agent 要搜尋 Hub 時用 Hub 自己的 MCP（[HMCP](HMCP-hub-mcp.md)）。
- 新的字幕來源 ID：`hub`（steps.md 第 3 節）；新的來源紀錄欄位：`cookie_required`、`hub_origin`；新的資料表：`hub_shares`（ADR-0032 第 4 點）。
- UI（Client，`@ui @manual`）：影音詳情的「分享到 Hub」對話框（選產出、授權、聲明勾選、預覽會送出的欄位）、`hub` 來源的標示（「來自 Hub：@alice 的原始字幕」）；設定頁的 Hub 區段（URL 唯讀、token、測試連線、ASR 前查詢的開關與說明）。頁面在 `docs/ui/pages.md` 補上之前，以本規格為準。

## 不在範圍內

- 自動分享（每部影音處理完就上傳）。
- 從 Hub 下載筆記或章節到 Client（只有原始字幕能重新生成）。
- 多個 Hub（`hub.url` 只有一個）。
- Hub 協調多個實例「誰在處理哪部影片」（ADR-0011 的評估項目，M5 不做）。

## 待決問題

- S1：ASR 前查詢直接採用 hash 前綴（HSHR-R13、ADR-0032 第 3 點），比 plan 第 20 節「先送影片 ID、之後評估只送前綴」保守。決定（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）。
- S2：`cookie_required`（HSHR-R7）是新的來源紀錄欄位，會動到 SRC 的 `transcript` 實作；判斷「用 cookie 取得的字幕」的依據是「來源需要登入才拿得到」，不是「下載時剛好帶了 cookie 檔」，否則上傳過 YouTube cookie 的使用者所有公開影片都不能分享。決定同上，待 owner 追認。
- S3：分享用 `admin` scope（HSHR-R2）：分享把資料送到公開服務、用的是 owner 的 Hub 身分，比 `ingest`（花錢）更需要 owner 本人。Agent 不能分享。
- S4：重新生成只接受原始字幕（HSHR-R9）。用別人校正過的字幕直接翻譯也有價值，但那等於信任別人的 LLM 輸出，先不做。
- S5：Hub 回來的字幕在 Client 的 LLM 步驟中照 LLM-06 當作資料處理；Hub 的字幕可能刻意夾帶指示，不另外加規則，靠既有的 LLM-R 系列。
- S6：Client 的 migration 編號（`hub_shares`）在實作時依 M3（規劃 00017）、M4 的合併順序取下一個號碼。

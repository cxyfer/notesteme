# ADR-0032：Client 與 Hub 的協定、送出的資料與隱私

- 狀態：提議中（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）
- 日期：2026-11-23

## 背景

plan 第 20 節：Client 用 PAT 連到 Hub；可以選擇在付費 ASR 之前先查 Hub，開啟時要說明送出哪些影片 ID，「之後評估改成只送 ID hash 的前綴」。plan 第 26 節把「Hub 查詢的隱私做法」列為 M5 前定案。ADR-0005 的 bundle v0 是給實例之間搬移用的（M4 的 [BND 規格](../specs/BND-bundles.md) 定了驗證規則），manifest 含 `created_by.instance_id` 與完整的來源紀錄，直接送到公開的 Hub 會洩漏本機資訊。

## 決策

### 1. Hub API 的形狀

- 路由前綴 `/api/v1/`（Hub 一開始就有版本，Client 與 Hub 會分開升級）；`/auth/<provider>/start|callback` 是瀏覽器的 OAuth 流程；`/mcp` 是 Hub MCP（HMCP）；`/healthz`、`/readyz` 同 Client。
- 用 Huma v2 產生 OpenAPI 3.1，`GET /api/v1/openapi.json` 公開；比照 Client 有 OpenAPI 快照測試。
- 錯誤格式與 Client 相同（`internal/api/errors.go`）：`{code, message, actions?, required_permission?, retry_after_s?, errors?, details?}`；`required_permission` 比照 Client 的 `required_scope`。限流一律 429 `rate_limited`，附 `retry_after_s` 與 `Retry-After` 標頭。
- **Client 契約**（fakehub 實作的子集，`fakehub.Routes`）：

  | 路由 | 權限 | 用途 |
  |---|---|---|
  | `GET /api/v1/me` | 任何有效的 PAT | 驗證 token、取得有效權限（設定頁的「測試連線」） |
  | `POST /api/v1/shares` | `upload` | 上傳 bundle（multipart：`bundle`、`attest`、`visibility`） |
  | `GET /api/v1/lookup` | `regenerate` | ASR 前查詢（`platform`、`prefix`） |
  | `GET /api/v1/versions/{id}` | `read` | 版本的 metadata 與來源紀錄 |
  | `GET /api/v1/versions/{id}/content` | `regenerate` | 原始的 cues JSON（Client 重新生成用） |

### 2. 送到 Hub 的 bundle

- 格式是 BND 規格的 bundle v0（`.notesteme.zip`、`manifest.json`、`artifacts/`），Client 在分享時產生，**另外**套用下面的規則（Hub 端也驗證，HREP-R3）：
  - 只含使用者選的產出；預設不含 `frames`，不含媒體檔、候選畫面、被取代的版本。
  - `created_by` 只有 `app_version`，**不送 `instance_id`**（Hub 已經知道是哪個帳號上傳，`instance_id` 只會讓不同 Hub、不同帳號的上傳可以互相關聯）。
  - `item` 只有 `platform`、`external_id`、`part`、`title`、`channel.{external_id,title}`、`published_at`、`duration_s`、`original_lang`、`availability`。
  - 每個產出的來源紀錄只保留白名單欄位：`executor`、`provider`、`model`、`model_reported`、`model_verified`、`prompt.{id,version,sha256}`、`params_sha256`、`source`、`track.{lang,kind}`、`timing`、`tool_versions`、`created_at`、`cookie_required`、`hub_origin.{version_id,sha256}`，以及 Client 計算的 `parents_sha256`（上游產出的內容雜湊，取代只在本機有意義的 artifact ID）。其他欄位（本機路徑、設定快照、chunk 細節、`timing_check`、`imported_from` 的本機資訊）一律不送。
  - `license` 是使用者選的 SPDX ID。
- `cookie_required`（新的來源紀錄欄位，Client 的 `transcript` 步驟寫入）：來源**需要登入才拿得到**時為 `true`，例如 `bilibili-ai`、`availability` 不是 `public` 的影音。只是剛好帶了 cookie 檔去下載公開字幕，不算。缺這個欄位的舊產出依來源 ID 與影音的 `availability` 推導（`bilibili-ai` 或不是 `public` 就視為 `true`）。

### 3. ASR 前查詢（HUB-05）只送 hash 前綴

- 查詢的鍵是 `video_key = "<platform>:<external_id>:<part>"`（沒有分P時 `part` 為空字串，例如 `youtube:abc123xxxxx:`）。Client 只送 `platform` 與 `sha256(video_key)` 的前 4 個十六進位字元：`GET /api/v1/lookup?platform=youtube&prefix=1a2b`。
- Hub 回傳這個前綴下所有影片的**完整** `video_key_sha256`，以及每部影片中 token 看得到的原始字幕版本（`variant` 為 `source` 或 `normalized`）；Client 用完整雜湊挑出自己的影片。前綴 4 碼約有 65,536 個桶，Hub 看得到「這個帳號查過這個桶」，但不知道是哪一部。
- 這比 plan 第 20 節「先送影片 ID、之後評估只送前綴」保守，是刻意的偏離（HSHR 規格的待決問題）。
- 查詢預設關閉；開啟時要確認說明文字（`disclosure_version`），說明列出實際送出的內容。本機檔案、非公開影音一律不查。

### 4. Client 的資料與設定

- 設定檔：`hub.url`（只能寫在設定檔，`https` 網址；理由同 SPB 的 `sponsorblock.base_url`：API 能改的話，可以把 Client 的請求與 token 導到任意主機）。Client 對 Hub 的請求走 netguard（私有網段要寫進 allowlist），逾時 30 秒（ASR 前查詢 5 秒），回應上限 64 MiB。
- Hub token：`PUT /api/settings/hub {token}`，存成 secret `(hub, token)`，比照 `PUT /api/settings/services` 的 API key：只能寫入，`GET` 只回 `token_set`。
- 新的資料表 `hub_shares (id, item_id, hub_url, share_id, created_at, versions_json)`；migration 編號在實作時依 M3、M4 的合併順序取下一個可用號碼。
- 新的字幕來源 ID `hub`（steps.md 第 3 節）：從 Hub 取得的字幕。來源紀錄 `executor: hub`、`source: hub`，附 `hub_origin.{url,version_id,sha256,uploader}` 與 Hub 上原本的模型欄位（`model_verified` 照抄，不會升級成 `true`）。

### 5. 錯誤對應

Client 呼叫 Hub 失敗時，Client 的 API 回 502，`code` 與錯誤類別如下（plan 第 5.2 節）：

| Hub 的回應 | Client 的 `code` | 錯誤類別 | 訊息 |
|---|---|---|---|
| 連線失敗、逾時、5xx | `hub_unavailable` | `retryable` | 無法連線到 Hub（<原因>）。請稍後再試。 |
| 401 | `hub_unauthorized` | `needs_action` | Hub token 無效或已過期。請在 Hub 建立新的 token，再到設定頁更新。 |
| 403 且有 `required_permission` | `hub_forbidden` | `needs_action` | Hub token 沒有「<權限>」權限。請在 Hub 建立有這個權限的 token。 |
| 429 | `hub_rate_limited` | `retryable` | Hub 暫時拒絕請求（太頻繁），請在 <n> 秒後再試。 |
| 404、410 | `hub_version_not_found`（回 404） | `permanent` | Hub 上找不到這個版本，可能已被移除或你沒有權限查看。 |
| 422 | 原樣轉出 Hub 的 `code`、`message`、`errors`（回 422） | `permanent` | （Hub 的訊息） |

## 考慮過的選項

- **ASR 前查詢直接送影片 ID**：實作最簡單，Hub 可以精確回答；但 Hub 會得到使用者看過哪些影片的完整清單，而這個功能的目的只是省錢。
- **k-匿名的更短前綴（3 碼）**：桶更大、隱私更好，但每次查詢的回應會變大；4 碼與 SPB 的做法一致，之後可以調整。
- **分享時送完整的 bundle（含 `instance_id`）**：與 BND 的格式完全相同，但會把只在本機有意義的資料送到公開服務。

## 影響

- 好處：送到 Hub 的資料都列在白名單，可以逐欄位審查；查詢不洩漏影片 ID。
- 代價：Client 要維護「給 Hub 的 bundle」這個額外的產生路徑（BND 的匯出加上過濾）；Hub 的 lookup 回應比精確查詢大。
- BND（M4）的 manifest 驗證要接受沒有 `instance_id` 的 `created_by`，或 Hub 另外放寬；兩邊的規格要對齊（HREP 規格的待決問題、handoff）。

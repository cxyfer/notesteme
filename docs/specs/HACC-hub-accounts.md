# HACC：Hub 帳號、權限與下架

- 狀態：ready
- 里程碑：M5
- 相關：plan 第 3.2、19、20 節；ADR-0012、ADR-0013、ADR-0014；[ADR-0030](../adr/0030-hub-deployment-and-storage.md)、[ADR-0031](../adr/0031-hub-identity-permissions-and-content.md)、[ADR-0032](../adr/0032-client-hub-protocol-and-privacy.md)
- 驗收場景：[`features/HACC-hub-accounts.feature`](../../features/HACC-hub-accounts.feature)（真 Hub，閘門 `Given a Hub is running`）

## 目的

Hub 是多使用者的公開服務：使用者用 Google、GitHub 或 Linux.do 登入，一個帳號可以連結多個登入方式；管理員依群組與自動規則決定誰能看、誰能下載、誰能上傳，並處理下架申請。帳號連結、權限與下架是 Hub 被濫用時的第一道防線，規則都要能被驗證。

## User stories

| ID | Story |
|---|---|
| HUB-01 | As a hub member, I want to sign in with Google, GitHub or Linux.do and link several of them to one account, so that I am not locked to one provider. |
| HUB-07 | As a hub admin, I want to set who can see what and handle takedown requests, so that the Hub stays within the rules. |

## 規則

| ID | 規則 |
|---|---|
| HACC-R1 | 登入方式：`google`（OIDC 授權碼流程）、`github`（OAuth2）、`linuxdo`（OAuth2，endpoint 見 ADR-0012）。endpoint 寫死在程式中、不能設定；設定檔只有 `oauth.<provider>.client_id`，client secret 只能來自環境變數 `NOTESTEME_HUB_OAUTH_<PROVIDER>_CLIENT_SECRET`。`GET /api/v1/auth/providers` 依 `google`、`github`、`linuxdo` 的順序列出已設定的登入方式 `{providers: [{id, name}]}`；沒有設定 `client_id` 或 secret 的登入方式不列出，`GET /auth/<provider>/start` 回 404 `provider_not_configured`「這個登入方式尚未啟用。」。 |
| HACC-R2 | `GET /auth/<provider>/start` 回 302，轉到供應商的 authorize URL，帶 `response_type=code`、`client_id`、`redirect_uri=<public_url>/auth/<provider>/callback`、`state`（32 bytes 隨機值，base64url）、`code_challenge`（S256，verifier 為 64 個字元）與 `code_challenge_method=S256`；Google 另外帶 `nonce`（32 bytes）與 `scope=openid email profile`；GitHub 只有設定檔的自動規則用到 `org_member` 時才多要 `read:org`。同時設定 cookie `notesteme_hub_oauth`（`HttpOnly`、`Secure`、`SameSite=Lax`、`Path=/auth`、`Max-Age=600`），state 的 hash、verifier（以 session 金鑰加密）與 nonce 存在 `oauth_states`，10 分鐘後失效。 |
| HACC-R3 | callback：`state` 必須與 cookie 及 `oauth_states` 中一筆未使用、未過期、同一個供應商的紀錄相符，否則回 400 `oauth_state_invalid`「登入請求已過期或無效，請重新登入。」；每個 state 只能使用一次，重送同一個 callback 也是這個錯誤。供應商回傳 `error=access_denied` 時回 400 `oauth_denied`「你取消了登入。」。換 token 時送出 `code_verifier`。Google 的 `id_token` 驗證簽章（JWKS）、`iss`、`aud`、`exp` 與 `nonce`；任何一項不符，或供應商的 token、使用者資訊 endpoint 失敗、逾時（10 秒）時，回 502 `oauth_provider_error`「無法向 <供應商名稱> 完成登入，請稍後再試。」，不建立帳號。 |
| HACC-R4 | 身分以 `(provider, subject)` 識別：Google 的 `sub`、GitHub 的數字 `id`、Linux.do 的 `id`；使用者名稱與 email 改變不影響識別。第一次以某個身分登入時建立新帳號，加入 `members` 群組；`display_name` 取自供應商的使用者名稱，去掉控制字元後最多 40 個字元。登入成功回 302 到 `/`，設定 session cookie `notesteme_hub_session` 與 `notesteme_hub_csrf`。 |
| HACC-R5 | 不依 email 合併帳號：以新的登入方式第一次登入時一律建立新帳號，就算 email 與既有帳號相同（ADR-0031 第 1 點）。 |
| HACC-R6 | 連結登入方式：已登入時 `POST /api/v1/me/identities/<provider>/link` 回 200 `{authorize_url}`，流程同 HACC-R2、R3，state 綁定這個帳號（`intent: link`）。session 最近一次登入超過 10 分鐘時回 403 `reauth_required`「請重新登入後再連結新的登入方式。」。每個帳號每個供應商最多一個身分：已經連結同一個供應商時回 409 `provider_already_linked`「這個帳號已經連結了一個 <供應商名稱> 帳號。」。身分已經屬於另一個帳號時，callback 回 409 `identity_in_use`「這個 <供應商名稱> 帳號已連結到另一個 Hub 帳號。」，兩個帳號都不變。連結成功後 callback 回 302 到 `/account`，`GET /api/v1/me` 的 `identities` 多一筆 `{provider, login}`（依連結的先後排列），之後用任何一個身分登入都進入同一個帳號。 |
| HACC-R7 | 移除登入方式：`DELETE /api/v1/me/identities/<provider>` 回 204；帳號只剩這一個登入方式時回 409 `last_identity`「這是你唯一的登入方式，不能移除。」。連結與移除都寫入稽核紀錄（HACC-R17）。 |
| HACC-R8 | session：cookie `HttpOnly`、`Secure`、`SameSite=Lax`，閒置 30 天失效，`POST /api/v1/auth/logout` 立即失效。非 GET 的 session 請求必須在 `X-CSRF-Token` 帶 `notesteme_hub_csrf` 的值，否則 403 `csrf_invalid`（同 AUTH-R5）。 |
| HACC-R9 | PAT：`POST /api/v1/me/tokens {name, permissions, expires_in_days}` 回 201，`token` 只顯示這一次（格式 `nsh_` 加 40 個 base62 字元），DB 只存 hash；`expires_in_days` 為 1～365，預設 90。`permissions` 不在帳號當下的有效權限中時回 422 `permission_not_held`，`errors[].path` 指到該項。每個請求的有效權限是「PAT 的權限 ∩ 帳號當下的有效權限」（ADR-0031 第 2 點）。撤銷（`DELETE /api/v1/me/tokens/:id`）或過期的 PAT 回 401 `unauthorized`。 |
| HACC-R10 | 群組：內建 `members`（每個登入的帳號，預設 `[read, download]`）、`contributors`（預設 `[read, download, regenerate, upload]`）、`moderators`（預設 `[read, download, moderate]`）、`admins`（`[admin]`，包含全部權限）；內建群組的權限與自訂群組只能在設定檔的 `groups` 定義。有效權限是所屬群組的聯集。 |
| HACC-R11 | 自動規則（ADR-0012 的形狀）：`provider: linuxdo` 的 `trust_level_gte`、`provider: github` 的 `org_member`、`provider: google` 的 `email_domain`（只在 `email_verified: true` 時成立）。每次以該供應商登入時重新評估：成立就以 `source: rule` 加入，不再成立就移除 `source: rule` 的成員資格；手動指派（`source: manual`）不受影響。 |
| HACC-R12 | 手動指派：`PUT /api/v1/admin/users/:id/groups/:group`、`DELETE …`（`admin`）。`admins` 群組不能用 API 指派或移除，回 403 `admin_by_config`「管理員只能在設定檔或用 notesteme-hub admin grant 指定。」；設定檔的 `admins: [{provider, subject}]` 在該身分登入時以 `source: config` 加入。 |
| HACC-R13 | 權限矩陣：每個路由宣告需要的權限（見「介面」的表），缺少時回 403 `forbidden`，回應帶 `required_permission`；權限在讀取 body 與查詢資源之前檢查（同 Client 的 scope，AUTH 規格待決問題），所以不存在的版本也是 403。站台可見度 `site.visibility` 為 `authenticated`（預設）時，未登入的請求除了 `/auth/*`、`GET /api/v1/auth/providers`、`POST /api/v1/takedowns`、`GET /api/v1/takedowns/:id`、`/healthz`、`/readyz` 之外一律 401 `unauthorized`「請先登入 Hub。」；為 `public` 時，未登入的請求只有 `public` 內容的 `read`，需要其他權限的路由仍然回 401。 |
| HACC-R14 | 單一內容的可見度：`public`、`authenticated`、`group:<name>`、`private`（只有上傳者與 `moderate`）；有效可見度不能超過站台上限，`site.visibility: authenticated` 時 `public` 的版本視為 `authenticated`。看不到的版本與不存在的版本一律回 404 `not_found`「找不到這個版本。」，列表中也不出現。各種產出的預設可見度在 `content_defaults`：`frames` 預設 `private`，其他預設跟隨站台。 |
| HACC-R15 | 下架申請：`POST /api/v1/takedowns {version_id, reason, statement, contact_email, good_faith}` 不需要登入；`reason` 為 `copyright`、`privacy`、`other`；`statement` 20～5,000 字元；`good_faith` 必須是 `true`；不符合時 422 並以 `errors[].path` 指出欄位。成功回 201 `{id, state: "open", message: "已收到下架申請，編號 <id>。管理員處理後會在這裡更新狀態。"}`。`contact_email` 以信封加密存放，只有 `moderate` 看得到。同一個 IP 每小時最多 5 筆（`rate_limits.takedowns_per_hour`），超過回 429 `rate_limited`。 |
| HACC-R16 | 下架決定：`POST /api/v1/admin/takedowns/:id/decision {decision, note}`（`moderate`）。`decision: remove` 時版本變成 `removed`：`GET /api/v1/versions/:id` 與下載回 410 `removed`「這個版本已依下架申請移除。」，沒有其他版本引用的 blob 刪除，`(video, sha256)` 記入 `removed_hashes`（HREP-R9）；上傳者在 `GET /api/v1/me/shares` 看到該版本 `state: removed` 與原因。`decision: reject` 時申請變成 `rejected`，版本不變。已經決定的申請再決定回 409 `takedown_decided`。 |
| HACC-R17 | 停用帳號：`POST /api/v1/admin/users/:id/suspend {reason}`（`admin`）後，這個帳號的 session 與 PAT 一律回 403 `account_suspended`「這個帳號已被停用。」，投票不再計入分數（HREP-R13）；`…/unsuspend` 恢復。所有 `admin`、`moderate` 動作與登入方式的連結、移除都寫入稽核紀錄，`GET /api/v1/admin/audit`（`admin`）依時間倒序列出 `{entries: [{actor, action, target_kind, target_id, at}]}`；`action` 為 `identity.link`、`identity.unlink`、`group.add`、`group.remove`、`user.suspend`、`user.unsuspend`、`takedown.decide`、`report.decide`、`version.hide` 之一，`actor` 是執行者的 `display_name`。 |
| HACC-R18 | 一般限流：每個帳號（session 與 PAT 合計）每分鐘 600 個請求，未登入的請求每個 IP 每分鐘 60 個（`rate_limits.requests_per_minute`、`anonymous_requests_per_minute`）；超過回 429 `rate_limited`，附 `retry_after_s` 與 `Retry-After` 標頭，訊息「請求太頻繁，請在 <n> 秒後再試。」。 |

## 介面

- 設定檔 `/config/notesteme-hub.yaml`（只能寫在設定檔）：`public_url`、`oauth.<provider>.client_id`、`site.visibility`、`content_defaults`、`groups`（權限與自動規則）、`admins`、`rate_limits`。環境變數：`NOTESTEME_HUB_OAUTH_<PROVIDER>_CLIENT_SECRET`、`NOTESTEME_HUB_MASTER_KEY`（信封加密，同 SEC-R4）、`NOTESTEME_HUB_SESSION_KEY`（session 金鑰，見 A10）。
- 瀏覽器流程：`GET /auth/<provider>/start`、`GET /auth/<provider>/callback`。
- API：

  | 路由 | 權限 |
  |---|---|
  | `GET /api/v1/auth/providers` | 不需要登入 |
  | `POST /api/v1/auth/logout` | session |
  | `GET /api/v1/me`（`{id, display_name, identities, groups, permissions}`） | 已登入或 PAT |
  | `POST /api/v1/me/identities/<provider>/link`、`DELETE /api/v1/me/identities/<provider>` | session（PAT 不能連結登入方式） |
  | `GET/POST/DELETE /api/v1/me/tokens` | session |
  | `GET /api/v1/me/shares` | `upload` |
  | `POST /api/v1/takedowns`、`GET /api/v1/takedowns/:id`（只回 `{id, state}`） | 不需要登入 |
  | `GET /api/v1/admin/takedowns`、`POST /api/v1/admin/takedowns/:id/decision` | `moderate` |
  | `GET /api/v1/admin/reports`、`POST /api/v1/admin/reports/:id/decision`、`POST /api/v1/admin/versions/:id/hide` | `moderate` |
  | `GET /api/v1/admin/users`、`PUT/DELETE /api/v1/admin/users/:id/groups/:group`、`POST /api/v1/admin/users/:id/suspend`、`…/unsuspend`、`GET /api/v1/admin/audit` | `admin` |
  | 內容的路由 | 見 [HREP](HREP-hub-repository.md) 與 [HMCP](HMCP-hub-mcp.md) |

- CLI：`notesteme-hub admin grant|revoke --provider <p> --subject <s>`、`notesteme-hub admin suspend <user-id>`（直接操作 DB，給第一位管理員與救援）。
- Hub UI（`web/hub`，`@ui @manual`）：登入頁（列出已啟用的登入方式）、帳號頁（登入方式的連結與移除、PAT 的建立與撤銷、我的分享）、管理頁（使用者與群組、檢舉、下架申請、稽核紀錄）、下架申請表單（不需要登入）。頁面清單與文案在 `docs/ui/README.md` 第 9 節補上之前，以本規格為準。
- 資料表：`users`、`identities`、`oauth_states`、`sessions`、`pats`、`groups`、`group_members`、`takedowns`、`removed_hashes`、`audit_log`（ADR-0031 第 5 點）。

## 不在範圍內

- 合併兩個已經存在的帳號、刪除帳號（之後另立規格）。
- MCP 的 OAuth 授權流程（ADR-0012：之後再評估，M5 只用 PAT）。
- 寄送 email 通知（下架與檢舉的結果只在 Hub 上顯示）。
- 多個 Hub 程序共用限流（ADR-0031 第 5 點）。

## 待決問題

- A1：GitHub 與 Linux.do 是否真的接受 PKCE。決定（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：Hub 一律送 S256 challenge 與 verifier（HACC-R2、R3），假供應商驗證 verifier；供應商忽略 PKCE 時 state 與 client secret 仍然有效，實作 child 對照官方文件後在這裡記錄實際支援的程度。Google 的 endpoint 主機名稱寫在 `fakeoauth` 的常數中，實作時對照官方文件。
  - M5-S1 對照的結果（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：GitHub 自 2025-07-14 起正式支援 PKCE（只接受 S256，verifier 至少 43 個字元；目前不強制，但一旦送出 challenge 就必須在換 token 時送出相符的 verifier，見 GitHub Changelog「PKCE support for OAuth and GitHub App authentication」與 docs.github.com 的 Authorizing OAuth apps）。Linux.do Connect 的官方 wiki 沒有找到 PKCE 的說明，社群的接入範例（ASP.NET 的 `UsePkce = true`、sub2api 的 `code_challenge`）把它當成選用；採保守作法照樣送出，供應商忽略時 state 與 client secret 仍然有效，Linux.do 的 token 請求以 HTTP Basic 送 client 憑證（同官方範例），GitHub、Google 放在 form。Google 的 endpoint 與 OIDC discovery 文件一致：`accounts.google.com/o/oauth2/v2/auth`、`oauth2.googleapis.com/token`、`openidconnect.googleapis.com/v1/userinfo`、`www.googleapis.com/oauth2/v3/certs`，`iss` 接受 `https://accounts.google.com` 與 `accounts.google.com`；寫死在 `internal/hub/hubauth/providers.go`，與 `fakeoauth.Hosts` 相同。
- A2：不依 email 合併（HACC-R5）。代價是使用者用兩個登入方式各登入一次會得到兩個帳號；合併與刪除帳號延到 M5 之後。決定同上，待 owner 追認。
- A3：連結登入方式要在 10 分鐘內登入過（HACC-R6）。數字依常見的「重新驗證」做法暫定；場景用 fake clock 驗證。
- A4：內建群組的預設權限（HACC-R10）：`members` 沒有 `regenerate` 與 `upload`，新帳號要靠自動規則（例如 Linux.do trust level ≥ 2）或手動指派才能上傳與讓 Client 取用原始字幕。保守預設，降低剛建立的帳號灌入垃圾內容的風險。
- A5：下架申請不需要登入（HACC-R15）。版權人不一定有帳號；用 IP 限流與 `good_faith` 聲明擋濫用。沒有 email 通知，申請人要記下編號回來看（`GET /api/v1/takedowns/:id` 只回 `{id, state}`，不需要登入）。
- A6：限流數字（HACC-R18、HREP-R11、R13、R14）都是暫定值，寫在設定檔可以調整；場景用調低的值觸發。
- A7：M5-S0（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：Hub 設定檔（`internal/hub/hubconfig`）接受本規格、HREP、HMCP 與 ADR-0030 列出的欄位，另外加 `listen`（預設 `127.0.0.1:8090`）與 `data`（預設 `/data`），環境變數 `NOTESTEME_HUB_CONFIG`、`NOTESTEME_HUB_LISTEN`、`NOTESTEME_HUB_DATA`、`NOTESTEME_HUB_STORE_DSN`（Postgres 的 DSN 含密碼，只來自環境變數）、`NOTESTEME_HUB_MASTER_KEY`、`NOTESTEME_HUB_OAUTH_<PROVIDER>_CLIENT_SECRET`。驗證：`public_url` 必填且是 https（「設定 public_url 必須是 https 網址。」）；`oauth.<provider>.client_id` 留空代表停用；`admins` 群組的權限固定 `[admin]`；`members`、`moderators`、`admins` 不能有 `rules`；內建群組只寫 `rules` 時沿用預設權限，自訂群組必須列出 `permissions`；每條規則剛好一個與 provider 相符的條件（`trust_level_gte` 為 0～4）；`content_defaults` 的 key 只能是 `transcript`、`chapters`、`note`、`frames`，值可以是 `group:<存在的群組>`；大小寫成 `"1KiB"` 這類字串或整數，時間長度寫成 `"168h"`；未知欄位一律失敗。session 金鑰的環境變數名稱留給 S1。
- A8：M5-S0（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：Hub 的 migration 00001 只建 `users` 與 `identities`（ADR-0031 第 5 點的欄位，另加 `UNIQUE (user_id, provider)` 落實 HACC-R6 的「每個帳號每個供應商最多一個身分」），其他資料表由各切片加 migration。
- A9：M5-S0（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：`/api/v1` 的路由在 HACC-S1／S2 之前都是 501 `not_implemented`，**不做身分驗證**（還沒有任何身分可以通過）；每條 stub 在 `internal/hub/hubapi/stubs.go` 記錄需要的存取層級（`public`、`session`、`authenticated` 或一個權限），OpenAPI 的 operation 以 `x-notesteme-access` 公開。S2 的權限 middleware 依這張表在讀取 body 之前檢查（HACC-R13）。`/auth/<provider>/start|callback` 與 `/mcp` 是不經過 Huma 的 501 handler。`notesteme-hub admin grant|revoke|suspend` 只檢查參數，結束碼 1「尚未實作」。另外，ADR-0032 規定 `GET /api/v1/openapi.json` 公開，但 HACC-R13 的免登入例外清單沒有它：採 ADR-0032，Huma 提供的 `/api/v1/openapi.json`、`.yaml`、`-3.0.json`、`-3.0.yaml`（`hubapi.PublicDocPaths`）一律不需要登入，S2 的 middleware 要放行。
- A10：session 金鑰（M5-S1，2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：環境變數 `NOTESTEME_HUB_SESSION_KEY`，至少 32 個字元（建議 `openssl rand -base64 32`），只用來以 HKDF-SHA256 推導加密等待中登入的 PKCE verifier 的金鑰（XChaCha20-Poly1305，AAD 綁定 state 的 hash 與供應商）；session 與 CSRF 不依賴它（session token 是隨機值，DB 只存 SHA-256；CSRF 由 session token 推導，同 AUTH-R5，與 Client 共用 `internal/websession`）。有啟用登入方式時必填：沒有時 `notesteme-hub serve` 啟動失敗、`doctor` 列為問題；太短時設定載入失敗，訊息不帶金鑰內容。換金鑰只會讓 10 分鐘內等待中的登入失效（`oauth_state_invalid`），已登入的 session 不受影響。不做 ephemeral 的自動產生：重新啟動後等待中的登入會無聲失敗，而且多個程序之間不一致。
- A11：M5-S1 的實作範圍與細節（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：
  - 身分驗證的 middleware 在 S1 加入（Huma middleware，依每個 operation 的 `x-notesteme-access`，在讀取 body 之前）：`public` 直接放行；其他層級沒有有效 session 時回 401 `unauthorized`「請先登入 Hub。」，`site.visibility: public` 時未登入的請求可以進到需要 `read` 的路由；帳號停用時回 403 `account_suspended`；非 GET 的 session 請求先檢查 CSRF（403 `csrf_invalid`），所以還沒實作的路由（例如 `POST /api/v1/me/tokens`）也一樣先擋。缺少權限時的 403 `forbidden`、PAT 與限流（HACC-R9、R13、R18）是 S2；在那之前任何 `Authorization` 標頭都回 401。沒有宣告存取層級的 operation 視為需要登入（預設拒絕）；`/healthz`、`/readyz` 宣告為 `public`，`hubapi.PublicDocPaths` 不是 Huma operation，不經過 middleware（A9）。
  - `/me` 的 `groups` 在 S1 只有 `members`，`permissions` 是設定檔中 `members` 的權限（依 `read`、`download`、`regenerate`、`upload`、`moderate`、`admin` 的順序；有 `admin` 時列出全部）；設定檔的 `admins`、手動指派與自動規則（HACC-R10～R12）由 S2 加入。供應商的 `trust_level`、`orgs` 在每次登入時存進 `identities.attrs_json`，給 S2 的規則使用；GitHub 只有設定檔用到 `org_member` 時才要 `read:org` 並讀取 `/user/orgs`。
  - 連結與移除登入方式在 S1 就寫入稽核紀錄（migration 00002 的 `audit_log`，`identity.link`、`identity.unlink`，`target_kind: identity`）；`GET /api/v1/admin/audit` 是 S2／S5。
  - 身分：Google 的 `login` 是 email（沒有時用 name），`display_name` 取 userinfo 的 `name`；`sub`、`email`、`email_verified` 以簽章驗證過的 `id_token` 為準，userinfo 的 `sub` 必須相同，否則 502。GitHub、Linux.do 的 `login` 是 `login`／`username`，subject 是數字 `id`（不是正整數時 502）。再次登入時更新 `login`、`email` 與屬性，`display_name` 不變。`display_name` 去掉控制字元與格式字元，結果是空的時用「<供應商> 使用者」。
  - callback：先比對 state 與 cookie、再以原子操作取用 `oauth_states`（`UPDATE … WHERE used_at IS NULL AND expires_at > now RETURNING`），之後才處理 `error=access_denied` 與換 token，所以被拒絕或失敗的 state 也不能再用；`error` 不是 `access_denied` 或沒有 `code` 時回 502 `oauth_provider_error`。連結的 state 只能由發起連結的帳號（目前的 session）完成，否則 `oauth_state_invalid`。成功後轉到固定的 `/`（登入）或 `/account`（連結），callback 的其他參數不影響（不做 open redirect）；callback 的錯誤目前回 JSON（Hub UI 的頁面是 S9），並清掉 `notesteme_hub_oauth` cookie、帶 `Referrer-Policy: no-referrer`。登入一律建立新的 session，同一個瀏覽器原本的 session 作廢（輪替）；停用的帳號登入時回 403 `account_suspended`，不建立 session。
  - 對供應商的請求走 `netguard` 的 client（SSRF 防護），另外只接受 https、不跟隨轉址、回應上限 1 MiB、一次 callback 合計 10 秒；token 回應接受 JSON 與 form-encoded，沒有 `access_token`（GitHub 的「200 加 `error`」）視為失敗。log 只記錄失敗的步驟與原因（`error` 參數只保留英數與 `_-`），不帶授權碼、token、client secret 或回應內容。JWKS 每個 Hub 實例各自快取 1 小時，遇到未知的 `kid` 時重新取得，但兩次至少間隔 1 分鐘；`id_token` 只接受 RS256、2048 bits 以上的金鑰。
  - 其他：`DELETE /api/v1/me/identities/<provider>` 移除沒有連結的登入方式時回 404 `not_found`「這個帳號沒有連結這個登入方式。」；`POST …/link` 先檢查 10 分鐘的重新驗證，再檢查是否已經連結同一個供應商（callback 時資料庫的 `UNIQUE (user_id, provider)` 再擋一次）；路徑的 `provider` 不是三者之一時由 Huma 回 422。session cookie `Path=/`、`Max-Age` 30 天，CSRF cookie 不是 `HttpOnly`（前端要讀取），兩者 `Secure`、`SameSite=Lax`；`POST /api/v1/auth/logout` 回 204 並清除兩個 cookie。等待中登入的紀錄在每次寫入時清掉已過期的列；在 S2 的限流之前，`/auth/<provider>/start` 沒有次數限制。
  - 驗收：真 Hub 的閘門 `Given a Hub is running` 只對登入範圍的 HACC 場景放行（至少一個 `@HACC-R1`～`R8`，而且沒有 R1～R8、R10 以外的 HACC 規則，`features/steps/hub.go` 的 `hubGate`）；其他 HACC、HREP、HMCP 場景停在同一個閘門，由各切片放寬。
- A12：M5-S2 的實作範圍與細節（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：
  - **Hub token 打到只接受 session 的路由**（規格沒寫）：回 403 `session_required`「這個操作只能在 Hub 網頁登入後進行，不能使用 Hub token。」（token 本身有效，所以不是 401；同 AUTH 待決問題中 Client 改密碼的路由對 token 回 403 的先例）。受影響的是介面表中標「session」的路由：登出、連結與移除登入方式、`GET/POST/DELETE /api/v1/me/tokens`。
  - middleware 的順序（在讀取 body 之前）：有 `Authorization` 標頭時只看 Hub token、不退回 cookie（scheme 必須是 `Bearer`，不分大小寫；格式不是 `nsh_` 加 40 個 base62、不存在、撤銷、過期都回 401 `unauthorized`）→ 帳號停用 403 `account_suspended`（session 與 PAT 都是，S5 只要加停用路由）→ `session_required` → session 的非 GET 請求檢查 CSRF（PAT 不需要）→ 一般限流 → 權限（403 `forbidden`「你沒有這個操作需要的權限（<權限>）。」，帶 `required_permission`）。
  - Hub token：`expires_at` 是建立時間加 `expires_in_days × 24h`，**到期的那一刻起失效**（`now < expires_at` 才有效）。驗證錯誤都是 422 並以 `errors[].path` 指到欄位：`name` 去掉控制字元後 1～100 字元、`permissions` 不能是空的、不認得的權限與 `expires_in_days` 超出 1～365 是 `invalid_request`；帳號當下沒有的權限是 `permission_not_held`，`errors` 列出每一個沒有的項目（`admin` 也一樣：只有管理員能建立帶 `admin` 的 token）。重複的權限合併。欄位的範圍刻意不寫進 OpenAPI 的 schema，否則 Huma 的驗證只會填 `details`。`GET /api/v1/me/tokens` 列出未撤銷的 token（含已過期的），只有 `{id, name, permissions, created_at, expires_at}`；撤銷別人的或不存在的 token 回 404 `not_found`，撤銷兩次回 204。用 Hub token 呼叫 `GET /api/v1/me` 時 `permissions` 是這個 token 的有效權限（PAT ∩ 帳號），`groups` 仍是帳號的群組。有效權限每個請求重新計算，不快取。
  - 群組：`members` 是隱含的（不存資料列）；`group_members` 以 `(user_id, group_name, source, rule_provider)` 為主鍵，同一個群組可以同時有 `manual`、`rule`（記錄是哪個供應商的規則給的）與 `config` 的成員資格，有效權限取聯集。設定檔已經沒有的群組，資料列保留但不列出、不計入權限。`/me` 的 `groups` 依 `members`、`contributors`、`moderators`、`admins`、其他群組（依名稱）排列。
  - 自動規則：以供應商 P 登入（或連結 P 的身分）成功後、建立 session 之前，只重算 P 的規則給的成員資格，其他供應商的規則與 `manual`、`config` 不動。`org_member` 的組織名稱不分大小寫；`email_domain` 取最後一個 `@` 之後的部分，完全相等（不分大小寫，子網域不算），而且要 `email_verified: true`；`trust_level_gte` 在供應商沒有回傳 trust level 時不成立。
  - 設定檔的 `admins`：身分登入時以 `source: config` 加入 `admins`；設定檔只在啟動時載入，所以 Hub **啟動時**依設定檔同步全部 `source: config` 的列（從設定檔移除的管理員重新啟動後立即失去權限，列出但還沒登入過的身分在第一次登入時加入）。之後的 `notesteme-hub admin grant`（還沒實作，仍是結束碼 1）要用另一個 source（例如 `cli`，migration 的 CHECK 要一起放寬），才不會被啟動時的同步清掉。
  - 指派群組：`PUT/DELETE /api/v1/admin/users/:id/groups/:group` 先檢查 `admins`（403 `admin_by_config`，不論使用者存不存在），`members` 回 422 `invalid_request`「每個帳號都在 members 群組，不能指派或移除。」，設定檔沒有的群組回 404「找不到這個群組。」、不存在的使用者回 404「找不到這個使用者。」；成功一律 204（重複指派、移除沒有的成員資格也是），`DELETE` 只移除 `manual` 的成員資格。只有資料真的改變時寫入稽核紀錄（`group.add`、`group.remove`，`target_kind: user`、`target_id` 是使用者 ID）。`GET /api/v1/admin/audit` 接受 `limit`（1～500，預設 100）。`GET /api/v1/admin/users` 還是 stub（S5 或 Hub UI 的切片）。
  - 一般限流（HACC-R18）：固定的一分鐘視窗（對齊 clock 的整分鐘），計數只在程序的記憶體中；帳號的 session 與 PAT 合計一個額度，未登入（含帶了無效 token 或 session 的請求）以來源 IP 計，所以猜 token 也受限；`/auth/<provider>/start|callback` 與公開的 API 路由以 IP 計，`/healthz`、`/readyz` 不限。`retry_after_s` 與 `Retry-After` 是到視窗結束的秒數（無條件進位，至少 1）。IP 取自連線的位址，**不信任 `X-Forwarded-For`**：放在反向代理後面時所有未登入的請求共用一個 IP 的額度，之後要加信任代理的設定（另立決定）。
  - 已知限制（待 owner 決定是否在啟動時依 `identities.attrs_json` 重算規則）：自動規則依 HACC-R11 的字面只在以該供應商登入時重算；設定檔的規則收緊或刪除並重新啟動之後，靠規則取得權限的帳號在下一次用那個供應商登入之前仍保有權限，而 Hub token 不會觸發登入，所以最長可能殘留到 token 到期（365 天）。設定檔的 `admins` 沒有這個問題（啟動時同步）。
  - 驗收：真 Hub 的閘門放行至少一個已實作的 HACC 規則（R1～R13、R18）而且沒有其他 HACC 規則、沒有 `@HREP-`、`@HMCP-`、`@HSHR-` tag 的場景；用到 4.31 句型（建立版本、bundle 等）的場景（例如 R13 的「Nothing but sign-in…」）仍停在閘門，等 HREP 的切片。`An admin appoints a moderator` 會放行，停在最後一步 `GET /api/v1/admin/takedowns`（S5 的 stub，501 → pending）。`Given the hub member … is in the group …` 直接寫 DB，不寫稽核紀錄。
- A13：M5-S5 的實作範圍與細節（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：
  - **下架申請（HACC-R15）**：不檢查版本存不存在、申請人看不看得到（預設站台是 `authenticated`，版權人不一定有帳號）。`version_id` 只要是 UUID，任何版本都回相同的 201，申請不能用來探測版本是否存在；版主決定 `remove` 時版本不存在才回 422 `invalid_request`「申請的版本不存在，只能駁回（reject）。」。欄位錯誤依 body 的順序（`version_id`、`reason`、`statement`、`contact_email`、`good_faith`）全部列出：`statement` 去掉頭尾空白後 20～5,000 個字元、除了換行與 tab 不能有控制字元；`contact_email` 只接受單純的位址（沒有顯示名稱，最多 254 個字元）。`GET /api/v1/takedowns/:id` 只回 `{id, state}`，不存在時 404「找不到這個下架申請。」。
  - **下架申請的限流**：以連線的 IP 計（不信任 `X-Forwarded-For`，同 A12），只計成功的申請，在驗證 body 之前預留額度、失敗時歸還；滑動的一小時，計數只在記憶體中（**不保存任何 IP 衍生的資料**，Hub 重新啟動後歸零），429 附 `retry_after_s` 與 `Retry-After`。
  - **主金鑰與信封加密**：`NOTESTEME_HUB_MASTER_KEY` 至少 32 個字元（太短時設定載入失敗，訊息不帶金鑰）；`notesteme-hub serve` 沒有它就不啟動、`doctor` 列為問題，`migrate` 與 `admin` 不需要；萬一沒有（程序內的 Hub）時 `POST /api/v1/takedowns` 回 503 `not_configured`。主金鑰以 HKDF-SHA256（info `notesteme-hub takedown contact v1`）推導 KEK，每筆申請一把隨機的資料金鑰（XChaCha20-Poly1305），email 以資料金鑰加密、資料金鑰以 KEK 加密，兩層的 AAD 都綁定申請 ID。解不開（換了主金鑰、資料被竄改）時版主的列表回 `contact_email: null`，不回 500。**已知限制**：不支援換主金鑰（換了之後舊申請的 email 都讀不到），之後要換時另立決定。
  - **下架決定（HACC-R16）**：`POST /api/v1/admin/takedowns/:id/decision {decision, note}` 回 200 `{id, state}`；`note` 最多 2,000 個字元。以條件式的 UPDATE（`WHERE state = 'open'`）判斷是否已決定，並發的第二個決定也是 409 `takedown_decided`。`remove` 在同一個交易中把版本變成 `removed`（記下移除的來源與理由）、`(video, sha256)` 記入 `removed_hashes`（帶申請 ID）、版本未處理的檢舉結案（`actioned`）；交易提交之後刪掉不再被任何**未移除**的版本引用的 blob（`withdrawn` 的版本仍算引用：再分享相同內容時會用回它）。之後別的影片上傳相同內容時，分享會依檔案是否存在把 blob 補回來。**已知限制**：刪檔與同時進行的分享之間沒有鎖（極少見的競爭可能讓新分享的 blob 被刪，之後下載回 500，重新分享即可恢復）。
  - **版主的列表**：`GET /api/v1/admin/takedowns` 與 `GET /api/v1/admin/reports` 接受 `state`（各自的狀態）與 `limit`（1～500，預設 100），依建立時間倒序，沒有 cursor；422 的 `errors[].path` 是 `query.state`、`query.limit`。檢舉的列表有檢舉者 `{id, display_name}`（只有 `moderate` 看得到，HREP-R14）。
  - **停用與恢復（HACC-R17）**：`POST /api/v1/admin/users/:id/suspend {reason}` 回 200 `{id, suspended: true}`；`reason` 必填、1～500 個字元；不能停用自己（422）；不存在的使用者 404「找不到這個使用者。」；已經停用時仍回 200，但不寫稽核紀錄。**停用期間保留 session 與 Hub token**，middleware 對它們回 403 `account_suspended`（場景要求 403 而不是 401）；`…/unsuspend` 回 200 `{id, suspended: false}`，同時刪掉停用前的 session、撤銷停用前的 Hub token，使用者要重新登入、重新建立 token（「連帶失效」落在恢復的時候）。停用與恢復都在同一個交易中重算這個帳號投過票的版本的分數（HREP-R13）。
  - **稽核紀錄**：`user.suspend`、`user.unsuspend`（`target_kind: user`）、`takedown.decide`（`takedown`）、`report.decide`（`report`）、`version.hide`（`version`），只在資料真的改變時寫入；檢舉造成的自動隱藏不是版主的動作，不寫入。命令列的動作 `actor_user_id` 是 NULL、`actor` 顯示「notesteme-hub admin」（migration 00005 重建 `audit_log`）。
  - **`notesteme-hub admin`**：`grant`、`revoke` 以 `source: cli` 把 `(provider, subject)` 的帳號加入或移出 `admins`（migration 00005 重建 `group_members` 放寬 CHECK；啟動時依設定檔的同步不會清掉它；`revoke` 只移除 `cli` 的資格，設定檔的 `admins` 要改設定檔）。身分要先登入過 Hub 一次，否則結束碼 1「找不到 … 的身分 …：請先用它登入 Hub 一次，或改寫在設定檔的 admins。」（還沒登入過的第一位管理員請用設定檔）。`suspend <user-id>` 的原因固定是「由 notesteme-hub admin 停用」。另外加了規格沒有的 **`admin unsuspend <user-id>`**：所有管理員都被停用時的救援。命令直接開資料庫（`hub.OpenStore`，會套用 migration），不需要 session 金鑰與主金鑰。
  - **`GET /api/v1/admin/users`**（`admin`）：依建立順序列出 `{id, display_name, created_at, groups, identities: [{provider, login}], suspended_at, suspended_reason}`，`limit` 1～500（預設 100），沒有 cursor。
  - **`GET /api/v1/me/shares`**（`upload`）：依建立時間倒序列出自己的分享（含已撤回的）`{id, video, created_at, withdrawn_at, versions}`，每個版本是列表的欄位加上 `visibility`、`state`，被移除時另有 `removal: {source: takedown|report, reason}`（申請或檢舉的理由；版主的說明不公開，可能含申請人的資訊）。`hidden` 的版本也列出。
  - **權限的兩層檢查**：middleware 依 `x-notesteme-access` 在讀取 body 之前檢查（HACC-R13），`moderate`、`admin` 與 `me/shares` 的 handler 再以 `requirePermission` 確認一次（註冊時 Access 寫錯也不會放行）。store 不知道設定檔的群組，無法判斷權限；store 層的防線是條件式的 UPDATE（已處理的決定不會被覆寫）與執行者必須是存在的帳號（foreign key）。
  - 驗收：`hubRules` 加 R15～R17，`hrepRules` 加 R13～R15；`Given the hub member … is suspended` 直接寫 DB（不寫稽核紀錄，分數照樣重算）；建立下架申請與檢舉回 201 時以回應的 `id` 記下 `{takedown:last}`、`{report:last}`。
- A14：安全修補 A（2026-11-24，依 /goal 採 advisor 建議，待 owner 追認）：
  - **無效的 token（HACC-R9）**：帶了 `Authorization` 標頭而驗證失敗（不是 `Bearer`、格式錯誤、不存在、撤銷、過期）時一律 401 `unauthorized`，`site.visibility: public` 也不退回匿名（原本的實作退回匿名，與 A12 的順序不符）；只有沒帶標頭的請求在公開站台以匿名讀取。過期或無效的 session cookie 仍視為沒有登入（瀏覽器留著舊 cookie 的訪客不應被擋）。
  - **停用管理員**：持有 `admin` 的帳號（設定檔、CLI 或任何帶 `admin` 的群組）不能由 `POST /api/v1/admin/users/:id/suspend` 停用，回 403 `admin_by_config`「管理員只能用 notesteme-hub admin suspend 停用。」；要停用管理員時先把他移出 `admins`（設定檔或 `admin revoke`），或用 CLI。原因：管理員由設定檔指定（ADR-0031 第 2 點），API 不能讓一位管理員鎖住其他管理員。
  - **cookie 前綴（HACC-R2、R4、R8）**：三個 cookie 改名為 `__Host-notesteme_hub_oauth`、`__Host-notesteme_hub_session`、`__Host-notesteme_hub_csrf`，等待中登入的 cookie 的 `Path` 由 `/auth` 改為 `/`（`__Host-` 要求 `Secure`、`Path=/`、沒有 `Domain`）；R2、R4、R8 中的名稱與路徑以這裡為準。原因：沒有前綴時，同一個註冊網域下的子網域（或中間人改寫的 http 回應）可以寫入 Hub 網域的 cookie，把受害者登入成攻擊者的帳號（login CSRF）或固定 CSRF cookie。Hub 的 cookie 本來就一律 `Secure`，主流瀏覽器把 `http://localhost` 視為安全來源、接受 `Secure` 與 `__Host-` 的 cookie，所以本機開發不需要無前綴的退路（Client 依 `X-Forwarded-Proto` 決定 `Secure` 的慣例不適用於 Hub）。升級後既有的 session 都要重新登入一次；不讀舊名稱的 cookie（讀了就等於保留漏洞）。等待中登入的 cookie 送到每個路徑，但它是 `HttpOnly`、10 分鐘失效。驗收場景以子字串比對 cookie 名稱，不需修改。

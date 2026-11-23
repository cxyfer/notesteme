# AUTH：登入與 token

- 狀態：ready
- 里程碑：M1（密碼登入、session、CSRF、token 與 scope、CLI 建立 token）、M2（MCP HTTP 的 Origin 檢查、UI 管理 token）、M4（反向代理驗證、OIDC、工作階段列表、token 輪替與管理 UI：AUTH-R12～R22）
- 相關：plan 第 19 節；ADR-0014；UI [登入](../ui/pages.md#13-登入)、[設定](../ui/pages.md#12-設定)
- 驗收場景：[`features/AUTH-access.feature`](../../features/AUTH-access.feature)；M4：[`features/AUTH-proxy-oidc.feature`](../../features/AUTH-proxy-oidc.feature)

## 目的

從 M1 起，沒有登入或有效 token 就不能存取任何資料與操作；Agent 用的 token 只拿得到它需要的權限。

## User stories

| ID | Story | 里程碑 |
|---|---|---|
| AUTH-01 | As the owner, I want every instance to require a password, so that nobody else can read my library or spend my API credits. | M1 |
| AUTH-02 | As the owner, I want repeated wrong passwords to be slowed down, so that the password cannot be guessed. | M1 |
| AUTH-03 | As the owner, I want tokens limited to specific scopes, so that a leaked agent token cannot change settings or start paid work. | M1 |
| AUTH-04 | As the owner, I want to revoke a token or let it expire, so that access I no longer need stops working. | M1 |
| AUTH-05 | As the owner, I want the MCP endpoint to reject requests from unexpected web pages, so that a malicious site cannot drive my instance through my browser. | M2 |
| AUTH-06 | As the owner, I want to sign in through my reverse proxy's authentication, so that I reuse the login I already have. | M4 |
| AUTH-07 | As the owner, I want to sign in with OIDC, so that I can use my identity provider. | M4 |
| AUTH-08 | As the owner, I want to create, revoke and set expiry for tokens in the UI, so that I do not need the CLI for it. | M4 |

## 規則

| ID | 規則 | 里程碑 |
|---|---|---|
| AUTH-R1 | `/api/*` 與 `/mcp` 都需要有效的 session 或 bearer token；例外只有 `POST /api/auth/login`、`/healthz`、`/readyz`。未驗證的請求回傳 401。 | M1 |
| AUTH-R2 | 第一次啟動時，以環境變數 `NOTESTEME_PASSWORD` 設定登入密碼；DB 只存 argon2id hash。之後修改密碼要透過設定頁或 `notesteme password set`。 | M1 |
| AUTH-R3 | 還沒有密碼、也沒有 `NOTESTEME_PASSWORD` 時，`serve` 拒絕啟動，訊息為「尚未設定登入密碼。請設定 NOTESTEME_PASSWORD 後重新啟動。」只有加上 `--insecure-no-auth` 才能在沒有密碼的情況下啟動，並在 log 與 UI 頂部持續顯示警告。 | M1 |
| AUTH-R4 | session cookie 設 `HttpOnly`、`SameSite=Lax`，經 HTTPS 存取時加上 `Secure`；閒置 30 天失效。登出後 session 立即失效。 | M1 |
| AUTH-R5 | 用 session 送出的非 GET 請求必須帶 CSRF token，否則回傳 403。用 bearer token 的請求不需要 CSRF token。 | M1 |
| AUTH-R6 | 同一個來源位址連續 5 次密碼錯誤後，該位址鎖定 1 分鐘；鎖定期間連正確的密碼也拒絕，回傳 429，回應包含剩餘秒數。其他位址不受影響。錯誤訊息一律為「密碼不正確。」，不透露其他資訊。來源位址取自連線；連線來自設定檔 `trusted_proxies` 中的位址時，改用 `X-Forwarded-For` 中最右邊一個不在 `trusted_proxies` 內的位址。 | M1 |
| AUTH-R7 | token 建立時只顯示一次完整內容，DB 只存 hash。每個 token 有名稱、scope、可選的到期時間。 | M1 |
| AUTH-R8 | 每個 API 與 MCP tool 都宣告需要的 scope（見 plan 第 16、19 節）。token 缺少需要的 scope 時回傳 403，並在回應中說明需要哪個 scope。session 擁有全部權限。 | M1 |
| AUTH-R9 | 撤銷或過期的 token 立即失效，回傳 401。 | M1 |
| AUTH-R10 | M1 用 `notesteme tokens create --name <n> --scopes <s> [--expires <d>]` 建立 token；M2 起也可以在設定頁建立。 | M1 |
| AUTH-R11 | MCP HTTP 檢查 `Origin`：有 `Origin` 標頭且不在允許清單中時回傳 403；同時啟用 DNS rebinding 防護（`Host` 必須是設定的主機名稱）。 | M2 |
| AUTH-R12 | 反向代理驗證只在設定檔有 `auth.proxy.user_header` 時啟用。啟用時 `trusted_proxies` 與 `auth.proxy.allowed_users`（字串清單，完全相等、區分大小寫）都不能是空的，否則 `serve` 拒絕啟動，訊息「設定了 auth.proxy.user_header，但 trusted_proxies 或 auth.proxy.allowed_users 是空的。」；`user_header` 不能是 `Authorization`、`Cookie`、`Host`、`Origin`、`X-Forwarded-For`、`X-Forwarded-Host`、`X-Forwarded-Proto`、`X-CSRF-Token`（不分大小寫），否則拒絕啟動，訊息「auth.proxy.user_header 不能使用 <名稱>。」。 | M4 |
| AUTH-R13 | 只信任 **TCP 對端位址**（連線本身的來源，不是 AUTH-R6 從 `X-Forwarded-For` 推出來的位址）在 `trusted_proxies` 中的請求所帶的 `user_header`。其他來源送來的同名標頭一律忽略（請求視為沒有這個標頭，照一般規則驗證，沒有其他憑證就是 401），並寫一筆 warn log「忽略來自不受信任位址 <IP> 的 <標頭> 標頭。」（不記錄標頭的值；同一個位址每分鐘最多一筆）。標頭出現多次、值超過 256 bytes 或含控制字元時同樣忽略並記 log。 | M4 |
| AUTH-R14 | 來自受信任 proxy、值在 `allowed_users` 中的請求視為 owner：第一次時建立一個 `method: proxy` 的 session，設定與密碼登入相同的 `notesteme_session`、`notesteme_csrf` cookie（AUTH-R4）。這個 session 只在之後的請求仍然來自受信任 proxy、標頭的值相同時有效；標頭消失或換成別的值時，這個 session 立即撤銷，請求照一般規則驗證。值不在 `allowed_users` 中時回 403 `proxy_user_not_allowed`，訊息「這個帳號（<值>）不能使用 Notesteme。」（值最多顯示 64 字元），不建立 session。沒有帶標頭時照一般規則驗證，密碼登入照常可用。 | M4 |
| AUTH-R15 | 反向代理驗證不改變其他規則：用 proxy 身分送出的非 GET 請求仍要帶 CSRF token（AUTH-R5，值取自 AUTH-R14 設定的 `notesteme_csrf`），否則 403；`/mcp` 不接受 proxy 身分，只帶標頭時回 401（AUTH-R1、R11）；帶 bearer token 的請求忽略 `user_header`，權限完全依 token 的 scope（AUTH-R8）。 | M4 |
| AUTH-R16 | OIDC 只在設定檔有 `auth.oidc.issuer` 時啟用，同時需要 `client_id`、`redirect_url`、`allowed_subjects`（非空），client secret 只能來自環境變數 `NOTESTEME_OIDC_CLIENT_SECRET`。缺少時 `serve` 拒絕啟動，訊息分別為「auth.oidc 缺少 <鍵>。」、「已設定 auth.oidc，但沒有設定環境變數 NOTESTEME_OIDC_CLIENT_SECRET。」；設定檔中出現 `auth.oidc.client_secret` 時拒絕啟動，訊息「auth.oidc 的 client secret 只能用環境變數 NOTESTEME_OIDC_CLIENT_SECRET 設定。」。`issuer` 必須是 `https://`。啟用時 `GET /api/auth/oidc/start` 與 `GET /api/auth/oidc/callback` 加進 AUTH-R1 的免驗證例外；沒有啟用時這兩條路由回 404。對 IdP 的請求（discovery、JWKS、token endpoint）逾時 10 秒、回應上限 1 MiB、不跟隨轉址；discovery 與 JWKS 快取 1 小時，遇到不認得的 `kid` 時重新取得 JWKS（每分鐘最多一次）。 | M4 |
| AUTH-R17 | `GET /api/auth/oidc/start[?next=<路徑>]` 回 302 到 discovery 的 `authorization_endpoint`，參數為 `response_type=code`、`response_mode=query`、`client_id`、`redirect_uri`（`redirect_url`）、`scope=openid`、`state`、`nonce`（各 32 bytes 隨機值的 base64url）、`code_challenge`（S256）、`code_challenge_method=S256`。state、nonce、code verifier、`next` 存在 server 端，10 分鐘後失效、只能用一次；同時設定 cookie `notesteme_oidc`（`HttpOnly`、`SameSite=Lax`、HTTPS 時 `Secure`、`Path=/api/auth/oidc`）綁定這次登入。`next` 只接受以單一 `/` 開頭、第二個字元不是 `/` 或 `\`、不含 `\` 與控制字元、最多 2048 bytes 的相對路徑，其他一律換成 `/`（與 ENTRY-R6 的 `next` 共用同一個檢查函式）。 | M4 |
| AUTH-R18 | `GET /api/auth/oidc/callback`：`state` 缺少、不認得、過期、已用過，或與 `notesteme_oidc` cookie 不符時回 400 `oidc_state_invalid`，訊息「登入請求已失效，請重新登入。」；IdP 回傳 `error` 時回 401 `oidc_failed`，訊息「身分提供者拒絕了登入（<error>）。」（`<error>` 只保留 `[a-z_]`、最多 64 字元，其他寫成 `unknown`）。code 以 `client_secret_basic` 與 code verifier 換 token，id_token 必須：`alg` 是 `RS256` 或 `ES256`（`none` 與 `HS*` 一律拒絕）、`kid` 在 JWKS 中、簽章正確、`iss` 與 `issuer` 完全相同、`aud` 含 `client_id`（有多個 `aud` 時 `azp` 必須是 `client_id`）、`nonce` 與這次登入相同、`exp` 晚於現在減 60 秒、`iat` 早於現在加 60 秒（時鐘偏差容許 60 秒）。任何一項不通過回 401 `oidc_failed`，訊息「無法驗證身分提供者的回應，請重新登入。」，log 記錄是哪一項（不記錄 code、token 與 client secret）。 | M4 |
| AUTH-R19 | id_token 的 `sub` 必須完全等於 `allowed_subjects` 中的一項，否則回 403 `oidc_not_allowed`，訊息「這個帳號不能使用 Notesteme。」，不建立 session。通過時建立 `method: oidc` 的 session（cookie、閒置期限、CSRF 與密碼登入相同，AUTH-R4、R5），記錄 `oidc_identities (issuer, subject, last_login_at)`，回 302 到 AUTH-R17 保存的 `next`。log、事件與回應都不含 code、access token、id_token、client secret。 | M4 |
| AUTH-R20 | 工作階段列表：`GET /api/auth/sessions`（`admin`；session 擁有全部權限）回 `{sessions: [{id, method, created_at, last_seen_at, ip, user_agent, current}]}`，依 `last_seen_at` 由新到舊；不回傳 session 的 secret 或 hash。`last_seen_at` 最多每分鐘更新一次。`DELETE /api/auth/sessions/:id`（`admin`）撤銷該 session，之後用它的請求立即回 401；`:id` 為 `current` 時撤銷送出請求的這個 session，並清除 cookie（等同登出）。找不到時回 404。 | M4 |
| AUTH-R21 | token 輪替：`POST /api/tokens/:id/rotate`（`admin`；`:id` 是 token ID 或有效的名稱，同 `DELETE /api/tokens/:id`）產生新的 secret，名稱、scope、`expires_at` 不變，回應格式與建立時相同（新 token 只顯示這一次，AUTH-R7）；舊的 secret 在同一個交易中失效，之後使用回 401（AUTH-R9）。已撤銷或過期的 token 回 409 `token_inactive`，訊息「這個 token 已撤銷或過期，請建立新的 token。」。CLI：`notesteme tokens rotate <name> [--json]`。 | M4 |
| AUTH-R22 | 設定頁「MCP 與 Agent」分區的 token 管理（AUTH-08）：列表顯示名稱、前綴、scope、建立時間、最後使用時間、到期時間與狀態（有效、已過期、已撤銷）；「建立 token」選擇 scope 與到期時間（7 天、30 天、90 天〔預設〕、1 年、不設到期，對應 `expires_in`），建立後只顯示一次完整內容並提供複製；「撤銷」與「輪替」都要再確認一次，訊息分別為「撤銷後，使用這個 token 的程式會立即失去存取權。」、「輪替後，舊的 token 會立即失效，請更新使用它的程式。」。「登入」分區列出 AUTH-R20 的工作階段，可以撤銷目前以外的任一個。 | M4 |

## 介面

- API：`POST /api/auth/login`、`POST /api/auth/logout`、`GET/POST/DELETE /api/tokens`
- CLI：`notesteme tokens create|ls|revoke`、`notesteme password set`
- 環境變數：`NOTESTEME_PASSWORD`
- 設定檔：`trusted_proxies`（CIDR 清單，預設為空；放在 Caddy 等反向代理後面時填入 proxy 的位址）
- 設定檔（AUTH-R11，M2）：`mcp.allowed_hosts`（主機名稱清單，不含 port；IP 位址與 `localhost` 一律接受，用網域名稱存取 `/mcp` 時要列出）、`mcp.allowed_origins`（`scheme://host[:port]` 清單；同源一律接受）。只檢查 TCP listener；本機 socket 與獨立模式不檢查。檢查在驗證之後：沒有有效憑證先回 401。
- Session 與 CSRF：登入成功後設定 `notesteme_session`（`HttpOnly`、`SameSite=Lax`）與 `notesteme_csrf` 兩個 cookie。`notesteme_csrf` 可以由前端讀取；非 GET 請求把它的值放在 `X-CSRF-Token` 標頭（double-submit）。
- Bearer token：`Authorization: Bearer <token>`。

### M4（AUTH-R12～R22）

- AUTH-R1 的免驗證例外在 M4 由 AUTH-R16 擴充（OIDC 啟用時的 `start` 與 `callback`）；ENTRY-R6 讓 `POST /api/auth/login` 多接受 `next`。ENTRY 的頁面路由不在 `/api` 底下：`/login`、`/manifest.webmanifest`、`/sw.js` 不需要登入，`/add`、`/share` 只認 session cookie（沒有時 303 到 `/login?next=`），見 ENTRY 規格。`next` 的檢查是 `auth.SafeNext`（ENTRY 待決問題 E8），AUTH-R17 的 OIDC 與反向代理驗證登入後也要用它。
- API：`GET /api/auth/oidc/start`、`GET /api/auth/oidc/callback`（OIDC 啟用時免驗證）；`GET /api/auth/sessions`、`DELETE /api/auth/sessions/:id`（`admin`）；`POST /api/tokens/:id/rotate`（`admin`）。
- API（AUTH 切片新增，見待決問題 A9）：`GET /api/auth/methods`（免驗證）回 `{oidc: {enabled, host?}}`，給登入頁決定要不要顯示 OIDC 按鈕。
- CLI：`notesteme tokens rotate <name> [--json]`。
- 環境變數：`NOTESTEME_OIDC_CLIENT_SECRET`。
- 設定檔（只能寫在設定檔，不開放 API 修改）：
  - `auth.proxy.user_header`（例如 `Remote-User`）、`auth.proxy.allowed_users`（字串清單）；信任的來源沿用 `trusted_proxies`。
  - `auth.oidc.issuer`、`auth.oidc.client_id`、`auth.oidc.redirect_url`（例如 `https://notes.example.com/api/auth/oidc/callback`）、`auth.oidc.allowed_subjects`（字串清單）。
- 資料表方向（ADR-0021 第 7 點）：`sessions` 加 `method`（`password`、`proxy`、`oidc`）、`ip`、`user_agent`、`last_seen_at`，proxy session 另記 `proxy_user`；`oidc_identities (issuer, subject, last_login_at)`；`oidc_logins (state_hash, nonce, code_verifier, next, expires_at)`（用過即刪除）。
- 對 IdP 的請求不經過 netguard 的私有網段封鎖：`issuer` 只能寫在設定檔，自架的 IdP 常在內網（見待決問題 A3）。
- UI：登入頁在 OIDC 啟用時多一個「用 <issuer 的主機名稱> 登入」按鈕（連到 `/api/auth/oidc/start?next=<目前的頁面>`）；設定頁見 AUTH-R22。

## 不在範圍內

- 反向代理驗證、OIDC 與工作階段列表：M4 加入，見 AUTH-R12～R20。M4 仍不做的部分見下方「M4」小節。
- 多使用者（Client 是單一使用者）。

### M4（AUTH-R12～R22）

- 同時設定多個 OIDC provider，或同時啟用多個 proxy 標頭。
- RP-initiated logout、back-channel／front-channel logout：登出只清除 Notesteme 的 session，不通知 IdP。
- refresh token 與 access token 的使用：Notesteme 只用 id_token 確認身分，之後就是一般的 session。
- 從 UI 或 API 設定反向代理驗證與 OIDC（只能寫在設定檔，AUTH-R12、R16）。
- SAML、LDAP、WebAuthn／passkey。
- `/mcp` 以 proxy 或 OIDC 身分存取（AUTH-R15；MCP 一律用 bearer token）。
- token 的權限細到單一影音或單一 tool（scope 仍是 AUTH-R8 的四種）。

## 待決問題

- session 閒置期限 30 天是否太長，M1 期間依實際使用調整。
- M2 的路由 scope（2026-11-21，M2 S0 的 501 stub 決定，待 owner 確認）：讀取 profiles、prompts、ass-styles、glossaries、channel overrides、term candidates 用 `read`（MCP-R6 讓 `read` 的 token 列出 prompt，這些物件也不含 secret）；寫入用 `admin`；`POST /api/prompts/playground`、`POST /api/prompts/:id/compare`、`POST /api/items/:id/subtitles/import`、`POST /api/items|tasks/:id/switch-executor` 用 `ingest`（會花錢或觸發重新處理）；`/mcp` 只要求有效身分，scope 依 tool 判斷；`GET /api/settings/:section` 維持 `admin`。S0 的 stub 在讀取 body 之前檢查 scope，所以缺憑證回 401、缺 scope 回 403，不會被 501 遮住。決定（2026-11-22，依 /goal 採 advisor 建議，待 owner 追認）：讀取設定物件（profiles、prompts、ass-styles、glossaries、channel overrides、term candidates）維持 `read`，`GET /api/settings/:section` 維持 `admin`；寫入一律 `admin`；`POST /api/ass-styles/preview` 會執行 ffmpeg，改用 `ingest`。
- 本機模式的 bearer token（2026-11-23，M2 S9 實作 `notesteme mcp --token` 時決定，依 /goal 採保守預設，**待 owner 追認**）：本機 socket 與獨立模式（`api.NewLocal`）原本不驗證身分、擁有全部權限；請求帶 `Authorization: Bearer` 時改用該 token 的身分（只會縮小權限），無效或過期的 token 回 401。CLI 只有 `notesteme mcp --token` 會在本機模式送出 token，一般指令不送，行為不變。細節見 MCP 規格待決問題 S4。
- `config export`／`import` 的路由 scope（2026-11-23，依 /goal 採保守預設，待 owner 追認）：`GET /api/config/export` 用 `read`（內容只有處理方案，與 `GET /api/profiles` 相同，不含 secrets）；`POST /api/config/import` 用 `admin`（會建立與取代處理方案、改預設處理方案）。scope 在讀取本體之前檢查。

- 設定頁修改密碼的 API（2026-11-23，UI-3b 依 /goal 採 advisor 建議，**待 owner 追認**）：AUTH-R2 要求「修改密碼要透過設定頁」，但介面沒有列出路由，`notesteme password set` 也還沒實作，所以新增 `POST /api/auth/password`（`{current_password, new_password}`）。只有 owner（session 或本機），token 即使有 `admin` 也回 403；session 要帶 CSRF。目前的密碼錯誤回 422 `invalid_password`「密碼不正確。」（不用 401，避免前端當成 session 失效），與登入**共用**同一套來源位址鎖定（AUTH-R6），鎖定時回 429 `login_locked` 與 `retry_after_s`；新密碼只要求非空（與 `NOTESTEME_PASSWORD` 一致），空的回 422 `invalid_request`。成功後在同一個交易中寫入新的 hash 並撤銷目前以外的所有 session（本機呼叫沒有 session，全部撤銷）。沒有 `.feature` 場景，由 `internal/auth`、`internal/api` 的單元測試守住；CLI 的 `password set` 之後可以呼叫同一個 `auth.Service.ChangePassword`。

### M4（AUTH-R12～R22）

- A1：OIDC 的 callback 與 start 加進 AUTH-R1 的免驗證例外（AUTH-R16）。AUTH-R1 只列了 login、healthz、readyz；決定（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：只有 OIDC 啟用時才開放，沒有啟用時回 404，不改 AUTH-R1 的文字。
- A2：設定了反向代理驗證或 OIDC 時，仍然要求登入密碼（AUTH-R3 不變）。密碼是 IdP 或 proxy 故障時的後備登入方式，也讓 `--insecure-no-auth` 的意義保持單純。決定（2026-11-23，依 /goal 採保守預設，待 owner 追認）。
- A3：對 IdP 的請求不經過 netguard 的私有網段封鎖（介面 M4 小節）。`issuer` 只能寫在設定檔，和 vault 的 git remote 一樣是 owner 自己的設定，不是不可信的輸入；仍然只接受 `https://`、限制逾時與大小、不跟隨轉址。之後如果開放 UI 設定 OIDC，要改走 netguard。
- A4：時鐘偏差 60 秒（AUTH-R18）。常見的 IdP 與函式庫預設 1～5 分鐘；個人實例通常有 NTP，60 秒已經足夠，越小越能限制被截取的 id_token 的重放時間（state 與 nonce 只能用一次，重放本來就會被擋下）。
- A5：proxy session 綁定標頭的值（AUTH-R14）。只靠 cookie 的話，proxy 登出之後 Notesteme 的 session 還能用 30 天；綁定之後，proxy 那邊登出（標頭消失）就同時失效。
- A6：`/mcp` 不接受 proxy 身分（AUTH-R15）。MCP client 不是瀏覽器，不會經過 proxy 的登入；接受的話，任何能經過 proxy 的瀏覽器分頁都能驅動 MCP，等於繞過 AUTH-R11 的 Origin 檢查。
- A7：token 輪替保留原本的 `expires_at`（AUTH-R21），不重新起算。重新起算等於用輪替延長期限，違反「到期就失效」的預期；需要更長的期限時建立新的 token。
- A8：AUTH-R10 的「M2 起也可以在設定頁建立」在 M2 沒有 UI（UI 切片另外排程）；AUTH-R22 是 token 管理 UI 的完整規格，API 沿用 AUTH-R7～R10 的 `GET/POST/DELETE /api/tokens`。
- M4-S0：`auth.proxy.*`、`auth.oidc.*` 與 `NOTESTEME_OIDC_CLIENT_SECRET` 已解析與驗證，AUTH-R12、R16 的啟動失敗訊息照規則（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）；功能本身沒有啟用（設定了也只是被接受）。另外補了規則沒有寫的檢查：`allowed_users`、`allowed_subjects` 不能有空字串，只寫了 `auth.oidc` 的其他鍵而沒有 `issuer` 時回「auth.oidc 缺少 issuer。」，`redirect_url` 必須是 http 或 https 的網址。`GET /api/auth/oidc/start`、`callback` 不註冊 stub：AUTH-R16 規定沒有啟用時回 404，S0 不啟用 OIDC；但 AUTH-R1 的免驗證例外還沒加，沒有身分的請求在 S0 先回 401。`GET /api/auth/sessions`、`DELETE /api/auth/sessions/:id`、`POST /api/tokens/:id/rotate` 是 501 stub（`admin`）。

#### AUTH 切片的實作決定（M4，AUTH-R12～R22）

以下皆為 （2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）。

- A9：登入頁要知道 OIDC 是否啟用與 issuer 的主機名稱，新增免驗證的 `GET /api/auth/methods`（只回 `oidc.enabled` 與 `oidc.host`，與登入頁本來就會顯示的按鈕文字相同）。這擴大了 AUTH-R1 的免驗證例外；免驗證的路徑只有 GET 與三條精確的路徑（`/api/auth/oidc/start`、`/api/auth/oidc/callback`、`/api/auth/methods`），`/api/auth/oidc/` 底下的其他路徑與非 GET 仍要驗證。OIDC 沒有啟用時 start、callback 仍是註冊的路由，回 404 `not_found`（OpenAPI 才有文件）。
- A10：`/mcp` 也不接受 OIDC 的 session（A6 的延伸，「不在範圍內」寫了 `/mcp` 不以 proxy 或 OIDC 身分存取）；密碼登入的 session 維持 AUTH-R1 的行為。proxy session 送到 `/mcp` 回 401，但**不撤銷**（`/mcp` 不判斷標頭，不能把「沒有判斷」當成「標頭消失」）。
- A11：受信任的 proxy 帶來的值不在 `allowed_users` 中時一律 403，即使請求同時帶著有效的密碼或 OIDC session（proxy 斷言的是另一個人）；值在清單中而請求已經帶著有效的 session（任何方式）時沿用那個 session，不另外建立 proxy session。設定檔關掉反向代理驗證之後，既有的 proxy session 在下一次使用時撤銷。
- A12：標頭出現多次、超過 256 bytes、含 C0／C1 控制字元或不是合法 UTF-8 時，log 為「忽略來自 <IP> 的 <標頭> 標頭：標頭出現多次、過長或含控制字元。」（不記錄值，同一個位址每分鐘最多一筆，節流表最多 1024 個位址）。判斷之後從請求中移除這個標頭，之後的 handler 看不到原始值；本機 socket 不做反向代理驗證。
- A13：OIDC 的 start 是免驗證又會寫入 DB 的端點：寫入前先刪除過期的登入，未完成的登入超過 1000 筆時回 429 `oidc_failed`；discovery 失敗回 502 `oidc_failed`「無法連線到身分提供者，請稍後再試。」。對 IdP 的 HTTP client 走 netguard 的 Transport，只把 issuer 的主機名稱加進私有網段的允許清單——**與 A3 和介面 M4 小節的「不經過 netguard 的私有網段封鎖」不同，比 A3 嚴格**（自架在內網的 IdP 仍然可用）：雲端 metadata 位址仍然被擋下，discovery 中其他主機的 endpoint 照 GRD 的一般規則。
- A14：callback 先原子地取出並刪除 state（`DELETE … RETURNING`），之後才比對 cookie、到期時間、IdP 的 `error`，最後換 token；所以 cookie 不符、IdP 拒絕、換 token 失敗之後，同一個 state 都不能再用。`notesteme_oidc` 只在成功時清除，失敗時 10 分鐘後自然失效。
- A15：id_token 的細節：`kid`、`exp`、`iat`、`sub` 必填；`exp` 必須嚴格晚於「現在減 60 秒」、`iat` 必須嚴格早於「現在加 60 秒」；只有一個 `aud` 但帶了 `azp` 時，`azp` 也必須是 `client_id`；JWK 有 `alg` 時必須與 header 相同，沒有時以金鑰種類判斷（RS256 只配 RSA ≥ 2048 bits、ES256 只配 P-256 且點在曲線上）；同一個 `kid` 出現多次時不採用；`exp`、`iat` 只接受 JSON 數字（字串不算）。實作在新的 `internal/oidc`，Hub 的 `internal/idtoken`（只接受 RS256）不變。
- A16：`client_secret_basic` 依 RFC 6749 §2.3.1，client_id 與 secret 先以 form-urlencoded 編碼再做 Basic。
- A17：token 輪替是原地更新（同一個 `id`，`last_used_at` 清空）；`:id` 先比對 ID（任何狀態），再比對有效的名稱；只有已撤銷或已過期的同名 token 時回 409 `token_inactive`（不是 404），完全找不到才是 404。
- A18：`DELETE /api/auth/sessions/<自己的 id>` 與 `current` 一樣清除 cookie；token 與本機呼叫沒有 session，`current` 回 404。
- A19：`last_seen_at` 每分鐘最多更新一次，與 AUTH-R4 的閒置期限延長合併成同一次寫入（活躍的 session 每分鐘最多一次寫入）。
- A20（**安全預設，請 owner 特別確認**）：`X-Forwarded-Proto: https` 仍然接受任何來源（AUTH-R4 的 `Secure`），不限 `trusted_proxies`。偽造它只會讓 cookie 多一個 `Secure`（更嚴格，不是漏洞）；改成只信任 `trusted_proxies` 的話，沒有設定 `trusted_proxies` 的反向代理部署就會失去 `Secure`。反向代理驗證的標頭（AUTH-R13）與來源位址（AUTH-R6）都只信任 TCP 對端在 `trusted_proxies` 中的請求。
- A21：稽核紀錄沒有另外建表：建立 proxy／OIDC session、撤銷 session、輪替 token、OIDC 登入失敗的原因都寫 log（只記 session／token 的 ID、來源位址與失敗原因，不記標頭值、code、token、client secret）。
- A22：驗收步驟的 `redirect_url` 用設定的 listen 位址當 base URL（實際的 port 在啟動後才知道）；callback 的步驟只取 IdP 轉回網址的 path 與 query，送到實際的位址（steps.md 4.21 的補充）。


#### `--insecure-no-auth` 的實作決定（後端補洞切片，AUTH-R3）

以下皆為 （2026-11-24，依 /goal 採 advisor 建議，待 owner 追認）。

- A23 啟動條件：AUTH-R3 只寫了「加上 `--insecure-no-auth` 才能在沒有密碼時啟動」。另外加上（依序檢查，`vps` 部署的 OPS-R21 在最前面）：（1）設定的 `listen` 必須只綁本機：host 是 `localhost` 或 loopback 的 IP 字面值（`127.0.0.0/8`、`::1`），不做 DNS 解析，空的 host（所有介面）、`0.0.0.0`、`::`、IPv4-mapped 與帶 zone 的位址一律拒絕，訊息「--insecure-no-auth 只能在只監聽本機（127.0.0.1、::1 或 localhost）時使用，目前的 listen 是 "<值>"。」；（2）設定了 `trusted_proxies`、`auth.proxy` 或 `auth.oidc` 時拒絕（代表會經由其他主機存取），訊息「--insecure-no-auth 不能與 trusted_proxies、auth.proxy 或 auth.oidc 一起使用。」；（3）已經有密碼時拒絕，不默默停用 owner 設好的驗證：設定了 `NOTESTEME_PASSWORD` 時訊息「設定了 NOTESTEME_PASSWORD，不能同時使用 --insecure-no-auth。請移除其中一個。」（在開啟 DB 之前）；DB 已經有密碼 hash 時訊息「已經設定了登入密碼，不能使用 --insecure-no-auth。請移除這個旗標，用密碼登入。」。無驗證模式不寫入任何密碼。
- A24 身分：TCP listener 上沒有憑證的請求一律是 owner（全部權限，身分種類 `insecure`，與本機 socket 的 `local` 分開，之後只給本機 socket 的能力不會被它繼承）；帶 bearer token 時改用 token 的身分（只會縮小權限，無效的 token 回 401），與本機 socket 相同。session cookie 不使用；`POST /api/auth/login` 與 `POST /api/auth/password` 回 409 `no_password`（訊息同 AUTH-R3 的啟動訊息），不是 500。ENTRY 的 `/add`、`/share` 視為已登入（不導回登入頁）。本機 socket 的行為不變。
- A25 瀏覽器防護：沒有 session 就沒有 CSRF token（AUTH-R5），所以在所有路徑（含 Web UI、`/mcp`）之前檢查：`Host` 必須是 `localhost`、loopback 的 IP（可帶 port），否則 403 `insecure_host`（擋 DNS rebinding）；會改變狀態的請求帶了 `Origin` 時必須與 `Host` 同源（`null`、多個值、帶路徑或帳號的都拒絕），`Sec-Fetch-Site` 只接受 `same-origin` 與 `none`，否則 403 `csrf_invalid`（擋其他網站、包含同一台電腦其他 port 的網頁）。不帶這兩個標頭的程式（curl、CLI）照常可用。偽造的 `X-Forwarded-For`、反向代理的使用者標頭沒有作用（啟動條件已排除 `trusted_proxies`）。
- A26 警告：log 在啟動時寫一筆 warn（固定文字「目前以 --insecure-no-auth 啟動：沒有任何登入驗證……」，listen 另外記錄），之後每小時再寫一次（「持續顯示」的 log 部分）。UI 從免驗證的 `GET /api/auth/methods` 多出的 `insecure_no_auth`（布林，一般模式是 `false`）得知，所有頁面頂部顯示警告橫幅（`role="alert"`，三語）；沒有 CSRF cookie 時不導到登入頁，登入頁在無驗證模式直接回到要去的頁面。沒有 `.feature` 場景，由 `internal/app`、`internal/api` 的單元測試與 Web UI 的元件測試守住。

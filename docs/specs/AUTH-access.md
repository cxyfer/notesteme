# AUTH：登入與 token

- 狀態：ready
- 里程碑：M1（密碼登入、session、CSRF、token 與 scope、CLI 建立 token）、M2（MCP HTTP 的 Origin 檢查、UI 管理 token）
- 相關：plan 第 19 節；ADR-0014；UI [登入](../ui/pages.md#13-登入)、[設定](../ui/pages.md#12-設定)
- 驗收場景：[`features/AUTH-access.feature`](../../features/AUTH-access.feature)

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

## 介面

- API：`POST /api/auth/login`、`POST /api/auth/logout`、`GET/POST/DELETE /api/tokens`
- CLI：`notesteme tokens create|ls|revoke`、`notesteme password set`
- 環境變數：`NOTESTEME_PASSWORD`
- 設定檔：`trusted_proxies`（CIDR 清單，預設為空；放在 Caddy 等反向代理後面時填入 proxy 的位址）
- Session 與 CSRF：登入成功後設定 `notesteme_session`（`HttpOnly`、`SameSite=Lax`）與 `notesteme_csrf` 兩個 cookie。`notesteme_csrf` 可以由前端讀取；非 GET 請求把它的值放在 `X-CSRF-Token` 標頭（double-submit）。
- Bearer token：`Authorization: Bearer <token>`。

## 不在範圍內

- 反向代理驗證、OIDC 與工作階段列表（M4）。
- 多使用者（Client 是單一使用者）。

## 待決問題

- session 閒置期限 30 天是否太長，M1 期間依實際使用調整。
- M2 的路由 scope（2026-11-21，M2 S0 的 501 stub 決定，待 owner 確認）：讀取 profiles、prompts、ass-styles、glossaries、channel overrides、term candidates 用 `read`（MCP-R6 讓 `read` 的 token 列出 prompt，這些物件也不含 secret）；寫入用 `admin`；`POST /api/prompts/playground`、`POST /api/prompts/:id/compare`、`POST /api/items/:id/subtitles/import`、`POST /api/items|tasks/:id/switch-executor` 用 `ingest`（會花錢或觸發重新處理）；`/mcp` 只要求有效身分，scope 依 tool 判斷；`GET /api/settings/:section` 維持 `admin`。S0 的 stub 在讀取 body 之前檢查 scope，所以缺憑證回 401、缺 scope 回 403，不會被 501 遮住。需要 owner 決定的是：讀取設定物件用 `read` 是否可以接受，還是要比照 settings 用 `admin`。

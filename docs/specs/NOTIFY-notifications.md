# NOTIFY：通知

- 狀態：ready
- 里程碑：M4
- 相關：plan 第 15、18.1、21 節；ADR-0014（netguard）、[ADR-0021](../adr/0021-m4-scheduler-subscriptions-and-outbox.md)（第 1、5 點）；[SEC](SEC-config-secrets.md)、[GRD](GRD-guards.md)、[DIG](DIG-digest.md)、[OPS](OPS-operations.md)（OPS-R11～R21）；UI [設定](../ui/pages.md#12-設定)（通知）、[總覽](../ui/pages.md#1-總覽)
- 驗收場景：[`features/NOTIFY-notifications.feature`](../../features/NOTIFY-notifications.feature)

## 目的

無人值守時，owner 要在自己常用的地方（手機的 ntfy、Discord、Telegram，或自己的自動化）知道結果與問題。通知事件先寫進 outbox，由排程送出：端點暫時失敗會重送，不會因為通知失敗而影響處理流程。通知會經過第三方伺服器，所以內容一律不含 secrets 與字幕全文，端點的憑證加密存放，對外請求一律經過 netguard。

## User stories

| ID | Story |
|---|---|
| NOTIFY-01 | As the owner, I want notifications through ntfy, Discord, Telegram or a webhook, so that I hear about results where I already am. |

## 規則

| ID | 規則 |
|---|---|
| NOTIFY-R1 | `/api/notifiers` 的所有路由都需要 scope `admin`（token 缺少時 403，AUTH-R8）。notifier 是 `{id, kind, config, events, enabled}`：`id` 符合 `^[a-z0-9][a-z0-9-]{0,39}$`，重複時 409 `notifier_exists`；`kind` 是 `ntfy`、`discord`、`telegram`、`webhook`；`enabled` 預設 `true`；最多 20 個（超過時 409 `too_many_notifiers`）。`POST` 回 201，`PATCH` 只更新給定的欄位，`DELETE` 回 204 並刪除這個 notifier 還沒送出的 outbox 項目。 |
| NOTIFY-R2 | 各種類的 `config` 與送出的請求（欄位錯誤時 422，`errors[].path` 指到該欄位，例如 `config.webhook_url`）：**ntfy** `{server, topic, token?, priority?}`：`server` 預設 `https://ntfy.sh`，`topic` 符合 `^[A-Za-z0-9_-]{1,64}$`，`priority` 1～5（預設 3）；送 `POST <server>/`，JSON `{topic, title, message, priority, click?}`，有 `token` 時加 `Authorization: Bearer <token>`。**discord** `{webhook_url}`：必須是 `https://discord.com/api/webhooks/<數字>/<token>`（或 `discordapp.com`），或主機列在設定檔 `notify.allow_hosts`，否則 422「Discord webhook 只能指向 discord.com，或設定檔 notify.allow_hosts 中列出的主機。」；送 `POST <webhook_url>?wait=true`，JSON `{content, allowed_mentions: {parse: []}}`。**telegram** `{bot_token, chat_id, api_base?}`：`bot_token` 符合 `^\d+:[A-Za-z0-9_-]{30,}$`；`api_base` 預設 `https://api.telegram.org`，其他值的主機必須列在 `notify.allow_hosts`，否則 422「Telegram 只能連到 api.telegram.org，或設定檔 notify.allow_hosts 中列出的主機。」；送 `POST <api_base>/bot<bot_token>/sendMessage`，JSON `{chat_id, text, link_preview_options: {is_disabled: true}}`，不帶 `parse_mode`（純文字）。**webhook** `{url, secret}`：`url` 是 `https://`（`http://` 只在主機列於 `network.allow_private` 時接受），`secret` 16～256 字元；送 `POST <url>`，JSON 見 NOTIFY-R4。 |
| NOTIFY-R3 | 事件：`item.done`（一次執行的所有步驟都是 `done` 或 `skipped`；以 `until` 提前結束的執行不算，所以 `until: transcript` 的 MCP 新增與測試的 seeding 不會通知）、`item.failed`（步驟 `failed` 且這次執行結束）、`item.blocked`（步驟 `blocked`，原因 `needs_action` 或 `budget_blocked`；`awaiting_user` 不算，交給 DIG 的彙整）、`budget.warning`（COST）、`digest`（DIG）、`alert`（OPS-R13～R18 的無人值守告警與 OPS-R12 的備份失敗）、`source.better_available`（SRC-10）。`events` 是 1～7 個不重複的事件名稱，不認得時 422，`errors[].path` 為 `events.<索引>`，訊息「不認得的事件：<值>。」。事件發生時，在**同一個交易**中為每個 `enabled` 而且 `events` 包含它的 notifier 寫入一筆 `notification_outbox`（ADR-0021 第 5 點）；沒有符合的 notifier 時不寫入。 |
| NOTIFY-R4 | webhook 的 body 是 JSON：`{id, event, created_at, instance, item?, stage?, error_class?, message?, digest?, items?, alert?}`；`id` 是 outbox 項目的 id（重送時不變），`instance` 是 `server.public_url`（沒有設定時 `null`），`item` 是 `{id, ref, title, channel, url}`（`url` 是 UI 連結，沒有 `public_url` 時 `null`）。標頭：`Content-Type: application/json`、`User-Agent: Notesteme/<版本>`、`X-Notesteme-Event`、`X-Notesteme-Delivery`（同 `id`）、`X-Notesteme-Timestamp`、`X-Notesteme-Signature`。文字管道（ntfy、Discord、Telegram）的標題依事件為「完成：<標題>」「失敗：<標題>」「需要你處理：<標題>」「Notesteme 告警：<摘要>」「Notesteme 已恢復：<摘要>」「有更好的字幕來源：<標題>」，內文是 UI 的狀態訊息（`失敗`、`需要你處理` 時是那個步驟的 `message` 與動作），有 `public_url` 時附上 UI 連結。 |
| NOTIFY-R5 | webhook 簽章：`X-Notesteme-Timestamp` 是送出時的 Unix 秒數，`X-Notesteme-Signature` 是 `sha256=` 加上 `hex(HMAC-SHA256(secret, <timestamp> + "." + <原始 body>))`。每次嘗試都用當下的時間重新簽章，body 不變。文件建議接收端拒絕與自己時間相差超過 5 分鐘的請求，並以 `X-Notesteme-Delivery` 去重。 |
| NOTIFY-R6 | 密鑰（ntfy 的 `token`、Discord 的 `webhook_url`、Telegram 的 `bot_token`、webhook 的 `url` 與 `secret`）以 SEC 的信封加密存放在 `notifiers.config_ciphertext`（SEC-R4）；DB、log、備份、事件與 outbox 的 `payload_json`、`last_error` 中都不會出現明文。API 回應只回傳非機密欄位與 `<欄位>_last4`（例如 `bot_token_last4`、`webhook_url_last4`；webhook 另外回傳 `url_host`），永遠不回傳明文。`PATCH` 沒有給的密鑰欄位保留原值。 |
| NOTIFY-R7 | 所有通知請求（含測試通知）都經過 netguard 的 client（GRD-R1～R5，ADR-0014）：連到內部網路時錯誤類別 `permanent`，訊息「這個網址指向內部網路，已封鎖。」（DNS rebinding 同樣以實際連線的 IP 判斷）；**不跟隨轉址**，回應 3xx 時為 `permanent`，訊息「通知端點回應了轉址，不跟隨。」；連線逾時 10 秒、整體逾時 15 秒；請求 body 上限 64 KiB；回應只讀前 64 KiB（內容只用來判斷成功與否，不記錄）。 |
| NOTIFY-R8 | 送出：排程工作 `notify.deliver` 每分鐘處理 `next_attempt_at <= now` 的 `pending` 項目。2xx 為 `sent`（記錄 `sent_at`）。連線錯誤、逾時、408、429、5xx 是可重試的失敗，依 1 分鐘、5 分鐘、30 分鐘、2 小時、6 小時退避；429 有 `Retry-After`（或 Telegram 的 `parameters.retry_after`）而且比退避長時採用它（最多 6 小時）。第 6 次失敗後標記 `dropped`。netguard 封鎖、3xx 與其他 4xx 是 `permanent`，第一次失敗就 `dropped`，不重試。`last_error` 是「通知端點回應 HTTP 503。」「連線失敗。」這類固定格式的訊息。標記 `dropped` 時寫入事件 `notify.dropped`（總覽顯示），這個事件**不會**再以通知送出。`GET /api/notifications`（scope `admin`）回傳最近 100 筆 `{id, notifier, event, state, attempts, last_error, created_at, sent_at}`，可以用 `?state=` 篩選；`sent` 與 `dropped` 的項目保留 30 天。 |
| NOTIFY-R9 | 內容清理：標題移除控制字元、把連續空白合併成一個、去掉頭尾空白，超過 100 字時保留前 99 字並加「…」。Discord 的內容在標題、摘要等不可信文字中跳脫 Markdown 字元（`\ * _ ~ \` \| > [ ] ( )`），而且一律帶 `allowed_mentions: {parse: []}`，`@everyone` 不會通知任何人；Telegram 不使用 `parse_mode`；ntfy 不開 Markdown。內文上限：ntfy 4000 bytes、Discord 2000 字、Telegram 4096 字、webhook 64 KiB；超過時在行的邊界截斷並加「…（內容過長，已截斷）」。通知**不含**：任何 secret（API key、cookie、token、notifier 的密鑰）、字幕 cue 的文字、筆記內容、外部工具的原始輸出；錯誤只用 UI 的狀態訊息（已經過遮蔽）。 |
| NOTIFY-R10 | 去重：每個事件有 `dedupe_key`；同一個 notifier 在 `notify.dedupe_window`（預設 `24h`，可設 `1m`～`30d`）內已有同一個 `dedupe_key` 的項目（任何狀態）時不再寫入。鍵：`item.done:<影音 id>:<執行 id>`、`item.failed:<影音 id>:<步驟>`、`item.blocked:<影音 id>:<步驟>:<原因>`、`budget.warning:<YYYY-MM>`、`digest:<彙整 id>:<排定的執行時間>`、`alert:<種類>:<對象>[:resolved]`、`source.better_available:<影音 id>:<來源>`。 |
| NOTIFY-R11 | 速率上限：每個 notifier 在任何 10 分鐘內最多送出 `notify.rate_limit.count`（預設 20）則（`notify.rate_limit.per` 預設 `10m`）；超過的項目保持 `pending`，`next_attempt_at` 延到有空位的時間，不計入嘗試次數，也不會 `dropped`。 |
| NOTIFY-R12 | 測試通知：`POST /api/notifiers/:id/test`（scope `admin`）立即同步送出一則「Notesteme 測試通知」（不經 outbox、不重試、不受 NOTIFY-R10、R11 限制，但經過 NOTIFY-R7 的 netguard）。成功時 200 `{ok: true, status: <HTTP 狀態碼>}`；失敗時仍回 200 `{ok: false, error_class, message}`，`message` 同 NOTIFY-R7、R8 的固定訊息。notifier 不存在時 404。 |
| NOTIFY-R13 | log 只記錄 notifier 的 `id`、`kind`、端點的主機、事件、outbox id、HTTP 狀態碼與錯誤類別；不記錄完整網址（Telegram 的路徑含 bot token、Discord 的路徑含 webhook token）、請求標頭、請求或回應的 body。 |

## 介面

- API（全部 scope `admin`）：`GET /api/notifiers`、`POST /api/notifiers`、`GET /api/notifiers/:id`、`PATCH /api/notifiers/:id`、`DELETE /api/notifiers/:id`、`POST /api/notifiers/:id/test`、`GET /api/notifications[?state=pending|sent|dropped]`。
- MCP：不提供（設定通知是 owner 的管理動作；之後需要再加 `admin` 的 tool）。
- CLI：`notesteme notifiers ls`、`notesteme notifiers test <id>`（client 模式呼叫 API）。
- 設定檔（只能寫在設定檔，避免 API 把請求導到任意主機）：`notify.allow_hosts`（主機名稱清單，比對規則同 GRD-R3）、`notify.dedupe_window`、`notify.rate_limit.{count, per}`；`server.public_url`（DIG 介面）。
- 資料表（ADR-0021 第 7 點）：`notifiers (id, kind, config_ciphertext, config_public_json, events_json, enabled, created_at, updated_at)`；`notification_outbox (id, notifier_id, event, dedupe_key, payload_json, state, attempts, next_attempt_at, last_error, created_at, sent_at)`，索引 `(state, next_attempt_at)` 與 `(notifier_id, dedupe_key, created_at)`。`config_public_json` 存非機密欄位與末四碼，列表不需要解密。
- 排程：`scheduled_jobs.kind = notify.deliver`（`key` 固定為 `outbox`）；有新的 outbox 項目時把 `next_run_at` 提前到現在。
- 資源類別：不進佇列（ADR-0021 第 6 點）。
- UI：設定的「通知」分區：新增、編輯、刪除、勾選事件、「傳送測試通知」；總覽顯示 `dropped` 的通知（UI 切片）。

## 與其他規格的關係

- SEC：密鑰的加密方式與 master key 輪替（SEC-R8）照 SEC；`secrets reset`（SEC-R9）會刪除 notifier 的密鑰，之後送出時為 `permanent`，訊息「notifier 的密鑰已被重設，請重新設定。」。
- GRD：NOTIFY-R7 是 GRD-R1 已列出的「通知 webhook」的細節；通知的大小與逾時上限比 RSS 小。
- DIG：`digest` 的內容與 dedupe 鍵由 DIG 決定，送出照這份規格。
- OPS：`alert` 事件的內容、是否重複告警由 OPS-R12～R18 決定。
- COST：`budget.warning` 的門檻與內容由 COST 規格決定，事件名稱與 dedupe 鍵用這裡的定義。
- SRC-10：`source.better_available` 的觸發條件由 SRC 規格決定。

## 不在範圍內

- Email、Slack、Matrix、Pushover 等其他管道（可以用 webhook 接）。
- 互動式通知（Telegram 的 inline keyboard、Discord 的按鈕）。
- 每個事件自訂訊息模板。
- 多個使用者各自的通知設定（單一 owner）。
- 安靜時段（例如晚上不送）：之後可以加在 notifier 上。

## 待決問題

- N1：`permanent` 的失敗第一次就 `dropped`（NOTIFY-R8），ADR-0021 第 5 點只寫「第 6 次失敗後標記 dropped」（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）。理由：401、404、封鎖不會自己好，重試 5 次只會拖 8 小時才讓 owner 看到問題；`dropped` 的項目在總覽顯示，owner 修正設定後可以用測試通知確認。ADR-0021 需要補一句。
- N2：通知不跟隨轉址（NOTIFY-R7），比 GRD-R4 的「最多 5 次」嚴格：四種端點都不需要轉址，而轉址是 SSRF 最常見的繞路。
- N3：Telegram 與 Discord 只能連到官方主機或 `notify.allow_hosts`（NOTIFY-R2）：bot token 與 webhook token 是憑證，允許任意主機等於讓設定錯誤（或被竄改的設定匯入）把憑證送到別人的伺服器。ntfy 與 webhook 可以自架，所以不限制主機，但仍受 netguard 限制（自架在內網時要列進 `network.allow_private`）。
- N4：webhook 的 `url` 也當成密鑰（NOTIFY-R6）：很多 webhook（例如 Slack、Home Assistant）把憑證放在網址裡；回應只給 `url_host` 與末四碼。代價是 owner 看不到完整網址，只能重新輸入。
- N5：去重視窗預設 24 小時、速率上限預設每 10 分鐘 20 則（NOTIFY-R10、R11）是保守的初值（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：VPS 上 YouTube 擋下大量請求時，`item.failed` 以「影音＋步驟」去重，同一部影音重試多次只通知一次；很多部影音同時失敗時由速率上限限制，ntfy.sh 對匿名使用者的上限也在這個量級。
- N6：outbox 寫入與事件在同一個交易（ADR-0021 第 5 點），所以 notifier 的設定在事件發生時決定；之後才勾選的事件不會補送。
- N8：`notifiers` 的欄位比 plan 第 21 節多了 `config_public_json`、`created_at`、`updated_at`，`id` 改成 owner 取的 slug（和處理方案以名稱識別相同，路由與彙整設定才能直接引用）；ADR-0021 第 7 點要同步。
- N7：`item.done` 的「一次執行」以 `POST /api/items`、`rerun`、`continue` 的批次為單位；重跑一次就通知一次（dedupe 鍵含執行 id）。只想知道失敗的 owner 可以不勾 `item.done`。
- M4-S0：設定檔的 `notify.*` 已解析與驗證（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：`allow_hosts` 是小寫的主機名稱（不含 scheme、port、萬用字元）；`dedupe_window` 接受 `24h`、`30d` 的寫法，範圍 `1m`～`30d`；`rate_limit.count` 為 1～10000、`rate_limit.per` 為 `1s`～`24h`（規格沒有寫上限，S0 只擋明顯不合理的值）。`server.public_url` 必須是 http 或 https、不含帳號密碼、query 與 fragment，結尾的 `/` 會去掉。通知的路由是 501 stub（scope `admin`）。

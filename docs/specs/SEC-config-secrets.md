# SEC：設定與 secrets

- 狀態：ready
- 里程碑：M1（`config export`／`import` 在 M2，和處理方案一起）
- 相關：plan 第 19 節；ADR-0013；UI [設定](../ui/pages.md#12-設定)
- 驗收場景：[`features/SEC-config-secrets.feature`](../../features/SEC-config-secrets.feature)

## 目的

每個設定只有一個來源；會執行命令的設定碰不到 API；API key 與 cookie 加密存放，master key 可以輪替，遺失時的影響有限且明確。

## User stories

| ID | Story | 里程碑 |
|---|---|---|
| SEC-01 | As the owner, I want to know where each setting lives and which one wins, so that I am never surprised by the configuration in effect. | M1 |
| SEC-02 | As the owner, I want settings that run commands to be changeable only in the config file, so that a leaked token can never execute code on my machine. | M1 |
| SEC-03 | As the owner, I want API keys and cookies encrypted and never shown in full again, so that a copied database or a glance at the screen does not leak them. | M1 |
| SEC-04 | As the owner, I want to rotate the master key and recover from losing it, so that a key problem never costs me my library. | M1 |
| SEC-05 | As the owner, I want to export and import my settings as YAML, so that I can version them or move them to another instance. | M2 |

## 規則

| ID | 規則 | 里程碑 |
|---|---|---|
| SEC-R1 | 設定的優先順序：環境變數 > 設定檔 > 內建預設值。環境變數用 `NOTESTEME_` 加上大寫的設定路徑，例如 `NOTESTEME_LISTEN`。 | M1 |
| SEC-R2 | 設定檔只放基礎設定：listen 位址、資料路徑、外部工具路徑、對外請求 allowlist、`command` adapter、publish hook。DB 管理的設定（處理方案、prompt、一般 provider、預算等）出現在設定檔時，啟動失敗並指出是哪個欄位。 | M1 |
| SEC-R3 | `command` 類型的 provider 與 hook 只能在設定檔定義。透過 API 新增或修改時回傳 403，訊息為「這個項目只能在設定檔中定義。」 | M1 |
| SEC-R4 | secrets 以資料金鑰加密，資料金鑰再由 master key（`NOTESTEME_MASTER_KEY`）加密。DB、log、備份中都不會出現 secret 的明文。 | M1 |
| SEC-R5 | 沒有設定 master key 時，`serve` 拒絕啟動，訊息為「尚未設定 NOTESTEME_MASTER_KEY。」master key 錯誤時拒絕啟動，訊息為「master key 不正確，無法解開資料金鑰。」並提示 `notesteme secrets reset`。 | M1 |
| SEC-R6 | API key 儲存後，API 與 UI 只回傳末四碼。 | M1 |
| SEC-R7 | cookie 在執行時才寫成權限 `0600` 的暫存檔交給 yt-dlp，子程序結束後立即刪除。每次呼叫各用一個檔名：yt-dlp 結束時會截斷並重寫 cookie 檔，共用檔案在並行時會讀到寫到一半的內容。 | M1 |
| SEC-R8 | `notesteme secrets rotate-key` 用新的 master key 重新加密資料金鑰；完成後舊的 master key 無法再啟動。 | M1 |
| SEC-R9 | `notesteme secrets reset` 刪除所有 secrets 與資料金鑰，並以新的 master key 建立新的資料金鑰；影音、產出、設定都保留，token 與密碼不受影響。 | M1 |
| SEC-R10 | `notesteme config export` 輸出 DB 管理的設定（不含 secrets）；`notesteme config import` 匯入時先驗證整份內容，有錯誤就整份不套用。 | M2 |

## 介面

- 設定檔：`/config/notesteme.yaml`
- 環境變數：`NOTESTEME_MASTER_KEY`、`NOTESTEME_<設定路徑>`
- API：`GET/PUT /api/settings/:section`、`POST /api/cookies`
- CLI：`notesteme secrets rotate-key|reset`、`notesteme config export|import`
- `POST /api/cookies`：上傳平台的 cookie。
  - 請求：`{ "platform": "youtube" | "bilibili", "content": "<Netscape cookie 檔，或 name=value; … 標頭>" }`，內容上限 64 KiB；請求本體（JSON）上限 512 KiB，超過同樣回 422 `cookie_too_large`（含沒有 Content-Length 的請求，讀到上限就停止；其他路由維持 Huma 預設的 1 MiB 與 413）。
  - 正規化：一律轉成 Netscape 格式。標頭格式的 cookie 用平台網域（`.youtube.com`、`.bilibili.com`）、path `/`、secure `TRUE`、expiry `0`；Netscape 格式只保留平台網域（含子網域）的 cookie，其他網域一律丟掉。
  - 成功：200 `{ "platform", "cookies": <數量>, "updated_at" }`。回應與錯誤訊息永遠不含 cookie 的值。每個平台只存一份，新的取代舊的。儲存後會恢復等待這個 cookie 的 `blocked` 步驟（見 PIPE 介面）；恢復用不受請求取消影響的 context，逾時 30 秒，客戶端在儲存後斷線不會中斷恢復。
  - 422，code 如下（訊息不含 cookie 的值）：

    | code | 情況 |
    |---|---|
    | `cookie_unsupported_platform` | 平台不是 `youtube` 或 `bilibili` |
    | `cookie_empty` | 內容是空的 |
    | `cookie_too_large` | 內容超過 64 KiB，或請求本體超過 512 KiB |
    | `cookie_no_match` | 丟掉其他網域之後，沒有剩下任何 cookie（含格式無法辨識） |
    | `cookie_missing_sessdata` | bilibili 的 cookie 缺 `SESSDATA` |
    | `cookie_invalid_format` | 名稱或值含控制字元（包含 tab、換行）；Netscape 格式的網域、path、名稱、值也一樣，控制字元包含 `\r`、NUL、ESC（只檢查保留下來的平台 cookie） |

  - 權限：只有 owner（session 或本機）可以使用；session 要帶 CSRF token（AUTH-R5），沒登入回 401，缺 CSRF 回 403。token 不可使用，回 403 `forbidden`（cookie 等同登入憑證）。standalone 與本機 socket 走同一個 handler，不需要 session。
- 儲存方式：secrets 表以 `(kind, name)` 為主鍵，cookie 是 `kind=cookie`、`name=<platform>`。內容用資料金鑰以 XChaCha20-Poly1305 加密，AAD 是 `kind/name`（`cookie/youtube`），每次加密用新的隨機 nonce；密文搬到別筆 secret 後無法解開。DB、WAL、log 都不會出現明文。
- 執行時：每次呼叫 yt-dlp 時解密，在 `<data>/tmp` 以 `O_EXCL` 建立權限 `0600` 的暫存檔 `cookies-<platform>-<隨機>.txt`，每次呼叫一個新檔名（SEC-R7），子程序結束後刪除。
- stderr 遮蔽：yt-dlp 讀 cookie 檔遇到格式有問題的行時，會在 stderr 印出含 cookie 值的 `skipping cookie file entry …`。stderr 進入錯誤訊息（`errclass.Error.Detail`）與 log 之前，含這段文字的整行以固定字串取代（`toolexec.RedactCookieLines`，YouTube 與 Bilibili 的 client 都套用），其他行不變。
- 使用政策：有該平台的 cookie 時，這個平台的每一次 yt-dlp 呼叫都帶 `--cookies`（Probe、字幕、音訊、播放清單），不是只有失敗時才用；`--version` 不帶。沒有 cookie 時不帶這個旗標。

## 不在範圍內

- 外部 secret manager（例如 Vault、1Password），目前沒有需求。

## 待決問題

- SEC-R1 新增「The config file is used when no environment variable is set」（2026-11-15，OPS 審查後 owner 同意）：原本只有「環境變數覆寫」一個案例，`listen_source` 寫死成 `env` 也會通過。
- master key 的格式（32 bytes 的 base64，或允許任意密語再經 KDF）。暫定兩者都接受：長度不是 32 bytes 時用 argon2id 推導。
- SEC-R7 的場景原本寫死 `/data/tmp/cookies-youtube.txt`，與每次呼叫一個檔名的設計衝突，改成斷言「交給 yt-dlp 的 cookie 檔」的權限與「沒有 cookie 檔留下」（2026-11-14，owner 同意；依據是 yt-dlp 的 `YoutubeDLCookieJar.save` 會先截斷再重寫）。
- cookie 的使用政策是「有就每次呼叫都帶」，這會讓 YouTube 把該帳號與 yt-dlp 的流量關聯在一起，有帳號被標記（限制或停權）的風險。建議上傳分身帳號（專門給 Notesteme 用）的 cookie，不要用主要帳號。是否改成「只有在需要時（403、會員限定）才帶」要看實際被標記的狀況再決定（2026-11-14，SRC-B）。
- 會員限定影片（stderr 含 `Join this channel to get access to members-only content`）歸為 `needs_action`，訊息「這部影片需要登入才能取得。上傳 YouTube cookie 後會自動繼續。」、動作「上傳 cookie」、`resume_on` 為 `cookie:youtube`。SRC 規格的待決問題要記下這個分類（由主代理負責）；文字依 yt-dlp 的原始碼，尚未對真實的 yt-dlp 驗證。
- cookie 上傳與執行中的步驟的競態（2026-11-14，SRC-B 審查）：步驟在沒有 cookie 時開始執行，使用者此時上傳，`ResumeBlocked` 找不到 `running` 的步驟；之後步驟回報需要 cookie 就會永遠 `blocked`。處理方式見 PIPE 介面的「執行中補上條件」，依據是 secrets 表 `(cookie, <platform>)` 的 `updated_at`。
- 請求本體上限 512 KiB 的依據：內容上限 64 KiB，JSON 跳脫最壞展開成 6 倍（`\u00xx`）約 384 KiB。Huma 的 `MaxBodyBytes` 超過時只會回 413，所以 `POST /api/cookies` 在 Huma 之前先檢查（`cookieBodyLimit`），讓所有「cookie 太大」都是同一個 422 code；`MaxBodyBytes` 仍明確設為 512 KiB 當第二道防線。

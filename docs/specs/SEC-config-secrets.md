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
| SEC-06 | As the owner, I want to see every provider, test its connection, and read model deprecation notices on the settings page, so that a wrong key or a retiring model shows up before a job fails. | M1 |

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
| SEC-R11 | 設定頁「服務與金鑰」的 provider 目錄與測試連線（pages.md §12）：`GET /api/providers`（`read`）列出內建與設定檔中定義的 provider 與模型的停用公告，不含 API key 與 `command` 的內容，全部唯讀。`POST /api/providers/:id/test`（`admin`）送一個帶認證、不產生費用的請求，成功回 `{ok, latency_ms, message}`；沒有 API key 或 key 被拒絕回 409 `needs_action`（動作「設定 API key」）；provider 失敗、逾時或連線失敗回 502 `upstream_failed`。`command` provider 只檢查指令存在且可以執行，不執行它。 | M1 |

## 介面

- 設定檔：`/config/notesteme.yaml`
- 環境變數：`NOTESTEME_MASTER_KEY`、`NOTESTEME_<設定路徑>`
- API：`GET/PUT /api/settings/:section`、`POST /api/cookies`、`GET /api/config/export`、`POST /api/config/import`、`GET /api/providers`、`GET /api/providers/:id`、`POST /api/providers/:id/test`
- provider 目錄與測試連線（SEC-R11）：
  - `GET /api/providers` 回 `{ "providers": [ … ] }`，依 id 排序（同一個 id 依 `asr`、`llm`、`transcript` 的順序）。每一項是 `{ id, type, kind, editable, defined_in, testable, untestable_reason?, deprecations }`：`kind` 是 `llm`、`asr`（語音轉錄）或 `transcript`（字幕服務）；`defined_in` 是 `builtin` 或 `config_file`；`editable` 一律是 `false`（API key 在 `PUT /api/settings/services` 設定）；`deprecations` 是 `[{ model, after, replacement?, message? }]`，沒有時是空陣列。
  - 目錄的內容：LLM 是內建的 `anthropic`、`openai`、`gemini` 與設定檔 `llm.providers`（同名時是設定檔的）；語音轉錄是內建的 `groq` 與設定檔 `asr.providers`（含 `command`；同名時設定檔的取代內建的）；字幕服務是內建的 `supadata`。`GET /api/providers/:id` 與 `/test` 接受 `?kind=`，省略時依 `asr`、`llm`、`transcript` 的順序取第一個。
  - 測試的請求：anthropic `GET /v1/models?limit=1`、OpenAI 相容 `GET <base_url>/models`、Gemini `GET /v1beta/models?pageSize=1`、Groq `GET /openai/v1/models`、Supadata `GET /v1/me`（帳號資訊，不消耗 credit）。API key 的來源與正式執行相同（環境變數優先，其次是資料庫）；請求經 netguard（ADR-0014）、不跟隨轉址、逾時 15 秒，回應只讀 64 KiB 並丟棄。
  - 錯誤：401、403 是 409 `needs_action`「provider <id> 拒絕了 API key（HTTP <碼>）。」；沒有 key 的訊息與正式執行相同（例如「LLM provider openai 沒有設定 API key。」）；429、5xx 是 502「provider <id> 暫時無法使用（HTTP <碼>），請稍後再試。」；其他狀態碼（含轉址）是 502「provider <id> 拒絕了測試請求（HTTP <碼>），請確認設定檔的 base_url。」；逾時、連線失敗、被 netguard 擋下各有固定訊息。訊息與 log 都不含 API key、網址與 provider 回應的內容。找不到 provider 回 404，不提供測試的 provider（`testable: false`）回 501。
  - `command` provider：只檢查 `command` 的第一個元素是存在且可以執行的檔案（含 `/` 時直接檢查，否則在 `PATH` 中找），成功時 `message` 是「找到指令且可以執行；沒有實際執行。」；找不到或沒有執行權限是 409 `needs_action`「找不到 command provider <id> 的指令，或沒有執行權限。」，動作「修改設定檔」。訊息不含指令與路徑。
- CLI：`notesteme secrets rotate-key|reset`、`notesteme config export [--out <file>]`、`notesteme config import <file>`
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
- `config export`／`config import`（SEC-R10，M2）：
  - 範圍：DB 管理的設定中，目前只涵蓋處理方案（不含內建的 `default`）與預設處理方案。檔案是 YAML，頂層只有兩個鍵：`profiles`（名稱 → 處理方案的內容，與 `POST /api/profiles` 的 `config` 同形）與 `default_profile`（只有明確設定過才輸出）。鍵依字母排序、縮排 2 格，輸出穩定，可以放進版本控制，也可以原樣匯入。
  - 不含 secrets：處理方案只有設定值與 prompt、provider 的引用名稱；API key、cookie、token、密碼雜湊不在這個流程碰到的任何資料中，單元測試在資料庫放了這些值再確認輸出沒有它們。
  - API：`GET /api/config/export`（`read`）回 `{ "content": "<YAML>", "profiles": [名稱…], "default_profile"? }`；`POST /api/config/import`（`admin`）收 `{ "content": "<YAML>" }`（請求本體上限 4 MiB，YAML 上限 1 MiB），成功回 200 `{ "created": […], "updated": […], "unchanged": […], "default_profile"?, "default_changed" }`。scope 在讀取本體之前檢查。YAML 的解析與驗證都在 server 端，所以 `--server` 模式（token 的 scope 決定能不能匯出入）與本機模式行為相同。
  - 匯入是「新增或取代同名處理方案」，不刪除檔案裡沒有的處理方案；先驗證整份，在**同一個交易**中才寫入，任何錯誤（驗證或寫入）都整份不套用。內容不正確回 422 `config_invalid`，`errors` 逐一列出欄位路徑，處理方案內的路徑加上 `profiles.<名稱>` 前綴（例如 `profiles.broken.language.targets`）。檢查項目：檔案大小（1 MiB）、YAML 語法（只能有一份文件；不接受別名、合併鍵、重複的鍵、非字串的鍵、NaN 與無限大、不認得的型別標籤）、不認識的頂層鍵、名稱規則（同 PRF）、不得覆蓋內建處理方案、每個處理方案用與 `POST /api/profiles` 完全相同的驗證（含 PRF-R5 的完整性與合併後的語意檢查）、`default_profile` 必須是匯入後存在的處理方案（檔案中的，或已經存在的，含內建的 `default`）。
  - 取代同名處理方案沿用 PRF-R3：建立新版本，舊版本保留；已新增的影音綁定的是當時的版本，進行中與已完成的步驟不受影響。
  - CLI：`config export` 預設輸出到 stdout（只有 YAML，可以直接轉向到檔案），`--out <file>`（別名 `--output`）寫到檔案（先寫暫存檔再改名）；`--json` 輸出 API 的回應。`config import` 成功時結束碼 0、輸出建立／更新／未變更的處理方案數量；內容不正確時結束碼 **1**，stderr 逐行列出欄位路徑（見待決問題）。權限：本機模式是 owner；`--server` 時 export 需要 `read`、import 需要 `admin`。
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
- 設定檔新增頂層的 `mcp`（2026-11-23，M2 S9 實作 AUTH-R11 時決定，依 /goal 採保守預設，**待 owner 追認**）：`mcp.allowed_hosts` 與 `mcp.allowed_origins` 是對外介面的基礎設定（同 listen 位址），只放在設定檔（SEC-R2），格式錯誤時啟動失敗並指出欄位。預設值見 AUTH 規格的介面。
- `config export`／`import` 的範圍與格式（2026-11-23，依 /goal 採保守預設，待 owner 追認）。以下是依保守原則做的決定，需要時再放寬：
  - 範圍只有 `profiles` 與 `default_profile`。prompt、頻道覆寫、全域設定（`PUT /api/settings/*`）、詞彙表、ASS 樣式、訂閱、預算、通知都**不**匯出，也不接受匯入（出現就是不認識的頂層鍵）。**已知限制：** 處理方案引用的自訂 prompt 不在檔案中，搬到另一個實例時目標實例要先有同名 prompt，否則匯入會因 `profiles.<名稱>.stages.<步驟>.prompt` 找不到而整份不套用；之後若要「整個實例搬家」，要一併涵蓋 prompt。
  - 內建的 `default` 不匯出，匯入時也不得覆蓋（回 `profiles.default` 的錯誤）。但現行的 `PATCH /api/profiles/default` 允許修改內建 default 的內容（PRF-R7 只規定不能刪除；handoff 寫「不能修改」，與程式不一致），所以使用者對 `default` 做過的自訂**不會**被匯出。若要涵蓋，需要 owner 決定格式（例如允許 `profiles.default`，或另設 `builtin_overrides`）。
  - 內容與目前版本完全相同（sha256 相同）的處理方案不新增版本，回報為 `unchanged`，讓重複匯入同一份檔案是冪等的；這和 `PATCH`（內容相同也新增版本，PRF D2）不同，因為匯入不是使用者逐一儲存。
  - `default_profile` 只在資料庫中明確設定過預設處理方案時才匯出；檔案沒有這個鍵時匯入不改動預設處理方案。
  - 處理方案的值必須是物件：`name:`（null）或純量都是錯誤，要寫 `name: {}`；不像 `POST /api/profiles` 把省略或 null 的 `config` 視為 `{}`，因為 YAML 中忘了縮排內容時 `name:` 會悄悄變成空的處理方案。
  - 結束碼：`config import` 內容不正確時是 **1**，不是 CLI-R6 對 API 422 規定的 2。理由：檔案的內容不是命令列參數，而且 `features/SEC-config-secrets.feature` 的「An invalid config import changes nothing」要求 1。只有 `config_invalid` 這個 422 走這條規則，其他 400、422 維持 2。CLI 規格有一行交叉引用。
  - YAML 解析器比標準嚴格（輸入是不可信的）：不接受別名與合併鍵（避免展開炸彈）、重複的鍵、非字串的鍵、NaN／無限大、多份文件、不認得的型別標籤；沒有加引號的日期（`2024-01-01`）保留原文字；巢狀上限 64 層、檔案上限 1 MiB。有 fuzz，並以「讀得進來的內容輸出再讀回來必須相同」為不變條件。
  - API 用 JSON 外層（`{ "content": "<YAML>" }`）而不是直接收 `application/yaml`，和 `POST /api/cookies`、字幕匯入的 `content` 一致，OpenAPI 與 Huma 的驗證也直接適用；代價是 `curl` 要先包成 JSON。沒有做 `--dry-run`（只驗證不套用）；需要時加在 API 與 CLI 上都很小。
- SEC-R11 的決定（2026-11-25，依 /goal 由實作者暫定，待 owner 追認）：
  - 規則放在 SEC（新增 SEC-06、SEC-R11），因為既有的 provider 規則（SEC-R3）與 API key（SEC-R4、R6）都在這裡；pages.md §12 把它列為 M1。
  - 測試請求一律用列出模型或帳號資訊的 GET 端點，不送 completion：不產生費用、不消耗 token 或 credit，也不需要選模型。代價是只驗證「key 有效、服務連得到」，不驗證某個模型能不能用（模型不存在在正式執行時仍是 LLM-R11 的 `needs_action`）。
  - 成功一律是 200 `ok: true`；失敗不用 200 `ok: false`，而是沿用 PRM 待決問題 P10 的錯誤碼（409 `needs_action` 帶 `actions`、502 `upstream_failed`），UI 用同一個錯誤元件顯示。429 也是 502（不是 429 `rate_limited`）：測試連線不排隊等待，提示稍後再試。
  - `command` provider 的測試沒有規格規定；保守的做法是不執行使用者的命令（命令可能很慢、會寫檔或花錢），只檢查指令存在且可以執行。
  - 設定檔 `asr.providers` 的 `openai-compatible`、`mistral` 目前沒有接上處理流程，也沒有對應的 client 與 API key 來源，所以 `testable: false`（`untestable_reason`「這種 provider 還沒有接上處理流程，無法測試連線。」），`/test` 回 501；不另外發明 key 的綁定方式。服務與金鑰中的 `mistral`、`openai-compatible` API key 也因此沒有測試按鈕。
  - 同一個 id 可能同時是 `asr.providers` 與 LLM provider（兩個命名空間獨立）：列表兩個都列，以 `kind` 區分；單一 id 的查詢先取 `asr`，維持 `GET /api/providers/{id}` 原本只查 `asr.providers` 的行為。
  - **模型停用公告：機制已有，目前沒有資料。** 規格沒有定義資料來源；目錄（`internal/providers.Deprecations`）以 provider id 為 key，每項是 `model`、`after`（YYYY-MM-DD）、`replacement`、`message`，只放能對照 provider 官方公告確認的資料，並記下來源與查證日期。2026-11-25 查證：程式內建的模型 ID 只有 Groq 的 `whisper-large-v3-turbo`（`groq.DefaultModel`），它不在 Groq 的官方停用清單（https://console.groq.com/docs/deprecations ，該頁只列出它是 `distil-whisper-large-v3-en` 的替代模型），也仍列在 production models；LLM 的模型 ID 都是處理方案中自由輸入，程式沒有模型清單可以掛公告。所以目前是空的，API 一律回空陣列。plan 第 25 節風險表的 `whisper-1` 日期不是官方公告的原文，`whisper-1` 也不是程式內建的模型，不放。之後內建了模型 ID（例如預設處理方案寫死模型）時，要對照該 provider 的官方停用頁補上。

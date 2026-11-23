# ENTRY：其他新增入口

- 狀態：ready
- 里程碑：M4
- 相關：plan 第 3.1、19 節；ADR-0008、ADR-0012、ADR-0014、[ADR-0021](../adr/0021-m4-scheduler-subscriptions-and-outbox.md)；[AUTH](AUTH-access.md)（登入、session、CSRF）；UI [新增](../ui/pages.md#2-新增)、[設定](../ui/pages.md#12-設定)、[登入](../ui/pages.md#13-登入)
- 驗收場景：[`features/ENTRY-entry-points.feature`](../../features/ENTRY-entry-points.feature)

## 目的

讓「把影片交給 Notesteme」在桌面瀏覽器上只要點一下書籤（bookmarklet），在手機上用系統的「分享」就能完成。兩個入口都只是把網址帶進既有的「新增」抽屜，由使用者確認後才開始處理：它們不建立影音、不帶任何憑證，也不讀取第三方頁面的內容。因為網址來自第三方頁面與其他 App，兩個入口都要擋住開放轉址與點擊劫持。

## User stories

| ID | Story |
|---|---|
| ENTRY-01 | As the owner, I want a bookmarklet that sends the current page to Notesteme, so that adding a video takes one click. |
| ENTRY-02 | As the owner, I want to share a link to Notesteme from my phone, so that I can add videos while browsing on mobile. |

## 規則

| ID | 規則 |
|---|---|
| ENTRY-R1 | 設定頁的「書籤小工具」區塊提供一個可以拖到書籤列的連結，內容固定是 `javascript:(()=>{window.open('<origin>/add?url='+encodeURIComponent(location.href),'_blank','noopener')})()`，`<origin>` 是設定頁目前的 `location.origin`（UI 產生，不經 API，所以不受 `Host` 標頭影響）。bookmarklet 只讀 `location.href`：不含 token、cookie 或任何憑證，不讀第三方頁面的 DOM、標題、選取的文字，不在第三方頁面發出請求或插入元素，只開新分頁。 |
| ENTRY-R2 | `GET /add?url=<網址>` 是 UI 的頁面路由（不是 `/api`）：有 owner 的 session 時回 200 的 SPA 頁面，UI 開啟「新增」抽屜、把網址填入並照常辨識（`POST /api/resolve`），**不自動開始處理**，要使用者按「開始處理」。沒有 session 時回 303 到 `/login?next=<URL 編碼後的原始路徑與 query>`。頁面路由只認 session cookie：bearer token 與 query 中的 token 一律不當成登入（同樣導向登入頁）。網址不是 `http`／`https`、或超過 2048 個字元時，UI 忽略它、輸入框留空，並顯示「書籤或分享的網址無效，已忽略。」。 |
| ENTRY-R3 | PWA：`GET /manifest.webmanifest` 不需要登入，`Content-Type: application/manifest+json`，內容包含 `name: "Notesteme"`、`start_url: "/"`、`scope: "/"`、`display: "standalone"`、192 與 512 像素的 PNG icon，以及 `share_target: {action: "/share", method: "GET", params: {title: "title", text: "text", url: "url"}}`；不含任何實例或使用者的資料。`GET /sw.js` 是只為了可安裝而存在的 service worker（`Content-Type: text/javascript`、`Cache-Control: no-cache`），不快取 `/api/*`、`/mcp`、`/add`、`/share` 的回應，也沒有離線功能。SPA 的 HTML 以 `<link rel="manifest" href="/manifest.webmanifest">` 引用它。 |
| ENTRY-R4 | `GET /share?url=&text=&title=`（share target）依序取第一個可用的網址：`url` 參數是 `http`／`https` 時用它；否則取 `text` 中第一個符合 `https?://[^\s<>"']+` 的字串；再否則取 `title` 中的。取出後反覆去掉結尾的 `.,;:!?)]}」』）。，、`，最長 2048 個字元（超過時視為沒有）。找到時回 303 到 `/add?url=<URL 編碼的網址>`；沒有時回 303 到 `/add?share_error=no_url`，UI 顯示「分享的內容中沒有網址。」。`/share` 不辨識網址、不建立影音，`text`、`title` 的其他內容丟棄，不寫進 log。 |
| ENTRY-R5 | 沒有 session 時，`/share` 先依 ENTRY-R4 算出要去的 `/add?…`，再回 303 到 `/login?next=<URL 編碼的 /add?…>`（不把原本的 `text`、`title` 帶到登入頁）。登入成功後 UI 前往登入回應中的 `next`（ENTRY-R6），所以分享的網址會在登入後出現在「新增」抽屜中。反向代理驗證與 OIDC（AUTH M4）的登入流程也帶同一個 `next`。 |
| ENTRY-R6 | 開放轉址防護：`POST /api/auth/login {password, next?}` 成功時回 `{next}`。`next` 的檢查與 OIDC 的 `next`（AUTH-R17）共用同一個函式，只有在下列條件全部成立時原樣回傳，否則回 `"/"`：以 `/` 開頭；第二個字元不是 `/` 或 `\`；不含 `\` 與 ASCII 控制字元（`\x00`～`\x1f`、`\x7f`）；最長 2048 bytes（見 E3）；以 URL 解析後沒有 scheme 也沒有主機。沒有 `next` 時回 `"/"`。密碼錯誤時照 AUTH-R6 回 401，不回 `next`。UI 只使用回應中的 `next`，不直接使用登入頁網址中的 `next`。 |
| ENTRY-R7 | `/add`、`/share`、`/login` 的回應帶 `Content-Security-Policy: frame-ancestors 'none'` 與 `Cache-Control: no-store`，不能被第三方頁面嵌入（點擊劫持）。這三個 GET 路由不改變任何狀態：不建立影音、不呼叫 resolve、不寫入資料庫（session 的最後使用時間除外）。 |

## 介面

- 頁面路由（Go 的 HTTP server，不在 `/api` 底下）：`GET /add`、`GET /share`、`GET /login`、`GET /manifest.webmanifest`、`GET /sw.js`。`/add`、`/share` 需要 session（沒有時 303 到 `/login`）；`/login`、`/manifest.webmanifest`、`/sw.js` 不需要登入，比照 AUTH-R1 的例外清單補上。
- API：`POST /api/auth/login` 加上可選的 `next`，回應加上 `next`（ENTRY-R6）。這是 AUTH 介面的擴充，AUTH-R1～R6 的其他行為不變。
- UI：設定頁的「書籤小工具」區塊（ENTRY-R1）；「新增」抽屜接受 `?url=` 與 `?share_error=`（ENTRY-R2、R4）；登入頁登入後前往 `next`（ENTRY-R5）。
- 沒有 MCP、CLI、處理方案或資料表的變更；`pipeline.Stages` 不變。

## 與其他規格的關係

- ING：`/add` 帶進來的網址照「新增」的流程辨識與新增（ING-R1～R8，含費用估算 ING-08）；頻道網址照 ING-R5 提示建立訂閱（SUB）。
- AUTH：session、CSRF 與登入鎖定照 AUTH-R1～R6；ENTRY 只加上 `next`。AUTH 的 M4 章節（反向代理驗證、OIDC）登入後也要依 ENTRY-R6 處理 `next`。
- GRD：`/share` 只轉交網址，不連外；之後的辨識才經過 GRD 的防護。

## 不在範圍內

- iOS Safari 的 share target（iOS 不支援 Web Share Target）：iOS 用 bookmarklet 或捷徑 App 開啟 `/add?url=`。
- 瀏覽器擴充功能、Android 原生 App。
- 分享檔案（share target 的 `files`）：只分享網址；檔案請用「新增」的拖放或 inbox。
- 一鍵直接開始處理（不經「新增」抽屜確認）。
- 離線功能與推播通知（通知見 NOTIFY 規格）。

## 待決問題

- E1：bookmarklet 開新分頁（`window.open`），不在目前的分頁跳轉，也不用 iframe 或彈出的小視窗：在第三方頁面插入 iframe 要讀寫對方的 DOM，也會被對方的 CSP 擋住；跳轉會離開使用者正在看的影片。決定（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）。
- E2：`/add` 不自動開始處理（ENTRY-R2）。故事說「一次點擊」，實際上是點 bookmarklet 再按「開始處理」；理由是 GET 的頁面不能改變狀態（ENTRY-R7），而且會花錢的動作要讓使用者先看到估算的費用（ING-08）。決定（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）。
- E3：`POST /api/auth/login` 的 `next` 是 AUTH 介面的擴充，寫在 ENTRY 而不是 AUTH 的 M4 章節。AUTH-R17（OIDC 的 `next`）的條件與 ENTRY-R6 相同，但上限是 512 bytes；ENTRY 的 `next` 是 `/add?url=<URL 編碼的網址>`，分享的網址被編碼兩次（`/login?next=` 再編碼一次），帶追蹤參數的網址很容易超過 512 bytes，超過時登入後只會回到首頁。決定（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：兩者共用同一個檢查函式，上限統一為 2048 bytes；AUTH-R17 已在整合時改成 2048。
- E4：URL 擷取只用簡單的 regex 與結尾標點的清單（ENTRY-R4），不處理括號配對（例如維基百科網址中的 `(…)` 會被截掉結尾的 `)`）；分享的多半是 YouTube、Bilibili 與 Podcast 的網址，不含括號。
- E5：manifest 的 icon 用現有的品牌圖示產生（UI 切片負責）；`name` 固定是「Notesteme」，不顯示實例名稱，因為 manifest 不需要登入就能讀取。
- E6：分享網址的擷取細節（ENTRY-R4）。決定（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：(1) scheme 不分大小寫；(2) 網址在 `\s` 之外也在 Unicode 空白（`\p{Z}`，全形空白 U+3000、NBSP）停下，因為 Go RE2 的 `\s` 只涵蓋 ASCII 空白；(3) 三個來源（`url`、`text`、`title`）都去掉結尾標點；(4) 2048 以字元（code point）計，UI 端相同；(5) 一個候選去掉標點後沒有主機、含控制字元或超過 2048 個字元時，視為沒有，繼續看同一個來源的下一個候選，再換下一個來源（而不是整個分享視為沒有網址）。實作在 `internal/entry`（`ShareURL`、`ValidURL`）。
- E7：「書籤小工具」區塊放在設定頁新的「書籤小工具」分區（`/settings/bookmarklet`，排在「外觀」之後），同一個分區也說明 Android 安裝成 App 後用「分享」、iOS 用捷徑 App 開啟 `<origin>/add?url={url}`。React 19 會擋下 `javascript:` 的 `href`，所以連結的 `href` 在 effect 中以 `setAttribute` 設定；在設定頁本身點它不執行（`preventDefault`，CSP 本來也不允許），只能拖到書籤列。決定（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）。
- E8：`next` 的檢查（ENTRY-R6）除了規則列出的條件，另外擋下「解碼後的路徑以 `//` 開頭」的值（例如 `/%2F%2Fevil.example.com`）：瀏覽器不會解碼，但前端路由在導向時可能把它解碼成 protocol-relative 的網址。這比規則更嚴格（這種 `next` 登入後回到首頁）。決定（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）。實作是 `auth.SafeNext`，OIDC（AUTH-R17）與反向代理驗證之後要共用它。
- E9：`/add` 的 UI 打開「新增」抽屜並帶入網址後，以 replace 換成媒體庫（`/library`）：重新整理或返回不會再開一次抽屜，網址也不留在瀏覽紀錄的目前項目。提示文字（ENTRY-R2 的「已忽略」、ENTRY-R4 的「沒有網址」）顯示在抽屜上方。決定（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）。
- E10：頁面路由（`/add`、`/share`、`/login`、manifest、service worker）只掛在 TCP listener 的 Web UI 之前（`internal/api/ui.go` 的 `entryPages`），本機 socket（CLI 用）沒有。session 驗證失敗以外的錯誤（例如 DB 錯誤）回 500，log 只記路徑、不記 query。manifest 另外帶 `short_name`、`background_color`、`theme_color` 與 icon 的 `purpose: "any"`；manifest 與 service worker 的 `Cache-Control` 是 `no-cache`。決定（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）。
- E11：登入頁同時接受舊的 `?redirect=`（UI 自己在 session 失效時帶的）與 `?next=`（頁面路由帶的），兩者都交給 `POST /api/auth/login` 的 `next` 檢查（`next` 優先），UI 只前往回應中的 `next`；回應是 `/` 時前往媒體庫。決定（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）。
- 實作狀態（M4，2026-11-23）：`features/ENTRY-entry-points.feature` 的 22 個非 `@manual` 場景（含 Outline 展開）全部通過；6 個 `@ui @manual` 場景（真實瀏覽器的書籤列、在其他網站點書籤、Android 的分享選單）屬於 owner 的人工驗收。iOS 捷徑沒有自動化驗收。

# ADR-0014：Client 的存取控制與不可信輸入

- 狀態：已採納
- 日期：2026-11-12

## 背景

- M1 起就有 Web UI，可以操作加密存放的 API key 和 cookie，也能新增會花錢的任務。原本登入卻排在 M4。
- HTTP API 與 MCP 的 token 原本沒有權限範圍，給 Agent 用的 token 也能修改設定。
- Agent 會讀到字幕內容，字幕中可能藏有 prompt injection。
- Client 會主動抓取外部 URL（RSS、Podcast 音檔、transcript、通知 webhook），也會接受本機路徑和 bundle 檔案。部署在 VPS 上時，尤其要防範 SSRF。

## 決策

### 登入

- M1 起提供單一密碼登入：
  - 初始密碼由環境變數設定，DB 只存 hash。
  - session cookie 設 `HttpOnly`、`SameSite=Lax`；非 GET 的請求都做 CSRF 防護。
  - 連續失敗 5 次後鎖定 1 分鐘。
- compose 預設只把 port 映射到 `127.0.0.1`。
- M4 加上反向代理驗證（只信任指定來源送來的使用者 header）、OIDC 登入與 token 管理 UI。

### Token 權限範圍

HTTP API 與 MCP 共用 bearer token。Token 可以設定到期時間，也可以隨時撤銷；DB 只存 hash。權限分成四個可以組合的 scope：

| scope | 能做什麼 |
|---|---|
| `read` | 查詢影音、字幕、筆記，全文搜尋 |
| `agent` | 領取與提交 LLM 任務 |
| `ingest` | 新增影音、重新處理、匯入字幕（會花錢） |
| `admin` | 設定、secrets、訂閱、token 管理、刪除影音 |

- 給 Agent 用的 token 預設是 `read + agent`。
- MCP HTTP 啟用 DNS rebinding 防護與 Origin 檢查。

### 對外請求

所有對外的 HTTP 請求都走同一個 client：

- 只接受 `http` 和 `https`。
- 在建立連線時檢查實際的目標 IP（netguard 自己解析 DNS、逐一檢查 IP，只連到剛檢查過的 IP；見 ADR-0015），擋掉 loopback、私有網段、link-local 與雲端 metadata 位址。因為檢查的是實際連線的 IP，DNS rebinding 也一併擋掉。
- 私有網段預設封鎖，兩種部署都一樣。自架的 RSSHub、NAS 等主機要寫進設定檔的 allowlist。雲端 metadata 位址一律封鎖，不能加入 allowlist。
- 最多轉址 5 次，每一跳都重新檢查；回應大小與逾時都有上限。
- yt-dlp 的請求不經過這個 client，所以只開放 YouTube 與 Bilibili 的 extractor（ADR-0009）。

### 本機路徑

- API 與 MCP 的 `ingest` 只接受設定好的根目錄（inbox、媒體庫）底下的路徑。比對前先解析 symlink，避免用連結跳出根目錄。
- CLI 獨立模式不限制，因為操作者本來就是本機使用者。

### Bundle 與 UI 渲染

- Bundle 匯入的規則見 ADR-0005：拒絕路徑跳脫與 symlink、限制解壓大小、驗證 hash 與 schema。
- 渲染筆記的 Markdown 時不允許原始 HTML。
- Web UI 加上 CSP，`frame-src` 只開放 YouTube 與 Bilibili 的嵌入網域。

### 不可信的資料

- 字幕、標題、說明、畫面都當作不可信的資料，prompt 中明確標示為資料。
- `submit_task` 只接受符合輸出 schema 的結果。
- Agent 的 token 沒有 `ingest` 和 `admin` scope。就算 Agent 被字幕中的指令誤導，也呼叫不到會花錢或修改設定的 tool。

## 考慮過的選項

- **登入維持 M4，M1 到 M3 只綁 `127.0.0.1`**：port 映射一旦設錯就會直接對外，任何人都能新增任務、花掉 API 額度。
- **Token 只分唯讀與完整權限**：Agent 為了領任務，就得拿到完整權限。
- **依部署 profile 決定是否封鎖私有網段**：家用環境比較方便，但兩邊的行為不同，惡意 RSS 在 WSL 上仍然能讓伺服器去存取路由器等內網設備。

## 影響

- 好處：
  - 從 M1 開始，未登入就無法存取任何 API。
  - 外洩的 Agent token 影響範圍有限。
  - 惡意的 RSS、URL、bundle 碰不到內網和主機檔案。
- 代價：
  - M1 多出登入相關的工作。
  - 接自架的 RSSHub、NAS 時，要在設定檔多加一行 allowlist。
  - 產生 token 時要選擇 scope。

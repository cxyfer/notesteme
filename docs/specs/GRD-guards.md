# GRD：對外請求與路徑防護

- 狀態：ready
- 里程碑：M1
- 相關：plan 第 19 節；ADR-0009、ADR-0014
- 驗收場景：[`features/GRD-guards.feature`](../../features/GRD-guards.feature)

## 目的

Notesteme 會依使用者或外部內容提供的網址主動發出請求，也會接受本機路徑。這些輸入都可能被惡意利用，所以所有對外請求與路徑存取都要經過同一道防護。

## User stories

| ID | Story |
|---|---|
| GRD-01 | As the owner, I want feeds and URLs to be unable to reach my private network, so that a malicious feed cannot probe my router or cloud metadata. |
| GRD-02 | As the owner, I want to allow specific private hosts such as my RSSHub or NAS, so that self-hosted services still work. |
| GRD-03 | As the owner, I want downloads limited in size and redirects, so that a hostile server cannot exhaust my disk or bounce requests around. |
| GRD-04 | As the owner, I want API and MCP clients limited to my media folders, so that they cannot read arbitrary files on the host. |

## 規則

| ID | 規則 |
|---|---|
| GRD-R1 | 所有對外 HTTP 請求（RSS、Podcast 音檔與 transcript、Supadata、通知 webhook）都經過同一個 client，只接受 `http` 與 `https`。 |
| GRD-R2 | 建立連線時檢查實際的目標 IP，至少擋掉 loopback（`127.0.0.0/8`、`::1`）、私有網段（`10/8`、`172.16/12`、`192.168/16`、`fc00::/7`）、link-local（`169.254/16`、`fe80::/10`），以及 CGNAT、multicast 等非公開位址。一個主機名稱解析出多個 IP 時，只連到通過檢查的 IP；全部被擋才算被封鎖。檢查的是實際連線的 IP，所以 DNS rebinding 也一併擋掉。被擋的請求錯誤類別為 `permanent`，訊息為「這個網址指向內部網路，已封鎖。」 |
| GRD-R3 | 設定檔的 `network.allow_private` 可以列出允許的主機或網段。主機名稱比對的是網址中的主機名稱，不分大小寫、完全相同才算（不支援萬用字元）；網段用 CIDR 或單一 IP。雲端 metadata 位址（`169.254.169.254`、`fd00:ec2::254`）一律封鎖：直接把它列進 allowlist 時拒絕啟動；網段包含它時可以設定，但它仍然被封鎖。 |
| GRD-R4 | 最多轉址 5 次，每一跳都重新套用 GRD-R1 到 GRD-R3。超過時錯誤類別為 `permanent`，訊息為「轉址超過 5 次。」 |
| GRD-R5 | 回應大小的上限依用途設定：RSS 與 transcript 10 MiB、音檔 2 GiB；超過時中斷下載，不截斷、不接受部分內容，錯誤類別為 `permanent`，訊息為「回應超過大小上限（10 MiB）。」（依用途顯示上限）。連線逾時 10 秒、整體逾時依用途設定。 |
| GRD-R6 | yt-dlp 只能使用 YouTube 與 Bilibili 的 extractor；generic extractor 關閉。新增其他平台的網址在辨識階段就會被拒絕（見 ING-R1）。 |
| GRD-R7 | API 與 MCP 的 `ingest` 只接受設定的根目錄（inbox、`media_roots`）底下的本機路徑。比對前先解析 symlink 與 `..`；解析後不在根目錄內時回傳 403，訊息為「只能新增媒體資料夾中的檔案。」 |
| GRD-R8 | CLI 在獨立模式下不受 GRD-R7 限制；在 client 模式下（包括本機 socket），路徑由 server 端檢查，同樣適用 GRD-R7。 |

## 介面

- 設定檔：`network.allow_private`（字串陣列）、`media_roots`（絕對路徑陣列）、`inbox`（預設 `<資料目錄>/inbox`）
- 被擋的辨識請求（`POST /api/resolve`）回 422，code 分別為 `blocked_address`、`too_many_redirects`、`response_too_large`、`unsupported_scheme`（轉址到 `http`、`https` 以外的 scheme，訊息為「只支援 http 與 https 網址。」）；路徑不在根目錄內回 403，code 為 `path_outside_roots`。

## 不在範圍內

- Bundle 匯入的防護（M4，見故事地圖 BND，規則見 ADR-0005）。
- Web UI 的 CSP 與 Markdown 渲染規則（見 NFR）。

## 待決問題

- 「Redirects are followed at most five times」場景中，最後的 `feed.xml` 沒有設定回應，就算拿掉轉址上限也一樣失敗，驗證不到規則。建議：補上 `the URL ".../feed.xml" serves "rsshub-feed.xml"`，並斷言回應含「轉址超過 5 次。」。要修改 `.feature`，等 owner 同意（2026-11-13，GRD 切片設計時發現）。
- 「DNS rebinding」場景：連線時才檢查 IP 的設計下，第一次解析到公開 IP 的那次連線會真的送出。場景沒有寫明哪一次解析對應哪一個請求（feed 與音檔），這個場景依賴 ING 的 podcast feed，交給 ING 切片釐清。
- 「Paths outside the media folders are refused」中預期 201 的兩個例子，要等 ING-05（本機檔案的新增）決定檔案是否必須存在、是否要經過 ffprobe；GRD 切片只做到路徑檢查，通過檢查後仍回 501。

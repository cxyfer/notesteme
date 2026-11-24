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
| GRD-R1 | 所有對外 HTTP 請求（RSS、Podcast 音檔與 transcript、Supadata、通知 webhook）都經過同一個 client，只接受 `http` 與 `https`；轉址到其他 scheme 時錯誤類別為 `permanent`，訊息為「只支援 http 與 https 網址。」 |
| GRD-R2 | 建立連線時檢查實際的目標 IP，至少擋掉 loopback（`127.0.0.0/8`、`::1`）、私有網段（`10/8`、`172.16/12`、`192.168/16`、`fc00::/7`）、link-local（`169.254/16`、`fe80::/10`），以及 CGNAT、multicast 等非公開位址。一個主機名稱解析出多個 IP 時，只連到通過檢查的 IP；全部被擋才算被封鎖。檢查的是實際連線的 IP，所以 DNS rebinding 也一併擋掉。被擋的請求錯誤類別為 `permanent`，訊息為「這個網址指向內部網路，已封鎖。」 |
| GRD-R3 | 設定檔的 `network.allow_private` 可以列出允許的主機或網段。主機名稱比對的是網址中的主機名稱，不分大小寫、完全相同才算（不支援萬用字元）；網段用 CIDR 或單一 IP。雲端 metadata 位址（`169.254.169.254`、`fd00:ec2::254`、`100.100.100.200`、Azure 的 `168.63.129.16`）一律封鎖：直接把它列進 allowlist 時拒絕啟動；網段包含它時可以設定，但它仍然被封鎖。 |
| GRD-R4 | 最多轉址 5 次，每一跳都重新套用 GRD-R1 到 GRD-R3。超過時錯誤類別為 `permanent`，訊息為「轉址超過 5 次。」 |
| GRD-R5 | 回應大小的上限依用途設定：RSS 與 transcript 10 MiB、音檔 2 GiB；超過時中斷下載，不截斷、不接受部分內容，錯誤類別為 `permanent`，訊息為「回應超過大小上限（10 MiB）。」（依用途顯示上限）。連線逾時 10 秒、整體逾時依用途設定。 |
| GRD-R6 | yt-dlp 只能使用 YouTube 與 Bilibili 的 extractor；generic extractor 關閉。新增其他平台的網址在辨識階段就會被拒絕（見 ING-R1）。 |
| GRD-R7 | API 與 MCP 的 `ingest` 只接受設定的根目錄（inbox、`media_roots`）底下的本機路徑。比對前先解析 symlink 與 `..`；解析後不在根目錄內時回傳 403，訊息為「只能新增媒體資料夾中的檔案。」 |
| GRD-R8 | CLI 在獨立模式下不受 GRD-R7 限制；在 client 模式下（包括本機 socket），路徑由 server 端檢查，同樣適用 GRD-R7。 |

## 介面

- 設定檔：`network.allow_private`（字串陣列）、`media_roots`（絕對路徑陣列）、`inbox`（預設 `<資料目錄>/inbox`）
- 被擋的辨識請求（`POST /api/resolve`）回 422，code 分別為 `blocked_address`、`too_many_redirects`、`response_too_large`、`unsupported_scheme`（轉址到 `http`、`https` 以外的 scheme，訊息為「只支援 http 與 https 網址。」）；路徑不在根目錄內回 403，code 為 `path_outside_roots`。
- yt-dlp 的 `--use-extractors`（GRD-R6）：yt-dlp 以 fullmatch 比對 extractor 的 IE_NAME，所以名單依用途列出完整名稱：
  - YouTube 單一影片、字幕、音訊：`youtube`。
  - YouTube 播放清單展開（ING-R4）：`youtube:tab,youtube:playlist`。播放清單的 extractor 叫 `youtube:tab` 與 `youtube:playlist`，只寫 `youtube` 時真實的 yt-dlp 會回報 Unsupported URL。
  - Bilibili：`BiliBili`（大小寫依 yt-dlp 的 IE_NAME）。

## 不在範圍內

- Bundle 匯入的防護（M4，見故事地圖 BND，規則見 ADR-0005）。
- Web UI 的 CSP 與 Markdown 渲染規則（見 NFR）。

## 待決問題

- ~~「Too many redirects are refused」場景驗證不到轉址上限~~：已結案（2026-11-14，owner 同意）。最後的 `feed.xml` 補上回應，並斷言 code 與訊息；同時補強轉址、大小上限場景的 code 斷言，新增「metadata 在允許的網段內仍被封鎖」與「轉址到非 http scheme」兩個場景。直接輸入非 http 網址的場景改用有主機名稱的 `ftp://`，但 Go 的 `http.Client` 本身也拒絕非 http scheme，這個場景驗證不到我們自己的檢查，由轉址場景與單元測試把關。
- 本機檔案的防護（2026-11-21，LOCAL 切片實作時決定）：
  - 程式自己讀取本機檔案（計算 `sha256`、讀外掛字幕、串流媒體）時，用 `os.OpenRoot` 在根目錄內開檔，並確認是一般檔案（不是 FIFO、裝置、目錄）。路徑檢查與開檔之間的 symlink 替換因此被擋下。
  - 交給 ffmpeg、ffprobe 的路徑在執行前重新解析檢查，但檢查與工具開檔之間仍有 TOCTOU 的空檔，記為技術債（之後可改成傳入已開啟的檔案描述元）。
  - ffmpeg、ffprobe 一律帶 `-protocol_whitelist file`（避免繞過 netguard 連外），並用 `-format_whitelist` 排除 `hls`、`concat` 等會讀取其他檔案的格式。
  - standalone 模式加入根目錄之外的檔案後，改由 serve 處理時，在執行階段以 `permanent` 拒絕（GRD-R8）。
- ~~「DNS rebinding」場景：連線時才檢查 IP 的設計下，第一次解析到公開 IP 的那次連線會真的送出。場景沒有寫明哪一次解析對應哪一個請求（feed 與音檔），這個場景依賴 ING 的 podcast feed，交給 ING 切片釐清。~~ 已釐清（2026-11-21，PODCAST 切片）：第一次解析是新增時對音檔網址做的只解析、不連線的檢查（見 ING 待決問題），第二次是 `metadata` 下載音檔時的連線，在連線前被擋下，所以完全沒有連到 rebind 的主機。場景依賴「新增時剛好解析過一次」這個順序，比較脆弱；真正的保證由 `netguard` 的 `TestTransportRechecksAfterRebinding`（每次連線都重新檢查）與 `pipeline` 的 `TestPodcastMetadataRebindingBlocked` 把關。場景只斷言 `permanent`，podcast 的 metadata 還沒實作時也會是 `permanent`；建議補上 `shows the message "這個網址指向內部網路，已封鎖。"`（已確認實際訊息相同），需要 owner 同意。
- 「Paths outside the media folders are refused」中預期 201 的兩個例子，要等 ING-05（本機檔案的新增）決定檔案是否必須存在、是否要經過 ffprobe；GRD 切片只做到路徑檢查，通過檢查後仍回 501。
- GRD-R6 的 extractor 限制（`--use-extractors`）只由 `internal/source/youtube` 的單元測試把關：假 yt-dlp 不模擬 extractor，「yt-dlp is never asked to open an arbitrary site」驗證的是辨識階段的拒絕。若要在驗收層驗證，需要新增斷言 yt-dlp 參數的句型。
- 安全修補 C（2026-11-24，依 /goal 採 advisor 建議，待 owner 追認）：
  - 轉址與 allowlist：GRD-R4 的「每一跳重新套用 GRD-R1 到 GRD-R3」保留，但 GRD-R3 的 allowlist 只給起點本身在 allowlist 的請求使用（起點的主機名稱列在 allowlist，或起點是 allowlist 網段內的 IP 字面值）。判斷時不重新解析起點的主機名稱：名稱由外部控制，DNS rebinding 可以讓第二次解析回內網位址；所以沒有列出、只是解析到 allowlist 網段的主機名稱，它的轉址不使用 allowlist（直接連線不受影響），要用轉址時把主機名稱列進 `allow_private`。起點是公開主機時，轉址的每一跳以不含 allowlist 的預設規則檢查：外部的 feed、音檔主機 302 到 `allow_private` 的網段時被擋（「這個網址指向內部網路，已封鎖。」），notesteme 不會替外部主機對內網發 blind GET。這類轉址使用另一個連線池，不沿用先前連到 allowlist 位址的閒置連線。代價：公開主機轉到自架服務（例如公開網址轉到內網的 RSSHub）不再可行，要直接訂閱內網的網址。
  - 預設規則另外擋下 `64:ff9b:1::/48`（RFC 8215 local-use NAT64，內嵌 IPv4 的位置隨前綴長度而定，所以整段擋）、`::ffff:0:0:0/96`（SIIT 的 IPv4-translated；IPv4-mapped `::ffff:0:0/96` 原本就先還原成 IPv4 再判斷）、`100::/64`（discard-only）；metadata 位址加上 `168.63.129.16`（allowlist 不能開放，也不能直接列出）。`fd00:ec2::254` 原本就已涵蓋。

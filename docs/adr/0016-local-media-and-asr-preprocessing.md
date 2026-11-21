# ADR-0016：本機影音的識別、inbox 監看與共用的 ASR 前處理

- 狀態：已採納
- 日期：2026-11-21

## 背景

ING-05 要讓 owner 用三種方式加入本機影音：上傳、把檔案放進 inbox、在 API 或 CLI 中指定路徑。這個需求帶出幾個彼此相關的決定：

- 影音以 `(platform, external_id, part)` 識別（ING-R2），但本機檔案沒有平台給的 ID。
- inbox 要等檔案「大小連續 10 秒不變」才新增（ING-R10），而且要能在 fake clock 與手動 worker 下測試（NFR-TEST-2）。
- 內容相同的檔案不能重複新增（ING-R10），永久刪除後 inbox 也不能把它加回來（DEL-R11）。
- 本機檔案的字幕來源依序是外掛字幕檔、內嵌字幕、ASR（SRC-R14）；超過 provider 上限的音訊要切塊後再接回（SRC-R12）。Podcast 的音檔之後也要走同一條 ASR 路徑。
- 本機路徑來自不可信的輸入（ADR-0014），讀檔與交給外部工具時都要防止逃出根目錄（GRD-R7）。

## 決策

1. **識別用位置，去重用內容。**
   - `external_id` 是檔案的位置：inbox 中是相對 inbox 的路徑；`media_roots` 或其他位置是解析後的絕對路徑；上傳的檔案是清理過的檔名，撞名時加 `-2`。
   - 內容的 `sha256` 另外存在 `items.content_sha256`，用來去重與比對 tombstone。
   - 影音另外記 `source_path`（上傳檔存相對資料目錄的 `uploads/<id>/<name>`，備份還原後仍然有效）與 `origin`（`manual`、`upload`、`inbox`）。
2. **inbox 監看是 worker 的定期工作。**
   - 掃描結果記在 `inbox_entries` 表（路徑、大小、修改時間、`sha256`、判定結果與原因），已有終態紀錄而且大小與修改時間沒變的檔案不再處理。
   - 穩定判定以「第一次看到時的修改時間與現在的較早者」起算，之後大小或修改時間改變就重新起算。所以 server 啟動前就已穩定的檔案，第一次掃描就會新增。
   - 測試中掛在手動 worker 的定期工作上，`RunUntilIdle` 每一輪都掃描；production 由獨立的 goroutine 每 5 秒掃描，避免計算大檔的 `sha256` 卡住任務領取。
   - inbox 自動新增遇到 tombstone 時不建立影音，也不移除 tombstone；手動新增與上傳仍依鍵移除 tombstone。
3. **上傳不經過 Huma 的 multipart。** 在 Huma 之前攔截 `multipart/form-data` 的 `POST /api/items`，先檢查 scope 與 `Content-Length`，再串流寫入 `<data>/tmp`，同時計算 `sha256`。檢查通過後才搬到 `<data>/uploads/<id>/`。
4. **本機讀檔一律在根目錄內開啟。** `pathguard.Guard.Open` 用 `os.OpenRoot`，並確認是一般檔案。交給 ffmpeg、ffprobe 的路徑在執行前重新解析，工具一律帶 `-protocol_whitelist file` 與 `-format_whitelist`。
5. **ASR 前處理由所有平台共用。**
   - 音訊先用 ffmpeg 轉成單聲道 16kHz 的 Opus。仍超過上限時，在靜音處切塊並保留重疊，再依每句的中點歸屬接回。
   - 音訊來源是一個介面：YouTube 用 yt-dlp 下載，本機用原檔，Podcast 用 netguard 下載。
   - 切塊與接回是純邏輯（`internal/audio`），ffmpeg 的呼叫集中在 `internal/media`，argv 固定，由單元測試的 golden 把關。

## 考慮過的選項

- **`external_id` 直接用 `sha256`**：同內容的檔案自然去重，但用 `path` 新增時不能同步雜湊（檔案可能很大或還不存在），而且短代號（`local:<路徑>`）就無法從 ID 推出。
- **只用觀察到的時間判定穩定**：server 重啟後每個檔案都要再等 10 秒；測試中「先推進時鐘、再啟動 server」的場景也無法成立。
- **用 fsnotify 監看 inbox**：Docker 的 bind mount、網路磁碟上不可靠，仍然需要定期掃描作為保底，所以 M1 只做定期掃描。
- **用 Huma 內建的 multipart**：會把檔案溢寫到系統暫存目錄，不在 `/data` 內，也不能邊寫邊雜湊。
- **ASR 只對本機檔案做前處理**：YouTube 的音訊直接上傳也常超過 25 MiB，分開兩條路徑只會讓切塊少一半的使用情境。

## 影響

- 好處：
  - 三種加入方式共用一套去重與 tombstone 規則。
  - Podcast 切片只需要多一個音訊來源。
- 代價：
  - 同一個檔案在 inbox 中搬移或改名，會被當成另一個位置，只靠 `sha256` 去重擋下重複處理。
  - YouTube 的 ASR 多了一次 ffmpeg 轉碼。前處理的參數也進了 Groq 的快取鍵，所以這個決定之前完成的 YouTube 語音轉錄，重跑時不會命中快取（M1 尚未發布，沒有舊資料）。
  - 本機檔案可以被 owner 原地替換：非上傳的本機影音每次 probe 都比對大小與修改時間，不同就重算 `sha256`；外掛字幕的內容 `sha256` 也進快取鍵。上傳的副本不可變，只算一次。
  - 上傳的原始檔無法重新取得，所以備份包含 `/data/uploads`（見 OPS 待決問題）。
  - 交給外部工具的路徑仍有 TOCTOU 的空檔，之後可以改成傳入已開啟的檔案描述元。
  - 切塊的參數（Opus 位元率、靜音門檻、重疊長度）都是預設值，要用真實的 ffmpeg 與 Groq 驗證（M0）。

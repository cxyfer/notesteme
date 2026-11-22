# ADR-0017：Podcast 單集的識別、feed key 與新增時的位址預檢

- 狀態：已採納
- 日期：2026-11-21

## 背景

ING-04 要讓 owner 從 podcast feed 挑出單集來處理，而不必訂閱。這帶出幾個決定：

- 影音以 `(platform, external_id, part)` 識別（ING-R2），但單集的 guid 只在同一個 feed 中唯一，不同 feed 常用 `1`、`2` 這種數字 guid。
- 場景的代號是 `pod:<feed>/<guid>`（steps.md），`source.Ref` 由 `platform` 與 `external_id` 組成，被 8 個地方呼叫。
- 音檔與字幕的網址都來自不可信的 feed（ADR-0014）。對外連線已在 dial 時檢查實際 IP（GRD-R2），但使用者在新增時看不到「這一集的音檔指向內部網路」，要等 `metadata` 失敗才知道。
- 有些 podcast 會動態插入廣告，同一集每次下載的內容與長度都可能不同。

## 決策

1. **單集是 `(podcast, "<feed key>/<guid>", 0)`。**
   - feed key 由 feed 網址最後一段推導：去掉 `.xml` 等副檔名、轉小寫、非英數換成 `-`；遇到 `rss`、`feed` 等泛用名稱就往前取一段，再不行用主機名稱。
   - feed key 存在 `channels(platform='podcast', external_id=<feed key>, url=<正規化的 feed 網址>)`。同一個網址永遠得到同一個 key；不同 feed 撞名時加 `-2`。
   - 這樣 `source.Ref` 不用改就得到 `pod:show/ep-42`，不同 feed 的同名 guid 也不會被誤判為 `already_in_library`。
2. **新增時對選中各集的音檔網址做一次只解析、不連線的檢查**（`netguard.CheckURL`，和連線時共用同一套 allowlist 與封鎖規則）。全部位址都被擋時就回 422 `blocked_address`；DNS 失敗只記 log。這只是提早回報，真正的防護仍在連線時。
3. **音檔在 `metadata` 步驟下載**（上限 2 GiB），記錄 `sha256` 與 ffprobe 量到的長度，並把 feed 中這一集的資料存成快照（`metadata_json.podcast`）。`transcript` 步驟只讀快照，不重新抓 feed。每次 `metadata` 都重新下載；ASR 補下載時不驗證 `sha256`。
4. **`podcast-transcript` 的時間偏移只提醒、不拒絕**（SRC-R15）：分母用 ffprobe 量到的長度，超過 2% 時步驟仍為 `done`，帶「時間可能偏移」的訊息。

## 考慮過的選項

- **feed key 直接用 fixture 名稱或只用 slug**：不同 feed 的數字 guid 會撞在一起。
- **`external_id` 放 feed 網址的雜湊**：代號不再是 `pod:show/…`，而且要改 `source.Ref` 的簽名與所有呼叫點。
- **新增時對音檔送 HEAD**：同一個 HTTP client 會沿用 keep-alive 連線，`metadata` 下載時不會重新解析，rebinding 反而驗證不到；而且部分 CDN 拒絕 HEAD。
- **沿用第一次解析的 IP（DNS pinning）**：下載會連到第一次解析的公開 IP，與「實際連線的 IP 都要檢查」的原則不同，也讓 rebinding 的場景失去意義。
- **偏移超過門檻就拒絕 `podcast-transcript`**：動態廣告只讓時間整段平移，文字仍然正確；拒絕會改走付費的 ASR。

## 影響

- 好處：
  - 不需要 migration。
  - podcast 共用 LOCAL 的 ASR 前處理與媒體端點。
  - 使用者在新增時就知道音檔被封鎖。
- 代價：
  - http 與 https、搬家的 feed（301、`itunes:new-feed-url`）會被當成不同的 feed，可能產生重複的影音。
  - DNS rebinding 的場景依賴「新增時剛好解析過一次」的順序，真正的保證在 netguard 與 pipeline 的單元測試。
  - 每次 `metadata` 都重新下載音檔，沒有磁碟空間檢查。

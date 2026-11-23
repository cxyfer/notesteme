# BND：Bundle 匯出與匯入

- 狀態：ready
- 里程碑：M4
- 相關：plan 第 17、19 節（Bundle 匯入）、第 20 節；[ADR-0005](../adr/0005-provenance-and-bundle-format.md)、ADR-0011、[ADR-0021](../adr/0021-m4-scheduler-subscriptions-and-outbox.md) 第 6 點；UI [媒體庫](../ui/pages.md#3-媒體庫)的批次操作「匯出 bundle」、[影音詳情](../ui/pages.md#4-影音詳情)
- 驗收場景：[`features/BND-bundles.feature`](../../features/BND-bundles.feature)

## 目的

把一部影音的字幕、筆記、畫面與來源紀錄打包成一個 `.notesteme.zip`，搬到另一個實例後原樣匯入。bundle 是不可信的輸入：匯入時先完整驗證結構、路徑、大小、manifest 與每個檔案的雜湊，全部通過才寫入，任何一項不通過就什麼都不留下。

## User stories

| ID | Story |
|---|---|
| BND-01 | As the owner, I want to export an item with its transcripts, notes and provenance as a bundle, so that I can move it to another instance. |
| BND-02 | As the owner, I want bundles imported safely, so that a malicious bundle cannot harm my instance. |

## 規則

| ID | 規則 |
|---|---|
| BND-R1 | 匯出：`GET /api/items/:id/bundle`（`read`）回 200，`Content-Type: application/zip`，`Content-Disposition: attachment; filename="<檔名>.notesteme.zip"`（檔名是代號中的英數字、`-`、`_`，其他字元換成 `-`，最多 80 字元）。CLI `notesteme export <item> --format bundle [--out <path>]` 寫出同樣的內容，沒有 `--out` 時寫到目前目錄的 `<檔名>.notesteme.zip`。內容是 ADR-0005 的 bundle v0：`manifest.json` 加上 `artifacts/<artifact-id>.<ext>`；只放**現行版本**的產出：每個變體與語言的字幕（`notesteme-cues+json`）、筆記（`.md`）、章節與贊助片段（`.json`）、`frames` 產出與其中**選中**的畫面圖片（`artifacts/<frames-id>-<nnn>.jpg`，`nnn` 為三位數畫面編號）。不含媒體檔、候選畫面、被取代的版本。在垃圾桶中的影音回 404 `in_trash`。 |
| BND-R2 | 匯出的內容不含 secrets 與本機路徑：API key、cookie 的內容、token、master key 都不會出現；來源紀錄中是路徑的欄位（例如本機來源的檔案路徑、外部工具的路徑）只保留檔名；`local:` 影音的 `item.external_id` 是檔案內容的 `sha256`，`item.source_name` 是不含目錄的檔名。來源紀錄中「用了 cookie」只保留布林值 `cookies_used`。`item.availability` 照實記錄（ADR-0005：非 `public` 的影音之後不能上傳到 Hub）。 |
| BND-R3 | 匯入：`POST /api/bundles/import`（`admin`），本體是 multipart 的 `file` 欄位；CLI `notesteme import <bundle>`（檔名以 `.notesteme.zip` 結尾，或內容以 zip 簽章 `PK\x03\x04` 開頭時視為 bundle；與 `--item` 一起用時結束碼 2，訊息「匯入 bundle 不能指定 --item。」）。上傳超過 `bundles.import.max_upload_bytes`（預設 64 MiB）回 413 `bundle_too_large`，訊息「bundle 超過 64 MiB 的上傳上限。」（數字依設定）。驗證全部通過之前不寫入 DB 與 `/data/artifacts`：bundle 先解到 `/data/tmp/import-<uuid>/`，通過後在一個寫入交易中建立影音與產出、把檔案移進 artifacts；任何一步失敗時刪除暫存目錄，DB 不變。成功回 201 `{item: {id, ref, title}, imported, skipped}`。 |
| BND-R4 | 項目的名稱與種類（讀取 central directory 時、解壓任何內容之前檢查）：名稱必須是合法的 UTF-8、NFC、不含 NUL 與控制字元、不含 `\`、最多 255 bytes；不能是絕對路徑（以 `/` 開頭，或 `C:` 這類磁碟代號開頭）；任何一段不能是 `..` 或 `.`；只能是 `manifest.json` 或 `artifacts/<檔名>`（`<檔名>` 符合 `^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`，不能再有子目錄；`artifacts/` 這個目錄項目本身可以有，會被略過）。external attributes 的 Unix mode 只接受一般檔案（或上述的目錄項目），symlink、裝置檔、FIFO、socket 一律拒絕。名稱重複（不分大小寫、NFC 正規化後相同）、local header 的名稱與 central directory 不同、兩個項目指向同一個 local header（zip 中對應 hardlink 的情況）、加密的項目、`store` 與 `deflate` 以外的壓縮方式，都拒絕。 |
| BND-R5 | 大小上限以**實際解壓出來的 bytes** 計算（header 宣告的大小不可信）：宣告的解壓大小已經超過上限時直接拒絕；解壓時以 `OpenRaw` 加上自己的 deflate reader 計數，讀到上限加 1 byte 就停止，實際的 bytes 與宣告的不同（多或少）時也拒絕。項目數最多 `bundles.import.max_files`（預設 2,000，含目錄項目）；單一檔案最多 `max_file_bytes`（預設 64 MiB）；所有檔案合計最多 `max_total_bytes`（預設 256 MiB）；`manifest.json` 固定最多 4 MiB；單一檔案解壓超過 1 MiB 之後，解壓大小與壓縮大小的比值不能超過 `max_ratio`（預設 100）。檢查順序是 BND-R4 → 項目數 → 逐檔解壓（單檔上限、合計上限、壓縮比、實際大小與宣告是否相符，依這個順序）→ BND-R6 → BND-R7，回報第一個不通過的項目。 |
| BND-R6 | `manifest.json` 以內建的 JSON schema 驗證：`bundle_version` 必須是 `0`（其他值回 422 `bundle_unsupported_version`）；不允許未定義的欄位（任何一層）；`item.platform` 是 `youtube`、`bilibili`、`podcast`、`local`；`item.title` 最多 500 字元；`artifacts` 最多 500 個，每個的 `id` 是 UUID、`kind` 是 `transcript`、`note`、`chapters`、`sponsor_segments`、`frames` 之一、`variant`（只有 `transcript`）是 `source`、`normalized`、`segmented`、`corrected`、`translated`、`edited` 之一、`lang` 是 BCP 47、`path` 必須列在 `files` 中；`files` 是 `[{path, sha256, size}]`，列出 `manifest.json` 以外的每個檔案。錯誤回 422 `bundle_invalid`，`errors[].path` 是欄位的點號路徑（例如 `artifacts.0.kind`）。 |
| BND-R7 | 檔案與 manifest 必須雙向一致：zip 中每個檔案都列在 `files`、`files` 的每一項都在 zip 中；每個檔案的 `sha256` 與 `size` 和實際內容相同。內容再依格式驗證：字幕以 `notesteme-cues+json` 的驗證器檢查（與 OUT-R13 的匯入相同的上限），JSON 必須能解析，圖片必須能以 JPEG 解碼，筆記必須是合法的 UTF-8。筆記的 Markdown 在 UI 渲染時不允許原始 HTML（ADR-0005）。 |
| BND-R8 | 匯入的每個產出的來源紀錄保留原本的內容，另外加上 `imported_from: {instance_id, app_version, bundle_sha256, imported_at}`（`instance_id`、`app_version` 取自 manifest 的 `created_by`）與 `verified: false`；原本是 `executor: agent` 的仍保留 `model_verified: false`。匯入不建立任何任務，也不執行 pipeline：有匯入產出的步驟記為 `done`（`stage_runs` 的 `executor` 為 `import`），其他步驟沒有執行紀錄；之後要補做的步驟由使用者重跑（LIB-R11）。 |
| BND-R9 | 已經存在的影音（以 `(platform, external_id, part)` 比對）：預設回 409 `bundle_item_exists`，訊息「這部影音已在媒體庫中（<代號>）。要把 bundle 的產出加成新版本，請加上 on_existing=add_versions。」；帶 `on_existing=add_versions`（CLI `--add-versions`）時，內容 `sha256` 與這部影音任何一個版本相同的產出略過（計入 `skipped`），其他的成為新的現行版本，舊版本保留。影音在垃圾桶中時一律回 409 `bundle_item_in_trash`，訊息「這部影音在垃圾桶中。請先還原或永久刪除後再匯入。」。只有 tombstone（已永久刪除）時視為手動新增：照常匯入，並在同一個交易中移除 tombstone（DEL-R11）。 |
| BND-R10 | 錯誤：BND-R4～R7 的錯誤都是 422 `bundle_invalid`（版本不符是 `bundle_unsupported_version`），錯誤類別 `permanent`，回應有 `entry`（出錯的項目名稱，沒有時省略）與下列訊息之一（`<項目>` 是名稱，控制字元換成 `�`，最多顯示 200 字元）：「這不是 Notesteme bundle（無法以 zip 讀取）。」、「bundle 中的項目「<項目>」使用絕對路徑，已拒絕匯入。」、「bundle 中的項目「<項目>」含有「..」，已拒絕匯入。」、「bundle 中的項目「<項目>」不是允許的路徑（只能是 manifest.json 或 artifacts/<檔名>），已拒絕匯入。」、「bundle 中的項目「<項目>」不是一般檔案（symlink），已拒絕匯入。」（括號內另有「裝置檔」、「FIFO」、「socket」）、「bundle 中的項目「<項目>」與「<另一個項目>」共用同一份資料，已拒絕匯入。」、「bundle 中有重複的項目「<項目>」，已拒絕匯入。」、「bundle 中的項目「<項目>」使用不支援的壓縮方式或加密，已拒絕匯入。」、「bundle 的項目超過 2000 個的上限，已拒絕匯入。」、「bundle 中的檔案「<項目>」解壓後超過 64 MiB 的上限，已拒絕匯入。」、「bundle 解壓後合計超過 256 MiB 的上限，已拒絕匯入。」、「bundle 中的檔案「<項目>」壓縮比超過 100 倍的上限，已拒絕匯入。」、「bundle 中的檔案「<項目>」實際大小與宣告的不符，已拒絕匯入。」、「manifest.json 不符合格式：<點號路徑>：<原因>。」、「不支援的 bundle 版本：<值>。這個版本只能匯入 bundle_version 0。」、「bundle 中的檔案「<項目>」沒有列在 manifest 中，已拒絕匯入。」、「manifest 列出的檔案「<項目>」不在 bundle 中，已拒絕匯入。」、「bundle 中的檔案「<項目>」與 manifest 記錄的 sha256 不符，已拒絕匯入。」（數字依設定）。BND-R4 的其他條件（非 UTF-8、非 NFC、含 NUL 或控制字元、含 `\`、超過 255 bytes、`.` 段落、不在允許的路徑中、local header 與 central directory 的名稱不同）都用「不是允許的路徑」那一條。每個項目依序檢查：絕對路徑 → `..` 段落 → 其他名稱條件 → 指向同一個 local header（「共用同一份資料」，在比對 local header 名稱之前）→ local header 名稱 → Unix mode → 加密與壓縮方式；名稱重複在全部項目的名稱都檢查過之後判斷。CLI 遇到這些錯誤時結束碼 1（內容不正確不是命令列參數錯誤，同 `config import`），409 時結束碼 1，413 時結束碼 1。 |

## 介面

- API：
  - `GET /api/items/:id/bundle`：`read`（BND-R1）。
  - `POST /api/bundles/import[?on_existing=add_versions]`：`admin`，multipart `file`（BND-R3）。scope 在讀取本體之前檢查。
- CLI：`notesteme export <item> --format bundle [--out <path>]`、`notesteme import <bundle> [--add-versions] [--json]`。
- MCP：無（bundle 是檔案，不適合經由 tool 傳遞；之後的 Hub MCP 另外設計）。
- UI：媒體庫批次操作列的「匯出 bundle」（多部影音時逐一下載，不合併成一個檔）；影音詳情的「匯出」選單加上 bundle；設定頁「系統」分區的「匯入 bundle」（上傳檔案，顯示 BND-R10 的錯誤訊息）。
- 設定檔（只能寫在設定檔）：`bundles.import.{max_upload_bytes, max_total_bytes, max_files, max_file_bytes, max_ratio}`。大小用 `"64MiB"` 這類寫法，必須大於 0，否則 server 啟動失敗，訊息「設定 bundles.import.<鍵> 必須是大於 0 的值。」。
- manifest（bundle v0，ADR-0005 加上 `files`）：

  ```json
  {
    "bundle_version": 0,
    "item": { "platform": "youtube", "external_id": "…", "part": null, "title": "…",
              "channel": { "external_id": "…", "title": "…" }, "published_at": "2026-11-01",
              "duration_s": 1935, "original_lang": "en", "availability": "public", "source_name": null },
    "artifacts": [ { "id": "<uuidv7>", "kind": "transcript", "variant": "corrected", "lang": "en",
                     "format": "notesteme-cues+json", "path": "artifacts/<uuidv7>.json",
                     "parents": [], "provenance": { "…": "…" } } ],
    "files": [ { "path": "artifacts/<uuidv7>.json", "sha256": "…", "size": 1234 } ],
    "created_by": { "instance_id": "<uuidv7>", "app_version": "0.4.0" },
    "created_at": "2026-11-23T10:00:00Z",
    "license": null
  }
  ```

- 資源類別：同步請求，不進佇列（ADR-0021 第 6 點）。
- 資料表方向：不新增資料表；`stage_runs.executor` 多一個值 `import`；匯出時被引用的版本不需要 `pinned`（匯出是當下的快照）。
- 對 `pipeline.Stages`／`outputs` 的影響：無。匯入的產出不觸發 `outputs`（sidecar、vault）；之後重跑或手動「寫出」時才寫出。

## 與其他規格的關係

- ADR-0005：bundle v0 的格式以本規格的 manifest 為準（加上 `files`、`source_name`、`created_at`），`bundle_version` 在 M5 前可以有不相容的變更。
- DEL-R11：只有 tombstone 時匯入視為手動新增（BND-R9）。
- OUT-R13：字幕內容的驗證器與上限沿用 OUT 匯入外部字幕的規則（BND-R7）。
- SRCH-R2：匯入的字幕產出一樣更新全文搜尋的索引。
- SEC：匯出不含 secrets（BND-R2）；After hook 的 secret 掃描也涵蓋場景寫出的 bundle。
- CLI：`notesteme import <subtitle> --item <item>`（OUT-R13）與 `notesteme import <bundle>` 共用子命令，以檔案判斷（BND-R3）。

## 不在範圍內

- 包含媒體檔的 bundle（ADR-0005：預設不含；M4 不提供選項）。
- 一次匯出或匯入多部影音的單一 bundle。
- bundle 的簽章與作者驗證（`imported_from` 只是紀錄，`verified: false`）。
- 授權（`license`）的選擇與 Hub 上傳的限制（M5）。
- 匯入時合併頻道設定、詞彙表、處理方案（bundle 只有產出）。

## 待決問題

- N1：匯入的 scope（BND-R3）。匯入會建立影音、寫入不可信的檔案，但不花錢也不改設定，`ingest` 也說得通。決定（2026-11-23，依 /goal 採保守預設，待 owner 追認）：`admin`，因為 bundle 來自其他人或其他實例，攻擊面比新增網址大；Agent 的 token 沒有理由匯入 bundle。匯出用 `read`（內容與 `get_transcript`、筆記相同）。
- N2：manifest 加上 `files`（BND-R6、R7）。ADR-0005 的 v0 只有 `artifacts[].path`、`sha256`，筆記引用的圖片沒有地方列雜湊。決定（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：`files` 是完整的完整性清單，`artifacts[].path` 指向其中一項，雜湊只在 `files` 記一次；ADR-0005 在實作切片時同步修訂。
- N3：zip 沒有原生的 hardlink 與裝置檔（BND-R4）。ADR-0005 定的是 `.notesteme.zip`，不改成 tar。對應的威脅寫成「Unix mode 不是一般檔案就拒絕」（產生器可以設定任何 mode bits）與「兩個 central directory 項目指向同一份資料就拒絕」。產品解壓時一律以 `0600` 建立一般檔案、不套用 bundle 中的 mode 與時間，所以就算檢查漏掉，也不會建立 symlink 或裝置檔。
- N4：大小上限的預設值（BND-R5）。個人規模的一部影音：字幕每個變體約 0.1～2 MiB，選中的畫面 20～60 張、每張約 100～300 KiB，合計通常在 30 MiB 以下；256 MiB 與 64 MiB 留了很大的餘裕。壓縮比 100 倍：字幕 JSON 的 deflate 壓縮比大約 5～15 倍，全零的內容是 1,000 倍左右；只對解壓超過 1 MiB 的檔案檢查，避免很小的高度重複檔被誤判。上限可以在設定檔調整，場景用調低的值觸發，不產生大檔。
- N5：已存在的影音（BND-R9）。預設不合併，避免匯入在使用者不知情時把現行版本換掉；`add_versions` 讓匯入的版本成為現行版本（和重跑一樣，舊版本保留、可以比較）。
- N6：tombstone（BND-R9）。tombstone 的目的是避免 inbox 與訂閱自動加回（DEL-R11），匯入是使用者的明確動作，比照手動新增。決定（2026-11-23，依 /goal 採保守預設，待 owner 追認）。
- N7：匯入後不自動執行 pipeline（BND-R8）。自動執行會下載媒體、呼叫付費的 provider，違反「匯入只搬資料」的預期；而且 bundle 可能只有部分步驟的產出。UI 在影音詳情顯示「從 bundle 匯入」與 `imported_from` 的資訊。
- N8：`local:` 影音匯入到另一個實例時沒有媒體檔，影音詳情的播放器顯示「這部影音是從 bundle 匯入的，沒有媒體檔。」；之後放入相同 `sha256` 的檔案時是否自動連結，M4 不做。
- M4-S0：`bundles.import.*` 已解析與驗證（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：大小接受整數或 `B`、`KiB`、`MiB`、`GiB` 的寫法，`max_files`、`max_ratio` 是正整數；不是大於 0 的值時啟動失敗，訊息「設定 bundles.import.<鍵> 必須是大於 0 的值。」（前面加上設定檔路徑）。預設值照 N4：64 MiB、256 MiB、2000、64 MiB、100。`GET /api/items/:id/bundle`（`read`）與 `POST /api/bundles/import`（`admin`）是 501 stub。

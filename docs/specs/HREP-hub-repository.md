# HREP：Hub 內容倉庫、篩選與社群

- 狀態：ready
- 里程碑：M5
- 相關：plan 第 3.2、19、20 節；ADR-0005、ADR-0012、ADR-0014；[ADR-0030](../adr/0030-hub-deployment-and-storage.md)、[ADR-0031](../adr/0031-hub-identity-permissions-and-content.md)、[ADR-0032](../adr/0032-client-hub-protocol-and-privacy.md)；M4 的 BND 規格（bundle 驗證，分支 `docs/m4-tracking-deploy-specs`）
- 驗收場景：[`features/HREP-hub-repository.feature`](../../features/HREP-hub-repository.feature)（真 Hub，閘門 `Given a Hub is running`）

## 目的

Hub 保存使用者分享的字幕、章節與筆記，每份都帶著來源紀錄：同一部影片可以有多個版本，依語言、模型與 prompt 區分，用內容 hash 去重，並記錄「誰的字幕衍生出誰的」。其他人可以依平台、頻道、語言與模型篩選，用投票與檢舉讓好的版本浮上來、壞的版本被移除。上傳的一切都是不可信的輸入。

## User stories

| ID | Story |
|---|---|
| HUB-02 | As the owner, I want to share my corrected subtitles and notes to the Hub with their provenance, so that others can reuse my work. |
| HUB-03 | As a hub member, I want to filter shared subtitles by platform, channel, language and models, so that I find a version I trust. |
| HUB-05 | As the owner, I want my client to check the Hub before paying for ASR, so that I reuse what already exists. |
| HUB-06 | As a hub member, I want to vote on and report shared versions, so that good versions rise and bad ones are removed. |

## 規則

| ID | 規則 |
|---|---|
| HREP-R1 | 分享：`POST /api/v1/shares`（`upload`），multipart 欄位 `bundle`（`.notesteme.zip`）、`attest`（必須是 `true`）、`visibility`（選填）。成功回 201 `{share_id, video: {platform, external_id, part}, versions: [{id, kind, variant, lang, sha256, deduplicated, visibility}]}`，`versions` 依 manifest 中產出的順序。`attest` 不是 `true` 時回 422 `attest_required`「分享前要確認你有權分享這些內容。」。 |
| HREP-R2 | bundle 是不可信的輸入，先依 BND-R4～R7 驗證（路徑、項目種類、大小、壓縮比、manifest schema、雜湊、內容格式），任何一項不通過就回 422 `bundle_invalid`，訊息同 BND-R10，什麼都不留下。上限另外由 Hub 的設定 `bundles.max_upload_bytes`（預設 32 MiB，超過回 413 `bundle_too_large`「bundle 超過 32 MiB 的上傳上限。」）與 BND 的其他上限（Hub 設定 `bundles.*`，預設同 BND）決定。 |
| HREP-R3 | Hub 另外檢查內容能不能分享，都回 422 `not_shareable`：`item.platform` 不是 `youtube`、`bilibili`、`podcast` 時「本機檔案不能分享到 Hub。」；`item.availability` 不是 `public` 時「非公開影音不能分享到 Hub。」；任何產出的來源紀錄 `cookie_required: true`，或沒有這個欄位而 `source` 是 `bilibili-ai` 時「需要登入才能取得的字幕不能分享到 Hub。」；bundle 含有音訊或影片檔（依副檔名與內容簽章）時「bundle 中不能包含媒體檔。」。`license` 不在 `licenses`（預設 `CC-BY-4.0`、`CC-BY-SA-4.0`、`CC-BY-NC-4.0`、`CC-BY-NC-SA-4.0`、`CC0-1.0`）中時回 422 `license_invalid`「不支援的授權：<值>。」。 |
| HREP-R4 | 來源紀錄只保存 ADR-0032 第 2 點的白名單欄位，其他欄位丟掉；`created_by.instance_id` 不保存。每個產出的來源紀錄 JSON 最多 16 KiB，超過回 422 `bundle_invalid`「產出「<id>」的來源紀錄超過 16 KiB。」。 |
| HREP-R5 | 去重：版本的鍵是 `(video, kind, variant, lang, sha256)`（ADR-0031 第 3 點）。鍵已經存在時不建立新版本，回應中的 `deduplicated` 為 `true`，版本的 `uploaders` 多一位；blob 依 sha256 只存一份。同一個人再次上傳完全相同的 bundle 也成功（冪等），不重複計入 `uploaders`。 |
| HREP-R6 | 衍生關係：來源紀錄的 `parents_sha256` 中，在同一部影片已有版本的，記為 `parent`；`hub_origin.version_id` 指到存在的版本時記為 `regenerated_from`。`GET /api/v1/versions/:id` 回傳 `parents: [{id, relation, kind, variant, lang, uploader}]` 與 `children: [...]`；`regenerated_from` 的 parent 另有 `summary: "以 @<上傳者> 的原始字幕重新生成"`。parent 已被移除時仍列出，`state: removed`。 |
| HREP-R7 | 模型欄位：從來源紀錄取出 `transcribe_model`（`source` 那一條線的 `provider/model`，平台字幕是來源 ID，例如 `youtube-manual`）、`correct_model`、`translate_model`（各自是 `provider/model`；`executor: agent` 時用 `model_reported`）。來源紀錄中任何一步是 `executor: agent`、`model_verified: false`，或 `executor: hub` 且 `hub_origin` 原本未驗證時，版本是 `model_verified: false`，否則是 `true`。 |
| HREP-R8 | 上傳時的可見度：`visibility` 省略時依 `content_defaults`（HACC-R14）；指定的值超過站台上限時回 422，`errors[].path` 為 `visibility`，訊息「可見度不能超過站台上限（<上限>）。」；`frames` 的版本不論指定什麼，都是 `content_defaults.frames`（預設 `private`）。 |
| HREP-R9 | 已下架的內容：`(video, sha256)` 在 `removed_hashes` 中時回 409 `removed_content`「這份內容已依下架申請移除，不能再上傳。」，整個 bundle 都不寫入。 |
| HREP-R10 | 撤回：上傳者 `DELETE /api/v1/shares/:id` 回 204。版本沒有其他未撤回的分享時變成 `withdrawn`（不出現在列表，`GET` 回 404 `not_found`），否則只從 `uploaders` 中移除這位。撤回不影響衍生出來的版本。 |
| HREP-R11 | 上傳限流：每個帳號每小時最多 30 次分享、每天合計最多 512 MiB（`rate_limits.shares_per_hour`、`upload_bytes_per_day`），超過回 429 `rate_limited`「上傳太頻繁，請在 <n> 秒後再試。」並附 `retry_after_s`。 |
| HREP-R12 | Hub 不抓取任何來自使用者的網址：manifest 的 `url`、筆記中的連結與圖片、Podcast 的 feed 網址都只當作文字保存與顯示，不建立連線；Hub 唯一的對外連線是 HACC-R1 的 OAuth endpoint（走 netguard）。 |
| HREP-R13 | 投票：`PUT /api/v1/versions/:id/vote {value}`（`read`），`value` 為 `1` 或 `-1`，同一人對同一版本只有一票，再送一次是更新；`DELETE` 收回。不能對自己是上傳者之一的版本投票，回 403 `own_version`「不能對自己上傳的版本投票。」；帳號建立未滿 24 小時回 403 `account_too_new`「帳號建立未滿 24 小時，還不能投票或檢舉。」；每人每小時最多 60 次（`rate_limits.votes_per_hour`）。版本的 `score` 是沒有被停用的帳號的票數總和，回應與列表中都有 `score`。 |
| HREP-R14 | 檢舉：`POST /api/v1/versions/:id/reports {reason, note}`（`read`），`reason` 為 `wrong_language`、`bad_quality`、`spam`、`offensive`、`other`，`note` 最多 1,000 字元。`reason: copyright` 或 `privacy` 回 422 `use_takedown`「版權與隱私問題請改用下架申請。」，`actions: ["提出下架申請"]`。同一人對同一版本已有未處理的檢舉時回 409 `already_reported`「你已經檢舉過這個版本，管理員處理前不能重複檢舉。」。帳號齡的限制同 HREP-R13；每人每天最多 10 筆（`rate_limits.reports_per_day`）。上傳者與其他使用者看不到檢舉者是誰，只有 `moderate` 看得到。 |
| HREP-R15 | 自動隱藏：一個版本有 3 筆未處理的檢舉、來自 3 個帳號齡滿 7 天的不同帳號時（`moderation.auto_hide_reports`、`auto_hide_min_account_age`），版本變成 `hidden`：不出現在列表與查詢中，其他人 `GET` 回 404，上傳者與 `moderate` 看得到 `state: hidden`。`POST /api/v1/admin/reports/:id/decision {decision}`（`moderate`）：`dismiss` 把這個版本的未處理檢舉都結案並恢復 `visible`；`remove` 等同 HACC-R16 的移除。 |
| HREP-R16 | 篩選：`GET /api/v1/versions`（`read`）的參數 `platform`、`channel`（頻道的 `external_id`）、`video`（`<platform>:<external_id>`，Bilibili 加 `/p<n>`）、`lang`、`kind`、`variant`、`transcribe_model`、`correct_model`、`translate_model`、`exclude_unverified`（`true` 時排除 `model_verified: false`）、`sort`（`score`，預設；或 `new`）、`limit`（1～200，預設 50）、`cursor`。值不合法時回 422，`errors[].path` 指到參數（例如 `query.limit`）。結果只含看得到的 `visible` 版本，每項有 `{id, video, kind, variant, lang, transcribe_model, correct_model, translate_model, model_verified, license, score, uploaders, created_at}`；`model_verified: false` 的版本另有 `badges: ["unverified_model"]`。 |
| HREP-R17 | 版本詳情與影片頁：`GET /api/v1/versions/:id` 回傳 HREP-R16 的欄位加上 `provenance`（白名單）、`parents`、`children`、`visibility`、`state`；`uploaders` 只有 `{display_name}`，不含身分、email。`GET /api/v1/videos/<platform>/<external_id>?part=` 回傳 `{video, groups: [{kind, variant, lang, versions: [...]}]}`：這部影片所有看得到的版本，依 `kind`（`transcript`、`chapters`、`note`、`frames`）、`variant`（pipeline 的順序：`source`、`normalized`、`segmented`、`corrected`、`translated`、`edited`）、`lang`（字典序）分組排序，組內依 `score` 由高到低。 |
| HREP-R18 | 取用：`GET /api/v1/versions/:id/download?format=`（`download`）依版本種類渲染：字幕 `srt`、`vtt`、`txt`、`cues-json`，筆記 `md`，章節 `json`；回應有 `Content-Disposition: attachment`、`X-Content-Type-Options: nosniff` 與 `X-Notesteme-License: <SPDX>`。`GET /api/v1/versions/:id/content`（`regenerate`）回傳原始的 cues JSON（只限字幕）與 `X-Notesteme-SHA256`。`removed` 的版本回 410（HACC-R16）。Hub UI 渲染筆記時不允許原始 HTML，標題與字幕一律當作文字。 |
| HREP-R19 | ASR 前查詢（Hub 端）：`GET /api/v1/lookup?platform=&prefix=`（`regenerate`），`prefix` 必須是剛好 4 個小寫十六進位字元，否則回 422（`errors[].path` 為 `query.prefix`）。回傳 `{videos: [{video_key_sha256, versions: [{id, variant, lang, sha256, score, model_verified}]}]}`：`sha256(video_key)`（ADR-0032 第 3 點）以這個前綴開頭的所有影片，每部只列看得到的 `visible` 字幕版本中 `variant` 為 `source` 或 `normalized` 的。回應不含影片的 `external_id` 與標題。 |

## 介面

- API（`/api/v1`，OpenAPI 3.1 在 `GET /api/v1/openapi.json`）：

  | 路由 | 權限 |
  |---|---|
  | `POST /api/v1/shares`、`DELETE /api/v1/shares/:id` | `upload`（撤回只限自己的分享） |
  | `GET /api/v1/versions`、`GET /api/v1/versions/:id`、`GET /api/v1/videos/<platform>/<external_id>` | `read` |
  | `GET /api/v1/versions/:id/download` | `download` |
  | `GET /api/v1/versions/:id/content`、`GET /api/v1/lookup` | `regenerate` |
  | `PUT/DELETE /api/v1/versions/:id/vote`、`POST /api/v1/versions/:id/reports` | `read` |

- Hub 設定檔：`licenses`、`bundles.*`、`content_defaults`、`moderation.*`、`rate_limits.*`。
- 資料表：`videos`、`blobs`、`versions`、`version_blobs`、`shares`、`share_versions`、`derivations`、`votes`、`reports`（ADR-0031 第 5 點）。
- Hub UI（`web/hub`，`@ui @manual`）：
  - 首頁與搜尋：篩選列（平台、頻道、語言、種類、三個模型、排除未驗證的模型）、結果列表（分數、授權、上傳者、`unverified_model` 標示）。
  - 影片頁：依種類與語言分組的版本；兩個版本並排比較，時間帶與字幕句列表用 `web/shared` 的元件（ADR-0008 第 9 點）。
  - 版本頁：來源紀錄、衍生關係（「以 @alice 的原始字幕重新生成」）、下載、投票、檢舉、下架申請的連結。
  - 文案：`model_verified: true` 寫成「由 Client 記錄的模型」，不寫「已驗證」（ADR-0031 第 3 點）。
  - 頁面清單在 `docs/ui/README.md` 第 9 節補上之前，以本規格與 HACC 的 UI 段落為準。

## 不在範圍內

- 線上編輯字幕、留言。
- 全文搜尋字幕內容（Hub 只依 metadata 與模型篩選）。
- 同一部影片的版本合併或自動挑選「最佳版本」（只有分數排序）。
- 媒體檔的保存與串流。

## 待決問題

- R1：Hub 的 bundle 驗證直接沿用 M4 的 BND 規則（HREP-R2），但 BND 是給實例之間搬移用的：Client 送給 Hub 的 bundle 沒有 `created_by.instance_id`（ADR-0032 第 2 點），BND 的 manifest schema 要接受缺少這個欄位，或 Hub 另外放寬。Hub 的上傳上限也比 BND 小（32 MiB 對 64 MiB）。決定（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：M4 合併後，由 Hub 的第一個實作切片在 BND 的驗證器加上「`instance_id` 選填」的選項，並在 handoff 記錄這個衝突點。
- R2：「平台字幕」的轉錄模型用來源 ID 表示（HREP-R7）。平台的自動字幕其實也是平台的 ASR，但無法知道模型。
- R3：分數只是票數總和（HREP-R13），沒有依時間衰減或信任加權；之後依使用情況再調整。
- R4：自動隱藏的門檻（3 個帳號、帳號齡 7 天，HREP-R15）可以在設定檔調整；場景用調低的值與 fake clock 驗證。
- R5：lookup 的前綴長度固定 4 碼（HREP-R19、ADR-0032 第 3 點）。影片數多到每個桶的回應太大時，再評估改成 5 碼並讓 Client 依 Hub 回報的長度送出。
- R6：Hub 上的版本內容（字幕、筆記）在 Hub UI 中只渲染、不執行；Hub 沒有 LLM，所以「字幕中的指示」只影響取用它的 Client 與 Agent（Client 端的處理見 HSHR、HMCP）。
- R7：M5-S0（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：Hub 設定的 `bundles` 是扁平的 `max_upload_bytes`（預設 32 MiB）、`max_total_bytes`（256 MiB）、`max_files`（2000）、`max_file_bytes`（64 MiB）、`max_ratio`（100），不沿用 BND 的 `bundles.import.*` 巢狀（Hub 只有上傳一種用途）；值不正確時「設定檔 … 的欄位 bundles.<鍵> 必須是大於 0 的值。」。`hubblob` 以 sha256 為鍵（`<data>/blobs/<前 2 碼>/<sha256>`，0600，寫入時自己計算雜湊，鍵不是 64 個小寫十六進位字元時一律當作不存在）；S3 只有佔位。
- R8：M5-S3 的實作範圍與細節（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：
  - **S3／S4 的切法**（閘門依 tag 放行，一條規則只屬於一段）：S3 是 R1～R6、R8～R12 與 HACC-R14；S4 是 R7、R16～R19；S5 是 R13～R15。R7 的模型欄位與 `model_verified` 在 S3 的上傳時就決定並存進 `versions`，但它的場景以 `GET /api/v1/versions?video=`（列表，S4）驗證，所以場景在 S4 放行；`GET /api/v1/versions/:id`（R17 的詳情）在 S3 實作，因為 R4、R5、R6、R10 與 HACC-R14 的場景都用到它，R17 的兩個場景（含影片頁）在 S4 一起放行。
  - **R1 的衝突**：bundle 的解析與驗證放在共用套件 `internal/bundle`（BND-R4～R7，之後的 BND 匯入切片直接使用）。`Options.InstanceIDOptional` 讓 Hub 接受沒有 `created_by.instance_id` 的 manifest（有的話仍要是 UUID，Hub 不保存）；BND 匯入維持必填。BND-R6 寫「任何一層都不允許未定義的欄位」，但 R4 的場景帶了 `artifacts.0.provenance.workdir` 並預期成功：`provenance` 在 manifest schema 中是自由的物件，Hub 再依 ADR-0032 第 2 點的白名單過濾（型別不對或超過 512 字元的值也丟掉）；其他層仍然嚴格（含重複的 JSON 鍵、結尾多餘的內容、巢狀超過 64 層）。上傳上限用 Hub 自己的 `bundles.max_upload_bytes`（R7），其他上限沿用 BND 的語意。
  - **檢查順序**：權限（middleware，讀取 body 之前）→ 每小時的分享次數 → `Content-Length` 超過上限直接 413（不讀 body）→ 讀取 multipart（bundle 超過上限 413）→ `attest` → `visibility` → 每日上傳量 → BND-R4～R7 的結構與雜湊 → R3 依序檢查平台、公開與否、需要登入的字幕、媒體檔、產出種類 → 授權 → R4 的 16 KiB（量 manifest 中收到的來源紀錄）→ BND-R7 的內容格式 → 寫入 blob → 一個交易寫入 DB（`removed_hashes` 在交易中檢查）。媒體檔排在內容格式之前：否則 `.mp4` 會因為「不是 JPEG」變成 `bundle_invalid`，而不是 R3 的 `not_shareable`。任何一步失敗時，這次新寫的 blob 若沒有版本引用就刪除，DB 不變（R2、R9 的「什麼都不留下」）。
  - **BND-R10 沒有列出的訊息**：zip 可以讀但沒有 `manifest.json`：「這不是 Notesteme bundle（沒有 manifest.json）。」；內容格式不正確：「bundle 中的檔案「<項目>」不是有效的 <格式>，已拒絕匯入。」（格式是 notesteme-cues+json 字幕、UTF-8 筆記、JSON、JPEG 圖片）；JPEG 另外限制最多 64M 像素（避免小檔案解碼成巨大的圖；安全修補 B 起改為 16M 像素與結構檢查，見 BND 待決問題 N25）。BND 匯入切片沿用這些訊息，或在 BND 的待決問題另外決定。
  - **Hub 不接受的產出**：bundle 的 `sponsor_segments`（R17 的種類只有 transcript、chapters、note、frames）回 422 `not_shareable`「Hub 不接受這種產出：sponsor_segments。」（Client 的分享不會產生它）。沒有 `license` 時回 422 `license_invalid`「不支援的授權：（沒有指定）。」。
  - **multipart**：只接受 `multipart/form-data`，否則 400 `invalid_request`；沒有 `bundle` 欄位時 422 `invalid_request`（`errors[].path` 為 `bundle`）；`attest` 必須剛好是字串 `true`。`visibility` 不合法時是 422 `invalid_request`（同 HACC A12 的欄位錯誤），`errors[].path` 為 `visibility`：不是 `public`、`authenticated`、`private`、`group:<設定檔中的群組>` 時「必須是 …」、群組不存在時「不認得的群組：<名稱>。」、超過站台上限時是 R8 的訊息。
  - **去重與可見度**（2026-11-24 安全修補 A 改寫，依 /goal 採 advisor 建議，待 owner 追認；取代原本「沿用既有版本的可見度」）：R5 的「鍵已經存在時不建立新版本」限縮為**只併入新上傳者看得到、可見度相同的版本**。依序：(1) 上傳者自己已有未撤回分享的版本直接併入（冪等，回應是那個版本的可見度）；(2) `visible` 或 `removed`、儲存的 `visibility` 與這次指定的值完全相同、且上傳者以一般成員身分（不靠上傳者身分或 `moderate`）看得到的版本併入，`uploaders` 多一位；(3) 否則有 `withdrawn` 的版本時在同一個交易中以這次分享的值（可見度、授權、來源紀錄、模型欄位、圖片）恢復成 `visible`，`deduplicated: false`；以它為 child 的衍生關係依新的來源紀錄重建（舊的 `parent`／`regenerated_from` 刪除；別的版本以它為 parent 的關係依內容判斷，保留），分數、票與未處理的檢舉沿用（待 owner 決定是否要歸零）；撤回者在 `GET /api/v1/me/shares` 看不到恢復後的版本時，那個版本只顯示識別欄位、`state: withdrawn`，不透露可見度、上傳者與模型；(4) 其他情況（別人的 `private`、看不到的 `group:<name>`、`hidden`、可見度不同）另建一個版本、只共用 blob，`deduplicated: false`、`visibility` 是自己指定的值。所以回應不會透露別人看不到的版本存在，也不會把選 `private` 的人併進公開版本的 `uploaders`，先 `private` 分享不能吞掉之後別人指定的公開可見度，撤回的內容不能藉再分享永久壓制。版本的鍵因此不再唯一（migration 00006；Postgres 以鎖住影片的列序列化同一部影片的分享）。取捨：`hidden` 的版本被另一個人以相同內容重新分享時會出現新的 `visible` 版本（繞過隱藏，等同新的內容，需要另外檢舉）；`removed` 仍併入（內容已移除，不能藉重新上傳繞過）。**冪等**：同一個人已有相同 bundle（整個檔案的 sha256）且未撤回的分享時，不寫入任何東西，回 201 與那次分享（每個版本 `deduplicated: true`）；撤回之後再分享相同的 bundle 是新的分享。
  - **影片的 metadata**（2026-11-24 安全修補 A，依 /goal 採 advisor 建議，待 owner 追認）：影片的標題、頻道與其他欄位仍由第一位分享者決定、之後不更新（規格沒有更正或投票決定 metadata 的機制，不另外發明）。最小防護：`item.external_id` 依平台檢查格式（YouTube `^[A-Za-z0-9_-]{11}$`、bilibili `^BV[0-9A-Za-z]{10}$`、Podcast 1～512 個非控制字元，與 `hubclient` 相同），`item.channel.external_id` 不能有空白或控制字元，否則 422 `bundle_invalid`；標題與頻道名稱保存前把控制字元換成空白、去掉雙向控制字元（U+202A～202E、U+2066～2069、U+200E／200F、U+061C）、合併空白並截到 300 個字元。頻道 ID 不檢查各平台的格式（`UC…` 等），因為 Podcast 與舊資料沒有固定格式；第一位分享者仍能把影片掛到錯誤的頻道，待 owner 決定是否要以平台 API 驗證或允許版主修正。
  - **衍生關係**：`parents_sha256` 連到同一部影片中有這個內容的所有版本（任何狀態）；新版本的 sha256 出現在既有版本的 `parents_sha256` 中時補上連結。`hub_origin.version_id` 只有在版本存在、上傳者看得到、不是 `removed` 時才記為 `regenerated_from`（否則可以藉衍生關係確認私人版本存在）；`executor: hub` 而 `hub_origin` 不成立時，`model_verified` 保守地是 `false`。版本詳情的 `parents`／`children` 只列出查看者看得到的版本（`withdrawn` 不列，`removed` 依可見度列出、`state: removed`），每項有第一位上傳者的顯示名稱。
  - **模型欄位（R7）**：沿著 `parents_sha256` 往上游找（同一個 bundle 的產出，或 Hub 上同一部影片已有的版本）：`transcribe_model` 取 `source` 那一步（沒有模型時用 `source` 欄位，例如 `youtube-manual`），`correct_model` 取 `corrected`，`translate_model` 取 `translated`；上游任何一步未驗證，版本就是 `model_verified: false`。
  - **圖片**：沒有被產出引用的檔案（畫面、筆記的圖片）必須是 JPEG；檔名以 `artifacts/<產出 ID>-` 開頭的記為那個產出的 `image` blob，其他的通過驗證但不保存。
  - **可見度（HACC-R14）**：上傳者與有 `moderate` 的人（PAT 要帶 `moderate`）一律看得到；`hidden` 只有他們看得到；`withdrawn` 一律看不到；`group:<name>` 看帳號當下的群組。看不到與不存在回完全相同的 404；看得到的 `removed` 版本回 410 `removed`（HACC-R16）。
  - **限流（R11）**：以 DB 中成功的分享計算（含撤回的，Hub 重新啟動後不歸零）：每小時的次數在讀取 body 之前檢查，`retry_after_s` 是最早的那次分享滿一小時的剩餘秒數；每日位元組數在讀完 bundle 之後檢查（`已上傳 + 這次 > 上限`）。被拒絕的分享不計入（一般限流 HACC-R18 仍然計入每個請求）。安全修補 B 起改為計入，見 R11。
  - **稽核紀錄**：分享與撤回不寫入（HACC-R17 的 `action` 清單沒有它們）。
  - 資料表：`internal/hub/hubstore/sqlite/migrations/00004_repository.sql`（`videos` 另有 `key_sha256` 給 R19，`shares` 另有 `bundle_size` 給 R11，`share_versions` 另有 `position`）；`removed_hashes` 以 `(video, sha256)` 為鍵，只擋同一部影片。Postgres 仍是佔位，`hubstore/storetest` 新增的內容倉庫案例實作 Postgres 時要一起通過。
- R9：M5-S4 的實作細節（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：
  - **可見度**：列表、影片頁與 lookup 先在 SQL 預先過濾（`state = 'visible'`；visibility 在 viewer 不靠上傳者身分就看得到的集合中——匿名只有站台公開時的 `public`，登入的人是 `public`、`authenticated` 與自己的 `group:<name>`，`moderate`（PAT 要帶它）不過濾；或 viewer 有這個版本未撤回的分享），分頁才不會被看不到的版本佔掉；取出後每一筆再經過與版本詳情相同的 `canSee`。`hubrepo.Listable` 與 `CanSee` 的一致性由單元測試鎖住。三者都只列 `visible`：`hidden` 的版本即使上傳者與 `moderate` 看得到，也只在 `GET /api/v1/versions/:id` 出現（R15「不出現在列表與查詢中」），影片頁同列表。
  - **參數驗證**：query 參數在 handler 內驗證（Huma 自己的驗證錯誤沒有 `errors[].path`），422 `invalid_request` 的 `errors[]` 依參數順序列出所有錯誤。`platform`、`kind`、`variant`、`sort`、`exclude_unverified`（`true`／`false`）只接受列出的值（大小寫有別）；`lang` 是 2～8 個字母加最多 4 段 `-xxx`、最多 35 字元；`video` 是 `<platform>:<external_id>`，結尾的 `/p<n>`（1～9999）是分P；`channel` 與三個模型是最多 256 個字元、沒有控制字元的文字；`limit` 是 1～200 的十進位整數（最多 3 位）。重複的參數只取第一個。
  - **排序與分頁**：`score` 依分數由高到低，同分依建立時間由新到舊、再依 ID；`new` 依建立時間與 ID。`next_cursor` 是 base64url 的 JSON（排序方式、排序分數、建立時間、ID），只有還有下一頁時才有；取 SQL 掃到的最後一列，不是可見度過濾後的最後一列。cursor 綁定排序方式，解碼失敗、有多餘欄位、排序不符或超過 512 bytes 時回 422（`query.cursor`）。cursor 沒有簽章：竄改只會換到別的位置，每一頁都重新過濾可見度。已知限制：分數在翻頁之間改變（S5 的投票）時可能重複或漏掉版本。
  - **影片頁**：`GET /api/v1/videos/<platform>/<external_id>?part=` 的影片不存在、平台不認得、`external_id` 不合法，或沒有任何看得到的 visible 版本時，一律回相同的 404 `not_found`「找不到這部影片。」（不洩漏只有私人版本的影片存在）；`part` 不是 1～9999 時 422（`query.part`）。`video` 另有 `title`、`channel_external_id`、`channel_title`（上傳的文字）。種類依 `transcript`、`chapters`、`note`、`frames`，沒有變體的產出排在有變體的之後；組內同分時新的在前。列表與影片頁的版本另有 `sha256`。
  - **下載**：`format` 省略時字幕是 `srt`、筆記 `md`、章節 `json`；`frames` 沒有可下載的格式，與不合的格式都回 422（`query.format`）。附件檔名是 `<platform>-<external_id>[-p<n>]-<kind>[-<variant>][-<lang>].<副檔名>`，每段只保留 `[A-Za-z0-9._-]`（其他換成底線）、最多 64 字元、去掉開頭的點；回應另有 `Cache-Control: private, no-store` 與 `Content-Security-Policy: default-src 'none'; sandbox`。`cues-json` 是重新渲染的 cues JSON；`/content` 回傳原始 blob 的 bytes（與 `X-Notesteme-SHA256` 相符），不是字幕的版本回 422 `invalid_request`「只有字幕版本有原始的 cues JSON。」。讀出的 blob 與版本的 sha256 不符時回 500，不送出內容。不支援 Range（規格沒有要求）。
  - **lookup**：`platform` 必填（422 `query.platform`）；`prefix` 必須完全符合 `^[0-9a-f]{4}$`。以 `key_sha256 >= prefix AND < prefix || 'g'` 的範圍查詢，回應不截斷；沒有看得到的版本的影片不列出，所以「桶中只有別人的私人版本」與空桶的回應 byte-for-byte 相同（`{"videos":[]}`）。版本依分數、建立時間排序；回應有 `Cache-Control: private, no-store`。次數限制沿用一般限流（HACC-R18，以帳號計），沒有另外的設定鍵；R5 的桶大小問題出現時再加。
- R10：M5-S5 的實作細節（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：
  - **檢查順序**（投票與檢舉）：權限（middleware）→ 版本看得到（不存在與看不到相同的 404，`removed` 410，同版本詳情；body 的內容不影響這個結果）→ 只接受 `visible`（上傳者與版主看得到的 `hidden` 回 409 `version_hidden`「這個版本目前被隱藏，不能投票或檢舉。」）→ body（422，`errors[].path`）→ 自己的版本（只限投票）→ 帳號齡 → 次數 → 寫入（重複檢舉在寫入的交易中判斷）。**檢舉 `removed` 的版本回 410**（已經移除，沒有可以處理的）。
  - **投票（R13）**：`PUT …/vote` 回 200 `{value, score}`，`value` 不是 1 或 -1 時 422（`errors[].path` 為 `value`）；`DELETE …/vote` 回 204，沒有投過票也是 204。上傳者收回不受 `own_version` 限制（本來就沒有票）。每小時的次數算 PUT 與 DELETE（成功的），存在 DB 的 `vote_events`（滑動的一小時，重新啟動不歸零）；檢查與寫入不在同一個交易，並發時最多多一次。
  - **分數**：`versions.score` 在投票、停用、恢復的同一個交易中以「沒有被停用的帳號的票」**整筆重算**（不用增減量），所以列表的 SQL 不用改。驗收 seeding 直接寫入的 `score` 在第一次投票時被重算的值取代。R9 的已知限制仍在：分數在翻頁之間改變時，以分數排序的列表可能重複或漏掉版本。
  - **檢舉（R14）**：`reason` 是 `copyright`、`privacy` 時先回 422 `use_takedown`（`actions: ["提出下架申請"]`），其他不認得的值是 `invalid_request`（`errors[].path` 為 `reason`）；`note` 最多 1,000 個字元，除了換行與 tab 不能有控制字元。成功回 201 `{id, state: "open"}`。同一人對同一版本的未處理檢舉以 partial unique index 保證只有一筆。每天的次數是滑動的 24 小時內建立的檢舉（含已處理的）。規格沒有禁止上傳者檢舉自己的版本，所以允許。
  - **自動隱藏（R15）**：在建立檢舉的同一個交易中判斷；**帳號齡以檢舉當時計算**（`reporter.created_at <= report.created_at - min_age`），結果才穩定；**已停用的檢舉者不計入**；只把 `visible` 變成 `hidden`。
  - **處理檢舉**：`POST /api/v1/admin/reports/:id/decision {decision, note}` 回 200 `{id, state, version_state}`，`decision` 是 `dismiss` 或 `remove`；已處理的回 409 `report_decided`「這筆檢舉已經處理過了。」（條件式的 UPDATE），不存在 404「找不到這筆檢舉。」。`dismiss` 把這個版本的未處理檢舉都結案（`dismissed`）並把 `hidden` 恢復成 `visible`（不論是自動還是手動隱藏的；`removed` 不變）；`remove` 同 HACC A13 的移除（`removal.source: report`，理由是這筆檢舉的 `reason`），檢舉都結案為 `actioned`。`note` 目前只驗證、不保存（下架申請的 `note` 有保存）。
  - **手動隱藏**：`POST /api/v1/admin/versions/:id/hide` 回 200 `{id, state: "hidden"}`，已經隱藏時同樣 200（不寫稽核紀錄），`removed`／`withdrawn` 回 409 `version_state`，不存在 404。**待 owner 決定**：規格沒有「取消隱藏」的路由，手動隱藏、又沒有未處理檢舉的版本目前無法由 API 恢復（只有新的檢舉再被 `dismiss`）；照規格不加路由，也不加稽核的 action。
  - 資料表：`internal/hub/hubstore/sqlite/migrations/00005_community.sql`（`votes`、`vote_events`、`reports`、`takedowns`，`versions` 加 `removal_source`、`removal_reason`，重建 `group_members` 與 `audit_log`，見 HACC A13）；`hubstore.CommunityStore` 的共用案例在 `storetest/community.go`（含並發投票），Postgres 實作時要一起通過（Postgres 的「整筆重算」與條件式的 UPDATE 在 read committed 下仍然正確，但投票的次數檢查要放進交易或加鎖）。
- R11：上傳的資源保護（安全修補 B，2026-11-24，依 /goal 採 advisor 建議，待 owner 追認）：（1）失敗的分享也計入每小時的次數（`shares_per_hour`）：讀了本體之後以 400、409、413、422 失敗的分享記在 Hub 程序的記憶體中（滑動一小時，Hub 重新啟動後歸零；成功的分享仍以 DB 計算），每小時的檢查是兩者的合計，`retry_after_s` 取兩者中最早的一次滿一小時的剩餘秒數；401、403、429 與 5xx 不計入。只看 `Content-Length` 就回的 413 也計入（不另外區分）。（2）全域的上傳並行名額 2（常數，不開設定鍵）：每個分享最多佔 `max_upload_bytes` 的上傳加上 `max_total_bytes` 的解壓內容，每個帳號同時最多 1 個（避免一個帳號的兩個慢速上傳佔住所有名額），讀取本體有 5 分鐘的期限（`http.Server` 只有 `ReadHeaderTimeout`；32 MiB 約需 110 KB/s），名額在讀取本體之前取得，滿了不排隊，直接回 429 `rate_limited`「Hub 正在處理其他上傳，請在 5 秒後再試。」並附 `retry_after_s: 5`（ADR-0032 第 1 點：限流一律 429，OpenAPI 不變）。請求大小原本就有上限（`Content-Length` 超過 `max_upload_bytes` 加 64 KiB 直接 413，本體以 `MaxBytesReader` 限制）。（3）圖片的驗證見 BND 待決問題 N25：Hub 不再完整解碼 JPEG。

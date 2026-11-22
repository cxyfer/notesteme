# HREP：Hub 內容倉庫、篩選與社群

- 狀態：draft
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
| HREP-R17 | 版本詳情與影片頁：`GET /api/v1/versions/:id` 回傳 HREP-R16 的欄位加上 `provenance`（白名單）、`parents`、`children`、`visibility`、`state`；`uploaders` 只有 `{display_name}`，不含身分、email。`GET /api/v1/videos/<platform>/<external_id>?part=` 列出這部影片所有看得到的版本，依 `kind`、`variant`、`lang` 分組。 |
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

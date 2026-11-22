# SPB：SponsorBlock

- 狀態：ready
- 里程碑：M3
- 相關：plan 第 5、10、12 節；ADR-0005、ADR-0014、[ADR-0020](../adr/0020-m3-stages-notes-and-vault.md)
- 驗收場景：[`features/SPB-sponsorblock.feature`](../../features/SPB-sponsorblock.feature)

## 目的

從 SponsorBlock 取得影片中的業配、自我宣傳等片段，讓章節、關鍵畫面與筆記略過它們；字幕本身保持完整。SponsorBlock 只是輔助資料，查不到或查詢失敗都不能擋住後續步驟。

## User stories

| ID | Story |
|---|---|
| SPB-01 | As the owner, I want sponsor segments left out of my frames and notes, so that ads do not end up in my notes. |

## 規則

| ID | 規則 |
|---|---|
| SPB-R1 | `sponsorblock` 步驟接在 `translate` 之後（ADR-0020）。只對 YouTube 影片執行；其他平台與本機檔案為 `skipped`（`not_applicable`）。`stages.sponsorblock.enabled: false` 時為 `skipped`（`off`）。預設 `enabled: true`。 |
| SPB-R2 | 以 hash 前綴查詢，不送出影片 ID：`GET <base_url>/api/skipSegments/<sha256(影片 ID) 的前 4 個十六進位字元>?categories=<JSON 陣列>&actionTypes=["skip"]`，再從回應中挑出 `videoID` 與這部影片相同的項目。`base_url` 由設定檔的 `sponsorblock.base_url` 決定（預設 `https://sponsor.ajay.app`），連線走 netguard 的 client（ADR-0014），回應上限 1 MiB、逾時 10 秒、不跟隨轉址。 |
| SPB-R3 | 採用的類別由 `stages.sponsorblock.categories` 決定，預設 `[sponsor, selfpromo, interaction]`；可以用的值是 `sponsor`、`selfpromo`、`interaction`、`intro`、`outro`、`preview`、`music_offtopic`、`filler`，其他值在處理方案與覆寫中回 422，`errors[].path` 指到該項（例如 `stages.sponsorblock.categories.0`）。只採用 `actionType` 為 `skip` 的片段；重疊或相接的片段合併；超出片長的部分截掉；合併後短於 1 秒的片段捨棄。 |
| SPB-R4 | 查詢結果存成產出（`kind: sponsor_segments`，ADR-0020 第 4 點），依開始時間排序。`GET /api/items/:id` 與 MCP `get_item` 回傳 `sponsor_segments: [{category, start, end}]`（時間為 `HH:MM:SS.mmm`）；沒有片段時是空陣列，步驟沒有執行過時沒有這個欄位。SponsorBlock 回 404 代表沒有片段，是正常結果。 |
| SPB-R5 | 查詢失敗（連線錯誤、逾時、429、5xx、回應不是預期的 JSON、超過大小上限）時視為沒有片段：步驟為 `done`，存成空的產出，不重試；影音顯示提醒「無法取得 SponsorBlock 片段（HTTP 503），這次當作沒有片段。」（連線錯誤時括號內是「連線失敗」），動作為「重新查詢 SponsorBlock」。後續步驟照常執行。 |
| SPB-R6 | 來源紀錄另外記錄 `sponsorblock.base_url`、`sponsorblock.categories`（以逗號連接）、`sponsorblock.outcome`（`ok`、`not_found`、`error`）與 `sponsorblock.fetched_at`。 |
| SPB-R7 | 一個 cue 有一半以上的時間落在片段內，就算是「片段內的 cue」：不放進 `chapters` 與 `note` 的輸入（CHAP-R5、NOTE-R5）；`frames` 不在片段內選圖（FRM-R6）。字幕的各個變體與下載的字幕都不刪除這些 cue。 |
| SPB-R8 | 從 `sponsorblock` 或更早的步驟重跑時一律重新查詢（片段會被社群更新，不使用快取）；新的結果和現行產出完全相同時不建立新版本。從 `chapters` 之後的步驟重跑不會重新查詢。 |

## 介面

- 處理方案：`stages.sponsorblock.{enabled, categories}`
- 設定檔：`sponsorblock.base_url`（新的頂層區段，只能寫在設定檔，避免 API 把請求導到任意主機）
- API：`GET /api/items/:id` 的 `sponsor_segments`；`POST /api/items/:id/rerun` 的 `from: sponsorblock`
- MCP：`get_item` 的 `sponsor_segments`
- 資源類別：`download`（ADR-0020 第 3 點）

## 不在範圍內

- 送出或投票 SponsorBlock 片段。
- 在播放器中自動跳過片段（UI 只在時間帶上標示，屬於影音詳情的 UI 切片）。
- Bilibili 的類似服務（例如 BilibiliSponsorBlock）：M3 不支援，`not_applicable`。

## 待決問題

- B1：SponsorBlock 片段要不要也排除在 M2 的 `segment`、`correct`、`translate` 之外。plan 第 5 節寫「SponsorBlock 標記的片段不送 LLM 步驟，字幕在這些片段保留原文」，故事地圖 SPB-01 只排除筆記輸入與選圖。決定（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：M3 只排除 `chapters`、`frames`、`note`（SPB-R7）；M2 的步驟已經實作並通過驗收，`sponsorblock` 又排在它們之後（ADR-0020），不回頭改。之後要省 M2 的 token，再把 `sponsorblock` 移到 `metadata` 之後並另立決策。
- B2：「片段內的 cue」用「一半以上的時間」判斷（SPB-R7）。跨越片段邊界的短句會被保留或排除其一，不會被切開；場景只用完全在片段內與完全在片段外的 cue。
- B3：查詢失敗不重試（SPB-R5）。SponsorBlock 的公開伺服器經常短暫失敗，重試會拖慢整個 pipeline；使用者可以用「重新查詢 SponsorBlock」動作重跑（`POST /api/items/:id/rerun {from: sponsorblock}`）。
- B4：預設類別是 `sponsor`、`selfpromo`、`interaction`。`intro`、`outro` 常含有影片的重點摘要，`music_offtopic`、`filler` 的標記品質不一，預設不排除。
- B5：harness 在每個場景預設登記沒有任何片段的假 SponsorBlock（ADR-0020 第 2 點），所以既有的 YouTube 場景在 `sponsorblock` 加入後只會多一個 `done` 的步驟，不會連到外部網路。
- B6：M3-S0 的暫時行為（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：`sponsorblock` 在 `pipeline.Stages` 中，預設開啟；在 SPB 的切片於 `stageImplemented` 登記之前，YouTube 影音的這一步是 `skipped`（`not_implemented`），其他平台照 SPB-R1 是 `not_applicable`，`enabled: false` 是 `off`。設定檔的 `sponsorblock.base_url` 已經接受（預設 `https://sponsor.ajay.app`，只接受 http／https、不含帳號密碼與查詢字串），harness 每個場景都登記了沒有片段的假 SponsorBlock（B5）。實作時：在 `stageImplemented` 登記 `(sponsorblock, local)`，YouTube 場景的 `sponsorblock` 會由 `skipped` 變成 `done`，要確認預設 tag 的場景沒有斷言這一步的狀態。

# GEM：Gemini 直接生成

- 狀態：draft
- 里程碑：M3
- 相關：plan 第 12、24 節（M0 的 Gemini 限制）；ADR-0003（例外）、ADR-0014、ADR-0019、[ADR-0020](../adr/0020-m3-stages-notes-and-vault.md) 第 7 點；UI [處理方案](../ui/pages.md#8-處理方案)（第 6 步筆記）
- 驗收場景：[`features/GEM-gemini.feature`](../../features/GEM-gemini.feature)

## 目的

公開的 YouTube 影片可以直接把網址交給 Gemini 寫筆記，不下載媒體、不取得字幕，省下時間與 ASR 費用。產出仍套用同一份筆記模板、跳脫規則與自動檢查（NOTE）；超出 Gemini 限制時說明原因，並提供改用一般流程的動作。

## User stories

| ID | Story |
|---|---|
| GEM-01 | As the owner, I want Gemini to write a note straight from a YouTube URL, so that I can skip downloading and transcription when that is good enough. |

## 規則

| ID | 規則 |
|---|---|
| GEM-R1 | `stages.note.engine` 是 `pipeline`（預設）或 `gemini`。`gemini` 時 `stages.note.provider` 必須是 `type: gemini` 的 provider（內建 id `gemini`），否則處理方案與覆寫回 422，`errors[].path` 是 `stages.note.provider`，訊息「engine: gemini 需要 type 為 gemini 的 provider。」。 |
| GEM-R2 | `engine: gemini` 時，`transcript`、`normalize`、`segment`、`correct`、`translate`、`chapters`、`frames` 都是 `skipped`（`gemini`），不下載字幕與媒體（假 yt-dlp 只被呼叫取得資訊）；`metadata`、`sponsorblock`、`note`、`publish` 照常執行。筆記沒有字幕檔，影片資訊表不列字幕。 |
| GEM-R3 | 呼叫前先檢查，不符合時 `note` 為 `blocked`（`needs_action`），動作為「改用一般流程重新處理」（以 `engine: pipeline` 的單次覆寫從 `transcript` 重跑）：不是 YouTube 影片時訊息為「Gemini 直接生成只支援 YouTube 影片。」；`availability` 不是 `public` 時為「Gemini 只能讀取公開的 YouTube 影片，這部影片的狀態是 <availability>。」；片長超過 `stages.note.gemini.max_duration`（預設 `1h`）時為「影片長度 1:30:00 超過 Gemini 直接生成的上限 1:00:00。」。 |
| GEM-R4 | 請求：`POST <base_url>/v1beta/models/<model>:generateContent`（標頭 `x-goog-api-key`，預設 `base_url` 為 `https://generativelanguage.googleapis.com`），`contents` 的第一個 part 是 `file_data: {file_uri: "https://www.youtube.com/watch?v=<id>"}`，接著是指示與 `<video_metadata>`（標題、頻道、平台章節、SponsorBlock 片段以「略過這些時間範圍」列出）；`generationConfig.responseMimeType` 為 `application/json` 並帶 `responseSchema`。連線走 netguard 的 client，回應上限 16 MiB，逾時 10 分鐘。 |
| GEM-R5 | 輸出是 `{sections: [{level, title, start, body}], self_check: [string]}`，`start` 是 `MM:SS` 或 `H:MM:SS`。驗證同 NOTE-R3，但以 `start` 取代 `cue`：`start` 必須落在 `[0, 片長)`，同一層不遞減；內文的 `[[t:MM:SS]]` 換成時間連結，同樣要在片長之內；`[[cue:N]]` 與 `frames` 不允許。不符合時依 LLM-R5 重試（例如「sections[1].start 12:00 超過片長 10:00」）；重試用盡時 `note` 為 `failed`（`permanent`），訊息「Gemini 回傳的筆記沒有通過驗證（sections[1].start 12:00 超過片長 10:00）。」，動作為「重新產生筆記」與「改用一般流程重新處理」。 |
| GEM-R6 | 產出套用同一份模板、NOTE-R8 的跳脫與 NOTE-R7 的自動檢查。frontmatter 另外有 `notesteme_engine: gemini` 與 `notesteme_timing: model`。來源紀錄記錄 `engine: gemini`、`timing: model`、`provider`、`model`、`gemini.file_uri` 與 `parents`（`metadata`，有片段時加 `sponsor_segments`）；ADR-0003 的例外只限這些標題時間（ADR-0020 第 7 點），不產生任何字幕。 |
| GEM-R7 | Gemini 的錯誤：429、5xx 與連線錯誤是 `retryable`（PIPE-R3）；400 `INVALID_ARGUMENT`、403 `PERMISSION_DENIED`（影片太長、無法存取）是 `needs_action`，`note` 為 `blocked`，訊息「Gemini 無法處理這部影片（HTTP 400 INVALID_ARGUMENT），請改用一般流程。」，動作為「改用一般流程重新處理」；缺少 API key 時同 LLM 的處理（`blocked`，動作「設定 API key」）。API key 讀取順序同 ADR-0019：`NOTESTEME_GEMINI_API_KEY`，再來是 `PUT /api/settings/services` 存放的 key。 |

## 介面

- 處理方案：`stages.note.{engine, provider, model, prompt}`、`stages.note.gemini.max_duration`；內建 prompt `note-gemini-default`（PRM）
- 設定檔：`llm.providers.<id>.type: gemini`（ADR-0019 的 provider 表多一種 `type`）
- API：同 NOTE；`POST /api/items/:id/rerun` 的「改用一般流程」是 `{from: transcript, overrides: {stages: {note: {engine: pipeline}}}}`
- UI：處理方案第 6 步的「筆記引擎」選項（[pages.md 第 8 節](../ui/pages.md#8-處理方案)）
- 資源類別：`llm`

## 不在範圍內

- Bilibili、本機檔案與 Podcast 的直接生成（Gemini 只讀 YouTube 網址）。
- 上傳媒體到 Gemini 的 File API（那是一般流程的替代 ASR，不在 M3）。
- 用 Gemini 產生字幕。

## 待決問題

- G1（`@pending-m0`）：Gemini 直接讀 YouTube 網址的限制要等 M0 實測（plan 第 24 節）：可接受的片長（暫定 `max_duration: 1h`）、`unlisted` 能不能讀（暫定只有 `public`）、每日配額與 token 計算。依賴這些數值的場景標記 `@pending-m0`；限制被 Gemini 拒絕時由 GEM-R7 處理，所以預檢的數值不準也不會讓步驟卡住。
- G2：時間戳來自模型（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：是 ADR-0003 的例外，只能用在筆記標題與內文的時間連結（ADR-0020 第 7 點），frontmatter 的 `notesteme_timing: model` 讓使用者篩出這類筆記。
- G3：`engine: gemini` 時略過 `transcript` 等步驟（GEM-R2），所以 MCP `get_transcript` 與下載字幕對這類影音沒有內容；要字幕就改用一般流程。
- G4：`sponsorblock` 照常執行，片段以文字的時間範圍交給 Gemini 略過；Gemini 不一定遵守，筆記不再另外過濾。
- G5：Gemini 的 wire 與 anthropic、openai 不同（`contents`、`responseSchema`），`features/internal/fakellm` 不處理它；驗收測試用 `features/internal/fakegemini`，並讓 4.6 的 LLM 步驟接受 provider `gemini`（steps.md 4.15）。
- G6：費用估算（ING-08）在 M3 只列「Gemini 直接生成」一項，以影片長度乘 M0 量到的每分鐘 token 數估算；在那之前 `estimate_usd` 為 `null`。

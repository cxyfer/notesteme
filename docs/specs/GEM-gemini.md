# GEM：Gemini 直接生成

- 狀態：ready
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
- G7：M3-S0（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：設定檔的 `llm.providers.<id>.type` 接受 `gemini`（預設 `base_url` 為 `https://generativelanguage.googleapis.com`）；一般的 LLM client 對 `type: gemini` 回 permanent 錯誤，避免用 openai 的格式送出。**內建的 `gemini` provider 還沒有加入**：加入會改變 provider 清單（`GET /api/providers` 與 PRF 的驗證），留給 GEM 的切片；在那之前，GEM 的 Background（`provider: gemini`）在閘門解除後會回 422「找不到 LLM provider」。GEM-R1 的 type 檢查經 `pipeline.ProviderTypes`（`llm.Registry.LLMProviderType`）實作；目錄沒有 type 資訊時略過。harness 已登記假 Gemini（`harness.TestGeminiKey`、環境變數 `NOTESTEME_GEMINI_API_KEY`），影片資訊還要由 GEM 的切片從假 yt-dlp 接上。（M3-S7 已加入內建 provider、client 與影片資訊的同步，見 G8、G9。）
- G8：M3-S7 的 client 與步驟（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：
  - 內建 provider `gemini`（`type: gemini`，`base_url` 預設 `https://generativelanguage.googleapis.com`）加入 `llm.Builtin` 與 `pipeline.BuiltinProviders`；key 讀 `NOTESTEME_GEMINI_API_KEY`，再來是 `PUT /api/settings/services`（設定頁的白名單已有 `gemini`）。處理方案編輯器的 provider 建議清單（`KNOWN_PROVIDERS`）**不加** `gemini`：編輯器目前只有 segment、correct、translate，選了也只會失敗；筆記引擎的 UI 屬於 NOTE／UI 的切片。`gemini` 的 provider 用在 Gemini 直接生成以外的步驟（含 prompt playground）時，執行期以 permanent 失敗，訊息「type 為 gemini 的 provider 只能用於 stages.note.engine: gemini。」；處理方案不另外回 422（GEM-R1 只規定反方向）。
  - 請求（GEM-R4）：`contents[0].parts` 依序是 `file_data`（一律用正規化的 `https://www.youtube.com/watch?v=<id>`，不用使用者輸入的網址）、指示（prompt `note-gemini-default` 加上引擎規則）、`<video_metadata>`（`title`、`channel`、`duration`、平台章節 `chapters`、SponsorBlock 片段 `skip_ranges`，引擎規則說明「略過這些時間範圍」）與驗證失敗的說明；不使用 `systemInstruction`。`responseSchema` 用 Gemini 的 OpenAPI 子集（大寫型別、沒有 `additionalProperties`、有 `propertyOrdering`），回應仍以程式內的 JSON schema 驗證（schema 以外的欄位、`cue`、`frames` 一律拒絕）。模型名稱只接受 `[A-Za-z0-9._-]`（不能改變端點路徑）。筆記語言依 NOTE-R5 的規則，以 `{{target_lang}}` 與引擎規則交給模型。
  - 逾時：GEM-R4 的 10 分鐘等於任務的租約（`queue.DefaultLease`），實際上限是「租約剩下的時間減 1 分鐘」（同 LLM 的 chunk，約 9 分鐘），到期為 `retryable`；Gemini 用獨立的連線池，等待回應標頭的逾時是 10 分鐘（anthropic、openai 維持 5 分鐘）。不跟隨轉址，回應上限 16 MiB（超過為 `permanent`）。
  - 錯誤分類（GEM-R7 的補充）：400 而 `details[].reason` 是 `API_KEY_INVALID`、401 → `blocked`，動作「設定 API key」；404 → 「更換模型」（LLM-R11）；其他 400 與 403 依 GEM-R7；狀態名稱只取 `error.status`（`[A-Z_]`，不符時用 HTTP 的狀態文字），`error.message` 一律不記錄。`promptFeedback.blockReason`（輸入被拒）→ `blocked`（`needs_action`），訊息「Gemini 拒絕處理這部影片（<原因>），請改用一般流程。」，動作「改用一般流程重新處理」；`finishReason` 為 `SAFETY`、`RECITATION`、`BLOCKLIST`、`PROHIBITED_CONTENT`、`SPII`、`IMAGE_SAFETY` 或 `MAX_TOKENS`、沒有 candidate、沒有文字時視為沒有結構化輸出（同 openai 的 refusal），依 LLM-R5 重試，用盡時是 GEM-R5 的失敗。`permanent` 的錯誤帶動作時，步驟一併記錄（GEM-R5 的兩個動作；之前沒有 permanent 錯誤帶動作）。
  - 用量：`promptTokenCount` → 輸入、`candidatesTokenCount + thoughtsTokenCount` → 輸出、`cachedContentTokenCount` → 快取讀取。計價表沒有 Gemini（G6：`estimate_usd` 維持 `null`）；TPM 限流的預扣以「片長秒數 × 300 token」估計影片的輸入，等 M0 實測（G1）。
  - GEM-R3：`availability` 是空值時視為公開（M1 建立的影音沒有記錄）；不知道片長時不擋（由 GEM-R7 處理）；`availability` 不是 `[a-z_]` 時訊息寫 `unknown`。GEM-R5 的時間格式只接受 `MM:SS`（分、秒都小於 60）與 `H:MM:SS`；同一層不遞減的規則同 NOTE（換到新的 level 2 時 level 3 重新計算）；內文的 `[[cue:N]]` 與不在片長內的 `[[t:…]]` 是驗證失敗。
  - `POST /api/items/:id/rerun {from: note}` 在 `engine: gemini` 時可以執行（「重新產生筆記」）；`engine: pipeline` 在 NOTE 實作之前仍回 501。
- G9：M3-S7 與 NOTE（M3-S4）的分工（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：筆記的模板、跳脫、自動檢查與 `note` 產出屬於 NOTE。M3-S7 做到 GEM-R5 的驗證為止：通過驗證的筆記交給 `pipeline.writeGeminiNote`，在 NOTE 合併之前它以 `permanent` 失敗，訊息是 `pipeline.GeminiNoteRendererMissing`。驗收 harness 依這個訊息把 `the stage "note" of … is "done"` 算成 pending，並讓 anthropic、openai 的 `answers "note"` 在 `pipeline.StageAvailable("note")` 之前 pending（「改用一般流程」的場景要 NOTE 的一般流程）。所以 `@M3` 的 GEM 場景中，422、Bilibili、缺 key、重試用盡 4 個通過；寫筆記、時間重試、429 停在 `is "done"`，R6 與跳脫停在 NOTE 的 `the note of …` 樁，「改用一般流程」停在 anthropic 的 `answers "note"`。NOTE 合併時：`writeGeminiNote` 改用 NOTE 的渲染（`StartMS` 取代 cue 的時間、`llm.GeminiTimeRefs` 的 `[[t:MM:SS]]` 換成時間連結、frontmatter 加 `notesteme_engine`、`notesteme_timing`，來源紀錄見 GEM-R6），閘門會自己失效（訊息不再出現、`StageAvailable("note")` 為 true）。NOTE 實作 `the note of … has the frontmatter:`、`does not contain` 之後，R6 與跳脫的兩個 GEM 場景會因為還沒有筆記而由 pending 變成 failed：S4 與 S7 之中後合併的一方要在同一次推送中把 `writeGeminiNote` 接上渲染。TPM 限流的預扣可能超過 provider 的 `tpm`：`Limiter` 在額度閒置時放行單次超量的請求，不會一直等待。`Given Gemini accepts YouTube videos up to …` 只對 GEM 的場景放行；假 Gemini 的影片資訊在 `World.SyncTools` 時從假 yt-dlp 同步。

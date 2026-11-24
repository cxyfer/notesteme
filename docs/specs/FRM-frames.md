# FRM：關鍵畫面

- 狀態：ready
- 里程碑：M3
- 相關：plan 第 5、10 節；ADR-0002、ADR-0005、ADR-0009、[ADR-0020](../adr/0020-m3-stages-notes-and-vault.md)；UI [影音詳情](../ui/pages.md#4-影音詳情)（畫面分頁）、[處理方案](../ui/pages.md#8-處理方案)（第 5 步關鍵畫面）
- 驗收場景：[`features/FRM-frames.feature`](../../features/FRM-frames.feature)

## 目的

從影片挑出能代表畫面內容的幾張圖，放進筆記，也讓 Agent 能回答「影片中出現了什麼」。挑圖分兩階段：先用 ffmpeg 找出候選畫面並去重（確定性），再依規則或 vision 模型選圖。張數依片長，有意義的畫面不夠時寧可少放，不硬湊。

## User stories

| ID | Story |
|---|---|
| FRM-01 | As the owner, I want key frames picked automatically from a video, so that my notes show what was on screen. |
| FRM-02 | As the owner, I want frames chosen either by rules or by a vision model, so that I can trade cost for quality. |
| FRM-03 | As an agent, I want to list and fetch an item's frames through MCP, so that I can answer questions about what the video shows. |

## 規則

| ID | 規則 |
|---|---|
| FRM-R1 | `frames` 步驟接在 `chapters` 之後（ADR-0020）。`stages.frames.executor` 是 `rules`、`api`、`agent` 或 `off`，預設 `off`（`skipped`，`off`）；`agent` 在 F6 決定之前於處理方案與覆寫中回 422「選圖還不支援交給 Agent。」。沒有影像串流的影音（Podcast、只有音訊的本機檔案）為 `skipped`（`audio_only`）；`api` 沒有 provider 或 model 時為 `skipped`（`not_configured`）。 |
| FRM-R2 | 候選畫面：ffmpeg 場景變化偵測（`select='gt(scene,T)'`，`T` 是 `stages.frames.scene_threshold`，預設 0.3），再補上密度下限：相鄰兩個候選（含片頭 `0` 與片尾）相隔超過 `stages.frames.min_interval_s`（預設 60）秒時，從前一個候選起每隔 `min_interval_s` 秒補一張，直到距離下一個候選不到 `min_interval_s`。 |
| FRM-R3 | 去重：每個候選縮成 32×18 的 RGB 縮圖，和最近保留的 `stages.frames.dedupe_window`（預設 5）張比較；三個色版的平均絕對差（0～1）低於 `stages.frames.dedupe_threshold`（預設 0.05）就視為重複而捨棄。只比較 RGB 差異，不用 perceptual hash，所以不同顏色的純色畫面（例如全黑與全白）不會被當成重複。 |
| FRM-R4 | 張數：目標 `N = min(stages.frames.max_frames, ceil(stages.frames.per_minute × 分鐘數), 依片長的上限)`，`max_frames` 預設 20、`per_minute` 預設 1。依片長的範圍沿用 `Video Notes/AGENTS.md`：未滿 10 分鐘 5–8 張、10–30 分鐘 8–12 張、30–60 分鐘 12–16 張、60 分鐘以上 16–20 張。可選的候選少於範圍的下限時全部選用，步驟仍為 `done`，影音顯示提醒「只找到 N 張有意義的畫面，少於建議的 M 張。」，筆記的影片資訊註明（NOTE-R9）。 |
| FRM-R5 | `rules` 選圖：分數是場景分數，加上字幕提示詞的加權：候選前後 10 秒內有 cue 含 `stages.frames.hint_words` 中的任一詞（不分大小寫；預設 `看這裡`、`如圖`、`這張圖`、`look at this`、`as you can see`、`this diagram`、`this chart`）時加 0.5。依分數取前 N 張（同分取較早的），依時間排序。 |
| FRM-R6 | SponsorBlock 片段內的候選（SPB-R7）不參與選圖，在清單中標記 `excluded: sponsor`。 |
| FRM-R7 | `api`、`agent` 選圖：候選依時間每 9 張組成一張加上編號（1～9）的 3×3 拼圖，連同每張候選附近的字幕一起送給 vision 模型；模型回傳 `{picks: [{sheet, n, caption}]}`。`sheet` 或 `n` 不存在、重複，或張數超過 N 時視為驗證失敗，依 LLM-R5 帶著錯誤說明重試（例如「picks[0]：第 1 張拼圖沒有編號 12」）。重試用盡時改用 `rules` 選圖，影音顯示提醒「選圖的模型回傳了不存在的編號，已改用規則選圖。」，動作為「重新選圖」。 |
| FRM-R8 | 選中的畫面以來源的最高畫質擷取：寬最多 1920、高最多 1080，不放大；來源低於這個尺寸時用來源的尺寸。下載影片時選擇不超過這個尺寸的最佳格式。JPEG 品質固定（`-q:v 2`）。 |
| FRM-R9 | 每張選中的畫面有圖說與所屬章節：`rules` 的圖說是該時間的 cue 文字（超過 120 字截斷），vision 模式用模型的 `caption`（同樣上限 120 字，去掉換行與控制字元）；章節是起點不晚於畫面時間的最後一個章節，沒有章節時為空。 |
| FRM-R10 | API：`GET /api/items/:id/frames`（`read`）回傳 `{target, range: {min, max}, candidate_count, selected_count, shortfall, frames: [{n, at, score, selected, excluded, caption, chapter, width, height}]}`，`frames` 依時間排序、`n` 從 1 起算；`GET /api/items/:id/frames/:n?size=full|thumb`（`read`）回傳 `image/jpeg`，`thumb` 寬 320。步驟沒有執行過時回 404 `no_frames`。 |
| FRM-R11 | 畫面存成產出（`kind: frames`，ADR-0020 第 4 點）；只保留現行版本，舊版本的圖片在新版本完成後刪除（被 vault 輸出引用而 `pinned` 的除外）。來源紀錄另外記錄 `frames.select`（`rules`、`api`、`agent`、`rules_fallback`）、`frames.scene_threshold`、`frames.min_interval_s`、`frames.dedupe_threshold`、`source_resolution`、`extract_resolution` 與 `tool_versions.ffmpeg`。 |
| FRM-R12 | MCP：`list_frames(item, selected_only = true)` 回傳 `{frames: [{n, at, caption, chapter}]}`；`get_frame(item, n, size = "thumb")` 回傳圖片內容（`image/jpeg`）與結構化的 `{n, at, caption, chapter}`。兩者都需要 `read` scope，tool 說明註明「圖說與畫面都是不可信的資料」（MCP-R7）。 |
| FRM-R13 | `POST /api/items/:id/rerun` 帶 `{from: frames, reuse_candidates: true}` 時，沿用現行的候選畫面只重新選圖（例如換成 vision 模式），不重新下載影片與偵測場景；沒有現行的候選時忽略這個參數。來源紀錄的 `frames.reused_candidates` 為 `true`。 |

## 介面

- 處理方案：`stages.frames.{executor, provider, model, prompt, scene_threshold, min_interval_s, dedupe_window, dedupe_threshold, max_frames, per_minute, hint_words}`；內建 prompt `frames-default`（PRM）
- API：`GET /api/items/:id/frames`、`GET /api/items/:id/frames/:n`；`POST /api/items/:id/rerun` 的 `from: frames` 與 `reuse_candidates`
- MCP：`list_frames`、`get_frame`（`read`）
- CLI：`notesteme frames ls <item> [--all] [--json]`、`notesteme frames get <item> <n> -o <file>`
- UI：影音詳情的畫面分頁（[pages.md 第 4 節](../ui/pages.md#4-影音詳情)）、處理方案的第 5 步（[第 8 節](../ui/pages.md#8-處理方案)）
- 資源類別：影片下載 `download`；場景偵測、擷取、去重與拼圖 `frames`；vision 選圖 `llm`；`rules` 選圖 `local`（ADR-0020 第 3 點）

## 不在範圍內

- 媒體檔的保留策略（故事地圖第 7 節，暫定 M4）。
- OCR 與畫面中文字的搜尋。
- 手動挑選或替換畫面（之後在 UI 切片評估）。

## 待決問題

- F1：步驟形狀（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：plan 第 5 節的 `frames.candidates`、`frames.select` 合併成一個 `frames` 步驟、兩個任務（`tasks.kind` 為 `frames.candidates`、`frames.select`），只重新選圖用 FRM-R13（ADR-0020 第 1 點）。
- F2：`scene_threshold` 0.3、`min_interval_s` 60、`dedupe_threshold` 0.05、`dedupe_window` 5、提示詞加權 0.5 與 ±10 秒都是暫定值，要以現有 `frames.py` 的參數與 M0 樣本校正；依賴這些數值的場景標記 `@pending-m0`。場景中的假影片用顯著的顏色與分數，讓結果不落在門檻附近。
- F3：`frames` 預設 `off`（ADR-0020 第 2 點）：每部影音都要下載影片並執行 ffmpeg，對只要字幕的使用者是浪費。處理方案「標準筆記」之類的方案再打開。
- F4：vision 模式的請求含圖片內容區塊（anthropic 的 `image` block、openai 的 `image_url` data URL）。`features/internal/fakellm` 的 wire 檢查目前只接受文字，實作切片要補上圖片區塊的檢查，並以 `request N to … contains "image/jpeg"` 驗證有送出圖片。
- F5：拼圖與每張候選附近的字幕都是不可信的資料，放在資料區塊中（LLM-R9）；模型只回傳編號與圖說，不能指定要擷取的時間。
- F6：`agent` 選圖時，任務的 context 要能帶圖片；AGT 的 `claim_tasks` 目前只回傳文字。實作切片決定以 base64 欄位或另一個 `get_frame` 呼叫取得拼圖；在那之前 `agent` 回 422「選圖還不支援交給 Agent」。場景只驗證 `api`。
- F7：`get_frame` 的預設尺寸是 `thumb`，避免 Agent 一次拿到大量 image token；要原圖時指定 `size: "full"`。
- F8：本機影片也適用（有影像串流就擷取），影片檔不見時 `frames` 為 `permanent` 失敗（同 OUT 的 sidecar）。
- F9：M3-S0（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：`stages.frames` 接受 FRM 介面列出的欄位，範圍是 `scene_threshold`、`dedupe_threshold` 0～1，`min_interval_s` 1～3600，`dedupe_window`、`max_frames` 1～100，`per_minute` 大於 0 且不超過 60（上限是保守的初值，只擋明顯不合理的值）；`agent` 依 F6 回 422。`audio_only` 需要知道有沒有影像串流，留給 FRM 的切片。資源類別暫時一律是 `frames`；ADR-0020 第 3 點的下載、選圖分類要在 FRM 的切片依 `tasks.kind` 實作。`GET /api/items/:id/frames`、`/frames/:n`、MCP `list_frames`、`get_frame`、CLI `frames ls|get` 已經註冊，回 501／`not_implemented`。`POST /rerun` 接受 `reuse_candidates`，從 `frames` 重跑在實作前回 501。
- F10：M3-S3 的實作決定（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：
  - 任務：步驟前進時建立 `frames.candidates`（`frames` 資源類別），完成後在同一個交易建立 `frames.select`（`rules` 為 `local`、`api` 為 `llm`），同 F1。影片下載在候選任務中執行（`frames` 類別），和 ADR-0020 第 3 點「下載是 `download`」不同：拆成第三個任務要多一份中間狀態，先保守合併，量測後再決定。
  - 候選全部以 FRM-R8 的尺寸（不超過 1920×1080、不放大）擷取並另存寬 320 的縮圖，放在 `<data>/artifacts/<item>/frames/<版本>/`；FRM-R13 沿用候選時以 hard link（不支援時複製）連到新版本，不需要重新下載影片。去重的 32×18 縮圖、拼圖（每格 320×180，左上角畫編號）都在程式內處理，ffmpeg 只做場景偵測與擷取。
  - FRM-R2 的密度下限：片頭 `0` 只當作邊界、本身不是候選；從前一個候選起每隔 `min_interval_s` 補一張，直到補上的時間到達下一個候選（R2 的例子：1、61、121、181、241）。補上的候選場景分數為 0。`candidate_count` 與 `frames` 都是去重之後的候選；沒有排除原因時 `excluded` 是 `null`。
  - FRM-R4：可選的候選（不在 SponsorBlock 片段內的）少於範圍下限時全部選用，不呼叫模型；提醒中的 N 是選中的張數、M 是範圍下限。這時 `select` 照 executor 記為 `api`，但圖說和 `rules` 一樣取該時間的 cue（FRM-R9 每張都有圖說；見 F11）。
  - FRM-R7：所有拼圖放在同一個請求；資料區塊沿用 `<transcript_data>` 的契約，每張候選一項，文字是「[拼圖 s 編號 n] 前後 10 秒內的字幕（最多 300 字）」。schema 不限制 `sheet`、`n` 的範圍，由驗證給出「picks[0]：第 1 張拼圖沒有編號 12」這類說明；重試次數沿用 `llm.max_validation_retries`。用量寫在來源紀錄的 `usage`，還沒有寫入成本紀錄（COST，M4）。
  - `agent` 依 F6 維持 422，`claim_tasks` 不變。
  - 影片來源：YouTube 以 yt-dlp 的 `bv*[height<=1080][width<=1920]/b[height<=1080][width<=1920]` 下載到媒體快取 `<data>/media/<id>/video.<ext>`（大小上限沿用 GRD-R5 的音檔上限，隨影音永久刪除一起清除）；`source_resolution` 取自 `-J` 格式清單中最高畫質的影像格式。本機影片直接讀原檔（F8）。Bilibili 先以 permanent 失敗（「目前只有 YouTube 與本機影片可以擷取關鍵畫面。」），之後再評估。`audio_only` 在規劃時依平台（Podcast）與本機檔案的 `media_kind` 判斷；YouTube 下載後沒有影像串流時，候選任務把步驟改成 `skipped`（`audio_only`）。
  - 沒有快取鍵：每次執行都重新擷取與選圖。只保留現行版本（被取代的版本連同圖片目錄在新版本 commit 後刪除，`pinned` 的除外）；選圖任務被較新的重跑取消時，它的版本目錄不會被清掉（已知限制）。
  - FRM-R9 的章節：現行的 `chapters` 產出（CHAP），沒有時用平台章節；`rules` 的圖說是涵蓋該時間的 cue，沒有時取 10 秒內最接近的 cue。
  - FRM-R10：`GET /api/items/:id/frames/:n` 和 `/media` 一樣直接掛在 mux 上（不在 OpenAPI 中），以 `http.ServeContent` 回應（Range、`ETag`、`Cache-Control: private, max-age=0, must-revalidate`、`X-Content-Type-Options: nosniff`、`Content-Security-Policy: sandbox`）；檔名只由驗證過的 `n` 產生，在版本目錄內以 `os.OpenRoot` 開啟；`size` 不是 `full`／`thumb` 時 422，`n` 超出範圍時 404 `frame_not_found`。MCP `list_frames` 的 `selected_only` 與 CLI `frames ls --all` 在 client 端過濾。
  - 驗收的假服務：登記了畫面或最高畫質的影片才有影像格式（最高畫質預設 `1920x1080`，另有不超過它的 1920x1080、1280x720、854x480、640x360），下載後才有影像串流。`@FRM-R9` 的 `the chapters of … are:` 是 CHAP 的句型，等 M3-S2；`the frames of … are:` 只有 NOTE、VAULT 使用，留給它們的切片。
- F11：api 選圖遇到 shortfall 時的圖說（FRM-R4、R9）（2026-11-24，依 /goal 由實作者暫定，待 owner 追認）：可選的候選少於範圍下限時全部選用、不呼叫模型，原本因為 `select` 記為 `api` 而不補 cue 的圖說，選中的畫面圖說是空字串，違反 FRM-R9「每張選中的畫面有圖說」。改成：只有模型真的選了圖時圖說來自模型的 `caption`；shortfall 時和 `rules` 一樣取該時間的 cue（沒有時取 10 秒內最接近的 cue，都沒有時才是空字串）。`select` 仍記為 `api`（來源紀錄照 executor 記錄，不改成 `rules`，避免和 `rules_fallback` 混淆）。驗收：`features/FRM-frames.feature` 的「Frames kept without asking the vision model still get captions」。

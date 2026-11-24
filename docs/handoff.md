# 交接：開發進度（M1～M5 與 Web UI）

> 更新：2026-11-24。M1～M5 的後端、Client Web UI（UI-0～UI-8）與 Hub Web UI 都已推送到 master；945 個自動化場景（預設 tag 315＋`@M3,@M4,@M5` 630）全部通過（本機與 CI 驗證）。安全審查的 16 項發現已修補（第 1 節「安全修補 A／B／C」）。`@manual`、`@pending-m0`、真實樣本與真實服務的驗收屬 owner，做不到的清單在第 2 節。GitHub Actions 自 2026-11-23 約 16:30 起因帳務問題（付款失敗或額度上限）無法啟動 job，所有驗證都在本機做。依 owner 的 /goal（MVP 階段不再開 PR）每個切片完成後直接推送 master。2026-11-22 前的 PR #1～#19（LOCAL、PODCAST、ESTIMATE、M2 S0～S2、S6a 等）已合併，之後的切片見 `git log`。**接手請先讀第 2 節的「2026-11-24 現況」。**
> 用途：讓下一個 session 或協作者不用重讀全部對話就能接手。內容會隨切片推進而更新；規則與決策以規格、ADR 為準，這裡只做索引與摘要。

## 1. 現況

- **安全修補 B：bundle 與 Client 分享（2026-11-24，分支 `fix/bundle-share-security-hardening`，決定都標「依 /goal 採 advisor 建議，待 owner 追認」）**：
  - JPEG 解碼放大（Client 與 Hub 共用的 `internal/bundle`）：驗證只做結構檢查（SOI／EOI、baseline 標記、最多 4 個 scan、`DecodeConfig` 尺寸），拒絕 progressive；單張 16M 像素、合計 256M 像素；合計壓縮比；Client 匯入先拒絕未引用的檔案，選中的畫面先檢查擷取尺寸，縮圖的完整解碼失敗回 `bundle_invalid`；Hub 失敗的分享計入每小時額度、全域上傳名額 2 且每帳號 1、本體讀取期限 5 分鐘（滿了 429）。量測（8192×8192 單色 JPEG，786 KB、deflate 後 1 KB）：每張驗證 278 ms／64 MiB → 31 µs／0 MiB；審查的 20 張 bundle 6.0 s／1280 MiB → `Read` 以合計壓縮比直接拒絕（即使調高 `max_ratio`，`ValidateContents` 84 µs／17 KiB）；Hub 一次含 8192×8192 的分享 201、397 ms、67 MiB → 422、8 ms、3 MiB。結構檢查沒有用真實的 ffmpeg 輸出驗證（環境沒有 ffmpeg），只用 Go 的 encoder 與手工樣本。BND N25、HREP R11。
  - ASR 前查詢命中後 Hub 知道是哪一部影片：不改協定（sha256 定址仍對應到版本），更正說明並換成 `hub-precheck-v2`（確認過 v1 的要重新確認），HSHR 場景只把 v1 換成 v2；不做誘餌請求。HSHR S20、ADR-0032 第 3 點的修訂。
  - 分享的筆記：刪掉 frontmatter 的 `notesteme_*` 與閱讀狀態、模板的 read button；`ShareToHub` 送出前解開 bundle 掃描 secret（環境變數＋資料庫，`secrets.Services.Values`）、本機影音 ID、read button。BND 匯出的洩漏掃描也加入資料庫的 secret。HSHR S21。
  - 加固：`sanitizeProvenance` 的鍵名正規化後做子字串比對（BND N26）；匯入的 `executor: hub` 改成 `import`（BND N27）。
  - 未處理：設定檔的 `hub.precheck.enabled: true` 不經過說明確認（S8 既有行為，換版本對它無效，記在 S20）；Hub 失敗的分享只記在記憶體（重啟歸零）；名額 2 是常數；Client 匯入「整份讀進記憶體、全域 1 個名額」照舊（N18）。
- **M3 規格展開（2026-11-23，分支 `docs/m3-specs-notes-frames`，只有文件、場景、步驟樁與假服務骨架，沒有產品程式）**：
  - 六份規格（狀態 `ready`）與 `.feature`：[SPB](specs/SPB-sponsorblock.md)、[CHAP](specs/CHAP-chapters.md)、[FRM](specs/FRM-frames.md)、[NOTE](specs/NOTE-notes.md)、[GEM](specs/GEM-gemini.md)、[VAULT](specs/VAULT-vault-output.md)；跨規格的決定在 [ADR-0020](adr/0020-m3-stages-notes-and-vault.md)（步驟順序 `translate → sponsorblock → chapters → frames → note → publish`、預設值、資源類別、產出 kind、`note_templates`、vault 與 hook 只在設定檔、`outputs` 加欄位）。
  - `@M3` 場景：`go test ./features -run TestFeatures -godog.tags='@M3'` 全部 pending（`@pending-m0` 另計）。每個 M3 的 Background 在第一個送出請求的步驟之前都有一個 M3 句型，所以**在第一個 M3 句型實作之前**不會 failed（閘門句型有共用，見 steps.md 4.13 開頭）；預設 tag 的 308 個場景不受影響。
  - 新句型在 `steps.md` 4.13～4.16（步驟樁在 `features/steps/chapters.go`、`frames.go`、`notes.go`、`vault.go`）；既有句型的 M3 語意補充（`processed up to` 接受 M3 步驟、產出名稱接受 M3 kind、LLM 步驟的 `gemini` provider、`/vault` 路徑對應）寫在 4.13 開頭，要在實作切片中補上。
  - 假服務骨架（M3-S0 已接進 fakenet 與 `faketool.Dispatch`，見下面 M3-S0 的 bullet；fakeframes、fakegemini 的影片資訊還沒接）：`features/internal/fakesponsorblock`、`fakeframes`、`fakegemini`、`fakegit`、`fakehook`；設計見 `features/fixtures/README.md`「M3 的假服務」。
  - 要 owner 追認的決定：ADR-0020 全文；SPB B1（M2 步驟不排除片段）；CHAP H1（章節在 translate 之後）；FRM F1（單一 `frames` 步驟）、F3（預設 off）；NOTE N1（自動檢查依 `Video Notes/AGENTS.md` 轉述）、N2（「標記已閱讀」按鈕來源不足）；GEM G2（模型時間戳的例外）；VAULT V1（vault 只在設定檔，UI 第 12 節已加修訂行）、V2（push 錯誤依種類分類）。
  - **M3-S0 骨架（2026-11-23，分支 `feat/m3-s0-skeleton`；決策記在 PIPE、PRF、SPB、CHAP、FRM、NOTE、GEM、VAULT 的待決問題，ADR-0020 改為「已採納（待 owner 追認）」並加修訂行）**：
    - 數字：預設 tag 308 passed（不變）；`@M2` 117 passed；`@M3` 109 個場景切片前後**逐場景相同**，全部 pending（停在同一個閘門句型）；`features/passing.txt` 沒有變動。
    - 步驟：`pipeline.Stages` 是 11 步（`… translate → sponsorblock → chapters → frames → note → publish`），`plannedStages` 只剩 `summary`（`frames.candidates`、`frames.select` 改為 422）。設定要求執行而還沒有實作的 M3 步驟在 `planChain` 改成 `skipped`（暫時的原因 `not_implemented`）；預設設定下 YouTube 的 `sponsorblock` 是 `not_implemented`、其他平台 `not_applicable`、`chapters`／`note` 是 `not_configured`、`frames` 是 `off`。從還沒有實作的 M3 步驟重跑回 501（`pipeline.StageAvailable`）。M3 之前建立的影音（7 個 stage run）前進時跳過不存在的步驟（`planChainFor`），不補建。資源類別、`impl_version` 已登記；M3 的預設值只在讀取時補上，不寫進快照（快照 sha、快取鍵不變）。
    - 處理方案：`stages.{sponsorblock,chapters,frames,note}` 與 `outputs[].target: vault` 依各規格的介面驗證（欄位表在 PRF 的介面）；vault 與 hook 的 id 由 `ValidateDeps.Outputs`（`pipeline.OutputCatalog`，app 從設定檔組出）檢查，GEM-R1 經 `pipeline.ProviderTypes`。`schema_m3_test.go` 把 6 個 M3 `.feature` 中 37 份處理方案、覆寫與預期 422 的請求直接跑過 `ValidateLayer`（閘門讓場景驗證不到這些）。
    - 設定檔：`vaults`、`hooks`（原本是任意內容，現在有結構與驗證）、`sponsorblock.base_url`、`llm.providers.<id>.type: gemini`（`internal/config/vault.go`）；`vault_test.go` 同樣把 M3 `.feature` 的 13 份設定檔（含 Scenario Outline 的每一列）載入一次。
    - migration 00017：`outputs.kind`（預設 `subtitle`）、`vault`、`git_commit`；`store.Output` 多三個欄位，既有寫入不變。`note_templates` 留給 S4。
    - 路由（新增，全部 501 `not_implemented`，scope 在讀 body 之前檢查，`internal/api/m3_stubs.go`）：`GET /api/items/{id}/frames`（read）、`GET /api/items/{id}/frames/{n}`（read）、`GET /api/items/{id}/note`（read）、`GET /api/note-templates`（read）、`POST /api/note-templates`（admin）、`PATCH /api/note-templates/{id}`（admin）、`POST /api/note-templates/{id}/preview`（admin）、`GET /api/vaults`（admin）；`POST /api/items/{id}/rerun` 的 body 多了 `reuse_candidates`（OpenAPI 描述是繁體中文）。MCP：`get_note`、`list_frames`、`get_frame`（read，轉呼叫 stub 路由）。CLI：`frames ls|get`、`note show`（usage 文字已加）。
    - harness：`World.SponsorBlock`、`World.Gemini`（fakenet，`harness.TestGeminiKey`／`NOTESTEME_GEMINI_API_KEY`，也加進 secret 掃描）、PATH 上的假 `git` 與 `notesteme-test-hook`（`faketool.Dispatch`；`World.FakeGit()`、`World.HookRecord()`）、虛擬路徑 `/vault`；`processed up to <M3 步驟>` 在 `StageAvailable` 之前 pending，決定重跑起點時跳過還沒有實作的 M3 步驟。M3 的步驟樁維持 pending，沒有新句型。
    - 已知限制：一般 LLM client 對 `type: gemini` 回 permanent（GEM 切片實作 client）；內建 `gemini` provider 與 `chapters-default`、`note-default`、`frames-default`、`note-gemini-default` 等 prompt 還沒有加入，閘門解除後 CHAP、NOTE、GEM 的 Background 會停在 422；`publish` 遇到 vault 目標以 permanent 失敗；`outputs` 主鍵沒有含 `vault`（VAULT V11）；`frames` 的資源類別暫時一律 `frames`；`audio_only`、`platform_chapters`、`gemini` 這三個略過原因只登記名稱；fakeframes 與 fakegemini 的影片資訊還沒接到假 ffmpeg／yt-dlp。
    - 給 S1～S7：在 `stageImplemented` 登記 `(stage, executor)` 就會開始執行並讓 `StageAvailable` 為 true（`processed up to` 不再 pending）；S1 登記 sponsorblock 後，每部 YouTube 影音會多一個 `done` 步驟，先跑預設 tag 逐場景比對；每個切片依 `docs/slice-protocol.md` 第 4 節的「M3 的逐場景比對」做 `@M3` 比對（不期待目標領域以外全部 pending）；改到路由時重產 OpenAPI 快照與前端的 `schema.d.ts`。
  - **M3-S1 SPB（2026-11-23，分支 `feat/m3-s1-sponsorblock`；決定記在 SPB 待決問題 B7～B12，待 owner 追認）**：
    - 數字：預設 tag 308 passed（不變，逐場景比對相同）；`@M3` 109 個：SPB 10 個 pending → passed，`@SPB-R7`（筆記輸入排除片段內的 cue）與其他 98 個仍 pending，第一個沒通過的步驟與切片前完全相同（SPB-R7 與 CHAP 停在 `SponsorBlock has the segments for`、FRM 停在 `the video of … has the scenes:`、NOTE 停在 `the chapters of … are:`）；`@M4` 371 個全部 pending，逐場景相同（SRC-10 兩個場景停在 Background 第一步的 M4 樁 `the platform adds manual captions …`，之後才會碰到 SponsorBlock 閘門）。`features/passing.txt` 沒有變動（`@M3` 還不在預設 tag）。
    - 產品：`internal/sponsorblock`（hash 前綴查詢、`categories`／`actionType` 過濾、合併、截到片長、捨棄短於 1 秒、1 MiB／10 秒／不跟隨轉址、只有上層 context 取消才回錯誤；單元測試與 fuzz）；`internal/pipeline/sponsorblock.go`（`stageImplemented` 登記 `(sponsorblock, local)`，`Handle` 分派；產出 `kind`／`variant` 都是 `sponsor_segments`；每次重新查詢，結果相同時沿用現行產出；失敗時 `done` 加提醒與「重新查詢 SponsorBlock」）；`Deps.SponsorBlock`、`Deps.SponsorBlockBaseURL`（app 從設定檔組出，沒有時用公開伺服器）；`GET /api/items/{id}` 與 MCP `get_item` 的 `sponsor_segments`（`*[]` 加 `omitempty`：步驟沒有執行過時沒有欄位）。OpenAPI 快照與 `schema.d.ts` 已重產。沒有 migration、沒有新路由、沒有新句型。
    - harness：SponsorBlock 的四個句型只對 `@SPB-` 場景放行（`Scenario.tags`，見 steps.md 4.13 與 SPB B12）；`a YouTube video … with:` 新建時沒有 `language` 預設 `en`（steps.md 4.3「同上」）。
    - 單元測試的影響：YouTube 影音的完整執行多一個任務與一個產出檔，M1／M2 的幾個計數測試已更新（pipeline、app）；`processed up to "sponsorblock"` 不再 pending。
    - 給 S2 CHAP：CHAP 的 Background 用 `SponsorBlock has the segments for`，要把 `spbGate` 放寬到 `@CHAP-`（或改成依 `pipeline.StageAvailable("chapters")` 判斷）；章節輸入排除片段內的 cue（SPB-R7、B2：一半以上的時間落在片段內）可以用 `Pipeline.SponsorSegments`；chapters 的 `parents` 是 `corrected,sponsor_segments`（CHAP 場景）；`@SPB-R7` 的閘門要等 NOTE（S4）。SRC-10（M4）在 M4 樁之後還會碰到 SponsorBlock 閘門，要另外放寬。共用句型 `a YouTube video … with:` 的預設語言改動會影響所有領域（預設 tag 308 個不受影響）。
  - **M3-S2 CHAP（2026-11-23，分支 `feat/m3-s2-chapters`；決定記在 CHAP 待決問題 H7～H11，待 owner 追認）**：
    - 數字：預設 tag 308 passed（逐場景相同）；`@M3` 109 個：CHAP 11 個 pending → passed（SPB 10 個維持 passed），其他 88 個仍 pending，第一個沒通過的步驟與切片前完全相同；`@M4` 371、`@M5` 148 全部 pending，逐場景相同。`features/passing.txt` 沒有變動。
    - 產品：`internal/llm/chapters.go`（`ChaptersSpec`、`CheckChapters`：cue 在 chunk 的輸入中且嚴格遞增、1～`max_chapters`、標題 1～80 字不含控制字元，錯誤同時記在 `Detail.error`；含 fuzz）；`internal/pipeline/chapters.go`（`llmStages` 登記 chapters，新的 `llmStage.write` hook 寫非字幕產出；`prepareLLM` 對 chapters 用 `translateSource` 選輸入、排除片段內的 cue（`cueInSponsor`，句號維持原編號）、sponsor 的 sha256 進步驟層快取鍵；平台章節是 `(chapters, local)` 任務，結束時 `skipped`（`platform_chapters`）；`Pipeline.ItemChapters`）；`StageContext.PlatformChapters`；`resolveStagePrompts` 也解析 chapters 的 prompt 版本；`Pipeline.rerunStart`（從 chapters／frames／note 重跑而 sponsorblock 從沒執行過時改從 sponsorblock 開始，H8）；內建 prompt `chapters-default`；`claim_tasks` 的 `kinds` 可以指定 `chapters`（省略時不含）；`GET /api/items/{id}` 與 MCP `get_item` 的 `chapters`（沒有產出時省略）。OpenAPI 快照與 `schema.d.ts` 已重產。沒有 migration、沒有新路由、沒有新句型。
    - harness：`spbGate` 在 `StageAvailable("chapters")` 之後也對 `@CHAP-` 放行；`Given the chapters of … are:` 仍是步驟樁。
    - 給 S3 FRM／S4 NOTE：章節用 `Pipeline.ItemChapters`（`Chapters{Origin, Chapters[{Title, StartMS, EndMS, Cue}]}`）；片段內的 cue 用 `cueInSponsor` 與 `Pipeline.chaptersSponsor`（`SPB-R7`）；`rerunStart` 已讓 frames、note 的重跑先補跑沒執行過的 sponsorblock。NOTE 要實作 `Given the chapters of … are:`（seeding 直接寫 `kind: chapters` 產出，可用 `newChaptersArtifact`），並放寬 `spbGate`（`@SPB-R7`、NOTE）。新的 LLM 步驟可以照 chapters 用 `llmStage.write`；`stageImplemented`、`spbGate`、`rerun.go`、`BuiltinIDs` 是共用的地方，S3 與這個切片都有改動，合併時注意衝突。
  - **M3-S3 FRM（2026-11-23，分支 `feat/m3-s3-frames-stage`；決定記在 FRM 待決問題 F10，待 owner 追認）**：
    - 數字：預設 tag 308 passed（逐場景相同）；`@M3` 109 個：FRM 21 個 pending → passed，`@FRM-R9`（圖說與章節）停在 CHAP 的 `the chapters of … are:`（等 M3-S2），其他 87 個與切片前逐場景相同（10 個 SPB 維持 passed）；`@M4` 371、`@M5` 148 逐場景相同。FRM 的 3 個 `@pending-m0` 場景不在 `@M3` 的執行中。`features/passing.txt` 沒有變動。
    - 產品：`internal/frames`（showinfo 解析、密度下限、32×18 去重、張數與片長級距、規則計分、picks 驗證、縮圖與拼圖；單元測試與兩個 fuzz）；`internal/media` 的 `DetectScenes`、`ExtractFrame` 與串流尺寸；`youtube.DownloadVideo` 與 `-J` 的最高畫質；`llm.Request.Images`（anthropic image block、openai image_url）；內建 prompt `frames-default`；`internal/pipeline/frames.go`（`stageImplemented` 登記 `(frames, rules)`、`(frames, api)`，`frames.candidates` → `frames.select` 兩個任務，`audio_only` 在規劃時判斷，`reuse_candidates`，frames 只保留 1 版並刪除舊圖片目錄）；`GET /api/items/{id}/frames`（Huma）、`/frames/{n}`（mux，同 `/media`）；MCP `list_frames`、`get_frame`；CLI `frames ls|get`。OpenAPI 快照與 `schema.d.ts` 已重產。沒有 migration、沒有新句型。
    - harness：`the video of … has the scenes:`、`the best video format of … is …`、`the tool result has an image of type …` 只對 `@FRM-` 場景放行；`spbGate` 也放行 `@FRM-`（FRM-R6）；`the frames of … are:` 仍是樁。假 yt-dlp 只對登記了畫面的影片列出影像格式，假 ffmpeg 加了場景偵測與擷取兩種 argv 契約，fakellm 檢查圖片區塊（F4）。
    - 沒做：`agent` 選圖（F6，維持 422）、Bilibili 影片（permanent）、vision 的成本紀錄、被取消的選圖任務留下的版本目錄清理、真實影片的視覺品質與 yt-dlp／ffmpeg 真實樣本（owner 驗收）。
    - 給 S4 NOTE：筆記取畫面時讀現行的 `frames` 產出（`Pipeline.Frames`，只有 `selected` 的）；圖片在 `<data>/artifacts/<item>/frames/<版本>/NNN.jpg`，版本即產出 ID；被 vault 引用的版本要標 `pinned`，否則下一次重跑會刪掉舊圖片（keep=1）。`the frames of … are:` 要由 NOTE 實作（寫入 manifest 與圖片，格式見 `framesManifest`）。給 S2 CHAP：`@FRM-R9` 在 `the chapters of … are:` 實作後應該會通過（frames 讀 `chapters` 產出的 `{chapters: [{start_ms, title}]}`）。
  - **M3-S7 GEM（2026-11-23，分支 `feat/m3-gemini-provider`；決定記在 GEM 待決問題 G8、G9，待 owner 追認）**：
    - 數字：預設 tag 308 passed（`passing.txt` 棘輪通過）；`@M3` 109 個：GEM 4 個 pending → passed（GEM-R1 的 422、Bilibili、缺 key、重試用盡），GEM 其他 6 個仍 pending、停在 NOTE（寫筆記、時間重試、429 停在 `the stage "note" … is "done"`；R6 與跳脫停在 `the note of …` 樁；「改用一般流程」停在 anthropic 的 `answers "note"`），其他 99 個與切片前逐場景相同（46 passed／63 pending）；`@M4` 371、`@M5` 148 逐場景相同。`features/passing.txt` 沒有變動。
    - 產品：`internal/llm/gemini.go`（generateContent 的 wire：`x-goog-api-key` 標頭、`file_data` 在第一個 part、`responseSchema`、16 MiB、不跟隨轉址、GEM-R7 的錯誤分類、安全封鎖與 `promptFeedback`、用量）；`internal/llm/gemini_note.go`（`GeminiNoteSpec`、`CheckGeminiNote`：`start` 在 `[0, 片長)`、同一層不遞減、`[[t:MM:SS]]`、不允許 `[[cue:N]]` 與 `frames`；`BuildGeminiFixed`、`RunGemini`）；內建 provider `gemini`（獨立連線池，標頭逾時 10 分鐘）；內建 prompt `note-gemini-default`（kind `note`）；`internal/pipeline/gemini.go`（GEM-R2 的略過在 `ResolveExecutor`，GEM-R3 的預檢，`Handle` 依 `geminiNoteRes` 分派，`planChain`／`Rerun` 視為已實作；`resolveStagePrompts` 也解析 note 的 prompt）；permanent 錯誤帶的動作會記錄到步驟。沒有 migration、沒有路由或 API 欄位變動（OpenAPI 快照不變），前端沒有改動（設定頁的 key 白名單已有 `gemini`）。
    - harness：`Gemini accepts YouTube videos up to …` 只對 `@GEM-` 放行；`World.SyncTools` 把假 yt-dlp 的影片同步進 fakegemini；4.6 的 LLM 步驟與 `has no API key` 接受 `gemini`；兩個暫時的閘門見 GEM G9（steps.md 4.13 開頭已記錄）。沒有新句型。
    - 給 S4 NOTE：通過驗證的 Gemini 筆記在 `pipeline.writeGeminiNote(ctx, it, eff, geminiNote)`，目前以 permanent（`GeminiNoteRendererMissing`）結束；接上 NOTE 的渲染即可（sections 的 `StartMS` 取代 cue 的時間、`llm.GeminiTimeRefs` 換時間連結、frontmatter 與來源紀錄見 GEM-R6，`parents` 已算好）。S4 實作 `the note of … has the frontmatter:`、`does not contain` 之後，GEM 的 R6 與跳脫場景會由 pending 變成 failed，所以 S4 與 S7 之中後合併的一方要在同一次推送中接上 `writeGeminiNote`。合併時注意：`prompts.BuiltinIDs`、`builtinKind`（兩邊加了相同的 `note-map-`／`note-` 分支）、`prompts_test.go` 與 `internal/api/prompts_test.go` 的清單、`resolveStagePrompts` 的 `StageNote`、`rerun.go` 的 note 501 判斷（`engine: pipeline` 在 `StageAvailable("note")` 之前仍回 501）。
    - 沒做：真實 Gemini API 的驗收（owner，含 G1 的片長、`unlisted`、token 數）、Gemini 的計價（G6）、`@pending-m0` 的 3 個場景（不在 `@M3` 的執行中）。
  - **M3-S4 NOTE（2026-11-23，分支 `feat/m3-s4-notes`；決定記在 NOTE 待決問題 N12～N22，待 owner 追認）**：
    - 數字（rebase 到含 M3-S7 GEM 的 master 之後）：預設 tag 308 passed（逐場景相同）；`@M3` 109 個：82 passed、27 pending（切片前的 master 是 46／63）。NOTE 的 28 個自動化場景、GEM 剩下的 6 個（從網址寫筆記、時間超出片長後重試、429 後重試、R6 的模板與來源紀錄、不可信內容的跳脫、改用一般流程）、`@SPB-R7`、`@FRM-R9` 由 pending → passed；VAULT 27 個仍停在 `the note of … is:`，與切片前逐場景相同。`@M4` 371、`@M5` 148（20 passed、128 pending）逐場景相同。`features/passing.txt` 沒有變動。
    - 產品：`internal/notes`（不可信 Markdown 的中和：HTML、連結與圖片只留 http／https／mailto、wikilink 與嵌入、標題改粗體、會執行的 fence 改成 `text`、行內 Dataview；YAML 雙引號；時間連結（YouTube `&t=`、Bilibili `?p=&t=`、Podcast `#t=`、本機純文字）；內建模板 `video-notes` 與受限的 text/template；五項自動檢查；單元測試與兩個 fuzz）；`internal/llm/note.go`（`NoteSpec`、`NoteMapSpec`、`CheckNote`、`CheckNoteMap`）；內建 prompt `note-default`、`note-map-default`，prompt 變數 `note_lang`；migration `00019_note_templates`（id＋version 主鍵）；`internal/pipeline/note.go`（`llmStage.prepare` hook，`api`／`agent` executor，輸入排除片段內的 cue、附上章節與選中的畫面、map-reduce（map 是 `note_map` 任務，最後一個 chunk 是 reduce，map 都完成才建立 reduce 任務）、自動檢查不通過時 `blocked`（`needs_action`）且仍寫出產出）與 `note_api.go`（模板版本、預覽、`ItemNote`、harness 的 `SeedChapters`、`SeedFrames`）；`GET /api/items/{id}/note`（`format=md` 回 text/markdown）、`/api/note-templates` 的 GET／POST／PATCH／preview（取代 501 stub）；MCP `get_note`、CLI `note show` 因此可用；`claim_tasks` 接受 `note`、`note_map`。OpenAPI 快照與 `schema.d.ts` 已重產。沒有新句型。
    - harness：實作 `the chapters of … are:`、`the frames of … are:` 與讀取筆記的三個句型；`spbGate` 放行 `@NOTE-` 與 `@SPB-R7`（steps.md 4.13 的 M3-S4 段落）。
    - 修正：note 的 map chunk 原本用 note prompt 的前綴當快取鍵，map 的 prompt 改版時會命中舊結果；模板規則的 `checks: []` 原本存回後會變成「全部執行」。兩者都有單元測試。
    - 涵蓋率：`bash scripts/ci/coverage.sh` 全過，`internal/pipeline` 84.6%（含 master 的 frames 補測）；note.go、note_api.go、gemini_note.go 有斷言的單元測試，並做過 mutation 檢查。
    - GEM：`writeGeminiNote`（`internal/pipeline/gemini_note.go`）接上 NOTE 的渲染（NOTE N22），GEM G9 的兩個暫時 pending 自然失效。
    - 沒做：VAULT 的寫入與 `outputs`（API 目前一律回空清單）、資訊表的「字幕」那一列（需要 vault 路徑，N20）、Web UI 的筆記分頁（`@ui @manual`）、真實模型的筆記品質（owner 驗收）。
    - 給 S5／S6 VAULT：筆記產出（`kind: note`，`notesteme-note+json`）的 JSON 有 `markdown`、`frontmatter`、`checks`、`images: [{n, file}]`（筆記嵌入的畫面編號與 vault 中的檔名，圖片在 frames 產出的版本目錄 `NNN.jpg`，寫入時記得把那個 frames 版本標成 `pinned`）與 `render`（重新套用模板用的 `cue_start`、`frames`、`extra`）。`checks.passed` 為 `false` 的筆記步驟是 `blocked`，不會走到 publish，VAULT 不要寫入。資訊表的「字幕」列要等 vault 路徑決定後加進 `notes.Input.Extra`（或模板變數）。`NoteBody.Outputs`（`internal/api/notes.go`）目前是 `[]`，VAULT 要補上 `{vault, path}`；`the note of … is:` 的 seeding 可以照 `SeedChapters` 直接寫 `noteDoc`。
  - **M3-S5／S6 VAULT（2026-11-23，分支 `feat/m3-vault-output-fs-git`；決定記在 VAULT 待決問題 V12～V14，待 owner 追認）**：
    - 數字（以含 M4-S2 SUB、M5-S3 HREP 的 master 075665b 為基準）：預設 tag 308 passed（逐場景相同）；`@M3` 109 個：104 passed、5 failed（切片前 82／27 pending）。VAULT 27 個中 22 個 pending → passed；5 個（`The note links its subtitles…`、`Publishing an unchanged note…`、`A rejected push…`、`Bad credentials…`、`A network error is retried…`）由 pending 變 failed：場景改了預設處理方案，但影音在 Background 已綁定 `vault-test`（PRF-R3），要 owner 決定場景修改（V12，最小修改的 diff 已驗證過：改用帶 `profile` 的 POST rerun 後 27 個全部通過）。`@M4`（84 passed／287 pending）、`@M5`（63／85）逐場景相同。`features/passing.txt` 沒有變動。
    - 產品：`internal/vault`（版面與檔名 VAULT-R2、R3；`CheckInside` 的 symlink 檢查 R4；git adapter R7～R9；hook R10、R11）；`internal/pipeline/vault_publish.go`（筆記、畫面、字幕寫進 vault，沿用 `output.Writer`，`File.Root` 讓寫入限制在 vault 的 `os.Root` 之內；筆記與 frames 版本 pin；git 的 clone／pull／add／commit／push；hook 在全部寫完之後執行）；note 步驟把字幕的 vault 路徑放進資訊表（V8，`noteRender.subtitle_link`）；刪除影音時 fs vault 的檔案依 DEL-R9；migration `00022_outputs_state`（`outputs.state`）；`GET /api/vaults`（取代 501 stub）、`GET /api/items/{id}/outputs` 與 `GET /api/items/{id}/note` 的 `outputs` 多了 `vault`、`kind`、`state`、`commit`；`toolexec.Cmd` 加 `Env` 與 `KeepOutput`；Docker 映像加 `git`、`openssh-client`。OpenAPI 快照與 `schema.d.ts` 已重產。
    - harness：實作 `the note of … is:`（只對 `@VAULT-` 放行，BND 等維持 pending）與 4.16 全部句型；`@VAULT-` 場景中 server 執行時改設定檔或環境變數會先關閉、再以新設定重新啟動；fakegit 加上遠端追蹤 ref、pull 快轉與 `--`（steps.md 4.16 的 M3-S5／S6 段落）。沒有新句型。
    - 沒做：git vault 的刪除 commit（VAULT-R12 的一部分，檔案保留、原因 `git_vault`）、secrets 中的 `vault:<id>` 憑證、`publish` 有 git vault 時的 `download` 資源類別、Web UI 的設定 › 輸出與筆記分頁的寫入狀態（`@ui @manual`）、真實 git 服務／Obsidian 的驗收（owner）。
    - 給後續：BND（M4）的 `the note of … is:` 要放寬 `noteIs` 的 tag 閘門；`outputs` 主鍵仍是 `(item_id, path)`（V11）。
  - 建議的實作切法：M3-S0 骨架（比照 M2 S0：schema 接受全部 M3 欄位與設定檔的 `vaults`、`sponsorblock`，M3 路由回 501 stub；假服務接線、`/vault` 對應、`processed up to` 的 M3 規則、預設的假 SponsorBlock；之後每個切片以 `@M3` junit 逐場景比對）→ S1 SPB → S2 CHAP → S3 FRM（候選與規則選圖，再 vision）→ S4 NOTE（模板、渲染、檢查、跳脫，再 map-reduce 與模板 API）→ S5 VAULT fs → S6 VAULT git＋hook → S7 GEM；每個步驟加進 `pipeline.Stages` 時同步移除 `plannedStages` 並確認 308 個場景不退步。
- **M4 規格展開（2026-11-23，分支 `docs/m4-tracking-deploy-specs`，只有文件、場景、步驟樁與假服務骨架，沒有產品程式）**：
  - 九份新規格（狀態 `ready`）：[SUB](specs/SUB-subscriptions.md)、[ENTRY](specs/ENTRY-entry-points.md)、[BND](specs/BND-bundles.md)、[DIG](specs/DIG-digest.md)、[BATCH](specs/BATCH-batch-api.md)、[GLOS](specs/GLOS-glossary-review.md)、[SRCH](specs/SRCH-search.md)、[NOTIFY](specs/NOTIFY-notifications.md)、[COST](specs/COST-cost-budget.md)；既有規格加 M4 章節：SRC（SRC-10，SRC-R18～R23）、LIB（LIB-08、09，LIB-R13～R23）、AUTH（AUTH-06～08，AUTH-R12～R22）、OPS（OPS-06～08，OPS-R11～R21），場景放在新的 `SRC-better-source.feature`、`LIB-bulk-overview.feature`、`AUTH-proxy-oidc.feature`、`OPS-unattended.feature`（LIB、OPS 檔頭仍是 `draft`，因為 M1 的部分還沒收尾；M4 章節依 DoR 撰寫）。跨規格的決定在 [ADR-0021](adr/0021-m4-scheduler-subscriptions-and-outbox.md)：排程器是 worker 的 `Periodic`（`scheduled_jobs`、重啟只補跑一次、hash 決定的 jitter、`scheduler.jitter_max`）、`discover` 不是步驟、`summary` 放在 `normalize` 之後且預設 off、預算預留與檢查同一交易、通知經 outbox、migration 方向、Postgres 與媒體檔保留策略不在 M4。
  - 數量：13 個 `.feature`、300 個場景定義（`@M4` 展開後 371 個自動化場景，`@manual` 21 個、`@pending-m0` 2 個另計），137 條新規則、29 則故事。`go test ./features -run TestFeatures -godog.tags='@M4'`：371 pending、0 failed、0 undefined；預設 tag 308 passed、`@M3` 109 pending 不變。
  - 新句型在 `steps.md` 4.17～4.21（107 個，步驟樁在 `features/steps/subscriptions.go`、`notify.go`、`costs.go`、`library.go`、`signin.go`，一律 `r.Step` 不限關鍵字）；既有句型的 M4 語意補充寫在各節開頭（`{subscription:<別名>}` 代入、`a YouTube video … with:` 的 `published_at`／`shorts`、`the worker runs until idle` 也執行到期的排程、`processed up to "summary"` 等）。
  - 假服務骨架（只有單元測試，還沒接進 fakenet）：`features/internal/fakefeed`（YouTube Atom、RSS、OPML、畸形與超大 XML）、`fakenotify`（ntfy、Discord、Telegram、webhook 接收端與 HMAC 驗證）、`fakebatch`（Anthropic／OpenAI Batch API）、`fakeoidc`（discovery、JWKS、authorize、token，含壞簽章／過期／錯 nonce）；設計見 `features/fixtures/README.md`「M4 的假服務」。
  - 要 owner 追認的決定：ADR-0021 全文；SUB S1（新內容的判準：頻道看發布時間，清單類看第一次檢查的基準）、S2（不用 Supadata channel/videos）、S3（Bilibili 只支援合集）、S5（回補上限 recent 200／全部 500／每次 50；rate limit YouTube 30、Bilibili 6、每個 Podcast 主機 10 次／分）、S6（Shorts 只看網址）、S7（直播等 VOD 由 metadata 的 `wait`）；SRC R1（重新 Probe 每 12 小時）、R2（`language.better_source: notify|rerun|off`）、R6（有 `edited` 時只通知）；ENTRY E1、E2（`/add` 不自動開始）、E3（`next` 上限 2048，AUTH-R17 已一併改）；DIG D2（summary 不切 chunk，超過 2 小時或 32000 tokens 為 `too_long`）、D3、D6（彙整連結只指向 UI）、D7／D11（對齊當地時間，ADR-0021 已補例外）、D9；NOTIFY N1（`permanent` 首次就 dropped）、N2（不跟隨轉址）、N3（Telegram/Discord 只能官方主機或 `notify.allow_hosts`）、N4、N5（去重 24 小時、每 10 分鐘 20 則）；OPS O1～O6（備份預設寫 `/data/backups`、保留只看檔名、yt-dlp 失敗率固定門檻、vps 的密碼檢查放在 `vps-check` 服務）；COST C1（`PUT /api/settings/budget`，UI 第 11 節已加修訂行）、C2、C3、C5、C6（M4 沒有成本的 MCP／CLI）、C10（金額以整數 micro-USD 比較）；BATCH H1～H4（預設 off、24 小時逾時、5 分鐘輪詢、每批 100 個）；LIB L-M1（M4 不做暫停）、L-M2（「刪除媒體檔」延後）、L-M3（`POST /api/items/batch`）；SRCH S1（每個語言索引最完整的一份字幕）、S4、S6（不依相關度排序、簡繁不互通）；BND N1（匯入要 `admin`）、N4（上限 64 MiB／256 MiB／2000 項／壓縮比 100）、N5（已存在回 409）；GLOS G1（審核放在設定頁新的「詞彙表」分區，UI 第 12 節已加列）、G4（批次一個交易）；AUTH A1～A7（OIDC 路由只在啟用時免驗證、仍要求密碼、IdP 請求不經私有網段封鎖但限 https、時鐘偏差 60 秒、proxy session 綁標頭、`/mcp` 不接受 proxy 身分、輪替保留到期時間）。
  - 已知的規格間張力（寫在各規格的待決問題）：story-map 的 LIB-08 寫了 pause 但 M4 不做；UI 第 5 節線框在「只抓之後的新內容」時仍顯示「會處理 8 部」；ADR-0005 的 bundle manifest 要補 `files`、`source_name`、`created_at`；plan 第 14 節「只索引校正與譯文」以 SRCH-R1 為準；既有單元測試 `internal/api/items_test.go`（budget 回 501）、`items_list_test.go`（`read_state` 回 501）在實作時要一起改。
  - 建議的實作切法：M4-S0 骨架（schema 接受全部 M4 欄位與設定檔鍵、M4 路由回 501 stub、`scheduled_jobs` 與排程器接進 `periodics`／`nextWake`、fakeXXX 接進 fakenet、確認 308 個場景不退步；之後每個切片以 `@M4` junit 逐場景比對）→ S1 NOTIFY outbox 與 notifier（多個領域的閘門）→ S2 SUB-A（建立、RSS 探索、回補、過濾、排程）→ S3 SUB-B（預覽、匯入、直播、rate limit、不可信輸入、MCP/CLI）＋ ENTRY（可最先平行）→ S4 COST（帳本、計價、預算預留；與 LIB-bulk-overview 共用閘門，要一起確認）→ S5 BATCH → S6 DIG（summary 加進 `pipeline.Stages` 時確認 308 個不退步，再做彙整）→ S7 SRC-10（依賴排程器與 M3 SPB）→ 並行 S8 SRCH、S9 GLOS、S10 BND、S11 AUTH（token 輪替與工作階段 → proxy → OIDC）→ S12 OPS（每日備份、告警、vps profile）→ S13 LIB-08、09。
  - **M4-S0 骨架（2026-11-23，分支 `feat/m4-s0-skeleton-wiring`）**：只有接線，沒有 M4 功能。
    - pipeline：`summary` 加進 `pipeline.Stages`（`normalize` 之後、`segment` 之前），預設 `executor: off`，`plannedStages` 清空；設定成 `api`／`agent` 時 `skipped`（`not_implemented`），從 `summary` 重跑回 501（M3 的 gating 改成 `gatedStage`，含 summary）；舊影音沒有 summary 的 stage run 時前進時跳過。預設快照 sha 不變（`internal/pipeline/m4_stages_test.go` 寫死）。
    - 處理方案 schema：`stages.summary`（`language`、`pause_after`、`max_duration`、`max_input_tokens`，`pause_after` 與 `off` 矛盾時 422）、`stages.<stage>.batch`（auto/off，在 executor 之後檢查；sponsorblock、frames、transcript 等回「這個步驟不支援 batch。」）、`language.better_source`（notify/rerun/off，`omitempty`，不進快取鍵）。M4 的 37 份 `.feature` 處理方案片段由 `internal/profiles/schema_m4_test.go` 全部驗證。
    - 設定檔：`internal/config/m4.go` 解析 `server.public_url`、`scheduler.jitter_max`、`subscriptions.*`、`notify.*`、`bundles.import.*`、`backup.daily.*`、`alerts.*`、`auth.proxy.*`、`auth.oidc.*`、`NOTESTEME_OIDC_CLIENT_SECRET`、`NOTESTEME_DEPLOYMENT`；`subscriptions` 移出 `dbManagedFields`，`budget` 仍由資料庫管理（steps.md 4.17 的 `budget` 是筆誤）。M4 的 27 個設定檔片段由 `internal/config/m4_test.go` 全部載入。
    - 路由：`internal/api/m4_stubs.go` 41 個 501 stub（scope 在讀 body 之前檢查），OpenAPI 快照與 `schema.d.ts` 已重產；budget 沿用既有 `settings/{section}`、OIDC start/callback 不註冊（AUTH-R16 要求未啟用時 404，S0 實際回 401）。
    - store／排程器：migration `00018_scheduled_jobs`、`internal/store/scheduled_jobs.go`；`internal/scheduler`（註冊制、注入 clock、30 秒單輪上限、退避 1m/5m/30m/6h、毫秒 jitter、`Deferred`），接進 `internal/app` 的 `periodics`；註冊表空時不查資料庫。ADR-0021 已加修訂段。
    - harness：`features/internal/harness/m4.go`（`ServeFeeds`、`ReachNotifyEndpoints`、`EnableBatch`／`Batch`、`StartOIDC`、batch 路徑預設 404 並記違規）；`processed up to "summary"` 改以 `StageAvailable` 判斷 pending。web：`STAGES` 與三個語系加上 summary。
    - 驗證：預設 tag 308 passed、`@M3` 42 passed、67 pending（M3-S3 之後，origin/master c6ed67a）、`@M4` 371 pending，三組逐場景（含停下的步驟）與 rebase 後的 origin/master 相同；`features/passing.txt` 不變。
    - 決定寫在 DIG、BATCH、COST、NOTIFY、SUB、BND、AUTH、OPS、SRC、LIB 的待決問題（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）。
    - 給後續切片：(1) 排程工作在 worker 開始之前用 `s.scheduler.Register` 登記 kind（之後 `ErrFrozen`），再用 store 的 `ScheduleJob` 排定；(2) S1 NOTIFY 的閘門句型要呼叫 `w.ReachNotifyEndpoints()`，SUB 呼叫 `w.ServeFeeds()`，BATCH 呼叫 `w.EnableBatch(…)`，AUTH 呼叫 `w.StartOIDC(…)`；(3) 閘門共用（4.17、4.18 的說明）：實作一個之後要用 `@M4` junit 逐場景比對其他領域；(4) COST 要改 `internal/api/items_test.go` 的 budget 501，LIB-R19 要改 `items_list_test.go` 的 `read_state` 501；(5) S6 DIG 要在 `stageImplemented` 登記 summary、補內建 prompt `summary-default`，並確認 harness 的 `firstUnfinished` 與 308 個場景；(6) 實作某個 stub 時從 `m4_stubs.go` 與 `m4_stubs_test.go` 一起移除，重產 OpenAPI。
  - **M4-S1 NOTIFY（2026-11-23，分支 `feat/m4-notify-outbox-notifiers`）**：通知的 outbox 與四種 notifier。
    - store：migration `00020_notify`（`notifiers` 加 `config_nonce`；`notification_outbox` 加 `last_attempt_at`，沒有外鍵）、`internal/store/notify.go`。
    - `internal/notify`：設定驗證（NOTIFY-R2，Telegram／Discord 只能官方主機或 `notify.allow_hosts`、http 只在 `network.allow_private`）、整份設定以資料金鑰加密（AAD `notifier/<id>`，API 只回末四碼與 `url_host`）、`Outbox.Emit`（同交易、去重視窗、有寫入時把 `notify.deliver` 提前到現在）、`Service.Deliver`（交易外送出、退避 1m/5m/30m/2h/6h、第 6 次或 permanent 就 dropped 並寫 `notify.dropped` 事件、速率上限算嘗試次數、Retry-After 上限 6 小時、30 天清理）、`NewClient`（netguard、不跟隨轉址、15 秒、`req.Close` 讓 DNS rebinding 重新檢查）、內容清理與 HMAC 簽章。錯誤只用固定訊息，log 只有 id、kind、主機、事件、outbox id、狀態碼與錯誤類別。
    - pipeline：`Deps.Notify`；`applyResult` 交易最後與 `rerun` 交易中呼叫 `emitRunEvent`（`item.failed`、`item.blocked`〔needs_action、budget_blocked〕、`item.done`〔最後一個步驟 done／skipped 且沒有 until〕）；沒有訂閱的 notifier 時只多一次 `StageRunsByItem` 與一次 notifier 查詢。
    - API：7 條路由從 `m4_stubs.go` 移除改為實作（admin），OpenAPI 與 `schema.d.ts` 已重產；CLI `notesteme notifiers ls | test <id>`（失敗的測試通知結束碼 1）；MCP 沒有 tool（規格如此）。
    - steps：`notifyGate`（只對 `@NOTIFY-` 放行，裸的 `ErrPending`）、`the notifier … is:` 接在佇列之後寫入（`Scenario.notifiers`、`flushNotifiers`）；harness 的 `ReachNotifyEndpoints` 解析到 `.60`～`.63` 並記錄抵達時間（`NotifyReceivedAt`）。
    - 數字（以 rebase 後的 origin/master 6905634 為基準，GEM 已合併）：預設 tag 308 passed、`@M3` 46 passed／63 pending、`@M5` 20 passed／128 pending，三組逐場景與基準相同；`@M4` 371 個：NOTIFY 24 個 pending → passed，其他 347 個（含共用 `the notification endpoints are reachable` 的 DIG 與 OPS-06～08）狀態與停下的步驟訊息都不變。`features/passing.txt` 沒有變動。
    - 場景修改（待 owner 追認）：「A failure reaches the webhook signed with its secret」刪掉 `item.title` 的預期（metadata 全部失敗時沒有標題來源），見 NOTIFY N9；其他決定見 NOTIFY 待決問題的「M4-S1 實作的決定」。
    - 給後續切片：(1) 在觸發事件的**同一個寫入交易**中呼叫 `outbox.Emit(ctx, r, notify.Event{Name, DedupeKey, …})`，`r` 是 `Store.Tx` 傳入的 Repo（在交易中呼叫 `Store.Write()` 會卡死）；鍵用 `notify.Dedupe*`（`DedupeBudgetWarning(<YYYY-MM>)`、`DedupeDigest(id, 排定時間)`、`DedupeAlert(kind, target, resolved)`、`DedupeBetterSource(item, source)`）。`*notify.Outbox` 由 `internal/app/notify.go` 的 `newNotify` 建立（目前只交給 pipeline；COST／DIG／OPS／SRC 要用時從 `Server` 傳進去）。(2) 影音事件用 `notify.ItemOf(ctx, r, itemID)` 取 `Item`；先用 `Outbox.Wants` 判斷有沒有 notifier，避免多讀。(3) `Event.Message` 必須是已遮蔽的 UI 狀態訊息，不放字幕、筆記或外部工具輸出；DIG 的明細放 `Digest`、`Items`（原樣進 webhook，超過 64 KiB 時去掉）；`alert` 用 `Event.Alert{Kind, Target, Summary, Resolved}`。(4) DIG、OPS 要放寬 `notifyGate`（同 `spbGate` 依 tag 放行），並做 `@M4` 逐場景比對；`budget.warning`、`digest` 的文字標題是暫定的，S4 COST、S6 DIG 確認。(5) 不經 `applyResult`／`rerun` 的狀態變更（租約回收、取消、`ResumeBlocked`）不會通知，需要時在那些交易中補呼叫 `emitRunEvent`。
  - **M4-S2 訂閱核心（2026-11-23，分支 `feat/m4-s2-subscriptions-core`；決定記在 SUB 待決問題的 M4-S2 兩項與 ADR-0021 的修訂行，皆待 owner 追認）**：
    - 範圍：SUB-R1～R5、R8～R12 與 R13 的 REST（7 條路由從 `m4_stubs.go` 移除）；R6 預覽、R7 匯入、R13 的 MCP 與 CLI 留給 S3（預覽、匯入仍是 501 stub）。
    - 程式：migration `00021_subscriptions`（master 已有 `00019_note_templates`、`00020_notify`）（`subscriptions`、`subscription_entries`、`items.subscription_id`）、`internal/store/subscriptions.go`、`internal/subscription`（網址辨識、探索、過濾、回補、記憶體 rate limit、`subscription.check`）、`internal/api/subscriptions.go`、`internal/app/subscriptions.go`；`scheduler.RetryAfter`／`NextWithJitter`；`pipeline.IngestRequest.SubscriptionID`（不清除 tombstone）與 `CheckIngestPlan`。
    - 數字（以 rebase 後的 origin/master 03788ac 為基準，NOTIFY 已合併）：`@M4` 84 passed、287 pending、0 failed（基準 24 passed／347 pending；改變的只有 60 個 SUB 核心場景，pending → passed）；預設 tag 308 passed、`@M3` 82 passed／27 pending、`@M5` 31 passed／117 pending，三組逐場景與基準相同；`features/passing.txt` 不變。
    - 給 S3：(1) 閘門 `subGate`（`features/steps/subscriptions.go`）的 `subCoreRules` 加上 6、7，並移除 `@mcp`／`@cli` 的排除；(2) 預覽可以重用 `check.run` 的過濾與回補（目前會寫入條目，要拆出不寫入的版本）；(3) 匯入建立訂閱時沿用 `Service.Create` 的驗證與重複檢查；(4) yt-dlp 的 extractor 名稱（`youtube:tab,youtube:playlist`、`BilibiliCollectionList`）只在假 yt-dlp 驗證過，要實測。
    - 給 ENTRY 與 SRC-10：`the platform adds … captions`、`the browser opens …` 等句型仍是步驟樁；DIG 等只有 `@SUB-01` 的場景仍停在 SUB 閘門，實作時要把它們的 Rule tag 加進閘門判斷或改用自己的閘門。
  - **M4-S3 訂閱的預覽、匯入、MCP 與 CLI（2026-11-23，分支 `feat/sub-preview-import-mcp-cli`；決定記在 SUB 待決問題的「M4-S3」一項，待 owner 追認）**：
    - 範圍：SUB-R6 試跑預覽（`POST /api/subscriptions/preview`、`/{id}/preview`）、R7 匯入（`POST /api/subscriptions/import`，OPML 與 Google Takeout 的 `subscriptions.csv`）、R13 的 MCP（`list_subscriptions`、`create_subscription`）與 CLI（`notesteme subs ls|add|show|preview|check|rm|import`）。3 條 stub 已從 `m4_stubs.go` 移除，OpenAPI 與 `schema.d.ts` 已重產。SUB 的規則全部實作（規格 `status` 不改）。
    - 程式：`internal/subscription/preview.go`（`check` 的 dry-run：視同現在建立、不讀已記錄的條目、不寫入任何東西）、`import.go`（格式判斷、OPML 的 directive／深度／編碼檢查、CSV、逐列以 `Create` 建立；`CreateInput` 多了只有匯入能設定的 `title`）；`internal/api/subscriptions_preview.go`（`subscriptionImportLimit` 在 Huma 之前檢查 scope 與大小，回 413 `too_large`）；`internal/mcptools/m4.go`；`internal/cli/subs.go`（backend 加 `callRaw`）。訂閱網址加了 2048 字元的上限。
    - 驗證：yt-dlp 2026.10.26 實測 `youtube:tab,youtube:playlist`（頻道 `/videos`、播放清單、`@handle`）與 `BilibiliCollectionList`（合集舊格式網址）。涵蓋率 `internal/subscription` S2 交付時 69.5%（加入本切片的新程式、尚未補測時 59.9%）→ 90.6%、`internal/api` 82.8%、`internal/cli` 88.5%；`scripts/ci/coverage.sh` 四個 package 都 ≥ 80%。
    - 數字（以 rebase 後的 origin/master 075665b 為基準，HREP 已合併）：`@M4` 97 passed、274 pending（基準 84／287；改變的只有 13 個 SUB 場景 pending → passed，SUB 73 個全部 passed）；預設 tag 308 passed、`@M3` 82／27、`@M5` 63／85，三組逐場景與基準相同；`features/passing.txt` 不變。
    - 閘門：`subGate` 的 `subCoreRules` 加上 6、7（SUB-R1～R13 全部）並移除 `@mcp`／`@cli` 的排除；仍要求至少一個 `@SUB-R*`，所以 DIG 只有 `@SUB-01` 的場景照舊停在 `the YouTube feeds are served from the registered videos`。
    - 給 ENTRY／SRC-10／DIG：(1) DIG 的訂閱場景要自己放行（在 `subGate` 加上 `@DIG-R*` 的判斷，或改用自己的閘門），放行後做 `@M4` 逐場景比對；(2) ENTRY 的「建立訂閱」可以直接用 `POST /api/subscriptions`（UI 的預覽用 `POST /api/subscriptions/preview`）；(3) SRC-10 對訂閱建立的影音一樣適用（`items.subscription_id` 有值），不需要 SUB 另外配合；(4) 預覽會連外並消耗 rate limit，UI 的即時預覽要 debounce。
  - **M4-S4 COST（2026-11-23，分支 `feat/m4-s4-cost-budget`；決定記在 COST 待決問題的「M4-S4 實作的決定」C11～C23 與 PIPE 待決問題的 M4-S4，皆待 owner 追認）**：成本帳本、計價表與預算。
    - store：migration `00023_cost`（`cost_ledger.stage`、`price_overrides`、三個索引）、`internal/store/costs.go`；`ClaimTasks`／`ListQueueTasks` 的同分排序加上 `stage_run_id, rowid`（PIPE 的 M4-S4）；`queue.RecoverStale` 在同一個交易刪除孤兒預留。
    - `internal/cost`：整數 micro-USD（`Micro`）；`Budget`（settings `cost.budget`）與 COST-R4 驗證；`Service.Reserve`（同交易估算、檢查、預留，COST-R5～R8）、`Settle`（只改 `reserved` 的列，重複結算無效）、`Release`、`RecordAgent`（COST-R3）、`Check`（不寫入，給 LIB 的 dry_run）、`warn`（`budget.warning` 事件與 outbox，每月一次）；計價 `Lookup`（覆寫 → 內建，自訂 base_url 不套用內建）、`Prices`／`PutPrice`／`DeletePrice`／`ConfirmPrices`／`Notices`；查詢 `ParseRange`、`Summarize`、`ListEntries`、`Export`（CSV injection 防護）。排程工作 `budget.month`（`internal/app/cost_month.go`；有預算時才排）讓 `resume_on: budget` 的步驟在月初恢復。
    - llm／pipeline：`RunOptions.AfterRequest`、`GeminiOptions.AfterRequest`、`Response.CostUSD`（OpenAI 相容的 `usage.cost`）、`llm.ErrorOutcome`；`internal/pipeline/meter.go` 在每個付費請求送出前預留、結果出來後結算（correct／segment／translate 的 LLM chunk、frames vision、Gemini 筆記），C11。恢復被擋下的 LLM 步驟時沿用已完成的 chunk（`runChunks`）。
    - API：`/api/prices`（GET read；PUT／DELETE／`POST /confirm` admin）、`/api/costs`、`/api/costs/entries`、`/api/costs/export.csv`（read）從 `m4_stubs.go` 移除改為實作；`GET`／`PUT /api/settings/budget` 接在既有的 `settings/{section}`（`items_test.go` 的 501 斷言已改）。OpenAPI 快照與 `schema.d.ts` 已重產。notify 的 webhook body 多 `budget`（C19）。
    - steps：`costGate`（只對 `@COST-` 放行）；計價與預算在 `flushConfig` 之後、`flushSubscriptions` 之前寫入；`the worker starts one round` 也在 worker 沒有執行中任務時結束等待（steps.md 4.19 的 M4-S4 說明）。fakellm 加 `SetUsage`、`SetCost`。
    - 數字（基準 origin/master 50badf5）：預設 tag 308 passed、`@M3` 82／27、`@M5` 31／117，逐場景相同；`@M4` 371 個：84／287 → 124 passed、246 pending、1 failed。COST 41 個中 40 個 pending → passed，LIB-bulk-overview 與 BATCH 停在切片前的同一個步驟；`features/passing.txt` 不變。涵蓋率 pipeline 84.6%、queue 89.2%、cost 85%。
    - **需要 owner 決定**：「A retried server error is recorded at no cost」是 failed。場景在 503 之後只跑一次 `runs until idle`，但 LLM-R6 的退避是 30 秒；建議比照 `LLM-execution.feature` 在後面加 `And the clock advances by "1m"` 與 `And the worker runs until idle`（C22）。這個切片沒有修改 `.feature`。
    - 沒做：Groq ASR（`audio_hour`）與 Supadata（`credit`）的計量、Playground 的計量（C17）；ING-R8 的估價改用覆寫（C23）；內建 LLM 單價（C18）；`/api/overview` 的提醒（LIB）。
    - 給 BATCH（S5）：每個 batch 請求各用一個 `cost.Call{…, Batch: true}`，在建立 batch 的寫入交易中呼叫 `Service.Reserve(ctx, r, call)`（`r` 是 `Store.Tx` 的 Repo；超過預算時回 `budget_blocked`，整個 batch 不送出），結果回來後各自 `Settle(ctx, r, res, call, cost.Usage{Outcome, InputTokens, OutputTokens, ProviderUSD})`，取消或逾時用 `Release`。batch 折扣（BATCH-R6、R7）目前沒有實作：`Reserve` 的估算與 `settlement` 的金額要乘上折扣，`unit_price_json.batch` 已記錄。BATCH 的句型要放寬 `costGate`（加 `@BATCH-`）並做 `@M4` 逐場景比對。
    - 給 LIB-08、09（S13）：dry_run 用 `Service.Check(ctx, r, itemID, est)` 判斷 `exceeds`（不寫入），估算時先看測試控制 `the cost estimate of every … task is …`（`Options.Estimates`，目前只在 `Reserve` 裡讀，要給 LIB 用時抽出公開方法）；`/api/overview` 的提醒用 `Service.Notices`。LIB 的閘門是 COST 的 `the cost estimate of every … task is …`，要放寬 `costGate`（加 `@LIB-R1`／對應 tag）才會開始執行，解除前先確認 `POST /api/items/batch` 與 `GET /api/overview` 已實作，否則 pending 會變 failed。
  - **M4 ENTRY（2026-11-23，分支 `feat/entry-points`；決定記在 ENTRY 待決問題 E6～E11，皆待 owner 追認）**：書籤小工具、`/add`、PWA share target。
    - Go：`internal/auth/next.go` 的 `SafeNext`（ENTRY-R6，上限 2048 bytes）；`POST /api/auth/login` 加上可選的 `next`，成功時回 `next`（不加 schema 限制，不合格的值回 `/` 而不是 422）。`internal/entry`：`ShareURL`／`ValidURL`（ENTRY-R4）與頁面路由 `Pages`（`/add`、`/share`、`/login`、`/manifest.webmanifest`、`/sw.js`），由 `internal/api/ui.go` 的 `entryPages` 掛在 Web UI 之前（只有 TCP listener）。`/add`、`/share` 只認 session cookie；三個頁面的回應（含 303）都帶 webui 的 CSP（`frame-ancestors 'none'`）與 `Cache-Control: no-store`。沒有 MCP、CLI、migration 或 `m4_stubs.go` 的變更（ENTRY 本來就不在 stub 清單）。
    - Web：設定頁新的「書籤小工具」分區（`/settings/bookmarklet`）；`/add` 是 lazy route，打開「新增」抽屜（`useAddDrawer().open(url, notice)`）後換成 `/library`；登入頁送出 `next`（或舊的 `redirect`）並只前往回應中的 `next`；`index.html` 引用 manifest，`main.tsx` 在 production 註冊 `/sw.js`；`public/icons/icon-192.png`、`icon-512.png` 依 favicon 放大繪製。bundle 初始 241.5 KB／300 KB。
    - steps：`features/steps/entry.go`（4 個句型），`subscriptions.go` 只改成呼叫 `registerEntry`。
    - 數字（基準 origin/master a0b3e60）：預設 tag 308 passed；`@M3` 109 passed；`@M4` 371 個：138／233 → 160 passed、211 pending、0 failed（改變的只有 22 個 ENTRY 場景 pending → passed）；`@M5` 74／74 不變；逐場景（cucumber 的第一個非 passed 步驟）其他場景都相同。涵蓋率 `internal/entry` 100%、`internal/auth` 89.6%。
    - 人工驗收（owner）：4 個 `@ui @manual` 場景（拖到書籤列並檢查程式碼、在 YouTube 分頁點書籤、無效網址的提示、Android 的 YouTube App 分享）；iOS 捷徑。
    - 給 S11 AUTH（proxy／OIDC）：登入後的 `next` 一律經 `auth.SafeNext`；OIDC 的 `start?next=` 與 proxy 登入的導向也要帶同一個 `next`（ENTRY-R5）。`/login` 由 `internal/entry` 提供，登入頁的「用 OIDC 登入」按鈕要把 `next` 一併帶到 `/api/auth/oidc/start?next=`。
  - **M4-S5 BATCH（2026-11-23，分支 `feat/m4-s5-batch-api`；決定記在 BATCH 待決問題的「M4-S5 的實作決定」，皆待 owner 追認）**：Anthropic Message Batches 與 OpenAI Batch API。
    - store：migration `00024_batches`（`batches`、`tasks.batch_id`、`llm_chunks.batch_json`）、`internal/store/batches.go`；任務狀態 `waiting`（不被領取、不屬於租約回收）；`CancelPendingTasks`／`CancelItemTasks` 也取消 `waiting` 並把相關的 `batch.poll` 提前到現在；開著的 batch 的預留不算孤兒預留。
    - llm：`internal/llm/batch.go`（`BatchClient` 的 Submit／Status／Cancel／Results、`DecodeBatchBody`、`BatchSupported`）；`ChunkRequest`、`ValidateResponse` 從 `RunChunk` 抽出，batch 與一般 API 共用同一份請求與驗證。結果逐行讀取（單行 10 MiB、總共 256 MiB），不跟隨重新導向，results_url 必須與 base_url 同主機。
    - cost：`Call.Batch`、`BatchDiscount = 0.5`（預留與結算都打折，provider 回報的金額不打折）、`SettleBatch`（預留不在時另寫一筆，給 discarded 用）。
    - pipeline：`internal/pipeline/batch.go`。chunk 任務（segment、correct、chapters、note；translate 與 summary 還沒接上）符合 BATCH-R1 時送出 batch、任務 `waiting`、步驟 `waiting`；`PollBatch` 是排程工作 `batch.poll`（`internal/app/batch.go`）。結果照一般 API 的驗證寫入 chunk；失敗、逾時、送出失敗時改走一般 API，`llmPayload.Batch` 帶著 fallback 原因（驗證失敗時連同重試說明）。逾時後仍無法查詢時放棄 batch（`batch_failed`）。`GET /api/items/:id` 的步驟多 `batch`（OpenAPI 與 `schema.d.ts` 已重產）。
    - steps：`costGate` 加 `@BATCH-`、`batchGate`；fakebatch 的答案由 fakellm 的 answers 產生（`harness.LLMBatchAnswerer`、`fakellm.AnswerBatch`，不記入一般請求數）；`provenance` 斷言的值 `*` 代表「存在且非空」（steps.md 的 M4-S5 說明）。
    - 數字（基準 origin/master a0b3e60）：預設 tag 308 passed、`@M3` 109 passed、`@M5` 74／74，逐場景相同；`@M4` 371 個：138／233 → 154 passed、217 pending、0 failed，只有 BATCH 的 16 個場景 pending → passed。`features/passing.txt` 與 `.feature` 沒有變動。
    - 沒做：真實 Anthropic／OpenAI 的 batch 驗收（只有 fakebatch）；translate 與 summary 的 batch；Gemini batch（不在範圍內）；送出後、寫入 external_id 前中斷時 provider 端已建立的 batch 無法取消（可能重複計費一次）。
    - 給後續切片：DIG（S6）的 summary 要用 batch 時，經 `llmChunk` 的 chunk 任務會自動適用；不經 chunk engine 的話要自行呼叫 `submitBatch`。新增會取消任務的路徑時，記得同時處理 `waiting` 狀態並提前 `batch.poll`（參考 `KickBatchPollsOfItem`）。重新啟動後 batch 由 `scheduled_jobs` 繼續輪詢，不需要額外接線。
  - **M4-S6 DIG（2026-11-23，分支 `feat/dig-summary-digest`；決定記在 DIG 待決問題的「M4-S6：實作時的決定」，待 owner 追認）**：摘要、摘要後暫停與繼續、每日／每週彙整。
    - llm／prompts：`internal/llm/summary.go`（`SummarySpec`、`CheckSummary`，含 fuzz）；schema 檢查支援 `["string","null"]`；prompt 變數 `summary_lang`；內建 prompt `summary-default`。
    - pipeline：`internal/pipeline/summary.go`（`llmStages` 登記 summary；一個請求；`too_long` 依片長或 token 估計 → `skipped`；重試用盡 → `done` 沒有產出；內容相同沿用產出）。`applyResult` 在 summary 結束後依快照的 `pause_after` 把下一步改成 `blocked`（`awaiting_user`）；`Pipeline.Continue`、`AwaitingUser`、`ItemSummary`、`SeedSummary`；`Detail.Summary`。從 summary 重跑不再 501。
    - store：migration `00025_digests`（`digests`、`digest_entries`、`stage_runs(stage, finished_at)` 索引）、`internal/store/digests.go`。
    - `internal/digest`：彙整設定 CRUD、排程工作 `digest`（當地時間對齊、DST、停機後補跑一次）、內容與預覽；經 `notify.Outbox.EmitTo` 只寫給彙整的 notifier。`scheduler.Run` 一輪最多兩趟，讓同一輪被提前的 `notify.deliver` 立刻執行。
    - API／MCP／CLI：`POST /api/items/{id}/continue`、`POST /api/items/continue`、`/api/digests` CRUD 與 `/preview`（從 `m4_stubs.go` 移除）；`ItemDetail.summary`；MCP `continue_item`；CLI `notesteme items continue <ref>...`（超過 100 部時分批）。OpenAPI 與 `schema.d.ts` 已重產。
    - steps：`features/steps/digest.go` 實作 DIG 的三個句型；`notifyGate`、`subGate` 對 `@DIG-R*` 放行；彙整在 notifier 之後寫入（steps.md 4.18）。
    - 數字（基準 origin/master a0b3e60）：預設 tag 308 passed、`@M3` 109 passed、`@M5` 74／74，逐場景相同；`@M4` 371 個：138／233 → 163 passed、208 pending，只有 25 個 DIG 場景 pending → passed（DIG 的自動化場景全部通過）；`features/passing.txt` 不變。涵蓋率 pipeline 84.6%、queue 89.2%、subtitle 95.8%、netguard 94.4%、digest 約 90%。
    - 沒做：UI（影音詳情的摘要、媒體庫「等你決定」篩選與批次繼續、設定頁的彙整），`@ui @manual` 場景「Continuing several items from the library」；真實 LLM 的摘要品質（owner 驗收）。
    - 給之後的切片：(1) UI 用 `GET /api/items/{id}` 的 `summary` 與 `blocked=awaiting_user` 篩選，批次繼續用 `POST /api/items/continue`；(2) 其他排程工作若會把別的工作提前到現在，同一輪就會執行（最多兩趟），不要寫出一直排回現在的工作；(3) 新的事件若只送給特定 notifier，用 `Outbox.EmitTo`；(4) 新的 M4 句型閘門要記得 DIG 場景已經放行 NOTIFY 與 SUB 的句型，改閘門時做 `@M4` 逐場景比對。
  - **M4-S8 SRCH（2026-11-23，分支 `feat/srch-search`；決定記在 SRCH 待決問題的「S8 實作時的決定」，待 owner 追認）**：字幕全文搜尋。
    - 產品：migration `00026_search`（`search_cues`、FTS5 外部內容表 `cues_fts`（`trigram case_sensitive 1`）與三個 trigger、`search_index_state`）；`internal/search`（查詢驗證、`strings.ToLower` 摺疊、FTS5 片語引號、UTF-16 的 highlights、變體順位）；store 的 `InsertArtifact`／`RestoreArtifact`／`WithdrawArtifacts` 對 transcript 在同一個交易中重建該語言的索引列，永久刪除同交易刪除；開啟 DB 時若 `built = 0` 以 Go 回填；`GET /api/search`（`internal/api/search.go`，keyset 游標）、MCP `search` 的 `mode: fulltext`、CLI `notesteme search`。
    - harness：`features/steps/library.go` 實作 SRCH 的六個句型；`Server.SeedItemCreatedAt`、`Server.SearchIndexCheck`（測試專用）。
    - 數字（基準 rebase 後的 origin/master ed078a9）：預設 tag 308 passed、`@M3` 109 passed、`@M5` 91 passed／57 pending，逐場景相同；`@M4` 371 個：201／170 → 231 passed、140 pending，只有 SRCH 的 30 個 case（20 個自動化場景，兩個 Scenario Outline 展開成 12 個範例列）pending → passed，SRCH 的自動化場景全部通過；第 21 個場景「Search results jump to the cue in the item page」是 `@ui @manual`，不在 371 個之內，屬 owner 人工驗收。`features/passing.txt` 不變。
    - 給之後的切片：(1) 新的字幕寫入路徑（BND 匯入等）只要經 `Repo.InsertArtifact` 就會更新索引，**不要**繞過它直接寫 `artifacts`；(2) UI 的搜尋頁用 `GET /api/search`，`highlights` 是 UTF-16 位移，直接 `slice`，不要把 `text` 當 HTML；(3) 新 migration 從 00027 起；(4) 在 store 內新增改寫 `superseded_by` 的 Repo 方法時，要比照 `InsertArtifact`／`RestoreArtifact`／`WithdrawArtifacts` 呼叫索引更新（`reindexSearch` 未匯出，只在 store 內使用）。
  - **M4-S7 SRC-10 更好的字幕來源（2026-11-23，分支 `feat/src-10-better-source-reprobe`；決定記在 SRC 待決問題的「SRC-10 實作的決定」R8～R16，待 owner 追認）**：重新 Probe、`better_source` 與 SponsorBlock 的重新查詢。
    - store：migration `00027_reprobes`（`source_probes.probe_trigger`、`source_reprobes`）、`internal/store/reprobes.go`（`Reprobe`／`SaveReprobe`、只改訊息與動作的 `SetStageMessage`、`InsertProbesFor`）；`LatestProbes` 只取 `stage` 的 Probe。
    - pipeline：`internal/pipeline/reprobe.go`。transcript、sponsorblock 完成時在寫回的交易中（`withReprobe` 串在 `res.inTx` 後面）判斷資格並以 `ScheduleJobIfAbsent` 排定 `source.reprobe`；`Pipeline.Reprobe` 是排程工作的本體（期限、垃圾桶、只列字幕軌、`RuleIndex` 比較評估順序、同交易寫 `source.better_available` 事件與 outbox、transcript 的訊息與動作、`rerun` 時以 backfill 自動重跑一次、有 `edited` 時只通知；SponsorBlock 結果不同時建新版本並帶提醒）。`Rerun` 在交易中呼叫 `endReprobeOnRerun`，`RerunRequest.Priority` 是新欄位。`transcript.RuleIndex`、`events.Event` 的 `current`／`better`／`action`／`estimate_usd`。
    - app：`internal/app/reprobe.go` 登記 `source.reprobe`，平台額度用 `subscription.Service.TakePlatform`（與訂閱共用 SUB-R10 的 bucket）。
    - API：`GET /api/items/{id}` 的 `reprobe`、`better_source`（一律有這兩個欄位，沒有時是 `null`；OpenAPI 以 `oneOf [ref, null]` 表示，見 `internal/api/nullable.go`）；MCP `get_item` 經同一條路由自動帶上；OpenAPI 與 `schema.d.ts` 已重產。
    - steps／harness：`the platform adds … captions in … to … at …` 實作（`features/steps/better_source.go`）；假 yt-dlp 讀 `fake/now`（`faketool.WriteNow`，`the clock is at`／`advances by` 與這個步驟寫入），`Video.Later` 的字幕軌到時間才列出與下載；`spbGate` 對 `@SRC-R20` 放行。
    - 數字（基準 rebase 後的 origin/master 99ca595，SRCH 已合併）：預設 tag 308 passed、`@M3` 109 passed、`@M5` 91／57，逐場景相同；`@M4` 371 個：231／140 → 243 passed、126 pending、2 failed，改變的只有 14 個 SRC-10 場景（12 個 pending → passed，2 個 failed 見下）。`go test -race ./...` 通過。`features/passing.txt` 不變。
    - **需要 owner 決定**：兩個 SRC-10 場景與規則衝突而 failed（SRC 待決問題 R16，`.feature` 沒有修改）：asr001 撞上 SRC-R6 的等待（建議改 `published_at`）；手動重跑後 SRC-R20 的 SponsorBlock 部分仍在（建議預期改成 `"reprobe": { "sources": false }`）。
    - 沒做：UI 的提醒與重跑前顯示金額（`@ui @manual`）；`estimate_usd` 的 LLM 估算（ING-08 的 LLM 費用還沒有）；Podcast 的 `<podcast:transcript>` 事後補上（規格不在範圍內）；CLI `items show` 的文字模式沒有另外列出 `better_source`（`--json` 有）；真實 YouTube／Bilibili 的驗收（owner）。
    - 給之後的切片：(1) 讀 `source_probes` 時注意 `probe_trigger`；(2) 其他會改變 transcript 或 sponsorblock 的路徑（例如 BND 匯入、LIB 的批次重跑）若不經 `Rerun`，要自己呼叫 `endReprobeOnRerun` 或讓步驟重新完成；(3) UI 的「改用更好的字幕重跑」就是 `POST /api/items/{id}/rerun {from: transcript}`。
  - **BND bundle 匯出與匯入（2026-11-23，分支 `feat/bnd-bundle-export-import`；決定記在 BND 待決問題 N9～N22，皆待 owner 追認）**：
    - 產品：`internal/bundle/write.go`（`Write` 寫出 bundle v0，manifest 是第一個項目，確定性的 bytes；`ValidLang`）；`internal/pipeline/bundle_export.go`（`ExportBundle`、`InstanceID`、來源紀錄的清理與最後的洩漏掃描）、`bundle_import.go`（`ImportBundle`：`bundle.Read` + `ValidateContents` 之後以產品的解碼器重新編碼，暫存目錄 → artifacts → 一個交易；BND-R9 的衝突、tombstone、`add_versions` 的略過；frames 重新編號與縮圖）；store 查詢 `ItemArtifactSHAs`（沒有 migration）；`pathguard.Guard.Roots`。
    - API／CLI：`GET /api/items/{id}/bundle`、`POST /api/bundles/import`（從 `m4_stubs.go` 移除；匯入由 `bundleImportIntercept` 在 Huma 之前處理）；`apierr.Error` 加 `entry`、`error_class`（Client 與 Hub 的 OpenAPI、`schema.d.ts` 已重產）；`api.Deps.BundleImport` 接設定檔 `bundles.import`；CLI `export <item> --format bundle [--out]`、`import <bundle> [--add-versions] [--json]`（以檔名或 zip 簽章判斷）。MCP 沒有（規格如此）。
    - steps：`features/steps/bundles.go` 實作 4.20 的 BND 句型；`noteIs` 對 `@BND-` 放行；`startServer` 之後寫入場景的實例 ID；`the owner runs` 先建立 `<場景根目錄>/tmp`。沒有新句型。
    - 數字（基準 rebase 後的 origin/master 4763298，SRCH 與 SRC-10 已合併）：預設 tag 308 passed、`@M3` 109 passed、`@M5` 91 passed／57 pending，逐場景相同；`@M4` 371 個：245／126 → 286 passed、85 pending、0 failed，只有 41 個 BND 場景（29 個 Scenario，Outline 展開後 41 個）pending → passed；`features/passing.txt` 不變。
    - 沒做：UI（媒體庫的批次「匯出 bundle」屬 LIB-R16～R21 的 `export_bundle`，影音詳情的匯出選單、設定頁的匯入）與 `@ui @manual` 場景「An imported note is shown without raw HTML」。
    - 給之後的切片：(1) LIB 的 `export_bundle` 直接用 `Pipeline.ExportBundle`（scope 同匯出的 `read`）；(2) 匯入走 `store.InsertArtifact`，匯入的字幕已確認會進 SRCH 的全文索引（rebase 到 SRCH 之後的單元測試）；(3) HSHR 要寫 bundle 時用 `bundle.Write`（`OutCreatedBy.InstanceID` 留空即省略），不要另寫一套；(4) 匯入的筆記沒有 `images`，寫進 vault 時不帶圖片（N12）；(5) `add_versions` 匯入 transcript／sponsorblock 時結束 SRC-10 的重新 Probe（N22）；(6) 匯入一次只做一個且整份在記憶體中（N18），正式環境若要放大上限要改成串流。
  - **M4-S11 AUTH（2026-11-23，分支 `feat/auth-access-control`；決定記在 AUTH 待決問題的「AUTH 切片的實作決定」A9～A22，皆待 owner 追認）**：token 輪替、工作階段管理、反向代理驗證、OIDC 登入。
    - store：migration `00028_auth_sessions`（`sessions.method`／`last_seen_at`／`proxy_user`、`oidc_identities`、`oidc_logins`）；`internal/store/oidc.go`（state 以 `DELETE … RETURNING` 原子取出）；`RotateAPIToken` 原地換 hash（保留 `id` 與 `expires_at`）。
    - `internal/oidc`（新）：discovery／JWKS 快取（JWKS 最多每分鐘重抓一次）、PKCE S256、`client_secret_basic`、id_token 驗證（只接受 RS256／ES256，alg 與金鑰種類必須相符，`iss`／`aud`／`azp`／`nonce`／`exp`／`iat` 60 秒偏差），含 fuzz；HTTP client 走 netguard，只把 issuer 主機加進私有網段允許清單。
    - `internal/auth`：`proxy.go`（只信任 TCP 對端在 `trusted_proxies` 的請求、值的格式檢查、`allowed_users` 常數時間比對、log 節流）、`oidc.go`（`StartOIDC`／`FinishOIDC`，未完成的登入上限 1000）、`sessions.go`（列出、撤銷、輪替）；proxy session 綁定標頭的值，標頭消失或改變時撤銷。
    - API：`GET /api/auth/sessions`、`DELETE /api/auth/sessions/{id}`、`POST /api/tokens/{id}/rotate`、`GET /api/auth/oidc/start`、`/callback`、`GET /api/auth/methods`（新的免驗證端點）；三個 AUTH stub 從 `m4_stubs.go` 移除；OpenAPI 與 `schema.d.ts` 已重產。CLI `notesteme tokens rotate <name|id> [--json]`。
    - Web：登入頁的「用 <主機> 登入」（帶 `next`）、設定頁 token 的輪替、登入分區的工作階段列表與撤銷（三個語系）。
    - steps：`features/steps/signin.go` 實作 steps.md 4.21 全部句型；`scenario.go` 只加了 `signin` 欄位、cookie jar 與啟動前寫入設定。
    - 數字（基準 rebase 後的 origin/master 4763298，含 SRC-10）：預設 tag 308 passed、`@M3` 109 passed、`@M5` 91 passed／57 pending，逐場景相同；`@M4` 371 個：245／126 → 283 passed、88 pending、0 failed，改變的只有 38 個 AUTH 場景（pending → passed）；`features/passing.txt` 與 `.feature` 沒有變動。涵蓋率 oidc 90.8%、auth 88.7%、api 80.1%。新 migration 從 00029 起。
    - 沒做：真實 IdP 與真實反向代理的部署驗收、2 個 `@ui @manual` 場景（owner）；稽核紀錄只寫 log，沒有另外建表（A21）。
    - 給之後的切片：(1) 新的免驗證路徑要加進 `internal/api/middleware.go` 的 `needsAuth`（只比對精確路徑與 GET）；(2) 需要「目前是誰」時用 `auth.Principal` 的 `Method`／`SessionID`，`/mcp` 只接受密碼登入的 session 與 token；(3) 要信任來源位址或標頭時一律經 `Service.ProxyUser`、`auth.SourceAddr` 的判斷（TCP 對端），不要直接讀 `X-Forwarded-*`；(4) `X-Forwarded-Proto` 仍接受任何來源（A20，待 owner 確認）。
  - **M4-S9 GLOS 術語審核（2026-11-24，分支 `feat/glos-glossary-review`；決定記在 GLOS 待決問題 G6～G16，皆待 owner 追認）**：接受時改寫術語、選擇頻道或全域詞彙表、批次決定、跨頻道的審核清單。
    - store：migration `00029_glossary_review`（`glossary_candidates.decided_term`／`decided_scope`／`decided_at`，`(state, created_at, id)` 索引）；queries `ReviewTermCandidates`、`CountReviewTermCandidates`、`GetTermCandidateByID`、`InsertTermCandidateAt`（seeding）；`DecideTermCandidate` 改成寫入決定的內容與時間（仍只更新 `pending`）。
    - pipeline：`glossary_review.go`（`checkTermDecision` 的清理與 422、`applyTermDecision` 是 PATCH 與批次共用的交易內邏輯：冪等、409、大小寫相同不重複、詞彙表已滿回 `ErrGlossaryFull`、更新後重新讀出候選；`DecideTermCandidates` 整批一個交易、`*TermBatchError` 指出第一個出錯的項目；`ListTermCandidates`）；M2 的 `DecideTermCandidate` 改成薄包裝。`llm.CleanTerm` 匯出 C4 的清理。
    - API：`GET /api/term-candidates`（read）、`POST /api/term-candidates/decide`（admin）從 `m4_stubs.go` 移除，實作在 `internal/api/term_review.go`；PATCH 的 body 加 `term`、`scope`；`TermCandidate` 加 `decided_*`；新錯誤碼 `glossary_full`（409）。規格指定 path 的欄位都不用 huma 的 enum／長度限制，改由 pipeline 檢查。OpenAPI 與 `schema.d.ts` 已重產（Web 沒有其他改動）。MCP、CLI、通知：規格明寫不做。
    - steps：`features/steps/glossary_review.go` 實作 4.20 的四個 GLOS 句型（只有 GLOS 場景使用，不需要閘門）；`scenario.go` 只加了 `termCandidates` 欄位與 `prepareOwner` 最後的 `flushTermCandidates`；`app.Server.SeedTermCandidate`。沒有新句型。
    - 數字（基準 origin/master 05c60db）：預設 tag 308 passed、`@M3` 109 passed、`@M5` 91 passed／57 pending，逐場景相同；`@M4` 371 個：324／47 → 336 passed、35 pending、0 failed，只有 12 個 GLOS 場景 pending → passed；`features/passing.txt` 與 `.feature` 沒有變動。涵蓋率 pipeline 84.3%、api 80.5%。變異測試（拿掉全域範圍、改寫、整批回滾、重新讀出候選、清理）都有場景失敗。
    - 沒做：GLOS-R7 的 UI（設定頁「詞彙表」分區的審核清單、改寫後接受、多選；總覽「需要你處理」的「有 N 個術語等你確認。」）與 `@ui @manual` 場景，屬後續 UI 切片與 LIB-09；UI-3b 的詞彙表頁仍只用 C4 的單一頻道 API。
    - 給之後的切片：(1) 總覽（LIB-09）的術語提醒用 `Pipeline.ListTermCandidates(ctx, TermCandidateQuery{Limit: 1})` 的 `Total`，不要另寫 COUNT；(2) UI 的審核頁直接用 `GET /api/term-candidates` 與 `POST /api/term-candidates/decide`，錯誤的 `errors[0].path` 是 `decisions.<n>[.<欄位>]`，可以對回第 n 列；(3) 要再加詞彙表的寫入入口時，經 `addGlossaryTerm`（大小寫相同不重複、500 個詞上限回 `ErrGlossaryFull`）；(4) 處理方案層的 `glossary`（PRF-R1）仍不經審核。
  - **M4 OPS 無人值守（2026-11-24，分支 `feat/ops-backup-alerts`；決定記在 OPS 待決問題的「M4 OPS 實作的決定」O11～O22 與 ADR-0021 的修訂段，皆待 owner 追認）**：每日備份、系統告警、VPS profile。
    - store：migration `00030_ops`（`alert_states`〔多一個 `message` 欄位〕、`tool_call_stats`〔5 分鐘時間格〕、`secrets.last_ok_at`）、`internal/store/ops.go`（告警狀態、工具統計、cookie 的最後成功使用、`FailingScheduledJobs`、`ActiveStageRuns`）。
    - 產品：`internal/ops`（`backup.daily` 的排程、空間檢查、固定的失敗原因、保留與清理；`alerts` 每 15 分鐘的六種告警與遲滯、安靜解除；`deploy-check vps` 的檢查；`Calls` 記錄 yt-dlp 結果與 cookie 使用）；`backup.SaveWith`（in-process 寫出）與隨機名稱的 `latest` 暫存 symlink；`notify.Alert` 加 `key`／`message`／`since`／`summary`；YouTube 與 Bilibili client 的 `Calls`；`app.registerOps`（**只有 serve** 登記兩個排程，CLI 獨立模式不跑）；`serve --insecure-no-auth` 的解析（vps 拒絕，local 回「尚未實作」）；vps 部署時 session cookie 一律 `Secure`；`GET /api/system` 的 `alerts[]`、`backup{last_ok_at,last_error,next_run_at}`（OpenAPI 與 `schema.d.ts` 已重產）；CLI `notesteme deploy-check vps`。
    - compose：`notesteme` 服務帶 `NOTESTEME_DEPLOYMENT`（預設 `local`）；`vps` profile 的 `vps-check`（資料 volume 唯讀、不設 restart）與 `caddy`（`deploy/docker/Caddyfile`，80／443，`caddy-data`、`caddy-config` volume）；`.env.example` 有 vps 說明。
    - harness：`features/steps/ops_unattended.go` 實作 OPS-06～08 的 8 個句型，只對 `@OPS-R11`～`@OPS-R21` 放行（`opsGate`，`notifyGate` 排除它）；`World.SetDiskUsage`（預設 10%，O10）；`When the owner runs "docker compose …"` 經 fakecompose 時帶 `.env` 表格的變數。
    - 數字（基準 rebase 後的 origin/master 05c60db，BND 與 AUTH 已合併）：預設 tag 308 passed、`@M3` 109 passed、`@M5` 91 passed／57 pending，逐場景相同；`@M4` 371 個：324／47 → 342 passed、29 pending、0 failed，改變的只有 18 個 OPS 場景（pending → passed）。`internal/ops` 涵蓋率約 89%。`features/OPS-unattended.feature` 共 19 個場景：R18 兩個 `@pending-m0`（預設排除；本機暫時拿掉排除時通過）、「總覽顯示告警」是 `@ui @manual`。`features/passing.txt` 不變。
    - 沒做：compose 的 `hub` profile（M5）；doctor 的 OPS 檢查（O22）；MCP（規格沒有列）；Web UI 的系統告警與備份狀態（讀 `GET /api/system`）；真實 docker／VPS／Caddy 的 ACME 與唯讀掛載下的 `vps-check`（owner）。
    - 給之後的切片：(1) 新的告警種類照 `internal/ops/alerts.go` 的 `condition` 模式加，經 `raise`／`resolve` 才會寫 `alert_states` 與 outbox；(2) 其他會呼叫外部工具的 client 要記失敗率時用 `ops.Calls.ToolCall`；(3) 新 migration 從 00030 起；(4) AUTH-R3 的無密碼模式實作時，改 `internal/app/serveopts.go` 的 `errInsecureNotImplemented` 分支；(5) 新的排程工作若不該出現在 `job_stale`，加到 `staleJobs` 的略過清單。
  - **M4-S13 LIB-08、09 批次操作與總覽（2026-11-24，分支 `feat/lib-bulk-overview-api`；決定記在 LIB 待決問題的「M4-S13 LIB-08、09 的實作決定」L-M10～L-M17，皆待 owner 追認）**：只有後端。
    - store：migration `00032_lib_read_state`（`items.read_state`、部分索引 `stage_runs_attention`、`stage_runs_finished`）；`internal/store/overview.go` 與 `queries/overview.sql`（最新一次執行的 running／blocked／failed、今天的事件、最近完成；執行計畫由 `overview_test.go` 守住，不掃整張 `stage_runs`、`events`）。
    - cost：`Service.Estimate`（`Reserve` 與 dry_run 共用，測試控制的金額優先）、`Estimated`、`LoadZone`、`ZoneMessage`。
    - pipeline：`prepareRerun`（從 `Rerun` 抽出寫入之前的檢查與組合，`Rerun` 行為不變）；`bulk.go` 的 `ResolveBatch`（代號、ID、不帶分P的 `bili:<BV>` 展開、去掉重複）、`SetReadState`、`EstimateRerun`（L-M10）；`overview.go` 的 `Overview`。`internal/diskusage`（LIB-R22，快取 5 分鐘、單次 30 秒、不跟隨 symlink）。
    - API：`internal/api/batch.go`（`POST /api/items/batch`：路由只要求已登入，scope 依動作在讀影音之前判斷；每部重用單一影音的方法；`items.batch` 事件）、`overview.go`（`GET /api/overview`）；`Item.read_state` 與 `GET /api/items?read_state=`；`trashOne` 讓單一刪除與批次共用。OpenAPI 與 `schema.d.ts` 已重產（`web/client/src/test/fixtures.ts` 補 `read_state`）。沒有 MCP 與 CLI（規格如此）。
    - steps：`costGate` 對 `@LIB-08`、`@LIB-09` 放行；`the effective config of … contains:` 在預期內容的頂層鍵都不是快照區段時改與 `stages.<步驟>` 比對（steps.md 4.11，L-M12）。沒有新句型，沒有修改 `.feature`。
    - 數字（基準 origin/master 16ad886，已含 AUTH、GLOS、OPS、HSHR、UI-5）：預設 tag 308 passed、`@M3` 109 passed、`@M5` 133／15，逐場景相同；`@M4` 371 個：354／17 → 371 passed、0 pending、0 failed，改變的只有 17 個 LIB-bulk-overview 場景 pending → passed（LIB-08、09 的自動化場景全部通過）；`features/passing.txt` 不變。涵蓋率 pipeline 84.5%、api 81.0%、diskusage 96.7%。
    - **需要 owner 決定**：L-M11 `export_bundle` 實際執行時回傳每部的 `bundle_url`，不合併成一個檔（LIB-R20 與 BND「不在範圍內」衝突）。
    - 人工驗收（owner）：2 個 `@ui @manual` 場景（總覽的區塊、批次刪除的確認對話框），要等 UI 切片。
    - 給 UI 切片：(1) 批次操作先送 `dry_run: true` 取得 `count`、`estimated_usd`、`unknown_estimates`（> 0 時要提示「有 N 個步驟無法估算」）、`budget.exceeds`、刪除的 `deletion`，確認後再送一次不帶 `dry_run`；(2) 回應的 `items` 依請求順序，`ok: false` 的列顯示 `code`／`message`；(3) `export_bundle` 用每部的 `bundle_url` 逐一下載；(4) 總覽的「需要你處理」直接用 `actions`（繁中字串，同影音詳情的對照表），沒有項目時隱藏區塊；`now.more` 連到佇列；`tz` 帶瀏覽器的 IANA 時區；`disk` 的數字可能是 `null`（還沒統計完成）；`notices` 除了預算提醒還可能有 `terms_pending`（GLOS-R7，「去審核」連到術語候選審核）；(5) `items.batch` 會經 SSE 送出，媒體庫收到後可以重抓列表；(6) 「等你決定」的批次繼續仍用 `POST /api/items/continue`（不是批次動作）。
- **M5 規格展開（2026-11-23，分支 `docs/m5-hub-specs`，只有文件、場景、步驟樁與假服務骨架，沒有產品程式）**：
  - 四份規格（狀態 `ready`）與 `.feature`：[HACC](specs/HACC-hub-accounts.md)（HUB-01、07）、[HREP](specs/HREP-hub-repository.md)（HUB-02 Hub 端、03、05 Hub 端、06）、[HSHR](specs/HSHR-hub-sharing.md)（HUB-02 Client 端、04、05）、[HMCP](specs/HMCP-hub-mcp.md)（HUB-08）；跨規格的決定在 [ADR-0030](adr/0030-hub-deployment-and-storage.md)（獨立 binary `cmd/notesteme-hub`、SQLite＋fs 為參考 backend／Postgres＋S3 為正式環境、測試替身依被測系統切開）、[ADR-0031](adr/0031-hub-identity-permissions-and-content.md)（身分與帳號連結、權限與內建群組、版本鍵與去重、衍生關係、`model_verified`、下架與濫用防護、資料表）、[ADR-0032](adr/0032-client-hub-protocol-and-privacy.md)（`/api/v1` 契約、送出的 bundle 白名單、ASR 前查詢只送 hash 前綴、錯誤對應）。ADR-0012、0005 的狀態改成已採納（細節由 0031、0032 定案）。
  - `@M5` 場景：`go test ./features -run TestFeatures -godog.tags='@M5'` 為 148 個場景全部 pending（`@ui`、`@manual` 另計）；每個 M5 的 Background 第一步是閘門句型（`Given a Hub is running` 或 `Given the owner's client is connected to a Hub`）。預設 tag 的 308 個場景不受影響。
  - 新句型在 `steps.md` 4.30～4.32（步驟樁在 `features/steps/hub.go`、`hub_content.go`、`hub_client.go`）；第 3 節加了字幕來源 ID `hub`。假服務骨架（M5-S0 已接進 harness，見下面的 M5-S0 bullet）：`features/internal/fakeoauth`、`fakehub`，設計見 `features/fixtures/README.md`「M5 的假服務」。
  - 和 M4 的衝突點：Hub 的 bundle 驗證沿用 BND-R4～R7（M4 分支），但送給 Hub 的 bundle 沒有 `created_by.instance_id`，Hub 的上傳上限也比 BND 小（HREP 待決問題 R1）。M4 合併後由 Hub 的第一個實作切片處理。
  - 要 owner 追認的決定：ADR-0030～0032 全文；HACC A1（PKCE 一律送）、A2（不依 email 合併帳號）、A4（新帳號只有 `read`、`download`）、A5（下架申請不需要帳號）；HREP R1（BND 的對齊）；HSHR S1（ASR 前查詢只送 hash 前綴，偏離 plan 第 20 節）、S2（`cookie_required` 的定義與 SRC 的來源紀錄新欄位）、S3（分享用 `admin` scope）、S4（只用原始字幕重新生成）、S7（Client 對 Hub 的逾時用注入的 clock）；HMCP M1（`get_transcript` 需要 `download`）。
  - **M5-S0 骨架（2026-11-23，分支 `feat/m5-s0-hub-skeleton`；決策記在 HACC A7～A9、HSHR S8、HREP R7、HMCP M3 與 ADR-0030 的修訂行，皆待 owner 追認）**：
    - 數字：預設 tag 308 passed（不變）；`@M5` 148 個場景切片前後**逐場景相同**，全部 pending（106 個停在 `Given a Hub is running`、42 個停在 `Given the owner's client is connected to a Hub`）；`@M3` 109、`@M4` 371 pending 逐場景不變；`features/passing.txt` 沒有變動。
    - Hub：`cmd/notesteme-hub`（`serve`、`migrate`、`doctor`、`version`；`admin grant|revoke|suspend` 只檢查參數）、`internal/hub`（`hub.Open` 組裝，serve 與 harness 共用）、`hubconfig`（設定檔與環境變數，驗證規則見 HACC A7）、`hubstore`（介面與 `storetest`；`sqlite` 的 migration 00001 只有 `users`、`identities`，`sqlc.yaml` 獨立；`postgres` 是佔位）、`hubblob`（fs；s3 佔位）、`hubapi`（`/healthz`、`/readyz`、`GET /api/v1/openapi.json`；HACC、HREP 的 33 條 `/api/v1` 路由與 `/auth/{provider}/start|callback`、`/mcp` 都是 501 stub，`stubs.go` 是權限矩陣）、`hubcli`。OpenAPI 快照 `internal/hub/hubapi/openapi.json`；`fakehub.Routes` 的 contract test 只確認路由存在。共用套件抽出 `internal/apierr`（錯誤格式與 `huma.NewError`）與 `internal/sqlitedb`；Hub 不 import pipeline 等套件有測試守著。
    - Client：設定檔接受 `hub.url`（https）與 `hub.precheck.enabled`；501 stub：`GET`／`PUT /api/settings/hub`、`POST /api/items/{id}/hub/share`（`admin`）、`POST /api/hub/import`（`ingest`）；OpenAPI 快照與 `schema.d.ts` 已重產（`Error` 多了 `required_permission`）。
    - harness：`World.StartHub`（程序內真 Hub，SQLite 與 blob 在場景的 `hub/`，設定檔 `config/notesteme-hub.yaml` 由預設設定與 `AddHubConfig` 合併：物件逐鍵、陣列取代；設定改變後 `RestartHubIfNeeded` 以新設定重啟、資料保留；Hub 的對外連線與 fakeoauth 都經 fakenet、時間用場景的 fake clock）、`World.StartFakeHub`（`TestHubToken`），兩者互斥；`hub.example.org` 與 OAuth 的真實主機名稱只在啟動時登記。`HubHTTPClient` 不跟隨轉址。`SubstRequest` 只在請求端（路徑、body、CLI 參數）代入 `{version:}`、`{user:}`、`{takedown:last}`、`{report:last}`、`{share:last}`（`SetHubRef` 記錄），預期 JSON 不展開。OAuth client secret 與 fakehub token 加進 log 的 secret 掃描。M5 的步驟樁（含兩個閘門）維持 pending，沒有新句型。
    - CI／建置：sqlc job 多了 Hub 的 diff 與 vet；Dockerfile 的 `notesteme-hub` target（在 Client 的 stage 之前，預設 target 不變）；goreleaser 加 linux 的 `notesteme-hub` build 與 archive，release-check 跑它的 `--version`（本機沒有跑 goreleaser 與 docker build，交給 CI）。
    - 沒做（後續項目）：Postgres 與 S3 backend、Postgres 的 CI service container、compose 的 `hub` profile；session 金鑰的環境變數名稱（S1）；`fakehub.Hang` 改成依 fake clock 結束（S7，HSHR S7）；contract test 的必要欄位比對（S3、S4）；Client 的 `hub_shares`、`hub` 來源 ID、`notesteme hub` CLI（S7、S8）；BND 驗證器的 `instance_id` 選填（HREP R1，S3）。
    - 給後續切片的順序提醒：S1 先把 `Given a Hub is running` 接到 `World.StartHub`（`the Hub config contains:` 接 `AddHubConfig`、送到 Hub 的請求之前呼叫 `RestartHubIfNeeded`、請求用 `HubHTTPClient` 並和 Client 共用「最近一次回應」），解除閘門後先跑 `@M5` 逐場景比對，確認 HSHR 的 42 個仍停在自己的閘門；權限 middleware（S2）依 `hubapi.Stubs` 的 Access 在讀 body 之前檢查；實作某條 Hub 路由時從 `Stubs` 移除並更新 `stubs_test.go` 的清單、重產 Hub 的 OpenAPI 快照；Hub 加資料表時加 `internal/hub/hubstore/sqlite/migrations/00002_*.sql`，並在 `storetest` 寫兩種 backend 共用的測試。
  - **M5-S1 HACC 登入（2026-11-23，分支 `feat/hub-oauth-login-sessions`；決定記在 HACC A1 的補充、A10、A11，皆待 owner 追認）**：
    - 數字：預設 tag 308 passed（逐場景相同）；`@M5` 148 個：HACC-R1～R8 的 20 個場景 pending → passed，其他 128 個仍 pending 且停在切片前的同一個閘門（86 個 `Given a Hub is running`、42 個 HSHR `Given the owner's client is connected to a Hub`）；`@M3`、`@M4` 逐場景不變；`features/passing.txt` 沒有變動（`@M5` 不在預設 tag）。
    - 產品：`internal/hub/hubauth`（OAuth 授權碼流程：state 32 bytes、PKCE S256 的 verifier 64 字元並以 session 金鑰加密存放、Google 的 nonce 與 `id_token` 驗證、GitHub／Linux.do 的使用者資訊與 `orgs`／`trust_level`；callback 原子取用 state；連結、移除、session（30 天閒置、輪替、登出）、停用帳號的檢查；`/me`）；`internal/idtoken`（RS256 `id_token` 與 JWKS 快取，只用注入的 clock，含 fuzz，之後 Client 的 OIDC 也可以用）；`internal/websession`（從 `internal/auth` 抽出的隨機 token、hash、CSRF 推導，Client 與 Hub 共用，推導值不變）；`hubapi/auth.go`（Huma 的身分驗證 middleware：依 `x-notesteme-access` 在讀 body 之前回 401、CSRF 403、停用 403；`GET /api/v1/auth/providers`、`POST /api/v1/auth/logout`、`GET /api/v1/me`、`POST /api/v1/me/identities/{provider}/link`、`DELETE /api/v1/me/identities/{provider}` 與 `/auth/{provider}/start|callback`，已從 `Stubs`／`RawStubs` 移除，`stubs_test.go` 與 Hub 的 OpenAPI 快照已更新）；migration `00002_sign_in.sql`（`oauth_states`、`sessions`、`audit_log`）與 `storetest` 的共用測試；`hubconfig` 的 `NOTESTEME_HUB_SESSION_KEY`（有啟用登入方式時必填，`doctor` 會檢查）。Client 的路由沒有變動（不需要重產 Client 的 OpenAPI 與 `schema.d.ts`）。沒有新句型。
    - harness：4.30 的登入句型、`the hub member … sends`、`the Hub client sends`（含 `with:`、`without a CSRF token`）、`the number of Hub accounts is`、`the response does not contain` 已實作，請求先 `RestartHubIfNeeded`、用 `HubHTTPClient`，與 Client 共用 `Scenario.last`（缺 CSRF 與匿名請求設 `preRoute403`）；每個人的 Hub cookie 以名稱保存（清除指令刻意忽略，同 Client）。閘門 `hubGate` 只放行登入範圍的 HACC 場景；session 金鑰 `harness.TestHubSessionKey` 已加進 `hubEnv` 與 `HubSecrets()`。`fakeoauth.Provider.User` 是新的 getter。
    - 給 S2（權限與 PAT）的提醒：把 `hubGate` 的 `hubLoginRules` 加上 R9～R14、R18（S2 範圍內的規則），再跑 `@M5` 逐場景比對；`Authorization` 標頭目前在 middleware 一律 401，PAT 的驗證接在同一個位置（`accessMiddleware`），`AccessSession` 的路由對 PAT 要回 403 或 401（規格沒寫，決定後記進 HACC 待決問題）；權限檢查放在 middleware 取得身分之後、`next` 之前，`required_permission` 用 `apierr.Error.RequiredPermission`；`hubauth.Service.Groups` 目前只回 `members`，S2 要加 `group_members`（手動、規則、`admins` 的 `source: config`）並在每次登入時依 `identities.attrs_json`（已存 `trust_level`、`orgs`、Google 的 `email_verified`）重新評估規則（登入的入口是 `hubstore.SignIn` 之後、建立 session 之前）；`Permissions` 已處理聯集與 `admin`；限流（R18）要涵蓋 `/auth/<provider>/start`（目前沒有次數限制，`oauth_states` 每次寫入會清掉過期列）。停用帳號的檢查已在 session 驗證與登入時做，S5 只要加停用路由與 `hubstore` 的寫入方法；稽核紀錄 `audit_log` 與 `hubstore.AuditEntries` 已有，`GET /api/v1/admin/audit` 可以直接用。`POST /api/v1/me/tokens` 在 S1 已經先經過 CSRF 檢查（`Session requests that change data need a CSRF token` 是 S1 的場景）。Postgres backend 仍是佔位，`storetest` 新增的案例（登入、連結、state、session）實作 Postgres 時要一起通過；SQLite 的 `SignIn` 依賴單一寫入連線序列化，Postgres 要以 `ON CONFLICT` 處理第一次登入的競爭。
  - **M5-S2 HACC 權限、群組與 Hub token（2026-11-23；決定記在 HACC A12，待 owner 追認）**：
    - 數字：預設 tag 308 passed（逐場景相同）；`@M5` 148 個：HACC-R9～R13、R18 的 11 個場景 pending → passed（S1 的 20 個維持 passed，共 31），`An admin appoints a moderator` 放行後停在最後一步 `GET /api/v1/admin/takedowns`（S5 的 stub，501 → pending），其他 116 個停在切片前的同一個閘門（74 個 `Given a Hub is running`、42 個 HSHR `Given the owner's client is connected to a Hub`）；`@M3`（46 passed／63 pending）、`@M4`（371 pending）與 rebase 後的 master 逐場景相同；`features/passing.txt` 沒有變動。
    - 產品：migration 00003（`group_members`：`(user_id, group_name, source, rule_provider)`，`source` 為 `manual`／`rule`／`config`；`pats`：只存 SHA-256，權限是 JSON 陣列）；`hubstore` 的群組成員、規則同步、設定檔 admins 同步與 PAT 方法（`storetest` 共用）；`hubauth/permissions.go`（群組與有效權限每個請求重算、PAT ∩ 帳號、`RuleHolds`、登入／連結後建立 session 之前重算該供應商的規則與 `admins`、啟動時 `SyncConfigAdmins`、PAT 的建立／驗證／撤銷）；`hubapi` 的 middleware（bearer 優先、停用 403、PAT 打 session 路由 403 `session_required`、CSRF、限流、403 `forbidden` 帶 `required_permission`）、`ratelimit.go`（固定一分鐘視窗、注入 clock、`Retry-After`，`/auth/*` 也以 IP 計）、`tokens.go`（`/me/tokens`、`/admin/users/:id/groups/:group`、`/admin/audit`）。Hub 的 OpenAPI 快照已重產；Client 的路由沒有變動。
    - harness：Hub token 與 `is in the group` 的句型（直接寫 DB、不寫稽核紀錄）；登入成功時從 DB 記 `{user:<名稱>}`；建立的 Hub token 加進 log 的 secret 掃描；`hubGate` 放行 R1～R13、R18，但有其他領域 tag 或用到 4.31 內容句型的場景仍停在閘門（Before hook 收集步驟文字到 `Scenario.stepTexts`）。
    - 沒做：`GET /api/v1/admin/users`（stub）、`notesteme-hub admin grant|revoke|suspend`（仍是結束碼 1；要用 `config` 以外的 source，見 A12）、信任反向代理的 IP 設定、Postgres backend（`storetest` 的新案例要一起通過）。
    - 給 S3（HREP 分享與驗證）的提醒：權限已在 middleware 檢查（`shares-create` 需要 `upload`），handler 用 `principalFrom(ctx)` 取得帳號（PAT 或 session，`p.PAT` 非 nil 是 token）；可見度（HACC-R14）要在 handler 內依 `hubauth.Service.Groups` 判斷 `group:<name>`，看不到與不存在一律 404。實作 `the Hub has the version …` 等 4.31 句型後，把 `hubContentSteps` 的閘門條件放寬（例如依 HREP 的路由是否已實作），並加上 `hubRules` 的 R14，HACC-R13 的「Nothing but sign-in…」與 R14 的 4 個場景就會放行；之後再跑 `@M5` 逐場景比對（junit 的 pending 訊息看不出停在哪一步，用 `-godog.format=cucumber` 找第一個非 passed 的步驟）。`An admin appoints a moderator` 要等 S5 的 `GET /api/v1/admin/takedowns`。一般限流以帳號計，HREP 的 `shares_per_hour` 等另外的額度可以重用 `rateLimiter`。給 S5：`Given the hub member … is in the group "admins"` 寫的是 `source: config` 的列，之後任何讓 Hub 重新啟動的步驟（在它之後的 `the Hub config contains:`）都會被 `SyncConfigAdmins` 清掉（目前的場景沒有這種順序）；S5 的停用只要加路由與 `hubstore` 的寫入，PAT 與 session 的 403 `account_suspended` 已在 middleware。自動規則只在登入時重算的限制見 A12。
  - **M5-S3 HREP 分享與驗證（2026-11-23，分支 `feat/hub-repo-upload-verify`；決定記在 HREP R8，BND 規格補一行，皆待 owner 追認）**：
    - 數字：預設 tag 308 passed（逐場景相同）；`@M5` 148 個：HREP-R1～R6、R8～R12 的 27 個場景與 HACC-R14 的 4 個、HACC-R13 的「Nothing but sign-in…」共 32 個 pending → passed（S1、S2 的 31 個維持 passed，共 63），`An admin appoints a moderator` 仍停在 `GET /api/v1/admin/takedowns`（S5 的 stub），其他 84 個停在切片前的同一個閘門（42 個 `Given a Hub is running`、42 個 HSHR `Given the owner's client is connected to a Hub`）；`@M3`（82 passed／27 pending）、`@M4`（含 M4-S1 NOTIFY、M4-S2 SUB 的 master：84 passed／287 pending）與 rebase 後的 master 逐場景相同；`features/passing.txt` 沒有變動。
    - S3／S4 的切法：S3 = HREP-R1～R6、R8～R12 + HACC-R14；S4 = R7（模型欄位在 S3 上傳時已決定，場景用列表驗證）、R16～R19（R17 的詳情路由 S3 已實作）；S5 = R13～R15。
    - 產品：共用套件 `internal/bundle`（BND-R4～R7：項目名稱與種類、項目數、以 `OpenRaw` 實際解壓計數的大小與壓縮比、嚴格的 manifest schema（`provenance` 是自由的物件、`InstanceIDOptional`）、雙向一致與 sha256；`ValidateContents` 另外做內容格式；有 fuzz，fuzz 找到的 local header 越界已修）；`internal/hub/hubrepo`（白名單、模型欄位與 `model_verified`、媒體檔判斷、能不能分享、可見度 `CanSee`）；migration 00004（`videos`、`blobs`、`versions`、`version_blobs`、`shares`、`share_versions`、`derivations`、`removed_hashes`）；`POST /api/v1/shares`（multipart 在 access middleware 之後由 `rawOperations` 直接處理，Huma 只產生 OpenAPI）、`DELETE /api/v1/shares/{id}`、`GET /api/v1/versions/{id}`；Hub 的 OpenAPI 快照已重產。
    - harness：4.31 全部的句型（真 Hub）；閘門改成依 tag 放行（`hubRules` 加 R14、新增 `hrepRules`），不再依內容句型擋場景；`hubRequest.contentType` 讓分享送 multipart。
    - 沒做：fakehub 的 seeding（HSHR，S7；`the Hub has the version …` 在 fakehub 上回 pending）、Postgres backend（`storetest` 已有內容倉庫的共用案例）、S3 blob、分享與撤回的稽核紀錄（HACC-R17 的清單沒有）、`GET /api/v1/me/shares`（HACC-R16，S5）。
    - 已知限制：每個分享請求把整個 bundle（上限 `max_upload_bytes`）與解壓後的內容（上限 `max_total_bytes`，預設 256 MiB）放在記憶體中，Hub 也沒有同時上傳數的上限（Client 的上傳有 2 個名額）；正式環境上線前要加名額或改成解到暫存目錄。harness 的 `a symlink` 把項目以正確的 sha256 列進 `files`，但 symlink 的內容不是 JPEG，拿掉 mode 檢查時仍會被內容格式擋下（場景只驗 `bundle_invalid`；訊息由 `internal/bundle` 的單元測試鎖住）。steps.md 4.30 開頭仍寫「4.31 仍是步驟樁」（只增不改，沒有動），以 4.31 新增的段落為準。
    - 給 S4 的提醒：放行時把 7、16～19 加進 `features/steps/hub.go` 的 `hrepRules`（並更新 `hub_gate_test.go`），再做 `@M5` 逐場景比對。列表、影片頁、lookup 都要經過 `routes.canSee`（`hubrepo.CanSee`）：看不到的版本不出現，`removed`／`withdrawn`／`hidden` 依 R16 只列 `visible`；`hubstore` 目前只有 `VideoVersions`，篩選要加新的查詢（`versions` 已有三個模型欄位、`model_verified`、`score`，`videos` 已有 `channel_external_id` 與 `key_sha256`，key 是 `sha256("<platform>:<external_id>:<part>")`、沒有分P時 part 是空字串，`sqlite.VideoKeySHA256`）。`VersionView`、`VideoRef` 可以直接給列表用（`video` 是 `{platform, external_id, part}` 物件）。下載與 `content` 的內容在 `hubblob`（`version_blobs` 的 `content`），字幕用 `subtitle.DecodeCuesJSON` 讀回再渲染；`removed` 回 410 `removed`（`getVersion` 已有）。`the Hub version … has the cues:` 已實作（`ReplaceVersionContent`）。給 S5：投票與檢舉要先確認看得到版本（不存在與看不到一樣 404），`SetVersionState`、`AddRemovedHash` 已有；下架刪 blob 時用 `BlobRefs` 確認沒有其他版本引用。
  - **M5-S4 HREP 篩選、影片頁、取用與 lookup（2026-11-23，分支 `feat/hub-repo-list-download-lookup`；決定記在 HREP R9，待 owner 追認）**：
    - 數字：預設 tag 308 passed（逐場景相同）；`@M5` 148 個：HREP-R7、R16～R19 的 11 個場景 pending → passed（共 74 passed），其他 74 個停在切片前的同一個步驟（31 個 `Given a Hub is running`、42 個 HSHR `Given the owner's client is connected to a Hub`、`An admin appoints a moderator` 的 `GET /api/v1/admin/takedowns`）；`@M3`（109 passed）、`@M4`（138 passed／233 pending）與 rebase 後的 master（origin/master 5720e55，含 M3-S5/S6 VAULT 與 M4-S3）逐場景相同；`features/passing.txt` 沒有變動。
    - 產品：`GET /api/v1/versions`（篩選、`score`／`new` 排序、keyset 的 `next_cursor`）、`GET /api/v1/videos/{platform}/{external_id}`（分組）、`GET /api/v1/lookup`（`hubapi/listing.go`）、`GET /api/v1/versions/{id}/download|content`（`hubapi/download.go`）；`hubstore.ListingStore`（`ListVersions`、`LookupVersions`，`queries/listing.sql`，可見度以 `json_each` 的參數預先過濾，`storetest` 有共用案例）；`hubrepo.Listable`（與 `CanSee` 一致，單元測試鎖住）。query 參數一律是字串、在 handler 內驗證，422 的 `errors[].path` 是 `query.<參數>`（`apierr` 的全域 `huma.NewError` 沒有動）。Hub 的 OpenAPI 快照已重產；Client 的路由沒有變動。
    - harness：`hrepRules` 加 7、16～19，`hub_gate_test.go` 同步；沒有新句型（steps.md 4.30 過時的「4.31 仍是步驟樁」已改掉）。
    - 沒做：Range（規格沒有要求）、cursor 的簽章（竄改只換位置，不繞過可見度）、lookup 專用的限流設定（沿用 HACC-R18）、Postgres。sqlc 的 sqlite 引擎在編號參數與 `sqlc.slice` 混用時會錯位、`ORDER BY` 中的 `sqlc.arg` 不會被替換，所以 `listing.sql` 用 `json_each` 與 SELECT 中的 `sort_score` 別名；之後的查詢避開這兩種寫法。
    - 給 S5（投票、檢舉、下架、停用）的提醒：`versions.score` 是列表排序與 cursor 的鍵，投票更新 `score` 時翻頁可能重複或漏掉（HREP R9 的已知限制）；R13 的「停用帳號的票不計入」要在停用與恢復時重算 `score`（或改成查詢時計算，列表的 SQL 要一起改）。`hidden`／`removed` 已經不會出現在列表、影片頁與 lookup（SQL 與 `routes.listable` 兩層），S5 只要改 `state`；下架後下載與 `/content` 已回 410。投票與檢舉的 404 請沿用 `seeVersion`（`download.go`，看不到與不存在相同、removed 410），但檢舉 `removed` 的版本要另外決定。`GET /api/v1/me/shares`（HACC-R16）可以重用 `versionItem`。
  - **M5-S5 HREP 投票、檢舉、自動隱藏＋HACC 下架、停用與稽核（2026-11-23，分支 `feat/hub-votes-reports-takedowns`；決定記在 HACC A13、HREP R10，待 owner 追認）**：
    - 數字：預設 tag 308 passed（逐場景相同）；`@M5` 148 個：HACC-R15～R17 的 7 個、HREP-R13～R15 的 9 個與 `An admin appoints a moderator` 共 17 個 pending → passed（共 91 passed），其他 57 個停在切片前的同一個步驟（15 個 HMCP 的 `Given a Hub is running`、42 個 HSHR 的 `Given the owner's client is connected to a Hub`）；`@M3`（109 passed）、`@M4`（138 passed／233 pending）與 rebase 後的 master 逐場景相同；`features/passing.txt` 沒有變動。
    - 產品：`hubapi/community.go`（投票、檢舉）、`takedowns.go`（不需要登入的下架申請、IP 的每小時額度、聯絡 email 的信封加密）、`moderation.go`（下架與檢舉的決定、手動隱藏、使用者列表、停用與恢復、`GET /api/v1/me/shares`）；`hubstore.CommunityStore`（`sqlite/community.go`、`queries/community.sql`、migration `00005_community`，`storetest/community.go` 是共用案例）；`notesteme-hub admin grant|revoke|suspend|unsuspend`（`hub.OpenStore`）。`hubapi.Stubs` 已經是空的（HMCP 的 `/mcp` 在 `RawStubs`）。`notesteme-hub serve` 現在需要 `NOTESTEME_HUB_MASTER_KEY`（≥ 32 字元），`doctor` 也會檢查；Hub 的 OpenAPI 快照已重產，Client 的路由沒有變動。
    - harness：`hubRules` 加 15～17、`hrepRules` 加 13～15（`hub_gate_test.go` 同步，另加規格沒有的規則編號維持 pending 的案例）；`the hub member … is suspended` 接到 `SuspendUser`；`hubDo` 在建立下架申請與檢舉回 201 時記下 `{takedown:last}`、`{report:last}`；真 Hub 的環境變數加測試主金鑰 `TestHubMasterKey`（也在 log 的 secret 掃描）。沒有新句型。
    - 沒做：取消手動隱藏的路由（規格沒有，見 HREP R10，待 owner 決定）、換主金鑰、下架申請的 IP 額度跨重新啟動保存（刻意不存 IP）、多個 Hub 程序共用額度、Postgres、Hub UI 的管理頁（S9）、email 通知（規格不在範圍）。
    - 給 S6～S9 的提醒：S6（HMCP）的 `A suspended account cannot use MCP` 用的 `is suspended` 已實作，HMCP 的閘門放行後直接可用；MCP 的工具查版本時沿用 `seeVersion`／`listable`（`hidden`、`removed` 已經處理），`removed` 的版本回 HMCP-R5 的說明可以用 `versions.removal_source`／`removal_reason`。S7、S8（HSHR）用 fakehub，與這裡無關；Client 端若要顯示「我的分享」的移除原因，用 `GET /api/v1/me/shares` 的 `removal`。S9（Hub UI）的管理頁要用的 API 都在：`/api/v1/admin/{takedowns,reports,users,audit}`、決定與停用的 POST、`…/versions/:id/hide`；下架申請表單是 `POST /api/v1/takedowns`（不需要登入，`errors[].path` 指到欄位）。命令列的稽核紀錄 `actor` 是「notesteme-hub admin」。
  - **M5-S7／S8 HSHR Client 分享與重新生成（2026-11-24，分支 `feat/m5-hshr-client-hub-sharing`；決定記在 HSHR S9～S19，皆待 owner 追認）**：
    - 數字：預設 tag 308 passed（逐場景相同）；`@M5` 148 個：HSHR 的 42 個場景全部 pending → passed（共 133 passed），其他 15 個停在切片前的同一個步驟（HMCP 的 `Given a Hub is running`）；`@M3`（109 passed）、`@M4`（336 passed／35 pending）與 rebase 後的 master（origin/master 53eaa91，含 SRCH、SRC-10、BND、AUTH、GLOS）逐場景相同；`features/passing.txt` 沒有變動。
    - 產品：`internal/hubclient`（Client 對 Hub 的 HTTP 用戶端：逾時用注入的 clock、不跟隨轉址、回應大小上限、ADR-0032 第 4 點的錯誤對應、查詢與版本資料的驗證、內容的 sha256 與 cues 檢查）、`internal/hubshare`（組 bundle v0：allowlist 的來源紀錄、`hub_origin`、不含 `instance_id`，組好後用 `bundle.Read` 自我驗證）、`internal/hubsettings`（`GET`／`PUT /api/settings/hub`、ASR 前查詢的說明與確認；token 存在 secrets 的 `hub` 種類）；`pipeline/hub.go`（分享、重新生成、ASR 前查詢、`cookie_required`）；`transcript` 步驟在第一個付費來源之前查詢一次 Hub，Hub 的字幕在 `hub_imports` 等 `transcript` 取用；migration 00031（`hub_shares`、`hub_imports`）；API `settings-hub-*`、`items-hub-share`、`hub-import`、新增的 `POST /api/settings/hub/test`（S9）；`GET /api/items/:id` 的 `hub_shares`；CLI `notesteme hub share|import|status`。`m5_stubs.go` 刪除。
    - harness：fakehub 加 `OnHang`（不回應時推進 fake clock 到 5 秒與 30 秒的逾時，S16）、`ReplaceContent`、與真 Hub 相同形狀的分享回應；`StartFakeHub`、`EnsureClientHubURL`；4.32 的步驟與 4.31 兩個 seeding 句型的 fakehub 分支（`hub_client.go`）；`startServer` 在啟動前補 `hub.url`、啟動後設定 token（不算最近一次回應）。閘門 `hshrRules` 依 `@HSHR-R` 放行。`.feature` 只改了一處檔名（S17）；`processing.go` 的 Bilibili 原文語言推斷比照 pipeline（S17）。
    - 沒做：撤回的 Client 路由與 CLI（規格沒有，S12）；真 Hub 對 Client 的端到端場景（HSHR 只用 fakehub）；`@ui`、`@manual`；Hub 端稽核（HSHR 不涉及）。
    - 給 S9（Hub UI）與 HMCP 的提醒：Client 的 `/api/settings/hub` 回 `{url, token_set, precheck:{enabled, disclosure, disclosure_version}}`，UI 開啟查詢時要帶 `acknowledge: "hub-precheck-v2"`（安全修補 B 起；原本是 v1），沒帶時 422 的錯誤附 `disclosure`、`disclosure_version` 可以直接顯示。HMCP 的 15 個場景仍停在 `Given a Hub is running`（真 Hub 的閘門 `hubGate`），與 `hshrRules` 無關；`Hub` 錯誤對應已集中在 `hubclient.Error`，MCP 若要從 Client 呼叫 Hub 可以重用。
  - **M5-S9／UI-6 Hub UI 與 Client 的分享頁面（2026-11-24，分支 `feat/hub-web-ui-and-share-pages`；決定記在 [ui/README 10.16、10.17](ui/README.md#1017-ui-6-的決定與待決問題)，皆待 owner 追認）**：
    - 產品：獨立的 Vite 專案 `web/hub`（自己的 bundle、預算 300KB gzip、三語、每頁 axe），`go:embed` 由 `notesteme-hub` 提供（`internal/hub/ui.go`：`/api`、`/auth`、`/mcp`、`/healthz`、`/readyz` 以外交給 UI，CSP 比 Client 嚴、`no-cache`、SPA fallback）；頁面：登入、瀏覽與篩選、影片頁與兩版本比較、版本頁（下載、預覽、投票、檢舉、隱藏、衍生關係）、下架申請與狀態、帳號（登入方式、Hub token、我的分享）、審核（檢舉、下架申請）、使用者（群組、停用）、稽核。筆記的 Markdown 渲染搬到 `web/shared`。Client 加影音詳情的「分享到 Hub」對話框與已分享清單、設定「Hub」分區（token、測試連線、ASR 前查詢、用 Hub 版本重新生成），都是 lazy。CI：`web.yml` 的 `hub` job、goreleaser、Dockerfile、release-check（只改設定，沒在 CI 上跑過）。
    - 數字：Hub UI vitest 60 個測試、i18n 三語各 335 個 key、初始 JS 163.0KB gzip；Client vitest 424 個測試、i18n 三語各 1591 個 key、初始 JS 274.0KB gzip（之前 271.2KB）；預設 tag 308 passed、`@M5` 133 passed／15 pending（HMCP），與切片前相同。API 與兩份 OpenAPI 快照不變。
    - 沒做：`@ui @manual` 的真實瀏覽器驗收（owner）；OAuth callback 錯誤轉回 UI（會改變 HACC 場景的狀態碼，待決）；Client 顯示 Hub 上傳者名稱（API 沒有 `hub_origin.uploader`）；「我投的票」（API 沒有 `my_vote`）；取消隱藏、群組清單（API 沒有）；Client 端撤回（HSHR S12）。
  - **M5-S6 HMCP Hub MCP（2026-11-24，分支 `feat/hub-mcp-endpoint`；決定記在 HMCP M4～M9，皆待 owner 追認）**：
    - 數字（基準 rebase 後的 origin/master 16ad886，HSHR 已合併）：預設 tag 308 passed、`@M3` 109 passed、`@M4` 354 passed／17 pending（LIB-08／09），三組逐場景相同；`@M5` 148 個：133／15 → **148 passed**，改變的只有 HMCP 的 15 個場景（pending → passed）。`features/passing.txt` 與 `.feature` 沒有變動。
    - 產品：`internal/hub/hubmcp`（四個唯讀 tool：`search_versions`、`get_version`、`list_video_versions`、`get_transcript`；權限 middleware；`get_transcript` 的範圍、渲染與截斷；被限流時的 JSON-RPC 回應 `RateLimitedReply`）；`hubapi/mcp.go`（`/mcp`：只接受 PAT → Origin／Host 依 `public_url` → 與 API 共用的帳號限流 → go-sdk stateless JSON；tool 以 loopback 呼叫 `/api/v1` 的 GET，`accessMiddleware` 看到 loopback 的 principal 時只檢查權限而且只允許 GET）；`hubconfig` 驗證並正規化 `mcp.allowed_origins`／`allowed_hosts`（`NormalizeOrigin`）。`RawStubs` 已空。Hub 的 OpenAPI 快照沒有變動（`/mcp` 不是 Huma 路由），Client 的路由沒有變動。
    - harness：`hmcpRules`（R1～R8）加進 `hubGate`（`hub_gate_test.go` 同步）；`features/steps/hub_mcp.go` 實作 `from the origin` 與兩個 Hub MCP 句型（連線以 token 快取，Hub 重新啟動後重連；`calls` 的連線失敗記成 tool 呼叫的錯誤，見 steps.md 4.30 的補充）。沒有新句型。
    - 測試：`hubmcp` 涵蓋率 93%、`hubapi` 88%、`hubconfig` 93%；對抗輸入（越權、hidden／private／removed、路徑 `../`、超大與畸形 JSON-RPC、並發、限流）與 fuzz（`parseTranscriptArgs`、`buildTranscript`）。變異測試（拿掉 hubmcp 的權限檢查、loopback 的權限與 GET 限制、Origin／Host 檢查、限流共用、截斷、`to` 篩選、路徑 escape）都有測試變紅。
    - 沒做：讀取的稽核紀錄、MCP 專用額度、真實 MCP client（Claude Code 等）的連線驗收（owner）、Postgres（Hub 仍是佔位）。
    - 給 S9（Hub UI）的提醒：帳號頁建立 PAT 後顯示 Agent 設定範例 `{"type": "http", "url": "<public_url>/mcp", "headers": {"Authorization": "Bearer <PAT>"}}`（HMCP「介面」）；`get_transcript` 需要 `download`，建立 token 的表單可以提示只勾 `read` 時只能搜尋與看來源紀錄。跨網域的網頁 Agent 要在 `mcp.allowed_origins` 加來源，反向代理用了別的主機名稱時加 `mcp.allowed_hosts`（Hub 不放行 IP 與 localhost）。
  - **Hub 正式環境基礎建設：Postgres、S3、部署（2026-11-24，分支 `feat/hub-postgres-s3-deploy`；決定記在 ADR-0030 的修訂行，待 owner 追認）**：
    - 數字（rebase 後的 origin/master 2a531ee 上實測）：預設 tag 308 passed、`@M3`＋`@M4`＋`@M5` 合跑 628 passed（驗收場景仍跑 SQLite＋fs）；`.feature`、`features/passing.txt`、Hub 與 Client 的 OpenAPI 快照都沒有變動。`go test -race -p 2 ./...` 一次全綠，執行時帶著 Postgres 與 MinIO 的測試環境變數（並行案例與列鎖也在 race detector 下跑過）；`coverage.sh` 通過。
    - 產品：`internal/hub/hubstore/postgres`（pgx 連線池、sqlc 的 postgresql engine、migration 00001～00005 與 SQLite 一對一、goose advisory lock、`Options{NoMigrate}` 給 doctor、`CheckWritable` 拒絕唯讀的 replica）；`internal/hub/hubblob/s3.go`（aws-sdk-go-v2 的 s3、靜態憑證、Content-MD5、下載時檢查長度與 sha256、`Ping`）；`hubconfig` 的 `store.dsn`（不能含密碼）與 `blobs.s3.*`／`NOTESTEME_HUB_S3_*`（secret 只來自環境變數，`S3Config.String` 遮掉 secret）；`hub.CheckBlobs` 與 `doctor` 檢查 Postgres 的 schema 版本與 S3 bucket；`deploy/docker/compose.hub.yaml`（`hub` profile：Hub、Postgres 16、MinIO、建 bucket、migrate）、`.env.hub.example`、`hub-config.example.yaml`、[docs/hub-deployment.md](hub-deployment.md)（部署、升級、備份還原）；`.dockerignore` 補上 `web/hub`（之前 docker build 會找不到 `web/hub`）；CI 的 `hub-backends` job 與 Postgres 的 `sqlc diff`／`vet`。
    - 語意對齊（SQLite 的寫入是單一連線）：讀了再寫的交易先鎖版本或帳號，40001／40P01／23505 整筆重試；時間截到毫秒、`COLLATE "C"`、`seq` 取代 `rowid`、`provenance_json` 用 TEXT。`storetest` 加了 7 個並行案例與 2 個精度／排序案例（SQLite 也跑）。
    - 驗證環境：**本機的 docker daemon 沒有回應**（`docker ps` 卡住），改用 zonky 的 Postgres 16.14 binary（`initdb --locale=en_US.UTF-8`，非 C locale，才驗得到 collation）與從原始碼建置的 MinIO（RELEASE.2025-04-22，官方 binary 已停止發布）。**已在真實 Postgres 上通過**：`hubstore/storetest` 全部共用案例（含並行案例，`-count=3`）、migration 00004→00005→Down 的資料保留、NoMigrate／schema 太新、唯讀連線、SQL 注入、錯誤密碼不洩漏、`hubcli` 的 migrate／doctor／admin、讀回的時間是 UTC。**已在真實 MinIO 上通過**：hubblob 共用案例、錯誤 secret 不洩漏、doctor 的 bucket 檢查。`compose.hub.yaml` 以 `docker compose config`（不需要 daemon）與 fakecompose 驗證。**沒有驗證**：實際 `docker build --target notesteme-hub` 與 `docker compose up`（沒有 daemon；`hub-migrate` 與 `hub` 都有同一個 build，避免 compose 去 registry 拉 `notesteme-hub:local`）、Cloudflare R2 與 AWS S3、GitHub Actions 的 `hub-backends` job（帳務問題無法執行；本機以 actionlint 檢查過語法）。
    - 涵蓋率（真實 Postgres／MinIO 環境）：`hubstore/postgres` 84%、`hubblob` 91%（只用假 S3 服務也是 91%）、`hubconfig` 94%、`hubcli` 84%。變異測試（拿掉投票、移除身分、檢舉、撤回的列鎖，拿掉 23505 重試，拿掉毫秒截斷、UTC 轉換、`COLLATE "C"`，S3 的 Open 提早取消、sha256 檢查、want 檢查、大小上限、secret 遮蔽、已存在不重寫）都有測試變紅；lookup 範圍上界改成 `"f"` 沒被抓到（SQLite 同樣的缺口）。沒有設定測試環境變數時 Postgres 的測試會略過（`coverage.sh` 沒有加入這兩個套件）。
    - 給 owner 的部署步驟：見 [docs/hub-deployment.md](hub-deployment.md) 第 2 節（`cp .env.hub.example .env.hub` 填入五個密鑰 → `hub-config/notesteme-hub.yaml` 填 `public_url` 與 OAuth → `docker compose -f compose.hub.yaml --env-file .env.hub --profile hub up -d --build` → `run --rm hub doctor`）。第一次部署請實際跑一次 `docker build` 與 `up`，並確認反向代理與 OAuth callback。
    - 需要 owner 追認：ADR-0030 的修訂行（compose 改放獨立檔案、Postgres／S3 不經 netguard、`store.dsn` 不能含密碼、S3 secret 只來自環境變數、Content-MD5 取代 SDK 預設 checksum、MinIO image 固定在最後一個社群版本）；Hub 的 image 沒有發布到 ghcr（compose 預設 `notesteme-hub:local` 從原始碼建置，`docker-publish.yml` 沒有改）。
    - 已知限制：Postgres 的 text 不接受 NUL 字元（SQLite 接受；來自 OAuth 的 display name 含 `\u0000` 時 Postgres 會回錯誤）；sqlc 產生的 `Seq` 是 `*int64`（identity 欄位，只用來排序）；S3 的 Put 會把不能 seek 的 reader 讀進記憶體（上限同 `bundles.max_file_bytes`，目前呼叫端都是 `bytes.Reader`）；多個 Hub 程序共用 Postgres 時，限流仍是每個程序各自計算（S5 已記錄）。
  - **安全修補 A：Hub（2026-11-24，分支 `fix/hub-security-hardening`；決定記在 HREP R8「去重與可見度」「影片的 metadata」、HACC A14、HMCP M7 的補充與 ADR-0031 的修訂行，皆待 owner 追認）**：
    - 修補（一個發現一個 commit，各附會先失敗再通過的測試）：去重只併入上傳者以一般成員身分看得到、可見度相同的版本，`withdrawn` 以新分享的值恢復，其他情況另建版本、只共用 blob（migration 00006：SQLite 重建 `versions`、Postgres 移除唯一鍵並在分享時鎖影片的列）；`admins` 以外的群組不能帶 `admin`（設定載入失敗），API 依群組權限回 403 `admin_by_config`；API 不能停用持有 `admin` 的帳號；帶了 `Authorization` 而無效時一律 401；`get_transcript` 在同一個開始時間內也截斷（新參數 `from_index`／結果 `next_from_index`），分享時每句 ≤ 8 KiB、≤ 100,000 句、不能有控制字元（含 `\r`）；cookie 改為 `__Host-` 前綴（OAuth cookie 的 Path 改 `/`）；`external_id` 依平台檢查、標題與頻道名稱去掉控制與雙向字元；`server.trusted_proxies`（含 doctor 警告、compose 與部署文件）；IPv6 限流以 /64、停用的帳號也受限流；HTTP server 的 `ReadTimeout`／`IdleTimeout`／`MaxHeaderBytes`；Hub token 建立與撤銷寫稽核紀錄（`token.create`、`token.revoke`），指派群組與稽核紀錄的 handler 自己也檢查 `admin`；session 90 天絕對期限，移除登入方式時刪除其他 session、撤銷所有 Hub token；MCP 結果加 `"untrusted": true`；Google `email_domain` 要求 `hd`，GitHub `org_member` 可用數字 id。
    - 數字（rebase 後的 origin/master 21564dc 上實測）：預設 tag 308 passed、`@M3,@M4,@M5` 628 passed、`@M1`～`@M5` 合跑 936 passed；`.feature` 與 `features/passing.txt` 沒有變動；`web/hub` typecheck、60 個測試、i18n、`check:api` 通過（OpenAPI 快照不變）。
    - Postgres：以 embedded Postgres（zonky binary，`fergusstrange/embedded-postgres`；docker daemon 仍沒有回應）實測 `hubstore/postgres` 全部測試（含新的去重、並行分享、PAT 稽核、移除登入方式的 storetest 案例，以及 migration 00006 的 Up／Down）與 `hubcli` 的 Postgres 測試；拿掉影片列鎖的變異會讓新的並行案例失敗。S3 這次沒有改動、沒有重跑。
    - 升級影響：既有 session 要重新登入一次（cookie 改名）；設定檔中 `admins` 以外的群組帶 `admin` 時無法啟動；Google `email_domain` 規則對個人帳號不再成立；移除登入方式會撤銷該帳號的 Hub token。
    - 補充：withdrawn 版本恢復時依新的來源紀錄重建它自己的衍生關係，分數、票與未處理的檢舉沿用；撤回者的 `GET /api/v1/me/shares` 看不到恢復後的版本時只顯示識別欄位與 `state: withdrawn`。`ReadTimeout` 保留 5 分鐘：在 Go 1.26.8 實測回應寫得比它久的 GET／POST（持續 flush）不會被取消（`TestHTTPServerTimeouts` 鎖住）。embedded Postgres 用的是預設 locale（不是先前驗 collation 的 `en_US.UTF-8`）。`hubapi.TestMCPLoopbackRechecksPermissions` 的「取消的請求應該失敗」在 origin/master 上同樣會偶發失敗（30 次中 5 次），不是這次造成的。
    - 未處理：分享時 `parents_sha256` 指到別人看不到的版本仍會沿用它的模型欄位（`buildShare` 的 `known` 沒有依可見度過濾，洩漏模型名稱）；`/mcp` 的帳號停用路徑沒有另外計入限流；`hidden` 版本被另一個人以相同內容重新分享會出現新的 `visible` 版本（取捨記在 HREP R8）；`org_member` 的名稱比對仍保留；影片 metadata 仍由第一位分享者決定（規格沒有更正機制）。
  - 建議的實作切法：M5-S0 骨架（Client 的設定 schema 接受 `hub.*`、Client 的 M5 路由回 501 stub；`cmd/notesteme-hub` 與 `internal/hub` 的空殼、Hub 的 SQLite migration 00001、OpenAPI 快照；harness 接上真 Hub、fakeoauth、fakehub 與 `{version:…}` 等代入；之後每個切片以 `@M5` junit 逐場景比對）→ S1 HACC 登入（OAuth、state、PKCE、session、帳號連結）→ S2 HACC 權限（群組、規則、PAT、可見度、限流）→ S3 HREP 分享與驗證（BND 對齊、白名單、去重、衍生關係、撤回）→ S4 HREP 篩選、詳情、下載、lookup → S5 HREP 投票、檢舉、自動隱藏＋HACC 下架與停用、稽核 → S6 HMCP → S7 HSHR 分享（含 `cookie_required`）→ S8 HSHR 重新生成與 ASR 前查詢 → S9 Hub UI（`web/hub`）與 Client 的分享 UI。S1～S6 與 S7、S8 可以平行（fakehub 與真 Hub 切開）；最後做 HSHR 的 `@manual` 端到端驗收。
- **2026-11-23 收尾摘要（owner 的 /goal：完成 SPEC 剩餘部分、每個切片直接推送 master、不開 PR）**：
  - **完成**：M1 與 M2 的後端與驗收場景。預設 tag（`@M1` 與 `@M2`）**308 個場景全部 passed**（191 個 M1、117 個 M2），`features/passing.txt` 308 行，CI 的 `go test -race` 逾時已調到 30 分鐘（場景在 `-race` 下約 10 分鐘）。M2 切片：S1 PRF、S2 PRM、S6a TRN OpenCC、S7 OUT 渲染與匯入、S3 LLM 核心、S8 OUT sidecar／folder、S4 COR、S5 SEG、S6b TRN LLM、PRM playground／compare＋DEL-R9、S9 MCP（stdio＋HTTP）、S10 AGT、S11（SEC config export／import、術語候選核可 API、translate 詞彙表、P7 prompt 刪除原子化、rerun `chunks` 檢查）。各切片的決策與限制在下面各 bullet 與規格的待決問題（標「待 owner 追認」的都是依 /goal 由 advisor 建議採用的保守預設）。
  - **做不到、需要 owner（blocked）**：
    1. **Web UI**：UI-0 骨架、UI-1 媒體庫與新增抽屜完成（2026-11-23，見下方 UI-0、UI-1 bullet），UI-3a（Prompt 庫、處理方案、字幕樣式）完成（見下方 UI-3a bullet），UI-2、UI-3b 待做（影音詳情與佇列；設定）。前端技術已由 ADR-0008 決定，ADR 沒有規定的細節記在 [ui/README 第 10 節](ui/README.md#10-實作現況)，待 owner 追認。
    1. **Web UI**：UI-0 骨架、UI-1 媒體庫與新增抽屜、UI-2 影音詳情與佇列完成（2026-11-23，見下方 UI-0、UI-1、UI-2 bullet），UI-3 待做（設定、Prompt 庫、處理方案、字幕樣式）。前端技術已由 ADR-0008 決定，ADR 沒有規定的細節記在 [ui/README 第 10 節](ui/README.md#10-實作現況)，待 owner 追認。
    2. **真實樣本驗收**：10 部真實影音要用真實的 yt-dlp、Groq、Supadata、Bilibili；`@manual` 與 `@ui` 場景（LIB 5 個、CLI 2 個、DEL 1 個）要人工驗收。
    3. **M0 量測**與 6 個 `@pending-m0` 場景（COR 相似度算法、SEG 斷點標記與字幕規則、SRC 不可用結果與 Whisper 幻覺、TRN 切分標記）；SRC-R11 mixed（`@wip`）。
    4. **真實 libass 預覽**（OUT-R9）與 Jellyfin／Plex 的 sidecar 語言碼實測。
    5. **規格狀態**：沒有任何規格改成 `implemented`——Definition of Done（specs/README 6.2）要求 UI 與 `@manual` 人工驗收，那是 owner 的步驟。
    6. **M3～M5**：只有條列式驗收標準，沒有展開成規則與場景（specs/README 第 6 節要求開發前先展開），未開始。
  - **要 owner 優先追認的決策**：TRN S6b-Q6（`length_ratio` 下限 0.3 對英翻中太緊，可用 `stages.translate.length_ratio` 覆寫）；AGT A1（並行上限解讀成每個 Agent 各自不超過 `llm` 上限）與 A6（translate 用 agent 直接失敗）；香港、澳門繁體的 localize 沒有內建 prompt；DEL-R9 永久刪除不處理輸出檔；內建 `default` 處理方案不匯出／不可被 config import 覆蓋（與 API 可 PATCH 它的行為不一致）；P7 剩下的寫入端窗口（寫入端在自己的交易外驗證 prompt）；MCP S1～S10（stateless HTTP、本機 bearer、`allowed_hosts`）；`estimate_usd` 一律 `null`（`internal/cost` 沒有 LLM 價格）。
  - **已知技術債**見第 6 節；`POST /api/tasks/{id}/retry`／`cancel` 仍回 501、`GET /api/queue` 不列 Agent 任務、試跑 segment／translate 回 501、`prepareLLM` 與試跑有約 25 行重複。

- **UI-0（Web UI 骨架，2026-11-23）**：`web/client`（React + Vite + TypeScript，npm、Node 24.12.0）用 `go:embed all:dist` 嵌入；`dist/` 只 commit `.gitkeep`，沒有 Node 時 Go 的建置與測試照常通過，UI 回 placeholder。`internal/webui` 提供 SPA fallback（`/assets/*` 與有副檔名的路徑找不到回 404）、長期快取、只接受 GET／HEAD、路徑正規化與 NFR-SEC-1 的 CSP 等標頭；`internal/api/ui.go` 在 authn 之前分派，`/api`、`/mcp`、`/healthz`、`/readyz` 不受影響，本機 socket 不提供 UI。OpenAPI 快照 `internal/api/openapi.json`（`go run ./internal/api/cmd/openapi -o internal/api/openapi.json`，`TestOpenAPISnapshot` 檢查）產生 `src/api/schema.d.ts`。前端：程式碼式路由、API client（CSRF、`Accept-Language`、`ApiError`、401 導回登入）、登入頁（鎖定倒數、導回原路徑）、App shell（M1 導覽、skip link、主題、介面語言）、SSE 失效表與重連、i18n 三語、設計 token 與 7 個基礎元件、axe 測試；頁面都是空頁。CI：`.github/workflows/web.yml`（`paths` 過濾）、`release-check` 確認 binary 內嵌建置產物；Dockerfile 加 node 階段、goreleaser 的 `before.hooks` 先建前端。慣例見 `web/client/README.md`，決定與待決問題見 `docs/ui/README.md` 第 10 節。
- **UI-1（媒體庫與「新增」抽屜，2026-11-23）**：`/library` 的 M1 基本版：列（縮圖、標題、平台、頻道、發布日、長度、精簡版時間帶、狀態欄，失敗或停住的列附錯誤類別、訊息與「從該步驟重新處理」等動作）；篩選全部存在 URL（平台、頻道、各步驟狀態〔同步驟 OR、跨步驟 AND〕、錯誤類別、語言、發布日、長度、時間來源、標題搜尋 debounce），可個別移除或清除全部，空結果提供「清除篩選條件」；keyset cursor 無限捲動與總數；`@tanstack/react-virtual` 虛擬化（10,000 列只渲染約 20 列）；`item.created` 只累計「有 N 個新項目」，`stage.status` 只更新那一列；鍵盤（roving focus、Enter、`/`、Esc）。「新增」抽屜（導覽按鈕、`N`、焦點不在輸入框時貼上網址）：resolve 預覽（影片、播放清單、頻道、Podcast 各集勾選、Bilibili 分P）、本機檔案 multipart 上傳、處理方案、依 `code` 的錯誤與動作（`in_trash` 還原、已在媒體庫中「打開」）、toast「已開始處理 N 部」。StatusBand 放在 `web/shared`（`@shared`）。**驗證**：vitest 146 個測試（含 axe、篩選↔URL、cursor、SSE、抽屜流程、虛擬化）、i18n 216 個 key、初始 JS 174.2KB gzip（預算 300KB）、`go test ./features` 308 passed。**決定**（不改後端；每列另外讀 detail；即時更新不重抓列表；預設不分組；播放清單整份新增）見 [ui/README 10.5](ui/README.md#105-ui-1-的決定) 與 LIB 規格待決問題，待 owner 追認。**已知限制**：列表 API 沒有 `stages`、頻道名稱、縮圖網址（N+1、只有 YouTube 縮圖）；沒有分組、預估花費、單次覆寫、多網址、拖放檔案、指令面板、圖示。**給 UI-2／3**：完整版時間帶在 `web/shared/src/StatusBand.tsx` 加 `variant="full"` 與新圖層 props；列表頁沿用 `VirtualList`、`LoadMore`、`EmptyState` 與 `filters.ts` 的模式（佇列頁的篩選同樣放 URL）；`src/test/mockApi.ts` 可直接用；影音詳情的 query key 用 `queryKeys.items.detail(id)`，媒體庫的列與詳情頁會共用快取。
- **UI-7（總覽、媒體庫的批次操作、已讀狀態、垃圾桶、快捷鍵，2026-11-24，分支 `feat/ui-7-overview-bulk-actions`）**：`/` 改成總覽（lazy；現在、需要你處理〔成本的「調整預算」「查看成本」與影音詳情的動作對照〕、提醒、今天、近期完成、本月花費、磁碟用量；帶瀏覽器時區，422 時不帶 tz 重讀；SSE 節流重讀），導覽第一項「總覽」。媒體庫加多選（勾選框、Shift 範圍、已載入、符合條件的全部 ≤500、換篩選清除）與 lazy 的批次操作列：重新處理、改用處理方案、匯出 bundle、刪除先 `dry_run`（預估、`unknown_estimates`、預算 `exceeds` 只警告、刪除合計與輸出檔選項、不能處理的影音），分批執行（刪除 10、其他 50）、進度、停止、逐項結果；標為已讀／未讀直接執行加復原；等你決定的檢視可逐列勾選後「繼續處理」（預估借用 `rerun from=segment` 的 dry_run）。已讀狀態的標示、篩選與影音詳情的切換；`/trash` 垃圾桶（還原、永久刪除、清空）；`G`+`L/Q/S`、`?`、影音詳情 `1`–`4`。**沒有改後端**（OpenAPI 不變）。**沒做**：後端補洞（`blocked=`、匯入 409 `item_id`、`imported_from`、`TermCandidateReview.item` nullable）交付時未合併，相關 UI 維持 UI-5 的行為；指令面板、影音詳情的播放快捷鍵、分組與「格」檢視。**待決（API）**：總覽 `today` 的事件沒有 stage／status 與批次的數量、事件只有代號沒有 ID；`/api/items/continue` 沒有 `dry_run`。**驗證**：vitest 46 個檔案 444 個測試（新頁面與對話框都有 axe）、i18n 三語各 1695 個 key、初始 JS 281.5KB gzip（UI-6 後 274.0KB，預算 300KB 未調整）、`check:api` 無差異、`go build`／`go vet`、`internal/webui`、`internal/api` 測試通過、`go test ./features` 308 passed。決定見 [ui/README 10.18、10.19](ui/README.md#1019-ui-7-的決定與待決問題)，待 owner 追認。**人工驗收**：`@ui @manual`（LIB-08、LIB-09、DIG 的從媒體庫挑選）。
- **UI-5（搜尋、術語審核、bundle、系統告警與備份、摘要與彙整、更好的字幕來源，2026-11-24）**：`/search?q=&lang=`（lazy；`highlights` 以 UTF-16 位移切成 `<mark>`、不渲染 HTML，依影音分組，游標分頁，點結果跳到那一句），入口在媒體庫搜尋欄下方。設定的「詞彙表」加跨頻道術語審核（GLOS-R7：改寫後接受、加到頻道或全域、多選批次，錯誤 `decisions.<n>` 對回列，`glossary_full`／`term_candidate_decided` 分開處理）；「系統」加告警（種類、持續時間）、每日備份狀態與「匯入 bundle」（`add_versions`、413／422／409）；「通知」加彙整設定（只列勾了 digest 的端點、預覽）。影音詳情加「匯出 bundle」、摘要與「繼續處理」、「有更好的字幕來源」與「改用更好的字幕重跑」（`estimate_usd` 為 null 時寫無法預估）；步驟動作對照表補上 DIG、SRC-10、SPB 的六個字串。媒體庫接受彙整連結的 `?blocked=awaiting_user`，並可批次繼續已載入的影音。移除通知事件的「尚未會發出」。**沒有改後端**（OpenAPI 不變）；AUTH 的 token 輪替與工作階段 UI 已由 AUTH 切片完成，沒有重做。**沒做**：總覽（LIB-09 `/api/overview` 仍是 501）、批次操作（LIB-08 `/api/items/batch` 仍是 501）、Hub UI（M5），都沒有放入口；匯出時選產出種類（API 沒有參數）。**待決（API 缺口）**：列表沒有 `block_reason` 篩選（「等你決定」用 `summary:done|skipped` + `segment:blocked` 近似）、bundle 匯入的 409 沒有 `item_id`、`GET /api/items/{id}` 沒有 `imported_from`、`TermCandidateReview.item` 在 OpenAPI 不是 nullable（GLOS-R5 說可以是 null）。**驗證**：vitest 414 個測試（新頁面與區塊都有 axe 測試）、i18n 三語各 1537 個 key、初始 JS 271.2KB gzip（預算 300KB）、`go test ./features` 308 passed。決定見 [ui/README 10.14、10.15](ui/README.md#1015-ui-5-的決定與待決問題)，待 owner 追認。**人工驗收**：`@ui @manual`（SRCH、GLOS、BND、OPS、DIG、SRC-10）。
- **後端補洞切片（2026-11-24，分支 `feat/backend-gap-fixes`）**：補 UI-5 列出的 API 缺口與 AUTH-R3。（1）`--insecure-no-auth`：`local` 部署在 listen 只綁 loopback、沒有 `trusted_proxies`／`auth.proxy`／`auth.oidc`、沒有任何密碼時可以啟動，否則以新訊息拒絕（`vps` 的 OPS-R21 不變，`TestServeInsecureNoAuth` 的「尚未實作」斷言改成新的啟動條件）；每個請求等同 owner（身分種類 `insecure`），只接受 loopback 的 `Host`、拒絕跨站寫入（`Origin`、`Sec-Fetch-Site`），log 每小時警告，`GET /api/auth/methods` 多 `insecure_no_auth`，UI 頂部顯示警告橫幅、沒有 CSRF cookie 時不導到登入頁；login 與修改密碼回 409 `no_password`（AUTH A23～A26）。（2）`GET /api/items?blocked=<原因>`（最新一次執行是 blocked 且原因相符；只驗格式；游標指紋包含它；LIB 待決問題），UI 的「等你決定」仍用近似條件。（3）bundle 匯入的 409 `bundle_item_exists` 帶 `item_id`，`bundle_item_in_trash` 不帶（BND N23）。（4）`GET /api/items/{id}` 的 `imported_from`（必有、可為 null，取所有版本中最近一次 bundle 匯入，manifest 字串去控制字元；BND N24），UI 顯示留給 UI 切片。（5）OpenAPI 的 `TermCandidateReview.item` 改成 nullable（GLOS-R5）。新增 sqlc 查詢 `LatestBundleImport` 與 `ListItemsPage`／`CountItemsFiltered` 的 `blocked` 條件，沒有 migration。初始 JS 282.1KB gzip（rebase 到 UI-7 之後；比 master 的 281.5KB 多 0.6KB，三語警告字串與 `auth/insecure.ts`）。決定都標「待 owner 追認」。
- **UI-8（後端補洞的欄位、指令面板、播放快捷鍵、底部分頁列、「格」檢視，2026-11-24，分支 `feat/ui-8-blocked-filter-bundle-import`）**：媒體庫的 `?blocked=<原因>` 直接送 API（取代 UI-5 的近似條件），「等你決定」改成逐列勾選（勾選後可選取符合條件的全部 ≤500）再由批次操作列繼續，移除「繼續處理已載入的 N 部」；bundle 匯入 409 `bundle_item_exists` 連到既有影音、`bundle_item_in_trash` 連到 `/trash`；影音詳情顯示「從 bundle 匯入」（實例、版本、時間，純文字）；`--insecure-no-auth` 的橫幅已確認一致。指令面板（`Ctrl K`，macOS 為 `⌘ K`，lazy：影音標題、字幕搜尋入口、頁面、新增／暫停或繼續佇列／更新 yt-dlp）；影音詳情 `Space`、`←`／`→`、`[`／`]`（Bilibili 不接 `Space` 與方向鍵）；767px 以下的底部分頁列（總覽、媒體庫、新增、佇列、更多）；媒體庫「列｜格」與 lazy 的格檢視（`view=grid`，沒有多選）。**沒有改後端**。**沒做**：依頻道分組（API 沒有排序／分組參數與頻道名稱，待決）、儲存檢視（不在 M4）、總覽 `today` 的缺口（UI-7 的待決仍有效）。**驗證**：vitest 51 個檔案 466 個測試、i18n 三語各 1714 個 key、初始 JS 283.3KB gzip（預算 300KB 未調整）。決定見 [ui/README 10.20、10.21](ui/README.md#1021-ui-8-的決定與待決問題)，待 owner 追認。**人工驗收**：`@ui @manual`（BND 的匯入顯示、DIG 的從媒體庫挑選）。
- **UI-4（訂閱、成本、筆記模板、通知與預算、影音詳情的畫面與筆記，2026-11-23）**：`/subscriptions`（列表、新增抽屜、OPML／Takeout 匯入）、`/subscriptions/$id`（PATCH 只送改過的欄位、立即檢查、停用與刪除先確認、探索到的條目）；試跑預覽會連外並消耗 rate limit，所以要先按一次「試跑預覽」，之後 800ms debounce 並取消舊請求，429 顯示 `retry_after_s`。`/costs`（本月花費／預算／預計月底用 2 位小數、`notices` 依 code 對應動作、分組與日期在 URL、表格式長條圖、cursor 分頁的明細〔未知單價不顯示 $0.00〕、CSV 匯出〔超過 366 天停用〕、計價表的覆寫〔先確認舊價 → 新價〕與確認）。`/note-templates`、`/note-templates/$id`（三欄：模板、連到筆記 prompt、用已儲存版本預覽；內建唯讀只能複製；全部取消勾選送 `checks: []`）。設定的「通知」（四種 notifier、密鑰只寫不讀且送出即清空、測試通知、停用與刪除先確認、最近的 outbox）與「預算」（只送四個欄位）。影音詳情加「畫面」（印樣、跳播）與「筆記」（自寫的安全 Markdown renderer、時間連結跳播、畫面嵌入、檢查、vault 寫入狀態、過時提示）分頁，只在切到該分頁時讀取。**沒有改後端**（OpenAPI 不變）。**沒做**：總覽（依賴 LIB-09 `/api/overview`）、批次操作（LIB-08）、搜尋（SRCH）、Hub UI——後端都還沒有，沒有放會 501 的入口；訂閱的「新內容」欄（API 沒有這個數字）。**驗證**：vitest 375 個測試（新頁面與分區都有 axe 測試）、i18n 三語各 1384 個 key、初始 JS 260.4KB gzip（預算 300KB）、`go test ./features` 308 passed。**決定與待決**（scope 不偵測、明細金額 4 位小數、預覽要先按一次、詳情是頁面不是抽屜、畫面嵌入在 frames 重跑後可能對不上）見 [ui/README 10.12、10.13](ui/README.md#1013-ui-4-的決定)，待 owner 追認。**人工驗收**：真實瀏覽器的視覺、鍵盤與螢幕閱讀器（`@ui @manual`）。
- **UI-3b（設定、詞彙表與術語候選、設定匯出與匯入，2026-11-23）**：`/settings/:section` 的 12 個分區（服務與金鑰、帳號與 Cookie、語言、輸出、MCP 與 Agent、通知〔M4〕、預算〔M4〕、詞彙表、系統、匯出與匯入、外觀、登入），未知分區導到第一個分區、窄螢幕改成頂部下拉選單。API key 只顯示末四碼、token 只顯示一次、cookie 上傳、全域與頻道詞彙表、術語候選的接受與拒絕（409 顯示已被處理）、yt-dlp 更新與回退、設定匯出與匯入（422 逐項錯誤）、修改密碼。**唯一的後端改動**：新增 `POST /api/auth/password`（只有 owner、與登入共用鎖定、成功後撤銷其他 session，AUTH 待決問題）。後端沒有的功能（測試連線、cookie 狀態、工具版本、磁碟用量、匯入預覽、頻道列表）UI 明確標示尚未提供，沒有補後端。主題與介面語言的快速切換保留在導覽底部，和設定分區共用同一個值。**驗證**：vitest 190 個測試（含每個分區的 axe）、i18n 468 個 key、初始 JS 193.9KB gzip。範圍與 13 項決定見 [ui/README 10.10、10.11](ui/README.md#1010-ui-3b設定詞彙表與術語候選設定匯出與匯入2026-11-23)，待 owner 追認。
- **UI-3a（Prompt 庫、處理方案、字幕樣式，2026-11-23）**：`/prompts`（依用途分組、使用中的處理方案）、`/prompts/$id`（lazy，CodeMirror 6 的變數高亮與 lint、版本列、另存新版本、設為預設、試跑的逐字差異與驗證、比較版本、刪除的 409 `used_by`）、`/profiles`、`/profiles/$id`（流程列、5 個編號步驟、merge patch、前端補的樂觀鎖、422 欄位錯誤）、`/styles`、`/styles/$id`（三個分頁、debounce 300ms 的 libass 預覽與失敗狀態、複製、匯入 .ass）。唯一的後端改動是 CSP 的 `img-src` 加 `blob:`。**驗證**：vitest 186 個測試、i18n 511 個 key、初始 JS 198KB gzip。**決定與未做**見 [ui/README 10.8、10.9](ui/README.md#108-ui-3aprompt-庫處理方案字幕樣式2026-11-23)，待 owner 追認；訂閱影響提示等 M4。
- **UI-2（影音詳情與佇列，2026-11-23）**：`/items/:id`（lazy route）：標題列與主要動作（重新處理、下載 SRT、停止處理、移到垃圾桶）；完整版時間帶（`StatusBand variant="full"`，`role="slider"`，方向鍵、點擊、hover，逐列文字標籤）；播放器（YouTube 嵌入＋postMessage，無回應時改成重新載入；Bilibili 用 `t` 重新載入；本機與 Podcast 用 `/media`）；字幕句列表（虛擬化、點時間碼跳播、目前句高亮、跟隨播放、搜尋、時間來源標示、純文字＋字幕 `lang`）；模式：原始、正規化、校正、差異（依時間重疊分組後逐字 LCS，`<del>`／`<ins>`）、譯文、對照、已編輯，切換時捲動位置不變；步驟面板（執行方式、provider／model／prompt、錯誤類別＋訊息＋動作，`actions` 字串對照表）；紀錄分頁（`/runs`）；重新處理對話框（起訖步驟、處理方案、provider／model／prompt 版本覆寫、chunks，422 逐筆顯示）；改用 API、上傳 cookie、更新 yt-dlp、停止處理；刪除確認（`deletion-preview`、`delete_outputs`、改過的檔案、toast 復原）；輸出（下載、寫出的檔案、匯入字幕與 422 的句數）。`/queue`：通道、上限 +／−、整個佇列與各類別的暫停／繼續、暫停橫幅、任務列表（篩選在 URL）、「需要你處理」依步驟計數、影音層級的停止處理。**驗證**：vitest 218 個測試（含 axe 13 個路由）、i18n 452 個 key、初始 JS 188.5KB gzip（詳情 lazy chunk 13.8KB）、`go test ./features` 308 passed。**決定與 API 缺口**（不改後端；`actions` 是繁中字串；沒有 log 片段、rerun 預估、provider 列表、變體列表、章節、chunk 範圍、Agent 任務、優先順序；單一任務 retry／cancel 是 501）見 [ui/README 10.7](ui/README.md#107-ui-2-的決定)，待 owner 追認。**人工驗收**：YouTube postMessage 跳播與 LIB-03、LIB-06 的 `@manual` 場景要在真實瀏覽器確認。**給 UI-3**：設定分區沿用 `pages/item/actions.ts` 連過去的 `services`、`accounts`、`system`；表單沿用 `RerunDialog` 的 422 `errors[]` 顯示方式；`api.patch` 已有。
- **Repo**：`github.com/cxyfer/notesteme`（private），base branch 是 `master`。Go module 路徑沿用 `github.com/cxyfer/notesteme`。
- **已完成**：
  - 切片 0：可執行的驗收骨架。
  - 切片 1：已登入的 owner 新增有人工字幕的 YouTube 影片，下載到 SRT。
  - 依審查結果修正，以及 PR #1（引入 sqlc）。
  - PIPE 切片：依類別的並行上限與暫停、優先順序與 aging、重啟回收、正常關閉的寬限期、取消、SSE 事件、lease_token 檢查、保留最近 3 版、執行紀錄 API、最小的 Supadata 來源。
  - YouTube 影片 ID 改為嚴格的 11 個字元（場景代號一併改名，例如 `abc123xxxxx`）；yt-dlp 回報影片不存在時不重試。
  - CLI 切片：`internal/cli`（`ingest`、`items`、`export`、`doctor`）、standalone 與 client 模式、本機 Unix socket（`/data/notesteme.sock`）、`--server` 加 token、結束碼、`internal/doctor`、`item.created` 事件、通過場景的棘輪（`features/passing.txt`）。
  - GRD 切片：netguard 的 allowlist（網段、單一 IP、主機名稱）接到設定檔 `network.allow_private`；轉址上限 5 次；回應大小上限（feed 10 MiB、音檔 2 GiB，超過即拒絕不截斷）；被擋的請求回 422（`blocked_address`、`too_many_redirects`、`response_too_large`、`unsupported_scheme`）並歸為 permanent；`internal/pathguard`（inbox 與 `media_roots`，解析 symlink、`..`、dangling symlink）在 serve 與本機 socket 回 403 `path_outside_roots`，standalone 不受限；YouTube 播放清單展開（ING-R4，yt-dlp `--flat-playlist -J`，同一個交易建立）。
  - SRC-A 切片：Groq ASR 來源（`internal/source/groq`，OpenAI 相容的 `verbose_json`，key 用環境變數 `NOTESTEME_GROQ_API_KEY`，預設 preset 內建）、yt-dlp 音訊下載（`--max-filesize 2G`，Groq 單檔 25 MiB 上限）；fallback 錯誤改為與來源無關（`errclass.ErrNoResult`、`ErrNotConfigured`，訊息與動作由來源的 `errclass.Error` 帶）；yt-dlp 403 → `needs_action`（SRC-R9）；等待 YouTube 自動字幕（SRC-R6）；`language_mismatch`（SRC-R4 的一部分）；transcript 依第一個 fallback 來源歸 `asr` 或 `download`；doctor 檢查 Groq key；harness 新增 fake Groq（Bearer）、stage 的 blocked／waiting／message／actions 斷言、provenance 與 cue timing 步驟，secret 掃描也涵蓋假工具的參數。
  - SRC-B 切片：Bilibili 來源（`internal/source/bilibili`，yt-dlp `--use-extractors BiliBili`；Probe 不帶 `--no-warnings`，靠登入警告判斷需要 cookie；字幕軌 `ai-<lang>` 是 `bilibili-ai`，其他是 `bilibili-cc`，timing 都是 `cue`；從 AI 字幕軌推斷原文語言）；分P 展開（ING-R3）；SRC-R8 需要 cookie 時 blocked；cookie 存放（`internal/cookies`：正規化成 Netscape、`secrets` 表以資料金鑰 XChaCha20-Poly1305 加密、每次呼叫一個 0600 暫存檔）；`POST /api/cookies`（512 KiB 上限、控制字元拒絕、回應不含值）；`stage_runs.resume_on` 與 `ResumeBlocked`（上傳 cookie 後恢復同一個 token 的 blocked 步驟；執行中才補上 cookie 時改為 requeue）；YouTube 的 cookie 注入、members-only 與 403 帶 `cookie:youtube`；playlist 的 extractor 修正為 `youtube:tab,youtube:playlist`；yt-dlp stderr 中的 cookie 警告行會遮蔽；harness 的假 yt-dlp 照真實語意模擬 Bilibili、`--use-extractors`（fullmatch）、Netscape cookie 檔，secret 掃描涵蓋 cookie 值與關閉後的 `/data`，CLI 步驟有 30 秒的真實時間逾時。
  - OPS 切片：
    - 備份：`POST /api/backup` 串流 tar.gz，內容是 manifest、`VACUUM INTO` 的 DB 快照、設定檔；只限 owner；CLI 寫到 `--out`，並更新 `latest` symlink。
    - 還原：`notesteme restore` 只取得資料目錄的鎖，不經 API；解壓只接受白名單項目，並檢查剩餘空間；先留 pre-restore 快照，含 WAL。
    - migration：前置快照，失敗時還原並拒絕啟動，DB 比程式新時拒絕啟動。
    - 新增 `events` 表：與發布在同一個交易中寫入，worker 每天清理 90 天前的事件。
    - yt-dlp：受管理的 venv 加 symlink 切換、自我檢查失敗時回退、固定版本、`pip --isolated`。
    - `toolexec.Locate` 的尋找順序：設定檔 → 環境變數 → 受管理的 venv → PATH，`/readyz`、`/api/system`、doctor 都使用這個順序。
    - `GET /api/system`。
    - 設定檔的 `asr.providers`；`POST /api/providers` 的 command 類型回 403，`GET /api/providers/{id}` 唯讀。
    - harness：fake python 與 pip、fakecompose（離線模擬 `docker compose config`）、指定 schema 版本的 DB 與 schema 比對、`.tar.gz` 內容比對、After hook 掃描 `/backups`。
  - LIB 切片：
    - `GET /api/items` 支援篩選：platform、channel、status（同一個 stage 是 OR、不同 stage 是 AND，沒有執行紀錄視為 pending）、error_class、lang、發布日、時長、timing、標題搜尋。
    - 標題搜尋用 `items.title_fold`（Go 的 `ToLower`）加 `instr`。
    - 分頁用 keyset cursor，`total` 另外查，排序由新到舊；cursor 帶篩選條件的指紋，只接受標準形式。
    - `artifacts.timings_json`。
    - migration 00006（`stage_runs_latest` 索引取代 `stage_runs_item`）。
    - `/api/queue` 改名為 `lanes`、`waiting`。
    - CLI 讀取所有頁面，`--status`、`--channel` 接上 API。
    - harness：影音 fixture（`features/fixtures/items/`，只適用於 `yt:`、`bili:`，而且影片沒有任何字幕軌、場景沒有準備任何來源時才用）；`listItems` 跟著 cursor 讀完所有頁面，有循環防護。
  - DEL 切片：
    - 移到垃圾桶：在一個交易中標記 `deleted_at`，步驟改成 skipped，原因 cancelled，動作「重試」；之後取消任務、刪除媒體快取。
    - 垃圾桶中的影音：所有讀取都回 404 `in_trash`；新增時回 409；播放清單與分P 中在垃圾桶的項目標示 `in_trash`。
    - 還原、垃圾桶列表。
    - 永久刪除，在一個交易中完成：cost ledger 保留標題、tombstone、刪除 events 並改寫 `item.purged`、cascade、孤兒 `config_snapshots`；之後刪除 `artifacts`、`media`、`uploads` 下的 `<id>` 目錄。
    - 清空垃圾桶、刪除預覽、每天清理到期的影音（`library.trash_retention_days`）。
    - CLI：`items rm`、`items restore`、`trash ls`、`trash empty`；不在終端機時沒有 `--yes` 就回 2，終端機判斷用 isatty。
    - migration 00007：`item_tombstones`、`cost_ledger`。
    - worker 的定期工作改在 round 鎖之外執行，避免清理呼叫 `Cancel` 時死結。
    - Rerun 與 Trash 的競態已修：engine 遇到垃圾桶中的影音時，取消任務並把步驟改成 skipped。
    - harness：拿掉 `titled` 預設語言不算「宣告」的例外（與 steps.md 一致）；`substRefs` 改用 `itemID`；垃圾桶中的 stage 斷言改讀 DB；`World.Refs` 在每次新增時更新。
  - AUTH-SEC 切片（`feat/auth-secrets`，疊在 `feat/delete` 上）：
    - migration `00008_api_tokens.sql`（`api_tokens` 表 + `api_tokens_active_name` partial unique index）與 `queries/{tokens,keys,secrets}.sql`（英文註解）。
    - API Token（`internal/auth/tokens.go`）：`nst_` + 32 bytes base64url（SHA-256 雜湊儲存、明文只在建立時回傳一次、`last_used_at` 寫入節流每小時一次）；名稱拒絕 `/` 與控制字元；共用 `auth.ParseTokenDuration`（整數 `d` 天數與 Go duration，拒絕空值／0／負數／`+`）。
    - Scope 權限矩陣（`internal/api/scopes.go`）：透過 `registerScoped`／`registerOwnerOnly` 在 Huma `Operation.Middlewares`（解析請求 body 之前）檢查 `requireScope`／`requireOwner`；精確比對 `read`、`agent`、`ingest`、`admin`（無隱含繼承），缺少時回 403 `forbidden` 並帶 `required_scope`；涵蓋 `DEL-R12`（`GET /api/trash` 與 `deletion-preview` 為 `read`，刪除／還原／清空為 `admin`）、`GET /api/events`（`read`）、`POST /api/estimate`（`ingest`）、`POST /api/tasks/{id}/retry|cancel`（`admin`）、`GET/PUT /api/settings/{section}` 與 `POST /api/providers`（`admin`）。
    - Token API（`internal/api/tokens.go`）：`GET /api/tokens`、`POST /api/tokens`（409 `token_exists`、422 驗證）、`DELETE /api/tokens/{id}`（支援 id 或作用中 name）。
    - 外部服務 API key（`internal/secrets/services.go`、`internal/api/settings_services.go`）：`GET/PUT /api/settings/services` 加密寫入 `secrets` 表（`Keyring.Seal("provider_api_key", ...)`）、`GET` 即時解密計算 `api_key_last4`、空字串刪除、`nil` 保留、控制字元 422 且不回顯值；`supadata`、`groq`、`pipeline`、`app/sources.go` 支援環境變數優先於 DB 解密 fallback（存完即生效、免重啟）。
    - Master key 輪替與重設（`internal/secrets/rotate.go`、`internal/app/secrets.go`）：在 `store.LockDir` 離線鎖與單一交易中完成（不呼 `openCore`/`secrets.Open`，確保遺失 master key 時仍可 `reset` 並保留影音庫）。
    - CLI（`internal/cli/tokens.go`、`internal/cli/secrets.go`）：`tokens create|ls|revoke`（經 `openBackend` 支援 standalone／本機 socket／`--server`）、`secrets rotate-key --new-key-env` 與 `secrets reset [--yes]`（離線鎖、拒絕 `--server`、非終端機缺 `--yes` 回 2）。
    - 驗收步驟（`features/steps/{access,files,scenario,secrets,processing}.go`）：實作 `a token … with scopes …`、`the token … expired`（經 `Server.ExpireToken`）、`the client sends … with the token …`（含 `and:` 與 `POST /api/items` 預設 body）、`the data directory does not contain the created token`、`the logs do not contain …`；`noteRequest` 追蹤 `PUT /api/settings/services` 的 `api_key`；`itemProcessedUpTo` 在未呼叫 `the server is running` 時收掉暫時啟動的 server。
  - LOCAL 切片（ING-05，PR #13，決策見 ADR-0016）：
    - migration 00009：`items` 的 `source_path`、`content_sha256`、`origin`；`inbox_entries`；tombstone 記 `sha256`。
    - 識別：`local:<相對 inbox 的路徑>`、`local:<絕對路徑>`（media_roots）、`local:<檔名>`（上傳，撞名加 `-2`，也避開 inbox 頂層的同名檔）。
    - `internal/pathguard/open.go`：`Resolve`、`Open`（`os.OpenRoot`、`O_NONBLOCK`、只收一般檔案）、`With`。
    - `internal/media`：固定 argv 的 ffprobe／ffmpeg，一律帶 `-protocol_whitelist file` 與 `-format_whitelist`；`CanonicalLang`。
    - 新增入口：`{"path"}`（延遲驗證）、multipart 上傳（4 GiB、閒置逾時 2 分鐘、並行 2、part 16，先檢查 scope 再讀 body）、`internal/inbox` watcher（穩定 10 秒、ffprobe 篩選、`sha256` 去重、tombstone）；`IngestLocal` 的判斷順序是鍵 → tombstone → `sha256`。
    - `GET /api/items/{id}/media`：`ServeContent`、ETag、Content-Type 白名單、`nosniff`、CSP sandbox。
    - 字幕來源 `local-sidecar`、`local-embedded`；讀不到時記 `rejected`／`unreadable`，改用下一條；Supadata 對 local 是 `unsupported`。
    - ASR（`internal/pipeline/asr.go`、`internal/audio`）：所有平台共用前處理（Opus 16kHz），超過上限時在靜音處切塊、依中點接回。ASR 語言寫回原文語言（`lang_source=asr`），之後重跑不把它當輸入。
    - 非上傳的本機檔案每次 probe 比對大小與修改時間，不同就重算 `sha256`；外掛字幕內容的 `sha256` 進快取鍵。
    - 備份包含 `uploads/`（manifest 記大小與 `sha256`），還原時原本的 `uploads/` 移到 `/backups/pre-restore-uploads-<時間>/`；啟動時清理孤兒的 `uploads/<id>`。
    - harness：marker v2（`streams`、`compressed`、`silences`、`offset`）、會轉碼／切塊／靜音偵測／抽字幕的假 ffmpeg、假 Groq 依 `offset` 過濾、`features/fixtures/{local,media}/`；字幕表格的 `#` 不從 1 起連號時依 `#` 比對。
  - PODCAST 切片（ING-04、SRC-R14／R15、GRD-R2 rebinding，疊在 PR #13 上，決策見 ADR-0017）：
    - `internal/podcast`：RSS／Atom 解析（itunes、podcast namespace、`<podcast:transcript>`）、`CanonicalURL`、`FeedSlug`。
    - 單集是 `(podcast, "<feed key>/<guid>", 0)`；feed key 存在 `channels`（`PodcastFeedKey`，撞名加 `-2`），不需要 migration。
    - 新增：`expandPodcast` 驗證 `episodes`，並用 `netguard.CheckURL` 對音檔網址做只解析、不連線的預檢；`insertTargets` 與一般新增共用同一個交易迴圈。
    - metadata：`runPodcastMetadata` 抓 feed、存快照（`metadata_json.podcast`）、下載音檔到 `/data/media/<id>/episode.<ext>`（2 GiB、`sha256`、ffprobe 長度）。
    - 來源 `podcast-transcript`：讀快照、支援 VTT／SRT／Podcasting 2.0 JSON；抓取失敗改走 fallback；SRC-R15 偏移超過 2% 時 transcript 為 done 帶「時間可能偏移」。
    - podcast 的 ASR 音訊來源與媒體端點共用 LOCAL 的 `transcribeASR`、`MediaFile`。
    - harness：`features/internal/fakepodcast`（每次依狀態產生 RSS、音檔改寫 marker 的 item、字幕呼叫計數）、`features/fixtures/podcast/`（marker 的 `listed=` 是 feed 宣告的長度）；`shows the message` 沒有非 done 的訊息時取 done 步驟的訊息。
  - ESTIMATE 切片（ING-R8，疊在 PODCAST 上）：`POST /api/estimate` 與新增共用辨識與展開（`expandRequest`），依生效的 `language` 用 transcript 步驟的規則選軌，平台字幕不計 ASR；fallback 依序估 Groq（內建計價表 `internal/cost`）或 Supadata（credit 計價，金額 `null`）。不寫入資料庫，本機檔案回 422。決定記在 ING 的待決問題。
  - M2 S0 骨架（`feat/m2-s0`，疊在 ESTIMATE 上；決策記在 PIPE、LLM、PRF、AUTH 等規格的待決問題，ADR-0002 加修訂行）：
    - `pipeline.Stages` 擴充為 7 步：metadata、transcript、normalize、segment、correct、translate、publish。
    - `ResolveExecutor`／`planChain`／`applyChain`（`internal/pipeline/executor.go`）：前進到某步時才依快照解析 executor；`off`、`api` 沒有 provider／model、`publish` 沒有輸出就直接 `skipped`（`block_reason` 為 `off`、`not_configured`、`no_outputs`），不建任務；要執行而尚未實作的步驟在寫入前回 501。
    - 設定快照的 `stages`（只限 LLM 步驟）與 `outputs`；`RerunRequest` 加 `Profile`、`Chunks`（回 501）。
    - queue：`llm` 類別上限 `Control.SetCap`；`TestControls.SerialLLM` 讓 llm 任務依序執行。
    - API：29 條 M2 路由的 501 stub（`internal/api/m2_stubs.go`，scope 檢查在讀 body 之前）；stage 回應加 `executor`；CLI `items show` 顯示 `skipped（not_configured）`、`blocked（reason）`；`--wait` 對沒有後續步驟的舊影音改看最後一個存在的步驟。
    - harness：`features/internal/fakellm`（Anthropic、OpenAI 的 wire 契約檢查、依序／依 chunk 回答、`Fail`、generator 登記與 `echo_cues`）、`features/steps/llm.go` 的 LLM 步驟、`features/fixtures/llm/` 9 個 fixture、`features/fixtures/items/` 7 個 M2 影音 fixture；`processed up to <M2 步驟>` 依規則 pending；`from the origin`；帶 bearer token 的請求視為完整憑證（預期 403 時 501 → pending）。
  - M2 S1 處理方案＋S2 Prompt 庫（`feat/m2-prf`，疊在 S0 上；決策 D1～D16 記在 PRF、PRM 規格的待決問題）：
    - 處理方案以名稱識別（`profiles`、migration 00010），內建 `default` 不能修改或刪除；`POST/GET/PATCH/DELETE /api/profiles`、`POST /api/profiles/{id}/default`。
    - 設定分層 builtin → 全域 → 處理方案 → 頻道 → 單次覆寫 → 重跑（`internal/pipeline/layers.go`），物件依 RFC 7396 合併、陣列整個取代；快照記下各層（`layers`）。頻道層在 `metadata` 完成時才併入（D6）：頻道與快照記下的不同時重建快照，並把還沒開始的下游步驟改綁。
    - 驗證（`internal/pipeline/schema.go`、`internal/validate`）：未知欄位、型別、列舉、provider、prompt 引用（類型要和步驟相符），422 回 `errors[{path,message}]`（D9）；完整性只檢查處理方案中寫出來的 `api` 步驟（D8）。
    - 頻道覆寫：`PATCH /api/channels/{key}/overrides`，key 是 `<platform>:<external_id>`（D14）。
    - Prompt 庫（`internal/prompts`、migration 00011）：版本只增不改，內建 prompt 嵌入在 `internal/prompts/builtin/<id>/v1.md`（D12）；引用寫成 `<id>`、`<id>@default`、`<id>@N`（D11）；變數在儲存時檢查；刪除時 `used_by` 依處理方案、`settings`、頻道、影音（`item:<代號>`，最多 20 個）的順序列出，檢查與刪除在同一個寫入交易（D13、P7）。
    - harness：`features/steps/config.go` 的處理方案、prompt、頻道覆寫步驟（Given 累積，第一個請求前寫入）、`jsonmatch.go` 的子集比對（陣列完全相等）；CLI 步驟遇到尚未寫入的處理方案等設定會隱含啟動 server；`words` 欄位接受秒數（`1.5`）。
    - 變異測試 10 個都會變紅。`DeleteProfile` 拿掉 builtin 檢查時，PRF-R7 場景仍然通過（內建的 `default` 同時是預設處理方案，「使用中」也回 409），只有單元測試 `TestProfiles_DeleteDefault409` 抓得到；`GET /api/profiles/{id}` 回傳合併了內建預設的 config 時，子集比對驗證不到。
  - M2 S6a TRN 確定性轉換（PR #19，ADR-0018）：
    - `internal/zhconv`：自己實作的 OpenCC 轉換器（最長詞優先，內嵌 Apache-2.0 的 OpenCC 1.4.2 字典，`scripts/opencc/update.sh` 重新產生），不依賴 GPLv2 的 port；`langtag` 判斷 BCP-47 的文字與地區，決定 `s2t`、`s2twp`、`s2hk`、`t2s`、`tw2sp` 等轉換表。
    - `translate` 步驟的 `opencc` executor（`runTranslateOpenCC`）：中文文字系統轉換不經過 LLM；`zhconv.GlossaryTable` 提供 COR-R4 詞彙表的轉換表，詞彙表的組裝屬於 LLM 切片。
    - `@M2`：20 passed、23 failed、74 pending；TRN「Chinese script conversion uses OpenCC」3 個範例通過，「Localisation runs OpenCC first…」要等 S6b。
  - M2 S7 OUT 渲染、ASS 樣式與匯入（直接推送到 master，不經 PR）：
    - `internal/assstyle`：內建樣式 `default`、`translation-top`、`translation-only`、`large`、`boxed`、`minimal`（唯讀）與自訂樣式（migration 00012，`GET/POST/PATCH /api/ass-styles`、`/{id}`、`/import`），字型依目標語言文字系統帶入（OUT-R7）。
    - `internal/subtitle`：ASS 解析與渲染、雙語輸出（`format=ass|bilingual-srt&style=`；同語言一律單語，回應標頭 `X-Notesteme-Bilingual`）、匯入解析（SRT／VTT／ASS，不可信輸入有 fuzz）。
    - 匯入：`POST /api/items/{id}/subtitles/import`（`path` 或 `content`；`path` 只收字幕副檔名、解析 symlink 之後才檢查、不受 GRD-R7 限制）存成 `edited` 變體（`executor: human`），既有譯文標示過時；CLI `export --format ass`、`import <subtitle> --item`。
    - 預覽：`POST /api/ass-styles/preview`（scope `ingest`，ffmpeg + libass，沒有影片時用 1280x720 合成畫布；同時 2 個、逾時 30 秒）。本機沒有真的 libass，只用假 ffmpeg 與單元測試驗證，**要在有 libass 的環境手動跑一次**。
    - harness：假 ffmpeg 的預覽模式、請求 body 的 `/tmp/<x>` 對應到場景根目錄（`steps.md` 第 4 節）；順手修了 `TestFFmpegSilencedetect` 的 flaky 斷言（暫存目錄的隨機數字可能含 "100"）。
    - **依 /goal 由 advisor 裁決、待 owner 追認**：OUT-R5 同語言場景最後一步改成斷言標頭（已改 `.feature`）；`path` 匯入不受 GRD-R7 限制；預覽 scope 改 `ingest`；M2 路由 scope（讀取 `read`、寫入 `admin`，settings 維持 `admin`）。記在 OUT、AUTH 規格的待決問題。
    - `@M2`：31 passed、23 failed、63 pending（OUT 剩 R10～R12 的 S8 與 MCP 的 `import_transcript`）。
  - M2 S3 LLM 核心引擎（直接推送到 master；ADR-0019）：
    - `internal/llm`：provider client（`anthropic`、`openai`、自訂 `base_url` 的 OpenAI 相容 provider；共用一個放寬 header timeout 的 Transport，出站走 netguard）、`PlanChunks`（章節優先，否則依 token 估算 L2）、`BuildFixed`／`DataBlock`／`RetryNote`（固定前綴、`<transcript_data>`／`<video_metadata>`、JSON 跳脫）、`Spec{Stage, Schema, OutputRules, Check}` 與 `Spec.Validate`（預設拒絕額外欄位）、`RunChunk`（驗證重試、租約期限內完成）、`MergeCues`（重疊區以前一個 chunk 為準）、`Registry.Client`（key：環境變數 → DB 加密 secrets）、`Limiter`（GCRA 的 RPM／TPM，429／5xx 與 `Retry-After` 另計）。
    - 新增一個 LLM 步驟只要在 `pipeline.llmStages` 登記 `{spec, variant, fallback, apply}`；`usesLLMEngine` 統一判斷（ingest、engine 分派、`stageImplemented`），`checkLLMStages` 在啟動時檢查 Spec。目前只登記 `correct`（最小可用：一對一 `{i, text}`、cue 數不符 → fallback `cue_count`）；`segment`、`translate` 的 `api` 路徑仍回「還沒有實作」。
    - 每個 chunk 一個任務（migration 00013 `llm_chunks`；`plan_json`、兩層快取、`fallbacks`、每 chunk token 用量記在產出來源紀錄的 `chunks[]`）；缺 key → `blocked`（`needs_action`，「設定 API key」）；模型不存在／停用 → `blocked`（「更換模型」）；`insufficient_quota` 的 429 → `needs_action`（LLM 規格 L15）。
    - 設定：`llm.providers.<id>.{type, base_url, rpm, tpm}`（provider id 正規化後的環境變數名稱撞名時拒絕）；`llm.max_validation_retries` ≤ 10、`context_cues`／`overlap_cues` ≤ 100、`max_tokens` ≤ 200000；rerun 的 `chunks` 不在最近一次切分實際存在的 chunk 編號中回 422（S6b-Q3，空洞也算不存在）。
    - 依 /goal 由 advisor 裁決、待 owner 追認：LIB-R11 場景修改 `.feature`（在場景內建立 `llm-lib` 處理方案，用 rerun 帶 `profile` 產生第一版）；L8（模板變數 `title`／`channel`／`description`／`chapters` 改寫成指向 `<video_metadata>`，LLM-R9 優先於 PRM-R3）、L9（驗證重試可透支限流）、L12～L15；`steps.md` 規則 (2c)（M2 目標步驟 done 或 skipped 都算「處理到」）。
    - 已知限制（LLM 規格 L14）：chunk／assemble 任務重新讀 title／chapters／description、前一個 chunk fallback 時重疊區仍取前者、驗證重試途中放回佇列時用量不記、TPM 只估輸入、時間上限到時已做的驗證重試作廢（沒有續租）。自訂 provider id 不能存 DB key（services 白名單），只能用環境變數；設定頁還沒有 rpm／tpm 欄位。
    - 審查：S3 推送前有一次只看 diff 的獨立審查，沒有 key 洩漏或 SSRF；修了連線池、租約、上限、重試訊息回灌、`Retry-After` 溢位等 11 項（commit 在 `git log`）。
    - `@M2`：52 passed、32 failed、33 pending。PRM 的 compare／playground（PRM-R6／R7）還沒做（M2 剩餘項目，`estimate_usd` 沒有成本帳本時回 `null`），不是 M4。
  - M2 S8 OUT sidecar／folder 輸出（直接推送到 master）：
    - `internal/output`：`Config`（`outputs[]` 每一項的驗證：`target`、`formats`、`lang`、`on_user_change`、folder 的 `path`）、`Writer`（`os.Root` 限制在資料夾內、不跟隨 symlink、`O_EXCL` 暫存檔再硬連結／rename、sha256 防覆蓋）。migration 00014 `outputs` 表（主鍵 `(item_id, path)`，item 刪除時 cascade）；`GET /api/items/{id}/outputs`（`read`），`items show` 列出輸出檔。
    - 行為：`sidecar` 只用於本機檔案（上傳的副本不寫），寫成 `<影片檔名>.<lang>.<ext>`；非本機檔案時 `publish` 是 `skipped`（`not_applicable`，新的 skipped 原因，steps.md 已同步）；`folder` 路徑走 pathguard（serve：inbox 與 `media_roots`）並排除資料目錄內部；目標檔案沒有紀錄或雜湊不同一律視為使用者的檔案：`save_as_new`（預設）另存 `<名稱> (notesteme YYYY-MM-DD).<ext>`（UTC，同日加序號）、`skip` 則 `blocked(needs_action)`；`bilingual-srt` 檔名多 `.bilingual`；`vault` 目標回「M3 才提供」。
    - 依 /goal 由 advisor 裁決、待 owner 追認：OUT-R10～R12 三個場景修改 `.feature`（補 `default profile`／`rerun` 帶 `profile`）；沒有紀錄的既有檔案視為使用者的；`path` 同 GRD-R7 的根目錄；sidecar 語言碼用 BCP-47（**待實測 Jellyfin／Plex**）。
    - 已知限制：`local-sidecar` 來源會把自己輸出的 `lecture.en.srt` 當外掛字幕讀回（要處理得動 `local.go` 的 `listSidecars`）；比對雜湊後、rename 前的小競態。（DEL-R9 的 `delete_outputs`／`kept_outputs` 已在 PRM／DEL-R9 切片完成。）
    - 審查：獨立的 diff 審查子任務被 OOM 殺掉（機器記憶體吃緊），改由主代理自己讀了 `writer.go` 與 folder 路徑解析，沒有發現問題。
    - `@M2`：58 passed、26 failed、33 pending。
  - M2 S4 COR 校正完整行為（直接推送到 master）：
    - `correct`：相似度檢查（rune 為單位、忽略大小寫的 normalized Levenshtein，取 chunk 最低值，門檻 `stages.correct.min_similarity` 預設 0.6，COR-R2／R3，演算法待 M0）、詞彙表（全域 → 頻道 → 方案層含頻道覆寫，大小寫去重；放在 `<video_metadata>` 而非系統指示，模板的 `{{glossary}}` 也改成指向它）、`mixed` 語言規則（COR-R5，目前只認 `items.lang_mixed`，沒有產品流程會標 mixed，SRC-R11 是 `@wip`）、術語候選 `term_candidates`（pending，不自動入詞彙表，已拒絕的詞不重複）。
    - 新 API：`GET/PUT /api/glossaries/{scope}/{id}`（`global`／`channel`）、`GET /api/channels/{id}/term-candidates`；migration 00015（`glossaries`、`glossary_candidates`、`llm_chunks.detail_json`）；詞彙表傳給 Groq ASR 的 `prompt`（最多 200 字元，沒有詞彙表時快取鍵不變，來源紀錄 `asr.prompt`）。
    - 依 /goal 由 advisor 採保守預設、待 owner 追認：COR 規格 C1～C7。
    - **還沒做／待決**：術語候選的核可／拒絕 API 已在 S11 補上（`PATCH /api/channels/{id}/term-candidates/{cid}`，見 COR 規格 C4）；`@pending-m0` 的 `A rewrite that strays too far` 場景寫「相似度 0.42」，Levenshtein 算出 0.19，要等 M0 決定算法或改場景（2026-11-24 已把場景改成 0.19，見第 2 節）；COR-R8 場景在 S6b 補上 `translate-two-cues` fixture 後已通過。
    - `@M2`：62 passed、24 failed、31 pending。
  - M2 S5 SEG 斷句（直接推送到 master）：
    - `segment` 登記在 `llmStages`：LLM 輸出 `{text, breaks}`（`breaks` 是新字幕在 `text` 中開始的 code point 位置，**不含時間**；時間欄位視為驗證失敗）；對回來源時忽略空白、標點與格式字元、大小寫要相同（`text_changed`），斷點切在字中間或產生空字幕是 `alignment`（fallback 保留原切法）；產出文字一律從來源切出。時間規則：`merged`／`source`（word 時間）／`silence`（`silencedetect`，停頓點取離字數比例估計值最近者）／`interpolated`；`timing.split_timing`（`forbid`／`when_needed`／`free`）、`timing.split_timing_audio`（`never` 不下載、`auto` 只下載音訊）；`stages.segment.on_manual: skip|run`，新的 skipped 原因 `manual_captions`（steps.md 已同步）；`internal/subtitle` 的 per-script 字幕規則（CJK 每行暫取 22 字）與 `warnings[]`。`timing`／`subtitle_rules` 只進步驟層快取鍵，不進 chunk 層，所以只改時間設定的重跑會重用已付費的 chunk。
    - 依 /goal 由 advisor 採保守預設、待 owner 追認（SEG 規格待決問題）：G1 輸出格式與「SEG-R2～R4」的解讀；SEG-R8 場景表格補第 2 列（獨立 commit，可單獨 revert）；R9／R10 衝突（R10 強制切分只作用在 LLM 沒有段內切開的句子，超過上限的只記 `warnings`）；R10 CJK 範例補宣告影片。
    - 已知限制：assemble 是 local 任務，會在租約內下載整份音訊並跑 `silencedetect`（逾時 10 分鐘），長片可能超過租約；`max_cps` 沒有使用；真實 ffmpeg 對下載原檔跑 `silencedetect` 的路徑沒有在本機驗證；`TestRunnableLLMStageCreatesLLMTaskThatFailsPermanently` 目前用 translate/api，S6b 登記 translate 後要一起改。
    - `@M2`：71 passed、15 failed、31 pending。
  - M2 S6b TRN LLM 翻譯（直接推送到 master）：
    - `translate` 的 LLM 路徑（`runTranslateLLM`）：多個目標共用一個 stage run、每個目標一份 `translated` 產出；句群以切分標記 `⟨|⟩` 組成翻譯單位並切回（TRN-R2～R4）、驗證重試與退回（R5～R7，空、與原文相同、語言、長度比、閱讀速度）、localize（R9，先 OpenCC 轉字形再交給 LLM 潤飾）、沿用平台人工字幕軌（R11）、有 `edited` 時以它為輸入、每個目標獨立的快取鍵。語言偵測是純 Go 的 `internal/langdetect`（只看文字系統）。
    - 依 /goal 採保守預設、待 owner 追認（TRN 規格 S6b-Q1～Q11）：Q2 的場景修改只補了場景缺少的 `correct` 設定與 `correct-echo` 答覆，保留所有斷言；COR-R8 場景補上 fixture `translate-two-cues` 後通過。
    - **仍待決**：S6b-Q6 `length_ratio` 下限 0.3 對英翻中太緊（照規格字面實作）；香港、澳門繁體的 localize 沒有內建 prompt（會失敗，建議改用 `convert`）；`POST /rerun` 的 `chunks` 在沿用目標有空洞時上限檢查不準（S6b-Q3，已於 2026-11-23 修正：改對照最近一次切分實際存在的 chunk 編號，待 owner 追認）。
    - `@M2`：83 passed、3 failed、31 pending（失敗的是 MCP stdio、DEL 兩個，分別等 S9 與 DEL-R9 切片）。
  - M2 PRM 試跑與比較＋DEL-R9 輸出檔刪除（直接推送到 master）：
    - `POST /api/prompts/playground`、`POST /api/prompts/{id}/compare`（`internal/pipeline/playground.go`、`internal/api/prompts_playground.go`、`internal/prompts/diff.go`）：同步呼叫 `internal/llm`（`PlanChunks`／`BuildFixed`／`RunChunk`），不經過 task queue 與 stage run，不寫入任何東西（單元測試比對 stage_runs、artifacts、llm_chunks、events、tasks、術語候選前後相同）。步驟由 prompt 的類型決定；`llmStages` 只讀；一對一的步驟（correct）可以用，自己合併輸出的 segment（`assemble`）與自己處理多個目標的 translate（沒有 `apply`／`fallback`）回 501，要各自的切片補上。範圍上限 200 句、同時最多 2 個、整體 3 分鐘；缺 key 409 `needs_action`、限流 429、provider 失敗 502。`estimate_usd` 目前一律 `null`（`internal/cost` 沒有任何 LLM 價格，只加了 `EstimateLLM` 與單位常數）。決定 P9、P10 記在 PRM 規格，待 owner 追認。
    - DEL-R9：`DELETE /api/items/{id}` 帶 `delete_outputs: true` 時，刪除 `outputs` 紀錄中 sha256 與現在檔案相同的檔案（`internal/output/remove.go`，`os.Root`、不跟隨 symlink；先過 pathguard），改過的列在 `kept_outputs`（`modified`、`outside_roots`、`unreadable`）；`deletion-preview` 列出輸出檔與 `modified`；還原時有紀錄但檔案不在就從 `publish` 重跑寫回。永久刪除不動輸出檔（規格資料表）。CLI `items rm --delete-outputs` 原本就有，現在會印保留的檔案。決定記在 DEL 規格。
    - 依 /goal 修改 `.feature`（待 owner 追認）：`A playground run changes nothing` 補 `standard` 處理方案，否則 `correct` 是 `skipped`；harness 的 `the response contains` 不再展開預期值中的虛擬路徑（回應已換回虛擬路徑，steps.md 已同步）。
    - `@M2`：以 S6b 之後的 master 為基準，83 → 87 passed、3 → 1 failed（剩 MCP stdio）、31 → 29 pending（PRM 兩個、DEL 兩個由失敗或 pending 變通過）。
  - M2 S9 MCP（stdio 與 Streamable HTTP）：
    - `internal/mcptools`：tool（`ingest`、`list_items`、`get_item`、`get_transcript`、`import_transcript`、`get_video_context`、`search`）與 prompts，全部轉成程序內的 API 呼叫（`Backend`），scope、驗證與錯誤和 API 相同（MCP-R3）；AGT 的四個 tool 在 `agent.go` 註冊成 `not_implemented`，**S10 只要替換那裡的 handler**。`internal/api/mcp.go`：`/mcp` 用官方 go-sdk v1.8.0（stateless、JSON 回應），Origin／Host 檢查（AUTH-R11，設定檔 `mcp.allowed_origins`／`allowed_hosts`，只在 TCP）、請求上限、同時處理數、讀取期限。`internal/cli/mcp.go`：`notesteme mcp [--token] [--list-tools]` 是轉送到 `/mcp` 的 stdio 代理，獨立模式在背景跑 worker。
    - API 的 `GET /api/items/{id}`、`/cues`、`/subtitles`、`POST …/subtitles/import` 的 `{id}` 也接受影音代號；`api.NewLocal` 遇到 bearer token 改用 token 的身分（`mcp --token` 在本機模式限制 scope）。
    - 依 /goal 採保守預設、待 owner 追認：MCP 規格待決問題 S1～S10（AGT stub、本機 `--token`、`import_transcript` 只收 content、`get_video_context` 做到 normalize 與完成的判斷、`timeout_s` 上限、SRT 結尾換行、prompts 的 L8 處理、上限、Origin／Host 預設）。
    - 已知限制：沒有 progress notification；已在媒體庫但沒有字幕也沒在處理的影音，`get_video_context` 回讀取字幕的錯誤，不會自動重跑；stdio 代理回傳的結果可能帶新版協定的欄位（`resultType` 等，舊 client 會忽略）。
    - `@M2`：以 PRM／DEL-R9 之後的 master（d109130）為基準，87 → 102 passed、1 → 0 failed、29 → 15 pending（MCP 13 個、AUTH-R11、CLI 的 stdio 場景通過；剩下的 pending 是 AGT 14 個與 SEC 的 config import）。M1 預設 tag：191 passed、0 pending。
  - M2 S11a SEC `config export`／`import`（`feat/config-export-import`，SEC-R10；直接推送到 master 前由主代理 rebase）：
    - `internal/configio`：不可信 YAML 的解析與輸出，沒有資料庫相依。比標準嚴格：不接受別名與合併鍵（展開炸彈）、重複的鍵、非字串的鍵、NaN／無限大、多份文件、不認得的型別標籤；大小上限 1 MiB、巢狀 64 層；輸出自己組 `yaml.Node`（鍵依字母排序、縮排 2 格），因為 yaml.v3 對只含換行的字串會選 literal 區塊、讀回來內容不同（fuzz 找到的）。`FuzzParse` 的不變條件是「讀得進來的內容輸出再讀回來相同、輸出穩定」，另有最壞情況輸入的時間測試。
    - `internal/pipeline/config_io.go`：`ExportConfig`（非內建處理方案＋明確設定過的預設處理方案，不含 secrets）、`ImportConfig`（在**單一交易**中先收集全部錯誤、沒有任何錯誤才寫入；與 `POST /api/profiles` 相同的驗證，錯誤路徑用 `validate.Prefix` 加 `profiles.<名稱>`；內建處理方案有驗證與寫入兩層保護；內容相同不新增版本）。
    - API：`GET /api/config/export`（`read`）、`POST /api/config/import`（`admin`，422 `config_invalid` 附逐欄位 `errors`）；請求用 `{"content": "<YAML>"}`，CLI 本機讀檔後送出，`--server` 模式也能用。CLI：`config export [--out|--output <file>] [--json]`、`config import <file> [--json]`；內容不正確結束碼是 **1**（不是 CLI-R6 對 422 的 2，場景要求），stderr 逐行列出欄位路徑。拿掉 `internal/cli` 的 `unimplemented` 與對應的舊測試。
    - harness：`the owner runs` 的參數套用 `/tmp/<x>` → 場景目錄的對應（steps.md 已同步），這是第一個把 `/tmp` 檔案交給 CLI 的場景；沒有新句型、沒有改 `.feature`。
    - 依 /goal 採保守預設、待 owner 追認（SEC 規格待決問題、CLI／AUTH 各一行、ADR-0013 修訂行）：範圍只有 `profiles`＋`default_profile`；內建 `default` 不匯出也不得覆蓋（但 `PATCH /api/profiles/default` 現在允許修改它，自訂內容不會被匯出）；內容相同不新增版本；處理方案值為 null 視為錯誤；結束碼 1；API 用 JSON 外層而非 `application/yaml`；沒有 `--dry-run`。
    - **已知限制**：處理方案引用的自訂 prompt 不在檔案中，搬到新實例要先有同名 prompt，否則整份不套用；全域設定、頻道覆寫、詞彙表、ASS 樣式等還不能匯出入。
    - 變異測試 14 個（驗證後才寫入、交易、內建保護、secrets 不外洩、冪等、名稱規則、default_profile、路徑前綴、結束碼、scope、大小上限、別名、重複鍵、未知頂層鍵）都會變紅。
    - `@M2`：102 → 103 passed、15 → 14 pending（剩下的 pending 全是 AGT，由 S10 處理）。M1 預設 tag 191 passed 不變。
  - M2 S10 AGT Agent 任務：
    - `executor: agent` 的 LLM 步驟和 api 共用 LLM 引擎：內建 worker 執行 plan 與 assemble，只有 chunk 任務的 executor 是 `agent`（`internal/pipeline/agent.go`）。MCP 的 `claim_tasks`、`extend_lease`、`submit_task`、`release_task`（`internal/mcptools/agent.go`，直接呼叫 pipeline，不經過 HTTP 路由）；領取用 `ClaimTasks` 加上 `kinds` 過濾，在同一個寫入交易中計數與領取（AGT-R3，`-race` 的並行測試）。提交用和 `RunChunk` 相同的 `Spec.Validate`，寫回用與 api chunk 任務共用的 `finishChunkTx`。`AgentSweeper` 是 worker 的定期工作：租約到期放回（attempts 保留）、逾時依 `on_timeout` 改用 API 或 blocked。`POST /api/items/{id}/switch-executor`、`POST /api/tasks/{id}/switch-executor`（`ingest`）取代 501 stub（M2 的 stub 清單已空）。`auth.Principal` 多了 `TokenName`（Agent 的身分）。
    - migration 00016：`tasks.agent_failures`、`tasks.release_reason`、`llm_chunks.executor`／`agent`／`model_reported`；`ReleaseLeases`／`ReleaseOwnedLeases` 不再動 Agent 的租約；chunk 層快取只重用 `executor = 'api'` 的 chunk，有 Agent chunk 的產出不寫步驟層快取鍵。
    - 依 /goal 採保守預設、待 owner 追認：AGT 規格 A1～A11（**A1 並行上限解讀成「每個 Agent 持有的不超過 llm 上限」**，字面讀法與 AGT-R3 場景衝突；**A6 translate 加 agent 以 permanent 失敗**，TRN-R5～R7 的檢查還沒抽成 Agent 可以共用的形式；A3 `max_claims` 讀全域加預設處理方案、其他 `agent.*` 讀 stage run 的快照）、MCP 規格 S3 的補充（AGT tool 是 MCP-R3 的例外）、PIPE 待決問題（重新啟動不動 Agent 的租約）。沒有修改 `.feature`；steps.md 只補了 Agent 步驟的語意說明，沒有新句型。新 fixture：`llm/correct-echo-reported-model`（LLM fixture 多了選填的 `model`，`fakellm.RenderSubmission` 給 Agent 的提交用）。
    - 已知限制：translate 不支援 agent；`GET /api/queue` 仍不列 Agent 任務；提交沒有 token 用量；Agent 任務到期放回沒有次數上限；內建 worker 的任務在執行期間到期仍只在啟動時回收。
    - 重跑取代 Agent 步驟時，`CancelPendingTasks` 也結束舊 stage run 中被 Agent 領取的任務（清掉租約，舊 token 回 `lease_expired`，不會到期後又被放回）。
    - 變異測試（11 項，全部變紅；第 11 項是上面這條）：每個 Agent 的上限、舊 token（heldTask 與 `CompleteTask` 的 SQL 兩層都拿掉才會變紅，單拿掉一層由另一層擋下）、3 次失敗、release 不計 attempts、逾時分支、來源紀錄、共用驗證、重新啟動保留 Agent 租約、Agent 結果不寫步驟層快取、sweeper 放回到期租約。
    - `@M2`：以 S9 之後的 master（472e948）為基準，102 → 116 passed、15 → 1 pending（AGT 14 個全部通過；剩下 SEC 的 config import，與 S11a 合併後是 117 passed、0 pending、0 failed）、0 failed。M1 預設 tag：191 passed、0 pending。
- **預設 tag 與棘輪（2026-11-23 起）**：M2 的場景全部通過後，預設 tag 納入 `@M2`（`features/features_test.go` 的 `defaultTags`，ADR-0015）：308 個場景（191 個 M1、117 個 M2）全部 passed，`features/passing.txt` 308 行（只新增 117 行）。下面的表與數字是 M1 收尾時的紀錄。
- **驗收結果**（M1 收尾時，預設 tag：`@M1 && ~@manual && ~@pending-m0 && ~@wip && ~@ui`）：191 個場景，191 passed、0 pending、0 failed、0 undefined（SRC-R11 mixed 標為 `@wip`，不在預設 tag 內；DEL `@mcp` 兩個在 S9 通過）。

  | Feature | passed | pending |
  |---|---|---|
  | AUTH | 23 | 0 |
  | CLI | 9 | 0 |
  | DEL | 21 | 0 |
  | GRD | 22 | 0 |
  | ING | 24 | 0 |
  | LIB | 10 | 0 |
  | NRM | 11 | 0 |
  | OPS | 14 | 0 |
  | OUT | 8 | 0 |
  | PIPE | 19 | 0 |
  | SEC | 11 | 0 |
  | SRC | 19 | 0 |

  AUTH、CLI、GRD、ING、LIB、OPS、OUT、PIPE、SEC、SRC 的 M1 場景全部通過（ING-R7 隨 S1 通過）。剩下的 3 個 pending：
  - **NRM-R7**：已在 2026-11-22 修好（場景宣告原文語言 `zh-Hans`，依 /goal 採 advisor 建議，待 owner 追認），M1 預設 tag 現在是 189 passed、2 pending（只剩 DEL `@mcp`）。原來的描述：場景的影片原文語言是預設的 `en`，卻只有 `zh-Hans` 的自動字幕。
  - **DEL `@mcp`（2 個）**：M2 S9 已通過。

  M1 的 OUT 場景全部通過，但 OUT 的 M2 部分還沒做，所以規格狀態仍是 `ready`，不是 `implemented`。

- **規格狀態**：AUTH、SEC、ING、SRC、NRM、OUT、PIPE、CLI、GRD 是 `ready`，其餘都是 `draft`。還沒有任何規格達到 `implemented`，因為每份都還有 pending 的場景。
- **M1 尚未收尾（2026-11-22 盤點；2026-11-23 更新：CI 補強、README 已完成，其餘見收尾摘要）**：後端與驗收場景（上表）已完成，但 plan 第 24 節對 M1 的定義還有下列項目沒交付，容易被「場景全綠」掩蓋：
  - **Web UI 只完成一部分**（2026-11-23 UI-0 骨架、UI-1 媒體庫與新增抽屜；原本的描述：「尚未開始：沒有 `web/`、沒有靜態檔案服務、沒有 UI 的 `go:embed`」）；其餘頁面由 UI-2、UI-3 交付，`docs/ui/mockups/` 只有 `library.html`、`item.html` 兩份草稿。plan 要求 M1 一起交付的頁面中，登入、新增、媒體庫已完成，影音詳情、佇列、設定還沒做；UI 相關 NFR 的自動檢查（NFR-SEC-1 CSP、NFR-PERF-10 bundle、NFR-A11Y-1 axe、NFR-I18N-1）已在 UI-0 落地（[nfr.md 第 11 節](specs/nfr.md#11-實作位置web-ui)），NFR-A11Y-2～3、NFR-CMP-2、NFR-RWD-1 的人工檢查要等頁面完成。決定與預設見第 7 節；plan 第 24、26 節已記錄。
  - **真實樣本驗收沒做**：M1 驗收條件「四個平台共 10 部樣本都能一鍵產出 SRT」從未用真實的 yt-dlp、Groq、Supadata 驗證，驗收場景都跑在假工具上。第 6 節「還沒對真實服務驗證」的項目屬於同一類。
  - **人工驗收**：M1 有 7 個 `@manual` 場景還沒驗收。其中 5 個同時是 `@ui`（DEL-R3、LIB-R2、LIB-R5／R6、LIB-R9、LIB-R10），要等 Web UI；另外 2 個是 CLI-03、CLI-04（`watch` skill 改成薄包裝）。M2 還有 LIB-R12（`@ui @manual`）。
  - **CI 已補（2026-11-23 已在 master 實測，綠）**：`vuln` job（govulncheck v1.10.0，NFR-SEC-4）、`go` job 的涵蓋率門檻（`scripts/ci/coverage.sh`，NFR-TEST-3）、`release-check` job（`.goreleaser.yaml` 的 check、snapshot 建置與 linux/amd64 冒煙，NFR-CMP-1）、每晚 `fuzz.yml`（`scripts/ci/fuzz.sh`，涵蓋 subtitle、podcast RSS／Atom、yt-dlp JSON、ffmpeg silencedetect、cookie、zhconv、langtag 的解析入口，NFR-SEC-6）。本機只驗證了涵蓋率腳本、fuzz 腳本與 YAML 語法；govulncheck 與 goreleaser action 要看 master 上 CI 的實際結果，fuzz 要看第一次 nightly（或手動 `workflow_dispatch`）的結果。govulncheck 若報出可利用的弱點，要升級依賴或 Go 版本。
  - **M0 結論沒有回寫紀錄**：6 個 `@pending-m0` 場景（SRC-R4、SRC-R13、COR-R2／R3、TRN-R4、SEG-R3、SEG-R10）的門檻仍是暫定值，盤點時沒有看到 M0 實測結果寫回規格或 ADR。
  - **README 已更新**：CLI 子命令、M1／M2 進度與 CI 說明已與實際狀態對齊。

- **安全修補 C（2026-11-24，分支 `fix/client-security-hardening`，Client 端）**：唯讀安全審查的 Client 發現逐項修補，各附會失敗再通過的測試（含 mutation 自檢）；決定都標「待 owner 追認」。
  - vps 鎖定共用：新增 `NOTESTEME_TRUSTED_PROXIES`（逗號分隔 CIDR，與設定檔的 `trusted_proxies` 取聯集，SEC-R1 的例外）；`compose.yaml` 固定預設網路 `172.31.250.0/24`、caddy `172.31.250.10`，預設信任 `172.31.250.10/32`；`deploy-check vps` 多一項檢查（`trusted_proxies` 是空的時失敗），`serve` 在 vps 且為空時記 warn。驗收步驟 `deployed with the "vps" profile` 比照 compose 設定它（OPS O23）。
  - VAULT（V15）：git 環境加 `GIT_LITERAL_PATHSPECS=1`（標題的 `[`、`]` 不再讓 `add` 選到其他檔案）；`http.followRedirects=false`；askpass 只把 token 交給設定的 remote 主機（`NOTESTEME_GIT_HOST`）；hook 以 `{{title}}`／`{{commit}}` 開頭的元素展開後以 `-` 開頭時加一個前導空白（場景把 `{{title}}` 當獨立元素，所以不在載入時拒絕），`{{ title }}` 有空白時執行時也會展開；禁用環境變數改成 `vault.ForbiddenHookEnv` 單一來源並補上 OIDC／Hub 秘密與 `_DSN`、`_PASSWORD`、`_SECRET` 等結尾。
  - netguard（GRD 待決問題）：allowlist 只給起點本身在 allowlist 的請求使用（主機名稱列在 allowlist，或 allowlist 內的 IP 字面值；不重新解析，避免 DNS rebinding），其他起點的轉址改走不含 allowlist、獨立連線池的 transport；另外擋 `64:ff9b:1::/48`、`::ffff:0:0:0/96`、`100::/64` 與 metadata `168.63.129.16`。
  - AUTH（A27）：IPv6 鎖定以 `/64` 計；OIDC start 每個來源 10 分鐘最多 20 次（全域 1000 不變）；token 建立 token 時 scope 必須是子集。
  - OPS（O24）：名稱時間在未來的備份檔在保留時視為最舊、最先刪除。
  - 工具鏈：`go.mod` 加 `toolchain go1.26.11`、Dockerfile `GO_VERSION=1.26.11`、CI 的 govulncheck 改用 go.mod 的版本；`golang.org/x/text` v0.29.0 → v0.41.0。govulncheck v1.10.0：修補前（go1.26.8）15 個會被呼叫到的弱點（標準函式庫 13、x/text 2），修補後 0 個。`web/client/embed.go` 的說明文字不再像 embed 指令（SA9009）；worker 的空 critical section 是刻意的屏障（SA2001，加說明與 `lint:ignore`）。
  - 未處理：OIDC 全域上限仍可用 50 個以上不同來源（或 `/64`）佔滿（要依來源淘汰未完成的登入，需要改 DB）；compose 的固定網段可能與主機既有網路衝突（`.env.example` 有說明）；本機沒有跑 `docker build`。
- **收尾 D2：VAULT-R12、SRC-R11 第三層、SRC-R6「現在改用語音轉錄」、yt-dlp 排程更新（2026-11-24，分支 `feat/spec-remainder-vault-lang-ytdlp`，決定都標「依 /goal 採 advisor 建議，待 owner 追認」）**：
  - 數字：預設 tag 308 → 313 passed（新增 `Use ASR now skips the wait for auto captions`、OPS 的三個排程更新場景，`@wip` 的 `Code-switching speech is marked as mixed` 拿掉 `@wip`）；`-godog.tags='@M1,@M2,@M3,@M4,@M5'` 943 個全部 passed（936 + VAULT 兩個新場景 + 上面五個）。`passing.txt` 只新增 5 行。
  - VAULT-R12（V16）：刪除影音帶 `delete_outputs` 時，git vault 依 vault 分組：取發布鎖 → `pull --ff-only` → 在 clone 中只刪 hash 相同的檔案 → `ls-files --cached -z` + `rm --cached` → 一個 commit「notesteme: 刪除 <標題>」→ 領先時 push。`kept_outputs` 的 `git_vault` 不再出現，改成 `git_unreachable`／`git_rejected`／`git_auth`／`git_failed`；推送失敗的 commit 留在本機，由下一次發布推送（不 reset）。真 git + 本機 bare repo 的單元測試（`vault_delete_real_test.go`、`vault.TestGitCommitRemoval`），fakegit 加 `ls-files`、`rm`（steps.md 4.16 已登記）。
  - SRC-R11：`internal/langdetect.Detect`（文字系統份量，一個英文詞與一個漢字等重；日文、簡繁、拉丁與西里爾的功能詞；少於 8 份不判斷）。前兩層都沒有時寫入偵測的主要語言（`lang_source: detected`，重跑時同 `asr` 不當成輸入，快取鍵不變）；`mixed` 每次產生新的原始字幕時依文字判斷（`metaUpdate.mixed` → `SetItemLangMixed`）。`@wip` 場景以「宣告 `language: zh`」最小修改（SRC 待決問題，原文語言留空時預設 `caption_priority` 選不到 zh 的自動字幕）。`Given … is a mixed-language item` 仍是 seeding。
  - SRC-R6：`POST /api/items/{id}/use-asr-now`（`ingest`）：只接受 transcript `waiting` 且任務 `pending`，在交易中把 `not_before` 改成現在、payload 加 `skip_auto_wait`，步驟改回 `pending`；否則 409 `not_waiting`。`Stage.action_codes`（只有 `use_asr_now`），前端 `mapActions` 先依代碼、按鈕改成呼叫這個路由（三語 i18n `item.actions.asrNow`，bundle 283.3 KB／300 KB）。sqlc 新查詢 `SkipTaskWait`，沒有 migration。
  - OPS（O25）：排程工作 `tools.yt-dlp.update`，設定檔 `updates.yt_dlp.{enabled,at,timezone}`，預設開啟、`04:00` `UTC`；呼叫與 API 共用的 `ytdlp.Manager.Update`；固定版本略過、回退或安裝失敗以告警 `ytdlp_update_failed` 通知並在成功時解除、外部指定的路徑或 Windows 不動作、手動更新進行中延後 30 分鐘。harness 在設定檔沒寫 `enabled` 時關閉（`TestControls.NoYtDlpAutoUpdate`）。
  - 驗證：`go build`／`vet`／`gofmt` 乾淨；`go test -p 2 ./internal/... ./cmd/...` 全過；`scripts/ci/coverage.sh` 通過（pipeline 84.7%）；`go test -race -p 2 -timeout 100m` 只跑改過的套件（langdetect、pipeline、ops、vault、config、app、api、store、`features/internal/fakegit`、`features/steps`）全過；`./features`（驗收場景）沒有在 `-race` 下跑，只跑了一般模式的 313／943 個場景；前端 typecheck、lint、format、test（467）、build、bundle（283.6KB）、i18n 都過；新的保留原因 `git_*` 與告警 `ytdlp_update_failed` 已補三語文字。
  - 沒實測：真實的 git 遠端（GitHub／GitLab）、真實的 PyPI 更新、真實語音樣本的語言偵測；都只用假工具與本機 bare repo。§6 的「每天的排程更新還沒做」與「現在改用語音轉錄只顯示」兩條已由這個切片處理（已在 §6 劃掉）。


- **收尾 D1（2026-11-24，分支 `feat/task-retry-cancel-lib-grouping`，已推送 master）**：決定都標「依 /goal 採 advisor 建議，待 owner 追認」。
  - 不穩定測試 `TestMCPLoopbackRechecksPermissions`：根因是 MCP 請求的 context 已取消時，`context.AfterFunc` 在另一個 goroutine 非同步取消內部請求，內部請求可能先做完。`mcpLoopback.Get` 在開始前與結束後都檢查 base 的 `Err()`；新增同一請求跑 300 次的 `TestMCPLoopbackCancelledBaseAlwaysFails`，`-count=50` 通過。
  - PIPE：`POST /api/tasks/{id}/retry`／`cancel` 取代 501 stub（單位是任務所屬的 stage run，scope `admin`，回影音詳情；409 `task_not_retryable`／`task_not_cancellable`）；`GET /api/queue` 多 `failed` 清單；migration 00033。佇列頁加單一任務的「取消…」與「失敗與停住」清單（每列重試、全部重試），佇列頁改成 lazy。MCP／CLI 規格沒有對應功能，沒有新增。見 PIPE 待決問題。
  - NFR-REL-2：快取命中重用產出時與讀取字幕產出時比對大小與 sha256，不符或不見就標記 `artifacts.corrupt_at`（migration 00034）並從產生它的步驟重跑（只有第一個標記的呼叫端觸發；匯入、編輯、seeded 的產出只標記）；LLM 的 chunk 快取照常重用。API 回 409 `artifact_corrupt`。frames 圖片、筆記、摘要、章節不自動重跑（已知限制）。見 PIPE 待決問題。
  - LIB-R4：`GET /api/items` 加 `group_by=channel|published|status`、每列的 `channel_name` 與 `group`；分組時游標 v2（HMAC 涵蓋組鍵，指紋含 `group_by`）；沒有另外的 `sort`。媒體庫預設依頻道分組，可以切換（網址 `group`），媒體庫頁改成 lazy。翻頁期間組鍵可能改變（已知限制）。見 LIB 待決問題與 ui/README 10.22。
  - 驗證：預設 tag 308 個、`@M1,@M2,@M3,@M4,@M5` 936 個場景逐場景與開工前相同（全部 passed）；涵蓋率 subtitle 95.8%、pipeline 84.6%、queue 89.2%、netguard 93.6%；`-race` 只跑改過的套件 hubapi、pipeline、store、api、queue、app（store/sqlcdb 是產生碼、沒有測試）；前端初始 JS 271.6KB／300KB。

- **收尾 D3：SRC-R4 其餘品質檢查、SRC-R13 幻覺過濾、LLM 費用估算、`@pending-m0` 盤點（2026-11-24，分支 `feat/src-quality-checks-llm-estimate`，決定都標「依 /goal 採 advisor 建議，待 owner 追認」）**：
  - SRC-R4：`runFallback` 在 `language_mismatch` 之後依序檢查 `music_only`（>80% 的 cue 只有音樂標記）→ `low_coverage`（cue 聯集 <50% 片長）→ `repetition`（>30% 重複句，少於 5 句不判斷），不通過記為 `rejected` 並改用下一個來源（`internal/pipeline/quality.go`）。只套用在 fallback 來源（平台字幕軌不檢查）；涵蓋率的分母：Groq 用它回報的音訊長度，其他用影音片長，不知道就略過。門檻是常數、沒有開放設定；transcript 的 `impl_version` 沒有提升。Groq 也套用：已付費的轉錄被拒絕時步驟仍失敗，重跑會再付費（待 owner 決定是否排除 ASR）。見 SRC 待決問題「SRC-R4 其餘品質檢查與 SRC-R13 幻覺過濾」。
  - SRC-R13：`groq.Transcribe` 移除 `no_speech_prob > 0.6` 且 `compression_ratio > 2.4` 的 segment；`transcribeASR` 在接回時間軸之後把連續 3 次以上相同的句子只留一句（單檔與切塊都套用）。
  - LLM 估算：`POST /api/estimate` 的 `llm_usd` 用片長推估資料區塊（每秒 6 token），依 COST-R5 與 `cost.Service.Estimate`（含價格覆寫）估算各付費 LLM 步驟（`internal/pipeline/estimate_llm.go`）；不知道時 `null`，沒有付費 LLM 步驟時 `0`。reprobe 的 `estimate_usd` 改用 `EstimateRerun(from=transcript)`；有無法估算的任務**或沒有任何付費任務**時維持 `null`（場景與訊息「目前無法估算金額」因此不變）。`RerunEstimate` 多了 `Priced`。OpenAPI 與 `schema.d.ts` 只改了說明文字。見 ING 待決問題、SRC R10、COST C23。
  - 規格整理：SRC 第 91／125／130 行與 R10、BND N21、§6 的 SRC「還沒做」條目已改或劃掉。其他 grep 到的「還沒實作」屬真實未做（vault 的 secrets 憑證；`notesteme password set` 在 D3 之後的收尾補上，見 AUTH 待決問題 A28）或是條件式的歷史敘述，沒有動。
  - 數字：預設 tag 313 個、`@M1,@M2,@M3,@M4,@M5` 943 個場景全部 passed（沒有新增場景，`passing.txt` 沒動）；涵蓋率 subtitle 95.8%、pipeline 84.7%、queue 89.2%、netguard 93.6%；`go test -p 2 ./internal/... ./cmd/... ./features/...` 全過；`-race` 只跑改過的 pipeline、`source/groq`、api，全過；`check_steps`、`check_trace` 通過；沒有改前端程式（UI 只在 `estimate_usd` 為 null 時顯示「無法預估費用」，沒有顯示 probe 原因與 `/api/estimate`）。
  - 沒實測：真實的 Groq `verbose_json`（`no_speech_prob`、`compression_ratio` 欄位與數值）、真實的 Supadata 結果、真實語音樣本的門檻；只用假服務與 fixture。
  - `@pending-m0` 盤點（14 個場景、20 個 example；`-godog.tags='@pending-m0'`，標籤都沒有拿掉，由 owner 決定）：

    | 場景 | 規則是否已用預設值實作 | 結果（開工前 → 現在） | 擋住的原因 |
    |---|---|---|---|
    | COR `A rewrite that strays too far falls back to the original` | 是（C1：rune 的 normalized Levenshtein，門檻 0.6） | failed → failed，停在訊息步驟：實際「相似度 0.19」，場景預期 0.42 | 相似度算法要用 M0 樣本決定（COR 待決問題 C1）；只有門檻符合，數字取決於算法（2026-11-24 場景改為 0.19，已通過） |
    | FRM `Quiet stretches still get a frame every interval` | 是 | passed → passed | 只差 M0 實測門檻 |
    | FRM `Repeated frames are dropped but different solid colours are kept` | 是 | passed → passed | 同上 |
    | FRM `A cue that points at the screen lifts the nearby frame` | 是 | passed → passed | 同上 |
    | GEM `Videos that are not public are not sent to Gemini` | 是 | passed → passed | 需要真實 Gemini 確認 |
    | GEM `Videos longer than the limit are not sent to Gemini` | 是 | passed → passed | 同上 |
    | GEM `Gemini refusing the video asks for the regular pipeline` | 是 | passed → passed | 同上（拒絕的回應形狀） |
    | SEG `Break markers survive small punctuation changes by the model` | 是 | passed → passed | M0 樣本 |
    | SEG `Subtitle rules are enforced per script`（2 個 example） | 是 | passed → passed | M0 樣本 |
    | TRN `A wrong number of split markers falls back to cue-by-cue translation` | 是 | passed → passed | M0 樣本 |
    | OPS `A jump in yt-dlp failures is reported only with enough calls`（3 個 example） | 是 | passed → passed | 門檻要用真實的 yt-dlp 失敗率確認 |
    | OPS `The yt-dlp alert says what to try` | 是 | passed → passed | 同上 |
    | **SRC `Unusable results are rejected and the next source is used`**（4 個 example） | **是（本切片）** | `language_mismatch` passed，其餘 3 個 failed（缺 fixture，檢查也還沒做；`sources` 是 `[]`）→ **4 個都 passed** | 補了 `supadata-covers-200s.json`、`supadata-repeating.json`、`supadata-music-only.json`；門檻待 M0 |
    | **SRC `Whisper hallucinations are filtered out`** | **是（本切片）** | failed（缺 `local/quiet-ending.m4a`）→ **passed** | 補了 `groq-hallucinated-tail.json` 與 `quiet-ending.m4a`；門檻與真實 Groq 欄位待 M0 |

    結論：20 個 example 中 19 個在本機通過，只剩 COR 一個因相似度算法還沒定而失敗。全部都還需要 M0 實測或真實服務，才能決定是否拿掉 `@pending-m0`。


- **收尾 D4：`notesteme password set`、規格文字掃描（2026-11-24）**：
  - 掃描：grep 規格與 ADR 裡的「還沒實作」類文字並逐條對照程式。AUTH 的介面列了 `notesteme password set`，程式裡沒有（UI-3b 當時只補了設定頁的 API）——這是唯一符合「有規則或介面、本機可驗證、沒有記為延後」的缺口，已補上。`docs/ui/README.md` 的三處過期文字（`/api/auth/sessions` 尚未實作、事件「尚未會發出」、`password set` 還沒實作）已更正。其餘是真的沒做或沒有規格：vault 的 `vault:<id>` secrets 憑證（沒有規則）、`summary` 步驟（`plannedStages`，沒有規格）。
  - 實作：`auth.Service.SetPassword`（不需要目前的密碼、撤銷所有 session）、`app.SetPassword`（和 `secrets` 一樣取資料目錄的鎖，serve 執行中時失敗，不需要 master key）、`internal/cli/password.go`（`password set --password-env <變數> | --password-stdin`，或終端機提示輸入兩次；不接受命令列參數、不能和 `--server` 並用、密碼不寫進任何輸出）。
  - 驗證：三層單元測試（auth、app、cli，含 serve 執行中被拒、stdin 的 CRLF、提示輸入不一致、資料目錄不存在）；新場景 AUTH `The password can be reset from the command line`（預設 tag 313 → 314，`passing.txt` 加一行）。
  - 決定（AUTH 待決問題 A28，待追認）：直接改資料目錄、不需要舊密碼、撤銷全部 session、新密碼只接受環境變數／stdin／終端機。
  - 介面對照（除了「還沒實作」文字，另用腳本把規格列出的介面對照程式）：規格裡的 API 路由（對 OpenAPI 與 `mux.Handle` 註冊）、CLI 子命令（對 CLI 分派）、MCP／HMCP／AGT 的 tool 名稱（55 個）與 `NOTESTEME_*` 環境變數（29 個）。除了 `password set`，沒有「規格有列、程式沒有」而且沒有被明確延後的項目。找到的差異都是規格自己記錄的：`notesteme costs`（COST C6 延後）、`GET/POST /api/views`（LIB、SRCH 明寫不在範圍）、`NOTESTEME_PROVIDERS_GROQ_API_KEY`（SRC 記錄的命名衝突，暫用 `NOTESTEME_GROQ_API_KEY`）；其餘是掃描的誤報（截斷的路徑、手寫路由、上游 provider 的 API）。

- **收尾 D5：SRC-R4 付費結果的例外（2026-11-24）**：D3 把「已付費的 ASR 被 R4 拒絕」留成待決；依 advisor 的裁決（照 /goal 由我決定、待追認）實作窄版：只在 `runFallback` 所有來源用完那一處，採用第一個因 `low_coverage`／`repetition` 被拒的付費結果，probe 換成 `used`（保留原因）、步驟訊息附提醒。測試：`internal/pipeline/quality_paid_test.go`（採用 `low_coverage` 與 `repetition`；`music_only`、免費來源、語言不符照舊失敗；後面有來源通過就用後面的；後面 `needs_action` 仍 blocked；兩個付費都被拒採用第一個；並做過兩個變異檢查）；新增預設 tag 場景 `A paid result that fails the coverage check is still used when nothing else is left`（預設 tag 314 → 315，總共 945）。

## 2. 下一步

### 2026-11-24 現況

- **已完成並推送（master）**：M1、M2（後端與場景）、M3（SPB、CHAP、FRM、NOTE、VAULT、GEM）、M4（NOTIFY、SUB、COST、BATCH、DIG、SRC-10、SRCH、GLOS、BND、AUTH、OPS、ENTRY、LIB-08/09）、M5（HACC、HREP、HMCP、HSHR、Postgres＋S3 backend、`deploy/docker/compose.hub.yaml`）、UI-0～UI-8、Hub Web UI（`web/hub`）。三份安全修補（Hub、bundle／分享、Client）已合併。
- **收尾切片（已完成並推送）**：盤點出規格有寫、沒有場景守住的項目，由五個切片補完——D1：`POST /api/tasks/{id}/retry`／`cancel`（PIPE）、NFR-REL-2 讀取時比對 sha256、LIB-R4 `group_by`、修好不穩定測試 `TestMCPLoopbackRechecksPermissions`；D2：VAULT-R12 的 git vault 刪除、SRC-R11 文字語言偵測與 `mixed`（`@wip` 場景已拿掉 `@wip`）、SRC-R6「現在改用語音轉錄」動作、yt-dlp 每日排程更新（OPS-R6／R7）；D3：SRC-R4 的 `low_coverage`／`repetition`／`music_only`、SRC-R13 的 Whisper 幻覺過濾、ING-08 的 `llm_usd` 與 reprobe 的 `estimate_usd`（SRC-10 R10）、規格裡過期的「還沒做」文字、`@pending-m0` 的盤點（14 個場景）；D4：`notesteme password set`（AUTH 介面列了卻一直沒實作）與規格文字掃描；D5：SRC-R4 對已付費結果的例外（付費 ASR 被拒時仍採用）。場景數現在是預設 tag（`@M1,@M2`）315＋`@M3,@M4,@M5` 630 = 945，全部 passed。摘要見第 1 節末尾的「收尾 D1／D2／D3／D4／D5」。
- **owner 要做的事（我做不到）**：
  1. **GitHub Actions**：repo 公開之後（2026-11-24）CI 才真的跑起來，第一次完整執行找到兩個問題，都已修正：
     - `go test -race ./...` 在 GitHub 的 runner 上超過每個測試程式 30 分鐘的上限，`features` 被中止（05:15 的那次還在 27.5 分鐘通過，之後測試變多）。CI 改成平行的四個 job：`go`（其餘套件，`-race`，逾時 60 分鐘）、`bdd`（`./features`，`-race`，60 分鐘）、`bdd-extra`（`@M3,@M4,@M5`，不加 `-race`，45 分鐘；在 runner 上約 3 分鐘）、`coverage`（涵蓋率門檻）。
     - `hub-backends` 從來沒通過：MinIO 的 image 已經拉不到，連 `compose.hub.yaml` 都啟動不了。改用 SeaweedFS（第 8 項、ADR-0030 修訂）；這個 job 第一次在真的 Postgres 16 與真的 S3 相容服務上跑完 Hub 的共用案例（136 個通過、0 個略過），並新增「沒簽章的 S3 請求必須回 403」的反證。
     - 新增 `docker` job（第 3 項）。
     - 結果：master 連續四次完整 CI 全部 9 個 job 成功——`ec666176`（CI 重構）、`a147a379`（含收尾 D3）、`da58184b`（含收尾 D4）、`58c7613b`（含收尾 D5，最後一個程式碼提交）。runner 上的實測耗時：`go`（其餘套件，`-race`）23～35 分鐘、`bdd`（`./features`，`-race`）22～27 分鐘、`bdd-extra` 約 3 分鐘、`docker` 約 3 分鐘，其餘 job 不到 3 分鐘；最慢的離 60 分鐘上限還有 25 分鐘以上的餘裕，但不同次執行的差異可到 8 分鐘。之後的提交以 `gh run list --branch master` 為準。
  2. **真實驗收**：yt-dlp（YouTube、Bilibili）、Groq、Supadata、Gemini、ntfy、真實 IdP、真實反向代理、Hub 的真實 OAuth、R2／AWS S3、真實 ffmpeg 輸出（bundle 的 JPEG 結構檢查只用 Go encoder 與手工樣本）、真實 libass 預覽、Jellyfin／Plex sidecar；`@manual`、`@ui @manual` 的人工驗收；M0 量測與 `@pending-m0`。
  3. **Docker**：CI 的 `docker` job 實際建置 Client 與 Hub 的 image（先前本機沒有 docker，這是第一次實測）。Client image 通過 `deploy/docker/smoke-test.sh`（工具齊全含 ffmpeg libass、非 root、缺 master key 時拒絕啟動、登入、SIGTERM 正常關閉、重啟後資料保存）；Hub 用 `compose.hub.yaml` 啟動整組（Postgres 16、SeaweedFS、`hub-s3-init` 建 bucket、`hub-migrate`、`hub`），`/readyz` 回 ok、`hub doctor` 通過、匿名的 S3 請求被拒絕（403）。**仍沒驗證**：Hub image 沒有發布到 ghcr；arm64 的多架構建置（只在發布 tag 時由 `docker-publish.yml` 建置）；Client 的 `compose.yaml`（`wsl`／`vps` profile 與 Caddy）沒有在 CI 啟動；真實反向代理後面的行為。
  4. **追認決定**：各規格「待決問題」中標「（2026-11-23／24，依 /goal 採 advisor 建議，待 owner 追認）」的項目（很長的清單，依規格 grep 這句話即可列出）。其中修改了既有 `.feature` 的：VAULT 5 個場景（V12，rerun 帶明確 profile）、COST C22、SRC-10 R16、NOTIFY N9、SUB-R4 的 regex 範例、HSHR S17（R13 檔名）與 S20（`hub-precheck-v1` → `v2`，12 處）、SRC-R11 的 `@wip` 場景（`SRC-transcript-sources.feature:214`：第一步從 `titled` 改成 `with:` 並加上 `language zh`，拿掉 `@wip`）。破壞性變更：Hub 需要 `NOTESTEME_HUB_MASTER_KEY`（≥ 32 字元）與 `NOTESTEME_HUB_SESSION_KEY`；Hub 的 cookie 改成 `__Host-` 前綴（部署後每個人要重新登入）。
  5. 各規格 `status` 仍不是 `implemented`（owner 的步驟）。
  6. **營運上要先知道的行為改變（收尾 D1／D2）**：
     - **yt-dlp 排程更新預設開啟**（OPS O25）：每天 04:00 UTC 執行 `tools.yt-dlp.update`，正式環境會真的連 PyPI；更新是同步執行，期間暫停領取任務；失敗會自我檢查、回退並發告警 `ytdlp_update_failed`。不想要就把設定檔的 `updates.yt_dlp.enabled` 設成 `false`；固定版本時略過。
     - 媒體庫預設依頻道分組（LIB-R4）；依狀態或頻道分組翻頁期間組鍵可能改變，同一部影音可能重複出現或漏掉（依 LIB-R9 不自動重新讀取）。
     - 產出損毀（NFR-REL-2）：重跑依影音**目前**的處理方案；匯入、編輯或從 bundle 播種的產出只標記不自動重跑；畫面、筆記、摘要、章節的產出讀到損毀只回報錯誤。
     - 單一任務的重試／取消以任務所屬的整個步驟為單位，scope 是 `admin`。
     - OPS 的「固定版本略過」場景偏弱：排程完全沒執行時它也會通過（同一組的第一個場景才證明排程真的有跑）。
  7. **CI 逾時餘裕**：已處理（第 1 項）。拆成平行的 job 之後每個測試程式的上限是 60 分鐘（實測最慢約 35 分鐘）；場景再增加時 `bdd`（`-race` 下的 `./features`）是第一個會碰到上限的，可以再依 feature 檔分片。
  8. **S3 相容服務改用 SeaweedFS（ADR-0030 修訂，待追認）**：compose 的 `hub-minio`、`hub-minio-init` 改為 `hub-s3`（`chrislusf/seaweedfs:4.55`，單容器 `weed server -s3`）與 `hub-s3-init`（`curlimages/curl:8.18.0` 以 SigV4 建 bucket）；volume 改名 `hub-s3-data`，沒有提供 MinIO 資料的遷移（沒有已知的部署）。代價：SeaweedFS 在 `hub-internal` network 上還開了沒有認證的內部服務（filer 8888、master 9333、Iceberg 8181 等），Hub 容器被攻破時可繞過 S3 簽章；network 是 internal，只有 Hub 與 migrate 連得到。也可以改用 R2 或 AWS S3（`docs/hub-deployment.md` §3）。
  9. **收尾 D3 的決定（待追認）**：
     - **已決定（收尾 D5，待追認）**：已付費的 Groq／Supadata 結果被 SRC-R4 的 `low_coverage` 或 `repetition` 拒絕、而且沒有其他來源可用時，改為**採用它並附提醒**，不再失敗（`music_only`、`language_mismatch`、免費來源照舊；後面來源 `needs_action` 仍 blocked；多個付費來源被拒時採用順序中第一個）。見 SRC 待決問題「付費結果的例外」。
     - SRC-R4：品質檢查只套用在 `fallback` 來源（Supadata、Groq），不套用平台字幕軌；Groq 的涵蓋率用它回報的音訊長度；順序固定為語言→音樂→涵蓋率→重複；少於 5 句不判斷重複；音樂標記用固定清單；門檻是常數、沒有開放設定；沒有提升 transcript 的 `impl_version`（已完成的步驟不重跑）。
     - SRC-R13：`no_speech_prob > 0.6` 且 `compression_ratio > 2.4`（都嚴格大於、同時成立）的 segment 移除；連續 3 次以上相同的句子只留一句（含切塊接縫）。
     - LLM 估算：每秒 6 個 token（刻意偏高）；ING 估價的 `total_usd` 在「只有 ASR 費用未知、LLM 確定是 0」時是 `0`（頂層仍帶「部分金額未知」，符合 ING-08「只加總已知的金額」）；reprobe 的 `estimate_usd` 在沒有任何付費 LLM 步驟時仍是 `null`。
     - `@pending-m0` 的 14 個場景（20 個 example）在本機全部通過（`go test ./features -run TestFeatures -godog.tags='@pending-m0'`：20 passed、195 steps passed），標籤一個都沒拿掉。原本唯一沒通過的是 COR 的相似度場景（算出 0.19，場景預期 0.42）；**已決定（2026-11-24，依 /goal 採 advisor 建議，待追認）**：場景訊息改為 0.19，對齊 COR C1 目前的算法（0.42 是 M0 之前的占位值，沒有任何算法算得出）；標籤保留，M0 改算法或門檻時連同場景一起改。
- **已知缺口（沒有規格規則或需要真實樣本，沒做）**：
  - UI 文件列的 API 缺口（`docs/ui/README.md` 10.7 第 8 點、10.13～10.21）：`Stage.actions` 的動作代碼、佇列優先順序與 Agent 任務、「需要你處理」彙總 API、`/api/items/continue` 沒有 `dry_run`、總覽 `today` 事件沒有步驟／狀態／影音代號、儲存的檢視（saved views）、provider 列表 API、字幕變體列表 API。
  - Hub Web UI 缺的 API：`my_vote`、unhide（HREP R8／R10 規格就沒有）、群組列表、`hub_origin.uploader`；OAuth callback 的錯誤仍回 JSON。
  - Supadata 非同步 job（202）還不會輪詢；FRM 的 `api` executor 有 shortfall 候選時字幕留空（待決問題）。
  - 安全修補沒處理的：OIDC 全域 1000 筆上限仍可用大量不同的 IPv6 `/64` 佔滿；Hub 的 `buildShare` 可能透過 `parents_sha256` 洩漏 `known` 模型、`/mcp` 的被停權帳號沒有限流、重新分享仍可繞過 hidden 版本、影片 metadata 仍由第一個分享者決定。
- **維護提醒**：5 小時 API 用量上限會讓 child 失敗（訊息含重置時間），重置後用 `t3_thread_send` 叫它繼續；機器 16GB（WSL），同時超過 3 個重度任務會被 OOM，全套測試用 `-p 2`、`-race` 的逾時要設 100 分鐘以上；別在主 checkout 做 `git checkout --detach`；child 還在跑時別動它的 worktree。

### 歷史（2026-11-22／23，M1／M2 時期；已完成或已被上面取代，保留供對照）


切片進度：PIPE → CLI 與 `/data` 鎖 → GRD → SRC-A → SRC-B → OPS → LIB → DEL → AUTH-SEC → LOCAL → PODCAST → ESTIMATE → M2 S0 骨架 → M2 S1 PRF＋S2 PRM → 規格修改 → M2 S6a TRN OpenCC（PR #1～#19，全部已合併）→ M2 S7 OUT 渲染與匯入（直接推送到 master）。下一步（S3 完成後）是 S8 OUT sidecar／folder 輸出、PRM compare／playground、DEL-R9 的 `delete_outputs`，再到並行的 S4 COR、S5 SEG、S6b TRN LLM（有 `edited` 時以它為輸入，見 TRN 規格）、S8 OUT publish；Web UI 已開始（2026-11-23 UI-0 骨架、UI-1 媒體庫與新增抽屜，接著 UI-2、UI-3）。**工作方式（owner 的 /goal，2026-11-22）**：MVP 階段不再開 PR，每個切片在 `.worktrees/<slug>` 的分支完成後 rebase 到 origin/master 直接 `git push origin HEAD:master`；需要決策時問 advisor，決定記在規格的待決問題並標「待 owner 追認」；推送後確認 master 的 CI 是綠的。

接下來：

1. 疊加 PR 的合併方式（已用在 #13～#17）：前一個 squash 合併後，把下一個的 base 改成 `master`，再把下層分支的修正 merge（不 rebase）進來，最後用 `-s ours` 併入 master，並確認 `git diff master...HEAD` 的 patch-id 與合併前相同。每個 PR 都要等 Codex 審查通過、CI 綠才 squash 合併（owner 已授權，不用另外詢問）；同一個 PR 最多連續重審 3 次，超過要交 owner 決定（#14 就因為這樣由 owner 再授權了幾輪）。
2. 剩下 M1 的項目（2026-11-23）：NRM-R7 與 CI 補強已完成；剩 Web UI、真實樣本驗收、人工驗收，見第 1 節開頭的「2026-11-23 收尾摘要」。
3. M2 的切片順序（S0 之後）：並行 S1 PRF（含 ING-R7）、S2 PRM、S6a TRN 確定性（OpenCC）、S7 OUT 渲染 → S3 LLM 核心（provider client、限流、執行層）→ 並行 S4 COR、S5 SEG、S6b TRN LLM、S8 OUT publish → S9 MCP → S10 AGT → S11 收尾。Web UI 切片不在這個順序內（2026-11-23：M2 後端完成後開始，UI-0 骨架已完成）。S0 之後 `@M2` 是 117 個場景：115 pending、2 failed，兩個失敗都需要 owner 決定（MCP stdio 場景與 Background 衝突、OUT-R9 預覽的影音沒有字幕來源）。規格與 `.feature` 修改後（第 2 節第 4 點）是 17 passed、26 failed、74 pending：Background 的處理方案、prompt 步驟可以執行了，場景會停在後面的步驟；S6a 之後（2026-11-22 實測）是 20 passed、23 failed、74 pending。下列 failed 都是之後的切片才會做的功能，沒有需要 owner 決定的：
   - 缺 LLM fixture（S5、S6b 要補）：SEG 7 個、TRN 8 個、localize 1 個；缺 media fixture `speech-with-pause-at-6.2s.m4a`（S5）。
   - 行為還沒做：COR glossary 傳給 ASR（S4）、SEG 手動字幕跳過 segment（S5，目前 `api` executor 回「還沒有實作」）、TRN 沿用手動字幕軌 1 個（S6a 之後仍失敗，OpenCC 的 3 個範例已通過）、OUT sidecar 對線上影音是 `skipped`（S8）。
4. **owner 決議（2026-11-22）：下列建議全部同意，其中規格與 `.feature` 的修改已在 `📝 docs(specs)` commit 完成，各規格對應的待決問題已關閉。** 決議內容：
   - **LLM 核心（擋 S3）**：
     - X1：沒有 provider 就 `skipped`（`not_configured`），維持 S0 的做法。
     - L1：LLM-R8 場景的 `received 4 requests` 改成 5。
     - L2：token 估算採 CJK 一字 1、其他每 4 字元 1（進位）、每句加 1、重疊句不計入。
     - L6：API key 環境變數 `NOTESTEME_ANTHROPIC_API_KEY`、`NOTESTEME_OPENAI_API_KEY`，其他 provider 用 `NOTESTEME_<ID>_API_KEY`。
     - wire 契約：同意，S3 寫成 ADR，fakellm 一起改。
     - P6：不帶版本號的 prompt 引用跟隨預設（`<id>` 等同 `<id>@default`）。
   - **S1/S2 設計決策**：
     - 同意：D6、D8、D13、X5、D10～D12、D14、D16、F4。
     - P7、P8 先接受。P7 已在 M2 收尾完成（2026-11-23，待 owner 追認）：刪除 prompt 的檢查掃描 `items.overrides_json`（含垃圾桶中的影音），並與刪除放在同一個寫入交易；寫入端驗證 prompt 存在時在自己的交易之外，仍有「驗證後才被刪除」的窗口（見 PRM 規格的 P7）。
   - **要改 `.feature` 或規格（已同意，已修改）**：
     - F1、F2：PRF-R2「Each layer」與 PRF-R3 加 `And the default profile is "standard"`。
     - F5：PRF-R8 與 PRF-R2 的 ingest 之後加 `And the worker runs until idle`。
     - P1：PRM-R7 compare 的 body 加 `"provider": "anthropic", "model": "fixture-model"`。
     - G1（PRF 版）：PRF-R1 補上 `glossary`（字串陣列），它與 COR `/api/glossaries` 的關係由 COR 切片決定。
     - O-4：PRF 開頭改成「處理方案在 M2（PRF 切片）實作；ING-R7 隨 PRF 切片通過」，並關閉 ING-R7 的待決問題。
     - MCP-R1：stdio 場景搬到 CLI 的 feature（`CLI-cli.feature` 的 `@M1` 改標在各個 M1 場景上，這個場景標 `@M2`）。
     - OUT-R9：`steps.md` 4.4 規則 (1) 改成只有 seed `source` 才算「場景已準備來源」，其餘走 fixture；改完 `@M2` 只有預覽場景由 failed 變 pending，沒有其他場景改變。
     - SEG-R10：補上 `the LLM provider "openai" answers "segment" with "segment-keep-all"`；fixture `segment-keep-all`（不合併也不切分、每句原樣輸出）要在 S5 建立。
     - COR-R4：影片標題改成 `服务拆分实践`；fixture `yt-zh001xxxxxx` 不含「微服务」、「数据库」，不用改。
   - **不用現在決定**：M0 量測的各項（`max_tokens`、重疊句數、`when_needed` 門檻、相似度算法、`⟨|⟩`）、OUT sidecar 檔名（先實測 BCP-47 與 ISO 639）、PIPE 的退避與 aging、translate 多語言（TRN 切片）。
   - **規格裡找不到原文，尚未處理**：X7、L7、A1、O1～O4、SEG-R2～R4 矛盾（handoff 的 G1）。AGT 的待決問題寫「無」。要處理時先讀相關 `.feature` 再問使用者。
   - **還沒回覆**：M2 路由的 scope（profiles 與 channel overrides 是否改 admin、ass-styles preview 是否改 ingest）。

   原本的待決清單（保留供對照）：X1 LLM 步驟預設值（S0 依建議 C：沒有 provider 就 skipped）、X7 兩個 Agent 同時領任務沒有場景、L1 LLM-R8 請求數 4 應為 5、L7 cost_ledger、G1 SEG-R2～R4 矛盾、P1 PRM-R7 body、P6 不帶版本號的 prompt 引用跟隨預設、F1 PRF-R2 Background、F2 PRF-R3 斷言驗證不到、F5 頻道層要等 metadata（場景加 `the worker runs until idle`）、O-4 PRF 的 M1 敘述、S1/S2 的 D1～D16 待確認（特別是 D6 頻道層在 metadata 才併入、D8 完整性只檢查處理方案、D13 硬刪除與 used_by 順序）、A1 AGT 並行上限、MCP stdio 場景搬到 CLI、O1～O4 OUT、COR-R4 斷言偏弱、SEG-R10 缺 answers、M2 路由的 scope（profiles 與 channel overrides 是否改 admin、ass-styles preview 是否改 ingest）。
5. 每個切片完成後執行 `go test ./features -run TestFeatures -update-passing` 更新棘輪清單，並檢查新增的通過場景。棘輪涵蓋預設 tag（`@M1` 與 `@M2`，2026-11-23 起；M2 全部通過後納入）。
6. `features/fixtures/items/` 有 `yt-pg100xxxxxx.json`、`yt-abc123xxxxx.json`（後者只在影片沒有任何字幕軌時用到，目前只有 SEC 的場景用得到）。
7. 大切片的做法（LOCAL、PODCAST 用過）：architect 先出設計，主代理把設計凍結成契約檔（Go 介面、argv、marker 語法、資料模型），再分給檔案所有權不重疊的 builder 並行實作；harness 的工作包可以從一開始就並行。整合後主代理跑完整驗收與變異測試，另開 builder 只看 diff 與規格做審查。

## 3. 怎麼執行

```
go build ./... && go vet ./... && gofmt -l .
go test -count=1 -race -timeout 7m ./...                    # 單元測試＋驗收場景＋規格一致性檢查
go test ./features -run TestFeatures -v                     # 只跑驗收場景（預設 tag）
go test ./features -run TestFeatures -godog.tags="@PIPE-R3" # 指定 tag；排除條件會自動併入
go generate ./internal/store/                               # 重新產生 sqlc 查詢
go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 diff -f internal/store/sqlc.yaml   # 檢查生成的程式碼是最新的
go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 vet  -f internal/store/sqlc.yaml

# 每次回報前都要跑的規格檢查（docs/specs/README.md 5.3）
python3 -I scripts/spec/check_steps.py .
python3 -I scripts/spec/check_trace.py .
uv run --with gherkin-official python -I scripts/spec/parse_all.py features
```

- 帶 `-godog.*` 旗標時寫 `./features`，不要寫 `./features/...`，因為子 package 沒有定義這些旗標。
- Go 版的規格檢查在 `features/speccheck_test.go`，和 Python 版的結果一致：1491 個步驟、167 個句型、216 條規則、108 則故事、275 個場景。另外會檢查 `steps.md` 的每個句型都已註冊。

## 4. 工作規則

這些規則來自使用者，不能違反：

- **規格優先**：行為和規格不一致時，不可以為了讓測試通過去改 `.feature`。先寫進該規格的「待決問題」並問使用者；同意後，規格與場景放在同一個 commit 修改。
- **只用 `steps.md` 的句型**：需要新句型時，先加進 `steps.md`，並在回報中列出。
- **紅燈要紅得正確**：寫完 step definition 先跑一次，確認失敗原因是「功能還沒做」。目前多數失敗會被「501 → pending」轉成 pending，要看到真正的紅燈時，可以暫時關掉這個判斷。
- **`docker` 指令**：`When the owner runs "docker compose -f … config --format json"` 由 `features/internal/fakecompose` 離線模擬（讀真正的 `deploy/docker/compose.yaml`），其他 docker 指令與不是 `notesteme` 的指令一律失敗，不是 pending。
- **pending 判斷的 helper**：`previousRequestNotImplemented()`（`features/steps/scenario.go`）只在前一個步驟的請求回 501 `not_implemented` 時才轉 pending，用在 `stageIs`、`no task is running` 與垃圾桶步驟。
- **godog 的 tag OR 用逗號**：`-godog.tags='@A,@B'`。`'@A || @B'` 會選到 0 個場景卻顯示通過。
- **斷言要能失敗**：PIPE 切片的審查用變異測試發現 5 個場景拿掉對應行為後仍然通過。每個切片的審查都要挑幾個場景做變異測試：在暫存 worktree 拿掉對應的 production 行為，場景必須變紅。
- **測試不連外網**（NFR-TEST-1）；時間一律用注入的 fake clock（NFR-TEST-2）。
- **不要讓已實作的步驟依參數回傳 pending 來避開失敗**。場景的步驟都已實作、行為還沒做時：
  - 小的行為就直接實作。
  - 大的就讓真正還沒實作的那個步驟維持 pending。
  - 目前允許的 pending 只有下列幾種：
    - 斷言遇到 production 回應的 `501 not_implemented`，或 CLI 回報 `not_implemented`（`the command exits with` 遇到尚未實作的子命令或 API 的 501），而且這個回應或結果必須是前一個步驟送出、執行的。
    - 腳本化來源的步驟查 production 的 `source.Implemented`，得知來源還沒實作。
    - `processed up to` 的影音沒有字幕來源，也沒有對應的影音 fixture（`itemFixtureFor` 的規則 3，見 steps.md 4.4）。
    - 步驟本身還沒實作。
- **通過場景的棘輪**（`features/passing.txt`）：
  - 「501 → pending」這類規則可能讓已通過的場景退步成 pending，整套測試卻仍然是綠的（審查用變異測試證明：讓 `GET /api/items` 回 501，CLI 場景 5 從 passed 變成 pending）。
  - `features/passing.txt` 一行一個通過的場景，格式 `<feature 檔名>: <場景名稱>`；Scenario Outline 的每個 example 加上 ` [example N]`（整份 Examples 的第幾列）。
  - `TestFeatures` 跑完後讀取每個場景的結果（自訂的 godog formatter，見 `features/ratchet_test.go`）：清單中的場景不是 passed（pending、failed、undefined、沒有被執行）就讓測試失敗並列出場景；通過但不在清單中的場景只提示。只有預設 tag 才要求清單中的場景都被執行；用 `-godog.tags` 指定其他 tag 時只檢查有執行到的。
  - **每個切片完成後要更新 `features/passing.txt`**：`go test ./features -run TestFeatures -update-passing`，檢視 diff 後一起 commit。清單中的場景不得退步；改場景名稱或刪除場景時，清單也要同步修改。
- **指令被權限限制擋下時不要繞過**（例如把指令寫進腳本再執行）；停下來回報給使用者。
- **subagent 分工**：
  - `architect`（opus，唯讀）：設計。
  - `builder`（sonnet）：實作；每次只給一個範圍明確的工作，並行時檔案所有權不重疊，並使用 `isolation: "worktree"`。
  - `scout`（haiku，唯讀）：查詢與摘要。
  - 每個切片綠燈後，另開一個 `builder` 只做審查：只給 diff 與規格，不給結論，只回報不修改。
  - 最後的驗收指令一定要親自跑，subagent 說通過不算證據。
- **Commit**：沿用 repo 歷史的格式，例如 `✨ feat(auth): …`、`🧪 test(features): …`、`📝 docs(specs): …`。每個切片綠燈後各自 commit。
- **Git**：
  - 現在已有 remote：開 `<repo>/.worktrees/<slug>/` 的 worktree 與分支，推送並建立 PR。
  - 沒有使用者明確授權不合併。合併用 squash，subject 保留 PR 編號。
  - 不直接 push `master`。
- **回報內容**：
  - 通過的場景（數量、tag、`go test` 摘要）。
  - 仍 pending 或失敗的場景與原因。
  - 新增或修改的步驟、ADR、待決問題。
  - commit 清單。
  - 需要使用者決定的事，附上建議。

## 5. 架構重點

驗收測試的完整設計見 [ADR-0015](adr/0015-acceptance-test-harness.md)。

- **入口**：`cmd/notesteme/main.go` 是唯一讀 `os.Environ` 與 `time.Now` 的地方，把它們轉成 `app.Env` 後交給 `internal/app`。`app.Env` 包含環境變數 map、時鐘、`netguard.Deps`、外部工具 runner、listener、log，以及只給測試用的 `Test *TestControls`（手動 worker、低成本雜湊）。
- **測試中的執行方式**：
  - server 與 CLI 都在測試程序內執行。
  - `When the server restarts` 用 `Server.Kill()` 模擬，不釋放租約。
  - `When the server shuts down gracefully` 走正常關閉。測試中寬限期為 0（`TestControls.SkipShutdownGrace`）；30 秒的行為由 worker、app 的單元測試用 fake clock 驗證。
  - worker 是手動模式：
    - `RunRound` 領取一輪任務、標記為 running，並在 goroutine 中開始執行，不等完成就回傳。要斷言任務停在 running 時，用假工具的 gate 擋住，不要依賴時序。
    - `RunUntilIdle` 執行到沒有任務為止。
- **測試替身**（`features/internal/`）：
  - `harness.World`：每個場景一份，包含暫存根目錄、env map、fake clock、fakenet、假工具狀態。
  - `fakenet`：可指定的 DNS 解析加程序內的 fake origin；也會攔截 `http.DefaultTransport` 等繞過 netguard 的連線。
  - `faketool`：測試 binary 以 `yt-dlp`、`ffmpeg`、`ffprobe` 的名稱被執行時，改扮假工具，並把每次呼叫記在 `calls.jsonl`。
  - `fakesource`：腳本化的 ASR 與 Supadata（掛在 `api.supadata.ai`，只接受 `x-api-key`）。
  - gate：`World.ArmGate`／`OpenGate`。被擋住的假 yt-dlp 各寫一個 `.waiting.<pid>`，並寫出 `/data/tmp/partial-download.part`。計數時只算仍存活的程序。
- **步驟的約定**：
  - 送出請求的步驟會在 server 尚未啟動時隱含啟動它。
  - 例外：場景明確讓 server 正常關閉之後，讀取狀態的 `Then` 步驟改為唯讀查詢 SQLite（`harness/db.go`），不啟動新 server。新 server 啟動時的租約回收會蓋掉要驗證的結果。
  - 「任務正在執行」要同時符合 DB 中 leased 的任務數與存活的假工具程序數。
  - `the next task belongs to` 實際執行一輪領取（arm gate 後 `RunRound`），檢查被領取的任務屬於哪部影音。
  - 還沒實作的 API 回 `501 not_implemented`，斷言步驟遇到時轉成 pending。例外：預期 401，或預期 403 且請求缺憑證時，不轉。
  - After hook 發現的違規（未攔截的連線、secret 外洩）記在收集器中，`TestFeatures` 結束後讓測試失敗。
  - 回應 JSON 是子集比對；陣列依序比對前 N 個元素。
- **程式地圖**（`internal/`）：

  | 套件 | 內容 |
  |---|---|
  | `app` | server 啟動、啟動防護、flock、手動 worker、測試用的 seeding |
  | `config` | 設定檔與環境變數（含 `asr`、`trusted_proxies`） |
  | `store` | SQLite（單一寫入連線、WAL）、goose migration（`migrations/`，前置快照、拒絕降級，`Options.Migrations` 可替換）、`VACUUM INTO` 快照、sqlc 查詢（`queries/` → `sqlcdb/`） |
  | `auth` | argon2id 密碼、session、CSRF（double-submit）、依來源位址的登入鎖定 |
  | `secrets` | master key 包裝資料金鑰（XChaCha20-Poly1305） |
  | `api` | Huma 路由、驗證與 CSRF middleware、items、cues、subtitles、resolve、settings |
  | `queue`、`worker` | 租約佇列、退避（30 秒、2 分鐘）、依類別的上限與暫停（`queue/control.go`，存在 settings 表）、worker 的 Drain 與 Cancel |
  | `events` | 記憶體中的事件匯流排；store 在同一個交易中寫入 `events` 表，commit 之後發布，`GET /api/events` 以 SSE 推送；`app/housekeeping.go` 每天清理 90 天前的事件 |
  | `pipeline` | metadata → transcript → normalize、重跑、快取鍵、網址辨識、取消、保留最近 3 版、可覆寫的 `impl_version` |
  | `transcript` | 依 `caption_priority` 選字幕軌 |
  | `source` | `Implemented` 宣告已實作的來源（目前 youtube-manual、youtube-auto、supadata） |
  | `source/youtube` | yt-dlp 的 Probe 與字幕下載 |
  | `source/supadata` | Supadata 的 transcript API（不跟隨轉址、依官方錯誤碼分類） |
  | `toolexec` | 子程序（process group、輸出上限、環境變數 allowlist）；`Locate` 依 OPS-R8 的順序找工具並回報來源 |
  | `tools/ytdlp` | 受管理的 yt-dlp venv：更新、自我檢查、回退、固定版本、`state.json` |
  | `backup` | 備份封存格式、安全解壓（白名單、大小與空間檢查） |
  | `netguard` | 對外 HTTP：在連線時封鎖私有網段與 metadata 位址 |
  | `subtitle` | cue 模型、VTT／SRT 解析、Normalize（NRM-R1～R6）、渲染、檔名 |
  | `errclass`、`clock` | 錯誤類別、可注入的時鐘 |

## 6. 已知待辦與技術債

審查時發現，但刻意延到對應切片處理的項目：

- **PIPE**：
  - 執行期回收過期租約延到 AGT（理由見 PIPE 待決問題），M1 只在啟動時回收。S10 只為 Agent 任務做了執行期回收（sweeper），內建 worker 的任務仍只在啟動時回收。
  - ~~產出檔的 sha256 不符時，應該標記損毀並重跑（NFR-REL-2）。~~ **已處理（收尾 D1）**：快取命中重用與讀取字幕產出時比對，畫面、筆記、摘要、章節的產出讀到損毀只回報錯誤、不自動重跑。
  - `transcript` 的資源類別一律是 `download`，ASR 來源實作時要依 PIPE 待決問題判斷。
  - ~~`POST /api/tasks/{id}/retry`、`/cancel`（單一任務）仍回 501。~~ **已處理（收尾 D1）**。
  - SSE：沒有寫入期限（要用真實時間，與 fake clock 衝突，需要另外設計）；登出後串流不會斷開；事件沒有 `id:` 與 replay。
  - `queue/control.go` 更新時持有寫鎖做 SQLite 寫入；pause 回傳後，進行中的一輪領取仍可能多領一個任務（毫秒級）。
  - `GET /api/queue` 只列 `executor=builtin` 的任務；S10 沒有加入 agent 任務，留給 UI 切片。
- **SRC**：
  - yt-dlp 的錯誤分類：影片不存在、私人、已移除是 `permanent`（依 stderr 的 `ERROR: [youtube] <id>: …`，見 `source/youtube/client.go`，還沒對真實的 yt-dlp 驗證過訊息）；`HTTP Error 403` 是 `needs_action`（SRC-R9）；其餘都當成 `retryable`。
  - 原文語言未知時，應該從 `*-orig` 字幕軌推斷。
  - ~~還沒做：cookie（SRC-R8）、品質檢查（SRC-R4，只做了 `language_mismatch`）、音訊切塊（SRC-R12）、幻覺過濾（SRC-R13）。~~ **已處理（cookie 在 SRC-B、切塊在 LOCAL、品質檢查與幻覺過濾在收尾 D3；門檻仍待 M0）**。
  - Supadata：非同步 job（202）還不會輪詢，目前當 permanent；暫時性錯誤時不會寫入 `source_probes`。（API key 已支援 DB 加密存放與 fallback，見 SEC-R4。）
  - 做 `youtube-auto` 前，要評估改用 yt-dlp 的 `json3` 格式（見 NRM 待決問題）。`features/fixtures/captions/auto-zh-en-mixed.json` 目前內容是 VTT，決定後與假 yt-dlp 一起改。
- **CLI**：
  - client 模式加 `--wait`、而 serve 是手動 worker 時，harness 還不會在背景推動 worker，步驟會失敗並說明（目前沒有場景用到）。
  - 本機路徑的 `ingest` 一律送 `{"path"}`（standalone 走程序內 handler，路徑不受限）。
  - `doctor` 的網路連線檢查要等 OPS 的已知影片；API key 檢查 Supadata 與 Groq。
  - JSON Lines 輸出在步驟中只比對第一個物件。
  - 非 Linux 平台的本機 socket 只靠檔案權限，沒有 uid 檢查。
- **GRD**：
  - GRD-R7、R8 的 403 場景在路徑檢查失效時會變成 pending（501），不是 failed，只靠 `passing.txt` 棘輪把關，不可以順手更新掉。
  - 直接輸入非 http 網址的場景驗證不到我們自己的 scheme 檢查（Go 的 `http.Client` 也會拒絕），由轉址場景與單元測試把關；GRD-R6 的 `--use-extractors` 只由單元測試把關（見 GRD 待決問題）。
  - 設定檔中「由資料庫管理的欄位」清單（profiles、prompts、budget(s)、settings、subscriptions、tokens、secrets、cookies、language、notifications）是實作時自訂的，需要 owner 確認；拿掉其中一項，`KnownFields` 仍會拒絕，場景驗證不到專用訊息。
  - `netguard.Deps` 同時有舊的 `Allowlist []netip.Prefix` 與新的 `AllowPrivate`，`features/internal/fakenet` 改用新欄位後可移除舊欄位。
  - 程式自己讀檔已改用 `Guard.Open`（`os.OpenRoot`）；交給 ffmpeg／ffprobe 的路徑仍有檢查與開檔之間的 TOCTOU（之後可在 toolexec 加 ExtraFiles，改傳 `/dev/fd/3`）。
  - 播放清單沒有項目數上限；yt-dlp 的播放清單錯誤訊息還沒對真實 yt-dlp 驗證（見 ING 待決問題）。
  - Supadata 回應超過大小上限時仍走 `isPermanent` 換下一個來源，最後訊息是「找不到可用的字幕來源」，看不到大小上限的訊息。
- **SRC**：
  - Groq：`verbose_json` 欄位、語言全名、25 MiB 上限、yt-dlp `--max-filesize` 的輸出字串都還沒對真實服務驗證。切塊（SRC-R12）已完成，但 Opus 位元率、靜音門檻、重疊長度、`-format_whitelist` 清單都是預設值，要用真實的 ffmpeg 與 Groq 確認（M0）；切塊不能續傳（`tasks.chunk_index` 尚未使用）。
  - LOCAL：
    - inbox 的判定原因只寫進 `inbox_entries` 與 log，還沒有 API。
    - inbox 中之後才出現、與既有上傳同名的檔案會記為衝突，不會新增。
    - 外掛字幕壞掉改走 ASR 時，metadata 決定的資源類別不知道這件事（只影響並行分類）。
    - 上傳的並行上限以資料目錄為鍵、全程序共用；閒置逾時用實際時間（讀取期限是 net 層的絕對時間，不能用 fake clock）。
    - `internal/backup.ParseUploadPath` 與 `pipeline.ValidUploadName` 各有一份檔名規則，條件一致。
    - ffprobe 的 disposition 在假工具中固定為「每種類型的第一條是 default」。
  - Groq 上傳用 chunked（`io.Pipe`），要求 Content-Length 的 proxy 會失敗。
  - Supadata 仍是先讀完 body 再依狀態碼分類（Groq 已改成先看狀態碼）。
  - ~~動作「現在改用語音轉錄」（SRC-R6）只顯示，點擊的處理還沒做。~~ **已處理（收尾 D2，`POST /api/items/{id}/use-asr-now`）**。
- **SRC-B**：
  - SRC-R8 是推定：Bilibili 未登入時只說「字幕需要登入」，分不出 CC 或 AI，也看不到語言；Bilibili 的錯誤字串、412 風控都依 yt-dlp 原始碼推測，等 M0 實測。
  - 假 yt-dlp 的兩處與真實不同：members-only 只在下載時失敗（`-J` 不失敗，為了不讓 ING-R9 的 `availability` 偵測失效）；空的 cookie 檔視為未登入（production 不會產生空檔）。production 之後應把 `-J` 的 members-only 錯誤歸為需要 cookie。
  - `ResumeBlocked` 只恢復同一個 token，沒有場景驗證（被誤恢復的步驟會再次 blocked，看不出差別），只靠單元測試。
  - 執行中補上 cookie 的判斷用毫秒比較，同一毫秒內上傳仍會 blocked。
  - 「改用語音轉錄」對 Bilibili 只顯示（Groq 還不能下載 Bilibili 音訊）；沒有 cookie、也沒有字幕的 Bilibili 影片會照 SRC-R5 失敗，訊息「設定語音轉錄服務」可能誤導。
  - ING-R9 的 Bilibili 部分（在 resolve 標示需要 cookie）做不到，規則待修。
  - cookie 的 GET／DELETE API 還沒做；有 cookie 就每次呼叫都帶，帳號被標記的風險寫在 SEC 待決問題。
- **OPS**：
  - `/readyz` 限速。
  - 自我檢查的檢查影片 `jNQXAC9IVRw` 寫死，能否長期使用、暫時性失敗是否也回退，等 M0 實測（見 OPS 待決問題）。
  - OPS-R9 的 `wsl` profile 實際上沒有任何服務宣告；`HEALTHCHECK` 沒有場景驗證。
  - 「備份的 schema 版本比程式新時拒絕還原」只由 `internal/app/restore_test.go` 把關（現有步驟做不出這種備份檔）。
  - 備份串流沒有寫入期限：慢速的 client 會讓 `<data>/tmp/backup-*` 的快照留到連線結束。
  - 受管理的 yt-dlp 在 Windows 上不支援（回 409）。
  - ~~每天的排程更新（OPS-R7 的「排程更新」）還沒做，目前只有手動更新。~~ **已處理（收尾 D2，排程工作 `tools.yt-dlp.update`）**。
  - 修改 provider 的 API 還沒有路由（SEC-R3 說「新增或修改」都回 403）；`POST /api/providers` 的非 command 類型回 501。
  - fixture 例外：`Given the file … contains:` 寫入、之後內容沒被改過的檔案，After hook 照字面掃描，不當成封存檔解壓（R2 刻意寫入不合法的 `.tar.gz`）。
- **AUTH / SEC**：
  - M1 的 token（`AUTH-R7～R10`）與 secrets（`SEC-R4、R6、R8、R9`）已完成；`AUTH` 的 `@mcp` origin 檢查（`AUTH-R11`）留待 M2。
  - `doctor` 維持 `noBackend: true`，目前仍只檢查環境變數 API key（透過 `/api/settings/services` 寫入 DB 的 key 仍會顯示 `warn`）。
  - `cookieBodyLimit` 包在 `mux` 外層、早於 Huma middleware，因此超過 512 KiB 的 `POST /api/cookies` 帶 bearer token 時會先回 `422 cookie_too_large` 而不是 `403`。
  - `RevokeAPIToken` 以 `id = ? OR name = ?` 比對（假設 token 名稱不會與另一個 token 的 UUIDv7 相同）；`CreateToken` 已禁止 `/` 與控制字元。
  - `X-Forwarded-Proto` 應該只採用可信 proxy 的值。
  - IPv6 的鎖定以單一位址計數。
- **其他**：
  - `Given the item … has been processed up to …` 會真的執行 pipeline；沒有準備字幕來源的場景要有 `features/fixtures/items/` 的 fixture（見第 2 節）。
- **DEL**：
  - 只靠單元測試守住：
    - 保留期限的邊界（剛好第 30 天）。
    - 每天只清一次。
    - tombstone 的寫入與移除。
    - 預覽不寫入資料。
    - purge 時取消殘留任務、刪除 events、不過度刪除。
  - 權限：`DEL-R12` 已接上（`GET /api/trash`、`deletion-preview` 為 `read`，其餘刪除／還原為 `admin`），`@mcp` 兩則留待 M2。
  - 手動新增與上傳移除 tombstone；inbox 自動新增遇到 tombstone 時不建立、不移除（ING-05 已接上）。M4 的訂閱接上時，比照 inbox 不移除。
  - tombstone 的 `sha256` 只有本機檔案有值（平台影音為空）。
  - `cues_fts`、`outputs`、術語候選還不存在，之後接上時，永久刪除要一併清除（uploads 已接上）。
  - 永久刪除後從舊備份還原會留下壞的產出（見 OPS 待決問題）。
  - `removeDirs` 失敗只記 log，沒有孤兒目錄的掃描。
  - `app.Server.SeedCost`、`CostEntries` 只給 harness 用。
- **LIB**：
  - 詳情 API（`Pipeline.Item` 的 `stages`）不列出沒有執行紀錄的 stage，列表篩選則視為 `pending`，UI 切片要決定是否補上。
  - 標題搜尋不做 NFKC 與簡繁互通；00006 的回填只處理 ASCII 的大小寫（M1 尚未發布，沒有舊資料）。
  - ~~`group_by`（LIB-R4 的分組）、`read_state`（回 501）還沒做~~ **已處理（`read_state` 在 LIB-08/09，`group_by` 在收尾 D1）**；`status` 只認 M1 的三個 stage。
  - `CountItemsFiltered` 每次掃全表（10k 筆約 3ms）；status 篩選在 10k 筆約 62ms。列表與 COUNT 是兩段相同的 WHERE，靠單元測試確認一致。
  - CLI 的 `resolveItem` 每次逐頁找，API 沒有依代號篩選的參數。
  - 只靠單元測試守住：`waiting` 不含執行中的任務、垃圾桶中的影音不在列表、排序、`limit` 與最後一頁沒有 `next_cursor`。
  - 字幕讀取端沒有串流（NFR-RES-3 只做到輸出端）。
  - ~~有 HTML 回應時要加上 CSP 等標頭（NFR-SEC-1）~~：**已處理（UI-0，`internal/webui`）**。`style-src` 允許 `'unsafe-inline'`（只限 style），見 ui/README 10.3 的待決問題。
  - **CI 時間（2026-11-23）**：master 的 CI 約 23 分鐘（`go test -race` 那一步最久：features 的 308 個場景在 `-race` 下約 10～12 分鐘，`-timeout 30m` 是每個測試程式的上限）。場景再增加時要考慮並行化或把 features 拆成獨立的 job；有牆鐘門檻的測試（例如 `internal/configio` 的 adversarial 測試，門檻 30 秒）在 `-race` 與共用主機上會被放大。
  - CI（`.github/workflows/ci.yml`）：gofmt、`go mod tidy -diff`、vet、`go test -race`、`sqlc diff`，不建置 image。image 由 `docker-publish.yml` 只在 `v*` tag 時建置、冒煙測試後發布到 GHCR（私人 repo，避免浪費 Actions 額度）。還沒有 Python 版規格檢查（Go 版在 `go test` 中）與 `govulncheck`。

## 7. 仍有效的待決問題

都有預設值，不阻塞開發；細節見各規格的「待決問題」段落。

| 規格 | 問題 | 預設 |
|---|---|---|
| AUTH | session 閒置期限 30 天是否太長 | 30 天 |
| SEC | master key 的格式 | 32 bytes base64 直接用，否則用 argon2id 推導 |
| ING | 上傳大小上限；inbox 穩定判定時間 | 4GB；10 秒 |
| SRC | 品質檢查與幻覺過濾的門檻；Groq 是否有 word 級時間戳 | 依 M0；相關場景為 `@pending-m0` |
| NRM | 300ms、40ms、1 秒等門檻；自動字幕改用 `json3` 的評估 | 暫定值；做 `youtube-auto` 前決定 |
| OUT | sidecar 檔名的語言代碼 | M2 實作前實測 |
| PIPE | 退避是否依資源類別區分 | 不區分 |
| PIPE | aging 級距：回補等 20 分鐘就排在新的手動新增前面 | 照規則；差距不能超過 17 級 |
| PIPE | `transcript` 的資源類別 | 見第 6 節 |
| PIPE | **R7、R13 的場景驗證不到規則，需要修改 `.feature`** | 等使用者同意；清除邏輯先以單元測試驗證 |
| SRC | 沒有 API key 的來源與預設 fallback（SRC-R10） | 略過沒有 key 的來源，全部都不能用才 blocked；Supadata 依官方錯誤碼分類 |
| CLI | Windows 原生 binary 的 client 模式 | 只支援 `--server` |
| OPS | 自我檢查用的已知影片 | M1 內建一部，不開放覆寫；M0 實測 |
| OPS | 自我檢查的暫時性失敗是否回退 | 照規則回退，原因放在 `reason` |
| LOCAL | steps.md 的語意補充（部分 `#` 表格依 `#` 比對；`processed up to` 規則 (1) 對 `local:` 也算外掛與內嵌字幕）、用路徑新增的延遲驗證、上傳上限 4 GiB、YouTube ASR 也走前處理、備份包含 uploads | 已依預設實作（PR #13），待 owner 確認 |
| NRM | NRM-R7 場景的字幕軌不會被 `caption_priority` 選中 | **已解決（2026-11-23）**：場景改成宣告原文語言 `zh-Hans`（`29825d4`） |
| UI | Web UI 的細節決定（npm、Node 24.12、程式碼式路由、session 判斷、主題防閃爍、CSP `style-src 'unsafe-inline'`、縮圖網域） | UI-0 已依 advisor 建議實作，見 [ui/README 10.2、10.3](ui/README.md#102-ui-0-的決定)，待 owner 追認；mockups 只沿用 token，HTML 不照搬 |

## 8. 已確定的技術選擇

- **Go 版本**：go.mod 維持 `go 1.25`，依賴釘在不會把 `go` 指令升到 1.26 的版本（例如 huma v2.34.1、goose v3.26.0、modernc sqlite v1.39.1）。執行 `go get -u` 前要先確認這點。建置用的 toolchain 另外以 `toolchain go1.26.11` 釘住（Dockerfile 的 `GO_VERSION` 同步、CI 的 govulncheck 也用它）；標準函式庫有新的安全修補時三處一起升。
- **sqlc**：
  - 不放進 go.mod，因為 sqlc v1.31.1 要求 go 1.26；用 `go run …@v1.31.1` 執行，需要 gcc 與 cgo。
  - `queries/*.sql` 的註解只能用英文：sqlc 的 sqlite parser 遇到非 ASCII 字元會讓後面查詢的參數錯位。
  - `ClaimTasks` 用 JSON 陣列加 `json_each`，不用 `sqlc.slice`。
- **排程**：自寫，不用 gocron v2（plan v0.3）。
- **godog**：v0.16.0，M1 期間使用非 strict 模式；M1 完成時改成 strict，並拿掉「501 → pending」的約定。

## 9. 環境注意事項

- 本機是 WSL2，Go 1.26.8。
- Bash 工具有一個 hook 會把指令改寫成 `rtk …` 的形式。自動模式會要求用改寫後的指令重送一次，照做即可。
- 自動模式會擋下破壞性的 git 操作，例如 `git branch -D`、`git push --delete`。需要時請使用者執行，或先在設定中允許。
- 待清理（內容都已在 `master`；`feat/auth-secrets` 的 Wave 2 暫存 worktree `.worktrees/auth-secrets-cli`、`.worktrees/auth-secrets-steps` 已清除；疊加的 PR 分支 `feat/sources`～`feat/auth-secrets` 保留等待 push 與建立 PR）：
  - `.worktrees/store-sqlc` 與 `refactor/store-sqlc` 分支（本機與遠端）。
  - `.claude/worktrees/` 下 builder 用過的 worktree（切片 1 到 OPS 的都是），以及 `worktree-agent-*` 分支。
  - `.worktrees/m1-skeleton`、`feat/m1-skeleton`、`docs/bdd-specs`、`docs/plan-review-revisions` 都已併入 `master`，可以一併移除。

# 交接：開發進度（M1 與 M2）

> 更新：2026-11-23（M2 後端與驗收場景全部完成，見第 1 節開頭的「2026-11-23 收尾摘要」）。2026-11-22：PR #1～#19 已合併（LOCAL #13、PODCAST #14、ESTIMATE #15、M2 S0 #16、M2 S1/S2 #17、規格與 `.feature` 修改 #18、M2 S6a #19）；之後依 owner 的 /goal 指示（MVP 階段，不再開 PR）改為每個切片完成後直接推送到 master，commit 在 `git log` 可查：M2 S7（OUT 渲染與匯入）、M2 S3（LLM 核心引擎）。owner 已同意的規格與 `.feature` 修改已完成（第 2 節第 4 點）。**M1 的後端已完成；Web UI 有 UI-0 骨架與 UI-1 媒體庫、新增抽屜（2026-11-23），影音詳情、佇列、設定等頁面（UI-2、UI-3）與真實樣本驗收還沒做，M1 尚未收尾（第 1 節「M1 尚未收尾」）。**
> 用途：讓下一個 session 或協作者不用重讀全部對話就能接手。內容會隨切片推進而更新；規則與決策以規格、ADR 為準，這裡只做索引與摘要。

## 1. 現況

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
  - 建議的實作切法：M3-S0 骨架（比照 M2 S0：schema 接受全部 M3 欄位與設定檔的 `vaults`、`sponsorblock`，M3 路由回 501 stub；假服務接線、`/vault` 對應、`processed up to` 的 M3 規則、預設的假 SponsorBlock；之後每個切片以 `@M3` junit 逐場景比對）→ S1 SPB → S2 CHAP → S3 FRM（候選與規則選圖，再 vision）→ S4 NOTE（模板、渲染、檢查、跳脫，再 map-reduce 與模板 API）→ S5 VAULT fs → S6 VAULT git＋hook → S7 GEM；每個步驟加進 `pipeline.Stages` 時同步移除 `plannedStages` 並確認 308 個場景不退步。
- **M4 規格展開（2026-11-23，分支 `docs/m4-tracking-deploy-specs`，只有文件、場景、步驟樁與假服務骨架，沒有產品程式）**：
  - 九份新規格（狀態 `ready`）：[SUB](specs/SUB-subscriptions.md)、[ENTRY](specs/ENTRY-entry-points.md)、[BND](specs/BND-bundles.md)、[DIG](specs/DIG-digest.md)、[BATCH](specs/BATCH-batch-api.md)、[GLOS](specs/GLOS-glossary-review.md)、[SRCH](specs/SRCH-search.md)、[NOTIFY](specs/NOTIFY-notifications.md)、[COST](specs/COST-cost-budget.md)；既有規格加 M4 章節：SRC（SRC-10，SRC-R18～R23）、LIB（LIB-08、09，LIB-R13～R23）、AUTH（AUTH-06～08，AUTH-R12～R22）、OPS（OPS-06～08，OPS-R11～R21），場景放在新的 `SRC-better-source.feature`、`LIB-bulk-overview.feature`、`AUTH-proxy-oidc.feature`、`OPS-unattended.feature`（LIB、OPS 檔頭仍是 `draft`，因為 M1 的部分還沒收尾；M4 章節依 DoR 撰寫）。跨規格的決定在 [ADR-0021](adr/0021-m4-scheduler-subscriptions-and-outbox.md)：排程器是 worker 的 `Periodic`（`scheduled_jobs`、重啟只補跑一次、hash 決定的 jitter、`scheduler.jitter_max`）、`discover` 不是步驟、`summary` 放在 `normalize` 之後且預設 off、預算預留與檢查同一交易、通知經 outbox、migration 方向、Postgres 與媒體檔保留策略不在 M4。
  - 數量：13 個 `.feature`、300 個場景定義（`@M4` 展開後 371 個自動化場景，`@manual` 21 個、`@pending-m0` 2 個另計），137 條新規則、29 則故事。`go test ./features -run TestFeatures -godog.tags='@M4'`：371 pending、0 failed、0 undefined；預設 tag 308 passed、`@M3` 109 pending 不變。
  - 新句型在 `steps.md` 4.17～4.21（107 個，步驟樁在 `features/steps/subscriptions.go`、`notify.go`、`costs.go`、`library.go`、`signin.go`，一律 `r.Step` 不限關鍵字）；既有句型的 M4 語意補充寫在各節開頭（`{subscription:<別名>}` 代入、`a YouTube video … with:` 的 `published_at`／`shorts`、`the worker runs until idle` 也執行到期的排程、`processed up to "summary"` 等）。
  - 假服務骨架（只有單元測試，還沒接進 fakenet）：`features/internal/fakefeed`（YouTube Atom、RSS、OPML、畸形與超大 XML）、`fakenotify`（ntfy、Discord、Telegram、webhook 接收端與 HMAC 驗證）、`fakebatch`（Anthropic／OpenAI Batch API）、`fakeoidc`（discovery、JWKS、authorize、token，含壞簽章／過期／錯 nonce）；設計見 `features/fixtures/README.md`「M4 的假服務」。
  - 要 owner 追認的決定：ADR-0021 全文；SUB S1（新內容的判準：頻道看發布時間，清單類看第一次檢查的基準）、S2（不用 Supadata channel/videos）、S3（Bilibili 只支援合集）、S5（回補上限 recent 200／全部 500／每次 50；rate limit YouTube 30、Bilibili 6、每個 Podcast 主機 10 次／分）、S6（Shorts 只看網址）、S7（直播等 VOD 由 metadata 的 `wait`）；SRC R1（重新 Probe 每 12 小時）、R2（`language.better_source: notify|rerun|off`）、R6（有 `edited` 時只通知）；ENTRY E1、E2（`/add` 不自動開始）、E3（`next` 上限 2048，AUTH-R17 已一併改）；DIG D2（summary 不切 chunk，超過 2 小時或 32000 tokens 為 `too_long`）、D3、D6（彙整連結只指向 UI）、D7／D11（對齊當地時間，ADR-0021 已補例外）、D9；NOTIFY N1（`permanent` 首次就 dropped）、N2（不跟隨轉址）、N3（Telegram/Discord 只能官方主機或 `notify.allow_hosts`）、N4、N5（去重 24 小時、每 10 分鐘 20 則）；OPS O1～O6（備份預設寫 `/data/backups`、保留只看檔名、yt-dlp 失敗率固定門檻、vps 的密碼檢查放在 `vps-check` 服務）；COST C1（`PUT /api/settings/budget`，UI 第 11 節已加修訂行）、C2、C3、C5、C6（M4 沒有成本的 MCP／CLI）、C10（金額以整數 micro-USD 比較）；BATCH H1～H4（預設 off、24 小時逾時、5 分鐘輪詢、每批 100 個）；LIB L-M1（M4 不做暫停）、L-M2（「刪除媒體檔」延後）、L-M3（`POST /api/items/batch`）；SRCH S1（每個語言索引最完整的一份字幕）、S4、S6（不依相關度排序、簡繁不互通）；BND N1（匯入要 `admin`）、N4（上限 64 MiB／256 MiB／2000 項／壓縮比 100）、N5（已存在回 409）；GLOS G1（審核放在設定頁新的「詞彙表」分區，UI 第 12 節已加列）、G4（批次一個交易）；AUTH A1～A7（OIDC 路由只在啟用時免驗證、仍要求密碼、IdP 請求不經私有網段封鎖但限 https、時鐘偏差 60 秒、proxy session 綁標頭、`/mcp` 不接受 proxy 身分、輪替保留到期時間）。
  - 已知的規格間張力（寫在各規格的待決問題）：story-map 的 LIB-08 寫了 pause 但 M4 不做；UI 第 5 節線框在「只抓之後的新內容」時仍顯示「會處理 8 部」；ADR-0005 的 bundle manifest 要補 `files`、`source_name`、`created_at`；plan 第 14 節「只索引校正與譯文」以 SRCH-R1 為準；既有單元測試 `internal/api/items_test.go`（budget 回 501）、`items_list_test.go`（`read_state` 回 501）在實作時要一起改。
  - 建議的實作切法：M4-S0 骨架（schema 接受全部 M4 欄位與設定檔鍵、M4 路由回 501 stub、`scheduled_jobs` 與排程器接進 `periodics`／`nextWake`、fakeXXX 接進 fakenet、確認 308 個場景不退步；之後每個切片以 `@M4` junit 逐場景比對）→ S1 NOTIFY outbox 與 notifier（多個領域的閘門）→ S2 SUB-A（建立、RSS 探索、回補、過濾、排程）→ S3 SUB-B（預覽、匯入、直播、rate limit、不可信輸入、MCP/CLI）＋ ENTRY（可最先平行）→ S4 COST（帳本、計價、預算預留；與 LIB-bulk-overview 共用閘門，要一起確認）→ S5 BATCH → S6 DIG（summary 加進 `pipeline.Stages` 時確認 308 個不退步，再做彙整）→ S7 SRC-10（依賴排程器與 M3 SPB）→ 並行 S8 SRCH、S9 GLOS、S10 BND、S11 AUTH（token 輪替與工作階段 → proxy → OIDC）→ S12 OPS（每日備份、告警、vps profile）→ S13 LIB-08、09。
- **M5 規格展開（2026-11-23，分支 `docs/m5-hub-specs`，只有文件、場景、步驟樁與假服務骨架，沒有產品程式）**：
  - 四份規格（狀態 `ready`）與 `.feature`：[HACC](specs/HACC-hub-accounts.md)（HUB-01、07）、[HREP](specs/HREP-hub-repository.md)（HUB-02 Hub 端、03、05 Hub 端、06）、[HSHR](specs/HSHR-hub-sharing.md)（HUB-02 Client 端、04、05）、[HMCP](specs/HMCP-hub-mcp.md)（HUB-08）；跨規格的決定在 [ADR-0030](adr/0030-hub-deployment-and-storage.md)（獨立 binary `cmd/notesteme-hub`、SQLite＋fs 為參考 backend／Postgres＋S3 為正式環境、測試替身依被測系統切開）、[ADR-0031](adr/0031-hub-identity-permissions-and-content.md)（身分與帳號連結、權限與內建群組、版本鍵與去重、衍生關係、`model_verified`、下架與濫用防護、資料表）、[ADR-0032](adr/0032-client-hub-protocol-and-privacy.md)（`/api/v1` 契約、送出的 bundle 白名單、ASR 前查詢只送 hash 前綴、錯誤對應）。ADR-0012、0005 的狀態改成已採納（細節由 0031、0032 定案）。
  - `@M5` 場景：`go test ./features -run TestFeatures -godog.tags='@M5'` 為 148 個場景全部 pending（`@ui`、`@manual` 另計）；每個 M5 的 Background 第一步是閘門句型（`Given a Hub is running` 或 `Given the owner's client is connected to a Hub`）。預設 tag 的 308 個場景不受影響。
  - 新句型在 `steps.md` 4.30～4.32（步驟樁在 `features/steps/hub.go`、`hub_content.go`、`hub_client.go`）；第 3 節加了字幕來源 ID `hub`。假服務骨架（只有單元測試，還沒接進 fakenet）：`features/internal/fakeoauth`、`fakehub`，設計見 `features/fixtures/README.md`「M5 的假服務」。
  - 和 M4 的衝突點：Hub 的 bundle 驗證沿用 BND-R4～R7（M4 分支），但送給 Hub 的 bundle 沒有 `created_by.instance_id`，Hub 的上傳上限也比 BND 小（HREP 待決問題 R1）。M4 合併後由 Hub 的第一個實作切片處理。
  - 要 owner 追認的決定：ADR-0030～0032 全文；HACC A1（PKCE 一律送）、A2（不依 email 合併帳號）、A4（新帳號只有 `read`、`download`）、A5（下架申請不需要帳號）；HREP R1（BND 的對齊）；HSHR S1（ASR 前查詢只送 hash 前綴，偏離 plan 第 20 節）、S2（`cookie_required` 的定義與 SRC 的來源紀錄新欄位）、S3（分享用 `admin` scope）、S4（只用原始字幕重新生成）、S7（Client 對 Hub 的逾時用注入的 clock）；HMCP M1（`get_transcript` 需要 `download`）。
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
    - **還沒做／待決**：術語候選的核可／拒絕 API 已在 S11 補上（`PATCH /api/channels/{id}/term-candidates/{cid}`，見 COR 規格 C4）；`@pending-m0` 的 `A rewrite that strays too far` 場景寫「相似度 0.42」，Levenshtein 算出 0.19，要等 M0 決定算法或改場景；COR-R8 場景在 S6b 補上 `translate-two-cues` fixture 後已通過。
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

## 2. 下一步

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
  - 產出檔的 sha256 不符時，應該標記損毀並重跑（NFR-REL-2）。
  - `transcript` 的資源類別一律是 `download`，ASR 來源實作時要依 PIPE 待決問題判斷。
  - `POST /api/tasks/{id}/retry`、`/cancel`（單一任務）仍回 501。
  - SSE：沒有寫入期限（要用真實時間，與 fake clock 衝突，需要另外設計）；登出後串流不會斷開；事件沒有 `id:` 與 replay。
  - `queue/control.go` 更新時持有寫鎖做 SQLite 寫入；pause 回傳後，進行中的一輪領取仍可能多領一個任務（毫秒級）。
  - `GET /api/queue` 只列 `executor=builtin` 的任務；S10 沒有加入 agent 任務，留給 UI 切片。
- **SRC**：
  - yt-dlp 的錯誤分類：影片不存在、私人、已移除是 `permanent`（依 stderr 的 `ERROR: [youtube] <id>: …`，見 `source/youtube/client.go`，還沒對真實的 yt-dlp 驗證過訊息）；`HTTP Error 403` 是 `needs_action`（SRC-R9）；其餘都當成 `retryable`。
  - 原文語言未知時，應該從 `*-orig` 字幕軌推斷。
  - 還沒做：cookie（SRC-R8）、品質檢查（SRC-R4，只做了 `language_mismatch`）、音訊切塊（SRC-R12）、幻覺過濾（SRC-R13）。
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
  - 動作「現在改用語音轉錄」（SRC-R6）只顯示，點擊的處理還沒做。
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
  - 每天的排程更新（OPS-R7 的「排程更新」）還沒做，目前只有手動更新。
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
  - `group_by`（LIB-R4 的分組）、`read_state`（回 501）還沒做；`status` 只認 M1 的三個 stage。
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

- **Go 版本**：go.mod 維持 `go 1.25`，依賴釘在不會把 `go` 指令升到 1.26 的版本（例如 huma v2.34.1、goose v3.26.0、modernc sqlite v1.39.1）。執行 `go get -u` 前要先確認這點。
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

# 故事地圖

> 狀態：草案 v0.1（2026-11-12）
> 相關文件：[規格與驗收](README.md)、[計畫第 24 節 Roadmap](../plan.md#24-roadmap)、[非功能需求](nfr.md)

故事地圖列出使用者能用 Notesteme 做到的事，以及各自排在哪個里程碑：

- **M1、M2**：故事已經寫成完整的能力規格與 `.feature` 檔。下表只是索引，故事全文、規則與驗收場景以各份規格為準。
- **M3**：2026-11-23 已展開成能力規格與 `.feature` 檔（第 4 節每則故事後面列出規格、規則與場景）；條列的驗收標準保留作為展開前的原始描述，以規格為準。
- **M4**：2026-11-23 已展開成能力規格與 `.feature` 檔（第 5 節每則故事後面列出規格、規則與場景；跨規格的決定見 [ADR-0021](../adr/0021-m4-scheduler-subscriptions-and-outbox.md)）；條列的驗收標準保留作為展開前的原始描述，以規格為準。
- **M5**：故事先用條列式的驗收標準描述。開發該里程碑前，依 [README 第 6 節](README.md#6-開發流程) 的流程展開成規格與場景；展開時可以調整 ID 以外的所有內容。
- **M3 到 M5**：故事先用條列式的驗收標準描述（M5 已在 2026-11-23 展開成規格與 `.feature` 檔，見第 6 節）。開發該里程碑前，依 [README 第 6 節](README.md#6-開發流程) 的流程展開成規格與場景；展開時可以調整 ID 以外的所有內容。

## 1. 角色

| 角色 | 英文用語 | 說明 |
|---|---|---|
| 擁有者 | the owner | 架設並使用實例的人。M1 到 M4 只有這一個人類使用者。 |
| Agent | an agent | 透過 MCP 或 CLI 使用 Notesteme 的 AI Agent，例如 Claude Code、Codex。權限由 token 的 scope 決定。 |
| Hub 使用者 | a hub member | M5 起登入 Hub、分享與取用字幕的人。 |
| Hub 管理員 | a hub admin | M5 起管理 Hub 權限與下架申請的人。 |

## 2. 地圖總覽

橫軸是使用者旅程中的活動，縱軸是里程碑。格子裡是能力 ID；有連結的已有完整規格。

| 里程碑 | 收集影音 | 取得字幕 | 加工字幕 | 取用與輸出 | 安全與維運 |
|---|---|---|---|---|---|
| **M1** | [ING](ING-ingest.md) | [SRC](SRC-transcript-sources.md)、[NRM](NRM-normalize.md)、[PIPE](PIPE-pipeline-queue.md) | — | [OUT](OUT-subtitle-output.md)（SRT／VTT）、[LIB](LIB-library.md)、[DEL](DEL-delete.md)、[CLI](CLI-cli.md) | [AUTH](AUTH-access.md)、[SEC](SEC-config-secrets.md)、[GRD](GRD-guards.md)、[OPS](OPS-operations.md) |
| **M2** | — | — | [LLM](LLM-execution.md)、[SEG](SEG-segment.md)、[COR](COR-correct.md)、[TRN](TRN-translate.md)、[PRM](PRM-prompts.md)、[PRF](PRF-profiles.md) | [OUT](OUT-subtitle-output.md)（ASS、sidecar、匯入）、[LIB](LIB-library.md)（對照檢視）、[DEL](DEL-delete.md)（輸出檔）、[MCP](MCP-tools.md)、[AGT](AGT-agent-tasks.md) | [AUTH](AUTH-access.md)（MCP Origin）、[SEC](SEC-config-secrets.md)（設定匯出入） |
| **M3** | — | [SPB](SPB-sponsorblock.md) | [FRM](FRM-frames.md)、[CHAP](CHAP-chapters.md)、[NOTE](NOTE-notes.md)、[GEM](GEM-gemini.md) | [VAULT](VAULT-vault-output.md) | — |
| **M4** | [SUB](SUB-subscriptions.md)、[ENTRY](ENTRY-entry-points.md)、[BND](BND-bundles.md) | [SRC](SRC-transcript-sources.md)（SRC-10） | [DIG](DIG-digest.md)、[BATCH](BATCH-batch-api.md)、[GLOS](GLOS-glossary-review.md) | [SRCH](SRCH-search.md)、[LIB](LIB-library.md)（LIB-08～10）、[NOTIFY](NOTIFY-notifications.md) | [COST](COST-cost-budget.md)、[AUTH](AUTH-access.md)（AUTH-06～08）、[OPS](OPS-operations.md)（OPS-06～08） |
| **M5** | HUB | HUB | — | HUB | HUB |
| **M3** | — | SPB | FRM、CHAP、NOTE、GEM | VAULT | — |
| **M4** | SUB、ENTRY、BND | SRC-10 | DIG、BATCH、GLOS | SRCH、LIB-08～LIB-10、NOTIFY | COST、AUTH-06～08、OPS-06～08 |
| **M5** | [HSHR](HSHR-hub-sharing.md) | [HSHR](HSHR-hub-sharing.md)、[HREP](HREP-hub-repository.md) | — | [HREP](HREP-hub-repository.md)、[HMCP](HMCP-hub-mcp.md) | [HACC](HACC-hub-accounts.md) |

## 3. M1、M2 故事索引

M1、M2 共 20 項能力、110 則故事（SEC-06、SEC-07 於 2026-11-25 新增）。故事的角色、目的與理由見各份規格的 User stories 表。

### 收集影音

| 故事 | 想做到的事（I want …） | 里程碑 | 規格 |
|---|---|---|---|
| ING-01 | paste video URLs and have them processed | M1 | [ING](ING-ingest.md) |
| ING-02 | Notesteme to tell me what a URL points to before I submit it | M1 | [ING](ING-ingest.md) |
| ING-03 | choose which parts of a multi-part Bilibili video to process | M1 | [ING](ING-ingest.md) |
| ING-04 | pick episodes from a podcast feed | M1 | [ING](ING-ingest.md) |
| ING-05 | add local media by uploading it or dropping it into an inbox folder | M1 | [ING](ING-ingest.md) |
| ING-06 | adding something that is already in my library to point me to it instead of duplicating it | M1 | [ING](ING-ingest.md) |
| ING-07 | adjust settings for a single run without editing my profile | M1 | [ING](ING-ingest.md) |
| ING-08 | see an estimated cost and any required cookies before I start | M1 | [ING](ING-ingest.md) |

### 取得字幕

| 故事 | 想做到的事（I want …） | 里程碑 | 規格 |
|---|---|---|---|
| SRC-01 | platform captions to be used whenever they exist, in my preferred order of languages and kinds | M1 | [SRC](SRC-transcript-sources.md) |
| SRC-02 | paid sources to be used only when no platform caption fits, in the order I set | M1 | [SRC](SRC-transcript-sources.md) |
| SRC-03 | unusable results such as the wrong language, hallucinations or music-only tracks to be skipped automatically | M1 | [SRC](SRC-transcript-sources.md) |
| SRC-04 | Notesteme to wait and check again when captions are not ready yet | M1 | [SRC](SRC-transcript-sources.md) |
| SRC-05 | a clear message and a fix action when a source needs something from me | M1 | [SRC](SRC-transcript-sources.md) |
| SRC-06 | long recordings to be transcribed even when they exceed a provider's file limit | M1 | [SRC](SRC-transcript-sources.md) |
| SRC-07 | know which source a transcript came from and how its timing was obtained | M1 | [SRC](SRC-transcript-sources.md) |
| SRC-08 | the original language detected when the platform does not provide it | M1 | [SRC](SRC-transcript-sources.md) |
| SRC-09 | subtitles that come with my local files or podcast feeds to be used before any transcription | M1 | [SRC](SRC-transcript-sources.md) |
| NRM-01 | rolling auto captions cleaned into readable lines | M1 | [NRM](NRM-normalize.md) |
| NRM-02 | subtitle timings to always move forward without overlaps | M1 | [NRM](NRM-normalize.md) |
| NRM-03 | the normalized transcript to be the same no matter which target languages I choose | M1 | [NRM](NRM-normalize.md) |
| PIPE-01 | each stage of an item to show a clear status | M1 | [PIPE](PIPE-pipeline-queue.md) |
| PIPE-02 | temporary failures retried automatically and permanent ones explained | M1 | [PIPE](PIPE-pipeline-queue.md) |
| PIPE-03 | work that has already been done to be reused | M1 | [PIPE](PIPE-pipeline-queue.md) |
| PIPE-04 | limit how much runs at once and pause the queue | M1 | [PIPE](PIPE-pipeline-queue.md) |
| PIPE-05 | items I add by hand to run before background work | M1 | [PIPE](PIPE-pipeline-queue.md) |
| PIPE-06 | processing to resume after a crash or restart | M1 | [PIPE](PIPE-pipeline-queue.md) |
| PIPE-07 | rerun an item from any stage and keep the older results | M1 | [PIPE](PIPE-pipeline-queue.md) |

### 加工字幕

| 故事 | 想做到的事（I want …） | 里程碑 | 規格 |
|---|---|---|---|
| LLM-01 | long transcripts processed in parallel chunks that still see their surroundings | M2 | [LLM](LLM-execution.md) |
| LLM-02 | invalid LLM output retried with the error and then safely fall back | M2 | [LLM](LLM-execution.md) |
| LLM-03 | a failed chunk retried on its own | M2 | [LLM](LLM-execution.md) |
| LLM-04 | prompts arranged so that providers can cache the shared part | M2 | [LLM](LLM-execution.md) |
| LLM-05 | provider rate limits respected and provider errors explained | M2 | [LLM](LLM-execution.md) |
| LLM-06 | instructions hidden in a transcript to be treated as plain data | M2 | [LLM](LLM-execution.md) |
| SEG-01 | subtitles split into readable sentences | M2 | [SEG](SEG-segment.md) |
| SEG-02 | subtitle timings to come only from the source, never from the model | M2 | [SEG](SEG-segment.md) |
| SEG-03 | choose how strict timing must be | M2 | [SEG](SEG-segment.md) |
| SEG-04 | good manual captions left as they are | M2 | [SEG](SEG-segment.md) |
| COR-01 | typos, names and punctuation fixed line by line | M2 | [COR](COR-correct.md) |
| COR-02 | a correction that strays too far from the original to be rejected and reverted | M2 | [COR](COR-correct.md) |
| COR-03 | channel and video terms used as a glossary | M2 | [COR](COR-correct.md) |
| COR-04 | new terms the model notices to be collected for my review | M2 | [COR](COR-correct.md) |
| TRN-01 | subtitles translated into one or more target languages | M2 | [TRN](TRN-translate.md) |
| TRN-02 | long sentences that span several cues translated as a whole | M2 | [TRN](TRN-translate.md) |
| TRN-03 | bad translations caught and handled safely | M2 | [TRN](TRN-translate.md) |
| TRN-04 | Simplified and Traditional Chinese converted cheaply, or localised with the right regional wording when I ask for it | M2 | [TRN](TRN-translate.md) |
| TRN-05 | existing platform subtitles in my target language reused | M2 | [TRN](TRN-translate.md) |
| PRM-01 | every prompt change saved as a new version | M2 | [PRM](PRM-prompts.md) |
| PRM-02 | try a prompt on part of a real item before using it | M2 | [PRM](PRM-prompts.md) |
| PRM-03 | profiles to keep using the version they were set to until I promote a new one | M2 | [PRM](PRM-prompts.md) |
| PRM-04 | mistakes in prompt variables caught when I save | M2 | [PRM](PRM-prompts.md) |
| PRF-01 | save a set of pipeline settings as a named profile | M2 | [PRF](PRF-profiles.md) |
| PRF-02 | global, profile, channel and per-run settings layered in a fixed order | M2 | [PRF](PRF-profiles.md) |
| PRF-03 | choose per LLM stage whether it runs through the API, an agent or not at all | M2 | [PRF](PRF-profiles.md) |
| PRF-04 | invalid profiles rejected with the exact field at fault | M2 | [PRF](PRF-profiles.md) |

### 取用與輸出

| 故事 | 想做到的事（I want …） | 里程碑 | 規格 |
|---|---|---|---|
| OUT-01 | download subtitles in SRT, VTT or plain text at any time | M1 | [OUT](OUT-subtitle-output.md) |
| OUT-02 | bilingual ASS subtitles with a readable default style | M2 | [OUT](OUT-subtitle-output.md) |
| OUT-03 | customise an ASS style and preview it on a real frame | M2 | [OUT](OUT-subtitle-output.md) |
| OUT-04 | subtitles written next to my local video files automatically | M2 | [OUT](OUT-subtitle-output.md) |
| OUT-05 | files I have edited by hand never to be overwritten | M2 | [OUT](OUT-subtitle-output.md) |
| OUT-06 | import subtitles I fixed in an external editor | M2 | [OUT](OUT-subtitle-output.md) |
| LIB-01 | filter my library by channel, platform, stage status and error class | M1 | [LIB](LIB-library.md) |
| LIB-02 | search my library by title | M1 | [LIB](LIB-library.md) |
| LIB-03 | read an item's subtitles next to its player and jump to any line | M1 | [LIB](LIB-library.md) |
| LIB-04 | see each resource lane of the queue with its limit and running tasks | M1 | [LIB](LIB-library.md) |
| LIB-05 | the library and item pages to update live while processing | M1 | [LIB](LIB-library.md) |
| LIB-06 | compare the original, corrected and translated subtitles and see the differences | M2 | [LIB](LIB-library.md) |
| LIB-07 | rerun an item from any stage or with another prompt or model | M2 | [LIB](LIB-library.md) |
| DEL-01 | deleted items to go to a trash first | M1 | [DEL](DEL-delete.md) |
| DEL-02 | see what will be lost before I delete an item | M1 | [DEL](DEL-delete.md) |
| DEL-03 | a permanently deleted item to leave nothing behind in my data directory except my spending records | M1 | [DEL](DEL-delete.md) |
| DEL-04 | files written to my vault or media folders left alone unless I ask, and never deleted if I edited them | M2 | [DEL](DEL-delete.md) |
| DEL-05 | deleted items not to come back on their own | M1 | [DEL](DEL-delete.md) |
| DEL-06 | only admin tokens to be able to delete, with no MCP tool for it | M1 | [DEL](DEL-delete.md) |
| DEL-07 | delete, restore and empty the trash from the CLI | M1 | [DEL](DEL-delete.md) |
| CLI-01 | CLI commands to work the same whether or not the server is running | M1 | [CLI](CLI-cli.md) |
| CLI-02 | machine-readable output and meaningful exit codes | M1 | [CLI](CLI-cli.md) |
| CLI-03 | a CLI on Windows to talk to the instance running in WSL Docker | M1 | [CLI](CLI-cli.md) |
| CLI-04 | the existing `watch` skill to become a thin wrapper over the CLI | M1 | [CLI](CLI-cli.md) |
| MCP-01 | get a video's transcript in the format and time range I need | M2 | [MCP](MCP-tools.md) |
| MCP-02 | one call that prepares a video and returns its context | M2 | [MCP](MCP-tools.md) |
| MCP-03 | add items and import subtitles through MCP when my token allows it | M2 | [MCP](MCP-tools.md) |
| MCP-04 | see only the tools my token can use | M2 | [MCP](MCP-tools.md) |
| MCP-05 | the prompt templates available through MCP | M2 | [MCP](MCP-tools.md) |
| AGT-01 | claim several tasks at once with everything I need to do them | M2 | [AGT](AGT-agent-tasks.md) |
| AGT-02 | two agents working at the same time never to get the same task | M2 | [AGT](AGT-agent-tasks.md) |
| AGT-03 | my invalid submissions explained while I keep the task | M2 | [AGT](AGT-agent-tasks.md) |
| AGT-04 | tasks abandoned by an agent to return to the queue | M2 | [AGT](AGT-agent-tasks.md) |
| AGT-05 | tasks nobody claims to fall back to the API after a while | M2 | [AGT](AGT-agent-tasks.md) |
| AGT-06 | results from agents marked as agent-reported | M2 | [AGT](AGT-agent-tasks.md) |

### 安全與維運

| 故事 | 想做到的事（I want …） | 里程碑 | 規格 |
|---|---|---|---|
| AUTH-01 | every instance to require a password | M1 | [AUTH](AUTH-access.md) |
| AUTH-02 | repeated wrong passwords to be slowed down | M1 | [AUTH](AUTH-access.md) |
| AUTH-03 | tokens limited to specific scopes | M1 | [AUTH](AUTH-access.md) |
| AUTH-04 | revoke a token or let it expire | M1 | [AUTH](AUTH-access.md) |
| AUTH-05 | the MCP endpoint to reject requests from unexpected web pages | M2 | [AUTH](AUTH-access.md) |
| SEC-01 | know where each setting lives and which one wins | M1 | [SEC](SEC-config-secrets.md) |
| SEC-02 | settings that run commands to be changeable only in the config file | M1 | [SEC](SEC-config-secrets.md) |
| SEC-03 | API keys and cookies encrypted and never shown in full again | M1 | [SEC](SEC-config-secrets.md) |
| SEC-04 | rotate the master key and recover from losing it | M1 | [SEC](SEC-config-secrets.md) |
| SEC-05 | export and import my settings as YAML | M2 | [SEC](SEC-config-secrets.md) |
| SEC-06 | see every provider, test its connection, and read model deprecation notices | M1 | [SEC](SEC-config-secrets.md) |
| SEC-07 | see when each cookie last worked and whether it has expired | M1 | [SEC](SEC-config-secrets.md) |
| GRD-01 | feeds and URLs to be unable to reach my private network | M1 | [GRD](GRD-guards.md) |
| GRD-02 | allow specific private hosts such as my RSSHub or NAS | M1 | [GRD](GRD-guards.md) |
| GRD-03 | downloads limited in size and redirects | M1 | [GRD](GRD-guards.md) |
| GRD-04 | API and MCP clients limited to my media folders | M1 | [GRD](GRD-guards.md) |
| OPS-01 | back up and restore my instance with one command | M1 | [OPS](OPS-operations.md) |
| OPS-02 | a snapshot taken automatically before every database migration | M1 | [OPS](OPS-operations.md) |
| OPS-03 | health endpoints that Docker can use | M1 | [OPS](OPS-operations.md) |
| OPS-04 | update yt-dlp without rebuilding the image, and have a bad update rolled back automatically | M1 | [OPS](OPS-operations.md) |
| OPS-05 | the default Docker setup to be reachable only from my own machine | M1 | [OPS](OPS-operations.md) |

## 4. M3 畫面與筆記

驗收條件（plan 第 24 節）：產出的筆記通過依 `Video Notes/AGENTS.md` 寫成的自動檢查（NOTE-R7；檢查項目的來源見 NOTE 規格待決問題 N1）。

M3 共 6 項能力、16 則故事，跨規格的決定（步驟順序、預設值、資源類別、產出、vault 與 hook 的設定位置）見 [ADR-0020](../adr/0020-m3-stages-notes-and-vault.md)。下列每則故事後的「規格」行是追溯：規則 ID 與驗證它的場景檔。

### FRM：關鍵畫面

- **FRM-01** As the owner, I want key frames picked automatically from a video, so that my notes show what was on screen.
  - 候選畫面由 ffmpeg 場景變化偵測產生，並套用密度下限（至少每 N 秒一張）；用縮圖 RGB 差異與滑動視窗去重，純色畫面不會互相誤判為重複。
  - 張數為 `min(Y, ceil(X × 分鐘數), 依片長的上限)`；有意義的畫面不夠時可以少於下限，筆記中註明。
  - 擷取畫質為來源最高畫質，最高 1080p、寬 1920px，不放大。
  - 只有音訊的內容（Podcast）略過這個步驟，狀態為 `skipped`。
  - 規格：[FRM](FRM-frames.md)：FRM-R1～R4、R8、R9、R11；場景 [FRM-frames.feature](../../features/FRM-frames.feature)
- **FRM-02** As the owner, I want frames chosen either by rules or by a vision model, so that I can trade cost for quality.
  - 規則模式依場景分數與字幕提示詞（例如「看這裡」「如圖」）的距離加權。
  - vision LLM 模式送出加上編號的 3×3 拼圖，模型只回傳編號；回傳不存在的編號視為驗證失敗。
  - SponsorBlock 標記的片段內不選圖。
  - 規格：[FRM](FRM-frames.md)：FRM-R5～R7、R13；場景同上
- **FRM-03** As an agent, I want to list and fetch an item's frames through MCP, so that I can answer questions about what the video shows.
  - `list_frames` 回傳時間與編號；`get_frame` 以圖片內容回傳。需要 `read` scope。
  - 規格：[FRM](FRM-frames.md)：FRM-R10、R12；場景同上

### CHAP：自動章節

- **CHAP-01** As the owner, I want chapters for every video, so that long videos are easy to navigate.
  - 平台提供章節時直接採用，`chapters` 步驟為 `skipped`。
  - 沒有章節時由 LLM 產生；每個章節的起點是存在的 cue 編號，時間由程式換算。
  - 章節存成產出（`kind: chapters`），`get_item` 會一併回傳。
  - 規格：[CHAP](CHAP-chapters.md)：CHAP-R1～R8；場景 [CHAP-chapters.feature](../../features/CHAP-chapters.feature)

### SPB：SponsorBlock

- **SPB-01** As the owner, I want sponsor segments left out of my frames and notes, so that ads do not end up in my notes.
  - 依 SponsorBlock 取得片段並存成產出（`kind: sponsor_segments`）。
  - 片段內的 cue 不放進筆記的輸入，片段內不選圖；字幕檔本身不刪除這些 cue。
  - SponsorBlock 查詢失敗時視為沒有片段並記錄警告，不阻擋後續步驟。
  - 規格：[SPB](SPB-sponsorblock.md)：SPB-R1～R8；場景 [SPB-sponsorblock.feature](../../features/SPB-sponsorblock.feature)

### NOTE：筆記

- **NOTE-01** As the owner, I want notes generated from a template where the model only writes the body, so that every note has the same reliable structure.
  - frontmatter、影片資訊表、SRT 連結、檔名與資料夾規則都由模板產生。
  - frontmatter 含 `notesteme_id` 與處理狀態。
  - 預設模板依 `Video Notes/AGENTS.md`；規則寫成可調整的設定。
  - 規格：[NOTE](NOTE-notes.md)：NOTE-R1、R2、R8、R9、R13；場景 [NOTE-notes.feature](../../features/NOTE-notes.feature)
- **NOTE-02** As the owner, I want every heading in a note to link to the right moment, so that I can jump back to the source.
  - LLM 以 cue 編號引用時間，程式換成時間戳連結；引用不存在的編號視為驗證失敗。
  - 規格：[NOTE](NOTE-notes.md)：NOTE-R3、R4；場景同上
- **NOTE-03** As the owner, I want notes written from the corrected original transcript in the language I choose, so that translation errors do not pile up.
  - 輸入以校正後的原文為主，譯文為選配；輸出語言由 `note.lang` 決定，預設是第一個目標語言。
  - 超過 1 小時的影片用 map-reduce 產生。
  - 規格：[NOTE](NOTE-notes.md)：NOTE-R5、R6、R10；場景同上
- **NOTE-04** As the owner, I want each note checked automatically after it is written, so that broken notes are caught before I open them.
  - 檢查標題都有時間戳、引用的圖片檔都存在、frontmatter 欄位齊全；不通過時顯示哪一項失敗。
  - 規格：[NOTE](NOTE-notes.md)：NOTE-R7；場景同上（含 `@ui @manual` 的筆記分頁）
- **NOTE-05** As the owner, I want to edit note templates and preview them on a real item, so that I can change the layout without code.
  - 規格：[NOTE](NOTE-notes.md)：NOTE-R11、R12；UI [筆記模板](../ui/pages.md#10-筆記模板)；場景同上
- **NOTE-06** As an agent, I want to fetch a note through MCP, so that I can reuse it in my answers.
  - `get_note` 需要 `read` scope。
  - 規格：[NOTE](NOTE-notes.md)：NOTE-R14；場景同上

### GEM：Gemini 直接生成

- **GEM-01** As the owner, I want Gemini to write a note straight from a YouTube URL, so that I can skip downloading and transcription when that is good enough.
  - 不下載媒體；產出套用同一份筆記模板與自動檢查。
  - 超出 Gemini 限制（片長、非公開影片）時顯示原因，並建議改用一般流程。限制內容依 M0 結果。
  - 規格：[GEM](GEM-gemini.md)：GEM-R1～R7；場景 [GEM-gemini.feature](../../features/GEM-gemini.feature)

### VAULT：輸出到 Vault 與 hook

- **VAULT-01** As the owner, I want notes written into my Obsidian vault folder, so that they appear where I already keep my notes.
  - `fs` adapter：處理 NTFS 不允許的字元；圖片依命名規則放在筆記旁。
  - 規格：[VAULT](VAULT-vault-output.md)：VAULT-R1～R5、R12；UI [設定](../ui/pages.md#12-設定)；場景 [VAULT-vault-output.feature](../../features/VAULT-vault-output.feature)
- **VAULT-02** As the owner running on a VPS, I want notes pushed to my vault's git repository, so that I get them on every device.
  - `git` adapter 只 commit 這次產出的檔案再 push；push 失敗時歸類為 `retryable`（展開時依錯誤種類拆開：網路類 `retryable`、non-fast-forward 與認證失敗 `needs_action`，見 VAULT 待決問題 V2）。
  - 規格：[VAULT](VAULT-vault-output.md)：VAULT-R7～R9；場景同上
- **VAULT-03** As the owner, I want notes I have edited never to be overwritten, so that my own changes are safe.
  - 依 `outputs` 記錄的 hash 判斷；檔案被改過時改寫成新檔名或略過，並在 UI 提示（ADR-0010）。
  - 規格：[VAULT](VAULT-vault-output.md)：VAULT-R6；場景同上
- **VAULT-04** As the owner, I want to run my own command after publishing, so that I can trigger other tools.
  - hook 只能在設定檔定義，以 argv 陣列執行並套用逾時、輸出上限與 `env_allowlist`（ADR-0014）。
  - 規格：[VAULT](VAULT-vault-output.md)：VAULT-R10、R11；場景同上

## 5. M4 追蹤與部署

驗收條件（plan 第 24 節）：VPS 上訂閱 3 個頻道，無人值守運作一週。這是真實環境的人工驗收，自動化場景只能驗證排程、告警與通知的規則（OPS-R11～R21、SUB-R9～R12）。

M4 共 13 項能力（9 份新規格，SRC、LIB、AUTH、OPS 另加 M4 章節）、29 則故事，跨規格的決定見 [ADR-0021](../adr/0021-m4-scheduler-subscriptions-and-outbox.md)。媒體檔保留策略（第 7 節）沒有排入 M4（ADR-0021 第 8 點）。

### SUB：訂閱

- **SUB-01** As the owner, I want to subscribe to channels, playlists, Bilibili collections and podcast feeds, so that new content is processed without me.
  - 規格：[SUB](SUB-subscriptions.md)：SUB-R1～R3、R5、R11～R13；場景 [SUB-subscriptions.feature](../../features/SUB-subscriptions.feature)
- **SUB-02** As the owner, I want to choose how far back a subscription goes and what it skips, so that I only pay for what I care about.
  - 回補範圍：只抓新的、最近 N 部、或全部。
  - 過濾：片長、Shorts、直播、標題 regex。
  - 垃圾桶中與已永久刪除（有 tombstone）的影音不會被重新加入（DEL-R11）。
  - 規格：[SUB](SUB-subscriptions.md)：SUB-R2～R5；場景 [SUB-subscriptions.feature](../../features/SUB-subscriptions.feature)
- **SUB-03** As the owner, I want a dry run of a subscription, so that I can see what it would pick up before it costs anything.
  - 列出會處理與會略過的項目及原因；不建立任何影音。
  - 規格：[SUB](SUB-subscriptions.md)：SUB-R6；場景 [SUB-subscriptions.feature](../../features/SUB-subscriptions.feature)
- **SUB-04** As the owner, I want to import my subscriptions from OPML and Google Takeout, so that I do not re-enter them by hand.
  - 不需要 OAuth；重複的訂閱只建立一次。
  - 規格：[SUB](SUB-subscriptions.md)：SUB-R7、R11；場景 [SUB-subscriptions.feature](../../features/SUB-subscriptions.feature)
- **SUB-05** As the owner, I want live streams and premieres processed only after the recording is available, so that I do not get half a transcript.
  - 規格：[SUB](SUB-subscriptions.md)：SUB-R8；場景 [SUB-subscriptions.feature](../../features/SUB-subscriptions.feature)
- **SUB-06** As the owner, I want subscription checks spread out and rate-limited, so that the platforms do not block me.
  - 排程加隨機延遲；遵守各平台的 rate limit。
  - 規格：[SUB](SUB-subscriptions.md)：SUB-R9～R10、R12；場景 [SUB-subscriptions.feature](../../features/SUB-subscriptions.feature)
- **SUB-07** As an agent, I want to list and create subscriptions through MCP, so that I can follow a channel for the owner.
  - `list_subscriptions` 需要 `read`；`create_subscription` 需要 `admin`。
  - 規格：[SUB](SUB-subscriptions.md)：SUB-R13；場景 [SUB-subscriptions.feature](../../features/SUB-subscriptions.feature)

### SRC-10：事後出現更好的來源

- **SRC-10** As the owner, I want to be told when a better transcript source appears after I processed a video, so that I can upgrade it.
  - 發布後 7 天內，採用較低優先來源的影音會定期重新 `Probe`；同時重新查詢 SponsorBlock。
  - 預設只通知；處理方案可改成自動重跑。重跑前顯示預估的 LLM 費用。
  - 規格：[SRC](SRC-transcript-sources.md)：SRC-R18～R23；場景 [SRC-better-source.feature](../../features/SRC-better-source.feature)

### DIG：摘要與彙整

- **DIG-01** As the owner, I want new subscription items summarised and sent to me as a daily or weekly digest, so that I can decide what is worth a full run.
  - 規格：[DIG](DIG-digest.md)：DIG-R1～R5、R8～R11；場景 [DIG-digest.feature](../../features/DIG-digest.feature)
- **DIG-02** As the owner, I want a profile that pauses after the summary, so that only the items I pick go on to cost more.
  - 只有在彙整或 UI 中勾選的影音才繼續完整流程；沒選的不再產生費用。
  - 規格：[DIG](DIG-digest.md)：DIG-R1、R6～R7、R10；場景 [DIG-digest.feature](../../features/DIG-digest.feature)

### COST：成本與預算

- **COST-01** As the owner, I want every API call recorded with its cost, so that I know exactly where my money goes.
  - 帳本細到每次呼叫，包括重試與驗證失敗，並保存當時的單價；provider 回報金額時優先採用。
  - Agent 模式記為 `unknown`，不計入預算。
  - 規格：[COST](COST-cost-budget.md)：COST-R1～R3、R7、R14～R16；場景 [COST-cost-budget.feature](../../features/COST-cost-budget.feature)
- **COST-02** As the owner, I want a monthly budget and a per-item cap, so that costs never run away.
  - 開始前估算並記為 `reserved`；預算以實際加預留計算，平行任務不會一起超支。
  - 超過時步驟為 `blocked`（原因 `budget_blocked`），並通知。
  - 規格：[COST](COST-cost-budget.md)：COST-R4～R11、R16；場景 [COST-cost-budget.feature](../../features/COST-cost-budget.feature)
- **COST-03** As the owner, I want to override built-in prices and be reminded when they are stale, so that estimates stay accurate.
  - 計價表超過 90 天沒有確認就在 UI 提醒。
  - 規格：[COST](COST-cost-budget.md)：COST-R12～R13、R16；場景 [COST-cost-budget.feature](../../features/COST-cost-budget.feature)

### NOTIFY：通知

- **NOTIFY-01** As the owner, I want notifications through ntfy, Discord, Telegram or a webhook, so that I hear about results where I already am.
  - 時機：完成、失敗、需要你處理、預算將用完、訂閱彙整、無人值守告警；每個管道可以分別選擇。
  - webhook 走 GRD 的對外請求防護。
  - 規格：[NOTIFY](NOTIFY-notifications.md)：NOTIFY-R1～R13；場景 [NOTIFY-notifications.feature](../../features/NOTIFY-notifications.feature)

### BATCH：Batch API

- **BATCH-01** As the owner, I want non-urgent LLM work sent through provider batch APIs, so that background processing costs less.
  - 只用於背景優先序的任務；等待結果時狀態為 `waiting`，逾時後改走一般 API。
  - 規格：[BATCH](BATCH-batch-api.md)：BATCH-R1～R11；場景 [BATCH-batch-api.feature](../../features/BATCH-batch-api.feature)

### GLOS：術語審核

- **GLOS-01** As the owner, I want to review the term candidates collected during correction, so that my glossary grows without manual typing.
  - 可以接受、修改或拒絕；接受後加入頻道或全域的詞彙表，之後的校正使用新詞彙。
  - 規格：[GLOS](GLOS-glossary-review.md)：GLOS-R1～R7；場景 [GLOS-glossary-review.feature](../../features/GLOS-glossary-review.feature)

### ENTRY：其他新增入口

- **ENTRY-01** As the owner, I want a bookmarklet that sends the current page to Notesteme, so that adding a video takes one click.
  - 規格：[ENTRY](ENTRY-entry-points.md)：ENTRY-R1～R2、R7；場景 [ENTRY-entry-points.feature](../../features/ENTRY-entry-points.feature)
- **ENTRY-02** As the owner, I want to share a link to Notesteme from my phone, so that I can add videos while browsing on mobile.
  - PWA 的 share target；未登入時先導向登入頁，登入後繼續新增。
  - 規格：[ENTRY](ENTRY-entry-points.md)：ENTRY-R3～R7；場景 [ENTRY-entry-points.feature](../../features/ENTRY-entry-points.feature)

### BND：Bundle 匯出與匯入

- **BND-01** As the owner, I want to export an item with its transcripts, notes and provenance as a bundle, so that I can move it to another instance.
  - 規格：[BND](BND-bundles.md)：BND-R1～R3、R8；場景 [BND-bundles.feature](../../features/BND-bundles.feature)
- **BND-02** As the owner, I want bundles imported safely, so that a malicious bundle cannot harm my instance.
  - 拒絕絕對路徑、`..` 與 symlink；限制解壓後的總大小、檔案數與壓縮比。
  - 逐檔驗證 `sha256`，manifest 用 JSON schema 驗證；來源紀錄標記 `imported_from`。
  - 規格：[BND](BND-bundles.md)：BND-R3～R10；場景 [BND-bundles.feature](../../features/BND-bundles.feature)

### SRCH：全文搜尋

- **SRCH-01** As the owner, I want to search inside every transcript, so that I can find the video where something was said.
  - FTS5 trigram；少於 3 個字的查詢改用掃描。只索引現行版本的校正與譯文字幕。
  - 結果附時間戳，可以直接跳到那一句。MCP `search` 支援 `mode: fulltext`。
  - 規格：[SRCH](SRCH-search.md)：SRCH-R1～R10；場景 [SRCH-search.feature](../../features/SRCH-search.feature)

### LIB-08～LIB-10：批次操作、總覽與儲存檢視

- **LIB-08** As the owner, I want to select many items and act on them at once, so that I can rerun, pause or delete in bulk.
  - 動作前顯示影響的數量與預估費用。
  - 批次刪除套用 DEL 的所有規則；確認對話框合計顯示 DEL-R3 的影響範圍。
  - 展開時決定（2026-11-23，LIB 規格待決問題 L-M1，待 owner 追認）：M4 的批次動作不含暫停；故事中的 pause 延到 M4 之後。
  - 規格：[LIB](LIB-library.md)：LIB-R13～R20、R23；場景 [LIB-bulk-overview.feature](../../features/LIB-bulk-overview.feature)
- **LIB-09** As the owner, I want an overview page, so that I see what needs my attention first.
  - 顯示需要處理的項目、最近完成、本月花費與磁碟用量。
  - 規格：[LIB](LIB-library.md)：LIB-R21～R23；場景 [LIB-bulk-overview.feature](../../features/LIB-bulk-overview.feature)
- **LIB-10** As the owner, I want to save my library filters as named views, so that I can return to a set of items in one click.
  - 補上的故事（2026-11-25，依 /goal 由實作者暫定，待 owner 追認）：UI pages.md §3 寫 M4 有儲存檢視，但原本沒有任何故事接手。
  - 規格：[LIB](LIB-library.md)：LIB-R26～R28；場景 [LIB-library.feature](../../features/LIB-library.feature)

### AUTH-06～08：進階登入與 token 管理

- **AUTH-06** As the owner, I want to sign in through my reverse proxy's authentication, so that I reuse the login I already have.
  - 只信任設定中列出的 proxy 位址送來的使用者標頭。
  - 規格：[AUTH](AUTH-access.md)：AUTH-R12～R15；場景 [AUTH-proxy-oidc.feature](../../features/AUTH-proxy-oidc.feature)
- **AUTH-07** As the owner, I want to sign in with OIDC, so that I can use my identity provider.
  - 規格：[AUTH](AUTH-access.md)：AUTH-R16～R20、R22；場景 [AUTH-proxy-oidc.feature](../../features/AUTH-proxy-oidc.feature)
- **AUTH-08** As the owner, I want to create, revoke and set expiry for tokens in the UI, so that I do not need the CLI for it.
  - 規格：[AUTH](AUTH-access.md)：AUTH-R20～R22；場景 [AUTH-proxy-oidc.feature](../../features/AUTH-proxy-oidc.feature)

### OPS-06～08：無人值守運作

- **OPS-06** As the owner, I want a backup taken every day and the last seven kept, so that I never need to remember.
  - 規格：[OPS](OPS-operations.md)：OPS-R11～R13；場景 [OPS-unattended.feature](../../features/OPS-unattended.feature)
- **OPS-07** As the owner, I want to be alerted when something quietly stops working, so that problems do not go unnoticed for days.
  - 排程超過 N 小時沒有成功、任務超過 N 小時沒有進展、磁碟用量超過門檻、cookie 太久沒有成功使用、yt-dlp 失敗率突然升高。
  - 規格：[OPS](OPS-operations.md)：OPS-R12～R18；場景 [OPS-unattended.feature](../../features/OPS-unattended.feature)
- **OPS-08** As the owner, I want a VPS compose profile with HTTPS, so that I can expose the instance safely.
  - `vps` profile 內建 Caddy；未設定密碼或 token 時拒絕啟動。
  - 規格：[OPS](OPS-operations.md)：OPS-R19～R21；場景 [OPS-unattended.feature](../../features/OPS-unattended.feature)

## 6. M5 Hub

驗收條件（plan 第 24 節）：可以分享字幕與筆記，也能用別人的字幕重新生成（HSHR 的 `@manual` 端到端場景）。

2026-11-23 已展開成 4 份能力規格與 `.feature` 檔（`@M5`，預設 tag 不執行）。故事依「被測系統」分到規格：Hub 本身的行為用程序內的真 Hub，Client 的行為用假 Hub（ADR-0030）。跨規格的決定見 [ADR-0030](../adr/0030-hub-deployment-and-storage.md)（部署、儲存、測試替身）、[ADR-0031](../adr/0031-hub-identity-permissions-and-content.md)（身分、權限、內容模型與資料表）、[ADR-0032](../adr/0032-client-hub-protocol-and-privacy.md)（協定、送出的資料、隱私）。條列的驗收標準保留作為展開前的原始描述，以規格為準。

| 規格 | 故事 | 被測系統 |
|---|---|---|
| [HACC：Hub 帳號、權限與下架](HACC-hub-accounts.md) | HUB-01、HUB-07 | 真 Hub |
| [HREP：Hub 內容倉庫、篩選與社群](HREP-hub-repository.md) | HUB-02（Hub 端）、HUB-03、HUB-05（Hub 端）、HUB-06 | 真 Hub |
| [HSHR：Client 的分享、重新生成與 ASR 前查詢](HSHR-hub-sharing.md) | HUB-02（Client 端）、HUB-04、HUB-05 | Client＋假 Hub |
| [HMCP：Hub MCP server](HMCP-hub-mcp.md) | HUB-08 | 真 Hub |


- **HUB-01** As a hub member, I want to sign in with Google, GitHub or Linux.do and link several of them to one account, so that I am not locked to one provider.
- **HUB-02** As the owner, I want to share my corrected subtitles and notes to the Hub with their provenance, so that others can reuse my work.
  - 預設只分享文字衍生物，畫面預設不公開；上傳時聲明授權。
  - 非公開影音與用 cookie 取得的字幕預設禁止上傳。
- **HUB-03** As a hub member, I want to filter shared subtitles by platform, channel, language and models, so that I find a version I trust.
  - Agent 自行回報的模型（`model_verified: false`）另外標示，篩選時可以排除。
- **HUB-04** As the owner, I want to regenerate a video in my client from someone else's raw subtitles, so that I skip paying for transcription.
  - 記錄衍生關係；用內容 hash 去重。
- **HUB-05** As the owner, I want my client to check the Hub before paying for ASR, so that I reuse what already exists.
  - 預設關閉；開啟時說明會送出哪些影片 ID。
- **HUB-06** As a hub member, I want to vote on and report shared versions, so that good versions rise and bad ones are removed.
- **HUB-07** As a hub admin, I want to set who can see what and handle takedown requests, so that the Hub stays within the rules（ADR-0012）.
- **HUB-08** As an agent, I want a Hub MCP server, so that I can search shared subtitles directly.
  - 只能讀；PAT 的權限決定看得到的 tool（HMCP）。

## 7. 尚未排入里程碑

- 說話者辨識、語意搜尋、Telegram bot（plan 第 3.4 節）。
- Postgres＋Redis backend（plan 第 26 節，M4 之後視需要）。
- **媒體檔保留策略**（plan 第 14 節）：N 天後刪影片、只保留選中的畫面、UI 顯示磁碟用量。plan 沒有排入里程碑；M4 規格展開時決定不排入 M4，LIB-09 只顯示磁碟用量（ADR-0021 第 8 點、LIB 規格待決問題）。只套用在可以重新下載的媒體快取，不包括上傳的原始檔。

## 8. M0 待驗證的問題

下列場景標記 `@pending-m0`：規則已經寫好，但門檻或做法要等 M0 量測後才定案。M0 結束時把結論回寫到對應的規格與 ADR，再移除標記。

| 問題 | 暫定做法 | 影響的規則與場景 |
|---|---|---|
| 品質檢查的門檻 | 涵蓋率 50%、重複句 30%、音樂標記 80% | SRC-R4 |
| Whisper 幻覺過濾的門檻 | `no_speech_prob` > 0.6 且 `compression_ratio` > 2.4 | SRC-R13 |
| 校正的相似度計算方式與門檻 | 字元層級 normalized Levenshtein，門檻 0.6 | COR-R2、COR-R3 |
| 斷點標記能否可靠對回文字 | 模糊比對，容許標點與空白差異 | SEG-R3 |
| `when_needed` 的長度與時長門檻 | 依 plan 第 8.5 節的字幕規則 | SEG-R10 |
| 句群翻譯的切分標記會不會被模型改寫 | 使用 `⟨|⟩`；不可靠時改用 JSON 陣列 | TRN-R4 |
| 關鍵畫面的場景門檻、密度下限、去重門檻與提示詞加權（M3） | 0.3、60 秒、0.05（視窗 5 張）、+0.5（±10 秒），以 `frames.py` 的參數校正 | FRM-R2、FRM-R3、FRM-R5 |
| Gemini 直接讀 URL 的限制（M3） | 只讀 `public`、片長上限 1 小時 | GEM-R3、GEM-R7 |

另外幾項 M0 結果會影響規格，但沒有對應的 `@pending-m0` 場景：

- Groq 是否提供 word 級時間戳：影響 SRC 的時間粒度。
- Bilibili AI 字幕能否用 cookie 取得：影響 SRC 的 `bilibili-ai` 來源。
- Bilibili 嵌入播放器能否控制播放位置：影響 LIB-03 的點句跳轉。
- OpenCC Go port 是否可行：影響 TRN 的 `opencc` executor。
- VPS 上有無 PO Token provider 的差異：影響 OPS-08 的預設 sidecar。
- Gemini 直接讀 URL 的限制：影響 GEM-01。
- LLM chunk 的 `max_tokens` 與重疊句數：影響 LLM 的預設值。

# Notesteme 計畫

> 狀態：草案 v0.4（2026-11-22，修訂紀錄見第 28 節）
> 相關文件：[ADR 索引](adr/README.md)、[UI 規格](ui/README.md)、[規格與驗收](specs/README.md)

## 1. 背景與目標

現有的 `watch` SKILL（`D:\Workspace\@Career\.agents\skills\watch`）讓 Agent 驅動整個流程，從下載、轉錄、抽幀到寫筆記都交給它。實際使用時有兩個問題：

- 轉錄效率低，每次都要 Agent 一步步推進。
- 常卡在下載、轉錄這類非核心問題上，例如 yt-dlp 403、缺少 binary、模型安裝。

Notesteme 把流程拆成兩段：

- **確定性 workflow**：下載、取得字幕、轉錄、輸出。不需要 Agent，可以排程、重試、續跑。
- **LLM 步驟**：斷句、校正、翻譯、選圖、寫筆記。可以用內建 API 執行，也可以透過 MCP 交給 Agent。

### 目標

1. 貼上網址或匯入檔案後，自動產出字幕（SRT／VTT／ASS），失敗時給出可行動的原因。
2. 由 LLM 斷句、校正、翻譯。Prompt 可切換，模型可依步驟分別設定。
3. 選配：抽取關鍵畫面、產生 Obsidian 筆記，結構與提示詞都可自訂。
4. 定時追蹤頻道，有新內容就自動處理。
5. 所有功能都可以從 Web UI 操作，CLI 和 MCP 的能力也相同。
6. 以 Docker 部署，WSL 和 VPS 都能跑。
7. 後期加入 Hub 資源站，用來分享字幕和筆記，並保留完整的來源紀錄。

### 非目標（目前不做）

- 把字幕燒錄進影片、剪輯影片。
- 人工校對編輯器：校對只靠 LLM，UI 只提供唯讀對照。在外部工具編輯過的字幕可以匯入成新版本（見第 11 節）。
- 多個 instance 之間的狀態同步，延到 Hub 階段（見 [ADR-0011](adr/0011-defer-multi-instance-sync.md)）。

## 2. 名詞

| 名詞 | 說明 |
|---|---|
| 影音（Item） | 一部影片、一集 Podcast，或一個本機檔案。Bilibili 分P 各算一個。 |
| 步驟（Stage） | pipeline 中的一個處理單元，例如 `transcript`、`correct`。 |
| 產出（Artifact） | 步驟產生的檔案或資料，例如原始字幕、校正字幕、摘要、筆記。每個產出都帶來源紀錄。 |
| 來源紀錄（Provenance） | 產出是由哪個 provider、model、prompt 版本、參數、上游產出生成的。 |
| 處理方案（Profile） | 一組可重複套用的 pipeline 設定：要跑哪些步驟、字幕來源順序、provider、目標語言、抽幀上限、筆記模板、輸出目標。 |
| 訂閱（Subscription） | 定期檢查新內容的來源：頻道、播放清單、合集或 Podcast feed，各自帶有排程、過濾規則與處理方案。 |
| 輸出目標（Output target） | 字幕與筆記寫出去的位置：vault、本機影片旁（sidecar）或指定資料夾。只有實際寫出去的檔案才會被記錄。 |
| 任務（Task） | 佇列中的一個執行單位，可以由內建 worker 執行，也可以由 Agent 領取。LLM 步驟的每個 chunk 是一個任務。 |
| Bundle | 可攜式的產出包，內含 manifest 與字幕、筆記等檔案，用於匯出、匯入和分享。 |

## 3. 範圍

### 3.1 Client

| 功能 | 優先級 | 里程碑 |
|---|---|---|
| 匯入 YouTube、Bilibili、Podcast RSS、本機檔案 | P0 | M1 |
| 取得平台字幕，或用 ASR 轉錄（來源可擴充） | P0 | M1 |
| 字幕語言優先順序與 fallback 順序 | P0 | M1 |
| 字幕品質檢查，不合格時自動換下一個來源 | P0 | M1 |
| SRT／VTT 即時渲染與下載 | P0 | M1 |
| 任務佇列與並行控制 | P0 | M1 |
| Web UI 單一密碼登入 | P0 | M1 |
| 備份、還原，migration 前自動快照 | P0 | M1 |
| LLM 斷句、校正、翻譯，可切換 prompt | P0 | M2 |
| MCP（Agent 領任務、查詢產出） | P0 | M2 |
| ASS 雙語字幕，有預設樣式也可自訂 | P1 | M2 |
| 匯入在外部工具編輯過的字幕 | P1 | M2 |
| 本機影片旁的字幕輸出（sidecar） | P1 | M2 |
| Prompt caching | P1 | M2 |
| 詞彙表回饋：校正時提出術語候選，核可後寫入頻道詞彙表 | P1 | M2 收集，M4 審核 UI |
| 關鍵畫面擷取與選圖（每分鐘 X 張、整部 Y 張） | P1 | M3 |
| Obsidian 筆記（自訂模板與提示詞） | P1 | M3 |
| 自動章節（平台沒有章節時） | P1 | M3 |
| SponsorBlock 片段標記與略過（YouTube） | P1 | M3 |
| Gemini 多模態引擎（直接看影片） | P1 | M3 |
| 頻道、播放清單、合集訂閱與排程 | P0 | M4 |
| 訂閱匯入：OPML、YouTube 訂閱（Google Takeout） | P1 | M4 |
| 訂閱摘要與每日／每週彙整通知 | P1 | M4 |
| 依狀態與頻道分類、篩選、全文搜尋 | P0 | M1 基本版，M4 完整版 |
| 成本估算、預算上限、通知 | P1 | M4 |
| Batch API（回補舊內容） | P1 | M4 |
| 反向代理驗證、OIDC 登入、token 管理 UI | P1 | M4 |
| Bookmarklet 與 PWA share target | P2 | M4 |
| 本地 ASR 入口（例如 WhisperX） | P2 | M1 介面，實作隨時可補 |
| Postgres＋Redis backend（選配） | P2 | M4 之後，視需要 |

### 3.2 Hub（M5）

- 登入方式：Google、GitHub、Linux.do OAuth。一個帳號可以綁定多個登入方式。
- 分享校對後的字幕和產生的筆記。每份都保存來源紀錄：轉錄模型、校對模型、翻譯模型、prompt 版本、語言。
- 可依平台、頻道、語言、轉錄模型、校對模型篩選；也可以拿別人的原始字幕，在自己的 Client 重新生成。
- 權限由管理員設定。預設只有登入者能看，也可以依 trust level 或群組調整（見 [ADR-0012](adr/0012-hub-access-control.md)）。
- 提供 MCP。

Client 必須能獨立擁有分享以外的所有功能，Hub 只是資源站，UI 風格也和 Client 不同。

### 3.3 MVP 支援平台

| 平台 | 探索新內容 | 字幕 | 媒體 | 時間連結 |
|---|---|---|---|---|
| YouTube | RSS、Supadata、`yt-dlp --flat-playlist` | 人工字幕、自動字幕（json3）、Supadata、ASR | yt-dlp | `&t=137s` |
| Bilibili | `yt-dlp` 抓 space 頁、選配 RSSHub | CC 字幕；AI 字幕需登入 cookie；ASR | yt-dlp | `?p=2&t=137` |
| Podcast RSS | RSS | `<podcast:transcript>`、ASR | 直接 HTTP 下載音檔 | `#t=137` 或純文字時間 |
| 本機檔案 | inbox 資料夾、上傳 | 外掛字幕檔、內嵌字幕軌、ASR | 本機 | 純文字時間 |

### 3.4 未來可能（未排入里程碑）

- Telegram bot：傳網址給 bot 就能新增影音。
- 說話者辨識：用 WhisperX，或支援說話者辨識的 ASR API。cues 格式已預留 `speaker` 欄位。
- 語意搜尋：用 embedding 搜尋「哪部影片講過 X」。MCP 的 `search` 已預留 `mode` 參數。

## 4. 系統架構

```
┌──── notesteme（Client：單一 Go binary／Docker） ──────────────────────────────────────────┐
│ 入口：Web UI ｜ CLI ｜ MCP（stdio、Streamable HTTP）｜ Scheduler                          │
│       └──────────── 全部走同一層 Service／HTTP API ────────────┘                          │
│                                                                                           │
│ Pipeline Engine（DAG；步驟冪等、可續跑、以 chunk 為單位快取）                             │
│   確定性  discover → metadata → transcript → normalize                                    │
│   字幕    normalize → summary → chapters → segment → correct → translate                  │
│   畫面    frames.candidates → frames.select                                               │
│   筆記    字幕＋章節＋畫面 → note                                                         │
│   輸出    publish → vault（fs／git）｜sidecar｜folder                                     │
│           SRT／VTT／ASS 由 cues JSON 即時渲染，不存成產出                                 │
│   LLM 步驟的 executor：api｜agent｜off；translate 另有 opencc；M4 加 batch                │
│                                                                                           │
│ Task Queue（租約、並行上限、rate limit）   Store：SQLite（預設）｜Postgres＋Redis（選配） │
│ Artifact Store：檔案＋來源紀錄             External Tools：yt-dlp、ffmpeg、deno           │
└─────────────────────────────────────────────┬─────────────────────────────────────────────┘
                                              │ bundle（manifest + cues JSON + 筆記）
                      notesteme-hub（M5：OAuth、權限、版本、篩選、MCP）
```

設計原則：

- **確定性的事交給程式**，LLM 只處理需要理解語意的部分（[ADR-0002](adr/0002-deterministic-workflow-and-llm-executors.md)）。
- **時間戳只取自來源**，LLM 不輸出時間；章節與筆記也只引用 cue 編號（[ADR-0003](adr/0003-timestamps-from-source.md)）。
- **字幕只有一種真實格式**：notesteme cues JSON。SRT、VTT、ASS 都是即時渲染的結果。
- **每個產出都記錄來源**，為之後的 Hub 分享預留（[ADR-0005](adr/0005-provenance-and-bundle-format.md)）。
- **API-first**：UI、CLI、MCP 的能力一致（[ADR-0008](adr/0008-web-ui-architecture.md)）。
- **會執行命令的設定只能寫在設定檔**，API 與 MCP 改不到（[ADR-0013](adr/0013-config-sources-and-secrets.md)）。

## 5. Pipeline

| 步驟 | 性質 | 輸入 | 輸出 | 說明 |
|---|---|---|---|---|
| `discover` | 確定性 | 訂閱 | 影音清單 | RSS、Supadata、yt-dlp；套用過濾規則 |
| `metadata` | 確定性 | URL | 中繼資料 | 標題、頻道、發布日、片長、章節、可用字幕軌、原文語言、公開狀態（`availability`）；YouTube 另查 SponsorBlock 片段 |
| `transcript` | 確定性 | 中繼資料 | 原始字幕 | 依來源優先順序取得，通過品質檢查才採用（見第 6 節） |
| `normalize` | 確定性 | 原始字幕 | 正規化字幕 | 去除重複的滾動字幕、合併碎句、時間單調性檢查；中繼資料沒有原文語言時做語言偵測。產出不依賴目標語言 |
| `summary` | LLM | 正規化字幕 | 摘要、標題譯文 | 選配。可設定「摘要後暫停」，等使用者決定是否繼續 |
| `chapters` | LLM | 正規化字幕 | 章節 | 選配，平台沒有章節時才跑。LLM 回傳起點 cue 編號與標題，時間由程式換算 |
| `segment` | LLM | 正規化字幕 | 斷句字幕 | 只回傳斷點標記，時間由程式計算；人工字幕預設跳過 |
| `correct` | LLM | 斷句字幕 | 校正字幕 | 一對一校正，使用詞彙表；可一併提出術語候選 |
| `translate` | LLM 或 OpenCC | 校正字幕 | 譯文字幕 | 以句群為單位翻譯，時間不變；可有多個目標語言。中文只差字形時可用 `opencc` executor |
| `frames.candidates` | 確定性 | 影片 | 候選畫面 | 場景變化 + 密度下限 + 去重 |
| `frames.select` | LLM 或規則 | 候選畫面 | 選中畫面與圖說 | 用拼圖降低 image token |
| `note` | LLM + 模板 | 校正字幕、譯文（選配）、章節、畫面、中繼資料 | Markdown 筆記 | LLM 只寫內文並引用 cue 編號；結構與時間連結由程式產生 |
| `publish` | 確定性 | 字幕、筆記與附件 | 輸出目標中的檔案 | 寫入 vault（fs／git）、本機影片旁（sidecar）或指定資料夾，接著執行 hook |

- SRT、VTT、ASS 等格式不是步驟的產出。下載、API、MCP 取用時從 cues JSON 即時渲染；只有 `publish` 寫出去的檔案會記錄在 `outputs`（見第 11 節）。
- `summary` 設為「摘要後暫停」時，後續步驟要等使用者決定。
- 有章節（來自平台或 `chapters`）時，`segment`、`correct`、`translate` 依章節切 chunk。
- SponsorBlock 標記的片段不送 LLM 步驟，字幕在這些片段保留原文。
- **修訂（2026-11-23，ADR-0020，待 owner 追認）**：M3 的實際順序是 `metadata` → `transcript` → `normalize` → `segment` → `correct` → `translate` → `sponsorblock` → `chapters` → `frames` → `note` → `publish`。`chapters` 以校正後的字幕為輸入，產生的章節不參與 M2 的 chunk 切分；`frames.candidates`、`frames.select` 是 `frames` 步驟內的兩個任務；SponsorBlock 片段只排除在 `chapters`、`frames`、`note` 之外。
- **修訂（2026-11-23，ADR-0021，待 owner 追認）**：`discover` 不是影音的步驟，是訂閱層的排程工作，不建立 `stage_runs`（SUB 規格）。`summary` 加進 `pipeline.Stages`，位置在 `normalize` 之後、`segment` 之前，預設 `off`；「摘要後暫停」讓下一個步驟 `blocked`（`awaiting_user`），見 DIG 規格。

### 5.1 冪等與快取

- 每個步驟的快取鍵 = `(item, stage, impl_version, 上游 artifact hash, provider, model, prompt sha256, params sha256)`。快取鍵相同就直接重用，不會重複付費。
  - `impl_version` 是每個步驟宣告的常數，演算法的行為改變時遞增。
  - yt-dlp 等外部工具的版本只寫進來源紀錄，不放進快取鍵，否則每次更新工具都會讓快取失效。
- LLM 步驟以 chunk 為單位快取與重試：每個 chunk 是一個任務，快取鍵另外包含 chunk 內容與上下文的 hash。全部 chunk 完成後才組成步驟的產出；某個 chunk 失敗時只重跑那一個。
- 從任一步驟重跑時，會產生新版本的產出。舊版本標記 `superseded_by` 指向新版。
- 舊版本的保留：
  - 同一組 `(item, kind, variant, lang)` 預設保留最近 3 版。
  - 畫面只保留現行版本。
  - 被輸出目標或 bundle 引用的版本標記 `pinned`，不會被清除。
- 中斷之後從最後一個完成的 chunk 繼續。

### 5.2 錯誤分類

| 類別 | 範例 | 系統行為 | 步驟狀態 |
|---|---|---|---|
| `retryable` | 網路逾時、429、5xx | 指數退避重試，次數有上限 | 重試期間為 `pending`，用盡後為 `failed` |
| `wait` | 自動字幕還沒生成、直播還沒結束、首播未開始 | 設定 `not_before`，時間到再檢查。不消耗重試次數，但有截止時間（例如自動字幕最多等 24 小時），逾時就改用下一個來源 | `waiting` |
| `needs_action` | cookie 過期、會員限定、年齡限制、缺 API key | 停下來，在 UI 的「需要你處理」顯示修正方式 | `blocked` |
| `budget_blocked` | 超過月預算或單片上限 | 停下來，調整預算後可繼續 | `blocked` |
| `permanent` | 影片已刪除、地區封鎖且無替代方案 | 標記失敗，附原因 | `failed` |

### 5.3 狀態

每個步驟各自有狀態：

| 狀態 | 說明 |
|---|---|
| `pending` | 等待執行，或等待重試 |
| `running` | 執行中 |
| `waiting` | 等外部條件成立，例如自動字幕生成；附 `not_before` 與截止時間 |
| `awaiting_agent` | 等 Agent 透過 MCP 領取 |
| `blocked` | 需要使用者介入；附原因：`needs_action`、`budget_blocked`，或 `awaiting_user`（摘要後暫停） |
| `done` | 完成 |
| `skipped` | 處理方案設為 `off`，或條件不符（例如只有音訊的內容沒有畫面） |
| `failed` | 重試次數用盡，或發生 `permanent` 錯誤 |

影音的總狀態由各步驟推導，UI 可依任一步驟的狀態篩選。

## 6. 字幕來源與時間戳

### 6.1 MVP 來源

| 來源 | 時間粒度 | 條件與備註 |
|---|---|---|
| YouTube 人工字幕 | cue | 預設跳過 `segment` |
| YouTube 自動字幕（json3） | 近 word 級 | 新影片上傳後要等幾小時才有，可設等待時間 |
| Bilibili CC 字幕 | cue | 公開可取得 |
| Bilibili AI 字幕 | cue | 需要 `SESSDATA` cookie，過期時歸為 `needs_action`（M0 實測 yt-dlp 能否取得） |
| Podcast `<podcast:transcript>` | cue | Podcasting 2.0 標籤，可能是 SRT、VTT 或 JSON |
| 本機外掛或內嵌字幕 | cue | 用 ffprobe 偵測內嵌字幕軌 |
| Supadata | chunk | 不支援 Bilibili；分 `native`、`generate`、`auto` 三種模式；長片會回傳 job id 要輪詢 |
| Groq `whisper-large-v3-turbo` | word + segment（M0 確認） | OpenAI 相容 API；預設的 ASR fallback |
| OpenAI `whisper-1` | word + segment | 單檔 25MB 上限；**2027-02-26 停止服務**，不作為預設 |
| Mistral Voxtral 3 | segment | 要求 word 級會回 400 |
| Gemini 多模態 | model | 時間戳由模型生成，長片可能漂移；預設排在 ASR 後面 |
| 本地 ASR（WhisperX 等） | word | 透過 `command` 或 `openai-compatible` adapter 接入 |

擴充方式見 [ADR-0006](adr/0006-pluggable-transcript-sources.md)。

### 6.2 時間戳策略

詳見 [ADR-0003](adr/0003-timestamps-from-source.md)。摘要：

- 校正、翻譯不碰時間。翻譯以句群為單位，但譯文會依切分標記分回原本的 cue（見 8.4 節）。
- 合併相鄰的 cue 時，用第一句的 start、最後一句的 end。
- 只有在單一 segment 內切開時才需要推算時間。由 `split_timing` 控制：
  - `forbid`：不允許段內切分。
  - `when_needed`（預設）：只有超過長度或時長上限時才切。先用 `silencedetect` 找停頓點，找不到才依字數比例插值。
  - `free`：讓 LLM 自由斷句。
- `silencedetect` 需要音訊，是否為此下載由 `split_timing_audio` 控制：
  - `auto`：需要段內切分、又沒有 word 級時間時，只下載音訊。`wsl` profile 的預設值。
  - `never`：直接依字數插值。`vps` profile 的預設值，因為下載音訊一樣會觸發 YouTube 反爬。
- 章節與筆記中的時間也一樣：LLM 只引用 cue 編號，由程式換算時間。
- 每個 cue 記錄時間怎麼來的（`Timing`）：`source`、`merged`、`silence`、`interpolated`、`model`。

### 6.3 來源品質與語言偵測

- `transcript` 取得字幕後先做品質檢查。沒有通過就記錄原因、標記為 `rejected`，改用下一個來源。檢查項目：
  - 時間涵蓋率低於片長的一定比例。
  - 語言和預期不符。
  - 重複句或幻覺的比例過高。
  - 幾乎只有音樂或 `[Music]` 之類的標記。

  門檻在 M0 用樣本調整。
- 原文語言依序採用：平台中繼資料、ASR 回報的語言、文字語言偵測的結果。中英夾雜時記錄主要語言，並標記 `mixed`；校正 prompt 要容許夾雜。
- Podcast 可能有動態插入的廣告，每次下載的音檔長度不同。下載時記錄音檔的 hash 與實際長度；和 transcript 的長度差超過門檻時，UI 標示「時間可能偏移」。

### 6.4 事後出現更好的來源

- 影音發布後 7 天內，如果採用的是較低優先的來源（例如自動字幕或 ASR），會定期重新 `Probe`。
- 發現更好的來源（例如上傳者補上人工字幕）時，預設只通知，由使用者決定是否重跑；重跑要再付一次 LLM 費用。可以在處理方案中改成自動重跑。
- 這段期間也會重新查詢 SponsorBlock，因為片段常在發布幾天後才有人提交。

## 7. 語言

語言一律用 BCP-47 標記。設定的覆寫順序：全域 → 處理方案 → 頻道 → 單次執行。

```yaml
language:
  targets: [zh-Hant-TW]              # 可設多個，第一個是主要譯文；也可以是 zh-Hans-CN、en
  caption_priority:                  # 依序嘗試，命中第一個就用
    - { lang: original, kind: manual }
    - { lang: original, kind: auto }
    - { lang: zh-Hans,  kind: manual }   # 命中後依目標語言做簡繁轉換
  allow_platform_translated: false   # 平台機器翻譯的字幕預設排除
  fallback: [supadata, groq]         # 平台都沒有字幕時才用，順序可調
  variant_strategy: convert          # 原文與目標只差中文字形時：convert 或 localize（見下方）
```

- OpenCC 只在目標語言是中文變體時套用，依地區選轉換表：`s2twp`、`s2hk` 或 `t2s`。
- 原文是中文、目標是另一種中文字形時（例如原文是简中、目標是 `zh-Hant-TW`），依 `variant_strategy` 處理。可以在處理方案、頻道或單次執行中切換：

  | 選項 | 流程 | 適合 |
  |---|---|---|
  | `convert`（預設） | `translate` 改用確定性的 `opencc` executor 轉字形，不呼叫 LLM | 成本最低；用詞差異不大的內容 |
  | `localize` | `translate` 由 LLM 執行，用在地化 prompt（預設為 `localize-zh-hant-tw`）調整成目標地區的用詞 | 想要遣詞用字符合目標地區習慣的內容 |

- 兩種方式的 `normalize`、`segment`、`correct` 都在原文字形上進行，產出不依賴目標語言。多個目標語言（例如 `zh-Hant-TW` 和 `en`）共用同一份校正字幕。詞彙表在使用前先用 OpenCC 轉成原文字形。
- `localize` 存在的原因：OpenCC 的詞庫只能處理固定的詞組對應，遇到要看語境的用詞就無法判斷。例如「质量」可能是「品質」也可能是物理上的「質量」，「视频」可能是「影片」也可能是「視訊」；口語的語氣也需要調整。
- `localize` 的翻譯步驟會先用 OpenCC 轉換一次，LLM 只負責修正用詞與語氣。驗證時，相似度以 OpenCC 轉換後的文字為基準，正常的在地化改寫才不會被誤判為改壞字幕。
- 兩種方式都會保留原文字形的校正字幕和轉換後的譯文。使用 `localize` 時可以輸出雙語，也可以只輸出譯文。
- 平台若同時有原文和目標語言的人工字幕，可以直接當雙語來源，依時間重疊對齊，不必再呼叫 LLM 翻譯。
- 原文語言和目標語言相同時（包括 `convert` 模式下只差字形的情況），只輸出單語字幕。
- UI 語言（繁中、简中、English）和字幕目標語言分開設定。

## 8. LLM 處理

### 8.1 Executor

每個 LLM 步驟都可以在處理方案中個別指定 executor（[ADR-0002](adr/0002-deterministic-workflow-and-llm-executors.md)）：

- `api`：內建 provider 直接呼叫。
- `agent`：放進任務佇列，等 Agent 透過 MCP 領取（[ADR-0004](adr/0004-task-queue-with-leases.md)）。
- `off`：跳過這個步驟。
- `batch`（M4）：把同類任務收集起來，透過 provider 的 Batch API 以較低價格處理，結果通常在 24 小時內回來。適合回補舊內容。

`translate` 另有確定性的 `opencc` executor，只用於中文字形轉換（見第 7 節）。

- Agent 模式的模型由 Agent 自行回報，無法驗證。來源紀錄寫成 `model_reported`，並標記 `model_verified: false`。
- 任務超過 `agent.timeout`（預設 24 小時）都沒有 Agent 領取時，預設自動改用 `api` executor，仍受預算限制。可以在處理方案中改成只通知。
- 不依賴 MCP sampling，因為各 agent host 的支援程度不一。

### 8.2 Provider

- `openai-compatible`：OpenAI、OpenRouter、DeepSeek、Groq、Ollama、LM Studio 等。
- `anthropic`。
- `gemini`。

模型 ID 一律寫在設定裡。例如 `gemini-2.5-pro` 和 `gemini-2.5-flash` 會在 2026-12-01 停用，請求自動改導到 `gemini-3.8-flash` 和 `gemini-3.5-flash-lite`。

### 8.3 Prompt

- 依類型分為 `summary`、`chapters`、`segment`、`correct`、`translate`、`frames.select`、`note`。`translate` 類型也包含中文字形的在地化 prompt（例如 `localize-zh-hant-tw`、`localize-zh-hans-cn`）。
- 有版本，可以比較差異，可以宣告變數：`title`、`channel`、`description`、`chapters`、`glossary`、`source_lang`、`target_lang`。
- 可以設為頻道的預設值，單次執行也能覆寫。執行時記錄 prompt 的 `id`、`version` 和 `sha256`。
- 組裝順序固定為「不變的前綴（系統指示、詞彙表、全域上下文）＋變動的 chunk」，讓同一部影片的各個 chunk 共用 provider 的 prompt cache。
- 透過 MCP prompts capability 對外提供。

### 8.4 Chunk、翻譯單位與驗證

- Chunk 優先依章節切，否則依 token 視窗切並保留重疊，然後平行處理。
- 每個 chunk 的上下文包含標題、說明、章節、詞彙表，以及前一段末尾 N 句的原文。這些內容處理前就有，不必等前一個 chunk 完成，所以不影響平行。
- 用 JSON schema 約束輸出。
- **翻譯單位是句群**：
  - 只有一句跨越多個 cue 時才組成句群，其餘仍是一個 cue 對一句譯文。
  - LLM 翻譯整句，並在譯文中插入切分標記，標記數必須等於 cue 數減一。程式依標記把譯文分回各個 cue，時間不變。
  - 標記數不符時，該句群退回逐 cue 翻譯。
- 驗證項目：

  | 適用 | 項目 |
  |---|---|
  | 共通 | cue 數量相同、長度沒超過上限、時間單調不重疊 |
  | `correct` | 每句相似度在門檻內 |
  | `translate` | 輸出語言正確、長度比例在範圍內、不可為空、不可和原文相同（專有名詞例外）、切分標記數正確 |

- 譯文超過閱讀速度上限時只記警告，不判失敗，因為時間不能動。
- 驗證失敗時帶著錯誤重試；超過重試次數就退回原文，並記錄在產出裡。
- 詞彙表來源：頻道設定、影片標題與說明、章節名稱。同一份詞彙表也提供給 ASR 的 `prompt` 或 `context_bias`。
- `correct` 的輸出 schema 有一個選填的「術語候選」欄位。候選累積在頻道底下，使用者核可後才寫入頻道詞彙表（M4 提供審核 UI）。

### 8.5 字幕規則

依語言設定，以下為預設值：

| 規則 | CJK | 拉丁語系 |
|---|---|---|
| 每行最多 | 20–25 字 | 42 字元 |
| 每句最多 | 2 行 | 2 行 |
| 顯示時長 | 0.8–7 秒 | 0.8–7 秒 |
| 閱讀速度上限 | 約 9 字／秒 | 約 17 字元／秒 |

## 9. 任務佇列與並行

詳見 [ADR-0004](adr/0004-task-queue-with-leases.md)。

- 任務依 DAG 建立：上游完成後才建立下游任務。LLM 步驟的每個 chunk 是一個任務。
- 並行上限依資源類別設定（`download`、`asr`、`llm`、`frames`），另外依 provider 設定 RPM 和 TPM。
- Agent 用 `claim_tasks(kinds, max=N)` 一次領取多個任務；租約到期的任務自動放回佇列。
- 優先順序：手動新增 > 訂閱的新內容 > 回補舊內容。等待越久的任務優先度會逐漸提高（aging），避免一直排不到；aging 直接寫在領取任務的排序條件裡。
- `wait` 類的重新檢查不消耗重試次數，但有截止時間。
- 重新啟動時，上一次執行的 worker 持有的租約放回佇列。子程序以 process group 管理，取消時整組結束；正常關閉時先停止領取，再主動釋放租約。
- 可以暫停或恢復整個佇列，也可以只暫停某個資源類別。
- 同一份 `/data` 同時只有一個擁有者（見第 17 節），所以 scheduler 與限流不會重複。

## 10. 關鍵畫面

- 兩階段處理：
  1. 候選擷取（確定性）：ffmpeg 場景變化偵測，加上密度下限（至少每 N 秒一張），再用縮圖 RGB 差異和滑動視窗去重。perceptual hash 遇到純色畫面容易失效，所以不用。
  2. 選圖：可以用規則（場景分數、與字幕提示詞的距離），也可以交給 vision LLM。送給 LLM 的是加上編號的 3×3 拼圖，模型只回傳編號，可以大幅降低 image token。
- 張數 = `min(Y, ceil(X × 分鐘數), 依片長的上限)`。依片長的區間沿用 `Video Notes/AGENTS.md`：未滿 10 分鐘 5–8 張、10–30 分鐘 8–12 張、30–60 分鐘 12–16 張、超過 60 分鐘 16–20 張。有意義的畫面不夠時可以少於下限，並在筆記中註明。
- 字幕中出現「看這裡」「如圖」之類的提示詞時，加權附近的畫面。
- SponsorBlock 標記的片段內不選圖。
- 筆記用的畫面以來源最高畫質擷取，最高 1080p、寬 1920px，不放大。
- 現有 `frames.py` 的演算法與參數作為移植規格。
- 只有音訊的內容（Podcast）自動略過這個步驟。

## 11. 字幕輸出

- 字幕的唯一真實格式是 notesteme cues JSON。以下格式都在下載、API、MCP 取用時即時渲染，不存成產出，換 ASS 樣式也不必重跑任何步驟：SRT、VTT、ASS、雙語 SRT、純文字。
- **輸出目標**：在處理方案中設定，由 `publish` 寫出。
  - `vault`：隨筆記一起寫入（見第 12 節）。
  - `sidecar`：只用於本機檔案，依 Jellyfin、Plex 的命名慣例把字幕寫在影片檔旁邊。
  - `folder`：寫到指定資料夾。
  - 寫出去的檔案都記錄在 `outputs`，包含寫入當時的 hash，用來偵測使用者是否改過（[ADR-0010](adr/0010-vault-output-adapters.md)）。
- **匯入外部編輯過的字幕**：
  - 上傳 SRT、VTT 或 ASS，存成新版本（`variant: edited`，來源標為 `human`），依時間對回既有的 cue。之後的翻譯與筆記可以接著用。
  - 編輯在外部工具（例如 Aegisub）完成，Notesteme 不提供校對編輯器。
- ASS：
  - 內建樣式：`default`（底部雙語、原文小字）、`translation-top`、`translation-only`、`large`、`boxed`、`minimal`。
  - 自訂：分別調整「原文行」和「譯文行」的樣式欄位與版面；也可以從現有 `.ass` 檔匯入 `[V4+ Styles]`。
  - 預設字型依目標語言的文字系統選擇（例如 Noto Sans CJK TC／SC、Noto Sans）。
  - 預覽用 ffmpeg + libass 在影片的一幀上實際渲染。

## 12. 筆記與 Vault 輸出

- **結構由模板產生，LLM 只寫內文**：frontmatter、影片資訊表、SRT 連結、標題的時間戳連結、檔名與資料夾規則，都由程式碼處理。
- **時間由程式產生**：LLM 在內文中引用 cue 編號，例如每個標題對應的起點；程式把編號換成時間戳連結，並檢查編號確實存在。
- **輸入與語言**：輸入以校正後的原文字幕為主，譯文為選配；輸出語言由 `note.lang` 決定，預設是第一個目標語言。用原文字幕寫筆記，可以避免翻譯誤差再疊一層。
- 章節來自平台，或 `chapters` 步驟。SponsorBlock 標記的片段不放進筆記的輸入。
- 預設模板依照 `@Library/Video Notes/AGENTS.md`：章節式、`##`／`###` 標題帶時間戳連結、frontmatter 欄位、callout 用法、圖片命名、`Channel/` 與其他資料夾的版面差異。這些規則寫成可調整的設定，不寫死在程式裡。
- frontmatter 加上 `notesteme_id` 和處理狀態，讓 Obsidian Dataview 可以直接篩選。
- 超過 1 小時的影片用 map-reduce 方式產生。
- 輸出後自動檢查：標題都有時間戳、引用的圖片檔都存在、frontmatter 欄位齊全。
- Gemini 直接生成模式：把 YouTube URL 直接交給 Gemini，不用下載影片，再套用同一份模板。
- 寫入 vault 是輸出目標的一種，透過 adapter 處理（[ADR-0010](adr/0010-vault-output-adapters.md)）：
  - `fs`：WSL Docker bind mount。
  - `git`：VPS 用，clone vault repo，只 commit 這次產出的檔案再 push。
  - 都不會覆蓋使用者修改過的筆記。

## 13. 訂閱與追蹤

- 訂閱的對象：頻道、播放清單、Bilibili 合集、Podcast feed。
- 探索方式：
  - YouTube：RSS（免費，只有最新約 15 部）、Supadata `channel/videos`（只回傳 ID，扣 credit）、`yt-dlp --flat-playlist`（用於回補與播放清單）。
  - Bilibili：`yt-dlp` 抓 space 頁或合集，或接自架的 RSSHub。
  - Podcast：RSS。
- 每個訂閱可以設定：
  - 回補範圍：只抓新的、最近 N 部、或全部。
  - 過濾規則：片長、Shorts、直播、標題 regex。
  - 排程。
  - 套用的處理方案。
- 匯入訂閱：OPML（Podcast），以及 Google Takeout 匯出的 YouTube 訂閱 CSV，不需要 OAuth。
- 摘要與彙整：
  - 訂閱的新內容可以先跑 `summary`，通知改成每日或每週一封彙整。
  - 處理方案設成「摘要後暫停」時，只有在彙整或 UI 中勾選的影片才繼續跑完整流程，沒選的不會再花錢。
- 直播和首播：等 VOD 出來再處理。
- 試跑預覽：顯示套用目前設定會抓到哪些、略過哪些，以及原因，但不實際處理。
- 請求加入隨機延遲，並遵守 rate limit。
- 同一個頻道只在一個實例上訂閱（[ADR-0011](adr/0011-defer-multi-instance-sync.md)）。
- **修訂（2026-11-23，ADR-0021，待 owner 追認）**：排程是 worker 的定期工作（`scheduled_jobs`），以注入的 clock 驅動；重啟後過期的排程只補跑一次；隨機延遲以 hash 決定，`scheduler.jitter_max` 預設 30 分鐘。細節見 [SUB 規格](specs/SUB-subscriptions.md)。

## 14. 狀態、篩選與搜尋

- 篩選條件：頻道、平台、各步驟狀態、錯誤類別、發布日、片長、語言、已讀狀態、有沒有筆記。
- 全文搜尋用 SQLite FTS5 trigram tokenizer。trigram 無法用索引查少於 3 個字的詞，但中文常見的詞都是 2 個字，所以 2 字以下改用掃描查詢；個人規模的資料量足以應付。
- 只索引現行版本的校正字幕與譯文字幕，被取代的舊版本不進索引。
- **修訂（2026-11-23，[SRCH 規格](specs/SRCH-search.md) SRCH-R1，待 owner 追認）**：每個語言只索引現行版本中最完整的一份字幕（原文依 `edited` > `corrected` > `segmented` > `normalized` > `source`，譯文依 `edited` > `translated`），沒有開啟校正的影音也搜得到。
- 媒體檔保留策略：例如 N 天後刪影片、只保留選中的畫面。產出舊版本的保留規則見 5.1 節。UI 顯示磁碟用量。保留策略只套用在可以重新下載的媒體快取，不包括上傳的原始檔（`/data/uploads`）。
- 刪除影音先移到垃圾桶，保留 30 天後永久刪除。Notesteme 寫到外部的檔案預設保留，使用者改過的一律不刪（[DEL 規格](specs/DEL-delete.md)）。

## 15. 成本與預算

- 每個 provider 都有計價表（ASR 按分鐘、LLM 按 token、Supadata 按 credit）：
  - 內建計價表隨版本發布，使用者可以覆寫。
  - 每筆記錄 `checked_at`，超過 90 天沒有確認就在 UI 提醒。
  - provider 在回應中回報金額時（例如 OpenRouter），優先採用回報的金額。
- 開始處理前先估算成本，並把估算金額記為預留（`reserved`），完成後改成實際金額。預算檢查以「實際金額加上預留金額」計算，平行任務才不會一起超支。
- 成本帳本細到每一次 API 呼叫，包括重試與驗證失敗，並保存當時的單價。
- Agent 模式的金額記為 `unknown`；Agent 有回報 token 數就一併記下。這類金額不計入預算。
- 可設月預算和單片上限，超過時任務標記為 `blocked`（原因 `budget_blocked`）。
- 通知管道：ntfy、Discord、Telegram、webhook。通知時機：完成、失敗、需要你處理、預算將用完、訂閱彙整，以及無人值守時的告警（見第 18 節）。
- **修訂（2026-11-23，ADR-0021，待 owner 追認）**：預算的檢查與預留在同一個寫入交易中完成；通知先寫入 `notification_outbox` 再由排程送出，失敗時退避重送。細節見 [COST](specs/COST-cost-budget.md)、[NOTIFY](specs/NOTIFY-notifications.md) 規格。

## 16. MCP 介面

同時提供 stdio（`notesteme mcp`）和 Streamable HTTP（`/mcp`，需要 bearer token）兩種方式。Token 的權限範圍見第 19 節。

| Tool | 用途 | 需要的 scope |
|---|---|---|
| `ingest` | 新增 URL 或本機路徑，可指定處理方案和覆寫設定，也可以指定只跑到某個步驟 | `ingest` |
| `list_items` / `get_item` | 查詢影音與各步驟狀態，包含摘要與章節 | `read` |
| `get_transcript` | 取得字幕（原始、校正、譯文），可指定格式與時間範圍；格式即時渲染 | `read` |
| `import_transcript` | 匯入外部編輯過的字幕，存成新版本 | `ingest` |
| `list_frames` / `get_frame` | 取得關鍵畫面（以圖片內容回傳） | `read` |
| `get_note` | 取得筆記 | `read` |
| `get_video_context` | 對應舊 `/watch` 的問答用途：必要時先處理到字幕步驟，再回傳字幕和畫面 | `read`；需要處理時另需 `ingest` |
| `search` | 全文搜尋字幕；預留 `mode` 參數給之後的語意搜尋 | `read` |
| `claim_tasks` / `extend_lease` / `submit_task` / `release_task` | Agent 領取並完成 LLM 任務 | `agent` |
| `list_subscriptions` / `create_subscription` | 管理訂閱 | `read`／`admin` |

Prompt 模板透過 MCP prompts capability 提供。

## 17. CLI 概要

```
notesteme serve                       # Web UI + API + MCP(HTTP) + worker + scheduler
notesteme ingest <url|path> [--profile p] [--until transcript] [--json]
notesteme items ls [--status ...] [--channel ...]
notesteme items rm <ref>... [--delete-outputs] [--purge] [--yes]   # 移到垃圾桶
notesteme items restore <ref>...
notesteme trash ls | empty [--yes]
notesteme export <item> --format srt|vtt|ass|bundle
notesteme import <bundle|subtitle> [--item <id>]
notesteme mcp                         # stdio MCP
notesteme doctor                      # 檢查 yt-dlp、ffmpeg、deno、API key
notesteme tools update                # 更新 yt-dlp，自我檢查失敗時自動回退
notesteme backup [--out <dir>]        # DB 快照與設定
notesteme restore <backup>
notesteme secrets rotate-key          # 輪替 master key
notesteme config export|import        # DB 中的設定（不含 secrets）與 YAML 互轉
```

CLI 有兩種模式：

- **獨立模式**：直接開本機的 SQLite。
- **client 模式**：透過正在執行的 `serve` 操作，事件也會即時出現在 UI。

同一份 `/data` 同時只有一個擁有者：

- `serve` 啟動時對 `/data` 取得 flock。
- CLI 偵測到 lock 就自動改用 client 模式；沒有 lock 時才用獨立模式。
- 也可以用 `--server <url> --token <t>` 明確連到其他實例，例如 Windows 上的 Agent 呼叫 WSL Docker 裡的實例。不要讓原生 binary 透過 `\\wsl$` 直接開 WSL 裡的 DB，跨檔案系統的檔案鎖不可靠。

現有 `watch` SKILL 會改寫成很薄的一層：只呼叫 MCP 或 CLI，Agent 只做需要思考的部分。

## 18. 部署

| 項目 | WSL Docker | VPS |
|---|---|---|
| YouTube 反爬 | 家用 IP，風險低 | **高**。對策依序：先走字幕；用 Supadata；Gemini 直接讀 URL；PO Token provider sidecar；cookie 用分身帳號 |
| 下載策略 | 正常；`split_timing_audio: auto` | 預設只在抽幀時才下載媒體；`split_timing_audio: never` |
| Vault 輸出 | `fs`：bind mount `/mnt/d/...`，處理 NTFS 不允許的字元 | `git`：push 到 vault repo |
| 驗證 | M1 起有單一密碼登入；compose 預設只映射到 `127.0.0.1` | **必須開啟**：Web UI 登入、MCP bearer token、Caddy 提供 HTTPS。M4 起可改用反向代理驗證或 OIDC |
| 對外請求 | 私有網段預設封鎖；自架 RSSHub、NAS 要加入 allowlist | 同左 |
| 本地 ASR | WSL2 CUDA，可跑 GPU sidecar | 通常沒 GPU，改走 API |

Image 內容：Go binary、ffmpeg（含 libass）、python3 + `yt-dlp[default]`（裝在 `/data/tools` 的 venv，可在執行期更新）、Deno。

Volume 配置：

- `/data`：DB、產出、媒體快取、上傳的原始檔、工具。
- `/vault`：bind mount，或作為 git adapter 的工作目錄。
- `/config`：設定檔。

compose 提供兩份 profile：`wsl` 和 `vps`。選配 sidecar：PO Token provider、本地 ASR（例如 Speaches 或 WhisperX）、Postgres＋Redis（選配 backend，M4 之後）。

另外用 goreleaser 發布 Windows、Linux、macOS 的原生 binary。

### 18.1 維運

- **健康檢查**：`/healthz` 確認程序存活，`/readyz` 確認 DB 可寫入、外部工具可用。Docker 的 `HEALTHCHECK` 用 `/readyz`。
- **無人值守告警**：透過既有的通知管道發出。
  - 排程超過 N 小時沒有成功執行。
  - 佇列中有任務超過 N 小時沒有進展。
  - 磁碟用量超過門檻。
  - cookie 太久沒有成功使用過，或 yt-dlp 的失敗率突然升高。
- **備份**：
  - 預設每日備份一次，內容是 DB 快照（`VACUUM INTO`）與 `/config`，保留 7 份。
  - 產出檔寫入後就不再修改，適合交給 rsync、restic 這類外部工具做增量備份，文件提供範例。
  - master key 不在備份內，要另外保存。
- **Migration**：啟動時有待執行的 migration，先留一份 DB 快照再升級。不支援降級，要回到舊版就還原快照。
- **yt-dlp 更新**：自動更新後執行自我檢查，失敗就回退到上一版並通知（[ADR-0009](adr/0009-external-tools-runtime.md)）。
- **事件紀錄**：`events` 保留 90 天。

## 19. 安全

詳見 [ADR-0013](adr/0013-config-sources-and-secrets.md)、[ADR-0014](adr/0014-client-access-control-and-untrusted-input.md)。

- **Secrets**：
  - API key、cookie、token 用信封加密：環境變數中的 master key 只用來加密一把存在 DB 的資料金鑰。
  - `notesteme secrets rotate-key` 輪替 master key，只需重新加密資料金鑰。
  - 遺失 master key 只會失去 secrets，重新輸入即可，其他資料不受影響。
  - Secrets 不寫進 log，也不出現在命令列參數裡。
- **登入**：M1 起提供單一密碼登入。session cookie 設 `HttpOnly`、`SameSite=Lax`；非 GET 的請求都做 CSRF 防護。
- **Token**：HTTP API 與 MCP 共用 bearer token，可以在 UI 產生、撤銷、設定到期時間。權限分成四個可以組合的 scope：

  | scope | 能做什麼 |
  |---|---|
  | `read` | 查詢影音、字幕、筆記，全文搜尋 |
  | `agent` | 領取與提交 LLM 任務 |
  | `ingest` | 新增影音、重新處理、匯入字幕（會花錢） |
  | `admin` | 設定、secrets、訂閱、token 管理、刪除影音 |

  給 Agent 用的 token 預設是 `read + agent`。MCP HTTP 啟用 DNS rebinding 防護與 Origin 檢查。
- **會執行命令的設定**：`command` adapter 與 publish hook 只能寫在設定檔。
  - 參數只能來自模板變數，以 argv 陣列直接傳入，不經過 shell。
  - 一律套用 `env_allowlist`、逾時與輸出大小上限，工作目錄固定在 `/data/tmp/<task>`。
  - API 與 MCP 只能選用已定義的項目，不能新增或修改。
- **對外請求**：RSS、Podcast 音檔、transcript、通知 webhook 都走同一個 HTTP client。
  - 只接受 `http` 和 `https`。
  - 在建立連線時檢查實際的目標 IP，擋掉 loopback、私有網段、link-local 與雲端 metadata 位址，也一併擋掉 DNS rebinding。私有網段可以用 allowlist 開放，metadata 位址一律封鎖。
  - 最多轉址 5 次，每一跳都重新檢查；回應大小與逾時都有上限。
  - yt-dlp 只開放 YouTube 與 Bilibili 的 extractor，關閉 generic extractor。
- **本機路徑**：API 與 MCP 的 `ingest` 只接受設定好的根目錄（inbox、媒體庫）底下的路徑，比對前先解析 symlink。CLI 獨立模式不限制。
- **Bundle 匯入**：
  - 拒絕絕對路徑、`..` 與 symlink。
  - 限制解壓後的總大小、檔案數與壓縮比。
  - 逐檔驗證 `sha256`，manifest 用 JSON schema 驗證。
  - 匯入的來源紀錄標記 `imported_from`，不視為已驗證。
- **Web UI**：渲染 Markdown 時不允許原始 HTML。CSP 的 `frame-src` 只開放 YouTube 與 Bilibili 的嵌入網域。
- **不可信的資料**：字幕、標題、畫面都當作不可信的資料，prompt 中明確標示為資料，Agent 模式下不能被當成指令。Agent 的 token 沒有 `ingest` 和 `admin` scope，就算被字幕中的指令誤導，也呼叫不到會花錢或修改設定的 tool。

## 20. Hub（M5）

- 內容模型就是 bundle 倉庫：
  - 同一部影片可以依語言、模型、prompt 分成多個版本。
  - 記錄衍生關係，例如「以某使用者的原始字幕重新校正」。
  - 用內容 hash 去重。
- 來源紀錄中由 Agent 自行回報的模型（`model_verified: false`）另外標示，依模型篩選時可以排除。
- 品質訊號：投票、檢舉。
- 版權：
  - 預設只分享文字衍生物，畫面預設不公開。
  - 上傳時要聲明授權，並提供下架流程。
  - 非公開的影音（`availability` 不是 `public`，例如會員限定、私人），以及用 cookie 取得的字幕，預設禁止上傳。
- Client 與 Hub 的串接：
  - 用 PAT 認證。
  - 可選擇開啟「付費跑 ASR 之前，先查 Hub 有沒有現成的字幕」。開啟時要說明會送出哪些影片 ID；之後評估改成只送 ID hash 的前綴，由 Client 端比對。
- 儲存：Postgres + S3 相容物件儲存（R2 或 MinIO）。
- 權限見 [ADR-0012](adr/0012-hub-access-control.md)。
- UI 另外設計，和 Client 共用時間帶元件。

## 21. 資料模型概要

```
channels         (id, platform, external_id, title, url, metadata_json, updated_at)
subscriptions    (id, kind, platform, external_id, url, title, channel_id, profile_id, overrides_json,
                  schedule, filters_json, backfill_json, enabled, last_checked_at, created_at)
                  -- kind: channel | playlist | collection | feed
items            (id, platform, external_id, part, channel_id, subscription_id, profile_id, overrides_json,
                  title, url, published_at, duration_s, original_lang, lang_source, lang_mixed,
                  availability, media_kind, metadata_json, read_state, instance_id, created_at, deleted_at)
                  -- UNIQUE (platform, external_id, part)；lang_source: metadata | asr | detected
source_probes    (id, item_id, source_id, lang, kind, available, est_cost, rejected_reason, probed_at)
artifacts        (id, item_id, kind, variant, lang, format, path, sha256, size, parents_json,
                  provenance_json, superseded_by, pinned, created_at)
stage_runs       (id, item_id, stage, lang, variant, status, block_reason, error_class, error_message,
                  executor, cache_key, impl_version, config_snapshot_id, artifact_id,
                  started_at, finished_at)
config_snapshots (id, sha256, config_json)                          -- 四層覆寫合併後的生效設定
tasks            (id, stage_run_id, kind, executor, resource_class, state, priority, payload_json,
                  chunk_index, result_path, lease_token, lease_owner, lease_until, attempts,
                  max_attempts, not_before, deadline_at, created_at)
outputs          (id, item_id, target, path, format, style_id, artifact_ids_json, sha256, written_at)
prompts          (id, kind, name, version, body, vars_schema_json, sha256, created_at)
profiles         (id, name, version, config_json)
ass_styles       (id, name, builtin, style_json)
glossaries       (id, scope, scope_id, terms_json, updated_at)      -- scope: global | channel
glossary_candidates (id, channel_id, item_id, term, suggestion, state, created_at)
cost_ledger      (id, item_id, item_title, stage_run_id, task_id, provider, model, units, unit_type, usd,
                  state, attempt, outcome, unit_price_json, at)     -- state: reserved | actual | unknown
item_tombstones  (platform, external_id, part, sha256, purged_at)  -- 永久刪除後避免 inbox 與訂閱加回來
price_overrides  (provider, model, unit_type, usd_per_unit, checked_at)
encryption_keys  (id, wrapped_key, created_at, retired_at)          -- 由 master key 加密的資料金鑰
secrets          (id, name, key_id, ciphertext, updated_at)
api_tokens       (id, name, scopes, token_hash, expires_at, last_used_at, revoked_at)
sessions         (id, token_hash, created_at, expires_at, ip, user_agent)
notifiers        (id, kind, config_ciphertext, events_json, enabled)
events           (id, item_id, type, payload_json, at)              -- SSE 與稽核；保留 90 天
cues_fts         (FTS5 trigram：item_id, artifact_id, start_ms, text) -- 只索引現行版本
```

- 所有 ID 都用 UUIDv7。每台實例有自己的 `instance_id`（[ADR-0011](adr/0011-defer-multi-instance-sync.md)）。
- `channels` 只存頻道本身的資料，訂閱設定都在 `subscriptions`。
- 多個目標語言時，`translate` 每個語言各一筆 `stage_runs`。
- `stage_runs` 不存金額，一律從 `cost_ledger` 加總。
- 刪除影音先設定 `deleted_at`（垃圾桶，預設 30 天），永久刪除時清除影音的所有資料，但 `cost_ledger` 保留（`item_id` 改為空，`item_title` 記錄標題），並寫入 `item_tombstones`。細節見 [DEL 規格](specs/DEL-delete.md)。
- 每筆 `stage_runs` 都指向一份 `config_snapshots`，用來回答「這份產出當時是用什麼設定做的」。
- 摘要、章節、SponsorBlock 片段都存成產出，`kind` 分別為 `summary`、`chapters`、`sponsor_segments`。
- `outputs` 取代舊的匯出產出，也是 ADR-0010 用來偵測使用者修改的「上次寫入的 hash」。
- 內建計價表跟著程式發布，`price_overrides` 只存使用者的覆寫。
- cues JSON 的格式見 ADR-0005；`Cue` 預留可為空的 `speaker` 欄位。
- **修訂（2026-11-23，ADR-0021 第 7 點，待 owner 追認）**：M4 另外新增 `subscription_entries`、`scheduled_jobs`、`digests`、`notification_outbox`、`batches`、`oidc_identities`；`subscriptions` 加 `next_check_at`、`last_error`；`glossary_candidates` 加審核欄位；`sessions` 加 `method`。Postgres＋Redis 不在 M4 範圍內。

## 22. 技術棧

詳見 [ADR-0001](adr/0001-go-as-primary-language.md)、[ADR-0007](adr/0007-storage.md)、[ADR-0008](adr/0008-web-ui-architecture.md)、[ADR-0009](adr/0009-external-tools-runtime.md)。

| 層 | 選擇 |
|---|---|
| 語言 | Go（穩定版，至少 1.25，MCP SDK 的最低需求） |
| HTTP／API | `net/http` + Huma v2（產生 OpenAPI 3.1） |
| DB | Client：SQLite（`modernc.org/sqlite`，WAL，FTS5），選配 Postgres＋Redis；Hub：Postgres（pgx）；migration 用 goose，查詢用 sqlc |
| 任務與排程 | 自寫 stage runner + 租約佇列 + 排程（到期判斷取自注入的時鐘，測試可以同步驅動，見 ADR-0015） |
| LLM | `openai-go`、`anthropic-sdk-go`、`google.golang.org/genai`，外面包一層薄介面 |
| MCP | `modelcontextprotocol/go-sdk` |
| 字幕 | `go-astisub` 讀寫 SRT／VTT；ASS 輸出自己實作 |
| 簡繁轉換 | 純 Go 的 OpenCC port 或 `opencc` CLI，M0 評估 |
| 語言偵測 | 純 Go 套件（例如 `lingua-go`），M1 評估 |
| 外部工具 | yt-dlp、ffmpeg／ffprobe、Deno，以子程序執行 |
| 前端 | React + Vite + TypeScript；TanStack Router、Query、Table；Radix primitives（shadcn/ui 只取元件骨架，套用自家 token）；CodeMirror 6；i18next |
| 打包 | 前端用 `embed.FS` 嵌入 binary；goreleaser；多架構 Docker image |
| 開發 | Vite dev server 代理到 Go 後端；後端用 `air` 熱重載 |

## 23. Repo 結構

```
notesteme/
  cmd/notesteme/            # serve | ingest | import | mcp | doctor | tools | backup | secrets 子命令
  internal/
    source/                # youtube, bilibili, podcast, local；SponsorBlock
    transcript/            # 字幕來源、ASR adapter、品質檢查
    subtitle/              # cue 模型、斷句與對時、SRT/VTT/ASS 渲染
    llm/                   # provider 抽象、結構化輸出、驗證、batch
    prompt/                # prompt 庫與版本
    frames/                # 候選擷取、去重、拼圖
    notes/                 # 筆記模板
    output/                # 輸出目標：vault（fs、git）、sidecar、folder
    pipeline/              # DAG、步驟、快取
    queue/                 # 租約佇列、並行與 rate limit
    store/                 # SQLite；選配 Postgres
    config/                # 設定檔載入、環境變數覆寫、匯出與匯入
    secrets/               # 信封加密、金鑰輪替
    netguard/              # 對外 HTTP client 與 SSRF 防護
    auth/                  # 登入、session、token scope
    backup/                # 備份、還原、migration 前快照
    mcp/                   # MCP tools 與 prompts
    bundle/                # 匯出與匯入
    api/                   # Huma handlers、SSE
    hub/                   # M5
  web/
    client/                # Client UI
    hub/                   # Hub UI（M5）
    shared/                # 共用元件，例如時間帶、字幕列表
  deploy/docker/           # Dockerfile、compose（wsl／vps）
  features/                # Gherkin 驗收場景，由 godog 執行（見 docs/specs/README.md）
    steps/                 # step definitions
    fixtures/              # yt-dlp 輸出、字幕檔、fake provider 的回應
  docs/
    specs/                 # 能力規格、故事地圖、步驟詞彙、非功能需求
```

## 24. Roadmap

| 里程碑 | 內容 | 同時完成的 UI | 驗收條件 |
|---|---|---|---|
| **M0 Spike**（限一週） | 用 5 部代表性影片量測各來源的 segment 長度分佈；量測 LLM 斷點標記對回字詞的成功率，以及一對一驗證的通過率與退回率；實測 Bilibili AI 字幕能否用 cookie 取得、Bilibili 嵌入播放器能否控制播放位置；在 VPS 上測 YouTube 有無 PO Token provider 的差異；確認 Gemini 直接讀 URL 的限制（片長、公開性、配額）與 Groq 的 word 級時間戳；確認 OpenCC Go port 的可行性 | 無 | 結論回寫到 ADR；品質檢查與 `when_needed` 的門檻有初值 |
| **M1 擷取核心** | 四個平台、字幕來源與品質檢查、語言偵測、語言優先順序、normalize、SRT／VTT 即時渲染、任務佇列（worker 端）、快取、單一密碼登入、備份與還原、`/data` 單一擁有者與 CLI client 模式、基礎版 Docker；`watch` SKILL 改成薄的一層 | 新增、媒體庫（基本篩選）、影音詳情（字幕）、任務佇列、設定（服務與帳號）、登入 | 四個平台共 10 部樣本都能一鍵產出 SRT；失敗都有分類與修正方式；未登入時無法存取任何 API |
| **M2 LLM 字幕** | 斷句、校正、句群翻譯、`opencc` executor；處理方案；LLM provider 與 prompt caching；MCP（含 Agent 一次領多個任務、token scope）；ASS 樣式；匯入外部編輯過的字幕；sidecar 輸出；收集術語候選 | Prompt 庫與試跑、處理方案、字幕樣式、字幕對照檢視、設定（MCP） | 雙語 ASS 可輸出；兩個 Agent 可以同時領任務並提交成功；驗證失敗會退回原文 |
| **M3 畫面與筆記** | 候選擷取、選圖、自動章節、SponsorBlock、筆記模板、Gemini 引擎、fs／git adapter、hook | 影音詳情（畫面、筆記）、筆記模板、設定（輸出） | 產出的筆記通過依 `Video Notes/AGENTS.md` 寫成的自動檢查 |
| **M4 追蹤與部署** | 訂閱（含播放清單、合集、OPML 與 Takeout 匯入）、排程、摘要與彙整通知、成本與預算、Batch API、通知與無人值守告警、每日備份、反向代理驗證與 OIDC、token 管理、術語候選審核、Bookmarklet 與 PWA、compose 的 wsl／vps profile | 總覽、訂閱、成本、批次操作、全文搜尋、詞彙表審核 | VPS 上訂閱 3 個頻道，無人值守運作一週 |
| **M5 Hub** | OAuth、權限、bundle 倉庫、Hub MCP | Hub UI | 可以分享字幕與筆記，也能用別人的字幕重新生成 |

**實作進度備註（2026-11-22）**：M1 的後端（API、CLI、pipeline、Docker）已完成，自動化驗收場景在預設 tag 下 188 個通過、3 個 pending（NRM-R7 待 owner 決定，2 個 DEL `@mcp` 等 M2 的 MCP）。但 M1 尚未收尾，缺口如下：

- **Web UI 尚未開始。** M1 的頁面（新增、媒體庫、影音詳情、佇列、設定、登入）與 UI 相關的 NFR（CSP、bundle 大小、A11y、I18N、瀏覽器相容性）都還沒做。這項工作改以獨立的「UI 切片」追蹤，不再附屬於後端切片；M2 的頁面（Prompt 庫、處理方案、字幕樣式、字幕對照）接在它後面。排入的時機見第 26 節。
- **M1 的驗收條件「四個平台共 10 部樣本都能一鍵產出 SRT」還沒用真實樣本驗證。** 驗收場景都跑在假的 yt-dlp 與 ASR 上，不能代替這項實測。
- **其他缺口**：7 個 `@manual` 場景還沒人工驗收、6 個 `@pending-m0` 場景的門檻仍是暫定值、M1 的 NFR 在 CI 上還缺 `govulncheck`、涵蓋率門檻與 goreleaser。完整清單見 [handoff 第 1 節「M1 尚未收尾」](handoff.md#1-現況)。

## 25. 風險與對策

| 風險 | 對策 |
|---|---|
| YouTube 反爬，VPS 上尤其嚴重 | 優先走字幕、使用 Supadata、Gemini 直接讀 URL、PO Token provider、yt-dlp 在執行期更新 |
| `whisper-1` 在 2027-02-26 停止服務 | 預設 fallback 改用 Groq `whisper-large-v3-turbo`，`whisper-1` 只當作選項 |
| 模型很快被汰換（例如 Gemini 2.5） | 模型 ID 一律寫在設定裡，UI 顯示停用公告 |
| Bilibili 風控 | 控制請求頻率、使用 cookie、選配 RSSHub |
| LLM 改壞字幕內容 | 一對一驗證、相似度門檻、失敗退回原文、記錄 prompt 版本 |
| 斷點對齊或句群翻譯的失敗率太高 | M0 先量測；失敗時退回原文或逐 cue 翻譯 |
| 平台字幕或 ASR 品質差（語言錯、幻覺、只有音樂） | 品質檢查不合格就換下一個來源 |
| 未授權存取：花掉額度、讀取 secrets、執行命令 | M1 起就有登入；token 分 scope；會執行命令的設定只能寫在設定檔 |
| 成本失控 | 開始前估算並預留、設預算上限、快取避免重複付費 |
| Hub 的版權問題 | 預設只分享文字、非公開內容禁止上傳、提供下架流程、上傳時聲明授權 |

## 26. 待決問題

- Web UI 切片（M1 欠下的 UI）的排入時機與前端範圍。預設：與 M2 的 MCP 切片並行，先做 M1 的頁面；前端工具鏈（建置、測試、i18n）以及是否沿用 `docs/ui/mockups/` 的設計，開工前由 owner 決定。
- 簡繁轉換用純 Go 的 OpenCC port 還是 CLI（M0 決定）。
- 品質檢查、`when_needed`、句群翻譯的門檻（M0 量測後定初值）。
- Bundle 格式的版本規則與授權預設值（M5 前定案）。
- Hub 查詢的隱私做法（M5 前定案）。
- Postgres＋Redis backend 的實作時機（目前排在 M4 之後，視需要）。
- 多個實例之間的協調，等 Hub 時再處理。

## 27. 參考資料

- [OpenAI Speech-to-text guide](https://developers.openai.com/api/docs/guides/speech-to-text)
- [OpenAI Transcriptions API reference](https://developers.openai.com/api/reference/resources/audio/subresources/transcriptions/methods/create)
- [Mistral – Transcribe files](https://docs.mistral.ai/studio/audio/speech-to-text/transcribe-files)
- [Groq Speech to Text](https://console.groq.com/docs/speech-to-text)
- [Gemini API deprecations](https://aistudio.google.com/docs/deprecations)
- [yt-dlp：External JS runtime announcement](https://github.com/yt-dlp/yt-dlp/issues/15012)
- [yt-dlp EJS wiki](https://github.com/yt-dlp/yt-dlp/wiki/EJS)
- [yt-dlp PO Token Guide](https://github.com/yt-dlp/yt-dlp/wiki/PO-Token-Guide)
- [Supadata – Get transcript](https://docs.supadata.ai/get-transcript)
- [Supadata – Channel videos](https://docs.supadata.ai/youtube/channel-videos)
- [Linux DO Connect](https://wiki.linux.do/Community/LinuxDoConnect)
- [MCP go-sdk releases](https://github.com/modelcontextprotocol/go-sdk/releases)
- [claude-real-video](https://github.com/huangchihhungleo/claude-real-video)
- [VideoCaptioner](https://github.com/WEIFENG2333/VideoCaptioner)

## 28. 修訂紀錄

| 版本 | 日期 | 摘要 |
|---|---|---|
| v0.1 | 2026-11-11 | 初版 |
| v0.2 | 2026-11-12 | 依審查修訂：簡繁轉換移到 `translate`、句群翻譯、步驟狀態加上 `waiting` 與 `blocked`、字幕格式改為即時渲染、登入提前到 M1、設定來源分層、安全與維運規則、資料模型調整、選配 Postgres＋Redis；新增摘要、自動章節、SponsorBlock、外部字幕匯入、sidecar 輸出、訂閱匯入、Batch API 等功能 |
| v0.3 | 2026-11-13 | 排程改為自寫，不用 gocron v2：gocron 在自己的 goroutine 中非同步觸發，驗收測試無法確定排程已經處理完（ADR-0015） |
| v0.4 | 2026-11-22 | Roadmap 註記 M1 的 Web UI 與真實樣本驗收尚未完成，Web UI 改以獨立的 UI 切片追蹤，排入時機列入待決問題 |
| v0.5 | 2026-11-23 | M3 規格展開（ADR-0020，待 owner 追認）：M3 的步驟接在 `translate` 之後，順序為 `sponsorblock` → `chapters` → `frames` → `note` → `publish`；`frames.candidates`、`frames.select` 合併成 `frames` 一個步驟；SponsorBlock 片段只排除在章節、選圖與筆記之外（不影響 M2 的步驟）；vault 與 hook 只能在設定檔定義 |
| v0.6 | 2026-11-23 | M4 規格展開（ADR-0021，待 owner 追認）：排程器是 worker 的定期工作；`discover` 是訂閱層的工作而非步驟；`summary` 放在 `normalize` 之後、預設 off；預算預留與檢查在同一交易；通知經 outbox 送出；媒體檔保留策略延到 M4 之後 |

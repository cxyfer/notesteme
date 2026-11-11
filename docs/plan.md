# Episteme 計畫

> 狀態：草案 v0.1（2026-11-11）
> 相關文件：[ADR 索引](adr/README.md)、[UI 規格](ui/README.md)

## 1. 背景與目標

現有的 `watch` SKILL（`D:\Workspace\@Career\.agents\skills\watch`）讓 Agent 驅動整個流程，從下載、轉錄、抽幀到寫筆記都交給它。實際使用時有兩個問題：

- 轉錄效率低，每次都要 Agent 一步步推進。
- 常卡在下載、轉錄這類非核心問題上，例如 yt-dlp 403、缺少 binary、模型安裝。

Episteme 把流程拆成兩段：

- **確定性 workflow**：下載、取得字幕、轉錄、匯出。不需要 Agent，可以排程、重試、續跑。
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
- 人工校對編輯器：校對只靠 LLM，UI 只提供唯讀對照。
- 多個 instance 之間的狀態同步，延到 Hub 階段（見 [ADR-0011](adr/0011-defer-multi-instance-sync.md)）。

## 2. 名詞

| 名詞 | 說明 |
|---|---|
| 影音（Item） | 一部影片、一集 Podcast，或一個本機檔案。Bilibili 分P 各算一個。 |
| 步驟（Stage） | pipeline 中的一個處理單元，例如 `transcript`、`correct`。 |
| 產出（Artifact） | 步驟產生的檔案或資料，例如原始字幕、校正字幕、ASS、筆記。每個產出都帶來源紀錄。 |
| 來源紀錄（Provenance） | 產出是由哪個 provider、model、prompt 版本、參數、上游產出生成的。 |
| 處理方案（Profile） | 一組可重複套用的 pipeline 設定：要跑哪些步驟、字幕來源順序、provider、目標語言、抽幀上限、筆記模板、輸出方式。 |
| 任務（Task） | 佇列中的一個執行單位，可以由內建 worker 執行，也可以由 Agent 領取。 |
| Bundle | 可攜式的產出包，內含 manifest 與字幕、筆記等檔案，用於匯出、匯入和分享。 |

## 3. 範圍

### 3.1 Client

| 功能 | 優先級 | 里程碑 |
|---|---|---|
| 匯入 YouTube、Bilibili、Podcast RSS、本機檔案 | P0 | M1 |
| 取得平台字幕，或用 ASR 轉錄（來源可擴充） | P0 | M1 |
| 字幕語言優先順序與 fallback 順序 | P0 | M1 |
| SRT／VTT 匯出 | P0 | M1 |
| 任務佇列與並行控制 | P0 | M1 |
| LLM 斷句、校正、翻譯，可切換 prompt | P0 | M2 |
| MCP（Agent 領任務、查詢產出） | P0 | M2 |
| ASS 雙語字幕，有預設樣式也可自訂 | P1 | M2 |
| 關鍵畫面擷取與選圖（每分鐘 X 張、整部 Y 張） | P1 | M3 |
| Obsidian 筆記（自訂模板與提示詞） | P1 | M3 |
| Gemini 多模態引擎（直接看影片） | P1 | M3 |
| 頻道訂閱與排程 | P0 | M4 |
| 依狀態與頻道分類、篩選、全文搜尋 | P0 | M1 基本版，M4 完整版 |
| 成本估算、預算上限、通知 | P1 | M4 |
| Web UI 登入（VPS 必要） | P0 | M4 |
| 本地 ASR 入口（例如 WhisperX） | P2 | M1 介面，實作隨時可補 |

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

## 4. 系統架構

```
 ┌──────────────────── episteme（Client：單一 Go binary／Docker）──────────────────────┐
 │ 入口：Web UI ｜ CLI ｜ MCP（stdio、Streamable HTTP）｜ Scheduler                      │
 │                     └──────────── 全部走同一層 Service／HTTP API ────────────┘        │
 │                                                                                     │
 │ Pipeline Engine（DAG；每個步驟冪等、可續跑、有快取）                                  │
 │  discover → metadata → transcript → normalize ─┬→ segment → correct → translate → export │
 │  └──────────── 確定性 workflow ─────────────┘  │  └──── LLM（api／agent／off）───┘  SRT/VTT/ASS │
 │                                                └→ frames.candidates → frames.select → note → publish │
 │                                                                                     │
 │ Task Queue（租約、並行上限、rate limit）    LLM Executor：內建 API provider｜MCP Agent │
 │ Artifact Store：檔案 + SQLite（含來源紀錄）  External Tools：yt-dlp、ffmpeg、deno     │
 └─────────────────────────────────┬───────────────────────────────────────────────────┘
                                   │ bundle（manifest + srt/ass/md）
                           episteme-hub（M5：OAuth、權限、版本、篩選、MCP）
```

設計原則：

- **確定性的事交給程式**，LLM 只處理需要理解語意的部分（[ADR-0002](adr/0002-deterministic-workflow-and-llm-executors.md)）。
- **時間戳只取自來源**，LLM 不輸出時間（[ADR-0003](adr/0003-timestamps-from-source.md)）。
- **每個產出都記錄來源**，為之後的 Hub 分享預留（[ADR-0005](adr/0005-provenance-and-bundle-format.md)）。
- **API-first**：UI、CLI、MCP 的能力一致（[ADR-0008](adr/0008-web-ui-architecture.md)）。

## 5. Pipeline

| 步驟 | 性質 | 輸入 | 輸出 | 說明 |
|---|---|---|---|---|
| `discover` | 確定性 | 訂閱 | 影音清單 | RSS、Supadata、yt-dlp；套用過濾規則 |
| `metadata` | 確定性 | URL | 中繼資料 | 標題、頻道、發布日、片長、章節、可用字幕軌、原文語言 |
| `transcript` | 確定性 | 中繼資料 | 原始字幕 | 依來源優先順序取得（見第 6 節） |
| `normalize` | 確定性 | 原始字幕 | 正規化字幕 | 去除重複的滾動字幕、合併碎句、簡繁轉換、時間單調性檢查 |
| `segment` | LLM | 正規化字幕 | 斷句字幕 | 只回傳斷點標記，時間由程式計算；人工字幕預設跳過 |
| `correct` | LLM | 斷句字幕 | 校正字幕 | 一對一校正，使用詞彙表 |
| `translate` | LLM | 校正字幕 | 譯文字幕 | 一對一翻譯，可有多個目標語言 |
| `export` | 確定性 | 字幕 | SRT／VTT／ASS | ASS 套用樣式 |
| `frames.candidates` | 確定性 | 影片 | 候選畫面 | 場景變化 + 密度下限 + 去重 |
| `frames.select` | LLM 或規則 | 候選畫面 | 選中畫面與圖說 | 用拼圖降低 image token |
| `note` | LLM + 模板 | 字幕、畫面、中繼資料 | Markdown 筆記 | LLM 只寫內文，結構由模板產生 |
| `publish` | 確定性 | 筆記與附件 | vault 檔案 | 經 fs 或 git adapter 寫入，接著執行 hook |

### 5.1 冪等與快取

- 每個步驟的快取鍵 = `(item, stage, 上游 artifact hash, provider, model, prompt sha256, params sha256)`。快取鍵相同就直接重用，不會重複付費。
- 從任一步驟重跑時，會產生新版本的產出。舊版本保留，標記 `superseded_by` 指向新版。
- 中斷之後從最後一個完成的步驟繼續。

### 5.2 錯誤分類

| 類別 | 範例 | 系統行為 |
|---|---|---|
| `retryable` | 網路逾時、429、5xx | 指數退避重試，次數有上限 |
| `wait` | 自動字幕還沒生成、直播還沒結束、首播未開始 | 設定 `not_before`，時間到再檢查 |
| `needs_action` | cookie 過期、會員限定、年齡限制、缺 API key | 停下來，在 UI 的「需要你處理」顯示修正方式 |
| `budget_blocked` | 超過月預算或單片上限 | 停下來，調整預算後可繼續 |
| `permanent` | 影片已刪除、地區封鎖且無替代方案 | 標記失敗，附原因 |

### 5.3 狀態

每個步驟各自有狀態：`pending`、`running`、`awaiting_agent`、`done`、`skipped`、`failed`（附錯誤類別）。影音的總狀態由各步驟推導，UI 可依任一步驟的狀態篩選。

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
| OpenAI `whisper-1` | word + segment | 單檔 25MB 上限；**2027-02-26 停止服務**，之後改用 Groq（設定切換即可） |
| Mistral Voxtral 3 | segment | 要求 word 級會回 400 |
| Gemini 多模態 | model | 時間戳由模型生成，長片可能漂移；預設排在 ASR 後面 |
| 本地 ASR（WhisperX 等） | word | 透過 `command` 或 `openai-compatible` adapter 接入 |

擴充方式見 [ADR-0006](adr/0006-pluggable-transcript-sources.md)。

### 6.2 時間戳策略

詳見 [ADR-0003](adr/0003-timestamps-from-source.md)。摘要：

- 校正、翻譯是一對一處理，不碰時間。
- 合併相鄰的 cue 時，用第一句的 start、最後一句的 end。
- 只有在單一 segment 內切開時才需要推算時間。由 `split_timing` 控制：
  - `forbid`：不允許段內切分。
  - `when_needed`（預設）：只有超過長度或時長上限時才切。先用 `silencedetect` 找停頓點，找不到才依字數比例插值。
  - `free`：讓 LLM 自由斷句。
- 每個 cue 記錄時間怎麼來的（`Timing`）：`source`、`merged`、`silence`、`interpolated`、`model`。

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
  fallback: [supadata, whisper-1]    # 平台都沒有字幕時才用，順序可調
```

- OpenCC 只在目標語言是中文變體時套用，依地區選轉換表：`s2twp`、`s2hk` 或 `t2s`。
- 原文是中文、目標是另一種中文字形時（例如原文是简中、目標是 `zh-Hant-TW`），視為同一種語言：在 `normalize` 轉換字形，之後的校正都在目標字形上進行，不需要翻譯步驟。
- 平台若同時有原文和目標語言的人工字幕，可以直接當雙語來源，依時間重疊對齊，不必再呼叫 LLM 翻譯。
- 原文語言和目標語言相同時，只輸出單語字幕。
- UI 語言（繁中、简中、English）和字幕目標語言分開設定。

## 8. LLM 處理

### 8.1 Executor

每個 LLM 步驟都可以在處理方案中個別指定 executor（[ADR-0002](adr/0002-deterministic-workflow-and-llm-executors.md)）：

- `api`：內建 provider 直接呼叫。
- `agent`：放進任務佇列，等 Agent 透過 MCP 領取（[ADR-0004](adr/0004-task-queue-with-leases.md)）。
- `off`：跳過這個步驟。

不依賴 MCP sampling，因為各 agent host 的支援程度不一。

### 8.2 Provider

- `openai-compatible`：OpenAI、OpenRouter、DeepSeek、Groq、Ollama、LM Studio 等。
- `anthropic`。
- `gemini`。

模型 ID 一律寫在設定裡。例如 `gemini-2.5-pro` 和 `gemini-2.5-flash` 會在 2026-12-01 停用，請求自動改導到 `gemini-3.8-flash` 和 `gemini-3.5-flash-lite`。

### 8.3 Prompt

- 依類型分為 `segment`、`correct`、`translate`、`frames.select`、`note`。
- 有版本，可以比較差異，可以宣告變數：`title`、`channel`、`description`、`chapters`、`glossary`、`source_lang`、`target_lang`。
- 可以設為頻道的預設值，單次執行也能覆寫。執行時記錄 prompt 的 `id`、`version` 和 `sha256`。
- 透過 MCP prompts capability 對外提供。

### 8.4 Chunk 與驗證

- Chunk 優先依章節切，否則依 token 視窗切並保留重疊，然後平行處理。每個 chunk 的上下文包含標題、說明、詞彙表和前一段摘要。
- 用 JSON schema 約束輸出。
- 驗證項目：cue 數量相同、每句相似度在門檻內、長度沒超過上限、時間單調不重疊。
- 驗證失敗時帶著錯誤重試；超過重試次數就退回原文，並記錄在產出裡。
- 詞彙表來源：頻道設定、影片標題與說明、章節名稱。同一份詞彙表也提供給 ASR 的 `prompt` 或 `context_bias`。

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

- 任務依 DAG 建立：上游完成後才建立下游任務。
- 並行上限依資源類別設定（`download`、`asr`、`llm`、`frames`），另外依 provider 設定 RPM 和 TPM。
- Agent 用 `claim_tasks(kinds, max=N)` 一次領取多個任務；租約到期的任務自動放回佇列。
- 優先順序：手動新增 > 訂閱的新內容 > 回補舊內容。等待越久的任務優先度會逐漸提高，避免一直排不到。
- 可以暫停或恢復整個佇列，也可以只暫停某個資源類別。

## 10. 關鍵畫面

- 兩階段處理：
  1. 候選擷取（確定性）：ffmpeg 場景變化偵測，加上密度下限（至少每 N 秒一張），再用縮圖 RGB 差異和滑動視窗去重。perceptual hash 遇到純色畫面容易失效，所以不用。
  2. 選圖：可以用規則（場景分數、與字幕提示詞的距離），也可以交給 vision LLM。送給 LLM 的是加上編號的 3×3 拼圖，模型只回傳編號，可以大幅降低 image token。
- 張數 = `min(Y, ceil(X × 分鐘數), 依片長的上限)`。依片長的區間沿用 `Video Notes/AGENTS.md`：未滿 10 分鐘 5–8 張、10–30 分鐘 8–12 張、30–60 分鐘 12–16 張、超過 60 分鐘 16–20 張。有意義的畫面不夠時可以少於下限，並在筆記中註明。
- 字幕中出現「看這裡」「如圖」之類的提示詞時，加權附近的畫面。
- 筆記用的畫面以來源最高畫質擷取，最高 1080p、寬 1920px，不放大。
- 現有 `frames.py` 的演算法與參數作為移植規格。
- 只有音訊的內容（Podcast）自動略過這個步驟。

## 11. 字幕輸出

- 格式：SRT、VTT、ASS、雙語 SRT、純文字、episteme cues JSON。
- ASS：
  - 內建樣式：`default`（底部雙語、原文小字）、`translation-top`、`translation-only`、`large`、`boxed`、`minimal`。
  - 自訂：分別調整「原文行」和「譯文行」的樣式欄位與版面；也可以從現有 `.ass` 檔匯入 `[V4+ Styles]`。
  - 預設字型依目標語言的文字系統選擇（例如 Noto Sans CJK TC／SC、Noto Sans）。
  - 預覽用 ffmpeg + libass 在影片的一幀上實際渲染。

## 12. 筆記與 Vault 輸出

- **結構由模板產生，LLM 只寫內文**：frontmatter、影片資訊表、SRT 連結、標題的時間戳連結、檔名與資料夾規則，都由程式碼處理。
- 預設模板依照 `@Library/Video Notes/AGENTS.md`：章節式、`##`／`###` 標題帶時間戳連結、frontmatter 欄位、callout 用法、圖片命名、`Channel/` 與其他資料夾的版面差異。這些規則寫成可調整的設定，不寫死在程式裡。
- frontmatter 加上 `episteme_id` 和處理狀態，讓 Obsidian Dataview 可以直接篩選。
- 超過 1 小時的影片用 map-reduce 方式產生。
- 輸出後自動檢查：標題都有時間戳、引用的圖片檔都存在、frontmatter 欄位齊全。
- Gemini 直接生成模式：把 YouTube URL 直接交給 Gemini，不用下載影片，再套用同一份模板。
- 寫入 vault 透過 adapter（[ADR-0010](adr/0010-vault-output-adapters.md)）：
  - `fs`：WSL Docker bind mount。
  - `git`：VPS 用，clone vault repo，只 commit 這次產出的檔案再 push。
  - 都不會覆蓋使用者修改過的筆記。

## 13. 頻道追蹤

- 探索方式：
  - YouTube：RSS（免費，只有最新約 15 部）、Supadata `channel/videos`（只回傳 ID，扣 credit）、`yt-dlp --flat-playlist`（用於回補）。
  - Bilibili：`yt-dlp` 抓 space 頁，或接自架的 RSSHub。
  - Podcast：RSS。
- 每個訂閱可以設定：
  - 回補範圍：只抓新的、最近 N 部、或全部。
  - 過濾規則：片長、Shorts、直播、標題 regex。
  - 排程。
  - 套用的處理方案。
- 直播和首播：等 VOD 出來再處理。
- 試跑預覽：顯示套用目前設定會抓到哪些、略過哪些，以及原因，但不實際處理。
- 請求加入隨機延遲，並遵守 rate limit。

## 14. 狀態、篩選與搜尋

- 篩選條件：頻道、平台、各步驟狀態、錯誤類別、發布日、片長、語言、已讀狀態、有沒有筆記。
- 全文搜尋用 SQLite FTS5 trigram tokenizer。trigram 無法用索引查少於 3 個字的詞，但中文常見的詞都是 2 個字，所以 2 字以下改用掃描查詢；個人規模的資料量足以應付。
- 媒體檔保留策略：例如 N 天後刪影片、只保留選中的畫面。UI 顯示磁碟用量。

## 15. 成本與預算

- 每個 provider 都有計價表（ASR 按分鐘、LLM 按 token、Supadata 按 credit）。開始處理前先估算成本。
- 成本帳本細到每個步驟一筆。
- 可設月預算和單片上限，超過時任務標記為 `budget_blocked`。
- 通知管道：ntfy、Discord、Telegram、webhook。通知時機：完成、失敗、需要你處理、預算將用完。

## 16. MCP 介面

同時提供 stdio（`episteme mcp`）和 Streamable HTTP（`/mcp`，需要 bearer token）兩種方式。

| Tool | 用途 |
|---|---|
| `ingest` | 新增 URL 或本機路徑，可指定處理方案和覆寫設定，也可以指定只跑到某個步驟 |
| `list_items` / `get_item` | 查詢影音與各步驟狀態 |
| `get_transcript` | 取得字幕（原始、校正、譯文），可指定格式與時間範圍 |
| `list_frames` / `get_frame` | 取得關鍵畫面（以圖片內容回傳） |
| `get_note` | 取得筆記 |
| `get_video_context` | 對應舊 `/watch` 的問答用途：必要時先處理到字幕步驟，再回傳字幕和畫面 |
| `search` | 全文搜尋字幕 |
| `claim_tasks` / `extend_lease` / `submit_task` / `release_task` | Agent 領取並完成 LLM 任務 |
| `list_subscriptions` / `create_subscription` | 管理訂閱 |

Prompt 模板透過 MCP prompts capability 提供。

## 17. CLI 概要

```
episteme serve                       # Web UI + API + MCP(HTTP) + worker + scheduler
episteme ingest <url|path> [--profile p] [--until transcript] [--json]
episteme items ls [--status ...] [--channel ...]
episteme export <item> --format srt|vtt|ass|bundle
episteme mcp                         # stdio MCP
episteme doctor                      # 檢查 yt-dlp、ffmpeg、deno、API key
episteme tools update                # 更新 yt-dlp
```

CLI 有兩種模式：獨立模式使用本機的 SQLite；client 模式用 `--server <url> --token <t>` 連到正在執行的實例。例如 Windows 上的 Agent 可以呼叫 WSL Docker 裡的實例。

現有 `watch` SKILL 會改寫成很薄的一層：只呼叫 MCP 或 CLI，Agent 只做需要思考的部分。

## 18. 部署

| 項目 | WSL Docker | VPS |
|---|---|---|
| YouTube 反爬 | 家用 IP，風險低 | **高**。對策依序：先走字幕；用 Supadata；Gemini 直接讀 URL；PO Token provider sidecar；cookie 用分身帳號 |
| 下載策略 | 正常 | 預設只在抽幀時才下載媒體 |
| Vault 輸出 | `fs`：bind mount `/mnt/d/...`，處理 NTFS 不允許的字元 | `git`：push 到 vault repo |
| 驗證 | 建議開啟 | **必須開啟**：Web UI 登入、MCP bearer token、Caddy 提供 HTTPS |
| 本地 ASR | WSL2 CUDA，可跑 GPU sidecar | 通常沒 GPU，改走 API |

Image 內容：Go binary、ffmpeg（含 libass）、python3 + `yt-dlp[default]`（裝在 `/data/tools` 的 venv，可在執行期更新）、Deno。

Volume 配置：

- `/data`：DB、產出、媒體快取、工具。
- `/vault`：bind mount，或作為 git adapter 的工作目錄。
- `/config`：設定檔。

compose 提供兩份 profile：`wsl` 和 `vps`。選配 sidecar：PO Token provider、本地 ASR（例如 Speaches 或 WhisperX）。

另外用 goreleaser 發布 Windows、Linux、macOS 的原生 binary。

## 19. 安全

- API key 與 cookie 加密存放（master key 來自環境變數）。不寫進 log，也不出現在命令列參數裡。
- Web UI：單一使用者登入，session cookie 設 `HttpOnly`、`SameSite=Lax`；非 GET 的請求都做 CSRF 防護。
- MCP HTTP：bearer token 可以在 UI 產生和撤銷；啟用 DNS rebinding 防護與 Origin 檢查。
- 字幕、標題、畫面都當作不可信的資料：prompt 中明確標示為資料，Agent 模式下不能被當成指令。
- 執行本地 ASR 的外部程序時，清除無關的環境變數（沿用現有 SKILL 的做法）。

## 20. Hub（M5）

- 內容模型就是 bundle 倉庫：
  - 同一部影片可以依語言、模型、prompt 分成多個版本。
  - 記錄衍生關係，例如「以某使用者的原始字幕重新校正」。
  - 用內容 hash 去重。
- 品質訊號：投票、檢舉。
- 版權：預設只分享文字衍生物，畫面預設不公開。上傳時要聲明授權，並提供下架流程。
- Client 與 Hub 的串接：
  - 用 PAT 認證。
  - 可選擇開啟「付費跑 ASR 之前，先查 Hub 有沒有現成的字幕」。
- 儲存：Postgres + S3 相容物件儲存（R2 或 MinIO）。
- 權限見 [ADR-0012](adr/0012-hub-access-control.md)。
- UI 另外設計，和 Client 共用時間帶元件。

## 21. 資料模型概要

```
channels      (id, platform, external_id, title, url, profile_id, schedule, filters_json, last_checked_at)
items         (id, platform, external_id, part, channel_id, title, url, published_at, duration_s,
               original_lang, media_kind, metadata_json, read_state, instance_id, created_at)
artifacts     (id, item_id, kind, variant, lang, format, path, sha256, size, parents_json,
               provenance_json, superseded_by, created_at)
stage_runs    (id, item_id, stage, status, error_class, error_message, artifact_id, cost_usd,
               started_at, finished_at)
tasks         (id, stage_run_id, kind, executor, resource_class, state, priority, payload_json,
               lease_token, lease_owner, lease_until, attempts, max_attempts, not_before, created_at)
prompts       (id, kind, name, version, body, vars_schema_json, sha256, created_at)
profiles      (id, name, version, config_json)
ass_styles    (id, name, builtin, style_json)
cost_ledger   (id, item_id, stage_run_id, provider, model, units, unit_type, usd, at)
secrets       (id, name, ciphertext, updated_at)
events        (id, item_id, type, payload_json, at)          -- SSE 與稽核
cues_fts      (FTS5 trigram：item_id, artifact_id, start_ms, text)
```

所有 ID 都用 UUIDv7。每台實例有自己的 `instance_id`（[ADR-0011](adr/0011-defer-multi-instance-sync.md)）。

## 22. 技術棧

詳見 [ADR-0001](adr/0001-go-as-primary-language.md)、[ADR-0007](adr/0007-storage.md)、[ADR-0008](adr/0008-web-ui-architecture.md)、[ADR-0009](adr/0009-external-tools-runtime.md)。

| 層 | 選擇 |
|---|---|
| 語言 | Go（穩定版，至少 1.25，MCP SDK 的最低需求） |
| HTTP／API | `net/http` + Huma v2（產生 OpenAPI 3.1） |
| DB | Client：SQLite（`modernc.org/sqlite`，WAL，FTS5）；Hub：Postgres（pgx）；migration 用 goose，查詢用 sqlc |
| 任務與排程 | 自寫 stage runner + 租約佇列；排程用 gocron v2 |
| LLM | `openai-go`、`anthropic-sdk-go`、`google.golang.org/genai`，外面包一層薄介面 |
| MCP | `modelcontextprotocol/go-sdk` |
| 字幕 | `go-astisub` 讀寫 SRT／VTT；ASS 輸出自己實作 |
| 簡繁轉換 | 純 Go 的 OpenCC port 或 `opencc` CLI，M1 評估 |
| 外部工具 | yt-dlp、ffmpeg／ffprobe、Deno，以子程序執行 |
| 前端 | React + Vite + TypeScript；TanStack Router、Query、Table；Radix primitives（shadcn/ui 只取元件骨架，套用自家 token）；CodeMirror 6；i18next |
| 打包 | 前端用 `embed.FS` 嵌入 binary；goreleaser；多架構 Docker image |
| 開發 | Vite dev server 代理到 Go 後端；後端用 `air` 熱重載 |

## 23. Repo 結構

```
episteme/
  cmd/episteme/            # serve | ingest | mcp | doctor | tools 子命令
  internal/
    source/                # youtube, bilibili, podcast, local
    transcript/            # 字幕來源與 ASR adapter
    subtitle/              # cue 模型、斷句與對時、SRT/VTT/ASS
    llm/                   # provider 抽象、結構化輸出、驗證
    prompt/                # prompt 庫與版本
    frames/                # 候選擷取、去重、拼圖
    notes/                 # 模板、vault adapter
    pipeline/              # DAG、步驟、快取
    queue/                 # 租約佇列、並行與 rate limit
    store/                 # SQLite（之後加 Postgres）
    mcp/                   # MCP tools 與 prompts
    bundle/                # 匯出與匯入
    api/                   # Huma handlers、SSE
    hub/                   # M5
  web/
    client/                # Client UI
    hub/                   # Hub UI（M5）
    shared/                # 共用元件，例如時間帶、字幕列表
  deploy/docker/           # Dockerfile、compose（wsl／vps）
  docs/
```

## 24. Roadmap

| 里程碑 | 內容 | 同時完成的 UI | 驗收條件 |
|---|---|---|---|
| **M0 Spike** | 用 5 部代表性影片量測各來源的 segment 長度分佈；實測 Bilibili AI 字幕能否用 cookie 取得；在 VPS 上測 YouTube 有無 PO Token provider 的差異；確認 OpenCC Go port 的可行性 | 無 | 結論回寫到 ADR |
| **M1 擷取核心** | 四個平台、字幕來源、語言優先順序、normalize、SRT／VTT、任務佇列（worker 端）、快取、基礎版 Docker；`watch` SKILL 改成薄的一層 | 新增、媒體庫（基本篩選）、影音詳情（字幕）、任務佇列、設定（服務與帳號） | 四個平台共 10 部樣本都能一鍵產出 SRT；失敗都有分類與修正方式 |
| **M2 LLM 字幕** | 斷句、校正、翻譯；處理方案；LLM provider；MCP（含 Agent 一次領多個任務）；ASS 樣式 | Prompt 庫與試跑、處理方案、字幕樣式、字幕對照檢視、設定（MCP） | 雙語 ASS 可輸出；兩個 Agent 可以同時領任務並提交成功；驗證失敗會退回原文 |
| **M3 畫面與筆記** | 候選擷取、選圖、筆記模板、Gemini 引擎、fs／git adapter、hook | 影音詳情（畫面、筆記）、筆記模板、設定（輸出） | 產出的筆記通過依 `Video Notes/AGENTS.md` 寫成的自動檢查 |
| **M4 追蹤與部署** | 訂閱、排程、成本與預算、通知、登入、compose 的 wsl／vps profile | 總覽、頻道訂閱、成本、登入、批次操作、全文搜尋 | VPS 上訂閱 3 個頻道，無人值守運作一週 |
| **M5 Hub** | OAuth、權限、bundle 倉庫、Hub MCP | Hub UI | 可以分享字幕與筆記，也能用別人的字幕重新生成 |

## 25. 風險與對策

| 風險 | 對策 |
|---|---|
| YouTube 反爬，VPS 上尤其嚴重 | 優先走字幕、使用 Supadata、Gemini 直接讀 URL、PO Token provider、yt-dlp 在執行期更新 |
| `whisper-1` 在 2027-02-26 停止服務 | 走 OpenAI 相容 adapter，改設定切到 Groq `whisper-large-v3-turbo` |
| 模型很快被汰換（例如 Gemini 2.5） | 模型 ID 一律寫在設定裡，UI 顯示停用公告 |
| Bilibili 風控 | 控制請求頻率、使用 cookie、選配 RSSHub |
| LLM 改壞字幕內容 | 一對一驗證、相似度門檻、失敗退回原文、記錄 prompt 版本 |
| 成本失控 | 開始前估算、設預算上限、快取避免重複付費 |
| Hub 的版權問題 | 預設只分享文字、提供下架流程、上傳時聲明授權 |

## 26. 待決問題

- 簡繁轉換用純 Go 的 OpenCC port 還是 CLI（M0 決定）。
- Bundle 格式的版本規則與授權預設值（M5 前定案）。
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

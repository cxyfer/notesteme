# ADR-0006：字幕來源做成插件，依能力選用

- 狀態：已採納
- 日期：2026-11-11
- 修訂：2026-11-12，新增品質檢查、語言偵測與重新 Probe；預設 fallback 改用 Groq；`command` adapter 只能寫在設定檔

## 背景

- MVP 要支援的來源：
  - `whisper-1`、Mistral Voxtral 3。
  - YouTube 與 Bilibili 的平台字幕（Bilibili AI 字幕需要登入）。
  - Supadata。
  - Podcast transcript、本機字幕檔。
  - Gemini 多模態。
- 之後還要能接入本地 ASR（例如 WhisperX）和更多 API。
- ASR 服務汰換得很快，例如 `whisper-1` 會在 2027-02-26 停止服務。

## 決策

### 介面

```go
type TranscriptSource interface {
    ID() string
    Capabilities() Caps
    // 確認這個影音有沒有可用的字幕、有哪些語言、要不要付費，不實際下載
    Probe(ctx context.Context, item Item) (Availability, error)
    Fetch(ctx context.Context, item Item, req Request) (*Transcript, error)
}

type Caps struct {
    Timing          TimingGranularity // word | segment | cue | chunk | model
    Platforms       []Platform        // 空表示接受任何音訊
    NeedsMedia      bool              // 需要先下載音訊
    MaxFileBytes    int64
    SupportsGlossary bool             // prompt / keywords / context_bias
    Cost            CostModel         // 每分鐘、每 credit 或免費
}
```

### 選用方式

- 依處理方案中的 `caption_priority` 與 `fallback` 順序，逐一呼叫 `Probe`，用第一個可用的來源。
- 平台的字幕規則（人工或自動、語言）也透過同一個介面實作。
- `Fetch` 之後先做品質檢查：時間涵蓋率、語言是否符合預期、重複句與幻覺的比例、是否幾乎只有音樂標記。沒有通過就記錄原因、標記 `rejected`，改用下一個來源。門檻在 M0 用樣本調整。
- 原文語言依序採用：平台中繼資料、ASR 回報的語言、文字語言偵測的結果。中英夾雜時記錄主要語言並標記 `mixed`。
- 影音發布後 7 天內，如果採用的是較低優先的來源，會定期重新 `Probe`。發現更好的來源時預設只通知，由使用者決定是否重跑。
- 每次 `Probe` 的結果都記錄在 `source_probes`，UI 可以顯示有哪些來源、為什麼沒有採用。

### 外部 adapter（不用重新編譯）

```yaml
asr:
  providers:
    openai:   { type: openai-compatible, base_url: https://api.openai.com/v1, model: whisper-1 }
    groq:     { type: openai-compatible, base_url: https://api.groq.com/openai/v1, model: whisper-large-v3-turbo }
    voxtral:  { type: mistral, model: voxtral-mini-transcribe-3 }
    whisperx:
      type: command
      command: ["whisperx", "{audio}", "--output_format", "json", "--output_dir", "{out}"]
      output: { format: whisperx-json, path: "{out}/{stem}.json" }
      timing: word
      env_allowlist: [PATH, HOME, HF_HOME]
```

- 預設的 ASR fallback 是 `groq`。`whisper-1` 會在 2027-02-26 停止服務，只當作選項。Groq 能否提供 word 級時間戳在 M0 確認。
- `openai-compatible`：OpenAI、Groq、自架的 Speaches 或 faster-whisper server 都能用。
- `command`：執行本機 CLI，再解析它的輸出（`whisperx-json`、`srt`、`vtt`、`notesteme-cues+json`）。執行時清除無關的環境變數。
  - `command` adapter 等於能在主機上執行命令，所以只能寫在設定檔，API 與 MCP 只能選用（ADR-0013）。
  - 參數只能來自模板變數，以 argv 陣列直接傳入，不經過 shell；一律套用逾時與輸出大小上限。

### ASR 共用前處理

- 轉成單聲道 16kHz 並壓縮，確保不超過 provider 的檔案大小上限。
- 在靜音處切塊，保留重疊，修正各塊的時間偏移，並移除重疊區的重複內容。
- 傳入語言提示和詞彙表。
- 用 `no_speech_prob`、`compression_ratio` 和重複句偵測，過濾 Whisper 幻覺。

## 考慮過的選項

- **寫死幾個 provider**：每次 provider 汰換都要改程式，使用者也不能自行接入本地模型。
- **把 Python ML 套件打包進主 image**：image 會變得很大，而且 GPU 和 CPU 的環境差異很難處理。改用 sidecar 或 `command` adapter。

## 影響

- 好處：
  - `whisper-1` 停用時，只要改設定切到 Groq 就好。
  - 本地 ASR 的入口從 M1 就有。
- 代價：
  - 每個 adapter 都要實作輸出格式的解析和 `Caps` 宣告。
  - 每種輸出格式都要準備測試用的樣本。

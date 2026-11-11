# ADR-0002：確定性 workflow 與 LLM 步驟分離，LLM 步驟可分別指定 executor

- 狀態：已採納
- 日期：2026-11-11
- 修訂：2026-11-12，`export` 改為即時渲染、新增 `summary` 與 `chapters`、`translate` 可用 `opencc` executor、新增 `batch` executor 與 Agent 逾時

## 背景

現有 `watch` SKILL 讓 Agent 驅動整個流程，下載和轉錄這類不需要判斷的工作也要 Agent 一步步推進。結果是：

- 效率低。
- 常卡在非核心問題上，例如 binary 缺失、403、安裝模型。
- 無法排程，也無法無人值守。

LLM 的處理方式有兩種需求：

- 有 API key 時，全自動完成。
- 用 MCP 交給 Agent 或 Harness 完成，例如利用訂閱方案的額度。

## 決策

1. 步驟分成兩類：
   - **確定性步驟**：`discover`、`metadata`、`transcript`、`normalize`、`frames.candidates`、`publish`。由內建 worker 執行，不需要 LLM。
   - **LLM 步驟**：`summary`、`chapters`、`segment`、`correct`、`translate`、`frames.select`、`note`。
   - SRT、VTT、ASS 不是步驟的產出，取用時從 cues JSON 即時渲染；寫到 vault 或其他輸出目標由 `publish` 負責。
2. 每個 LLM 步驟都可以在處理方案中個別指定 executor：
   - `api`：內建 provider 直接呼叫（OpenAI 相容、Anthropic、Gemini）。
   - `agent`：建立任務，狀態設為 `awaiting_agent`，等 Agent 透過 MCP 領取（見 ADR-0004）。
   - `off`：跳過這個步驟。下游會改用上游的產出；例如跳過 `correct`，`translate` 就直接處理斷句後的字幕。
   - `batch`（M4）：把同類任務收集起來，透過 provider 的 Batch API 以較低價格處理，適合回補舊內容。
   - `translate` 另有確定性的 `opencc` executor，只用於中文字形轉換。這樣 `normalize` 與 `correct` 的產出不依賴目標語言，多個目標語言可以共用同一份校正字幕。
3. **Agent 逾時**：任務超過 `agent.timeout`（預設 24 小時）沒有 Agent 領取時，預設自動改用 `api` executor，仍受預算限制；可以在處理方案中改成只通知。Agent 回報的模型無法驗證，來源紀錄標記 `model_verified: false`。
4. **不依賴 MCP sampling**：各 agent host 的支援程度不一，不適合當主要路徑。
5. 需要無人值守的 Agent 模式時，在主機上排程執行 headless Agent（例如 `claude -p` 或 Codex）來消化佇列。這件事在 Notesteme 外部設定。
6. Gemini 多模態引擎是一種特殊的 `api` executor，可以擔任三種角色：
   - 直接把 URL 或影片轉成筆記。
   - 當作字幕來源（時間標記為 `model`）。
   - 協助校正（同時參考字幕與畫面）。

## 考慮過的選項

- **全部交給 Agent（現狀）**：效率低、不穩定，也無法排程。
- **全部用 API**：沒辦法使用 Agent 訂閱方案的額度，也沒辦法讓 Agent 結合自己的工具（例如查官方文件來校正術語）。
- **用 MCP sampling 讓 server 反過來請 client 產生內容**：client 支援不足，而且多數 host 需要使用者在場確認。

## 影響

- 好處：
  - 字幕這類主要產出的取得不再依賴 Agent 的穩定度。
  - 同一份處理方案可以混用 executor，例如校正用 API、筆記交給 Agent。
- 代價：
  - 需要維護任務佇列和驗證機制。
  - Agent 交回的結果要和 API 的結果走同一套驗證。
- UI 要清楚顯示「等待 Agent」的任務，並讓使用者可以改用 API 執行。

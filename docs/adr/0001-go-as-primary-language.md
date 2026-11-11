# ADR-0001：Client 與 Hub 都使用 Go

- 狀態：已採納
- 日期：2026-11-11

## 背景

- 偏好開發快、效能不錯的語言，不考慮需要長時間編譯的方案。候選是 Go 和 Java。
- 工作負載以 I/O 為主：呼叫外部 API（ASR、LLM、Supadata），以及以子程序執行 yt-dlp 和 ffmpeg。CPU 很少是瓶頸。
- Client 會在 WSL Docker 或 VPS 上 24 小時常駐，也希望能在 Windows 上直接跑原生 binary，讓 Agent 呼叫 CLI。
- Hub 需要 OAuth（Google、GitHub、Linux.do）。

## 決策

Client 和 Hub 都用 Go，放在同一個 repo，共用 domain 套件與 bundle 格式。

## 考慮過的選項

| 面向 | Go | Java（Spring Boot） |
|---|---|---|
| 編譯與迭代 | 秒級 | 增量編譯尚可；GraalVM native image 編譯很久，不考慮 |
| 常駐記憶體 | 約 20–50MB | 約 200–400MB |
| 散布 | 單一靜態 binary，可跨平台編譯 | 需要 JRE 或 jlink |
| 子程序與並行 | `os/exec` + context 取消，goroutine | Virtual threads 也可以 |
| MCP SDK | 官方 `modelcontextprotocol/go-sdk` v1.x | Spring AI MCP |
| LLM SDK | OpenAI、Anthropic、Google 都有官方 SDK | Spring AI 的多 provider 抽象比較完整 |
| OAuth | `x/oauth2` + `go-oidc`；Linux.do 要自己接 | Spring Security 最完整；Linux.do 一樣要自訂 |

- **Java**：只有 Hub 的 OAuth 明顯比較好，但 Hub 的優先度最低，而且 Linux.do 不論用哪種語言都要自訂。
- **Python**：現有 SKILL 是 Python，yt-dlp 也是。但 Python 的打包、常駐服務的並行處理、單一 binary 散布都比較弱。yt-dlp 本來就建議用子程序呼叫（見 ADR-0009），所以不需要 Python。
- **TypeScript（Bun／Node）**：可以和前端共用型別，但不在偏好清單中，而且常駐的記憶體用量與散布都不如 Go。

## 影響

- 好處：
  - 建置快，部署簡單。
  - 常駐服務的資源用量低。
  - 同一個 binary 就能提供 Web UI、API、MCP、worker 和 scheduler。
- 代價：
  - Hub 的 OAuth 和 session 要自己組裝。
  - ASS 輸出、部分字幕處理沒有現成套件，要自己寫。
- Docker image 的大小主要來自 ffmpeg、Python（給 yt-dlp 用）和 Deno，不是 Go。

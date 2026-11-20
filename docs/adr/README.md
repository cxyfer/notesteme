# 架構決策紀錄（ADR）

每份 ADR 記錄一個決策的背景、選項與影響。決策改變時，不修改舊的 ADR，而是新增一份，並把舊的那份狀態改成「已取代」。

例外：M1 開始實作之前，所有 ADR 都還是草案階段，可以直接修訂內容，並在檔頭加上一行「修訂：日期，摘要」。實作開始後，一律依上面的規則新增 ADR 取代舊的。

| 編號 | 標題 | 狀態 |
|---|---|---|
| [0001](0001-go-as-primary-language.md) | Client 與 Hub 都使用 Go | 已採納 |
| [0002](0002-deterministic-workflow-and-llm-executors.md) | 確定性 workflow 與 LLM 步驟分離，LLM 步驟可分別指定 executor | 已採納 |
| [0003](0003-timestamps-from-source.md) | 時間戳只取自來源，LLM 不輸出時間 | 已採納 |
| [0004](0004-task-queue-with-leases.md) | 用租約制任務佇列，Agent 可以一次領多個任務 | 已採納 |
| [0005](0005-provenance-and-bundle-format.md) | 產出記錄完整來源，並以 bundle 格式匯出 | 提議中 |
| [0006](0006-pluggable-transcript-sources.md) | 字幕來源做成插件，依能力選用 | 已採納 |
| [0007](0007-storage.md) | Client 預設用 SQLite 加檔案，Hub 用 Postgres 加 S3 | 已採納 |
| [0008](0008-web-ui-architecture.md) | API-first，React SPA 嵌入 Go binary | 已採納 |
| [0009](0009-external-tools-runtime.md) | yt-dlp、ffmpeg、Deno 以子程序執行，yt-dlp 可在執行期更新 | 已採納 |
| [0010](0010-vault-output-adapters.md) | 輸出目標用統一介面，vault 用 fs 與 git adapter，不覆蓋使用者修改 | 已採納 |
| [0011](0011-defer-multi-instance-sync.md) | 多實例同步延到 Hub 階段 | 已採納 |
| [0012](0012-hub-access-control.md) | Hub 權限：可見度、群組與自動規則 | 提議中 |
| [0013](0013-config-sources-and-secrets.md) | 設定來源分層，secrets 用信封加密 | 已採納 |
| [0014](0014-client-access-control-and-untrusted-input.md) | Client 的存取控制與不可信輸入 | 已採納 |
| [0015](0015-acceptance-test-harness.md) | 驗收測試在程序內執行，外部依賴在注入點替換 | 已採納 |
| [0016](0016-local-media-and-asr-preprocessing.md) | 本機影音的識別、inbox 監看與共用的 ASR 前處理 | 已採納 |

## 範本

```markdown
# ADR-XXXX：標題

- 狀態：提議中｜已採納｜已取代（由 ADR-YYYY）
- 日期：YYYY-MM-DD
- 修訂：YYYY-MM-DD，摘要（只在 M1 前使用，可省略）

## 背景
為什麼需要做這個決策，有哪些限制。

## 決策
決定怎麼做。

## 考慮過的選項
其他選項，以及沒有選它們的原因。

## 影響
好處、代價，以及之後要注意的事。
```

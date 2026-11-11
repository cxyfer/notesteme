# 規格與驗收（SDD／BDD）

> 狀態：草案 v0.1（2026-11-12）
> 相關文件：[計畫](../plan.md)、[ADR 索引](../adr/README.md)、[UI 規格](../ui/README.md)

Notesteme 採用規格驅動（SDD）的開發方式，驗收以行為（BDD）描述：

- **先寫規格，再寫程式。** 行為的改變一律先改規格與驗收場景，再改程式。
- **驗收場景就是測試。** `.feature` 檔用 Gherkin 撰寫，由 Go 的 [godog](https://github.com/cucumber/godog) 直接執行。規格和測試是同一份文件，不會脫節。

## 1. 文件階層

| 層 | 文件 | 回答的問題 | 誰會改 |
|---|---|---|---|
| 計畫 | [`docs/plan.md`](../plan.md) | 要做什麼、為什麼、排在哪個里程碑 | 範圍變更時 |
| 決策 | [`docs/adr/`](../adr/README.md) | 為什麼選這個做法 | 架構決策變更時 |
| 故事地圖 | [`story-map.md`](story-map.md) | 使用者能做到哪些事，各在哪個里程碑 | 新增或調整故事時 |
| 能力規格 | `docs/specs/<ID>-<slug>.md` | 一項能力的規則、介面與限制 | 規則變更時 |
| 驗收場景 | `features/<ID>-<slug>.feature` | 具體的例子：在什麼情況下、做什麼、得到什麼 | 規則或故事變更時 |
| 步驟詞彙 | [`steps.md`](steps.md) | 場景可以用哪些句子，以及每句的意思 | 新增步驟時 |
| 非功能需求 | [`nfr.md`](nfr.md) | 效能、資源、可靠度等跨能力的要求 | 目標變更時 |

規格與 plan、ADR 衝突時，以較新的決定為準，並在同一個 PR 中修正另一邊。

## 2. 語言慣例

- **Gherkin 一律英文**，包括關鍵字、場景名稱與步驟內容。User story 也用英文，寫在 `.feature` 檔的描述與故事地圖中。
- **規格說明用繁體中文**：背景、規則、介面、待決問題。
- 領域用語的英文與中文對照見 [`steps.md`](steps.md#1-領域用語)。場景中只使用對照表裡的英文用語。

## 3. 能力清單

| ID | 能力 | 里程碑 | 規格 | 驗收場景 |
|---|---|---|---|---|
| ING | 新增影音 | M1 | [ING-ingest.md](ING-ingest.md) | [ING-ingest.feature](../../features/ING-ingest.feature) |
| SRC | 字幕來源與品質 | M1 | [SRC-transcript-sources.md](SRC-transcript-sources.md) | [SRC-transcript-sources.feature](../../features/SRC-transcript-sources.feature) |
| NRM | 正規化 | M1 | [NRM-normalize.md](NRM-normalize.md) | [NRM-normalize.feature](../../features/NRM-normalize.feature) |
| OUT | 字幕輸出與匯入 | M1、M2 | [OUT-subtitle-output.md](OUT-subtitle-output.md) | [OUT-subtitle-output.feature](../../features/OUT-subtitle-output.feature) |
| PIPE | Pipeline 與任務佇列 | M1 | [PIPE-pipeline-queue.md](PIPE-pipeline-queue.md) | [PIPE-pipeline-queue.feature](../../features/PIPE-pipeline-queue.feature) |
| AUTH | 登入與 token | M1、M2 | [AUTH-access.md](AUTH-access.md) | [AUTH-access.feature](../../features/AUTH-access.feature) |
| SEC | 設定與 secrets | M1 | [SEC-config-secrets.md](SEC-config-secrets.md) | [SEC-config-secrets.feature](../../features/SEC-config-secrets.feature) |
| GRD | 對外請求與路徑防護 | M1 | [GRD-guards.md](GRD-guards.md) | [GRD-guards.feature](../../features/GRD-guards.feature) |
| CLI | CLI 與資料擁有權 | M1 | [CLI-cli.md](CLI-cli.md) | [CLI-cli.feature](../../features/CLI-cli.feature) |
| OPS | 備份、健康檢查與外部工具 | M1 | [OPS-operations.md](OPS-operations.md) | [OPS-operations.feature](../../features/OPS-operations.feature) |
| LIB | 媒體庫、影音詳情與佇列頁 | M1、M2 | [LIB-library.md](LIB-library.md) | [LIB-library.feature](../../features/LIB-library.feature) |
| DEL | 刪除影音與垃圾桶 | M1、M2 | [DEL-delete.md](DEL-delete.md) | [DEL-delete.feature](../../features/DEL-delete.feature) |
| LLM | LLM 執行、chunk 與驗證 | M2 | [LLM-execution.md](LLM-execution.md) | [LLM-execution.feature](../../features/LLM-execution.feature) |
| SEG | 斷句 | M2 | [SEG-segment.md](SEG-segment.md) | [SEG-segment.feature](../../features/SEG-segment.feature) |
| COR | 校正 | M2 | [COR-correct.md](COR-correct.md) | [COR-correct.feature](../../features/COR-correct.feature) |
| TRN | 翻譯與中文字形 | M2 | [TRN-translate.md](TRN-translate.md) | [TRN-translate.feature](../../features/TRN-translate.feature) |
| PRM | Prompt 庫 | M2 | [PRM-prompts.md](PRM-prompts.md) | [PRM-prompts.feature](../../features/PRM-prompts.feature) |
| PRF | 處理方案與設定覆寫 | M2 | [PRF-profiles.md](PRF-profiles.md) | [PRF-profiles.feature](../../features/PRF-profiles.feature) |
| AGT | Agent 任務 | M2 | [AGT-agent-tasks.md](AGT-agent-tasks.md) | [AGT-agent-tasks.feature](../../features/AGT-agent-tasks.feature) |
| MCP | MCP 工具 | M2 | [MCP-tools.md](MCP-tools.md) | [MCP-tools.feature](../../features/MCP-tools.feature) |

M3 到 M5 的能力目前只在 [`story-map.md`](story-map.md) 中列出故事與條列式的驗收標準，開發前再展開成規格與場景。

## 4. 能力規格的格式

每份能力規格依序包含：

1. **檔頭**：ID、狀態、里程碑、相關的 plan 章節與 ADR。
2. **目的**：一兩句話說明這項能力解決什麼問題。
3. **User stories**：英文，格式為 `As a <role>, I want <goal>, so that <benefit>.`，每則有 ID（例如 `ING-01`）。
4. **規則**：編號為 `<ID>-R<n>`（例如 `ING-R3`）。規則是可以驗證的陳述，每條至少對應一個場景。
5. **介面**：API、CLI、MCP、UI 的接觸點。
6. **不在範圍內**：明確排除的行為。
7. **待決問題**：沒有答案就不能開工的問題；依賴 M0 的項目標明預設值。

狀態流程：`draft`（草稿）→ `ready`（可以開工）→ `implemented`（場景全部通過）→ `superseded`（被取代）。

## 5. 驗收場景的寫法

### 5.1 原則

- **一個場景只驗證一件事。** 場景名稱要能直接說出這件事，例如 `Manual captions skip the segment stage`。
- **描述行為，不描述操作。** 寫「the owner ingests …」，不寫「the owner clicks the Start button」。只有 `@ui` 場景可以描述畫面上的操作。
- **場景跑在服務層。** 預設透過 HTTP API 驗證，因為 UI、CLI、MCP 都呼叫同一層 service（ADR-0008）。CLI 與 MCP 特有的行為才用 `@cli`、`@mcp` 場景驗證。
- **外部依賴一律用 test double 取代**，不連真實的網路服務：
  - 平台與 yt-dlp：用 fixture（錄好的 `yt-dlp -J` 輸出、字幕檔）。
  - ASR、LLM：用腳本化的 fake provider，依步驟回傳指定的 fixture。
  - HTTP 來源：用本機的 fake origin，DNS 解析可以指定。
  - 時間：用可控制的 fake clock。
  - 資料目錄：每個場景使用一個新的暫存 `/data`。
- **Fixture** 放在 `features/fixtures/`，以場景中的名稱引用。
- **`Background`** 只放每個場景都需要的前提，不放和驗證點有關的設定。
- **`Scenario Outline`** 用於同一規則的多組例子，例如各平台的網址辨識。

### 5.2 Tag

| Tag | 意思 |
|---|---|
| `@M1`、`@M2` … | 場景所屬的里程碑，CI 只跑已開始的里程碑 |
| `@ING-01` 等故事 ID | 場景驗證的故事；一個場景可以有多個 |
| `@ING-R3` 等規則 ID | 場景驗證的規則 |
| `@api` | 透過 HTTP API 驗證（預設，可以省略） |
| `@cli` | 透過 CLI 驗證 |
| `@mcp` | 透過 MCP 驗證 |
| `@ui` | 需要瀏覽器的場景，之後用 Playwright 執行 |
| `@manual` | 無法自動化，由人工驗收並在 PR 中記錄結果 |
| `@pending-m0` | 門檻或行為取決於 M0 的實測結果，場景先用預設值 |
| `@wip` | 正在實作，CI 允許失敗 |

### 5.3 執行

```
go test ./features/...                     # 驗收場景、規格一致性檢查與測試替身的單元測試
go test ./features -godog.tags="@ING-01"    # 只跑一則故事
go test ./features -godog.tags="@M2"        # 只跑 M2
```

- 沒有指定 tag 時，預設為 `@M1 && ~@manual && ~@pending-m0 && ~@wip && ~@ui`。
- 指定 `-godog.tags` 時，`@manual`、`@pending-m0`、`@wip`、`@ui` 的排除條件會自動併入；明確寫出其中某個 tag 時，才會選到它（ADR-0015）。
- `-godog.*` 旗標只在 `features` 這個 package 中定義，所以帶旗標時寫 `./features`，不要寫 `./features/...`。

- 步驟實作放在 `features/steps/`，依 [`steps.md`](steps.md) 的分組拆檔。
- `@ui` 場景在 M1 期間先用 `@manual` 驗收，Playwright 的執行環境建好後再改成自動。
- 規格的一致性檢查目前是 `scripts/spec/` 中的 Python 腳本，之後改寫成 Go 測試（NFR-TEST-4）：

  ```
  python3 -I scripts/spec/check_steps.py .   # 每個步驟都在 steps.md 中
  python3 -I scripts/spec/check_trace.py .   # 規則與故事都有場景、沒有不存在的 tag
  uv run --with gherkin-official python -I scripts/spec/parse_all.py features   # Gherkin 語法
  ```

## 6. 開發流程

1. **提出變更**：範圍或架構變了，先改 plan 或 ADR。
2. **更新規格**：補上或修改規則與故事，狀態設為 `draft`。
3. **寫驗收場景**：每條規則至少一個場景。需要新的步驟時，先加到 `steps.md`。
4. **Review 規格**：確認符合「Definition of Ready」，狀態改為 `ready`。
5. **紅燈**：寫 step definition，確認場景失敗的原因是功能還沒做，而不是步驟寫錯。
6. **綠燈**：實作功能，讓場景通過。單元測試在實作過程中依需要補上。
7. **重構**：場景保持通過。
8. **完成**：符合「Definition of Done」，規格狀態改為 `implemented`。

### 6.1 Definition of Ready（可以開工）

- 故事有英文的 user story 與 ID，並列在故事地圖中。
- 每條規則都有編號，且至少對應一個場景。
- 場景用到的步驟都已列在 `steps.md`。
- 待決問題都有答案；依賴 M0 的項目有預設值，相關場景標記 `@pending-m0`。
- 涉及 UI 的故事已連到 `docs/ui/pages.md` 的對應章節，文案符合 `docs/ui/README.md`。

### 6.2 Definition of Done（完成）

- 故事的所有自動化場景都在 CI 通過；`@manual` 場景已人工驗收，結果記錄在 PR 中。
- 每個新產出都寫入來源紀錄。沒有來源紀錄的產出視為錯誤（ADR-0005）。
- 錯誤訊息屬於 plan 第 5.2 節的某個類別，並附上可行動的修正方式。
- UI、CLI、MCP 中屬於這則故事範圍的介面都已完成，能力一致（ADR-0008）。
- 符合 [`nfr.md`](nfr.md) 中與這則故事相關、屬於同一里程碑的項目。
- plan、ADR、UI 規格中受影響的段落已同步更新。

## 7. 追溯

- 故事地圖列出每則故事對應的規格與場景。
- 每個場景都標記故事 ID 與規則 ID，可以從規則找到驗證它的場景，也可以反過來找。
- PR 標題或描述引用故事 ID，例如 `ING-02`。
- 規則刪除或改寫時，同時處理引用它的場景，不留下孤兒 tag。
- CI 在每個 PR 自動檢查上述對應：每條規則至少一個場景、場景的標籤都存在於規格中、每個步驟都在 `steps.md` 中（NFR-TEST-4）。

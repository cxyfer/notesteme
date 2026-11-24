# ADR-0020：M3 的步驟順序、產出、筆記模板與 vault 輸出

- 狀態：已採納（待 owner 追認）
- 日期：2026-11-23
- 修訂：2026-11-23 M3-S0 實作第 1、2、6、8 點的骨架：四個步驟加進 `pipeline.Stages`、`plannedStages` 只剩 `summary`；還沒有實作的 M3 步驟前進時是 `skipped`（`not_implemented`，見 PIPE 待決問題），從它們重跑回 501；`frames.candidates`、`frames.select` 改為 422。`Effective.Stage` 對 M3 步驟只讀自己的 `stages.<stage>`，沒有從其他步驟繼承 provider（第 2 點最後一項已確認，`chapters`、`note` 維持預設 `api`）。migration 00017 加上 `outputs.kind`、`vault`、`git_commit`；`note_templates` 留給 NOTE 的切片。

## 背景

M3「畫面與筆記」要在既有的 pipeline（`metadata` → `transcript` → `normalize` → `segment` → `correct` → `translate` → `publish`，`internal/pipeline/stages.go`）上加入 SponsorBlock、自動章節、關鍵畫面、筆記與 vault 輸出。規格展開時（[SPB](../specs/SPB-sponsorblock.md)、[CHAP](../specs/CHAP-chapters.md)、[FRM](../specs/FRM-frames.md)、[NOTE](../specs/NOTE-notes.md)、[GEM](../specs/GEM-gemini.md)、[VAULT](../specs/VAULT-vault-output.md)）遇到幾個跨規格的決定：

- plan 第 5 節把 `chapters` 放在 `segment` 之前，`frames` 拆成 `frames.candidates`、`frames.select` 兩步；程式的 `plannedStages` 也是這幾個名稱。
- M1、M2 已有 308 個通過的場景與 `features/passing.txt` 棘輪。新步驟一旦加進 `pipeline.Stages`，每部影音都會多出步驟；插在前面會改變任務數、資源類別與 `next task` 這類斷言。
- vault 的 `git` adapter 會執行 `git`，remote URL 可以用 `ext::` 這類傳輸執行任意命令；hook 本來就只能寫在設定檔（ADR-0010、ADR-0013）。
- Gemini 直接生成時沒有 cue 可以引用，時間戳只能來自模型，和 ADR-0003 衝突。

## 決策

### 1. 步驟名稱與順序

```
metadata → transcript → normalize → segment → correct → translate
         → sponsorblock → chapters → frames → note → publish
```

- 新步驟都接在 `translate` 之後、`publish` 之前。`frames` 是一個步驟，內部有「候選擷取」與「選圖」兩個任務；`frames.candidates`、`frames.select` 不再使用。`summary` 維持在 `plannedStages`（M4 與 DIG 一起決定）。
- 實作 child 把步驟加進 `pipeline.Stages` 時，同時從 `plannedStages` 移除；在那之前 `chapters`、`note` 的重跑回 501，`sponsorblock`、`frames` 回 422（名稱不對）。
- `chapters` 放在 `translate` 之後而不是 `segment` 之前，和 plan 第 5 節不同：
  - 章節的輸入是校正後的字幕，cue 編號和筆記的輸入（NOTE-R5）相同，章節起點可以直接對到筆記引用的 cue。
  - M2 的 chunk 切分已經使用平台章節（LLM-R1）；由 LLM 產生的章節不回頭影響 M2 的 chunk 切分，M1、M2 的行為不變。
  - 代價：沒有平台章節的影音，`segment`、`correct`、`translate` 仍依 token 切 chunk。之後要改成依產生的章節切，再另立決策。
- `sponsorblock` 也在 `translate` 之後：SponsorBlock 片段只影響 M3 的步驟（章節、選圖、筆記）。plan 第 5 節「SponsorBlock 標記的片段不送 LLM 步驟」不套用到 M2 的 `segment`、`correct`、`translate`（SPB 規格的待決問題）。

### 2. 預設值與略過的原因

| 步驟 | executor | 預設 | 略過的原因 |
|---|---|---|---|
| `sponsorblock` | `local`（確定性，HTTP） | `stages.sponsorblock.enabled: true` | `off`（`enabled: false`）、`not_applicable`（不是 YouTube） |
| `chapters` | `api`、`agent`、`off` | `api`；沒有 provider 或 model 時 `skipped`（`not_configured`，同 X1） | `platform_chapters`（平台已有章節）、`off`、`not_configured`、`gemini` |
| `frames` | `rules`、`api`、`agent`、`off` | `off`（每部影音都要下載影片與執行 ffmpeg，預設不做） | `audio_only`（沒有影像串流）、`off`、`not_configured`（`api` 沒有 provider）、`gemini` |
| `note` | `api`、`agent`、`off` | `api`；沒有 provider 或 model 時 `not_configured` | `off`、`not_configured` |

- 新的略過原因：`platform_chapters`、`audio_only`、`gemini`。
- 驗收要求：步驟加入之後，預設 tag 的 308 個場景與 `features/passing.txt` 不能退步。harness 在每個場景預設登記沒有任何片段的假 SponsorBlock（`features/internal/fakesponsorblock`），否則 YouTube 影音的 `sponsorblock` 會撞上 fakenet trap。
- 新的 LLM 步驟與 M2 一樣只讀自己 `stages.<stage>` 的 provider 與 model。實作時要確認 `Effective.Stage` 沒有從其他地方繼承 provider；有的話，新步驟的預設改為 `off`，不讓 M2 場景多出 LLM 請求。

### 3. 資源類別

| 工作 | 資源類別 |
|---|---|
| `sponsorblock` 查詢 | `download` |
| `chapters`、`note` 的 LLM 呼叫（`api`、`agent`、`gemini`） | `llm` |
| `frames` 的影片下載 | `download`（與 OUT-R9 預覽共用媒體快取） |
| `frames` 的場景偵測、擷取、縮圖去重、拼圖 | `frames`（`queue.Classes` 已經有，預設上限 1） |
| `frames` 的 vision 選圖 | `llm` |
| `frames` 的規則選圖、`note` 的模板渲染與自動檢查 | `local` |
| `publish` | 沒有 `git` vault 時 `local`；有 `git` vault 時 `download`（push 是網路操作） |

### 4. 產出（`artifacts.kind`）

| kind | 內容 | 版本 |
|---|---|---|
| `sponsor_segments` | JSON：`{segments: [{category, start_ms, end_ms, uuid}]}`，依開始時間排序 | 預設保留 3 版 |
| `chapters` | JSON：`{origin: platform｜llm, chapters: [{start_ms, title, cue}]}`；`cue` 只在 `origin: llm` 時有值 | 預設保留 3 版 |
| `frames` | JSON manifest：`{source_resolution, extract_resolution, candidates: [{n, at_ms, score, file, selected, caption, chapter}]}`；圖片放在 `/data/artifacts/<item>/frames/<version>/` | 只保留現行版本（plan 第 5.1 節） |
| `note` | Markdown 本文；另一份 JSON `{frontmatter, checks[], engine, template}`；`lang` 是筆記的語言 | 預設保留 3 版 |

- 來源紀錄沿用 ADR-0005 的欄位（`stage`、`executor`、`provider`、`model`、`prompt`、`params`、`parents`、`tool_versions`）。各規格另外列出的欄位：SPB-R6、CHAP-R6、FRM-R11、NOTE-R13、GEM-R5。
- 被 vault 輸出引用的 `note` 與 `frames` 版本標記 `pinned`（plan 第 5.1 節）。

### 5. 筆記模板

- 新資料表 `note_templates (id, name, version, builtin, body, rules_json, sha256, created_at)`，和 `prompts` 一樣以版本累積，處理方案引用 `template: <id>` 或 `<id>@<version>`。內建模板 `video-notes`（依 `Video Notes/AGENTS.md`）只能複製，不能修改。
- 模板是 Go `text/template`，只能使用 NOTE 規格列出的變數；儲存時檢查（NOTE-R11）。所有值都先經過 NOTE-R8 的處理才放進模板，模板本身不需要處理跳脫。

### 6. vault 與 hook 的設定位置

- vault 與 hook 只能在設定檔定義（新的頂層區段 `vaults:`；`hooks:` 已經是允許的欄位，內容由這個 ADR 定義），UI 與 API 只能選用已定義的 id。理由：`fs` 的路徑等於寫檔權限，`git` 會以 remote URL 執行命令，hook 會執行任意程式。這和 UI 第 12 節「設定 › 輸出可以編輯 vault 的路徑或 repo」不同：UI 改為唯讀顯示 vault 的定義，只能在處理方案中選用（待 owner 追認）。
- 處理方案的 `outputs` 加上 `target: vault`：`{target: vault, vault: <id>, formats, lang, on_user_change, hooks: [<id>]}`。
- `git` adapter 以 `git` CLI 經 `internal/toolexec` 執行（ADR-0009），不用 go-git：和 yt-dlp、ffmpeg 相同的逾時、process group 與環境變數處理，驗收測試以 `PATH` 中的假 `git` 取代（`features/internal/fakegit`）。安全規則見 VAULT 規格 VAULT-R7、R8。

### 7. Gemini 直接生成的時間戳

- `stages.note.engine: gemini` 時沒有 cue，標題的時間由模型提供，是 ADR-0003 的明確例外：只用在筆記標題的時間連結，不產生任何字幕；時間必須落在 `[0, 片長)` 且遞增，否則視為驗證失敗；筆記的來源紀錄寫 `timing: model`，frontmatter 的 `notesteme_timing` 是 `model`。細節見 GEM 規格。

### 8. 資料庫 migration 的方向

- `outputs` 加欄位：`kind TEXT NOT NULL DEFAULT 'subtitle'`（`subtitle`、`note`、`asset`）、`vault TEXT`（vault id，其他目標為 NULL）、`git_commit TEXT`（該次寫入所在的 commit）。主鍵維持 `(item_id, path)`；vault 的 `path` 存相對於 vault 根目錄的路徑，另以 `vault` 區分。
- 新資料表 `note_templates`（第 5 點）。
- `stage_runs`、`tasks` 不需要新欄位；`frames` 兩個任務用 `tasks.kind`（`frames.candidates`、`frames.select`）區分。

## 考慮過的選項

- **照 plan 第 5 節把 `chapters` 放在 `segment` 之前**：產生的章節可以用來切 chunk，但 M2 的 chunk 切分、快取鍵與 308 個場景都要重新驗證，而且章節的 cue 編號會指向正規化字幕，和筆記引用的校正字幕不同。
- **`frames.candidates`、`frames.select` 兩個步驟**：UI 與狀態篩選要多一個步驟，但兩者總是一起重跑（換選圖方式也要重新擷取已選圖片的高畫質版本）。改成一個步驟、兩個任務，重跑「只重新選圖」用 `POST /api/items/:id/rerun {from: frames, reuse_candidates: true}`（FRM 規格）。
- **vault 在 UI 編輯**：方便，但持有 `admin` scope 的 token 就能讓 `git` 執行任意 remote helper，或把 `fs` vault 指到任何目錄。
- **go-git**：不需要外部程式，但驗收測試要另外模擬 remote，與既有的「`PATH` 只有假工具」做法不同；推送失敗的錯誤種類也比較難對到 CLI 的訊息。

## 影響

- 好處：M1、M2 的行為與場景不受影響；新步驟可以逐一實作，每個步驟在 `stageImplemented` 登記後才會執行。
- 代價：產生的章節不參與 M2 的 chunk 切分；vault 改設定要重新啟動。
- 之後要注意：`Effective.Stage` 對新步驟的預設 provider、harness 預設的假 SponsorBlock、`plannedStages` 的同步。

## 修訂

- 2026-11-25（依 /goal 由實作者暫定，待 owner 追認；一次唯讀稽核：每份 ADR 的「決定」對照程式與規格）：
  - 第 3 點「`frames` 的影片下載 → `download`」：
    - 原本寫：影片下載屬 `download` 資源類別（與 OUT-R9 預覽共用媒體快取），場景偵測、擷取、縮圖去重與拼圖屬 `frames`。
    - 實際是：影片下載在 `frames.candidates` 任務內執行，屬 `frames` 資源類別，沒有獨立的下載任務；`frames.select` 才依 executor 分到 `local`（`rules`）或 `llm`（`api`）。原因是拆成第三個任務要多一份中間狀態，先保守合併，量測後再決定。**以實作與 [FRM 規格](../specs/FRM-frames.md) F10 為準**；F10 本身也明寫與本 ADR 第 3 點不同。
    - 依據：`docs/specs/FRM-frames.md`（F10 第 65 行）；`internal/pipeline/frames.go:185`（候選任務的 `ResourceClass: queue.ResourceFrames`）、`frames.go:327`（候選任務內以 yt-dlp 下載到媒體快取）。
  - 第 3 點「`frames` 的預設並行上限是 1」：
    - 原本寫：`frames`（`queue.Classes` 已經有，預設上限 1）。
    - 實際是：預設上限是 CPU 核心數（`max(runtime.NumCPU(), 1)`），與 [ADR-0004](0004-task-queue-with-leases.md) 的「`frames=CPU 核心數`」一致；兩份 ADR 原本互相矛盾，**以實作與 ADR-0004 為準**；並行上限可調整，範圍 1～64（`MaxLimit`）。
    - 依據：`internal/queue/control.go:38`（`DefaultLimit`）。

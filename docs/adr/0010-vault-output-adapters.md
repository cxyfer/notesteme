# ADR-0010：輸出目標用統一介面，vault 用 fs 與 git adapter，不覆蓋使用者修改

- 狀態：已採納
- 日期：2026-11-11
- 修訂：2026-11-12，vault 改為輸出目標的一種，新增 `sidecar` 與 `folder`；寫入紀錄存在 `outputs`；hook 只能寫在設定檔

## 背景

- Obsidian vault 在 Windows 上（`D:\Workspace\@Career\@Library`），本身也是 git repo，會 push 到 `private/master`。
- 部署有兩種情境：
  - WSL Docker：可以直接 bind mount vault 目錄。
  - VPS：碰不到本機的檔案系統。
- `Video Notes/AGENTS.md` 對檔名、資料夾、附件位置有明確規則，並要求：
  - 只 commit 這次修改的檔案。
  - push 被拒時要回報，不自動 rebase。
- 使用者可能會在 Obsidian 裡修改產生的筆記，重新生成時不能把修改蓋掉。

## 決策

1. **Adapter 介面**：所有輸出目標都實作同一個介面，由 `publish` 步驟呼叫。字幕在寫出時才從 cues JSON 渲染成 SRT、ASS 等格式。

   ```go
   type OutputWriter interface {
       Plan(ctx context.Context, out OutputBundle) (WritePlan, error) // 列出要寫哪些檔、哪些有衝突
       Apply(ctx context.Context, plan WritePlan) (Result, error)
   }
   ```

   | 目標 | 用途 |
   |---|---|
   | `vault`（`fs`、`git`） | 筆記、附件與筆記引用的字幕檔 |
   | `sidecar` | 只用於本機檔案，依 Jellyfin、Plex 的命名慣例把字幕寫在影片檔旁邊 |
   | `folder` | 寫到指定資料夾 |

   vault 的兩種 adapter：

   - `fs`：直接寫入 bind mount 的目錄。
   - `git`：
     1. 在 `/data/vault-repo` clone vault。
     2. 寫入檔案。
     3. 只 `git add` 這次產出的檔案，commit 後 push 到設定的分支。
     4. 遇到 non-fast-forward 或認證失敗時，步驟標記為 `blocked`（原因 `needs_action`）。不自動 rebase、merge 或 stash。
2. **檔名與路徑**：
   - 過濾 NTFS 不允許的字元 `\/:*?"<>|`，避開保留名稱（`CON`、`NUL` 等），並限制路徑長度。
   - 資料夾版面規則（`Channel/` 與其他資料夾的差異）寫成可調整的設定。預設值依照 `Video Notes/AGENTS.md`。
3. **不覆蓋使用者修改**：
   - 每次寫入時，把檔案的 `sha256` 記錄在 `outputs` 表。所有輸出目標都適用。
   - 重新生成時，如果目標檔案的 hash 和上次寫入的不同，就代表使用者改過。依設定擇一處理：
     - 另存為新檔（例如 `<名稱> (notesteme 2026-11-11).md`）。
     - 跳過，並在 UI 上標記「需要你處理」。
4. **Hook**：寫入完成後可以執行自訂命令，例如通知或額外的 git 操作。
   - Hook 只能寫在設定檔，UI、API、MCP 只能選用已定義的 hook（ADR-0013）。
   - 命令的參數只能來自模板變數，以 argv 陣列直接傳入，不接受任意的 shell 字串。
   - 一律套用環境變數 allowlist、逾時與輸出大小上限。

## 考慮過的選項

- **Obsidian Local REST API plugin**：Obsidian 必須在本機開著，VPS 也連不到，所以只當作未來的選項。
- **Syncthing、Remotely Save**：可行，但屬於外部設定，Notesteme 只負責寫入一個資料夾（`fs` adapter）。
- **一律覆蓋**：會丟失使用者的修改。

## 影響

- 好處：
  - 兩種部署情境都有可行的寫入方式。
  - 符合 vault 既有的 commit 規則。
- 代價：
  - `git` adapter 要處理認證（deploy key 或 token）和衝突回報。
- Windows 端的 Obsidian 會自動偵測 bind mount 的變更。git 模式下則要使用者自己 pull（或用 Obsidian Git plugin）。

## 修訂

- 2026-11-25（依 /goal 由實作者暫定，待 owner 追認；一次唯讀稽核：每份 ADR 的「決定」對照程式與規格）：
  - 決策 1 的 `OutputWriter{Plan, Apply}` 介面**不存在**：
    - 原本寫：所有輸出目標實作同一個 `OutputWriter` 介面，先 `Plan` 列出要寫的檔案與衝突，再 `Apply` 執行；`fs`、`git` 是兩個 adapter。
    - 實際是：沒有 `OutputWriter`、`WritePlan`，也沒有「先計畫、再執行」的兩階段。所有輸出目標（`sidecar`、`folder`、`vault`）都經過同一個 `output.Writer.Write(ctx, File)`（`internal/output/writer.go:127`）逐檔寫入，由它負責不覆蓋使用者修改的 sha256 比對（決策 3）；vault 另外由 `internal/vault` 提供兩個零件：`fs.go` 的 `CheckInside`／`EscapeError`（路徑不能經由 symlink 離開 vault，VAULT-R4）與 `git.go` 的 `Git`（`Sync`、`Commit`、`CommitRemoval`、`Push`，經 `internal/toolexec` 執行 `git`）；兩者由 `internal/pipeline/vault_publish.go`（`publishVault`、`publishVaultTarget`）串起來，`fs` 與 `git` 的差別只在寫入前後有沒有 `Sync`、`Commit`、`Push`，不是兩個實作同一介面的 adapter。
    - 行為沒有改變，衝突與 hash guard 由場景覆蓋：`features/OUT-subtitle-output.feature` 的「A file edited by hand is saved alongside instead of overwritten」（OUT-R11、R12）、「With the skip policy an edited file blocks publishing」（OUT-R12）；`features/VAULT-vault-output.feature` 的「A note edited in the vault is kept and the new one is saved beside it」（VAULT-R6）、「A rejected push asks the owner to sync the vault」（VAULT-R9，non-fast-forward 時 `blocked`、不自動 rebase）。
    - 依據：`internal/output/writer.go:127`（`Write`）；`internal/vault/fs.go`、`internal/vault/git.go:121`；`internal/pipeline/publish.go:62`、`internal/pipeline/vault_publish.go:178、322、343`；`docs/specs/OUT-subtitle-output.md`（OUT-R11、R12）、`docs/specs/VAULT-vault-output.md`（VAULT-R4～R9）。

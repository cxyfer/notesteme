# ADR-0010：Vault 輸出用 fs 與 git adapter，不覆蓋使用者修改

- 狀態：已採納
- 日期：2026-11-11

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

1. **Adapter 介面**：

   ```go
   type VaultWriter interface {
       Plan(ctx context.Context, note NoteBundle) (WritePlan, error) // 列出要寫哪些檔、哪些有衝突
       Apply(ctx context.Context, plan WritePlan) (Result, error)
   }
   ```

   - `fs`：直接寫入 bind mount 的目錄。
   - `git`：
     1. 在 `/data/vault-repo` clone vault。
     2. 寫入檔案。
     3. 只 `git add` 這次產出的檔案，commit 後 push 到設定的分支。
     4. 遇到 non-fast-forward 或認證失敗時，任務標記為 `needs_action`。不自動 rebase、merge 或 stash。
2. **檔名與路徑**：
   - 過濾 NTFS 不允許的字元 `\/:*?"<>|`，避開保留名稱（`CON`、`NUL` 等），並限制路徑長度。
   - 資料夾版面規則（`Channel/` 與其他資料夾的差異）寫成可調整的設定。預設值依照 `Video Notes/AGENTS.md`。
3. **不覆蓋使用者修改**：
   - 每次寫入時記錄檔案的 `sha256`。
   - 重新生成時，如果目標檔案的 hash 和上次寫入的不同，就代表使用者改過。依設定擇一處理：
     - 另存為新檔（例如 `<名稱> (notesteme 2026-11-11).md`）。
     - 跳過，並在 UI 上標記「需要你處理」。
4. **Hook**：寫入完成後可以執行自訂命令，例如通知或額外的 git 操作。命令的參數只能來自模板變數，不接受任意的 shell 字串。

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

# VAULT：輸出到 Vault 與 hook

- 狀態：ready
- 里程碑：M3
- 相關：plan 第 11、12、19 節；ADR-0009、ADR-0010、ADR-0013、ADR-0014、[ADR-0020](../adr/0020-m3-stages-notes-and-vault.md) 第 6、8 點；OUT（OUT-R4、R11、R12 與待決問題 S8）；DEL-R9；UI [設定](../ui/pages.md#12-設定)（輸出分區）、[影音詳情](../ui/pages.md#4-影音詳情)（筆記分頁的寫入狀態）
- 驗收場景：[`features/VAULT-vault-output.feature`](../../features/VAULT-vault-output.feature)

## 目的

把筆記、選中的畫面與字幕寫進 Obsidian vault：WSL Docker 直接寫進 bind mount 的資料夾（`fs`），VPS 推到 vault 的 git repo（`git`）。檔名與資料夾版面依 `Video Notes/AGENTS.md`，不覆蓋使用者改過的檔案；寫完之後可以執行設定檔定義的 hook。

## User stories

| ID | Story |
|---|---|
| VAULT-01 | As the owner, I want notes written into my Obsidian vault folder, so that they appear where I already keep my notes. |
| VAULT-02 | As the owner running on a VPS, I want notes pushed to my vault's git repository, so that I get them on every device. |
| VAULT-03 | As the owner, I want notes I have edited never to be overwritten, so that my own changes are safe. |
| VAULT-04 | As the owner, I want to run my own command after publishing, so that I can trigger other tools. |

## 規則

| ID | 規則 |
|---|---|
| VAULT-R1 | vault 只能在設定檔定義（`vaults.<id>`，ADR-0020 第 6 點）：`adapter: fs` 帶 `path`（絕對路徑）；`adapter: git` 帶 `remote`、`branch`（預設 `main`）、`author`（預設 `Notesteme <notesteme@localhost>`），clone 放在 `/data/vault-repo/<id>`；兩者都可以設 `base`（vault 內的根資料夾，預設 `Video Notes`）。處理方案與覆寫只能用 `outputs: [{target: vault, vault: <id>, formats, lang, folder, on_user_change, hooks}]` 選用；`vault` 不存在時回 422，`errors[].path` 是 `outputs.N.vault`，訊息「沒有這個 vault：<id>。vault 只能在設定檔定義。」；`path`、`remote` 等欄位寫在處理方案中是「不認識的欄位。」。`GET /api/vaults`（`admin`）只列出 id、adapter 與 `base`，不回傳路徑以外的憑證資訊。 |
| VAULT-R2 | 資料夾版面：筆記放在 `<base>/<folder>/`。`folder` 預設依平台：`YouTube`、`Bilibili`、`Podcasts`、`Local`；可以用 `{channel}`、`{platform}` 變數（例如 `Channel/{channel}`），不能是絕對路徑、不能含 `..`（422，`outputs.N.folder`，「folder 不能是絕對路徑或包含 ..。」）。`folder` 的第一段是 `Channel` 時：筆記為 `YYYY-MM-DD_<標題>.md`、字幕放 `_Subtitles/<筆記名>.<ext>`、圖片放 `_Assets/<筆記名>/`；其他資料夾：筆記為 `<標題> - YYYY-MM-DD.md`，圖片與字幕都放 `_Assets/<標題>/`（字幕檔名 `<標題>.<ext>`）。日期是平台的發布日（UTC），沒有發布日時是影音的新增日。筆記影片資訊表的字幕列以含 `<base>` 的完整 vault 路徑寫成 wikilink（`[[Video Notes/YouTube/_Assets/<標題>/<標題>.srt]]`）。 |
| VAULT-R3 | 名稱的處理和下載檔名相同（OUT-R4，`subtitle.DownloadFileName` 的規則）：移除 NTFS 不允許的字元 `<>:"/\|?*`、控制字元與雙向文字控制字元，合併連續空白，去掉尾端的空白與句點；Windows 保留名稱（`CON`、`NUL`、`COM1`…）加上 `_` 前綴；空的名稱改成 `untitled`；`{channel}` 展開的每一段同樣處理，處理後是 `.` 或 `..` 的段落改成 `_`。每一段最多 200 位元組，相對於 vault 根目錄的整個路徑最多 240 字元，超過時截短標題。 |
| VAULT-R4 | 寫入限制在 vault 的根目錄內（`os.Root`）：不跟隨 vault 內部指向外部的 symlink，遇到時這個檔案不寫入，`publish` 為 `failed`（`permanent`），訊息「vault 內的 <路徑> 是指向 vault 以外的連結，已拒絕寫入。」。寫入方式同 OUT 的 S8（同資料夾的暫存檔再 rename、新檔用硬連結建立、權限 0644）。 |
| VAULT-R5 | 一次發布寫出：筆記（`.md`）、筆記引用的選中畫面（NOTE-R9 的檔名）與 `formats` 指定的字幕（預設 `[srt]`，語言同 OUT 的輸出目標；`formats: []` 時只寫筆記與圖片）。每個檔案都記錄在 `outputs`（`kind` 為 `note`、`asset`、`subtitle`，`vault` 是 vault id，`path` 相對於 vault 根目錄；ADR-0020 第 8 點）。筆記沒有通過自動檢查時 `publish` 不會執行（NOTE-R7）。`GET /api/items/:id/outputs` 與 `GET /api/items/:id/note` 的 `outputs` 列出每個檔案的 `{target, vault, kind, path, state, commit}`，`state` 是 `written`、`unchanged`、`saved_as_new`、`skipped_user_changed`。 |
| VAULT-R6 | 不覆蓋使用者的修改：同 OUT-R11、R12，依 `outputs` 記錄的 `sha256` 判斷，每個檔案各自判斷。`on_user_change: save_as_new`（預設）另存為 `<名稱> (notesteme YYYY-MM-DD).<ext>`（日期是 UTC 的今天）；`skip` 時這個檔案不寫入，`publish` 為 `blocked`（`needs_action`），動作為「處理該檔案後重新發布」，hook 不執行。 |
| VAULT-R7 | `git` adapter：第一次使用時 `git clone --branch <branch> --single-branch`；每次發布先 `git pull --ff-only`，再寫入檔案，只 `git add --` 這次寫入或另存的檔案（其他未提交的變更不加入），有變更才 `git commit`（訊息 `notesteme: <標題>`，作者為 `author`），再 `git push origin HEAD:<branch>`。沒有任何檔案改變時不 commit 也不 push。不自動 rebase、merge 或 stash（ADR-0010）。 |
| VAULT-R8 | `git` 的安全：`remote` 只接受 `https://` 與 `ssh://`（或 `user@host:path`）網址，其他（`ext::`、`file://`、`fd::`、本機路徑）在載入設定時拒絕，server 無法啟動，訊息「vaults.<id>.remote 只接受 https 或 ssh 網址。」。每次執行 git 都帶 `-c core.hooksPath=/dev/null -c credential.helper= -c protocol.allow=never -c protocol.https.allow=always -c protocol.ssh.allow=always`，環境變數只有 `PATH`、`GIT_TERMINAL_PROMPT=0`、`GIT_CONFIG_NOSYSTEM=1`、`GIT_CONFIG_GLOBAL=/dev/null`、`HOME`（任務的暫存目錄）與憑證需要的變數。憑證（`NOTESTEME_VAULT_<ID>_TOKEN`，或 secrets 中的 `vault:<id>`）經 `GIT_ASKPASS` 交給 git，不出現在 argv、remote URL、log 與錯誤訊息中。 |
| VAULT-R9 | `git` 的錯誤分類：連線失敗、逾時、遠端 5xx 是 `retryable`（PIPE-R3）；重試時沿用已經建立的本機 commit，不重複 commit。non-fast-forward 或 `pull --ff-only` 無法快轉時 `publish` 為 `blocked`（`needs_action`），訊息「vault 的遠端分支 <branch> 有本機沒有的提交，Notesteme 不會自動合併。請先同步 vault repo 再重新發布。」，動作為「重新發布」；認證失敗時為 `blocked`（`needs_action`），訊息「無法登入 vault 的遠端（認證失敗）。請更新 vault <id> 的憑證。」，動作為「更新憑證」與「重新發布」。 |
| VAULT-R10 | hook 只能在設定檔定義（`hooks.<id>: {argv, timeout, max_output, env_allowlist}`，ADR-0013）；處理方案的 `outputs[].hooks` 只能列 hook 的 id，不存在時回 422（`outputs.N.hooks.M`，「沒有這個 hook：<id>。hook 只能在設定檔定義。」），寫成物件時是「必須是字串。」。`argv` 是字串陣列，第一個元素是程式；元素中可以使用 `{{note_path}}`（筆記的絕對路徑）、`{{vault_root}}`、`{{item_id}}`、`{{title}}`、`{{commit}}`，每個變數只會展開在它所在的那個元素內，不經過 shell；使用其他變數時載入設定失敗。 |
| VAULT-R11 | hook 的執行：所有檔案寫入（與 `git push`）成功之後，每個列出的 hook 執行一次，工作目錄 `/data/tmp/<task>`，以 process group 執行（ADR-0009）。環境變數只有 `env_allowlist` 列出的變數加上 `NOTESTEME_ITEM_ID`；`env_allowlist` 不能包含 `NOTESTEME_MASTER_KEY`、`NOTESTEME_PASSWORD` 與名稱以 `_API_KEY`、`_TOKEN` 結尾的變數，否則載入設定失敗（「hooks.<id>.env_allowlist 不能包含 <變數>。」）。`timeout` 預設 `30s`、最多 `5m`，逾時時結束整個 process group；stdout 與 stderr 合計只保留前 `max_output`（預設 64 KiB）寫進 log。hook 失敗或逾時不影響已寫入的檔案：`publish` 仍為 `done`，影音顯示提醒「hook <id> 失敗（結束碼 N）。檔案已經寫入。」或「hook <id> 逾時（N 秒），已停止。檔案已經寫入。」，動作為「重新發布」。 |
| VAULT-R12 | 刪除影音時 vault 中的檔案依 DEL-R9 處理：預設保留，選擇刪除時只刪 hash 與紀錄相同的檔案；`git` vault 的刪除另外以一個 commit 推送，規則同 VAULT-R7～R9。 |

## 介面

- 設定檔：`vaults.<id>.{adapter, path, remote, branch, author, base}`、`hooks.<id>.{argv, timeout, max_output, env_allowlist}`
- 處理方案：`outputs[].{target: vault, vault, formats, lang, folder, on_user_change, hooks}`
- 環境變數：`NOTESTEME_VAULT_<ID>_TOKEN`（`git` 的憑證，ID 大寫、`-` 換成 `_`）
- API：`GET /api/vaults`（`admin`）、`GET /api/items/:id/outputs`（`read`）、`GET /api/items/:id/note` 的 `outputs`；`POST /api/items/:id/rerun` 的 `from: publish`
- UI：設定 › 輸出唯讀顯示 vault 與 hook 的定義（[pages.md 第 12 節](../ui/pages.md#12-設定)，見 V1）；影音詳情的筆記分頁顯示寫入狀態、路徑與 git commit（[第 4 節](../ui/pages.md#4-影音詳情)）
- 資源類別：`publish` 有 `git` vault 時 `download`，否則 `local`（ADR-0020 第 3 點）

## 不在範圍內

- Obsidian Local REST API、Syncthing 等其他同步方式（ADR-0010）。
- 自動處理 git 衝突（rebase、merge、stash）。
- 在 vault 中刪除或搬移使用者的檔案（除了 VAULT-R12 明確要求的刪除）。

## 待決問題

- V1：vault 與 hook 只能在設定檔定義（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）。UI 第 12 節原本寫「設定 › 輸出」可以編輯 vault 的路徑或 repo、分支；但 `fs` 的路徑等於寫檔權限、`git` 會依 remote URL 執行程式（`ext::`），所以和 hook 一樣只能寫在設定檔，UI 只能唯讀顯示與選用（ADR-0020 第 6 點）。`docs/ui/pages.md` 第 12 節已補修訂說明。
- V2：push 失敗的分類（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：故事地圖寫「push 失敗時歸類為 `retryable`」，ADR-0010 寫「non-fast-forward 或認證失敗時 `blocked`」。依錯誤種類拆開（VAULT-R9）：網路類 `retryable`，需要使用者處理的 `needs_action`。
- V3：`git` 的 remote 不經過 netguard（git 自己連線）。載入設定時只檢查 scheme；remote 的主機若解析到私有網段，git 仍然會連線。VPS 上的 vault 通常在公開的 git 服務，風險可以接受；之後要限制再加 `network.allow` 的檢查（DNS rebinding 擋不住）。
- V4：`pull --ff-only` 會把遠端的新提交帶進 clone，但只會快轉，不產生 merge commit；這和 ADR-0010「不自動 merge」一致。clone 中使用者自己的未提交變更會讓 pull 失敗，歸類為 `needs_action`（同 non-fast-forward 的訊息）。
- V5：`fs` vault 的 `path` 不套用 GRD-R7 的 `media_roots` 限制，因為它只能寫在設定檔（和 OUT 的 `folder` 目標不同，`folder` 可以由持有 `ingest` 的 token 指定）。
- V6：hook 的逾時以真實時間計算（`toolexec` 的 context），場景用 1 秒的逾時與會一直等待的假 hook（`features/internal/fakehook` 的 `--hang`），不用 fake clock。
- V7：同一部影音同時有 `vault` 與其他輸出目標時，各目標各自判斷略過與擋下；任何一個 `blocked` 時 `publish` 為 `blocked`，已寫入的檔案保留，hook 不執行。
- V8：SRT 的 wikilink 在 `note` 步驟就要知道 vault 的版面（VAULT-R2），所以筆記依生效設定中的第一個 `vault` 輸出計算路徑；沒有 `vault` 輸出時影片資訊表不列字幕。之後在 `publish` 改了 `folder` 而沒有重跑 `note`，連結會指到舊路徑；`publish` 偵測到這種情況時以影音的提醒說明「筆記中的字幕連結可能不正確，請從 note 重新處理。」。

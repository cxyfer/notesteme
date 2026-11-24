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
| VAULT-R8 | `git` 的安全：`remote` 只接受 `https://` 與 `ssh://`（或 `user@host:path`）網址，其他（`ext::`、`file://`、`fd::`、本機路徑）在載入設定時拒絕，server 無法啟動，訊息「vaults.<id>.remote 只接受 https 或 ssh 網址。」。每次執行 git 都帶 `-c core.hooksPath=/dev/null -c credential.helper= -c protocol.allow=never -c protocol.https.allow=always -c protocol.ssh.allow=always -c http.followRedirects=false`（V15），環境變數只有 `PATH`、`GIT_TERMINAL_PROMPT=0`、`GIT_CONFIG_NOSYSTEM=1`、`GIT_CONFIG_GLOBAL=/dev/null`、`GIT_LITERAL_PATHSPECS=1`（V15）、`HOME`（任務的暫存目錄）與憑證需要的變數。憑證（`NOTESTEME_VAULT_<ID>_TOKEN`，或 secrets 中的 `vault:<id>`）經 `GIT_ASKPASS` 交給 git，不出現在 argv、remote URL、log 與錯誤訊息中。 |
| VAULT-R9 | `git` 的錯誤分類：連線失敗、逾時、遠端 5xx 是 `retryable`（PIPE-R3）；重試時沿用已經建立的本機 commit，不重複 commit。non-fast-forward 或 `pull --ff-only` 無法快轉時 `publish` 為 `blocked`（`needs_action`），訊息「vault 的遠端分支 <branch> 有本機沒有的提交，Notesteme 不會自動合併。請先同步 vault repo 再重新發布。」，動作為「重新發布」；認證失敗時為 `blocked`（`needs_action`），訊息「無法登入 vault 的遠端（認證失敗）。請更新 vault <id> 的憑證。」，動作為「更新憑證」與「重新發布」。 |
| VAULT-R10 | hook 只能在設定檔定義（`hooks.<id>: {argv, timeout, max_output, env_allowlist}`，ADR-0013）；處理方案的 `outputs[].hooks` 只能列 hook 的 id，不存在時回 422（`outputs.N.hooks.M`，「沒有這個 hook：<id>。hook 只能在設定檔定義。」），寫成物件時是「必須是字串。」。`argv` 是字串陣列，第一個元素是程式；元素中可以使用 `{{note_path}}`（筆記的絕對路徑）、`{{vault_root}}`、`{{item_id}}`、`{{title}}`、`{{commit}}`，每個變數只會展開在它所在的那個元素內，不經過 shell；使用其他變數時載入設定失敗。`{{title}}`、`{{commit}}` 是不可信的值：建議寫成帶固定前綴的元素（例如 `--title={{title}}`），或在它們之前放 `--`；元素以這兩個變數開頭、展開後以 `-` 開頭時，前面加一個空白，不會被當成選項（V15）。 |
| VAULT-R11 | hook 的執行：所有檔案寫入（與 `git push`）成功之後，每個列出的 hook 執行一次，工作目錄 `/data/tmp/<task>`，以 process group 執行（ADR-0009）。環境變數只有 `env_allowlist` 列出的變數加上 `NOTESTEME_ITEM_ID`；`env_allowlist` 不能包含 `NOTESTEME_MASTER_KEY`、`NOTESTEME_PASSWORD`、`NOTESTEME_OIDC_CLIENT_SECRET`、`NOTESTEME_HUB_SESSION_KEY`、`NOTESTEME_HUB_MASTER_KEY` 與名稱以 `_API_KEY`、`_TOKEN`、`_DSN`、`_PASSWORD`、`_SECRET`、`_MASTER_KEY`、`_SESSION_KEY`、`_SECRET_ACCESS_KEY` 結尾的變數（V15），否則載入設定失敗（「hooks.<id>.env_allowlist 不能包含 <變數>。」）。`timeout` 預設 `30s`、最多 `5m`，逾時時結束整個 process group；stdout 與 stderr 合計只保留前 `max_output`（預設 64 KiB）寫進 log。hook 失敗或逾時不影響已寫入的檔案：`publish` 仍為 `done`，影音顯示提醒「hook <id> 失敗（結束碼 N）。檔案已經寫入。」或「hook <id> 逾時（N 秒），已停止。檔案已經寫入。」，動作為「重新發布」。 |
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
- V4：`pull --ff-only` 會把遠端的新提交帶進 clone，但只會快轉，不產生 merge commit；這和 ADR-0010「不自動 merge」一致。只有會被快轉覆蓋的本機變更（遠端新提交也改了同一個檔案）會讓 pull 失敗，歸類為 `needs_action`（同 non-fast-forward 的訊息）；clone 中其他未追蹤或未提交的檔案不影響 pull，也不會被 commit（VAULT-R7）。
- V5：`fs` vault 的 `path` 不套用 GRD-R7 的 `media_roots` 限制，因為它只能寫在設定檔（和 OUT 的 `folder` 目標不同，`folder` 可以由持有 `ingest` 的 token 指定）。
- V6：hook 的逾時以真實時間計算（`toolexec` 的 context），場景用 1 秒的逾時與會一直等待的假 hook（`features/internal/fakehook` 的 `--hang`），不用 fake clock。
- V7：同一部影音同時有 `vault` 與其他輸出目標時，各目標各自判斷略過與擋下；任何一個 `blocked` 時 `publish` 為 `blocked`，已寫入的檔案保留，hook 不執行。
- V8：SRT 的 wikilink 在 `note` 步驟就要知道 vault 的版面（VAULT-R2），所以筆記依生效設定中的第一個 `vault` 輸出計算路徑；沒有 `vault` 輸出時影片資訊表不列字幕。之後在 `publish` 改了 `folder` 而沒有重跑 `note`，連結會指到舊路徑；`publish` 偵測到這種情況時以影音的提醒說明「筆記中的字幕連結可能不正確，請從 note 重新處理。」。
- V9：M3-S0 的設定檔驗證（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：vault 與 hook 的 id 只能用小寫英數字、`_`、`-`（同 LLM provider，因為會變成 `NOTESTEME_VAULT_<ID>_TOKEN`），`-`／`_` 互換後相同的兩個 vault id 拒絕。`fs` 不能寫 `remote`、`branch`、`author`；`git` 不能寫 `path`。`remote` 另外拒絕帶密碼的 https 網址（憑證只能經 `GIT_ASKPASS`）、https 的帳號、以 `-` 開頭的值與 `host::helper`。`branch` 不能以 `-` 開頭、不含 `..`、不以 `.lock` 結尾；`author` 必須是「名稱 <email>」。`base` 不能是絕對路徑或含 `..`。hook 的 `argv[0]` 不能使用變數；`timeout` 用 Go 的期間格式（`2s`、`1m`），大於 0、最多 `5m`；`max_output` 接受位元組數或 `KiB`、`MiB`，最多 16 MiB；`env_allowlist` 的名稱必須是有效的環境變數名稱，禁用的名稱不分大小寫。
- V10：M3-S0 的發布（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：處理方案可以儲存 `target: vault`，但 VAULT 的切片實作之前，`publish` 遇到 vault 目標會以 permanent 失敗（「vault 輸出目標還沒有實作」），不會寫任何檔案。`GET /api/vaults` 已註冊為 501 stub（`admin`）。
- V11：`outputs` 的主鍵維持 `(item_id, path)`（ADR-0020 第 8 點），vault 的 `path` 相對於 vault 根目錄（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：同一部影音寫到兩個 vault 的同一個相對路徑時，兩筆紀錄會互相覆蓋。場景沒有這種情況；VAULT 的切片若要支援多個 vault，主鍵要改成含 `vault`（另一個 migration）。
- V12：M3-S5／S6 的場景修改提案（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認；已依此提案修改 `.feature` 的 5 個場景）：`The note links its subtitles by the full vault path`、`Publishing an unchanged note pushes nothing new`、`A rejected push asks the owner to sync the vault`、`Bad credentials ask for new ones`、`A network error is retried without a second commit` 在場景中改了預設處理方案（`vault-note`、`vault-git`），但影音已在 Background 的 `the note of … is:` 以當時的預設處理方案 `vault-test` 建立；依 PRF-R3，`reruns … from …` 沿用影音記錄的處理方案，所以 note 不會執行、git vault 不會被用到，這 5 個場景目前 failed。最小修改：把這些場景的 `When/And the owner reruns "yt:vlt001xxxxx" from "<stage>"` 改成 `When the owner sends "POST /api/items/yt:vlt001xxxxx/rerun" with:`，body `{ "from": "<stage>", "profile": "<該場景的處理方案>" }`；以這個修改後的副本執行，`@M3` 的 VAULT 27 個場景全部通過。不採用的替代：讓 seeding 建立的影音不記錄處理方案（等於在 harness 繞過 PRF-R3）。
- V13：M3-S5 的寫入細節（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：
  - `outputs` 加欄位 `state`（migration 00022，預設 `written`）：最近一次發布時每個檔案的結果（VAULT-R5）。`skip` 擋下的檔案只更新 `state` 為 `skipped_user_changed`，不動 `sha256`（下次仍認得使用者改過）；另存的新檔記為 `saved_as_new`；內容相同時是 `unchanged`。V11 的主鍵維持 `(item_id, path)`，沒有改。
  - 同一個時間寫出的檔案在 API 中依種類排列（筆記、圖片、字幕），再依路徑。
  - 檔名：title 經 `subtitle.DownloadFileName` 的規則；每一段最多 200 位元組、整個相對路徑最多 240 字元，以筆記與最長的字幕檔名（保留 40 字元給 `.<語言>.bilingual.srt`）計算，超過時從尾端截短標題；base 與 folder 本身就超過時 `permanent`。不做 Unicode 正規化（VAULT-R3 沒有要求，沿用下載檔名的處理）。筆記嵌入的畫面檔名沿用筆記中的名稱（NOTE-R9），不另外截短。
  - 字幕檔名：`lang` 有兩個以上的語言時是 `<名稱>.<語言>.<ext>`，雙語 SRT 是 `<名稱>.bilingual.srt`（避免互相覆蓋）；只有一種語言時是 `<名稱>.<ext>`。
  - 沒有筆記時只寫字幕（步驟 `done`，訊息「這部影音還沒有筆記，只寫出字幕。」）；沒有字幕時只寫筆記與圖片（訊息「沒有字幕，未寫出字幕檔。」）；指定的語言沒有字幕時同 OUT 的訊息。筆記來源紀錄的 `checks.passed` 為 `false` 時 `blocked`（`needs_action`），動作「重新產生筆記」，不寫任何檔案。
  - symlink：路徑的每一段先 `Lstat`，遇到連結時經 `os.Root` 解析，解析失敗（指向 vault 外、懸空）就拒絕（VAULT-R4 的訊息）；`os.Root` 一律把絕對路徑的連結視為離開根目錄，所以 vault 內以絕對路徑指向 vault 內的連結也會被拒絕。檢查之後才被換成連結的情況由 `os.Root` 的寫入本身拒絕。最後一段是指向 vault 內的連結時視為使用者的檔案（同 OUT，依 `on_user_change`）。
  - 筆記與它引用的 `frames` 版本在寫入圖片後標成 `pinned`。
  - V8 的提醒：只有筆記記錄了字幕連結、而且與這次 publish 的版面不同時才提醒（seeding 的筆記沒有連結）。
  - `GET /api/vaults` 只回 `{vaults: [{id, adapter, base}]}`，依 id 排列；不列 hook（UI 選用 hook 的來源另外決定）。
  - 刪除影音（VAULT-R12）：`fs` vault 依 DEL-R9 只刪 hash 相同的檔案（`os.Root` 之內）；~~`git` vault 的刪除 commit 與推送還沒有實作，檔案保留，`kept_outputs` 的原因是 `git_vault`~~（已實作，見 V16）；設定檔已沒有那個 vault 時原因是 `vault_not_configured`。
- V14：M3-S6 的 git 與 hook（2026-11-23，依 /goal 採 advisor 建議，待 owner 追認）：
  - 每次執行 git 另外帶 `-c core.fsmonitor=false`；commit 帶 `-c user.name=… -c user.email=…`（由 `author` 拆出；`GIT_CONFIG_GLOBAL=/dev/null` 時沒有 committer 身分，git 會拒絕 commit）與 `--no-verify`。commit 訊息壓成一行、去掉控制字元。`git add --`、`clone … -- <remote> <dir>`、`push origin HEAD:refs/heads/<branch>`。
  - 憑證只支援環境變數 `NOTESTEME_VAULT_<ID>_TOKEN`；secrets 中的 `vault:<id>` 還沒有實作。askpass 是暫存目錄中的 `/bin/sh` 腳本，帳號固定 `x-access-token`、密碼讀環境變數，腳本內容不含 token；錯誤訊息中的 token 換成 `***`。git 的環境另外傳 `SSH_AUTH_SOCK`（有設定時）。
  - 有沒有要推送：比較 `HEAD` 與 `refs/remotes/origin/<branch>`（追蹤分支不存在而本機有 commit 時也推送），所以重試時沿用已建立的本機 commit。遠端分支還不存在（空的 repo）時不 `pull`。
  - 有檔案被 `skip` 擋下時，這次寫入或另存的其他檔案仍然 commit 與推送（V7：已寫入的檔案保留），之後 `publish` 為 `blocked`，hook 不執行。
  - 錯誤分類依 git 的 stderr（clone、pull、push 都適用）：認證（`Authentication failed`、`Permission denied (publickey)`、`terminal prompts disabled`、HTTP 401／403）、被拒（`non-fast-forward`、`Not possible to fast-forward`、`[rejected]`、`fetch first`）、網路（`Could not resolve host`、`unable to access`、連線逾時或被拒、HTTP 5xx、`early EOF`；git 本身逾時 5 分鐘）；其他是 `permanent`。
  - 同一個 vault 的發布在程序內互斥（同一個 clone 不能同時有兩個 git 操作）。
  - `publish` 的資源類別仍是 `local`（偏離 ADR-0020 第 3 點「有 git vault 時 `download`」：建立任務時還沒有讀生效設定）。
  - hook：`argv[0]` 不是絕對路徑時在 notesteme 的 `PATH` 中尋找；環境只有 `env_allowlist` 中、而且有設定的變數，加上 `NOTESTEME_ITEM_ID`（執行時再擋一次禁用的名稱）；stdout 與 stderr 合計只保留前 `max_output`，其餘丟棄但不結束 hook；輸出寫進 info log。無法執行時的提醒是「hook <id> 無法執行。檔案已經寫入。」。多個 vault 輸出都有 hook 時，全部目標寫完、沒有任何目標被擋下之後才依序執行。
  - Docker 映像加上 `git` 與 `openssh-client`（`deploy/docker/Dockerfile`）；真實的 git 服務（空的 repo 的第一次 clone、ssh remote、真實的認證錯誤訊息）與 Obsidian 中的顯示屬於 owner 驗收。
- V15：安全修補 C（2026-11-24，依 /goal 採 advisor 建議，待 owner 追認）：
  - git 的環境另外帶 `GIT_LITERAL_PATHSPECS=1`：git 預設把 `add --` 之後的路徑當萬用字元，檔名來自影片標題（`Segment` 保留 `[`、`]`），`[A-Z] - 2026-01-01.md` 會一併加入同一個資料夾裡使用者還沒提交的 `B - 2026-01-01.md`，違反 VAULT-R7。
  - git 不跟隨 https 的轉址（`-c http.followRedirects=false`；預設會跟隨初次轉址，轉到其他主機時憑證與推送的內容都會跟著過去）。askpass 比對 git 提示中的主機（不分大小寫、含 port）與設定的 `remote` 主機（環境變數 `NOTESTEME_GIT_HOST`，非 https 的 remote 為空字串）：不符或提示沒有網址時回空字串，並在 stderr 寫「notesteme: 拒絕把 vault 的憑證交給 <主機>（設定的 remote 主機是 <主機>）。」，這段文字會留在 git 的錯誤訊息中（認證失敗，`needs_action`），不含 token。代價：轉址到新網址的 remote 會失敗，要在設定檔改成新網址。
  - hook 的 `{{title}}`、`{{commit}}`：場景（`features/VAULT-vault-output.feature` 的 `notify`）把 `{{title}}` 當成獨立的元素，所以不在載入設定時拒絕；改成執行時，元素以這兩個變數開頭、展開後以 `-` 開頭就在前面加一個空白（getopt、pflag、argparse 等都把它當成一般參數），標題本身其他部分不變。代價：這種標題傳給 hook 時多一個前導空白。另外修正 `{{ title }}`（大括號內有空白）在設定檔通過檢查、執行時卻沒有展開的問題：執行時與設定檔用同一個變數的正規表示式（`vault.HookVar`）。
  - hook 的禁用變數改成單一來源（`vault.ForbiddenHookEnv`，設定檔載入與執行時共用），另外禁止 OIDC 的 client secret、Hub 的 session／master key 與名稱以 `_DSN`、`_PASSWORD`、`_SECRET`、`_MASTER_KEY`、`_SESSION_KEY`、`_SECRET_ACCESS_KEY` 結尾的變數（VAULT-R11 只列了 master key、密碼、`_API_KEY`、`_TOKEN`）。
- V16：`git` vault 的刪除（VAULT-R12，2026-11-24，依 /goal 採 advisor 建議，待 owner 追認）：
  - `DELETE /api/items/:id` 帶 `delete_outputs: true` 時，`git` vault 的紀錄依 vault 分組，每個 vault 在請求中同步處理一次：取得該 vault 的發布鎖 → `git pull --ff-only`（先同步，遠端有使用者改過的版本時 hash 不同，檔案保留）→ 在 clone 中只刪 hash 與紀錄相同的檔案（DEL-R9，`os.Root` 之內）→ `git ls-files --cached -z -- <路徑>` 取出其中有追蹤的 → `git rm --cached --quiet -- <路徑>` → 一個 commit（訊息「notesteme: 刪除 <標題>」，作者與 committer 同 VAULT-R7；`author` 沒有設定時用預設的 `Notesteme <notesteme@localhost>`）→ 本機領先遠端時 `git push`。沒有任何檔案要刪時不 commit、不 push。整個 vault 的期限 3 分鐘（含等鎖），不受請求取消影響（同 DEL 的善後）；刪除仍然成功，失敗只反映在 `kept_outputs`。
  - 失敗的處理沿用 VAULT-R9 的分類，`kept_outputs` 的原因是 `git_unreachable`（連線失敗、逾時、5xx）、`git_rejected`（non-fast-forward、無法快轉）、`git_auth`（認證失敗）、`git_failed`（找不到 git、clone 不能用等）；原本的 `git_vault` 不再出現。同步就失敗時 clone 中什麼都沒動、所有檔案保留；commit 之後推送失敗時，刪除留在本機的 commit，這次刪除的檔案也列為保留（遠端仍有它們）。不回滾（clone 中可能有使用者還沒提交的檔案，不自動 reset，ADR-0010）。
  - 重試：刪除沒有自己的任務可以重試；留在本機的刪除 commit 由下一次發布到同一個 vault 時一併推送（VAULT-R9「沿用已建立的本機 commit」）。被拒絕（`git_rejected`）時同發布：使用者要先處理遠端，Notesteme 不自動合併。是否要提供單獨的「重新推送 vault」動作，待 owner 決定。
  - 還原：被刪除的檔案由還原後的 `publish` 重跑寫回並重新 commit（DEL-R9）；推送失敗留在本機的刪除 commit 會和重新寫回的 commit 一起推送，遠端的結果是檔案仍在。
  - 測試：`internal/pipeline/vault_delete_real_test.go` 用真的 git 與本機 bare repo（測試在產品的 `-c` 之後補 `protocol.file.allow=always`）驗證成功、遠端改過的檔案保留、沒有 commit 身分、推送時遠端多了 commit（non-fast-forward）；認證、連線與其他錯誤用腳本化的 git。驗收場景用假 git（`features/internal/fakegit` 加上 `ls-files`、`rm --cached`）。真實的 git 遠端（GitHub、GitLab 的 https／ssh）沒有實測。

# 切片工作規程

> 用途：委派 subagent／child 實作一個切片時的共用規則，避免每次把同一段話貼進 prompt。現況與切片順序見 [handoff.md](handoff.md)，規格規則見 [specs/README.md](specs/README.md)。

## 1. 協作方式（owner 的 /goal，2026-11-22）

- MVP 階段**不開 PR**：切片在獨立的 worktree 與分支完成、`git rebase origin/master` 之後，由主代理 `git push origin HEAD:master`（不 force），推送後確認 master 的 CI 是綠的。
- child 只改自己的 worktree 與分支；**不要 push、不要開 PR、不要碰主 checkout 或其他 worktree**；做完在分支上 commit，由主代理驗證後合併。
- 需要決策時：child 採保守預設、寫進該規格的「待決問題」並標「待 owner 追認」，在報告列出「需要決策」（附 `.feature` 原文、預設、影響）；主代理向 advisor 確認。

## 2. 規則（來自 owner，不能違反）

- **規格優先**：行為和規格不一致時，不可以為了讓測試通過去改 `.feature`；先寫進待決問題。規格與場景要改時放在同一個 commit。
- **只用 `steps.md` 的句型**；需要新句型時先加進 `steps.md` 並在報告列出。
- **紅燈要紅得正確**：寫完 step definition 先跑一次，確認失敗原因是「功能還沒做」（501 → pending 的判斷可以暫時關掉看真紅燈，看完還原）。
- 註解用繁體中文、簡潔；sqlc 查詢的 SQL 註解用英文；程式碼識別符用英文。
- commit message：`<emoji> <type>(<scope>): <description>`（description <60 字元、小寫、祈使語氣），一個 commit 一個 type，小步多次。例：`✨ feat(correct): glossary and term candidates`。
- 不用 `git stash`／`reset --hard`；`features/passing.txt` 只能新增行（`-update-passing`）。
- 不要把真實 API key 或個人資訊寫進輸出或 commit；字幕、LLM 輸出都是不可信輸入。

## 3. 環境注意事項

- 本機 `internal/toolexec` 的 `TestRunKillsWholeProcessGroupOnCancel` 在 WSL 可能失敗，CI 綠，忽略。
- hook 會把 Bash 指令改寫成 `rtk …`；第一次被拒絕時，原樣重送被改寫後的指令即可。
- 機器記憶體吃緊（同時有多個 child 與其他 session）：平時只跑受影響的 package 與 `-godog.tags`；全套 `go test -race -timeout 25m -p 2 ./...` 只在交付前跑一次；`govulncheck` 在本機會被 OOM kill，交給 CI。
- `-godog.*` 旗標要寫 `./features`，不要寫 `./features/...`；godog 的 tag OR 用逗號：`-godog.tags='@A,@B'`。

## 4. 交付前驗證（全部跑完並貼結果摘要）

```
go build ./... && go vet ./... && gofmt -l .
go test -count=1 -race -timeout 25m -p 2 ./...
go test ./features -run TestFeatures -godog.tags='@M2' | tail -40      # 目標場景要由 pending/failed 變 passed
python3 -I scripts/spec/check_steps.py . ; python3 -I scripts/spec/check_trace.py .
uv run --with gherkin-official python -I scripts/spec/parse_all.py features
go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 diff -f internal/store/sqlc.yaml   # 動過 SQL 才需要，並跑 vet
go test ./features -run TestFeatures -update-passing                    # 只新增行，確認 diff 只有新增

# 動過 API 路由或欄位（Huma operation、輸入輸出的 struct）時：重產 OpenAPI 快照與前端型別，兩個檔案一起 commit
go run ./internal/api/cmd/openapi -o internal/api/openapi.json          # 否則 go test 的 TestOpenAPISnapshot 失敗
(cd web/client && npm ci && npm run gen:api)                           # 否則 web.yml 的 check:api 失敗（需要 Node 24.12）
```

- rebase 到 origin/master 之後，如果 master 上有其他切片改了路由，要再重產一次上面兩個檔案（快照有衝突時直接重產，不要手動合併）。

- **不能有任何原本 passed 的場景退步**：用 `go test -v ./features -run TestFeatures -godog.tags='@M2' -godog.format=junit > old.xml` 輸出 `@M2` 的逐場景結果（**要加 `-v`**：全部通過時沒有 `-v` 會是空的 XML；輸出前面有 `=== RUN` 行，解析時從 `<testsuites` 開始切），與切片開始前的基準逐一比對。預設 tag 已涵蓋 `@M1` 與 `@M2`，`go test ./features -run TestFeatures` 跑的就是全部 308 個。
- 變異測試：挑 5～10 個關鍵檢查暫時破壞，確認對應場景或單元測試會變紅，再還原。
- 不可信輸入的解析器要有單元測試與 fuzz。

## 5. 交付報告（最後一則訊息，精簡）

1. 分支名與 commit 清單（`git log --oneline origin/master..HEAD`）。
2. `@M2` 前後數字、哪些場景由 failed／pending 變 passed、仍未通過的場景與原因。
3. 需要決策的項目。
4. 偏離規格或新增的句型／API／migration／設定欄位。
5. 已知限制與下一個切片要注意的事。

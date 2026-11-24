# 人工驗收清單

> 狀態：草案（2026-11-25，依 /goal 由實作者暫定，待 owner 追認）
> 相關文件：[規格與驗收](specs/README.md)（5.2 的 `@manual` tag、6.2 Definition of Done）、[非功能需求](specs/nfr.md)、[UI 設計語言](ui/design-language.md)、[UI 規格](ui/README.md)、[交接文件](handoff.md)

## 1. 用途與記錄方式

自動化檢查（CI、`go test ./features/...`、vitest、axe）涵蓋不到的項目，要由人在真實環境驗收。這份文件列出這些項目與具體的檢查步驟：

- 第 2～9 節：`nfr.md` 中驗證方式含 `manual` 的非功能需求（NFR-RES-6、NFR-REL-6、NFR-CMP-2、NFR-A11Y-2、NFR-A11Y-3、NFR-I18N-3、NFR-RWD-1；第 9 節的 NFR-PERF-10 也含 `manual`，一併列出）。
- 第 10 節：全部 37 個 `@manual` 場景（3 個非 UI、34 個 `@ui @manual`）。

依據：`specs/README.md` 的 Definition of Done（6.2）要求「`@manual` 場景已人工驗收」；`nfr.md` 第 1 節規定 `manual` 項目在每個里程碑結束前人工檢查，沒有達標視同驗收場景失敗，不能把該里程碑標記完成。UI 改版後，受影響的項目要重做。

### 1.1 結果怎麼記錄

6.2 寫的是「記錄在 PR 中」，但 MVP 階段不開 PR（直接推 master），所以改用下面兩種方式擇一：

1. 記在 `docs/handoff.md` 的「人工驗收紀錄」表。**目前 handoff.md 還沒有這一節**：第一次驗收時，在「下一步」之後新增 `### 人工驗收紀錄` 小節並貼上下面的模板。
2. 一份帶日期的 issue 或筆記（標題 `人工驗收 YYYY-MM-DD`），內容同樣用下面的表格；在 handoff.md 的紀錄表中留一行連結。

表格模板（一個項目一列；一次驗收多個項目就多列）：

| 日期 | 驗收者 | 項目 | 結果 | 備註 |
|---|---|---|---|---|
| YYYY-MM-DD | 姓名或帳號 | 例如 `NFR-A11Y-2`，或 `features/LIB-library.feature` 的「Filters survive a reload」 | 通過／未通過／部分通過／無法驗收 | 版本（`GET /api/system` 的程式版本或 git commit）、環境（瀏覽器與版本、裝置、參考環境）、失敗的步驟與截圖或連結 |

- 「結果」只用四個值：**通過**（每個預期結果都成立）、**未通過**（有任何預期結果不成立）、**部分通過**（寫清楚哪幾步沒過）、**無法驗收**（環境不足，寫缺什麼）。
- 未通過的項目要另外開 issue 並連回這一列；該里程碑在問題修好、重新驗收通過之前不標記完成。
- 備註一定要有版本與環境，否則無法判斷之後的改動是否讓結果失效。

### 1.2 環境用語

第 10 節的「需要的環境」用下面幾個簡稱：

| 簡稱 | 意思 |
|---|---|
| Client | 執行中、已登入的 Client（`notesteme serve`）加桌面瀏覽器。測試實例即可，但資料要符合場景的 Given（例如有失敗的步驟、有處理中的影音）。 |
| 手機 | 真實的手機（iOS 或 Android）加手機連得到的 HTTPS 位址。`compose.yaml` 只綁 `127.0.0.1`（OPS-R9），手機要用 `vps` profile 的 Caddy（OPS-R19）或自己的隧道、反向代理。 |
| 真 YouTube | 能連外網，用真實的 YouTube 影片或頻道。 |
| Hub | 執行中的 `notesteme-hub` 加瀏覽器；OAuth 要用真實的供應商（Google、GitHub、Linux.do）。M5 驗收的 Hub 用 Postgres 加 S3 相容儲存（`docs/hub-deployment.md`）。 |
| IdP | 真實的 OIDC 供應商，Client 的設定檔已啟用 OIDC（AUTH-R16）。 |

## 2. NFR-RES-6：`ref-vps` 無人值守一週

- 目標（`nfr.md` 第 3 節）：`ref-vps` 上無人值守運作一週，RSS 沒有持續成長（一週內的增量 < 20%）。M4。
- 需要的環境：符合 `ref-vps`（2 vCPU、4GB RAM、SSD）的主機；`vps` profile 已部署（HTTPS、有登入密碼）；至少一個訂閱（真實頻道或可重複探索的 feed）、一個已勾選 `alert` 與「失敗」的通知通道、一組可用的 provider key 與預算。一週內不重啟、不手動操作（除了記錄）。

步驟：

1. 部署並確認健康：`cd deploy/docker && docker compose up -d`，`docker compose ps` 全部 healthy，`curl -fsS http://127.0.0.1:8080/readyz` 回 200。用 `GET /api/system` 記下程式版本與 yt-dlp、ffmpeg 版本。
2. 讓它自己跑：訂閱每天探索新內容並處理，不要手動新增、不要重啟。需要時只調整預算與 provider。
3. 暖機 1 小時後記下基準值：`docker top $(docker compose ps -q notesteme) -eo pid,rss,comm`，取 `comm` 是 `notesteme` 那一列的 RSS（KB；不含 yt-dlp、ffmpeg 子程序的列）。這是 t0。
4. 每 6 小時記錄一次同樣的 RSS（可以用主機的 cron 寫進檔案），同時記下當時有沒有任務在處理（`GET /api/queue` 或 UI 的佇列）。
5. 7 天後記下最後一筆，並檢查：`docker inspect -f '{{.RestartCount}} {{.State.StartedAt}}' $(docker compose ps -q notesteme)` 沒有重啟；`GET /api/system` 的 `alerts` 沒有 `stuck_task`、`job_stale`；總覽沒有被丟棄的通知。

預期結果：

- （最後一筆 RSS − t0 的 RSS）÷ t0 的 RSS < 20%。
- 曲線沒有隨時間單調上升（處理期間的尖峰會回落）；閒置時 RSS 仍符合 NFR-RES-1（< 150MB），處理中 < 500MB（這兩項只記錄，不影響本項結果）。
- 一週內沒有重啟，也沒有卡住的任務或失敗的排程。

## 3. NFR-REL-6：每日備份最多遺失 24 小時

- 目標（`nfr.md` 第 4 節）：每日備份（OPS-06）讓資料遺失最多 24 小時。M4。
- 需要的環境：`ref-vps` 或 `ref-wsl`；設定檔的 `backup.daily.dir` 指到另一個 volume（例如 `/backups`，OPS-R11、`compose.yaml` 的註解）；至少運作 2 天；另一個空的資料目錄用來還原；保存好 `NOTESTEME_MASTER_KEY`（備份不含它，README 的警告）。

步驟：

1. 設定 `backup.daily`（預設每天 `03:00` UTC，`enabled` 預設 `true`）。啟動後 `GET /api/system` 的 `backup` 應有 `next_run_at`。
2. 讓實例正常使用：每天新增或修改幾部影音。記下最後一次寫入資料的時間，以及備份執行前後各一筆「標記資料」（例如標題含時間戳的影音）。
3. 等到至少兩次排程執行後，檢查備份目錄：有 `notesteme-backup-<時間>.tar.gz`，連續兩份的時間差不超過 24 小時，`notesteme-backup-latest.tar.gz` 指向最新一份；`GET /api/system` 的 `backup.last_ok_at` 是最近一次成功的時間。
4. 模擬停機錯過排程：在排程時間之前停掉實例，過了排程時間再啟動，確認只補跑一次（OPS-R11）。
5. 還原到乾淨的環境：另用一個空的資料目錄（`restore` 只能在該資料目錄的 `serve` 停止時執行，OPS-R2），執行 `notesteme restore <備份檔> --data <空目錄>`，再用同一把 `NOTESTEME_MASTER_KEY` 啟動 `serve` 並登入。
6. 檢查還原後的資料：備份時間點之前的標記資料都在，之後新增的標記資料不在。

預期結果：

- 任何時間點，最新一份可還原的備份距離現在不超過 24 小時（連續兩份備份的間隔 ≤ 24 小時）。
- 還原後最多遺失「備份時間點到故障時間點」之間的資料，這段時間 ≤ 24 小時。
- 備份目錄只保留最新 `backup.daily.keep`（預設 7）份（OPS-R12），不刪除其他檔案。

## 4. NFR-CMP-2：瀏覽器支援

- 目標（`nfr.md` 第 7 節）：Web UI 支援 Chrome、Edge、Firefox、Safari 的最新兩個主要版本，以及 iOS Safari 與 Android Chrome。M1。
- 需要的環境：Client；各瀏覽器各兩個主要版本（桌面）；iOS Safari 與 Android Chrome 的實機（手機）；一部有字幕與時間帶的真 YouTube 影片。

對每個「瀏覽器與版本」做下面的檢查，並在紀錄的備註中寫出版本號：

1. 登入（密碼）。總覽、媒體庫、佇列、設定都能開啟，沒有版面破圖、字型缺字或水平捲軸。
2. 主題：設定或側欄的偏好切換淺色、深色、跟隨系統，顏色立即套用（UI 用 `light-dark()`），重新整理後保留；沒有「先閃一下白色再變暗」。
3. 媒體庫：篩選（網址會帶 `?platform=` 等參數）、列與格的切換、捲動；時間帶正常繪製。
4. 影音詳情：YouTube 嵌入播放器載入並可播放（CSP 的 `frame-src`）；點字幕句跳轉播放位置；時間帶的播放頭可以拖曳與用方向鍵移動；`1`–`4` 切換分頁。
5. 「新增」抽屜：貼上 YouTube 網址，預覽與預估花費正常顯示（不需要真的開始處理）。
6. 「更多」選單與對話框：開啟、選取、Esc 關閉；Safari（桌面與 iOS）要特別確認「更多」選單點選後不會在選到項目之前就收起來（見 UI README 10.24 第 1 點與 commit `e3864924`）。
7. 開發人員工具的 Console：整個流程中沒有 CSP 違規與未捕捉的錯誤。
8. 手機（iOS Safari、Android Chrome）另外確認：可以捲動、輸入框不被鍵盤擋住、抽屜與對話框可以關閉；加到主畫面（PWA，ENTRY-R3）後以 App 開啟仍能登入。

預期結果：每個瀏覽器與版本的上面各項都成立；任何一個失敗都記錄瀏覽器、版本與失敗步驟。

## 5. NFR-A11Y-2：全鍵盤可用、焦點框、目標 24px

- 目標（`nfr.md` 第 8 節）：所有功能都能只用鍵盤完成；焦點框依 UI 規格；可點擊目標至少 24×24px。M1。UI 規格見 [README 第 6 節](ui/README.md#6-無障礙)、[design-language 第 3.3、6.1 節](ui/design-language.md)。
- 需要的環境：Client；桌面瀏覽器（至少 Chrome 與 Firefox 各一次）；不使用滑鼠或觸控板；有各種內容的資料（有字幕與章節的影音、佇列中的任務、一個失敗的步驟）。

步驟與預期結果：

1. **跳過連結與換頁焦點**：重新載入任何頁面後第一個 Tab 停在「跳到主要內容」；按 Enter 焦點移到 `<main>`。用鍵盤換頁（連結、`G` 然後 `L`／`Q`／`S`）後，焦點都移到新頁的 `<main>`。
2. **每個互動元素都可達**：對總覽、媒體庫、影音詳情（四個分頁）、新增抽屜、佇列、訂閱、Prompt 庫、處理方案、成本、設定的每個分區、登入頁，逐一 Tab 與 Shift+Tab。每個按鈕、連結、輸入框、核取方塊、下拉選單、字幕句、時間帶都能到達並操作（Enter、Space、方向鍵）；Tab 順序與視覺順序一致；沒有鍵盤陷阱（對話框內的焦點鎖定除外）。
3. **焦點框**：每個取得焦點的元素都有可見的外框：淺色主題 2px Ink 外框、間隔 2px；深色主題 2px Cue（程式是 `:focus-visible` 的 `outline: 2px solid var(--focus)`，`outline-offset: 2px`）。特別檢查圖示按鈕、時間帶、字幕句、底部分頁列、選單項目。外框不能被父元素裁掉（`overflow: hidden`）。
4. **對話框、抽屜、選單**：開啟後焦點進入；Tab 被鎖在裡面；Esc 關閉；關閉後焦點回到觸發的按鈕。「更多」選單按 Esc 關閉，焦點回到「更多」；選了項目後若打開對話框，對話框關閉時焦點回到「更多」。
5. **快捷鍵**（UI README 3.6）：`Ctrl K`（macOS `⌘ K`）指令面板、`N` 新增、`/` 聚焦搜尋、`G` 然後 `L`／`Q`／`S`、`?` 說明；影音詳情的 `Space`、`←`／`→`（5 秒）、`[`／`]`（章節）、`1`–`4`。焦點在文字輸入框時，單鍵快捷鍵不作用；對話框開著時全域快捷鍵不作用。
6. **時間帶**（完整版是 `role="slider"`）：`←`／`→` 移動 5 秒，`PageUp`／`PageDown` 30 秒，`Home`／`End` 到頭尾，不超出範圍；`aria-valuetext` 是時間碼。
7. **三個代表任務只用鍵盤完成**：(a) 新增一個網址並開始處理；(b) 在字幕句上移動、跳到某個時間；(c) 處理一個「需要你處理」的項目（重試或取消），再刪除一部影音並用「復原」還原。
8. **目標大小 ≥ 24×24px**：在 767px 以下與桌面寬度各做一次。可以在 Console 執行下面的片段，列出小於 24px 的互動元素，逐一判斷（行內文字連結不在 WCAG 2.5.8 的範圍內）：

   ```js
   [...document.querySelectorAll('a,button,input,select,textarea,[role=button],[role=slider],[role=tab],[tabindex]')]
     .filter(e => { const r = e.getBoundingClientRect(); return r.width && r.height && (r.width < 24 || r.height < 24); })
     .map(e => e.outerHTML.slice(0, 100))
   ```

   預期結果：清單是空的，或只剩行內文字連結與被視覺隱藏的元素（例如 skip link 在失去焦點時）。底部分頁列的項目另外確認高度（程式是 44px）。

## 6. NFR-A11Y-3：狀態不只靠顏色、時間帶文字版本、reduced-motion

- 目標（`nfr.md` 第 8 節）：狀態不只靠顏色區分；時間帶都有文字版本；`prefers-reduced-motion` 時沒有動畫。M1。UI 規格見 [design-language 第 6.1、6.4、7 節](ui/design-language.md)。
- 需要的環境：Client；桌面瀏覽器（Chrome 的開發人員工具可以模擬色覺缺陷與減少動態）；一個螢幕閱讀器（Windows NVDA、macOS VoiceOver 擇一）；測試資料要涵蓋每一種狀態：等待中、處理中、稍後檢查（`waiting`）、等待 Agent、完成、已略過、失敗、需要你處理、超出預算、等你決定。

### 6.1 狀態不只靠顏色

1. 開啟媒體庫、佇列、影音詳情（含完整版時間帶）。把頁面變成灰階：Chrome 開發人員工具 › Rendering › Emulate vision deficiencies 選 Achromatopsia；再各做一次 Protanopia、Deuteranopia、Tritanopia。
2. 每個狀態標籤都同時有「圖示形狀」與「文字」，而且彼此分得開（對照 design-language 6.4 的表：空心圓、半實心圓、時鐘、Agent 圖示、勾、斜線圓、叉號、驚嘆號、錢包、問號）。
3. 時間帶每種狀態的紋理不同（design-language 6.1 的表）：完成是安靜的實心；處理中的 chunk 有「顯影」；等待 Agent 是長虛線；需要你處理是點線；稍後檢查是 Pewter 點線；失敗是 Fault 加白色斜線紋；略過不畫並由 tooltip 說明。
4. 預期結果：在灰階與三種色覺模擬下，不看顏色也能分辨每一種狀態與它的文字。

### 6.2 時間帶的文字版本

1. 媒體庫的每一列：時間帶旁的狀態欄用文字寫出目前的步驟與狀態（例如「校正：處理中」）。
2. 精簡版時間帶是 `role="img"`，`aria-label` 是文字摘要（例如「字幕完成，校正 9/16 段，翻譯未開始」）。用螢幕閱讀器停在該列，確認唸出摘要；或在開發人員工具的 Accessibility 面板確認 role 與名稱。
3. 影音詳情的完整版：右側逐列顯示文字標籤；整條是 `role="slider"`，`aria-valuetext` 是時間碼；hover 時顯示時間碼、章節與該時間點的字幕句。
4. 即時更新的宣告：任務完成、失敗、變成「需要你處理」時，螢幕閱讀器以 `aria-live="polite"` 唸出；進度百分比的變化不唸。
5. 預期結果：每一條時間帶的資訊都有對應的文字，不需要看圖形就能得到相同的資訊。

### 6.3 reduced-motion

1. 開啟系統的「減少動態」（Windows：設定 › 協助工具 › 視覺效果 › 動畫效果關閉；macOS：系統設定 › 輔助使用 › 顯示器 › 減少動態）；或在 Chrome 開發人員工具 › Rendering 選 Emulate CSS media feature prefers-reduced-motion: reduce。
2. 開啟一部有 chunk 正在處理的影音（或佇列中的處理中任務），觀察時間帶：「顯影」動畫不動，改成靜態斜線紋；步驟完成時帶子立即填實，沒有 200ms 的過渡。
3. Spinner 不轉動（改成靜態的四分之一實心環）；開啟抽屜、對話框、指令面板沒有位移或淡入；Toast 直接出現與消失；沒有任何會自己移動的元素。
4. 關閉「減少動態」後重新整理：「顯影」恢復（約 1.6 秒一個週期的掃過），抽屜有 200ms 的位移與淡入。
5. 預期結果：開啟時沒有任何持續或進場動畫；關閉時動畫都恢復。

## 7. NFR-I18N-3：Intl 格式化、時間碼不隨語言、字幕 `lang`

- 目標（`nfr.md` 第 8 節）：日期、數字、金額用 `Intl` 格式化；時間碼不隨語言改變；顯示字幕的元素設定字幕自己的 `lang`。M1。UI 規格見 [README 第 7 節](ui/README.md#7-多語系)、[design-language 4.2](ui/design-language.md#42-依語言的字型堆疊)。
- 需要的環境：Client；桌面瀏覽器；一部原文是简中、有校正字幕（繁中）與英文翻譯的影音（或等價的資料），並且有發布日、花費、檔案大小等數字。

步驟與預期結果：

1. **`<html lang>`**：把介面語言依序切到繁中、简中、English；在開發人員工具的 Elements 確認 `<html lang>` 分別是 `zh-Hant`、`zh-Hans`、`en`，字型堆疊隨之切換。
2. **日期與數字**：在三種語言下檢查媒體庫的發布日、事件的時間、佇列的時間、花費與檔案大小。顯示要和同一個瀏覽器的 `Intl` 輸出一致，例如在 Console 執行 `new Intl.DateTimeFormat('zh-Hant', {dateStyle: 'medium', timeStyle: 'short'}).format(new Date())`（語言換成 `zh-Hans`、`en`）與 `new Intl.NumberFormat('en').format(1234567)`，和畫面比對；不應有手寫的格式字串或固定的千分位。
3. **時間碼不隨語言改變**：三種語言下，字幕句的時間碼、時間帶刻度、播放位置、影片長度一律是 `MM:SS` 或 `H:MM:SS`（例如 `06:22`、`1:05:03`），沒有「分」「秒」，也沒有在地化的數字。
4. **字幕自己的 `lang`**：UI 設為繁中，開啟那部原文是简中的影音。在開發人員工具確認：標題 `<h1>` 的 `lang` 是影音的原文語言（`zh-Hans`）；字幕句 `.cue__text` 的 `lang` 是該版本字幕自己的語言（原始與正規化是 `zh-Hans`，校正是 `zh-Hant`，翻譯是 `en`）；筆記與摘要區塊的 `lang` 是筆記與摘要的語言；時間帶 hover 說明的 `lang` 是字幕語言。
5. **字形**：同一個畫面上，简中的標題與字幕用簡體字形（Computed 的 `font-family` 取 `zh-Hans` 的堆疊，例如 PingFang SC、Microsoft YaHei UI、Noto Sans CJK SC），繁中內容用繁體字形；不會因為頁面是繁中就把简中內容也畫成繁體字形。
6. **版面**：English 的字串約比中文寬 30%：在三種語言下檢查側邊導覽、按鈕、表格欄位、對話框都沒有截斷、重疊或固定寬度造成的溢出。
7. **UI 語言與字幕目標語言分開**：改 UI 語言不會改變處理方案的目標語言，反之亦然。

## 8. NFR-RWD-1：767px 以下完成三件事

- 目標（`nfr.md` 第 8 節）：767px 以下的寬度可以完成三件主要的事：查看進度、處理「需要你處理」、貼網址新增。M1。UI 規格見 [README 第 8 節](ui/README.md#8-響應式)。
- 需要的環境：手機（真實手機一支；另外用桌面瀏覽器的裝置模擬 375×667 與 767／768 的邊界各看一次）；Client；有處理中的影音、一個「需要你處理」的步驟（例如超出預算或 needs_action）。

步驟與預期結果：

1. **導覽**：767px 以下側邊導覽消失，底部分頁列有 5 項（總覽、媒體庫、新增、佇列、更多），「新增」在中間；「更多」開啟的對話框列出其餘頁面、偏好（主題、語言）與登出；點頁面後對話框關閉。768px 以上只有側邊導覽，沒有底部分頁列。
2. **查看進度**：總覽的「現在」列出處理中的影音與時間帶；點進影音可以看到步驟狀態；媒體庫的時間帶移到標題下方，狀態欄的文字可讀；佇列頁可以看到各資源類別與任務。
3. **處理「需要你處理」**：總覽的「需要你處理」顯示那個步驟與它的動作按鈕；在手機上完成動作（例如「調整預算」、重試、取消）。預期結果：不需要縮放或橫向捲動，按鈕好點（目標大小見第 5 節），完成後該項目從區塊消失。
4. **貼網址新增**：點底部的「新增」開啟抽屜，貼上 YouTube 網址，看到辨識結果與預估花費，按「開始處理」，回到媒體庫看到新項目。5. **其他**：影音詳情的播放器固定在頂部且不遮住字幕；整個流程沒有水平捲軸，文字沒有被截斷。

## 9. NFR-PERF-10：Web UI 的 LCP 與 10,000 列捲動

這一項不在原本指定的清單內，但 `nfr.md` 第 2 節的驗證方式是 `ci`（bundle 大小）加 `manual`（LCP 與捲動），所以一併列出。

- 目標：初始載入的 JS（gzip 後）< 300KB（CI 已檢查）；`ref-wsl` 上本機連線的 LCP < 2 秒；媒體庫列表虛擬化，捲動 10,000 列不掉幀。M1。
- 需要的環境：`ref-wsl`；Client 載入 `lib-10k` 規模的資料（10,000 部影音、500 個頻道，`nfr.md` 1.2）。用 `go run ./internal/benchdata/cmd/genlib -out <資料目錄> -profile lib-10k` 產生（完整規模約十幾分鐘、數 GB；`-profile item-long` 產生步驟 3 的 3 小時影片），再以 `NOTESTEME_DATA=<資料目錄> notesteme serve` 啟動；產生器的說明見 `internal/benchdata/doc.go`。

步驟與預期結果：

1. LCP：Chrome 無痕視窗、停用快取，用開發人員工具的 Lighthouse（Performance，Desktop，不節流）或 Performance 面板記錄載入媒體庫；LCP < 2 秒。重複 3 次取中位數。
2. 捲動：Performance 面板開始錄製，從頂端連續捲到底（10,000 列，必要時一直載入更多），停止後檢查 Frames：沒有持續的紅色掉幀區段，沒有超過 50ms 的長工作；DOM 中的列數遠少於 10,000（虛擬化）。
3. 影音詳情的字幕句列表（`item-long`，約 3,000 句）做同樣的捲動檢查。

## 10. `@manual` 場景

`grep -n -B3 '@manual' features/*.feature` 找到 37 個場景：3 個非 UI（A）與 34 個 `@ui @manual`（B）。驗收時依該場景的 Given／When／Then 操作，結果依 1.1 記錄，「項目」欄寫 feature 檔名與場景名。

### A. 非 UI（3 個）

| feature 檔 | 場景 | 需要的環境 | 驗收什麼 |
|---|---|---|---|
| [`CLI-cli.feature`](../features/CLI-cli.feature) | The watch skill only calls the CLI | 已改寫的 watch skill、一個 AI agent（例如 Claude Code）、執行中的 Client、一部新的 YouTube 網址 | agent 使用 skill 處理新網址時，只執行 `notesteme ingest` 與 `notesteme export`，不自己下載、轉錄或擷取畫面。 |
| [`CLI-cli.feature`](../features/CLI-cli.feature) | A Windows agent drives the WSL instance | Windows 主機、WSL Docker 內的 `notesteme serve`、`notesteme.exe` | 在 Windows 執行 `notesteme.exe items ls --server http://localhost:8080 --token <token>`，列出的項目和 Web UI 相同。 |
| [`HSHR-hub-sharing.feature`](../features/HSHR-hub-sharing.feature) | The M5 acceptance check with a real Hub | 真實的 Hub（兩個成員）加兩個 Client 實例，一部公開影片 | 第一位分享校正字幕與筆記；第二位以第一位的原始字幕重新生成，不付轉錄費用；Hub 顯示第二個版本是從第一位的字幕重新生成。 |

### B. `@ui @manual`（34 個）

| feature 檔 | 場景 | 需要的環境 | 驗收什麼 |
|---|---|---|---|
| [`AUTH-proxy-oidc.feature`](../features/AUTH-proxy-oidc.feature) | Tokens are created, rotated and revoked in the settings page | Client | 設定 › MCP 與 Agent 建立 token（`claude`、`read, agent`、30 天）時完整 token 只顯示一次並有複製按鈕，輪替與撤銷的確認文字正確，舊 token 立即失效。 |
| [`AUTH-proxy-oidc.feature`](../features/AUTH-proxy-oidc.feature) | The sign-in page offers OIDC and the settings page lists sessions | Client、IdP | 登入頁在密碼欄旁有「用 id.example.com 登入」按鈕；OIDC 登入後，設定 › 登入標出目前的工作階段，其他的都有「撤銷」。 |
| [`BND-bundles.feature`](../features/BND-bundles.feature) | An imported note is shown without raw HTML | Client、含 `<script>` 與 `<img onerror>` 的 bundle | 從設定頁匯入後，筆記分頁把標籤當純文字顯示、不執行腳本，影音頁註明是從 bundle 匯入並顯示來源實例。 |
| [`COST-cost-budget.feature`](../features/COST-cost-budget.feature) | The cost page shows this month against the budget | Client（月預算 $20、本月已花 $3.42） | 成本頁頂端文字、依 provider／頻道／步驟分組的圖表、含 CSV 匯出的明細表，且沒有大數字統計卡。 |
| [`COST-cost-budget.feature`](../features/COST-cost-budget.feature) | Stale prices are shown at the top of the cost page | Client（計價表 145 天前確認） | 頂端出現「計價表已經 145 天沒有確認…」提醒，並有「確認計價表」「調整單價」兩個動作。 |
| [`DEL-delete.feature`](../features/DEL-delete.feature) | The delete dialog explains the consequences and can be undone | Client | 影音詳情的「更多」›「刪除」對話框列出產出數、可釋放的空間與已花金額；確認後出現「已移到垃圾桶。」與「復原」按鈕。 |
| [`DIG-digest.feature`](../features/DIG-digest.feature) | Paused items are picked from the library | Client（三部摘要後暫停的影音） | 媒體庫篩「等你決定」、選兩部「繼續處理」，對話框顯示數量與預估花費，確認後只有選的兩部繼續。 |
| [`ENTRY-entry-points.feature`](../features/ENTRY-entry-points.feature) | The bookmarklet only sends the address of the current page | Client、有書籤列的桌面瀏覽器 | 把書籤小工具拖到書籤列並檢視程式碼：只讀 `location.href`、在新分頁開 `<origin>/add?url=…`，沒有 token、cookie 或存取頁面的 document。 |
| [`ENTRY-entry-points.feature`](../features/ENTRY-entry-points.feature) | Clicking the bookmarklet on a video page fills in the add drawer | Client、真 YouTube、已安裝的書籤 | 在 YouTube 影片頁點書籤，新分頁的新增抽屜帶入該網址與預估花費，按「開始處理」之前不處理任何東西。 |
| [`ENTRY-entry-points.feature`](../features/ENTRY-entry-points.feature) | An address that is not http or https is ignored | Client | 開啟 `/add?url=javascript%3Aalert(1)`，抽屜是空的，並顯示「書籤或分享的網址無效，已忽略。」 |
| [`ENTRY-entry-points.feature`](../features/ENTRY-entry-points.feature) | Sharing from the YouTube app on Android | 手機（Android，已把 Notesteme 安裝成 App、登出狀態）、YouTube App | 從 YouTube App 分享影片到 Notesteme：先開登入頁，登入後新增抽屜顯示分享的網址。 |
| [`FRM-frames.feature`](../features/FRM-frames.feature) | The frames tab shows the picks with their captions | Client（已處理到 `frames` 的影音） | 畫面分頁的印樣顯示選中的畫面與圖說、章節，並可切換成全部候選。 |
| [`GLOS-glossary-review.feature`](../features/GLOS-glossary-review.feature) | The owner reviews candidates in the glossary settings | Client（兩個頻道有待審的術語候選） | 總覽「需要你處理」顯示「有 3 個術語等你確認。」與「去審核」；「改寫後接受」預填原詞並可選頻道或全域；多選拒絕後清單清空，兩個頻道的詞彙表只剩已接受的詞。 |
| [`HACC-hub-accounts.feature`](../features/HACC-hub-accounts.feature) | The sign-in, account and admin pages of the Hub | Hub（Google、GitHub、Linux.do） | 登入頁列出三個供應商；用 GitHub 登入後，帳號頁可連結 Google、移除登入方式、建立與撤銷 Hub token；管理員可以在管理頁指派群組、停用帳號、處理下架申請。 |
| [`HREP-hub-repository.feature`](../features/HREP-hub-repository.feature) | Browsing, comparing and voting in the Hub UI | Hub（同一部影片有多位上傳者與模型的多個版本） | 依語言與校正模型篩選；版本依種類與語言分組並顯示分數、授權、未驗證模型標章；可並排比較；版本頁有來源紀錄、「regenerated from @alice」、下載、投票與檢舉；筆記不渲染原始 HTML。 |
| [`HSHR-hub-sharing.feature`](../features/HSHR-hub-sharing.feature) | Sharing, regenerating and the Hub settings in the client UI | Client、Hub | 「Share to Hub」對話框列出產出、授權、確認勾選與將送出欄位的預覽；從 Hub 轉錄的影音顯示「From Hub: @alice's raw subtitles」；設定的 Hub 分區有唯讀 URL、token、連線測試與查詢開關及說明。 |
| [`LIB-bulk-overview.feature`](../features/LIB-bulk-overview.feature) | The overview puts what needs the owner first | Client（兩部處理中、一個步驟被預算擋住、今天有三個事件） | 總覽「現在」「需要你處理」（「調整預算」「查看成本」）「今天」的內容與順序，底部是「本月花費 $3.42，預算 $20」，沒有待處理事項時「需要你處理」區塊隱藏。 |
| [`LIB-bulk-overview.feature`](../features/LIB-bulk-overview.feature) | The bulk bar confirms with the combined impact before deleting | Client（媒體庫選三部影音） | 批次刪除的確認框顯示「3 部影音」、釋放的空間與已花金額，並提供「同時刪除未修改過的輸出檔」；其中一部失敗時訊息列出失敗的影音與原因，其餘兩部進垃圾桶。 |
| [`LIB-library.feature`](../features/LIB-library.feature) | Filters survive a reload | Client | 篩選平台 YouTube 後重新整理，仍只顯示 YouTube 的影音，網址帶 `platform=youtube`。 |
| [`LIB-library.feature`](../features/LIB-library.feature) | Clicking a line seeks the player | Client、真 YouTube | 點 00:01:05 的字幕句，YouTube 播放器跳到該時間；在「原始」與「正規化」之間切換時保持捲動位置。 |
| [`LIB-library.feature`](../features/LIB-library.feature) | New items do not reshuffle the list | Client | 捲到列表中段後新增三部影音，出現「有 3 個新項目」的提示，可見的列不動。 |
| [`LIB-library.feature`](../features/LIB-library.feature) | An empty filter result offers to clear the filters | Client | 篩選一個沒有影音的頻道，顯示「沒有符合條件的影音。」與「清除篩選條件」按鈕。 |
| [`LIB-library.feature`](../features/LIB-library.feature) | The diff view highlights what the LLM changed | Client（有校正字幕的影音） | 開啟「差異」模式，每個被 LLM 改過的字都相對原文標出。 |
| [`NOTE-notes.feature`](../features/NOTE-notes.feature) | The note tab shows the checks and where the note was written | Client（已通過所有檢查並寫入 vault 的筆記） | 筆記分頁渲染 callout、wikilink 與內嵌畫面；點標題的時間碼播放器跳過去；側欄列出每項檢查皆通過，並顯示 vault 路徑與 git commit。 |
| [`NOTE-notes.feature`](../features/NOTE-notes.feature) | Editing a template shows a live preview on a real item | Client | 複製 `video-notes` 為 `my-notes` 並選一部影音預覽：用閱讀字型渲染，儲存前就以底線標出未知變數。 |
| [`NOTIFY-notifications.feature`](../features/NOTIFY-notifications.feature) | The owner sets up a channel and sends a test message from the settings | Client、真實的 Telegram bot 與聊天室 | 新增 Telegram 通道、勾選「失敗」與「需要你處理」並儲存，「傳送測試通知」顯示是否送達；bot token 只顯示末四碼。 |
| [`OPS-unattended.feature`](../features/OPS-unattended.feature) | Active alerts are shown on the overview | Client（磁碟用量告警成立） | 總覽列出告警的訊息與起始時間，旁邊列出被丟棄、沒有送出的通知。 |
| [`SRC-better-source.feature`](../features/SRC-better-source.feature) | The item page offers the upgrade with its estimated cost | Client（處理過、後來才出現 YouTube 人工字幕的影片） | 影音頁提醒有更好的字幕來源並說明是哪一種；選「改用更好的字幕重跑」會先顯示預估的 LLM 花費才執行。 |
| [`SRCH-search.feature`](../features/SRCH-search.feature) | Search results jump to the cue in the item page | Client | 搜尋框輸入「ordered log」並選「字幕內容」：每筆結果有標題、時間碼與標出命中的字幕句，標示依 `highlights` 的位移繪製、不渲染字幕中的 HTML；點結果後影音頁的播放器在 00:00:04，第 2 句捲入畫面。 |
| [`SUB-subscriptions.feature`](../features/SUB-subscriptions.feature) | The subscriptions page lists each subscription with its schedule | Client（兩個 YouTube 頻道與一個 Podcast 的訂閱） | 每個訂閱顯示平台、檢查間隔、上次與下次檢查、處理方案與新內容；上次檢查失敗的顯示錯誤訊息。 |
| [`SUB-subscriptions.feature`](../features/SUB-subscriptions.feature) | Editing the filters updates the dry run at once | Client、真 YouTube 頻道 | 抽屜中設定長度 5 到 90 分鐘並排除標題含「抽獎」：試跑立即列出會處理與被略過（附原因）的影片，儲存之前媒體庫不會新增任何東西。 |
| [`SUB-subscriptions.feature`](../features/SUB-subscriptions.feature) | Pasting a channel address offers to subscribe | Client | 在新增抽屜貼上頻道網址，抽屜提供「建立訂閱」而不是開始處理；建立後顯示「同一個頻道只在一台實例上訂閱，避免重複處理。」 |
| [`SUB-subscriptions.feature`](../features/SUB-subscriptions.feature) | Importing a Takeout file reports what was created | Client、Google Takeout 的 `subscriptions.csv`（40 個頻道，其中 2 個已訂閱） | 從訂閱頁匯入後回報建立 38 個、重複 2 個。 |
| [`VAULT-vault-output.feature`](../features/VAULT-vault-output.feature) | The output settings show the vaults and hooks from the config file | Client（設定檔定義 vault `main` 與 hook `notify`） | 設定 › 輸出列出 vault `main` 的 adapter 與根資料夾，且沒有修改路徑的方式；hook `notify` 可以選用但不能編輯。 |

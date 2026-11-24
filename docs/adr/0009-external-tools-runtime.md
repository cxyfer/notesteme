# ADR-0009：yt-dlp、ffmpeg、Deno 以子程序執行，yt-dlp 可在執行期更新

- 狀態：已採納
- 日期：2026-11-11
- 修訂：2026-11-12，新增 process group、限定 extractor、更新後自我檢查與自動回退

## 背景

- YouTube 經常改版。yt-dlp 從 `2025.11.12` 起，需要外部 JS runtime（建議 Deno）才能完整支援 YouTube。PO Token 的強制範圍也在擴大。
- 如果把 yt-dlp 寫死在 image 裡，每次 YouTube 改版都要重建 image。
- ffmpeg 要用來探測媒體、抽音訊、抽畫面、偵測靜音，以及預覽 ASS（需要 libass）。

## 決策

1. **一律以子程序執行**，不嵌入 Python：
   - 用 `exec.CommandContext` 處理逾時與取消。
   - 子程序放在獨立的 process group 中執行（Windows 用 Job Object），取消或關閉時整組一起結束，不留下孤兒程序。
   - stdout 和 stderr 限制大小。
   - 解析結構化的輸出：`yt-dlp -J`、`--print`、`--progress-template` 搭配 `--newline`、`ffprobe -of json`。
2. **安裝 yt-dlp**：
   - 在 `/data/tools/yt-dlp-venv` 建立 venv，用 pip 安裝 `yt-dlp[default]`。這樣會附帶 `yt-dlp-ejs`，也能安裝 PO Token provider 之類的 plugin。
   - 排程自動更新，UI 上也有「更新 yt-dlp」按鈕。
   - 更新後執行自我檢查：`--version`，並對一部已知的影片跑一次 `Probe`。失敗就自動回退到上一版並發出通知。
   - 每次執行時記錄使用的 yt-dlp 版本，寫進來源紀錄。
3. **Docker image** 以 Debian slim 為基礎，內含：
   - `python3`（只給 yt-dlp 用）。
   - `ffmpeg`（含 libass）。
   - Deno（官方發布的 `deno`，不是 `denort`）。
4. **工具探索**：
   - 順序：設定的路徑 → 環境變數 → `PATH`。
   - `notesteme doctor` 檢查版本與可用性；UI 的系統狀態頁也顯示同樣的資訊。
5. **錯誤分類**：把 yt-dlp 和 ffmpeg 的錯誤對應到 `retryable`、`wait`、`needs_action`、`permanent`（見 plan 第 5.2 節），並附上修正方式。例如 403 時先建議更新 yt-dlp。
6. **cookie**：
   - 在 UI 上傳，加密存放，執行時才寫成暫存檔交給 yt-dlp。
   - 記錄最後一次成功使用的時間；失敗時歸為 `needs_action`。
7. **選配 sidecar**：PO Token provider，透過 yt-dlp plugin 連到 sidecar。
8. **限定 extractor**：yt-dlp 的請求不經過 Notesteme 的對外 HTTP client，無法套用 SSRF 防護（ADR-0014）。所以呼叫時只開放 YouTube 與 Bilibili 的 extractor，關閉 generic extractor；Podcast 音檔由 Notesteme 自己下載。

## 考慮過的選項

- **呼叫 yt-dlp 的 Python API**：需要嵌入 Python，而且 yt-dlp 的 Python API 不保證穩定。
- **使用 yt-dlp 的獨立執行檔**：更新容易，但不方便安裝 plugin。
- **重寫下載器**：維護成本太高。

## 影響

- 好處：
  - YouTube 改版時，只要更新 yt-dlp，不需要發新版本。
  - 錯誤訊息具體、可行動。
- 代價：
  - image 要包含 Python 和 Deno，大約幾百 MB。
  - 執行期更新可能引入新的錯誤，所以 UI 要能固定或回退 yt-dlp 的版本，自我檢查失敗時也會自動回退。

## 修訂

- 2026-11-25（依 /goal 由實作者暫定，待 owner 追認；一次唯讀稽核：每份 ADR 的「決定」對照程式與規格）：
  - 決策 7「選配 sidecar：PO Token provider」：
    - 原本寫：選配的 PO Token provider sidecar，透過 yt-dlp plugin 連到 sidecar。
    - 實際是：**延後，VPS 被擋再做**。沒有實作，`deploy/docker/` 沒有這個 sidecar；`docs/specs/story-map.md` 第 450 行仍把「VPS 上有無 PO Token provider 的差異（影響 OPS-08 的預設 sidecar）」列為 M0 待驗證的問題。決策 2 的「`yt-dlp[default]` 也能安裝 PO Token provider 之類的 plugin」只是保留空間，沒有預設啟用。
  - 決策 1 的「解析結構化的輸出：`yt-dlp -J`、`--print`、`--progress-template` 搭配 `--newline`、`ffprobe -of json`」：
    - 原本寫：四種結構化輸出都解析。
    - 實際是：只有 `yt-dlp -J`（`internal/source/youtube/client.go:287`；播放清單與 Bilibili 另有 `internal/source/youtube/playlist.go:46`、`internal/source/bilibili/client.go:225、302`）與 `ffprobe -of json`（`internal/media/media.go:212`）。沒有用 `--print`、`--progress-template`、`--newline`，也**沒有解析下載進度**。
  - 「影響」的「UI 要能固定或回退 yt-dlp 的版本」只做了一部分：
    - 實際是：設定 › 系統有「更新 yt-dlp」、「回到上一個版本」並顯示目前固定的版本（`web/client/src/pages/settings/SystemSection.tsx`：`pinned`、`update`、`rollback`）；**固定（pin）與解除固定（unpin）只有 CLI 與 API**：`notesteme tools update --pin <版本>`／`--unpin`（`internal/cli/tools.go`）、`POST /api/tools/yt-dlp/update {"pin": "<版本>"}`／`{"unpin": true}`（`docs/specs/OPS-operations.md` OPS-R6、R7）。`docs/ui/pages.md` 設定 › 系統分區已同步註明。

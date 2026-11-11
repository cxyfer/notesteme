# ADR-0009：yt-dlp、ffmpeg、Deno 以子程序執行，yt-dlp 可在執行期更新

- 狀態：已採納
- 日期：2026-11-11

## 背景

- YouTube 經常改版。yt-dlp 從 `2025.11.12` 起，需要外部 JS runtime（建議 Deno）才能完整支援 YouTube。PO Token 的強制範圍也在擴大。
- 如果把 yt-dlp 寫死在 image 裡，每次 YouTube 改版都要重建 image。
- ffmpeg 要用來探測媒體、抽音訊、抽畫面、偵測靜音，以及預覽 ASS（需要 libass）。

## 決策

1. **一律以子程序執行**，不嵌入 Python：
   - 用 `exec.CommandContext` 處理逾時與取消。
   - stdout 和 stderr 限制大小。
   - 解析結構化的輸出：`yt-dlp -J`、`--print`、`--progress-template` 搭配 `--newline`、`ffprobe -of json`。
2. **安裝 yt-dlp**：
   - 在 `/data/tools/yt-dlp-venv` 建立 venv，用 pip 安裝 `yt-dlp[default]`。這樣會附帶 `yt-dlp-ejs`，也能安裝 PO Token provider 之類的 plugin。
   - 排程自動更新，UI 上也有「更新 yt-dlp」按鈕。
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
  - 執行期更新可能引入新的錯誤，所以 UI 要能固定或回退 yt-dlp 的版本。

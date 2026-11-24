// Package benchdata 產生非功能需求（docs/specs/nfr.md）量測用的資料目錄：
// lib-10k（10,000 部影音、500 個頻道、每部約 600 句、每部 3 個字幕版本）與 item-long（一部 3 小時、約 3,000 句）。
//
// 產生的目錄就是一個正常的 notesteme 資料目錄（SQLite、artifacts/ 底下的字幕產出檔、全文搜尋索引），
// `notesteme serve` 與 CLI 獨立模式可以直接使用。寫入一律經過 store 的公開方法：字幕產出先照 pipeline 的寫法
// 寫成檔案（0700 目錄、0600 檔、sha256），再由 Repo.InsertArtifact 在同一個交易中建立全文搜尋索引（SRCH-R2），
// 所以索引與產品實際寫入的結果相同，不是另外灌進去的。
//
// 使用者：
//   - internal/nfrbench 的 bench（NFR-PERF、NFR-RES、NFR-REL-5、NFR-CMP-5）；
//   - internal/pipeline 的 BenchmarkListItems10k；
//   - 之後的故障注入與並行寫入測試（NFR-REL-1～4、7）可以用 Generate 建立資料、用 Cues 產生長字幕。
//   - 獨立入口：go run ./internal/benchdata/cmd/genlib -out <dir> -profile lib-10k|item-long [-scale 0.05]
//
// 這個套件只給測試、bench 與 genlib 使用，正式的 binary（./cmd/...）不得 import 它；
// TestNotImportedByBinaries 以 go list -deps 守住。
//
// 決定論：同一個 Profile 與 Seed 產生的標題、字幕文字與時間碼完全相同（ID 由 store 產生，每次不同），
// 讓每晚的搜尋命中數一致。詞彙中有固定比例的常見詞、罕見詞與 2 字中文詞，見 Vocab。
package benchdata

// Package nfrbench 放 docs/specs/nfr.md 中驗證方式為 `bench` 的量測（.github/workflows/bench.yml 每晚執行）。
//
// 每個 Benchmark 對應一或多個 NFR ID，門檻就是 nfr.md 的數字：自己取樣固定次數，以 b.ReportMetric 回報
// p50／p95／max，超過門檻時 b.Fatalf。執行方式：
//
//	go test ./internal/nfrbench -run '^$' -bench 'Perf1|Perf3' -benchtime=1x -count=1 -timeout 60m
//
// 環境變數：
//   - NOTESTEME_BENCH_LIB：已產生的 lib-10k 資料目錄（genlib -profile lib-10k）；沒設定時在暫存目錄產生
//     NOTESTEME_BENCH_SCALE（預設 0.01）比例的縮小版，只能確認 bench 本身可以執行，數字不代表 lib-10k。
//   - NOTESTEME_BENCH_BIN：已建置的 notesteme binary；沒設定時以 go build 建一份。
//   - NOTESTEME_BENCH_REPORT：每項結果寫一行 JSON 到這個檔案，bench.yml 用它產生 job summary。
//
// 這個套件只有測試檔與這份說明，不被任何正式的程式 import。
package nfrbench

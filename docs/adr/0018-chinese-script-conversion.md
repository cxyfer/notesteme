# ADR-0018：中文字形轉換：自寫 OpenCC 轉換器與內嵌字典

- 狀態：已採納
- 日期：2026-11-22

## 背景

TRN-R8 要求原文與目標都是中文時，用 OpenCC 直接轉換字形，不呼叫 LLM；TRN-R9 的 localize 也要先用 OpenCC 轉換。需要一個在 Go 中可用的 OpenCC 實作：

- 現有的 Go port（`longbridgeapp/opencc`、`cupogo/opencc`）都依賴 `liuzl/cedar-go`，它的授權是 GPLv2，沒有雙授權；Docker image 會發布到 GHCR，屬於散布。
- longbridge 版在 `init` 時註冊全域 flag 並讀取 `GOPATH` 環境變數，違反「只有 `main` 讀環境變數」的規則；兩個 port 都沒有實作上游的 segmentation（最大正向匹配分段）。
- 呼叫 `opencc` CLI 不是純 Go，每個 cue 一個程序或要做批次協定，驗收測試還要多一個假工具（ADR-0009）。

## 決策

1. **自寫 `internal/zhconv`，不加任何依賴。** 依上游的設定 json 實作：normalization、mmseg 分段，以及 `conversion_chain` 與字典群組（`short_circuit`、`union`）。轉換鏈從設定檔讀入，不寫死在程式中；字典每行取第一個候選。
2. **內嵌 OpenCC 官方字典**：`internal/zhconv/opencc/` 是 `ver.1.4.2` release 的 10 份設定與 17 份字典，原封不動，附 `LICENSE`（Apache-2.0）、`NOTICE.md`、`VERSION` 與 `manifest.json`（sha256，`TestEmbeddedChecksums` 比對）。更新時由維護者手動執行 `scripts/opencc/update.sh <tag>`；CI 與測試都不連網。
3. **上游的 `testcases.json` 是 golden 測試**（`TestUpstreamTestcases`），涵蓋這 10 個表的全部案例。
4. `zhconv.DataVersion` 放進 translate 的快取鍵與來源紀錄（`opencc_data`），字典更新後舊快取不再命中。
5. 轉換表的選擇（`TableFor`）、詞彙表的轉換（`GlossaryTable`）與字形判斷（`DetectScript`）也放在 `zhconv`；語言標記的解析放在共用的 `internal/langtag`。

## 考慮過的選項

- **依賴 `longbridgeapp/opencc`**：GPLv2、init 副作用、module 路徑與 go.mod 宣告不符、不做 segmentation。
- **依賴 `cupogo/opencc`**：同樣依賴 GPLv2 的 `cedar-go`；剛發布的小型 fork，維護者不確定；不做 segmentation 與 normalization。
- **呼叫 `opencc` CLI**：見背景。
- **字典壓縮後內嵌**：約 1.2 MiB 的未壓縮文字，換來更新時 diff 看得懂；binary 的大小影響可以接受。

## 影響

- 好處：授權乾淨（Apache-2.0）、沒有 init 副作用、可以並行呼叫、行為由上游測試把關。
- 代價：
  - 要自己維持與上游演算法一致；分段邊界上的差異靠 `testcases.json` 把關。
  - 字典在第一次使用時解析（`STPhrases` 約 4.9 萬筆，每份字典只解析一次），常駐記憶體數 MB。
  - Docker image 與發布物要附上 LICENSE 與 NOTICE。

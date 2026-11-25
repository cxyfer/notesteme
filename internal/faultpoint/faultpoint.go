// Package faultpoint 是 NFR-REL-1～3 的故障注入點。
//
// production 程式在關鍵位置呼叫 Hit(點, 範圍)。一般建置（沒有 faultinject tag）的 Hit 是空函式，
// 編譯期被內聯掉，正式 binary 不含任何故障注入的程式碼（faultpoint_test.go 以 go list 驗證）。
//
// 以 `-tags faultinject` 建置時（只支援 unix），環境變數決定行為：
//   - NOTESTEME_FAULT=<名稱>:<第幾次命中>：命中指定的點第 n 次時，對自己送 SIGKILL（等同 kill -9）。
//     名稱是「點」或「點/範圍」，例如 stage.commit/transcript。
//   - NOTESTEME_FAULT_TRACE=<檔案>：每次命中都附加一行名稱，被殺之前另外附加一行 "KILL <名稱> <n>"，
//     讓故障注入的 harness 知道實際命中的點與次數。
//
// 故障注入的 harness 在 features/fault（同樣要 faultinject tag），規格見 docs/specs/nfr.md 第 11 節。
package faultpoint

// 故障點（名稱中的「點」）。範圍（scope）由呼叫端給：步驟名稱、字幕來源 ID，或 llm.<步驟>。
const (
	// StageStart 是任務開始執行（讀到步驟之後、做任何事之前）。範圍是步驟名稱。
	StageStart = "stage.start"
	// StageMid 是步驟的工作做完（外部呼叫已回來、產出檔已寫出），結果還沒寫回 DB。範圍是步驟名稱。
	StageMid = "stage.mid"
	// StageCommit 是寫回結果的交易做完所有寫入、還沒 commit。範圍是步驟名稱。
	StageCommit = "stage.commit"
	// ArtifactTmpWritten 是產出的暫存檔寫完並 fsync、還沒 rename（NFR-REL-2）。範圍是空的。
	ArtifactTmpWritten = "artifact.tmp-written"
	// ArtifactRenamed 是產出檔 rename 到正式檔名並 fsync 目錄之後、DB 記錄之前（NFR-REL-2）。範圍是空的。
	ArtifactRenamed = "artifact.renamed"
	// PaidReturned 是付費呼叫的結果回來之後、結果落地之前（NFR-REL-3）。範圍是來源 ID 或 llm.<步驟>。
	PaidReturned = "paid.returned"
	// PaidLanded 是付費呼叫的結果已經落地（交易已 commit）、步驟還沒標記完成（NFR-REL-3）。範圍是 llm.<步驟>。
	PaidLanded = "paid.landed"
)

// Name 組出「點/範圍」的名稱（NOTESTEME_FAULT 與 trace 檔使用的寫法）。
func Name(point, scope string) string {
	if scope == "" {
		return point
	}
	return point + "/" + scope
}

// Package fault 是 NFR-REL-1～3 的故障注入測試（docs/specs/nfr.md 第 11 節）。
//
// 整個套件只在 `-tags faultinject` 建置：父程序（TestFaultInjection）重新執行同一個測試 binary 當子程序
// （TestFaultChild），子程序以 godog 跑一份固定劇本（features 的步驟與 fake provider，不連真實網路，NFR-TEST-1），
// 在 NOTESTEME_FAULT 指定的故障點被 SIGKILL；父程序再起一個子程序在同一份 data dir 上重新啟動，然後斷言
// DB 沒有損毀、pipeline 依 PIPE-R11 做完且結果與沒中斷時相同、產出檔的 sha256 相符、付費呼叫沒有重複付費。
//
//	go test -tags faultinject ./features/fault -run TestFaultInjection -count=1                       # 每點 3 次（預設；CI 的 go job 設 1 次）
//	NOTESTEME_FAULT_ITERATIONS=100 go test -tags faultinject ./features/fault -run TestFaultInjection -timeout 0  # 每晚（fault.yml）
//
// 一般建置只有這個檔案（沒有測試），go test ./features/... 不會因為整個套件被 build tag 排除而失敗。
package fault

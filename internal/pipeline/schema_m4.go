package pipeline

import (
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/cxyfer/notesteme/internal/auth"
)

// M4 的處理方案欄位（M4-S0）：stages.summary（DIG 的「介面」）、stages.<stage>.batch（BATCH-R1）與
// language.better_source（SRC-R22）。S0 只做型別、範圍與 DIG-R1 的組合檢查，語意由各自的切片使用。

// language.better_source 的值（SRC-R22）；沒有設定時是 notify（讀取時補上，不寫進快照）。
const (
	BetterSourceNotify = "notify"
	BetterSourceRerun  = "rerun"
	BetterSourceOff    = "off"
)

// stages.<stage>.batch 的值（BATCH-R1）；預設 off。
const (
	BatchAuto = "auto"
	BatchOff  = "off"
)

// batchStages 是可以設定 batch 的步驟（BATCH-R1）。
var batchStages = []string{StageSummary, StageSegment, StageCorrect, StageTranslate, StageChapters, StageNote}

// stages.summary 的範圍（DIG-R2）。
const (
	summaryMinDuration    = 10 * time.Minute
	summaryMaxDuration    = 6 * time.Hour
	summaryMinInputTokens = 1000
	summaryMaxInputTokens = 200000
)

// 驗證訊息（BATCH-R1、DIG-R1；場景依賴這些句子）。
const (
	msgBatchValue       = "batch 只能是 auto 或 off。"
	msgBatchUnsupported = "這個步驟不支援 batch。"
	msgSummaryPauseOff  = "摘要關閉時不能設定摘要後暫停。"
	// msgBatchNotExecutor 是 executor 寫成 batch 時的訊息（%s 是步驟名稱）。
	msgBatchNotExecutor = "batch 不是 executor；請改用 stages.%s.batch: auto。"
)

// stageSpecs 讓 stageFields 的每一項可以寫成 fieldSpec{…} 的清單。
func stageSpecs(specs ...fieldSpec) []fieldSpec { return specs }

func checkBatch(v *layerValidator, path string, val any) {
	switch t := val.(type) {
	case nil:
		v.add(path, msgNull)
	case string:
		if t != BatchAuto && t != BatchOff {
			v.add(path, msgBatchValue)
		}
	default:
		v.add(path, msgBatchValue)
	}
}

// rejectBatch 在不支援 batch 的步驟上回報 batch，回傳去掉 batch 的物件（其餘欄位照原本的規則檢查）。
func (v *layerValidator) rejectBatch(path string, m map[string]any) map[string]any {
	if _, has := m["batch"]; !has {
		return m
	}
	v.add(joinPath(path, "batch"), msgBatchUnsupported)
	rest := maps.Clone(m)
	delete(rest, "batch")
	return rest
}

// checkSummaryPause 是 DIG-R1：summary 關閉時不能設定 pause_after: true。
// 處理方案沒有寫 executor 時用預設（off）；覆寫沒有寫 executor 時不知道下層的設定，不檢查。
func (v *layerValidator) checkSummaryPause(path string, m map[string]any) {
	if m["pause_after"] != true {
		return
	}
	exec, has := m["executor"]
	if has && exec == ExecutorOff || !has && v.kind == KindProfile {
		v.add(joinPath(path, "pause_after"), msgSummaryPauseOff)
	}
}

// checkDurationRange 檢查期間字串（同 checkDuration 的寫法）介於 [lo, hi]。
func checkDurationRange(lo, hi time.Duration) func(*layerValidator, string, any) {
	msg := fmt.Sprintf("必須是 %s 到 %s 之間的期間。", shortDuration(lo), shortDuration(hi))
	return func(v *layerValidator, path string, val any) {
		s, ok := val.(string)
		if !ok {
			checkDuration(v, path, val)
			return
		}
		d, err := auth.ParseTokenDuration(s)
		switch {
		case err != nil:
			v.add(path, msgNotDuration)
		case d < lo || d > hi:
			v.add(path, msg)
		}
	}
}

// shortDuration 把 10m0s、6h0m0s 寫成 10m、6h。
func shortDuration(d time.Duration) string {
	switch {
	case d%time.Hour == 0:
		return fmt.Sprintf("%dh", d/time.Hour)
	case d%time.Minute == 0:
		return fmt.Sprintf("%dm", d/time.Minute)
	}
	return d.String()
}

// validBetterSource 回報 language.better_source 的值是否合法（空字串代表沒有設定）。
func validBetterSource(s string) bool {
	return s == "" || slices.Contains([]string{BetterSourceNotify, BetterSourceRerun, BetterSourceOff}, s)
}

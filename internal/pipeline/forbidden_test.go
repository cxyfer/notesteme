package pipeline

import (
	"slices"
	"testing"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/store"
)

// 工具回報的錯誤帶有給使用者看的訊息與動作時，步驟就用它們（SRC-R9）；哪個步驟遇到就由哪個步驟 blocked。
func TestNeedsActionUsesTheErrorMessageAndActions(t *testing.T) {
	const msg = "YouTube 拒絕了下載請求（403）。先更新 yt-dlp 再重試；如果仍然失敗，上傳 YouTube cookie。"
	forbidden := &errclass.Error{
		Class: errclass.ClassNeedsAction, Source: "yt-dlp", ExitCode: 1, Detail: "HTTP Error 403",
		Message: msg, Actions: []string{"更新 yt-dlp", "上傳 cookie"},
	}
	// metadata 的 Probe 失敗。
	r := newRig(t)
	r.yt.probeErr = forbidden
	it := r.ingest(watch, nil, "")
	r.run()
	checkForbidden(t, "metadata", r.stage(it, StageMetadata), msg)
	if r.yt.probes != 1 {
		t.Errorf("needs_action 不應重試：Probe %d 次", r.yt.probes)
	}

	// metadata 成功，transcript 的 Probe 失敗：由 transcript blocked。
	r = newRig(t)
	r.yt.onProbe = func() {
		if r.yt.probes == 2 {
			r.yt.probeErr = forbidden
		}
	}
	it = r.ingest(watch, nil, "")
	r.run()
	if st := r.stage(it, StageMetadata); st.Status != store.StatusDone {
		t.Errorf("metadata = %+v", st)
	}
	checkForbidden(t, "transcript", r.stage(it, StageTranscript), msg)
	if st := r.stage(it, StageNormalize); st.Status != store.StatusPending {
		t.Errorf("下游不該執行：%+v", st)
	}

	// 沒有具體訊息的 needs_action 維持通用訊息，不帶動作。
	r = newRig(t)
	r.yt.probeErr = &errclass.Error{Class: errclass.ClassNeedsAction, Source: "yt-dlp"}
	it = r.ingest(watch, nil, "")
	r.run()
	if st := r.stage(it, StageMetadata); st.Message != "這個步驟需要你處理才能繼續。" || len(st.Actions) != 0 {
		t.Errorf("%+v", st)
	}
}

func checkForbidden(t *testing.T, name string, st store.StageRun, msg string) {
	t.Helper()
	if st.Status != store.StatusBlocked || st.BlockReason != "needs_action" || st.ErrorClass != "needs_action" ||
		st.Message != msg || !slices.Equal(st.Actions, []string{"更新 yt-dlp", "上傳 cookie"}) {
		t.Errorf("%s：%+v", name, st)
	}
}

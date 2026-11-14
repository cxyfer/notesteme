package pipeline

import (
	"testing"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/store"
)

// 工具的錯誤帶了 ResumeOn（例如缺 cookie）時，engine 把它寫進 blocked 步驟，供補上 cookie 後恢復。
func TestBlockedStageRecordsResumeOn(t *testing.T) {
	r := newRig(t)
	r.yt.probeErr = &errclass.Error{
		Class: errclass.ClassNeedsAction, Source: "yt-dlp", Message: "需要 cookie。", Actions: []string{"上傳 cookie"},
		ResumeOn: "cookie:youtube",
	}
	it := r.ingest(watch, nil, "")
	r.run()
	st := r.stage(it, StageMetadata)
	if st.Status != store.StatusBlocked || st.ResumeOn != "cookie:youtube" {
		t.Fatalf("metadata = %+v，預期 blocked 且 ResumeOn=cookie:youtube", st)
	}
	blocked, err := r.st.Read().BlockedRunsByResume(t.Context(), "cookie:youtube")
	if err != nil || len(blocked) != 1 || blocked[0].ID != st.ID {
		t.Errorf("BlockedRunsByResume = %+v, %v", blocked, err)
	}
}

// 沒有 ResumeOn 的 blocked 維持空字串；非 blocked 的狀態（例如重試中）即使錯誤帶了 ResumeOn 也不記錄。
func TestResumeOnOnlyRecordedWhenBlocked(t *testing.T) {
	r := newRig(t)
	r.yt.probeErr = &errclass.Error{Class: errclass.ClassNeedsAction, Source: "yt-dlp"}
	it := r.ingest(watch, nil, "")
	r.run()
	if st := r.stage(it, StageMetadata); st.Status != store.StatusBlocked || st.ResumeOn != "" {
		t.Errorf("metadata = %+v，預期 blocked 且 ResumeOn 為空", st)
	}

	r = newRig(t)
	r.yt.probeErr = &errclass.Error{Class: errclass.ClassRetryable, Source: "yt-dlp", ResumeOn: "cookie:youtube"}
	it = r.ingest(watch, nil, "")
	r.run()
	if st := r.stage(it, StageMetadata); st.Status == store.StatusBlocked || st.ResumeOn != "" {
		t.Errorf("retryable：metadata = %+v，ResumeOn 應為空", st)
	}
}

package pipeline

import (
	"context"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/store"
)

// putCookie 直接寫入一筆 cookie secret（模擬 POST /api/cookies 儲存成功），updated_at 為 fake clock 的現在。
func (r *rig) putCookie(platform string) {
	r.t.Helper()
	err := r.st.Tx(context.Background(), func(tx *store.Repo) error {
		return tx.UpsertSecret(context.Background(), "cookie", platform, []byte("n"), []byte("c"))
	})
	if err != nil {
		r.t.Fatal(err)
	}
}

func TestResumeSecret(t *testing.T) {
	for token, want := range map[string][2]string{
		"cookie:youtube":  {"cookie", "youtube"},
		"cookie:bilibili": {"cookie", "bilibili"},
	} {
		kind, name, ok := resumeSecret(token)
		if !ok || kind != want[0] || name != want[1] {
			t.Errorf("resumeSecret(%q) = %q, %q, %v", token, kind, name, ok)
		}
	}
	for _, token := range []string{"", "cookie:", ":youtube", "cookie", "other:x"} {
		if _, _, ok := resumeSecret(token); ok {
			t.Errorf("resumeSecret(%q) 不該成功", token)
		}
	}
}

// 步驟開始執行後才上傳 cookie，步驟再回報需要 cookie：這時 ResumeBlocked 找不到 blocked 的步驟，
// 所以 apply 要改為 requeue（pending、新任務、不計次數），不能寫成永遠卡住的 blocked。
func TestCookieUploadedWhileRunningRequeues(t *testing.T) {
	r := newRig(t)
	it := r.ingest(watch, nil, "")
	n := 0
	// onProbe 在 fakeYT 的鎖內呼叫，直接設定 probeErr 即可。
	r.yt.onProbe = func() {
		n++
		if n == 1 {
			r.clk.Advance(time.Second)
			r.putCookie("youtube")
			r.clk.Advance(time.Second)
			r.yt.probeErr = needsCookie()
			return
		}
		r.yt.probeErr = nil // 第二次執行時 cookie 已經存在，成功
	}
	r.run()
	if st := r.stage(it, StageMetadata); st.Status != store.StatusDone {
		t.Fatalf("metadata = %+v，預期 requeue 後重跑完成", st)
	}
	// metadata 重跑一次（2 次 probe）；下游 transcript 也會 probe 一次。
	if n != 3 {
		t.Errorf("probe 次數 = %d，預期 3", n)
	}
}

// requeue 只重跑一次、不計入嘗試次數；再次回報需要 cookie 時（cookie 早於這次開始）才真正 blocked。
func TestCookieRaceRequeueThenBlocked(t *testing.T) {
	r := newRig(t)
	it := r.ingest(watch, nil, "")
	n := 0
	r.yt.onProbe = func() {
		n++
		r.clk.Advance(time.Second)
		if n == 1 {
			r.putCookie("youtube")
			r.clk.Advance(time.Second)
		}
		r.yt.probeErr = needsCookie()
	}
	r.run()
	st := r.stage(it, StageMetadata)
	if st.Status != store.StatusBlocked || st.ResumeOn != "cookie:youtube" {
		t.Fatalf("metadata = %+v，預期第二次才 blocked", st)
	}
	if r.yt.probes != 2 {
		t.Errorf("probe 次數 = %d，預期 2", r.yt.probes)
	}
	tk, err := r.st.Read().TaskByStageRun(context.Background(), st.ID)
	if err != nil || tk.Attempts > 1 {
		t.Errorf("任務 = %+v, %v，requeue 不該計入嘗試次數", tk, err)
	}
}

// cookie 在步驟開始之前就存在（例如 cookie 過期、被平台拒絕）：維持 blocked，不能無限重跑。
func TestCookieOlderThanStartStaysBlocked(t *testing.T) {
	r := newRig(t)
	r.putCookie("youtube")
	r.clk.Advance(time.Minute)
	r.yt.probeErr = needsCookie()
	it := r.ingest(watch, nil, "")
	r.run()
	if st := r.stage(it, StageMetadata); st.Status != store.StatusBlocked || st.ResumeOn != "cookie:youtube" {
		t.Fatalf("metadata = %+v，預期 blocked", st)
	}
	if r.yt.probes != 1 {
		t.Errorf("probe 次數 = %d，預期 1", r.yt.probes)
	}
}

// 開始的同一刻已存在的 cookie 不算晚於開始，否則 fake clock 不前進時會無限重跑。
func TestCookieSameInstantAsStartStaysBlocked(t *testing.T) {
	r := newRig(t)
	r.putCookie("youtube")
	r.yt.probeErr = needsCookie()
	it := r.ingest(watch, nil, "")
	r.run()
	if st := r.stage(it, StageMetadata); st.Status != store.StatusBlocked {
		t.Fatalf("metadata = %+v，預期 blocked", st)
	}
	if r.yt.probes != 1 {
		t.Errorf("probe 次數 = %d，預期 1", r.yt.probes)
	}
}

// 別的平台的 cookie 不影響判斷。
func TestOtherPlatformCookieDoesNotRequeue(t *testing.T) {
	r := newRig(t)
	it := r.ingest(watch, nil, "")
	r.yt.onProbe = func() {
		r.clk.Advance(time.Second)
		r.putCookie("bilibili")
		r.clk.Advance(time.Second)
		r.yt.probeErr = needsCookie()
	}
	r.run()
	if st := r.stage(it, StageMetadata); st.Status != store.StatusBlocked {
		t.Fatalf("metadata = %+v，預期 blocked", st)
	}
	if r.yt.probes != 1 {
		t.Errorf("probe 次數 = %d，預期 1", r.yt.probes)
	}
}

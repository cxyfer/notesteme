package pipeline

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/sponsorblock"
	"github.com/cxyfer/notesteme/internal/store"
)

const testSponsorBase = "https://sb.test"

// fakeSponsor 是測試用的 SponsorBlock：預設沒有片段（not_found），同 harness 的預設（SPB-B5）。
type fakeSponsor struct {
	mu    sync.Mutex
	res   sponsorblock.Result
	err   error
	calls int
	cats  []string
}

func (f *fakeSponsor) Lookup(_ context.Context, _ string, cats []string, _ int64) (sponsorblock.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.cats = cats
	if f.err != nil {
		return sponsorblock.Result{}, f.err
	}
	if f.res.Outcome == "" {
		return sponsorblock.Result{Outcome: sponsorblock.OutcomeNotFound, Segments: []sponsorblock.Segment{}}, nil
	}
	return f.res, nil
}

func sponsorRig(t *testing.T, fs *fakeSponsor) *rig {
	t.Helper()
	r := newRig(t, func(d *Deps) { d.SponsorBlock = fs })
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	return r
}

func TestSponsorBlockStoresSegmentsAndProvenance(t *testing.T) {
	fs := &fakeSponsor{res: sponsorblock.Result{Outcome: sponsorblock.OutcomeOK, Segments: []sponsorblock.Segment{
		{Category: "sponsor", StartMS: 60000, EndMS: 90000, UUID: "u1"},
	}}}
	r := sponsorRig(t, fs)
	it := r.ingest(watch, nil, "")
	r.run()
	st := latestStage(t, r, it.ID, StageSponsorBlock)
	if st.Status != store.StatusDone || st.Message != "" || st.ArtifactID == "" {
		t.Fatalf("sponsorblock = %+v", st)
	}
	if fs.calls != 1 || len(fs.cats) != 3 {
		t.Fatalf("calls=%d cats=%v", fs.calls, fs.cats)
	}
	segs, ok, err := r.p.SponsorSegments(context.Background(), it.ID)
	if err != nil || !ok || len(segs) != 1 || segs[0].StartMS != 60000 {
		t.Fatalf("segs=%+v ok=%v err=%v", segs, ok, err)
	}
	arts, _ := r.st.Read().CurrentArtifacts(context.Background(), it.ID, KindSponsorSegments)
	var prov struct {
		Stage        string            `json:"stage"`
		SponsorBlock map[string]string `json:"sponsorblock"`
	}
	if err := json.Unmarshal(arts[0].Provenance, &prov); err != nil {
		t.Fatal(err)
	}
	if prov.Stage != StageSponsorBlock || prov.SponsorBlock["outcome"] != "ok" || prov.SponsorBlock["base_url"] != testSponsorBase ||
		prov.SponsorBlock["categories"] != "sponsor,selfpromo,interaction" || prov.SponsorBlock["fetched_at"] == "" {
		t.Fatalf("provenance = %+v", prov)
	}
	// 下游照常前進到 publish。
	if pub := latestStage(t, r, it.ID, StagePublish); pub.Status != store.StatusSkipped {
		t.Errorf("publish = %+v", pub)
	}
}

func TestSponsorBlockFailureIsDoneWithReminder(t *testing.T) {
	fs := &fakeSponsor{res: sponsorblock.Result{Outcome: sponsorblock.OutcomeError, Reason: "HTTP 503", Segments: []sponsorblock.Segment{}}}
	r := sponsorRig(t, fs)
	it := r.ingest(watch, nil, "")
	r.run()
	st := latestStage(t, r, it.ID, StageSponsorBlock)
	if st.Status != store.StatusDone || st.Message != "無法取得 SponsorBlock 片段（HTTP 503），這次當作沒有片段。" ||
		len(st.Actions) != 1 || st.Actions[0] != actionRequerySponsor {
		t.Fatalf("sponsorblock = %+v", st)
	}
	if fs.calls != 1 {
		t.Errorf("失敗不重試：calls = %d", fs.calls)
	}
	segs, ok, _ := r.p.SponsorSegments(context.Background(), it.ID)
	if !ok || segs == nil || len(segs) != 0 {
		t.Errorf("失敗時存空的產出：%+v %v", segs, ok)
	}
}

func TestSponsorBlockRerunKeepsIdenticalResult(t *testing.T) {
	fs := &fakeSponsor{}
	r := sponsorRig(t, fs)
	it := r.ingest(watch, nil, "")
	r.run()
	rerun := func() {
		t.Helper()
		if _, err := r.p.Rerun(context.Background(), it.ID, RerunRequest{From: StageSponsorBlock}); err != nil {
			t.Fatal(err)
		}
		r.run()
	}
	rerun()
	n, err := r.st.Read().ArtifactVersions(context.Background(), it.ID, KindSponsorSegments, KindSponsorSegments, "")
	if err != nil || n != 1 || fs.calls != 2 {
		t.Fatalf("相同結果：versions=%d calls=%d err=%v", n, fs.calls, err)
	}
	// outcome 不同（內容同樣是空的）也算不同的結果。
	fs.res = sponsorblock.Result{Outcome: sponsorblock.OutcomeError, Reason: "連線失敗", Segments: []sponsorblock.Segment{}}
	rerun()
	if n, _ := r.st.Read().ArtifactVersions(context.Background(), it.ID, KindSponsorSegments, KindSponsorSegments, ""); n != 2 {
		t.Fatalf("outcome 改變：versions=%d", n)
	}
	// 片段改變：再一個新版本。
	fs.res = sponsorblock.Result{Outcome: sponsorblock.OutcomeOK, Segments: []sponsorblock.Segment{{Category: "sponsor", StartMS: 0, EndMS: 5000}}}
	rerun()
	if n, _ := r.st.Read().ArtifactVersions(context.Background(), it.ID, KindSponsorSegments, KindSponsorSegments, ""); n != 3 {
		t.Fatalf("片段改變：versions=%d", n)
	}
	if st := latestStage(t, r, it.ID, StageSponsorBlock); st.Message != "" {
		t.Errorf("成功的重跑要清掉提醒：%+v", st)
	}
}

func TestSponsorBlockShutdownDiscardsResult(t *testing.T) {
	fs := &fakeSponsor{err: context.Canceled}
	r := sponsorRig(t, fs)
	it := r.ingest(watch, nil, "")
	r.run()
	if st := latestStage(t, r, it.ID, StageSponsorBlock); st.Status == store.StatusDone {
		t.Fatalf("ctx 取消不能寫成 done：%+v", st)
	}
	if _, ok, _ := r.p.SponsorSegments(context.Background(), it.ID); ok {
		t.Error("ctx 取消不能留下產出")
	}
}

func TestRunSponsorBlockRejectsNonYouTube(t *testing.T) {
	r := sponsorRig(t, &fakeSponsor{})
	_, err := r.p.runSponsorBlock(context.Background(), store.Item{Platform: "bilibili", ExternalID: "BV1sp4y1c7mD"}, mustEffective(t, nil))
	if err == nil {
		t.Fatal("不是 YouTube 應該失敗")
	}
}

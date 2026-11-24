package pipeline

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

// NFR-REL-2 的故障注入：改壞產出檔（大小不變，只看大小抓不到）之後，讀取或重用時標記損毀並重跑產生它的步驟。

// flipByte 把產出檔中間的一個位元組改掉，檔案大小不變。
func flipByte(t *testing.T, r *rig, a store.Artifact) {
	t.Helper()
	p := filepath.Join(r.dir, filepath.FromSlash(a.Path))
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	i := len(b) / 2
	b[i] ^= 0x01
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func currentArtifact(t *testing.T, r *rig, itemID string, variant subtitle.Variant) store.Artifact {
	t.Helper()
	arts, err := r.st.Read().CurrentArtifacts(context.Background(), itemID, "transcript")
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range arts {
		if a.Variant == string(variant) {
			return a
		}
	}
	t.Fatalf("沒有現行的 %s", variant)
	return store.Artifact{}
}

func artifactByID(t *testing.T, r *rig, id string) store.Artifact {
	t.Helper()
	a, err := r.st.Read().Artifact(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func (r *rig) fetchCount() int {
	r.yt.mu.Lock()
	defer r.yt.mu.Unlock()
	return len(r.yt.fetched)
}

// API 讀到損毀的字幕（GET /cues）：標記損毀，從產生它的步驟（transcript）重新處理；
// 並行或之後再讀到同一份壞檔不會再觸發重跑。重跑完成後可以正常讀取。
func TestCorruptTranscriptOnReadReruns(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	it := r.ingest(watch, nil, StageTranscript)
	r.run()
	src := currentArtifact(t, r, it.ID, subtitle.VariantSource)
	fetched := r.fetchCount()
	flipByte(t, r, src)

	_, err := r.p.Cues(ctx, it.ID, CuesQuery{ToMS: -1})
	var ce *CorruptArtifactError
	if !errors.As(err, &ce) || !errors.Is(err, ErrCorruptArtifact) || !ce.Rerun || ce.Stage != StageTranscript || ce.ArtifactID != src.ID {
		t.Fatalf("讀到損毀的字幕 = %v", err)
	}
	if a := artifactByID(t, r, src.ID); a.CorruptAt == nil {
		t.Error("損毀的產出應標記 corrupt_at")
	}
	if _, err := r.p.Cues(ctx, it.ID, CuesQuery{ToMS: -1}); !errors.As(err, &ce) || ce.Rerun {
		t.Errorf("同一份壞檔第二次讀到不再重跑：%v", err)
	}
	r.run()
	if n := r.fetchCount(); n != fetched+1 {
		t.Errorf("transcript 重新下載字幕 %d 次，預期 1 次", n-fetched)
	}
	st := latestStage(t, r, it.ID, StageTranscript)
	if st.Status != store.StatusDone || st.ArtifactID == src.ID {
		t.Fatalf("重跑後 transcript = %+v", st)
	}
	if it2, _ := r.st.Read().ItemByID(ctx, it.ID); it2.UntilStage != StageTranscript {
		t.Errorf("重跑沿用影音的 until：%q", it2.UntilStage)
	}
	if tr, err := r.p.Cues(ctx, it.ID, CuesQuery{ToMS: -1}); err != nil || len(tr.Cues) != 4 {
		t.Errorf("重跑後讀取 = %d 句, %v", len(tr.Cues), err)
	}
}

// 下游步驟讀到損毀的輸入（normalize 讀 source）：重跑產生它的 transcript，normalize 跟著重跑並完成；
// 原本那次 normalize 已被取代，它的結果不寫回。
func TestCorruptInputRerunsProducer(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	it := r.ingest(watch, nil, StageTranscript)
	r.run()
	src := currentArtifact(t, r, it.ID, subtitle.VariantSource)
	flipByte(t, r, src)
	fetched := r.fetchCount()

	if _, err := r.p.Rerun(ctx, it.ID, RerunRequest{From: StageNormalize, Until: StageNormalize}); err != nil {
		t.Fatal(err)
	}
	r.run()
	if n := r.fetchCount(); n != fetched+1 {
		t.Errorf("transcript 重新下載字幕 %d 次，預期 1 次", n-fetched)
	}
	if a := artifactByID(t, r, src.ID); a.CorruptAt == nil || a.SupersededBy == "" {
		t.Errorf("損毀的 source 應標記並被新版本取代：%+v", a)
	}
	for _, s := range []string{StageTranscript, StageNormalize} {
		if st := latestStage(t, r, it.ID, s); st.Status != store.StatusDone {
			t.Errorf("%s = %+v，預期 done", s, st)
		}
	}
	norm := currentArtifact(t, r, it.ID, subtitle.VariantNormalized)
	if err := verifyArtifactFile(r.dir, norm); err != nil {
		t.Errorf("新的 normalized 應完好：%v", err)
	}
}

// 快取命中時檢查檔案：命中的 normalized 已損毀，就重新產生（不重用、不恢復成現行版本），損毀的那份標記起來。
func TestCorruptCacheHitIsRecomputed(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.yt.info.Tracks = []youtube.Track{{Lang: "en", Kind: "manual"}}
	it := r.ingest(watch, nil, StageNormalize)
	r.run()
	bad := currentArtifact(t, r, it.ID, subtitle.VariantNormalized)
	flipByte(t, r, bad)
	if _, err := r.p.Rerun(ctx, it.ID, RerunRequest{From: StageNormalize, Until: StageNormalize}); err != nil {
		t.Fatal(err)
	}
	r.run()
	st := latestStage(t, r, it.ID, StageNormalize)
	if st.Status != store.StatusDone || st.ArtifactID == "" || st.ArtifactID == bad.ID {
		t.Fatalf("損毀的快取不應重用：%+v", st)
	}
	if err := verifyArtifactFile(r.dir, artifactByID(t, r, st.ArtifactID)); err != nil {
		t.Errorf("重新產生的檔案：%v", err)
	}
	if a := artifactByID(t, r, bad.ID); a.CorruptAt == nil {
		t.Error("損毀的快取應標記 corrupt_at")
	}

	// 完好的快取照常重用（不建立新版本）。
	good := st.ArtifactID
	if _, err := r.p.Rerun(ctx, it.ID, RerunRequest{From: StageNormalize, Until: StageNormalize}); err != nil {
		t.Fatal(err)
	}
	r.run()
	if st := latestStage(t, r, it.ID, StageNormalize); st.ArtifactID != good {
		t.Errorf("完好的快取應重用 %s：%+v", good, st)
	}
}

// LLM 步驟的產出損毀：重跑 correct 時步驟層的快取不命中，但 chunk 層的快取照常重用，不再呼叫 provider、不新增花費。
func TestCorruptLLMArtifactKeepsChunkCache(t *testing.T) {
	fake := &chunkLLM{}
	r, it := newLLMRig(t, fake)
	ctx := context.Background()
	rerunCorrect(t, r, it)
	calls := fake.callCount()
	costs := r.count(`SELECT COUNT(*) FROM cost_ledger`)
	bad := currentArtifact(t, r, it.ID, subtitle.VariantCorrected)
	flipByte(t, r, bad)

	_, err := r.p.Cues(ctx, it.ID, CuesQuery{Variant: "corrected", ToMS: -1})
	var ce *CorruptArtifactError
	if !errors.As(err, &ce) || !ce.Rerun || ce.Stage != StageCorrect {
		t.Fatalf("讀到損毀的 corrected = %v", err)
	}
	r.run()
	st := latestStage(t, r, it.ID, StageCorrect)
	if st.Status != store.StatusDone || st.ArtifactID == bad.ID {
		t.Fatalf("重跑後 correct = %+v", st)
	}
	if n := fake.callCount(); n != calls {
		t.Errorf("chunk 快取應重用：provider 多被呼叫 %d 次", n-calls)
	}
	if n := r.count(`SELECT COUNT(*) FROM cost_ledger`); n != costs {
		t.Errorf("不應新增花費紀錄：%d → %d", costs, n)
	}
	if tr, err := r.p.Cues(ctx, it.ID, CuesQuery{Variant: "corrected", ToMS: -1}); err != nil || len(tr.Cues) != 4 || tr.Cues[0].Text != "alpha!" {
		t.Errorf("重跑後讀取 = %+v, %v", tr.Cues, err)
	}
}

// 不是由步驟產生的產出（從 bundle／Hub 播種的、匯入的）只標記，不自動重跑。
func TestCorruptSeededArtifactIsOnlyMarked(t *testing.T) {
	fake := &chunkLLM{}
	r, it := newLLMRig(t, fake)
	ctx := context.Background()
	seg := currentArtifact(t, r, it.ID, subtitle.VariantSegmented)
	flipByte(t, r, seg)
	_, err := r.p.Cues(ctx, it.ID, CuesQuery{Variant: "segmented", ToMS: -1})
	var ce *CorruptArtifactError
	if !errors.As(err, &ce) || ce.Rerun || ce.Stage != "" || ce.Error() == "" {
		t.Fatalf("播種的產出損毀 = %v", err)
	}
	if a := artifactByID(t, r, seg.ID); a.CorruptAt == nil {
		t.Error("應標記 corrupt_at")
	}
	if n, _ := r.st.Read().CountTasks(ctx, store.TaskPending); n != 0 {
		t.Errorf("不應建立任務：%d", n)
	}
}

func TestVerifyArtifactFile(t *testing.T) {
	dir := t.TempDir()
	rel, sha, size, err := writeArtifactFile(dir, "item", "art", func(w io.Writer) error { _, err := w.Write([]byte("hello world")); return err })
	if err != nil {
		t.Fatal(err)
	}
	a := store.Artifact{Path: rel, SHA256: sha, Size: size}
	if err := verifyArtifactFile(dir, a); err != nil {
		t.Fatalf("完好的檔案：%v", err)
	}
	for name, mod := range map[string]func(store.Artifact) store.Artifact{
		"大小不同":   func(a store.Artifact) store.Artifact { a.Size++; return a },
		"sha 不同": func(a store.Artifact) store.Artifact { a.SHA256 = "00" + a.SHA256[2:]; return a },
		"檔案不見":   func(a store.Artifact) store.Artifact { a.Path = "artifacts/none/none.json"; return a },
	} {
		if err := verifyArtifactFile(dir, mod(a)); !errors.Is(err, ErrCorruptArtifact) {
			t.Errorf("%s：%v", name, err)
		}
	}
	// 檔案被換成目錄也是損毀（大小不符）。
	if err := verifyArtifactFile(dir, store.Artifact{Path: "artifacts"}); !errors.Is(err, ErrCorruptArtifact) {
		t.Errorf("目錄：%v", err)
	}
	// 其他讀取錯誤（例如沒有權限）原樣回傳，不當成損毀：不能因為暫時讀不到就重跑付費的步驟。
	if os.Geteuid() != 0 {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.Chmod(full, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(full, 0o600) })
		if err := verifyArtifactFile(dir, a); err == nil || errors.Is(err, ErrCorruptArtifact) {
			t.Errorf("沒有權限：%v", err)
		}
	}
}

func TestProducerStage(t *testing.T) {
	for prov, want := range map[string]string{
		`{"stage":"transcript"}`:             StageTranscript,
		`{"stage":"correct","provider":"x"}`: StageCorrect,
		`{"stage":"correct","seeded":true}`:  "",
		`{"stage":"import"}`:                 "",
		`{}`:                                 "",
		`not json`:                           "",
	} {
		if got := producerStage(store.Artifact{Provenance: []byte(prov)}); got != want {
			t.Errorf("%s = %q，預期 %q", prov, got, want)
		}
	}
}

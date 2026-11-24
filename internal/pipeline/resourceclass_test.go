package pipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/vault"
)

func TestIsASR(t *testing.T) {
	for id, want := range map[string]bool{"groq": true, "supadata": false, "youtube-auto": false, "": false} {
		if got := isASR(id); got != want {
			t.Errorf("isASR(%q) = %v", id, got)
		}
	}
}

func (r *rig) transcriptClass(item store.Item) string {
	r.t.Helper()
	runs, err := r.st.Read().StageRunsByItem(context.Background(), item.ID)
	if err != nil {
		r.t.Fatal(err)
	}
	tk, err := r.st.Read().TaskByStageRun(context.Background(), latestRuns(runs)[StageTranscript].ID) // 重跑後取最新的一筆
	if err != nil {
		r.t.Fatal(err)
	}
	return tk.ResourceClass
}

// transcript 的資源類別在 metadata 完成、建立任務時決定：
// fallback 的第一個來源是 ASR，而且平台字幕不符合 caption_priority 時是 asr，否則是 download。
func TestTranscriptResourceClassIsDecidedAfterMetadata(t *testing.T) {
	manualEN := []youtube.Track{{Lang: "en", Kind: "manual"}}
	for _, tt := range []struct {
		name     string
		fallback []any // nil 表示用預設的 [supadata, groq]
		tracks   []youtube.Track
		want     string
	}{
		{"ASR 優先而且沒有平台字幕", []any{"groq"}, nil, queue.ResourceASR},
		{"ASR 優先但平台字幕符合", []any{"groq"}, manualEN, queue.ResourceDownload},
		{"預設的 fallback 先 supadata", nil, nil, queue.ResourceDownload},
		{"supadata 在 groq 之前", []any{"supadata", "groq"}, nil, queue.ResourceDownload},
		{"機器翻譯軌不採用", []any{"groq"}, []youtube.Track{{Lang: "ja", Kind: "platform-translated"}}, queue.ResourceASR},
		{"沒有 fallback", []any{}, nil, queue.ResourceDownload},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t)
			r.yt.info.Tracks = tt.tracks
			var ov map[string]any
			if tt.fallback != nil {
				ov = fallbackOverride(tt.fallback...)
			}
			it := r.ingest(watch, ov, StageTranscript)
			r.run()
			if got := r.transcriptClass(it); got != tt.want {
				t.Errorf("資源類別 = %s，預期 %s", got, tt.want)
			}
			if got := r.metadataClass(it); got != queue.ResourceDownload {
				t.Errorf("metadata 的資源類別 = %s", got)
			}
		})
	}
}

func (r *rig) metadataClass(item store.Item) string {
	r.t.Helper()
	tk, err := r.st.Read().TaskByStageRun(context.Background(), r.stage(item, StageMetadata).ID)
	if err != nil {
		r.t.Fatal(err)
	}
	return tk.ResourceClass
}

// 從 transcript 重跑時不知道字幕軌，一律是 download。
func TestRerunFromTranscriptIsDownloadClass(t *testing.T) {
	r := newRig(t)
	it := r.ingest(watch, fallbackOverride("groq"), StageTranscript)
	r.run()
	if got := r.transcriptClass(it); got != queue.ResourceASR {
		t.Fatalf("第一次的資源類別 = %s", got)
	}
	if err := r.rerunTranscript(it, nil); err != nil {
		t.Fatal(err)
	}
	if got := r.transcriptClass(it); got != queue.ResourceDownload {
		t.Errorf("從 transcript 重跑的資源類別 = %s，預期 download", got)
	}
}

// publish 有 git vault 時是 download（ADR-0020 第 3 點）；重試、恢復時沿用原任務的類別，不重新判斷（同 transcript 的 asr）。
func TestInsertStageTaskLikeKeepsTheEstimatedClass(t *testing.T) {
	r, it := blockedRig(t)
	ctx := context.Background()
	base := r.stage(it, StageMetadata)
	publish, transcript := base, base
	publish.Stage, publish.Executor = StagePublish, ExecutorLocal
	transcript.Stage, transcript.Executor = StageTranscript, ExecutorLocal
	for _, tt := range []struct {
		name string
		run  store.StageRun
		old  *store.Task
		want string
	}{
		{"publish 沿用 download", publish, &store.Task{ResourceClass: queue.ResourceDownload}, queue.ResourceDownload},
		{"publish 原本是 local", publish, &store.Task{ResourceClass: queue.ResourceLocal}, queue.ResourceLocal},
		{"publish 找不到原任務", publish, nil, queue.ResourceLocal},
		{"transcript 沿用 asr", transcript, &store.Task{ResourceClass: queue.ResourceASR}, queue.ResourceASR},
		{"transcript 的 download 照推定", transcript, &store.Task{ResourceClass: queue.ResourceDownload}, queue.ResourceDownload},
		{"metadata 不受原任務影響", base, &store.Task{ResourceClass: queue.ResourceASR}, queue.ResourceDownload},
	} {
		t.Run(tt.name, func(t *testing.T) {
			errRollback := errors.New("rollback")
			err := r.st.Tx(ctx, func(tx *store.Repo) error {
				tk, err := insertStageTaskLike(ctx, tx, it.ID, tt.run, queue.PriorityManual, tt.old)
				if err != nil {
					return err
				}
				if tk.ResourceClass != tt.want {
					t.Errorf("資源類別 = %s，預期 %s", tk.ResourceClass, tt.want)
				}
				return errRollback
			})
			if !errors.Is(err, errRollback) {
				t.Fatal(err)
			}
		})
	}
}

func TestHasGitVault(t *testing.T) {
	p := &Pipeline{vaults: map[string]vault.Def{
		"main":   {ID: "main", Adapter: vault.AdapterFS},
		"remote": {ID: "remote", Adapter: vault.AdapterGit},
	}}
	for _, tt := range []struct {
		ids  []string
		want bool
	}{
		{nil, false}, {[]string{"main"}, false}, {[]string{"main", "remote"}, true}, {[]string{"missing"}, false},
	} {
		if got := p.hasGitVault(tt.ids); got != tt.want {
			t.Errorf("hasGitVault(%v) = %v", tt.ids, got)
		}
	}
}

package pipeline

import (
	"context"
	"testing"

	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/store"
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

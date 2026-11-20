package pipeline

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/store"
)

// ingestLocalPath 以路徑加入本機檔案，跑到 until 為止。
func (l *localRig) ingestLocalPath(path string, overrides map[string]any, until string) store.Item {
	l.t.Helper()
	res, err := l.p.IngestPath(context.Background(), path, LocalOptions{Inbox: l.root, Overrides: overrides, Until: until})
	if err != nil {
		l.t.Fatal(err)
	}
	return res.Item
}

// 用路徑加入的檔案在同一路徑被替換之後，sha256 要重算：快取不命中，轉錄重新執行（Codex P1）。
func TestLocalReplacedFileRecomputesSHAAndMissesCache(t *testing.T) {
	l, g := newLocalGroqRig(t)
	media := l.file("talk.mp4", "MEDIA-ONE")
	it := l.ingestLocalPath(media, groqOnly(), StageTranscript)
	l.run()
	ctx := context.Background()
	if g.calls() != 1 {
		t.Fatalf("第一次轉錄：Groq 呼叫 %d 次", g.calls())
	}
	rerun := func() {
		t.Helper()
		if err := l.rerunTranscript(it, nil); err != nil {
			t.Fatal(err)
		}
		l.run()
	}

	rerun()
	if g.calls() != 1 {
		t.Fatalf("檔案沒變，重跑應命中快取：Groq 呼叫 %d 次", g.calls())
	}

	// 同一路徑換成別的內容（大小與時間都不同）。
	if err := os.WriteFile(media, []byte("MEDIA-TWO-LONGER"), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(media, later, later); err != nil {
		t.Fatal(err)
	}
	rerun()
	if g.calls() != 2 {
		t.Fatalf("檔案被替換，重跑不該命中快取：Groq 呼叫 %d 次", g.calls())
	}
	got, err := l.st.Read().ItemByID(ctx, it.ID)
	if err != nil || got.ContentSHA256 != sha("MEDIA-TWO-LONGER") {
		t.Fatalf("content_sha256 應更新為新內容：%+v, %v", got, err)
	}
	if got.OriginalLang != "ja" || got.LangSource != "asr" {
		t.Errorf("其他欄位應保留：%+v", got)
	}

	rerun()
	if g.calls() != 2 {
		t.Errorf("替換後再重跑應命中新的快取：Groq 呼叫 %d 次", g.calls())
	}
}

// 檔案沒變時不重算 sha256。上傳的副本不可變，只算一次。
func TestLocalUnchangedFileKeepsSHA(t *testing.T) {
	l, _ := newLocalGroqRig(t)
	media := l.file("talk.mp4", "MEDIA")
	it := l.ingestLocalPath(media, groqOnly(), StageMetadata)
	l.run()
	got, _ := l.st.Read().ItemByID(context.Background(), it.ID)
	if got.ContentSHA256 != sha("MEDIA") {
		t.Fatalf("sha = %q", got.ContentSHA256)
	}
	ad, _ := l.p.adapterFor("local")
	info, err := ad.probe(context.Background(), got)
	if err != nil {
		t.Fatal(err)
	}
	if info.ContentSHA256 != sha("MEDIA") || info.ComputedSHA256 != "" {
		t.Errorf("檔案沒變不該重算：%+v", info)
	}

	// 已經有 sha256 的上傳副本：不重算（不可變）。
	up := got
	up.Origin = store.OriginUpload
	up.Metadata = nil
	info, err = ad.probe(context.Background(), up)
	if err != nil || info.ComputedSHA256 != "" {
		t.Errorf("上傳的副本不重算：%+v, %v", info, err)
	}
}

// transcript 步驟重算 sha256 時，寫回的 metadata_json 要保留其他欄位（UpdateItemMeta 整欄覆寫），並記下新的 stat。
func TestLocalRefreshKeepsMetadataFields(t *testing.T) {
	l := newLocalRig(t)
	media := l.file("talk.mp4", "MEDIA")
	l.file("talk.en.srt", srtEN)
	it := l.item(media)
	it.Metadata = json.RawMessage(`{"description":"keep me"}`)

	res, err := l.p.runTranscript(context.Background(), it, DefaultLanguage())
	if err != nil {
		t.Fatal(err)
	}
	if res.meta == nil || res.meta.item.ContentSHA256 != sha("MEDIA") {
		t.Fatalf("meta = %+v", res.meta)
	}
	var md struct {
		Description string     `json:"description"`
		Local       *localStat `json:"local"`
	}
	if err := json.Unmarshal(res.meta.item.Metadata, &md); err != nil || md.Description != "keep me" || md.Local == nil || md.Local.Size != 5 {
		t.Errorf("metadata_json = %s, %v", res.meta.item.Metadata, err)
	}
	// 同一份結果也帶著推斷出的原文語言。
	if res.meta.item.OriginalLang != "en" || res.meta.item.LangSource != "metadata" {
		t.Errorf("meta = %+v", res.meta.item)
	}
}

// 外掛字幕被原地修改：內容的 sha256 在快取鍵中，重跑不命中並採用新內容（Codex P2）。
func TestLocalSidecarContentIsPartOfTheCacheKey(t *testing.T) {
	l := newLocalRig(t)
	media := l.file("talk.mp4", "MEDIA")
	sub := l.file("talk.en.srt", srtEN)
	it := l.ingestLocalPath(media, nil, StageTranscript)
	l.run()
	ctx := context.Background()
	rerun := func() {
		t.Helper()
		if err := l.rerunTranscript(it, nil); err != nil {
			t.Fatal(err)
		}
		l.run()
	}
	versions := func() int {
		t.Helper()
		n, err := l.st.Read().ArtifactVersions(ctx, it.ID, "transcript", "source", "en")
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if versions() != 1 {
		t.Fatalf("第一次：版本 %d", versions())
	}
	rerun()
	if versions() != 1 {
		t.Fatalf("字幕沒變，重跑應命中快取：版本 %d", versions())
	}

	changed := "1\n00:00:00,000 --> 00:00:02,000\nhello again\n"
	if err := os.WriteFile(sub, []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	rerun()
	if versions() != 2 {
		t.Fatalf("字幕被修改，重跑不該命中快取：版本 %d", versions())
	}
	tr, err := l.p.Cues(ctx, it.ID, CuesQuery{Variant: "source", ToMS: -1})
	if err != nil || len(tr.Cues) != 1 || tr.Cues[0].Text != "hello again" {
		t.Errorf("應採用新內容：%+v, %v", tr, err)
	}
}

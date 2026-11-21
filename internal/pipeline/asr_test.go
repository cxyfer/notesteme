package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/media"
	"github.com/cxyfer/notesteme/internal/source/groq"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

const miB = 1 << 20

func asrCue(startS, endS float64, text string) subtitle.Cue {
	return subtitle.Cue{StartMS: int64(startS * 1000), EndMS: int64(endS * 1000), Text: text, Timing: subtitle.TimingSource}
}

// asrEnv 建立 transcribeASR 的環境：假 ffmpeg 與 ffprobe、一個原始音檔、暫存目錄。
func asrEnv(t *testing.T) (*fakeMedia, *media.Tool, string, string) {
	t.Helper()
	fm, tool := newFakeMedia(t)
	dir := t.TempDir()
	in := filepath.Join(dir, "audio.m4a")
	if err := os.WriteFile(in, []byte("AUDIO"), 0o600); err != nil {
		t.Fatal(err)
	}
	return fm, tool, in, dir
}

func chunkArgs(fm *fakeMedia) [][2]string {
	var out [][2]string
	for _, a := range fm.ffmpegCalls() {
		if ffmpegKind(a) == "chunk" {
			out = append(out, [2]string{argAfter(a, "-ss"), argAfter(a, "-t")})
		}
	}
	return out
}

// 前處理後不超過上限：只上傳一次，不切塊、不偵測靜音。
func TestTranscribeASRUploadsOnceWhenWithinLimit(t *testing.T) {
	fm, tool, in, dir := asrEnv(t)
	fm.prepSize = 24 * miB
	g := &fakeGroq{lang: "en"}

	res, err := transcribeASR(context.Background(), tool, g, in, dir, "en")
	if err != nil {
		t.Fatal(err)
	}
	if g.calls() != 1 || filepath.Base(g.audios[0]) != "prepared.ogg" || g.hints[0] != "en" || !g.existed[0] {
		t.Errorf("audios = %v，hints = %v", g.audios, g.hints)
	}
	if len(res.Cues) != 2 || res.Lang != "en" {
		t.Errorf("result = %+v", res)
	}
	if fm.countKind("prepare") != 1 || fm.countKind("chunk") != 0 || fm.countKind("silence") != 0 {
		t.Errorf("ffmpeg 呼叫：%v", fm.ffmpegCalls())
	}
	// 剛好等於上限仍上傳一次。
	fm2, tool2, in2, dir2 := asrEnv(t)
	fm2.prepSize = 25 * miB
	g2 := &fakeGroq{lang: "en"}
	if _, err := transcribeASR(context.Background(), tool2, g2, in2, dir2, ""); err != nil || g2.calls() != 1 {
		t.Errorf("= %v，上傳 %d 次", err, g2.calls())
	}
}

// 契約第 6 節的場景：D=3004、壓縮後 40 MiB、上限 25 MiB → 2 塊、切點 1502；
// #41（1498–1502）歸第 0 塊，#42（1502–1506）歸第 1 塊。
func TestTranscribeASRChunksAndStitches(t *testing.T) {
	fm, tool, in, dir := asrEnv(t)
	fm.prepSize = 40 * miB
	fm.duration = 3004
	fm.silences = []media.Silence{{StartS: 1501.8, EndS: 1502.2}}
	g := &fakeGroq{}
	g.respond = func(i int, _, hint string) (*groq.Result, error) {
		if i == 0 { // 第 0 塊 [0, 1507]：重疊區重複轉錄了 #42 的前半
			return &groq.Result{Lang: "ja", Cues: []subtitle.Cue{
				asrCue(1490, 1498, "#40"),
				asrCue(1498, 1502, "and that is where the overlap starts"),
				asrCue(1502, 1506, "so we remove the duplicate line"),
			}}, nil
		}
		// 第 1 塊 [1497, 3004]：時間相對 1497
		return &groq.Result{Lang: "ja", Cues: []subtitle.Cue{
			asrCue(1, 5, "and that is where the overlap starts"),
			asrCue(5, 9, "so we remove the duplicate line"),
			asrCue(9, 13, "#43"),
		}}, nil
	}

	res, err := transcribeASR(context.Background(), tool, g, in, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if g.calls() != 2 {
		t.Fatalf("上傳 %d 次，預期 2", g.calls())
	}
	if got, want := chunkArgs(fm), [][2]string{{"0.000", "1507.000"}, {"1497.000", "1507.000"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("切塊 = %v，預期 %v", got, want)
	}
	// 靜音偵測：切點 1502 前後各 30 秒。
	for _, a := range fm.ffmpegCalls() {
		if ffmpegKind(a) == "silence" && (argAfter(a, "-ss") != "1472.000" || argAfter(a, "-t") != "60.000" || argAfter(a, "-i") != filepath.Join(dir, "prepared.ogg")) {
			t.Errorf("靜音偵測 argv = %q", a)
		}
	}
	// 語言提示：原文語言不明時，第 2 塊用第 1 塊回報的語言。
	if g.hints[0] != "" || g.hints[1] != "ja" {
		t.Errorf("hints = %v", g.hints)
	}
	var texts []string
	var prevEnd int64
	for i, c := range res.Cues {
		texts = append(texts, c.Text)
		if c.Index != i+1 || c.StartMS < prevEnd || c.EndMS < c.StartMS {
			t.Errorf("cue %d = %+v", i, c)
		}
		prevEnd = c.EndMS
	}
	want := []string{"#40", "and that is where the overlap starts", "so we remove the duplicate line", "#43"}
	if !reflect.DeepEqual(texts, want) {
		t.Errorf("texts = %q，預期 %q", texts, want)
	}
	if res.Cues[1].StartMS != 1498000 || res.Cues[2].StartMS != 1502000 || res.Lang != "ja" || res.DurationS != 3004 {
		t.Errorf("cues = %+v，lang = %q，duration = %v", res.Cues, res.Lang, res.DurationS)
	}
	// 暫存的塊上傳後就刪掉。
	if _, err := os.Stat(filepath.Join(dir, "chunk-0.ogg")); !os.IsNotExist(err) {
		t.Errorf("塊檔案應已刪除：%v", err)
	}
}

// 有原文語言提示時，每一塊都用同一個提示。
func TestTranscribeASRKeepsGivenLangHint(t *testing.T) {
	fm, tool, in, dir := asrEnv(t)
	fm.prepSize = 40 * miB
	fm.duration = 3004
	g := &fakeGroq{lang: "ja"}
	if _, err := transcribeASR(context.Background(), tool, g, in, dir, "en"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(g.hints, []string{"en", "en"}) {
		t.Errorf("hints = %v", g.hints)
	}
}

// 沒有靜音時直接在 target 切。
func TestTranscribeASRCutsAtTargetWithoutSilence(t *testing.T) {
	fm, tool, in, dir := asrEnv(t)
	fm.prepSize = 40 * miB
	fm.duration = 3000
	g := &fakeGroq{lang: "en"}
	if _, err := transcribeASR(context.Background(), tool, g, in, dir, ""); err != nil {
		t.Fatal(err)
	}
	if got, want := chunkArgs(fm), [][2]string{{"0.000", "1505.000"}, {"1495.000", "1505.000"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("切塊 = %v，預期 %v", got, want)
	}
}

// 切出的塊仍超過上限：多切一塊重新規劃（2 → 3 塊）。
func TestTranscribeASRReplansWhenAChunkIsStillTooLarge(t *testing.T) {
	fm, tool, in, dir := asrEnv(t)
	fm.prepSize = 40 * miB
	fm.duration = 3000
	fm.chunkSize = func(_, lenS float64) int64 {
		if lenS > 1200 { // 2 塊時每塊約 1505 秒：超過上限；3 塊時每塊約 1010 秒
			return 30 * miB
		}
		return 10 * miB
	}
	g := &fakeGroq{lang: "en"}
	res, err := transcribeASR(context.Background(), tool, g, in, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	// 第一次規劃在第一塊就發現太大（1 次），重新規劃成 3 塊（3 次）。
	if n := len(chunkArgs(fm)); n != 4 {
		t.Errorf("切塊呼叫 %d 次，預期 4：%v", n, chunkArgs(fm))
	}
	if g.calls() != 3 || len(res.Cues) == 0 {
		t.Errorf("上傳 %d 次，cues = %d", g.calls(), len(res.Cues))
	}
}

// 多切 3 塊仍太大就放棄（permanent），不上傳。
func TestTranscribeASRGivesUpAfterExtraChunks(t *testing.T) {
	fm, tool, in, dir := asrEnv(t)
	fm.prepSize = 40 * miB
	fm.duration = 3000
	fm.chunkSize = func(_, _ float64) int64 { return 30 * miB }
	g := &fakeGroq{lang: "en"}
	_, err := transcribeASR(context.Background(), tool, g, in, dir, "")
	var ce *errclass.Error
	if !errors.As(err, &ce) || ce.Class != errclass.ClassPermanent || ce.Message == "" {
		t.Fatalf("err = %v，應為 permanent", err)
	}
	// n = 2，最多到 n+3 = 5：共規劃 4 次，每次在第一塊就放棄。
	if n := len(chunkArgs(fm)); n != 4 || g.calls() != 0 {
		t.Errorf("切塊呼叫 %d 次、上傳 %d 次", n, g.calls())
	}
}

// 某一塊沒有語音（ErrNoResult）不讓整份失敗；全部都沒有才是沒有結果。
func TestTranscribeASRToleratesSilentChunks(t *testing.T) {
	fm, tool, in, dir := asrEnv(t)
	fm.prepSize = 40 * miB
	fm.duration = 3000
	g := &fakeGroq{}
	g.respond = func(i int, _, _ string) (*groq.Result, error) {
		if i == 0 {
			return nil, groq.ErrNoTranscript
		}
		return &groq.Result{Lang: "en", Cues: []subtitle.Cue{asrCue(10, 12, "late")}}, nil
	}
	res, err := transcribeASR(context.Background(), tool, g, in, dir, "")
	if err != nil || len(res.Cues) != 1 || res.Cues[0].Text != "late" {
		t.Fatalf("= %+v, %v", res, err)
	}

	fm, tool, in, dir = asrEnv(t)
	fm.prepSize = 40 * miB
	fm.duration = 3000
	g = &fakeGroq{respond: func(int, string, string) (*groq.Result, error) { return nil, groq.ErrNoTranscript }}
	if _, err := transcribeASR(context.Background(), tool, g, in, dir, ""); !errors.Is(err, errclass.ErrNoResult) {
		t.Errorf("err = %v，應為沒有結果", err)
	}
}

// 轉錄服務的錯誤（例如 key 被拒）原樣回傳，不繼續上傳下一塊。
func TestTranscribeASRStopsOnProviderError(t *testing.T) {
	fm, tool, in, dir := asrEnv(t)
	fm.prepSize = 40 * miB
	fm.duration = 3000
	boom := &errclass.Error{Class: errclass.ClassRetryable, Source: "groq", Detail: "HTTP 503"}
	g := &fakeGroq{err: boom}
	if _, err := transcribeASR(context.Background(), tool, g, in, dir, ""); !errors.Is(err, boom) || g.calls() != 1 {
		t.Errorf("err = %v，上傳 %d 次", err, g.calls())
	}
}

func TestTranscribeASRErrors(t *testing.T) {
	// 沒有 ffmpeg：needs_action。
	_, err := transcribeASR(context.Background(), nil, &fakeGroq{}, "/a.m4a", t.TempDir(), "")
	if c, ok := errclass.ClassOf(err); !ok || c != errclass.ClassNeedsAction {
		t.Errorf("沒有 Media：err = %v", err)
	}
	// ffmpeg 失敗：permanent，不上傳。
	fm, tool, in, dir := asrEnv(t)
	fm.ffmpegExit = 1
	g := &fakeGroq{}
	_, err = transcribeASR(context.Background(), tool, g, in, dir, "")
	if c, ok := errclass.ClassOf(err); !ok || c != errclass.ClassPermanent || g.calls() != 0 {
		t.Errorf("ffmpeg 失敗：err = %v，上傳 %d 次", err, g.calls())
	}
	// 切塊需要總長：ffprobe 回報 0 時 permanent。
	fm, tool, in, dir = asrEnv(t)
	fm.prepSize, fm.duration = 40*miB, 0
	_, err = transcribeASR(context.Background(), tool, &fakeGroq{}, in, dir, "")
	if c, ok := errclass.ClassOf(err); !ok || c != errclass.ClassPermanent {
		t.Errorf("沒有長度：err = %v", err)
	}
}

// YouTube 的 ASR 也走前處理：Groq 收到的是壓縮後的 prepared.ogg，不是 yt-dlp 下載的原檔。
func TestYouTubeASRGoesThroughPreprocessing(t *testing.T) {
	r, g := newGroqRig(t)
	it := r.ingest(watch, groqOnly(), StageTranscript)
	r.run()
	if st := r.stage(it, StageTranscript); st.Status != store.StatusDone {
		t.Fatalf("transcript = %+v", st)
	}
	if r.fm.countKind("prepare") != 1 || g.calls() != 1 || filepath.Base(g.audios[0]) != "prepared.ogg" {
		t.Errorf("prepare = %d，audios = %v", r.fm.countKind("prepare"), g.audios)
	}
	prep := r.fm.ffmpegCalls()[0]
	if filepath.Base(argAfter(prep, "-i")) != "audio.m4a" {
		t.Errorf("前處理的輸入 = %q", argAfter(prep, "-i"))
	}
}

// 前處理的 ffmpeg 不存在（沒有設定 Media）：blocked，要求安裝，而不是失敗或上傳原檔。
func TestYouTubeASRWithoutMediaIsNeedsAction(t *testing.T) {
	g := &fakeGroq{lang: "en", baseURL: groq.DefaultBaseURL, model: groq.DefaultModel}
	r := newRig(t, func(d *Deps) { d.Groq = g })
	it := r.ingest(watch, groqOnly(), StageTranscript)
	r.run()
	st := r.stage(it, StageTranscript)
	if st.Status != store.StatusBlocked || g.calls() != 0 {
		t.Errorf("transcript = %+v，上傳 %d 次", st, g.calls())
	}
}

// SRC-R11：原文語言未知、ASR 回報了語言：寫回 original_lang，lang_source 為 asr，並保留 metadata_json。
func TestASRWritesBackLanguage(t *testing.T) {
	r, g := newGroqRig(t)
	r.yt.info.Language = ""
	g.lang = "ja"
	it := r.ingest(watch, groqOnly(), StageTranscript)
	r.run()

	got, err := r.st.Read().ItemByID(context.Background(), it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.OriginalLang != "ja" || got.LangSource != "asr" {
		t.Errorf("original_lang = %q，lang_source = %q", got.OriginalLang, got.LangSource)
	}
	var meta map[string]any
	if err := json.Unmarshal(got.Metadata, &meta); err != nil {
		t.Fatal(err)
	}
	if _, ok := meta["live_status"]; !ok || len(meta) < 3 {
		t.Errorf("metadata_json 被覆寫：%s", got.Metadata)
	}
	if got.Title != "T" {
		t.Errorf("title = %q", got.Title)
	}
}

// 原文語言已知時，ASR 回報的語言不會覆蓋它（語言不符另有規則，見 language_mismatch）。
func TestASRDoesNotOverwriteKnownLanguage(t *testing.T) {
	r, g := newGroqRig(t)
	g.lang = "" // 來源沒有回報語言
	it := r.ingest(watch, groqOnly(), StageTranscript)
	r.run()
	got, _ := r.st.Read().ItemByID(context.Background(), it.ID)
	if got.OriginalLang != "en" || got.LangSource != "metadata" {
		t.Errorf("original_lang = %q，lang_source = %q", got.OriginalLang, got.LangSource)
	}
}

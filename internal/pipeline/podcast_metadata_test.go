package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/netguard"
	"github.com/cxyfer/notesteme/internal/podcast"
	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/store"
)

const (
	podAudioBody   = "NOTESTEME-AUDIO-BYTES-42"
	ep42Transcript = "https://media.example.com/show/ep42.vtt"
	vttTag         = `<podcast:transcript url="` + ep42Transcript + `" type="text/vtt"/>`
)

func sumHex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func stubResponse(r *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status, Status: http.StatusText(status), Body: io.NopCloser(strings.NewReader(body)),
		Header: http.Header{}, ContentLength: int64(len(body)), Request: r,
	}
}

// podNet 是對外 client 的替身：依網址回應，沒登記的網址回 404，並記錄每個網址被請求的次數。
type podNet struct {
	mu     sync.Mutex
	routes map[string]func(*http.Request) (*http.Response, error)
	hits   map[string]int
}

func newPodNet() *podNet {
	return &podNet{routes: map[string]func(*http.Request) (*http.Response, error){}, hits: map[string]int{}}
}

func (n *podNet) client() *http.Client {
	return &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		n.mu.Lock()
		n.hits[r.URL.String()]++
		f := n.routes[r.URL.String()]
		n.mu.Unlock()
		if f == nil {
			return stubResponse(r, http.StatusNotFound, ""), nil
		}
		return f(r)
	})}
}

func (n *podNet) route(u string, f func(*http.Request) (*http.Response, error)) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.routes[u] = f
}

func (n *podNet) serve(u string, status int, body string) {
	n.route(u, func(r *http.Request) (*http.Response, error) { return stubResponse(r, status, body), nil })
}

func (n *podNet) count(u string) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.hits[u]
}

// podRig 是 Podcast 的測試環境：假的對外 client、假 ffmpeg 與 ffprobe。
type podRig struct {
	*rig
	net *podNet
}

func newPodRig(t *testing.T, opts ...func(*Deps)) *podRig {
	t.Helper()
	fm, tool := newFakeMedia(t)
	n := newPodNet()
	r := newRig(t, append([]func(*Deps){func(d *Deps) { d.Media = tool; d.HTTP = n.client() }}, opts...)...)
	r.fm = fm
	return &podRig{rig: r, net: n}
}

// serveShow 登記 feed（只有 ep-42，帶 extra 的 XML）與它的音檔。
func (r *podRig) serveShow(extra string) {
	r.net.serve(showURL, 200, podFeed("The Show", podEp{guid: "ep-42", title: "Episode 42", audio: audio42, extra: extra}))
	r.net.serve(audio42, 200, podAudioBody)
}

// ingestEpisode 新增 ep-42，執行到 until 為止（空字串＝全部步驟），回傳最新的影音。
func (r *podRig) ingestEpisode(until string) store.Item {
	r.t.Helper()
	res, err := r.p.Ingest(context.Background(), IngestRequest{URL: showURL, Episodes: []string{"ep-42"}, Until: until})
	if err != nil {
		r.t.Fatal(err)
	}
	r.run()
	return r.reload(res[0].Item.ID)
}

func (r *podRig) reload(id string) store.Item {
	r.t.Helper()
	it, err := r.st.Read().ItemByID(context.Background(), id)
	if err != nil {
		r.t.Fatal(err)
	}
	return it
}

// latestStage 回傳步驟最新的一次執行（重跑之後 rig.stage 取到的是最舊的）。
func (r *podRig) latestStage(it store.Item, name string) store.StageRun {
	r.t.Helper()
	runs, err := r.st.Read().StageRunsByItem(context.Background(), it.ID)
	if err != nil {
		r.t.Fatal(err)
	}
	return latestRuns(runs)[name]
}

func (r *podRig) mediaDir(it store.Item) string { return filepath.Join(r.dir, "media", it.ID) }

func (r *podRig) mediaFiles(it store.Item) []string {
	entries, _ := os.ReadDir(r.mediaDir(it))
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func TestPodcastMetadataDownloadsAudio(t *testing.T) {
	r := newPodRig(t)
	r.fm.duration = 1800.4
	r.serveShow(vttTag)
	it := r.ingestEpisode(StageMetadata)

	if s := r.stage(it, StageMetadata); s.Status != store.StatusDone {
		t.Fatalf("metadata = %s %q", s.Status, s.Message)
	}
	// 音檔：<data>/media/<id>/episode.mp3，沒有留下 .part。
	b, err := os.ReadFile(filepath.Join(r.mediaDir(it), "episode.mp3"))
	if err != nil || string(b) != podAudioBody {
		t.Fatalf("音檔 = %q, %v", b, err)
	}
	if got := r.mediaFiles(it); len(got) != 1 {
		t.Errorf("媒體目錄 = %v，不該留下 .part", got)
	}
	// ffprobe 量的是絕對路徑。
	var probed string
	for _, c := range r.fm.calls {
		if c.Tool == "ffprobe" {
			probed = c.Args[len(c.Args)-1]
		}
	}
	if !filepath.IsAbs(probed) {
		t.Errorf("ffprobe 路徑 %q 不是絕對路徑", probed)
	}

	// items 欄位。
	if it.Title != "Episode 42" || it.DurationS == nil || *it.DurationS != 1800 || it.OriginalLang != "en" || it.LangSource != "metadata" ||
		it.MediaKind != "audio" || it.ContentSHA256 != sumHex(podAudioBody) {
		t.Errorf("item = %+v", it)
	}
	// metadata_json 的快照。
	var m podcastMeta
	if err := json.Unmarshal(it.Metadata, &m); err != nil {
		t.Fatal(err)
	}
	if m.Channel != "The Show" || m.Podcast.GUID != "ep-42" || m.Podcast.FeedURL != showURL || m.Podcast.FeedLanguage != "en" ||
		m.Podcast.Enclosure.URL != audio42 || len(m.Podcast.Transcripts) != 1 || m.Podcast.Transcripts[0].URL != ep42Transcript {
		t.Errorf("podcast 快照 = %+v", m)
	}
	if m.Audio == nil || m.Audio.File != "episode.mp3" || m.Audio.URL != audio42 || m.Audio.Bytes != int64(len(podAudioBody)) ||
		m.Audio.SHA256 != sumHex(podAudioBody) || m.Audio.DurationS != 1800.4 {
		t.Errorf("audio 快照 = %+v", m.Audio)
	}
	// channel：指向 feed。
	ch, err := r.st.Read().ChannelByKey(context.Background(), "podcast", "show")
	if err != nil || it.ChannelID != ch.ID || ch.Title != "The Show" {
		t.Errorf("channel = %+v, %v；item.ChannelID = %q", ch, err, it.ChannelID)
	}
}

// 重新下載後副檔名改變：舊的 episode.* 要等新的快照 commit 之後才刪。
// 還沒 commit（任務被取消、過期被丟棄、寫回失敗）時影音仍指向舊檔，不能先刪。
func TestPodcastMetadataReplacesStaleEpisodeFile(t *testing.T) {
	r := newPodRig(t)
	r.serveShow("")
	it := r.ingestEpisode(StageMetadata)
	// 伺服器轉址到 .m4a：最後的網址決定副檔名。
	final, _ := url.Parse("https://media.example.com/show/ep42.m4a")
	r.net.route(audio42, func(req *http.Request) (*http.Response, error) {
		resp := stubResponse(req, 200, "NEW-AUDIO")
		resp.Request = &http.Request{URL: final}
		return resp, nil
	})
	// 只執行、不寫回：快照仍指向 episode.mp3，它要留著。
	res, err := r.p.runPodcastMetadata(context.Background(), r.reload(it.ID), DefaultLanguage())
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(r.mediaFiles(it), ","); got != "episode.m4a,episode.mp3" {
		t.Errorf("commit 之前媒體目錄 = %v，舊檔應保留", got)
	}
	if res.afterCommit == nil {
		t.Fatal("metadata 應在 commit 之後清掉舊檔")
	}
	// 結果被丟棄（引擎對取消、過期、租約失效與寫回失敗都會這樣做）：只刪這次暫存的檔案，舊檔仍在。
	res.discard()
	if got := strings.Join(r.mediaFiles(it), ","); got != "episode.mp3" {
		t.Errorf("丟棄之後媒體目錄 = %v，預期只剩快照指向的 episode.mp3", got)
	}

	// 經過 pipeline 重跑並寫回：commit 之後只剩新檔，影音指向它。
	if _, err := r.p.Rerun(context.Background(), it.ID, RerunRequest{From: StageMetadata, Until: StageMetadata}); err != nil {
		t.Fatal(err)
	}
	r.run()
	got := r.mediaFiles(it)
	m, ok := readPodcastMeta(r.reload(it.ID).Metadata)
	if len(got) != 1 || !ok || m.Audio.File != got[0] || path.Ext(got[0]) != ".m4a" {
		t.Errorf("媒體目錄 = %v，快照 = %+v；預期只剩快照所指的 .m4a", got, m.Audio)
	}
}

// 另一個仍在執行的 metadata 任務已暫存的檔案，不能被這次下載刪掉（Codex P1）：
// 那個任務若是贏家，它 commit 的快照要指向這個檔案。
func TestDownloadEpisodeAudioKeepsOtherStagedEpisodes(t *testing.T) {
	r := newPodRig(t)
	it, enc := r.downloadFixture()
	dir := r.mediaDir(it)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(dir, "episode.m4a")
	if err := os.WriteFile(staged, []byte("STAGED-BY-NEWER-RUN"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.p.downloadEpisodeAudio(context.Background(), it, enc); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(r.mediaFiles(it), ","); got != "episode.m4a,episode.mp3" {
		t.Errorf("媒體目錄 = %s，其他任務暫存的 episode.m4a 應保留", got)
	}
}

// 另一個任務已暫存同名的 episode.mp3 時（兩個初次 metadata 任務並行），這次下載改用依內容命名的檔名，
// 不能蓋掉對方的檔案（Codex P1）。
func TestDownloadEpisodeAudioDoesNotOverwriteStagedSameName(t *testing.T) {
	r := newPodRig(t)
	it, enc := r.downloadFixture()
	dir := r.mediaDir(it)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(dir, "episode.mp3")
	if err := os.WriteFile(staged, []byte("STAGED-BY-OTHER-RUN"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := r.p.downloadEpisodeAudio(context.Background(), it, enc)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(staged); string(b) != "STAGED-BY-OTHER-RUN" {
		t.Errorf("episode.mp3 被蓋掉：%q", b)
	}
	if got.File == "episode.mp3" || !episodeFilePattern.MatchString(got.File) {
		t.Errorf("檔名 = %q，預期依內容命名的 episode-<sha8>.mp3", got.File)
	}
	if files := strings.Join(r.mediaFiles(it), ","); files != "episode-"+got.SHA256[:8]+".mp3,episode.mp3" {
		t.Errorf("媒體目錄 = %s", files)
	}
}

// 比新的重跑晚完成的 metadata 任務，它剛暫存的音檔要刪掉：沒有記錄會指向它（Codex P2）。
func TestPodcastMetadataStaleRunDiscardsItsStagedAudio(t *testing.T) {
	// 兩次重跑下載到相同內容時也一樣：贏家 commit 的檔案不能被過期任務的清理刪掉（Codex P1）。
	for _, tt := range []struct{ name, stale string }{
		{"內容不同", "STALE-RUN-AUDIO-BYTES-LONGER"},
		{"內容相同", "WINNER-AUDIO-BYTES"},
	} {
		t.Run(tt.name, func(t *testing.T) { staleRunDiscardsStagedAudio(t, tt.stale) })
	}
}

func staleRunDiscardsStagedAudio(t *testing.T, staleBody string) {
	r := newPodRig(t)
	r.serveShow("")
	it := r.ingestEpisode(StageMetadata)
	ctx := context.Background()

	// 第一次重跑：任務被領取，先不處理。
	if _, err := r.p.Rerun(ctx, it.ID, RerunRequest{From: StageMetadata, Until: StageMetadata}); err != nil {
		t.Fatal(err)
	}
	claimed, err := queue.New(r.st, r.clk, "w1").Claim(ctx, 5)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	// 第二次重跑先完成。
	if _, err := r.p.Rerun(ctx, it.ID, RerunRequest{From: StageMetadata, Until: StageMetadata}); err != nil {
		t.Fatal(err)
	}
	r.net.serve(audio42, 200, "WINNER-AUDIO-BYTES")
	r.run()
	// 舊任務現在才完成，下載到另一份內容。
	r.net.serve(audio42, 200, staleBody)
	if err := r.p.Handle(ctx, claimed[0]); err != nil {
		t.Fatal(err)
	}
	got := r.mediaFiles(it)
	m, ok := readPodcastMeta(r.reload(it.ID).Metadata)
	if !ok || len(got) != 1 || got[0] != m.Audio.File {
		t.Errorf("媒體目錄 = %v，快照 = %+v；預期只剩快照所指的檔案（過期任務暫存的音檔要刪掉）", got, m.Audio)
	}
}

// 每次下載都建立自己的檔案，不共用別的下載已經建立的同名檔：那個檔案仍屬於另一個還沒 commit 的執行，
// 它的結果被丟棄時會刪掉它（Codex P1）。內容相同的重複下載也一樣。
func TestDownloadEpisodeAudioNeverSharesFiles(t *testing.T) {
	r := newPodRig(t)
	it, enc := r.downloadFixture()
	seen := map[string]bool{}
	for i := 0; i < 4; i++ {
		got, err := r.p.downloadEpisodeAudio(context.Background(), it, enc)
		if err != nil || !got.created || seen[got.File] || !episodeFilePattern.MatchString(got.File) {
			t.Fatalf("第 %d 次下載 = %+v, %v；已用 %v", i+1, got, err, seen)
		}
		seen[got.File] = true
	}
	if n := len(r.mediaFiles(it)); n != 4 {
		t.Errorf("媒體目錄有 %d 個檔案，預期 4 個（每次下載各一個）：%v", n, r.mediaFiles(it))
	}
}

// 還在執行的任務暫存的檔案，不能被贏家 commit 之後的清理刪掉（Codex P1）：檔名一旦被釋放，之後的下載可能保留到同一個名稱，
// 原本那個任務稍後的丟棄又會刪到別人的檔案。只有檔案自己的執行（commit 或丟棄）才能放掉它。
func TestStagedEpisodeSurvivesOtherRunsCleanup(t *testing.T) {
	dir := t.TempDir()
	stage := func(body string) string {
		t.Helper()
		part := filepath.Join(dir, ".episode-"+sumHex(body)[:6]+".part")
		if err := os.WriteFile(part, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		name, err := stageEpisodeFile(part, dir, ".mp3", sumHex(body))
		if err != nil {
			t.Fatal(err)
		}
		return name
	}
	read := func(name string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return "<" + err.Error() + ">"
		}
		return string(b)
	}

	// 舊任務 A 暫存了 episode.mp3，然後贏家 B 暫存自己的檔案並 commit：commit 之後的清理要保留 B 的檔案與 A 的暫存檔。
	a := stage("A-BYTES")
	b := stage("B-BYTES")
	if a != "episode.mp3" || b == a {
		t.Fatalf("a = %q，b = %q", a, b)
	}
	releaseStagedEpisode(dir, b)
	removeStaleEpisodes(dir, b)
	if got := read(a); got != "A-BYTES" {
		t.Fatalf("A 的暫存檔被清理刪掉：%q", got)
	}

	// 之後的下載 C 拿不到 A 的檔名（檔案還在，名稱沒有被釋放）。
	c := stage("C-BYTES")
	if c == a || c == b {
		t.Fatalf("C 拿到已被占用的檔名 %q", c)
	}
	// A 的結果被丟棄：只刪 A 自己的檔案，C 的檔案完好。
	discardStagedEpisode(dir, a)
	if got := read(a); got == "A-BYTES" {
		t.Errorf("A 丟棄之後檔案還在")
	}
	if got := read(c); got != "C-BYTES" {
		t.Errorf("C 的檔案 = %q，不該被 A 的丟棄刪掉", got)
	}
	if got := read(b); got != "B-BYTES" {
		t.Errorf("B 的檔案 = %q", got)
	}
	// 解除登記、檔案也刪了：名稱可以重用。
	if d := stage("D-BYTES"); d != a {
		t.Errorf("D = %q，A 丟棄之後 %q 應該可以重用", d, a)
	}
}

// ASR 重新下載要放回快照記錄的檔名時，那個檔名若是同時進行的 metadata 任務已暫存的，不能蓋掉它（Codex P1）：
// 回傳可重試的錯誤；那個任務 commit 或丟棄之後才可以放回。
func TestRecoverEpisodeFileDoesNotReplaceStagedFile(t *testing.T) {
	dir := t.TempDir()
	part := func(body string) string {
		p := filepath.Join(dir, ".episode-"+body+".part")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	name, err := stageEpisodeFile(part("META"), dir, ".mp3", sumHex("META"))
	if err != nil || name != "episode.mp3" {
		t.Fatalf("name = %q, %v", name, err)
	}
	asr := part("ASR")
	err = recoverEpisodeFile(asr, dir, name)
	if err == nil || classOf(t, err) != errclass.ClassRetryable {
		t.Fatalf("err = %v，預期可重試的錯誤", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, name)); string(b) != "META" {
		t.Errorf("暫存檔被蓋掉：%q", b)
	}
	// metadata 結果被丟棄之後可以放回。
	discardStagedEpisode(dir, name)
	if err := recoverEpisodeFile(asr, dir, name); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, name)); string(b) != "ASR" {
		t.Errorf("放回之後內容 = %q", b)
	}
	// 下載期間別的任務已 commit 了同名的新檔（登記已放掉）：不蓋掉它，沿用現有的檔案，丟掉這次下載。
	late := part("LATE-ASR")
	if err := recoverEpisodeFile(late, dir, name); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, name)); string(b) != "ASR" {
		t.Errorf("已存在的檔案被蓋掉：%q", b)
	}
	if _, err := os.Stat(late); !os.IsNotExist(err) {
		t.Errorf("這次下載的 part 應被刪掉：%v", err)
	}
}

// 程序崩潰遺留的暫存檔（沒有任何執行登記它）由下一次 commit 之後的清理刪除；不符合檔名格式的檔案不動。
func TestRemoveStaleEpisodesSweepsUnregisteredOrphans(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"episode.mp3": "CURRENT", "episode-deadbeef.m4a": "ORPHAN", "episode.m4a": "OLD", "notes.txt": "KEEP", ".episode-x.part": "PART",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	removeStaleEpisodes(dir, "episode.mp3")
	var got []string
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		got = append(got, e.Name())
	}
	if want := ".episode-x.part,episode.mp3,notes.txt"; strings.Join(got, ",") != want {
		t.Errorf("清理之後 = %v，預期 %s", got, want)
	}
}

// 重新下載後副檔名相同：新檔不能在 commit 之前蓋掉影音現在指向的 episode.mp3（Codex P2）。
// 沒有 commit 時影音讀到的仍是舊內容；commit 之後快照指向依內容命名的新檔，舊檔被刪。
func TestPodcastMetadataSameNameAudioWaitsForCommit(t *testing.T) {
	const newBody = "NEW-AUDIO-BYTES-WITH-DIFFERENT-LENGTH"
	r := newPodRig(t)
	r.serveShow("")
	it := r.ingestEpisode(StageMetadata)
	r.net.serve(audio42, 200, newBody)
	ctx := context.Background()
	played := func() string {
		t.Helper()
		f, _, _, err := r.p.MediaFile(ctx, it.ID)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		b, err := io.ReadAll(f)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}

	// 只執行、不寫回（模擬被取消、過期或寫回失敗）：影音仍指向舊檔，播放與 ASR 讀到的還是舊內容。
	res, err := r.p.runPodcastMetadata(ctx, r.reload(it.ID), DefaultLanguage())
	if err != nil {
		t.Fatal(err)
	}
	staged := "episode-" + sumHex(newBody)[:8] + ".mp3"
	if got := strings.Join(r.mediaFiles(it), ","); got != staged+",episode.mp3" {
		t.Fatalf("commit 之前媒體目錄 = %v，預期 %s 與舊的 episode.mp3", got, staged)
	}
	if b, err := os.ReadFile(filepath.Join(r.mediaDir(it), "episode.mp3")); err != nil || string(b) != podAudioBody {
		t.Fatalf("episode.mp3 不該被蓋掉：%q, %v", b, err)
	}
	if got := played(); got != podAudioBody {
		t.Errorf("commit 之前播放到 %q，預期舊內容", got)
	}
	if res.afterCommit == nil {
		t.Fatal("metadata 應在 commit 之後清掉舊檔")
	}
	// 結果被丟棄：暫存檔刪掉，舊的 episode.mp3 與它的內容都還在。
	res.discard()
	if got := strings.Join(r.mediaFiles(it), ","); got != "episode.mp3" {
		t.Fatalf("丟棄之後媒體目錄 = %v，預期只剩 episode.mp3", got)
	}
	if got := played(); got != podAudioBody {
		t.Errorf("丟棄之後播放到 %q，預期舊內容", got)
	}

	// 經過 pipeline 重跑並寫回：快照指向依內容命名的新檔，舊檔刪除。
	if _, err := r.p.Rerun(ctx, it.ID, RerunRequest{From: StageMetadata, Until: StageMetadata}); err != nil {
		t.Fatal(err)
	}
	r.run()
	cur := r.reload(it.ID)
	m, ok := readPodcastMeta(cur.Metadata)
	if !ok || m.Audio.File != staged || m.Audio.SHA256 != sumHex(newBody) || cur.ContentSHA256 != sumHex(newBody) {
		t.Fatalf("快照 = %+v，sha = %s", m.Audio, cur.ContentSHA256)
	}
	if got := r.mediaFiles(it); len(got) != 1 || got[0] != m.Audio.File {
		t.Errorf("媒體目錄 = %v，預期只有 %s", got, m.Audio.File)
	}
	if got := played(); got != newBody {
		t.Errorf("commit 之後播放到 %q，預期新內容", got)
	}

	// 再重跑一次（內容又變）：新檔回到 episode.mp3，不與目前的檔名衝突。
	r.net.serve(audio42, 200, podAudioBody)
	if _, err := r.p.Rerun(ctx, it.ID, RerunRequest{From: StageMetadata, Until: StageMetadata}); err != nil {
		t.Fatal(err)
	}
	r.run()
	if got := r.mediaFiles(it); len(got) != 1 || got[0] != "episode.mp3" {
		t.Errorf("第二次重跑後媒體目錄 = %v，預期只有 episode.mp3", got)
	}
}

// downloadFixture 建立影音並取得 feed 中這一集的 enclosure，供直接測 downloadEpisodeAudio。
func (r *podRig) downloadFixture() (store.Item, podcast.Enclosure) {
	r.t.Helper()
	r.serveShow("")
	res, err := r.p.Ingest(context.Background(), IngestRequest{URL: showURL, Episodes: []string{"ep-42"}})
	if err != nil {
		r.t.Fatal(err)
	}
	return res[0].Item, podcast.Enclosure{URL: audio42, Type: "audio/mpeg"}
}

func classOf(t *testing.T, err error) errclass.Class {
	t.Helper()
	c, ok := errclass.ClassOf(err)
	if !ok {
		t.Fatalf("錯誤沒有分類：%v", err)
	}
	return c
}

func TestDownloadEpisodeAudioStatuses(t *testing.T) {
	for _, tt := range []struct {
		status int
		want   errclass.Class
	}{
		{403, errclass.ClassPermanent}, {404, errclass.ClassPermanent}, {410, errclass.ClassPermanent}, {400, errclass.ClassPermanent},
		{206, errclass.ClassPermanent}, {429, errclass.ClassRetryable}, {500, errclass.ClassRetryable}, {503, errclass.ClassRetryable},
	} {
		t.Run(fmt.Sprint(tt.status), func(t *testing.T) {
			r := newPodRig(t)
			it, enc := r.downloadFixture()
			r.net.serve(audio42, tt.status, "x")
			_, err := r.p.downloadEpisodeAudio(context.Background(), it, enc)
			if err == nil || classOf(t, err) != tt.want {
				t.Fatalf("err = %v，預期 %s", err, tt.want)
			}
			if got := r.mediaFiles(it); len(got) != 0 {
				t.Errorf("失敗後媒體目錄 = %v", got)
			}
		})
	}
}

// 回應宣告的大小超過 LimitAudio：不下載、不留檔，錯誤是 ErrTooLarge（防護的訊息）。
func TestDownloadEpisodeAudioLimit(t *testing.T) {
	r := newPodRig(t)
	it, enc := r.downloadFixture()
	r.net.route(audio42, func(req *http.Request) (*http.Response, error) {
		resp := stubResponse(req, 200, "x")
		resp.ContentLength = netguard.LimitAudio + 1
		return resp, nil
	})
	_, err := r.p.downloadEpisodeAudio(context.Background(), it, enc)
	if !errors.Is(err, netguard.ErrTooLarge) {
		t.Fatalf("err = %v，預期 ErrTooLarge", err)
	}
	if msg, ok := netguard.PolicyMessage(err); !ok || !strings.Contains(msg, "2 GiB") {
		t.Errorf("PolicyMessage = %q, %v", msg, ok)
	}
	if got := r.mediaFiles(it); len(got) != 0 {
		t.Errorf("媒體目錄 = %v", got)
	}
	// 沒有宣告大小、串流超過上限的情況由 netguard.LimitBody 把關（它的單元測試涵蓋）；這裡確認用的是 LimitAudio。
	if netguard.LimitAudio != 2<<30 {
		t.Errorf("LimitAudio = %d", netguard.LimitAudio)
	}
}

// 防護擋下的連線錯誤原樣傳出：classify 轉成 permanent，訊息是防護的說明。
func TestDownloadEpisodeAudioPolicyErrorPassesThrough(t *testing.T) {
	r := newPodRig(t)
	it, enc := r.downloadFixture()
	r.net.route(audio42, func(*http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("dial: %w", netguard.ErrBlockedAddress)
	})
	_, err := r.p.downloadEpisodeAudio(context.Background(), it, enc)
	if !errors.Is(err, netguard.ErrBlockedAddress) {
		t.Fatalf("err = %v，預期保留 ErrBlockedAddress", err)
	}
	res := r.p.classify(store.Task{Attempts: 1, MaxAttempts: 5}, err)
	if res.state.ErrorClass != "permanent" || res.state.Message != "這個網址指向內部網路，已封鎖。" || res.task != taskFailed {
		t.Errorf("classify = %+v", res.state)
	}
}

func TestDownloadEpisodeAudioNetworkErrorIsRetryable(t *testing.T) {
	r := newPodRig(t)
	it, enc := r.downloadFixture()
	r.net.route(audio42, func(*http.Request) (*http.Response, error) { return nil, errors.New("connection reset") })
	_, err := r.p.downloadEpisodeAudio(context.Background(), it, enc)
	if err == nil {
		t.Fatal("應該失敗")
	}
	if res := r.p.classify(store.Task{Attempts: 1, MaxAttempts: 5}, err); res.state.ErrorClass != "retryable" {
		t.Errorf("classify = %+v", res.state)
	}
}

// ffprobe 認為不是影音（例如伺服器回了 HTML 頁面）：permanent，.part 刪除。
func TestDownloadEpisodeAudioNotMedia(t *testing.T) {
	r := newPodRig(t)
	it, enc := r.downloadFixture()
	r.fm.streams = `{"index":0,"codec_type":"data"}`
	_, err := r.p.downloadEpisodeAudio(context.Background(), it, enc)
	if err == nil || classOf(t, err) != errclass.ClassPermanent {
		t.Fatalf("err = %v", err)
	}
	if got := r.mediaFiles(it); len(got) != 0 {
		t.Errorf("媒體目錄 = %v", got)
	}
}

// 沒有 ffprobe：needs_action，不留檔。
func TestDownloadEpisodeAudioWithoutFFprobe(t *testing.T) {
	r := newPodRig(t)
	it, enc := r.downloadFixture()
	r.p.media = nil
	_, err := r.p.downloadEpisodeAudio(context.Background(), it, enc)
	if err == nil || classOf(t, err) != errclass.ClassNeedsAction {
		t.Fatalf("err = %v", err)
	}
	if got := r.mediaFiles(it); len(got) != 0 {
		t.Errorf("媒體目錄 = %v", got)
	}
}

// 下載期間影音被移到垃圾桶：丟棄結果，不留檔。
func TestDownloadEpisodeAudioItemTrashed(t *testing.T) {
	r := newPodRig(t)
	it, enc := r.downloadFixture()
	if _, err := r.p.Trash(context.Background(), it.ID, TrashOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.p.downloadEpisodeAudio(context.Background(), it, enc); !errors.Is(err, errItemGone) {
		t.Fatalf("err = %v，預期 errItemGone", err)
	}
	if got := r.mediaFiles(it); len(got) != 0 {
		t.Errorf("媒體目錄 = %v", got)
	}
}

func TestAudioExt(t *testing.T) {
	for _, tt := range []struct{ url, typ, want string }{
		{"https://x.example/a/ep.MP3?token=1", "", ".mp3"},
		{"https://x.example/a/ep.m4a", "audio/mpeg", ".m4a"},
		{"https://x.example/dl?id=3", "audio/mpeg", ".mp3"},
		{"https://x.example/dl?id=3", "audio/x-m4a; charset=binary", ".m4a"},
		{"https://x.example/ep.exe", "application/octet-stream", ".bin"},
		{"https://x.example/ep", "", ".bin"},
	} {
		if got := audioExt(tt.url, tt.typ); got != tt.want {
			t.Errorf("audioExt(%q, %q) = %q，預期 %q", tt.url, tt.typ, got, tt.want)
		}
	}
}

// 重新抓 feed 的各種結果：分類與訊息。
func TestPodcastMetadataFeedOutcomes(t *testing.T) {
	r := newPodRig(t)
	it, _ := r.downloadFixture()
	run := func() (errclass.Class, string) {
		t.Helper()
		_, err := r.p.runPodcastMetadata(context.Background(), it, DefaultLanguage())
		if err == nil {
			t.Fatal("應該失敗")
		}
		res := r.p.classify(store.Task{Attempts: 1, MaxAttempts: 5}, err)
		return errclass.Class(res.state.ErrorClass), res.state.Message
	}

	r.net.serve(showURL, 404, "")
	if c, msg := run(); c != errclass.ClassPermanent || !strings.Contains(msg, "404") {
		t.Errorf("feed 404：%s %q", c, msg)
	}
	r.net.serve(showURL, 410, "")
	if c, _ := run(); c != errclass.ClassPermanent {
		t.Errorf("feed 410：%s", c)
	}
	r.net.serve(showURL, 200, "<html><body>moved</body></html>")
	if c, msg := run(); c != errclass.ClassPermanent || !strings.Contains(msg, "不是 Podcast feed") {
		t.Errorf("不是 feed：%s %q", c, msg)
	}
	r.net.serve(showURL, 500, "")
	if c, _ := run(); c != errclass.ClassRetryable {
		t.Errorf("feed 500：%s", c)
	}
	r.net.serve(showURL, 429, "")
	if c, _ := run(); c != errclass.ClassRetryable {
		t.Errorf("feed 429：%s", c)
	}
	// feed 中已經沒有這一集。
	r.net.serve(showURL, 200, podFeed("The Show", podEp{guid: "ep-99", title: "Other", audio: audio41}))
	if c, msg := run(); c != errclass.ClassPermanent || msg != "feed 中已經沒有這一集（ep-42）。" {
		t.Errorf("沒有這一集：%s %q", c, msg)
	}
	// 這一集的音檔被拿掉。
	r.net.serve(showURL, 200, podFeed("The Show", podEp{guid: "ep-42", title: "Episode 42"}))
	if c, msg := run(); c != errclass.ClassPermanent || !strings.Contains(msg, "沒有音檔") {
		t.Errorf("沒有音檔：%s %q", c, msg)
	}
	// feed 被防護擋下：原樣傳出，classify 說明原因。
	r.net.route(showURL, func(*http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("dial: %w", netguard.ErrBlockedAddress)
	})
	if c, msg := run(); c != errclass.ClassPermanent || msg != "這個網址指向內部網路，已封鎖。" {
		t.Errorf("feed 被擋：%s %q", c, msg)
	}
}

// loopDialer 把所有連線導到測試伺服器，並記錄 netguard 放行的目標 IP。
type loopDialer struct {
	mu      sync.Mutex
	addr    string
	targets []netip.Addr
}

func (d *loopDialer) Dial(ctx context.Context, t netguard.Target) (net.Conn, error) {
	d.mu.Lock()
	d.targets = append(d.targets, t.IP)
	d.mu.Unlock()
	var nd net.Dialer
	return nd.DialContext(ctx, "tcp", d.addr)
}

// hostResolver 依主機名稱依序回傳解析結果（最後一筆重複）。
type hostResolver struct {
	mu      sync.Mutex
	answers map[string][]string
	calls   map[string]int
}

func (h *hostResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	seq := h.answers[host]
	if len(seq) == 0 {
		return nil, fmt.Errorf("no such host %s", host)
	}
	i := min(h.calls[host], len(seq)-1)
	h.calls[host]++
	return []netip.Addr{netip.MustParseAddr(seq[i])}, nil
}

// GRD-R2：新增時預檢通過（公開位址），metadata 下載音檔時同一個主機改解析到內網位址：
// 連線時重新檢查而擋下，步驟是 permanent，訊息說明原因（不是「還不支援 podcast」）。
func TestPodcastMetadataRebindingBlocked(t *testing.T) {
	var audioHits int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Host {
		case "feeds.example.com":
			_, _ = io.WriteString(w, podFeed("The Show", podEp{guid: "ep-42", title: "Episode 42", audio: "http://rebind.example.com/ep42.mp3"}))
		default:
			mu.Lock()
			audioHits++
			mu.Unlock()
			_, _ = io.WriteString(w, podAudioBody)
		}
	}))
	defer srv.Close()

	res := &hostResolver{
		answers: map[string][]string{"feeds.example.com": {"93.184.216.34"}, "rebind.example.com": {"93.184.216.34", "192.168.1.1"}},
		calls:   map[string]int{},
	}
	dialer := &loopDialer{addr: srv.Listener.Addr().String()}
	nd := netguard.Deps{Resolver: res, Dialer: dialer}
	fm, tool := newFakeMedia(t)
	r := newRig(t, func(d *Deps) {
		d.Media = tool
		d.HTTP = netguard.NewClient(nd)
		d.CheckURL = func(ctx context.Context, u string) error { return netguard.CheckURL(ctx, nd, u) }
	})
	r.fm = fm

	got, err := r.p.Ingest(context.Background(), IngestRequest{URL: "http://feeds.example.com/show.xml", Episodes: []string{"ep-42"}})
	if err != nil {
		t.Fatalf("新增時預檢應該通過：%v", err)
	}
	r.run()
	st := r.stage(got[0].Item, StageMetadata)
	if st.Status != store.StatusFailed || st.ErrorClass != "permanent" || st.Message != "這個網址指向內部網路，已封鎖。" {
		t.Fatalf("metadata = %s %s %q", st.Status, st.ErrorClass, st.Message)
	}
	if res.calls["rebind.example.com"] != 2 {
		t.Errorf("rebind 主機解析 %d 次，預期 2（預檢一次、連線一次）", res.calls["rebind.example.com"])
	}
	for _, ip := range dialer.targets {
		if ip.IsPrivate() {
			t.Errorf("Dialer 收到私有 IP %s", ip)
		}
	}
	if audioHits != 0 {
		t.Errorf("內網的音檔伺服器被請求了 %d 次", audioHits)
	}
}

// feed 回 4xx（408、429 除外）是 permanent，與音檔、字幕的分類一致；408、429、5xx 可以重試。
func TestPodcastMetadataFeed4xxIsPermanent(t *testing.T) {
	r := newPodRig(t)
	it, _ := r.downloadFixture()
	for status, want := range map[int]errclass.Class{
		400: errclass.ClassPermanent, 401: errclass.ClassPermanent, 403: errclass.ClassPermanent, 451: errclass.ClassPermanent,
		408: errclass.ClassRetryable, 429: errclass.ClassRetryable, 500: errclass.ClassRetryable, 503: errclass.ClassRetryable,
	} {
		r.net.serve(showURL, status, "")
		_, err := r.p.runPodcastMetadata(context.Background(), it, DefaultLanguage())
		if err == nil {
			t.Fatalf("HTTP %d：應該失敗", status)
		}
		res := r.p.classify(store.Task{Attempts: 1, MaxAttempts: 5}, err)
		if errclass.Class(res.state.ErrorClass) != want {
			t.Errorf("HTTP %d：%s，預期 %s", status, res.state.ErrorClass, want)
		}
		if want == errclass.ClassPermanent && !strings.Contains(res.state.Message, fmt.Sprint(status)) {
			t.Errorf("HTTP %d：訊息 %q 應包含狀態碼", status, res.state.Message)
		}
	}
}

// 程序崩潰遺留的 .episode-*.part 在下一次下載開始前清掉；剛寫入的（可能是進行中的下載）與其他檔案不動。
func TestDownloadEpisodeAudioRemovesStaleParts(t *testing.T) {
	r := newPodRig(t)
	it, enc := r.downloadFixture()
	dir := r.mediaDir(it)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	old, fresh, other := filepath.Join(dir, ".episode-111.part"), filepath.Join(dir, ".episode-222.part"), filepath.Join(dir, "notes.txt")
	for _, f := range []string{old, fresh, other} {
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-time.Hour)
	for _, f := range []string{old, other} {
		if err := os.Chtimes(f, past, past); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.p.downloadEpisodeAudio(context.Background(), it, enc); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(r.mediaFiles(it), ",")
	if got != ".episode-222.part,episode.mp3,notes.txt" {
		t.Errorf("媒體目錄 = %s", got)
	}
}

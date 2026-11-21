package pipeline

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/netguard"
	"github.com/cxyfer/notesteme/internal/podcast"
	"github.com/cxyfer/notesteme/internal/store"
)

// schemeRig 用真正的 netguard client（連到本機測試伺服器）建立 Podcast 環境：
// feed 在 feeds.example.com，這一集的字幕網址是 transcriptURL，音檔是 http://media.example.com/ep42.mp3。
func schemeRig(t *testing.T, transcriptURL string, g *fakeGroq) *podRig {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == "feeds.example.com" {
			tag := `<podcast:transcript url="` + transcriptURL + `" type="text/vtt"/>`
			_, _ = io.WriteString(w, podFeed("The Show", podEp{guid: "ep-42", title: "Episode 42", audio: "http://media.example.com/ep42.mp3", extra: tag}))
			return
		}
		_, _ = io.WriteString(w, podAudioBody)
	}))
	t.Cleanup(srv.Close)
	res := &hostResolver{
		answers: map[string][]string{"feeds.example.com": {"93.184.216.34"}, "media.example.com": {"93.184.216.34"}},
		calls:   map[string]int{},
	}
	nd := netguard.Deps{Resolver: res, Dialer: &loopDialer{addr: srv.Listener.Addr().String()}}
	fm, tool := newFakeMedia(t)
	r := newRig(t, func(d *Deps) {
		d.Media = tool
		d.Groq = g
		d.HTTP = netguard.NewClient(nd)
	})
	r.fm = fm
	return &podRig{rig: r, net: newPodNet()}
}

const schemeFeedURL = "http://feeds.example.com/show.xml"

// 字幕網址不是 http、https（ftp、file、相對網址）：是被防護擋下的 permanent，改用 ASR，而不是當成可重試。
func TestPodcastTranscriptUnsupportedSchemeFallsBackToASR(t *testing.T) {
	for _, u := range []string{"ftp://media.example.com/t.vtt", "file:///etc/passwd", "t.vtt", "//media.example.com/t.vtt"} {
		t.Run(u, func(t *testing.T) {
			g := &fakeGroq{lang: "en", baseURL: "u", model: "m"}
			r := schemeRig(t, u, g)
			got, err := r.p.Ingest(context.Background(), IngestRequest{URL: schemeFeedURL, Episodes: []string{"ep-42"}, Until: StageMetadata})
			if err != nil {
				t.Fatal(err)
			}
			r.run()
			res, err := r.transcribe(r.reload(got[0].Item.ID), groqLocalLang())
			if err != nil {
				t.Fatalf("字幕網址不支援不該讓步驟失敗或重試：%v", err)
			}
			want := []string{"podcast-transcript:unavailable:permanent", "groq:used:"}
			if got := probesOf(res); !reflect.DeepEqual(got, want) || g.calls() != 1 {
				t.Fatalf("probes = %v，Groq %d 次，預期 %v 與 1 次", got, g.calls(), want)
			}
		})
	}
}

// 音檔或 feed 網址不是 http、https：permanent 並說明原因，不重試（GRD-R1）。
func TestPodcastUnsupportedSchemeIsPermanent(t *testing.T) {
	r := schemeRig(t, "", &fakeGroq{lang: "en", baseURL: "u", model: "m"})
	got, err := r.p.Ingest(context.Background(), IngestRequest{URL: schemeFeedURL, Episodes: []string{"ep-42"}, Until: StageMetadata})
	if err != nil {
		t.Fatal(err)
	}
	it := got[0].Item
	check := func(name string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s：應該失敗", name)
		}
		res := r.p.classify(store.Task{Attempts: 1, MaxAttempts: 5}, err)
		if res.state.ErrorClass != string(errclass.ClassPermanent) || res.state.Message != "只支援 http 與 https 網址。" {
			t.Errorf("%s：classify = %q %q（err = %v）", name, res.state.ErrorClass, res.state.Message, err)
		}
	}
	for _, u := range []string{"ftp://media.example.com/a.mp3", "a.mp3"} {
		_, err := r.p.downloadEpisodeAudio(context.Background(), it, podcast.Enclosure{URL: u, Type: "audio/mpeg"})
		check("音檔 "+u, err)
	}
	it.URL = "ftp://feeds.example.com/show.xml"
	_, err = r.p.runPodcastMetadata(context.Background(), it, DefaultLanguage())
	check("feed", err)
}

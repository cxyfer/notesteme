package subscription

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/clock"
	"github.com/cxyfer/notesteme/internal/config"
	"github.com/cxyfer/notesteme/internal/store"
	"github.com/cxyfer/notesteme/internal/toolexec"
)

// 這個檔案是 YouTube／Bilibili 探索的測試替身：假的 YouTube RSS（經 RoundTripper，不開 port）與假 yt-dlp（Runner）。

// ytVideo 是假 RSS 與假 yt-dlp 共用的一部影片。
type ytVideo struct {
	ID        string
	Title     string
	Published time.Time
	Shorts    bool
	Duration  *float64
	Live      string
}

// ytFake 依頻道 ID 提供 RSS；status 非 0 時回該狀態碼。
type ytFake struct {
	mu       sync.Mutex
	channels map[string][]ytVideo
	status   int
	requests int
}

func (f *ytFake) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	f.serve(rec, req)
	resp := rec.Result()
	resp.Request = req
	return resp, nil
}

func (f *ytFake) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests++
	if r.URL.Host != "www.youtube.com" || r.URL.Path != "/feeds/videos.xml" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if f.status != 0 {
		w.WriteHeader(f.status)
		return
	}
	vs, ok := f.channels[r.URL.Query().Get("channel_id")]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><feed xmlns="http://www.w3.org/2005/Atom" xmlns:yt="http://www.youtube.com/xml/schemas/2015"><title>Fixture &amp; Co</title>`)
	for _, v := range vs {
		link := "https://www.youtube.com/watch?v=" + v.ID
		if v.Shorts {
			link = "https://www.youtube.com/shorts/" + v.ID
		}
		fmt.Fprintf(&b, `<entry><id>yt:video:%s</id><yt:videoId>%s</yt:videoId><title>%s</title><link rel="alternate" href="%s"/><published>%s</published></entry>`,
			v.ID, v.ID, v.Title, link, v.Published.Format(time.RFC3339))
	}
	b.WriteString(`</feed>`)
	w.Header().Set("Content-Type", "application/atom+xml")
	_, _ = w.Write([]byte(b.String()))
}

// fakeDlp 依目標網址回應 --flat-playlist -J；probe 依影片 ID 回應 Details。
type fakeDlp struct {
	mu      sync.Mutex
	lists   map[string]toolexec.Result
	calls   []toolexec.Cmd
	failRun error
}

func (f *fakeDlp) Run(_ context.Context, c toolexec.Cmd) (toolexec.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
	if f.failRun != nil {
		return toolexec.Result{}, f.failRun
	}
	target := c.Args[len(c.Args)-1]
	if r, ok := f.lists[target]; ok {
		return r, nil
	}
	return toolexec.Result{ExitCode: 1, Stderr: []byte("ERROR: [youtube:tab] x: The playlist does not exist")}, nil
}

func (f *fakeDlp) set(target string, r toolexec.Result) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lists[target] = r
}

// flatJSON 產生 --flat-playlist -J 的輸出。
func flatJSON(t *testing.T, title, channelID string, vs ...ytVideo) toolexec.Result {
	t.Helper()
	fl := flatList{Title: title, Channel: title, ChannelID: channelID}
	for _, v := range vs {
		e := &flatEntry{ID: v.ID, URL: "https://www.youtube.com/watch?v=" + v.ID, Title: v.Title, Duration: v.Duration, LiveStatus: v.Live}
		if strings.HasPrefix(v.ID, "BV") {
			e.URL = "https://www.bilibili.com/video/" + v.ID
		}
		if !v.Published.IsZero() {
			ts := v.Published.Unix()
			e.Timestamp = &ts
		}
		fl.Entries = append(fl.Entries, e)
	}
	raw, err := json.Marshal(fl)
	if err != nil {
		t.Fatal(err)
	}
	return toolexec.Result{Stdout: raw}
}

type probeFake struct {
	mu    sync.Mutex
	info  map[string]Details
	calls int
}

func (p *probeFake) probe(_ context.Context, _, id string) (Details, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	d, ok := p.info[id]
	if !ok {
		return Details{}, fmt.Errorf("probe %s 失敗", id)
	}
	return d, nil
}

type ytRig struct {
	svc   *Service
	st    *store.Store
	clk   *clock.Fake
	ing   *fakeIngest
	feed  *ytFake
	tool  *fakeDlp
	probe *probeFake
}

const (
	testChannel  = "UCfixture000000000000000"
	testChannel2 = "UCsecond0000000000000000"
)

func newYtRig(t *testing.T, cfg config.Subscriptions) *ytRig {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC))
	st, err := store.Open(context.Background(), t.TempDir(), clk)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	bin := filepath.Join(t.TempDir(), "yt-dlp")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := &ytRig{
		st: st, clk: clk, ing: &fakeIngest{},
		feed:  &ytFake{channels: map[string][]ytVideo{}},
		tool:  &fakeDlp{lists: map[string]toolexec.Result{}},
		probe: &probeFake{info: map[string]Details{}},
	}
	r.svc = New(Deps{
		Store: st, Clock: clk, HTTP: &http.Client{Transport: r.feed}, Ingest: r.ing, Runner: r.tool,
		Tools: toolexec.Resolver{Config: map[string]string{toolexec.YtDlp.ConfigKey: bin}}, Probe: r.probe.probe, Config: cfg,
	})
	return r
}

func (r *ytRig) entries(t *testing.T, id string) map[string]string {
	t.Helper()
	es, err := r.svc.Entries(t.Context(), id, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, e := range es {
		reason := ""
		if e.Reason != nil {
			reason = *e.Reason
		}
		out[e.ExternalID] = e.Decision + "/" + reason
	}
	return out
}

func floatp(f float64) *float64 { return &f }

func strp(s string) *string { return &s }

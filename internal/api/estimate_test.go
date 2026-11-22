package api

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/source/youtube"
)

// ING-R8：有平台字幕時不計 ASR 費用，不建立影音；回應的形狀固定（null 與空陣列都要出現）。
func TestEstimateShape(t *testing.T) {
	r := newItemsRig(t)
	rec := r.owner("POST", "/api/estimate", `{"url":"https://www.youtube.com/watch?v=abc123xxxxx"}`)
	if rec.Code != 200 {
		t.Fatalf("status = %d %s", rec.Code, rec.Body)
	}
	var raw struct {
		Items []map[string]json.RawMessage `json:"items"`
		Rest  map[string]json.RawMessage   `json:"-"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil || len(raw.Items) != 1 {
		t.Fatalf("body = %s, %v", rec.Body, err)
	}
	it := raw.Items[0]
	want := map[string]string{
		"ref": `"yt:abc123xxxxx"`, "title": `"Hello"`, "duration_s": `100`, "expected_source": `"youtube-manual"`,
		"asr_usd": `0`, "source_usd": `null`, "llm_usd": `null`, "total_usd": `0`,
		"requires_cookie": `false`, "already_in_library": `false`, "notes": `[]`,
	}
	for k, v := range want {
		if got := string(it[k]); got != v {
			t.Errorf("items[0].%s = %s，預期 %s", k, got, v)
		}
	}
	var top map[string]json.RawMessage
	_ = json.Unmarshal(rec.Body.Bytes(), &top)
	for k, v := range map[string]string{"total_usd": `0`, "currency": `"USD"`, "prices_as_of": `"2026-11-21"`, "notes": `[]`} {
		if got := string(top[k]); got != v {
			t.Errorf("%s = %s，預期 %s", k, got, v)
		}
	}
	if list := decode[struct{ Items []Item }](t, r.owner("GET", "/api/items", "")); len(list.Items) != 0 {
		t.Errorf("估算不應建立影音：%+v", list)
	}
}

// 播放清單展開成多項；錯誤對應同 POST /api/items。
func TestEstimatePlaylistAndErrors(t *testing.T) {
	r := newItemsRig(t, func(d *pipeline.Deps) {
		d.YouTube = stubYT{playlist: []youtube.PlaylistEntry{{ID: "abc123xxxxx"}, {ID: "def456xxxxx"}}}
	})
	body := decode[struct {
		Items []EstimateItem `json:"items"`
	}](t, r.owner("POST", "/api/estimate", playlistBody))
	if len(body.Items) != 2 || body.Items[1].Ref != "yt:def456xxxxx" {
		t.Errorf("body = %+v", body)
	}

	tests := []struct {
		name   string
		yt     pipeline.YouTube
		body   string
		status int
		code   string
	}{
		{"頻道", nil, `{"url":"https://www.youtube.com/@someone"}`, 422, CodeChannel},
		{"無法辨識", nil, `{"url":"not a url"}`, 422, CodeUnrecognizedURL},
		{"本機檔案", nil, `{"path":"/tmp/a.mp4"}`, 422, CodeInvalidInput},
		{"沒有網址", nil, `{}`, 422, CodeInvalidInput},
		{"處理方案", nil, `{"url":"https://www.youtube.com/watch?v=abc123xxxxx","profile":"fast"}`, 501, CodeNotImplemented},
		{"yt-dlp 失敗", stubYT{playlistErr: &errclass.Error{Class: errclass.ClassRetryable, Source: "yt-dlp", Detail: "SECRET-STDERR"}},
			playlistBody, 502, CodeUpstream},
		{"播放清單不存在", stubYT{playlistErr: &errclass.Error{Class: errclass.ClassPermanent, Source: "yt-dlp", Err: errors.Join(youtube.ErrPlaylistGone)}},
			playlistBody, 422, CodeInvalidInput},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newItemsRig(t, func(d *pipeline.Deps) {
				if tt.yt != nil {
					d.YouTube = tt.yt
				}
			})
			rec := r.owner("POST", "/api/estimate", tt.body)
			e := decode[Error](t, rec)
			if rec.Code != tt.status || e.Code != tt.code {
				t.Fatalf("%d %s", rec.Code, rec.Body)
			}
			if strings.Contains(rec.Body.String(), "SECRET-STDERR") {
				t.Errorf("回應不應洩漏 yt-dlp 的輸出：%s", rec.Body)
			}
		})
	}
	// 本機檔案的訊息。
	e := decode[Error](t, r.owner("POST", "/api/estimate", `{"path":"/tmp/a.mp4"}`))
	if e.Message != "本機檔案還不支援估算花費。" {
		t.Errorf("message = %q", e.Message)
	}
}

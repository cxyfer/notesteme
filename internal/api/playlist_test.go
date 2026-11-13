package api

import (
	"errors"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/pipeline"
	"github.com/cxyfer/notesteme/internal/source/youtube"
)

const playlistBody = `{"url":"https://www.youtube.com/playlist?list=PL123"}`

// ING-R4：播放清單展開成多部影音，回 201，每部各一個項目。
func TestCreateItemPlaylist(t *testing.T) {
	r := newItemsRig(t, func(d *pipeline.Deps) {
		d.YouTube = stubYT{playlist: []youtube.PlaylistEntry{{ID: "abc123xxxxx"}, {ID: "def456xxxxx"}, {ID: "ghi789xxxxx"}}}
	})
	rec := r.owner("POST", "/api/items", playlistBody)
	if rec.Code != 201 {
		t.Fatalf("status = %d %s", rec.Code, rec.Body)
	}
	body := decode[itemsBody](t, rec)
	if len(body.Items) != 3 || body.Items[0].Ref != "yt:abc123xxxxx" || body.Items[2].Ref != "yt:ghi789xxxxx" {
		t.Fatalf("body = %+v", body)
	}
	if list := decode[struct{ Items []Item }](t, r.owner("GET", "/api/items", "")); len(list.Items) != 3 {
		t.Errorf("影音數 = %d，預期 3", len(list.Items))
	}
	// 全部已存在時回 200（ING-R2）。
	if rec := r.owner("POST", "/api/items", playlistBody); rec.Code != 200 {
		t.Errorf("再次新增應回 200：%d %s", rec.Code, rec.Body)
	}
}

// yt-dlp 失敗時的回應：不存在 422、其他 502（訊息是給使用者的說明，不含 yt-dlp 的輸出）。
func TestCreateItemPlaylistFailures(t *testing.T) {
	tests := []struct {
		name    string
		yt      stubYT
		status  int
		code    string
		message string
	}{
		{"不存在", stubYT{playlistErr: &errclass.Error{Class: errclass.ClassPermanent, Source: "yt-dlp", Err: youtube.ErrPlaylistGone}},
			422, CodeInvalidInput, "找不到這個播放清單，可能已刪除或不是公開的。"},
		{"暫時性失敗", stubYT{playlistErr: &errclass.Error{Class: errclass.ClassRetryable, Source: "yt-dlp", Detail: "SECRET-STDERR"}},
			502, CodeUpstream, "yt-dlp 讀取播放清單失敗，請稍後再試。"},
		{"找不到 yt-dlp", stubYT{playlistErr: &errclass.Error{Class: errclass.ClassNeedsAction, Source: "yt-dlp", Err: errors.New("boom")}},
			502, CodeUpstream, "無法執行 yt-dlp，請確認已安裝（可執行 notesteme doctor）。"},
		{"空的播放清單", stubYT{}, 422, CodeInvalidInput, "這個播放清單沒有可新增的影片。"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newItemsRig(t, func(d *pipeline.Deps) { d.YouTube = tt.yt })
			rec := r.owner("POST", "/api/items", playlistBody)
			e := decode[Error](t, rec)
			if rec.Code != tt.status || e.Code != tt.code || e.Message != tt.message {
				t.Fatalf("%d %s", rec.Code, rec.Body)
			}
			if strings.Contains(rec.Body.String(), "SECRET-STDERR") {
				t.Errorf("回應不應洩漏 yt-dlp 的輸出：%s", rec.Body)
			}
			if list := decode[struct{ Items []Item }](t, r.owner("GET", "/api/items", "")); len(list.Items) != 0 {
				t.Errorf("失敗時不應建立影音：%+v", list)
			}
		})
	}
}

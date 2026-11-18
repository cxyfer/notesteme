package pipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/queue"
	"github.com/cxyfer/notesteme/internal/source/youtube"
	"github.com/cxyfer/notesteme/internal/store"
)

const playlistURL = "https://www.youtube.com/playlist?list=PL123"

func threeVideos() []youtube.PlaylistEntry {
	return []youtube.PlaylistEntry{{ID: "abc123xxxxx", Title: "One"}, {ID: "def456xxxxx", Title: "Two"}, {ID: "ghi789xxxxx", Title: "Three"}}
}

// ING-R4：播放清單展開成多部影音，每部各自建立 item 與 metadata 任務，優先順序為手動新增。
func TestIngestPlaylistExpandsIntoItems(t *testing.T) {
	r := newRig(t)
	r.yt.playlist = threeVideos()
	ctx := context.Background()

	res, err := r.p.Ingest(ctx, IngestRequest{URL: playlistURL, Until: "transcript", Overrides: map[string]any{"language": map[string]any{"targets": []any{"en"}}}})
	if err != nil || len(res) != 3 {
		t.Fatalf("Ingest = %+v, %v", res, err)
	}
	if len(r.yt.playlists) != 1 || r.yt.playlists[0] != "PL123" {
		t.Fatalf("應只展開一次，播放清單 ID = %v", r.yt.playlists)
	}
	for i, want := range []string{"abc123xxxxx", "def456xxxxx", "ghi789xxxxx"} {
		it := res[i].Item
		if res[i].AlreadyInLibrary || it.Platform != "youtube" || it.ExternalID != want || it.Part != 0 ||
			it.URL != "https://www.youtube.com/watch?v="+want {
			t.Errorf("結果 %d = %+v", i, res[i])
		}
		// 單次覆寫與 until 套用到每一部（ING-R7）。
		if it.UntilStage != "transcript" || string(it.Overrides) == "{}" {
			t.Errorf("影音 %s 的 until／overrides = %q／%s", want, it.UntilStage, it.Overrides)
		}
		tk, err := r.st.Read().TaskByStageRun(ctx, r.stage(it, StageMetadata).ID)
		if err != nil || tk.Priority != queue.PriorityManual || tk.State != store.TaskPending {
			t.Errorf("%s 的 metadata 任務 = %+v, %v", want, tk, err)
		}
	}
	if items, _ := r.p.Items(ctx); len(items) != 3 {
		t.Errorf("影音數 = %d，預期 3", len(items))
	}
	if n, _ := r.st.Read().CountTasks(ctx, store.TaskPending); n != 3 {
		t.Errorf("pending 任務數 = %d，預期 3", n)
	}
}

// 展開是一次性的（ING-R4）：再次新增同一個播放清單，不會有新的影音；清單之後新增的影片，只有再次新增時才會出現。
func TestIngestPlaylistIsOneShot(t *testing.T) {
	r := newRig(t)
	r.yt.playlist = threeVideos()
	ctx := context.Background()
	if _, err := r.p.Ingest(ctx, IngestRequest{URL: playlistURL}); err != nil {
		t.Fatal(err)
	}
	// 清單中已經存在的影音（ING-R2）：回應標示 already_in_library，不建立新項目與新任務。
	again, err := r.p.Ingest(ctx, IngestRequest{URL: playlistURL})
	if err != nil || len(again) != 3 {
		t.Fatalf("again = %+v, %v", again, err)
	}
	for _, a := range again {
		if !a.AlreadyInLibrary {
			t.Errorf("已存在的影音應標示 already_in_library：%+v", a)
		}
	}
	if items, _ := r.p.Items(ctx); len(items) != 3 {
		t.Errorf("影音數 = %d，預期 3", len(items))
	}
	if n, _ := r.st.Read().CountTasks(ctx, store.TaskPending); n != 3 {
		t.Errorf("pending 任務數 = %d，預期 3", n)
	}
}

// 一部分已經在資料庫、一部分是新的，以及清單中重複的影片：各自套用 ING-R2。
func TestIngestPlaylistMixesExistingAndDuplicates(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.ingest("https://youtu.be/def456xxxxx", nil, "")
	r.yt.playlist = []youtube.PlaylistEntry{{ID: "abc123xxxxx"}, {ID: "def456xxxxx"}, {ID: "abc123xxxxx"}}
	res, err := r.p.Ingest(ctx, IngestRequest{URL: playlistURL})
	if err != nil || len(res) != 3 {
		t.Fatalf("Ingest = %+v, %v", res, err)
	}
	if got := []bool{res[0].AlreadyInLibrary, res[1].AlreadyInLibrary, res[2].AlreadyInLibrary}; got[0] || !got[1] || !got[2] {
		t.Errorf("already_in_library = %v，預期 [false true true]", got)
	}
	if res[0].Item.ID != res[2].Item.ID {
		t.Errorf("清單中重複的影片應指向同一個影音：%q／%q", res[0].Item.ID, res[2].Item.ID)
	}
	if items, _ := r.p.Items(ctx); len(items) != 2 {
		t.Errorf("影音數 = %d，預期 2", len(items))
	}
}

func TestIngestPlaylistFailures(t *testing.T) {
	gone := &errclass.Error{Class: errclass.ClassPermanent, Source: "yt-dlp", Err: youtube.ErrPlaylistGone}
	transient := &errclass.Error{Class: errclass.ClassRetryable, Source: "yt-dlp", ExitCode: 1, Detail: "timed out"}
	missing := &errclass.Error{Class: errclass.ClassNeedsAction, Source: "yt-dlp", Err: errors.New("找不到 yt-dlp")}

	tests := []struct {
		name    string
		entries []youtube.PlaylistEntry
		err     error
		check   func(error) bool
	}{
		{"播放清單不存在", nil, gone, func(e error) bool {
			var inv *InvalidError
			return errors.As(e, &inv) && inv.Msg == "找不到這個播放清單，可能已刪除或不是公開的。"
		}},
		{"暫時性失敗", nil, transient, func(e error) bool {
			var up *UpstreamError
			return errors.As(e, &up) && up.Msg == "yt-dlp 讀取播放清單失敗，請稍後再試。" && errors.Is(e, transient)
		}},
		{"找不到 yt-dlp", nil, missing, func(e error) bool {
			var up *UpstreamError
			return errors.As(e, &up) && up.Msg == "無法執行 yt-dlp，請確認已安裝（可執行 notesteme doctor）。"
		}},
		{"清單是空的", nil, nil, func(e error) bool {
			var inv *InvalidError
			return errors.As(e, &inv) && inv.Msg == "這個播放清單沒有可新增的影片。"
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t)
			r.yt.playlist, r.yt.playlistErr = tt.entries, tt.err
			res, err := r.p.Ingest(context.Background(), IngestRequest{URL: playlistURL})
			if err == nil || !tt.check(err) || res != nil {
				t.Fatalf("res = %+v，err = %v", res, err)
			}
			if items, _ := r.p.Items(context.Background()); len(items) != 0 {
				t.Errorf("失敗時不應建立影音：%d", len(items))
			}
		})
	}
}

// 被取消時原樣回傳 context 的錯誤，不包成使用者看的訊息。
func TestIngestPlaylistCancelled(t *testing.T) {
	r := newRig(t)
	r.yt.playlistErr = context.Canceled
	if _, err := r.p.Ingest(context.Background(), IngestRequest{URL: playlistURL}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

// 播放清單與影片一樣不能指定分P、單集；也不先呼叫 yt-dlp。
func TestIngestPlaylistRejectsPartsBeforeExpanding(t *testing.T) {
	r := newRig(t)
	r.yt.playlist = threeVideos()
	var inv *InvalidError
	if _, err := r.p.Ingest(context.Background(), IngestRequest{URL: playlistURL, Parts: []int{1}}); !errors.As(err, &inv) {
		t.Fatalf("err = %v", err)
	}
	if len(r.yt.playlists) != 0 {
		t.Errorf("驗證失敗時不應展開播放清單：%v", r.yt.playlists)
	}
}

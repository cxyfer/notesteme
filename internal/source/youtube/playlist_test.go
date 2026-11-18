package youtube

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/toolexec"
)

const samplePlaylist = `{
  "_type": "playlist", "id": "PL123", "title": "Talks",
  "entries": [
    {"_type": "url", "ie_key": "Youtube", "id": "abc123xxxxx", "title": "One", "url": "https://www.youtube.com/watch?v=abc123xxxxx"},
    {"_type": "url", "ie_key": "Youtube", "id": "def456xxxxx", "title": "Two"},
    {"_type": "url", "ie_key": "Youtube", "id": "ghi789xxxxx", "title": "Three"}
  ]
}`

func TestPlaylistParsesEntriesAndUsesSafeArguments(t *testing.T) {
	r := &stubRunner{res: toolexec.Result{Stdout: []byte(samplePlaylist)}}
	got, err := newClient(t, r).Playlist(context.Background(), "PL123")
	if err != nil {
		t.Fatal(err)
	}
	want := []PlaylistEntry{{"abc123xxxxx", "One"}, {"def456xxxxx", "Two"}, {"ghi789xxxxx", "Three"}}
	if !slices.Equal(got, want) {
		t.Fatalf("entries = %+v，預期 %+v", got, want)
	}

	args := r.calls[0].Args
	for _, must := range []string{"--flat-playlist", "-J", "--ignore-config", "--use-extractors"} {
		if !slices.Contains(args, must) {
			t.Errorf("缺少旗標 %s：%v", must, args)
		}
	}
	// 展開播放清單時不能帶 --no-playlist，否則 yt-dlp 不會列出清單內容。
	if slices.Contains(args, "--no-playlist") {
		t.Errorf("不應有 --no-playlist：%v", args)
	}
	// 只開放 YouTube 播放清單的 extractor（GRD-R6、ADR-0009）：yt-dlp 以 fullmatch 比對 IE_NAME，
	// 播放清單由 youtube:tab 與 youtube:playlist 處理，只寫 youtube 在真實的 yt-dlp 會是 Unsupported URL。
	if i := slices.Index(args, "--use-extractors"); args[i+1] != "youtube:tab,youtube:playlist" {
		t.Errorf("--use-extractors 應為 youtube:tab,youtube:playlist：%v", args)
	}
	// 網址是正規化的播放清單網址，放在 -- 之後。
	if n := len(args); args[n-2] != "--" || args[n-1] != "https://www.youtube.com/playlist?list=PL123" {
		t.Errorf("網址應在 -- 之後：%v", args)
	}
	if !filepath.IsAbs(r.calls[0].Path) {
		t.Errorf("工具路徑應是絕對路徑：%q", r.calls[0].Path)
	}
}

// 播放清單的內容是不可信的資料：ID 不是 11 個字元的項目（已刪除、不可用）略過，null 不會讓解析失敗。
func TestPlaylistSkipsInvalidEntries(t *testing.T) {
	const out = `{"entries": [
	  null,
	  {"id": "abc123xxxxx", "title": "ok"},
	  {"id": "short", "title": "bad id"},
	  {"id": "--exec=evil;;", "title": "bad chars"},
	  {"title": "no id"},
	  {"id": "def456xxxxx"}
	]}`
	r := &stubRunner{res: toolexec.Result{Stdout: []byte(out)}}
	got, err := newClient(t, r).Playlist(context.Background(), "PL123")
	if err != nil {
		t.Fatal(err)
	}
	want := []PlaylistEntry{{"abc123xxxxx", "ok"}, {"def456xxxxx", ""}}
	if !slices.Equal(got, want) {
		t.Fatalf("entries = %+v，預期 %+v", got, want)
	}
}

func TestPlaylistEmptyAndMalformedOutput(t *testing.T) {
	r := &stubRunner{res: toolexec.Result{Stdout: []byte(`{"entries": []}`)}}
	if got, err := newClient(t, r).Playlist(context.Background(), "PL123"); err != nil || len(got) != 0 {
		t.Errorf("空的播放清單：%+v, %v", got, err)
	}
	r = &stubRunner{res: toolexec.Result{Stdout: []byte(`not json`)}}
	_, err := newClient(t, r).Playlist(context.Background(), "PL123")
	if c, ok := errclass.ClassOf(err); !ok || c != errclass.ClassRetryable {
		t.Errorf("無法解析的輸出應是 retryable：%v", err)
	}
}

func TestPlaylistClassifiesFailures(t *testing.T) {
	tests := []struct {
		name   string
		res    toolexec.Result
		err    error
		class  errclass.Class
		isGone bool
	}{
		{"播放清單不存在", toolexec.Result{ExitCode: 1, Stderr: []byte("ERROR: [youtube:tab] PL123: The playlist does not exist.\n")}, nil, errclass.ClassPermanent, true},
		{"私人播放清單", toolexec.Result{ExitCode: 1, Stderr: []byte("ERROR: [youtube:tab] PL123: This playlist is private\n")}, nil, errclass.ClassPermanent, true},
		{"其他錯誤", toolexec.Result{ExitCode: 1, Stderr: []byte("ERROR: unable to download webpage: timed out\n")}, nil, errclass.ClassRetryable, false},
		{"工具無法啟動", toolexec.Result{}, errors.New("exec: boom"), errclass.ClassRetryable, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newClient(t, &stubRunner{res: tt.res, err: tt.err}).Playlist(context.Background(), "PL123")
			c, ok := errclass.ClassOf(err)
			if !ok || c != tt.class {
				t.Fatalf("err = %v，預期類別 %s", err, tt.class)
			}
			if got := errors.Is(err, ErrPlaylistGone); got != tt.isGone {
				t.Errorf("errors.Is(ErrPlaylistGone) = %v，預期 %v", got, tt.isGone)
			}
		})
	}
}

func TestPlaylistMissingToolNeedsAction(t *testing.T) {
	c := &Client{Runner: &stubRunner{}, Tools: toolexec.Resolver{Vars: map[string]string{"PATH": t.TempDir()}}}
	_, err := c.Playlist(context.Background(), "PL123")
	if cl, ok := errclass.ClassOf(err); !ok || cl != errclass.ClassNeedsAction {
		t.Errorf("找不到 yt-dlp 應是 needs_action：%v", err)
	}
}

func TestValidVideoID(t *testing.T) {
	for id, want := range map[string]bool{
		"abc123xxxxx": true, "a-_B9xxxxxx": true, "abc123xxxx": false, "abc123xxxxxx": false, "": false, "--exec=evil": false, "abc 123xxxx": false,
	} {
		if got := ValidVideoID(id); got != want {
			t.Errorf("ValidVideoID(%q) = %v", id, got)
		}
	}
}

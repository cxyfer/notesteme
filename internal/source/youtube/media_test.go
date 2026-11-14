package youtube

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/netguard"
	"github.com/cxyfer/notesteme/internal/toolexec"
)

// writeAudio 回傳一個會在 -o 模板指定的目錄寫出 ext 副檔名音檔的 after 函式。
func writeAudio(t *testing.T, ext string) func(toolexec.Cmd) {
	return func(c toolexec.Cmd) {
		i := slices.Index(c.Args, "-o")
		if i < 0 {
			t.Error("缺少 -o")
			return
		}
		dir := filepath.Dir(c.Args[i+1])
		if err := os.WriteFile(filepath.Join(dir, "audio."+ext), []byte("AUDIO"), 0o600); err != nil {
			t.Error(err)
		}
	}
}

func TestDownloadAudioArgumentsAndPath(t *testing.T) {
	dir := t.TempDir()
	r := &stubRunner{after: writeAudio(t, "webm")}
	path, err := newClient(t, r).DownloadAudio(context.Background(), "abc123xxxxx", dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "audio.webm"); path != want {
		t.Errorf("path = %q，預期 %q", path, want)
	}

	args := r.calls[0].Args
	for _, must := range []string{"--ignore-config", "--use-extractors", "--no-playlist", "-f", "bestaudio"} {
		if !slices.Contains(args, must) {
			t.Errorf("缺少旗標 %s：%v", must, args)
		}
	}
	if i := slices.Index(args, "-f"); args[i+1] != "bestaudio" {
		t.Errorf("-f 應為 bestaudio：%v", args)
	}
	if i := slices.Index(args, "--use-extractors"); args[i+1] != "youtube" {
		t.Errorf("--use-extractors 應只開放 youtube：%v", args)
	}
	if i := slices.Index(args, "-o"); args[i+1] != filepath.Join(dir, "audio.%(ext)s") {
		t.Errorf("-o = %q", args[i+1])
	}
	if n := len(args); args[n-2] != "--" || args[n-1] != "https://www.youtube.com/watch?v=abc123xxxxx" {
		t.Errorf("網址應在 -- 之後：%v", args)
	}
	if r.calls[0].Timeout <= 0 {
		t.Error("應該設定逾時")
	}
}

func TestDownloadAudioEscapesPercentInDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "100%done")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	r := &stubRunner{after: func(c toolexec.Cmd) {
		i := slices.Index(c.Args, "-o")
		if want := filepath.Join(filepath.Dir(dir), "100%%done", "audio.%(ext)s"); c.Args[i+1] != want {
			t.Errorf("-o = %q，預期 %q", c.Args[i+1], want)
		}
		_ = os.WriteFile(filepath.Join(dir, "audio.m4a"), []byte("A"), 0o600)
	}}
	path, err := newClient(t, r).DownloadAudio(context.Background(), "x", dir)
	if err != nil || path != filepath.Join(dir, "audio.m4a") {
		t.Errorf("path = %q, err = %v", path, err)
	}
}

func TestDownloadAudioIgnoresPartialAndStaleFiles(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, "audio.m4a")
	if err := os.WriteFile(stale, []byte("OLD"), 0o600); err != nil {
		t.Fatal(err)
	}
	// yt-dlp 結束碼為 0 但只留下暫存檔：不能回傳舊檔或暫存檔。
	r := &stubRunner{after: func(toolexec.Cmd) {
		_ = os.WriteFile(filepath.Join(dir, "audio.webm.part"), []byte("P"), 0o600)
	}}
	_, err := newClient(t, r).DownloadAudio(context.Background(), "x", dir)
	if !errors.Is(err, ErrNoAudioFile) {
		t.Fatalf("err = %v，預期 ErrNoAudioFile", err)
	}
	if cl, _ := errclass.ClassOf(err); cl != errclass.ClassRetryable {
		t.Errorf("class = %s", cl)
	}
	if _, statErr := os.Stat(stale); !os.IsNotExist(statErr) {
		t.Error("下載前應清掉殘留的舊檔")
	}
}

func TestDownloadAudioClassifiesFailures(t *testing.T) {
	for name, tt := range map[string]struct {
		stderr string
		want   errclass.Class
	}{
		"影片不存在": {"ERROR: [youtube] abc123xxxxx: Video unavailable\n", errclass.ClassPermanent},
		"私人影片":  {"ERROR: [youtube] abc123xxxxx: Private video. Sign in\n", errclass.ClassPermanent},
		"403":   {"ERROR: unable to download video data: HTTP Error 403: Forbidden\n", errclass.ClassNeedsAction},
		"其他錯誤":  {"ERROR: unable to download video data: HTTP Error 503: Service Unavailable\n", errclass.ClassRetryable},
	} {
		t.Run(name, func(t *testing.T) {
			r := &stubRunner{res: toolexec.Result{ExitCode: 1, Stderr: []byte(tt.stderr)}}
			_, err := newClient(t, r).DownloadAudio(context.Background(), "abc123xxxxx", t.TempDir())
			var e *errclass.Error
			if !errors.As(err, &e) || e.Class != tt.want || e.ExitCode != 1 {
				t.Errorf("err = %#v，預期類別 %s", err, tt.want)
			}
		})
	}
}

func TestDownloadAudioRunnerErrorAndCancel(t *testing.T) {
	r := &stubRunner{err: errors.New("boom")}
	if _, err := newClient(t, r).DownloadAudio(context.Background(), "x", t.TempDir()); !errors.Is(err, r.err) {
		t.Errorf("err = %v", err)
	} else if cl, _ := errclass.ClassOf(err); cl != errclass.ClassRetryable {
		t.Errorf("class = %s", cl)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r = &stubRunner{err: ctx.Err()}
	if _, err := newClient(t, r).DownloadAudio(ctx, "x", t.TempDir()); !errors.Is(err, context.Canceled) {
		t.Errorf("取消應原樣回傳：%v", err)
	}
}

func TestDownloadAudioWithoutToolNeedsAction(t *testing.T) {
	c := &Client{Runner: &stubRunner{}, Tools: toolexec.Resolver{Vars: map[string]string{"PATH": t.TempDir()}}}
	_, err := c.DownloadAudio(context.Background(), "x", t.TempDir())
	if cl, ok := errclass.ClassOf(err); !ok || cl != errclass.ClassNeedsAction {
		t.Errorf("找不到 yt-dlp 時應是 needs_action：%v", err)
	}
}

// 音檔上限（GRD-R5，2 GiB）交給 yt-dlp 的 --max-filesize：超過時不下載。
func TestDownloadAudioPassesTheAudioSizeLimit(t *testing.T) {
	r := &stubRunner{after: writeAudio(t, "m4a")}
	if _, err := newClient(t, r).DownloadAudio(context.Background(), "x", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	args := r.calls[0].Args
	i := slices.Index(args, "--max-filesize")
	if i < 0 || args[i+1] != strconv.FormatInt(netguard.LimitAudio, 10) {
		t.Errorf("--max-filesize 應為 %d bytes：%v", netguard.LimitAudio, args)
	}
}

// 超過上限時 yt-dlp 回報 "File is larger than max-filesize"（可能結束碼為 0 而沒有寫出檔案）：重試也不會變小，permanent。
func TestDownloadAudioTooLargeIsPermanent(t *testing.T) {
	const line = "[download] File is larger than max-filesize (3000000000 bytes > 2147483648 bytes). Aborting.\n"
	for name, res := range map[string]toolexec.Result{
		"結束碼 0":  {ExitCode: 0, Stdout: []byte(line)},
		"結束碼非 0": {ExitCode: 1, Stderr: []byte("ERROR: File is larger than max-filesize (3000000000 bytes > 2147483648 bytes)\n")},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := newClient(t, &stubRunner{res: res}).DownloadAudio(context.Background(), "x", t.TempDir())
			if cl, ok := errclass.ClassOf(err); !ok || cl != errclass.ClassPermanent {
				t.Errorf("err = %#v，預期 permanent", err)
			}
		})
	}
}

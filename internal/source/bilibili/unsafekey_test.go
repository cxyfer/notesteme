package bilibili

import (
	"bytes"
	"context"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/toolexec"
)

const unsafeKeysInfo = `{
  "id": "BV1xx411c7mD", "title": "T",
  "subtitles": {
    "zh-Hans": [{"ext": "srt"}],
    "ai-zh": [{"ext": "srt"}],
    "-evil": [{"ext": "srt"}],
    "all": [{"ext": "srt"}],
    "has space": [{"ext": "srt"}],
    "zh;rm": [{"ext": "srt"}],
    "": [{"ext": "srt"}],
    "danmaku": [{"ext": "xml"}]
  }
}`

// 不合 safeKey 的字幕軌鍵（FetchCaptions 一定會拒絕）在 parseInfo 就丟掉並記 log，
// 讓選軌不會選到下載不了的軌。
func TestParseInfoDropsUnsafeTrackKeys(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	info, err := parseInfo(t.Context(), []byte(unsafeKeysInfo), log)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"ai-zh", "danmaku", "zh-Hans"}; !slices.Equal(info.Tracks, want) {
		t.Errorf("tracks = %v，預期 %v", info.Tracks, want)
	}
	for _, k := range []string{"-evil", "has space", "zh;rm"} {
		if !strings.Contains(buf.String(), k) {
			t.Errorf("log 沒有記錄被丟掉的鍵 %q：%s", k, buf.String())
		}
	}
	for _, tr := range info.Tracks {
		if !usableKey(tr) {
			t.Errorf("留下的軌 %q 下載不了", tr)
		}
	}
}

// Probe 經由 Client.Log 記錄；沒有設定 Log 時也不會 panic。
func TestProbeLogsDroppedTrackKeys(t *testing.T) {
	r := &stubRunner{res: toolexec.Result{Stdout: []byte(unsafeKeysInfo)}}
	c := newClient(t, r, nil)
	var buf bytes.Buffer
	c.Log = slog.New(slog.NewTextHandler(&buf, nil))
	info, err := c.Probe(context.Background(), "BV1xx411c7mD", 1)
	if err != nil || slices.Contains(info.Tracks, "-evil") {
		t.Fatalf("info = %+v, %v", info, err)
	}
	if !strings.Contains(buf.String(), "-evil") {
		t.Errorf("log = %s", buf.String())
	}

	c.Log = nil
	if _, err := c.Probe(context.Background(), "BV1xx411c7mD", 1); err != nil {
		t.Fatal(err)
	}
}

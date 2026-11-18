package faketool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const flatPlaylistJSON = `{"_type":"playlist","id":"PL123","entries":[{"id":"abc123xxxxx","title":"One"},{"id":"def456xxxxx","title":"Two"}]}`

// plArgs 是 production 展開播放清單時使用的參數（見 youtube.Client.Playlist）。
func plArgs(rawURL string) []string {
	return []string{"--ignore-config", "--use-extractors", "youtube:tab,youtube:playlist", "--no-warnings", "--flat-playlist", "-J", "--", rawURL}
}

func playlistState(t *testing.T) State {
	t.Helper()
	fx := t.TempDir()
	if err := os.MkdirAll(filepath.Join(fx, "ytdlp"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fx, "ytdlp", "pl.json"), []byte(flatPlaylistJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	return State{FixturesDir: fx, Playlists: map[string]string{"PL123": "pl.json", "PLGONE": "missing.json"}}
}

func TestFlatPlaylistPrintsFixture(t *testing.T) {
	s := newScenario(t, playlistState(t))
	code, out, errs := s.exec(YtDlp, plArgs("https://www.youtube.com/playlist?list=PL123")...)
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errs)
	}
	var got struct {
		Entries []struct{ ID string } `json:"entries"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil || len(got.Entries) != 2 || got.Entries[0].ID != "abc123xxxxx" {
		t.Fatalf("stdout = %q, %v", out, err)
	}
	// 呼叫有記錄（NFR-TEST 的審計用）。
	calls, err := ReadCalls(s.root)
	if err != nil || len(calls) != 1 || !strings.Contains(strings.Join(calls[0].Argv, " "), "--flat-playlist") {
		t.Fatalf("calls = %+v, %v", calls, err)
	}
}

func TestFlatPlaylistUnknownOrBrokenFixture(t *testing.T) {
	s := newScenario(t, playlistState(t))
	// 沒有登記：與真實的 yt-dlp 相同，回報播放清單不存在（結束碼 1）。
	code, _, errs := s.exec(YtDlp, plArgs("https://www.youtube.com/playlist?list=PL999")...)
	if code != 1 || !strings.Contains(errs, "ERROR: [youtube:tab] PL999: The playlist does not exist.") {
		t.Fatalf("code=%d stderr=%q", code, errs)
	}
	// 登記了但 fixture 檔不存在：明確回報檔名，不是靜默的空輸出。
	code, out, errs := s.exec(YtDlp, plArgs("https://www.youtube.com/playlist?list=PLGONE")...)
	if code == 0 || out != "" || !strings.Contains(errs, "missing.json") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out, errs)
	}
	// 不是播放清單的網址。
	code, _, errs = s.exec(YtDlp, plArgs("https://www.youtube.com/watch?v=abc123xxxxx")...)
	if code != 1 || errs == "" {
		t.Fatalf("code=%d stderr=%q", code, errs)
	}
}

// --flat-playlist 單獨出現（沒有 -J）不是 production 會用的組合：維持「未知組合」的 usage error。
func TestFlatPlaylistWithoutDumpJSONIsUsageError(t *testing.T) {
	s := newScenario(t, playlistState(t))
	code, _, _ := s.exec(YtDlp, "--flat-playlist", "--", "https://www.youtube.com/playlist?list=PL123")
	if code != 2 {
		t.Fatalf("code=%d", code)
	}
}

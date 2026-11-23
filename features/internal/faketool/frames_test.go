package faketool

import (
	"encoding/json"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/features/internal/fakeframes"
)

// 關鍵畫面（FRM）：-J 的影像格式、影片下載、ffprobe 的尺寸、場景偵測與擷取。
func TestFramesRoundTrip(t *testing.T) {
	st := sampleState(t)
	red, _ := fakeframes.ParseColor("#ff0000")
	st.Videos["abc123xxxxx"].Scenes = []fakeframes.Scene{{At: 5, Score: 0.9, Color: red}, {At: 40, Score: 0.2, Color: red}}
	st.Videos["abc123xxxxx"].BestFormat = "3840x2160"
	s := newScenario(t, st)
	url := "https://www.youtube.com/watch?v=abc123xxxxx"

	code, out, errs := s.inproc("yt-dlp", "-J", url)
	if code != 0 || !strings.Contains(out, `"width":3840`) || !strings.Contains(out, `"width":1920`) {
		t.Fatalf("-J = %d %s %s", code, out, errs)
	}
	if code, _, _ := s.inproc("yt-dlp", "-f", "bv*[height<=2160]", "-o", "v.%(ext)s", url); code != 2 {
		t.Errorf("契約以外的格式選擇要結束碼 2，得到 %d", code)
	}
	code, _, errs = s.inproc("yt-dlp", "-f", videoFormatContract, "-o", filepath.Join(s.work, "video.%(ext)s"), "--", url)
	if code != 0 {
		t.Fatalf("download = %d %s", code, errs)
	}
	in := filepath.Join(s.work, "video.mp4")
	code, out, _ = s.inproc("ffprobe", "-v", "error", "-protocol_whitelist", "file", "-format_whitelist", testFW, "-of", "json", "-show_streams", in)
	var pj struct {
		Streams []map[string]any `json:"streams"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &pj) != nil || len(pj.Streams) != 1 || pj.Streams[0]["width"] != float64(1920) {
		t.Fatalf("ffprobe = %d %s", code, out)
	}

	base := []string{"-nostdin", "-hide_banner", "-nostats", "-v", "info", "-protocol_whitelist", "file", "-format_whitelist", testFW, "-i", in, "-an", "-sn", "-dn"}
	code, _, errs = s.inproc("ffmpeg", append(base, "-vf", "select='gt(scene,0.3000)',showinfo,metadata=print", "-f", "null", "-")...)
	if code != 0 || strings.Count(errs, "lavfi.scene_score=") != 1 || !strings.Contains(errs, "pts_time:5.000") {
		t.Fatalf("scenes = %d %s", code, errs)
	}
	out1 := filepath.Join(s.work, "f.jpg")
	frame := []string{"-nostdin", "-hide_banner", "-v", "error", "-protocol_whitelist", "file", "-format_whitelist", testFW,
		"-ss", "6.000", "-i", in, "-an", "-sn", "-dn", "-frames:v", "1", "-vf", "scale=64:36", "-q:v", "2", "-f", "mjpeg", "-y", out1}
	if code, _, errs := s.inproc("ffmpeg", frame...); code != 0 {
		t.Fatalf("frame = %d %s", code, errs)
	}
	f, err := os.Open(out1)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := jpeg.Decode(f)
	if err != nil || img.Bounds().Dx() != 64 {
		t.Fatalf("frame image %v %v", img, err)
	}
	// -ss 在 -i 之後、相對路徑的輸出都違反契約。
	bad := append([]string(nil), frame...)
	bad[len(bad)-1] = "f.jpg"
	if code, _, _ := s.inproc("ffmpeg", bad...); code != 2 {
		t.Errorf("相對路徑的輸出要結束碼 2，得到 %d", code)
	}
}

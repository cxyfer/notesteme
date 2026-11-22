package fakeframes

import (
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func video(t *testing.T) Video {
	t.Helper()
	mk := func(at, score float64, hex string) Scene {
		c, err := ParseColor(hex)
		if err != nil {
			t.Fatal(err)
		}
		return Scene{At: at, Score: score, Color: c}
	}
	return NewVideo([]Scene{mk(60, 0.1, "#ff0000"), mk(1, 0.9, "#101010"), mk(120, 0.5, "#00ff00")})
}

func TestShowinfoOnlyListsScenesAboveThreshold(t *testing.T) {
	log := video(t).ShowinfoLog(0.3)
	if strings.Count(log, "lavfi.scene_score=") != 2 {
		t.Fatalf("log = %s", log)
	}
	if strings.Contains(log, "pts_time:60.000") || !strings.Contains(log, "pts_time:1.000") || !strings.Contains(log, "pts_time:120.000") {
		t.Errorf("log = %s", log)
	}
}

func TestColorAtUsesLastSceneNotAfter(t *testing.T) {
	v := video(t)
	if c := v.ColorAt(0.5); c.R != 0 || c.G != 0 || c.B != 0 {
		t.Errorf("片頭 = %v，應是黑色", c)
	}
	if c := v.ColorAt(61); c.R != 0xff || c.G != 0 {
		t.Errorf("61 秒 = %v", c)
	}
	if c := v.ColorAt(500); c.G != 0xff {
		t.Errorf("片尾 = %v", c)
	}
}

func TestWriteFrameIsSolidJPEG(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f.jpg")
	if err := video(t).WriteFrame(p, 61, 64, 36); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := jpeg.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 64 || b.Dy() != 36 {
		t.Errorf("尺寸 = %v", b)
	}
	r, g, _, _ := img.At(10, 10).RGBA()
	if r>>8 < 0xf0 || g>>8 > 0x10 {
		t.Errorf("顏色 = %d,%d", r>>8, g>>8)
	}
}

func TestFitWithinNeverUpscales(t *testing.T) {
	cases := [][4]int{{3840, 2160, 1920, 1080}, {1920, 1080, 1920, 1080}, {1280, 720, 1280, 720}, {1080, 1920, 606, 1080}}
	for _, c := range cases {
		if w, h := FitWithin(c[0], c[1], 1920, 1080); w != c[2] || h != c[3] {
			t.Errorf("FitWithin(%d,%d) = %d,%d，預期 %d,%d", c[0], c[1], w, h, c[2], c[3])
		}
	}
	if _, err := ParseColor("red"); err == nil {
		t.Error("不合法的顏色要回錯誤")
	}
}

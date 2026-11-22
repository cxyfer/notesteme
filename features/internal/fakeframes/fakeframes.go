// Package fakeframes 是驗收測試中假 ffmpeg 的「畫面」部分（FRM，M3）。
//
// 場景用 `Given the video of … has the scenes:` 描述一部影片的畫面：每一列是時間、場景分數與顏色。
// 這個套件提供兩件事，接進 faketool 的 ffmpeg 由 M3 的實作切片負責：
//
//   - ShowinfoLog：場景偵測（`select='gt(scene,T)',showinfo`）的 stderr，只列出分數大於 T 的列，
//     格式模仿 ffmpeg 的 showinfo 與 `lavfi.scene_score` metadata。
//   - WriteFrame：任何時間點擷取的畫面都是「時間不晚於它的最後一列」的純色 JPEG（第一列之前是黑色），
//     所以去重（FRM-R3）與密度下限（FRM-R2）的結果可以預測。
package fakeframes

import (
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Scene 是影片中的一個畫面變化。
type Scene struct {
	At    float64 // 秒
	Score float64 // 0～1
	Color color.RGBA
}

// Video 是一部影片的畫面描述，Scenes 依時間排序。
type Video struct {
	Scenes []Scene
}

// NewVideo 依時間排序後建立 Video。
func NewVideo(scenes []Scene) Video {
	s := append([]Scene(nil), scenes...)
	sort.SliceStable(s, func(i, j int) bool { return s[i].At < s[j].At })
	return Video{Scenes: s}
}

// ParseColor 解析 `#rrggbb`。
func ParseColor(hex string) (color.RGBA, error) {
	h, ok := strings.CutPrefix(hex, "#")
	if !ok || len(h) != 6 {
		return color.RGBA{}, fmt.Errorf("顏色 %q 應是 #rrggbb", hex)
	}
	v, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return color.RGBA{}, fmt.Errorf("顏色 %q：%w", hex, err)
	}
	return color.RGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 255}, nil
}

// ColorAt 回傳時間 t（秒）的畫面顏色：時間不晚於 t 的最後一列，第一列之前是黑色。
func (v Video) ColorAt(t float64) color.RGBA {
	c := color.RGBA{A: 255}
	for _, s := range v.Scenes {
		if s.At > t {
			break
		}
		c = s.Color
	}
	return c
}

// ShowinfoLog 產生場景偵測的 stderr：分數大於 threshold 的列，依時間排序。
func (v Video) ShowinfoLog(threshold float64) string {
	var b strings.Builder
	n := 0
	for _, s := range v.Scenes {
		if s.Score <= threshold {
			continue
		}
		fmt.Fprintf(&b, "[Parsed_showinfo_1 @ 0x0] n:%4d pts:%8d pts_time:%.3f duration:1 fmt:yuv420p\n", n, int(s.At*1000), s.At)
		fmt.Fprintf(&b, "[Parsed_metadata_2 @ 0x0] frame:%d pts:%d pts_time:%.3f\n", n, int(s.At*1000), s.At)
		fmt.Fprintf(&b, "[Parsed_metadata_2 @ 0x0] lavfi.scene_score=%.6f\n", s.Score)
		n++
	}
	return b.String()
}

// WriteFrame 把時間 t 的畫面寫成 w×h 的純色 JPEG。
func (v Video) WriteFrame(path string, t float64, w, h int) error {
	if w <= 0 || h <= 0 {
		return fmt.Errorf("畫面尺寸 %dx%d 不正確", w, h)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	c := v.ColorAt(t)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 90}); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// FitWithin 回傳把 srcW×srcH 縮到不超過 maxW×maxH 的尺寸（保持比例，不放大，寬高為偶數），對應 FRM-R8。
func FitWithin(srcW, srcH, maxW, maxH int) (int, int) {
	if srcW <= maxW && srcH <= maxH {
		return srcW, srcH
	}
	scale := min(float64(maxW)/float64(srcW), float64(maxH)/float64(srcH))
	w, h := int(float64(srcW)*scale), int(float64(srcH)*scale)
	return w - w%2, h - h%2
}

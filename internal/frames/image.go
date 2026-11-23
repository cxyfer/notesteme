package frames

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"io"
)

// 去重縮圖的尺寸（FRM-R3）。
const (
	DedupeW = 32
	DedupeH = 18
)

// ThumbWidth 是 `size=thumb` 的寬度（FRM-R10）。
const ThumbWidth = 320

// MaxDecodePixels 是解碼擷取畫面時允許的最大像素數（略大於 1920×1080）：擷取的檔案理論上由 ffmpeg 產生，
// 但仍把它當成不可信的輸入，先讀標頭的尺寸再解碼，避免超大圖片吃光記憶體。
const MaxDecodePixels = 2200 * 1300

// ErrImageTooLarge 表示圖片的尺寸超過 MaxDecodePixels。
var ErrImageTooLarge = errors.New("圖片尺寸超過上限")

// DecodeJPEG 解碼 JPEG；先檢查尺寸，超過 MaxDecodePixels 時回傳 ErrImageTooLarge。
func DecodeJPEG(b []byte) (image.Image, error) {
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > MaxDecodePixels {
		return nil, fmt.Errorf("%dx%d：%w", cfg.Width, cfg.Height, ErrImageTooLarge)
	}
	return jpeg.Decode(bytes.NewReader(b))
}

// Signature 是去重用的 32×18 RGB 縮圖（每個像素三個 0～255 的值，box average）。
type Signature []uint8

// SignatureOf 把圖片縮成 DedupeW×DedupeH 的 RGB（區塊平均）。
func SignatureOf(img image.Image) Signature {
	b := img.Bounds()
	sig := make(Signature, 0, DedupeW*DedupeH*3)
	for y := 0; y < DedupeH; y++ {
		y0, y1 := b.Min.Y+y*b.Dy()/DedupeH, b.Min.Y+(y+1)*b.Dy()/DedupeH
		y1 = max(y1, y0+1)
		for x := 0; x < DedupeW; x++ {
			x0, x1 := b.Min.X+x*b.Dx()/DedupeW, b.Min.X+(x+1)*b.Dx()/DedupeW
			x1 = max(x1, x0+1)
			var r, g, bl, n uint64
			for yy := y0; yy < y1 && yy < b.Max.Y; yy++ {
				for xx := x0; xx < x1 && xx < b.Max.X; xx++ {
					cr, cg, cb, _ := img.At(xx, yy).RGBA()
					r, g, bl, n = r+uint64(cr>>8), g+uint64(cg>>8), bl+uint64(cb>>8), n+1
				}
			}
			if n == 0 {
				n = 1
			}
			sig = append(sig, uint8(r/n), uint8(g/n), uint8(bl/n))
		}
	}
	return sig
}

// Diff 回傳兩個縮圖三個色版的平均絕對差（0～1）；長度不同時視為完全不同（1）。
func Diff(a, b Signature) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 1
	}
	var sum int
	for i := range a {
		d := int(a[i]) - int(b[i])
		if d < 0 {
			d = -d
		}
		sum += d
	}
	return float64(sum) / float64(len(a)) / 255
}

// Dedupe 依序決定每個候選是否保留（FRM-R3）：和最近保留的 window 張比較，差異低於 threshold 就捨棄。
// 回傳保留的索引。
func Dedupe(sigs []Signature, window int, threshold float64) []int {
	window = max(window, 1)
	var kept []int
	for i, s := range sigs {
		dup := false
		for k := len(kept) - 1; k >= 0 && k >= len(kept)-window; k-- {
			if Diff(sigs[kept[k]], s) < threshold {
				dup = true
				break
			}
		}
		if !dup {
			kept = append(kept, i)
		}
	}
	return kept
}

// FitWithin 回傳把 w×h 縮到不超過 maxW×maxH 的尺寸：保持比例、不放大、寬高為偶數（FRM-R8）。
func FitWithin(w, h, maxW, maxH int) (int, int) {
	if w <= 0 || h <= 0 {
		return 0, 0
	}
	if w <= maxW && h <= maxH {
		return w, h
	}
	scale := min(float64(maxW)/float64(w), float64(maxH)/float64(h))
	nw, nh := int(float64(w)*scale), int(float64(h)*scale)
	return max(nw-nw%2, 2), max(nh-nh%2, 2)
}

// Resize 以最近鄰取樣把圖片縮放到 w×h（縮圖與拼圖用，不追求畫質）。
func Resize(src image.Image, w, h int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, max(w, 1), max(h, 1)))
	b := src.Bounds()
	for y := 0; y < dst.Bounds().Dy(); y++ {
		sy := b.Min.Y + y*b.Dy()/dst.Bounds().Dy()
		for x := 0; x < dst.Bounds().Dx(); x++ {
			sx := b.Min.X + x*b.Dx()/dst.Bounds().Dx()
			dst.Set(x, y, src.At(sx, sy))
		}
	}
	return dst
}

// Thumb 回傳寬 ThumbWidth（保持比例，不放大）的縮圖。
func Thumb(src image.Image) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w > ThumbWidth {
		h = max(h*ThumbWidth/w, 1)
		w = ThumbWidth
	}
	return Resize(src, w, h)
}

// EncodeJPEG 以固定品質編碼。
func EncodeJPEG(w io.Writer, img image.Image) error {
	return jpeg.Encode(w, img, &jpeg.Options{Quality: 85})
}

// 拼圖（FRM-R7）：每張最多 9 格（3×3），每格 SheetCellW×SheetCellH。
const (
	SheetSize  = 9
	sheetCols  = 3
	SheetCellW = 320
	SheetCellH = 180
)

// ContactSheet 把最多 9 張圖排成 3×3 的拼圖，每格左上角畫上編號（1～9）。
func ContactSheet(imgs []image.Image) *image.RGBA {
	rows := (min(len(imgs), SheetSize) + sheetCols - 1) / sheetCols
	sheet := image.NewRGBA(image.Rect(0, 0, sheetCols*SheetCellW, max(rows, 1)*SheetCellH))
	draw.Draw(sheet, sheet.Bounds(), image.NewUniform(color.Black), image.Point{}, draw.Src)
	for i, img := range imgs {
		if i >= SheetSize {
			break
		}
		x, y := (i%sheetCols)*SheetCellW, (i/sheetCols)*SheetCellH
		cell := Resize(img, SheetCellW-4, SheetCellH-4)
		draw.Draw(sheet, image.Rect(x+2, y+2, x+SheetCellW-2, y+SheetCellH-2), cell, image.Point{}, draw.Src)
		drawNumber(sheet, x+6, y+6, i+1)
	}
	return sheet
}

// digits 是 3×5 的數字點陣（1～9）。
var digits = map[int][5]string{
	1: {".#.", "##.", ".#.", ".#.", "###"},
	2: {"##.", "..#", ".#.", "#..", "###"},
	3: {"##.", "..#", ".#.", "..#", "##."},
	4: {"#.#", "#.#", "###", "..#", "..#"},
	5: {"###", "#..", "##.", "..#", "##."},
	6: {".##", "#..", "###", "#.#", "###"},
	7: {"###", "..#", ".#.", ".#.", ".#."},
	8: {"###", "#.#", "###", "#.#", "###"},
	9: {"###", "#.#", "###", "..#", "##."},
}

// drawNumber 在 (x, y) 畫一個白底黑字的編號標籤（每個點 4×4 像素）。
func drawNumber(img *image.RGBA, x, y, n int) {
	const px = 4
	draw.Draw(img, image.Rect(x, y, x+5*px, y+7*px), image.NewUniform(color.White), image.Point{}, draw.Src)
	pat := digits[n]
	for r, row := range pat {
		for c, ch := range row {
			if ch == '#' {
				x0, y0 := x+px+c*px, y+px+r*px
				draw.Draw(img, image.Rect(x0, y0, x0+px, y0+px), image.NewUniform(color.Black), image.Point{}, draw.Src)
			}
		}
	}
}

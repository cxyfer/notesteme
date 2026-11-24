package bundle

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io/fs"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// 安全修補 B 的第 1 項：圖片只做結構檢查，像素有單張與合計上限，合計壓縮比，可以取消。

func encodeJPEG(t testing.TB, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 1}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

var grayOnce sync.Map // side → []byte

// flatGray 回傳 side×side 的單色灰階 JPEG（快取，避免每個測試都重新編碼）。
func flatGray(t testing.TB, side int) []byte {
	if v, ok := grayOnce.Load(side); ok {
		return v.([]byte)
	}
	img := image.NewGray(image.Rect(0, 0, side, side))
	for i := range img.Pix {
		img.Pix[i] = 128
	}
	d := encodeJPEG(t, img)
	grayOnce.Store(side, d)
	return d
}

// imageBundle 組出一個字幕加上 n 張同樣圖片的 bundle（圖片沒有被產出引用，會被當成 JPEG 檢查）。
func imageBundle(t testing.TB, jpg []byte, n int) []byte {
	t.Helper()
	c := cues(t)
	tp := "artifacts/" + artID + ".json"
	files := map[string][]byte{tp: c}
	entries := []entrySpec{{name: "artifacts/", mode: fs.ModeDir | 0o755}, {name: tp, data: c, method: zip.Deflate}}
	for i := range n {
		name := fmt.Sprintf("artifacts/img-%04d.jpg", i)
		files[name] = jpg
		entries = append(entries, entrySpec{name: name, data: jpg, method: zip.Deflate})
	}
	m := manifest(files, transcriptArtifact())
	return build(t, append([]entrySpec{{name: ManifestName, data: mj(t, m), method: zip.Deflate}}, entries...)...)
}

func TestJPEGPixelBombIsCheap(t *testing.T) {
	big := flatGray(t, 8192) // 64M 像素，約 786 KB，deflate 後約 1 KB
	if _, ok := JPEGPixels(big); ok {
		t.Fatal("超過 16M 像素的 JPEG 要被拒絕")
	}
	// 剛好 16M 像素（4096×4096）可以接受；驗證不配置像素。
	edge := flatGray(t, 4096)
	runtime.GC()
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	start := time.Now()
	const n = 50
	for range n {
		if px, ok := JPEGPixels(edge); !ok || px != 4096*4096 {
			t.Fatalf("4096×4096 = %d, %v", px, ok)
		}
	}
	el := time.Since(start) / n
	runtime.ReadMemStats(&m1)
	perImage := (m1.TotalAlloc - m0.TotalAlloc) / n
	t.Logf("4096×4096（%d bytes）每張驗證 %v，配置 %d bytes", len(edge), el, perImage)
	if perImage > 1<<20 {
		t.Errorf("每張驗證配置 %d bytes，應遠小於解碼一張圖（16 MiB）", perImage)
	}
}

// 審查的重現：20 張 8192×8192 原本要 6 秒、1280 MiB。現在 Read 以合計壓縮比先擋下；
// 即使站台把 max_ratio 調得很高，ValidateContents 也在第一張以 JPEG 格式拒絕，而且不解碼。
func TestValidateContentsRejectsHugeImagesWithoutDecoding(t *testing.T) {
	data := imageBundle(t, flatGray(t, 8192), 20)
	if _, err := Read(data, Options{}); err == nil || !strings.Contains(err.Error(), "合計的壓縮比") {
		t.Fatalf("預設上限的 Read = %v", err)
	}
	b, err := Read(data, Options{Limits: Limits{MaxRatio: 1 << 20}})
	if err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	var m0, m1 runtime.MemStats
	runtime.ReadMemStats(&m0)
	start := time.Now()
	err = b.ValidateContents()
	el := time.Since(start)
	runtime.ReadMemStats(&m1)
	t.Logf("ValidateContents = %v（%v，配置 %d KiB）", err, el, (m1.TotalAlloc-m0.TotalAlloc)>>10)
	var e *Error
	if !errors.As(err, &e) || !strings.Contains(e.Message, "JPEG") || e.Entry != "artifacts/img-0000.jpg" {
		t.Fatalf("err = %v", err)
	}
	if a := m1.TotalAlloc - m0.TotalAlloc; a > 8<<20 {
		t.Errorf("配置了 %d bytes：不應解碼圖片", a)
	}
	// 16M 像素以內的 15 張（合計在 256M 像素的預算內）：通過，而且一樣不解碼。
	b, err = Read(imageBundle(t, flatGray(t, 4096), 15), Options{Limits: Limits{MaxRatio: 1 << 20}})
	if err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	runtime.ReadMemStats(&m0)
	start = time.Now()
	if err := b.ValidateContents(); err != nil {
		t.Fatal(err)
	}
	el = time.Since(start)
	runtime.ReadMemStats(&m1)
	t.Logf("15 張 4096×4096：ValidateContents %v，配置 %d KiB", el, (m1.TotalAlloc-m0.TotalAlloc)>>10)
	if a := m1.TotalAlloc - m0.TotalAlloc; a > 8<<20 {
		t.Errorf("配置了 %d bytes：不應解碼圖片", a)
	}
}

func TestValidateContentsPixelBudget(t *testing.T) {
	img := flatGray(t, 1024) // 1M 像素
	data := imageBundle(t, img, 5)
	b, err := Read(data, Options{Limits: Limits{MaxTotalPixels: 4 << 20}})
	if err != nil {
		t.Fatal(err)
	}
	err = b.ValidateContents()
	var e *Error
	if !errors.As(err, &e) || e.Entry != "artifacts/img-0004.jpg" || !strings.Contains(e.Message, "合計超過 4 百萬像素") {
		t.Fatalf("超過像素預算 = %v", err)
	}
	b, err = Read(data, Options{Limits: Limits{MaxTotalPixels: 5 << 20}})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.ValidateContents(); err != nil {
		t.Fatalf("剛好等於預算 = %v", err)
	}
	if DefaultLimits().MaxTotalPixels != 256<<20 {
		t.Errorf("預設像素預算 = %d", DefaultLimits().MaxTotalPixels)
	}
}

func TestValidateContentsHonoursContext(t *testing.T) {
	b, err := Read(imageBundle(t, jpegBytes(t), 3), Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.ValidateContentsContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("取消後 = %v", err)
	}
	if err := b.ValidateContentsContext(context.Background()); err != nil {
		t.Fatalf("有效的圖片 = %v", err)
	}
}

// 許多單檔小於 1 MiB、壓縮比很高的檔案：單檔不檢查壓縮比，合計要擋下。
func TestAggregateRatio(t *testing.T) {
	data := imageBundle(t, flatGray(t, 8192), 4) // 4 × 786 KB 解壓，壓縮後約 4 KB
	_, err := Read(data, Options{})
	var e *Error
	if !errors.As(err, &e) || !strings.Contains(e.Message, "合計的壓縮比超過 100 倍") || e.Entry != "artifacts/img-0001.jpg" {
		t.Fatalf("合計壓縮比 = %v", err)
	}
	// 單一大檔仍先回報單檔的壓縮比（BND 的 zip bomb 場景）。
	zeros := make([]byte, 2<<20)
	m := manifest(map[string][]byte{"artifacts/zeros.bin": zeros})
	_, err = Read(build(t, entrySpec{name: ManifestName, data: mj(t, m)}, entrySpec{name: "artifacts/zeros.bin", data: zeros, method: zip.Deflate}), Options{})
	if !errors.As(err, &e) || !strings.Contains(e.Message, "「artifacts/zeros.bin」壓縮比超過 100 倍") {
		t.Fatalf("單檔壓縮比 = %v", err)
	}
	// 一般的 bundle（不可壓縮的圖片）不受影響。
	noisy := image.NewRGBA(image.Rect(0, 0, 640, 360))
	for i := range noisy.Pix {
		noisy.Pix[i] = byte(i*7919 + i/13)
	}
	var nb bytes.Buffer
	if err := jpeg.Encode(&nb, noisy, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	b, err := Read(imageBundle(t, nb.Bytes(), 40), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.ValidateContents(); err != nil {
		t.Fatal(err)
	}
}

// segment 組出一個帶長度的 JPEG 標記段。
func segment(marker byte, payload []byte) []byte {
	l := len(payload) + 2
	return append([]byte{0xFF, marker, byte(l >> 8), byte(l)}, payload...)
}

func TestJPEGStructure(t *testing.T) {
	good := jpegBytes(t)
	ycc := encodeJPEG(t, image.NewYCbCr(image.Rect(0, 0, 33, 17), image.YCbCrSubsampleRatio420))
	for name, d := range map[string][]byte{"gray": good, "ycbcr": ycc, "rgba": encodeJPEG(t, image.NewRGBA(image.Rect(0, 0, 100, 50)))} {
		if _, ok := JPEGPixels(d); !ok {
			t.Errorf("%s：Go 編碼的 baseline JPEG 要通過", name)
		}
		if _, err := jpeg.Decode(bytes.NewReader(d)); err != nil {
			t.Fatal(err)
		}
	}
	sofAt := bytes.Index(good, []byte{0xFF, 0xC0})
	sosAt := bytes.Index(good, []byte{0xFF, 0xDA})
	if sofAt < 0 || sosAt < 0 {
		t.Fatal("找不到 SOF0／SOS")
	}
	mut := func(f func(d []byte) []byte) []byte { return f(append([]byte(nil), good...)) }
	insert := func(d []byte, at int, x []byte) []byte {
		out := append(append(append([]byte(nil), d[:at]...), x...), d[at:]...)
		return out
	}
	sosLen := int(good[sosAt+2])<<8 | int(good[sosAt+3])
	scanStart := sosAt + 2 + sosLen
	// DRI 與 RST：結構檢查要略過 scan 中的 RSTn、FF00 與填充的 0xFF。
	withRST := mut(func(d []byte) []byte {
		d = insert(d, sosAt, segment(0xDD, []byte{0, 1}))
		at := scanStart + 6 // DRI 段落有 6 bytes
		return insert(d, at, []byte{0xFF, 0xD0, 0xFF, 0x00, 0xFF, 0xD7})
	})
	withFill := mut(func(d []byte) []byte { return insert(d, len(d)-2, []byte{0xFF, 0xFF, 0xFF}) })
	withComment := mut(func(d []byte) []byte { return insert(d, 2, segment(0xFE, []byte("Lavc61"))) })
	for name, d := range map[string][]byte{"DRI＋RST": withRST, "EOI 前的填充": withFill, "COM": withComment} {
		if !jpegStructureOK(d) {
			t.Errorf("%s 要通過結構檢查", name)
		}
	}
	bad := map[string][]byte{
		"progressive（SOF2）": mut(func(d []byte) []byte { d[sofAt+1] = 0xC2; return d }),
		"算術編碼（SOF9）":        mut(func(d []byte) []byte { d[sofAt+1] = 0xC9; return d }),
		"EOI 之後還有資料":        append(append([]byte(nil), good...), 0x00),
		"兩張 JPEG 接在一起":      append(append([]byte(nil), good...), good...),
		"結尾不是 EOI":          good[:len(good)-2],
		"不是 SOI 開頭":         append([]byte{0x00}, good...),
		"空":                 {},
		"只有 SOI EOI":        {0xFF, 0xD8, 0xFF, 0xD9},
		"兩個 SOF":            mut(func(d []byte) []byte { return insert(d, sosAt, good[sofAt:sofAt+2+int(good[sofAt+3])]) }),
		"SOS 在 SOF 之前": mut(func(d []byte) []byte {
			return insert(d, 2, good[sosAt:scanStart])
		}),
		"scan 外的 RST": mut(func(d []byte) []byte { return insert(d, 2, []byte{0xFF, 0xD0}) }),
		"段落長度超出檔案":    mut(func(d []byte) []byte { return insert(d, 2, []byte{0xFF, 0xFE, 0xFF, 0xF0}) }),
		"段落長度小於 2":    mut(func(d []byte) []byte { return insert(d, 2, []byte{0xFF, 0xFE, 0x00, 0x01}) }),
		"標記之間不是 0xFF": mut(func(d []byte) []byte { return insert(d, 2, []byte{0x00}) }),
		"DNL":         mut(func(d []byte) []byte { return insert(d, 2, segment(0xDC, []byte{0, 1})) }),
		"五個 scan": mut(func(d []byte) []byte {
			scan := good[sosAt : len(good)-2]
			for range 4 {
				d = insert(d, len(d)-2, scan)
			}
			return d
		}),
	}
	for name, d := range bad {
		if _, ok := JPEGPixels(d); ok {
			t.Errorf("%s 要被拒絕", name)
		}
	}
	// 四個 scan 還可以（每個 component 一個）。
	four := mut(func(d []byte) []byte {
		scan := good[sosAt : len(good)-2]
		for range 3 {
			d = insert(d, len(d)-2, scan)
		}
		return d
	})
	if !jpegStructureOK(four) {
		t.Error("四個 scan 要通過結構檢查")
	}
}

// FuzzJPEGPixels：任何輸入都不能 panic；通過時 DecodeConfig 一定成功，尺寸在上限內，結尾是 EOI。
func FuzzJPEGPixels(f *testing.F) {
	f.Add(encodeJPEG(f, image.NewGray(image.Rect(0, 0, 4, 4))))
	f.Add(encodeJPEG(f, image.NewYCbCr(image.Rect(0, 0, 17, 9), image.YCbCrSubsampleRatio422)))
	f.Add(encodeJPEG(f, image.NewGray16(image.Rect(0, 0, 3, 70))))
	f.Add([]byte{0xFF, 0xD8, 0xFF, 0xD9})
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, d []byte) {
		px, ok := JPEGPixels(d)
		if !ok {
			return
		}
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(d))
		if err != nil || int64(cfg.Width)*int64(cfg.Height) != px || px <= 0 || px > MaxImagePixels {
			t.Fatalf("通過但 DecodeConfig = %+v, %v（px %d）", cfg, err, px)
		}
		if !bytes.HasSuffix(d, []byte{0xFF, 0xD9}) || !bytes.HasPrefix(d, []byte{0xFF, 0xD8}) {
			t.Fatal("通過但不是 SOI…EOI")
		}
	})
}

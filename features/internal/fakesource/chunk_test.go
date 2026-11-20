package fakesource

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/features/internal/faketool"
)

// uploadMarker 上傳任意標記（可帶 offset、streams、compressed），內容後面接 padding 個位元組。
func uploadMarker(t *testing.T, h http.Handler, m faketool.Media, padding int) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("response_format", "verbose_json")
	fw, _ := mw.CreateFormFile("file", "audio.ogg")
	_, _ = io.WriteString(fw, faketool.FormatMedia(m))
	_, _ = io.WriteString(fw, strings.Repeat("x", padding))
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/openai/v1/audio/transcriptions", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

type verbose struct {
	Language string  `json:"language"`
	Duration float64 `json:"duration"`
	Text     string  `json:"text"`
	Segments []struct {
		ID    int     `json:"id"`
		Start float64 `json:"start"`
		End   float64 `json:"end"`
		Text  string  `json:"text"`
	} `json:"segments"`
}

func decodeVerbose(t *testing.T, rec *httptest.ResponseRecorder) verbose {
	t.Helper()
	if rec.Code != 200 {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	var v verbose
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("%v\n%s", err, rec.Body.String())
	}
	return v
}

// 上傳的標記有 offset 時，只回傳完全落在 [offset, offset+duration] 內的 segments，時間減去 offset。
func TestOpenAIChunkOffsetFiltersSegments(t *testing.T) {
	s := New(fixturesDir(t))
	s.Returns("groq", "local:long-talk.m4a", "groq-two-chunks.json")
	h := s.OpenAIHandler("groq")

	// 沒有 offset：完整的轉錄（80 句，時間不變）。
	full := decodeVerbose(t, uploadMarker(t, h, faketool.Media{Item: "local:long-talk.m4a", Duration: 3004}, 0))
	if len(full.Segments) != 80 || full.Segments[40].Start != 1498 || full.Segments[79].End != 3004 {
		t.Fatalf("完整轉錄 = %d 句，#41=%+v", len(full.Segments), full.Segments[40])
	}

	// 第一塊 [0, 1507]：#1–#42，時間不變（offset=0 也要過濾）。
	first := decodeVerbose(t, uploadMarker(t, h, faketool.Media{Item: "local:long-talk.m4a", Duration: 1507, Offset: 0, HasOffset: true}, 0))
	if len(first.Segments) != 42 || first.Segments[41].Text != "so we remove the duplicate line" || first.Segments[41].End != 1506 {
		t.Fatalf("第一塊 = %d 句：%+v", len(first.Segments), first.Segments[len(first.Segments)-1])
	}
	if first.Language != "english" || first.Duration != 1507 {
		t.Errorf("第一塊 language=%q duration=%v", first.Language, first.Duration)
	}

	// 第二塊 [1497, 3004]：#41 起（1498 之前結束的都不在），時間減 1497。
	second := decodeVerbose(t, uploadMarker(t, h, faketool.Media{Item: "local:long-talk.m4a", Duration: 1507, Offset: 1497, HasOffset: true}, 0))
	if len(second.Segments) != 40 {
		t.Fatalf("第二塊 = %d 句", len(second.Segments))
	}
	if g := second.Segments[0]; g.ID != 0 || g.Start != 1 || g.End != 5 || g.Text != "and that is where the overlap starts" {
		t.Errorf("第二塊 #1 = %+v", g)
	}
	if g := second.Segments[1]; g.Start != 5 || g.End != 9 || g.Text != "so we remove the duplicate line" {
		t.Errorf("第二塊 #2 = %+v", g)
	}
	if last := second.Segments[39]; last.End != 1507 {
		t.Errorf("第二塊最後一句 = %+v", last)
	}
	if !strings.HasPrefix(second.Text, "and that is where the overlap starts so we remove") {
		t.Errorf("text = %q", second.Text)
	}

	// 跨出區間的 segment 不回傳：[1500, 1504] 內沒有任何完整的 segment（#41、#42 都跨出）。
	none := decodeVerbose(t, uploadMarker(t, h, faketool.Media{Item: "local:long-talk.m4a", Duration: 4, Offset: 1500, HasOffset: true}, 0))
	if len(none.Segments) != 0 {
		t.Errorf("跨出區間的 segment 不該回傳：%+v", none.Segments)
	}
}

// 上傳的是 ASR 前處理後的檔案（標記帶 streams、compressed、silences，內容是稀疏填充）時，仍能辨識 item。
func TestOpenAIAcceptsPreparedMarker(t *testing.T) {
	s := New(fixturesDir(t))
	s.Returns("groq", "local:meetup.mp4", "groq-no-language-ja.json")
	h := s.OpenAIHandler("groq")
	m := faketool.Media{
		Item: "local:meetup.mp4", Duration: 1800, Compressed: 41943040,
		Streams:  []faketool.MediaStream{{Type: "audio", Codec: "opus"}},
		Silences: []faketool.MediaSilence{{Start: 1, End: 2}},
	}
	v := decodeVerbose(t, uploadMarker(t, h, m, 100))
	if v.Language != "japanese" || len(v.Segments) != 3 {
		t.Fatalf("v = %+v", v)
	}
}

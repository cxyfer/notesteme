package faketool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// testFW 是 -format_whitelist 的清單（與 internal/media 相同，不含 hls、concat 等）。
const testFW = "mov,mp4,m4a,3gp,3g2,mj2,matroska,webm,mp3,ogg,wav,flac,aac,avi,mpegts,asf,srt,webvtt,ass,flv,mpeg,aiff,ac3,eac3,amr,caf,w64,wv"

func TestParseMedia(t *testing.T) {
	// v1：沒有 streams 等於一條音訊。
	m, ok := ParseMedia([]byte("NOTESTEME-FAKE-MEDIA item=local:a.mp3 duration=60\nxxxx"))
	if !ok || m.Item != "local:a.mp3" || m.Duration != 60 || m.HasOffset || m.Compressed != 0 || len(m.Streams) != 0 {
		t.Fatalf("v1 = %+v ok=%v", m, ok)
	}
	if st := m.EffectiveStreams(); len(st) != 1 || st[0].Type != StreamAudio {
		t.Fatalf("預設串流 = %+v", st)
	}

	// v2：全部欄位；fixture 含 /；lang 與 fixture 可省略。
	line := "NOTESTEME-FAKE-MEDIA item=local:lecture.mp4 duration=600.5 " +
		"streams=video/h264,audio/aac/eng,subtitle/mov_text/eng/embedded/lecture-en.srt,subtitle/hdmv_pgs_subtitle compressed=41943040 " +
		"silences=1501.8-1502.2,10-11 offset=0\n"
	m, ok = ParseMedia([]byte(line))
	if !ok {
		t.Fatal("v2 解析失敗")
	}
	want := Media{
		Item: "local:lecture.mp4", Duration: 600.5, Compressed: 41943040, HasOffset: true,
		Streams: []MediaStream{
			{Type: "video", Codec: "h264"},
			{Type: "audio", Codec: "aac", Lang: "eng"},
			{Type: "subtitle", Codec: "mov_text", Lang: "eng", Fixture: "embedded/lecture-en.srt"},
			{Type: "subtitle", Codec: "hdmv_pgs_subtitle"},
		},
		Silences: []MediaSilence{{1501.8, 1502.2}, {10, 11}},
	}
	if !reflect.DeepEqual(m, want) {
		t.Fatalf("v2 = %+v\n預期 %+v", m, want)
	}
	if !m.HasTextSubtitle() {
		t.Error("有文字字幕串流")
	}
	if (Media{Streams: []MediaStream{{Type: "subtitle", Codec: "hdmv_pgs_subtitle"}}}).HasTextSubtitle() {
		t.Error("點陣字幕不算文字字幕")
	}
	// 格式化後再解析得到同樣的結果（offset=0 要保留）。
	if m2, ok := ParseMedia([]byte(FormatMedia(m))); !ok || !reflect.DeepEqual(m2, m) {
		t.Fatalf("round trip = %+v ok=%v\n%s", m2, ok, FormatMedia(m))
	}

	// 沒有 item 的標記（media/ 的 fixture）：ParseMedia 成功，ParseMediaMarker 不成立。
	nofx := []byte("NOTESTEME-FAKE-MEDIA duration=3004 compressed=41943040 silences=1501.8-1502.2\n")
	if m, ok := ParseMedia(nofx); !ok || m.Item != "" || m.Compressed != 41943040 || len(m.Silences) != 1 {
		t.Fatalf("no item = %+v ok=%v", m, ok)
	}
	if _, _, ok := ParseMediaMarker(nofx); ok {
		t.Error("ParseMediaMarker 要求 item")
	}

	// 壞的標記。
	for _, bad := range []string{
		"", "hello\n", "NOTESTEME-FAKE-MEDIA item=x\n", "NOTESTEME-FAKE-MEDIA item=x duration=-1\n",
		"NOTESTEME-FAKE-MEDIA item=x duration=abc\n", "NOTESTEME-FAKE-MEDIA item=x duration=1 streams=movie/h264\n",
		"NOTESTEME-FAKE-MEDIA item=x duration=1 streams=audio\n", "NOTESTEME-FAKE-MEDIA item=x duration=1 silences=3\n",
		"NOTESTEME-FAKE-MEDIA item=x duration=1 silences=5-2\n", "NOTESTEME-FAKE-MEDIA item=x duration=1 compressed=-5\n",
		"NOTESTEME-FAKE-MEDIA item=x duration=1 offset=-1\n",
	} {
		if _, ok := ParseMedia([]byte(bad)); ok {
			t.Errorf("%q 應該解析失敗", bad)
		}
	}
}

func TestMediaMarkerWrapperIsBackwardCompatible(t *testing.T) {
	if got := MediaMarker("local:keynote.mp4", 60); got != "NOTESTEME-FAKE-MEDIA item=local:keynote.mp4 duration=60\n" {
		t.Fatalf("MediaMarker = %q", got)
	}
	item, dur, ok := ParseMediaMarker([]byte("NOTESTEME-FAKE-MEDIA item=abc123xxxxx duration=125\nxxx"))
	if !ok || item != "abc123xxxxx" || dur != 125 {
		t.Fatalf("wrapper = %q %d %v", item, dur, ok)
	}
	// 沒有 streams 的 FormatMedia 與 v1 的輸出完全相同。
	if got := FormatMedia(Media{Item: "abc123xxxxx", Duration: 125}); got != "NOTESTEME-FAKE-MEDIA item=abc123xxxxx duration=125\n" {
		t.Fatalf("FormatMedia = %q", got)
	}
}

// writeMarkerFile 在場景的工作目錄寫一個假媒體檔。
func (s *scenario) writeMarkerFile(name string, m Media) string {
	s.t.Helper()
	p := filepath.Join(s.work, name)
	if err := os.WriteFile(p, []byte(FormatMedia(m)), 0o644); err != nil {
		s.t.Fatal(err)
	}
	return p
}

// ffprobeArgs 是契約的 ffprobe 參數。
func ffprobeArgs(path string) []string {
	return []string{"-v", "error", "-hide_banner", "-protocol_whitelist", "file", "-format_whitelist", testFW,
		"-show_format", "-show_streams", "-of", "json", path}
}

func TestFFprobeStreams(t *testing.T) {
	s := newScenario(t, State{})
	p := s.writeMarkerFile("lecture.mp4", Media{
		Item: "local:lecture.mp4", Duration: 600,
		Streams: []MediaStream{
			{Type: "video", Codec: "h264"},
			{Type: "audio", Codec: "aac", Lang: "eng"},
			{Type: "audio", Codec: "aac"},
			{Type: "subtitle", Codec: "mov_text", Lang: "eng", Fixture: "embedded/lecture-en.srt"},
		},
	})
	code, out, errs := s.exec("ffprobe", ffprobeArgs(p)...)
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errs)
	}
	var res struct {
		Format struct {
			Duration   string `json:"duration"`
			FormatName string `json:"format_name"`
			NbStreams  int    `json:"nb_streams"`
		} `json:"format"`
		Streams []struct {
			Index       int               `json:"index"`
			CodecType   string            `json:"codec_type"`
			CodecName   string            `json:"codec_name"`
			Tags        map[string]string `json:"tags"`
			Disposition map[string]int    `json:"disposition"`
		} `json:"streams"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if res.Format.Duration != "600.000000" || res.Format.NbStreams != 4 || !strings.Contains(res.Format.FormatName, "mp4") {
		t.Fatalf("format = %+v", res.Format)
	}
	if len(res.Streams) != 4 {
		t.Fatalf("streams = %+v", res.Streams)
	}
	type row struct {
		typ, codec, lang string
		def              int
	}
	var got []row
	for i, st := range res.Streams {
		if st.Index != i {
			t.Errorf("index = %d，預期 %d", st.Index, i)
		}
		got = append(got, row{st.CodecType, st.CodecName, st.Tags["language"], st.Disposition["default"]})
		if st.Disposition["forced"] != 0 || st.Disposition["attached_pic"] != 0 {
			t.Errorf("disposition = %v", st.Disposition)
		}
	}
	want := []row{{"video", "h264", "", 1}, {"audio", "aac", "eng", 1}, {"audio", "aac", "", 0}, {"subtitle", "mov_text", "eng", 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("streams = %v\n預期 %v", got, want)
	}
	// 沒有 -show_streams 時不輸出 streams。
	args := []string{"-v", "error", "-protocol_whitelist", "file", "-format_whitelist", testFW, "-show_format", "-of", "json", p}
	if _, out, _ := s.exec("ffprobe", args...); strings.Contains(out, `"streams"`) {
		t.Errorf("不該有 streams：%s", out)
	}
	// 非標記檔：exit 1，stderr 與真實 ffprobe 相近。
	bad := filepath.Join(s.work, "notes.pdf")
	_ = os.WriteFile(bad, []byte("just text"), 0o644)
	if code, _, errs := s.exec("ffprobe", ffprobeArgs(bad)...); code != 1 || !strings.Contains(errs, "Invalid data found when processing input") {
		t.Fatalf("code=%d stderr=%q", code, errs)
	}
}

func TestFFprobeRequiresWhitelists(t *testing.T) {
	s := newScenario(t, State{})
	p := s.writeMarkerFile("a.mp3", Media{Item: "x", Duration: 1})
	for name, args := range map[string][]string{
		"沒有 protocol_whitelist": {"-format_whitelist", testFW, "-of", "json", p},
		"沒有 format_whitelist":   {"-protocol_whitelist", "file", "-of", "json", p},
		"protocol 不是 file":      {"-protocol_whitelist", "file,http", "-format_whitelist", testFW, "-of", "json", p},
		"format 含 hls":          {"-protocol_whitelist", "file", "-format_whitelist", "mp4,hls", "-of", "json", p},
		"format 含 concat":       {"-protocol_whitelist", "file", "-format_whitelist", "concat", "-of", "json", p},
		"未知旗標":                  {"-protocol_whitelist", "file", "-format_whitelist", testFW, "-bogus", "-of", "json", p},
	} {
		if code, _, errs := s.exec("ffprobe", args...); code != 2 {
			t.Errorf("%s：code=%d stderr=%q，預期 2", name, code, errs)
		}
	}
}

// ffmpegASR 是契約的 ASR 前處理參數；chunk 非 nil 時加上 -ss／-t（在 -i 之前）。
func ffmpegASR(in, out string, chunk ...string) []string {
	args := []string{"-nostdin", "-hide_banner", "-v", "error", "-protocol_whitelist", "file", "-format_whitelist", testFW}
	args = append(args, chunk...)
	return append(args, "-i", in, "-vn", "-sn", "-dn", "-map", "0:a:0", "-ac", "1", "-ar", "16000",
		"-c:a", "libopus", "-b:a", "32k", "-application", "voip", "-f", "ogg", "-y", out)
}

func readMarkerAndSize(t *testing.T, p string) (Media, int64) {
	t.Helper()
	m, size, ok, err := readMedia(p)
	if err != nil || !ok {
		t.Fatalf("%s：ok=%v err=%v", p, ok, err)
	}
	return m, size
}

func TestFFmpegTranscodeSize(t *testing.T) {
	s := newScenario(t, State{})
	in := s.writeMarkerFile("long.m4a", Media{
		Item: "local:long-talk.m4a", Duration: 3004, Compressed: 41943040,
		Silences: []MediaSilence{{1501.8, 1502.2}},
		Streams:  []MediaStream{{Type: "video", Codec: "h264"}, {Type: "audio", Codec: "aac"}},
	})
	out := filepath.Join(s.work, "tmp", "prepared.ogg")
	if code, _, errs := s.exec("ffmpeg", ffmpegASR(in, out)...); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errs)
	}
	m, size := readMarkerAndSize(t, out)
	if size != 41943040 {
		t.Errorf("大小 = %d，預期 compressed 41943040", size)
	}
	if m.Item != "local:long-talk.m4a" || m.Duration != 3004 || m.HasOffset || !reflect.DeepEqual(m.Silences, []MediaSilence{{1501.8, 1502.2}}) {
		t.Errorf("輸出標記 = %+v", m)
	}
	// 輸出只剩一條音訊（-vn -sn -dn）。
	if st := m.EffectiveStreams(); len(st) != 1 || st[0].Type != StreamAudio {
		t.Errorf("輸出串流 = %+v", st)
	}
	// 實際上是稀疏檔：標記仍在開頭。
	if b, _ := os.ReadFile(out); !strings.HasPrefix(string(b), "NOTESTEME-FAKE-MEDIA ") {
		t.Errorf("開頭不是標記")
	}

	// 沒有 compressed：輸出只有標記。
	small := s.writeMarkerFile("small.mp3", Media{Item: "local:s.mp3", Duration: 5})
	out2 := filepath.Join(s.work, "small.ogg")
	if code, _, errs := s.exec("ffmpeg", ffmpegASR(small, out2)...); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errs)
	}
	if m, size := readMarkerAndSize(t, out2); size != int64(len(FormatMedia(m))) {
		t.Errorf("大小 = %d", size)
	}

	// 沒有音訊串流：失敗。
	vonly := s.writeMarkerFile("v.mp4", Media{Item: "x", Duration: 5, Streams: []MediaStream{{Type: "video", Codec: "h264"}}})
	if code, _, errs := s.exec("ffmpeg", ffmpegASR(vonly, filepath.Join(s.work, "v.ogg"))...); code != 1 || !strings.Contains(errs, "matches no streams") {
		t.Errorf("code=%d stderr=%q", code, errs)
	}
	// 輸入不存在、不是媒體：失敗（1），不是用法錯誤。
	if code, _, _ := s.exec("ffmpeg", ffmpegASR(filepath.Join(s.work, "nope.mp3"), out2)...); code != 1 {
		t.Errorf("不存在：code=%d", code)
	}
	bad := filepath.Join(s.work, "bad.bin")
	_ = os.WriteFile(bad, []byte("garbage"), 0o644)
	if code, _, errs := s.exec("ffmpeg", ffmpegASR(bad, out2)...); code != 1 || !strings.Contains(errs, "Invalid data") {
		t.Errorf("不是媒體：code=%d stderr=%q", code, errs)
	}
}

func TestFFmpegChunkOffset(t *testing.T) {
	s := newScenario(t, State{})
	in := s.writeMarkerFile("long.m4a", Media{Item: "local:long-talk.m4a", Duration: 3004, Compressed: 41943040})
	for _, tc := range []struct {
		ss, t      string
		offset     float64
		dur        float64
		wantApprox int64
	}{
		{"0.000", "1507.000", 0, 1507, 21043000},
		{"1497.000", "1507.000", 1497, 1507, 21043000},
		{"2900.000", "500.000", 2900, 104, 1452000}, // 超過檔尾時截到 duration
	} {
		out := filepath.Join(s.work, "chunk.ogg")
		args := ffmpegASR(in, out, "-ss", tc.ss, "-t", tc.t)
		if code, _, errs := s.exec("ffmpeg", args...); code != 0 {
			t.Fatalf("%s：code=%d stderr=%q", tc.ss, code, errs)
		}
		m, size := readMarkerAndSize(t, out)
		if !m.HasOffset || m.Offset != tc.offset || m.Duration != tc.dur || m.Item != "local:long-talk.m4a" {
			t.Errorf("%s：標記 = %+v", tc.ss, m)
		}
		if diff := size - tc.wantApprox; diff < -50000 || diff > 50000 {
			t.Errorf("%s：大小 = %d，預期約 %d（依時長比例）", tc.ss, size, tc.wantApprox)
		}
	}
	// -ss 在 -i 之後、只有 -ss 沒有 -t：用法錯誤。
	args := ffmpegASR(in, filepath.Join(s.work, "x.ogg"))
	late := append(append([]string{}, args...), "-ss", "5", "-t", "5")
	if code, _, _ := s.exec("ffmpeg", late...); code != 2 {
		t.Errorf("-ss 不在 -i 之前：code=%d", code)
	}
	if code, _, _ := s.exec("ffmpeg", ffmpegASR(in, filepath.Join(s.work, "x.ogg"), "-ss", "5")...); code != 2 {
		t.Errorf("只有 -ss：code=%d", code)
	}
	// 起點超過檔尾：失敗（1）。
	if code, _, _ := s.exec("ffmpeg", ffmpegASR(in, filepath.Join(s.work, "x.ogg"), "-ss", "4000", "-t", "10")...); code != 1 {
		t.Errorf("起點超過檔尾：code=%d", code)
	}
}

// ffmpegSilenceArgs 是契約的靜音偵測參數。
func ffmpegSilenceArgs(in string, from, length string) []string {
	return []string{"-nostdin", "-hide_banner", "-nostats", "-v", "info", "-protocol_whitelist", "file", "-format_whitelist", testFW,
		"-ss", from, "-t", length, "-i", in, "-af", "silencedetect=noise=-35dB:d=0.3", "-f", "null", "-"}
}

func TestFFmpegSilencedetect(t *testing.T) {
	s := newScenario(t, State{})
	in := s.writeMarkerFile("prepared.ogg", Media{Item: "x", Duration: 3004, Silences: []MediaSilence{{1501.8, 1502.2}, {100, 100.5}, {3000, 3010}}})
	code, stdout, errs := s.exec("ffmpeg", ffmpegSilenceArgs(in, "1472.000", "60.000")...)
	if code != 0 || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, errs)
	}
	// 時間相對視窗起點：1501.8 - 1472 = 29.8。
	re := `\[silencedetect @ 0x[0-9a-f]+\] silence_start: 29\.8\n\[silencedetect @ 0x[0-9a-f]+\] silence_end: 30\.2 \| silence_duration: 0\.4\n`
	if !regexp.MustCompile(re).MatchString(errs) {
		t.Errorf("stderr 格式不符：%q", errs)
	}
	if strings.Contains(errs, "100") {
		t.Errorf("視窗外的靜音不該出現：%q", errs)
	}
	// 視窗起點之前就開始的靜音：起點截在 0；超過檔尾的靜音截在檔尾。
	_, _, errs = s.exec("ffmpeg", ffmpegSilenceArgs(in, "100.250", "30.000")...)
	if !strings.Contains(errs, "silence_start: 0\n") || !strings.Contains(errs, "silence_end: 0.25 | silence_duration: 0.25") {
		t.Errorf("跨視窗起點：%q", errs)
	}
	_, _, errs = s.exec("ffmpeg", ffmpegSilenceArgs(in, "2990.000", "60.000")...)
	if !strings.Contains(errs, "silence_start: 10\n") || !strings.Contains(errs, "silence_end: 14 | silence_duration: 4") {
		t.Errorf("超過檔尾：%q", errs)
	}
	// 視窗內沒有靜音：沒有 silence_ 行。
	if _, _, errs := s.exec("ffmpeg", ffmpegSilenceArgs(in, "500.000", "60.000")...); strings.Contains(errs, "silence_") {
		t.Errorf("不該有靜音：%q", errs)
	}
	// 靜音偵測也必須帶 -format_whitelist，而且不含危險格式。
	noFW := slices.Delete(ffmpegSilenceArgs(in, "0", "60"), 7, 9)
	if code, _, _ := s.exec("ffmpeg", noFW...); code != 2 {
		t.Errorf("缺少 -format_whitelist：code=%d", code)
	}
	badFW := ffmpegSilenceArgs(in, "0", "60")
	badFW[8] =testFW + ",lavfi"
	if code, _, _ := s.exec("ffmpeg", badFW...); code != 2 {
		t.Errorf("白名單含 lavfi：code=%d", code)
	}
	// 輸出不是 -、缺少 -ss：用法錯誤。
	bad := ffmpegSilenceArgs(in, "0", "60")
	bad[len(bad)-1] = filepath.Join(s.work, "out.null")
	if code, _, _ := s.exec("ffmpeg", bad...); code != 2 {
		t.Errorf("輸出不是 -：code=%d", code)
	}
	noSS := []string{"-nostdin", "-hide_banner", "-nostats", "-v", "info", "-protocol_whitelist", "file", "-i", in,
		"-af", "silencedetect=noise=-35dB:d=0.3", "-f", "null", "-"}
	if code, _, _ := s.exec("ffmpeg", noSS...); code != 2 {
		t.Errorf("沒有 -ss：code=%d", code)
	}
	// 門檻參數不同：用法錯誤（argv 逐字檢查）。
	wrongAF := ffmpegSilenceArgs(in, "0", "60")
	for i, a := range wrongAF {
		if strings.HasPrefix(a, "silencedetect=") {
			wrongAF[i] = "silencedetect=noise=-30dB:d=0.5"
		}
	}
	if code, _, _ := s.exec("ffmpeg", wrongAF...); code != 2 {
		t.Errorf("silencedetect 參數不同：code=%d", code)
	}
}

// ffmpegSubArgs 是契約的抽字幕參數。
func ffmpegSubArgs(in, mapSpec, out string) []string {
	return []string{"-nostdin", "-hide_banner", "-v", "error", "-protocol_whitelist", "file", "-format_whitelist", testFW,
		"-i", in, "-map", mapSpec, "-c:s", "srt", "-f", "srt", "-y", out}
}

func TestFFmpegExtractSubtitle(t *testing.T) {
	fx, err := filepath.Abs(filepath.Join("..", "..", "fixtures"))
	if err != nil {
		t.Fatal(err)
	}
	s := newScenario(t, State{FixturesDir: fx})
	in := s.writeMarkerFile("movie.mkv", Media{
		Item: "local:movie.mkv", Duration: 600,
		Streams: []MediaStream{
			{Type: "video", Codec: "h264"},
			{Type: "audio", Codec: "aac"},
			{Type: "subtitle", Codec: "subrip", Lang: "eng", Fixture: "embedded/movie-en.srt"},
			{Type: "subtitle", Codec: "hdmv_pgs_subtitle", Lang: "jpn"},
		},
	})
	out := filepath.Join(s.work, "sub", "out.srt")
	if code, _, errs := s.exec("ffmpeg", ffmpegSubArgs(in, "0:2", out)...); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errs)
	}
	got, _ := os.ReadFile(out)
	want, _ := os.ReadFile(filepath.Join(fx, "local", "embedded", "movie-en.srt"))
	if len(got) == 0 || string(got) != string(want) {
		t.Fatalf("輸出 = %q", got)
	}
	// 點陣字幕、非字幕串流、不存在的串流：失敗（1）。
	for _, idx := range []string{"0:3", "0:1", "0:9"} {
		if code, _, errs := s.exec("ffmpeg", ffmpegSubArgs(in, idx, filepath.Join(s.work, "o.srt"))...); code != 1 {
			t.Errorf("%s：code=%d stderr=%q", idx, code, errs)
		}
	}
	// -map 不是 0:<數字>、缺 -format_whitelist：用法錯誤。
	if code, _, _ := s.exec("ffmpeg", ffmpegSubArgs(in, "0:s:0", out)...); code != 2 {
		t.Errorf("0:s:0：code=%d", code)
	}
	noFW := []string{"-nostdin", "-hide_banner", "-v", "error", "-protocol_whitelist", "file", "-i", in, "-map", "0:2", "-c:s", "srt", "-f", "srt", "-y", out}
	if code, _, _ := s.exec("ffmpeg", noFW...); code != 2 {
		t.Errorf("沒有 -format_whitelist：code=%d", code)
	}
}

func TestFFmpegUnknownArgumentsExit2(t *testing.T) {
	s := newScenario(t, State{})
	in := s.writeMarkerFile("a.mp3", Media{Item: "x", Duration: 5})
	out := filepath.Join(s.work, "o.ogg")
	base := ffmpegASR(in, out)

	replace := func(args []string, flag, value string) []string {
		c := append([]string{}, args...)
		for i := range c {
			if c[i] == flag {
				c[i+1] = value
			}
		}
		return c
	}
	drop := func(args []string, flag string) []string {
		var c []string
		for i := 0; i < len(args); i++ {
			if args[i] == flag {
				if flag != "-nostdin" && flag != "-hide_banner" && flag != "-vn" && flag != "-sn" && flag != "-dn" && flag != "-y" {
					i++
				}
				continue
			}
			c = append(c, args[i])
		}
		return c
	}
	cases := map[string][]string{
		"沒有任何參數":                     {},
		"只有 -i":                      {"-i", in, out},
		"未知旗標":                       append([]string{"-frobnicate"}, base...),
		"protocol_whitelist 不是 file": replace(base, "-protocol_whitelist", "file,http"),
		"format_whitelist 含 hls":     replace(base, "-format_whitelist", "mp4,hls"),
		"format_whitelist 含 concat":  replace(base, "-format_whitelist", "concat"),
		"缺 -protocol_whitelist":      drop(base, "-protocol_whitelist"),
		"缺 -format_whitelist":        drop(base, "-format_whitelist"),
		"缺 -nostdin":                 drop(base, "-nostdin"),
		"缺 -vn":                      drop(base, "-vn"),
		"編碼器不同":                      replace(base, "-c:a", "aac"),
		"位元率不同":                      replace(base, "-b:a", "64k"),
		"取樣率不同":                      replace(base, "-ar", "44100"),
		"-map 不同":                    replace(base, "-map", "0:v:0"),
		"輸入重複":                       append(append([]string{}, base...), "-i", in),
		"多個輸出":                       append(append([]string{}, base...), "extra.ogg"),
		"-ss 不是數字":                   ffmpegASR(in, out, "-ss", "abc", "-t", "5"),
		"-ss 是負數":                    ffmpegASR(in, out, "-ss", "-1", "-t", "5"),
		"缺輸出":                        base[:len(base)-1],
	}
	for name, args := range cases {
		if code, _, errs := s.exec("ffmpeg", args...); code != 2 {
			t.Errorf("%s：code=%d stderr=%q，預期 2", name, code, errs)
		}
	}
	// 用法錯誤也要記在 calls.jsonl（每次呼叫都記）。
	calls, err := ReadCalls(s.root)
	if err != nil || len(calls) != len(cases) {
		t.Fatalf("calls = %d（預期 %d）err=%v", len(calls), len(cases), err)
	}
	if calls[0].Tool != "ffmpeg" {
		t.Errorf("call = %+v", calls[0])
	}
	// 契約的呼叫成功，且被記錄。
	if code, _, errs := s.exec("ffmpeg", base...); code != 0 {
		t.Fatalf("base：code=%d stderr=%q", code, errs)
	}
	calls, _ = ReadCalls(s.root)
	if last := calls[len(calls)-1]; !strings.Contains(strings.Join(last.Argv, " "), "-protocol_whitelist file") {
		t.Errorf("最後一次呼叫 = %+v", last)
	}
}

func TestYtDlpMediaMarkerCarriesAudioProperties(t *testing.T) {
	st := State{Videos: map[string]*Video{"abc123xxxxx": {
		Title: "T", Duration: 3004, Compressed: 41943040, Silences: []MediaSilence{{1501.8, 1502.2}},
	}}}
	s := newScenario(t, st)
	code, _, errs := s.exec("yt-dlp", "-f", "bestaudio", "-o", "media/%(id)s.%(ext)s",
		"--newline", "--no-progress", "--quiet", "https://www.youtube.com/watch?v=abc123xxxxx")
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, errs)
	}
	b, _ := os.ReadFile(filepath.Join(s.work, "media", "abc123xxxxx.m4a"))
	m, ok := ParseMedia(b)
	if !ok || m.Item != "abc123xxxxx" || m.Duration != 3004 || m.Compressed != 41943040 || len(m.Silences) != 1 {
		t.Fatalf("marker = %q → %+v", b, m)
	}
}

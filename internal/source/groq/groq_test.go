package groq

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/netguard"
	"github.com/cxyfer/notesteme/internal/subtitle"
)

const testKey = "gsk_test_SECRET_key_123"

const okBody = `{
  "language": "english", "duration": 12.5, "text": "Hello world. Bye.",
  "segments": [
    {"id": 0, "start": 0.0, "end": 3.2, "text": " Hello world.", "no_speech_prob": 0.01},
    {"id": 1, "start": 3.2, "end": 5.0, "text": "   "},
    {"id": 2, "start": 5.0, "end": 12.4, "text": " Bye."}
  ]
}`

func audioFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "audio.m4a")
	if err := os.WriteFile(p, []byte("FAKE-AUDIO-BYTES"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func newClient(srv *httptest.Server) *Client {
	return &Client{HTTP: srv.Client(), BaseURL: srv.URL, APIKey: testKey}
}

func respond(code int, body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(code)
		io.WriteString(w, body)
	}))
}

func classOf(t *testing.T, err error) errclass.Class {
	t.Helper()
	c, ok := errclass.ClassOf(err)
	if !ok {
		t.Fatalf("err = %#v，不是 *errclass.Error", err)
	}
	return c
}

func TestTranscribeSuccessAndMultipartFields(t *testing.T) {
	var gotAuth, gotPath, gotMethod string
	var fields map[string]string
	var fileName, fileBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath, gotMethod = r.Header.Get("Authorization"), r.URL.Path, r.Method
		mr, err := r.MultipartReader()
		if err != nil {
			t.Errorf("不是 multipart：%v", err)
			return
		}
		fields = map[string]string{}
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			b, _ := io.ReadAll(p)
			if p.FormName() == "file" {
				fileName, fileBody = p.FileName(), string(b)
			} else {
				fields[p.FormName()] = string(b)
			}
		}
		io.WriteString(w, okBody)
	}))
	defer srv.Close()

	res, err := newClient(srv).Transcribe(context.Background(), audioFile(t), "en")
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != http.MethodPost || gotPath != "/audio/transcriptions" {
		t.Errorf("請求 = %s %s", gotMethod, gotPath)
	}
	if gotAuth != "Bearer "+testKey {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if fields["model"] != DefaultModel || fields["response_format"] != "verbose_json" || fields["language"] != "en" {
		t.Errorf("fields = %v", fields)
	}
	if fileName != "audio.m4a" || fileBody != "FAKE-AUDIO-BYTES" {
		t.Errorf("file = %q %q", fileName, fileBody)
	}

	if res.Lang != "en" || res.DurationS != 12.5 {
		t.Errorf("res = %+v", res)
	}
	want := []subtitle.Cue{
		{Index: 1, StartMS: 0, EndMS: 3200, Text: "Hello world.", Timing: subtitle.TimingSource},
		{Index: 2, StartMS: 5000, EndMS: 12400, Text: "Bye.", Timing: subtitle.TimingSource},
	}
	if len(res.Cues) != len(want) {
		t.Fatalf("cues = %+v", res.Cues)
	}
	for i := range want {
		if res.Cues[i].Index != want[i].Index || res.Cues[i].StartMS != want[i].StartMS || res.Cues[i].EndMS != want[i].EndMS ||
			res.Cues[i].Text != want[i].Text || res.Cues[i].Timing != want[i].Timing {
			t.Errorf("cue[%d] = %+v，預期 %+v", i, res.Cues[i], want[i])
		}
	}
}

func TestTranscribeOmitsLanguageWithoutHintAndHonorsModel(t *testing.T) {
	var fields map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Error(err)
		}
		fields = map[string]string{}
		for k, v := range r.MultipartForm.Value {
			fields[k] = v[0]
		}
		io.WriteString(w, okBody)
	}))
	defer srv.Close()

	c := newClient(srv)
	c.Model = "whisper-large-v3"
	if _, err := c.Transcribe(context.Background(), audioFile(t), ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["language"]; ok {
		t.Errorf("沒有語言提示時不應送出 language：%v", fields)
	}
	if fields["model"] != "whisper-large-v3" {
		t.Errorf("model = %q", fields["model"])
	}
}

func TestTranscribeLanguageNames(t *testing.T) {
	for in, want := range map[string]string{
		"english": "en", "Japanese": "ja", "chinese": "zh", "cantonese": "yue", "korean": "ko",
		"en": "en", "zh-TW": "zh-TW", "JA": "ja", "klingon": "", "": "",
	} {
		body := `{"language":"` + in + `","segments":[{"start":0,"end":1,"text":"x"}]}`
		srv := respond(200, body)
		res, err := newClient(srv).Transcribe(context.Background(), audioFile(t), "")
		srv.Close()
		if err != nil {
			t.Fatalf("%q：%v", in, err)
		}
		if res.Lang != want {
			t.Errorf("language %q → %q，預期 %q", in, res.Lang, want)
		}
	}
}

func TestTranscribeDurationFallsBackToLastCue(t *testing.T) {
	srv := respond(200, `{"language":"english","segments":[{"start":0,"end":7.5,"text":"x"}]}`)
	defer srv.Close()
	res, err := newClient(srv).Transcribe(context.Background(), audioFile(t), "")
	if err != nil || res.DurationS != 7.5 {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
}

func TestTranscribeEmptyResult(t *testing.T) {
	for _, body := range []string{
		`{"language":"english","segments":[]}`,
		`{"language":"english","text":"hi"}`,
		`{"language":"english","segments":[{"start":0,"end":1,"text":"  "}]}`,
	} {
		srv := respond(200, body)
		_, err := newClient(srv).Transcribe(context.Background(), audioFile(t), "")
		srv.Close()
		if !errors.Is(err, ErrNoTranscript) {
			t.Errorf("%s：err = %v，預期 ErrNoTranscript", body, err)
		}
	}
}

func TestTranscribeStatusClasses(t *testing.T) {
	tests := []struct {
		code int
		want errclass.Class
	}{
		{401, errclass.ClassNeedsAction}, {403, errclass.ClassNeedsAction},
		{400, errclass.ClassPermanent}, {404, errclass.ClassPermanent}, {413, errclass.ClassPermanent}, {422, errclass.ClassPermanent},
		{302, errclass.ClassPermanent}, {204, errclass.ClassPermanent},
		{429, errclass.ClassRetryable}, {500, errclass.ClassRetryable}, {502, errclass.ClassRetryable}, {503, errclass.ClassRetryable},
	}
	for _, tt := range tests {
		srv := respond(tt.code, `{"error":{"message":"nope"}}`)
		_, err := newClient(srv).Transcribe(context.Background(), audioFile(t), "")
		srv.Close()
		if err == nil || classOf(t, err) != tt.want {
			t.Errorf("HTTP %d：err = %v，預期 %s", tt.code, err, tt.want)
		}
	}
}

func TestTranscribeKeyRejectedSentinel(t *testing.T) {
	srv := respond(401, `{}`)
	defer srv.Close()
	_, err := newClient(srv).Transcribe(context.Background(), audioFile(t), "")
	if !errors.Is(err, ErrKeyRejected) {
		t.Errorf("err = %v", err)
	}
	// 帶給使用者看的訊息與修正動作（SRC-R10）；key 被拒不是「沒有設定」，也不是「沒有結果」。
	var ce *errclass.Error
	if !errors.As(err, &ce) || ce.Message != MsgKeyRejected || len(ce.Actions) != 1 || ce.Actions[0] != ActionSetKey {
		t.Errorf("Message／Actions = %+v", ce)
	}
	if errors.Is(err, errclass.ErrNotConfigured) || errors.Is(err, errclass.ErrNoResult) {
		t.Errorf("key 被拒不該是沒有設定或沒有結果：%v", err)
	}
}

func TestTranscribeNoKeyDoesNotRequest(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer srv.Close()
	c := newClient(srv)
	c.APIKey = ""
	_, err := c.Transcribe(context.Background(), audioFile(t), "")
	if called || !errors.Is(err, ErrNoKey) || !errors.Is(err, errclass.ErrNotConfigured) || classOf(t, err) != errclass.ClassNeedsAction {
		t.Errorf("called = %v, err = %v", called, err)
	}
}

func TestTranscribeMissingAudioFileIsPermanent(t *testing.T) {
	srv := respond(200, okBody)
	defer srv.Close()
	_, err := newClient(srv).Transcribe(context.Background(), filepath.Join(t.TempDir(), "nope.m4a"), "")
	if err == nil || classOf(t, err) != errclass.ClassPermanent {
		t.Errorf("err = %v", err)
	}
}

func TestTranscribeBadJSONIsRetryable(t *testing.T) {
	srv := respond(200, `<html>oops`)
	defer srv.Close()
	_, err := newClient(srv).Transcribe(context.Background(), audioFile(t), "")
	if err == nil || classOf(t, err) != errclass.ClassRetryable {
		t.Errorf("err = %v", err)
	}
}

func TestTranscribeNetworkErrorIsRetryable(t *testing.T) {
	srv := respond(200, okBody)
	c := newClient(srv)
	srv.Close() // 連線被拒
	_, err := c.Transcribe(context.Background(), audioFile(t), "")
	if err == nil || classOf(t, err) != errclass.ClassRetryable {
		t.Errorf("err = %v", err)
	}
}

func TestTranscribeOversizeResponseIsPermanentAndNotTruncated(t *testing.T) {
	// 有 Content-Length 與串流（無 Content-Length）兩種情況，都要在超過上限時失敗。
	big := strings.Repeat("a", int(netguard.LimitFeed)+10)
	body := `{"language":"english","segments":[{"start":0,"end":1,"text":"` + big + `"}]}`
	for name, h := range map[string]http.HandlerFunc{
		"content-length": func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) },
		"chunked": func(w http.ResponseWriter, r *http.Request) {
			w.(http.Flusher).Flush() // 先送出標頭，強制 chunked
			io.WriteString(w, body)
		},
	} {
		srv := httptest.NewServer(h)
		res, err := newClient(srv).Transcribe(context.Background(), audioFile(t), "")
		srv.Close()
		if res != nil || err == nil || classOf(t, err) != errclass.ClassPermanent || !errors.Is(err, netguard.ErrTooLarge) {
			t.Errorf("%s：res = %v, err = %v", name, res, err)
		}
	}
}

func TestTranscribeContextCancelReturnsCtxErr(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer srv.Close()
	defer close(release)

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	_, err := newClient(srv).Transcribe(ctx, audioFile(t), "")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v，預期 context.Canceled", err)
	}
}

func TestTranscribeDoesNotFollowRedirectsOrLeakKey(t *testing.T) {
	var otherAuth string
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		otherAuth = r.Header.Get("Authorization")
		io.WriteString(w, okBody)
	}))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()

	c := newClient(srv)
	hc := *srv.Client()
	hc.CheckRedirect = nil // 呼叫端的預設行為是跟隨；Client 必須自己停下來
	c.HTTP = &hc
	_, err := c.Transcribe(context.Background(), audioFile(t), "")
	if err == nil || classOf(t, err) != errclass.ClassPermanent {
		t.Errorf("err = %v", err)
	}
	if otherAuth != "" {
		t.Errorf("轉址的目標收到 Authorization：%q", otherAuth)
	}
	if hc.CheckRedirect != nil {
		t.Error("不應改動呼叫端的 http.Client")
	}
}

func TestErrorsNeverContainAPIKey(t *testing.T) {
	// 回應內文故意回顯 key，錯誤訊息也不得帶出。
	echo := `{"error":{"message":"bad key ` + testKey + `"}}`
	for _, code := range []int{400, 401, 403, 413, 429, 500, 302} {
		srv := respond(code, echo)
		_, err := newClient(srv).Transcribe(context.Background(), audioFile(t), "")
		srv.Close()
		if err == nil || strings.Contains(err.Error(), testKey) {
			t.Errorf("HTTP %d：err = %v", code, err)
		}
	}
	// 連線失敗的錯誤也不得含 key。
	srv := respond(200, okBody)
	c := newClient(srv)
	srv.Close()
	_, err := c.Transcribe(context.Background(), audioFile(t), "")
	if err == nil || strings.Contains(err.Error(), testKey) {
		t.Errorf("network err = %v", err)
	}
	// 成功時的結果也不含 key（Result 沒有這個欄位，順便確認 log 友善的 Error 字串）。
	if _, ok := errclass.ClassOf(err); !ok {
		t.Errorf("err = %#v", err)
	}
}

func TestBCP47(t *testing.T) {
	for in, want := range map[string]string{
		"english": "en", "ENGLISH": "en", " Japanese ": "ja", "chinese": "zh", "spanish": "es", "castilian": "es",
		"javanese": "jv", "ja": "ja", "yue": "yue", "pt-BR": "pt-BR", "unknownlanguage": "", "": "", "e": "",
	} {
		if got := BCP47(in); got != want {
			t.Errorf("BCP47(%q) = %q，預期 %q", in, got, want)
		}
	}
}

func TestTimingIsSegment(t *testing.T) {
	if Timing != "segment" || ID != "groq" || DefaultModel != "whisper-large-v3-turbo" {
		t.Error("常數不符")
	}
}

// Groq 要轉錄完才回標頭，長音檔會等很久；Transport 的 ResponseHeaderTimeout 不能截斷它，
// 要放寬到整個請求的逾時（保留 Transport 的其他設定）。
func TestResponseHeaderTimeoutIsRelaxedToTheRequestTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		time.Sleep(300 * time.Millisecond) // 比 Transport 的標頭逾時久
		io.WriteString(w, okBody)
	}))
	defer srv.Close()

	tr := srv.Client().Transport.(*http.Transport).Clone()
	tr.ResponseHeaderTimeout = 50 * time.Millisecond
	c := &Client{HTTP: &http.Client{Transport: tr}, BaseURL: srv.URL, APIKey: testKey, timeout: 5 * time.Second}
	if _, err := c.Transcribe(context.Background(), audioFile(t), ""); err != nil {
		t.Fatalf("Transcribe = %v", err)
	}
	// 呼叫端提供的 Transport 不被改動。
	if tr.ResponseHeaderTimeout != 50*time.Millisecond {
		t.Errorf("呼叫端的 Transport 被改動：%v", tr.ResponseHeaderTimeout)
	}
}

// relaxableTransport 模擬 netguard.NewClient 的 Transport（外層包了 scheme 檢查，自己複製內層並放寬標頭逾時）。
type relaxableTransport struct {
	base    http.RoundTripper
	relaxed *time.Duration
}

func (r relaxableTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return r.base.RoundTrip(req)
}

func (r relaxableTransport) WithResponseHeaderTimeout(d time.Duration) http.RoundTripper {
	*r.relaxed = d
	return r.base
}

// Transport 提供 WithResponseHeaderTimeout 時（netguard.NewClient），標頭逾時放寬到整個請求的逾時。
func TestResponseHeaderTimeoutIsRelaxedForWrappedTransport(t *testing.T) {
	srv := respond(200, okBody)
	defer srv.Close()
	var relaxed time.Duration
	hc := &http.Client{Transport: relaxableTransport{base: srv.Client().Transport, relaxed: &relaxed}}
	c := &Client{HTTP: hc, BaseURL: srv.URL, APIKey: testKey, timeout: 5 * time.Second}
	if _, err := c.Transcribe(context.Background(), audioFile(t), ""); err != nil {
		t.Fatalf("Transcribe = %v", err)
	}
	if relaxed != 5*time.Second {
		t.Errorf("標頭逾時 = %v，應為整個請求的逾時 5s", relaxed)
	}
}

// 不是 *http.Transport 的 Transport 原樣使用。
func TestOtherTransportsAreKept(t *testing.T) {
	srv := respond(200, okBody)
	defer srv.Close()
	used := false
	base := srv.Client().Transport
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) { used = true; return base.RoundTrip(r) })
	c := &Client{HTTP: &http.Client{Transport: rt}, BaseURL: srv.URL, APIKey: testKey}
	if _, err := c.Transcribe(context.Background(), audioFile(t), ""); err != nil || !used {
		t.Fatalf("Transcribe = %v，使用自訂 Transport = %v", err, used)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// API key 含控制字元時不可能成功：needs_action 並說明格式不正確，不是 retryable，也不發出請求。
func TestTranscribeMalformedKeyNeedsAction(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits++ }))
	defer srv.Close()
	c := newClient(srv)
	c.APIKey = "gsk_abc\ndef"
	_, err := c.Transcribe(context.Background(), audioFile(t), "")
	var ce *errclass.Error
	if !errors.As(err, &ce) || ce.Class != errclass.ClassNeedsAction {
		t.Fatalf("err = %#v，應為 needs_action", err)
	}
	if ce.Message != "API key 格式不正確。請重新設定 Groq 的 API key。" || len(ce.Actions) != 1 || ce.Actions[0] != ActionSetKey {
		t.Errorf("Message = %q，Actions = %v", ce.Message, ce.Actions)
	}
	if hits != 0 || strings.Contains(err.Error(), "gsk_abc") {
		t.Errorf("請求數 = %d，錯誤 = %v", hits, err)
	}
}

// 超過 Groq 單檔上限的音檔不上傳，直接 permanent 並說明原因（呼叫端應該先切塊，見 pipeline 的 transcribeASR）。
func TestTranscribeRejectsFilesOverTheSizeLimit(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits++ }))
	defer srv.Close()

	path := audioFile(t)
	if err := os.Truncate(path, MaxFileBytes+1); err != nil { // 稀疏檔，不佔磁碟
		t.Fatal(err)
	}
	_, err := newClient(srv).Transcribe(context.Background(), path, "")
	var ce *errclass.Error
	if !errors.As(err, &ce) || ce.Class != errclass.ClassPermanent {
		t.Fatalf("err = %#v，應為 permanent", err)
	}
	if want := "音檔超過語音轉錄服務的大小上限（25 MiB）。"; ce.Message != want {
		t.Errorf("Message = %q", ce.Message)
	}
	if hits != 0 {
		t.Errorf("不該上傳：%d 次請求", hits)
	}

	// 剛好等於上限仍可上傳。
	if err := os.Truncate(path, MaxFileBytes); err != nil {
		t.Fatal(err)
	}
	srv2 := respond(200, okBody)
	defer srv2.Close()
	if _, err := newClient(srv2).Transcribe(context.Background(), path, ""); err != nil {
		t.Errorf("剛好等於上限應可上傳：%v", err)
	}
}

// 先依狀態碼分類，再決定要不要讀 body：非 200 的回應就算 body 很大也不影響分類（401 仍是 needs_action）。
func TestTranscribeClassifiesByStatusBeforeReadingTheBody(t *testing.T) {
	huge := strings.Repeat("x", int(netguard.LimitFeed)+1024)
	for code, want := range map[int]errclass.Class{
		401: errclass.ClassNeedsAction, 403: errclass.ClassNeedsAction,
		429: errclass.ClassRetryable, 500: errclass.ClassRetryable,
		400: errclass.ClassPermanent, 413: errclass.ClassPermanent,
	} {
		srv := respond(code, huge)
		_, err := newClient(srv).Transcribe(context.Background(), audioFile(t), "")
		srv.Close()
		if got := classOf(t, err); got != want {
			t.Errorf("HTTP %d 搭超大 body：class = %s，預期 %s", code, got, want)
		}
	}
}

func TestKeyFuncResolutionAndEnvPriority(t *testing.T) {
	ctx := context.Background()
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		io.WriteString(w, okBody)
	}))
	defer srv.Close()

	dbKey := ""
	c := &Client{
		HTTP:    srv.Client(),
		BaseURL: srv.URL,
		KeyFunc: func(context.Context) (string, error) { return dbKey, nil },
	}
	if mustConfigured(t, c) {
		t.Fatal("empty KeyFunc should report Configured == false")
	}

	// 動態寫入 DB key 後立即可用。
	dbKey = "gsk-from-db"
	if !mustConfigured(t, c) {
		t.Fatal("non-empty KeyFunc should report Configured == true")
	}
	if _, err := c.Transcribe(ctx, audioFile(t), ""); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer gsk-from-db" {
		t.Errorf("Authorization = %q, want Bearer gsk-from-db", gotAuth)
	}

	// 環境變數（APIKey）優先於 KeyFunc（SEC-R1）。
	c.APIKey = "gsk-from-env"
	c.KeyFunc = func(context.Context) (string, error) {
		t.Fatal("APIKey 非空時不應呼叫 KeyFunc")
		return "", nil
	}
	if !mustConfigured(t, c) {
		t.Fatal("APIKey should report Configured == true")
	}
	if _, err := c.Transcribe(ctx, audioFile(t), ""); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer gsk-from-env" {
		t.Errorf("Authorization = %q, want Bearer gsk-from-env", gotAuth)
	}
}

// mustConfigured 呼叫 Configured，讀取 key 失敗時讓測試失敗。
func mustConfigured(t *testing.T, c interface {
	Configured(context.Context) (bool, error)
}) bool {
	t.Helper()
	ok, err := c.Configured(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

// 從資料庫讀取 key 失敗時，Configured 回傳錯誤，不當成「沒有設定」。
func TestConfiguredReportsKeyLookupFailure(t *testing.T) {
	boom := errors.New("database is locked")
	c := &Client{KeyFunc: func(context.Context) (string, error) { return "", boom }}
	if ok, err := c.Configured(context.Background()); ok || !errors.Is(err, boom) {
		t.Errorf("Configured = %v, %v；預期 false 與讀取錯誤", ok, err)
	}
}

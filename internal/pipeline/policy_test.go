package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cxyfer/notesteme/internal/errclass"
	"github.com/cxyfer/notesteme/internal/netguard"
	"github.com/cxyfer/notesteme/internal/store"
)

// errClient 讓每個請求都失敗，模擬 netguard 在 dial 或轉址時擋下請求。
func errClient(err error) *http.Client {
	return &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		return nil, &urlErr{err: err, url: r.URL.String()}
	})}
}

// urlErr 模擬 *url.Error 的包裝方式（Unwrap 到原始錯誤）。
type urlErr struct {
	err error
	url string
}

func (e *urlErr) Error() string { return "Get " + e.url + ": " + e.err.Error() }
func (e *urlErr) Unwrap() error { return e.err }

func TestResolveReturnsPolicyErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"被擋":   {fmt.Errorf("%w: 10.0.0.5", netguard.ErrBlockedAddress), "這個網址指向內部網路，已封鎖。"},
		"轉址太多": {netguard.ErrTooManyRedirects, "轉址超過 5 次。"},
		"回應太大": {&netguard.TooLargeError{Limit: netguard.LimitFeed}, "回應超過大小上限（10 MiB）。"},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.p.http = errClient(tc.err)
			_, err := r.p.Resolve(context.Background(), "https://feeds.example.com/show.xml")
			var pe *PolicyError
			if !errors.As(err, &pe) || pe.Msg != tc.want || !errors.Is(err, tc.err) {
				t.Fatalf("err = %v，預期 *PolicyError %q", err, tc.want)
			}
			if errors.Is(err, ErrUnrecognizedURL) {
				t.Error("防護擋下的請求不能回報成無法辨識")
			}
		})
	}
}

// GRD-R5：超過上限就失敗，不截斷、不接受部分內容。
func TestResolveRejectsOversizedFeed(t *testing.T) {
	big := rssBody + strings.Repeat(" ", int(netguard.LimitFeed))
	var hits []string
	r := newRig(t)
	r.p.http = feedClient(200, big, &hits)
	_, err := r.p.Resolve(context.Background(), "https://feeds.example.com/huge.xml")
	var pe *PolicyError
	if !errors.As(err, &pe) || !errors.Is(err, netguard.ErrTooLarge) || pe.Msg != "回應超過大小上限（10 MiB）。" {
		t.Fatalf("err = %v", err)
	}

	// 剛好 10 MiB 的 feed 可以接受。
	exact := rssBody + strings.Repeat(" ", int(netguard.LimitFeed)-len(rssBody))
	r.p.http = feedClient(200, exact, &hits)
	if res, err := r.p.Resolve(context.Background(), "https://feeds.example.com/exact.xml"); err != nil || res.Title != "The Show" {
		t.Fatalf("上限內的 feed：%+v, %v", res, err)
	}
}

// Content-Length 已經超過上限時不必下載就失敗。
func TestResolveRejectsOversizedContentLength(t *testing.T) {
	r := newRig(t)
	r.p.http = &http.Client{Transport: rtFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Status: "OK", ContentLength: netguard.LimitFeed + 1,
			Body: io.NopCloser(strings.NewReader(rssBody)), Header: http.Header{}}, nil
	})}
	if _, err := r.p.Resolve(context.Background(), "https://feeds.example.com/huge.xml"); !errors.Is(err, netguard.ErrTooLarge) {
		t.Fatalf("err = %v", err)
	}
}

// classify 在 ClassOf 之前先看防護：被包成 retryable 的連線錯誤也是 permanent，不會一直重試。
func TestClassifyPolicyErrorsArePermanent(t *testing.T) {
	r := newRig(t)
	wrapped := func(err error) error {
		return &errclass.Error{Class: errclass.ClassRetryable, Source: "supadata", Err: err}
	}
	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"被擋（包成 retryable）": {wrapped(fmt.Errorf("%w: 192.168.1.1", netguard.ErrBlockedAddress)), "這個網址指向內部網路，已封鎖。"},
		"被擋（沒有分類）":         {fmt.Errorf("抓取音檔：%w", netguard.ErrBlockedAddress), "這個網址指向內部網路，已封鎖。"},
		"轉址太多":             {wrapped(netguard.ErrTooManyRedirects), "轉址超過 5 次。"},
		"回應太大":             {wrapped(&netguard.TooLargeError{Limit: netguard.LimitAudio}), "回應超過大小上限（2 GiB）。"},
	} {
		t.Run(name, func(t *testing.T) {
			// Attempts 還有餘裕：若被當成 retryable 就會排程重試。
			res := r.p.classify(store.Task{Attempts: 1, MaxAttempts: 5}, tc.err)
			if res.task != taskFailed || res.notBefore != nil {
				t.Fatalf("task = %v，notBefore = %v；不該重試", res.task, res.notBefore)
			}
			if res.state.Status != store.StatusFailed || res.state.ErrorClass != "permanent" || res.state.Message != tc.want {
				t.Errorf("state = %+v，預期 permanent 與訊息 %q", res.state, tc.want)
			}
		})
	}
	// 沒有分類的一般錯誤仍然是 retryable。
	if res := r.p.classify(store.Task{Attempts: 1, MaxAttempts: 5}, errors.New("boom")); res.task != taskRequeue {
		t.Errorf("一般錯誤應重試：%v", res.task)
	}
}

// Supadata 的連線被擋下時，步驟直接失敗，不排程重試（端到端）。
func TestSupadataBlockedAddressFailsWithoutRetry(t *testing.T) {
	r, sd := newSupadataRig(t)
	sd.err = &errclass.Error{Class: errclass.ClassRetryable, Source: "supadata",
		Err: fmt.Errorf("%w: 10.0.0.5", netguard.ErrBlockedAddress)}
	it := r.ingest(watch, nil, StageTranscript)
	r.run()

	st := r.stage(it, StageTranscript)
	if st.Status != store.StatusFailed || st.ErrorClass != "permanent" || st.Message != "這個網址指向內部網路，已封鎖。" || st.NotBefore != nil {
		t.Fatalf("transcript = %+v", st)
	}
	if sd.calls() != 1 {
		t.Errorf("被擋下後不該再重試：呼叫 %d 次", sd.calls())
	}
}

package cli

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func roundTrip(t *testing.T, h http.HandlerFunc) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, localBase+"/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	return handlerTransport{h: h}.RoundTrip(req)
}

// 小回應：狀態、標頭與本體照原樣交給 client。
func TestHandlerTransportBuffered(t *testing.T) {
	resp, err := roundTrip(t, func(w http.ResponseWriter, r *http.Request) {
		if r.RemoteAddr != "127.0.0.1:0" || r.RequestURI != "/x" {
			t.Errorf("請求 = %q %q", r.RemoteAddr, r.RequestURI)
		}
		w.Header().Set("X-Test", "1")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, "hello")
		w.Header().Set("X-Late", "ignored") // 標頭送出之後的修改不影響 client
	})
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || string(body) != "hello" || resp.StatusCode != http.StatusCreated || resp.Header.Get("X-Test") != "1" || resp.Header.Get("X-Late") != "" {
		t.Errorf("回應 = %d %v %q, %v", resp.StatusCode, resp.Header, body, err)
	}
}

// 沒有寫任何內容：200 與空的本體。
func TestHandlerTransportEmpty(t *testing.T) {
	resp, err := roundTrip(t, func(http.ResponseWriter, *http.Request) {})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || len(body) != 0 {
		t.Errorf("回應 = %d %q", resp.StatusCode, body)
	}
}

// 串流：handler 還在寫的時候，client 已經拿到回應並讀到第一段；整份內容不必先放進記憶體。
func TestHandlerTransportStreams(t *testing.T) {
	release := make(chan struct{})
	finished := make(chan struct{})
	resp, err := roundTrip(t, func(w http.ResponseWriter, r *http.Request) {
		defer close(finished)
		_, _ = io.WriteString(w, "first")
		<-release
		_, _ = io.WriteString(w, "second")
	})
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 5)
	if _, err := io.ReadFull(resp.Body, buf); err != nil || string(buf) != "first" {
		t.Fatalf("第一段 = %q, %v", buf, err)
	}
	select {
	case <-finished:
		t.Fatal("handler 不該在 client 讀完之前就結束")
	default:
	}
	close(release)
	rest, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || string(rest) != "second" {
		t.Errorf("剩下的 = %q, %v", rest, err)
	}
}

// client 提早關閉本體：handler 的 context 被取消，Close 等 handler 結束才回傳（之後會釋放資料目錄）。
func TestHandlerTransportCloseCancelsHandler(t *testing.T) {
	exited := false
	resp, err := roundTrip(t, func(w http.ResponseWriter, r *http.Request) {
		defer func() { exited = true }()
		for {
			if _, err := io.WriteString(w, strings.Repeat("x", 1024)); err != nil {
				return
			}
			select {
			case <-r.Context().Done():
				return
			default:
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(resp.Body, make([]byte, 10)); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if !exited {
		t.Error("Close 回傳時 handler 還沒結束")
	}
}

// handler 中斷（http.ErrAbortHandler）：client 讀到錯誤，不會把已寫的部分當成完整內容。
func TestHandlerTransportAbort(t *testing.T) {
	resp, err := roundTrip(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "partial")
		panic(http.ErrAbortHandler)
	})
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !errors.Is(err, io.ErrUnexpectedEOF) || string(body) != "partial" {
		t.Errorf("讀取 = %q, %v；預期 partial 與 ErrUnexpectedEOF", body, err)
	}
}

// 其他 panic：標頭送出前發生時 client 拿到回應並在讀本體時看到錯誤；不讓程序崩潰。
func TestHandlerTransportPanic(t *testing.T) {
	resp, err := roundTrip(t, func(http.ResponseWriter, *http.Request) { panic("boom") })
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("讀取錯誤 = %v", err)
	}
}

// 標頭送出之前 context 被取消：回傳錯誤，等 handler 結束。
func TestHandlerTransportCancelBeforeResponse(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	started := make(chan struct{})
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, localBase+"/x", nil)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	})
	go func() {
		<-started
		cancel()
	}()
	done := make(chan error, 1)
	go func() { _, err := handlerTransport{h: h}.RoundTrip(req); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("錯誤 = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RoundTrip 沒有在 context 取消後回傳")
	}
}
